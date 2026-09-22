package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

const maxMetricsPerMinute = 500

type metricKey struct {
	name   string
	minute int64
}

// AddMetric records one value of a custom metric. Timers are in seconds.
func (a *Aggregator) AddMetric(t time.Time, name, kind string, value float64) {
	name = truncate(strings.TrimSpace(name), 191)
	if name == "" || (kind != "counter" && kind != "gauge" && kind != "timer") {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.metrics == nil {
		a.metrics = map[metricKey]*protocol.MetricBucket{}
	}
	key := metricKey{name, minuteOf(t)}
	b := a.metrics[key]
	if b == nil {
		if len(a.metrics) >= maxMetricsPerMinute*5 {
			return
		}
		b = &protocol.MetricBucket{Minute: key.minute, Name: name, Kind: kind, Min: value, Max: value}
		if kind == "timer" {
			b.Hist = make([]int, len(protocol.HistogramEdges)+1)
		}
		a.metrics[key] = b
	}
	if b.Kind != kind {
		return
	}
	b.Count++
	b.Sum += value
	b.Min = min(b.Min, value)
	b.Max = max(b.Max, value)
	b.Last = value
	if kind == "timer" {
		b.Hist[protocol.HistogramIndex(value)]++
	}
}

// ParseStatsD handles "name:value|type[|@rate][|#tags]" (c, g, ms, h, d).
func ParseStatsD(a *Aggregator, now func() time.Time, line string) {
	nameValue, rest, ok := strings.Cut(strings.TrimSpace(line), "|")
	if !ok {
		return
	}
	name, rawValue, ok := strings.Cut(nameValue, ":")
	if !ok || name == "" {
		return
	}
	value, err := strconv.ParseFloat(rawValue, 64)
	if err != nil {
		return
	}
	parts := strings.Split(rest, "|")
	rate := 1.0
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "@") {
			if r, err := strconv.ParseFloat(p[1:], 64); err == nil && r > 0 && r <= 1 {
				rate = r
			}
		}
	}
	switch parts[0] {
	case "c":
		a.AddMetric(now(), name, "counter", value/rate)
	case "g":
		a.AddMetric(now(), name, "gauge", value)
	case "ms":
		a.AddMetric(now(), name, "timer", value/1000)
	case "h", "d":
		a.AddMetric(now(), name, "timer", value)
	}
}

// listenStatsD receives StatsD packets on UDP until the context ends.
func listenStatsD(ctx context.Context, addr string, a *Aggregator) error {
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("statsd: read failed", "error", err)
				}
				return
			}
			for _, line := range strings.Split(string(buf[:n]), "\n") {
				ParseStatsD(a, time.Now, line)
			}
		}
	}()
	return nil
}

// promScraper reads a Prometheus text endpoint: counters become deltas, gauges keep their value.
type promScraper struct {
	src  Source
	prev map[string]float64
}

func newPromScraper(src Source) *promScraper {
	return &promScraper{src: src, prev: map[string]float64{}}
}

var promLineRe = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*(?:\{[^}]*\})?)\s+([-+0-9.eE]+|NaN|\+Inf|-Inf)(?:\s+\d+)?$`)

func (p *promScraper) ingest(a *Aggregator, now time.Time, body string) {
	types := map[string]string{}
	s := bufio.NewScanner(strings.NewReader(body))
	s.Buffer(make([]byte, 1024*1024), 1024*1024)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if strings.HasPrefix(line, "# TYPE ") {
			if f := strings.Fields(line); len(f) == 4 {
				types[f[2]] = f[3]
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := promLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		value, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		series := m[1]
		family := series
		if i := strings.Index(series, "{"); i >= 0 {
			family = series[:i]
		}
		name := p.src.Name + "." + series
		switch types[family] {
		case "counter":
			if prev, ok := p.prev[series]; ok && value >= prev {
				a.AddMetric(now, name, "counter", value-prev)
			}
			p.prev[series] = value
		case "gauge":
			a.AddMetric(now, name, "gauge", value)
		}
	}
}

func (p *promScraper) scrape(ctx context.Context, a *Aggregator, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.src.URL, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 20<<20))
	if err != nil {
		return err
	}
	p.ingest(a, now, string(raw))
	return nil
}
