package integrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/ingest"
	"github.com/getslowpoke/slowpoke/internal/server/integrations"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func TestStatusOfASource(t *testing.T) {
	ok := protocol.CheckItem{Key: "connection", Status: "ok"}
	cases := []struct {
		items []protocol.CheckItem
		want  string
	}{
		{[]protocol.CheckItem{ok, {Key: "statistics", Status: "ok", Optional: true}}, integrations.Complete},
		{[]protocol.CheckItem{ok, {Key: "plans", Status: "off", Optional: true}}, integrations.Partial},
		{[]protocol.CheckItem{ok, {Key: "statistics", Status: "error", Optional: true}}, integrations.Problem},
		{[]protocol.CheckItem{{Key: "readable", Status: "ok"}, {Key: "duration", Status: "waiting"}}, integrations.Waiting},
		{[]protocol.CheckItem{{Key: "connection", Status: "error"}}, integrations.Problem},
	}
	for i, c := range cases {
		if got := integrations.Status(c.items); got != c.want {
			t.Errorf("case %d: got %s want %s", i, got, c.want)
		}
	}
}

// The staging machines this was written against: the web server with everything working, the database host
// with statistics but no plans. Checks replace the previous ones of the same machine, and
// anything that looks like a secret never reaches the database.
func TestChecksAreStoredPerMachineAndSummarized(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, err := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	svc := ingest.New(db)
	now := time.Now()
	svc.Now = func() time.Time { return now }

	send := func(batch, host string, checks []protocol.SourceCheck) {
		t.Helper()
		if _, err := svc.Write(ctx, p, protocol.Payload{BatchID: batch, Agent: protocol.AgentInfo{Host: host, Kind: "agent"}, Checks: checks}); err != nil {
			t.Fatal(err)
		}
	}
	send("w1", "web-1", []protocol.SourceCheck{
		{Source: "nginx:shop", Items: []protocol.CheckItem{{Key: "readable", Status: "ok"}, {Key: "duration", Status: "ok"}}, Facts: map[string]string{"path": "/var/log/nginx/shop.log"}},
		{Source: "php-fpm:www", Items: []protocol.CheckItem{{Key: "readable", Status: "ok"}}},
	})
	send("d1", "db-1", []protocol.SourceCheck{
		{Source: "mysql:main", Items: []protocol.CheckItem{
			{Key: "connection", Status: "ok"}, {Key: "statistics", Status: "ok", Optional: true},
			{Key: "slow_log", Status: "off", Optional: true, Reason: "slow_log_off"},
			{Key: "plans", Status: "off", Optional: true, Reason: "no_select", Detail: "shop"},
		}, Facts: map[string]string{"user": "slowpoke@127.0.0.1", "schemas": "shop", "password": "leak", "api_token": "leak"}},
	})

	byHost, err := integrations.ForProject(ctx, db, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var web, dbHost []integrations.Check
	for id, checks := range byHost {
		if len(checks) == 2 {
			web = checks
		} else {
			dbHost = checks
		}
		_ = id
	}
	if s := integrations.Summarize(web); s.Total != 2 || s.Complete != 2 {
		t.Fatalf("web summary %+v", s)
	}
	if len(dbHost) != 1 || dbHost[0].Type != "mysql" || dbHost[0].Name != "main" || dbHost[0].Status != integrations.Partial {
		t.Fatalf("db checks %+v", dbHost)
	}
	if _, leaked := dbHost[0].Facts["password"]; leaked || dbHost[0].Facts["api_token"] != "" || dbHost[0].Facts["user"] != "slowpoke@127.0.0.1" {
		t.Fatalf("facts %+v", dbHost[0].Facts)
	}
	if s := integrations.Summarize(dbHost); s.Partial != 1 || s.Total != 1 {
		t.Fatalf("db summary %+v", s)
	}

	// The web machine drops PHP-FPM from its configuration: its check disappears.
	send("w2", "web-1", []protocol.SourceCheck{{Source: "nginx:shop", Items: []protocol.CheckItem{{Key: "readable", Status: "ok"}}}})
	byHost, _ = integrations.ForProject(ctx, db, p.ID)
	total := 0
	for _, checks := range byHost {
		total += len(checks)
	}
	if total != 2 {
		t.Fatalf("stale checks kept: %+v", byHost)
	}

	// A payload without checks (most of them) leaves the last ones alone.
	send("w3", "web-1", nil)
	byHost, _ = integrations.ForProject(ctx, db, p.ID)
	total = 0
	for _, checks := range byHost {
		total += len(checks)
	}
	if total != 2 {
		t.Fatalf("checks lost on a payload without them: %+v", byHost)
	}
}
