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

// Excluding "GET /buy-sell-contract/" left "POST /buy-sell-contract/" open as a regression on
// a real install: the same path under another method is what the dialog must offer next.
func TestSamePathTellsTheOtherMethodsOfAnEndpoint(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"GET /buy-sell-contract/", "POST /buy-sell-contract/", true},
		{"GET /orders/{order}", "DELETE /orders/:id", true},
		{"GET /orders/{id}", "GET /orders/{id}", false}, // the same endpoint, not another one
		{"GET /orders", "GET /orders/{id}", false},
		{"GET /", "POST /scanner", false},
		{"GET", "POST", false},
	}
	for _, c := range cases {
		if got := SamePath(c.a, c.b); got != c.want {
			t.Errorf("SamePath(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// A php-fpm stall whose deepest frame is an outbound call is time spent waiting on somebody
// else's API: in a real install "curl_exec() vendor/guzzlehttp/..." stayed a regression after every
// endpoint calling that API had been left out.
func TestOutboundFrameTellsAWaitOnAnotherService(t *testing.T) {
	yes := []string{
		"curl_exec() vendor/guzzlehttp/guzzle/src/Handler/CurlHandler.php:85",
		"curl_multi_exec() vendor/guzzlehttp/guzzle/src/Handler/CurlMultiHandler.php:159",
		"curl_multi_select() vendor/guzzlehttp/guzzle/src/Handler/CurlMultiHandler.php:180",
		"SoapClient->__doRequest() app/Services/Ade.php:40",
		"stream_socket_client() vendor/symfony/http-client/Response/NativeResponse.php:200",
		"fsockopen() app/Legacy/Http.php:12",
	}
	no := []string{
		"PDOStatement->execute() vendor/laravel/framework/src/Illuminate/Database/Connection.php:421",
		"sleep() app/Jobs/Wait.php:3",
		"(empty stack)",
		"",
	}
	for _, f := range yes {
		if !OutboundFrame(f) {
			t.Errorf("%q is a wait on another service", f)
		}
	}
	for _, f := range no {
		if OutboundFrame(f) {
			t.Errorf("%q is not a wait on another service", f)
		}
	}
}
