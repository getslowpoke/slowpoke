package game

import (
	"context"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/metrics"
	"github.com/getslowpoke/slowpoke/internal/server/project"
)

type Debt struct {
	SecondsPerDay         float64        `json:"seconds_per_day"`
	PreviousSecondsPerDay float64        `json:"previous_seconds_per_day"`
	RepaidSecondsPerDay   float64        `json:"repaid_seconds_per_day"`
	// What the endpoints the team excluded cost: out of the debt, never out of sight.
	ExcludedSecondsPerDay float64 `json:"excluded_seconds_per_day"`
	Items                 []metrics.Cost `json:"items"`
}

// Debt is the waiting time the known problems cost per day: the last 24 hours
// against the 24 hours before.
func (e *Engine) Debt(ctx context.Context, p project.Project) (Debt, error) {
	now := e.Now()
	var d Debt
	current, err := e.Metrics.Costs(ctx, p.ID, now.Add(-24*time.Hour), now)
	if err != nil {
		return d, err
	}
	previous, err := e.Metrics.Costs(ctx, p.ID, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	if err != nil {
		return d, err
	}
	for _, c := range current {
		d.SecondsPerDay += c.Seconds
	}
	for _, c := range previous {
		d.PreviousSecondsPerDay += c.Seconds
	}
	excluded, err := e.Metrics.ExcludedCosts(ctx, p.ID, now.Add(-24*time.Hour), now)
	if err != nil {
		return d, err
	}
	for _, c := range excluded {
		d.ExcludedSecondsPerDay += c.Seconds
	}
	d.Items = current
	if len(d.Items) > 20 {
		d.Items = d.Items[:20]
	}
	d.RepaidSecondsPerDay, err = e.repaidPerDay(ctx, p)
	return d, err
}
