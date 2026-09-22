// Package metrics reads the rollup tables back as totals over a time window.
package metrics

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/exclude"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

type HTTPTotals struct {
	N, N1, N2, N5, E4, E5 int64
	Sum, Max              float64
	Hist                  [15]int64
}

func (h *HTTPTotals) add(o HTTPTotals) {
	h.N += o.N
	h.N1 += o.N1
	h.N2 += o.N2
	h.N5 += o.N5
	h.E4 += o.E4
	h.E5 += o.E5
	h.Sum += o.Sum
	h.Max = max(h.Max, o.Max)
	for i := range h.Hist {
		h.Hist[i] += o.Hist[i]
	}
}

// Percentile estimates a percentile from the histogram, interpolating linearly inside the slot.
func (h HTTPTotals) Percentile(q float64) float64 {
	var total int64
	for _, c := range h.Hist {
		total += c
	}
	if total == 0 {
		return 0
	}
	target := q * float64(total)
	var seen float64
	for i, c := range h.Hist {
		if c == 0 {
			continue
		}
		if seen+float64(c) >= target {
			lower := 0.0
			if i > 0 {
				lower = protocol.HistogramEdges[i-1]
			}
			upper := h.Max
			if i < len(protocol.HistogramEdges) {
				upper = protocol.HistogramEdges[i]
			}
			if upper < lower {
				upper = lower
			}
			return lower + (upper-lower)*(target-seen)/float64(c)
		}
		seen += float64(c)
	}
	return h.Max
}

func (h HTTPTotals) Avg() float64 {
	if h.N == 0 {
		return 0
	}
	return h.Sum / float64(h.N)
}

type DBTotals struct {
	SlowN            int64
	SlowSum, SlowMax float64
}

// CustomTotals sums a custom metric over a window. Counters and timers of several machines are
// one total; a gauge is not, so Last is only filled when a single machine reports the metric.
type CustomTotals struct {
	Kind          string
	Count         int64
	Sum, Min, Max float64
	Last          float64
	Hosts         int64
	Hist          [15]int64
}

// HostMetric is what one machine reported of a metric: the only honest reading of a gauge
// when the same name arrives from several machines (four Redis servers all called "cache").
type HostMetric struct {
	HostID int64
	Host   string
	CustomTotals
	Series []MetricPoint
}

// MetricPoint is one step of a metric's chart.
type MetricPoint struct {
	T     int64
	Count int64
	Sum   float64
	Max   float64
}

func (c CustomTotals) Percentile(q float64) float64 {
	return HTTPTotals{N: c.Count, Max: c.Max, Hist: c.Hist}.Percentile(q)
}

type Snapshot struct {
	From, To  int64
	HTTP      map[string]HTTPTotals
	Custom    map[string]CustomTotals
	PHPStalls int64
	DB        DBTotals
	Hours     float64
	Has       struct{ HTTP, PHP, DB bool }
}

// HTTPFor sums the sources starting with prefix ("" = all).
func (s Snapshot) HTTPFor(prefix string) HTTPTotals {
	var out HTTPTotals
	for source, t := range s.HTTP {
		if strings.HasPrefix(source, prefix) {
			out.add(t)
		}
	}
	return out
}

type Reader struct{ DB *store.DB }

func hourFloor(t time.Time) int64 { return t.Unix() - t.Unix()%3600 }

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func (r Reader) Snapshot(ctx context.Context, projectID int64, from, to time.Time) (Snapshot, error) {
	q := r.DB.Q()
	snap := Snapshot{From: from.Unix(), To: to.Unix(), HTTP: map[string]HTTPTotals{}, Custom: map[string]CustomTotals{}}

	hcols := make([]string, 15)
	for i := range hcols {
		hcols[i] = fmt.Sprintf("SUM(h%d)", i)
	}
	shadowed, err := r.Shadowed(ctx, projectID, from)
	if err != nil {
		return snap, err
	}
	rows, err := q.Query(ctx, `SELECT source, SUM(n), SUM(n1), SUM(n2), SUM(n5), SUM(e4), SUM(e5), SUM(sum), MAX(max), `+strings.Join(hcols, ", ")+`
		FROM http_minutes WHERE project_id = ? AND bucket >= ? AND bucket < ? GROUP BY source`, projectID, from.Unix(), to.Unix())
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var source string
		var t HTTPTotals
		dest := []any{&source, &t.N, &t.N1, &t.N2, &t.N5, &t.E4, &t.E5, &t.Sum, &t.Max}
		for i := range t.Hist {
			dest = append(dest, &t.Hist[i])
		}
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return snap, err
		}
		if shadowed[source] {
			continue // the web server in front of it reported the same requests
		}
		snap.HTTP[source] = t
	}
	rows.Close()
	if err := r.subtractExcluded(ctx, projectID, from, to, shadowed, &snap); err != nil {
		return snap, err
	}

	var minutes int64
	if err := q.Row(ctx, `SELECT COUNT(DISTINCT bucket) FROM http_minutes WHERE project_id = ? AND bucket >= ? AND bucket < ?`, projectID, from.Unix(), to.Unix()).Scan(&minutes); err != nil {
		return snap, err
	}
	if minutes == 0 {
		_ = q.Row(ctx, `SELECT COUNT(DISTINCT bucket) FROM db_minutes WHERE project_id = ? AND bucket >= ? AND bucket < ?`, projectID, from.Unix(), to.Unix()).Scan(&minutes)
	}
	snap.Hours = float64(minutes) / 60

	if err := q.Row(ctx, `SELECT COALESCE(SUM(slow_n),0), COALESCE(SUM(slow_sum),0), COALESCE(MAX(slow_max),0) FROM db_minutes WHERE project_id = ? AND bucket >= ? AND bucket < ?`,
		projectID, from.Unix(), to.Unix()).Scan(&snap.DB.SlowN, &snap.DB.SlowSum, &snap.DB.SlowMax); err != nil {
		return snap, err
	}
	if err := q.Row(ctx, `SELECT COALESCE(SUM(n),0) FROM php_stall_hours WHERE project_id = ? AND hour >= ? AND hour < ?`,
		projectID, hourFloor(from), to.Unix()).Scan(&snap.PHPStalls); err != nil {
		return snap, err
	}
	// The queries and the PHP frames the team left out, once their totals are in.
	set, err := exclude.Active(ctx, r.DB, projectID, to)
	if err != nil {
		return snap, err
	}
	if err := r.subtractExcludedQueries(ctx, projectID, from, to, set, &snap); err != nil {
		return snap, err
	}
	if err := r.subtractExcludedStalls(ctx, projectID, from, to, set, &snap); err != nil {
		return snap, err
	}

	crows, err := q.Query(ctx, `SELECT name, MAX(kind), SUM(count), SUM(sum), MIN(min), MAX(max), COUNT(DISTINCT host_id), `+strings.Join(hcols, ", ")+`
		FROM metric_minutes WHERE project_id = ? AND bucket >= ? AND bucket < ? GROUP BY name`, projectID, from.Unix(), to.Unix())
	if err != nil {
		return snap, err
	}
	for crows.Next() {
		var name string
		var c CustomTotals
		dest := []any{&name, &c.Kind, &c.Count, &c.Sum, &c.Min, &c.Max, &c.Hosts}
		for i := range c.Hist {
			dest = append(dest, &c.Hist[i])
		}
		if err := crows.Scan(dest...); err != nil {
			crows.Close()
			return snap, err
		}
		snap.Custom[name] = c
	}
	crows.Close()
	for name, c := range snap.Custom {
		if c.Kind == "gauge" && c.Hosts == 1 {
			_ = q.Row(ctx, `SELECT last FROM metric_minutes WHERE project_id = ? AND name = ? AND bucket >= ? AND bucket < ? ORDER BY bucket DESC LIMIT 1`, projectID, name, from.Unix(), to.Unix()).Scan(&c.Last)
			snap.Custom[name] = c
		}
	}

	var sources []string
	hostRows, err := q.Query(ctx, `SELECT sources FROM hosts WHERE project_id = ?`, projectID)
	if err != nil {
		return snap, err
	}
	for hostRows.Next() {
		var s string
		_ = hostRows.Scan(&s)
		sources = append(sources, s)
	}
	hostRows.Close()
	joined := strings.Join(sources, " ")
	snap.Has.HTTP = len(snap.HTTP) > 0
	snap.Has.PHP = strings.Contains(joined, `"php-fpm:`)
	snap.Has.DB = strings.Contains(joined, `"mysql`) || strings.Contains(joined, `"postgres:`)
	return snap, nil
}

// CustomByHost reads a metric one machine at a time, newest value included, with the chart of
// each machine when step is not zero. A host_id of 0 is a row written before machines were kept
// apart: it has no name and disappears with retention.
func (r Reader) CustomByHost(ctx context.Context, projectID int64, name string, from, to time.Time, step int64) ([]HostMetric, error) {
	q := r.DB.Q()
	hcols := make([]string, 15)
	for i := range hcols {
		hcols[i] = fmt.Sprintf("SUM(m.h%d)", i)
	}
	rows, err := q.Query(ctx, `SELECT m.host_id, COALESCE(h.name, ''), MAX(m.kind), SUM(m.count), SUM(m.sum), MIN(m.min), MAX(m.max), `+strings.Join(hcols, ", ")+`
		FROM metric_minutes m LEFT JOIN hosts h ON h.id = m.host_id
		WHERE m.project_id = ? AND m.name = ? AND m.bucket >= ? AND m.bucket < ?
		GROUP BY m.host_id, h.name`, projectID, name, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	var out []HostMetric
	byID := map[int64]int{}
	for rows.Next() {
		var hm HostMetric
		hm.Hosts = 1
		dest := []any{&hm.HostID, &hm.Host, &hm.Kind, &hm.Count, &hm.Sum, &hm.Min, &hm.Max}
		for i := range hm.Hist {
			dest = append(dest, &hm.Hist[i])
		}
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return nil, err
		}
		byID[hm.HostID] = len(out)
		out = append(out, hm)
	}
	rows.Close()
	if len(out) == 0 {
		return nil, nil
	}

	// The newest value of each machine: the row of its last minute in the window.
	rows, err = q.Query(ctx, `SELECT m.host_id, m.last FROM metric_minutes m
		JOIN (SELECT host_id, MAX(bucket) AS last_bucket FROM metric_minutes
			WHERE project_id = ? AND name = ? AND bucket >= ? AND bucket < ? GROUP BY host_id) t
		ON t.host_id = m.host_id AND t.last_bucket = m.bucket
		WHERE m.project_id = ? AND m.name = ? AND m.bucket >= ? AND m.bucket < ?`,
		projectID, name, from.Unix(), to.Unix(), projectID, name, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var hostID int64
		var last float64
		if err := rows.Scan(&hostID, &last); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := byID[hostID]; ok {
			out[i].Last = last
		}
	}
	rows.Close()

	if step > 0 {
		bucketExpr := fmt.Sprintf("m.bucket - (m.bucket %% %d)", step)
		rows, err = q.Query(ctx, `SELECT m.host_id, `+bucketExpr+`, SUM(m.count), SUM(m.sum), MAX(m.max) FROM metric_minutes m
			WHERE m.project_id = ? AND m.name = ? AND m.bucket >= ? AND m.bucket < ?
			GROUP BY m.host_id, `+bucketExpr+` ORDER BY 2`, projectID, name, from.Unix(), to.Unix())
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var hostID int64
			var p MetricPoint
			if err := rows.Scan(&hostID, &p.T, &p.Count, &p.Sum, &p.Max); err != nil {
				rows.Close()
				return nil, err
			}
			if i, ok := byID[hostID]; ok {
				out[i].Series = append(out[i].Series, p)
			}
		}
		rows.Close()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, nil
}

// Change is what one thing costs now against what it cost in the window right before.
// SavedPerDay is positive when it got better: those are the wins the team can claim.
type Change struct {
	Kind         string  `json:"kind"`
	Target       string  `json:"target"`
	Label        string  `json:"label"`
	NowPerDay    float64 `json:"now_seconds_per_day"`
	BeforePerDay float64 `json:"before_seconds_per_day"`
	SavedPerDay  float64 `json:"saved_seconds_per_day"`
	New          bool    `json:"new,omitempty"`  // it was not there before
	Gone         bool    `json:"gone,omitempty"` // it is not there any more
}

// Changes compares a window with the one right before it, per day on both sides so that two
// windows observed for different lengths of time can still be compared. It answers false when
// the project was not being measured long enough before: a project installed yesterday has
// nothing to compare with, and inventing a comparison would invent wins.
func (r Reader) Changes(ctx context.Context, projectID int64, from, to time.Time) ([]Change, bool, error) {
	span := to.Sub(from)
	if span <= 0 {
		return nil, false, nil
	}
	first, err := r.FirstData(ctx, projectID)
	if err != nil {
		return nil, false, err
	}
	observed := func(a, b time.Time) float64 {
		if !first.IsZero() && first.After(a) {
			a = first
		}
		if d := b.Sub(a); d > 0 {
			return d.Hours() / 24
		}
		return 0
	}
	// Both sides are counted per day, so a shorter window before is still a fair comparison;
	// less than half a window is too little to say anything.
	nowDays, beforeDays := observed(from, to), observed(from.Add(-span), from)
	if nowDays <= 0 || beforeDays < 0.5*nowDays {
		return nil, false, nil
	}

	now, err := r.Costs(ctx, projectID, from, to)
	if err != nil {
		return nil, false, err
	}
	before, err := r.Costs(ctx, projectID, from.Add(-span), from)
	if err != nil {
		return nil, false, err
	}
	key := func(c Cost) string { return c.Kind + "|" + c.Target }
	changes := map[string]*Change{}
	for _, c := range before {
		changes[key(c)] = &Change{Kind: c.Kind, Target: c.Target, Label: c.Label, BeforePerDay: round1(c.Seconds / beforeDays), Gone: true}
	}
	for _, c := range now {
		ch := changes[key(c)]
		if ch == nil {
			ch = &Change{Kind: c.Kind, Target: c.Target, Label: c.Label, New: true}
			changes[key(c)] = ch
		}
		ch.Gone = false
		ch.Label = c.Label
		ch.NowPerDay = round1(c.Seconds / nowDays)
	}
	out := make([]Change, 0, len(changes))
	for _, ch := range changes {
		ch.SavedPerDay = round1(ch.BeforePerDay - ch.NowPerDay)
		out = append(out, *ch)
	}
	// Best first: the wins are what the team is here for.
	sort.Slice(out, func(i, j int) bool { return out[i].SavedPerDay > out[j].SavedPerDay })
	return out, true, nil
}

// Cost is the time lost by one target (query, endpoint, PHP frame) in a window.
type Cost struct {
	Kind    string  `json:"kind"` // query | endpoint | php
	Target  string  `json:"target"`
	Label   string  `json:"label"`
	Seconds float64 `json:"seconds"`
	Count   int64   `json:"count"`
	Max     float64 `json:"max"`
}

// PHPStallSeconds is the minimum time a slowlog entry represents: the default request_slowlog_timeout.
const PHPStallSeconds = 1.0

// Source precedence for queries: a complete source beats the slow log, which only sees slow statements.
var querySourceRank = map[string]int{"mysql-digest": 3, "pg-statements": 3, "app": 2, "mysql-slowlog": 1}

// Shadowed lists the sources whose requests a web server has already counted. An application
// that answers for a host a web server also answers for is behind that web server: the same
// requests, seen twice. On one machine the agent recognises them by pairing single requests and
// never sends the duplicates; when the proxy and the application run on different machines
// nobody can pair anything, and the host each source declares is what is left to go by.
//
// The web server's line wins: it is the whole wait, including queueing for a free worker. What
// only the application knows - the route templates, the queries of each request, the file and
// line - lives in other tables and is untouched by this.
func (r Reader) Shadowed(ctx context.Context, projectID int64, since time.Time) (map[string]bool, error) {
	// Only what a source answered for in the window being read: an application that stopped
	// serving a host, or a machine somebody once called by hand, must not keep the pairing alive.
	rows, err := r.DB.Q().Query(ctx, `SELECT source, site FROM source_sites WHERE project_id = ? AND last_seen_at >= ?`,
		projectID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	served := map[string]bool{}        // hosts a web server answers for
	claimed := map[string][]string{}   // application source -> the hosts it answered for
	for rows.Next() {
		var source, site string
		if err := rows.Scan(&source, &site); err != nil {
			return nil, err
		}
		switch {
		case strings.HasPrefix(source, "nginx:"), strings.HasPrefix(source, "apache:"), strings.HasPrefix(source, "caddy:"):
			served[site] = true
		case strings.HasPrefix(source, "otlp:"):
			claimed[source] = append(claimed[source], site)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for source, sites := range claimed {
		behind := true
		for _, site := range sites {
			if !served[site] {
				behind = false // it answers for something no web server of this project serves
				break
			}
		}
		if behind {
			out[source] = true
		}
	}
	return out, nil
}

// QueriesOfHost is what one machine saw, with the same precedence between sources: a machine
// that runs both the application and its database would otherwise count a statement twice.
func (r Reader) QueriesOfHost(ctx context.Context, projectID, hostID int64, from, to time.Time) ([]Cost, error) {
	rows, err := r.DB.Q().Query(ctx, `SELECT f.hash, f.fingerprint, h.source, SUM(h.sum), SUM(h.n), MAX(h.max)
		FROM query_hours h JOIN query_fingerprints f ON f.id = h.fingerprint_id
		WHERE h.project_id = ? AND h.host_id = ? AND h.hour >= ? AND h.hour < ?
		GROUP BY f.hash, f.fingerprint, h.source`, projectID, hostID, hourFloor(from), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	best := map[string]Cost{}
	bestRank := map[string]int{}
	for rows.Next() {
		var c Cost
		var source string
		if err := rows.Scan(&c.Target, &c.Label, &source, &c.Seconds, &c.Count, &c.Max); err != nil {
			return nil, err
		}
		c.Kind = "query"
		if rank := querySourceRank[source]; rank > bestRank[c.Target] {
			best[c.Target], bestRank[c.Target] = c, rank
		}
	}
	out := make([]Cost, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seconds > out[j].Seconds })
	return out, nil
}

// Costs is what each thing cost in a window, without what the team excluded.
func (r Reader) Costs(ctx context.Context, projectID int64, from, to time.Time) ([]Cost, error) {
	all, err := r.allCosts(ctx, projectID, from, to)
	if err != nil {
		return nil, err
	}
	set, err := exclude.Active(ctx, r.DB, projectID, to)
	if err != nil || len(set) == 0 {
		return all, err
	}
	out := all[:0]
	for _, c := range all {
		if !set.Cost(c.Kind, c.Target) {
			out = append(out, c)
		}
	}
	return out, nil
}

// ExcludedCosts is what the excluded things cost in a window: the time is not repaid by
// excluding them, and a report that hid it would be a report nobody can trust.
func (r Reader) ExcludedCosts(ctx context.Context, projectID int64, from, to time.Time) ([]Cost, error) {
	set, err := exclude.Active(ctx, r.DB, projectID, to)
	if err != nil || len(set) == 0 {
		return []Cost{}, err
	}
	all, err := r.allCosts(ctx, projectID, from, to)
	if err != nil {
		return nil, err
	}
	out := []Cost{}
	for _, c := range all {
		if set.Cost(c.Kind, c.Target) {
			out = append(out, c)
		}
	}
	return out, nil
}

// subtractExcluded takes what the team excluded out of the totals the speed index is read from:
// endpoints from the requests, queries from the slow query totals and PHP frames from the stalls.
// The totals are per minute and the endpoints per hour: an hour only partly inside the window
// counts for the part inside it. Rows from agents older than the endpoint histogram place the
// slow requests and the others at their own average.
func (r Reader) subtractExcluded(ctx context.Context, projectID int64, from, to time.Time, shadowed map[string]bool, snap *Snapshot) error {
	set, err := exclude.Active(ctx, r.DB, projectID, to)
	if err != nil || len(set) == 0 {
		return err
	}
	hcols := make([]string, 15)
	for i := range hcols {
		hcols[i] = fmt.Sprintf("h%d", i)
	}
	rows, err := r.DB.Q().Query(ctx, `SELECT source, endpoint, hour, n, sum, slow, slow_sum, e5, `+strings.Join(hcols, ", ")+`
		FROM endpoint_hours WHERE project_id = ? AND hour >= ? AND hour < ?`, projectID, hourFloor(from), to.Unix())
	if err != nil {
		return err
	}
	defer rows.Close()
	type minus struct {
		n, sum, e5 float64
		hist       [15]float64
	}
	bySource := map[string]*minus{}
	for rows.Next() {
		var source, endpoint string
		var hour, n, slow, e5 int64
		var sum, slowSum float64
		var hist [15]int64
		dest := []any{&source, &endpoint, &hour, &n, &sum, &slow, &slowSum, &e5}
		for i := range hist {
			dest = append(dest, &hist[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		if _, counted := snap.HTTP[source]; !counted || shadowed[source] || !set.Endpoint(endpoint) || n == 0 {
			continue
		}
		lo, hi := max(hour, from.Unix()), min(hour+3600, to.Unix())
		if hi <= lo {
			continue
		}
		frac := float64(hi-lo) / 3600
		var known int64
		for _, c := range hist {
			known += c
		}
		if known == 0 {
			if slow > 0 {
				hist[protocol.HistogramIndex(slowSum/float64(slow))] += slow
			}
			if rest := n - slow; rest > 0 {
				hist[protocol.HistogramIndex(math.Max(0, sum-slowSum)/float64(rest))] += rest
			}
		}
		m := bySource[source]
		if m == nil {
			m = &minus{}
			bySource[source] = m
		}
		m.n += float64(n) * frac
		m.sum += sum * frac
		m.e5 += float64(e5) * frac
		for i, c := range hist {
			m.hist[i] += float64(c) * frac
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	less := func(v int64, by float64) int64 { return max(0, v-int64(math.Round(by))) }
	for source, m := range bySource {
		t := snap.HTTP[source]
		var over1, over2, over5 float64
		for i, c := range m.hist {
			t.Hist[i] = less(t.Hist[i], c)
			// Slot i holds (HistogramEdges[i-1], HistogramEdges[i]]: 1, 2 and 5 s are edges 6, 8 and 10.
			if i > 6 {
				over1 += c
			}
			if i > 8 {
				over2 += c
			}
			if i > 10 {
				over5 += c
			}
		}
		t.N, t.E5 = less(t.N, m.n), less(t.E5, m.e5)
		t.N1, t.N2, t.N5 = less(t.N1, over1), less(t.N2, over2), less(t.N5, over5)
		t.Sum = math.Max(0, t.Sum-m.sum)
		snap.HTTP[source] = t
	}
	return nil
}

// subtractExcludedQueries takes an excluded query out of the slow query totals of the index.
// Those totals come from the slow log minute by minute, so only what the slow log saw of that
// query is taken out; the slowest query of the window is left as it is, because a maximum cannot
// be split between the queries that made it.
func (r Reader) subtractExcludedQueries(ctx context.Context, projectID int64, from, to time.Time, set exclude.Set, snap *Snapshot) error {
	rows, err := r.DB.Q().Query(ctx, `SELECT f.hash, SUM(h.n), SUM(h.sum) FROM query_hours h JOIN query_fingerprints f ON f.id = h.fingerprint_id
		WHERE h.project_id = ? AND h.hour >= ? AND h.hour < ? AND h.source LIKE '%slowlog' GROUP BY f.hash`, projectID, hourFloor(from), to.Unix())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		var n int64
		var sum float64
		if err := rows.Scan(&hash, &n, &sum); err != nil {
			return err
		}
		if !set.Cost(exclude.KindQuery, hash) {
			continue
		}
		snap.DB.SlowN = max(0, snap.DB.SlowN-n)
		snap.DB.SlowSum = math.Max(0, snap.DB.SlowSum-sum)
	}
	return rows.Err()
}

// subtractExcludedStalls takes an excluded PHP frame out of the stalls the index counts.
func (r Reader) subtractExcludedStalls(ctx context.Context, projectID int64, from, to time.Time, set exclude.Set, snap *Snapshot) error {
	rows, err := r.DB.Q().Query(ctx, `SELECT frame, SUM(n) FROM php_stall_hours WHERE project_id = ? AND hour >= ? AND hour < ? GROUP BY frame`,
		projectID, hourFloor(from), to.Unix())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var frame string
		var n int64
		if err := rows.Scan(&frame, &n); err != nil {
			return err
		}
		if set.Cost(exclude.KindPHP, frame) {
			snap.PHPStalls = max(0, snap.PHPStalls-n)
		}
	}
	return rows.Err()
}

func (r Reader) allCosts(ctx context.Context, projectID int64, from, to time.Time) ([]Cost, error) {
	q := r.DB.Q()
	var out []Cost
	shadowed, err := r.Shadowed(ctx, projectID, from)
	if err != nil {
		return nil, err
	}

	rows, err2 := q.Query(ctx, `SELECT f.hash, f.fingerprint, h.source, SUM(h.sum), SUM(h.n), MAX(h.max)
		FROM query_hours h JOIN query_fingerprints f ON f.id = h.fingerprint_id
		WHERE h.project_id = ? AND h.hour >= ? AND h.hour < ?
		GROUP BY f.hash, f.fingerprint, h.source`, projectID, hourFloor(from), to.Unix())
	if err2 != nil {
		return nil, err2
	}
	best := map[string]Cost{}
	bestRank := map[string]int{}
	for rows.Next() {
		var c Cost
		var source string
		if err := rows.Scan(&c.Target, &c.Label, &source, &c.Seconds, &c.Count, &c.Max); err != nil {
			rows.Close()
			return nil, err
		}
		c.Kind = "query"
		if rank := querySourceRank[source]; rank > bestRank[c.Target] {
			best[c.Target], bestRank[c.Target] = c, rank
		}
	}
	rows.Close()
	for _, c := range best {
		out = append(out, c)
	}

	rows, err = q.Query(ctx, `SELECT source, endpoint, SUM(slow_sum), SUM(slow), MAX(max) FROM endpoint_hours
		WHERE project_id = ? AND hour >= ? AND hour < ? GROUP BY source, endpoint HAVING SUM(slow) > 0`, projectID, hourFloor(from), to.Unix())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var source, endpoint string
		c := Cost{Kind: "endpoint"}
		if err := rows.Scan(&source, &endpoint, &c.Seconds, &c.Count, &c.Max); err != nil {
			rows.Close()
			return nil, err
		}
		if shadowed[source] {
			continue // counted from the web server in front of it
		}
		c.Target, c.Label = source+" "+endpoint, endpoint
		out = append(out, c)
	}
	rows.Close()

	rows, err = q.Query(ctx, `SELECT name, SUM(sum), SUM(count), MAX(max) FROM metric_minutes WHERE project_id = ? AND kind = 'timer' AND bucket >= ? AND bucket < ? GROUP BY name`,
		projectID, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c := Cost{Kind: "metric"}
		if err := rows.Scan(&c.Target, &c.Seconds, &c.Count, &c.Max); err != nil {
			rows.Close()
			return nil, err
		}
		c.Label = c.Target
		out = append(out, c)
	}
	rows.Close()

	rows, err = q.Query(ctx, `SELECT frame, SUM(n) FROM php_stall_hours WHERE project_id = ? AND hour >= ? AND hour < ? GROUP BY frame`, projectID, hourFloor(from), to.Unix())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c := Cost{Kind: "php"}
		if err := rows.Scan(&c.Target, &c.Count); err != nil {
			rows.Close()
			return nil, err
		}
		c.Label, c.Seconds = c.Target, float64(c.Count)*PHPStallSeconds
		out = append(out, c)
	}
	rows.Close()

	sort.Slice(out, func(i, j int) bool { return out[i].Seconds > out[j].Seconds })
	return out, nil
}

// HasData tells whether any measure arrived in the window: without it a quiet
// window proves nothing (agent down, no traffic).
func (r Reader) HasData(ctx context.Context, projectID int64, from, to time.Time) (bool, error) {
	var n int
	err := r.DB.Q().Row(ctx, `SELECT
		(SELECT COUNT(*) FROM http_minutes WHERE project_id = ? AND bucket >= ? AND bucket < ?) +
		(SELECT COUNT(*) FROM db_minutes WHERE project_id = ? AND bucket >= ? AND bucket < ?) +
		(SELECT COUNT(*) FROM query_hours WHERE project_id = ? AND hour >= ? AND hour < ?)`,
		projectID, from.Unix(), to.Unix(), projectID, from.Unix(), to.Unix(), projectID, from.Unix(), to.Unix()).Scan(&n)
	return n > 0, err
}

// ObservedDays is how many days of data a window really holds (see project.Repo.ObservedDays).
func (r Reader) ObservedDays(ctx context.Context, projectID int64, window time.Duration, now time.Time) float64 {
	return project.Repo{DB: r.DB}.ObservedDays(ctx, projectID, window, now)
}

// FirstData is the time of the oldest measure of the project (zero if none).
func (r Reader) FirstData(ctx context.Context, projectID int64) (time.Time, error) {
	var first int64
	err := r.DB.Q().Row(ctx, `SELECT COALESCE(MIN(t), 0) FROM (
		SELECT MIN(bucket) AS t FROM http_minutes WHERE project_id = ?
		UNION ALL SELECT MIN(bucket) FROM db_minutes WHERE project_id = ?
		UNION ALL SELECT MIN(hour) FROM query_hours WHERE project_id = ?) x WHERE t IS NOT NULL`, projectID, projectID, projectID).Scan(&first)
	if first == 0 {
		return time.Time{}, err
	}
	return time.Unix(first, 0), err
}
