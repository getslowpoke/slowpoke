package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// A queue is where work waits, and waiting is what Slowpoke prices. RabbitMQ knows how deep every
// queue is and how fast it drains; the management API it ships with says both.
func rabbitServer(t *testing.T, queues []map[string]any, overview map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "slowpoke" || pass != "s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/overview"):
			_ = json.NewEncoder(w).Encode(overview)
		case strings.HasPrefix(r.URL.Path, "/api/queues"):
			_ = json.NewEncoder(w).Encode(queues)
		default:
			http.NotFound(w, r)
		}
	}))
}

func queue(name string, ready, unacked, consumers int, publish, deliver float64) map[string]any {
	return map[string]any{
		"name": name, "vhost": "/", "messages_ready": ready, "messages_unacknowledged": unacked,
		"consumers": consumers,
		"message_stats": map[string]any{
			"publish_details":    map[string]any{"rate": publish},
			"deliver_get_details": map[string]any{"rate": deliver},
		},
	}
}

func rabbitMetrics(t *testing.T, s *rabbitmqSource, now time.Time) map[string]float64 {
	t.Helper()
	a := NewAggregator()
	if err := s.poll(context.Background(), a, now); err != nil {
		t.Fatalf("poll: %v", err)
	}
	out := map[string]float64{}
	for _, m := range drainAll(a).Metrics {
		out[m.Name] = m.Last
		if m.Kind == "counter" {
			out[m.Name] = m.Sum
		}
	}
	return out
}

func TestRabbitMQReadsTheDepthAndTheDrainOfEveryQueue(t *testing.T) {
	now := time.Now()
	srv := rabbitServer(t,
		[]map[string]any{
			queue("emails", 1200, 3, 2, 40, 10),
			queue("invoices", 0, 0, 1, 2, 2),
			queue("stuck", 500, 0, 0, 5, 0),
		},
		map[string]any{
			"object_totals": map[string]any{"connections": 4, "channels": 6, "queues": 3, "consumers": 3},
			"queue_totals":  map[string]any{"messages_ready": 1700, "messages_unacknowledged": 3},
		})
	defer srv.Close()

	s, err := newRabbitMQSource(Source{Type: "rabbitmq", Name: "broker", URL: srv.URL, User: "slowpoke", Password: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	got := rabbitMetrics(t, s, now)

	for name, want := range map[string]float64{
		"rabbitmq.broker.ready":                    1700,
		"rabbitmq.broker.unacked":                  3,
		"rabbitmq.broker.connections":              4,
		"rabbitmq.broker.queues":                   3,
		"rabbitmq.broker.consumers":                3,
		"rabbitmq.broker.queue.emails.ready":       1200,
		"rabbitmq.broker.queue.emails.unacked":     3,
		"rabbitmq.broker.queue.emails.consumers":   2,
		"rabbitmq.broker.queue.emails.deliver_rate": 10,
		// 1200 waiting, ten a second going out: two minutes before a message added now is handled.
		"rabbitmq.broker.queue.emails.wait_seconds": 120,
		"rabbitmq.broker.queue.stuck.consumers":     0,
		"rabbitmq.broker.queue.stuck.ready":         500,
	} {
		if v, ok := got[name]; !ok || v != want {
			t.Errorf("%s = %v (present: %v), want %v", name, v, ok, want)
		}
	}
	// Nobody is draining that queue: a wait of infinity is not a number to put on a chart.
	if _, ok := got["rabbitmq.broker.queue.stuck.wait_seconds"]; ok {
		t.Error("a queue with nothing consuming it must not report a waiting time")
	}
	// An empty queue is not a problem: it says so with a zero, not with silence.
	if v, ok := got["rabbitmq.broker.queue.invoices.ready"]; !ok || v != 0 {
		t.Errorf("invoices.ready = %v (present %v)", v, ok)
	}
}

func TestRabbitMQKeepsItsMetricsBounded(t *testing.T) {
	var many []map[string]any
	for i := 0; i < 400; i++ {
		many = append(many, queue(fmt.Sprintf("queue-%03d", i), i, 0, 1, 1, 1))
	}
	// Names from the wild: a slash, a space, and something far too long for a metric.
	many = append(many, queue("orders/high priority", 900, 0, 1, 10, 5), queue(strings.Repeat("x", 300), 950, 0, 1, 1, 1))
	srv := rabbitServer(t, many, map[string]any{})
	defer srv.Close()

	s, err := newRabbitMQSource(Source{Type: "rabbitmq", Name: "b", URL: srv.URL, User: "slowpoke", Password: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	got := rabbitMetrics(t, s, time.Now())

	queues := map[string]bool{}
	for name := range got {
		// ...queue.<name>.<metric>, and a sanitised name may carry dots of its own.
		if rest, ok := strings.CutPrefix(name, "rabbitmq.b.queue."); ok {
			queues[rest[:strings.LastIndex(rest, ".")]] = true
		}
		if strings.ContainsAny(name, " /") || len(name) > 120 {
			t.Fatalf("a metric name that no chart can carry: %q", name)
		}
	}
	if len(queues) > maxRabbitQueues {
		t.Fatalf("%d queues reported, at most %d", len(queues), maxRabbitQueues)
	}
	// The deepest queues are the ones worth reporting.
	if !queues["orders.high_priority"] {
		t.Fatalf("the deepest queue was dropped: %v", queues)
	}
}

func TestRabbitMQSaysWhatIsWrongInsteadOfCrashing(t *testing.T) {
	deaf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer deaf.Close()
	s, _ := newRabbitMQSource(Source{Type: "rabbitmq", Name: "b", URL: deaf.URL, User: "wrong", Password: "wrong"})
	err := s.poll(context.Background(), NewAggregator(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("a refused login must say so: %v", err)
	}

	nonsense := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not the management plugin</html>"))
	}))
	defer nonsense.Close()
	s, _ = newRabbitMQSource(Source{Type: "rabbitmq", Name: "b", URL: nonsense.URL})
	if err := s.poll(context.Background(), NewAggregator(), time.Now()); err == nil {
		t.Fatal("an answer that is not the management API must be an error")
	}

	// The management plugin is off: that is the one thing to tell the person, once.
	if _, err := newRabbitMQSource(Source{Type: "rabbitmq", Name: "b", URL: "not a url at all"}); err == nil {
		t.Fatal("a url that is not one must be refused when the source is built")
	}
}

// Against a real broker, when there is one: RABBITMQ_URL=http://127.0.0.1:15672 ./scripts/go test
// ./internal/agent/ -run TestRabbitMQAgainstARealBroker. The canned answers above say what the
// source does with the numbers; this says the numbers are the ones RabbitMQ actually gives.
func TestRabbitMQAgainstARealBroker(t *testing.T) {
	url := os.Getenv("RABBITMQ_URL")
	if url == "" {
		t.Skip("set RABBITMQ_URL to run this against a broker")
	}
	s, err := newRabbitMQSource(Source{Type: "rabbitmq", Name: "lab", URL: url,
		User: envOr("RABBITMQ_USER", "guest"), Password: envOr("RABBITMQ_PASSWORD", "guest")})
	if err != nil {
		t.Fatal(err)
	}
	got := rabbitMetrics(t, s, time.Now())
	for _, name := range []string{"rabbitmq.lab.ready", "rabbitmq.lab.queues", "rabbitmq.lab.connections"} {
		if _, ok := got[name]; !ok {
			t.Errorf("%s missing from %v", name, got)
		}
	}
	if got["rabbitmq.lab.queue.emails.ready"] <= 0 {
		t.Errorf("the queue with messages in it reads empty: %v", got)
	}
	t.Logf("from the broker: %v", got)
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
