package agent

import (
	"encoding/binary"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

var servicePorts = map[int]string{
	80: "http", 443: "https", 8080: "http", 3306: "mysql", 5432: "postgres", 6379: "redis", 11211: "memcached",
	9200: "elasticsearch", 9300: "elasticsearch", 27017: "mongodb", 5672: "rabbitmq", 9000: "php-fpm", 8125: "statsd",
	2379: "etcd", 9092: "kafka", 4222: "nats", 7700: "meilisearch", 8108: "typesense", 25: "smtp", 587: "smtp", 465: "smtp",
	22: "ssh", 6001: "websocket", 8888: "websocket", 9090: "prometheus", 3000: "node", 5000: "python", 8000: "http",
}

const maxConnections = 200

// readConnections reads /proc/net/tcp{,6}: listening ports and established
// connections, grouped by peer and service. Loopback is kept: it links services of the machine.
func readConnections(root string) ([]protocol.ListeningPort, []protocol.Connection) {
	return readConnectionsAt(filepath.Join(root, "proc", "net"), filepath.Join(root, "proc"))
}

// readConnectionsAt reads the sockets of netDir and asks procRoot which process holds each one.
func readConnectionsAt(netDir, procRoot string) ([]protocol.ListeningPort, []protocol.Connection) {
	type row struct {
		localIP, remoteIP     net.IP
		localPort, remotePort int
		state                 string
		inode                 uint64
	}
	var rows []row
	for _, name := range []string{"tcp", "tcp6"} {
		raw, err := os.ReadFile(filepath.Join(netDir, name))
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(raw), "\n") {
			f := strings.Fields(line)
			if i == 0 || len(f) < 4 {
				continue
			}
			lip, lport, ok1 := parseHexAddr(f[1])
			rip, rport, ok2 := parseHexAddr(f[2])
			if !ok1 || !ok2 {
				continue
			}
			var inode uint64
			if len(f) > 9 {
				inode, _ = strconv.ParseUint(f[9], 10, 64)
			}
			rows = append(rows, row{lip, rip, lport, rport, f[3], inode})
		}
	}

	listen := map[int]*protocol.ListeningPort{}
	listenInode := map[int]uint64{}
	for _, r := range rows {
		if r.state != "0A" {
			continue
		}
		lp := listen[r.localPort]
		if lp == nil {
			lp = &protocol.ListeningPort{Port: r.localPort, Local: true, Service: servicePorts[r.localPort]}
			listen[r.localPort] = lp
			listenInode[r.localPort] = r.inode
		}
		if !r.localIP.IsLoopback() {
			lp.Local = false
		}
	}

	type key struct {
		dir, ip, process string
		port             int
	}
	// The process is read first: two services of the same machine may talk to the same address
	// (PHP-FPM and a queue worker both reach MySQL) and must stay apart.
	type established struct {
		dir   string
		ip    string
		port  int
		inode uint64
	}
	var live []established
	wanted := map[uint64]bool{}
	add := func(e established) {
		if len(live) >= maxConnections*4 {
			return
		}
		live = append(live, e)
		if e.inode > 0 && len(wanted) < maxConnections*2 {
			wanted[e.inode] = true
		}
	}
	for _, r := range rows {
		if r.state != "01" {
			continue
		}
		_, inbound := listen[r.localPort]
		// Loopback links services of this same machine (PHP to MySQL): keep it, but only from
		// the side that opened the connection, since /proc holds both ends.
		if r.remoteIP.IsLoopback() || r.localIP.IsLoopback() {
			if inbound {
				continue
			}
			add(established{"out", r.remoteIP.String(), r.remotePort, r.inode})
			continue
		}
		if inbound {
			add(established{"in", r.remoteIP.String(), r.localPort, r.inode})
		} else {
			add(established{"out", r.remoteIP.String(), r.remotePort, r.inode})
		}
	}

	for _, inode := range listenInode {
		if inode > 0 && len(wanted) < maxConnections*2+64 {
			wanted[inode] = true
		}
	}
	processes := processesForInodes(procRoot, wanted)

	counts := map[key]int{}
	for _, e := range live {
		counts[key{e.dir, e.ip, processes[e.inode], e.port}]++
	}

	var listening []protocol.ListeningPort
	for port, lp := range listen {
		lp.Process = processes[listenInode[port]]
		listening = append(listening, *lp)
	}
	sort.Slice(listening, func(i, j int) bool { return listening[i].Port < listening[j].Port })

	var conns []protocol.Connection
	for k, n := range counts {
		c := protocol.Connection{Direction: k.dir, RemoteIP: k.ip, Service: servicePorts[k.port], Process: k.process, Count: n}
		if k.dir == "out" {
			c.RemotePort = k.port
		} else {
			c.LocalPort = k.port
		}
		conns = append(conns, c)
	}
	sort.Slice(conns, func(i, j int) bool { return conns[i].Count > conns[j].Count })
	if len(conns) > maxConnections {
		conns = conns[:maxConnections]
	}
	return listening, conns
}

// parseHexAddr decodes "0500000A:C350" (IPv4, little endian) or the 32-hex IPv6 form.
func parseHexAddr(s string) (net.IP, int, bool) {
	addr, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return nil, 0, false
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return nil, 0, false
	}
	raw, err := hex.DecodeString(addr)
	if err != nil {
		return nil, 0, false
	}
	switch len(raw) {
	case 4:
		return net.IPv4(raw[3], raw[2], raw[1], raw[0]), int(port), true
	case 16:
		ip := make(net.IP, 16)
		for i := 0; i < 16; i += 4 {
			binary.BigEndian.PutUint32(ip[i:], binary.LittleEndian.Uint32(raw[i:]))
		}
		if v4 := ip.To4(); v4 != nil {
			return v4, int(port), true
		}
		return ip, int(port), true
	}
	return nil, 0, false
}

// localAddresses lists the non-loopback IPs of the machine.
func localAddresses() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
			out = append(out, ipn.IP.String())
		}
	}
	if extra := os.Getenv("SLOWPOKE_ADDRESSES"); extra != "" {
		out = append(out, strings.Split(extra, ",")...)
	}
	return out
}
