package game

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// Busy is the hours of the week a project is judged on: the ones its people use it. A fix is
// judged on a Tuesday morning, not on a Sunday night when nobody is there to wait: at night and
// on weekends the traffic is smaller and the machines idle, and both make everything look faster
// than it is. The waiting of every hour still counts in the debt.
type Busy struct {
	hours  [7 * 24]bool // weekday*24 + hour, in the project's time zone
	loc    *time.Location
	Known  bool // false: not enough history, every hour counts
	Manual bool // set by the team in the settings
}

// historyDays is how much traffic busy hours are learnt from, minDays how much is enough.
const (
	historyDays = 28
	minDays     = 7
)

func slot(t time.Time) int { return int(t.Weekday())*24 + t.Hour() }

// At tells whether a moment falls in the busy hours.
func (b Busy) At(t time.Time) bool { return b.hours[slot(t.In(b.loc))] }

// BusyHours reads the busy hours of a project: set by hand, or learnt from the last four weeks
// of traffic (an hour of the week is busy when it carries at least half of what the busiest
// hours carry).
func BusyHours(ctx context.Context, db *store.DB, p project.Project, now time.Time) (Busy, error) {
	b := Busy{loc: p.Location()}
	if tr := p.Settings.Traffic; len(tr.BusyDays) > 0 && tr.BusyFrom < tr.BusyTo {
		for _, d := range tr.BusyDays {
			for h := tr.BusyFrom; h < tr.BusyTo; h++ {
				b.hours[d*24+h] = true
			}
		}
		b.Known, b.Manual = true, true
		return b, nil
	}
	all := func() Busy {
		for i := range b.hours {
			b.hours[i] = true
		}
		return b
	}
	from := now.Add(-historyDays * 24 * time.Hour)
	rows, err := db.Q().Query(ctx, `SELECT bucket - (bucket % 3600), SUM(n) FROM http_minutes WHERE project_id = ? AND bucket >= ? AND bucket < ?
		GROUP BY bucket - (bucket % 3600)`, p.ID, from.Unix(), now.Unix())
	if err != nil {
		return b, err
	}
	var sums [7 * 24]float64
	days := map[string]bool{}
	first := now
	for rows.Next() {
		var hour, n int64
		if err := rows.Scan(&hour, &n); err != nil {
			rows.Close()
			return b, err
		}
		t := time.Unix(hour, 0).In(b.loc)
		sums[slot(t)] += float64(n)
		days[t.Format("2006-01-02")] = true
		if t.Before(first) {
			first = t
		}
	}
	rows.Close()
	if len(days) < minDays {
		return all(), nil
	}
	// The average of each hour of the week over the weeks observed, a quiet hour included.
	var seen [7 * 24]float64
	for t := first.Truncate(time.Hour); t.Before(now); t = t.Add(time.Hour) {
		seen[slot(t.In(b.loc))]++
	}
	avgs := make([]float64, 0, len(sums))
	var avg [7 * 24]float64
	for i := range sums {
		if seen[i] > 0 {
			avg[i] = sums[i] / seen[i]
		}
		avgs = append(avgs, avg[i])
	}
	sort.Float64s(avgs)
	p90 := avgs[len(avgs)*9/10]
	if p90 <= 0 {
		return all(), nil
	}
	for i := range avg {
		b.hours[i] = avg[i] >= p90/2
	}
	b.Known = true
	return b, nil
}

// Ranges is the busy part of a window, as consecutive stretches.
func (b Busy) Ranges(from, to time.Time) [][2]time.Time {
	var out [][2]time.Time
	for t := from; t.Before(to); {
		next := t.Truncate(time.Hour).Add(time.Hour)
		if next.After(to) {
			next = to
		}
		if b.At(t) {
			if n := len(out); n > 0 && out[n-1][1].Equal(t) {
				out[n-1][1] = next
			} else {
				out = append(out, [2]time.Time{t, next})
			}
		}
		t = next
	}
	return out
}

// BusyGroup is days with the same busy hours: from_day to to_day (0 = Sunday), hours "9-19".
type BusyGroup struct {
	FromDay int    `json:"from_day"`
	ToDay   int    `json:"to_day"`
	Hours   string `json:"hours"`
}

// Groups are the busy hours as a person would say them, Monday first.
func (b Busy) Groups() []BusyGroup {
	order := []int{1, 2, 3, 4, 5, 6, 0}
	hoursOf := func(d int) string {
		var parts []string
		for h := 0; h < 24; {
			if !b.hours[d*24+h] {
				h++
				continue
			}
			start := h
			for h < 24 && b.hours[d*24+h] {
				h++
			}
			parts = append(parts, fmt.Sprintf("%d-%d", start, h))
		}
		return strings.Join(parts, ", ")
	}
	out := []BusyGroup{}
	for i := 0; i < len(order); {
		hs := hoursOf(order[i])
		j := i
		for j+1 < len(order) && hoursOf(order[j+1]) == hs {
			j++
		}
		if hs != "" {
			out = append(out, BusyGroup{FromDay: order[i], ToDay: order[j], Hours: hs})
		}
		i = j + 1
	}
	return out
}

// Describe says the busy hours in English: "Mon-Fri 9-19".
func (b Busy) Describe() string {
	if !b.Known {
		return "every hour"
	}
	names := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	var parts []string
	for _, g := range b.Groups() {
		days := names[g.FromDay]
		if g.ToDay != g.FromDay {
			days += "-" + names[g.ToDay]
		}
		parts = append(parts, days+" "+g.Hours)
	}
	if len(parts) == 0 {
		return "no hour"
	}
	return strings.Join(parts, "; ")
}
