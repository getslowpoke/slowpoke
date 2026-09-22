// Package protocol defines the JSON payloads exchanged by agents, the Laravel
// package and the server. Everything is pre-aggregated per minute: no raw
// request or user data ever leaves the monitored machine.
package protocol

import "encoding/json"

// HistogramEdges are the upper bounds (seconds) of the response time histogram.
var HistogramEdges = []float64{0.05, 0.1, 0.2, 0.3, 0.5, 0.75, 1, 1.5, 2, 3, 5, 10, 20, 60}

// Payload is the body of POST /api/v1/ingest.
type Payload struct {
	BatchID  string         `json:"batch_id"`
	Agent    AgentInfo      `json:"agent"`
	HTTP     []HTTPBucket   `json:"http,omitempty"`
	PHP      []PHPBucket    `json:"php,omitempty"`
	DB       []DBBucket     `json:"db,omitempty"`
	DBStatus []DBStatus     `json:"db_status,omitempty"`
	Host     []HostSample   `json:"host,omitempty"`
	Explains []Explain      `json:"explains,omitempty"`
	App      []AppBucket    `json:"app,omitempty"`
	Jobs     []JobBucket    `json:"jobs,omitempty"`
	Commands []CommandRun   `json:"commands,omitempty"`
	Metrics  []MetricBucket `json:"metrics,omitempty"`
	// Network snapshot of the machine: who this host talks to (the infrastructure map).
	Network *NetworkSnapshot `json:"network,omitempty"`
	// Checks say what each source can do on the machine, refreshed every few minutes, so the
	// UI shows complete, partial and optional integrations and how to change them.
	Checks []SourceCheck `json:"checks,omitempty"`
}

// SourceCheck is the state of one configured source (Source as in AgentInfo.Sources).
type SourceCheck struct {
	Source string      `json:"source"`
	Items  []CheckItem `json:"items"`
	// Facts are non-secret details the instructions need: versions, the database user and
	// address the agent uses, file paths, the listed schemas. Never passwords or tokens.
	Facts map[string]string `json:"facts,omitempty"`
}

// CheckItem is one capability of a source: statistics, slow log, plans, request duration...
type CheckItem struct {
	Key      string `json:"key"`
	Status   string `json:"status"` // ok | off | error | waiting
	Optional bool   `json:"optional,omitempty"`
	Reason   string `json:"reason,omitempty"` // stable code the UI turns into instructions
	Detail   string `json:"detail,omitempty"` // the underlying message, e.g. the database error
}

type NetworkSnapshot struct {
	Addresses   []string        `json:"addresses"`
	Listening   []ListeningPort `json:"listening"`
	Connections []Connection    `json:"connections"`
}

type ListeningPort struct {
	Port    int    `json:"port"`
	Local   bool   `json:"local"` // bound to loopback only
	Service string `json:"service,omitempty"`
	Process string `json:"process,omitempty"`
}

type Connection struct {
	Direction  string `json:"direction"` // out | in
	RemoteIP   string `json:"remote_ip"`
	RemotePort int    `json:"remote_port,omitempty"` // for out
	LocalPort  int    `json:"local_port,omitempty"`  // for in
	Service    string `json:"service,omitempty"`
	// Process that holds the socket ("php-fpm:www", "nginx", "php artisan queue:work"),
	// so the map can start the arrow from the service that really opened the connection.
	Process string `json:"process,omitempty"`
	Count   int    `json:"count"`
}

type AgentInfo struct {
	Host    string   `json:"host"`
	Kind    string   `json:"kind"` // agent | laravel
	Version string   `json:"version"`
	OS      string   `json:"os,omitempty"`
	Sources []string `json:"sources,omitempty"`
}

// HTTPBucket summarizes one minute of an access log.
type HTTPBucket struct {
	Source    string                  `json:"source"`
	Minute    int64                   `json:"minute"` // unix seconds, start of the minute (UTC)
	N         int                     `json:"n"`
	Sum       float64                 `json:"sum"`
	Max       float64                 `json:"max"`
	N1        int                     `json:"n1"` // > 1 s
	N2        int                     `json:"n2"` // > 2 s
	N5        int                     `json:"n5"` // > 5 s
	E4        int                     `json:"e4"`
	E5        int                     `json:"e5"`
	Hist      []int                   `json:"hist"`
	Endpoints map[string]EndpointStat `json:"endpoints,omitempty"` // "GET /orders/{id}"
	// Hosts these requests were addressed to ("shop.example.com"): a web server knows them from
	// its configuration, an application from the Host header. When a web server and an
	// application report the same host, they are the same requests seen twice and the server
	// counts the web server's, which is the whole wait. Empty when nobody could tell.
	Sites []string `json:"sites,omitempty"`
}

type EndpointStat struct {
	N       int     `json:"n"`
	Sum     float64 `json:"sum"`
	Max     float64 `json:"max"`
	Slow    int     `json:"slow"`
	SlowSum float64 `json:"slow_sum"`
	E5      int     `json:"e5"`
	// Hist is the response time histogram of this endpoint alone (HistogramEdges): what lets the
	// server take an endpoint the team excluded out of the speed index. Empty from older agents.
	Hist []int `json:"hist,omitempty"`
}

// PHPBucket summarizes one minute of a php-fpm slowlog.
type PHPBucket struct {
	Source string     `json:"source"`
	Minute int64      `json:"minute"`
	N      int        `json:"n"`
	Stalls []PHPStall `json:"stalls,omitempty"`
}

type PHPStall struct {
	Frame string `json:"frame"` // deepest frame: where PHP was waiting
	Code  string `json:"code"`  // first frame in application code
	N     int    `json:"n"`
}

// DBBucket summarizes one minute of queries (slow log, performance_schema or pg_stat_statements).
type DBBucket struct {
	Source  string               `json:"source"`
	Minute  int64                `json:"minute"`
	SlowN   int                  `json:"slow_n"`
	SlowSum float64              `json:"slow_sum"`
	SlowMax float64              `json:"slow_max"`
	Queries map[string]QueryStat `json:"queries,omitempty"` // key: fingerprint hash
}

type QueryStat struct {
	Fingerprint string  `json:"fingerprint"`
	Sample      string  `json:"sample,omitempty"`
	User        string  `json:"user,omitempty"`
	Database    string  `json:"database,omitempty"`
	N           int64   `json:"n"`
	Sum         float64 `json:"sum"`
	Max         float64 `json:"max"`
	Rows        int64   `json:"rows"`
}

type DBStatus struct {
	Minute           int64   `json:"minute"`
	QPS              float64 `json:"qps"`
	RowsReadPS       float64 `json:"rows_read_ps"`
	ThreadsRunning   int     `json:"threads_running"`
	ThreadsConnected int     `json:"threads_connected"`
}

type HostSample struct {
	Minute       int64     `json:"minute"`
	CPU          float64   `json:"cpu"` // 0-100, busy time
	CPUIOWait    float64   `json:"cpu_iowait"`
	CPUSteal     float64   `json:"cpu_steal"` // taken by the hypervisor: a noisy neighbour
	Load1        float64   `json:"load1"`
	Load5        float64   `json:"load5"`
	Load15       float64   `json:"load15"`
	CPUs         int       `json:"cpus"`
	MemUsed      uint64    `json:"mem_used"`
	MemTotal     uint64    `json:"mem_total"`
	SwapUsed     uint64    `json:"swap_used"`
	SwapTotal    uint64    `json:"swap_total"`
	SwapOutPages uint64    `json:"swap_out_pages"` // pages swapped out since the previous sample
	OOMKills     uint64    `json:"oom_kills"`      // processes killed for lack of memory since the previous sample
	DiskUsed     uint64    `json:"disk_used"`
	DiskTotal    uint64    `json:"disk_total"`
	DiskReadBps  float64   `json:"disk_read_bps"`
	DiskWriteBps float64   `json:"disk_write_bps"`
	NetRxBps     float64   `json:"net_rx_bps"`
	NetTxBps     float64   `json:"net_tx_bps"`
	TopProcesses []Process `json:"top_processes,omitempty"`
}

type Process struct {
	Name string  `json:"name"`
	CPU  float64 `json:"cpu"` // percent of one core since the previous sample (0 on the first)
	RSS  uint64  `json:"rss"`
	N    int     `json:"n"` // processes with this name (php-fpm workers are grouped)
}

// Explain is the answer of the agent to an ExplainRequest.
type Explain struct {
	Hash   string `json:"hash"`
	Engine string `json:"engine"` // mysql | postgres
	// Generic plans come from EXPLAIN (GENERIC_PLAN) on a statement without values:
	// row estimates are the planner's averages, not those of a real call.
	Generic bool                `json:"generic,omitempty"`
	Plan    json.RawMessage     `json:"plan,omitempty"`
	Tables  map[string]TableDef `json:"tables,omitempty"`
	Error   string              `json:"error,omitempty"`
}

type TableDef struct {
	Rows    int64    `json:"rows"`
	Columns []Column `json:"columns"`
	Indexes []Index  `json:"indexes"`
}

type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

// AppBucket comes from the framework packages: routes and the queries they run.
type AppBucket struct {
	Minute  int64                `json:"minute"`
	Routes  map[string]RouteStat `json:"routes,omitempty"`
	Queries map[string]AppQuery  `json:"queries,omitempty"` // key: fingerprint hash
}

type RouteStat struct {
	N       int                   `json:"n"`
	Sum     float64               `json:"sum"`
	Max     float64               `json:"max"`
	Queries int                   `json:"queries"`
	DBSum   float64               `json:"db_sum"`
	E5      int                   `json:"e5"`
	Hashes  map[string]RouteQuery `json:"hashes,omitempty"`
	// ExtSum is the time the requests spent waiting on outbound HTTP calls (a payment provider,
	// a partner API, another service): overlapping calls count once, so it is never more than
	// Sum. ExtRequests is how many requests made at least one call.
	ExtSum      float64            `json:"ext_sum,omitempty"`
	ExtRequests int                `json:"ext_requests,omitempty"`
	Ext         map[string]ExtCall `json:"ext,omitempty"` // key: remote host ("api.stripe.com")
}

// ExtCall is what the requests of one route spent calling one remote host. Only the host
// leaves the machine: paths and query strings carry tokens and personal data.
type ExtCall struct {
	N        int     `json:"n"`
	Sum      float64 `json:"sum"`
	Max      float64 `json:"max"`
	Errors   int     `json:"errors"`
	Requests int     `json:"requests"`
	Origin   string  `json:"origin,omitempty"` // app/Services/Stripe.php:88
}

type RouteQuery struct {
	N        int     `json:"n"`
	Sum      float64 `json:"sum"`
	Requests int     `json:"requests"`
}

type AppQuery struct {
	Fingerprint string  `json:"fingerprint"`
	Sample      string  `json:"sample,omitempty"`
	Origin      string  `json:"origin,omitempty"` // app/Http/Controllers/OrderController.php:42
	N           int     `json:"n"`
	Sum         float64 `json:"sum"`
	NPlusOne    int     `json:"n_plus_one"` // requests where it ran as N+1
}

type JobBucket struct {
	Minute int64   `json:"minute"`
	Kind   string  `json:"kind"` // job | command
	Name   string  `json:"name"`
	N      int     `json:"n"`
	Failed int     `json:"failed"`
	Sum    float64 `json:"sum"`
	Max    float64 `json:"max"`
}

type CommandRun struct {
	Host       string `json:"host"`
	Name       string `json:"name"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
}

// MetricBucket is one minute of a custom metric (SDK, StatsD, Prometheus).
type MetricBucket struct {
	Minute int64   `json:"minute"`
	Name   string  `json:"name"`
	Kind   string  `json:"kind"` // counter | gauge | timer (seconds)
	Count  int64   `json:"count"`
	Sum    float64 `json:"sum"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Last   float64 `json:"last"`
	Hist   []int   `json:"hist,omitempty"` // timers only, HistogramEdges
}

// IngestResponse may carry work for the agent: the server never connects to it.
type IngestResponse struct {
	OK      bool             `json:"ok"`
	Explain []ExplainRequest `json:"explain,omitempty"`
	// ServerVersion lets an agent notice it is older than the server it talks to.
	ServerVersion string `json:"server_version,omitempty"`
	// AgentDownload is where this server hands out the agent for that machine
	// ("/agent/linux-amd64"), empty when it does not carry one.
	AgentDownload string `json:"agent_download,omitempty"`
}

type ExplainRequest struct {
	Hash     string `json:"hash"`
	SQL      string `json:"sql"`
	Database string `json:"database,omitempty"`
}

// Deploy is the body of POST /api/v1/deploys.
type Deploy struct {
	Version    string `json:"version,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Author     string `json:"author,omitempty"`
	Message    string `json:"message,omitempty"`
	URL        string `json:"url,omitempty"`
	DeployedAt int64  `json:"deployed_at,omitempty"`
}

// HistogramIndex returns the histogram slot of a duration.
func HistogramIndex(seconds float64) int {
	for i, edge := range HistogramEdges {
		if seconds <= edge {
			return i
		}
	}
	return len(HistogramEdges)
}
