package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// fakeProc writes a /proc tree: processes with their command line and the sockets they hold.
func fakeProc(t *testing.T, procs map[int][2]string, fds map[int][]uint64) string {
	t.Helper()
	root := t.TempDir()
	for pid, p := range procs {
		dir := filepath.Join(root, itoa(pid))
		if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o755); err != nil {
			t.Fatal(err)
		}
		write := func(name, content string) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("comm", p[0]+"\n")
		write("cmdline", p[1])
		for i, inode := range fds[pid] {
			// /proc/<pid>/fd/<n> is a symlink to socket:[inode]; it never resolves.
			if err := os.Symlink("socket:["+itoa(int(inode))+"]", filepath.Join(dir, "fd", itoa(i+3))); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestProcessNamesFromSockets(t *testing.T) {
	nul := "\x00"
	root := fakeProc(t, map[int][2]string{
		101: {"php-fpm7.4", "php-fpm: pool shop-testing" + nul},
		102: {"nginx", "nginx: worker process" + nul},
		103: {"mysqld", "/usr/sbin/mysqld" + nul},
		104: {"php", "/usr/bin/php" + nul + "/var/www/app/artisan" + nul + "queue:work" + nul + "--queue=emails" + nul},
		105: {"node", "node" + nul + "/var/www/app/server.js" + nul},
		106: {"VVAgent", "/opt/BUAgent/VVAgent" + nul + "-d" + nul},
	}, map[int][]uint64{
		101: {1001, 1002}, 102: {2001}, 103: {3001}, 104: {4001}, 105: {5001}, 106: {6001},
	})

	got := processesForInodes(root, map[uint64]bool{1001: true, 2001: true, 3001: true, 4001: true, 5001: true, 6001: true})
	want := map[uint64]string{
		1001: "php-fpm:shop-testing",
		2001: "nginx",
		3001: "mysql",
		4001: "php artisan queue:work",
		5001: "node server.js",
		6001: "VVAgent",
	}
	for inode, name := range want {
		if got[inode] != name {
			t.Errorf("inode %d: got %q want %q", inode, got[inode], name)
		}
	}

	// Sockets nobody asked for are not looked up, and an unknown inode simply has no name.
	if len(got) != len(want) {
		t.Fatalf("unexpected entries: %+v", got)
	}
	if n := processesForInodes(root, map[uint64]bool{9999: true}); len(n) != 0 {
		t.Fatalf("unknown inode: %+v", n)
	}
	if n := processesForInodes(root, nil); len(n) != 0 {
		t.Fatalf("nothing to look up must cost nothing: %+v", n)
	}
}

// The map needs to know which process opened each connection: PHP talking to MySQL is not the
// same as the backup agent talking to the outside.
func TestConnectionsCarryTheirProcess(t *testing.T) {
	nul := "\x00"
	dir := t.TempDir()
	netDir := filepath.Join(dir, "net")
	if err := os.MkdirAll(netDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// inode is the 10th field. 0100007F = 127.0.0.1, 0CEA = 3306, 01BB = 443, 73B8D850 = 80.216.184.115.
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:0CEA 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 3001
   1: 00000000:01BB 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2000
   2: 0100007F:B3C4 0100007F:0CEA 01 00000000:00000000 00:00000000 00000000     0        0 1001
   3: 73B8D850:01BB 0A13A8C0:D431 01 00000000:00000000 00:00000000 00000000     0        0 2001
`
	if err := os.WriteFile(filepath.Join(netDir, "tcp"), []byte(tcp), 0o644); err != nil {
		t.Fatal(err)
	}
	procRoot := fakeProc(t, map[int][2]string{
		101: {"php-fpm7.4", "php-fpm: pool www" + nul},
		103: {"mysqld", "/usr/sbin/mysqld" + nul},
		102: {"nginx", "nginx: worker process" + nul},
	}, map[int][]uint64{101: {1001}, 103: {3001}, 102: {2001}})

	listening, conns := readConnectionsAt(netDir, procRoot)
	for _, l := range listening {
		if l.Port == 3306 && l.Process != "mysql" {
			t.Fatalf("listening process: %+v", listening)
		}
	}
	byProcess := map[string]protocol.Connection{}
	for _, c := range conns {
		byProcess[c.Process] = c
	}
	if c, ok := byProcess["php-fpm:www"]; !ok || c.RemotePort != 3306 || c.Direction != "out" {
		t.Fatalf("php to mysql: %+v", conns)
	}
	if c, ok := byProcess["nginx"]; !ok || c.Direction != "in" || c.LocalPort != 443 {
		t.Fatalf("visitor to nginx: %+v", conns)
	}
}
