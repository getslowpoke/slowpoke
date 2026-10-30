package demo

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// A project imported from a profile has no agents: replaying keeps it alive the way the demo
// shop's generator does, with its own numbers. Every minute gets what the same minute of the
// week before got, so the real weekly rhythm (quiet nights, busy Monday mornings) goes on.
const replayLag = 7 * 24 * time.Hour

type replayTable struct {
	name, at string
	host     bool // scoped by host_id
	hourly   bool // one row per hour: copied at the start of the hour
}

var replayTables = []replayTable{
	{name: "http_minutes", at: "bucket"},
	{name: "db_minutes", at: "bucket"},
	{name: "metric_minutes", at: "bucket"},
	{name: "host_minutes", at: "bucket", host: true},
	{name: "endpoint_hours", at: "hour", hourly: true},
	{name: "query_hours", at: "hour", hourly: true},
	{name: "route_hours", at: "hour", hourly: true},
	{name: "route_calls", at: "hour", hourly: true},
	{name: "php_stall_hours", at: "hour", hourly: true},
	{name: "job_hours", at: "hour", hourly: true},
}

// ReplayMinute copies into minute m the rows of m a week earlier. Running it twice for the same
// minute adds nothing.
func ReplayMinute(ctx context.Context, db *store.DB, projectID int64, m time.Time) error {
	to := m.Truncate(time.Minute).Unix()
	from := to - int64(replayLag/time.Second)
	for _, t := range replayTables {
		if t.hourly && to%3600 != 0 {
			continue
		}
		cols, err := columnsOf(ctx, db, t.name)
		if err != nil {
			return err
		}
		sel := make([]string, len(cols))
		for i, c := range cols {
			sel[i] = c
			if c == t.at {
				sel[i] = "CAST(? AS BIGINT)"
			}
		}
		where := "project_id = ?"
		if t.host {
			where = "host_id IN (SELECT id FROM hosts WHERE project_id = ?)"
		}
		q := "INSERT INTO " + t.name + " (" + strings.Join(cols, ", ") + ") SELECT " + strings.Join(sel, ", ") +
			" FROM " + t.name + " WHERE " + where + " AND " + t.at + " = ? ON CONFLICT DO NOTHING"
		if _, err := db.Q().Exec(ctx, q, to, projectID, from); err != nil {
			return fmt.Errorf("replay %s: %w", t.name, err)
		}
	}
	// Whoever was reporting a moment ago is still reporting.
	for _, q := range []string{
		"UPDATE hosts SET last_seen_at = ? WHERE project_id = ? AND last_seen_at >= ?",
		"UPDATE source_sites SET last_seen_at = ? WHERE project_id = ? AND last_seen_at >= ?",
	} {
		if _, err := db.Q().Exec(ctx, q, to, projectID, to-3600); err != nil {
			return err
		}
	}
	return nil
}

func columnsOf(ctx context.Context, db *store.DB, table string) ([]string, error) {
	rows, err := db.Q().Query(ctx, "SELECT * FROM "+table+" WHERE 1 = 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return rows.Columns()
}

// RunReplay keeps replaying a project minute by minute until the context ends. After a restart
// it catches up from the last replayed minute, at most a day back.
func RunReplay(ctx context.Context, db *store.DB, slug string) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		p, err := project.Repo{DB: db}.BySlug(ctx, slug)
		if err == nil {
			var last int64
			_ = db.Q().Row(ctx, "SELECT COALESCE(MAX(bucket), 0) FROM http_minutes WHERE project_id = ?", p.ID).Scan(&last)
			from := time.Unix(last+60, 0)
			if limit := time.Now().Add(-24 * time.Hour); from.Before(limit) {
				from = limit
			}
			for m := from.Truncate(time.Minute); m.Before(time.Now().Truncate(time.Minute)); m = m.Add(time.Minute) {
				if err := ReplayMinute(ctx, db, p.ID, m); err != nil {
					slog.Warn("demo replay failed", "project", slug, "error", err)
					break
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
