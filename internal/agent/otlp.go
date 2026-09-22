package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	common "go.opentelemetry.io/proto/otlp/common/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// The OTLP receiver makes every backend language compatible: PHP, Python,
// Node.js, Go, Ruby, Java... already emit traces with OpenTelemetry. The agent
// listens on localhost, turns spans into per-minute route and query statistics,
// redacts statement values and only then sends them. Raw traces never leave
// the machine.

const (
	maxOTLPBody     = 8 << 20
	nPlusOneRepeats = 5
	traceSettle     = 10 * time.Second
	maxPendingTrace = 20000
)

type span struct {
	traceID, spanID, parentID string
	name                      string
	kind                      int32
	start, end                time.Time
	attrs                     map[string]string
	service                   string
}

type pendingTrace struct {
	spans    []span
	lastSeen time.Time
}

type otlpReceiver struct {
	agg      *Aggregator
	codeRoot string
	mu       sync.Mutex
	pending  map[string]*pendingTrace
	now      func() time.Time
	last     time.Time // last spans received, for the source check
}

func (r *otlpReceiver) lastTrace() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

func newOTLPReceiver(agg *Aggregator) *otlpReceiver {
	return &otlpReceiver{agg: agg, pending: map[string]*pendingTrace{}, now: time.Now}
}

// withCodeRoot strips the deploy directory from code paths ("/srv/app/" -> "shop/views.py").
func (r *otlpReceiver) withCodeRoot(root string) *otlpReceiver {
	if root != "" && !strings.HasSuffix(root, "/") {
		root += "/"
	}
	r.codeRoot = root
	return r
}

func (r *otlpReceiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if req.URL.Path != "/v1/traces" {
		// Metrics and logs: accepted and ignored for now, so exporters do not retry.
		_, _ = io.Copy(io.Discard, io.LimitReader(req.Body, maxOTLPBody))
		w.WriteHeader(http.StatusOK)
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, maxOTLPBody+1))
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	if len(body) > maxOTLPBody {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	ctype, _, _ := mime.ParseMediaType(req.Header.Get("Content-Type"))
	var spans []span
	switch ctype {
	case "application/x-protobuf", "application/protobuf":
		// ExportTraceServiceRequest has the same wire format as TracesData
		// (field 1: resource_spans): decoding it this way avoids pulling gRPC into the agent.
		var msg trace.TracesData
		if err := proto.Unmarshal(body, &msg); err != nil {
			http.Error(w, "invalid protobuf", http.StatusBadRequest)
			return
		}
		spans = spansFromProto(&msg)
		w.Header().Set("Content-Type", "application/x-protobuf")
		r.add(spans)
		// An empty ExportTraceServiceResponse is zero bytes on the wire.
		return
	case "application/json":
		spans, err = spansFromJSON(body)
		if err != nil {
			http.Error(w, "invalid OTLP JSON", http.StatusBadRequest)
			return
		}
		r.add(spans)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
		return
	}
	http.Error(w, "use application/x-protobuf or application/json", http.StatusUnsupportedMediaType)
}

func (r *otlpReceiver) add(spans []span) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if len(spans) > 0 {
		r.last = now
	}
	for _, s := range spans {
		t := r.pending[s.traceID]
		if t == nil {
			if len(r.pending) >= maxPendingTrace {
				continue // backpressure: never grow without bounds
			}
			t = &pendingTrace{}
			r.pending[s.traceID] = t
		}
		if len(t.spans) < 2000 {
			t.spans = append(t.spans, s)
		}
		t.lastSeen = now
	}
}

// flush finalizes traces that received no span for traceSettle.
func (r *otlpReceiver) flush(now time.Time) {
	r.mu.Lock()
	var ready [][]span
	for id, t := range r.pending {
		if now.Sub(t.lastSeen) >= traceSettle {
			ready = append(ready, t.spans)
			delete(r.pending, id)
		}
	}
	r.mu.Unlock()
	for _, spans := range ready {
		r.agg.addTrace(spans, r.codeRoot)
	}
}

func (r *otlpReceiver) listen(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: r, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("otlp receiver stopped", "error", err)
		}
	}()
	go func() {
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				r.flush(now)
			}
		}
	}()
	return nil
}

// ---------------------------------------------------------------- decoding

func anyString(v *common.AnyValue) string {
	switch x := v.GetValue().(type) {
	case *common.AnyValue_StringValue:
		return x.StringValue
	case *common.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10)
	case *common.AnyValue_DoubleValue:
		return strconv.FormatFloat(x.DoubleValue, 'f', -1, 64)
	case *common.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue)
	}
	return ""
}

var wantedAttrs = map[string]bool{
	"http.route": true, "http.request.method": true, "http.method": true, "http.response.status_code": true, "http.status_code": true,
	"url.path": true, "http.target": true, "db.statement": true, "db.query.text": true, "db.system": true, "db.system.name": true,
	"code.filepath": true, "code.file.path": true, "code.lineno": true, "code.line.number": true, "code.function": true, "code.function.name": true,
	"messaging.operation": true, "messaging.destination.name": true, "exception.type": true,
	// What a framework package says this trace is: a request, a queued job, a scheduled command.
	"slowpoke.kind": true,
	// The host the application answered for: the same requests, reported by a web server on one
	// machine and by the application on another, are recognised by it.
	"server.address": true, "url.domain": true, "http.host": true, "net.host.name": true,
	// Where an outbound HTTP call went. Only the host is kept: the full URL is read to find it
	// and dropped, since paths and query strings carry tokens and personal data.
	"server.port": true, "url.full": true, "http.url": true, "net.peer.name": true, "net.peer.port": true,
}

func spansFromProto(msg *trace.TracesData) []span {
	var out []span
	for _, rs := range msg.ResourceSpans {
		service := ""
		for _, kv := range rs.GetResource().GetAttributes() {
			if kv.Key == "service.name" {
				service = anyString(kv.Value)
			}
		}
		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				sp := span{traceID: hex.EncodeToString(s.TraceId), spanID: hex.EncodeToString(s.SpanId), parentID: hex.EncodeToString(s.ParentSpanId),
					name: s.Name, kind: int32(s.Kind), start: time.Unix(0, int64(s.StartTimeUnixNano)), end: time.Unix(0, int64(s.EndTimeUnixNano)),
					attrs: map[string]string{}, service: service}
				for _, kv := range s.Attributes {
					if wantedAttrs[kv.Key] {
						sp.attrs[kv.Key] = anyString(kv.Value)
					}
				}
				if s.Status.GetCode() == trace.Status_STATUS_CODE_ERROR {
					sp.attrs["otel.status"] = "error"
				}
				out = append(out, sp)
			}
		}
	}
	return out
}

type jsonAny struct {
	StringValue *string         `json:"stringValue"`
	IntValue    json.RawMessage `json:"intValue"`
	DoubleValue *float64        `json:"doubleValue"`
	BoolValue   *bool           `json:"boolValue"`
}

type jsonKV struct {
	Key   string  `json:"key"`
	Value jsonAny `json:"value"`
}

func (v jsonAny) String() string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case len(v.IntValue) > 0:
		return strings.Trim(string(v.IntValue), `"`)
	case v.DoubleValue != nil:
		return strconv.FormatFloat(*v.DoubleValue, 'f', -1, 64)
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue)
	}
	return ""
}

func jsonNano(raw json.RawMessage) int64 {
	n, _ := strconv.ParseInt(strings.Trim(string(raw), `"`), 10, 64)
	return n
}

func spansFromJSON(body []byte) ([]span, error) {
	var msg struct {
		ResourceSpans []struct {
			Resource struct {
				Attributes []jsonKV `json:"attributes"`
			} `json:"resource"`
			ScopeSpans []struct {
				Spans []struct {
					TraceID      string          `json:"traceId"`
					SpanID       string          `json:"spanId"`
					ParentSpanID string          `json:"parentSpanId"`
					Name         string          `json:"name"`
					Kind         int32           `json:"kind"`
					Start        json.RawMessage `json:"startTimeUnixNano"`
					End          json.RawMessage `json:"endTimeUnixNano"`
					Attributes   []jsonKV        `json:"attributes"`
					Status       struct {
						Code int `json:"code"`
					} `json:"status"`
				} `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	var out []span
	for _, rs := range msg.ResourceSpans {
		service := ""
		for _, kv := range rs.Resource.Attributes {
			if kv.Key == "service.name" {
				service = kv.Value.String()
			}
		}
		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				sp := span{traceID: strings.ToLower(s.TraceID), spanID: s.SpanID, parentID: s.ParentSpanID, name: s.Name, kind: s.Kind,
					start: time.Unix(0, jsonNano(s.Start)), end: time.Unix(0, jsonNano(s.End)), attrs: map[string]string{}, service: service}
				for _, kv := range s.Attributes {
					if wantedAttrs[kv.Key] {
						sp.attrs[kv.Key] = kv.Value.String()
					}
				}
				if s.Status.Code == 2 {
					sp.attrs["otel.status"] = "error"
				}
				out = append(out, sp)
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- aggregation

var (
	routeParamRe = regexp.MustCompile(`<(?:[a-z_]+:)?([A-Za-z_][A-Za-z0-9_]*)>|:([A-Za-z_][A-Za-z0-9_]*)|\[([A-Za-z_][A-Za-z0-9_.]*)\]|\{([A-Za-z_][A-Za-z0-9_]*)(?::[^}]*)?\}`)
)

// normalizeRoute unifies route templates of Django, Flask, Express, Gin, gorilla, Next.js, Laravel, FastAPI into "/orders/{id}".
func normalizeRoute(route string) string {
	route = routeParamRe.ReplaceAllStringFunc(route, func(m string) string {
		sub := routeParamRe.FindStringSubmatch(m)
		for _, name := range sub[1:] {
			if name != "" {
				return "{" + name + "}"
			}
		}
		return m
	})
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}
	return truncate(route, 150)
}

func first(attrs map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := attrs[k]; v != "" {
			return v
		}
	}
	return ""
}

func (a *Aggregator) appBucket(minute int64) *protocol.AppBucket {
	if a.app == nil {
		a.app = map[int64]*protocol.AppBucket{}
	}
	b := a.app[minute]
	if b == nil {
		b = &protocol.AppBucket{Minute: minute, Routes: map[string]protocol.RouteStat{}, Queries: map[string]protocol.AppQuery{}}
		a.app[minute] = b
	}
	return b
}

// addTrace turns one finished trace into route, query and HTTP statistics.
func (a *Aggregator) addTrace(spans []span, codeRoot string) {
	// The root is what the application was doing: answering a request (server) or running a job
	// or a scheduled command (consumer).
	var root *span
	for i := range spans {
		kind := spans[i].kind
		if kind != int32(trace.Span_SPAN_KIND_SERVER) && kind != int32(trace.Span_SPAN_KIND_CONSUMER) {
			continue
		}
		if root == nil || spans[i].start.Before(root.start) {
			root = &spans[i]
		}
	}

	// A queued job and a scheduled command are not endpoints: the framework package says which
	// it is, and a trace carrying a queue is a job even when it does not say so.
	jobKind := ""
	if root != nil {
		jobKind = first(root.attrs, "slowpoke.kind")
		if jobKind != "job" && jobKind != "command" {
			jobKind = ""
			if root.kind == int32(trace.Span_SPAN_KIND_CONSUMER) || first(root.attrs, "messaging.destination.name", "messaging.operation") != "" {
				jobKind = "job"
			}
		}
	}

	route := "(background)"
	var method, source string
	status := 0
	var seconds float64
	if root != nil {
		method = strings.ToUpper(first(root.attrs, "http.request.method", "http.method"))
		if r := first(root.attrs, "http.route"); r != "" {
			route = method + " " + normalizeRoute(r)
		} else if p := first(root.attrs, "url.path", "http.target"); p != "" {
			route = method + " " + NormalizePath(p)
		} else {
			route = strings.TrimSpace(method + " " + root.name)
		}
		if jobKind != "" {
			route = jobKind + " " + root.name
		}
		status, _ = strconv.Atoi(first(root.attrs, "http.response.status_code", "http.status_code"))
		seconds = root.end.Sub(root.start).Seconds()
		source = "otlp:" + firstNonEmptyString(root.service, "app")
		// The host the application answered for: when a web server on another machine reports
		// the same host, these are the same requests and only one of the two is counted.
		if jobKind == "" {
			a.SourceSite(source, first(root.attrs, "server.address", "url.domain", "http.host", "net.host.name"))
		}
	}

	type queryAgg struct {
		fp, sample, origin string
		n                  int
		sum                float64
	}
	queries := map[string]*queryAgg{}
	var dbSum float64
	var dbCount int
	minuteTime := time.Now()
	if root != nil {
		minuteTime = root.end
	} else if len(spans) > 0 {
		minuteTime = spans[0].end
	}
	byID := make(map[string]*span, len(spans))
	for i := range spans {
		byID[spans[i].spanID] = &spans[i]
	}
	statement := func(s *span) string { return first(s.attrs, "db.query.text", "db.statement") }
	for i := range spans {
		s := &spans[i]
		stmt := statement(s)
		if stmt == "" || ignoredStatement(stmt) {
			continue
		}
		// ORM and driver instrumentations nest one database span in another for the same
		// statement (SQLAlchemy over psycopg2): only the outermost one counts.
		nested := false
		for p, hops := byID[s.parentID], 0; p != nil && hops < 50; p, hops = byID[p.parentID], hops+1 {
			if statement(p) != "" {
				nested = true
				break
			}
		}
		if nested {
			continue
		}
		fp := fingerprint.Of(stmt)
		hash := fingerprint.Hash(fp)
		d := s.end.Sub(s.start).Seconds()
		q := queries[hash]
		if q == nil {
			q = &queryAgg{fp: fp, sample: truncate(fingerprint.Redact(stmt), maxSampleLen)}
			queries[hash] = q
		}
		q.n++
		q.sum += d
		if q.origin == "" {
			q.origin = codeOrigin(s, byID, codeRoot)
		}
		dbSum += d
		dbCount++
	}

	calls := outboundCalls(spans, byID, root, codeRoot)

	a.mu.Lock()
	defer a.mu.Unlock()
	minute := minuteOf(minuteTime)
	b := a.appBucket(minute)

	rs := b.Routes[route]
	if root != nil {
		rs.N++
		rs.Sum += seconds
		rs.Max = math.Max(rs.Max, seconds)
		if status >= 500 {
			rs.E5++
		}
	}
	rs.Queries += dbCount
	rs.DBSum += dbSum
	if rs.Hashes == nil {
		rs.Hashes = map[string]protocol.RouteQuery{}
	}
	for hash, q := range queries {
		rq := rs.Hashes[hash]
		rq.N += q.n
		rq.Sum += q.sum
		rq.Requests++
		rs.Hashes[hash] = rq

		aq := b.Queries[hash]
		aq.Fingerprint = truncate(q.fp, maxSampleLen)
		aq.Sample = q.sample
		aq.N += q.n
		aq.Sum += q.sum
		if q.origin != "" {
			aq.Origin = truncate(q.origin, 300)
		}
		if q.n >= nPlusOneRepeats {
			aq.NPlusOne++
		}
		b.Queries[hash] = aq
	}
	if len(calls.hosts) > 0 {
		rs.ExtSum += calls.waited
		rs.ExtRequests++
		if rs.Ext == nil {
			rs.Ext = map[string]protocol.ExtCall{}
		}
		for host, c := range calls.hosts {
			if _, known := rs.Ext[host]; !known && len(rs.Ext) >= maxExtHostsPerRoute {
				host = otherHost
			}
			e := rs.Ext[host]
			e.N += c.N
			e.Sum += c.Sum
			e.Max = math.Max(e.Max, c.Max)
			e.Errors += c.Errors
			e.Requests++
			if e.Origin == "" {
				e.Origin = c.Origin
			}
			rs.Ext[host] = e
		}
	}
	b.Routes[route] = rs

	if root != nil {
		a.mu.Unlock()
		if jobKind != "" {
			a.AddJob(root.end, jobKind, root.name, seconds, root.attrs["otel.status"] == "error")
		} else {
			path := strings.TrimPrefix(route, method+" ")
			a.AddRequest(source, root.end, method, path, status, seconds, 1)
		}
		a.mu.Lock()
	}
}

// codeOrigin is the first application file found on the span or its ancestors: ORMs put
// code attributes on their own parent span, and framework files are never an answer.
func codeOrigin(s *span, byID map[string]*span, codeRoot string) string {
	for hops := 0; s != nil && hops < 50; s, hops = byID[s.parentID], hops+1 {
		file := first(s.attrs, "code.file.path", "code.filepath")
		if file == "" || thirdPartyCode(file) {
			continue
		}
		if codeRoot != "" && strings.HasPrefix(file, codeRoot) {
			file = strings.TrimPrefix(file, codeRoot)
		} else if strings.HasPrefix(file, "/") {
			// An SDK that sends the path on disk: cut it down to the project. A path that is
			// already relative comes from one of our packages, which made it relative to the
			// application root - shortening it further would point at a file that is not there.
			file = strings.TrimPrefix(relativeFrame(" "+file), " ")
		}
		if thirdPartyCode(file) {
			continue
		}
		if line := first(s.attrs, "code.line.number", "code.lineno"); line != "" {
			file += ":" + line
		}
		return file
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
