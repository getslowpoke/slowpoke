package topology

import (
	"context"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/ingest"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func TestInfrastructureIsRebuiltFromAgents(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	svc := ingest.New(db)
	now := time.Now()
	svc.Now = func() time.Time { return now }

	send := func(host string, sources []string, n protocol.NetworkSnapshot) {
		_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: host, Agent: protocol.AgentInfo{Host: host, Kind: "agent", Sources: sources}, Network: &n})
		if err != nil {
			t.Fatal(err)
		}
	}
	send("backoffice-1", []string{"nginx:backoffice", "php-fpm:www", "host:host"}, protocol.NetworkSnapshot{
		Addresses: []string{"10.0.0.5"},
		Listening: []protocol.ListeningPort{{Port: 80, Service: "http"}},
		Connections: []protocol.Connection{
			{Direction: "out", RemoteIP: "10.0.0.9", RemotePort: 3306, Service: "mysql", Count: 12},
			{Direction: "out", RemoteIP: "10.0.0.7", RemotePort: 6379, Service: "redis", Count: 3},
			{Direction: "out", RemoteIP: "34.216.184.93", RemotePort: 443, Service: "https", Count: 2},
			{Direction: "in", RemoteIP: "203.0.113.10", LocalPort: 80, Service: "http", Count: 40},
		},
	})
	send("api-1", []string{"nginx:api", "host:host"}, protocol.NetworkSnapshot{
		Addresses:   []string{"10.0.0.6"},
		Connections: []protocol.Connection{{Direction: "out", RemoteIP: "10.0.0.9", RemotePort: 3306, Service: "mysql", Count: 30}},
	})
	send("db-1", []string{"mysql-slowlog:mysql-slowlog", "mysql:db", "host:host"}, protocol.NetworkSnapshot{
		Addresses: []string{"10.0.0.9"},
		Listening: []protocol.ListeningPort{{Port: 3306, Service: "mysql"}},
		Connections: []protocol.Connection{
			{Direction: "in", RemoteIP: "10.0.0.5", LocalPort: 3306, Service: "mysql", Count: 12},
			{Direction: "in", RemoteIP: "10.0.0.6", LocalPort: 3306, Service: "mysql", Count: 30},
		},
	})

	g, err := Build(ctx, db, p.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]Node{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	edge := func(from, to, service string) *Edge {
		for i, e := range g.Edges {
			if e.From == from && e.To == to && e.Service == service {
				return &g.Edges[i]
			}
		}
		return nil
	}

	backoffice, api, dbNode := nodes["host:backoffice-1"], nodes["host:api-1"], nodes["host:db-1"]
	if !has(backoffice.Roles, "web") || !has(backoffice.Roles, "app") || !has(dbNode.Roles, "database") || !has(api.Roles, "web") {
		t.Fatalf("roles %+v %+v %+v", backoffice.Roles, api.Roles, dbNode.Roles)
	}
	// Arrows end on the service that answers, inside the machine that runs it.
	mysqlNode := nodes["svc:db-1:3306"]
	if mysqlNode.Parent != "host:db-1" || mysqlNode.Name != "MySQL" {
		t.Fatalf("mysql service node: %+v", nodes)
	}
	if e := edge("host:backoffice-1", mysqlNode.ID, "mysql"); e == nil || e.Connections != 12 {
		t.Fatalf("backoffice -> db edge: %+v", g.Edges)
	}
	if e := edge("host:api-1", mysqlNode.ID, "mysql"); e == nil || e.Connections != 30 {
		t.Fatalf("api -> db edge: %+v", g.Edges)
	}
	// The same link seen from both sides must not be drawn twice.
	count := 0
	for _, e := range g.Edges {
		if e.To == mysqlNode.ID && e.From == "host:api-1" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate edges: %d", count)
	}
	// A private peer without agent becomes an unmonitored service; a public HTTPS peer goes into "internet".
	if n, ok := nodes["external:10.0.0.7:6379"]; !ok || n.Kind != "service" || !has(n.Roles, "cache") {
		t.Fatalf("redis node %+v", nodes)
	}
	if e := edge("host:backoffice-1", "internet:out", "https"); e == nil {
		t.Fatalf("outbound internet edge: %+v", g.Edges)
	}
	if e := edge("internet:in", "svc:backoffice-1:80", "http"); e == nil || e.Connections != 40 {
		t.Fatalf("inbound clients edge: %+v", g.Edges)
	}
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestOldLinksStayVisibleButStale(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	svc := ingest.New(db)
	then := time.Now().Add(-2 * time.Hour)
	svc.Now = func() time.Time { return then }
	_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "x", Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent"},
		Network: &protocol.NetworkSnapshot{Addresses: []string{"10.0.0.5"}, Connections: []protocol.Connection{{Direction: "out", RemoteIP: "10.0.0.9", RemotePort: 3306, Service: "mysql", Count: 3}}}})
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(ctx, db, p.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Edges) != 1 || !g.Edges[0].Stale {
		t.Fatalf("edges %+v", g.Edges)
	}
}

// The map must show the services inside each machine, not just the machines: on one address
// live nginx, PHP-FPM, MySQL and Redis, and the arrows go to the service that answers.
func TestServicesInsideMachinesAndCloudflare(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	svc := ingest.New(db)
	now := time.Now()
	svc.Now = func() time.Time { return now }

	_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "b1", Agent: protocol.AgentInfo{Host: "shared-1", Kind: "agent",
		Sources: []string{"nginx:testing", "php-fpm:testing", "mysql:main", "redis:cache", "host:host"}},
		Network: &protocol.NetworkSnapshot{
			Addresses: []string{"80.211.184.115"},
			Listening: []protocol.ListeningPort{{Port: 443, Service: "https"}, {Port: 3306, Service: "mysql"}, {Port: 6379, Service: "redis", Local: true}},
			Connections: []protocol.Connection{
				// Cloudflare in front of the site, and one visitor straight to the origin.
				{Direction: "in", RemoteIP: "172.68.19.248", LocalPort: 443, Service: "https", Count: 20},
				{Direction: "in", RemoteIP: "203.0.113.9", LocalPort: 443, Service: "https", Count: 1},
				// PHP talking to MySQL and Redis of the same machine.
				{Direction: "out", RemoteIP: "127.0.0.1", RemotePort: 3306, Service: "mysql", Count: 5},
				{Direction: "out", RemoteIP: "127.0.0.1", RemotePort: 6379, Service: "redis", Count: 4},
			}}})
	if err != nil {
		t.Fatal(err)
	}
	// The API machine uses the database of the other one.
	_, err = svc.Ingest(ctx, p, protocol.Payload{BatchID: "b2", Agent: protocol.AgentInfo{Host: "staging", Kind: "agent", Sources: []string{"nginx:api", "host:host"}},
		Network: &protocol.NetworkSnapshot{Addresses: []string{"31.14.138.168"},
			Listening:   []protocol.ListeningPort{{Port: 443, Service: "https"}},
			Connections: []protocol.Connection{{Direction: "out", RemoteIP: "80.211.184.115", RemotePort: 3306, Service: "mysql", Count: 7}}}})
	if err != nil {
		t.Fatal(err)
	}

	g, err := Build(ctx, db, p.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]Node{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	// Services live inside their machine.
	var services []Node
	for _, n := range g.Nodes {
		if n.Kind == "service" && n.Parent == "host:shared-1" {
			services = append(services, n)
		}
	}
	if len(services) != 4 {
		t.Fatalf("services of shared-1: %+v", services)
	}
	byPort := map[int]Node{}
	for _, s := range services {
		byPort[s.Port] = s
	}
	if byPort[443].Name != "nginx" || byPort[3306].Name != "MySQL" || byPort[6379].Name != "Redis" || byPort[0].Name != "PHP-FPM" {
		t.Fatalf("service names: %+v", services)
	}
	if !byPort[6379].LocalOnly || byPort[443].LocalOnly {
		t.Fatalf("local services: %+v", services)
	}

	edge := func(from, to string) *Edge {
		for i := range g.Edges {
			if g.Edges[i].From == from && g.Edges[i].To == to {
				return &g.Edges[i]
			}
		}
		return nil
	}
	// Cloudflare sits between the visitors and the site; a direct visitor still reaches it.
	if _, ok := nodes["cdn:cloudflare"]; !ok {
		t.Fatalf("no Cloudflare node: %+v", nodes)
	}
	if e := edge("internet:in", "cdn:cloudflare"); e == nil || e.Connections != 20 {
		t.Fatalf("visitors to Cloudflare: %+v", g.Edges)
	}
	if e := edge("cdn:cloudflare", byPort[443].ID); e == nil {
		t.Fatalf("Cloudflare to nginx: %+v", g.Edges)
	}
	if e := edge("internet:in", byPort[443].ID); e == nil || e.Connections != 1 {
		t.Fatalf("direct visitor: %+v", g.Edges)
	}
	// Inside the machine and between machines, arrows point at the service, not at the box.
	if e := edge("host:shared-1", byPort[3306].ID); e == nil || e.Connections != 5 {
		t.Fatalf("php to mysql: %+v", g.Edges)
	}
	if e := edge("host:shared-1", byPort[6379].ID); e == nil {
		t.Fatalf("php to redis: %+v", g.Edges)
	}
	if e := edge("host:staging", byPort[3306].ID); e == nil || e.Connections != 7 {
		t.Fatalf("api to the database of the other machine: %+v", g.Edges)
	}
}

// Names on the map can be changed by the team: "shared-1" means nothing, "DB e siti legacy" does.
func TestCustomNames(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	svc := ingest.New(db)
	now := time.Now()
	svc.Now = func() time.Time { return now }
	_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "b1", Agent: protocol.AgentInfo{Host: "shared-1", Kind: "agent", Sources: []string{"mysql:main"}},
		Network: &protocol.NetworkSnapshot{Addresses: []string{"10.0.0.1"}, Listening: []protocol.ListeningPort{{Port: 3306, Service: "mysql"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := SetLabel(ctx, db, p.ID, "host:shared-1", "Legacy box"); err != nil {
		t.Fatal(err)
	}
	if err := SetLabel(ctx, db, p.ID, "svc:shared-1:3306", "DB testing"); err != nil {
		t.Fatal(err)
	}
	g, _ := Build(ctx, db, p.ID, now)
	names := map[string]Node{}
	for _, n := range g.Nodes {
		names[n.ID] = n
	}
	if names["host:shared-1"].Name != "Legacy box" || names["host:shared-1"].DefaultName != "shared-1" {
		t.Fatalf("host: %+v", names["host:shared-1"])
	}
	if names["svc:shared-1:3306"].Name != "DB testing" || names["svc:shared-1:3306"].DefaultName != "MySQL" {
		t.Fatalf("service: %+v", names["svc:shared-1:3306"])
	}
	// An empty name goes back to the measured one.
	if err := SetLabel(ctx, db, p.ID, "host:shared-1", "  "); err != nil {
		t.Fatal(err)
	}
	g, _ = Build(ctx, db, p.ID, now)
	for _, n := range g.Nodes {
		if n.ID == "host:shared-1" && n.Name != "shared-1" {
			t.Fatalf("reset: %+v", n)
		}
	}
}

// The arrow starts from the service that opened the connection: PHP-FPM, a queue worker or
// the backup agent are not the same thing, even on the same machine.
func TestArrowsStartFromTheProcess(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	svc := ingest.New(db)
	now := time.Now()
	svc.Now = func() time.Time { return now }

	_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "p1", Agent: protocol.AgentInfo{Host: "shared-1", Kind: "agent", Sources: []string{"nginx:site", "php-fpm:www", "mysql:main"}},
		Network: &protocol.NetworkSnapshot{Addresses: []string{"10.0.0.1"},
			Listening: []protocol.ListeningPort{{Port: 443, Service: "https", Process: "nginx"}, {Port: 3306, Service: "mysql", Process: "mysql"}, {Port: 808, Process: "VVAgent"}},
			Connections: []protocol.Connection{
				{Direction: "in", RemoteIP: "203.0.113.5", LocalPort: 443, Service: "https", Process: "nginx", Count: 9},
				{Direction: "out", RemoteIP: "127.0.0.1", RemotePort: 3306, Service: "mysql", Process: "php-fpm:www", Count: 6},
				{Direction: "out", RemoteIP: "127.0.0.1", RemotePort: 3306, Service: "mysql", Process: "php artisan queue:work", Count: 2},
				{Direction: "out", RemoteIP: "34.117.59.81", RemotePort: 443, Service: "https", Process: "VVAgent", Count: 3},
			}}})
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(ctx, db, p.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]Node{}
	byName := map[string]Node{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
		byName[n.Name] = n
	}
	// A port nobody knows takes the name of the process that answers on it.
	if byName["VVAgent"].Port != 808 {
		t.Fatalf("unknown port named after its process: %+v", g.Nodes)
	}
	// A process without a port of its own (a queue worker) is still a service of the machine.
	worker := byName["php artisan queue:work"]
	if worker.Parent != "host:shared-1" || worker.Kind != "service" {
		t.Fatalf("worker node: %+v", g.Nodes)
	}
	edge := func(from, to string) *Edge {
		for i := range g.Edges {
			if g.Edges[i].From == from && g.Edges[i].To == to {
				return &g.Edges[i]
			}
		}
		return nil
	}
	php, mysql := byName["PHP-FPM"], byName["MySQL"]
	if e := edge(php.ID, mysql.ID); e == nil || e.Connections != 6 {
		t.Fatalf("php-fpm -> mysql: %+v", g.Edges)
	}
	if e := edge(worker.ID, mysql.ID); e == nil || e.Connections != 2 {
		t.Fatalf("worker -> mysql: %+v", g.Edges)
	}
	if e := edge(byName["VVAgent"].ID, "internet:out"); e == nil {
		t.Fatalf("backup agent -> internet: %+v", g.Edges)
	}
	if e := edge("internet:in", byName["nginx"].ID); e == nil || e.Connections != 9 {
		t.Fatalf("visitors -> nginx: %+v", g.Edges)
	}
	// Nothing starts from the machine itself any more when the process is known.
	for _, e := range g.Edges {
		if e.From == "host:shared-1" {
			t.Fatalf("edge from the box instead of a service: %+v", e)
		}
	}
}

// Two PHP projects on the same machine are two pools: the map must not show two boxes both
// called "PHP-FPM", or nobody can tell which project talks to which database.
func TestTwoPoolsOnOneMachineKeepTheirNames(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	svc := ingest.New(db)
	now := time.Now()
	svc.Now = func() time.Time { return now }

	_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "p1",
		Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent", Sources: []string{"nginx:shop", "php-fpm:shop", "php-fpm:admin"}},
		Network: &protocol.NetworkSnapshot{Addresses: []string{"10.0.0.1"},
			Connections: []protocol.Connection{
				{Direction: "out", RemoteIP: "10.0.0.9", RemotePort: 3306, Service: "mysql", Process: "php-fpm:shop", Count: 8},
				{Direction: "out", RemoteIP: "10.0.0.9", RemotePort: 3306, Service: "mysql", Process: "php-fpm:admin", Count: 3},
			}}})
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(ctx, db, p.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Node{}
	for _, n := range g.Nodes {
		byName[n.Name] = n
	}
	shop, admin := byName["PHP-FPM shop"], byName["PHP-FPM admin"]
	if shop.ID == "" || admin.ID == "" || shop.ID == admin.ID {
		t.Fatalf("one node per pool: %+v", g.Nodes)
	}
	counts := map[string]int{}
	for _, e := range g.Edges {
		counts[e.From] = e.Connections
	}
	// Each pool keeps its own traffic: merging them would hide which project is heavy.
	if counts[shop.ID] != 8 || counts[admin.ID] != 3 {
		t.Fatalf("per pool connections %v: %+v", counts, g.Edges)
	}
}

// The team can arrange the machines on the map and keep that arrangement: a saved place wins
// over the automatic layout, and it survives renaming the node.
func TestSavedPlacesOnTheMap(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	svc := ingest.New(db)
	now := time.Now()
	svc.Now = func() time.Time { return now }
	_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "b1", Agent: protocol.AgentInfo{Host: "db-1", Kind: "agent", Sources: []string{"mysql:main"}},
		Network: &protocol.NetworkSnapshot{Addresses: []string{"10.0.0.1"}, Listening: []protocol.ListeningPort{{Port: 3306, Service: "mysql"}}}})
	if err != nil {
		t.Fatal(err)
	}
	find := func() Node {
		t.Helper()
		g, err := Build(ctx, db, p.ID, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range g.Nodes {
			if n.ID == "host:db-1" {
				return n
			}
		}
		t.Fatal("machine missing from the map")
		return Node{}
	}
	if n := find(); n.X != nil || n.Y != nil {
		t.Fatalf("a machine nobody moved has no place of its own: %+v", n)
	}

	if err := SetPlace(ctx, db, p.ID, "host:db-1", f(640), f(220)); err != nil {
		t.Fatal(err)
	}
	if n := find(); n.X == nil || *n.X != 640 || n.Y == nil || *n.Y != 220 {
		t.Fatalf("place: %+v", n)
	}

	// Renaming keeps the place, and moving keeps the name.
	if err := SetLabel(ctx, db, p.ID, "host:db-1", "Main database"); err != nil {
		t.Fatal(err)
	}
	if n := find(); n.Name != "Main database" || n.X == nil || *n.X != 640 {
		t.Fatalf("rename lost the place: %+v", n)
	}
	if err := SetPlace(ctx, db, p.ID, "host:db-1", f(100), f(50)); err != nil {
		t.Fatal(err)
	}
	if n := find(); n.Name != "Main database" || *n.X != 100 || *n.Y != 50 {
		t.Fatalf("move lost the name: %+v", n)
	}

	// Back to the automatic layout, one node or the whole map.
	if err := SetPlace(ctx, db, p.ID, "host:db-1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := find(); n.X != nil || n.Name != "Main database" {
		t.Fatalf("reset of one node: %+v", n)
	}
	if err := SetPlace(ctx, db, p.ID, "host:db-1", f(10), f(20)); err != nil {
		t.Fatal(err)
	}
	if err := ClearPlaces(ctx, db, p.ID); err != nil {
		t.Fatal(err)
	}
	if n := find(); n.X != nil || n.Y != nil || n.Name != "Main database" {
		t.Fatalf("reset of the whole map keeps the names: %+v", n)
	}
}

func f(v float64) *float64 { return &v }

// The agent opens two ports of its own: 4318 for the traces its own packages send it, 8125 for
// StatsD. They are Slowpoke listening to itself, not a service of the machine, and on the map
// they were a box named after a protocol nobody in the team asked about. Same rule as the
// housekeeping queries: what Slowpoke does to measure is not a measure.
func TestSlowpokeOwnPortsAreNotServicesOnTheMap(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	svc := ingest.New(db)
	now := time.Now()
	svc.Now = func() time.Time { return now }

	_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "b1", Agent: protocol.AgentInfo{Host: "app-1", Kind: "agent",
		Sources: []string{"nginx:shop", "otlp:app", "statsd:app", "host:host"}},
		Network: &protocol.NetworkSnapshot{
			Addresses: []string{"10.0.0.5"},
			Listening: []protocol.ListeningPort{
				{Port: 443, Service: "https"},
				{Port: 4318, Local: true, Process: "slowpoke-agent"},
				{Port: 8125, Local: true, Process: "slowpoke-agent"},
			},
		}})
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(ctx, db, p.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if n.Port == 4318 || n.Port == 8125 {
			t.Fatalf("the agent's own port is on the map as %q", n.Name)
		}
	}
}
