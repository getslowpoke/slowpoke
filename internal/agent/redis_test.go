package agent

import (
	"bufio"
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeRedis answers INFO and SLOWLOG GET with canned replies, one per call.
type fakeRedis struct {
	ln      net.Listener
	replies map[string]string
	calls   []string
}

func newFakeRedis(t *testing.T, replies map[string]string) *fakeRedis {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRedis{ln: ln, replies: replies}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if !strings.HasPrefix(line, "*") {
						continue
					}
					n := int(line[1] - '0')
					var args []string
					for i := 0; i < n; i++ {
						if _, err := r.ReadString('\n'); err != nil { // $len
							return
						}
						arg, err := r.ReadString('\n')
						if err != nil {
							return
						}
						args = append(args, strings.TrimSpace(arg))
					}
					cmd := strings.ToUpper(strings.Join(args, " "))
					f.calls = append(f.calls, cmd)
					reply, ok := f.replies[strings.Fields(cmd)[0]]
					if !ok {
						reply = "-ERR unknown\r\n"
					}
					if _, err := conn.Write([]byte(reply)); err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f
}

func bulk(s string) string {
	return "$" + itoa(len(s)) + "\r\n" + s + "\r\n"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

const redisInfo = `# Server
redis_version:7.2.5
# Clients
connected_clients:12
blocked_clients:1
# Memory
used_memory:52428800
maxmemory:104857600
mem_fragmentation_ratio:1.35
# Stats
total_commands_processed:1000
instantaneous_ops_per_sec:42
keyspace_hits:900
keyspace_misses:100
evicted_keys:5
expired_keys:7
rejected_connections:0
# Keyspace
db0:keys=17,expires=15,avg_ttl=3569100
`

// One slow command: id 7, unix time, 25000 microseconds, args GET session:ada@example.com.
var slowlogTwo = "*2\r\n" +
	"*6\r\n:7\r\n:1789660000\r\n:25000\r\n*2\r\n" + bulk("GET") + bulk("session:ada@example.com") + bulk("127.0.0.1:53124") + bulk("") +
	"*6\r\n:8\r\n:1789660001\r\n:150000\r\n*3\r\n" + bulk("HGETALL") + bulk("cart:42") + bulk("x") + bulk("127.0.0.1:53124") + bulk("") + ""

func TestRedisSourceMetricsAndSlowCommands(t *testing.T) {
	f := newFakeRedis(t, map[string]string{"INFO": bulk(redisInfo), "SLOWLOG": slowlogTwo})
	a := NewAggregator()
	r, err := newRedisSource(Source{Type: "redis", Name: "cache", Addr: f.ln.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	now := time.Unix(1789660010, 0)
	if err := r.poll(context.Background(), a, now); err != nil {
		t.Fatal(err)
	}
	d := drainAll(a)
	metrics := map[string]float64{}
	kinds := map[string]string{}
	for _, m := range d.Metrics {
		metrics[m.Name], kinds[m.Name] = m.Last, m.Kind
		if m.Kind == "timer" {
			metrics[m.Name] = m.Sum
		}
	}
	for name, want := range map[string]float64{
		"redis.cache.ops_per_sec":    42,
		"redis.cache.connected":      12,
		"redis.cache.blocked":        1,
		"redis.cache.memory_percent": 50,
		"redis.cache.hit_rate":       90,
		"redis.cache.keys":           17,
	} {
		if got := metrics[name]; got != want {
			t.Errorf("%s: got %v want %v (%+v)", name, got, want, metrics)
		}
	}
	if kinds["redis.cache.ops_per_sec"] != "gauge" {
		t.Fatalf("kinds %+v", kinds)
	}
	// The slow log already holds old commands when the agent starts: the first round only
	// remembers where it is, or every restart would invent a spike in the past.
	for name := range metrics {
		if strings.HasPrefix(name, "redis.cache.slow.") {
			t.Fatalf("old slow commands counted at startup: %s", name)
		}
	}

	// Same entries again: nothing new.
	if err := r.poll(context.Background(), a, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, m := range drainAll(a).Metrics {
		if strings.HasPrefix(m.Name, "redis.cache.slow.") {
			t.Fatalf("slow command counted twice: %+v", m)
		}
	}

	// A new entry (id 9) is recorded, by command name only: no key, no value.
	f.replies["SLOWLOG"] = "*1\r\n*6\r\n:9\r\n:1789660120\r\n:25000\r\n*2\r\n" + bulk("GET") + bulk("session:ada@example.com") + bulk("127.0.0.1:53124") + bulk("")
	if err := r.poll(context.Background(), a, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	slow := map[string]float64{}
	for _, m := range drainAll(a).Metrics {
		if strings.HasPrefix(m.Name, "redis.cache.slow.") {
			slow[m.Name] = m.Sum
			if m.Kind != "timer" {
				t.Fatalf("slow commands are timers: %+v", m)
			}
		}
	}
	if len(slow) != 1 || slow["redis.cache.slow.GET"] != 0.025 {
		t.Fatalf("slow commands: %+v", slow)
	}
	for name := range slow {
		if strings.Contains(name, "session:") || strings.Contains(name, "ada@example.com") {
			t.Fatalf("a key reached the metrics: %s", name)
		}
	}
}

func TestRedisSourceCountersAreDeltas(t *testing.T) {
	info2 := strings.Replace(redisInfo, "total_commands_processed:1000", "total_commands_processed:1600", 1)
	info2 = strings.Replace(info2, "evicted_keys:5", "evicted_keys:9", 1)
	f := newFakeRedis(t, map[string]string{"INFO": bulk(redisInfo), "SLOWLOG": "*0\r\n"})
	a := NewAggregator()
	r, _ := newRedisSource(Source{Type: "redis", Name: "cache", Addr: f.ln.Addr().String()})
	defer r.close()
	now := time.Unix(1789660010, 0)
	_ = r.poll(context.Background(), a, now)
	drainAll(a)
	f.replies["INFO"] = bulk(info2)
	_ = r.poll(context.Background(), a, now.Add(time.Minute))
	got := map[string]float64{}
	for _, m := range drainAll(a).Metrics {
		got[m.Name] = m.Sum
	}
	if got["redis.cache.commands"] != 600 || got["redis.cache.evicted"] != 4 {
		t.Fatalf("counters must be increases: %+v", got)
	}
}

// Against a real server: REDIS_ADDR=127.0.0.1:6379 ./scripts/go test ./internal/agent/ -run
// TestRedisAgainstARealServer. scripts/check-stores does it for you, with a server full of keys
// and a slow command in its log.
func TestRedisAgainstARealServer(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("set REDIS_ADDR to run this against a server")
	}
	s, err := newRedisSource(Source{Type: "redis", Name: "cache", Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// The first round only learns where the slow log is; the second is the minute a person sees.
	if err := s.poll(context.Background(), NewAggregator(), now); err != nil {
		t.Fatal(err)
	}
	a := NewAggregator()
	if err := s.poll(context.Background(), a, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := metricsByName(drainAll(a))
	for _, name := range []string{"redis.cache.memory_bytes", "redis.cache.connected", "redis.cache.keys"} {
		if _, ok := got[name]; !ok {
			t.Errorf("%s missing from %v", name, got)
		}
	}
	if got["redis.cache.keys"] <= 0 {
		t.Errorf("a server full of keys reported none: %v", got)
	}
	t.Logf("from the server: %v", got)
}
