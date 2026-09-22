package game

import (
	"context"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// A regression from a spike nobody will act on (an import, a deploy already rolled back) can be
// waved away on its own: the boss closes with no points and does not come back the next day,
// while the endpoint keeps counting in the score.
func TestDismissingARegressionKeepsItQuietWithoutExcludingItsTarget(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	id, err := db.Q().InsertID(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, points, status, opened_at, checked_at)
		VALUES (?, 'boss', 'endpoint', 'nginx:shop POST /scanner', 'POST /scanner', 300, 120, 'open', ?, ?)`, p.ID, now.Unix(), now.Unix())
	if err != nil {
		t.Fatal(err)
	}

	if err := Dismiss(ctx, db, p.ID, id, "one-off import", 7, now); err != nil {
		t.Fatal(err)
	}
	var status string
	var closed, quiet int64
	db.Q().Row(ctx, `SELECT status, closed_at, quiet_until FROM missions WHERE id = ?`, id).Scan(&status, &closed, &quiet)
	if status != "dismissed" || closed != now.Unix() || quiet != now.Add(7*24*time.Hour).Unix() {
		t.Fatalf("status=%s closed=%d quiet=%d", status, closed, quiet)
	}
	var points int
	db.Q().Row(ctx, "SELECT COUNT(*) FROM point_entries").Scan(&points)
	if points != 0 {
		t.Fatalf("%d points for waving a regression away", points)
	}

	// While it is quiet the same target opens nothing; after that it can come back.
	e := &Engine{DB: db, Now: func() time.Time { return now.Add(3 * 24 * time.Hour) }}
	busy, err := e.busyTargets(ctx, p.ID, e.Now())
	if err != nil || !busy["endpoint|nginx:shop POST /scanner"] {
		t.Fatalf("busy %v err %v", busy, err)
	}
	e.Now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	if busy, _ := e.busyTargets(ctx, p.ID, e.Now()); busy["endpoint|nginx:shop POST /scanner"] {
		t.Fatal("still quiet after the days asked for")
	}

	if err := Dismiss(ctx, db, p.ID, id, "", 7, now); err != ErrClosed {
		t.Fatalf("a closed mission was dismissed again: %v", err)
	}
}
