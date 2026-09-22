package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// elasticsearchSource reads what Elasticsearch (or OpenSearch) already knows about itself: how
// much time it spent searching, how much indexing, what it had to reject, and how the cluster is.
// Two GETs per round, nothing written, no index read: queries and documents never leave the
// machine, only counters.
//
// What the node reports are totals since it started; a person wants the last minute, so the
// increases are what this source sends. A node that restarted starts again from where it is.
type elasticsearchSource struct {
	src    Source
	base   string
	client *http.Client
	prev   map[string]float64
	seen   bool
}

const elasticTimeout = 8 * time.Second

func newElasticsearchSource(src Source) (*elasticsearchSource, error) {
	raw := src.URL
	if raw == "" {
		raw = "http://127.0.0.1:9200"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("url %q is not an address of the cluster (http://127.0.0.1:9200)", raw)
	}
	if src.Name == "" {
		src.Name = "elasticsearch"
	}
	return &elasticsearchSource{
		src:    src,
		base:   strings.TrimRight(u.String(), "/"),
		client: &http.Client{Timeout: elasticTimeout},
		prev:   map[string]float64{},
	}, nil
}

func (e *elasticsearchSource) poll(ctx context.Context, agg *Aggregator, now time.Time) error {
	var health struct {
		Status           string  `json:"status"`
		Nodes            float64 `json:"number_of_nodes"`
		ActiveShards     float64 `json:"active_shards"`
		UnassignedShards float64 `json:"unassigned_shards"`
	}
	if err := e.get(ctx, "/_cluster/health", &health); err != nil {
		return err
	}
	gauge := func(metric string, v float64) { agg.AddMetric(now, "elasticsearch."+e.src.Name+"."+metric, "gauge", v) }
	// Green, yellow, red as 0, 1, 2: a chart of it is flat until the day it is not.
	gauge("status", map[string]float64{"green": 0, "yellow": 1, "red": 2}[health.Status])
	gauge("nodes", health.Nodes)
	gauge("active_shards", health.ActiveShards)
	gauge("unassigned_shards", health.UnassignedShards)

	var stats struct {
		Nodes map[string]struct {
			Indices struct {
				Search struct {
					QueryTotal   float64 `json:"query_total"`
					QueryMillis  float64 `json:"query_time_in_millis"`
					FetchTotal   float64 `json:"fetch_total"`
					FetchMillis  float64 `json:"fetch_time_in_millis"`
				} `json:"search"`
				Indexing struct {
					IndexTotal  float64 `json:"index_total"`
					IndexMillis float64 `json:"index_time_in_millis"`
				} `json:"indexing"`
				Docs struct {
					Count float64 `json:"count"`
				} `json:"docs"`
			} `json:"indices"`
			JVM struct {
				Mem struct {
					HeapPercent float64 `json:"heap_used_percent"`
				} `json:"mem"`
			} `json:"jvm"`
			ThreadPool map[string]struct {
				Queue    float64 `json:"queue"`
				Rejected float64 `json:"rejected"`
			} `json:"thread_pool"`
		} `json:"nodes"`
	}
	if err := e.get(ctx, "/_nodes/stats/indices,jvm,thread_pool", &stats); err != nil {
		return err
	}

	// The cluster is the sum of its nodes; the heap is the worst of them, because that is the one
	// that will fall over.
	var queries, queryMillis, fetches, fetchMillis, indexed, indexMillis, docs float64
	var searchQueue, writeQueue, searchRejected, writeRejected, heap float64
	for _, n := range stats.Nodes {
		queries += n.Indices.Search.QueryTotal
		queryMillis += n.Indices.Search.QueryMillis
		fetches += n.Indices.Search.FetchTotal
		fetchMillis += n.Indices.Search.FetchMillis
		indexed += n.Indices.Indexing.IndexTotal
		indexMillis += n.Indices.Indexing.IndexMillis
		docs += n.Indices.Docs.Count
		if n.JVM.Mem.HeapPercent > heap {
			heap = n.JVM.Mem.HeapPercent
		}
		searchQueue += n.ThreadPool["search"].Queue
		writeQueue += n.ThreadPool["write"].Queue
		searchRejected += n.ThreadPool["search"].Rejected
		writeRejected += n.ThreadPool["write"].Rejected
	}
	gauge("heap_percent", heap)
	gauge("docs", docs)
	gauge("search_queue", searchQueue)
	gauge("write_queue", writeQueue)

	counter := func(metric string, total float64) float64 {
		d := e.delta(metric, total)
		if e.seen && d > 0 {
			agg.AddMetric(now, "elasticsearch."+e.src.Name+"."+metric, "counter", d)
		}
		return d
	}
	searched := counter("search_queries", queries)
	seconds := counter("search_seconds", queryMillis/1000)
	counter("fetches", fetches)
	counter("fetch_seconds", fetchMillis/1000)
	counter("indexed", indexed)
	counter("indexing_seconds", indexMillis/1000)
	counter("search_rejected", searchRejected)
	counter("write_rejected", writeRejected)
	// What a person asks first: how long a search takes here, right now.
	if e.seen && searched > 0 {
		gauge("search_avg_ms", seconds/searched*1000)
	}
	e.seen = true
	return nil
}

// delta is the increase since the last round, and zero when the counter went backwards: a node
// that restarted has its totals back at the beginning, which is not a negative minute.
func (e *elasticsearchSource) delta(key string, total float64) float64 {
	previous, had := e.prev[key]
	e.prev[key] = total
	if !had || total < previous {
		return 0
	}
	return total - previous
}

func (e *elasticsearchSource) get(ctx context.Context, path string, into any) error {
	ctx, cancel := context.WithTimeout(ctx, elasticTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.base+path, nil)
	if err != nil {
		return err
	}
	if e.src.User != "" || e.src.Password != "" {
		req.SetBasicAuth(e.src.User, e.src.Password)
	}
	res, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d: is the cluster up, and may this user read it?", e.base+path, res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%s did not answer like a cluster: %w", e.base+path, err)
	}
	return nil
}
