package score

import (
	"math"
	"testing"

	"github.com/getslowpoke/slowpoke/internal/server/metrics"
	"github.com/getslowpoke/slowpoke/internal/server/project"
)

func TestFractionIsLogarithmicBetweenThresholds(t *testing.T) {
	if Fraction(0.1, 0.3, 1.5) != 1 || Fraction(2, 0.3, 1.5) != 0 {
		t.Fatal("clamping")
	}
	// geometric mean of good and bad is exactly half way
	if got := Fraction(math.Sqrt(0.3*1.5), 0.3, 1.5); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("got %v", got)
	}
}

func snapshot(n, n2, e5 int64, p95Slot int, stalls int64, slowSum, slowMax, hours float64) metrics.Snapshot {
	t := metrics.HTTPTotals{N: n, N2: n2, E5: e5, Max: 1}
	t.Hist[p95Slot] = n
	s := metrics.Snapshot{HTTP: map[string]metrics.HTTPTotals{"nginx:app": t}, PHPStalls: stalls, Hours: hours}
	s.DB.SlowSum, s.DB.SlowMax = slowSum, slowMax
	s.Has.HTTP, s.Has.PHP, s.Has.DB = true, true, true
	return s
}

func TestPerfectDayScores100(t *testing.T) {
	r := Compute(project.Defaults(), snapshot(100000, 0, 0, 1, 0, 10, 1, 24))
	if r.Total == nil || *r.Total != 100 || r.Partial {
		t.Fatalf("%+v", r)
	}
}

func TestTerribleDayScores0(t *testing.T) {
	r := Compute(project.Defaults(), snapshot(1000, 900, 500, 14, 900, 50000, 300, 24))
	if r.Total == nil || *r.Total != 0 {
		t.Fatalf("%v", *r.Total)
	}
	for _, c := range r.Components {
		if c.Lost != c.Weight {
			t.Fatalf("%+v", c)
		}
	}
}

func TestMissingSourcesMakeThePartialScoreRelative(t *testing.T) {
	s := snapshot(100000, 0, 0, 1, 0, 0, 0, 24)
	s.Has.PHP, s.Has.DB = false, false
	r := Compute(project.Defaults(), s)
	if !r.Partial || r.Total == nil || *r.Total != 100 {
		t.Fatalf("%+v", r)
	}
	unavailable := 0
	for _, c := range r.Components {
		if !c.Available {
			unavailable++
		}
	}
	if unavailable != 3 {
		t.Fatalf("php_stalls, db_time, db_worst should be unavailable, got %d", unavailable)
	}
}

func TestNoDataNoScore(t *testing.T) {
	if r := Compute(project.Defaults(), metrics.Snapshot{}); r.Total != nil {
		t.Fatalf("%v", *r.Total)
	}
}

func TestComponentSourceFilterAndCustomWeights(t *testing.T) {
	s := project.Defaults()
	s.Score.Components = []project.Component{
		{Key: "api_p95", Label: "API p95", Metric: "http_p95", Source: "nginx:api", Weight: 1, Good: 0.3, Bad: 1.5},
	}
	snap := snapshot(100, 0, 0, 1, 0, 0, 0, 1)
	slowAPI := metrics.HTTPTotals{N: 100, Max: 20}
	slowAPI.Hist[12] = 100
	snap.HTTP["nginx:api"] = slowAPI

	r := Compute(s, snap)
	if *r.Total != 0 {
		t.Fatalf("only the api source must count: %v %+v", *r.Total, r.Components)
	}
}

func TestCustomMetricComponents(t *testing.T) {
	s := project.Defaults()
	s.Score.Components = []project.Component{
		{Key: "pdf", Label: "Invoice PDF p95", Metric: "custom_p95", Name: "invoices.pdf", Weight: 1, Good: 1, Bad: 10},
		{Key: "failures", Label: "Checkout failures per hour", Metric: "custom_per_hour", Name: "checkout.failed", Weight: 1, Good: 1, Bad: 50},
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	snap := metrics.Snapshot{Hours: 2, Custom: map[string]metrics.CustomTotals{}}
	pdf := metrics.CustomTotals{Kind: "timer", Count: 10, Max: 0.5}
	pdf.Hist[3] = 10
	snap.Custom["invoices.pdf"] = pdf
	snap.Custom["checkout.failed"] = metrics.CustomTotals{Kind: "counter", Count: 3, Sum: 200}

	r := Compute(s, snap)
	if r.Total == nil || *r.Total != 50 {
		t.Fatalf("fast pdf (full) + 100 failures/hour (zero) = 50, got %+v", r)
	}

	s.Score.Components[0].Name = ""
	if s.Validate() == nil {
		t.Fatal("custom component without name must be invalid")
	}
}
