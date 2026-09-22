package game

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/score"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

type Side struct {
	Requests      int64    `json:"requests"`
	Score         *float64 `json:"score"`
	P95           float64  `json:"p95"`
	SlowRatio     float64  `json:"slow_ratio"`
	ErrorRate     float64  `json:"error_rate"`
	DBSlowSeconds float64  `json:"db_slow_seconds"`
}

type Impact struct {
	Minutes int      `json:"minutes"`
	Before  Side     `json:"before"`
	After   Side     `json:"after"`
	Delta   *float64 `json:"delta"`
	Verdict string   `json:"verdict"` // better | worse | neutral | no_data
	// Compared says what "before" is: the same hour on the days before (same_hour), or the hour
	// right before the deploy when there is not enough history (hour_before).
	Compared string `json:"compared"`
}

const verdictThreshold = 3

func (e *Engine) side(ctx context.Context, p project.Project, from, to time.Time) (Side, error) {
	snap, err := e.Metrics.Snapshot(ctx, p.ID, from, to)
	if err != nil {
		return Side{}, err
	}
	m := SummarizeSnapshot(snap)
	return Side{Requests: m.Requests, Score: score.Compute(p.Settings, snap).Total, P95: m.P95, SlowRatio: m.SlowRatio, ErrorRate: m.ErrorRate, DBSlowSeconds: m.DBSlowSum}, nil
}

func (e *Engine) evaluateDeploys(ctx context.Context, p project.Project) error {
	window := time.Duration(p.Settings.Deploys.CompareMinutes) * time.Minute
	now := e.Now()
	rows, err := e.DB.Q().Query(ctx, `SELECT id, version, commit_sha, author, deployed_at FROM deploys
		WHERE project_id = ? AND impact IS NULL AND deployed_at <= ? AND deployed_at >= ? ORDER BY deployed_at`,
		p.ID, now.Add(-window).Unix(), now.Add(-7*24*time.Hour).Unix())
	if err != nil {
		return err
	}
	type pending struct {
		id                      int64
		version, commit, author string
		at                      int64
	}
	var list []pending
	for rows.Next() {
		var d pending
		if err := rows.Scan(&d.id, &d.version, &d.commit, &d.author, &d.at); err != nil {
			rows.Close()
			return err
		}
		list = append(list, d)
	}
	rows.Close()

	for _, d := range list {
		at := time.Unix(d.at, 0)
		before, compared, err := e.deployBaseline(ctx, p, at, window)
		if err != nil {
			return err
		}
		after, err := e.side(ctx, p, at, at.Add(window))
		if err != nil {
			return err
		}
		impact := Impact{Minutes: int(window.Minutes()), Before: before, After: after, Verdict: "no_data", Compared: compared}
		if before.Score != nil && after.Score != nil && after.Requests > 0 {
			delta := *after.Score - *before.Score
			delta = float64(int(delta*10)) / 10
			impact.Delta = &delta
			switch {
			case delta >= verdictThreshold:
				impact.Verdict = "better"
			case delta <= -verdictThreshold:
				impact.Verdict = "worse"
			default:
				impact.Verdict = "neutral"
			}
		}
		raw, _ := json.Marshal(impact)
		label := d.version
		if label == "" && len(d.commit) >= 7 {
			label = d.commit[:7]
		}
		if label == "" {
			label = fmt.Sprintf("#%d", d.id)
		}

		err = e.DB.Tx(ctx, func(q store.Q) error {
			if _, err := q.Exec(ctx, "UPDATE deploys SET impact = ? WHERE id = ?", string(raw), d.id); err != nil {
				return err
			}
			data := map[string]any{"deploy_id": d.id, "verdict": impact.Verdict, "delta": impact.Delta, "person": d.author}
			switch impact.Verdict {
			case "better":
				if err := e.award(ctx, q, p.ID, 30, "Faster deploy "+label, d.author, "deploy", d.id); err != nil {
					return err
				}
				return e.activity(ctx, q, p.ID, "deploy", "✅", fmt.Sprintf("Deploy %s made the speed index better (+%.1f)", label, *impact.Delta), data)
			case "worse":
				return e.activity(ctx, q, p.ID, "deploy", "🐢", fmt.Sprintf("Deploy %s made the speed index worse (%.1f)", label, *impact.Delta), data)
			case "neutral":
				return e.activity(ctx, q, p.ID, "deploy", "🚀", fmt.Sprintf("Deploy %s: speed index unchanged", label), data)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// deployBaseline is what the hour after a deploy is compared with. Traffic and load change with
// the hour: at 18 people leave and the requests left are the heavy ones, so the hour after an
// evening deploy is slower than the afternoon on any day. The same hour on the days before (of
// the same kind: weekdays with weekdays, weekends with weekends) is the fair comparison; the
// hour right before the deploy stands in while there are fewer than three of them.
func (e *Engine) deployBaseline(ctx context.Context, p project.Project, at time.Time, window time.Duration) (Side, string, error) {
	loc := p.Location()
	weekend := func(t time.Time) bool { wd := t.In(loc).Weekday(); return wd == time.Saturday || wd == time.Sunday }
	var days []Side
	for k := 1; k <= 14 && len(days) < 7; k++ {
		from := at.AddDate(0, 0, -k)
		if weekend(from) != weekend(at) {
			continue
		}
		s, err := e.side(ctx, p, from, from.Add(window))
		if err != nil {
			return Side{}, "", err
		}
		if s.Score != nil && s.Requests > 0 {
			days = append(days, s)
		}
	}
	if len(days) >= 3 {
		sort.Slice(days, func(i, j int) bool { return *days[i].Score < *days[j].Score })
		return days[len(days)/2], "same_hour", nil
	}
	s, err := e.side(ctx, p, at.Add(-window), at)
	return s, "hour_before", err
}
