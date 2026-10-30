package game

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/metrics"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

const (
	bossLookbackWindows = 7
	bossMinHistory      = 4
	respawnCooldown     = 24 * time.Hour
	bossDefeatTolerance = 1.5
)

type mission struct {
	ID          int64
	Type, Kind  string
	Target      string
	Label       string
	Baseline    float64
	Points      int
	GoodWindows int
	OpenedAt    int64
	DueAt       sql.NullInt64
	CheckedAt   int64
	DeployID    sql.NullInt64
	Person      string
	Shipped     sql.NullInt64 // the deploy that carried the fix, once known
	// BaselineRate is the cost per request on busy hours when it was opened.
	BaselineRate sql.NullFloat64
}

func windowOf(p project.Project) time.Duration {
	return time.Duration(p.Settings.Missions.WindowHours) * time.Hour
}

// boundary is the end of the last complete window (UTC aligned).
func boundary(now time.Time, w time.Duration) time.Time {
	sec := int64(w / time.Second)
	return time.Unix(now.Unix()-now.Unix()%sec, 0)
}

func perDay(seconds float64, w time.Duration) float64 {
	return seconds * float64(24*time.Hour) / float64(w)
}

func (e *Engine) evaluateMissions(ctx context.Context, p project.Project) error {
	w := windowOf(p)
	now := e.Now()
	end := boundary(now, w)

	if err := e.shipByDeploy(ctx, p); err != nil {
		return err
	}
	if err := e.progress(ctx, p, w, end); err != nil {
		return err
	}
	if err := e.watchCompleted(ctx, p, w, end); err != nil {
		return err
	}
	if err := e.closeExpired(ctx, p, now); err != nil {
		return err
	}

	stateKey := fmt.Sprintf("game_window:%d", p.ID)
	var last string
	_ = e.DB.Q().Row(ctx, "SELECT value FROM app_settings WHERE key = ?", stateKey).Scan(&last)
	if lastEnd, _ := strconv.ParseInt(last, 10, 64); lastEnd >= end.Unix() {
		return nil
	}

	costs, err := e.Metrics.Costs(ctx, p.ID, end.Add(-w), end)
	if err != nil {
		return err
	}
	if err := e.spawnBosses(ctx, p, w, end, costs); err != nil {
		return err
	}
	if err := e.spawnMissions(ctx, p, w, end, costs); err != nil {
		return err
	}
	_, err = e.DB.Q().Exec(ctx, "INSERT INTO app_settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", stateKey, strconv.FormatInt(end.Unix(), 10))
	return err
}

func (e *Engine) openMissions(ctx context.Context, projectID int64) ([]mission, error) {
	rows, err := e.DB.Q().Query(ctx, `SELECT id, type, kind, target, label, baseline, points, good_windows, opened_at, due_at, checked_at, deploy_id, person, shipped_deploy_id, baseline_rate
		FROM missions WHERE project_id = ? AND status = 'open' ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mission
	for rows.Next() {
		var m mission
		if err := rows.Scan(&m.ID, &m.Type, &m.Kind, &m.Target, &m.Label, &m.Baseline, &m.Points, &m.GoodWindows, &m.OpenedAt, &m.DueAt, &m.CheckedAt, &m.DeployID, &m.Person, &m.Shipped, &m.BaselineRate); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func findCost(costs []metrics.Cost, kind, target string) float64 {
	for _, c := range costs {
		if c.Kind == kind && c.Target == target {
			return c.Seconds
		}
	}
	return 0
}

func (e *Engine) progress(ctx context.Context, p project.Project, w time.Duration, end time.Time) error {
	open, err := e.openMissions(ctx, p.ID)
	if err != nil {
		return err
	}
	s := p.Settings
	for _, m := range open {
		for checked := time.Unix(m.CheckedAt, 0); !checked.Add(w).After(end); checked = checked.Add(w) {
			from, to := checked, checked.Add(w)
			costs, err := e.Metrics.Costs(ctx, p.ID, from, to)
			if err != nil {
				return err
			}
			current := findCost(costs, m.Kind, m.Target)
			// Judged per request on the busy hours: a weekend or a busy Monday changes how much
			// is waited in total, not whether the fix worked.
			j, err := e.judge(ctx, p, from, to)
			if err != nil {
				return err
			}
			evidence := j.ok
			value, base := current, m.Baseline
			var rate any
			// A boss starts from the history before the regression, recorded when it appeared;
			// one from before this existed keeps the old reading.
			if j.ok && j.perRequest && (m.Type == "mission" || m.BaselineRate.Valid) {
				opening, err := e.openingRate(ctx, p, &m, w)
				if err != nil {
					return err
				}
				if opening.Valid {
					value, base = j.value(m.Kind, m.Target), opening.Float64
					rate = value
				}
			}

			status := "open"
			if evidence {
				if m.Type == "boss" {
					if value <= base*bossDefeatTolerance || current <= s.Bosses.MinSeconds/2 {
						status = "defeated"
						if !m.Shipped.Valid {
							if err := e.linkFixingDeploy(ctx, p, &m, from.Add(-w), to); err != nil {
								return err
							}
						}
					}
				} else if value <= base*s.Missions.TargetRatio {
					m.GoodWindows++
					if m.GoodWindows == 1 && !m.Shipped.Valid {
						// The cost just went down: the deploy right before it is, most likely,
						// the one that carried the fix.
						if err := e.linkFixingDeploy(ctx, p, &m, from.Add(-w), to); err != nil {
							return err
						}
					}
					if m.GoodWindows >= s.Missions.GoodWindows {
						status = "completed"
					}
				} else {
					m.GoodWindows = 0
				}
			}
			if rate != nil {
				if _, err := e.DB.Q().Exec(ctx, `UPDATE missions SET current_rate = ? WHERE id = ?`, rate, m.ID); err != nil {
					return err
				}
			}

			err = e.DB.Tx(ctx, func(q store.Q) error {
				closedAt := any(nil)
				if status != "open" {
					closedAt = to.Unix()
				}
				person := m.Person
				if status == "completed" && person == "" {
					person = e.deployAuthor(ctx, q, p.ID, m.OpenedAt, to.Unix())
				}
				var cur any
				if evidence {
					cur = current
				}
				_, err := q.Exec(ctx, `UPDATE missions SET current = COALESCE(?, current), good_windows = ?, checked_at = ?, status = ?, closed_at = ?, person = ? WHERE id = ?`,
					cur, m.GoodWindows, to.Unix(), status, closedAt, person, m.ID)
				if err != nil || status == "open" {
					return err
				}
				saved := perDay(m.Baseline-current, w)
				switch status {
				case "completed":
					if err := e.award(ctx, q, p.ID, m.Points, "Mission completed: "+m.Label, person, "mission", m.ID); err != nil {
						return err
					}
					return e.activity(ctx, q, p.ID, "mission_completed", "🧹", "Debt repaid: "+m.Label,
						map[string]any{"mission_id": m.ID, "kind": m.Kind, "points": m.Points, "person": person, "saved_seconds_per_day": math.Round(saved)})
				case "defeated":
					if err := e.award(ctx, q, p.ID, m.Points, "Boss defeated: "+m.Label, person, "mission", m.ID); err != nil {
						return err
					}
					return e.activity(ctx, q, p.ID, "boss_defeated", "⚔️", "Regression fixed: "+m.Label,
						map[string]any{"mission_id": m.ID, "kind": m.Kind, "points": m.Points, "person": person})
				}
				return nil
			})
			if err != nil {
				return err
			}
			if status != "open" {
				break
			}
			m.CheckedAt = to.Unix()
		}
	}
	return nil
}

func (e *Engine) deployAuthor(ctx context.Context, q store.Q, projectID, from, to int64) string {
	var author string
	err := q.Row(ctx, `SELECT author FROM deploys WHERE project_id = ? AND deployed_at >= ? AND deployed_at <= ? AND author <> '' ORDER BY deployed_at DESC LIMIT 1`, projectID, from, to).Scan(&author)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	return author
}

func (e *Engine) closeExpired(ctx context.Context, p project.Project, now time.Time) error {
	open, err := e.openMissions(ctx, p.ID)
	if err != nil {
		return err
	}
	for _, m := range open {
		if m.Type == "boss" && m.DueAt.Valid && now.Unix() < m.DueAt.Int64 && m.DueAt.Int64-now.Unix() <= int64(bossReminder/time.Second) {
			if err := e.remindBoss(ctx, p, m.ID, m.Label, m.Person, m.DueAt.Int64); err != nil {
				return err
			}
		}
		var status, icon, title string
		switch {
		case m.Type == "boss" && m.DueAt.Valid && now.Unix() >= m.DueAt.Int64:
			status, icon, title = "escaped", "💨", "A regression escaped: "+m.Label
		case m.Type == "mission" && now.Sub(time.Unix(m.OpenedAt, 0)) >= time.Duration(p.Settings.Missions.ExpireDays)*24*time.Hour:
			status = "expired"
		default:
			continue
		}
		err := e.DB.Tx(ctx, func(q store.Q) error {
			if _, err := q.Exec(ctx, "UPDATE missions SET status = ?, closed_at = ? WHERE id = ?", status, now.Unix(), m.ID); err != nil {
				return err
			}
			if status == "escaped" {
				return e.activity(ctx, q, p.ID, "boss_escaped", icon, title, map[string]any{"mission_id": m.ID})
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// busyTargets are targets with an open mission, or closed recently (no instant respawn).
func (e *Engine) busyTargets(ctx context.Context, projectID int64, now time.Time) (map[string]bool, error) {
	// A dismissed regression stays quiet for the days the team asked for, not only the cooldown.
	rows, err := e.DB.Q().Query(ctx, `SELECT kind, target FROM missions WHERE project_id = ? AND (status = 'open' OR closed_at >= ? OR quiet_until > ?)`,
		projectID, now.Add(-respawnCooldown).Unix(), now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	busy := map[string]bool{}
	for rows.Next() {
		var kind, target string
		if err := rows.Scan(&kind, &target); err != nil {
			return nil, err
		}
		busy[kind+"|"+target] = true
	}
	return busy, rows.Err()
}

func (e *Engine) spawnMissions(ctx context.Context, p project.Project, w time.Duration, end time.Time, costs []metrics.Cost) error {
	s := p.Settings.Missions
	var open int
	if err := e.DB.Q().Row(ctx, "SELECT COUNT(*) FROM missions WHERE project_id = ? AND type = 'mission' AND status = 'open'", p.ID).Scan(&open); err != nil {
		return err
	}
	busy, err := e.busyTargets(ctx, p.ID, e.Now())
	if err != nil {
		return err
	}
	for _, c := range costs {
		if open >= s.MaxOpen {
			break
		}
		if c.Seconds < s.MinSeconds || busy[c.Kind+"|"+c.Target] || c.Label == "OTHER" {
			continue
		}
		// One point per minute of waiting per day that the mission removes.
		points := clamp(int(math.Round(perDay(c.Seconds, w)/60)), 10, 500)
		_, err := e.DB.Q().Exec(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, points, status, good_windows, opened_at, checked_at)
			VALUES (?, 'mission', ?, ?, ?, ?, ?, 'open', 0, ?, ?)`, p.ID, c.Kind, c.Target, c.Label, c.Seconds, points, e.Now().Unix(), end.Unix())
		if err != nil {
			return err
		}
		open++
	}
	return nil
}

func (e *Engine) spawnBosses(ctx context.Context, p project.Project, w time.Duration, end time.Time, costs []metrics.Cost) error {
	s := p.Settings.Bosses
	first, err := e.Metrics.FirstData(ctx, p.ID)
	if err != nil || first.IsZero() || first.After(end.Add(-bossMinHistory*w)) {
		return err
	}
	history, err := e.Metrics.Costs(ctx, p.ID, end.Add(-(bossLookbackWindows+1)*w), end.Add(-w))
	// The average is over the windows that were measured: a project with four days of history
	// divided by seven saw everything 15% busier than usual as a regression.
	lookFrom := end.Add(-(bossLookbackWindows + 1) * w)
	if first.After(lookFrom) {
		lookFrom = first
	}
	measured := math.Min(bossLookbackWindows, math.Max(1, float64(end.Add(-w).Sub(lookFrom))/float64(w)))
	if err != nil {
		return err
	}
	busy, err := e.busyTargets(ctx, p.ID, e.Now())
	if err != nil {
		return err
	}
	// Judged per request on busy hours: a window nobody could judge (a Sunday) opens no boss, and
	// a Monday with twice the traffic is not twice as slow.
	now, err := e.judge(ctx, p, end.Add(-w), end)
	if err != nil || !now.ok {
		return err
	}
	var histCosts map[string]float64
	var histRequests int64
	if now.perRequest {
		histCosts = map[string]float64{}
		for k := 1; k <= bossLookbackWindows; k++ {
			j, err := e.judge(ctx, p, end.Add(-time.Duration(k+1)*w), end.Add(-time.Duration(k)*w))
			if err != nil {
				return err
			}
			if !j.ok || !j.perRequest {
				continue
			}
			for key, v := range j.costs {
				histCosts[key] += v
			}
			histRequests += j.requests
		}
	}

	for _, c := range costs {
		if c.Seconds < s.MinSeconds || busy[c.Kind+"|"+c.Target] || c.Label == "OTHER" {
			continue
		}
		avg := findCost(history, c.Kind, c.Target) / measured
		var baselineRate any
		if now.perRequest && histRequests > 0 {
			rate, before := now.value(c.Kind, c.Target), histCosts[c.Kind+"|"+c.Target]/float64(histRequests)
			if before == 0 && c.Seconds < 3*s.MinSeconds {
				continue
			}
			if rate < s.Growth*before {
				continue
			}
			baselineRate = before
		} else {
			if avg == 0 && c.Seconds < 3*s.MinSeconds {
				continue
			}
			if c.Seconds < s.Growth*avg {
				continue
			}
		}
		err := e.DB.Tx(ctx, func(q store.Q) error {
			var deployID sql.NullInt64
			var author string
			err := q.Row(ctx, `SELECT id, author FROM deploys WHERE project_id = ? AND deployed_at >= ? AND deployed_at < ? ORDER BY deployed_at DESC LIMIT 1`,
				p.ID, end.Add(-2*w).Unix(), end.Unix()).Scan(&deployID, &author)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			points := clamp(int(math.Round(perDay(c.Seconds, w)/30)), 25, 1000)
			due := e.Now().Add(time.Duration(s.DeadlineHours * float64(time.Hour)))
			id, err := q.InsertID(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, current, points, status, good_windows, deploy_id, person, opened_at, due_at, checked_at, baseline_rate)
				VALUES (?, 'boss', ?, ?, ?, ?, ?, ?, 'open', 0, ?, ?, ?, ?, ?, ?)`,
				p.ID, c.Kind, c.Target, c.Label, avg, c.Seconds, points, deployID, author, e.Now().Unix(), due.Unix(), end.Unix(), baselineRate)
			if err != nil {
				return err
			}
			// A regression on something the team just repaid is that fix coming back.
			var fixed int64
			var fixer string
			var reached float64
			err = q.Row(ctx, `SELECT id, person, COALESCE(current, 0) FROM missions WHERE project_id = ? AND type = 'mission' AND status = 'completed' AND verdict = ''
				AND kind = ? AND target = ? AND closed_at >= ? ORDER BY closed_at DESC LIMIT 1`,
				p.ID, c.Kind, c.Target, end.Add(-time.Duration(watchWindows+2)*w).Unix()).Scan(&fixed, &fixer, &reached)
			if err == nil {
				if err := e.relapse(ctx, q, p, w, end, fixed, c.Kind, c.Target, c.Label, reached, c.Seconds, fixer); err != nil {
					return err
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			growth := "new"
			if avg > 0 {
				growth = fmt.Sprintf("x%.1f", c.Seconds/avg)
			}
			return e.activity(ctx, q, p.ID, "boss", "👾", "A regression appeared: "+c.Label,
				map[string]any{"mission_id": id, "kind": c.Kind, "growth": growth, "deploy_id": deployID.Int64, "person": author, "due_at": due.Unix()})
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) repaidPerDay(ctx context.Context, p project.Project) (float64, error) {
	var repaid sql.NullFloat64
	err := e.DB.Q().Row(ctx, `SELECT SUM(baseline - COALESCE(current, 0)) FROM missions WHERE project_id = ? AND type = 'mission' AND status = 'completed' AND verdict <> 'relapsed'`, p.ID).Scan(&repaid)
	return perDay(repaid.Float64, windowOf(p)), err
}

// bossReminder is how long before its deadline a boss is announced again: enough to ship a fix.
const bossReminder = 2 * time.Hour

// remindBoss announces once that a boss is about to escape.
func (e *Engine) remindBoss(ctx context.Context, p project.Project, id int64, label, person string, due int64) error {
	return e.DB.Tx(ctx, func(q store.Q) error {
		res, err := q.Exec(ctx, "INSERT INTO app_settings (key, value) VALUES (?, '1') ON CONFLICT (key) DO NOTHING", fmt.Sprintf("boss_deadline:%d", id))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return e.activity(ctx, q, p.ID, "boss_deadline", "⏰", "Escapes in 2 hours: "+label,
			map[string]any{"mission_id": id, "person": person, "due_at": due})
	})
}
