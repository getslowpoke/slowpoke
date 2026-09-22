package demo

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/ingest"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// liveDay is a day after every fix of the story: the remaining debt, without the regressions.
const liveDay = 1000

// LiveMinute builds what the demo agents send for one minute, in the final state of the story.
func LiveMinute(p project.Project, t time.Time, rng *rand.Rand) []protocol.Payload {
	intensity := load(t.In(p.Location()))
	byName := minute(t, p.Location(), liveDay, never, rng)
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	conns := map[string]machine{}
	for _, m := range demoMachines() {
		conns[m.name] = m
	}
	out := make([]protocol.Payload, 0, len(names))
	for _, name := range names {
		pl := byName[name]
		m := conns[name]
		pl.BatchID = fmt.Sprintf("live-%d-%s-%d", p.ID, name, t.Unix()/60*60)
		pl.Network = &protocol.NetworkSnapshot{Addresses: []string{m.ip}, Listening: m.listen, Connections: jitter(m.conns, intensity, rng)}
		out = append(out, *pl)
	}
	return out
}

func jitter(conns []protocol.Connection, intensity float64, rng *rand.Rand) []protocol.Connection {
	out := make([]protocol.Connection, len(conns))
	for i, c := range conns {
		c.Count = max(1, int(float64(c.Count)*(0.4+intensity)*(0.8+rng.Float64()*0.4)))
		out[i] = c
	}
	return out
}

// RunLive keeps the demo project alive: one minute of data every minute, as real agents would.
func RunLive(ctx context.Context, db *store.DB, slug string) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	ing := ingest.New(db)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		p, err := project.Repo{DB: db}.BySlug(ctx, slug)
		if err == nil {
			// Fill the gap since the last run (at most one hour), then keep up.
			var last int64
			_ = db.Q().Row(ctx, "SELECT COALESCE(MAX(bucket), 0) FROM http_minutes WHERE project_id = ?", p.ID).Scan(&last)
			from := time.Unix(last+60, 0)
			if limit := time.Now().Add(-time.Hour); from.Before(limit) {
				from = limit
			}
			for t := from.Truncate(time.Minute); t.Before(time.Now().Truncate(time.Minute)); t = t.Add(time.Minute) {
				for _, payload := range LiveMinute(p, t, rng) {
					ing.Now = time.Now
					if _, err := ing.Ingest(ctx, p, payload); err != nil {
						slog.Warn("demo live: ingest failed", "error", err)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
