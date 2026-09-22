package game

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/metrics"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/score"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// Late data can still arrive for a few minutes after midnight.
const closeGrace = 10 * time.Minute

type DayMetrics struct {
	Requests  int64   `json:"requests"`
	P95       float64 `json:"p95"`
	Avg       float64 `json:"avg"`
	SlowRatio float64 `json:"slow_ratio"`
	ErrorRate float64 `json:"error_rate"`
	E5        int64   `json:"e5"`
	PHPStalls int64   `json:"php_stalls"`
	DBSlowSum float64 `json:"db_slow_sum"`
	DBSlowMax float64 `json:"db_slow_max"`
	Hours     float64 `json:"hours"`
	HasHTTP   bool    `json:"has_http"`
	HasPHP    bool    `json:"has_php"`
	HasDB     bool    `json:"has_db"`
}

func SummarizeSnapshot(s metrics.Snapshot) DayMetrics {
	all := s.HTTPFor("")
	m := DayMetrics{Requests: all.N, P95: round3(all.Percentile(0.95)), Avg: round3(all.Avg()), E5: all.E5, PHPStalls: s.PHPStalls,
		DBSlowSum: round3(s.DB.SlowSum), DBSlowMax: round3(s.DB.SlowMax), Hours: round3(s.Hours), HasHTTP: s.Has.HTTP, HasPHP: s.Has.PHP, HasDB: s.Has.DB}
	if all.N > 0 {
		m.SlowRatio = float64(all.N2) / float64(all.N)
		m.ErrorRate = float64(all.E5) / float64(all.N) * 10000
	}
	return m
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func (e *Engine) evaluateDays(ctx context.Context, p project.Project) error {
	now := e.Now().In(p.Location())
	today := dayStart(now)

	for back := 3; back >= 0; back-- {
		day := today.AddDate(0, 0, -back)
		end := day.AddDate(0, 0, 1)
		key := day.Format("2006-01-02")

		var wasClosed bool
		err := e.DB.Q().Row(ctx, "SELECT closed FROM score_days WHERE project_id = ? AND day = ?", p.ID, key).Scan(&wasClosed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if wasClosed {
			continue
		}

		to := end
		if now.Before(end) {
			to = now
		}
		snap, err := e.Metrics.Snapshot(ctx, p.ID, day, to)
		if err != nil {
			return err
		}
		if snap.Hours == 0 {
			continue
		}
		result := score.Compute(p.Settings, snap)
		closed := !now.Before(end.Add(closeGrace))
		partial := result.Partial || snap.Hours < p.Settings.Score.MinHoursForFullDay

		components, _ := json.Marshal(result.Components)
		summary, _ := json.Marshal(SummarizeSnapshot(snap))
		var total any
		if result.Total != nil {
			total = *result.Total
		}

		err = e.DB.Tx(ctx, func(q store.Q) error {
			_, err := q.Exec(ctx, `INSERT INTO score_days (project_id, day, score, partial, closed, hours, components, metrics, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (project_id, day) DO UPDATE SET score = excluded.score, partial = excluded.partial, closed = excluded.closed,
				hours = excluded.hours, components = excluded.components, metrics = excluded.metrics, updated_at = excluded.updated_at`,
				p.ID, key, total, partial, closed, snap.Hours, string(components), string(summary), e.Now().Unix())
			if err != nil || !closed || partial || result.Total == nil {
				return err
			}
			return e.levelUp(ctx, q, p, key, *result.Total)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// levelUp celebrates the first full day at a level never reached before.
func (e *Engine) levelUp(ctx context.Context, q store.Q, p project.Project, day string, dayScore float64) error {
	var best sql.NullFloat64
	if err := q.Row(ctx, "SELECT MAX(score) FROM score_days WHERE project_id = ? AND closed AND NOT partial AND day < ?", p.ID, day).Scan(&best); err != nil {
		return err
	}
	level := p.Settings.LevelFor(dayScore)
	if level.Min == 0 {
		return nil
	}
	if best.Valid && p.Settings.LevelFor(best.Float64).Min >= level.Min {
		return nil
	}
	if first, err := e.once(ctx, q, fmt.Sprintf("level_up:%d:%s", p.ID, day)); err != nil || !first {
		return err
	}
	if err := e.award(ctx, q, p.ID, 100, "Reached "+level.Name, "", "level", int64(level.Min)); err != nil {
		return err
	}
	return e.activity(ctx, q, p.ID, "level_up", level.Icon, fmt.Sprintf("New level reached: %s (%.1f)", level.Name, dayScore), map[string]any{"level": level.Name, "score": dayScore, "day": day})
}

// ---------------------------------------------------------------- badges

type BadgeDef struct {
	Key         string `json:"key"`
	Icon        string `json:"icon"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

const badgePoints = 25

type closedDay struct {
	Day     string
	Score   float64
	Partial bool
	M       DayMetrics
}

type facts struct {
	days           []closedDay
	goalsKept      int
	missionsDone   int
	bossesDefeated int
	betterDeploys  int
	repaidPerDay   float64
}

func BadgeDefs(s project.Settings) []BadgeDef {
	defs := []BadgeDef{{"start", "🚀", "Kick-off", "The first day with measures."}}
	for i := len(s.Score.Levels) - 1; i >= 0; i-- {
		l := s.Score.Levels[i]
		if l.Min > 0 {
			defs = append(defs, BadgeDef{"level_" + strings.ToLower(l.Name), l.Icon, l.Name, fmt.Sprintf("A full day at %.0f points or more.", l.Min)})
		}
	}
	return append(defs,
		BadgeDef{"half_second", "⚡", "Half a second", "p95 under 500 ms for a whole day."},
		BadgeDef{"zero_500", "🛡️", "Zero 500", "No 5xx errors for a whole day."},
		BadgeDef{"no_minute", "⏱️", "Never over a minute", "No query over 60 s in a day."},
		BadgeDef{"no_ten", "🎯", "Never over 10 seconds", "No query over 10 s in a day."},
		BadgeDef{"light_db", "🪶", "Light database", "Less than 5 minutes of slow queries in a day."},
		BadgeDef{"smooth_php", "🧈", "Smooth PHP", "Less than 50 PHP stalls in a day."},
		BadgeDef{"comeback", "📈", "Comeback", "10 points more than the day before."},
		BadgeDef{"three_up", "🔥", "Three in a row", "Three days in a row, each better than the previous."},
		BadgeDef{"silver_week", "🏆", "Silver week", "Seven full days in a row at 70 or more."},
		BadgeDef{"first_mission", "🧹", "First repayment", "The first debt mission completed."},
		BadgeDef{"mission_hunter", "🏹", "Debt hunter", "Ten debt missions completed."},
		BadgeDef{"boss_slayer", "⚔️", "Boss slayer", "A regression defeated before its deadline."},
		BadgeDef{"green_deploy", "✅", "Green deploy", "A deploy that made the speed index better."},
		BadgeDef{"debt_cutter", "✂️", "Debt cutter", "One hour per day of waiting repaid with missions."},
		BadgeDef{"budget_kept", "🎯", "Budget kept", "The weekly debt budget repaid for the first time."},
		BadgeDef{"steady_payer", "🗓️", "Steady payer", "The weekly debt budget kept for four weeks."},
	)
}

func (e *Engine) loadFacts(ctx context.Context, p project.Project) (facts, error) {
	var f facts
	rows, err := e.DB.Q().Query(ctx, "SELECT day, score, partial, metrics FROM score_days WHERE project_id = ? AND closed AND score IS NOT NULL ORDER BY day", p.ID)
	if err != nil {
		return f, err
	}
	for rows.Next() {
		var d closedDay
		var raw string
		if err := rows.Scan(&d.Day, &d.Score, &d.Partial, &raw); err != nil {
			rows.Close()
			return f, err
		}
		_ = json.Unmarshal([]byte(raw), &d.M)
		f.days = append(f.days, d)
	}
	rows.Close()

	q := e.DB.Q()
	if err := q.Row(ctx, "SELECT COUNT(*) FROM missions WHERE project_id = ? AND type = 'mission' AND status = 'completed'", p.ID).Scan(&f.missionsDone); err != nil {
		return f, err
	}
	if err := q.Row(ctx, "SELECT COUNT(*) FROM missions WHERE project_id = ? AND type = 'boss' AND status = 'defeated'", p.ID).Scan(&f.bossesDefeated); err != nil {
		return f, err
	}
	if err := q.Row(ctx, "SELECT COUNT(*) FROM deploys WHERE project_id = ? AND impact LIKE '%\"verdict\":\"better\"%'", p.ID).Scan(&f.betterDeploys); err != nil {
		return f, err
	}
	if err := q.Row(ctx, "SELECT COUNT(*) FROM app_settings WHERE key LIKE ?", fmt.Sprintf("goal:%d:%%", p.ID)).Scan(&f.goalsKept); err != nil {
		return f, err
	}
	repaid, err := e.repaidPerDay(ctx, p)
	f.repaidPerDay = repaid
	return f, err
}

func (e *Engine) evaluateBadges(ctx context.Context, p project.Project) error {
	f, err := e.loadFacts(ctx, p)
	if err != nil {
		return err
	}
	var full []closedDay
	for _, d := range f.days {
		if !d.Partial {
			full = append(full, d)
		}
	}
	firstWhere := func(days []closedDay, test func(i int, d closedDay) bool) string {
		for i, d := range days {
			if test(i, d) {
				return d.Day
			}
		}
		return ""
	}
	today := e.Now().In(p.Location()).Format("2006-01-02")
	whenFact := func(ok bool) string {
		if ok {
			return today
		}
		return ""
	}

	unlocked := map[string]string{
		"start":       firstWhere(f.days, func(int, closedDay) bool { return true }),
		"half_second": firstWhere(full, func(_ int, d closedDay) bool { return d.M.HasHTTP && d.M.Requests > 0 && d.M.P95 < 0.5 }),
		"zero_500":    firstWhere(full, func(_ int, d closedDay) bool { return d.M.Requests > 0 && d.M.E5 == 0 }),
		"no_minute":   firstWhere(full, func(_ int, d closedDay) bool { return d.M.HasDB && d.M.DBSlowMax < 60 }),
		"no_ten":      firstWhere(full, func(_ int, d closedDay) bool { return d.M.HasDB && d.M.DBSlowMax < 10 }),
		"light_db":    firstWhere(full, func(_ int, d closedDay) bool { return d.M.HasDB && d.M.DBSlowSum < 300 }),
		"smooth_php":  firstWhere(full, func(_ int, d closedDay) bool { return d.M.HasPHP && d.M.PHPStalls < 50 }),
		"comeback":    firstWhere(full, func(i int, d closedDay) bool { return i > 0 && d.Score-full[i-1].Score >= 10 }),
		"three_up": firstWhere(full, func(i int, d closedDay) bool {
			return i >= 3 && full[i].Score > full[i-1].Score && full[i-1].Score > full[i-2].Score && full[i-2].Score > full[i-3].Score
		}),
		"silver_week": firstWhere(full, func(i int, d closedDay) bool {
			if i < 6 {
				return false
			}
			for k := i - 6; k <= i; k++ {
				if full[k].Score < 70 {
					return false
				}
			}
			return true
		}),
		"first_mission":  whenFact(f.missionsDone >= 1),
		"mission_hunter": whenFact(f.missionsDone >= 10),
		"boss_slayer":    whenFact(f.bossesDefeated >= 1),
		"green_deploy":   whenFact(f.betterDeploys >= 1),
		"debt_cutter":    whenFact(f.repaidPerDay >= 3600),
		"budget_kept":    whenFact(f.goalsKept >= 1),
		"steady_payer":   whenFact(f.goalsKept >= 4),
	}
	for _, l := range p.Settings.Score.Levels {
		if l.Min > 0 {
			min := l.Min
			unlocked["level_"+strings.ToLower(l.Name)] = firstWhere(full, func(_ int, d closedDay) bool { return d.Score >= min })
		}
	}

	defs := map[string]BadgeDef{}
	for _, d := range BadgeDefs(p.Settings) {
		defs[d.Key] = d
	}

	for _, def := range BadgeDefs(p.Settings) {
		day := unlocked[def.Key]
		if day == "" {
			continue
		}
		err := e.DB.Tx(ctx, func(q store.Q) error {
			res, err := q.Exec(ctx, "INSERT INTO unlocked_badges (project_id, badge, day, created_at) VALUES (?, ?, ?, ?) ON CONFLICT (project_id, badge) DO NOTHING", p.ID, def.Key, day, e.Now().Unix())
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil
			}
			if err := e.award(ctx, q, p.ID, badgePoints, "Badge: "+def.Title, "", "badge", 0); err != nil {
				return err
			}
			return e.activity(ctx, q, p.ID, "badge", def.Icon, "Badge unlocked: "+def.Title, map[string]any{"badge": def.Key, "description": def.Description})
		})
		if err != nil {
			return err
		}
	}
	return nil
}
