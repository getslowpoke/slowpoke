// Package retention deletes measures older than their retention.
package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/store"
)

type Policy struct {
	Minutes time.Duration
	Hours   time.Duration
	// Detail is how long custom metrics and machines keep their minutes before Compact makes
	// them one row per hour; Compacted is how long those hours are kept. A day and a bit: the
	// 24-hour charts step by five minutes, everything longer by the hour.
	Detail    time.Duration
	Compacted time.Duration
	// Hourly is how long endpoints, queries and PHP stalls keep their hours before Compact makes
	// them one row per day (kept for Hours). Short is for what no page reads past a month:
	// routes, outbound calls and jobs.
	Hourly   time.Duration
	Short    time.Duration
	Commands time.Duration
	Batches  time.Duration
}

func Defaults() Policy {
	return Policy{Minutes: 14 * 24 * time.Hour, Hours: 120 * 24 * time.Hour, Detail: 26 * time.Hour, Compacted: 31 * 24 * time.Hour,
		Hourly: 22 * 24 * time.Hour, Short: 31 * 24 * time.Hour, Commands: 30 * 24 * time.Hour, Batches: 48 * time.Hour}
}

func Prune(ctx context.Context, db *store.DB, now time.Time, p Policy) (int64, error) {
	steps := []struct {
		sql string
		age time.Duration
	}{
		{"DELETE FROM http_minutes WHERE bucket < ?", p.Minutes},
		{"DELETE FROM db_minutes WHERE bucket < ?", p.Minutes},
		{"DELETE FROM host_minutes WHERE bucket < ?", p.Compacted},
		{"DELETE FROM metric_minutes WHERE bucket < ?", p.Compacted},
		{"DELETE FROM endpoint_hours WHERE hour < ?", p.Hours},
		{"DELETE FROM query_hours WHERE hour < ?", p.Hours},
		{"DELETE FROM php_stall_hours WHERE hour < ?", p.Hours},
		{"DELETE FROM route_hours WHERE hour < ?", p.Short},
		{"DELETE FROM route_calls WHERE hour < ?", p.Short},
		{"DELETE FROM job_hours WHERE hour < ?", p.Short},
		{"DELETE FROM command_runs WHERE started_at < ?", p.Commands},
		{"DELETE FROM ingest_batches WHERE created_at < ?", p.Batches},
		// A host a source has not answered for in as long as the traffic is kept says nothing
		// about the traffic that is left.
		{"DELETE FROM source_sites WHERE last_seen_at < ?", p.Hours},
		{"DELETE FROM sessions WHERE expires_at < ?", 0},
	}
	var total int64
	for _, s := range steps {
		res, err := db.Q().Exec(ctx, s.sql, now.Add(-s.age).Unix())
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

func Run(ctx context.Context, db *store.DB, p Policy) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		if err := Compact(ctx, db, time.Now(), p); err != nil {
			slog.Error("retention: compaction failed", "error", err)
		}
		if n, err := Prune(ctx, db, time.Now(), p); err != nil {
			slog.Error("retention: prune failed", "error", err)
		} else if n > 0 {
			slog.Info("retention: pruned old rows", "rows", n)
		}
		if err := db.Reclaim(ctx); err != nil {
			slog.Error("retention: reclaiming disk space failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
