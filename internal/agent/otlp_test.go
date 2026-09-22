package agent

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	common "go.opentelemetry.io/proto/otlp/common/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"google.golang.org/protobuf/proto"
)

func str(k, v string) *common.KeyValue {
	return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: v}}}
}
func num(k string, v int64) *common.KeyValue {
	return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_IntValue{IntValue: v}}}
}

// A Django-like request: 12 identical queries in a loop (N+1) and one heavy query.
func djangoTrace(start time.Time) *trace.TracesData {
	traceID := bytes.Repeat([]byte{7}, 16)
	server := &trace.Span{TraceId: traceID, SpanId: []byte{1, 1, 1, 1, 1, 1, 1, 1}, Name: "GET orders/<int:pk>", Kind: trace.Span_SPAN_KIND_SERVER,
		StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(start.Add(1800 * time.Millisecond).UnixNano()),
		Attributes: []*common.KeyValue{str("http.request.method", "GET"), str("http.route", "orders/<int:pk>"), num("http.response.status_code", 200)}}
	spans := []*trace.Span{server}
	for i := 0; i < 12; i++ {
		spans = append(spans, &trace.Span{TraceId: traceID, SpanId: []byte{2, 0, 0, 0, 0, 0, 0, byte(i)}, ParentSpanId: server.SpanId, Name: "SELECT", Kind: trace.Span_SPAN_KIND_CLIENT,
			StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(start.Add(20 * time.Millisecond).UnixNano()),
			Attributes: []*common.KeyValue{str("db.system", "postgresql"), str("db.statement", `SELECT * FROM "customers" WHERE "id" = 17`),
				str("code.filepath", "/srv/app/shop/views.py"), num("code.lineno", 42)}})
	}
	spans = append(spans, &trace.Span{TraceId: traceID, SpanId: []byte{3, 3, 3, 3, 3, 3, 3, 3}, ParentSpanId: server.SpanId, Name: "SELECT", Kind: trace.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(start.Add(900 * time.Millisecond).UnixNano()),
		Attributes: []*common.KeyValue{str("db.system", "postgresql"), str("db.query.text", "SELECT sum(total) FROM orders WHERE email = 'ada@example.com'")}})
	return &trace.TracesData{ResourceSpans: []*trace.ResourceSpans{{
		Resource:   &resource.Resource{Attributes: []*common.KeyValue{str("service.name", "shop-api")}},
		ScopeSpans: []*trace.ScopeSpans{{Spans: spans}},
	}}}
}

func checkDjango(t *testing.T, d Drained) {
	t.Helper()
	if len(d.App) != 1 {
		t.Fatalf("app buckets %+v", d.App)
	}
	route, ok := d.App[0].Routes["GET /orders/{pk}"]
	if !ok || route.N != 1 || route.Queries != 13 || route.E5 != 0 || route.Max < 1.7 {
		t.Fatalf("routes %+v", d.App[0].Routes)
	}
	var loop, heavy bool
	for _, q := range d.App[0].Queries {
		switch {
		case strings.HasPrefix(q.Fingerprint, "select * from customers"):
			loop = q.N == 12 && q.NPlusOne == 1 && q.Origin == "shop/views.py:42"
		case strings.HasPrefix(q.Fingerprint, "select sum(total)"):
			heavy = q.N == 1 && q.Sum > 0.89
			if strings.Contains(q.Sample, "ada@example.com") {
				t.Fatalf("real values leaked: %q", q.Sample)
			}
		}
	}
	if !loop || !heavy {
		t.Fatalf("queries %+v", d.App[0].Queries)
	}
	if len(d.HTTP) != 1 || d.HTTP[0].Source != "otlp:shop-api" || d.HTTP[0].Endpoints["GET /orders/{pk}"].N != 1 {
		t.Fatalf("http %+v", d.HTTP)
	}
}

func TestOTLPProtobufTraces(t *testing.T) {
	a := NewAggregator()
	r := newOTLPReceiver(a).withCodeRoot("/srv/app")
	start := time.Now().Add(-2 * time.Minute)
	body, _ := proto.Marshal(djangoTrace(start))

	req := httptest.NewRequest("POST", "/v1/traces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/x-protobuf" {
		t.Fatalf("status %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	r.flush(time.Now().Add(time.Hour))
	checkDjango(t, a.Drain(time.Now().Add(time.Hour)))
}

// OTLP/JSON uses hex ids and lowerCamelCase: what Node and Python exporters send with http/json.
func TestOTLPJSONTraces(t *testing.T) {
	a := NewAggregator()
	r := newOTLPReceiver(a).withCodeRoot("/srv/app")
	start := time.Now().Add(-2 * time.Minute)
	msg := djangoTrace(start)

	var spans []map[string]any
	for _, s := range msg.ResourceSpans[0].ScopeSpans[0].Spans {
		var attrs []map[string]any
		for _, kv := range s.Attributes {
			v := map[string]any{}
			switch x := kv.Value.Value.(type) {
			case *common.AnyValue_StringValue:
				v["stringValue"] = x.StringValue
			case *common.AnyValue_IntValue:
				v["intValue"] = strconv.FormatInt(x.IntValue, 10) // proto3 JSON: int64 as string
			}
			attrs = append(attrs, map[string]any{"key": kv.Key, "value": v})
		}
		spans = append(spans, map[string]any{
			"traceId": hex.EncodeToString(s.TraceId), "spanId": hex.EncodeToString(s.SpanId), "parentSpanId": hex.EncodeToString(s.ParentSpanId),
			"name": s.Name, "kind": int(s.Kind), "startTimeUnixNano": json.Number(strconv.FormatUint(s.StartTimeUnixNano, 10)), "endTimeUnixNano": strconv.FormatUint(s.EndTimeUnixNano, 10), "attributes": attrs,
		})
	}
	payload := map[string]any{"resourceSpans": []any{map[string]any{
		"resource":   map[string]any{"attributes": []any{map[string]any{"key": "service.name", "value": map[string]any{"stringValue": "shop-api"}}}},
		"scopeSpans": []any{map[string]any{"spans": spans}},
	}}}
	raw, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/traces", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	r.flush(time.Now().Add(time.Hour))
	checkDjango(t, a.Drain(time.Now().Add(time.Hour)))
}

func TestOTLPRejectsGarbageAndBigBodies(t *testing.T) {
	r := newOTLPReceiver(NewAggregator())
	for _, tc := range []struct {
		ctype string
		body  []byte
		want  int
	}{
		{"application/x-protobuf", []byte{0xff, 0xff, 0xff}, http.StatusBadRequest},
		{"application/json", []byte("{nope"), http.StatusBadRequest},
		{"text/plain", []byte("x"), http.StatusUnsupportedMediaType},
		{"application/json", bytes.Repeat([]byte("a"), maxOTLPBody+10), http.StatusRequestEntityTooLarge},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/traces", bytes.NewReader(tc.body))
		req.Header.Set("Content-Type", tc.ctype)
		r.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: got %d want %d", tc.ctype, rec.Code, tc.want)
		}
	}
	// Metrics and logs are accepted and ignored, so exporters do not retry forever.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/metrics", bytes.NewReader(nil)))
	if rec.Code != 200 {
		t.Fatalf("metrics: %d", rec.Code)
	}
}

func TestRoutesFromDifferentFrameworks(t *testing.T) {
	cases := map[string]string{
		"orders/<int:pk>":     "/orders/{pk}",        // Django
		"/orders/{order_id}":  "/orders/{order_id}",  // FastAPI, Laravel
		"/orders/:id/items":   "/orders/{id}/items",  // Express, Gin
		"/users/{id:[0-9]+}":  "/users/{id}",         // gorilla/mux
		"/api/v1/orders/[id]": "/api/v1/orders/{id}", // Next.js
	}
	for in, want := range cases {
		if got := normalizeRoute(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

// A queued job and a scheduled command are not endpoints: they belong to the Jobs page, with how
// long they took and how often they failed. Until now nothing in the agent ever produced a job:
// the page had demo data only, and a Laravel job landed among the routes.
func TestJobsAndCommandsBecomeJobsNotRoutes(t *testing.T) {
	a := NewAggregator()
	r := newOTLPReceiver(a)
	// On a minute boundary: buckets are keyed by the minute a run ended, so a start at :58 would
	// put the two runs of the same job in two buckets and fail this test about one time in twenty.
	start := time.Now().Truncate(time.Minute).Add(-2 * time.Minute)

	root := func(id byte, name string, attrs []*common.KeyValue, seconds float64, failed bool) *trace.Span {
		s := &trace.Span{
			TraceId: bytes.Repeat([]byte{id}, 16), SpanId: []byte{id, id, id, id, id, id, id, id},
			Name: name, Kind: trace.Span_SPAN_KIND_SERVER,
			StartTimeUnixNano: uint64(start.UnixNano()),
			EndTimeUnixNano:   uint64(start.Add(time.Duration(seconds * float64(time.Second))).UnixNano()),
			Attributes:        attrs,
		}
		if failed {
			s.Status = &trace.Status{Code: trace.Status_STATUS_CODE_ERROR}
		}
		return s
	}
	msg := &trace.TracesData{ResourceSpans: []*trace.ResourceSpans{{
		Resource: &resource.Resource{Attributes: []*common.KeyValue{str("service.name", "shop")}},
		ScopeSpans: []*trace.ScopeSpans{{Spans: []*trace.Span{
			root(1, "App\\Jobs\\SendInvoice", []*common.KeyValue{str("slowpoke.kind", "job"), str("messaging.destination.name", "emails")}, 2, false),
			root(2, "App\\Jobs\\SendInvoice", []*common.KeyValue{str("slowpoke.kind", "job"), str("messaging.destination.name", "emails")}, 4, true),
			root(3, "invoices:close", []*common.KeyValue{str("slowpoke.kind", "command")}, 30, false),
		}}},
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
	out := a.Drain(time.Now().Add(time.Hour))

	jobs := map[string]protocol.JobBucket{}
	for _, j := range out.Jobs {
		jobs[j.Kind+":"+j.Name] = j
	}
	job := jobs["job:App\\Jobs\\SendInvoice"]
	if job.N != 2 || job.Failed != 1 || job.Sum < 5.9 || job.Max < 3.9 {
		t.Fatalf("the two runs of one job: %+v (all: %+v)", job, out.Jobs)
	}
	command := jobs["command:invoices:close"]
	if command.N != 1 || command.Failed != 0 || command.Sum < 29 {
		t.Fatalf("the scheduled command: %+v", command)
	}
	for _, b := range out.HTTP {
		for endpoint := range b.Endpoints {
			if strings.Contains(endpoint, "SendInvoice") || strings.Contains(endpoint, "invoices:close") {
				t.Fatalf("a job is not an endpoint: %q", endpoint)
			}
		}
	}
}

// A package sends the path of the application's own file, already relative to the deploy
// directory: "src/routes/orders.js" is where that file is, and the panel makes a link out of it.
// The agent used to apply the php-fpm heuristic to it as well, which strips a leading folder when
// the next one also looks like a source directory: a whole Express project lost its "src/".
func TestAnOriginFromAPackageIsKeptAsItWasSent(t *testing.T) {
	kept := []string{
		"src/routes/orders.js", "src/config/database.js", "src/lib/db.js", "app/lib/query.rb",
		"app/Models/Order.php", "shop/views.py", "src/Controller/OrderController.php",
	}
	for _, file := range kept {
		s := &span{attrs: map[string]string{"code.file.path": file, "code.line.number": "18"}}
		if got := codeOrigin(s, map[string]*span{}, "/srv/app/"); got != file+":18" {
			t.Errorf("%s came out as %q", file, got)
		}
	}
	// An absolute path from an SDK that sends one is still cut down to the project.
	s := &span{attrs: map[string]string{"code.file.path": "/var/www/app/routes/web.php", "code.line.number": "7"}}
	if got := codeOrigin(s, map[string]*span{}, ""); got != "routes/web.php:7" {
		t.Errorf("absolute path: %q", got)
	}
	// Third-party code is never an origin, wherever it comes from.
	s = &span{attrs: map[string]string{"code.file.path": "node_modules/pg/lib/client.js", "code.line.number": "1"}}
	if got := codeOrigin(s, map[string]*span{}, ""); got != "" {
		t.Errorf("node_modules: %q", got)
	}
}

// The application says which host it answered for. A job has no host, and must not invent one:
// nothing in front of it ever reported those runs.
func TestATracedRequestSaysWhichHostItAnswered(t *testing.T) {
	a := NewAggregator()
	r := newOTLPReceiver(a)
	start := time.Now().Truncate(time.Minute).Add(-2 * time.Minute)
	span := func(id byte, name string, kind trace.Span_SpanKind, attrs []*common.KeyValue) *trace.Span {
		return &trace.Span{
			TraceId: bytes.Repeat([]byte{id}, 16), SpanId: []byte{id, id, id, id, id, id, id, id},
			Name: name, Kind: kind,
			StartTimeUnixNano: uint64(start.UnixNano()),
			EndTimeUnixNano:   uint64(start.Add(100 * time.Millisecond).UnixNano()),
			Attributes:        attrs,
		}
	}
	msg := &trace.TracesData{ResourceSpans: []*trace.ResourceSpans{{
		Resource: &resource.Resource{Attributes: []*common.KeyValue{str("service.name", "shop")}},
		ScopeSpans: []*trace.ScopeSpans{{Spans: []*trace.Span{
			span(1, "GET /orders/{id}", trace.Span_SPAN_KIND_SERVER, []*common.KeyValue{
				str("http.request.method", "GET"), str("http.route", "/orders/{id}"),
				str("server.address", "Shop.Example.com:8443"),
			}),
			span(2, "App\\Jobs\\SendInvoice", trace.Span_SPAN_KIND_CONSUMER, []*common.KeyValue{
				str("slowpoke.kind", "job"), str("server.address", "worker.example.com"),
			}),
		}}},
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

	for _, b := range a.Drain(time.Now().Add(time.Hour)).HTTP {
		if b.Source != "otlp:shop" {
			continue
		}
		if len(b.Sites) != 1 || b.Sites[0] != "shop.example.com" {
			t.Fatalf("hosts of the traced source: %v", b.Sites)
		}
		return
	}
	t.Fatal("no bucket for otlp:shop")
}
