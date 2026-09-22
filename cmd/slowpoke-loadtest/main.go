// Command slowpoke-loadtest simulates many busy agents against a server and
// reports throughput, latency percentiles, backpressure and queue drain time.
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
)

type result struct {
	status  int
	latency time.Duration
	err     bool
}

func main() {
	server := flag.String("server", "http://localhost:8080", "server URL")
	token := flag.String("token", os.Getenv("SLOWPOKE_TOKEN"), "project token")
	agents := flag.Int("agents", 100, "simulated agents")
	duration := flag.Duration("duration", time.Minute, "test duration")
	interval := flag.Duration("interval", 2*time.Second, "time between payloads of one agent (real agents: 30s)")
	endpoints := flag.Int("endpoints", 150, "endpoints per payload")
	queries := flag.Int("queries", 150, "queries per payload")
	flag.Parse()
	if *token == "" {
		fmt.Fprintln(os.Stderr, "missing -token")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: *agents}}
	results := make(chan result, 100000)
	var sent atomic.Int64

	var wg sync.WaitGroup
	start := time.Now()
	for a := 0; a < *agents; a++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			time.Sleep(time.Duration(rng.Int63n(int64(*interval))))
			tick := time.NewTicker(*interval)
			defer tick.Stop()
			for n := 0; ; n++ {
				body := payload(id, n, *endpoints, *queries, rng)
				req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(*server, "/")+"/api/v1/ingest", bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+*token)
				req.Header.Set("Content-Encoding", "gzip")
				t0 := time.Now()
				res, err := client.Do(req)
				r := result{latency: time.Since(t0)}
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					r.err = true
				} else {
					r.status = res.StatusCode
					res.Body.Close()
				}
				sent.Add(1)
				results <- r
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
			}
		}(a)
	}

	var all []result
	done := make(chan struct{})
	go func() {
		for r := range results {
			all = append(all, r)
		}
		close(done)
	}()
	progress := time.NewTicker(10 * time.Second)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-progress.C:
				fmt.Printf("  %5.0fs  sent %d  queue %d\n", time.Since(start).Seconds(), sent.Load(), queueDepth(client, *server, *token))
			}
		}
	}()
	wg.Wait()
	close(results)
	<-done
	elapsed := time.Since(start)

	var latencies []time.Duration
	codes := map[int]int{}
	errors := 0
	for _, r := range all {
		if r.err {
			errors++
			continue
		}
		codes[r.status]++
		latencies = append(latencies, r.latency)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	pct := func(p float64) time.Duration {
		if len(latencies) == 0 {
			return 0
		}
		return latencies[int(float64(len(latencies)-1)*p)]
	}

	fmt.Printf("\nSlowpoke load test\n")
	fmt.Printf("  agents %d · every %s · %d endpoints + %d queries per payload · %s\n", *agents, *interval, *endpoints, *queries, elapsed.Round(time.Second))
	fmt.Printf("  payloads %d (%.1f/s) · equivalent to %d real agents at 30s\n", len(all), float64(len(all))/elapsed.Seconds(), int(float64(*agents)*30/interval.Seconds()))
	fmt.Printf("  latency p50 %s · p95 %s · p99 %s · max %s\n", pct(0.5).Round(time.Millisecond), pct(0.95).Round(time.Millisecond), pct(0.99).Round(time.Millisecond), pct(1).Round(time.Millisecond))
	fmt.Printf("  status %v · network errors %d\n", codes, errors)

	drainStart := time.Now()
	for queueDepth(client, *server, *token) > 0 && time.Since(drainStart) < 10*time.Minute {
		time.Sleep(time.Second)
	}
	fmt.Printf("  queue drained in %s after the test\n", time.Since(drainStart).Round(time.Second))
	if errors > 0 || codes[500] > 0 {
		os.Exit(1)
	}
}

func queueDepth(c *http.Client, server, token string) int {
	req, _ := http.NewRequest(http.MethodGet, strings.TrimRight(server, "/")+"/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.Do(req)
	if err != nil {
		return -1
	}
	defer res.Body.Close()
	var out struct {
		Depth int `json:"queue_depth"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return out.Depth
}

func payload(agent, n, endpoints, queries int, rng *rand.Rand) []byte {
	minute := time.Now().Add(-time.Minute).Unix() / 60 * 60
	hist := make([]int, len(protocol.HistogramEdges)+1)
	hb := protocol.HTTPBucket{Source: "nginx:app", Minute: minute, Hist: hist, Endpoints: map[string]protocol.EndpointStat{}}
	for i := 0; i < endpoints; i++ {
		c := 1 + rng.Intn(50)
		d := rng.ExpFloat64() * 0.2
		hb.N += c
		hb.Sum += d * float64(c)
		hist[protocol.HistogramIndex(d)] += c
		e := protocol.EndpointStat{N: c, Sum: d * float64(c), Max: d * 2}
		if d > 1 {
			e.Slow, e.SlowSum = c, d*float64(c)
		}
		hb.Endpoints[fmt.Sprintf("GET /svc-%d/resource-%d/{id}", agent%20, i)] = e
	}
	db := protocol.DBBucket{Source: "mysql-digest", Minute: minute, Queries: map[string]protocol.QueryStat{}}
	for i := 0; i < queries; i++ {
		fp := fmt.Sprintf("select * from table_%d_%d where id = ?", agent%20, i)
		db.Queries[fingerprint.Hash(fp)] = protocol.QueryStat{Fingerprint: fp, Sample: strings.Replace(fp, "?", "0", 1), N: int64(1 + rng.Intn(100)), Sum: rng.Float64() * 5, Max: rng.Float64()}
	}
	p := protocol.Payload{
		BatchID: fmt.Sprintf("load-%d-%d-%d", agent, n, rng.Int63()),
		Agent:   protocol.AgentInfo{Host: fmt.Sprintf("load-agent-%d", agent), Kind: "agent", Version: "loadtest", Sources: []string{"nginx:app", "mysql:db"}},
		HTTP:    []protocol.HTTPBucket{hb},
		DB:      []protocol.DBBucket{db},
		Host:    []protocol.HostSample{{Minute: minute, CPU: rng.Float64() * 100, MemUsed: 4 << 30, MemTotal: 8 << 30}},
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_ = json.NewEncoder(zw).Encode(p)
	_ = zw.Close()
	return buf.Bytes()
}
