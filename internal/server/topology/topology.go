// Package topology rebuilds the infrastructure map from what every agent sees:
// its addresses, listening ports and established TCP connections.
package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

type Node struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"` // host | service | internet | cdn
	Name       string   `json:"name"`
	Roles      []string `json:"roles"`
	HostID     int64    `json:"host_id,omitempty"`
	Online     bool     `json:"online"`
	Addresses  []string `json:"addresses,omitempty"`
	Sources    []string `json:"sources,omitempty"`
	Listening  []int    `json:"listening,omitempty"`
	CPU        *float64 `json:"cpu,omitempty"`
	MemPercent *float64 `json:"mem_percent,omitempty"`
	CPUs       *int64   `json:"cpus,omitempty"`
	Monitored  bool     `json:"monitored"`
	// X and Y are where the team put the node on the map; nil means the automatic layout.
	X *float64 `json:"x,omitempty"`
	Y *float64 `json:"y,omitempty"`
	// Services of a machine carry the id of their host and what they answer on.
	// DefaultName is what Slowpoke measured, when the team renamed the node.
	DefaultName string `json:"default_name,omitempty"`
	Parent      string `json:"parent,omitempty"`
	Port        int    `json:"port,omitempty"`
	Ports       []int  `json:"ports,omitempty"`
	Service     string `json:"service,omitempty"`
	Process     string `json:"process,omitempty"`
	LocalOnly   bool   `json:"local_only,omitempty"`
}

type Edge struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Service     string `json:"service"`
	Port        int    `json:"port"`
	Connections int    `json:"connections"`
	// Stale: the last snapshot of the reporting agent is older than staleAfter (agent stopped or unreachable).
	Stale bool `json:"stale"`
}

type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

var serviceRole = map[string]string{
	"mysql": "database", "postgres": "database", "mongodb": "database", "redis": "cache", "memcached": "cache",
	"elasticsearch": "search", "meilisearch": "search", "typesense": "search", "rabbitmq": "queue", "kafka": "queue", "nats": "queue",
	"http": "web", "https": "web", "php-fpm": "app", "websocket": "realtime", "smtp": "mail",
}

func rolesFromSources(sources []string, listening []protocol.ListeningPort) []string {
	set := map[string]bool{}
	for _, s := range sources {
		switch {
		case strings.HasPrefix(s, "nginx:"), strings.HasPrefix(s, "apache:"), strings.HasPrefix(s, "caddy:"):
			set["web"] = true
		case strings.HasPrefix(s, "php-fpm:"):
			set["app"] = true
		case strings.HasPrefix(s, "mysql"), strings.HasPrefix(s, "postgres"):
			set["database"] = true
		case strings.HasPrefix(s, "statsd:"), strings.HasPrefix(s, "prometheus:"):
			set["metrics"] = true
		}
	}
	for _, l := range listening {
		if l.Local {
			continue
		}
		if r := serviceRole[l.Service]; r != "" {
			set[r] = true
		}
	}
	return sortedKeys(set)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

const staleAfter = 15 * time.Minute

// portKey is one listening port of one machine.
type portKey struct {
	host string
	port int
}

func Build(ctx context.Context, db *store.DB, projectID int64, now time.Time) (Graph, error) {
	g := Graph{Nodes: []Node{}, Edges: []Edge{}}
	rows, err := db.Q().Query(ctx, `SELECT h.id, h.name, h.kind, h.sources, h.addresses, h.listening, h.last_seen_at,
		(SELECT cpu FROM host_minutes m WHERE m.host_id = h.id ORDER BY bucket DESC LIMIT 1),
		(SELECT CASE WHEN mem_total > 0 THEN mem_used * 100.0 / mem_total END FROM host_minutes m WHERE m.host_id = h.id ORDER BY bucket DESC LIMIT 1),
		(SELECT cpus FROM host_minutes m WHERE m.host_id = h.id ORDER BY bucket DESC LIMIT 1)
		FROM hosts h WHERE h.project_id = ? AND h.kind = 'agent' ORDER BY h.name`, projectID)
	if err != nil {
		return g, err
	}
	byIP := map[string]string{}
	hostIDs := map[int64]string{}
	servicesByPort := map[portKey]string{}
	// Which service of a machine answers to the name of a process ("php-fpm:www", "nginx").
	servicesByProcess := map[string]string{}
	indexProcess := func(host, name, id string) {
		if name != "" {
			servicesByProcess[host+"|"+strings.ToLower(name)] = id
		}
	}
	for rows.Next() {
		var id, lastSeen int64
		var name, kind, sources, addresses, listening string
		var cpu, mem *float64
		var cpus *int64
		if err := rows.Scan(&id, &name, &kind, &sources, &addresses, &listening, &lastSeen, &cpu, &mem, &cpus); err != nil {
			rows.Close()
			return g, err
		}
		n := Node{ID: "host:" + name, Kind: "host", Name: name, HostID: id, Monitored: true, CPU: cpu, MemPercent: mem, CPUs: cpus,
			Online: now.Sub(time.Unix(lastSeen, 0)) < 3*time.Minute}
		var lp []protocol.ListeningPort
		_ = json.Unmarshal([]byte(sources), &n.Sources)
		_ = json.Unmarshal([]byte(addresses), &n.Addresses)
		_ = json.Unmarshal([]byte(listening), &lp)
		for _, l := range lp {
			if !l.Local {
				n.Listening = append(n.Listening, l.Port)
			}
		}
		n.Roles = rolesFromSources(n.Sources, lp)
		for _, a := range n.Addresses {
			byIP[a] = n.ID
		}
		hostIDs[id] = n.ID
		g.Nodes = append(g.Nodes, n)
		for _, svc := range services(n, lp) {
			g.Nodes = append(g.Nodes, svc)
			for _, port := range svc.Ports {
				servicesByPort[portKey{n.ID, port}] = svc.ID
			}
			indexProcess(n.ID, svc.Process, svc.ID)
			indexProcess(n.ID, svc.Name, svc.ID)
			if svc.Service == "php-fpm" {
				indexProcess(n.ID, "php-fpm:"+strings.TrimPrefix(svc.ID, "svc:"+n.Name+":php-fpm:"), svc.ID)
			}
		}
	}
	rows.Close()

	// The last known links are always shown: a stopped agent must not erase the map.
	rows, err = db.Q().Query(ctx, `SELECT c.host_id, c.direction, c.remote_ip, c.port, c.service, c.process, c.count, COALESCE(h.network_at, 0) FROM host_connections c
		JOIN hosts h ON h.id = c.host_id WHERE h.project_id = ?`, projectID)
	if err != nil {
		return g, err
	}
	type edgeKey struct {
		from, to, service string
	}
	edges := map[edgeKey]*Edge{}
	extra := map[string]*Node{}
	var stale bool
	addEdge := func(from, to, service string, port, count int) {
		k := edgeKey{from, to, service}
		if e := edges[k]; e != nil {
			// Between two machines both ends report the same link: keep the largest view. From
			// the internet, every address is a different visitor: those add up.
			if strings.HasPrefix(from, "internet:") || strings.HasPrefix(from, "cdn:") {
				e.Connections += count
			} else {
				e.Connections = max(e.Connections, count)
			}
			e.Stale = e.Stale && stale
			return
		}
		edges[k] = &Edge{From: from, To: to, Service: service, Port: port, Connections: count, Stale: stale}
	}
	for rows.Next() {
		var hostID int64
		var dir, ip, service, process string
		var port, count int
		var networkAt int64
		if err := rows.Scan(&hostID, &dir, &ip, &port, &service, &process, &count, &networkAt); err != nil {
			rows.Close()
			return g, err
		}
		stale = now.Sub(time.Unix(networkAt, 0)) > staleAfter
		self := hostIDs[hostID]
		if self == "" {
			continue
		}
		if service == "" {
			service = fmt.Sprintf("tcp/%d", port)
		}
		peer, monitored := byIP[ip]
		parsed := net.ParseIP(ip)
		public := parsed != nil && !parsed.IsPrivate() && !parsed.IsLoopback()
		if parsed != nil && parsed.IsLoopback() {
			// Two services of this same machine: the target is one of its own ports.
			peer, monitored = self, true
		}
		// The arrow points at the service that answers, when the machine reports that port.
		target := func(host string) string {
			if id := servicesByPort[portKey{host, port}]; id != "" {
				return id
			}
			return host
		}
		// ...and starts from the service that opened it, when the agent could read the process.
		source := func(host string) string {
			if process == "" {
				return host
			}
			if id := servicesByProcess[host+"|"+strings.ToLower(process)]; id != "" {
				return id
			}
			hostName := strings.TrimPrefix(host, "host:")
			id := "svc:" + hostName + ":proc:" + strings.ToLower(process)
			extra[id] = &Node{ID: id, Kind: "service", Parent: host, HostID: hostID, Name: processLabel(process),
				Process: process, Roles: []string{"app"}, LocalOnly: true, Online: true, Monitored: true}
			servicesByProcess[host+"|"+strings.ToLower(process)] = id
			return id
		}

		switch {
		case monitored && dir == "out":
			addEdge(source(self), target(peer), service, port, count)
		case monitored && dir == "in":
			addEdge(peer, target(self), service, port, count)
		case public && dir == "out":
			ensure(extra, "internet:out", "internet", "Internet · APIs", []string{"external"})
			addEdge(source(self), "internet:out", service, port, count)
		case public && dir == "in":
			ensure(extra, "internet:in", "internet", "Users · Internet", []string{"clients"})
			if fromCloudflare(ip) {
				// Visitors reach Cloudflare, Cloudflare reaches the origin.
				ensure(extra, "cdn:cloudflare", "cdn", "Cloudflare", []string{"cdn"})
				addEdge("internet:in", "cdn:cloudflare", service, port, count)
				addEdge("cdn:cloudflare", target(self), service, port, count)
				break
			}
			addEdge("internet:in", target(self), service, port, count)
		case dir == "out":
			id := fmt.Sprintf("external:%s:%d", ip, port)
			role := serviceRole[service]
			if role == "" {
				role = "service"
			}
			ensure(extra, id, "service", fmt.Sprintf("%s · %s", service, ip), []string{role})
			// Where it is and what it answers, so the panel can offer to measure it too.
			extra[id].Addresses, extra[id].Service, extra[id].Port = []string{ip}, service, port
			addEdge(source(self), id, service, port, count)
		default:
			id := "clients:" + ip
			ensure(extra, id, "service", "clients · "+ip, []string{"clients"})
			addEdge(id, self, service, port, count)
		}
	}
	rows.Close()

	for _, n := range extra {
		g.Nodes = append(g.Nodes, *n)
	}
	for _, e := range edges {
		g.Edges = append(g.Edges, *e)
	}
	if err := applyLabels(ctx, db, projectID, g.Nodes); err != nil {
		return g, err
	}
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		return g.Edges[i].To < g.Edges[j].To
	})
	return g, nil
}

func ensure(m map[string]*Node, id, kind, name string, roles []string) {
	if m[id] == nil {
		m[id] = &Node{ID: id, Kind: kind, Name: name, Roles: roles, Online: true}
	}
}

// SetLabel renames a machine or a service on the map. An empty name goes back to the measured one,
// without forgetting where the team put the node.
func SetLabel(ctx context.Context, db *store.DB, projectID int64, nodeID, label string) error {
	nodeID = strings.TrimSpace(nodeID)
	label = strings.TrimSpace(label)
	if nodeID == "" || len(nodeID) > 191 {
		return fmt.Errorf("node id is required")
	}
	if len(label) > 60 {
		label = label[:60]
	}
	_, err := db.Q().Exec(ctx, `INSERT INTO node_labels (project_id, node_id, label, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (project_id, node_id) DO UPDATE SET label = excluded.label, updated_at = excluded.updated_at`,
		projectID, nodeID, label, time.Now().Unix())
	if err != nil {
		return err
	}
	return cleanup(ctx, db, projectID, nodeID)
}

// SetPlace remembers where the team dragged a node. Nil coordinates go back to the automatic layout.
func SetPlace(ctx context.Context, db *store.DB, projectID int64, nodeID string, x, y *float64) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || len(nodeID) > 191 {
		return fmt.Errorf("node id is required")
	}
	if x == nil || y == nil {
		x, y = nil, nil
	}
	_, err := db.Q().Exec(ctx, `INSERT INTO node_labels (project_id, node_id, label, x, y, updated_at) VALUES (?, ?, '', ?, ?, ?)
		ON CONFLICT (project_id, node_id) DO UPDATE SET x = excluded.x, y = excluded.y, updated_at = excluded.updated_at`,
		projectID, nodeID, x, y, time.Now().Unix())
	if err != nil {
		return err
	}
	return cleanup(ctx, db, projectID, nodeID)
}

// ClearPlaces puts the whole map back to the automatic layout, keeping the names.
func ClearPlaces(ctx context.Context, db *store.DB, projectID int64) error {
	if _, err := db.Q().Exec(ctx, "UPDATE node_labels SET x = NULL, y = NULL WHERE project_id = ?", projectID); err != nil {
		return err
	}
	_, err := db.Q().Exec(ctx, "DELETE FROM node_labels WHERE project_id = ? AND label = '' AND x IS NULL", projectID)
	return err
}

// cleanup drops a row that says nothing any more: no name and no place.
func cleanup(ctx context.Context, db *store.DB, projectID int64, nodeID string) error {
	_, err := db.Q().Exec(ctx, "DELETE FROM node_labels WHERE project_id = ? AND node_id = ? AND label = '' AND x IS NULL", projectID, nodeID)
	return err
}

func applyLabels(ctx context.Context, db *store.DB, projectID int64, nodes []Node) error {
	rows, err := db.Q().Query(ctx, "SELECT node_id, label, x, y FROM node_labels WHERE project_id = ?", projectID)
	if err != nil {
		return err
	}
	type decided struct {
		label string
		x, y  *float64
	}
	byNode := map[string]decided{}
	for rows.Next() {
		var id string
		var d decided
		if err := rows.Scan(&id, &d.label, &d.x, &d.y); err != nil {
			rows.Close()
			return err
		}
		byNode[id] = d
	}
	rows.Close()
	for i := range nodes {
		d, ok := byNode[nodes[i].ID]
		if !ok {
			continue
		}
		if d.label != "" {
			nodes[i].DefaultName, nodes[i].Name = nodes[i].Name, d.label
		}
		nodes[i].X, nodes[i].Y = d.x, d.y
	}
	return nil
}
