package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConnectionsFromProcNetTCP(t *testing.T) {
	listening, conns := readConnections("testdata/netproc")
	// Listening on :80 (all interfaces) and 127.0.0.1:3306.
	if len(listening) != 2 || listening[0].Port != 80 || listening[1].Port != 3306 || listening[1].Service != "mysql" {
		t.Fatalf("listening %+v", listening)
	}

	byKey := map[string]int{}
	for _, c := range conns {
		byKey[c.Direction+" "+c.RemoteIP+" "+c.Service] = c.Count
	}
	// 10.0.0.5 -> 10.0.0.9:3306 twice (the CLOSE_WAIT one is ignored), -> 10.0.0.9:6379 once.
	if byKey["out 10.0.0.9 mysql"] != 2 || byKey["out 10.0.0.9 redis"] != 1 {
		t.Fatalf("outbound %v", byKey)
	}
	// A client from 192.168.1.42 on our port 80.
	if byKey["in 192.168.1.42 http"] != 1 {
		t.Fatalf("inbound %v", byKey)
	}
	// 34.216.184.93:443 is a public HTTPS service.
	if byKey["out 34.216.184.93 https"] != 1 {
		t.Fatalf("public %v", byKey)
	}
	// Loopback links two services of this machine: kept, and counted from the client side only.
	local := 0
	for _, c := range conns {
		if c.RemoteIP == "127.0.0.1" {
			local++
			if c.Direction != "out" {
				t.Fatalf("loopback counted from the server side: %+v", c)
			}
		}
	}
	if local == 0 {
		t.Fatal("loopback connections are part of the map")
	}
}

// Services on the same machine talk over loopback (PHP to MySQL on 127.0.0.1:3306): the map
// needs them, so they are kept, counted once, from the side that opened them.
func TestLoopbackConnectionsAreKept(t *testing.T) {
	dir := t.TempDir()
	// state 0A = listening, 01 = established. 0100007F = 127.0.0.1, 0CEA = 3306, 1F90 = 8080.
	tcp := `  sl  local_address rem_address   st
   0: 0100007F:0CEA 00000000:0000 0A
   1: 0100007F:1F90 00000000:0000 0A
   2: 0100007F:B3C4 0100007F:0CEA 01
   3: 0100007F:B3C5 0100007F:0CEA 01
   4: 0100007F:0CEA 0100007F:B3C4 01
`
	if err := os.WriteFile(filepath.Join(dir, "tcp"), []byte(tcp), 0o644); err != nil {
		t.Fatal(err)
	}
	listening, conns := readConnectionsAt(dir, dir)
	ports := map[int]bool{}
	for _, l := range listening {
		ports[l.Port] = true
	}
	if !ports[3306] || !ports[8080] {
		t.Fatalf("listening %+v", listening)
	}
	var out, in int
	for _, c := range conns {
		if c.RemoteIP != "127.0.0.1" {
			continue
		}
		if c.Direction == "out" && c.RemotePort == 3306 {
			out = c.Count
		}
		if c.Direction == "in" {
			in++
		}
	}
	// Both ends of a loopback connection are in /proc: counting the server side too would
	// double every local link.
	if out != 2 || in != 0 {
		t.Fatalf("loopback connections: %+v", conns)
	}
}
