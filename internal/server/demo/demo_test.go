package demo

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func TestDemoTellsTheWholeStory(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	db := store.OpenTest(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 16, 11, 30, 0, 0, time.UTC)

	p, err := Seed(ctx, db, Options{Days: 21, Now: now, Name: "Demo Shop"})
	if err != nil {
		t.Fatal(err)
	}
	count := func(q string, args ...any) int {
		var n int
		if err := db.Q().Row(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	checks := map[string]int{
		"closed score days":  count("SELECT COUNT(*) FROM score_days WHERE project_id = ? AND closed", p.ID),
		"completed missions": count("SELECT COUNT(*) FROM missions WHERE project_id = ? AND status = 'completed'", p.ID),
		"bosses":             count("SELECT COUNT(*) FROM missions WHERE project_id = ? AND type = 'boss'", p.ID),
		"badges":             count("SELECT COUNT(*) FROM unlocked_badges WHERE project_id = ?", p.ID),
		"deploy verdicts":    count("SELECT COUNT(*) FROM deploys WHERE project_id = ? AND impact IS NOT NULL", p.ID),
		"points":             count("SELECT COALESCE(SUM(points),0) FROM point_entries WHERE project_id = ?", p.ID),
		"explained queries":  count("SELECT COUNT(*) FROM query_fingerprints WHERE project_id = ? AND explain IS NOT NULL", p.ID),
		"custom metrics":     count("SELECT COUNT(DISTINCT name) FROM metric_minutes WHERE project_id = ?", p.ID),
		"command overlaps":   count("SELECT COUNT(*) FROM activities WHERE project_id = ? AND type = 'command_overlap'", p.ID),
	}
	for what, n := range checks {
		if n == 0 {
			t.Errorf("demo has no %s", what)
		}
	}
	if checks["closed score days"] < 19 {
		t.Errorf("closed days %d", checks["closed score days"])
	}

	// The story: the index gets better over time.
	var first, last float64
	_ = db.Q().Row(ctx, "SELECT score FROM score_days WHERE project_id = ? AND closed ORDER BY day LIMIT 1", p.ID).Scan(&first)
	_ = db.Q().Row(ctx, "SELECT score FROM score_days WHERE project_id = ? AND closed ORDER BY day DESC LIMIT 1", p.ID).Scan(&last)
	if last <= first {
		t.Errorf("the index should improve: first %.1f last %.1f", first, last)
	}

	// Seeding again replaces the demo project instead of duplicating it.
	if _, err := Seed(ctx, db, Options{Days: 2, Now: now, Name: "Demo Shop"}); err != nil {
		t.Fatal(err)
	}
	list, _ := project.Repo{DB: db}.List(ctx)
	if len(list) != 1 {
		t.Fatalf("projects %d", len(list))
	}
}

func TestLiveMinuteProducesEveryMachine(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Demo Shop", "Europe/Rome")
	payloads := LiveMinute(p, time.Date(2026, 9, 16, 15, 0, 0, 0, time.UTC), rand.New(rand.NewSource(1)))
	hosts := map[string]bool{}
	for _, pl := range payloads {
		hosts[pl.Agent.Host] = true
		if pl.Network == nil || len(pl.Host) != 1 {
			t.Fatalf("%s misses network or host sample", pl.Agent.Host)
		}
	}
	if len(hosts) != len(demoMachines()) {
		t.Fatalf("payloads %+v", hosts)
	}
}
