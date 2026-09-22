package retention

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/metrics"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// A real installation, four days in: 200 MB, 110 of them Redis statistics of 17 machines stored minute by
// minute and read, past the first day, one point per hour. Past the detail window an hour of
// minutes becomes one row: the readers sum, average and take maxima per step, so they get the
// same numbers from far fewer rows.
func TestOldMinutesOfMetricsAndMachinesBecomeOneRowPerHour(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	host, _ := db.Q().InsertID(ctx, `INSERT INTO hosts (project_id, name, kind, version, os, sources, last_seen_at) VALUES (?, 'cache-1', 'agent', '', 'linux', '[]', 0)`, p.ID)
	now := time.Date(2026, 9, 22, 12, 30, 0, 0, time.UTC)
	oldHour := now.Add(-3 * 24 * time.Hour).Truncate(time.Hour).Unix()
	recent := now.Add(-2 * time.Hour).Truncate(time.Hour).Unix()

	q := db.Q()
	for _, hour := range []int64{oldHour, recent} {
		for m := int64(0); m < 60; m++ {
			b := hour + m*60
			// A counter and a gauge: the counter adds up, the gauge keeps its average, extremes and last value.
			q.Exec(ctx, `INSERT INTO metric_minutes (project_id, host_id, name, bucket, kind, count, sum, min, max, last, h2) VALUES (?, ?, 'redis.cache.commands', ?, 'counter', 1, 10, 10, 10, 10, 1)`, p.ID, host, b)
			v := float64(m)
			q.Exec(ctx, `INSERT INTO metric_minutes (project_id, host_id, name, bucket, kind, count, sum, min, max, last) VALUES (?, ?, 'redis.cache.memory_bytes', ?, 'gauge', 1, ?, ?, ?, ?)`, p.ID, host, b, v, v, v, v)
			q.Exec(ctx, `INSERT INTO host_minutes (host_id, bucket, cpu, load1, mem_used, mem_total, disk_used, disk_total, swap_out_pages, oom_kills, top_processes)
				VALUES (?, ?, ?, 1, 100, 1000, 5, 50, 2, 0, '[{"name":"redis-server"}]')`, host, b, v)
		}
	}
	custom := func() (count int64, sum, max, last float64) {
		list, err := metrics.Reader{DB: db}.CustomByHost(ctx, p.ID, "redis.cache.memory_bytes", time.Unix(oldHour, 0), time.Unix(oldHour+3600, 0), 3600)
		if err != nil || len(list) != 1 {
			t.Fatalf("custom %v %v", list, err)
		}
		return list[0].Count, list[0].Sum, list[0].Max, list[0].Last
	}
	c0, s0, m0, _ := custom()

	if err := Compact(ctx, db, now, Defaults()); err != nil {
		t.Fatal(err)
	}
	rows := func(table string, from, to int64) int {
		var n int
		_ = q.Row(ctx, "SELECT COUNT(*) FROM "+table+" WHERE bucket >= ? AND bucket < ?", from, to).Scan(&n)
		return n
	}
	if n := rows("metric_minutes", oldHour, oldHour+3600); n != 2 {
		t.Fatalf("old hour of metrics: %d rows, want one per metric", n)
	}
	if n := rows("host_minutes", oldHour, oldHour+3600); n != 1 {
		t.Fatalf("old hour of the machine: %d rows", n)
	}
	if rows("metric_minutes", recent, recent+3600) != 120 || rows("host_minutes", recent, recent+3600) != 60 {
		t.Fatal("the last day must keep its minutes")
	}

	var count, h2 int64
	var sum, min, max, last float64
	q.Row(ctx, `SELECT count, sum, h2 FROM metric_minutes WHERE name = 'redis.cache.commands' AND bucket = ?`, oldHour).Scan(&count, &sum, &h2)
	if count != 60 || sum != 600 || h2 != 60 {
		t.Fatalf("counter hour: count=%d sum=%v h2=%d", count, sum, h2)
	}
	q.Row(ctx, `SELECT min, max, last FROM metric_minutes WHERE name = 'redis.cache.memory_bytes' AND bucket = ?`, oldHour).Scan(&min, &max, &last)
	if min != 0 || max != 59 || last != 59 {
		t.Fatalf("gauge hour: min=%v max=%v last=%v", min, max, last)
	}
	if c1, s1, m1, l1 := custom(); c1 != c0 || s1 != s0 || m1 != m0 || l1 != 59 {
		t.Fatalf("the reader changed: %d/%v/%v -> %d/%v/%v last %v", c0, s0, m0, c1, s1, m1, l1)
	}

	var cpu, peak float64
	var swapOut, memUsed, memTotal int64
	var top *string
	q.Row(ctx, `SELECT cpu, cpu_peak, swap_out_pages, mem_used, mem_total, top_processes FROM host_minutes WHERE host_id = ? AND bucket = ?`, host, oldHour).
		Scan(&cpu, &peak, &swapOut, &memUsed, &memTotal, &top)
	if math.Abs(cpu-29.5) > 1e-9 || peak != 59 || swapOut != 120 || memUsed != 100 || memTotal != 1000 || top != nil {
		t.Fatalf("machine hour: cpu=%v peak=%v swap_out=%d mem=%d/%d top=%v", cpu, peak, swapOut, memUsed, memTotal, top)
	}

	// Running again, as every hour, changes nothing that is already compacted.
	if err := Compact(ctx, db, now, Defaults()); err != nil {
		t.Fatal(err)
	}
	q.Row(ctx, `SELECT count, sum FROM metric_minutes WHERE name = 'redis.cache.commands' AND bucket = ?`, oldHour).Scan(&count, &sum)
	if count != 60 || sum != 600 || rows("host_minutes", oldHour, oldHour+3600) != 1 {
		t.Fatalf("compacted twice: count=%d sum=%v", count, sum)
	}

	// A late minute that lands in an hour already compacted is folded in the next time.
	q.Exec(ctx, `INSERT INTO metric_minutes (project_id, host_id, name, bucket, kind, count, sum, min, max, last) VALUES (?, ?, 'redis.cache.commands', ?, 'counter', 1, 10, 10, 10, 10)`, p.ID, host, oldHour+120)
	if err := Compact(ctx, db, now, Defaults()); err != nil {
		t.Fatal(err)
	}
	q.Row(ctx, `SELECT count, sum FROM metric_minutes WHERE name = 'redis.cache.commands' AND bucket = ?`, oldHour).Scan(&count, &sum)
	if count != 61 || sum != 610 || rows("metric_minutes", oldHour, oldHour+3600) != 2 {
		t.Fatalf("late minute: count=%d sum=%v", count, sum)
	}
}

// Compacted hours are small, so metrics and machines keep a month: the 30-day charts are full.
func TestCompactedHoursAreKeptForAMonth(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	q := db.Q()
	for _, age := range []time.Duration{20 * 24 * time.Hour, 40 * 24 * time.Hour} {
		q.Exec(ctx, "INSERT INTO metric_minutes (project_id, host_id, name, bucket, kind) VALUES (?, 0, 'queue.backlog', ?, 'gauge')", p.ID, now.Add(-age).Truncate(time.Hour).Unix())
	}
	if _, err := Prune(ctx, db, now, Defaults()); err != nil {
		t.Fatal(err)
	}
	var n int
	q.Row(ctx, "SELECT COUNT(*) FROM metric_minutes").Scan(&n)
	if n != 1 {
		t.Fatalf("%d metric rows left, want the one of 20 days ago", n)
	}
}

// Endpoints, queries and PHP stalls keep their hours for three weeks (a deploy is compared with
// the same hour up to 14 days earlier); past that, nothing reads them by the hour, and a day of
// hours becomes one row with the same totals.
func TestOldHoursOfEndpointsQueriesAndStallsBecomeOneRowPerDay(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	now := time.Date(2026, 9, 22, 12, 30, 0, 0, time.UTC)
	oldDay := now.Add(-30 * 24 * time.Hour).Truncate(24 * time.Hour).Unix()
	recentHour := now.Add(-5 * 24 * time.Hour).Truncate(time.Hour).Unix()
	q := db.Q()
	fp, _ := q.InsertID(ctx, `INSERT INTO query_fingerprints (project_id, hash, fingerprint, first_seen_at, last_seen_at) VALUES (?, 'aaaa', 'SELECT 1', ?, ?)`, p.ID, oldDay, oldDay)
	put := func(hour int64, max float64) {
		q.Exec(ctx, `INSERT INTO endpoint_hours (project_id, source, endpoint, hour, n, sum, max, slow, slow_sum, e5, h3) VALUES (?, 'nginx:app', 'GET /search', ?, 10, 5, ?, 2, 3, 1, 10)`, p.ID, hour, max)
		q.Exec(ctx, `INSERT INTO query_hours (fingerprint_id, source, host_id, hour, project_id, n, sum, max, rows) VALUES (?, 'mysql-slowlog', 0, ?, ?, 4, 2, ?, 100)`, fp, hour, p.ID, max)
		q.Exec(ctx, `INSERT INTO php_stall_hours (project_id, source, hour, frame, code, n) VALUES (?, 'php-fpm:www', ?, 'PDO->query', 'app/Repo.php:10', 3)`, p.ID, hour)
	}
	for h := int64(0); h < 24; h++ {
		put(oldDay+h*3600, float64(h))
	}
	put(recentHour, 1)
	put(recentHour+3600, 1)

	costs := func() map[string]metrics.Cost {
		list, err := metrics.Reader{DB: db}.Costs(ctx, p.ID, time.Unix(oldDay-86400, 0), time.Unix(oldDay+2*86400, 0))
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]metrics.Cost{}
		for _, c := range list {
			out[c.Kind] = c
		}
		return out
	}
	before := costs()
	if len(before) != 3 {
		t.Fatalf("costs before: %v", before)
	}

	for i := 0; i < 2; i++ { // the second run, as every hour, must change nothing
		if err := Compact(ctx, db, now, Defaults()); err != nil {
			t.Fatal(err)
		}
	}
	count := func(table string, from, to int64) (n int) {
		q.Row(ctx, "SELECT COUNT(*) FROM "+table+" WHERE hour >= ? AND hour < ?", from, to).Scan(&n)
		return
	}
	for _, table := range []string{"endpoint_hours", "query_hours", "php_stall_hours"} {
		if n := count(table, oldDay, oldDay+86400); n != 1 {
			t.Errorf("%s: %d rows for the old day, want 1", table, n)
		}
		if n := count(table, recentHour, recentHour+7200); n != 2 {
			t.Errorf("%s: the last three weeks must keep their hours, got %d rows", table, n)
		}
	}
	var n, slow, e5, h3 int64
	var sum, max float64
	q.Row(ctx, `SELECT n, sum, max, slow, e5, h3 FROM endpoint_hours WHERE hour = ?`, oldDay).Scan(&n, &sum, &max, &slow, &e5, &h3)
	if n != 240 || sum != 120 || max != 23 || slow != 48 || e5 != 24 || h3 != 240 {
		t.Fatalf("endpoint day: n=%d sum=%v max=%v slow=%d e5=%d h3=%d", n, sum, max, slow, e5, h3)
	}
	var rows int64
	q.Row(ctx, `SELECT n, max, rows FROM query_hours WHERE hour = ?`, oldDay).Scan(&n, &max, &rows)
	if n != 96 || max != 23 || rows != 2400 {
		t.Fatalf("query day: n=%d max=%v rows=%d", n, max, rows)
	}
	after := costs()
	for kind, c := range before {
		if a := after[kind]; a.Count != c.Count || math.Abs(a.Seconds-c.Seconds) > 1e-9 || a.Max != c.Max {
			t.Errorf("%s cost changed: %+v -> %+v", kind, c, a)
		}
	}
}

// Routes, outbound calls and jobs are never read past 30 days: keeping them 120 was only weight.
func TestRoutesAndJobsAreKeptForAMonth(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	q := db.Q()
	for _, age := range []time.Duration{20 * 24 * time.Hour, 40 * 24 * time.Hour} {
		h := now.Add(-age).Truncate(time.Hour).Unix()
		q.Exec(ctx, `INSERT INTO route_hours (project_id, route, hour) VALUES (?, 'GET /search', ?)`, p.ID, h)
		q.Exec(ctx, `INSERT INTO route_calls (project_id, route, host, hour) VALUES (?, 'GET /search', 'api.stripe.com', ?)`, p.ID, h)
		q.Exec(ctx, `INSERT INTO job_hours (project_id, kind, name, hour) VALUES (?, 'job', 'SendMail', ?)`, p.ID, h)
	}
	if _, err := Prune(ctx, db, now, Defaults()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"route_hours", "route_calls", "job_hours"} {
		var n int
		q.Row(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n)
		if n != 1 {
			t.Errorf("%s: %d rows left, want the one of 20 days ago", table, n)
		}
	}
}
