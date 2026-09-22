package topology

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// serviceNames turns a port and what the agent reads on the machine into a name a person
// recognizes: "nginx" rather than "https", "MySQL" rather than "tcp/3306".
var serviceNames = map[int]string{
	3306: "MySQL", 5432: "PostgreSQL", 6379: "Redis", 11211: "Memcached", 27017: "MongoDB",
	9200: "Elasticsearch", 7700: "Meilisearch", 5672: "RabbitMQ", 9092: "Kafka", 4222: "NATS",
	22: "SSH", 25: "SMTP", 587: "SMTP", 9090: "Prometheus",
}

// agentPorts are the ports the agent opens to listen to the applications of its own machine: the
// traces its framework packages send it, and StatsD. They are Slowpoke measuring, not something on
// the machine to measure, so the map leaves them out - the same rule as the housekeeping queries.
var agentPorts = map[int]bool{4318: true, 8125: true}

func serviceName(port int, service, process string, sources []string) string {
	if port == 80 || port == 443 || service == "http" || service == "https" {
		for _, s := range sources {
			switch {
			case strings.HasPrefix(s, "nginx:"):
				return "nginx"
			case strings.HasPrefix(s, "apache:"):
				return "Apache"
			case strings.HasPrefix(s, "caddy:"):
				return "Caddy"
			}
		}
		return "HTTP"
	}
	if name := serviceNames[port]; name != "" {
		return name
	}
	if service != "" {
		return service
	}
	// A port nobody knows: the process answering on it is the best name there is.
	if process != "" {
		return processLabel(process)
	}
	return fmt.Sprintf("tcp/%d", port)
}

// services builds the nodes that live inside a machine: one per listening port, plus the ones
// that answer on a unix socket and would otherwise be invisible (PHP-FPM).
func services(host Node, listening []protocol.ListeningPort) []Node {
	var out []Node
	byName := map[string]int{}
	for _, l := range listening {
		if agentPorts[l.Port] {
			continue
		}
		name := serviceName(l.Port, l.Service, l.Process, host.Sources)
		if i, seen := byName[name]; seen {
			// One server answering on several ports (nginx on 80 and 443) is one service.
			out[i].Ports = append(out[i].Ports, l.Port)
			out[i].LocalOnly = out[i].LocalOnly && l.Local
			continue
		}
		role := serviceRole[l.Service]
		if role == "" {
			role = serviceRole[strings.ToLower(name)]
		}
		byName[name] = len(out)
		out = append(out, Node{
			ID: fmt.Sprintf("svc:%s:%d", host.Name, l.Port), Kind: "service", Parent: host.ID, HostID: host.HostID,
			Name: name, Port: l.Port, Ports: []int{l.Port}, Service: l.Service, Process: l.Process, LocalOnly: l.Local,
			Online: host.Online, Monitored: true, Roles: nonEmpty(role),
		})
	}
	// Two PHP projects on one machine are two pools: each keeps its name, or the map shows two
	// identical boxes and nobody can tell which project talks to which database.
	pools := 0
	for _, s := range host.Sources {
		if strings.HasPrefix(s, "php-fpm:") {
			pools++
		}
	}
	for _, s := range host.Sources {
		if name, ok := strings.CutPrefix(s, "php-fpm:"); ok {
			label := "PHP-FPM"
			if pools > 1 {
				label = "PHP-FPM " + name
			}
			out = append(out, Node{ID: "svc:" + host.Name + ":php-fpm:" + name, Kind: "service", Parent: host.ID, HostID: host.HostID,
				Name: label, Service: "php-fpm", Process: "php-fpm:" + name, LocalOnly: true, Online: host.Online, Monitored: true, Roles: []string{"app"}})
		}
	}
	return out
}

func nonEmpty(role string) []string {
	if role == "" {
		return nil
	}
	return []string{role}
}

// cloudflareRanges are the published Cloudflare edge networks (https://www.cloudflare.com/ips/,
// read on 2026-09-17). Requests of a site behind Cloudflare all arrive from them, which is how
// the map knows to draw it in front of the web server.
var cloudflareRanges = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
	"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
	"162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
	"2a06:98c0::/29", "2c0f:f248::/32",
}

var cloudflarePrefixes = parsePrefixes(cloudflareRanges)

func parsePrefixes(list []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(list))
	for _, s := range list {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// fromCloudflare tells whether an address belongs to the Cloudflare edge.
func fromCloudflare(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, p := range cloudflarePrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// processLabel is how a process is shown when it becomes a service of its own: a PHP-FPM pool
// keeps its pool name, everything else keeps the name the agent read.
func processLabel(process string) string {
	if pool, ok := strings.CutPrefix(process, "php-fpm:"); ok {
		return "PHP-FPM " + pool
	}
	return process
}
