package metrics

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/exclude"
)

// Outbound is what the requests of one endpoint spent waiting on other services: a payment
// provider, a partner API, another service of the company. It is part of the endpoint's time,
// never added to it: it explains the cost, it is not a second one.
type Outbound struct {
	Requests  int64          `json:"requests"`   // requests seen by the framework package
	WithCalls int64          `json:"with_calls"` // of which made at least one call
	Seconds   float64        `json:"seconds"`    // waited outside, overlapping calls once
	Share     float64        `json:"share"`      // of the requests' own time, 0-1
	Hosts     []OutboundHost `json:"hosts"`
}

type OutboundHost struct {
	Host       string  `json:"host"`
	Calls      int64   `json:"calls"`
	PerRequest float64 `json:"per_request"`
	Seconds    float64 `json:"seconds"`
	Avg        float64 `json:"avg"`
	Max        float64 `json:"max"`
	Errors     int64   `json:"errors"`
	Origin     string  `json:"origin"`
}

// OutboundByEndpoint reads the time waited outside for every endpoint at once, keyed by
// exclude.Key: the list of slow things shows it next to each endpoint.
func (r Reader) OutboundByEndpoint(ctx context.Context, projectID int64, from, to time.Time) (map[string]Outbound, error) {
	rows, err := r.DB.Q().Query(ctx, `SELECT route, SUM(n), SUM(sum), SUM(ext_sum), SUM(ext_requests) FROM route_hours
		WHERE project_id = ? AND hour >= ? AND hour < ? GROUP BY route`, projectID, hourFloor(from), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type acc struct {
		n, with   int64
		sum, ext float64
	}
	byKey := map[string]*acc{}
	for rows.Next() {
		var route string
		var n, with int64
		var sum, ext float64
		if err := rows.Scan(&route, &n, &sum, &ext, &with); err != nil {
			return nil, err
		}
		k := exclude.Key(exclude.KindEndpoint, route)
		a := byKey[k]
		if a == nil {
			a = &acc{}
			byKey[k] = a
		}
		a.n, a.with, a.sum, a.ext = a.n+n, a.with+with, a.sum+sum, a.ext+ext
	}
	out := make(map[string]Outbound, len(byKey))
	for k, a := range byKey {
		o := Outbound{Requests: a.n, WithCalls: a.with, Seconds: round3(a.ext), Hosts: []OutboundHost{}}
		if a.sum > 0 {
			o.Share = round3(math.Min(1, a.ext/a.sum))
		}
		out[k] = o
	}
	return out, rows.Err()
}

// Outbound reads one endpoint, with the hosts it called, the worst first.
func (r Reader) Outbound(ctx context.Context, projectID int64, endpoint string, from, to time.Time) (Outbound, error) {
	key := exclude.Key(exclude.KindEndpoint, endpoint)
	all, err := r.OutboundByEndpoint(ctx, projectID, from, to)
	if err != nil {
		return Outbound{}, err
	}
	out, ok := all[key]
	if !ok {
		return Outbound{Hosts: []OutboundHost{}}, nil
	}
	rows, err := r.DB.Q().Query(ctx, `SELECT route, host, SUM(n), SUM(sum), MAX(max), SUM(errors), SUM(requests), MAX(origin) FROM route_calls
		WHERE project_id = ? AND hour >= ? AND hour < ? GROUP BY route, host`, projectID, hourFloor(from), to.Unix())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	type acc struct {
		OutboundHost
		requests int64
	}
	hosts := map[string]*acc{}
	for rows.Next() {
		var route, host, origin string
		var n, errs, requests int64
		var sum, mx float64
		if err := rows.Scan(&route, &host, &n, &sum, &mx, &errs, &requests, &origin); err != nil {
			return out, err
		}
		if exclude.Key(exclude.KindEndpoint, route) != key {
			continue
		}
		h := hosts[host]
		if h == nil {
			h = &acc{OutboundHost: OutboundHost{Host: host}}
			hosts[host] = h
		}
		h.Calls += n
		h.Seconds += sum
		h.Max = math.Max(h.Max, mx)
		h.Errors += errs
		h.requests += requests
		if h.Origin == "" {
			h.Origin = origin
		}
	}
	for _, h := range hosts {
		h.Seconds = round3(h.Seconds)
		h.Avg = round3(h.Seconds / math.Max(1, float64(h.Calls)))
		h.PerRequest = round1(float64(h.Calls) / math.Max(1, float64(h.requests)))
		out.Hosts = append(out.Hosts, h.OutboundHost)
	}
	sort.Slice(out.Hosts, func(i, j int) bool { return out.Hosts[i].Seconds > out.Hosts[j].Seconds })
	return out, rows.Err()
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// Requests is how many HTTP requests the project answered in a window, each counted once (a
// source behind a web server that reported the same requests is left out).
func (r Reader) Requests(ctx context.Context, projectID int64, from, to time.Time) (int64, error) {
	shadowed, err := r.Shadowed(ctx, projectID, from)
	if err != nil {
		return 0, err
	}
	rows, err := r.DB.Q().Query(ctx, `SELECT source, SUM(n) FROM http_minutes WHERE project_id = ? AND bucket >= ? AND bucket < ? GROUP BY source`,
		projectID, from.Unix(), to.Unix())
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var source string
		var n int64
		if err := rows.Scan(&source, &n); err != nil {
			return 0, err
		}
		if !shadowed[source] {
			total += n
		}
	}
	return total, rows.Err()
}
