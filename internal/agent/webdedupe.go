package agent

import (
	"regexp"
	"strings"
	"time"
)

// With nginx in front of Laravel (or gunicorn, or Node) the same request is seen twice: in the
// access log and as an OpenTelemetry server span. Counting both doubles traffic, slowness and
// debt. The agent pairs single requests: same status and same path - where a route parameter
// may stand for any one segment, since the log sees a word and the application a placeholder -
// ending within a second, with the web server taking as long as the application plus a little
// (the wait for a free worker, and writing the response out). A service is behind the web
// server when 80% of its first requests pair up; from then on its requests are counted from
// the access log line only, which is the whole wait.
// Two apps exposing the same paths on the same machine do not pair: their timings differ.
//
// All of this only works where one agent sees both sides. When the web server runs elsewhere -
// an ingress, a load balancer, containers on another machine - the host each source answers for
// is what links them, and the server does the same job in metrics.Shadowed.

const (
	webRecognitionSamples = 20
	webRecognitionRatio   = 0.8
	webRingSize           = 20000
	webDecisionTimeout    = 5 * time.Minute
	webRecheckAfter       = 10 * time.Minute
)

type webVerdict int

const (
	webEmit webVerdict = iota
	webWait
	webDuplicate
)

type webRequest struct {
	shape  string
	status int
	end    time.Time
	dur    float64
	paired bool
}

type webDedupe struct {
	ring      []webRequest
	next      int
	pending   map[string][]webRequest // traced requests of services not decided yet
	firstSeen map[string]time.Time
	behind    map[string]bool      // decided: true = behind the web server, false = on its own
	recheck   map[string]time.Time // when an "on its own" verdict is sampled again
	webSince  time.Time            // first access log line read: earlier traces cannot pair
}

var paramShapeRe = regexp.MustCompile(`\{[^}]*\}`)

// endpointShape ignores parameter names: nginx guesses "/orders/{id}", Laravel says "/orders/{order}".
func endpointShape(endpoint string) string { return paramShapeRe.ReplaceAllString(endpoint, "{}") }

// shapeMatch says whether two shapes can be the same request. A web server turns into a
// placeholder only what it can recognise on its own - numbers, uuids, hashes - so a parameter
// that reads as a word stays a word in the log while the application calls it a parameter:
// "/user/me/services" is the route "/user/{id}/services". A placeholder therefore stands for
// exactly one segment, on whichever side it is. Everything else still has to agree, and what
// keeps two different applications apart is the status and the time they take, not the path.
func shapeMatch(a, b string) bool {
	for a != "" || b != "" {
		sa, resta := cutSegment(a)
		sb, restb := cutSegment(b)
		if sa != sb && sa != "{}" && sb != "{}" {
			return false
		}
		a, b = resta, restb
	}
	return true
}

// cutSegment splits off the text up to the next slash, and returns what follows it.
func cutSegment(s string) (string, string) {
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func webSource(source string) bool {
	return strings.HasPrefix(source, "nginx:") || strings.HasPrefix(source, "apache:") || strings.HasPrefix(source, "caddy:")
}

func (w *webDedupe) observe(source, endpoint string, status int, end time.Time, seconds float64) {
	r := webRequest{shape: endpointShape(endpoint), status: status, end: end, dur: seconds}
	switch {
	case webSource(source):
		if w.webSince.IsZero() {
			w.webSince = end
		}
		if len(w.ring) < webRingSize {
			w.ring = append(w.ring, r)
		} else {
			w.ring[w.next] = r
			w.next = (w.next + 1) % webRingSize
		}
	case strings.HasPrefix(source, "otlp:"):
		if w.pending == nil {
			w.pending, w.firstSeen, w.behind, w.recheck = map[string][]webRequest{}, map[string]time.Time{}, map[string]bool{}, map[string]time.Time{}
		}
		if behind, decided := w.behind[source]; decided {
			if behind || end.Before(w.recheck[source]) {
				return
			}
			delete(w.behind, source) // sample again: the verdict stays "on its own" meanwhile
			w.pending[source] = nil
		}
		if _, ok := w.firstSeen[source]; !ok {
			w.firstSeen[source] = end
		}
		if len(w.pending[source]) < 4*webRecognitionSamples {
			w.pending[source] = append(w.pending[source], r)
		}
	}
}

// pairs reports whether a traced request has an unpaired web server line for it.
func (w *webDedupe) pairs(t webRequest) bool {
	for i := range w.ring {
		r := &w.ring[i]
		if r.paired || r.status != t.status || !shapeMatch(r.shape, t.shape) {
			continue
		}
		// Access logs have second precision and are written when the response ends.
		if r.end.Before(t.end.Add(-2*time.Second)) || r.end.After(t.end.Add(time.Second)) {
			continue
		}
		extra := r.dur - t.dur
		if extra < -0.005 || extra > max(0.05, 0.25*t.dur) {
			continue
		}
		r.paired = true
		return true
	}
	return false
}

// decide pairs the traced requests old enough for their access log lines to have been read.
func (w *webDedupe) decide(now time.Time) {
	for source, reqs := range w.pending {
		var ready, kept []webRequest
		for _, r := range reqs {
			switch {
			case w.webSince.IsZero() || r.end.Before(w.webSince.Add(2*time.Second)):
				// No access log line read yet for that moment: this request says nothing.
			case r.end.Before(now.Add(-5 * time.Second)):
				ready = append(ready, r)
			default:
				kept = append(kept, r)
			}
		}
		w.pending[source] = append(ready, kept...)
		if len(ready) < webRecognitionSamples {
			continue
		}
		paired := 0
		for _, r := range ready {
			if w.pairs(r) {
				paired++
			}
		}
		w.behind[source] = float64(paired) >= webRecognitionRatio*float64(len(ready))
		if !w.behind[source] {
			w.recheck[source] = now.Add(webRecheckAfter)
		}
		delete(w.pending, source)
	}
}

// verdict says what to do with an OpenTelemetry HTTP bucket: send it, drop it as a duplicate,
// or keep it a little longer while the service is being recognised.
func (w *webDedupe) verdict(source string, minute int64, now time.Time) webVerdict {
	if !strings.HasPrefix(source, "otlp:") {
		return webEmit
	}
	if behind, decided := w.behind[source]; decided {
		if behind {
			return webDuplicate
		}
		return webEmit
	}
	if _, rechecking := w.recheck[source]; rechecking {
		return webEmit // was on its own: keep sending while sampled again
	}
	if len(w.ring) == 0 {
		return webEmit // no web server on this machine
	}
	if first, ok := w.firstSeen[source]; ok && now.Sub(first) < webDecisionTimeout {
		return webWait
	}
	return webEmit // too little traffic to tell: better counted twice than lost
}
