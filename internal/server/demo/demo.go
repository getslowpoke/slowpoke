// Package demo generates a realistic project history: useful to try Slowpoke
// in one minute, for screenshots, and as an end-to-end test of the pipeline.
package demo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/exclude"
	"github.com/getslowpoke/slowpoke/internal/server/game"
	"github.com/getslowpoke/slowpoke/internal/server/ingest"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

type Options struct {
	Name string
	Days int
	Now  time.Time
}

// deployAt is the time of day of every deploy of the story: early afternoon, when the traffic
// is flat, so the hour before and the hour after a deploy compare the deploy and not the crowd.
const deployAt = 14 * time.Hour

// traffic shape: quiet at night, peak in the afternoon, lower on weekends.
func load(t time.Time) float64 {
	h := float64(t.Hour()) + float64(t.Minute())/60
	daily := 0.15 + 0.85*math.Max(0, math.Sin((h-6)/16*math.Pi))
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		daily *= 0.55
	}
	return daily
}

func Seed(ctx context.Context, db *store.DB, o Options) (project.Project, error) {
	if o.Days <= 0 {
		o.Days = 30
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Name == "" {
		o.Name = "Demo Shop"
	}
	repo := project.Repo{DB: db}
	if existing, err := repo.BySlug(ctx, project.Slugify(o.Name)); err == nil {
		if err := repo.Delete(ctx, existing.ID); err != nil {
			return project.Project{}, err
		}
	} else if !errors.Is(err, project.ErrNotFound) {
		return project.Project{}, err
	}
	p, err := repo.Create(ctx, o.Name, "Europe/Rome")
	if err != nil {
		return p, err
	}
	p.Settings.Missions.WindowHours = 24
	p.Settings.Missions.GoodWindows = 2
	p.Settings.People.Leaderboard = true
	p.Settings.Score.Components = append(p.Settings.Score.Components, project.Component{
		Key: "invoice_pdf", Label: "Invoice PDF time", Metric: "custom_p95", Name: "invoices.pdf", Weight: 10, Good: 1, Bad: 8,
	})
	if err := repo.Update(ctx, p); err != nil {
		return p, err
	}

	rng := rand.New(rand.NewSource(42))
	ing := ingest.New(db)
	engine := game.New(db)
	start := o.Now.In(p.Location()).AddDate(0, 0, -o.Days)
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	lastDay := o.Days - 1

	// The notification bell's long polling is slow by design: the team left it out of the score.
	if _, err := exclude.Add(ctx, db, p.ID, exclude.Exclusion{Kind: exclude.KindEndpoint, Target: "GET /notifications/stream", Reason: "streaming",
		Note: "Long polling for the notification bell: it stays open 25 s on purpose", CreatedBy: "grace"}, start.Add(12*24*time.Hour)); err != nil {
		return p, err
	}

	// One payload per machine per hour: what an hour of agents sends, in a fraction of the calls.
	batch := 0
	hour := map[string]*protocol.Payload{}
	flush := func(end time.Time) error {
		names := make([]string, 0, len(hour))
		for name := range hour {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			batch++
			pl := hour[name]
			pl.BatchID = fmt.Sprintf("demo-%d-%d", p.ID, batch)
			ing.Now = func() time.Time { return end }
			if _, err := ing.Ingest(ctx, p, *pl); err != nil {
				return err
			}
		}
		hour = map[string]*protocol.Payload{}
		evalAt := end.Add(time.Minute)
		engine.Now = func() time.Time { return evalAt }
		return engine.EvaluateProject(ctx, p)
	}
	for t := start; t.Before(o.Now.Truncate(time.Minute)); t = t.Add(time.Minute) {
		// A day of the story starts at the time of that day's deploy: a fix or a regression lands
		// when it is deployed, and the verdict of the deploy measures exactly that.
		day := 0
		if since := t.Sub(start.Add(deployAt)); since >= 0 {
			day = int(since.Hours()/24) + 1
		}
		for name, pl := range minute(t, p.Location(), day, lastDay, rng) {
			merge(hour, name, pl)
		}
		lt := t.In(p.Location())
		if lt.Hour()*60+lt.Minute() == int(deployAt/time.Minute) {
			if d, ok := deploys[day]; ok {
				if _, err := db.Q().Exec(ctx, "INSERT INTO deploys (project_id, version, commit_sha, author, message, deployed_at) VALUES (?, ?, ?, ?, ?, ?)",
					p.ID, d.version, fmt.Sprintf("%040x", rng.Int63()), d.author, d.message, t.Unix()); err != nil {
					return p, err
				}
			}
		}
		if next := t.Add(time.Minute); next.Minute() == 0 || !next.Before(o.Now.Truncate(time.Minute)) {
			if err := flush(next); err != nil {
				return p, err
			}
		}
	}

	// The agent answered the EXPLAIN requests.
	for table, plan := range explains {
		if _, err := db.Q().Exec(ctx, "UPDATE query_fingerprints SET explain = ?, explained_at = ? WHERE project_id = ? AND (fingerprint LIKE ? OR fingerprint LIKE ?)",
			plan, o.Now.Unix(), p.ID, "%from "+table+" %", "%from "+table); err != nil {
			return p, err
		}
	}

	// The team at work: the costliest open mission taken yesterday by linus, the next one taken
	// by margaret whose fix went online this morning and is being verified.
	var open []int64
	if rows, err := db.Q().Query(ctx, `SELECT id FROM missions WHERE project_id = ? AND type = 'mission' AND status = 'open' ORDER BY baseline DESC LIMIT 2`, p.ID); err == nil {
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				open = append(open, id)
			}
		}
		rows.Close()
	}
	if len(open) > 0 {
		if err := game.Claim(ctx, db, p.ID, open[0], "linus", false, o.Now.Add(-26*time.Hour)); err != nil {
			return p, err
		}
	}
	if len(open) > 1 {
		if err := game.Claim(ctx, db, p.ID, open[1], "margaret", false, o.Now.Add(-50*time.Hour)); err != nil {
			return p, err
		}
		if err := game.MarkShipped(ctx, db, p.ID, open[1], "margaret", o.Now.Add(-5*time.Hour)); err != nil {
			return p, err
		}
	}

	if err := seedInfrastructure(ctx, ing, p, o.Now); err != nil {
		return p, err
	}

	engine.Now = func() time.Time { return o.Now }
	if err := engine.EvaluateProject(ctx, p); err != nil {
		return p, err
	}
	// Past activities are history: do not flood freshly configured channels.
	_, err = db.Q().Exec(ctx, "UPDATE activities SET notified = TRUE WHERE project_id = ?", p.ID)
	return p, err
}

// merge adds one minute of a machine to its hour.
func merge(hour map[string]*protocol.Payload, name string, pl *protocol.Payload) {
	h := hour[name]
	if h == nil {
		cp := *pl
		hour[name] = &cp
		return
	}
	h.HTTP = append(h.HTTP, pl.HTTP...)
	h.App = append(h.App, pl.App...)
	h.DB = append(h.DB, pl.DB...)
	h.DBStatus = append(h.DBStatus, pl.DBStatus...)
	h.PHP = append(h.PHP, pl.PHP...)
	h.Metrics = append(h.Metrics, pl.Metrics...)
	h.Jobs = append(h.Jobs, pl.Jobs...)
	h.Commands = append(h.Commands, pl.Commands...)
	h.Host = append(h.Host, pl.Host...)
}

func addRequest(b *protocol.HTTPBucket, endpoint string, d float64, status int) {
	b.N++
	b.Sum += d
	b.Max = math.Max(b.Max, d)
	b.Hist[protocol.HistogramIndex(d)]++
	if d > 1 {
		b.N1++
	}
	if d > 2 {
		b.N2++
	}
	if d > 5 {
		b.N5++
	}
	if status >= 500 {
		b.E5++
	}
	e := b.Endpoints[endpoint]
	if e.Hist == nil {
		e.Hist = make([]int, len(protocol.HistogramEdges)+1)
	}
	e.Hist[protocol.HistogramIndex(d)]++
	e.N++
	e.Sum += d
	e.Max = math.Max(e.Max, d)
	if d >= 1 {
		e.Slow++
		e.SlowSum += d
	}
	if status >= 500 {
		e.E5++
	}
	b.Endpoints[endpoint] = e
}

// seedInfrastructure is the last snapshot of the connections each machine has: what draws the
// infrastructure map.
func seedInfrastructure(ctx context.Context, ing *ingest.Service, p project.Project, now time.Time) error {
	for i, m := range demoMachines() {
		payload := protocol.Payload{
			BatchID: fmt.Sprintf("demo-infra-%d-%d", p.ID, i),
			Agent:   protocol.AgentInfo{Host: m.name, Kind: "agent", Version: "0.1.0", OS: "linux", Sources: m.sources},
			Network: &protocol.NetworkSnapshot{Addresses: []string{m.ip}, Listening: m.listen, Connections: m.conns},
		}
		ing.Now = func() time.Time { return now }
		if _, err := ing.Ingest(ctx, p, payload); err != nil {
			return err
		}
	}
	return nil
}

type machine struct {
	name, ip string
	sources  []string
	cpus     int
	cpu, mem float64
	listen   []protocol.ListeningPort
	conns    []protocol.Connection
	procs    []protocol.Process
}

func demoMachines() []machine {
	web := func(name, ip string, cpu float64) machine {
		return machine{
			name: name, ip: ip, cpus: 4, cpu: cpu, mem: 0.6, sources: []string{"nginx:shop", "php-fpm:shop", "host:host", "statsd:statsd"},
			listen: []protocol.ListeningPort{{Port: 80, Service: "http"}, {Port: 443, Service: "https"}},
			conns: []protocol.Connection{
				{Direction: "in", RemoteIP: "203.0.113.24", LocalPort: 443, Service: "https", Count: 184},
				{Direction: "out", RemoteIP: "10.0.0.9", RemotePort: 3306, Service: "mysql", Count: 24},
				{Direction: "out", RemoteIP: "10.0.0.12", RemotePort: 6379, Service: "redis", Count: 16},
				{Direction: "out", RemoteIP: "10.0.0.14", RemotePort: 9200, Service: "elasticsearch", Count: 6},
				{Direction: "out", RemoteIP: "34.216.184.93", RemotePort: 443, Service: "https", Count: 9},
			},
			procs: []protocol.Process{{Name: "php-fpm8.3", CPU: 142, RSS: 3 << 30, N: 24}, {Name: "nginx", CPU: 9, RSS: 180 << 20, N: 5}, {Name: "slowpoke-agent", CPU: 0.4, RSS: 22 << 20, N: 1}},
		}
	}
	return []machine{
		web("web-1", "10.0.0.5", 38),
		web("web-2", "10.0.0.7", 34),
		{
			name: "api-1", ip: "10.0.0.6", cpus: 2, cpu: 22, mem: 0.48, sources: []string{"nginx:api", "php-fpm:api", "host:host"},
			listen: []protocol.ListeningPort{{Port: 443, Service: "https"}},
			conns: []protocol.Connection{
				{Direction: "in", RemoteIP: "198.51.100.7", LocalPort: 443, Service: "https", Count: 96},
				{Direction: "out", RemoteIP: "10.0.0.9", RemotePort: 3306, Service: "mysql", Count: 40},
				{Direction: "out", RemoteIP: "10.0.0.10", RemotePort: 3306, Service: "mysql", Count: 18},
				{Direction: "out", RemoteIP: "10.0.0.12", RemotePort: 6379, Service: "redis", Count: 8},
				{Direction: "out", RemoteIP: "34.216.184.93", RemotePort: 443, Service: "https", Count: 4},
			},
			procs: []protocol.Process{{Name: "php-fpm8.3", CPU: 61, RSS: 1400 << 20, N: 12}, {Name: "nginx", CPU: 4, RSS: 90 << 20, N: 3}},
		},
		{
			name: "worker-1", ip: "10.0.0.8", cpus: 2, cpu: 30, mem: 0.55, sources: []string{"host:host", "statsd:statsd"},
			conns: []protocol.Connection{
				{Direction: "out", RemoteIP: "10.0.0.9", RemotePort: 3306, Service: "mysql", Count: 10},
				{Direction: "out", RemoteIP: "10.0.0.12", RemotePort: 6379, Service: "redis", Count: 22},
				{Direction: "out", RemoteIP: "10.0.0.14", RemotePort: 9200, Service: "elasticsearch", Count: 4},
				{Direction: "out", RemoteIP: "52.95.110.1", RemotePort: 443, Service: "https", Count: 7},
			},
			procs: []protocol.Process{{Name: "php", CPU: 88, RSS: 900 << 20, N: 6}, {Name: "supervisord", CPU: 0.2, RSS: 30 << 20, N: 1}},
		},
		{
			name: "db-1", ip: "10.0.0.9", cpus: 8, cpu: 55, mem: 0.81, sources: []string{"mysql-slowlog:mysql-slowlog", "mysql:db", "host:host"},
			listen: []protocol.ListeningPort{{Port: 3306, Service: "mysql"}},
			conns: []protocol.Connection{
				{Direction: "in", RemoteIP: "10.0.0.5", LocalPort: 3306, Service: "mysql", Count: 24},
				{Direction: "in", RemoteIP: "10.0.0.7", LocalPort: 3306, Service: "mysql", Count: 22},
				{Direction: "in", RemoteIP: "10.0.0.6", LocalPort: 3306, Service: "mysql", Count: 40},
				{Direction: "in", RemoteIP: "10.0.0.8", LocalPort: 3306, Service: "mysql", Count: 10},
				{Direction: "in", RemoteIP: "10.0.0.10", LocalPort: 3306, Service: "mysql", Count: 1},
			},
			procs: []protocol.Process{{Name: "mysqld", CPU: 310, RSS: 22 << 30, N: 1}, {Name: "slowpoke-agent", CPU: 0.6, RSS: 25 << 20, N: 1}},
		},
		{
			name: "db-2", ip: "10.0.0.10", cpus: 4, cpu: 18, mem: 0.7, sources: []string{"mysql:db", "host:host"},
			listen: []protocol.ListeningPort{{Port: 3306, Service: "mysql"}},
			conns: []protocol.Connection{
				{Direction: "out", RemoteIP: "10.0.0.9", RemotePort: 3306, Service: "mysql", Count: 1},
				{Direction: "in", RemoteIP: "10.0.0.6", LocalPort: 3306, Service: "mysql", Count: 18},
			},
			procs: []protocol.Process{{Name: "mysqld", CPU: 70, RSS: 9 << 30, N: 1}},
		},
	}
}

func (m machine) sample(t time.Time, rng *rand.Rand) protocol.HostSample {
	memTotal := uint64(8<<30) * uint64(m.cpus) / 2
	wave := load(t)
	return protocol.HostSample{
		Minute: t.Unix() / 60 * 60, CPUs: m.cpus, CPU: m.cpu*(0.5+wave*0.7) + rng.Float64()*8 - 4, CPUIOWait: rng.Float64() * 3, CPUSteal: rng.Float64(),
		Load1: float64(m.cpus) * m.cpu / 100 * (0.5 + wave), MemTotal: memTotal, MemUsed: uint64(float64(memTotal) * (m.mem + rng.Float64()*0.03)),
		SwapTotal: 2 << 30, SwapUsed: uint64(rng.Intn(200)) << 20, DiskTotal: 200 << 30, DiskUsed: 91 << 30,
		NetRxBps: (200000 + rng.Float64()*300000) * (0.4 + wave), NetTxBps: (500000 + rng.Float64()*500000) * (0.4 + wave),
		DiskReadBps: rng.Float64() * 4e6, DiskWriteBps: rng.Float64() * 9e6, TopProcesses: m.procs,
	}
}
