package game

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/ingest"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

type fixture struct {
	t       *testing.T
	ctx     context.Context
	db      *store.DB
	project project.Project
	ingest  *ingest.Service
	engine  *Engine
	now     time.Time
	batch   atomic.Int64
}

func newFixture(t *testing.T, mutate func(*project.Settings)) *fixture {
	t.Helper()
	db := store.OpenTest(t)
	ctx := context.Background()
	repo := project.Repo{DB: db}
	p, err := repo.Create(ctx, "Shop", "Europe/Rome")
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(&p.Settings)
		if err := repo.Update(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	f := &fixture{t: t, ctx: ctx, db: db, project: p, ingest: ingest.New(db)}
	f.engine = New(db)
	f.engine.Now = func() time.Time { return f.now }
	return f
}

func (f *fixture) send(at time.Time, payload protocol.Payload) {
	f.t.Helper()
	payload.BatchID = fmt.Sprintf("b%d", f.batch.Add(1))
	if payload.Agent.Host == "" {
		payload.Agent = protocol.AgentInfo{Host: "web-1", Kind: "agent", Sources: []string{"nginx:app", "php-fpm:www", "mysql-slowlog:mysql-slowlog"}}
	}
	restore := f.ingest.Now
	f.ingest.Now = func() time.Time { return at }
	defer func() { f.ingest.Now = restore }()
	if _, err := f.ingest.Ingest(f.ctx, f.project, payload); err != nil {
		f.t.Fatal(err)
	}
}

// traffic writes `minutes` minutes of HTTP traffic starting at `from`.
func (f *fixture) traffic(from time.Time, minutes, perMinute int, slot int, slow, e5 int) {
	f.t.Helper()
	var buckets []protocol.HTTPBucket
	for m := 0; m < minutes; m++ {
		hist := make([]int, 15)
		hist[slot] = perMinute
		buckets = append(buckets, protocol.HTTPBucket{
			Source: "nginx:app", Minute: from.Add(time.Duration(m) * time.Minute).Unix(), N: perMinute, N2: slow, E5: e5, Sum: float64(perMinute) * 0.1, Max: 3, Hist: hist,
		})
	}
	f.send(from, protocol.Payload{HTTP: buckets})
}

// query records `seconds` of a query at `at`.
func (f *fixture) query(at time.Time, sql string, seconds float64) {
	f.t.Helper()
	fp := fingerprint.Of(sql)
	f.send(at, protocol.Payload{DB: []protocol.DBBucket{{
		Source: "mysql-slowlog", Minute: at.Unix() - at.Unix()%60, SlowN: 1, SlowSum: seconds, SlowMax: seconds,
		Queries: map[string]protocol.QueryStat{fingerprint.Hash(fp): {Fingerprint: fp, Sample: sql, N: 1, Sum: seconds, Max: seconds}},
	}}})
}

func (f *fixture) evaluate(at time.Time) {
	f.t.Helper()
	f.now = at
	p, err := project.Repo{DB: f.db}.ByID(f.ctx, f.project.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.engine.EvaluateProject(f.ctx, p); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.db.Q().Row(f.ctx, query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *fixture) points() int {
	return f.count("SELECT COALESCE(SUM(points), 0) FROM point_entries WHERE project_id = ?", f.project.ID)
}
