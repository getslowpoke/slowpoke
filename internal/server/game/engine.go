// Package game turns measures into the game: daily speed index, badges,
// missions to repay technical debt, bosses born from regressions, deploy
// verdicts and the points ledger.
package game

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	_, err := q.Exec(ctx, `INSERT INTO point_entries (project_id, points, reason, person, subject_type, subject_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projectID, points, reason, person, subjectType, subjectID, e.Now().Unix())
	return err
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
