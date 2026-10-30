package missionctx

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/doctor"
	"github.com/getslowpoke/slowpoke/internal/server/ingest"
	"github.com/getslowpoke/slowpoke/internal/server/mute"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func setup(t *testing.T) (*store.DB, project.Project, context.Context, time.Time) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	return db, p, ctx, time.Now().Truncate(time.Hour)
}

func TestQueryMissionContext(t *testing.T) {
	db, p, ctx, now := setup(t)
	sql := "select * from orders where customer_id = '42' and status = 'paid'"
	fp := fingerprint.Of(sql)
	hash := fingerprint.Hash(fp)
	svc := ingest.New(db)
	svc.Now = func() time.Time { return now }
	for i := 1; i <= 3; i++ {
		minute := now.Add(-time.Duration(i) * time.Hour).Unix()
		_, err := svc.Ingest(ctx, p, protocol.Payload{
			BatchID: "b" + string(rune('0'+i)), Agent: protocol.AgentInfo{Host: "db-1", Kind: "agent", Sources: []string{"mysql:db"}},
			DB: []protocol.DBBucket{{Source: "mysql-digest", Minute: minute, Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, Sample: fingerprint.Redact(sql), N: 100, Sum: float64(100 * i), Max: 2, Rows: 480000}}}},
			App: []protocol.AppBucket{{Minute: minute,
				Routes:  map[string]protocol.RouteStat{"GET /account/orders": {N: 10, Sum: 20, Hashes: map[string]protocol.RouteQuery{hash: {N: 100, Sum: 5, Requests: 10}}}},
				Queries: map[string]protocol.AppQuery{hash: {Fingerprint: fp, Origin: "app/Http/Controllers/OrderController.php:42", N: 100, Sum: 5, NPlusOne: 10}},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	db.Q().Exec(ctx, `UPDATE query_fingerprints SET explain = ? WHERE hash = ?`,
		`{"engine":"mysql","plan":[{"table":"orders","type":"ALL","rows":480000}],"tables":{"orders":{"rows":480000,"columns":[{"name":"customer_id","type":"bigint"},{"name":"status","type":"varchar(20)"}],"indexes":[{"name":"PRIMARY","columns":["id"]}]}}}`, hash)
	id, _ := db.Q().InsertID(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, current, points, status, good_windows, person, opened_at, checked_at)
		VALUES (?, 'mission', 'query', ?, ?, 300, 280, 120, 'open', 0, '', ?, ?)`, p.ID, hash, fp, now.Add(-4*time.Hour).Unix(), now.Unix())

	c, err := Build(ctx, db, p, id, Options{PublicURL: "https://slowpoke.example.com", Now: now, Windows: 6})
	if err != nil {
		t.Fatal(err)
	}
	if c.Query == nil || c.Query.Origin != "app/Http/Controllers/OrderController.php:42" || len(c.Query.Diagnoses) == 0 || len(c.Query.Routes) != 1 {
		t.Fatalf("query context %+v", c.Query)
	}
	if len(c.History) != 6 {
		t.Fatalf("history %+v", c.History)
	}
	md := c.Markdown()
	for _, want := range []string{
		"Slow query", "OrderController.php:42", "ALTER TABLE `orders` ADD INDEX", "GET /account/orders", "N+1",
		"https://slowpoke.example.com/projects/shop/missions/", "customer_id = 's'", "Definition of done",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown misses %q\n%s", want, md)
		}
	}
	if strings.Contains(md, "'42'") {
		t.Fatal("real values must never appear")
	}
}

func TestEndpointMissionContextListsItsQueries(t *testing.T) {
	db, p, ctx, now := setup(t)
	fp := "select * from products where category_id = ?"
	hash := fingerprint.Hash(fp)
	svc := ingest.New(db)
	svc.Now = func() time.Time { return now }
	_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "x", Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent"},
		HTTP: []protocol.HTTPBucket{{Source: "nginx:app", Minute: now.Add(-time.Hour).Unix(), N: 10, Hist: make([]int, 15),
			Endpoints: map[string]protocol.EndpointStat{"GET /products/{id}": {N: 10, Sum: 30, Max: 5, Slow: 8, SlowSum: 28}}}},
		App: []protocol.AppBucket{{Minute: now.Add(-time.Hour).Unix(),
			Routes:  map[string]protocol.RouteStat{"GET /products/{product}": {N: 10, Hashes: map[string]protocol.RouteQuery{hash: {N: 400, Sum: 20, Requests: 10}}}},
			Queries: map[string]protocol.AppQuery{hash: {Fingerprint: fp, N: 400, Sum: 20, NPlusOne: 10, Origin: "app/Http/Controllers/ProductController.php:21"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := db.Q().InsertID(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, points, status, good_windows, person, opened_at, checked_at)
		VALUES (?, 'boss', 'endpoint', 'nginx:app GET /products/{id}', 'GET /products/{id}', 28, 60, 'open', 0, 'ada', ?, ?)`, p.ID, now.Add(-2*time.Hour).Unix(), now.Unix())

	c, err := Build(ctx, db, p, id, Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if c.Endpoint == nil || len(c.Endpoint.Queries) != 1 || c.Endpoint.Queries[0].PerRequest != 40 {
		t.Fatalf("endpoint %+v", c.Endpoint)
	}
	if md := c.Markdown(); !strings.Contains(md, "40 per request") || !strings.Contains(md, "Regression") {
		t.Fatalf("markdown\n%s", md)
	}
	if _, err := Build(ctx, db, p, 999999, Options{}); err != ErrNotFound {
		t.Fatalf("unknown mission: %v", err)
	}
}

// When an endpoint is slow because it waits on another service, the brief the assistant reads
// says so, names the host and the line that calls it, and points at the usual repairs instead
// of the database.
func TestEndpointMissionContextSaysWhatItWaitsOn(t *testing.T) {
	db, p, ctx, now := setup(t)
	svc := ingest.New(db)
	svc.Now = func() time.Time { return now }
	_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "x", Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent"},
		HTTP: []protocol.HTTPBucket{{Source: "nginx:app", Minute: now.Add(-time.Hour).Unix(), N: 10, Hist: make([]int, 15),
			Endpoints: map[string]protocol.EndpointStat{"POST /checkout": {N: 10, Sum: 80, Max: 9, Slow: 10, SlowSum: 80}}}},
		App: []protocol.AppBucket{{Minute: now.Add(-time.Hour).Unix(), Routes: map[string]protocol.RouteStat{"POST /checkout": {N: 10, Sum: 80, ExtSum: 60, ExtRequests: 10,
			Ext: map[string]protocol.ExtCall{"api.stripe.com": {N: 10, Sum: 60, Max: 7, Errors: 1, Requests: 10, Origin: "app/Services/Stripe.php:88"}}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := db.Q().InsertID(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, points, status, good_windows, person, opened_at, checked_at)
		VALUES (?, 'mission', 'endpoint', 'nginx:app POST /checkout', 'POST /checkout', 80, 60, 'open', 0, '', ?, ?)`, p.ID, now.Add(-2*time.Hour).Unix(), now.Unix())
	c, err := Build(ctx, db, p, id, Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if c.Endpoint == nil || c.Endpoint.Outbound == nil || c.Endpoint.Outbound.Share != 0.75 {
		t.Fatalf("endpoint %+v", c.Endpoint)
	}
	md := c.Markdown()
	for _, want := range []string{"api.stripe.com", "app/Services/Stripe.php:88", "75%", "timeout"} {
		if !strings.Contains(md, want) {
			t.Fatalf("%q missing from\n%s", want, md)
		}
	}
}

// The brief tells the assistant what the team decided not to do, so it does not propose it.
func TestTheBriefNamesTheAdviceTheTeamMuted(t *testing.T) {
	c := Context{Query: &Query{Fingerprint: "SELECT * FROM orders", Muted: []mute.Muted{{
		Diagnosis: doctor.Diagnosis{Code: "full_scan", Title: "Full table scan on orders"},
		Mute:      mute.Diagnosis{Note: "vendor schema"}}}}}
	md := c.Markdown()
	if !strings.Contains(md, "Muted by the team") || !strings.Contains(md, "Full table scan on orders (vendor schema)") {
		t.Fatalf("brief:\n%s", md)
	}
}

// A list of 50 missions on a production project took 17 s: every mission without an effort re-read the
// costs of the whole project for the last week to diagnose one query. Queries reads them once for the list.
func TestQueriesReadTheProjectCostsOnceForAList(t *testing.T) {
	db, p, ctx, now := setup(t)
	svc := ingest.New(db)
	svc.Now = func() time.Time { return now }
	slow := func(batch, sql string, sum float64) string {
		fp := fingerprint.Of(sql)
		hash := fingerprint.Hash(fp)
		_, err := svc.Ingest(ctx, p, protocol.Payload{
			BatchID: batch, Agent: protocol.AgentInfo{Host: "db-1", Kind: "agent", Sources: []string{"mysql:db"}},
			DB: []protocol.DBBucket{{Source: "mysql-digest", Minute: now.Add(-time.Hour).Unix(), Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, Sample: fingerprint.Redact(sql), N: 100, Sum: sum, Max: 2, Rows: 480000}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return hash
	}
	a := slow("a", "select * from orders where customer_id = 42", 300)
	b := slow("b", "select * from invoices where customer_id = 42", 200)

	qs := NewQueries(db, p, now)
	for _, hash := range []string{a, b} {
		got, err := qs.Load(ctx, hash)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := LoadQuery(ctx, db, p, hash, now)
		if got.Stats != want.Stats || len(got.Diagnoses) != len(want.Diagnoses) || got.Fingerprint != want.Fingerprint {
			t.Fatalf("%s: loader %+v, alone %+v", hash, got, want)
		}
	}

	// Once read, the costs are not read again: what arrives later does not change the list being built.
	slow("a2", "select * from orders where customer_id = 42", 900)
	again, _ := qs.Load(ctx, a)
	fresh, _ := LoadQuery(ctx, db, p, a, now)
	if again.Stats == fresh.Stats {
		t.Fatalf("the loader read the costs again: %+v", again.Stats)
	}
	if _, err := qs.Load(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("unknown hash: %v", err)
	}
}
