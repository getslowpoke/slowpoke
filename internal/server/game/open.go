package game

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
)

var (
	ErrUnknownKind   = errors.New("not something a mission can be about")
	ErrUnknownTarget = errors.New("nothing measured with that name")
)

// OpenMission opens a mission the team asked for, from a slow endpoint or query they are looking at.
// It is a person's choice, so neither the cap on open missions nor the minimum cost applies; an
// open mission on the same thing is returned instead of a second one.
func (e *Engine) OpenMission(ctx context.Context, p project.Project, kind, target string) (id int64, created bool, err error) {
	switch kind {
	case "endpoint", "query", "php", "metric":
	default:
		return 0, false, ErrUnknownKind
	}
	err = e.DB.Q().Row(ctx, `SELECT id FROM missions WHERE project_id = ? AND kind = ? AND target = ? AND status = 'open' ORDER BY id LIMIT 1`,
		p.ID, kind, target).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	label, err := e.labelOf(ctx, p.ID, kind, target)
	if err != nil {
		return 0, false, err
	}

	// The baseline is what it costs per window now, read over the week (or the days observed, on a
	// young install) rather than the last closed window, which may predate the data.
	w := windowOf(p)
	now := e.Now()
	week := 7 * 24 * time.Hour
	costs, err := e.Metrics.Costs(ctx, p.ID, now.Add(-week), now)
	if err != nil {
		return 0, false, err
	}
	days := e.Metrics.ObservedDays(ctx, p.ID, week, now)
	baseline := findCost(costs, kind, target) / math.Max(days, 1.0/24) * w.Hours() / 24
	points := clamp(int(math.Round(perDay(baseline, w)/60)), 10, 500)
	// Judged from the next complete window on, like the ones the engine opens.
	id, err = e.DB.Q().InsertID(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, points, status, good_windows, person, opened_at, checked_at)
		VALUES (?, 'mission', ?, ?, ?, ?, ?, 'open', 0, '', ?, ?)`, p.ID, kind, target, label, baseline, points, now.Unix(), boundary(now, w).Unix())
	return id, err == nil, err
}

// labelOf names the target the way the engine does, and proves it was measured.
func (e *Engine) labelOf(ctx context.Context, projectID int64, kind, target string) (string, error) {
	var label string
	var err error
	switch kind {
	case "endpoint":
		source, endpoint, ok := strings.Cut(target, " ")
		if !ok {
			return "", ErrUnknownTarget
		}
		label = endpoint
		err = e.DB.Q().Row(ctx, `SELECT endpoint FROM endpoint_hours WHERE project_id = ? AND source = ? AND endpoint = ? LIMIT 1`, projectID, source, endpoint).Scan(&label)
	case "query":
		err = e.DB.Q().Row(ctx, `SELECT fingerprint FROM query_fingerprints WHERE project_id = ? AND hash = ?`, projectID, target).Scan(&label)
	case "php":
		label = target
		err = e.DB.Q().Row(ctx, `SELECT frame FROM php_stall_hours WHERE project_id = ? AND frame = ? LIMIT 1`, projectID, target).Scan(&label)
	case "metric":
		label = target
		err = e.DB.Q().Row(ctx, `SELECT name FROM metric_minutes WHERE project_id = ? AND kind = 'timer' AND name = ? LIMIT 1`, projectID, target).Scan(&label)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnknownTarget
	}
	return label, err
}
