package agent

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// The numbers Elasticsearch keeps are totals since the node started: what a person wants to know
// is what happened in the last minute, so the source reports the increase.
func elasticServer(t *testing.T, health map[string]any, stats map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/_cluster/health"):
			_ = json.NewEncoder(w).Encode(health)
		case strings.HasPrefix(r.URL.Path, "/_nodes/stats"):
			_ = json.NewEncoder(w).Encode(stats)
		default:
			http.NotFound(w, r)
		}
	}))
}

// nodeStats is the shape Elasticsearch and OpenSearch both answer with.
func nodeStats(queries, queryMillis, fetches, fetchMillis, indexed, indexMillis int, heap int, searchRejected, writeRejected int) map[string]any {
	return map[string]any{
		"nodes": map[string]any{
			"n1": map[string]any{
				"name": "node-1",
				"indices": map[string]any{
					"search": map[string]any{
						"query_total": queries, "query_time_in_millis": queryMillis,
						"fetch_total": fetches, "fetch_time_in_millis": fetchMillis,
					},
					"indexing": map[string]any{"index_total": indexed, "index_time_in_millis": indexMillis},
					"docs":     map[string]any{"count": 1000},
				},
				"jvm": map[string]any{"mem": map[string]any{"heap_used_percent": heap}},
				"thread_pool": map[string]any{
					"search": map[string]any{"queue": 3, "rejected": searchRejected},
					"write":  map[string]any{"queue": 1, "rejected": writeRejected},
				},
			},
		},
	}
}

func TestElasticsearchReportsTheTimeSpentSearchingInTheLastMinute(t *testing.T) {
	health := map[string]any{"status": "yellow", "number_of_nodes": 1, "unassigned_shards": 4, "active_shards": 10}
	stats := nodeStats(100, 5000, 100, 500, 50, 1000, 71, 0, 0)
	srv := elasticServer(t, health, stats)
	defer srv.Close()

	s, err := newElasticsearchSource(Source{Type: "elasticsearch", Name: "search", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	a := NewAggregator()
	now := time.Now()
	// The first round only learns where the counters are: it invents no spike in the past.
	if err := s.poll(context.Background(), a, now); err != nil {
		t.Fatal(err)
	}
	first := metricsByName(drainAll(a))
	for _, counter := range []string{"elasticsearch.search.search_queries", "elasticsearch.search.search_seconds"} {
		if _, ok := first[counter]; ok {
			t.Errorf("%s was reported on the first round", counter)
		}
	}
	if v, ok := first["elasticsearch.search.heap_percent"]; !ok || v != 71 {
		t.Errorf("heap_percent = %v (%v): a gauge is true right away", v, ok)
	}
	if v, ok := first["elasticsearch.search.unassigned_shards"]; !ok || v != 4 {
		t.Errorf("unassigned_shards = %v (%v)", v, ok)
	}
	if v, ok := first["elasticsearch.search.status"]; !ok || v != 1 {
		t.Errorf("status = %v (%v), yellow is 1", v, ok)
	}

	// A minute later: 200 more searches and four more seconds of searching.
	*s = *mustElastic(t, srv.URL)
	_ = s.poll(context.Background(), NewAggregator(), now)
	srv.Config.Handler = elasticHandler(health, nodeStats(300, 9000, 300, 900, 60, 1200, 68, 2, 0))
	a = NewAggregator()
	if err := s.poll(context.Background(), a, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := metricsByName(a.Drain(time.Now().Add(time.Hour)))
	for name, want := range map[string]float64{
		"elasticsearch.search.search_queries":  200,
		"elasticsearch.search.search_seconds":  4,
		"elasticsearch.search.indexed":         10,
		"elasticsearch.search.indexing_seconds": 0.2,
		"elasticsearch.search.search_rejected": 2,
		"elasticsearch.search.heap_percent":    68,
		// 4 seconds over 200 searches: the average search took 20 ms.
		"elasticsearch.search.search_avg_ms": 20,
	} {
		// Milliseconds turned into seconds do not land on round numbers: near enough is the truth.
		if v, ok := got[name]; !ok || math.Abs(v-want) > 1e-9 {
			t.Errorf("%s = %v (present %v), want %v", name, v, ok, want)
		}
	}
}

func TestElasticsearchSurvivesARestartAndAnUnreachableCluster(t *testing.T) {
	health := map[string]any{"status": "green", "number_of_nodes": 1}
	srv := elasticServer(t, health, nodeStats(500, 5000, 0, 0, 0, 0, 40, 0, 0))
	defer srv.Close()
	s := mustElastic(t, srv.URL)
	now := time.Now()
	_ = s.poll(context.Background(), NewAggregator(), now)

	// The node restarted: its totals went back to zero, which is not minus five hundred searches.
	srv.Config.Handler = elasticHandler(health, nodeStats(7, 70, 0, 0, 0, 0, 41, 0, 0))
	a := NewAggregator()
	if err := s.poll(context.Background(), a, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := metricsByName(drainAll(a))
	if v, ok := got["elasticsearch.search.search_queries"]; ok && v < 0 {
		t.Fatalf("a restart reported %v searches", v)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()
	if err := mustElastic(t, down.URL).poll(context.Background(), NewAggregator(), now); err == nil {
		t.Fatal("a cluster that answers 503 must be an error, not silence")
	}
}

func TestElasticsearchAgainstARealCluster(t *testing.T) {
	url := os.Getenv("ELASTICSEARCH_URL")
	if url == "" {
		t.Skip("set ELASTICSEARCH_URL to run this against a cluster")
	}
	s := mustElastic(t, url)
	now := time.Now()
	if err := s.poll(context.Background(), NewAggregator(), now); err != nil {
		t.Fatal(err)
	}

	// Real searches between the two rounds: this is the minute a person would be looking at.
	const searches = 20
	for i := 0; i < searches; i++ {
		res, err := http.Get(url + "/_search?q=*:*")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}

	a := NewAggregator()
	if err := s.poll(context.Background(), a, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := metricsByName(drainAll(a))
	for _, name := range []string{"elasticsearch.search.heap_percent", "elasticsearch.search.status", "elasticsearch.search.nodes"} {
		if _, ok := got[name]; !ok {
			t.Errorf("%s missing from %v", name, got)
		}
	}
	if got["elasticsearch.search.search_queries"] < searches {
		t.Errorf("%d searches went in, the cluster reported %v", searches, got["elasticsearch.search.search_queries"])
	}
	if got["elasticsearch.search.search_seconds"] <= 0 || got["elasticsearch.search.search_avg_ms"] <= 0 {
		t.Errorf("searching took no time at all: %v", got)
	}
	t.Logf("from the cluster: %v", got)
}

func mustElastic(t *testing.T, url string) *elasticsearchSource {
	t.Helper()
	s, err := newElasticsearchSource(Source{Type: "elasticsearch", Name: "search", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func elasticHandler(health, stats map[string]any) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/_cluster/health") {
			_ = json.NewEncoder(w).Encode(health)
			return
		}
		_ = json.NewEncoder(w).Encode(stats)
	})
}

// metricsByName is the value a chart would show: the last one for a gauge, the total for a counter.
func metricsByName(d Drained) map[string]float64 {
	out := map[string]float64{}
	for _, m := range d.Metrics {
		// A counter is its increase and a timer is the time it stands for; a gauge is where it is.
		if m.Kind == "counter" || m.Kind == "timer" {
			out[m.Name] = m.Sum
			continue
		}
		out[m.Name] = m.Last
	}
	return out
}
