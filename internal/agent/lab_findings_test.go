package agent

import (
	"fmt"
	"strings"
	"testing"
	"time"

	trace "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// Regressions found by running four real apps (Laravel, FastAPI, Express, Go) against the agent.

func serverSpan(traceID, id string, start time.Time, route string) span {
	return span{traceID: traceID, spanID: id, name: "GET " + route, kind: int32(trace.Span_SPAN_KIND_SERVER), start: start, end: start.Add(200 * time.Millisecond),
		service: "shop", attrs: map[string]string{"http.request.method": "GET", "http.route": route, "http.response.status_code": "200"}}
}

func dbSpan(traceID, id, parent string, start time.Time, attrs map[string]string) span {
	return span{traceID: traceID, spanID: id, parentID: parent, name: "SELECT", kind: int32(trace.Span_SPAN_KIND_CLIENT), start: start, end: start.Add(10 * time.Millisecond),
		service: "shop", attrs: attrs}
}

func appQueries(t *testing.T, d Drained) map[string]protocol.AppQuery {
	t.Helper()
	out := map[string]protocol.AppQuery{}
	for _, b := range d.App {
		for _, q := range b.Queries {
			out[q.Fingerprint] = q
		}
	}
	return out
}

// SQLAlchemy and psycopg2 are both instrumented by opentelemetry-bootstrap: every
// statement arrives as a SQLAlchemy span with a psycopg2 child carrying the same text.
func TestNestedDatabaseSpansCountOnce(t *testing.T) {
	a := NewAggregator()
	start := time.Now().Add(-2 * time.Minute)
	stmt := map[string]string{"db.system": "postgresql", "db.statement": "SELECT name FROM customers WHERE id = %(id)s"}
	spans := []span{serverSpan("t1", "s", start, "/orders")}
	for i := 0; i < 6; i++ {
		outer := fmt.Sprintf("sqla%d", i)
		spans = append(spans, dbSpan("t1", outer, "s", start, stmt), dbSpan("t1", "pg"+outer, outer, start, stmt))
	}
	a.addTrace(spans, "")
	d := a.Drain(time.Now())

	q, ok := appQueries(t, d)["select name from customers where id = ?"]
	if !ok || q.N != 6 || q.NPlusOne != 1 {
		t.Fatalf("queries %+v", appQueries(t, d))
	}
	if r := d.App[0].Routes["GET /orders"]; r.Queries != 6 {
		t.Fatalf("route counts %d queries, want 6", r.Queries)
	}
}

// Laravel puts code.file.path on the Eloquent span (the parent of the SQL span) and it points
// into vendor/. The origin must come from an ancestor and never be a framework file.
func TestQueryOriginFromAncestorsSkipsVendor(t *testing.T) {
	start := time.Now().Add(-2 * time.Minute)
	sql := map[string]string{"db.system.name": "mysql", "db.query.text": "select * from `orders` where `id` = ?"}
	cases := []struct {
		name, file, want string
	}{
		{"application file on the parent", "/var/www/app/app/Http/Controllers/OrderController.php", "app/Http/Controllers/OrderController.php:31"},
		{"framework file", "/var/www/app/vendor/laravel/framework/src/Illuminate/Database/Eloquent/Builder.php", ""},
		{"node_modules", "/app/node_modules/sequelize/lib/model.js", ""},
		{"site-packages", "/usr/local/lib/python3.13/site-packages/django/db/models/query.py", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := NewAggregator()
			eloquent := span{traceID: "t", spanID: "e", parentID: "s", name: "Order::get", start: start, end: start.Add(time.Millisecond),
				attrs: map[string]string{"code.file.path": c.file, "code.line.number": "31"}}
			a.addTrace([]span{serverSpan("t", "s", start, "orders/{id}"), eloquent, dbSpan("t", "q", "e", start, sql)}, "/var/www/app/")
			q := appQueries(t, a.Drain(time.Now()))["select * from orders where id = ?"]
			if q.Origin != c.want {
				t.Fatalf("origin %q, want %q", q.Origin, c.want)
			}
		})
	}
}

// Drivers and connection pools run session and transaction statements on every request:
// they cost nothing and bury the real queries. The agent's own monitoring queries too.
func TestHousekeepingStatementsAreIgnored(t *testing.T) {
	noise := []string{
		"SET NAMES utf8mb4 COLLATE utf8mb4_unicode_ci, SESSION sql_mode='NO_ENGINE_SUBSTITUTION'",
		"use shop", "BEGIN", "COMMIT", "ROLLBACK", "START TRANSACTION", "SAVEPOINT a", "select pg_catalog.version()",
		"SHOW TRANSACTION ISOLATION LEVEL", "select current_schema()", "SELECT @@session.transaction_isolation", "DISCARD ALL",
		"SELECT COALESCE(SUM(xact_commit + xact_rollback),0) FROM pg_stat_database",
		"SELECT * FROM performance_schema.events_statements_summary_by_digest",
		// The agent's own EXPLAIN and table descriptions, and pgx loading types on connect.
		"EXPLAIN select * from orders where status = 'paid'", "explain (format json) select 1 from t",
		"select t.oid, typarray from pg_type t join pg_namespace n on n.oid = t.typnamespace where t.typtype = 'b'",
		"SELECT COALESCE(TABLE_ROWS,0) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?",
		"SELECT indexname, indexdef FROM pg_indexes WHERE tablename = $1",
		// The MariaDB image loads time zones on first start (mariadb-tzinfo-to-sql).
		"execute immediate if(@wsrep_cannot_replicate_tz, 'ALTER TABLE time_zone ENGINE=InnoDB', 'do 0')",
		"ALTER TABLE time_zone_transition ORDER BY Time_zone_id, Transition_time",
	}
	for _, sql := range noise {
		if !ignoredStatement(sql) {
			t.Errorf("not ignored: %s", sql)
		}
	}
	for _, sql := range []string{"select * from settings", "UPDATE users SET name = ? WHERE id = ?", "select * from sessions where id = ?", "SELECT beginning FROM t", "INSERT INTO commits VALUES (1)", "select * from explanations", "select * from pg_types_custom"} {
		if ignoredStatement(sql) {
			t.Errorf("real query ignored: %s", sql)
		}
	}

	a := NewAggregator()
	start := time.Now().Add(-2 * time.Minute)
	a.addTrace([]span{serverSpan("t", "s", start, "/"), dbSpan("t", "b", "s", start, map[string]string{"db.statement": "BEGIN"}),
		dbSpan("t", "q", "s", start, map[string]string{"db.statement": "select 1 from t"})}, "")
	a.AddSlowQuery("mysql-slowlog", SlowQuery{Time: start, SQL: "SET timestamp=1", Seconds: 2})
	d := a.Drain(time.Now())
	if qs := appQueries(t, d); len(qs) != 1 || d.App[0].Routes["GET /"].Queries != 1 {
		t.Fatalf("queries %+v routes %+v", qs, d.App[0].Routes)
	}
	for _, b := range d.DB {
		if len(b.Queries) != 0 {
			t.Fatalf("slow log noise kept: %+v", b.Queries)
		}
	}
}

// With nginx in front of Laravel the same request is seen twice. The web server line wins;
// a Go API on its own port keeps its traces, and so does a Python app exposing the very same
// paths as Laravel with different timings.
func TestRequestsSeenByWebServerAndTracesCountOnce(t *testing.T) {
	a := NewAggregator()
	start := time.Now().Add(-4 * time.Minute).Truncate(time.Minute)
	for i := 0; i < 30; i++ {
		end := start.Add(time.Duration(i)*time.Second + 400*time.Millisecond)
		path := fmt.Sprintf("/orders/%d", 1000+i)
		a.AddRequest("nginx:nginx", end.Truncate(time.Second), "GET", path, 200, 0.12, 1)
		a.AddRequest("otlp:laravel-shop", end.Add(-2*time.Millisecond), "GET", "/orders/{order}", 200, 0.11, 1)
		a.AddRequest("otlp:fastapi-shop", end.Add(-300*time.Millisecond), "GET", "/orders/{order_id}", 200, 0.01, 1)
		a.AddRequest("otlp:go-shop", end, "GET", "/stats", 200, 0.05, 1)
	}
	sources := map[string]int{}
	for _, b := range a.Drain(time.Now()).HTTP {
		sources[b.Source] += b.N
	}
	if sources["nginx:nginx"] != 30 || sources["otlp:go-shop"] != 30 || sources["otlp:fastapi-shop"] != 30 || sources["otlp:laravel-shop"] != 0 {
		t.Fatalf("requests per source %v", sources)
	}

	// Once recognised, the service stays behind the web server even in quiet minutes.
	later := time.Now().Add(-2 * time.Minute).Truncate(time.Minute)
	a.AddRequest("otlp:laravel-shop", later, "GET", "/checkout", 200, 1.2, 1)
	a.AddRequest("nginx:nginx", later, "GET", "/checkout", 200, 1.21, 1)
	for _, b := range a.Drain(time.Now()).HTTP {
		if strings.HasPrefix(b.Source, "otlp:laravel") {
			t.Fatalf("duplicate kept: %+v", b)
		}
	}
}

// A quiet service is held back while it cannot be recognised, then sent: never lost.
func TestQuietTracedServiceIsSentAfterTimeout(t *testing.T) {
	a := NewAggregator()
	at := time.Now().Add(-3 * time.Minute)
	a.AddRequest("nginx:nginx", at, "GET", "/", 200, 0.02, 1)
	a.AddRequest("otlp:admin", at, "GET", "/reports", 200, 0.3, 1)
	for _, b := range a.Drain(time.Now()).HTTP {
		if b.Source == "otlp:admin" {
			t.Fatalf("sent before the service could be recognised")
		}
	}
	a.web.firstSeen["otlp:admin"] = time.Now().Add(-webDecisionTimeout)
	sent := false
	for _, b := range a.Drain(time.Now()).HTTP {
		sent = sent || b.Source == "otlp:admin"
	}
	if !sent {
		t.Fatal("quiet service lost")
	}
}

// At start the agent tails the access log from its end, while traces of the same seconds do
// arrive: those first requests cannot pair and must not decide that the service is on its own.
func TestWebServerRecognitionIgnoresRequestsBeforeTheLogIsRead(t *testing.T) {
	a := NewAggregator()
	start := time.Now().Add(-6 * time.Minute).Truncate(time.Minute)
	for i := 0; i < 25; i++ { // before the first access log line was read
		a.AddRequest("otlp:laravel-shop", start.Add(time.Duration(i)*time.Second), "GET", "/orders", 200, 0.1, 1)
	}
	_ = a.Drain(start.Add(40 * time.Second))
	for i := 30; i < 60; i++ {
		end := start.Add(time.Duration(i)*time.Second + 300*time.Millisecond)
		a.AddRequest("nginx:nginx", end.Truncate(time.Second), "GET", "/orders", 200, 0.11, 1)
		a.AddRequest("otlp:laravel-shop", end, "GET", "/orders", 200, 0.1, 1)
	}
	sources := map[string]int{}
	for _, b := range a.Drain(time.Now()).HTTP {
		sources[b.Source] += b.N
	}
	if sources["otlp:laravel-shop"] != 0 || sources["nginx:nginx"] != 30 {
		t.Fatalf("requests per source %v", sources)
	}
}

// A "not behind" verdict is checked again later: nginx may be put in front of the app.
func TestIndependentServiceIsRecheckedLater(t *testing.T) {
	w := webDedupe{}
	base := time.Now().Add(-30 * time.Minute)
	w.observe("nginx:nginx", "GET /", 200, base, 0.01)
	for i := 0; i < 25; i++ {
		w.observe("otlp:api", "GET /v2/items", 200, base.Add(time.Duration(i+5)*time.Second), 0.2)
	}
	w.decide(base.Add(time.Minute))
	if behind, decided := w.behind["otlp:api"]; !decided || behind {
		t.Fatalf("first verdict %v %v", behind, decided)
	}
	later := base.Add(15 * time.Minute)
	for i := 0; i < 25; i++ {
		end := later.Add(time.Duration(i)*time.Second + 500*time.Millisecond)
		w.observe("nginx:nginx", "GET /v2/items", 200, end.Truncate(time.Second), 0.21)
		w.observe("otlp:api", "GET /v2/items", 200, end, 0.2)
	}
	w.decide(later.Add(time.Minute))
	if !w.behind["otlp:api"] {
		t.Fatal("not recognised after nginx was put in front")
	}
}

// The lab VPS runs MariaDB and PostgreSQL side by side: every EXPLAIN went to the first
// configured database, so a PostgreSQL statement was explained by MariaDB (and failed).
func TestExplainGoesToTheDatabaseThatSawTheStatement(t *testing.T) {
	mariadb := &dbSource{src: Source{Type: "mysql", Name: "mariadb"}, engine: "mysql", id: "0"}
	pg := &dbSource{src: Source{Type: "postgres", Name: "pg"}, engine: "postgres", id: "1"}
	r := &Runner{cfg: &Config{}, agg: NewAggregator(), dbs: []*dbSource{mariadb, pg}}

	pgStats := map[string]protocol.QueryStat{hashOf("select * from orders where id = 7"): {Fingerprint: "select * from orders where id = ?", Sample: "select * from orders where id = 7", N: 1, Sum: 2, Max: 2}}
	r.agg.AddQueryStats("pg-statements", pg.sampleOwner(), t0, pgStats, 0, 0, 0)
	r.agg.AddSlowQuery("mysql-slowlog", SlowQuery{Time: t0, Seconds: 3, SQL: "select * from carts where user_id = 9"})

	for sql, want := range map[string]*dbSource{"select * from orders where id = 7": pg, "select * from carts where user_id = 9": mariadb} {
		hash := hashOf(sql)
		_, got, err := r.explainTarget(protocol.ExplainRequest{Hash: hash})
		if err != "" || got != want {
			t.Errorf("%s: got %v (%q), want %s", sql, got, err, want.src.Name)
		}
	}

	// A slow-log statement with no MySQL source to run on must not be sent to PostgreSQL.
	only := &Runner{cfg: &Config{}, agg: NewAggregator(), dbs: []*dbSource{pg}}
	only.agg.AddSlowQuery("mysql-slowlog", SlowQuery{Time: t0, Seconds: 3, SQL: "select * from carts where user_id = 9"})
	if _, got, err := only.explainTarget(protocol.ExplainRequest{Hash: hashOf("select * from carts where user_id = 9")}); got != nil || err == "" {
		t.Fatalf("mysql statement routed to %v", got)
	}
}

func hashOf(sql string) string { return fingerprint.Hash(fingerprint.Of(sql)) }

// Found in production: half the traffic of an application goes to /api/v3/user/me/services,
// whose Laravel route is /api/v3/user/{id}/services. nginx can only turn numbers, uuids and
// hashes into a placeholder, so "me" stayed a word, the two lines never paired, the service was
// taken for one running on its own and every request of that application was counted twice - in
// the traffic, in the speed index and in the debt - for weeks.
func TestRouteParameterSpelledAsAWordCountsOnce(t *testing.T) {
	a := NewAggregator()
	start := time.Now().Add(-4 * time.Minute).Truncate(time.Minute)
	for i := 0; i < 30; i++ {
		end := start.Add(time.Duration(i)*time.Second + 400*time.Millisecond)
		// The endpoint that never paired: a word where the route has a parameter.
		a.AddRequest("nginx:app.example.com", end, "GET", "/api/v3/user/me/services", 200, 0.09, 1)
		a.AddRequest("otlp:Shop", end.Add(-3*time.Millisecond), "GET", "/api/v3/user/{id}/services", 200, 0.086, 1)
		// And one that did pair, so the mix is the real application's.
		a.AddRequest("nginx:app.example.com", end, "GET", fmt.Sprintf("/api/v3/practices/%d", 4000+i), 200, 0.3, 1)
		a.AddRequest("otlp:Shop", end.Add(-2*time.Millisecond), "GET", "/api/v3/practices/{practice}", 200, 0.29, 1)
	}
	sources := map[string]int{}
	for _, b := range a.Drain(time.Now()).HTTP {
		sources[b.Source] += b.N
	}
	if sources["nginx:app.example.com"] != 60 || sources["otlp:Shop"] != 0 {
		t.Fatalf("requests per source %v: the traced ones are the same requests nginx logged", sources)
	}
}

// A placeholder standing for any word must not pair two different applications: what keeps them
// apart is the time they take, not the shape of their paths.
func TestTwoApplicationsWithTheSamePathsStaySeparate(t *testing.T) {
	a := NewAggregator()
	start := time.Now().Add(-4 * time.Minute).Truncate(time.Minute)
	for i := 0; i < 30; i++ {
		end := start.Add(time.Duration(i)*time.Second + 400*time.Millisecond)
		a.AddRequest("nginx:shop", end, "GET", "/api/v3/user/me/services", 200, 0.09, 1)
		a.AddRequest("otlp:shop", end.Add(-3*time.Millisecond), "GET", "/api/v3/user/{id}/services", 200, 0.086, 1)
		// Same paths, its own port, nothing in front of it: five times slower, never the same request.
		a.AddRequest("otlp:reporting", end.Add(-5*time.Millisecond), "GET", "/api/v3/user/{user}/services", 200, 0.5, 1)
	}
	sources := map[string]int{}
	for _, b := range a.Drain(time.Now()).HTTP {
		sources[b.Source] += b.N
	}
	if sources["nginx:shop"] != 30 || sources["otlp:shop"] != 0 || sources["otlp:reporting"] != 30 {
		t.Fatalf("requests per source %v: only the application behind nginx is a duplicate", sources)
	}
}
