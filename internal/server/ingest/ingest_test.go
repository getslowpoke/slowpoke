package ingest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

var minute = time.Date(2026, 9, 16, 10, 7, 0, 0, time.UTC).Unix()

func setup(t testing.TB) (*Service, project.Project, context.Context) {
	t.Helper()
	db := store.OpenTest(t)
	ctx := context.Background()
	p, err := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db)
	svc.Now = func() time.Time { return time.Unix(minute+120, 0) }
	return svc, p, ctx
}

func agentInfo(sources ...string) protocol.AgentInfo {
	return protocol.AgentInfo{Host: "web-1", Kind: "agent", Version: "0.1.0", OS: "linux", Sources: sources}
}

func httpBucket(n int, slow float64) protocol.HTTPBucket {
	hist := make([]int, len(protocol.HistogramEdges)+1)
	hist[2] = n
	return protocol.HTTPBucket{
		Source: "nginx:app", Minute: minute, N: n, Sum: float64(n) * 0.2, Max: slow, N1: 1, N2: 1, E5: 1, Hist: hist,
		Endpoints: map[string]protocol.EndpointStat{"GET /orders/{id}": {N: n, Sum: float64(n) * 0.2, Max: slow, Slow: 1, SlowSum: slow, E5: 1}},
	}
}

func TestHTTPIsAdditiveAndBatchesAreDeduplicated(t *testing.T) {
	svc, p, ctx := setup(t)

	first := protocol.Payload{BatchID: "b1", Agent: agentInfo("nginx:app"), HTTP: []protocol.HTTPBucket{httpBucket(10, 3)}}
	for range 2 {
		if _, err := svc.Ingest(ctx, p, first); err != nil {
			t.Fatal(err)
		}
	}
	second := protocol.Payload{BatchID: "b2", Agent: agentInfo("nginx:app"), HTTP: []protocol.HTTPBucket{httpBucket(5, 4)}}
	if _, err := svc.Ingest(ctx, p, second); err != nil {
		t.Fatal(err)
	}

	var n, h2, e5 int
	var sum, mx float64
	err := svc.db.Q().Row(ctx, "SELECT n, sum, max, h2, e5 FROM http_minutes WHERE project_id = ? AND bucket = ?", p.ID, minute).Scan(&n, &sum, &mx, &h2, &e5)
	if err != nil || n != 15 || h2 != 15 || e5 != 2 || mx != 4 {
		t.Fatalf("n=%d h2=%d e5=%d max=%v err=%v", n, h2, e5, mx, err)
	}

	var en, slow int
	var hour int64
	err = svc.db.Q().Row(ctx, "SELECT n, slow, hour FROM endpoint_hours WHERE endpoint = 'GET /orders/{id}'").Scan(&en, &slow, &hour)
	if err != nil || en != 15 || slow != 2 || hour != minute-minute%3600 {
		t.Fatalf("endpoint n=%d slow=%d hour=%d err=%v", en, slow, hour, err)
	}

	var hosts int
	var sources string
	_ = svc.db.Q().Row(ctx, "SELECT COUNT(*), MAX(sources) FROM hosts WHERE project_id = ?", p.ID).Scan(&hosts, &sources)
	if hosts != 1 || sources != `["nginx:app"]` {
		t.Fatalf("hosts=%d sources=%s", hosts, sources)
	}
}

func TestQueriesKeepTheSlowestSample(t *testing.T) {
	svc, p, ctx := setup(t)
	fp := "select * from orders where note like ?"
	hash := fingerprint.Hash(fp)
	send := func(batch string, max float64, sample string) {
		_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: batch, Agent: agentInfo("mysql-slowlog:mysql-slowlog"), DB: []protocol.DBBucket{{
			Source: "mysql-slowlog", Minute: minute, SlowN: 2, SlowSum: 3, SlowMax: max,
			Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, Sample: sample, User: "app", Database: "shop", N: 2, Sum: 3, Max: max, Rows: 500}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	send("a", 2, "select * from orders where note like '%slow%'")
	send("b", 1, "select * from orders where note like '%fast%'")

	var sample, dbName string
	var n, rows int64
	var sum float64
	err := svc.db.Q().Row(ctx, `SELECT f.sample, f.db_name, h.n, h.sum, h.rows FROM query_fingerprints f JOIN query_hours h ON h.fingerprint_id = f.id WHERE f.hash = ?`, hash).Scan(&sample, &dbName, &n, &sum, &rows)
	if err != nil || sample != "select * from orders where note like '%slow%'" || dbName != "shop" || n != 4 || sum != 6 || rows != 1000 {
		t.Fatalf("sample=%q db=%s n=%d sum=%v rows=%d err=%v", sample, dbName, n, sum, rows, err)
	}

	var slowN int
	var slowMax float64
	_ = svc.db.Q().Row(ctx, "SELECT slow_n, slow_max FROM db_minutes WHERE project_id = ?", p.ID).Scan(&slowN, &slowMax)
	if slowN != 4 || slowMax != 2 {
		t.Fatalf("db minutes n=%d max=%v", slowN, slowMax)
	}
}

func TestHostStatusAndPHP(t *testing.T) {
	svc, p, ctx := setup(t)
	_, err := svc.Ingest(ctx, p, protocol.Payload{
		BatchID: "x", Agent: agentInfo("host:host", "php-fpm:www"),
		Host: []protocol.HostSample{{Minute: minute, CPU: 42, CPUSteal: 7, MemUsed: 1, MemTotal: 2, SwapUsed: 3, SwapTotal: 4, OOMKills: 1, NetRxBps: 900,
			TopProcesses: []protocol.Process{{Name: "mysqld", RSS: 1 << 30, CPU: 80, N: 1}}}},
		DBStatus: []protocol.DBStatus{{Minute: minute, QPS: 120, ThreadsRunning: 3}},
		PHP:      []protocol.PHPBucket{{Source: "php-fpm:www", Minute: minute, N: 3, Stalls: []protocol.PHPStall{{Frame: "curl_exec()", Code: "app/A.php:1", N: 3}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var cpu, qps, steal, rx float64
	var stalls, swap, oom int
	var top string
	_ = svc.db.Q().Row(ctx, "SELECT cpu, cpu_steal, swap_used, oom_kills, net_rx_bps, top_processes FROM host_minutes").Scan(&cpu, &steal, &swap, &oom, &rx, &top)
	if steal != 7 || swap != 3 || oom != 1 || rx != 900 || !strings.Contains(top, "mysqld") {
		t.Fatalf("host details steal=%v swap=%d oom=%d rx=%v top=%s", steal, swap, oom, rx, top)
	}
	_ = svc.db.Q().Row(ctx, "SELECT qps FROM db_minutes").Scan(&qps)
	_ = svc.db.Q().Row(ctx, "SELECT n FROM php_stall_hours WHERE frame = 'curl_exec()'").Scan(&stalls)
	if cpu != 42 || qps != 120 || stalls != 3 {
		t.Fatalf("cpu=%v qps=%v stalls=%d", cpu, qps, stalls)
	}
}

func TestAppRoutesJobsAndCommands(t *testing.T) {
	svc, p, ctx := setup(t)
	fp := "select * from products where category_id = ?"
	hash := fingerprint.Hash(fp)
	exit := 0
	_, err := svc.Ingest(ctx, p, protocol.Payload{
		BatchID: "app1", Agent: protocol.AgentInfo{Host: "web-1", Kind: "laravel", Version: "0.1.0"},
		App: []protocol.AppBucket{{
			Minute:  minute,
			Routes:  map[string]protocol.RouteStat{"GET products.index": {N: 4, Sum: 2, Max: 1, Queries: 80, DBSum: 1.5, Hashes: map[string]protocol.RouteQuery{hash: {N: 80, Sum: 1.2, Requests: 4}}}},
			Queries: map[string]protocol.AppQuery{hash: {Fingerprint: fp, Origin: "app/Http/Controllers/ProductController.php:21", N: 80, Sum: 1.2, NPlusOne: 4}},
		}},
		Jobs:     []protocol.JobBucket{{Minute: minute, Kind: "job", Name: "App\\Jobs\\SendInvoice", N: 3, Failed: 1, Sum: 9, Max: 5}},
		Commands: []protocol.CommandRun{{Host: "web-1", Name: "reports:build", StartedAt: minute}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The same command run, now finished.
	_, err = svc.Ingest(ctx, p, protocol.Payload{BatchID: "app2", Agent: protocol.AgentInfo{Host: "web-1", Kind: "laravel"},
		Commands: []protocol.CommandRun{{Host: "web-1", Name: "reports:build", StartedAt: minute, FinishedAt: minute + 30, ExitCode: &exit}}})
	if err != nil {
		t.Fatal(err)
	}

	var origin string
	var nPlusOne int
	_ = svc.db.Q().Row(ctx, "SELECT origin, n_plus_one FROM query_fingerprints WHERE hash = ?", hash).Scan(&origin, &nPlusOne)
	if origin != "app/Http/Controllers/ProductController.php:21" || nPlusOne != 4 {
		t.Fatalf("origin=%q n+1=%d", origin, nPlusOne)
	}
	var routeQueries, requests int
	_ = svc.db.Q().Row(ctx, "SELECT n, requests FROM route_queries WHERE route = 'GET products.index'").Scan(&routeQueries, &requests)
	var failed int
	_ = svc.db.Q().Row(ctx, "SELECT failed FROM job_hours").Scan(&failed)
	var runs int
	var finished int64
	_ = svc.db.Q().Row(ctx, "SELECT COUNT(*), MAX(finished_at) FROM command_runs").Scan(&runs, &finished)
	if routeQueries != 80 || requests != 4 || failed != 1 || runs != 1 || finished != minute+30 {
		t.Fatalf("routeQueries=%d requests=%d failed=%d runs=%d finished=%d", routeQueries, requests, failed, runs, finished)
	}
}

func TestExplainRequestsAndResults(t *testing.T) {
	svc, p, ctx := setup(t)
	costly := "select * from orders where note like '%x%'"
	cheap := "select * from orders where id = 1"
	withBindings := "select * from orders where user_id = ?"
	queries := map[string]protocol.QueryStat{}
	for sql, sum := range map[string]float64{costly: 500, cheap: 0.1, withBindings: 900} {
		fp := fingerprint.Of(sql)
		queries[fingerprint.Hash(fp)] = protocol.QueryStat{Fingerprint: fp, Sample: sql, N: 10, Sum: sum, Max: sum / 10}
	}
	payload := protocol.Payload{BatchID: "q", Agent: agentInfo("mysql:db"), DB: []protocol.DBBucket{{Source: "mysql-digest", Minute: minute, Queries: queries}}}

	resp, err := svc.Ingest(ctx, p, payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Explain) != 1 || resp.Explain[0].SQL != costly {
		t.Fatalf("only the costly explainable query must be requested: %+v", resp.Explain)
	}

	// Not requested again right away.
	payload.BatchID = "q2"
	resp, _ = svc.Ingest(ctx, p, payload)
	if len(resp.Explain) != 0 {
		t.Fatalf("requested twice: %+v", resp.Explain)
	}

	// An agent without database access never receives requests.
	resp, _ = svc.Ingest(ctx, p, protocol.Payload{BatchID: "n", Agent: agentInfo("nginx:app")})
	if len(resp.Explain) != 0 {
		t.Fatal("agent without db source got explain requests")
	}

	hash := fingerprint.Hash(fingerprint.Of(costly))
	_, err = svc.Ingest(ctx, p, protocol.Payload{BatchID: "e", Agent: agentInfo("mysql:db"), Explains: []protocol.Explain{{
		Hash: hash, Engine: "mysql", Plan: []byte(`[{"table":"orders","type":"ALL","rows":100000}]`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var plan string
	var explainedAt int64
	_ = svc.db.Q().Row(ctx, "SELECT explain, explained_at FROM query_fingerprints WHERE hash = ?", hash).Scan(&plan, &explainedAt)
	if explainedAt == 0 || plan == "" {
		t.Fatalf("explain not stored: %q %d", plan, explainedAt)
	}
}

// In the lab a project installed minutes ago waited hours for its first plan: the threshold
// compared seconds of the last 24 hours with a per-day cost. Costs are per observed day.
func TestExplainThresholdUsesTheObservedPeriod(t *testing.T) {
	svc, p, ctx := setup(t)
	sql := "select * from orders where note like '%x%'"
	fp := fingerprint.Of(sql)
	// 5 s in the first 20 minutes: 360 s per day, well above the 30 s default.
	queries := map[string]protocol.QueryStat{fingerprint.Hash(fp): {Fingerprint: fp, Sample: sql, N: 5, Sum: 5, Max: 1}}
	svc.Now = func() time.Time { return time.Unix(minute+20*60, 0) }
	resp, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "fresh", Agent: agentInfo("mysql:db"),
		DB: []protocol.DBBucket{{Source: "mysql-digest", Minute: minute, Queries: queries}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Explain) != 1 {
		t.Fatalf("a fresh project must get its first plan: %+v", resp.Explain)
	}
}

// In the lab the agent started before MariaDB and answered "connection refused": that
// error counted as a fresh plan and the query was not explained again for a week.
func TestFailedExplainIsRetriedSoon(t *testing.T) {
	svc, p, ctx := setup(t)
	sql := "select * from orders where note like '%x%'"
	fp := fingerprint.Of(sql)
	hash := fingerprint.Hash(fp)
	payload := protocol.Payload{BatchID: "a", Agent: agentInfo("mysql:db"),
		DB: []protocol.DBBucket{{Source: "mysql-digest", Minute: minute, Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, Sample: sql, N: 5, Sum: 500, Max: 100}}}}}
	start := time.Unix(minute+120, 0)
	at := func(d time.Duration, batch string) protocol.IngestResponse {
		svc.Now = func() time.Time { return start.Add(d) }
		payload.BatchID = batch
		resp, err := svc.Ingest(ctx, p, payload)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if resp := at(0, "a"); len(resp.Explain) != 1 {
		t.Fatalf("first request: %+v", resp.Explain)
	}
	failed := protocol.Payload{BatchID: "err", Agent: agentInfo("mysql:db"), Explains: []protocol.Explain{{Hash: hash, Error: "dial tcp: connection refused"}}}
	if _, err := svc.Ingest(ctx, p, failed); err != nil {
		t.Fatal(err)
	}
	if resp := at(10*time.Minute, "b"); len(resp.Explain) != 0 {
		t.Fatalf("retried too soon: %+v", resp.Explain)
	}
	if resp := at(2*time.Hour, "c"); len(resp.Explain) != 1 {
		t.Fatalf("a failed EXPLAIN must be retried within hours, not a week: %+v", resp.Explain)
	}
}

// pg_stat_statements keeps only statements with $n placeholders: PostgreSQL 16+ agents
// explain them with a generic plan, so they must be requested like any other.
func TestPostgresStatementsWithoutValuesAreRequested(t *testing.T) {
	svc, p, ctx := setup(t)
	sql := "select id, name from products where description ilike $1 limit $2"
	fp := fingerprint.Of(sql)
	queries := map[string]protocol.QueryStat{fingerprint.Hash(fp): {Fingerprint: fp, Sample: sql, N: 50, Sum: 400, Max: 9}}
	resp, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "pg", Agent: agentInfo("postgres:pg"),
		DB: []protocol.DBBucket{{Source: "pg-statements", Minute: minute, Queries: queries}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Explain) != 1 || resp.Explain[0].SQL != sql {
		t.Fatalf("postgres statement not requested: %+v", resp.Explain)
	}

	_, err = svc.Ingest(ctx, p, protocol.Payload{BatchID: "pg-plan", Agent: agentInfo("postgres:pg"), Explains: []protocol.Explain{{
		Hash: fingerprint.Hash(fp), Engine: "postgres", Generic: true, Plan: []byte(`[{"Plan":{"Node Type":"Seq Scan"}}]`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	_ = svc.db.Q().Row(ctx, "SELECT explain FROM query_fingerprints WHERE hash = ?", fingerprint.Hash(fp)).Scan(&stored)
	if !strings.Contains(stored, `"generic":true`) {
		t.Fatalf("generic plan not marked: %s", stored)
	}
}

func TestRejectsBucketsFarInTheFuture(t *testing.T) {
	svc, p, ctx := setup(t)
	b := httpBucket(1, 1)
	b.Minute = minute + 86400*3
	if _, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "f", Agent: agentInfo(), HTTP: []protocol.HTTPBucket{b}}); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = svc.db.Q().Row(ctx, "SELECT COUNT(*) FROM http_minutes").Scan(&n)
	if n != 0 {
		t.Fatal("a broken clock must not write the future")
	}
}

// MySQL writes the database as it appears in the log — `contract_service`, backticks and all —
// and that is what the panel showed as the name of the database (production, 2026-09-18).
func TestDatabaseNamesAreStoredWithoutQuotes(t *testing.T) {
	svc, p, ctx := setup(t)
	fp := "select * from contracts where id = ?"
	hash := fingerprint.Hash(fp)
	_, err := svc.Ingest(ctx, p, protocol.Payload{
		BatchID: "1", Agent: agentInfo("mysql-slowlog:mysql-slowlog"),
		DB: []protocol.DBBucket{{Source: "mysql-slowlog", Minute: minute, SlowN: 1, SlowSum: 2,
			Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, N: 1, Sum: 2,
				Database: "`contract_service`", User: "`app`@`localhost`"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var db, user string
	if err := svc.db.Q().Row(ctx, "SELECT db_name, db_user FROM query_fingerprints WHERE hash = ?", hash).Scan(&db, &user); err != nil {
		t.Fatal(err)
	}
	if db != "contract_service" {
		t.Fatalf("database name: %q", db)
	}
	if user != "app@localhost" {
		t.Fatalf("database user: %q", user)
	}
}

// An endpoint's histogram and a route's outbound calls add up across batches like the rest.
func TestEndpointHistogramsAndOutboundCallsAreStored(t *testing.T) {
	svc, p, ctx := setup(t)
	hist := make([]int, len(protocol.HistogramEdges)+1)
	hist[3], hist[12] = 4, 1
	for i, batch := range []string{"x1", "x2"} {
		_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: batch, Agent: agentInfo("otlp:shop"),
			HTTP: []protocol.HTTPBucket{{Source: "otlp:shop", Minute: minute + int64(i)*60, N: 5, Hist: hist,
				Endpoints: map[string]protocol.EndpointStat{"POST /checkout": {N: 5, Sum: 14, Max: 12, Hist: hist}}}},
			App: []protocol.AppBucket{{Minute: minute + int64(i)*60, Routes: map[string]protocol.RouteStat{"POST /checkout": {
				N: 5, Sum: 14, Max: 12, ExtSum: 9, ExtRequests: 3,
				Ext: map[string]protocol.ExtCall{"api.stripe.com": {N: 4, Sum: 10, Max: 8, Errors: 1, Requests: 3, Origin: "app/Services/Stripe.php:88"}},
			}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var h3, h12 int
	if err := svc.db.Q().Row(ctx, "SELECT h3, h12 FROM endpoint_hours WHERE endpoint = 'POST /checkout'").Scan(&h3, &h12); err != nil || h3 != 8 || h12 != 2 {
		t.Fatalf("h3=%d h12=%d err=%v", h3, h12, err)
	}
	var extSum float64
	var extRequests int
	if err := svc.db.Q().Row(ctx, "SELECT ext_sum, ext_requests FROM route_hours WHERE route = 'POST /checkout'").Scan(&extSum, &extRequests); err != nil || extSum != 18 || extRequests != 6 {
		t.Fatalf("ext_sum=%v ext_requests=%d err=%v", extSum, extRequests, err)
	}
	var n, errors, requests int
	var sum, mx float64
	var origin string
	err := svc.db.Q().Row(ctx, "SELECT n, sum, max, errors, requests, origin FROM route_calls WHERE route = 'POST /checkout' AND host = 'api.stripe.com'").
		Scan(&n, &sum, &mx, &errors, &requests, &origin)
	if err != nil || n != 8 || sum != 20 || mx != 8 || errors != 2 || requests != 6 || origin != "app/Services/Stripe.php:88" {
		t.Fatalf("n=%d sum=%v max=%v errors=%d requests=%d origin=%q err=%v", n, sum, mx, errors, requests, origin, err)
	}
}
