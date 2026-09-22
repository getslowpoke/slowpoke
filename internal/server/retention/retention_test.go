package retention

import (
	"context"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func TestPruneKeepsRecentData(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	old, recent := now.Add(-20*24*time.Hour).Unix(), now.Add(-time.Hour).Unix()
	veryOld := now.Add(-200 * 24 * time.Hour).Unix()

	q := db.Q()
	for _, b := range []int64{old, recent} {
		q.Exec(ctx, "INSERT INTO http_minutes (project_id, source, bucket) VALUES (?, 'nginx:app', ?)", p.ID, b)
		q.Exec(ctx, "INSERT INTO ingest_batches (id, project_id, created_at) VALUES (?, ?, ?)", time.Unix(b, 0).String(), p.ID, b)
		// Metrics are compacted to one row per hour past the first day, so they are kept a month:
		// 20 days old is still there, unlike the minutes of requests.
		q.Exec(ctx, "INSERT INTO metric_minutes (project_id, host_id, name, bucket, kind) VALUES (?, 0, 'queue.backlog', ?, 'gauge')", p.ID, b)
	}
	for _, h := range []int64{veryOld, old} {
		q.Exec(ctx, "INSERT INTO endpoint_hours (project_id, source, endpoint, hour) VALUES (?, 'nginx:app', 'GET /', ?)", p.ID, h)
	}

	deleted, err := Prune(ctx, db, now, Defaults())
	if err != nil {
		t.Fatal(err)
	}
	count := func(table string) int {
		var n int
		_ = q.Row(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n)
		return n
	}
	if count("http_minutes") != 1 || count("ingest_batches") != 1 || count("metric_minutes") != 2 || count("endpoint_hours") != 1 || deleted != 3 {
		t.Fatalf("http=%d batches=%d metrics=%d endpoints=%d deleted=%d",
			count("http_minutes"), count("ingest_batches"), count("metric_minutes"), count("endpoint_hours"), deleted)
	}
}
