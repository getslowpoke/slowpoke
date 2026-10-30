package demo

import (
	"context"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func TestReplayMinuteRepeatsTheSameMinuteOfLastWeek(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, err := project.Repo{DB: db}.Create(ctx, "Nestwise", "Europe/Rome")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)
	week := now.Add(-7 * 24 * time.Hour).Unix()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Q().Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	hostID, err := db.Q().InsertID(ctx, "INSERT INTO hosts (project_id, name, last_seen_at) VALUES (?, 'app-1', ?)", p.ID, now.Add(-10*time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO hosts (project_id, name, last_seen_at) VALUES (?, 'retired', ?)", p.ID, now.Add(-9*24*time.Hour).Unix())
	exec("INSERT INTO http_minutes (project_id, source, bucket, n, sum, max, h3) VALUES (?, 'nginx:app', ?, 40, 12.5, 2.1, 40)", p.ID, week)
	exec("INSERT INTO endpoint_hours (project_id, source, endpoint, hour, n, sum, max) VALUES (?, 'nginx:app', 'GET /leases', ?, 900, 300, 4)", p.ID, week)
	exec("INSERT INTO host_minutes (host_id, bucket, cpu, cpu_peak) VALUES (?, ?, 35, 80)", hostID, week)

	for i := 0; i < 2; i++ { // twice: the second run adds nothing
		if err := ReplayMinute(ctx, db, p.ID, now); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	var sum float64
	_ = db.Q().Row(ctx, "SELECT n, sum FROM http_minutes WHERE project_id = ? AND bucket = ?", p.ID, now.Unix()).Scan(&n, &sum)
	if n != 40 || sum != 12.5 {
		t.Errorf("http minute %d %.1f", n, sum)
	}
	_ = db.Q().Row(ctx, "SELECT n FROM endpoint_hours WHERE project_id = ? AND hour = ?", p.ID, now.Unix()).Scan(&n)
	if n != 900 {
		t.Errorf("endpoint hour %d", n)
	}
	var cpu float64
	_ = db.Q().Row(ctx, "SELECT cpu FROM host_minutes WHERE host_id = ? AND bucket = ?", hostID, now.Unix()).Scan(&cpu)
	if cpu != 35 {
		t.Errorf("host minute cpu %.0f", cpu)
	}
	// The machines that were alive stay alive; one gone for days stays gone.
	var alive int
	_ = db.Q().Row(ctx, "SELECT COUNT(*) FROM hosts WHERE project_id = ? AND last_seen_at = ?", p.ID, now.Unix()).Scan(&alive)
	if alive != 1 {
		t.Errorf("alive hosts %d", alive)
	}

	// An hourly table is copied once, at the start of the hour, not every minute.
	exec("DELETE FROM endpoint_hours WHERE project_id = ? AND hour = ?", p.ID, now.Unix())
	if err := ReplayMinute(ctx, db, p.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	_ = db.Q().Row(ctx, "SELECT COUNT(*) FROM endpoint_hours WHERE project_id = ? AND hour = ?", p.ID, now.Unix()).Scan(&n)
	if n != 0 {
		t.Errorf("hourly rows copied again at minute 1")
	}
}
