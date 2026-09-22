package agent

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	common "go.opentelemetry.io/proto/otlp/common/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// Each endpoint carries its own histogram: without it the server cannot take an endpoint the
// team excluded out of the speed index, whose percentiles are computed from histograms.
func TestAnEndpointKeepsItsOwnHistogram(t *testing.T) {
	a := NewAggregator()
	now := time.Now()
	a.AddRequest("nginx:shop", now, "GET", "/checkout", 200, 0.04, 1)
	a.AddRequest("nginx:shop", now, "GET", "/checkout", 200, 7, 1)
	a.AddRequest("nginx:shop", now, "GET", "/orders", 200, 0.4, 1)
	d := a.Drain(now.Add(time.Hour))
	if len(d.HTTP) != 1 {
		t.Fatalf("http %+v", d.HTTP)
	}
	checkout := d.HTTP[0].Endpoints["GET /checkout"]
	if len(checkout.Hist) != len(protocol.HistogramEdges)+1 {
		t.Fatalf("hist %v", checkout.Hist)
	}
	if checkout.Hist[protocol.HistogramIndex(0.04)] != 1 || checkout.Hist[protocol.HistogramIndex(7)] != 1 {
		t.Fatalf("checkout hist %v", checkout.Hist)
	}
	if orders := d.HTTP[0].Endpoints["GET /orders"]; orders.Hist[protocol.HistogramIndex(0.4)] != 1 {
		t.Fatalf("orders hist %v", orders.Hist)
	}
}

// A checkout that waits on a payment provider: the time spent outside is measured per host,
// overlapping calls count once for the request, a call traced twice (the HTTP client and the
// library under it) is one call, and nothing but the host leaves the machine.
func TestOutboundCallsAreMeasuredPerHost(t *testing.T) {
	a := NewAggregator()
	r := newOTLPReceiver(a).withCodeRoot("/var/www/app/")
	start := time.Now().Truncate(time.Minute).Add(-2 * time.Minute)
	traceID := bytes.Repeat([]byte{9}, 16)
	at := func(ms int) uint64 { return uint64(start.Add(time.Duration(ms) * time.Millisecond).UnixNano()) }
	id := func(b byte) []byte { return []byte{b, b, b, b, b, b, b, b} }
	root := &trace.Span{TraceId: traceID, SpanId: id(1), Name: "POST /checkout", Kind: trace.Span_SPAN_KIND_SERVER,
		StartTimeUnixNano: at(0), EndTimeUnixNano: at(3000),
		Attributes: []*common.KeyValue{str("http.request.method", "POST"), str("http.route", "/checkout"), num("http.response.status_code", 200)}}
	call := func(b byte, parent []byte, from, to int, attrs ...*common.KeyValue) *trace.Span {
		return &trace.Span{TraceId: traceID, SpanId: id(b), ParentSpanId: parent, Name: "POST", Kind: trace.Span_SPAN_KIND_CLIENT,
			StartTimeUnixNano: at(from), EndTimeUnixNano: at(to), Attributes: attrs}
	}
	spans := []*trace.Span{root,
		// Two calls to the payment provider, overlapping between 500 and 1000 ms.
		call(2, root.SpanId, 0, 1000, str("http.request.method", "POST"), str("server.address", "API.Stripe.com"), num("http.response.status_code", 200),
			str("code.file.path", "/var/www/app/app/Services/Stripe.php"), num("code.line.number", 88)),
		call(3, root.SpanId, 500, 2000, str("http.request.method", "GET"), str("url.full", "https://api.stripe.com/v1/customers/cus_123?token=secret"),
			num("http.response.status_code", 200)),
		// The same second call, seen again by the library under the HTTP client.
		call(4, id(3), 510, 1990, str("http.request.method", "GET"), str("server.address", "api.stripe.com")),
		// A partner API that failed, with the old attribute names.
		call(5, root.SpanId, 2000, 2500, str("http.method", "GET"), str("http.url", "http://partner.example.com:8080/stock?sku=1")),
		// A database query is a client span too, and it is not an outbound HTTP call.
		call(6, root.SpanId, 2500, 2600, str("db.system", "mysql"), str("db.statement", "SELECT 1 FROM orders")),
	}
	spans[4].Status = &trace.Status{Code: trace.Status_STATUS_CODE_ERROR}
	msg := &trace.TracesData{ResourceSpans: []*trace.ResourceSpans{{
		Resource:   &resource.Resource{Attributes: []*common.KeyValue{str("service.name", "shop")}},
		ScopeSpans: []*trace.ScopeSpans{{Spans: spans}},
	}}}
	body, _ := proto.Marshal(msg)
	req := httptest.NewRequest("POST", "/v1/traces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	r.flush(time.Now().Add(time.Hour))
	d := a.Drain(time.Now().Add(time.Hour))
	if len(d.App) != 1 {
		t.Fatalf("app %+v", d.App)
	}
	route := d.App[0].Routes["POST /checkout"]
	// 0-2000 ms on the provider, 2000-2500 on the partner: 2.5 s waited outside.
	if route.ExtRequests != 1 || route.ExtSum < 2.49 || route.ExtSum > 2.51 {
		t.Fatalf("route %+v", route)
	}
	stripe := route.Ext["api.stripe.com"]
	if stripe.N != 2 || stripe.Requests != 1 || stripe.Sum < 2.49 || stripe.Sum > 2.51 || stripe.Max < 1.49 || stripe.Errors != 0 {
		t.Fatalf("stripe %+v", stripe)
	}
	if stripe.Origin != "app/Services/Stripe.php:88" {
		t.Fatalf("origin %q", stripe.Origin)
	}
	partner := route.Ext["partner.example.com:8080"]
	if partner.N != 1 || partner.Errors != 1 {
		t.Fatalf("partner %+v (all %+v)", partner, route.Ext)
	}
	if len(route.Ext) != 2 || route.Queries != 1 {
		t.Fatalf("route %+v", route)
	}
	blob, _ := json.Marshal(d)
	raw := strings.ToLower(string(blob))
	for _, leak := range []string{"cus_123", "secret", "/stock", "sku="} {
		if strings.Contains(raw, leak) {
			t.Fatalf("%q left the machine: %s", leak, raw)
		}
	}
}

// A route that calls hundreds of hosts (one per tenant) must not grow without bounds.
func TestOutboundHostsPerRouteAreCapped(t *testing.T) {
	a := NewAggregator()
	start := time.Now().Add(-time.Minute)
	for i := 0; i < maxExtHostsPerRoute+30; i++ {
		spans := []span{
			{spanID: "r", kind: int32(trace.Span_SPAN_KIND_SERVER), start: start, end: start.Add(time.Second),
				attrs: map[string]string{"http.request.method": "GET", "http.route": "/sync"}},
			{spanID: "c", parentID: "r", kind: int32(trace.Span_SPAN_KIND_CLIENT), start: start, end: start.Add(100 * time.Millisecond),
				attrs: map[string]string{"http.request.method": "GET", "server.address": "tenant" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".example.com"}},
		}
		a.addTrace(spans, "")
	}
	d := a.Drain(time.Now().Add(time.Hour))
	route := d.App[0].Routes["GET /sync"]
	if len(route.Ext) > maxExtHostsPerRoute+1 || route.Ext[otherHost].N != 30 {
		t.Fatalf("%d hosts, other %+v", len(route.Ext), route.Ext[otherHost])
	}
}

// expectedCall is what a framework package fixture says the agent must read about one host.
type expectedCall struct {
	Host   string `json:"host"`
	N      int    `json:"n"`
	Errors int    `json:"errors"`
	Origin string `json:"origin"`
}

// checkOutbound compares the calls a fixture route made with what its package promised: a
// scenario with no outbound calls must produce none.
func checkOutbound(t *testing.T, route protocol.RouteStat, want []expectedCall) {
	t.Helper()
	if len(route.Ext) != len(want) {
		t.Fatalf("outbound hosts %+v, want %+v", route.Ext, want)
	}
	for _, w := range want {
		got, ok := route.Ext[w.Host]
		if !ok || got.N != w.N || got.Errors != w.Errors || got.Origin != w.Origin || got.Sum <= 0 {
			t.Fatalf("outbound %q = %+v, want %+v", w.Host, got, w)
		}
	}
	if len(want) > 0 && (route.ExtRequests != route.N || route.ExtSum <= 0 || route.ExtSum > route.Sum) {
		t.Fatalf("route waited %v of %v in %d requests", route.ExtSum, route.Sum, route.ExtRequests)
	}
}
