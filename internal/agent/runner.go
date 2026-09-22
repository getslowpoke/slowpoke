package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/getslowpoke/slowpoke/internal/agent/detect"
	"github.com/getslowpoke/slowpoke/internal/protocol"
)

var Version = "0.1.0-dev"

type fileSource struct {
	src    Source
	parser LineParser
}

// Runner reads every source once per interval and ships closed minutes.
type Runner struct {
	cfg       *Config
	agg       *Aggregator
	positions *Positions
	sender    *Sender
	files     []fileSource
	dbs       []*dbSource
	redises   []*redisSource
	rabbits   []*rabbitmqSource
	elastics  []*elasticsearchSource
	host      *hostCollector
	proms     []*promScraper
	statsd    []string
	otlp      []Source

	mu       sync.Mutex
	explains []protocol.Explain
	// saidAbout is the server version we already reported or tried: said once, not every round.
	saidAbout string
	// restart is set when the binary was replaced: the process stops and the supervisor starts
	// the new one.
	restart bool
	busy    map[string]bool

	otlpReceivers []*otlpListener
	fileOK        map[string]bool                 // last known state of each log file: only changes are logged
	scan          func(root string) detect.Report // injectable for tests
	scanRoot      string
	scannedAt     time.Time
	startedAt     time.Time
	cost          costMeter
	scanned       []protocol.SourceCheck
	waitingDBs    []Source // configured databases whose password is still a placeholder
	checkedAt     time.Time
	checkWait     time.Duration
}

// otlpListener is a running OTLP receiver with what its source check needs.
type otlpListener struct {
	*otlpReceiver
	name, addr, listenError string
}

func NewRunner(cfg *Config) (*Runner, error) {
	r := &Runner{
		startedAt: time.Now(),
		cfg:       cfg,
		agg:       NewAggregator(),
		positions: LoadPositions(cfg.StateDir),
		sender:    NewSender(cfg.Server, cfg.Token),
		busy:      map[string]bool{},
	}

	r.agg.ShareSamples = cfg.ShareSamples
	hasSlowlog := false
	for _, s := range cfg.Sources {
		if s.Type == "mysql-slowlog" {
			hasSlowlog = true
		}
	}

	for _, s := range cfg.Sources {
		switch s.Type {
		case "nginx", "apache", "caddy":
			r.files = append(r.files, fileSource{s, NewNginxParser(s, r.agg)})
		case "php-fpm":
			r.files = append(r.files, fileSource{s, NewFpmParser(s, r.agg)})
		case "mysql-slowlog":
			r.files = append(r.files, fileSource{s, NewMySQLSlowParser(s, r.agg)})
		case "mongodb":
			r.files = append(r.files, fileSource{s, NewMongoSlowParser(s, r.agg)})
		case "mysql", "postgres":
			// The generated config carries ${SLOWPOKE_DB_PASSWORD} until someone fills it in.
			// Connecting with the literal placeholder would knock on the monitored database
			// every 30 s with a wrong password, and those failures count on the server.
			if hasUnsetPlaceholder(s.DSN) {
				slog.Warn("database source waiting for its password: set it in agent.yaml, or remove the source",
					"source", s.Name, "type", s.Type)
				r.waitingDBs = append(r.waitingDBs, s)
				continue
			}
			// With a slow log the slow totals come from there: do not count them twice.
			d, err := openDBSource(s, !hasSlowlog)
			if err != nil {
				return nil, fmt.Errorf("source %s: %w", s.Name, err)
			}
			d.id = strconv.Itoa(len(r.dbs))
			r.dbs = append(r.dbs, d)
		case "redis":
			rs, err := newRedisSource(s)
			if err != nil {
				return nil, fmt.Errorf("source %s: %w", s.Name, err)
			}
			r.redises = append(r.redises, rs)
		case "rabbitmq":
			mq, err := newRabbitMQSource(s)
			if err != nil {
				return nil, fmt.Errorf("source %s: %w", s.Name, err)
			}
			r.rabbits = append(r.rabbits, mq)
		case "elasticsearch", "opensearch":
			es, err := newElasticsearchSource(s)
			if err != nil {
				return nil, fmt.Errorf("source %s: %w", s.Name, err)
			}
			r.elastics = append(r.elastics, es)
		case "host":
			r.host = newHostCollector()
		case "statsd":
			addr := s.Listen
			if addr == "" {
				addr = ":8125"
			}
			r.statsd = append(r.statsd, addr)
		case "otlp":
			if s.Listen == "" {
				s.Listen = "127.0.0.1:4318"
			}
			r.otlp = append(r.otlp, s)
		case "prometheus":
			if s.URL == "" {
				return nil, fmt.Errorf("source %q: prometheus needs url", s.Name)
			}
			r.proms = append(r.proms, newPromScraper(s))
		default:
			return nil, fmt.Errorf("source %q: unknown type %q", s.Name, s.Type)
		}
	}
	if len(r.dbs) > 0 {
		r.sender.Capabilities = "explain"
	}
	return r, nil
}

// Run blocks until the context is cancelled.
func (r *Runner) Run(ctx context.Context) {
	slog.Info("slowpoke agent started", "version", Version, "host", r.cfg.Host, "server", r.cfg.Server, "sources", len(r.cfg.Sources))
	for _, addr := range r.statsd {
		if err := listenStatsD(ctx, addr, r.agg); err != nil {
			slog.Error("statsd: cannot listen", "addr", addr, "error", err)
		}
	}
	for _, src := range r.otlp {
		l := &otlpListener{otlpReceiver: newOTLPReceiver(r.agg).withCodeRoot(src.CodeRoot), name: src.Name, addr: src.Listen}
		if err := l.otlpReceiver.listen(ctx, src.Listen); err != nil {
			l.listenError = err.Error()
			slog.Error("otlp: cannot listen", "addr", src.Listen, "error", err)
		} else {
			slog.Info("otlp receiver listening", "addr", src.Listen)
		}
		r.otlpReceivers = append(r.otlpReceivers, l)
	}
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	r.Round(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			// Ship what is complete before leaving.
			r.Round(context.Background(), time.Now())
			return
		case now := <-ticker.C:
			r.Round(ctx, now)
			if r.restart {
				slog.Info("stopping so the new agent can start")
				return
			}
		}
	}
}

// sayOnce logs only when a source changes state: missing -> readable, or back. The first round
// of a healthy source is silent, so the journal shows what changed and nothing else.
func (r *Runner) sayOnce(path string, readable bool, say func()) {
	if r.fileOK == nil {
		r.fileOK = map[string]bool{}
	}
	was, seen := r.fileOK[path]
	r.fileOK[path] = readable
	if seen && was == readable {
		return
	}
	if !seen && readable {
		return // nothing to announce: it works, as expected
	}
	say()
}

// Round reads, polls and sends once. `now` is injectable for tests.
func (r *Runner) Round(ctx context.Context, now time.Time) {
	for _, f := range r.files {
		err := ReadNewLines(r.positions, f.src.Path, f.src.FromStart, f.parser.Feed)
		if err != nil {
			// A log that does not exist yet is the normal state of a fresh install: the line is
			// said once, then the agent keeps looking without filling the journal.
			r.sayOnce(f.src.Path, false, func() {
				if os.IsNotExist(err) {
					slog.Warn("waiting for source", "source", f.src.Name, "path", f.src.Path)
					return
				}
				slog.Warn("cannot read source", "source", f.src.Name, "path", f.src.Path, "error", err)
			})
			continue
		}
		r.sayOnce(f.src.Path, true, func() {
			slog.Info("reading source", "source", f.src.Name, "path", f.src.Path)
		})
		if fl, ok := f.parser.(Flusher); ok {
			fl.Flush()
		}
	}
	if err := r.positions.Save(); err != nil {
		slog.Warn("cannot save positions", "error", err)
	}

	for _, d := range r.dbs {
		if err := d.poll(ctx, r.agg, now); err != nil {
			slog.Warn("database poll failed", "error", err)
		}
	}
	for _, rs := range r.redises {
		if err := rs.poll(ctx, r.agg, now); err != nil {
			slog.Warn("redis poll failed", "source", rs.src.Name, "error", err)
		}
	}
	for _, mq := range r.rabbits {
		if err := mq.poll(ctx, r.agg, now); err != nil {
			slog.Warn("rabbitmq poll failed", "source", mq.src.Name, "error", err)
		}
	}
	for _, es := range r.elastics {
		if err := es.poll(ctx, r.agg, now); err != nil {
			slog.Warn("elasticsearch poll failed", "source", es.src.Name, "error", err)
		}
	}
	for _, p := range r.proms {
		if err := p.scrape(ctx, r.agg, now); err != nil {
			slog.Warn("prometheus scrape failed", "source", p.src.Name, "error", err)
		}
	}
	if r.host != nil {
		if s, ok := r.host.sample(now); ok {
			r.agg.SetHost(s)
		}
	}

	payload := r.agg.Drain(now)
	if r.host != nil {
		listening, conns := readConnections(r.host.root)
		if r.host.root != "/" {
			// Inside a container /proc/net is the container's own network: read the host's through its pid 1.
			listening, conns = readConnectionsAt(filepath.Join(r.host.root, "proc", "1", "net"), filepath.Join(r.host.root, "proc"))
		}
		payload.Network = &protocol.NetworkSnapshot{Addresses: localAddresses(), Listening: listening, Connections: conns}
	}
	payload.BatchID = newBatchID()
	payload.Agent = protocol.AgentInfo{Host: r.cfg.Host, Kind: "agent", Version: Version, OS: runtime.GOOS, Sources: r.sourceNames()}

	r.mu.Lock()
	payload.Explains, r.explains = r.explains, nil
	r.mu.Unlock()
	if r.checkedAt.IsZero() || now.Sub(r.checkedAt) >= r.checkWait {
		// A failed send keeps the payload for retry, so the checks are not lost.
		payload.Checks = r.checks(ctx, now)
		r.checkedAt, r.checkWait = now, nextCheckIn(payload.Checks)
		// What is on the machine and nobody reads: sent next to the configured sources.
		payload.Checks = append(payload.Checks, r.machineChecks(now)...)
	}

	resp, err := r.sender.Send(ctx, payload)
	if err != nil {
		slog.Warn("send failed, will retry", "error", err, "pending", r.sender.Pending())
		return
	}
	r.handleExplains(ctx, resp.Explain)
	if r.upgrade(ctx, resp.ServerVersion, resp.AgentDownload) {
		r.restart = true
	}
}

func (r *Runner) handleExplains(ctx context.Context, requests []protocol.ExplainRequest) {
	if len(r.dbs) == 0 {
		return
	}
	for _, req := range requests {
		r.mu.Lock()
		if r.busy[req.Hash] {
			r.mu.Unlock()
			continue
		}
		r.busy[req.Hash] = true
		r.mu.Unlock()

		go func(req protocol.ExplainRequest) {
			res := protocol.Explain{Hash: req.Hash}
			if sql, d, problem := r.explainTarget(req); d != nil {
				req.SQL = sql
				res = d.explain(ctx, req)
			} else {
				res.Error = problem
			}
			r.mu.Lock()
			r.explains = append(r.explains, res)
			delete(r.busy, req.Hash)
			r.mu.Unlock()
		}(req)
	}
}

// explainTarget picks the statement and the database to EXPLAIN it on: the source that
// saw it, or a source of the same engine for slow-log statements. Never the SQL chosen
// by the server, unless samples are shared on purpose.
func (r *Runner) explainTarget(req protocol.ExplainRequest) (string, *dbSource, string) {
	sql, owner, ok := r.agg.Samples.Get(req.Hash)
	if !ok {
		if !r.cfg.ShareSamples || req.SQL == "" {
			return "", nil, "no local sample of this query yet"
		}
		// A shared sample carries no origin: only unambiguous with a single database.
		if len(r.dbs) != 1 {
			return "", nil, "cannot tell which local database ran this query"
		}
		return req.SQL, r.dbs[0], ""
	}
	for _, d := range r.dbs {
		if d.id == owner.Source || owner.Source == "" && d.engine == owner.Engine {
			return sql, d, ""
		}
	}
	return "", nil, "no " + owner.Engine + " source configured on this machine to explain it"
}

func (r *Runner) sourceNames() []string {
	var names []string
	for _, s := range r.cfg.Sources {
		names = append(names, s.Type+":"+s.Name)
	}
	return names
}

func newBatchID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
