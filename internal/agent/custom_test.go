package agent

import (
	"math"
	"testing"
	"time"
)

func TestCustomMetricsAggregation(t *testing.T) {
	a := NewAggregator()
	a.AddMetric(t0, "invoices.pdf", "timer", 1.5)
	a.AddMetric(t0.Add(time.Second), "invoices.pdf", "timer", 0.5)
	a.AddMetric(t0, "checkout.failed", "counter", 1)
	a.AddMetric(t0, "checkout.failed", "counter", 2)
	a.AddMetric(t0, "queue.backlog", "gauge", 10)
	a.AddMetric(t0.Add(2*time.Second), "queue.backlog", "gauge", 4)

	byName := map[string]struct {
		count            int64
		sum, min, max, l float64
		hist             []int
	}{}
	for _, m := range a.Drain(t0.Add(time.Minute)).Metrics {
		byName[m.Name] = struct {
			count            int64
			sum, min, max, l float64
			hist             []int
		}{m.Count, m.Sum, m.Min, m.Max, m.Last, m.Hist}
	}
	pdf := byName["invoices.pdf"]
	if pdf.count != 2 || pdf.sum != 2 || pdf.min != 0.5 || pdf.max != 1.5 || pdf.hist[4] != 1 || pdf.hist[7] != 1 {
		t.Fatalf("timer %+v", pdf)
	}
	if c := byName["checkout.failed"]; c.count != 2 || c.sum != 3 {
		t.Fatalf("counter %+v", c)
	}
	if g := byName["queue.backlog"]; g.l != 4 || g.max != 10 || g.min != 4 {
		t.Fatalf("gauge %+v", g)
	}
}

func TestStatsDLines(t *testing.T) {
	a := NewAggregator()
	now := func() time.Time { return t0 }
	for _, line := range []string{
		"checkout.failed:1|c",
		"checkout.failed:3|c|@0.5", // sampled: counts 6
		"invoices.pdf:250|ms",
		"invoices.pdf:750|ms|#env:prod",
		"queue.backlog:42|g",
		"api.latency:0.2|h",
		"broken line",
		"bad:value|c",
	} {
		ParseStatsD(a, now, line)
	}
	m := map[string][3]float64{}
	for _, b := range a.Drain(t0.Add(time.Minute)).Metrics {
		m[b.Name] = [3]float64{float64(b.Count), b.Sum, b.Last}
	}
	if m["checkout.failed"][1] != 7 {
		t.Fatalf("counter %v", m["checkout.failed"])
	}
	if pdf := m["invoices.pdf"]; pdf[0] != 2 || math.Abs(pdf[1]-1.0) > 1e-9 {
		t.Fatalf("timer ms->s %v", pdf)
	}
	if m["queue.backlog"][2] != 42 || m["api.latency"][0] != 1 {
		t.Fatalf("%v", m)
	}
	if len(m) != 4 {
		t.Fatalf("invalid lines must be ignored: %v", m)
	}
}

func TestPrometheusScrapeTurnsCountersIntoDeltas(t *testing.T) {
	a := NewAggregator()
	p := newPromScraper(Source{Name: "app"})
	first := `# HELP http_requests_total Requests
# TYPE http_requests_total counter
http_requests_total{code="200"} 100
http_requests_total{code="500"} 5
# TYPE queue_depth gauge
queue_depth 7
# TYPE go_gc_duration_seconds summary
go_gc_duration_seconds{quantile="0.5"} 0.001
`
	second := `# TYPE http_requests_total counter
http_requests_total{code="200"} 130
http_requests_total{code="500"} 6
# TYPE queue_depth gauge
queue_depth 3
`
	p.ingest(a, t0, first)
	p.ingest(a, t0.Add(30*time.Second), second)

	got := map[string]float64{}
	for _, b := range a.Drain(t0.Add(time.Minute)).Metrics {
		if b.Kind == "gauge" {
			got[b.Name] = b.Last
		} else {
			got[b.Name] = b.Sum
		}
	}
	if got[`app.http_requests_total{code="200"}`] != 30 || got[`app.http_requests_total{code="500"}`] != 1 || got["app.queue_depth"] != 3 {
		t.Fatalf("%v", got)
	}
	if _, ok := got[`app.go_gc_duration_seconds{quantile="0.5"}`]; ok {
		t.Fatal("summaries are not supported and must be skipped")
	}
}
