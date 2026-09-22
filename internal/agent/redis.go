package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// redisSource reads what Redis already knows about itself: the INFO counters and the slow
// command log. Commands are recorded by name only: keys and values never leave the machine.
type redisSource struct {
	src        Source
	addr       string
	conn       net.Conn
	rd         *bufio.Reader
	prev       map[string]float64
	lastSlowID int64
	started    bool
}

const (
	redisTimeout   = 5 * time.Second
	redisSlowLogN  = 128
	maxSlowCommand = 64
)

func newRedisSource(src Source) (*redisSource, error) {
	addr := src.Addr
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	if src.Name == "" {
		src.Name = "redis"
	}
	return &redisSource{src: src, addr: addr, prev: map[string]float64{}}, nil
}

func (r *redisSource) close() {
	if r.conn != nil {
		r.conn.Close()
		r.conn = nil
	}
}

func (r *redisSource) dial(ctx context.Context) error {
	if r.conn != nil {
		return nil
	}
	d := net.Dialer{Timeout: redisTimeout}
	conn, err := d.DialContext(ctx, "tcp", r.addr)
	if err != nil {
		return err
	}
	r.conn, r.rd = conn, bufio.NewReader(conn)
	if r.src.Password != "" {
		if _, err := r.command("AUTH", r.src.Password); err != nil {
			r.close()
			return fmt.Errorf("auth: %w", err)
		}
	}
	return nil
}

// poll reads INFO and the new slow log entries. Anything unreadable closes the connection:
// the next round starts a new one.
func (r *redisSource) poll(ctx context.Context, agg *Aggregator, now time.Time) error {
	if err := r.dial(ctx); err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	if deadline.IsZero() {
		deadline = time.Now().Add(redisTimeout)
	}
	_ = r.conn.SetDeadline(deadline)

	info, err := r.command("INFO")
	if err != nil {
		r.close()
		return err
	}
	text, _ := info.(string)
	r.metrics(agg, now, parseRedisInfo(text))

	if r.src.enabled(r.src.SlowLog) {
		reply, err := r.command("SLOWLOG", "GET", strconv.Itoa(redisSlowLogN))
		if err != nil {
			r.close()
			return err
		}
		r.slowCommands(agg, now, reply)
	}
	r.started = true
	return nil
}

func (r *redisSource) name(metric string) string {
	return "redis." + r.src.Name + "." + metric
}

func (r *redisSource) metrics(agg *Aggregator, now time.Time, info map[string]float64) {
	gauge := func(metric, key string) {
		if v, ok := info[key]; ok {
			agg.AddMetric(now, r.name(metric), "gauge", v)
		}
	}
	gauge("ops_per_sec", "instantaneous_ops_per_sec")
	gauge("connected", "connected_clients")
	gauge("blocked", "blocked_clients")
	gauge("memory_bytes", "used_memory")
	gauge("fragmentation", "mem_fragmentation_ratio")
	gauge("keys", "keys")
	if used, max := info["used_memory"], info["maxmemory"]; max > 0 {
		agg.AddMetric(now, r.name("memory_percent"), "gauge", used/max*100)
	}
	// Hit rate of the interval: the totals are cumulative since the server started.
	hits, misses := info["keyspace_hits"], info["keyspace_misses"]
	dh, dm := hits-r.prev["keyspace_hits"], misses-r.prev["keyspace_misses"]
	if !r.started {
		dh, dm = hits, misses
	}
	if dh+dm > 0 {
		agg.AddMetric(now, r.name("hit_rate"), "gauge", dh/(dh+dm)*100)
	}
	for metric, key := range map[string]string{"commands": "total_commands_processed", "evicted": "evicted_keys", "expired": "expired_keys", "rejected": "rejected_connections"} {
		v, ok := info[key]
		if !ok {
			continue
		}
		if r.started {
			if d := v - r.prev[key]; d >= 0 {
				agg.AddMetric(now, r.name(metric), "counter", d)
			}
		}
		r.prev[key] = v
	}
	r.prev["keyspace_hits"], r.prev["keyspace_misses"] = hits, misses
}

// slowCommands turns new SLOWLOG entries into timers named after the command only.
func (r *redisSource) slowCommands(agg *Aggregator, now time.Time, reply any) {
	entries, ok := reply.([]any)
	if !ok {
		return
	}
	highest := r.lastSlowID
	for _, e := range entries {
		fields, ok := e.([]any)
		if !ok || len(fields) < 4 {
			continue
		}
		id, _ := fields[0].(int64)
		when, _ := fields[1].(int64)
		micros, _ := fields[2].(int64)
		args, _ := fields[3].([]any)
		if id > highest {
			highest = id
		}
		// The first round only remembers where the log is: those commands are already gone by.
		if !r.started || id <= r.lastSlowID || len(args) == 0 {
			continue
		}
		command, _ := args[0].(string)
		command = strings.ToUpper(strings.TrimSpace(command))
		if command == "" || len(command) > maxSlowCommand || !isCommandName(command) {
			continue
		}
		at := now
		if when > 0 {
			at = time.Unix(when, 0)
		}
		agg.AddMetric(at, r.name("slow."+command), "timer", float64(micros)/1e6)
	}
	r.lastSlowID = highest
}

func isCommandName(s string) bool {
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// parseRedisInfo reads the "key:value" lines of INFO, plus the keys of every database.
func parseRedisInfo(text string) map[string]float64 {
	out := map[string]float64{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.HasPrefix(key, "db") && strings.Contains(value, "keys=") {
			for _, part := range strings.Split(value, ",") {
				if k, v, ok := strings.Cut(part, "="); ok && k == "keys" {
					if n, err := strconv.ParseFloat(v, 64); err == nil {
						out["keys"] += n
					}
				}
			}
			continue
		}
		if v, err := strconv.ParseFloat(value, 64); err == nil {
			out[key] = v
		}
	}
	return out
}

// --- the little RESP client ---

func (r *redisSource) command(args ...string) (any, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := r.conn.Write([]byte(b.String())); err != nil {
		return nil, err
	}
	return readReply(r.rd)
}

func readReply(rd *bufio.Reader) (any, error) {
	line, err := rd.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return nil, errors.New("empty reply")
	}
	body := line[1:]
	switch line[0] {
	case '+':
		return body, nil
	case '-':
		return nil, errors.New(body)
	case ':':
		n, err := strconv.ParseInt(body, 10, 64)
		return n, err
	case '$':
		n, err := strconv.Atoi(body)
		if err != nil || n < 0 {
			return "", err
		}
		buf := make([]byte, n+2)
		if _, err := readFull(rd, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(body)
		if err != nil || n < 0 {
			return []any{}, err
		}
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			item, err := readReply(rd)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	}
	return nil, fmt.Errorf("unexpected reply %q", line)
}

func readFull(rd *bufio.Reader, buf []byte) (int, error) {
	read := 0
	for read < len(buf) {
		n, err := rd.Read(buf[read:])
		read += n
		if err != nil {
			return read, err
		}
	}
	return read, nil
}

// check tells what the agent can read on this Redis: the connection, the counters of INFO and
// the slow command log, which the server may have turned off.
func (r *redisSource) check(ctx context.Context) (map[string]string, []protocol.CheckItem) {
	facts := map[string]string{"address": r.addr}
	if err := r.dial(ctx); err != nil {
		return facts, []protocol.CheckItem{item("connection", StatusError, false, "connect_failed", err.Error())}
	}
	_ = r.conn.SetDeadline(time.Now().Add(redisTimeout))
	info, err := r.command("INFO", "server")
	if err != nil {
		r.close()
		return facts, []protocol.CheckItem{item("connection", StatusError, false, "connect_failed", err.Error())}
	}
	text, _ := info.(string)
	for _, line := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "redis_version:"); ok {
			facts["version"] = v
		}
	}
	items := []protocol.CheckItem{item("connection", StatusOK, false, "", ""), item("metrics", StatusOK, true, "", "")}

	slow := item("slow_log", StatusOK, true, "", "")
	switch {
	case !r.src.enabled(r.src.SlowLog):
		slow = item("slow_log", StatusOff, true, "disabled", "")
	default:
		reply, err := r.command("CONFIG", "GET", "slowlog-log-slower-than")
		values, _ := reply.([]any)
		if err != nil || len(values) < 2 {
			slow = item("slow_log", StatusError, true, "no_config_grant", fmt.Sprint(err))
			break
		}
		threshold, _ := values[1].(string)
		facts["slowlog_threshold_us"] = threshold
		if threshold == "-1" {
			slow = item("slow_log", StatusOff, true, "slow_log_off", "")
		}
	}
	return facts, append(items, slow)
}
