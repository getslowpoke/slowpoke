// Package debt applies established technical debt practices to measured data:
// a register with interest (cost per day) and principal (effort), a standing
// repayment budget, delivery health (DORA-style) and hotspots by file.
package debt

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/doctor"
	"github.com/getslowpoke/slowpoke/internal/server/metrics"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// EffortHours is the typical work of each size, used to rank by return on investment.
var EffortHours = map[string]float64{"xs": 1, "s": 4, "m": 16, "l": 40}

var Efforts = []string{"xs", "s", "m", "l"}

// EstimateEffort guesses the principal from what the doctor found: an index or a
// cast is quick, an N+1 needs code changes, search or pagination rewrites take longer.
func EstimateEffort(kind, missionType string, diagnoses []doctor.Diagnosis) string {
	size := map[string]int{"xs": 0, "s": 1, "m": 2, "l": 3}
	names := []string{"xs", "s", "m", "l"}
	per := map[string]string{
		"full_scan": "xs", "filesort": "xs", "temporary": "xs", "implicit_conversion": "xs",
		"n_plus_one": "s", "select_star_wide": "s", "count_big_table": "s", "or_conditions": "s", "not_in_subquery": "s",
		"function_on_column": "m", "leading_wildcard": "m", "deep_offset": "m",
	}
	best := -1
	for _, d := range diagnoses {
		if e, ok := per[d.Code]; ok && size[e] > best {
			best = size[e]
		}
	}
	if best < 0 {
		switch kind {
		case "php":
			best = size["s"] // timeouts, cache or queue around an external call
		default:
			best = size["m"]
		}
	}
	// A regression was just introduced: the change that caused it is known and recent.
	if missionType == "boss" && best > size["s"] {
		best = size["s"]
	}
	return names[best]
}

// ROI is seconds of waiting removed per day for each hour of work.
func ROI(secondsPerDay float64, effort string) float64 {
	h := EffortHours[effort]
	if h == 0 {
		h = EffortHours["m"]
	}
	return math.Round(secondsPerDay/h*10) / 10
}

// ---------------------------------------------------------------- weekly goal

type Goal struct {
	GoalSecondsPerDay   float64 `json:"goal_seconds_per_day"`
	RepaidSecondsPerDay float64 `json:"repaid_seconds_per_day"`
	Progress            float64 `json:"progress"`
	Reached             bool    `json:"reached"`
	CapacityPercent     int     `json:"capacity_percent"`
	WeekStart           int64   `json:"week_start"`
}

// WeekGoal measures the waiting time repaid by missions closed in the last 7 days against the budget.
func WeekGoal(ctx context.Context, db *store.DB, p project.Project, now time.Time) (Goal, error) {
	g := Goal{GoalSecondsPerDay: p.Settings.Debt.WeeklyGoalSeconds, CapacityPercent: p.Settings.Debt.CapacityPercent}
	from := now.Add(-7 * 24 * time.Hour)
	g.WeekStart = from.Unix()
	var repaid *float64
	err := db.Q().Row(ctx, `SELECT SUM(baseline - COALESCE(current, 0)) FROM missions
		WHERE project_id = ? AND status IN ('completed', 'defeated') AND closed_at >= ? AND closed_at <= ?`, p.ID, from.Unix(), now.Unix()).Scan(&repaid)
	if err != nil {
		return g, err
	}
	if repaid != nil && *repaid > 0 {
		g.RepaidSecondsPerDay = math.Round(*repaid * 24 / p.Settings.Missions.WindowHours)
	}
	if g.GoalSecondsPerDay > 0 {
		g.Progress = math.Round(math.Min(1, g.RepaidSecondsPerDay/g.GoalSecondsPerDay)*1000) / 1000
		g.Reached = g.RepaidSecondsPerDay >= g.GoalSecondsPerDay
	}
	return g, nil
}

// ---------------------------------------------------------------- delivery health

type Health struct {
	Days               int     `json:"days"`
	Deploys            int     `json:"deploys"`
	PerWeek            float64 `json:"per_week"`
	ChangeFailureRate  float64 `json:"change_failure_rate"`   // % of measured deploys that made things worse or caused a regression
	MeanHoursToRestore float64 `json:"mean_hours_to_restore"` // regressions: opened -> defeated
	OpenRegressions    int     `json:"open_regressions"`
	Rating             string  `json:"rating"` // elite | high | medium | low
}

func Delivery(ctx context.Context, db *store.DB, p project.Project, now time.Time) (Health, error) {
	const days = 28
	h := Health{Days: days}
	from := now.Add(-days * 24 * time.Hour).Unix()

	rows, err := db.Q().Query(ctx, `SELECT d.id, d.impact, (SELECT COUNT(*) FROM missions m WHERE m.deploy_id = d.id AND m.type = 'boss')
		FROM deploys d WHERE d.project_id = ? AND d.deployed_at >= ? AND d.deployed_at <= ?`, p.ID, from, now.Unix())
	if err != nil {
		return h, err
	}
	measured, failed := 0, 0
	for rows.Next() {
		var id int64
		var impact *string
		var bosses int
		if err := rows.Scan(&id, &impact, &bosses); err != nil {
			rows.Close()
			return h, err
		}
		h.Deploys++
		verdict := ""
		if impact != nil {
			var v struct {
				Verdict string `json:"verdict"`
			}
			_ = json.Unmarshal([]byte(*impact), &v)
			verdict = v.Verdict
		}
		if verdict != "" && verdict != "no_data" {
			measured++
			if verdict == "worse" || bosses > 0 {
				failed++
			}
		} else if bosses > 0 {
			measured++
			failed++
		}
	}
	rows.Close()
	h.PerWeek = math.Round(float64(h.Deploys)/(days/7.0)*100) / 100
	if measured > 0 {
		h.ChangeFailureRate = math.Round(float64(failed)/float64(measured)*1000) / 10
	}

	var mean *float64
	if err := db.Q().Row(ctx, `SELECT AVG(closed_at - opened_at) FROM missions WHERE project_id = ? AND type = 'boss' AND status = 'defeated' AND closed_at >= ?`,
		p.ID, now.Add(-90*24*time.Hour).Unix()).Scan(&mean); err != nil {
		return h, err
	}
	if mean != nil {
		h.MeanHoursToRestore = math.Round(*mean/3600*10) / 10
	}
	if err := db.Q().Row(ctx, `SELECT COUNT(*) FROM missions WHERE project_id = ? AND type = 'boss' AND status = 'open'`, p.ID).Scan(&h.OpenRegressions); err != nil {
		return h, err
	}

	// Loosely following the DORA bands, adapted to what production data can tell.
	switch {
	case h.PerWeek >= 5 && h.ChangeFailureRate <= 15 && (h.MeanHoursToRestore == 0 || h.MeanHoursToRestore < 24):
		h.Rating = "elite"
	case h.PerWeek >= 1 && h.ChangeFailureRate <= 30:
		h.Rating = "high"
	case h.PerWeek >= 0.25 && h.ChangeFailureRate <= 45:
		h.Rating = "medium"
	default:
		h.Rating = "low"
	}
	return h, nil
}

// ---------------------------------------------------------------- hotspots

type Hotspot struct {
	File          string   `json:"file"`
	Kind          string   `json:"kind"` // query | php | mixed
	SecondsPerDay float64  `json:"seconds_per_day"`
	Items         int      `json:"items"`
	Lines         []string `json:"lines"`
}

func splitOrigin(origin string) (string, string) {
	if i := strings.LastIndex(origin, ":"); i > 0 {
		if _, err := time.ParseDuration(origin[i+1:] + "s"); err == nil {
			return origin[:i], origin[i+1:]
		}
	}
	return origin, ""
}

// Hotspots groups the measured cost of the last 7 days by source file: where repaying debt pays the most.
func Hotspots(ctx context.Context, db *store.DB, p project.Project, now time.Time) ([]Hotspot, error) {
	from := now.Add(-7 * 24 * time.Hour)
	costs, err := metrics.Reader{DB: db}.Costs(ctx, p.ID, from, now)
	if err != nil {
		return nil, err
	}
	origins := map[string]string{}
	rows, err := db.Q().Query(ctx, "SELECT hash, origin FROM query_fingerprints WHERE project_id = ? AND origin <> ''", p.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var hash, origin string
		if err := rows.Scan(&hash, &origin); err == nil {
			origins[hash] = origin
		}
	}
	rows.Close()

	byFile := map[string]*Hotspot{}
	add := func(origin, kind string, seconds float64) {
		file, line := splitOrigin(origin)
		if file == "" {
			return
		}
		h := byFile[file]
		if h == nil {
			h = &Hotspot{File: file, Kind: kind}
			byFile[file] = h
		} else if h.Kind != kind {
			h.Kind = "mixed"
		}
		h.SecondsPerDay += seconds / 7
		h.Items++
		if line != "" && len(h.Lines) < 10 {
			h.Lines = append(h.Lines, line)
		}
	}
	for _, c := range costs {
		if c.Kind == "query" && origins[c.Target] != "" {
			add(origins[c.Target], "query", c.Seconds)
		}
	}

	rows, err = db.Q().Query(ctx, `SELECT code, SUM(n) FROM php_stall_hours WHERE project_id = ? AND hour >= ? AND code <> '' GROUP BY code`, p.ID, from.Unix()-from.Unix()%3600)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var code string
		var n int64
		if err := rows.Scan(&code, &n); err == nil {
			add(code, "php", float64(n)*metrics.PHPStallSeconds)
		}
	}
	rows.Close()

	out := make([]Hotspot, 0, len(byFile))
	for _, h := range byFile {
		h.SecondsPerDay = math.Round(h.SecondsPerDay*1000) / 1000
		sort.Strings(h.Lines)
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SecondsPerDay > out[j].SecondsPerDay })
	if len(out) > 30 {
		out = out[:30]
	}
	return out, nil
}
