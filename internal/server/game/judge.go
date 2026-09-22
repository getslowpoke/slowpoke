package game

import (
	"context"
	"database/sql"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
)

// minJudgedRequests is the traffic under which a window says nothing: with a handful of requests
// the time per request is noise.
const minJudgedRequests = 50

// judged is one window as missions, bosses and the watch read it: the cost of each thing per
// request, on the busy hours of the window only. A window with no busy hours, or too little
// traffic in them (a Sunday, a holiday), is not judged at all. A project with no HTTP traffic
// measured (only queries or jobs) is judged on the whole window, in seconds, as it always was.
type judged struct {
	ok         bool
	perRequest bool
	requests   int64
	costs      map[string]float64 // kind|target -> seconds in the busy hours
}

func (j judged) value(kind, target string) float64 {
	c := j.costs[kind+"|"+target]
	if j.perRequest {
		return c / float64(j.requests)
	}
	return c
}

func (e *Engine) judge(ctx context.Context, p project.Project, from, to time.Time) (judged, error) {
	key := [2]int64{from.Unix(), to.Unix()}
	if j, ok := e.judgeCache[key]; ok {
		return j, nil
	}
	j, err := e.judgeWindow(ctx, p, from, to)
	if err == nil && e.judgeCache != nil {
		e.judgeCache[key] = j
	}
	return j, err
}

func (e *Engine) judgeWindow(ctx context.Context, p project.Project, from, to time.Time) (judged, error) {
	ranges := [][2]time.Time{{from, to}}
	if e.busy.Known {
		ranges = e.busy.Ranges(from, to)
	}
	var requests int64
	for _, r := range ranges {
		n, err := e.Metrics.Requests(ctx, p.ID, r[0], r[1])
		if err != nil {
			return judged{}, err
		}
		requests += n
	}
	collect := func(rs [][2]time.Time) (map[string]float64, error) {
		out := map[string]float64{}
		for _, r := range rs {
			costs, err := e.Metrics.Costs(ctx, p.ID, r[0], r[1])
			if err != nil {
				return nil, err
			}
			for _, c := range costs {
				out[c.Kind+"|"+c.Target] += c.Seconds
			}
		}
		return out, nil
	}
	if requests == 0 {
		total, err := e.Metrics.Requests(ctx, p.ID, from, to)
		if err != nil || total > 0 {
			return judged{}, err // traffic, but none in the busy hours
		}
		has, err := e.Metrics.HasData(ctx, p.ID, from, to)
		if err != nil || !has {
			return judged{}, err
		}
		costs, err := collect([][2]time.Time{{from, to}})
		return judged{ok: true, costs: costs}, err
	}
	if requests < minJudgedRequests {
		return judged{}, nil
	}
	costs, err := collect(ranges)
	return judged{ok: true, perRequest: true, requests: requests, costs: costs}, err
}

// openingRate is what a mission cost per request when it was opened: the judged window it was
// opened on, or the last judged one before (a mission opened on a Monday morning was measured on
// a Sunday, which says nothing). Missions opened before this existed get theirs the same way.
func (e *Engine) openingRate(ctx context.Context, p project.Project, m *mission, w time.Duration) (sql.NullFloat64, error) {
	if m.BaselineRate.Valid {
		return m.BaselineRate, nil
	}
	end := boundary(time.Unix(m.OpenedAt, 0), w)
	for k := 0; k < 7; k++ {
		j, err := e.judge(ctx, p, end.Add(-time.Duration(k+1)*w), end.Add(-time.Duration(k)*w))
		if err != nil {
			return sql.NullFloat64{}, err
		}
		if j.ok && j.perRequest {
			rate := sql.NullFloat64{Float64: j.value(m.Kind, m.Target), Valid: true}
			if rate.Float64 <= 0 {
				continue
			}
			if _, err := e.DB.Q().Exec(ctx, `UPDATE missions SET baseline_rate = ? WHERE id = ?`, rate.Float64, m.ID); err != nil {
				return rate, err
			}
			m.BaselineRate = rate
			return rate, nil
		}
	}
	return sql.NullFloat64{}, nil
}
