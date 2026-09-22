// Package ingest writes agent and package payloads into the rollup tables.
package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
	"github.com/getslowpoke/slowpoke/internal/sqlsafe"
)

const (
	maxExplainsPerResponse = 3
	explainRetryAfter      = 24 * time.Hour
	explainRefreshAfter    = 7 * 24 * time.Hour
	// A failed EXPLAIN is often transient (database still starting, agent restarted without
	// samples): retry within the hour instead of waiting for the weekly refresh.
	explainErrorRetryAfter = time.Hour
	// Sources times hosts in one payload: a machine has a handful of each, and a broken agent
	// sending thousands must not write thousands of rows.
	maxSourceSites = 500
)

type tokenKey struct{}

// WithTokenID tells Write which agent token sent the payload, to link the host to it.
func WithTokenID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, tokenKey{}, id)
}

type Service struct {
	db  *store.DB
	Now func() time.Time
}

func New(db *store.DB) *Service {
	return &Service{db: db, Now: time.Now}
}

func hourOf(minute int64) int64 { return minute - minute%3600 }

// Ingest writes synchronously and returns the explain requests (tests, demo, CLI).
// The HTTP server uses the Queue instead.
func (s *Service) Ingest(ctx context.Context, p project.Project, payload protocol.Payload) (protocol.IngestResponse, error) {
	duplicate, err := s.Write(ctx, p, payload)
	if err != nil {
		return protocol.IngestResponse{}, err
	}
	resp := protocol.IngestResponse{OK: true}
	if !duplicate && hasDatabaseSource(payload.Agent) {
		resp.Explain, err = s.ExplainRequests(ctx, p, s.Now())
	}
	return resp, err
}

// Write stores a payload; it returns true when the batch was already processed.
func (s *Service) Write(ctx context.Context, p project.Project, payload protocol.Payload) (bool, error) {
	if payload.BatchID == "" || len(payload.BatchID) > 64 {
		return false, fmt.Errorf("batch_id is required (max 64 chars)")
	}
	now := s.Now()
	duplicate := false

	err := s.db.Tx(ctx, func(q store.Q) error {
		res, err := q.Exec(ctx, "INSERT INTO ingest_batches (id, project_id, created_at) VALUES (?, ?, ?) ON CONFLICT (id) DO NOTHING", payload.BatchID, p.ID, now.Unix())
		if err != nil {
			return err
		}
		if affected, _ := res.RowsAffected(); affected == 0 {
			duplicate = true
			return nil
		}

		w := writer{q: q, db: s.db, project: p, now: now}
		hostID, err := w.host(ctx, payload.Agent)
		if err != nil {
			return err
		}
		steps := []func() error{
			func() error { return w.http(ctx, payload.HTTP, now.Unix()) },
			func() error { return w.php(ctx, payload.PHP) },
			func() error { return w.database(ctx, hostID, payload.DB) },
			func() error { return w.dbStatus(ctx, hostID, payload.DBStatus) },
			func() error { return w.hostSamples(ctx, hostID, payload.Host) },
			func() error { return w.explains(ctx, payload.Explains) },
			func() error { return w.app(ctx, hostID, payload.App) },
			func() error { return w.jobs(ctx, payload.Jobs) },
			func() error { return w.commands(ctx, payload.Commands) },
			func() error { return w.metrics(ctx, hostID, payload.Metrics) },
			func() error { return w.network(ctx, hostID, payload.Network) },
			func() error { return w.checks(ctx, hostID, payload.Checks) },
		}
		for _, step := range steps {
			if err := step(); err != nil {
				return err
			}
		}
		return nil
	})
	return duplicate, err
}

func hasDatabaseSource(a protocol.AgentInfo) bool {
	for _, src := range a.Sources {
		if strings.HasPrefix(src, "mysql:") || strings.HasPrefix(src, "postgres:") {
			return true
		}
	}
	return false
}

type writer struct {
	q       store.Q
	db      *store.DB
	project project.Project
	now     time.Time
}

// valid ignores buckets from a broken clock: more than a day in the future or older than retention.
func (w writer) valid(minute int64) bool {
	return minute <= w.now.Add(24*time.Hour).Unix() && minute >= w.now.Add(-120*24*time.Hour).Unix()
}

func (w writer) host(ctx context.Context, a protocol.AgentInfo) (int64, error) {
	name := strings.TrimSpace(a.Host)
	if name == "" {
		name = "unknown"
	}
	kind := a.Kind
	if kind == "" {
		kind = "agent"
	}
	sources, _ := json.Marshal(nonNil(a.Sources))
	_, err := w.q.Exec(ctx, `INSERT INTO hosts (project_id, name, kind, version, os, sources, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (project_id, name, kind) DO UPDATE SET version = excluded.version, os = excluded.os, sources = excluded.sources, last_seen_at = excluded.last_seen_at`,
		w.project.ID, truncate(name, 191), kind, truncate(a.Version, 32), truncate(a.OS, 32), string(sources), w.now.Unix())
	if err != nil {
		return 0, err
	}
	var id int64
	err = w.q.Row(ctx, "SELECT id FROM hosts WHERE project_id = ? AND name = ? AND kind = ?", w.project.ID, truncate(name, 191), kind).Scan(&id)
	if tokenID, ok := ctx.Value(tokenKey{}).(int64); ok && err == nil {
		_, err = w.q.Exec(ctx, "UPDATE hosts SET token_id = ? WHERE id = ?", tokenID, id)
	}
	return id, err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// upsertMany writes rows with multi-row INSERT ... ON CONFLICT, in chunks.
// Rows must have unique conflict keys: callers aggregate duplicates first.
func (w writer) upsertMany(ctx context.Context, table string, cols []string, conflict string, updates string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	perRow := len(cols)
	chunk := min(500, 30000/perRow)
	placeholder := "(" + strings.TrimSuffix(strings.Repeat("?, ", perRow), ", ") + ")"
	for start := 0; start < len(rows); start += chunk {
		end := min(start+chunk, len(rows))
		var sb strings.Builder
		sb.WriteString("INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES ")
		args := make([]any, 0, (end-start)*perRow)
		for i := start; i < end; i++ {
			if i > start {
				sb.WriteString(", ")
			}
			sb.WriteString(placeholder)
			args = append(args, rows[i]...)
		}
		sb.WriteString(" ON CONFLICT (" + conflict + ") DO ")
		if updates == "" {
			sb.WriteString("NOTHING")
		} else {
			sb.WriteString("UPDATE SET " + updates)
		}
		if _, err := w.q.Exec(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
	}
	return nil
}

func histColumns(table string) (cols []string, sets []string) {
	for i := range 15 {
		cols = append(cols, fmt.Sprintf("h%d", i))
		sets = append(sets, fmt.Sprintf("h%d = %s.h%d + excluded.h%d", i, table, i, i))
	}
	return cols, sets
}

type httpKey struct {
	source string
	minute int64
}

type endpointKey struct {
	source, endpoint string
	hour             int64
}

func (w writer) http(ctx context.Context, buckets []protocol.HTTPBucket, now int64) error {
	minutes := map[httpKey]*protocol.HTTPBucket{}
	// Which host each source answers for: a pair is written once per batch, not once per minute.
	sites := map[[2]string]bool{}
	var minuteOrder []httpKey
	endpoints := map[endpointKey]*protocol.EndpointStat{}
	var endpointOrder []endpointKey

	for _, b := range buckets {
		if !w.valid(b.Minute) || b.N < 0 {
			continue
		}
		k := httpKey{truncate(b.Source, 64), b.Minute}
		for _, site := range b.Sites {
			if site = strings.ToLower(truncate(site, 191)); site != "" && len(sites) < maxSourceSites {
				sites[[2]string{k.source, site}] = true
			}
		}
		m := minutes[k]
		if m == nil {
			m = &protocol.HTTPBucket{Source: k.source, Minute: k.minute, Hist: make([]int, 15)}
			minutes[k] = m
			minuteOrder = append(minuteOrder, k)
		}
		m.N += b.N
		m.Sum += b.Sum
		m.Max = max(m.Max, b.Max)
		m.N1 += b.N1
		m.N2 += b.N2
		m.N5 += b.N5
		m.E4 += b.E4
		m.E5 += b.E5
		for i := 0; i < 15 && i < len(b.Hist); i++ {
			m.Hist[i] += b.Hist[i]
		}
		for endpoint, e := range b.Endpoints {
			ek := endpointKey{k.source, truncate(endpoint, 191), hourOf(b.Minute)}
			cur := endpoints[ek]
			if cur == nil {
				cur = &protocol.EndpointStat{}
				endpoints[ek] = cur
				endpointOrder = append(endpointOrder, ek)
			}
			cur.N += e.N
			cur.Sum += e.Sum
			cur.Max = max(cur.Max, e.Max)
			cur.Slow += e.Slow
			cur.SlowSum += e.SlowSum
			cur.E5 += e.E5
			if len(e.Hist) > 0 && cur.Hist == nil {
				cur.Hist = make([]int, 15)
			}
			for i := 0; i < 15 && i < len(e.Hist); i++ {
				cur.Hist[i] += e.Hist[i]
			}
		}
	}

	g := w.db.Greatest
	hcols, hsets := histColumns("http_minutes")
	rows := make([][]any, 0, len(minuteOrder))
	for _, k := range minuteOrder {
		m := minutes[k]
		row := []any{w.project.ID, k.source, k.minute, m.N, m.Sum, m.Max, m.N1, m.N2, m.N5, m.E4, m.E5}
		for _, v := range m.Hist {
			row = append(row, v)
		}
		rows = append(rows, row)
	}
	if len(sites) > 0 {
		siteRows := make([][]any, 0, len(sites))
		for pair := range sites {
			siteRows = append(siteRows, []any{w.project.ID, pair[0], pair[1], now})
		}
		sort.Slice(siteRows, func(i, j int) bool {
			a, b := siteRows[i], siteRows[j]
			return a[1].(string)+"\x00"+a[2].(string) < b[1].(string)+"\x00"+b[2].(string)
		})
		if err := w.upsertMany(ctx, "source_sites", []string{"project_id", "source", "site", "last_seen_at"},
			"project_id, source, site", "last_seen_at = excluded.last_seen_at", siteRows); err != nil {
			return err
		}
	}

	err := w.upsertMany(ctx, "http_minutes", append([]string{"project_id", "source", "bucket", "n", "sum", "max", "n1", "n2", "n5", "e4", "e5"}, hcols...),
		"project_id, source, bucket",
		`n = http_minutes.n + excluded.n, sum = http_minutes.sum + excluded.sum, max = `+g("http_minutes.max", "excluded.max")+`,
		n1 = http_minutes.n1 + excluded.n1, n2 = http_minutes.n2 + excluded.n2, n5 = http_minutes.n5 + excluded.n5,
		e4 = http_minutes.e4 + excluded.e4, e5 = http_minutes.e5 + excluded.e5, `+strings.Join(hsets, ", "), rows)
	if err != nil {
		return err
	}

	rows = rows[:0]
	for _, k := range endpointOrder {
		e := endpoints[k]
		row := []any{w.project.ID, k.source, k.endpoint, k.hour, e.N, e.Sum, e.Max, e.Slow, e.SlowSum, e.E5}
		for i := range 15 {
			v := 0
			if i < len(e.Hist) {
				v = e.Hist[i]
			}
			row = append(row, v)
		}
		rows = append(rows, row)
	}
	ecols, esets := histColumns("endpoint_hours")
	return w.upsertMany(ctx, "endpoint_hours", append([]string{"project_id", "source", "endpoint", "hour", "n", "sum", "max", "slow", "slow_sum", "e5"}, ecols...),
		"project_id, source, endpoint, hour",
		`n = endpoint_hours.n + excluded.n, sum = endpoint_hours.sum + excluded.sum, max = `+g("endpoint_hours.max", "excluded.max")+`,
		slow = endpoint_hours.slow + excluded.slow, slow_sum = endpoint_hours.slow_sum + excluded.slow_sum, e5 = endpoint_hours.e5 + excluded.e5, `+strings.Join(esets, ", "), rows)
}

func (w writer) php(ctx context.Context, buckets []protocol.PHPBucket) error {
	type key struct {
		source      string
		hour        int64
		frame, code string
	}
	agg := map[key]int{}
	var order []key
	for _, b := range buckets {
		if !w.valid(b.Minute) {
			continue
		}
		for _, s := range b.Stalls {
			k := key{truncate(b.Source, 64), hourOf(b.Minute), truncate(s.Frame, 191), truncate(s.Code, 191)}
			if _, ok := agg[k]; !ok {
				order = append(order, k)
			}
			agg[k] += s.N
		}
	}
	rows := make([][]any, 0, len(order))
	for _, k := range order {
		rows = append(rows, []any{w.project.ID, k.source, k.hour, k.frame, k.code, agg[k]})
	}
	return w.upsertMany(ctx, "php_stall_hours", []string{"project_id", "source", "hour", "frame", "code", "n"}, "project_id, source, hour, frame, code",
		"n = php_stall_hours.n + excluded.n", rows)
}

type fingerprintRow struct {
	hash, fp, sample string
	sampleSeconds    float64
	user, db         string
}

// fingerprintIDs upserts fingerprints in bulk and returns hash -> id. A sample
// replaces the stored one only when it was slower: that is the one worth an EXPLAIN.
func (w writer) fingerprintIDs(ctx context.Context, list map[string]*fingerprintRow) (map[string]int64, error) {
	ids := map[string]int64{}
	if len(list) == 0 {
		return ids, nil
	}
	hashes := make([]string, 0, len(list))
	rows := make([][]any, 0, len(list))
	for hash, f := range list {
		if len(hash) != 16 || f.fp == "" {
			return nil, fmt.Errorf("invalid query hash %q", hash)
		}
		var sample any
		if f.sample != "" {
			sample = truncate(f.sample, 8000)
		}
		hashes = append(hashes, hash)
		rows = append(rows, []any{w.project.ID, hash, truncate(f.fp, 8000), sample, f.sampleSeconds, truncate(f.user, 64), truncate(f.db, 64), w.now.Unix(), w.now.Unix()})
	}
	better := "excluded.sample IS NOT NULL AND (query_fingerprints.sample IS NULL OR excluded.sample_seconds >= query_fingerprints.sample_seconds)"
	err := w.upsertMany(ctx, "query_fingerprints", []string{"project_id", "hash", "fingerprint", "sample", "sample_seconds", "db_user", "db_name", "first_seen_at", "last_seen_at"},
		"project_id, hash",
		`last_seen_at = excluded.last_seen_at,
		sample = CASE WHEN `+better+` THEN excluded.sample ELSE query_fingerprints.sample END,
		sample_seconds = CASE WHEN `+better+` THEN excluded.sample_seconds ELSE query_fingerprints.sample_seconds END,
		db_user = CASE WHEN excluded.db_user <> '' THEN excluded.db_user ELSE query_fingerprints.db_user END,
		db_name = CASE WHEN excluded.db_name <> '' THEN excluded.db_name ELSE query_fingerprints.db_name END`, rows)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(hashes); start += 500 {
		end := min(start+500, len(hashes))
		args := []any{w.project.ID}
		for _, h := range hashes[start:end] {
			args = append(args, h)
		}
		res, err := w.q.Query(ctx, "SELECT hash, id FROM query_fingerprints WHERE project_id = ? AND hash IN ("+strings.TrimSuffix(strings.Repeat("?, ", end-start), ", ")+")", args...)
		if err != nil {
			return nil, err
		}
		for res.Next() {
			var h string
			var id int64
			if err := res.Scan(&h, &id); err != nil {
				res.Close()
				return nil, err
			}
			ids[h] = id
		}
		res.Close()
	}
	return ids, nil
}

func mergeFingerprint(list map[string]*fingerprintRow, hash, fp, sample string, seconds float64, user, db string) {
	f := list[hash]
	if f == nil {
		f = &fingerprintRow{hash: hash, fp: fp}
		list[hash] = f
	}
	if sample != "" && (f.sample == "" || seconds >= f.sampleSeconds) {
		f.sample, f.sampleSeconds = sample, seconds
	}
	if user != "" {
		f.user = unquote(user)
	}
	if db != "" {
		f.db = unquote(db)
	}
}

// unquote drops what MySQL puts around names in its slow log: `contract_service` is a database
// called contract_service, and that is what a person reads in the panel.
func unquote(v string) string {
	return strings.TrimSpace(strings.NewReplacer("`", "", `"`, "").Replace(v))
}

type queryHourKey struct {
	hash, source string
	hour         int64
}

type queryHour struct {
	n, rows  int64
	sum, max float64
}

func (w writer) database(ctx context.Context, hostID int64, buckets []protocol.DBBucket) error {
	type minuteAgg struct {
		n        int
		sum, max float64
	}
	minutes := map[int64]*minuteAgg{}
	var minuteOrder []int64
	fps := map[string]*fingerprintRow{}
	hours := map[queryHourKey]*queryHour{}
	var hourOrder []queryHourKey

	for _, b := range buckets {
		if !w.valid(b.Minute) {
			continue
		}
		if b.SlowN > 0 {
			m := minutes[b.Minute]
			if m == nil {
				m = &minuteAgg{}
				minutes[b.Minute] = m
				minuteOrder = append(minuteOrder, b.Minute)
			}
			m.n += b.SlowN
			m.sum += b.SlowSum
			m.max = max(m.max, b.SlowMax)
		}
		for hash, qs := range b.Queries {
			mergeFingerprint(fps, hash, qs.Fingerprint, qs.Sample, qs.Max, qs.User, qs.Database)
			k := queryHourKey{hash, truncate(b.Source, 32), hourOf(b.Minute)}
			h := hours[k]
			if h == nil {
				h = &queryHour{}
				hours[k] = h
				hourOrder = append(hourOrder, k)
			}
			h.n += qs.N
			h.sum += qs.Sum
			h.max = max(h.max, qs.Max)
			h.rows += qs.Rows
		}
	}

	g := w.db.Greatest
	rows := make([][]any, 0, len(minuteOrder))
	for _, m := range minuteOrder {
		a := minutes[m]
		rows = append(rows, []any{hostID, m, w.project.ID, a.n, a.sum, a.max})
	}
	if err := w.upsertMany(ctx, "db_minutes", []string{"host_id", "bucket", "project_id", "slow_n", "slow_sum", "slow_max"}, "host_id, bucket",
		"slow_n = db_minutes.slow_n + excluded.slow_n, slow_sum = db_minutes.slow_sum + excluded.slow_sum, slow_max = "+g("db_minutes.slow_max", "excluded.slow_max"), rows); err != nil {
		return err
	}

	ids, err := w.fingerprintIDs(ctx, fps)
	if err != nil {
		return err
	}
	rows = make([][]any, 0, len(hourOrder))
	for _, k := range hourOrder {
		h := hours[k]
		rows = append(rows, []any{ids[k.hash], k.source, hostID, k.hour, w.project.ID, h.n, h.sum, h.max, h.rows})
	}
	return w.upsertMany(ctx, "query_hours", []string{"fingerprint_id", "source", "host_id", "hour", "project_id", "n", "sum", "max", "rows"}, "fingerprint_id, source, host_id, hour",
		"n = query_hours.n + excluded.n, sum = query_hours.sum + excluded.sum, max = "+g("query_hours.max", "excluded.max")+", rows = query_hours.rows + excluded.rows", rows)
}

func (w writer) dbStatus(ctx context.Context, hostID int64, samples []protocol.DBStatus) error {
	latest := map[int64]protocol.DBStatus{}
	var order []int64
	for _, s := range samples {
		if !w.valid(s.Minute) {
			continue
		}
		if _, ok := latest[s.Minute]; !ok {
			order = append(order, s.Minute)
		}
		latest[s.Minute] = s
	}
	rows := make([][]any, 0, len(order))
	for _, m := range order {
		s := latest[m]
		rows = append(rows, []any{hostID, m, w.project.ID, s.QPS, s.RowsReadPS, s.ThreadsRunning, s.ThreadsConnected})
	}
	return w.upsertMany(ctx, "db_minutes", []string{"host_id", "bucket", "project_id", "qps", "rows_read_ps", "threads_running", "threads_connected"}, "host_id, bucket",
		"qps = excluded.qps, rows_read_ps = excluded.rows_read_ps, threads_running = excluded.threads_running, threads_connected = excluded.threads_connected", rows)
}

func (w writer) hostSamples(ctx context.Context, hostID int64, samples []protocol.HostSample) error {
	latest := map[int64]protocol.HostSample{}
	var order []int64
	for _, s := range samples {
		if !w.valid(s.Minute) {
			continue
		}
		if _, ok := latest[s.Minute]; !ok {
			order = append(order, s.Minute)
		}
		latest[s.Minute] = s
	}
	cols := []string{"host_id", "bucket", "cpu", "cpu_iowait", "cpu_steal", "load1", "load5", "load15", "cpus", "mem_used", "mem_total", "swap_used", "swap_total",
		"swap_out_pages", "oom_kills", "disk_used", "disk_total", "disk_read_bps", "disk_write_bps", "net_rx_bps", "net_tx_bps", "top_processes"}
	rows := make([][]any, 0, len(order))
	for _, m := range order {
		s := latest[m]
		procs := s.TopProcesses
		if len(procs) > 10 {
			procs = procs[:10]
		}
		for i := range procs {
			procs[i].Name = truncate(procs[i].Name, 64)
		}
		top, _ := json.Marshal(procs)
		rows = append(rows, []any{hostID, m, s.CPU, s.CPUIOWait, s.CPUSteal, s.Load1, s.Load5, s.Load15, s.CPUs, int64(s.MemUsed), int64(s.MemTotal),
			int64(s.SwapUsed), int64(s.SwapTotal), int64(s.SwapOutPages), int64(s.OOMKills), int64(s.DiskUsed), int64(s.DiskTotal),
			s.DiskReadBps, s.DiskWriteBps, s.NetRxBps, s.NetTxBps, string(top)})
	}
	var sets []string
	for _, c := range cols[2:] {
		sets = append(sets, c+" = excluded."+c)
	}
	return w.upsertMany(ctx, "host_minutes", cols, "host_id, bucket", strings.Join(sets, ", "), rows)
}

func (w writer) explains(ctx context.Context, explains []protocol.Explain) error {
	for _, e := range explains {
		payload, _ := json.Marshal(map[string]any{"engine": e.Engine, "generic": e.Generic, "plan": e.Plan, "tables": e.Tables})
		var plan, errText any
		if e.Error != "" {
			errText = truncate(e.Error, 1000)
		} else {
			plan = string(payload)
		}
		_, err := w.q.Exec(ctx, `UPDATE query_fingerprints SET explain = COALESCE(?, explain), explain_error = ?, explained_at = ? WHERE project_id = ? AND hash = ?`,
			plan, errText, w.now.Unix(), w.project.ID, e.Hash)
		if err != nil {
			return err
		}
	}
	return nil
}

func (w writer) app(ctx context.Context, hostID int64, buckets []protocol.AppBucket) error {
	type routeKey struct {
		route string
		hour  int64
	}
	type routeQueryKey struct{ route, hash string }
	type routeCallKey struct {
		route, host string
		hour        int64
	}
	calls := map[routeCallKey]*protocol.ExtCall{}
	var callOrder []routeCallKey
	fps := map[string]*fingerprintRow{}
	origins := map[string]string{}
	nPlusOne := map[string]int{}
	hours := map[queryHourKey]*queryHour{}
	var hourOrder []queryHourKey
	routes := map[routeKey]*protocol.RouteStat{}
	var routeOrder []routeKey
	routeQueries := map[routeQueryKey]*protocol.RouteQuery{}
	var rqOrder []routeQueryKey

	for _, b := range buckets {
		if !w.valid(b.Minute) {
			continue
		}
		for hash, aq := range b.Queries {
			mergeFingerprint(fps, hash, aq.Fingerprint, aq.Sample, 0, "", "")
			if aq.Origin != "" {
				origins[hash] = truncate(aq.Origin, 300)
			}
			nPlusOne[hash] += aq.NPlusOne
			k := queryHourKey{hash, "app", hourOf(b.Minute)}
			h := hours[k]
			if h == nil {
				h = &queryHour{}
				hours[k] = h
				hourOrder = append(hourOrder, k)
			}
			h.n += int64(aq.N)
			h.sum += aq.Sum
		}
		for route, r := range b.Routes {
			rk := routeKey{truncate(route, 191), hourOf(b.Minute)}
			cur := routes[rk]
			if cur == nil {
				cur = &protocol.RouteStat{}
				routes[rk] = cur
				routeOrder = append(routeOrder, rk)
			}
			cur.N += r.N
			cur.Sum += r.Sum
			cur.Max = max(cur.Max, r.Max)
			cur.Queries += r.Queries
			cur.DBSum += r.DBSum
			cur.E5 += r.E5
			cur.ExtSum += r.ExtSum
			cur.ExtRequests += r.ExtRequests
			for host, e := range r.Ext {
				ck := routeCallKey{rk.route, truncate(host, 150), rk.hour}
				c := calls[ck]
				if c == nil {
					c = &protocol.ExtCall{}
					calls[ck] = c
					callOrder = append(callOrder, ck)
				}
				c.N += e.N
				c.Sum += e.Sum
				c.Max = max(c.Max, e.Max)
				c.Errors += e.Errors
				c.Requests += e.Requests
				if e.Origin != "" {
					c.Origin = truncate(e.Origin, 300)
				}
			}
			for hash, rq := range r.Hashes {
				if _, known := b.Queries[hash]; !known {
					continue
				}
				qk := routeQueryKey{rk.route, hash}
				c := routeQueries[qk]
				if c == nil {
					c = &protocol.RouteQuery{}
					routeQueries[qk] = c
					rqOrder = append(rqOrder, qk)
				}
				c.N += rq.N
				c.Sum += rq.Sum
				c.Requests += rq.Requests
			}
		}
	}

	ids, err := w.fingerprintIDs(ctx, fps)
	if err != nil {
		return err
	}
	for hash, id := range ids {
		if origins[hash] == "" && nPlusOne[hash] == 0 {
			continue
		}
		if _, err := w.q.Exec(ctx, `UPDATE query_fingerprints SET origin = CASE WHEN ? <> '' THEN ? ELSE origin END, n_plus_one = n_plus_one + ? WHERE id = ?`,
			origins[hash], origins[hash], nPlusOne[hash], id); err != nil {
			return err
		}
	}

	g := w.db.Greatest
	rows := make([][]any, 0, len(hourOrder))
	for _, k := range hourOrder {
		h := hours[k]
		rows = append(rows, []any{ids[k.hash], "app", hostID, k.hour, w.project.ID, h.n, h.sum, 0, 0})
	}
	if err := w.upsertMany(ctx, "query_hours", []string{"fingerprint_id", "source", "host_id", "hour", "project_id", "n", "sum", "max", "rows"}, "fingerprint_id, source, host_id, hour",
		"n = query_hours.n + excluded.n, sum = query_hours.sum + excluded.sum", rows); err != nil {
		return err
	}

	rows = make([][]any, 0, len(routeOrder))
	for _, k := range routeOrder {
		r := routes[k]
		rows = append(rows, []any{w.project.ID, k.route, k.hour, r.N, r.Sum, r.Max, r.Queries, r.DBSum, r.E5, r.ExtSum, r.ExtRequests})
	}
	if err := w.upsertMany(ctx, "route_hours", []string{"project_id", "route", "hour", "n", "sum", "max", "queries", "db_sum", "e5", "ext_sum", "ext_requests"}, "project_id, route, hour",
		`n = route_hours.n + excluded.n, sum = route_hours.sum + excluded.sum, max = `+g("route_hours.max", "excluded.max")+`,
		queries = route_hours.queries + excluded.queries, db_sum = route_hours.db_sum + excluded.db_sum, e5 = route_hours.e5 + excluded.e5,
		ext_sum = route_hours.ext_sum + excluded.ext_sum, ext_requests = route_hours.ext_requests + excluded.ext_requests`, rows); err != nil {
		return err
	}

	rows = make([][]any, 0, len(callOrder))
	for _, k := range callOrder {
		c := calls[k]
		rows = append(rows, []any{w.project.ID, k.route, k.host, k.hour, c.N, c.Sum, c.Max, c.Errors, c.Requests, c.Origin})
	}
	if err := w.upsertMany(ctx, "route_calls", []string{"project_id", "route", "host", "hour", "n", "sum", "max", "errors", "requests", "origin"}, "project_id, route, host, hour",
		`n = route_calls.n + excluded.n, sum = route_calls.sum + excluded.sum, max = `+g("route_calls.max", "excluded.max")+`,
		errors = route_calls.errors + excluded.errors, requests = route_calls.requests + excluded.requests,
		origin = CASE WHEN excluded.origin <> '' THEN excluded.origin ELSE route_calls.origin END`, rows); err != nil {
		return err
	}

	rows = make([][]any, 0, len(rqOrder))
	for _, k := range rqOrder {
		c := routeQueries[k]
		rows = append(rows, []any{w.project.ID, k.route, ids[k.hash], c.N, c.Sum, c.Requests, w.now.Unix()})
	}
	return w.upsertMany(ctx, "route_queries", []string{"project_id", "route", "fingerprint_id", "n", "sum", "requests", "last_seen_at"}, "project_id, route, fingerprint_id",
		"n = route_queries.n + excluded.n, sum = route_queries.sum + excluded.sum, requests = route_queries.requests + excluded.requests, last_seen_at = excluded.last_seen_at", rows)
}

func (w writer) jobs(ctx context.Context, buckets []protocol.JobBucket) error {
	sql := `INSERT INTO job_hours (project_id, kind, name, hour, n, failed, sum, max) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (project_id, kind, name, hour) DO UPDATE SET n = job_hours.n + excluded.n, failed = job_hours.failed + excluded.failed,
		sum = job_hours.sum + excluded.sum, max = ` + w.db.Greatest("job_hours.max", "excluded.max")
	for _, b := range buckets {
		if !w.valid(b.Minute) || (b.Kind != "job" && b.Kind != "command") {
			continue
		}
		if _, err := w.q.Exec(ctx, sql, w.project.ID, b.Kind, truncate(b.Name, 191), hourOf(b.Minute), b.N, b.Failed, b.Sum, b.Max); err != nil {
			return err
		}
	}
	return nil
}

func (w writer) commands(ctx context.Context, runs []protocol.CommandRun) error {
	for _, r := range runs {
		if !w.valid(r.StartedAt) || r.Name == "" {
			continue
		}
		var finished, exit any
		if r.FinishedAt > 0 {
			finished = r.FinishedAt
		}
		if r.ExitCode != nil {
			exit = *r.ExitCode
		}
		_, err := w.q.Exec(ctx, `INSERT INTO command_runs (project_id, host, name, started_at, finished_at, exit_code) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (project_id, host, name, started_at) DO UPDATE SET finished_at = COALESCE(excluded.finished_at, command_runs.finished_at),
			exit_code = COALESCE(excluded.exit_code, command_runs.exit_code)`,
			w.project.ID, truncate(r.Host, 191), truncate(r.Name, 191), r.StartedAt, finished, exit)
		if err != nil {
			return err
		}
	}
	return nil
}

// metrics stores one series per machine: the same name from two hosts is two series, never a sum.
func (w writer) metrics(ctx context.Context, hostID int64, buckets []protocol.MetricBucket) error {
	type key struct {
		name   string
		minute int64
	}
	agg := map[key]*protocol.MetricBucket{}
	var order []key
	for _, b := range buckets {
		name := truncate(strings.TrimSpace(b.Name), 191)
		if !w.valid(b.Minute) || name == "" || (b.Kind != "counter" && b.Kind != "gauge" && b.Kind != "timer") {
			continue
		}
		k := key{name, b.Minute}
		cur := agg[k]
		if cur == nil {
			cur = &protocol.MetricBucket{Name: name, Minute: b.Minute, Kind: b.Kind, Min: b.Min, Max: b.Max, Hist: make([]int, 15)}
			agg[k] = cur
			order = append(order, k)
		}
		if cur.Kind != b.Kind {
			continue
		}
		cur.Count += b.Count
		cur.Sum += b.Sum
		cur.Min = min(cur.Min, b.Min)
		cur.Max = max(cur.Max, b.Max)
		cur.Last = b.Last
		if b.Kind == "timer" {
			for i := 0; i < 15 && i < len(b.Hist); i++ {
				cur.Hist[i] += b.Hist[i]
			}
		}
	}
	least := "MIN(metric_minutes.min, excluded.min)"
	if w.db.Dialect == store.Postgres {
		least = "LEAST(metric_minutes.min, excluded.min)"
	}
	hcols, hsets := histColumns("metric_minutes")
	rows := make([][]any, 0, len(order))
	for _, k := range order {
		b := agg[k]
		row := []any{w.project.ID, hostID, k.name, k.minute, b.Kind, b.Count, b.Sum, b.Min, b.Max, b.Last}
		for _, v := range b.Hist {
			row = append(row, v)
		}
		rows = append(rows, row)
	}
	return w.upsertMany(ctx, "metric_minutes", append([]string{"project_id", "host_id", "name", "bucket", "kind", "count", "sum", "min", "max", "last"}, hcols...), "project_id, host_id, name, bucket",
		`count = metric_minutes.count + excluded.count, sum = metric_minutes.sum + excluded.sum, min = `+least+`,
		max = `+w.db.Greatest("metric_minutes.max", "excluded.max")+`, last = excluded.last, `+strings.Join(hsets, ", "), rows)
}

// network replaces the connection snapshot of a host.
func (w writer) network(ctx context.Context, hostID int64, n *protocol.NetworkSnapshot) error {
	if n == nil {
		return nil
	}
	clip := func(list []string, max int) []string {
		if len(list) > max {
			return list[:max]
		}
		return list
	}
	addresses, _ := json.Marshal(clip(n.Addresses, 32))
	listening := n.Listening
	if len(listening) > 64 {
		listening = listening[:64]
	}
	listenJSON, _ := json.Marshal(listening)
	if _, err := w.q.Exec(ctx, "UPDATE hosts SET addresses = ?, listening = ?, network_at = ? WHERE id = ?", string(addresses), string(listenJSON), w.now.Unix(), hostID); err != nil {
		return err
	}
	if _, err := w.q.Exec(ctx, "DELETE FROM host_connections WHERE host_id = ?", hostID); err != nil {
		return err
	}
	type key struct {
		dir, ip, process string
		port             int
	}
	merged := map[key]*protocol.Connection{}
	var order []key
	for _, c := range n.Connections {
		if (c.Direction != "in" && c.Direction != "out") || net.ParseIP(c.RemoteIP) == nil {
			continue
		}
		port := c.RemotePort
		if c.Direction == "in" {
			port = c.LocalPort
		}
		k := key{c.Direction, c.RemoteIP, truncate(c.Process, 64), port}
		if merged[k] == nil {
			cc := c
			merged[k] = &cc
			order = append(order, k)
			continue
		}
		merged[k].Count += c.Count
	}
	if len(order) > 200 {
		order = order[:200]
	}
	rows := make([][]any, 0, len(order))
	for _, k := range order {
		c := merged[k]
		rows = append(rows, []any{hostID, k.dir, k.ip, k.port, k.process, truncate(c.Service, 32), c.Count})
	}
	return w.upsertMany(ctx, "host_connections", []string{"host_id", "direction", "remote_ip", "port", "process", "service", "count"},
		"host_id, direction, remote_ip, port, process", "count = excluded.count, service = excluded.service", rows)
}

// explainRequests picks the costliest explainable queries of the last day that
// have no fresh plan and were not asked recently. The threshold is per observed day,
// so a project installed minutes ago gets its first plans right away.
func (s *Service) ExplainRequests(ctx context.Context, p project.Project, now time.Time) ([]protocol.ExplainRequest, error) {
	days := project.Repo{DB: s.db}.ObservedDays(ctx, p.ID, 24*time.Hour, now)
	minCost := p.Settings.DB.ExplainMinSecondsPerDay * days
	rows, err := s.db.Q().Query(ctx, `SELECT f.id, f.hash, f.sample, f.db_name, SUM(h.sum) AS cost
		FROM query_fingerprints f JOIN query_hours h ON h.fingerprint_id = f.id
		WHERE f.project_id = ? AND h.hour >= ? AND h.source <> 'app' AND f.sample IS NOT NULL
		  AND ((f.explain_error IS NULL AND (f.explain_requested_at IS NULL OR f.explain_requested_at < ?)
		        AND (f.explained_at IS NULL OR f.explained_at < ?))
		    OR (f.explain_error IS NOT NULL AND f.explain_requested_at < ?))
		GROUP BY f.id, f.hash, f.sample, f.db_name
		HAVING SUM(h.sum) >= ?
		ORDER BY cost DESC LIMIT 20`,
		p.ID, now.Add(-24*time.Hour).Unix()-3600, now.Add(-explainRetryAfter).Unix(), now.Add(-explainRefreshAfter).Unix(), now.Add(-explainErrorRetryAfter).Unix(), minCost)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		id                 int64
		hash, sample, name string
	}
	var picked []candidate
	for rows.Next() {
		var c candidate
		var cost float64
		if err := rows.Scan(&c.id, &c.hash, &c.sample, &c.name, &cost); err != nil {
			rows.Close()
			return nil, err
		}
		// The agent decides for real, on its own sample; statements with $n placeholders
		// are planned without values on PostgreSQL 16+.
		if sqlsafe.ExplainableWithoutValues(c.sample) && len(picked) < maxExplainsPerResponse {
			picked = append(picked, c)
		}
	}
	rows.Close()

	var out []protocol.ExplainRequest
	for _, c := range picked {
		if _, err := s.db.Q().Exec(ctx, "UPDATE query_fingerprints SET explain_requested_at = ? WHERE id = ?", now.Unix(), c.id); err != nil {
			return nil, err
		}
		out = append(out, protocol.ExplainRequest{Hash: c.hash, SQL: c.sample, Database: c.name})
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
