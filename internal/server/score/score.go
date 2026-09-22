// Package score turns a metrics snapshot into the speed index (0-100).
package score

import (
	"math"

	"github.com/getslowpoke/slowpoke/internal/server/metrics"
	"github.com/getslowpoke/slowpoke/internal/server/project"
)

type ComponentResult struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	Metric    string   `json:"metric"`
	Weight    float64  `json:"weight"`
	Good      float64  `json:"good"`
	Bad       float64  `json:"bad"`
	Available bool     `json:"available"`
	Value     *float64 `json:"value"`
	Points    float64  `json:"points"`
	Lost      float64  `json:"lost"`
}

type Result struct {
	Total      *float64          `json:"total"`
	Partial    bool              `json:"partial"`
	Components []ComponentResult `json:"components"`
}

// Fraction is 1 at or below good, 0 at or above bad, logarithmic in between:
// halving a slow time is worth the same everywhere on the scale.
func Fraction(value, good, bad float64) float64 {
	if value <= good {
		return 1
	}
	if value >= bad {
		return 0
	}
	return 1 - (math.Log(value)-math.Log(good))/(math.Log(bad)-math.Log(good))
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func value(c project.Component, s metrics.Snapshot) (float64, bool) {
	httpT := s.HTTPFor(c.Source)
	all := s.HTTPFor("")
	switch c.Metric {
	case "http_slow_ratio":
		if httpT.N == 0 {
			return 0, false
		}
		return float64(httpT.N2) / float64(httpT.N), true
	case "http_p95":
		if httpT.N == 0 {
			return 0, false
		}
		return httpT.Percentile(0.95), true
	case "http_error_rate":
		if httpT.N == 0 {
			return 0, false
		}
		return float64(httpT.E5) / float64(httpT.N) * 10000, true
	case "php_stalls":
		if !s.Has.PHP || all.N == 0 {
			return 0, false
		}
		return float64(s.PHPStalls) / float64(all.N) * 1000, true
	case "db_slow_per_hour":
		if !s.Has.DB || s.Hours <= 0 {
			return 0, false
		}
		return s.DB.SlowSum / math.Max(0.5, s.Hours), true
	case "custom_p95", "custom_avg", "custom_max", "custom_per_hour":
		m, ok := s.Custom[c.Name]
		if !ok || m.Count == 0 {
			return 0, false
		}
		switch c.Metric {
		case "custom_p95":
			return m.Percentile(0.95), m.Kind == "timer"
		case "custom_avg":
			return m.Sum / float64(m.Count), true
		case "custom_max":
			return m.Max, true
		default:
			return m.Sum / math.Max(0.5, s.Hours), m.Kind == "counter"
		}
	case "db_worst":
		if !s.Has.DB || s.Hours <= 0 {
			return 0, false
		}
		return s.DB.SlowMax, true
	}
	return 0, false
}

func Compute(settings project.Settings, s metrics.Snapshot) Result {
	var r Result
	var got, possible float64

	for _, c := range settings.Score.Components {
		v, ok := value(c, s)
		cr := ComponentResult{Key: c.Key, Label: c.Label, Metric: c.Metric, Weight: c.Weight, Good: c.Good, Bad: c.Bad, Available: ok}
		if ok {
			rounded := math.Round(v*10000) / 10000
			cr.Value = &rounded
			// Values at or below "good" can be zero: Fraction handles them before the logarithm.
			cr.Points = round1(c.Weight * Fraction(v, c.Good, c.Bad))
			cr.Lost = round1(c.Weight - cr.Points)
			got += cr.Points
			possible += c.Weight
		} else {
			r.Partial = true
		}
		r.Components = append(r.Components, cr)
	}
	if possible > 0 {
		total := round1(got / possible * 100)
		r.Total = &total
	}
	return r
}
