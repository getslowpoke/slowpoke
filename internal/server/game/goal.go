package game

import (
	"context"
	"fmt"

	"github.com/getslowpoke/slowpoke/internal/server/debt"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

const goalPoints = 50

// evaluateDebtGoal celebrates the week the team repays its debt budget (once per ISO week).
func (e *Engine) evaluateDebtGoal(ctx context.Context, p project.Project) error {
	if p.Settings.Debt.WeeklyGoalSeconds <= 0 {
		return nil
	}
	now := e.Now()
	g, err := debt.WeekGoal(ctx, e.DB, p, now)
	if err != nil || !g.Reached {
		return err
	}
	year, week := now.In(p.Location()).ISOWeek()
	return e.DB.Tx(ctx, func(q store.Q) error {
		first, err := e.once(ctx, q, fmt.Sprintf("goal:%d:%d-%02d", p.ID, year, week))
		if err != nil || !first {
			return err
		}
		if err := e.award(ctx, q, p.ID, goalPoints, "Weekly debt budget kept", "", "goal", 0); err != nil {
			return err
		}
		return e.activity(ctx, q, p.ID, "debt_goal", "🎯", fmt.Sprintf("Weekly debt budget kept: %s of waiting per day repaid", humanSeconds(g.RepaidSecondsPerDay)),
			map[string]any{"repaid_seconds_per_day": g.RepaidSecondsPerDay, "goal_seconds_per_day": g.GoalSecondsPerDay, "points": goalPoints})
	})
}

func humanSeconds(v float64) string {
	s := int64(v)
	switch {
	case s >= 3600:
		return fmt.Sprintf("%dh %dm", s/3600, (s%3600)/60)
	case s >= 60:
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	}
	return fmt.Sprintf("%ds", s)
}
