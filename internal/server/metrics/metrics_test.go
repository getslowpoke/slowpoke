package metrics

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/exclude"
	"github.com/getslowpoke/slowpoke/internal/server/ingest"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func TestPercentileInterpolatesInsideTheBucket(t *testing.T) {
	var h HTTPTotals
	// 90 requests in (0.1, 0.2], 10 in (1.5, 2]
	h.N = 100
	h.Hist[2] = 90
	h.Hist[8] = 10
	if got := h.Percentile(0.95); math.Abs(got-1.75) > 0.001 {
		t.Fatalf("p95 = %v", got)
	}
	if got := h.Percentile(0.5); got <= 0.1 || got > 0.2 {
		t.Fatalf("p50 = %v", got)
	}
	var empty HTTPTotals
	if empty.Percentile(0.95) != 0 {
		t.Fatal("empty")
	}
}

func TestSnapshotAndCosts(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	base := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)

	hist := make([]int, 15)
	hist[3] = 60
	fp := "select * from orders where note like ?"
	hash := fingerprint.Hash(fp)
	payload := protocol.Payload{
		BatchID: "1",
		Agent:   protocol.AgentInfo{Host: "web", Kind: "agent", Sources: []string{"nginx:app", "php-fpm:www", "mysql-slowlog:mysql-slowlog"}},
		HTTP: []protocol.HTTPBucket{
			{Source: "nginx:app", Minute: base.Unix(), N: 50, N2: 2, E5: 1, Sum: 10, Max: 3, Hist: hist,
				Endpoints: map[string]protocol.EndpointStat{"GET /search": {N: 50, Slow: 2, SlowSum: 6}}},
			{Source: "nginx:api", Minute: base.Add(time.Minute).Unix(), N: 10, Sum: 1, Hist: hist},
		},
		PHP: []protocol.PHPBucket{{Source: "php-fpm:www", Minute: base.Unix(), N: 2, Stalls: []protocol.PHPStall{{Frame: "curl_exec()", Code: "app/X.php:1", N: 2}}}},
		DB: []protocol.DBBucket{
			{Source: "mysql-slowlog", Minute: base.Unix(), SlowN: 3, SlowSum: 9, SlowMax: 4, Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, N: 3, Sum: 9, Max: 4}}},
			{Source: "mysql-digest", Minute: base.Unix(), Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, N: 30, Sum: 12}}},
		},
	}
	timerHist := make([]int, 15)
	timerHist[8] = 4
	payload.Metrics = []protocol.MetricBucket{
		{Minute: base.Unix(), Name: "invoices.pdf", Kind: "timer", Count: 4, Sum: 7, Min: 1.2, Max: 2, Last: 2, Hist: timerHist},
		{Minute: base.Unix(), Name: "queue.backlog", Kind: "gauge", Count: 1, Sum: 40, Min: 40, Max: 40, Last: 40},
		{Minute: base.Add(time.Minute).Unix(), Name: "queue.backlog", Kind: "gauge", Count: 1, Sum: 12, Min: 12, Max: 12, Last: 12},
	}
	svc := ingest.New(db)
	if _, err := svc.Ingest(ctx, p, payload); err != nil {
		t.Fatal(err)
	}

	r := Reader{DB: db}
	snap, err := r.Snapshot(ctx, p.ID, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	all := snap.HTTPFor("")
	if all.N != 60 || all.E5 != 1 || all.N2 != 2 || snap.HTTPFor("nginx:api").N != 10 {
		t.Fatalf("http %+v", snap.HTTP)
	}
	if snap.PHPStalls != 2 || snap.DB.SlowSum != 9 || snap.DB.SlowMax != 4 || !snap.Has.PHP || !snap.Has.DB || !snap.Has.HTTP {
		t.Fatalf("snapshot %+v", snap)
	}
	if pdf := snap.Custom["invoices.pdf"]; pdf.Count != 4 || pdf.Percentile(0.95) <= 1.5 || pdf.Percentile(0.95) > 2 {
		t.Fatalf("custom timer %+v", pdf)
	}
	if q := snap.Custom["queue.backlog"]; q.Last != 12 || q.Max != 40 {
		t.Fatalf("custom gauge %+v", q)
	}
	if math.Abs(snap.Hours-2.0/60) > 0.0001 {
		t.Fatalf("hours %v", snap.Hours)
	}

	costs, err := r.Costs(ctx, p.ID, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	byTarget := map[string]Cost{}
	for _, c := range costs {
		byTarget[c.Kind+"|"+c.Target] = c
	}
	// The same query from two sources counts once: the most complete source wins.
	if c := byTarget["query|"+hash]; c.Seconds != 12 || c.Label != fp {
		t.Fatalf("query cost %+v", c)
	}
	if c := byTarget["endpoint|nginx:app GET /search"]; c.Seconds != 6 {
		t.Fatalf("endpoint cost %+v", costs)
	}
	if c := byTarget["metric|invoices.pdf"]; c.Seconds != 7 {
		t.Fatalf("custom timer cost %+v", costs)
	}
	if _, ok := byTarget["metric|queue.backlog"]; ok {
		t.Fatal("gauges are not a waiting cost")
	}
	if c := byTarget["php|curl_exec()"]; c.Count != 2 {
		t.Fatalf("php cost %+v", costs)
	}
}

// A project installed a few minutes ago must not spread its costs over a whole week: the
// query doctor told a new user "2 calls per day, 0 seconds lost" for a query run every 5 s.
func TestObservedDaysOfAFreshProject(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	r := Reader{DB: db}
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour

	if d := r.ObservedDays(ctx, p.ID, week, now); d != 7 {
		t.Fatalf("no data yet: %v days, want the whole window", d)
	}

	hist := make([]int, 15)
	payload := protocol.Payload{BatchID: "fresh", Agent: protocol.AgentInfo{Host: "web", Kind: "agent"},
		HTTP: []protocol.HTTPBucket{{Source: "nginx:app", Minute: now.Add(-40 * time.Minute).Unix(), N: 1, Hist: hist}}}
	if _, err := ingest.New(db).Ingest(ctx, p, payload); err != nil {
		t.Fatal(err)
	}
	if d := r.ObservedDays(ctx, p.ID, week, now); math.Abs(d*24*60-40) > 0.01 {
		t.Fatalf("40 minutes of data: %v days", d)
	}
	if d := r.ObservedDays(ctx, p.ID, 20*time.Minute, now); math.Abs(d*24*60-20) > 0.01 {
		t.Fatalf("never more than the window: %v days", d)
	}
}

// Four machines run a Redis called "cache": their memory is four numbers, not their sum.
func TestSnapshotKeepsOneSeriesPerMachine(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	base := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	svc := ingest.New(db)
	send := func(batch, host string, memory float64, orders int64) {
		payload := protocol.Payload{
			BatchID: batch,
			Agent:   protocol.AgentInfo{Host: host, Kind: "agent"},
			Metrics: []protocol.MetricBucket{
				{Minute: base.Unix(), Name: "redis.cache.memory_bytes", Kind: "gauge", Count: 1, Sum: memory, Min: memory, Max: memory, Last: memory},
				{Minute: base.Unix(), Name: "orders.created", Kind: "counter", Count: orders, Sum: float64(orders)},
			},
		}
		if _, err := svc.Ingest(ctx, p, payload); err != nil {
			t.Fatal(err)
		}
	}
	send("1", "web-1", 100, 3)
	send("2", "web-2", 900, 4)

	snap, err := Reader{DB: db}.Snapshot(ctx, p.ID, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	memory := snap.Custom["redis.cache.memory_bytes"]
	if memory.Hosts != 2 || memory.Last != 0 {
		t.Fatalf("a gauge of two machines has no single last value: %+v", memory)
	}
	orders := snap.Custom["orders.created"]
	if orders.Hosts != 2 || orders.Count != 7 || orders.Sum != 7 {
		t.Fatalf("one counter of two machines is one total: %+v", orders)
	}

	perHost, err := Reader{DB: db}.CustomByHost(ctx, p.ID, "redis.cache.memory_bytes", base, base.Add(time.Hour), 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(perHost) != 2 || perHost[0].Host != "web-1" || perHost[0].Last != 100 || perHost[1].Host != "web-2" || perHost[1].Last != 900 {
		t.Fatalf("per machine: %+v", perHost)
	}
	if len(perHost[0].Series) != 1 || perHost[0].Series[0].Sum != 100 || len(perHost[1].Series) != 1 || perHost[1].Series[0].Sum != 900 {
		t.Fatalf("one chart per machine: %+v %+v", perHost[0].Series, perHost[1].Series)
	}
}

// What got better and what got worse: the window against the one right before it.
func TestChangesCompareTheWindowWithTheOneBefore(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	svc := ingest.New(db)
	svc.Now = func() time.Time { return now }

	fp := "select * from orders where note like ?"
	hash := fingerprint.Hash(fp)
	slow := "select * from carts where token = ?"
	slowHash := fingerprint.Hash(slow)
	hist := make([]int, 15)
	hist[3] = 10
	// at is hours before now; a query and an endpoint, each with the seconds they cost then.
	at := func(batch string, hours float64, querySeconds, endpointSeconds float64, queryHash, queryFp string) {
		minute := now.Add(-time.Duration(hours * float64(time.Hour))).Unix() / 60 * 60
		payload := protocol.Payload{
			BatchID: batch,
			Agent:   protocol.AgentInfo{Host: "web", Kind: "agent", Sources: []string{"nginx:app", "mysql-slowlog:mysql-slowlog"}},
			DB: []protocol.DBBucket{{Source: "mysql-slowlog", Minute: minute, SlowN: 1, SlowSum: querySeconds, SlowMax: querySeconds,
				Queries: map[string]protocol.QueryStat{queryHash: {Fingerprint: queryFp, N: 1, Sum: querySeconds, Max: querySeconds}}}},
			HTTP: []protocol.HTTPBucket{{Source: "nginx:app", Minute: minute, N: 10, Sum: endpointSeconds, Max: 3, Hist: hist,
				Endpoints: map[string]protocol.EndpointStat{"GET /search": {N: 10, Sum: endpointSeconds, Slow: 2, SlowSum: endpointSeconds}}}},
		}
		if _, err := svc.Ingest(ctx, p, payload); err != nil {
			t.Fatal(err)
		}
	}
	at("old", 50, 1, 1, hash, fp)       // the project has been measured for more than two windows
	at("before", 36, 120, 60, hash, fp) // the window before: expensive
	at("now", 6, 30, 90, hash, fp)      // this window: the query was fixed, the endpoint got worse
	at("new", 4, 45, 0, slowHash, slow) // something that was not there before

	r := Reader{DB: db}
	changes, comparable, err := r.Changes(ctx, p.ID, now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if !comparable {
		t.Fatal("two full windows of data are comparable")
	}
	byTarget := map[string]Change{}
	for _, c := range changes {
		byTarget[c.Target] = c
	}
	query := byTarget[hash]
	if query.Kind != "query" || query.BeforePerDay != 120 || query.NowPerDay != 30 || query.SavedPerDay != 90 {
		t.Fatalf("the query that was fixed: %+v", query)
	}
	endpoint := byTarget["nginx:app GET /search"]
	if endpoint.Kind != "endpoint" || endpoint.SavedPerDay != -30 {
		t.Fatalf("the endpoint that got worse: %+v", endpoint)
	}
	fresh := byTarget[slowHash]
	if !fresh.New || fresh.NowPerDay != 45 || fresh.SavedPerDay != -45 {
		t.Fatalf("something new is not a win: %+v", fresh)
	}
	// Ordered by what changed the most, best first.
	if changes[0].Target != hash {
		t.Fatalf("order: %+v", changes)
	}

	// Half a window of measures before is enough, because both sides are counted per day.
	half, _ := project.Repo{DB: db}.Create(ctx, "Half", "UTC")
	for i, hours := range []float64{40, 30, 6} {
		if _, err := svc.Ingest(ctx, half, protocol.Payload{BatchID: fmt.Sprint(i), Agent: protocol.AgentInfo{Host: "w", Kind: "agent"},
			DB: []protocol.DBBucket{{Source: "mysql-slowlog", Minute: now.Add(-time.Duration(hours*float64(time.Hour))).Unix() / 60 * 60, SlowN: 1, SlowSum: 10,
				Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, N: 1, Sum: 10}}}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, comparable, _ := r.Changes(ctx, half.ID, now.Add(-24*time.Hour), now); !comparable {
		t.Fatal("sixteen hours of measures before a full day are enough to compare")
	}

	// A project younger than two windows has nothing to compare with.
	young, _ := project.Repo{DB: db}.Create(ctx, "Young", "UTC")
	if _, err := svc.Ingest(ctx, young, protocol.Payload{BatchID: "y", Agent: protocol.AgentInfo{Host: "w", Kind: "agent"},
		DB: []protocol.DBBucket{{Source: "mysql-slowlog", Minute: now.Add(-2 * time.Hour).Unix() / 60 * 60, SlowN: 1, SlowSum: 5,
			Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, N: 1, Sum: 5}}}}}); err != nil {
		t.Fatal(err)
	}
	if _, comparable, _ := r.Changes(ctx, young.ID, now.Add(-24*time.Hour), now); comparable {
		t.Fatal("a project measured for two hours cannot be compared with the day before")
	}
}

// The proxy on one machine, the application on another: no agent sees both, so no agent can pair
// single requests. What is left is the host each of them answers for, and the same requests must
// still be counted once - from the web server, which measured the whole wait.
func TestTheSameRequestsFromTwoMachinesAreCountedOnce(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	base := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	hist := make([]int, 15)
	hist[3] = 100

	// The machine with nginx in front of the shop.
	proxy := protocol.Payload{
		BatchID: "proxy", Agent: protocol.AgentInfo{Host: "proxy", Kind: "agent", Sources: []string{"nginx:shop"}},
		HTTP: []protocol.HTTPBucket{{
			Source: "nginx:shop", Minute: base.Unix(), N: 100, Sum: 50, Max: 2, Hist: hist,
			Sites:     []string{"shop.example.com"},
			Endpoints: map[string]protocol.EndpointStat{"GET /orders/{id}": {N: 100, Sum: 50, Slow: 10, SlowSum: 30}},
		}},
	}
	// The machine running the application, which reports the same requests and, being alone,
	// also serves an internal host nothing sits in front of.
	app := protocol.Payload{
		BatchID: "app", Agent: protocol.AgentInfo{Host: "app-1", Kind: "agent", Sources: []string{"otlp:otlp"}},
		HTTP: []protocol.HTTPBucket{
			{Source: "otlp:shop", Minute: base.Unix(), N: 100, Sum: 47, Max: 1.9, Hist: hist,
				Sites:     []string{"shop.example.com"},
				Endpoints: map[string]protocol.EndpointStat{"GET /orders/{order}": {N: 100, Sum: 47, Slow: 10, SlowSum: 28}}},
			{Source: "otlp:reporting", Minute: base.Unix(), N: 20, Sum: 40, Max: 5, Hist: hist,
				Sites:     []string{"reports.internal"},
				Endpoints: map[string]protocol.EndpointStat{"GET /report": {N: 20, Sum: 40, Slow: 20, SlowSum: 40}}},
		},
	}
	svc := ingest.New(db)
	for _, payload := range []protocol.Payload{proxy, app} {
		if _, err := svc.Ingest(ctx, p, payload); err != nil {
			t.Fatal(err)
		}
	}

	r := Reader{DB: db}
	snap, err := r.Snapshot(ctx, p.ID, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if all := snap.HTTPFor(""); all.N != 120 {
		t.Fatalf("requests counted: %d, want 120 (100 of the shop once, 20 of the reporting service)", all.N)
	}
	if _, kept := snap.HTTP["otlp:shop"]; kept {
		t.Fatal("the application behind the web server is still counted on its own")
	}
	if _, kept := snap.HTTP["otlp:reporting"]; !kept {
		t.Fatal("a service nothing sits in front of must keep counting")
	}

	costs, err := r.Costs(ctx, p.ID, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	seconds := map[string]float64{}
	for _, c := range costs {
		if c.Kind == "endpoint" {
			seconds[c.Target] = c.Seconds
		}
	}
	if seconds["nginx:shop GET /orders/{id}"] != 30 {
		t.Fatalf("the web server's endpoint must carry the cost: %v", seconds)
	}
	if _, twice := seconds["otlp:shop GET /orders/{order}"]; twice {
		t.Fatalf("the same endpoint is charged twice: %v", seconds)
	}
	if seconds["otlp:reporting GET /report"] != 40 {
		t.Fatalf("the service on its own lost its cost: %v", seconds)
	}
}

// What a source answered for is only true while it keeps saying so: an application that stopped
// serving a host, or one somebody called by hand once, must not keep a pairing alive for ever.
func TestAHostStopsCountingWhenTheSourceStopsSayingIt(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	base := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	hist := make([]int, 15)
	hist[3] = 10

	bucket := func(source, site string, minute time.Time) protocol.HTTPBucket {
		return protocol.HTTPBucket{Source: source, Minute: minute.Unix(), N: 10, Sum: 5, Max: 1, Hist: hist, Sites: []string{site},
			Endpoints: map[string]protocol.EndpointStat{"GET /x": {N: 10, Sum: 5, Slow: 1, SlowSum: 2}}}
	}
	svc := ingest.New(db)
	// Two days ago the application also answered for an internal host nothing sits in front of.
	svc.Now = func() time.Time { return base.Add(-48 * time.Hour) }
	past := protocol.Payload{BatchID: "past", Agent: protocol.AgentInfo{Host: "app", Kind: "agent"},
		HTTP: []protocol.HTTPBucket{bucket("otlp:shop", "internal.local", base.Add(-48*time.Hour))}}
	if _, err := svc.Ingest(ctx, p, past); err != nil {
		t.Fatal(err)
	}
	svc.Now = func() time.Time { return base }
	today := protocol.Payload{BatchID: "today", Agent: protocol.AgentInfo{Host: "app", Kind: "agent"},
		HTTP: []protocol.HTTPBucket{bucket("nginx:shop", "shop.example.com", base), bucket("otlp:shop", "shop.example.com", base)}}
	if _, err := svc.Ingest(ctx, p, today); err != nil {
		t.Fatal(err)
	}

	r := Reader{DB: db}
	snap, err := r.Snapshot(ctx, p.ID, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := snap.HTTP["otlp:shop"]; kept {
		t.Fatal("the host it answered for two days ago is keeping the duplicate alive")
	}
	if all := snap.HTTPFor(""); all.N != 10 {
		t.Fatalf("requests counted: %d, want 10", all.N)
	}
	// Reading a window that includes the old day, the application is not behind anything.
	wide, err := r.Snapshot(ctx, p.ID, base.Add(-72*time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := wide.HTTP["otlp:shop"]; !kept {
		t.Fatal("in a window where it also answered for its own host, it counts on its own")
	}
}

// A checkout that waits on a payment provider, excluded by the team: it leaves the debt and the
// speed index, and its time is still there to be read, apart.
func TestAnExcludedEndpointLeavesTheDebtAndTheScore(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	base := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)

	fast, slow := make([]int, 15), make([]int, 15)
	fast[2] = 90                           // 90 requests around 0.15 s
	slow[protocol.HistogramIndex(8)] = 10  // 10 checkouts of 8 s
	total := make([]int, 15)
	for i := range total {
		total[i] = fast[i] + slow[i]
	}
	payload := protocol.Payload{BatchID: "1", Agent: protocol.AgentInfo{Host: "web", Kind: "agent", Sources: []string{"nginx:shop"}},
		HTTP: []protocol.HTTPBucket{{Source: "nginx:shop", Minute: base.Unix(), N: 100, N1: 10, N2: 10, N5: 10, E5: 2, Sum: 93.5, Max: 9, Hist: total,
			Endpoints: map[string]protocol.EndpointStat{
				"GET /products":  {N: 90, Sum: 13.5, Max: 0.2, Hist: fast},
				"POST /checkout": {N: 10, Sum: 80, Max: 9, Slow: 10, SlowSum: 80, E5: 2, Hist: slow},
			}}}}
	svc := ingest.New(db)
	svc.Now = func() time.Time { return base.Add(time.Hour) }
	if _, err := svc.Ingest(ctx, p, payload); err != nil {
		t.Fatal(err)
	}
	r := Reader{DB: db}
	from, to := base, base.Add(time.Hour)

	before, _ := r.Snapshot(ctx, p.ID, from, to)
	if before.HTTPFor("").Percentile(0.95) < 5 {
		t.Fatalf("p95 before %v", before.HTTPFor("").Percentile(0.95))
	}
	if _, err := exclude.Add(ctx, db, p.ID, exclude.Exclusion{Kind: exclude.KindEndpoint, Target: "POST /checkout", Reason: "external"}, base); err != nil {
		t.Fatal(err)
	}

	costs, err := r.Costs(ctx, p.ID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range costs {
		if c.Label == "POST /checkout" {
			t.Fatalf("still in the debt: %+v", c)
		}
	}
	excluded, err := r.ExcludedCosts(ctx, p.ID, from, to)
	if err != nil || len(excluded) != 1 || excluded[0].Seconds != 80 || excluded[0].Label != "POST /checkout" {
		t.Fatalf("excluded %+v err %v", excluded, err)
	}

	after, _ := r.Snapshot(ctx, p.ID, from, to)
	h := after.HTTPFor("")
	if h.N != 90 || h.N2 != 0 || h.N5 != 0 || h.E5 != 0 || math.Abs(h.Sum-13.5) > 0.001 {
		t.Fatalf("totals after %+v", h)
	}
	if p95 := h.Percentile(0.95); p95 > 0.2 {
		t.Fatalf("p95 after %v", p95)
	}
}

// Rows written by agents older than the endpoint histogram have zeros in it: the excluded
// requests are placed by their average, which is what is known about them.
func TestAnExcludedEndpointFromAnOlderAgentIsEstimated(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	base := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	total := make([]int, 15)
	total[2] = 90
	total[protocol.HistogramIndex(8)] = 10
	payload := protocol.Payload{BatchID: "1", Agent: protocol.AgentInfo{Host: "web", Kind: "agent", Sources: []string{"nginx:shop"}},
		HTTP: []protocol.HTTPBucket{{Source: "nginx:shop", Minute: base.Unix(), N: 100, N2: 10, N5: 10, Sum: 93.5, Max: 9, Hist: total,
			Endpoints: map[string]protocol.EndpointStat{"POST /checkout": {N: 10, Sum: 80, Max: 9, Slow: 10, SlowSum: 80}}}}}
	svc := ingest.New(db)
	svc.Now = func() time.Time { return base.Add(time.Hour) }
	if _, err := svc.Ingest(ctx, p, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := exclude.Add(ctx, db, p.ID, exclude.Exclusion{Kind: exclude.KindEndpoint, Target: "POST /checkout", Reason: "external"}, base); err != nil {
		t.Fatal(err)
	}
	after, _ := Reader{DB: db}.Snapshot(ctx, p.ID, base, base.Add(time.Hour))
	h := after.HTTPFor("")
	if h.N != 90 || h.N5 != 0 || h.Percentile(0.95) > 0.2 {
		t.Fatalf("totals after %+v p95 %v", h, h.Percentile(0.95))
	}
}

// A query of a library and a PHP frame inside a vendor's code: the team cannot fix them, leaves
// them out, and they stop weighing on the debt and on the speed index, while their time stays
// readable apart.
func TestAnExcludedQueryAndPHPFrameLeaveTheDebtAndTheScore(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	base := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	sql := "select payload from failed_jobs where uuid = ?"
	fp := fingerprint.Of(sql)
	hash := fingerprint.Hash(fp)
	frame := "curl_exec() vendor/guzzlehttp/guzzle/src/Handler/CurlHandler.php:85"

	svc := ingest.New(db)
	svc.Now = func() time.Time { return base.Add(time.Hour) }
	_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "1",
		Agent: protocol.AgentInfo{Host: "db", Kind: "agent", Sources: []string{"mysql-slowlog:mysql-slowlog", "php-fpm:shop"}},
		DB: []protocol.DBBucket{{Source: "mysql-slowlog", Minute: base.Unix(), SlowN: 10, SlowSum: 40, SlowMax: 6,
			Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, Sample: sql, N: 10, Sum: 40, Max: 6}}}},
		PHP: []protocol.PHPBucket{{Source: "php-fpm:shop", Minute: base.Unix(), Stalls: []protocol.PHPStall{{Frame: frame, Code: "app/Jobs/Send.php:20", N: 6}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := Reader{DB: db}
	from, to := base, base.Add(time.Hour)
	before, err := r.Snapshot(ctx, p.ID, from, to)
	if err != nil || before.DB.SlowSum != 40 || before.PHPStalls != 6 {
		t.Fatalf("before: %+v err %v", before, err)
	}

	for _, e := range []exclude.Exclusion{
		{Kind: exclude.KindQuery, Target: hash, Reason: "third_party"},
		{Kind: exclude.KindPHP, Target: frame, Reason: "external"},
	} {
		if _, err := exclude.Add(ctx, db, p.ID, e, base); err != nil {
			t.Fatal(err)
		}
	}

	costs, err := r.Costs(ctx, p.ID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range costs {
		if c.Target == hash || c.Target == frame {
			t.Fatalf("still in the debt: %+v", c)
		}
	}
	excluded, err := r.ExcludedCosts(ctx, p.ID, from, to)
	if err != nil || len(excluded) != 2 {
		t.Fatalf("excluded %+v err %v", excluded, err)
	}

	after, err := r.Snapshot(ctx, p.ID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if after.DB.SlowSum != 0 || after.DB.SlowN != 0 || after.PHPStalls != 0 {
		t.Fatalf("after: slow %v/%d stalls %d", after.DB.SlowSum, after.DB.SlowN, after.PHPStalls)
	}
}
