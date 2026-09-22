package agent

import (
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
)

const (
	maxEndpointsPerMinute = 300
	maxSitesPerSource     = 8
	maxSourcesWithSites   = 200
	siteForgottenAfter    = time.Hour
	maxQueriesPerMinute   = 300
	maxJobsPerMinute      = 300
	otherEndpoint         = "OTHER"
	maxSampleLen          = 4000
)

// Drained is what leaves the aggregator: a payload without batch and agent info.
type Drained = protocol.Payload

// SlowQuery is one entry of a slow query log.
type SlowQuery struct {
	Time     time.Time
	User     string
	Database string
	Seconds  float64
	Rows     int64
	SQL      string
}

// Aggregator sums everything per minute. Safe for concurrent use.
type Aggregator struct {
	// ShareSamples sends real query samples; otherwise they are redacted and kept in Samples.
	ShareSamples bool
	Samples      *SampleStore

	mu       sync.Mutex
	http     map[bucketKey]*protocol.HTTPBucket
	php      map[bucketKey]*phpBucket
	db       map[bucketKey]*protocol.DBBucket
	dbStatus map[int64]protocol.DBStatus
	host     map[int64]protocol.HostSample
	metrics  map[metricKey]*protocol.MetricBucket
	app      map[int64]*protocol.AppBucket
	jobs     map[jobKey]*protocol.JobBucket

	// Recognises OpenTelemetry services that sit behind a web server on this machine.
	web webDedupe
	// Hosts each source answers for, and when each was last seen: how a server recognises the
	// same requests reported by a web server on one machine and by the application on another.
	// They fade, so a host a source stopped answering for stops travelling with its buckets.
	sites map[string]map[string]time.Time
}

type bucketKey struct {
	source string
	minute int64
}

type phpBucket struct {
	n      int
	stalls map[[2]string]int
}

func NewAggregator() *Aggregator {
	return &Aggregator{
		Samples:  NewSampleStore(2000),
		http:     map[bucketKey]*protocol.HTTPBucket{},
		php:      map[bucketKey]*phpBucket{},
		db:       map[bucketKey]*protocol.DBBucket{},
		dbStatus: map[int64]protocol.DBStatus{},
		host:     map[int64]protocol.HostSample{},
	}
}

func minuteOf(t time.Time) int64 {
	return t.Unix() - ((t.Unix()%60)+60)%60
}

// SourceSite records a host a source answers for: the web server reads it in its configuration,
// an application in the Host header of the request it just served. Bounded like everything else
// here - eight hosts per source, which is more than any single log or service has in practice.
func (a *Aggregator) SourceSite(source, site string) {
	site = strings.ToLower(strings.TrimSpace(site))
	if site == "" || strings.ContainsAny(site, " /\\") {
		return // not a host: nothing a server could match on
	}
	if i := strings.LastIndexByte(site, ':'); i > 0 && !strings.Contains(site[i:], "]") {
		site = site[:i] // "shop.example.com:8443"
	}
	// An address is not a name: a container binds its own, an SDK reports the one it listens on,
	// and two of them meeting would say nothing about the same requests being seen twice.
	if net.ParseIP(strings.Trim(site, "[]")) != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sites == nil {
		a.sites = map[string]map[string]time.Time{}
	}
	hosts := a.sites[source]
	if hosts == nil {
		if len(a.sites) >= maxSourcesWithSites {
			return
		}
		hosts = map[string]time.Time{}
		a.sites[source] = hosts
	}
	now := time.Now()
	if _, known := hosts[site]; known || len(hosts) < maxSitesPerSource {
		hosts[site] = now
		return
	}
	// Full: the host nobody has answered for in the longest time makes room for this one.
	oldest, at := "", now
	for host, seen := range hosts {
		if seen.Before(at) {
			oldest, at = host, seen
		}
	}
	if oldest != "" && now.Sub(at) > siteForgottenAfter {
		delete(hosts, oldest)
		hosts[site] = now
	}
}

// sitesOf is the sorted list of hosts a source has answered for lately, as it travels with every
// bucket. One it has not answered for in an hour is dropped: the server would otherwise keep
// believing a pairing that stopped being true.
func (a *Aggregator) sitesOf(source string) []string {
	hosts := a.sites[source]
	if len(hosts) == 0 {
		return nil
	}
	cutoff := time.Now().Add(-siteForgottenAfter)
	out := make([]string, 0, len(hosts))
	for site, seen := range hosts {
		if seen.Before(cutoff) {
			delete(hosts, site)
			continue
		}
		out = append(out, site)
	}
	if len(out) == 0 {
		delete(a.sites, source)
		return nil
	}
	sort.Strings(out)
	return out
}

func (a *Aggregator) AddRequest(source string, t time.Time, method, path string, status int, seconds, slowSeconds float64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	key := bucketKey{source, minuteOf(t)}
	b := a.http[key]
	if b == nil {
		b = &protocol.HTTPBucket{
			Source:    source,
			Minute:    key.minute,
			Hist:      make([]int, len(protocol.HistogramEdges)+1),
			Endpoints: map[string]protocol.EndpointStat{},
		}
		a.http[key] = b
	}

	b.N++
	b.Sum += seconds
	b.Max = max(b.Max, seconds)
	b.Hist[protocol.HistogramIndex(seconds)]++
	if seconds > 1 {
		b.N1++
	}
	if seconds > 2 {
		b.N2++
	}
	if seconds > 5 {
		b.N5++
	}
	if status >= 500 {
		b.E5++
	} else if status >= 400 {
		b.E4++
	}

	endpoint := method + " " + NormalizePath(path)
	a.web.observe(source, method+" "+NormalizePath(path), status, t, seconds)
	if _, known := b.Endpoints[endpoint]; !known && len(b.Endpoints) >= maxEndpointsPerMinute {
		endpoint = otherEndpoint
	}
	e := b.Endpoints[endpoint]
	e.N++
	e.Sum += seconds
	e.Max = max(e.Max, seconds)
	if e.Hist == nil {
		e.Hist = make([]int, len(protocol.HistogramEdges)+1)
	}
	e.Hist[protocol.HistogramIndex(seconds)]++
	if seconds >= slowSeconds {
		e.Slow++
		e.SlowSum += seconds
	}
	if status >= 500 {
		e.E5++
	}
	b.Endpoints[endpoint] = e
}

// jobKey is one job class or one scheduled command, per minute.
type jobKey struct {
	minute int64
	kind   string // job | command
	name   string
}

// AddJob records one run of a queued job or of a scheduled command: the Jobs page is about
// things nobody is waiting for, which is exactly why nobody notices when they get slower.
func (a *Aggregator) AddJob(t time.Time, kind, name string, seconds float64, failed bool) {
	name = truncate(strings.TrimSpace(name), 191)
	if name == "" || (kind != "job" && kind != "command") {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.jobs == nil {
		a.jobs = map[jobKey]*protocol.JobBucket{}
	}
	k := jobKey{minuteOf(t), kind, name}
	b := a.jobs[k]
	if b == nil {
		if len(a.jobs) >= maxJobsPerMinute*5 {
			return
		}
		b = &protocol.JobBucket{Minute: k.minute, Kind: kind, Name: name}
		a.jobs[k] = b
	}
	b.N++
	b.Sum += seconds
	if seconds > b.Max {
		b.Max = seconds
	}
	if failed {
		b.Failed++
	}
}

func (a *Aggregator) AddPHPStall(source string, t time.Time, frame, code string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	key := bucketKey{source, minuteOf(t)}
	b := a.php[key]
	if b == nil {
		b = &phpBucket{stalls: map[[2]string]int{}}
		a.php[key] = b
	}
	b.n++
	b.stalls[[2]string{truncate(frame, 191), truncate(code, 191)}]++
}

func (a *Aggregator) AddSlowQuery(source string, q SlowQuery) {
	if ignoredStatement(q.SQL) {
		return
	}
	fp := fingerprint.Of(q.SQL)
	hash := fingerprint.Hash(fp)

	a.mu.Lock()
	defer a.mu.Unlock()

	b := a.dbBucket(source, minuteOf(q.Time))
	b.SlowN++
	b.SlowSum += q.Seconds
	b.SlowMax = max(b.SlowMax, q.Seconds)

	qs, known := b.Queries[hash]
	if !known && len(b.Queries) >= maxQueriesPerMinute {
		return
	}
	qs.Fingerprint = truncate(fp, maxSampleLen)
	qs.User = q.User
	qs.Database = q.Database
	qs.N++
	qs.Sum += q.Seconds
	qs.Rows += q.Rows
	if q.Seconds >= qs.Max {
		// Keep the slowest real statement: it is the one worth an EXPLAIN.
		qs.Max = q.Seconds
		qs.Sample = a.outgoingSample(hash, q.SQL, q.Seconds, SampleOwner{Engine: "mysql"})
	}
	b.Queries[hash] = qs
}

// AddQueryStats adds already aggregated statistics (performance_schema, pg_stat_statements).
func (a *Aggregator) AddQueryStats(source string, owner SampleOwner, t time.Time, stats map[string]protocol.QueryStat, slowN int, slowSum, slowMax float64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	b := a.dbBucket(source, minuteOf(t))
	b.SlowN += slowN
	b.SlowSum += slowSum
	b.SlowMax = max(b.SlowMax, slowMax)
	for hash, s := range stats {
		if ignoredStatement(s.Fingerprint) {
			continue
		}
		cur, known := b.Queries[hash]
		if !known && len(b.Queries) >= maxQueriesPerMinute {
			continue
		}
		cur.Fingerprint, cur.User, cur.Database = s.Fingerprint, s.User, s.Database
		if s.Sample != "" {
			cur.Sample = a.outgoingSample(hash, s.Sample, s.Max, owner)
		}
		cur.N += s.N
		cur.Sum += s.Sum
		cur.Max = max(cur.Max, s.Max)
		cur.Rows += s.Rows
		b.Queries[hash] = cur
	}
}

func (a *Aggregator) outgoingSample(hash, sql string, seconds float64, owner SampleOwner) string {
	sql = truncate(sql, maxSampleLen)
	a.Samples.Put(hash, sql, seconds, owner)
	if a.ShareSamples {
		return sql
	}
	return fingerprint.Redact(sql)
}

func (a *Aggregator) dbBucket(source string, minute int64) *protocol.DBBucket {
	key := bucketKey{source, minute}
	b := a.db[key]
	if b == nil {
		b = &protocol.DBBucket{Source: source, Minute: minute, Queries: map[string]protocol.QueryStat{}}
		a.db[key] = b
	}
	return b
}

func (a *Aggregator) SetDBStatus(s protocol.DBStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dbStatus[s.Minute] = s
}

func (a *Aggregator) SetHost(s protocol.HostSample) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.host[s.Minute] = s
}

// Drain removes and returns every bucket whose minute ended before `before`.
func (a *Aggregator) Drain(before time.Time) Drained {
	a.mu.Lock()
	defer a.mu.Unlock()

	limit := before.Unix()
	var out Drained

	a.web.decide(before)
	for k, b := range a.http {
		if k.minute+60 <= limit {
			switch a.web.verdict(k.source, k.minute, before) {
			case webWait:
				continue
			case webDuplicate:
				delete(a.http, k)
				continue
			}
			b.Sites = a.sitesOf(k.source)
			out.HTTP = append(out.HTTP, *b)
			delete(a.http, k)
		}
	}
	for k, b := range a.php {
		if k.minute+60 <= limit {
			pb := protocol.PHPBucket{Source: k.source, Minute: k.minute, N: b.n}
			for fc, n := range b.stalls {
				pb.Stalls = append(pb.Stalls, protocol.PHPStall{Frame: fc[0], Code: fc[1], N: n})
			}
			sort.Slice(pb.Stalls, func(i, j int) bool { return pb.Stalls[i].N > pb.Stalls[j].N })
			out.PHP = append(out.PHP, pb)
			delete(a.php, k)
		}
	}
	for k, b := range a.db {
		if k.minute+60 <= limit {
			out.DB = append(out.DB, *b)
			delete(a.db, k)
		}
	}
	for m, s := range a.dbStatus {
		if m+60 <= limit {
			out.DBStatus = append(out.DBStatus, s)
			delete(a.dbStatus, m)
		}
	}
	for m, s := range a.host {
		if m+60 <= limit {
			out.Host = append(out.Host, s)
			delete(a.host, m)
		}
	}

	for m, b := range a.app {
		if m+60 <= limit {
			out.App = append(out.App, *b)
			delete(a.app, m)
		}
	}
	sort.Slice(out.App, func(i, j int) bool { return out.App[i].Minute < out.App[j].Minute })

	for k, b := range a.metrics {
		if k.minute+60 <= limit {
			out.Metrics = append(out.Metrics, *b)
			delete(a.metrics, k)
		}
	}
	sort.Slice(out.Metrics, func(i, j int) bool {
		if out.Metrics[i].Minute != out.Metrics[j].Minute {
			return out.Metrics[i].Minute < out.Metrics[j].Minute
		}
		return out.Metrics[i].Name < out.Metrics[j].Name
	})

	sort.Slice(out.HTTP, func(i, j int) bool { return out.HTTP[i].Minute < out.HTTP[j].Minute })
	sort.Slice(out.PHP, func(i, j int) bool { return out.PHP[i].Minute < out.PHP[j].Minute })
	sort.Slice(out.DB, func(i, j int) bool { return out.DB[i].Minute < out.DB[j].Minute })
	sort.Slice(out.DBStatus, func(i, j int) bool { return out.DBStatus[i].Minute < out.DBStatus[j].Minute })
	sort.Slice(out.Host, func(i, j int) bool { return out.Host[i].Minute < out.Host[j].Minute })
	for k, b := range a.jobs {
		if k.minute+60 <= limit {
			out.Jobs = append(out.Jobs, *b)
			delete(a.jobs, k)
		}
	}

	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Do not cut a multi-byte character in half.
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
