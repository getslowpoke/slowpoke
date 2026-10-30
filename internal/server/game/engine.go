// Package game turns measures into the game: daily speed index, badges,
// missions to repay technical debt, bosses born from regressions, deploy
// verdicts and the points ledger.
package game

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/metrics"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

type Engine struct {
	DB      *store.DB
	Metrics metrics.Reader
	Now     func() time.Time

	// Set at the start of each project's evaluation: the hours it is judged on, and the windows
	// already judged (missions, bosses and the watch read the same windows).
	busy       Busy
	judgeCache map[[2]int64]judged
}

func New(db *store.DB) *Engine {
	return &Engine{DB: db, Metrics: metrics.Reader{DB: db}, Now: time.Now}
}

func (e *Engine) EvaluateProject(ctx context.Context, p project.Project) error {
	busy, err := BusyHours(ctx, e.DB, p, e.Now())
	if err != nil {
		return &StepError{Step: "busy hours", Err: err}
	}
	e.busy, e.judgeCache = busy, map[[2]int64]judged{}
	steps := []struct {
		name string
		fn   func(context.Context, project.Project) error
	}{
		{"days", e.evaluateDays},
		{"window", e.evaluateWindow},
		{"missions", e.evaluateMissions},
		{"deploys", e.evaluateDeploys},
		{"commands", e.evaluateCommandOverlaps},
		{"debt goal", e.evaluateDebtGoal},
		{"badges", e.evaluateBadges},
	}
	for _, s := range steps {
		if err := s.fn(ctx, p); err != nil {
			return &StepError{Step: s.name, Err: err}
		}
	}
	return nil
}

type StepError struct {
	Step string
	Err  error
}

func (s *StepError) Error() string { return s.Step + ": " + s.Err.Error() }
func (s *StepError) Unwrap() error { return s.Err }

// Run evaluates every project periodically until the context ends.
func (e *Engine) Run(ctx context.Context, interval time.Duration) {
	repo := project.Repo{DB: e.DB}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		projects, err := repo.List(ctx)
		if err != nil {
			slog.Error("game: cannot list projects", "error", err)
		}
		for _, p := range projects {
			unlock, ok, err := e.DB.TryLock(ctx, fmt.Sprintf("game:%d", p.ID))
			if err != nil || !ok {
				continue // another instance is evaluating it
			}
			if err := e.EvaluateProject(ctx, p); err != nil {
				slog.Error("game: evaluation failed", "project", p.Slug, "error", err)
			}
			unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// once returns true the first time a key is seen.
func (e *Engine) once(ctx context.Context, q store.Q, key string) (bool, error) {
	res, err := q.Exec(ctx, "INSERT INTO app_settings (key, value) VALUES (?, '1') ON CONFLICT (key) DO NOTHING", key)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (e *Engine) award(ctx context.Context, q store.Q, projectID int64, points int, reason, person, subjectType string, subjectID int64) error {
	if points == 0 {
		return nil
	}
	var season int64
	var before int64
	if person != "" && points > 0 {
		var tz string
		_ = q.Row(ctx, "SELECT timezone FROM projects WHERE id = ?", projectID).Scan(&tz)
		loc, err := time.LoadLocation(tz)
		if err != nil {
			loc = time.UTC
		}
		now := e.Now().In(loc)
		season = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).Unix()
		if err := q.Row(ctx, "SELECT COALESCE(SUM(points), 0) FROM point_entries WHERE project_id = ? AND person = ? AND created_at >= ?", projectID, person, season).Scan(&before); err != nil {
			return err
		}
	}
	_, err := q.Exec(ctx, `INSERT INTO point_entries (project_id, points, reason, person, subject_type, subject_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projectID, points, reason, person, subjectType, subjectID, e.Now().Unix())
	if err != nil || person == "" || points <= 0 {
		return err
	}
	return e.overtake(ctx, q, projectID, person, season, before, before+int64(points))
}

// overtake announces passing someone in the month's points: the best-placed person passed is named.
func (e *Engine) overtake(ctx context.Context, q store.Q, projectID int64, person string, season, before, after int64) error {
	var other string
	var theirs int64
	err := q.Row(ctx, `SELECT person, SUM(points) FROM point_entries WHERE project_id = ? AND person <> '' AND person <> ? AND created_at >= ?
		GROUP BY person HAVING SUM(points) >= ? AND SUM(points) < ? ORDER BY SUM(points) DESC LIMIT 1`, projectID, person, season, max(before, 1), after).Scan(&other, &theirs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	short := func(p string) string { return strings.SplitN(p, "@", 2)[0] }
	return e.activity(ctx, q, projectID, "leaderboard", "🏁", fmt.Sprintf("%s overtakes %s: %d to %d points this month", short(person), short(other), after, theirs),
		map[string]any{"person": person, "overtaken": other, "total": after, "their_total": theirs})
}

func (e *Engine) activity(ctx context.Context, q store.Q, projectID int64, kind, icon, title string, data map[string]any) error {
	raw, _ := json.Marshal(data)
	_, err := q.Exec(ctx, `INSERT INTO activities (project_id, type, icon, title, data, notified, created_at) VALUES (?, ?, ?, ?, ?, FALSE, ?)`,
		projectID, kind, icon, title, string(raw), e.Now().Unix())
	return err
}

func clamp(v, lo, hi int) int {
	return max(lo, min(hi, v))
}
