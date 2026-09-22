package exclude

import (
	"context"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

var now = time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*store.DB, project.Project, context.Context) {
	t.Helper()
	db := store.OpenTest(t)
	ctx := context.Background()
	p, err := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	return db, p, ctx
}

func TestAnEndpointIsExcludedWithAReasonUntilItExpires(t *testing.T) {
	db, p, ctx := setup(t)
	until := now.Add(30 * 24 * time.Hour).Unix()
	if _, err := Add(ctx, db, p.ID, Exclusion{Kind: KindEndpoint, Target: "POST /checkout", Reason: "external", Note: "Stripe", CreatedBy: "ada@example.com", Until: &until}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(ctx, db, p.ID, Exclusion{Kind: KindEndpoint, Target: "GET /export", Reason: "nonsense"}, now); err == nil {
		t.Fatal("an unknown reason was accepted")
	}
	if _, err := Add(ctx, db, p.ID, Exclusion{Kind: KindEndpoint, Target: " ", Reason: "export"}, now); err == nil {
		t.Fatal("an empty endpoint was accepted")
	}

	set, err := Active(ctx, db, p.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	// The same endpoint seen by nginx and by the framework, whatever the placeholder is called.
	for _, target := range []string{"nginx:shop POST /checkout", "otlp:shop POST /checkout"} {
		if !set.Target(target) {
			t.Errorf("%s is not excluded", target)
		}
	}
	if set.Target("nginx:shop GET /checkout") || set.Target("nginx:shop POST /checkout/{id}") {
		t.Error("another endpoint was excluded")
	}
	later, _ := Active(ctx, db, p.ID, now.Add(31*24*time.Hour))
	if later.Target("nginx:shop POST /checkout") {
		t.Error("the exclusion did not expire")
	}

	// Excluding it again updates the reason instead of failing.
	if _, err := Add(ctx, db, p.ID, Exclusion{Kind: KindEndpoint, Target: "POST /checkout", Reason: "streaming"}, now); err != nil {
		t.Fatal(err)
	}
	list, err := List(ctx, db, p.ID)
	if err != nil || len(list) != 1 || list[0].Reason != "streaming" || list[0].Until != nil {
		t.Fatalf("list %+v err %v", list, err)
	}
	if err := Remove(ctx, db, p.ID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if set, _ := Active(ctx, db, p.ID, now); set.Target("nginx:shop POST /checkout") {
		t.Error("still excluded after removal")
	}
}

func TestPlaceholdersOfDifferentFrameworksAreTheSameEndpoint(t *testing.T) {
	db, p, ctx := setup(t)
	if _, err := Add(ctx, db, p.ID, Exclusion{Kind: KindEndpoint, Target: "GET /orders/{order}/pdf", Reason: "export"}, now); err != nil {
		t.Fatal(err)
	}
	set, _ := Active(ctx, db, p.ID, now)
	if !set.Endpoint("GET /orders/{id}/pdf") || !set.Endpoint("GET /orders/:id/pdf") {
		t.Error("the same route with another placeholder name is not excluded")
	}
	if set.Endpoint("GET /orders/latest/pdf") {
		t.Error("a literal segment matched a placeholder")
	}
}

// Excluding an endpoint must not look like repaying it: its open missions and bosses close as
// excluded, with no points, and the missions of other things stay open.
func TestExcludingClosesItsMissionsWithoutPoints(t *testing.T) {
	db, p, ctx := setup(t)
	insert := func(typ, kind, target string) {
		_, err := db.Q().Exec(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, points, status, opened_at, checked_at)
			VALUES (?, ?, ?, ?, ?, 100, 50, 'open', ?, ?)`, p.ID, typ, kind, target, target, now.Unix(), now.Unix())
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("mission", "endpoint", "nginx:shop POST /checkout")
	insert("boss", "endpoint", "otlp:shop POST /checkout")
	insert("mission", "endpoint", "nginx:shop GET /orders")
	insert("mission", "query", "abc123")

	closed, err := Add(ctx, db, p.ID, Exclusion{Kind: KindEndpoint, Target: "POST /checkout", Reason: "external"}, now)
	if err != nil || closed != 2 {
		t.Fatalf("closed %d err %v", closed, err)
	}
	var excluded, open, points int
	_ = db.Q().Row(ctx, "SELECT COUNT(*) FROM missions WHERE status = 'excluded' AND closed_at = ?", now.Unix()).Scan(&excluded)
	_ = db.Q().Row(ctx, "SELECT COUNT(*) FROM missions WHERE status = 'open'").Scan(&open)
	_ = db.Q().Row(ctx, "SELECT COUNT(*) FROM point_entries").Scan(&points)
	if excluded != 2 || open != 2 || points != 0 {
		t.Fatalf("excluded %d open %d points %d", excluded, open, points)
	}
}

// Not only endpoints: a query of a library nobody can change and a PHP frame inside a vendor's
// code can be left out too, each one on its own.
func TestQueriesAndPHPFramesCanBeLeftOutToo(t *testing.T) {
	db, p, ctx := setup(t)
	for _, e := range []Exclusion{
		{Kind: KindQuery, Target: "6d68288d48f736a9", Reason: "third_party", Note: "vendor migration"},
		{Kind: KindPHP, Target: "curl_exec() vendor/guzzlehttp/guzzle/src/Handler/CurlHandler.php:85", Reason: "external"},
	} {
		if _, err := Add(ctx, db, p.ID, e, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Add(ctx, db, p.ID, Exclusion{Kind: "nonsense", Target: "x", Reason: "other"}, now); err == nil {
		t.Fatal("an unknown kind was accepted")
	}

	set, err := Active(ctx, db, p.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Cost(KindQuery, "6d68288d48f736a9") || !set.Cost(KindPHP, "curl_exec() vendor/guzzlehttp/guzzle/src/Handler/CurlHandler.php:85") {
		t.Fatalf("not excluded: %v", set)
	}
	// A hash is not an endpoint and an endpoint is not a hash: the kinds do not mix.
	if set.Cost(KindEndpoint, "nginx:shop 6d68288d48f736a9") || set.Cost(KindQuery, "aaaabbbbccccdddd") {
		t.Fatal("the kinds mixed")
	}
	if list, _ := List(ctx, db, p.ID); len(list) != 2 {
		t.Fatalf("list %v", list)
	}
}

// Excluding a query closes the missions and bosses of that query only.
func TestExcludingAQueryClosesItsOwnMissions(t *testing.T) {
	db, p, ctx := setup(t)
	insert := func(typ, kind, target string) {
		_, err := db.Q().Exec(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, points, status, opened_at, checked_at)
			VALUES (?, ?, ?, ?, ?, 100, 50, 'open', ?, ?)`, p.ID, typ, kind, target, target, now.Unix(), now.Unix())
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("boss", "query", "6d68288d48f736a9")
	insert("mission", "query", "aaaabbbbccccdddd")
	insert("mission", "endpoint", "nginx:shop POST /checkout")

	closed, err := Add(ctx, db, p.ID, Exclusion{Kind: KindQuery, Target: "6d68288d48f736a9", Reason: "third_party"}, now)
	if err != nil || closed != 1 {
		t.Fatalf("closed %d err %v", closed, err)
	}
	var open int
	_ = db.Q().Row(ctx, "SELECT COUNT(*) FROM missions WHERE status = 'open'").Scan(&open)
	if open != 2 {
		t.Fatalf("%d missions left open", open)
	}
}
