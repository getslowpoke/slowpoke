package agent

import (
	"math"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	trace "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

const (
	// A route that calls one host per tenant would otherwise grow a map without bounds.
	maxExtHostsPerRoute = 20
	otherHost           = "other"
)

// tracedCalls is what one request spent on outbound HTTP calls.
type tracedCalls struct {
	waited float64 // wall time with at least one call in flight: overlapping calls count once
	hosts  map[string]protocol.ExtCall
}

// outboundCalls finds the HTTP calls a traced request made to other services. A call is a
// client span with an HTTP method and no SQL; one traced twice (an HTTP client over the
// library that does the work) counts once, the outermost.
func outboundCalls(spans []span, byID map[string]*span, root *span, codeRoot string) tracedCalls {
	out := tracedCalls{hosts: map[string]protocol.ExtCall{}}
	isCall := func(s *span) bool {
		return s.kind == int32(trace.Span_SPAN_KIND_CLIENT) &&
			first(s.attrs, "http.request.method", "http.method") != "" &&
			first(s.attrs, "db.query.text", "db.statement") == ""
	}
	type interval struct{ from, to time.Time }
	var busy []interval
	for i := range spans {
		s := &spans[i]
		if !isCall(s) {
			continue
		}
		nested := false
		for p, hops := byID[s.parentID], 0; p != nil && hops < 50; p, hops = byID[p.parentID], hops+1 {
			if isCall(p) {
				nested = true
				break
			}
		}
		if nested {
			continue
		}
		from, to := s.start, s.end
		if root != nil {
			// A call still running when the response left is not time the user waited for.
			if from.Before(root.start) {
				from = root.start
			}
			if to.After(root.end) {
				to = root.end
			}
		}
		d := math.Max(0, to.Sub(from).Seconds())
		host := callHost(s.attrs)
		c := out.hosts[host]
		c.N++
		c.Sum += d
		c.Max = math.Max(c.Max, d)
		status, _ := strconv.Atoi(first(s.attrs, "http.response.status_code", "http.status_code"))
		if s.attrs["otel.status"] == "error" || status >= 500 {
			c.Errors++
		}
		if c.Origin == "" {
			c.Origin = truncate(codeOrigin(s, byID, codeRoot), 300)
		}
		out.hosts[host] = c
		if to.After(from) {
			busy = append(busy, interval{from, to})
		}
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].from.Before(busy[j].from) })
	var end time.Time
	for _, iv := range busy {
		if iv.from.Before(end) {
			if iv.to.After(end) {
				out.waited += iv.to.Sub(end).Seconds()
				end = iv.to
			}
			continue
		}
		out.waited += iv.to.Sub(iv.from).Seconds()
		end = iv.to
	}
	return out
}

// callHost is the remote host of a call, with the port when it is not the scheme's default:
// two internal services on one address are two services.
func callHost(attrs map[string]string) string {
	host := strings.ToLower(first(attrs, "server.address", "net.peer.name"))
	port := first(attrs, "server.port", "net.peer.port")
	if raw := first(attrs, "url.full", "http.url"); raw != "" {
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			if host == "" {
				host = strings.ToLower(u.Hostname())
			}
			if port == "" {
				port = u.Port()
			}
		}
	}
	if h, p, err := net.SplitHostPort(host); err == nil {
		host = h
		if port == "" {
			port = p
		}
	}
	if host == "" {
		return "unknown"
	}
	if port != "" && port != "80" && port != "443" {
		host = net.JoinHostPort(host, port)
	}
	return truncate(host, 150)
}
