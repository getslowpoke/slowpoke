package agent

import (
	"fmt"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)

func TestRequestsAreGroupedPerMinute(t *testing.T) {
	a := NewAggregator()
	a.AddRequest("nginx:app", t0.Add(5*time.Second), "GET", "/orders/15", 200, 0.12, 1)
	a.AddRequest("nginx:app", t0.Add(50*time.Second), "GET", "/orders/16", 502, 2.5, 1)
	a.AddRequest("nginx:app", t0.Add(70*time.Second), "POST", "/cart", 200, 0.04, 1)

	p := a.Drain(t0.Add(2 * time.Minute))
	if len(p.HTTP) != 2 {
		t.Fatalf("want 2 buckets, got %d", len(p.HTTP))
	}
	b := p.HTTP[0]
	if b.Minute != t0.Unix() || b.N != 2 || b.N1 != 1 || b.N2 != 1 || b.N5 != 0 || b.E5 != 1 {
		t.Fatalf("unexpected bucket %+v", b)
	}
	if b.Max != 2.5 || fmt.Sprintf("%.2f", b.Sum) != "2.62" {
		t.Fatalf("sum/max %+v", b)
	}
	if b.Hist[2] != 1 || b.Hist[9] != 1 {
		t.Fatalf("histogram %v", b.Hist)
	}
	e := b.Endpoints["GET /orders/{id}"]
	if e.N != 2 || e.Slow != 1 || e.SlowSum != 2.5 || e.E5 != 1 {
		t.Fatalf("endpoint %+v", e)
	}
}

func TestDrainKeepsOpenMinutes(t *testing.T) {
	a := NewAggregator()
	a.AddRequest("nginx:app", t0, "GET", "/", 200, 0.1, 1)
	a.AddRequest("nginx:app", t0.Add(61*time.Second), "GET", "/", 200, 0.1, 1)

	if p := a.Drain(t0.Add(time.Minute)); len(p.HTTP) != 1 {
		t.Fatalf("only the closed minute must be drained, got %d", len(p.HTTP))
	}
	if p := a.Drain(t0.Add(time.Minute)); len(p.HTTP) != 0 {
		t.Fatalf("already drained")
	}
	if p := a.Drain(t0.Add(2 * time.Minute)); len(p.HTTP) != 1 {
		t.Fatalf("second minute must drain later")
	}
}

func TestEndpointCardinalityIsCapped(t *testing.T) {
	a := NewAggregator()
	for i := 0; i < maxEndpointsPerMinute+50; i++ {
		a.AddRequest("nginx:app", t0, "GET", fmt.Sprintf("/page-%c%d", 'a'+i%26, i), 200, 0.1, 1)
	}
	b := a.Drain(t0.Add(time.Minute)).HTTP[0]
	if len(b.Endpoints) != maxEndpointsPerMinute+1 {
		t.Fatalf("want %d endpoints (with OTHER), got %d", maxEndpointsPerMinute+1, len(b.Endpoints))
	}
	if b.Endpoints[otherEndpoint].N != 50 {
		t.Fatalf("OTHER must absorb the rest: %+v", b.Endpoints[otherEndpoint])
	}
}

func TestSlowQueriesAndStalls(t *testing.T) {
	a := NewAggregator()
	q := SlowQuery{Time: t0.Add(3 * time.Second), User: "app", Database: "shop", Seconds: 1.5, Rows: 1000, SQL: "select * from orders where id = 5"}
	a.AddSlowQuery("mysql-slowlog", q)
	q.Seconds, q.SQL = 3, "select * from orders where id = 9"
	a.AddSlowQuery("mysql-slowlog", q)
	a.AddPHPStall("php-fpm:www", t0, "sleep() /app/a.php:3", "app/Http/X.php:10")
	a.AddPHPStall("php-fpm:www", t0, "sleep() /app/a.php:3", "app/Http/X.php:10")

	p := a.Drain(t0.Add(time.Minute))
	db := p.DB[0]
	if db.SlowN != 2 || db.SlowSum != 4.5 || db.SlowMax != 3 || len(db.Queries) != 1 {
		t.Fatalf("db %+v", db)
	}
	for _, qs := range db.Queries {
		if qs.N != 2 || qs.Rows != 2000 || qs.Sample != "select * from orders where id = 0" || qs.Fingerprint != "select * from orders where id = ?" {
			t.Fatalf("query %+v", qs)
		}

	}
	if p.PHP[0].N != 2 || len(p.PHP[0].Stalls) != 1 || p.PHP[0].Stalls[0].N != 2 {
		t.Fatalf("php %+v", p.PHP[0])
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"/api/orders/4848337/items?x=1":                                 "/api/orders/{id}/items",
		"/carica/e598035f-1c2d-4e5f-9a8b-7c6d5e4f3a2b":                  "/carica/{uuid}",
		"/files/a3f9c2e1b4d5a6f7e8d9":                                   "/files/{hash}",
		"/reset/" + "Zx9_" + "abcdefghijklmnopqrstuvwxyz0123456789ABCD": "/reset/{token}",
		"":  "/",
		"/": "/",
	}
	for in, want := range cases {
		if got := NormalizePath(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestRealSamplesStayOnTheMachineUnlessShared(t *testing.T) {
	sql := "select * from users where email = 'ada@example.com'"
	for _, share := range []bool{false, true} {
		a := NewAggregator()
		a.ShareSamples = share
		a.AddSlowQuery("mysql-slowlog", SlowQuery{Time: t0, Seconds: 2, SQL: sql})
		for hash, q := range a.Drain(t0.Add(time.Minute)).DB[0].Queries {
			local, _, ok := a.Samples.Get(hash)
			if !ok || local != sql {
				t.Fatalf("local sample %q", local)
			}
			want := "select * from users where email = 's'"
			if share {
				want = sql
			}
			if q.Sample != want {
				t.Fatalf("share=%v sent %q", share, q.Sample)
			}
		}
	}
}

func TestSampleStoreKeepsSlowestAndEvictsOldest(t *testing.T) {
	s := NewSampleStore(2)
	s.Put("a", "fast", 1, SampleOwner{})
	s.Put("a", "slow", 3, SampleOwner{})
	s.Put("a", "faster", 0.5, SampleOwner{})
	s.Put("b", "b", 1, SampleOwner{})
	s.Put("c", "c", 1, SampleOwner{})
	if v, _, _ := s.Get("b"); v != "b" {
		t.Fatal("b")
	}
	if _, _, ok := s.Get("a"); ok {
		t.Fatal("oldest must be evicted")
	}
}

// The host travels with the traffic: it is how a server recognises that the web server on one
// machine and the application on another are reporting the same requests.
func TestBucketsCarryTheHostsOfTheirSource(t *testing.T) {
	a := NewAggregator()
	at := time.Now().Add(-2 * time.Minute)
	a.SourceSite("nginx:web", "Shop.Example.com:8443") // written as configured
	a.SourceSite("nginx:web", "shop.example.com")      // the same host, said twice
	a.SourceSite("nginx:web", "")                      // nothing to say
	a.SourceSite("nginx:web", "api.example.com")
	a.AddRequest("nginx:web", at, "GET", "/orders/1", 200, 0.1, 1)
	a.AddRequest("otlp:reporting", at, "GET", "/reports", 200, 0.2, 1)

	sites := map[string][]string{}
	for _, b := range a.Drain(time.Now()).HTTP {
		sites[b.Source] = b.Sites
	}
	if got := sites["nginx:web"]; len(got) != 2 || got[0] != "api.example.com" || got[1] != "shop.example.com" {
		t.Fatalf("hosts of the web source: %v", got)
	}
	if got := sites["otlp:reporting"]; len(got) != 0 {
		t.Fatalf("a source that never said its host must claim none: %v", got)
	}
}

// Bounded like every other map here: a service answering a thousand hosts cannot grow the agent.
func TestTheHostsOfASourceAreBounded(t *testing.T) {
	a := NewAggregator()
	for i := 0; i < 50; i++ {
		a.SourceSite("nginx:web", fmt.Sprintf("host%d.example.com", i))
	}
	a.AddRequest("nginx:web", time.Now().Add(-2*time.Minute), "GET", "/", 200, 0.1, 1)
	for _, b := range a.Drain(time.Now()).HTTP {
		if len(b.Sites) > maxSitesPerSource {
			t.Fatalf("%d hosts kept", len(b.Sites))
		}
	}
}

// A host a source stopped answering for stops travelling with its buckets: the server would
// otherwise keep believing a pairing that is no longer true.
func TestAHostIsForgottenWhenNobodyAnswersForItAnymore(t *testing.T) {
	a := NewAggregator()
	a.SourceSite("otlp:shop", "internal.local")
	a.sites["otlp:shop"]["internal.local"] = time.Now().Add(-2 * siteForgottenAfter)
	a.SourceSite("otlp:shop", "shop.example.com")
	a.AddRequest("otlp:shop", time.Now().Add(-2*time.Minute), "GET", "/x", 200, 0.1, 1)

	for _, b := range a.Drain(time.Now()).HTTP {
		if len(b.Sites) != 1 || b.Sites[0] != "shop.example.com" {
			t.Fatalf("hosts %v, want only the one it still answers for", b.Sites)
		}
	}
}
