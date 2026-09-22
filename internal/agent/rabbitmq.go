package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// rabbitmqSource reads what RabbitMQ already knows about itself, through the management API that
// ships with it: how deep every queue is, how fast it drains, and how many consumers are on it.
// A queue is where work waits, and waiting is the thing Slowpoke puts a price on.
//
// Two GETs per round, nothing written, no queue declared, no message read: the broker does not
// notice. Names and counts only - a message never leaves the machine.
type rabbitmqSource struct {
	src    Source
	base   string
	client *http.Client
}

const (
	rabbitTimeout = 5 * time.Second
	// The queues reported per round, deepest first. A broker with a queue per user would otherwise
	// turn into thousands of series that no chart can hold and nobody reads.
	maxRabbitQueues = 25
	maxQueueName    = 60
)

func newRabbitMQSource(src Source) (*rabbitmqSource, error) {
	raw := src.URL
	if raw == "" {
		raw = "http://127.0.0.1:15672"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("url %q is not an address of the management API (http://127.0.0.1:15672)", raw)
	}
	if src.Name == "" {
		src.Name = "rabbitmq"
	}
	return &rabbitmqSource{
		src:    src,
		base:   strings.TrimRight(u.String(), "/"),
		client: &http.Client{Timeout: rabbitTimeout},
	}, nil
}

func (r *rabbitmqSource) poll(ctx context.Context, agg *Aggregator, now time.Time) error {
	var overview struct {
		ObjectTotals struct {
			Connections float64 `json:"connections"`
			Channels    float64 `json:"channels"`
			Queues      float64 `json:"queues"`
			Consumers   float64 `json:"consumers"`
		} `json:"object_totals"`
		QueueTotals struct {
			Ready   float64 `json:"messages_ready"`
			Unacked float64 `json:"messages_unacknowledged"`
		} `json:"queue_totals"`
	}
	if err := r.get(ctx, "/api/overview", &overview); err != nil {
		return err
	}
	gauge := func(metric string, v float64) { agg.AddMetric(now, "rabbitmq."+r.src.Name+"."+metric, "gauge", v) }
	gauge("ready", overview.QueueTotals.Ready)
	gauge("unacked", overview.QueueTotals.Unacked)
	gauge("connections", overview.ObjectTotals.Connections)
	gauge("channels", overview.ObjectTotals.Channels)
	gauge("queues", overview.ObjectTotals.Queues)
	gauge("consumers", overview.ObjectTotals.Consumers)

	var queues []struct {
		Name      string  `json:"name"`
		VHost     string  `json:"vhost"`
		Ready     float64 `json:"messages_ready"`
		Unacked   float64 `json:"messages_unacknowledged"`
		Consumers float64 `json:"consumers"`
		Stats     struct {
			Publish struct {
				Rate float64 `json:"rate"`
			} `json:"publish_details"`
			Deliver struct {
				Rate float64 `json:"rate"`
			} `json:"deliver_get_details"`
		} `json:"message_stats"`
	}
	if err := r.get(ctx, "/api/queues?columns=name,vhost,messages_ready,messages_unacknowledged,consumers,message_stats.publish_details.rate,message_stats.deliver_get_details.rate", &queues); err != nil {
		return err
	}
	// Deepest first: those are the ones somebody is waiting on.
	sort.SliceStable(queues, func(i, j int) bool {
		return queues[i].Ready+queues[i].Unacked > queues[j].Ready+queues[j].Unacked
	})
	if len(queues) > maxRabbitQueues {
		queues = queues[:maxRabbitQueues]
	}
	for _, q := range queues {
		name := metricSafe(q.Name)
		if name == "" {
			continue
		}
		prefix := "rabbitmq." + r.src.Name + ".queue." + name + "."
		agg.AddMetric(now, prefix+"ready", "gauge", q.Ready)
		agg.AddMetric(now, prefix+"unacked", "gauge", q.Unacked)
		agg.AddMetric(now, prefix+"consumers", "gauge", q.Consumers)
		agg.AddMetric(now, prefix+"publish_rate", "gauge", q.Stats.Publish.Rate)
		agg.AddMetric(now, prefix+"deliver_rate", "gauge", q.Stats.Deliver.Rate)
		// How long a message added now would wait, at the rate this queue is draining. With
		// nothing draining it the answer is infinity, which is not a number to put on a chart:
		// the consumer count says that story instead.
		if q.Stats.Deliver.Rate > 0 {
			agg.AddMetric(now, prefix+"wait_seconds", "gauge", q.Ready/q.Stats.Deliver.Rate)
		}
	}
	return nil
}

func (r *rabbitmqSource) get(ctx context.Context, path string, into any) error {
	ctx, cancel := context.WithTimeout(ctx, rabbitTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+path, nil)
	if err != nil {
		return err
	}
	if r.src.User != "" || r.src.Password != "" {
		req.SetBasicAuth(r.src.User, r.src.Password)
	}
	res, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d: is the management plugin on, and may this user read it?", r.base+path, res.StatusCode)
	}
	// Bounded: a broker with thousands of queues must not be able to make the agent grow.
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%s did not answer with the management API: %w", r.base+path, err)
	}
	return nil
}

// metricSafe turns a queue name into something a metric name can carry: a person still recognises
// it, and no chart, path or query has to be escaped for it.
func metricSafe(name string) string {
	if len(name) > maxQueueName {
		name = name[:maxQueueName]
	}
	var b strings.Builder
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_':
			b.WriteRune(c)
		case c == '.' || c == '/' || c == ':':
			b.WriteRune('.')
		default:
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), ".")
}
