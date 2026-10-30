package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/debt"
	"github.com/getslowpoke/slowpoke/internal/server/doctor"
	"github.com/getslowpoke/slowpoke/internal/server/exclude"
	"github.com/getslowpoke/slowpoke/internal/server/game"
	"github.com/getslowpoke/slowpoke/internal/server/integrations"
	"github.com/getslowpoke/slowpoke/internal/server/metrics"
	"github.com/getslowpoke/slowpoke/internal/server/missionctx"
	"github.com/getslowpoke/slowpoke/internal/server/mute"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/score"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func r2(v float64) float64 { return math.Round(v*100) / 100 }

// ---------------------------------------------------------------- overview

type dayPoint struct {
	Day     string   `json:"day"`
	Score   *float64 `json:"score"`
	Partial bool     `json:"partial"`
	Closed  bool     `json:"closed"`
	Level   string   `json:"level"`
	Color   string   `json:"color"`
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, p project.Project) {
	ctx := r.Context()
	now := time.Now()
	out := map[string]any{"project": map[string]any{"name": p.Name, "slug": p.Slug, "timezone": p.Timezone}}

	// Live: the last 15 minutes.
	liveFrom := now.Add(-15 * time.Minute)
	snap, err := s.Metrics.Snapshot(ctx, p.ID, liveFrom, now)
	if err != nil {
		serverError(w, err)
		return
	}
	all := snap.HTTPFor("")
	series := []map[string]any{}
	rows, err := s.DB.Q().Query(ctx, `SELECT bucket, SUM(n), SUM(sum), SUM(e5), SUM(n2) FROM http_minutes WHERE project_id = ? AND bucket >= ? GROUP BY bucket ORDER BY bucket`, p.ID, now.Add(-60*time.Minute).Unix())
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var bucket, n, e5, n2 int64
		var sum float64
		if err := rows.Scan(&bucket, &n, &sum, &e5, &n2); err == nil {
			avg := 0.0
			if n > 0 {
				avg = sum / float64(n)
			}
			series = append(series, map[string]any{"t": bucket, "requests": n, "avg": r2(avg), "e5": e5, "slow": n2})
		}
	}
	rows.Close()
	minutes := math.Max(1, snap.Hours*60)
	out["live"] = map[string]any{
		"requests_per_minute": r2(float64(all.N) / minutes),
		"p95":                 r2(all.Percentile(0.95)),
		"errors":              all.E5,
		"slow":                all.N2,
		"db_slow_seconds":     r2(snap.DB.SlowSum),
		"php_stalls":          snap.PHPStalls,
		"series":              series,
	}

	hosts, err := s.hostList(ctx, p)
	if err != nil {
		serverError(w, err)
		return
	}
	out["hosts"] = hosts

	// Today and the chart.
	days, err := s.scoreDays(ctx, p, 30)
	if err != nil {
		serverError(w, err)
		return
	}
	out["chart"] = days
	// The index is the last 24 hours: the calendar day alone reads 100 at night, when nobody is
	// using the app. What the day has scored so far stays as a footnote.
	type scoreRow struct {
		score      *float64
		partial    bool
		hours      float64
		components string
	}
	var win, day scoreRow
	todayKey := now.In(p.Location()).Format("2006-01-02")
	err = s.DB.Q().Row(ctx, "SELECT score, partial, hours, components FROM score_days WHERE project_id = ? AND day = ?", p.ID, todayKey).Scan(&day.score, &day.partial, &day.hours, &day.components)
	hasDay := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		serverError(w, err)
		return
	}
	err = s.DB.Q().Row(ctx, "SELECT score, partial, hours, components FROM score_window WHERE project_id = ?", p.ID).Scan(&win.score, &win.partial, &win.hours, &win.components)
	hasWindow := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		serverError(w, err)
		return
	}
	today := map[string]any{"score": nil}
	shown, window := day, "day"
	if hasWindow {
		shown, window = win, "24h"
	}
	if hasWindow || hasDay {
		var comps []score.ComponentResult
		_ = json.Unmarshal([]byte(shown.components), &comps)
		sort.SliceStable(comps, func(i, j int) bool { return comps[i].Lost > comps[j].Lost })
		today = map[string]any{"score": shown.score, "partial": shown.partial, "hours": shown.hours, "components": comps, "window": window}
		if hasDay && hasWindow {
			today["so_far"] = map[string]any{"score": day.score, "hours": day.hours, "partial": day.partial}
		}
		if shown.score != nil {
			today["level"] = p.Settings.LevelFor(*shown.score)
			if next := p.Settings.NextLevel(*shown.score); next != nil {
				today["next_level"] = map[string]any{"name": next.Name, "icon": next.Icon, "min": next.Min, "missing": r2(next.Min - *shown.score)}
			}
			for i := len(days) - 1; i >= 0; i-- {
				if days[i].Day < todayKey && days[i].Closed && days[i].Score != nil {
					today["delta"] = r2(*shown.score - *days[i].Score)
					break
				}
			}
		}
	}
	out["today"] = today
	out["streak"] = streak(days)
	weekChanges, weekComparable, err := s.Metrics.Changes(ctx, p.ID, now.Add(-7*24*time.Hour), now)
	if err != nil {
		serverError(w, err)
		return
	}
	out["wins"] = wins(weekChanges, weekComparable)
	out["levels"] = p.Settings.Score.Levels

	badges, err := s.badges(ctx, p)
	if err != nil {
		serverError(w, err)
		return
	}
	out["badges"] = badges

	points, err := s.points(ctx, p)
	if err != nil {
		serverError(w, err)
		return
	}
	out["points"] = points

	missions, err := s.missionList(ctx, p, "status = 'open'", 20)
	if err != nil {
		serverError(w, err)
		return
	}
	out["missions"] = missions

	debtSummary, err := s.Engine.Debt(ctx, p)
	if err != nil {
		serverError(w, err)
		return
	}
	out["debt"] = debtSummary
	if goal, err := debt.WeekGoal(ctx, s.DB, p, now); err == nil {
		out["goal"] = goal
	}

	activity, err := s.activities(ctx, p, 25)
	if err != nil {
		serverError(w, err)
		return
	}
	out["activity"] = activity

	deploys, err := s.deployList(ctx, p, now.Add(-30*24*time.Hour), 100)
	if err != nil {
		serverError(w, err)
		return
	}
	out["deploys"] = deploys

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) scoreDays(ctx context.Context, p project.Project, n int) ([]dayPoint, error) {
	since := time.Now().In(p.Location()).AddDate(0, 0, -n).Format("2006-01-02")
	rows, err := s.DB.Q().Query(ctx, "SELECT day, score, partial, closed FROM score_days WHERE project_id = ? AND day > ? ORDER BY day", p.ID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dayPoint{}
	for rows.Next() {
		var d dayPoint
		if err := rows.Scan(&d.Day, &d.Score, &d.Partial, &d.Closed); err != nil {
			return nil, err
		}
		if d.Score != nil {
			l := p.Settings.LevelFor(*d.Score)
			d.Level, d.Color = l.Name, l.Color
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// streak counts closed days in a row without getting worse (2 points of tolerance).
func streak(days []dayPoint) map[string]any {
	var closed []dayPoint
	for _, d := range days {
		if d.Closed && d.Score != nil {
			closed = append(closed, d)
		}
	}
	count, since := 0, ""
	for i := len(closed) - 1; i > 0; i-- {
		if *closed[i].Score >= *closed[i-1].Score-2 {
			count++
			since = closed[i].Day
		} else {
			break
		}
	}
	return map[string]any{"days": count, "since": since}
}

func (s *Server) badges(ctx context.Context, p project.Project) ([]map[string]any, error) {
	rows, err := s.DB.Q().Query(ctx, "SELECT badge, day FROM unlocked_badges WHERE project_id = ?", p.ID)
	if err != nil {
		return nil, err
	}
	unlocked := map[string]string{}
	for rows.Next() {
		var b, d string
		if err := rows.Scan(&b, &d); err == nil {
			unlocked[b] = d
		}
	}
	rows.Close()
	out := []map[string]any{}
	for _, def := range game.BadgeDefs(p.Settings) {
		day, ok := unlocked[def.Key]
		out = append(out, map[string]any{"key": def.Key, "icon": def.Icon, "title": def.Title, "description": def.Description, "unlocked": ok, "day": day})
	}
	return out, nil
}

func (s *Server) points(ctx context.Context, p project.Project) (map[string]any, error) {
	loc := p.Location()
	now := time.Now().In(loc)
	seasonStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	var total, season, week int64
	q := s.DB.Q()
	if err := q.Row(ctx, "SELECT COALESCE(SUM(points),0) FROM point_entries WHERE project_id = ?", p.ID).Scan(&total); err != nil {
		return nil, err
	}
	if err := q.Row(ctx, "SELECT COALESCE(SUM(points),0) FROM point_entries WHERE project_id = ? AND created_at >= ?", p.ID, seasonStart.Unix()).Scan(&season); err != nil {
		return nil, err
	}
	if err := q.Row(ctx, "SELECT COALESCE(SUM(points),0) FROM point_entries WHERE project_id = ? AND created_at >= ?", p.ID, now.Add(-7*24*time.Hour).Unix()).Scan(&week); err != nil {
		return nil, err
	}
	return map[string]any{"total": total, "season": season, "season_name": seasonStart.Format("January 2006"), "season_start": seasonStart.Format("2006-01"), "last_7_days": week}, nil
}

type missionItem struct {
	ID          int64    `json:"id"`
	Type        string   `json:"type"`
	Kind        string   `json:"kind"`
	Target      string   `json:"target"`
	Label       string   `json:"label"`
	Baseline    float64  `json:"baseline"`
	Current     *float64 `json:"current"`
	Points      int      `json:"points"`
	Status      string   `json:"status"`
	GoodWindows int      `json:"good_windows"`
	NeedWindows int      `json:"need_windows"`
	WindowHours float64  `json:"window_hours"`
	Person      string   `json:"person"`
	OpenedAt    int64    `json:"opened_at"`
	DueAt       *int64   `json:"due_at"`
	ClosedAt    *int64   `json:"closed_at"`
	Progress    float64  `json:"progress"`
	SavedPerDay float64  `json:"saved_seconds_per_day"`
	Deploy      *string  `json:"deploy"`
	CostPerDay  float64  `json:"cost_seconds_per_day"`
	Effort      string   `json:"effort"`
	EffortAuto  bool     `json:"effort_auto"`
	ROI         float64  `json:"roi"`
	// Where it stands: who is on it, whether the fix is online, whether it held.
	game.Life
}

func (s *Server) missionList(ctx context.Context, p project.Project, where string, limit int, args ...any) ([]missionItem, error) {
	return s.missionListWith(ctx, p, missionctx.NewQueries(s.DB, p, time.Now()), where, limit, args...)
}

// missionListWith shares one read of the project's costs between the lists of a page.
func (s *Server) missionListWith(ctx context.Context, p project.Project, queries *missionctx.Queries, where string, limit int, args ...any) ([]missionItem, error) {
	rows, err := s.DB.Q().Query(ctx, `SELECT m.id, m.type, m.kind, m.target, m.label, m.baseline, m.current, m.points, m.status, m.good_windows, m.person, m.opened_at, m.due_at, m.closed_at, m.effort,
		CASE WHEN d.id IS NULL THEN NULL ELSE COALESCE(NULLIF(d.version, ''), d.commit_sha) END
		FROM missions m LEFT JOIN deploys d ON d.id = m.deploy_id
		WHERE m.project_id = ? AND `+where+` ORDER BY CASE WHEN m.type = 'boss' THEN 0 ELSE 1 END, COALESCE(m.closed_at, 0) DESC, m.points DESC LIMIT ?`, append(append([]any{p.ID}, args...), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []missionItem{}
	perDay := 24 / p.Settings.Missions.WindowHours
	for rows.Next() {
		var m missionItem
		if err := rows.Scan(&m.ID, &m.Type, &m.Kind, &m.Target, &m.Label, &m.Baseline, &m.Current, &m.Points, &m.Status, &m.GoodWindows, &m.Person, &m.OpenedAt, &m.DueAt, &m.ClosedAt, &m.Effort, &m.Deploy); err != nil {
			return nil, err
		}
		cost := m.Baseline
		if m.Current != nil {
			cost = *m.Current
		}
		m.CostPerDay = math.Round(cost * perDay)
		m.NeedWindows = p.Settings.Missions.GoodWindows
		m.WindowHours = p.Settings.Missions.WindowHours
		if m.Type == "mission" {
			m.Progress = r2(float64(m.GoodWindows) / float64(max(1, m.NeedWindows)))
			if m.Status == "completed" && m.Current != nil {
				m.SavedPerDay = math.Round((m.Baseline - *m.Current) * perDay)
			}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	ids := make([]int64, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	lives, err := game.Lives(ctx, s.DB, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Life = lives[out[i].ID]
		if out[i].Effort == "" {
			out[i].Effort = estimateEffort(ctx, queries, out[i].Type, out[i].Kind, out[i].Target)
			out[i].EffortAuto = true
		}
		out[i].ROI = debt.ROI(out[i].CostPerDay, out[i].Effort)
	}
	return out, nil
}

func estimateEffort(ctx context.Context, queries *missionctx.Queries, missionType, kind, target string) string {
	var diagnoses []doctor.Diagnosis
	if kind == "query" {
		if q, err := queries.Load(ctx, target); err == nil {
			diagnoses = q.Diagnoses
		}
	}
	return debt.EstimateEffort(kind, missionType, diagnoses)
}

func (s *Server) activities(ctx context.Context, p project.Project, limit int) ([]map[string]any, error) {
	rows, err := s.DB.Q().Query(ctx, "SELECT id, type, icon, title, data, created_at FROM activities WHERE project_id = ? ORDER BY created_at DESC, id DESC LIMIT ?", p.ID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, at int64
		var kind, icon, title, data string
		if err := rows.Scan(&id, &kind, &icon, &title, &data, &at); err != nil {
			return nil, err
		}
		var decoded map[string]any
		_ = json.Unmarshal([]byte(data), &decoded)
		out = append(out, map[string]any{"id": id, "type": kind, "icon": icon, "title": title, "data": decoded, "created_at": at})
	}
	return out, rows.Err()
}

func (s *Server) deployList(ctx context.Context, p project.Project, since time.Time, limit int) ([]map[string]any, error) {
	rows, err := s.DB.Q().Query(ctx, "SELECT id, version, commit_sha, author, message, url, deployed_at, impact FROM deploys WHERE project_id = ? AND deployed_at >= ? ORDER BY deployed_at DESC LIMIT ?", p.ID, since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, at int64
		var version, commit, author, message, url string
		var impact sql.NullString
		if err := rows.Scan(&id, &version, &commit, &author, &message, &url, &at, &impact); err != nil {
			return nil, err
		}
		item := map[string]any{"id": id, "version": version, "commit": commit, "author": author, "message": message, "url": url, "deployed_at": at,
			"day": time.Unix(at, 0).In(p.Location()).Format("2006-01-02")}
		if impact.Valid {
			var decoded game.Impact
			_ = json.Unmarshal([]byte(impact.String), &decoded)
			item["impact"] = decoded
		}
		item["fixed"], item["caused"] = []map[string]any{}, []map[string]any{}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	// What each deploy did to the missions: the ones that started going down right after it (most
	// likely its fix), and the regressions it caused.
	byID := map[int64]map[string]any{}
	for _, item := range out {
		byID[item["id"].(int64)] = item
	}
	mrows, err := s.DB.Q().Query(ctx, `SELECT id, type, label, status, shipped_deploy_id, deploy_id FROM missions
		WHERE project_id = ? AND (shipped_deploy_id IS NOT NULL OR (type = 'boss' AND deploy_id IS NOT NULL)) AND opened_at >= ?`, p.ID, since.Add(-30*24*time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var id int64
		var typ, label, status string
		var shipped, caused sql.NullInt64
		if err := mrows.Scan(&id, &typ, &label, &status, &shipped, &caused); err != nil {
			return nil, err
		}
		entry := map[string]any{"id": id, "type": typ, "label": label, "status": status}
		if d := byID[shipped.Int64]; shipped.Valid && d != nil {
			d["fixed"] = append(d["fixed"].([]map[string]any), entry)
		}
		if d := byID[caused.Int64]; typ == "boss" && caused.Valid && d != nil {
			d["caused"] = append(d["caused"].([]map[string]any), entry)
		}
	}
	return out, mrows.Err()
}

func (s *Server) handleDeploys(w http.ResponseWriter, r *http.Request, p project.Project) {
	list, err := s.deployList(r.Context(), p, time.Now().Add(-90*24*time.Hour), 200)
	if err != nil {
		serverError(w, err)
		return
	}
	health, err := debt.Delivery(r.Context(), s.DB, p, time.Now())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deploys": list, "compare_minutes": p.Settings.Deploys.CompareMinutes, "health": health})
}

// ---------------------------------------------------------------- slow things

func parseWindow(v string) time.Duration {
	switch v {
	case "1h":
		return time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// databaseNames lists the databases the queries of this project come from, for the filter.
func databaseNames(ctx context.Context, db *store.DB, projectID int64) []string {
	rows, err := db.Q().Query(ctx, "SELECT DISTINCT db_name FROM query_fingerprints WHERE project_id = ? AND db_name <> '' ORDER BY db_name", projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			out = append(out, name)
		}
	}
	return out
}

// wins turns the comparison into what a team wants to read: what got better, what got worse,
// and the seconds of waiting saved every day, all counted by Slowpoke and not by anyone's memory.
func wins(changes []metrics.Change, comparable bool) map[string]any {
	improved, worse := []map[string]any{}, []map[string]any{}
	var saved float64
	for _, ch := range changes {
		saved += ch.SavedPerDay
		item := map[string]any{"kind": ch.Kind, "target": ch.Target, "label": ch.Label,
			"now_seconds_per_day": ch.NowPerDay, "before_seconds_per_day": ch.BeforePerDay, "saved_seconds_per_day": ch.SavedPerDay}
		if ch.New {
			item["new"] = true
		}
		if ch.Gone {
			item["gone"] = true
		}
		switch {
		// Half a second a day is noise, not a win.
		case ch.SavedPerDay >= 0.5 && len(improved) < 5:
			improved = append(improved, item)
		case ch.SavedPerDay <= -0.5 && len(worse) < 5:
			worse = append(worse, item)
		}
	}
	// The worst first, the way they will be read.
	for i, j := 0, len(worse)-1; i < j; i, j = i+1, j-1 {
		worse[i], worse[j] = worse[j], worse[i]
	}
	return map[string]any{"comparable": comparable, "improved": improved, "worse": worse, "saved_seconds_per_day": math.Round(saved*10) / 10}
}

func (s *Server) handleSlow(w http.ResponseWriter, r *http.Request, p project.Project) {
	ctx := r.Context()
	window := parseWindow(r.URL.Query().Get("window"))
	now := time.Now()
	from := now.Add(-window)
	fromHour := from.Unix() - from.Unix()%3600
	days := s.Metrics.ObservedDays(ctx, p.ID, window, now)

	// One machine at a time. What a machine measures is what its sources are called, so the
	// filter is on them: a query has no machine of its own yet, and the answer says so rather
	// than showing the project's queries as if they were this machine's.
	machine := strings.TrimSpace(r.URL.Query().Get("host"))
	var sources []string
	if machine != "" {
		var raw string
		_ = s.DB.Q().Row(ctx, "SELECT sources FROM hosts WHERE project_id = ? AND name = ?", p.ID, machine).Scan(&raw)
		_ = json.Unmarshal([]byte(raw), &sources)
		if len(sources) == 0 {
			sources = []string{""} // a machine we know nothing about measures nothing
		}
	}
	// keeps answers the rows of the machine asked for; with no filter it keeps everything.
	keeps := func(source string) bool {
		if machine == "" {
			return true
		}
		for _, s := range sources {
			if s == source {
				return true
			}
		}
		return false
	}

	// What each thing cost in the window before this one: the wins are counted, not claimed.
	changes, comparable, err := s.Metrics.Changes(ctx, p.ID, from, now)
	if err != nil {
		serverError(w, err)
		return
	}
	before := map[string]metrics.Change{}
	for _, ch := range changes {
		before[ch.Kind+"|"+ch.Target] = ch
	}
	compare := func(item map[string]any, kind, target string) {
		if !comparable {
			return
		}
		ch, ok := before[kind+"|"+target]
		if !ok {
			return
		}
		item["before_seconds_per_day"] = ch.BeforePerDay
		item["saved_seconds_per_day"] = ch.SavedPerDay
		if ch.New {
			item["new"] = true
		}
	}

	endpoints := []map[string]any{}
	excluded, err := exclude.Active(ctx, s.DB, p.ID, now)
	if err != nil {
		serverError(w, err)
		return
	}
	outbound, err := s.Metrics.OutboundByEndpoint(ctx, p.ID, from, now)
	if err != nil {
		serverError(w, err)
		return
	}
	shadowed, err := s.Metrics.Shadowed(ctx, p.ID, time.Unix(fromHour, 0))
	if err != nil {
		serverError(w, err)
		return
	}
	rows, err := s.DB.Q().Query(ctx, `SELECT source, endpoint, SUM(n), SUM(sum), MAX(max), SUM(slow), SUM(slow_sum), SUM(e5) FROM endpoint_hours
		WHERE project_id = ? AND hour >= ? GROUP BY source, endpoint ORDER BY SUM(slow_sum) DESC, SUM(sum) DESC LIMIT 60`, p.ID, fromHour)
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var source, endpoint string
		var n, slow, e5 int64
		var sum, mx, slowSum float64
		if err := rows.Scan(&source, &endpoint, &n, &sum, &mx, &slow, &slowSum, &e5); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		if shadowed[source] {
			continue // the same requests, already counted from the web server in front
		}
		avg := 0.0
		if n > 0 {
			avg = sum / float64(n)
		}
		if !keeps(source) {
			continue
		}
		item := map[string]any{"source": source, "endpoint": endpoint, "requests": n, "avg": r2(avg), "max": r2(mx), "slow": slow,
			"slow_seconds": r2(slowSum), "slow_ratio": r2(float64(slow) / math.Max(1, float64(n)) * 100), "e5": e5,
			"slow_seconds_per_day": r2(slowSum / days)}
		compare(item, "endpoint", source+" "+endpoint)
		// The share of its time spent waiting on other services, when a framework package says.
		if o, ok := outbound[exclude.Key(exclude.KindEndpoint, endpoint)]; ok && o.Seconds > 0 {
			item["outbound_share"] = o.Share
		}
		if excluded.Endpoint(endpoint) {
			item["excluded"] = true
		}
		endpoints = append(endpoints, item)
	}
	rows.Close()

	var costs []metrics.Cost
	if machine != "" {
		var hostID int64
		_ = s.DB.Q().Row(ctx, "SELECT id FROM hosts WHERE project_id = ? AND name = ?", p.ID, machine).Scan(&hostID)
		costs, err = s.Metrics.QueriesOfHost(ctx, p.ID, hostID, from, now)
	} else {
		costs, err = s.Metrics.Costs(ctx, p.ID, from, now)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	// What the team left out is still shown, marked: hiding it would make the list lie.
	if left, err := s.Metrics.ExcludedCosts(ctx, p.ID, from, now); err == nil {
		costs = append(costs, left...)
	}
	database := strings.TrimSpace(r.URL.Query().Get("db"))
	// Statements recorded before machines were kept apart have no machine: they are not this
	// one's, so they are not shown, and the panel says how many are waiting to age out.
	var withoutMachine int64
	if machine != "" {
		_ = s.DB.Q().Row(ctx, "SELECT COUNT(*) FROM query_hours WHERE project_id = ? AND host_id = 0 AND hour >= ?", p.ID, fromHour).Scan(&withoutMachine)
	}
	queries := []map[string]any{}
	for _, c := range costs {
		if c.Kind != "query" || len(queries) >= 60 {
			continue
		}
		var origin, dbName string
		var nPlusOne int64
		var explained sql.NullInt64
		_ = s.DB.Q().Row(ctx, "SELECT origin, n_plus_one, explained_at, db_name FROM query_fingerprints WHERE project_id = ? AND hash = ?", p.ID, c.Target).Scan(&origin, &nPlusOne, &explained, &dbName)
		if database != "" && dbName != database {
			continue
		}
		avg := 0.0
		if c.Count > 0 {
			avg = c.Seconds / float64(c.Count)
		}
		item := map[string]any{"hash": c.Target, "fingerprint": c.Label, "calls": c.Count, "seconds": r2(c.Seconds), "avg": math.Round(avg*10000) / 10000,
			"max": r2(c.Max), "seconds_per_day": r2(c.Seconds / days), "origin": origin, "n_plus_one": nPlusOne, "explained": explained.Valid, "db": dbName}
		if excluded.Cost(exclude.KindQuery, c.Target) {
			item["excluded"] = true
		}
		compare(item, "query", c.Target)
		queries = append(queries, item)
	}

	php := []map[string]any{}
	rows, err = s.DB.Q().Query(ctx, `SELECT frame, code, source, SUM(n) FROM php_stall_hours WHERE project_id = ? AND hour >= ? GROUP BY frame, code, source ORDER BY SUM(n) DESC LIMIT 60`, p.ID, fromHour)
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var frame, code, source string
		var n int64
		if err := rows.Scan(&frame, &code, &source, &n); err == nil && keeps(source) {
			item := map[string]any{"frame": frame, "code": code, "source": source, "count": n}
			if excluded.Cost(exclude.KindPHP, frame) {
				item["excluded"] = true
			}
			php = append(php, item)
		}
	}
	rows.Close()

	writeJSON(w, http.StatusOK, map[string]any{"window": r.URL.Query().Get("window"), "endpoints": endpoints, "queries": queries, "php": php,
		"compare": map[string]any{"comparable": comparable}, "wins": wins(changes, comparable),
		"host": machine, "db": database, "databases": databaseNames(ctx, s.DB, p.ID), "queries_without_machine": withoutMachine})
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request, p project.Project) {
	ctx := r.Context()
	hash := r.PathValue("hash")
	var f struct {
		id                       int64
		fp, dbUser, dbName       string
		sample, explain, explErr sql.NullString
		explainedAt, requestedAt sql.NullInt64
		origin                   string
		nPlusOne, first, last    int64
	}
	err := s.DB.Q().Row(ctx, `SELECT id, fingerprint, sample, db_user, db_name, explain, explain_error, explained_at, explain_requested_at, origin, n_plus_one, first_seen_at, last_seen_at
		FROM query_fingerprints WHERE project_id = ? AND hash = ?`, p.ID, hash).Scan(&f.id, &f.fp, &f.sample, &f.dbUser, &f.dbName, &f.explain, &f.explErr, &f.explainedAt, &f.requestedAt, &f.origin, &f.nPlusOne, &f.first, &f.last)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "query not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}

	now := time.Now()
	week, err := s.Metrics.Costs(ctx, p.ID, now.Add(-7*24*time.Hour), now)
	if err != nil {
		serverError(w, err)
		return
	}
	var cost metrics.Cost
	for _, c := range week {
		if c.Kind == "query" && c.Target == hash {
			cost = c
		}
	}
	var rows7 int64
	_ = s.DB.Q().Row(ctx, "SELECT COALESCE(SUM(rows),0) FROM query_hours WHERE fingerprint_id = ? AND hour >= ?", f.id, now.Add(-7*24*time.Hour).Unix()).Scan(&rows7)
	days := s.Metrics.ObservedDays(ctx, p.ID, 7*24*time.Hour, now)
	stats := doctor.Stats{CallsPerDay: float64(cost.Count) / days, SecondsPerDay: cost.Seconds / days}
	if cost.Count > 0 {
		stats.AvgSeconds = cost.Seconds / float64(cost.Count)
		stats.RowsExaminedAvg = float64(rows7) / float64(cost.Count)
	}

	in := doctor.Input{Fingerprint: f.fp, Sample: f.sample.String, Stats: stats, Origin: f.origin, NPlusOne: f.nPlusOne}
	if f.explain.Valid {
		var e doctor.Explain
		if json.Unmarshal([]byte(f.explain.String), &e) == nil {
			in.Explain = &e
		}
	}
	mutes, err := mute.DiagnosesOf(ctx, s.DB, p.ID, hash)
	if err != nil {
		serverError(w, err)
		return
	}
	diagnoses, muted := mute.Split(doctor.Analyze(in), mutes)

	timeline := []map[string]any{}
	rows, err := s.DB.Q().Query(ctx, `SELECT hour, source, n, sum, max FROM query_hours WHERE fingerprint_id = ? AND hour >= ? ORDER BY hour`, f.id, now.Add(-7*24*time.Hour).Unix())
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var hour, n int64
		var source string
		var sum, mx float64
		if err := rows.Scan(&hour, &source, &n, &sum, &mx); err == nil {
			timeline = append(timeline, map[string]any{"hour": hour, "source": source, "calls": n, "seconds": r2(sum), "max": r2(mx)})
		}
	}
	rows.Close()

	routes := []map[string]any{}
	rows, err = s.DB.Q().Query(ctx, `SELECT route, n, sum, requests FROM route_queries WHERE fingerprint_id = ? ORDER BY sum DESC LIMIT 20`, f.id)
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var route string
		var n, requests int64
		var sum float64
		if err := rows.Scan(&route, &n, &sum, &requests); err == nil {
			per := r2(float64(n) / math.Max(1, float64(requests)))
			routes = append(routes, map[string]any{"route": route, "calls": n, "seconds": r2(sum), "requests": requests, "per_request": per})
			in.Routes = append(in.Routes, doctor.RouteUse{Route: route, PerRequest: per, Requests: requests})
		}
	}
	rows.Close()
	// The routes where it repeats the most come first: that is where the loop is.
	sort.SliceStable(in.Routes, func(i, j int) bool { return in.Routes[i].PerRequest > in.Routes[j].PerRequest })
	if len(in.Routes) > 8 {
		in.Routes = in.Routes[:8]
	}
	if s.PublicURL != "" {
		in.Link = s.PublicURL + "/projects/" + p.Slug + "/queries/" + hash
	}

	missions, err := s.missionList(ctx, p, "m.kind = 'query' AND m.target = ?", 10, hash)
	if err != nil {
		serverError(w, err)
		return
	}

	explainStatus := "not_needed"
	switch {
	case f.explainedAt.Valid && f.explErr.Valid:
		explainStatus = "error"
	case f.explainedAt.Valid:
		explainStatus = "done"
	case f.requestedAt.Valid:
		explainStatus = "requested"
	case f.sample.Valid:
		explainStatus = "waiting"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"hash": hash, "fingerprint": f.fp, "sample": f.sample.String, "db_user": f.dbUser, "database": f.dbName,
		"origin": f.origin, "n_plus_one": f.nPlusOne, "first_seen_at": f.first, "last_seen_at": f.last,
		"stats": stats, "explain": in.Explain, "explain_status": explainStatus, "explain_error": f.explErr.String,
		"exclusion": s.exclusionOf(r, p, exclude.KindQuery, hash),
		"diagnoses": diagnoses, "muted_diagnoses": muted, "prompt": doctor.Prompt(in, diagnoses), "timeline": timeline, "routes": routes, "missions": missions,
	})
}

func (s *Server) handleEndpoint(w http.ResponseWriter, r *http.Request, p project.Project) {
	ctx := r.Context()
	source, endpoint := r.URL.Query().Get("source"), r.URL.Query().Get("endpoint")
	now := time.Now()
	timeline := []map[string]any{}
	rows, err := s.DB.Q().Query(ctx, `SELECT hour, n, sum, max, slow, slow_sum, e5 FROM endpoint_hours WHERE project_id = ? AND source = ? AND endpoint = ? AND hour >= ? ORDER BY hour`,
		p.ID, source, endpoint, now.Add(-7*24*time.Hour).Unix())
	if err != nil {
		serverError(w, err)
		return
	}
	var total, slowTotal, e5Total int64
	var sumTotal, maxTotal float64
	for rows.Next() {
		var hour, n, slow, e5 int64
		var sum, mx, slowSum float64
		if err := rows.Scan(&hour, &n, &sum, &mx, &slow, &slowSum, &e5); err == nil {
			total += n
			slowTotal += slow
			e5Total += e5
			sumTotal += sum
			maxTotal = math.Max(maxTotal, mx)
			timeline = append(timeline, map[string]any{"hour": hour, "requests": n, "avg": r2(sum / math.Max(1, float64(n))), "max": r2(mx), "slow": slow, "e5": e5})
		}
	}
	rows.Close()

	// Queries of the matching framework route, when the package reports it ("GET /orders/{id}" -> routes with that method).
	queries := []map[string]any{}
	method := strings.SplitN(endpoint, " ", 2)[0]
	rows, err = s.DB.Q().Query(ctx, `SELECT rq.route, f.hash, f.fingerprint, rq.n, rq.sum, rq.requests, f.n_plus_one FROM route_queries rq JOIN query_fingerprints f ON f.id = rq.fingerprint_id
		WHERE rq.project_id = ? AND rq.route LIKE ? ORDER BY rq.sum DESC LIMIT 50`, p.ID, method+" %")
	if err != nil {
		serverError(w, err)
		return
	}
	path := strings.TrimPrefix(endpoint, method+" ")
	for rows.Next() {
		var route, hash, fp string
		var n, requests, nPlusOne int64
		var sum float64
		if err := rows.Scan(&route, &hash, &fp, &n, &sum, &requests, &nPlusOne); err == nil && routeMatches(route, method, path) {
			queries = append(queries, map[string]any{"route": route, "hash": hash, "fingerprint": fp, "calls": n, "seconds": r2(sum), "per_request": r2(float64(n) / math.Max(1, float64(requests))), "n_plus_one": nPlusOne})
		}
	}
	rows.Close()

	outbound, err := s.Metrics.Outbound(ctx, p.ID, endpoint, now.Add(-7*24*time.Hour), now)
	if err != nil {
		serverError(w, err)
		return
	}
	missions, err := s.missionList(ctx, p, "m.kind = 'endpoint' AND m.target = ?", 10, source+" "+endpoint)
	if err != nil {
		serverError(w, err)
		return
	}
	days := s.Metrics.ObservedDays(ctx, p.ID, 7*24*time.Hour, now)
	writeJSON(w, http.StatusOK, map[string]any{"source": source, "endpoint": endpoint, "missions": missions, "requests": total, "avg": r2(sumTotal / math.Max(1, float64(total))), "max": r2(maxTotal),
		"slow": slowTotal, "e5": e5Total, "timeline": timeline, "queries": queries,
		"outbound": outbound, "outbound_seconds_per_day": r2(outbound.Seconds / days), "exclusion": s.exclusionOf(r, p, exclude.KindEndpoint, endpoint)})
}

// routeMatches compares a framework route ("GET /orders/{order}") with a normalized path ("/orders/{id}").
func routeMatches(route, method, path string) bool {
	parts := strings.SplitN(route, " ", 2)
	if len(parts) != 2 || parts[0] != method {
		return false
	}
	a, b := strings.Split(strings.Trim(parts[1], "/"), "/"), strings.Split(strings.Trim(path, "/"), "/")
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		wa, wb := strings.HasPrefix(a[i], "{"), strings.HasPrefix(b[i], "{")
		if wa != wb || (!wa && a[i] != b[i]) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- missions page

func (s *Server) handleMissions(w http.ResponseWriter, r *http.Request, p project.Project) {
	ctx := r.Context()
	queries := missionctx.NewQueries(s.DB, p, time.Now())
	open, err := s.missionListWith(ctx, p, queries, "m.status = 'open'", 50)
	if err != nil {
		serverError(w, err)
		return
	}
	closed, err := s.missionListWith(ctx, p, queries, "m.status <> 'open'", 50)
	if err != nil {
		serverError(w, err)
		return
	}
	fame := []missionItem{}
	for _, m := range closed {
		if m.Status == "completed" && m.SavedPerDay > 0 && m.Verdict != "relapsed" {
			fame = append(fame, m)
		}
	}
	sort.Slice(fame, func(i, j int) bool { return fame[i].SavedPerDay > fame[j].SavedPerDay })
	if len(fame) > 10 {
		fame = fame[:10]
	}

	points, err := s.points(ctx, p)
	if err != nil {
		serverError(w, err)
		return
	}

	loc := p.Location()
	now := time.Now().In(loc)
	seasonStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	leaderboard := []map[string]any{}
	if p.Settings.People.Leaderboard {
		rows, err := s.DB.Q().Query(ctx, `SELECT person, SUM(points) FROM point_entries WHERE project_id = ? AND person <> '' AND created_at >= ? GROUP BY person ORDER BY SUM(points) DESC LIMIT 20`, p.ID, seasonStart.Unix())
		if err != nil {
			serverError(w, err)
			return
		}
		for rows.Next() {
			var person string
			var pts int64
			if err := rows.Scan(&person, &pts); err == nil {
				leaderboard = append(leaderboard, map[string]any{"person": person, "points": pts})
			}
		}
		rows.Close()
	}

	ledger := []map[string]any{}
	rows, err := s.DB.Q().Query(ctx, `SELECT points, reason, person, created_at FROM point_entries WHERE project_id = ? ORDER BY created_at DESC, id DESC LIMIT 40`, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var pts, at int64
		var reason, person string
		if err := rows.Scan(&pts, &reason, &person, &at); err == nil {
			ledger = append(ledger, map[string]any{"points": pts, "reason": reason, "person": person, "created_at": at})
		}
	}
	rows.Close()

	debtSummary, err := s.Engine.Debt(ctx, p)
	if err != nil {
		serverError(w, err)
		return
	}

	goal, err := debt.WeekGoal(ctx, s.DB, p, time.Now())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"goal": goal, "open": open, "closed": closed, "hall_of_fame": fame, "points": points, "leaderboard": leaderboard,
		"leaderboard_enabled": p.Settings.People.Leaderboard, "ledger": ledger, "debt": debtSummary, "settings": p.Settings.Missions})
}

// ---------------------------------------------------------------- heatmap, jobs, hosts

func (s *Server) handleHeatmap(w http.ResponseWriter, r *http.Request, p project.Project) {
	ctx := r.Context()
	loc := p.Location()
	from := time.Now().Add(-28 * 24 * time.Hour)
	rows, err := s.DB.Q().Query(ctx, `SELECT bucket - (bucket % 3600), SUM(n), SUM(sum), SUM(n2), SUM(e5) FROM http_minutes WHERE project_id = ? AND bucket >= ? GROUP BY bucket - (bucket % 3600)`, p.ID, from.Unix())
	if err != nil {
		serverError(w, err)
		return
	}
	type cell struct {
		n, slow, e5 int64
		sum         float64
		hours       int
	}
	var grid [7][24]cell
	for rows.Next() {
		var hour, n, slow, e5 int64
		var sum float64
		if err := rows.Scan(&hour, &n, &sum, &slow, &e5); err != nil {
			continue
		}
		t := time.Unix(hour, 0).In(loc)
		c := &grid[(int(t.Weekday())+6)%7][t.Hour()]
		c.n += n
		c.sum += sum
		c.slow += slow
		c.e5 += e5
		c.hours++
	}
	rows.Close()
	out := make([][]map[string]any, 7)
	for d := range grid {
		out[d] = make([]map[string]any, 24)
		for h, c := range grid[d] {
			avg, slowRatio, rph := 0.0, 0.0, 0.0
			if c.n > 0 {
				avg = c.sum / float64(c.n)
				slowRatio = float64(c.slow) / float64(c.n) * 100
			}
			if c.hours > 0 {
				rph = float64(c.n) / float64(c.hours)
			}
			out[d][h] = map[string]any{"avg": math.Round(avg*1000) / 1000, "slow_ratio": r2(slowRatio), "requests_per_hour": math.Round(rph), "e5": c.e5}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": 28, "grid": out, "timezone": p.Timezone})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request, p project.Project) {
	ctx := r.Context()
	now := time.Now()
	// The week, and the week before it: a job that doubled is the news on this page, and the
	// average alone never shows it.
	week := now.Add(-7 * 24 * time.Hour).Unix()
	before := now.Add(-14 * 24 * time.Hour).Unix()
	type jobKey struct{ kind, name string }
	type jobStat struct {
		runs, failed int64
		sum, max     float64
		last         int64
	}
	past := map[jobKey]jobStat{}
	rows, err := s.DB.Q().Query(ctx, `SELECT kind, name, SUM(n), SUM(sum) FROM job_hours
		WHERE project_id = ? AND hour >= ? AND hour < ? GROUP BY kind, name`, p.ID, before, week)
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var k jobKey
		var st jobStat
		if err := rows.Scan(&k.kind, &k.name, &st.runs, &st.sum); err == nil {
			past[k] = st
		}
	}
	rows.Close()

	jobs := []map[string]any{}
	rows, err = s.DB.Q().Query(ctx, `SELECT kind, name, SUM(n), SUM(failed), SUM(sum), MAX(max), MAX(hour) FROM job_hours
		WHERE project_id = ? AND hour >= ? GROUP BY kind, name ORDER BY SUM(sum) DESC LIMIT 100`, p.ID, week)
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var k jobKey
		var st jobStat
		if err := rows.Scan(&k.kind, &k.name, &st.runs, &st.failed, &st.sum, &st.max, &st.last); err != nil {
			continue
		}
		avg := st.sum / math.Max(1, float64(st.runs))
		item := map[string]any{
			"kind": k.kind, "name": k.name, "runs": st.runs, "failed": st.failed,
			"avg": r2(avg), "max": r2(st.max), "seconds": r2(st.sum),
			// Per day, over the week: "this job costs 4 minutes a day" is the sentence that lands.
			"seconds_per_day": r2(st.sum / 7),
			"failure_rate":    r2(float64(st.failed) / math.Max(1, float64(st.runs)) * 100),
			"last_at":         st.last,
		}
		if was, ok := past[k]; ok && was.runs > 0 {
			wasAvg := was.sum / float64(was.runs)
			item["was_avg"] = r2(wasAvg)
			item["was_runs"] = was.runs
			if wasAvg > 0 {
				item["avg_change"] = r2((avg - wasAvg) / wasAvg * 100) // % against the week before
			}
		}
		jobs = append(jobs, item)
	}
	rows.Close()

	runs := []map[string]any{}
	rows, err = s.DB.Q().Query(ctx, `SELECT host, name, started_at, finished_at, exit_code FROM command_runs WHERE project_id = ? AND started_at >= ? ORDER BY started_at DESC LIMIT 200`,
		p.ID, now.Add(-24*time.Hour).Unix())
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var host, name string
		var started int64
		var finished, exit sql.NullInt64
		if err := rows.Scan(&host, &name, &started, &finished, &exit); err == nil {
			item := map[string]any{"host": host, "name": name, "started_at": started, "finished_at": nil, "exit_code": nil}
			if finished.Valid {
				item["finished_at"] = finished.Int64
				item["seconds"] = finished.Int64 - started
			}
			if exit.Valid {
				item["exit_code"] = exit.Int64
			}
			runs = append(runs, item)
		}
	}
	rows.Close()

	overlaps, err := s.activitiesOfType(ctx, p, "command_overlap", 20)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "runs": runs, "overlaps": overlaps})
}

func (s *Server) activitiesOfType(ctx context.Context, p project.Project, kind string, limit int) ([]map[string]any, error) {
	all, err := s.activities(ctx, p, 500)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, a := range all {
		if a["type"] == kind && len(out) < limit {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *Server) hostList(ctx context.Context, p project.Project) ([]map[string]any, error) {
	// Read before the hosts query: SQLite has a single connection, a nested query would wait forever.
	checks, err := integrations.ForProject(ctx, s.DB, p.ID)
	if err != nil {
		return nil, err
	}
	muted, err := mute.Hosts(ctx, s.DB, p.ID)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Q().Query(ctx, `SELECT h.id, h.name, h.kind, h.version, h.os, h.sources, h.last_seen_at,
		(SELECT cpu FROM host_minutes m WHERE m.host_id = h.id ORDER BY bucket DESC LIMIT 1),
		(SELECT mem_used FROM host_minutes m WHERE m.host_id = h.id ORDER BY bucket DESC LIMIT 1),
		(SELECT mem_total FROM host_minutes m WHERE m.host_id = h.id ORDER BY bucket DESC LIMIT 1),
		(SELECT disk_used FROM host_minutes m WHERE m.host_id = h.id ORDER BY bucket DESC LIMIT 1),
		(SELECT disk_total FROM host_minutes m WHERE m.host_id = h.id ORDER BY bucket DESC LIMIT 1),
		(SELECT qps FROM db_minutes d WHERE d.host_id = h.id AND qps IS NOT NULL ORDER BY bucket DESC LIMIT 1),
		(SELECT swap_used FROM host_minutes m WHERE m.host_id = h.id ORDER BY bucket DESC LIMIT 1),
		(SELECT swap_total FROM host_minutes m WHERE m.host_id = h.id ORDER BY bucket DESC LIMIT 1),
		(SELECT cpu_steal FROM host_minutes m WHERE m.host_id = h.id ORDER BY bucket DESC LIMIT 1),
		(SELECT load1 FROM host_minutes m WHERE m.host_id = h.id ORDER BY bucket DESC LIMIT 1),
		(SELECT cpus FROM host_minutes m WHERE m.host_id = h.id ORDER BY bucket DESC LIMIT 1)
		FROM hosts h WHERE h.project_id = ? ORDER BY h.name, h.kind`, p.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, lastSeen int64
		var name, kind, version, osName, sources string
		var cpu, qps sql.NullFloat64
		var memUsed, memTotal, diskUsed, diskTotal, swapUsed, swapTotal, cpus sql.NullInt64
		var steal, load1 sql.NullFloat64
		if err := rows.Scan(&id, &name, &kind, &version, &osName, &sources, &lastSeen, &cpu, &memUsed, &memTotal, &diskUsed, &diskTotal, &qps, &swapUsed, &swapTotal, &steal, &load1, &cpus); err != nil {
			return nil, err
		}
		var list []string
		_ = json.Unmarshal([]byte(sources), &list)
		item := map[string]any{"id": id, "name": name, "kind": kind, "version": version, "os": osName, "sources": list, "last_seen_at": lastSeen,
			"online": time.Since(time.Unix(lastSeen, 0)) < 3*time.Minute}
		if c := checks[id]; len(c) > 0 {
			item["integrations"] = integrations.Summarize(c)
		}
		// A muted machine keeps its numbers: the panel stops asking for attention about it.
		if m, ok := muted[id]; ok {
			item["muted"] = m
		}
		if cpu.Valid {
			item["cpu"] = r2(cpu.Float64)
		}
		if memTotal.Valid && memTotal.Int64 > 0 {
			item["mem_percent"] = r2(float64(memUsed.Int64) / float64(memTotal.Int64) * 100)
		}
		if diskTotal.Valid && diskTotal.Int64 > 0 {
			item["disk_percent"] = r2(float64(diskUsed.Int64) / float64(diskTotal.Int64) * 100)
		}
		if qps.Valid {
			item["db_qps"] = r2(qps.Float64)
		}
		if swapTotal.Valid && swapTotal.Int64 > 0 {
			item["swap_percent"] = r2(float64(swapUsed.Int64) / float64(swapTotal.Int64) * 100)
		}
		if steal.Valid {
			item["cpu_steal"] = r2(steal.Float64)
		}
		if load1.Valid {
			item["load1"] = r2(load1.Float64)
		}
		if cpus.Valid {
			item["cpus"] = cpus.Int64
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request, p project.Project) {
	hosts, err := s.hostList(r.Context(), p)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts, "token": p.Token})
}

// ---------------------------------------------------------------- debt report

// handleDebtReport gathers what a CTO shows to the management: cost of the
// technical debt, its trend, the repayment plan and what was already repaid.
func (s *Server) handleDebtReport(w http.ResponseWriter, r *http.Request, p project.Project) {
	ctx := r.Context()
	debtSummary, err := s.Engine.Debt(ctx, p)
	if err != nil {
		serverError(w, err)
		return
	}
	days, err := s.scoreDays(ctx, p, 30)
	if err != nil {
		serverError(w, err)
		return
	}
	queries := missionctx.NewQueries(s.DB, p, time.Now())
	open, err := s.missionListWith(ctx, p, queries, "m.status = 'open'", 50)
	if err != nil {
		serverError(w, err)
		return
	}
	closed, err := s.missionListWith(ctx, p, queries, "m.status IN ('completed', 'defeated')", 50)
	if err != nil {
		serverError(w, err)
		return
	}

	// Advice the team muted is not what the report leads with: the next one is, if any.
	mutes, err := mute.Diagnoses(ctx, s.DB, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	findings := []map[string]any{}
	for _, item := range debtSummary.Items {
		if item.Kind != "query" || len(findings) >= 10 {
			continue
		}
		var sample, explain sql.NullString
		var origin string
		var nPlusOne int64
		_ = s.DB.Q().Row(ctx, "SELECT sample, explain, origin, n_plus_one FROM query_fingerprints WHERE project_id = ? AND hash = ?", p.ID, item.Target).Scan(&sample, &explain, &origin, &nPlusOne)
		in := doctor.Input{Fingerprint: item.Label, Sample: sample.String, Origin: origin, NPlusOne: nPlusOne}
		if explain.Valid {
			var e doctor.Explain
			if json.Unmarshal([]byte(explain.String), &e) == nil {
				in.Explain = &e
			}
		}
		if d, _ := mute.Split(doctor.Analyze(in), mutes[item.Target]); len(d) > 0 {
			findings = append(findings, map[string]any{"hash": item.Target, "fingerprint": item.Label, "seconds_per_day": r2(item.Seconds), "diagnosis": d[0]})
		}
	}

	var first, last *float64
	for _, d := range days {
		if d.Closed && d.Score != nil && !d.Partial {
			if first == nil {
				first = d.Score
			}
			last = d.Score
		}
	}

	excludedCosts, err := s.Metrics.ExcludedCosts(ctx, p.ID, time.Now().Add(-24*time.Hour), time.Now())
	if err != nil {
		serverError(w, err)
		return
	}
	reasons := map[string]string{}
	if list, err := exclude.List(ctx, s.DB, p.ID); err == nil {
		for _, e := range list {
			reasons[exclude.Key(e.Kind, e.Target)] = e.Reason
		}
	}
	excluded := []map[string]any{}
	for _, c := range excludedCosts {
		key := exclude.Key(c.Kind, c.Target)
		if c.Kind == exclude.KindEndpoint {
			key = exclude.Key(c.Kind, c.Label)
		}
		excluded = append(excluded, map[string]any{"kind": c.Kind, "target": c.Target, "endpoint": c.Label, "label": c.Label,
			"seconds_per_day": r2(c.Seconds), "reason": reasons[key]})
	}

	goal, _ := debt.WeekGoal(ctx, s.DB, p, time.Now())
	health, _ := debt.Delivery(ctx, s.DB, p, time.Now())
	hotspots, _ := debt.Hotspots(ctx, s.DB, p, time.Now())
	sort.SliceStable(open, func(i, j int) bool { return open[i].ROI > open[j].ROI })
	writeJSON(w, http.StatusOK, map[string]any{
		"goal": goal, "delivery": health, "hotspots": hotspots,
		"project":      map[string]any{"name": p.Name, "timezone": p.Timezone},
		"generated_at": time.Now().Unix(),
		"debt":         debtSummary,
		"score":        map[string]any{"first": first, "last": last, "days": days},
		"plan":         open,
		"repaid":       closed,
		"findings":     findings,
		"excluded":     excluded,
	})
}
