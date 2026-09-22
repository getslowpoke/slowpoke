package agent

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/agent/detect"
	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// The agent reads the machine to say what it is NOT reading: a database nobody looks at, a pool
// without its slow log, a site whose requests are invisible, an application without the package.
// Without this the panel shows what is configured and stays silent about the rest.
func TestWhatIsOnTheMachineButNotConfigured(t *testing.T) {
	report := detect.Report{
		Sites: []detect.Site{
			{Name: "shop.example.com", AccessLog: "/var/log/nginx/shop.log", HasDuration: true},
			{Name: "admin.example.com", AccessLog: "/var/log/nginx/admin.log"},
		},
		Pools:     []detect.Pool{{Name: "www", Version: "8.3", SlowLogOn: true, SlowLog: "/var/log/php8.3-fpm-www.slow.log"}},
		Databases: []detect.Database{{Engine: "mysql", Process: "mariadbd", SlowLog: "/var/log/mysql/slow.log"}},
		Caches:    []detect.Cache{{Engine: "redis", Port: 6379}},
		Apps: []detect.App{
			{Dir: "/var/www/shop", Framework: "Laravel", Version: "12.1", Site: "shop.example.com", Language: "php"},
			{Dir: "/var/www/admin", Framework: "Laravel", Version: "5.8", HasSlowpoke: true},
		},
	}
	// The panel of Slowpoke itself is never a gap to close.
	report.Sites = append(report.Sites, detect.Site{Name: "slowpoke.example.com", AccessLog: "/var/log/nginx/slowpoke.log"})
	// Configured: only the first site and the Redis.
	sources := []Source{
		{Type: "nginx", Name: "shop.example.com", Path: "/var/log/nginx/shop.log"},
		{Type: "redis", Name: "cache", Addr: "127.0.0.1:6379"},
	}
	found := map[string]string{}
	facts := map[string]map[string]string{}
	for _, c := range discoveries(report, sources, "https://slowpoke.example.com") {
		if len(c.Items) != 1 || c.Items[0].Status != StatusOff {
			t.Fatalf("%s: %+v", c.Source, c.Items)
		}
		found[c.Source] = c.Items[0].Reason
		facts[c.Source] = c.Facts
	}
	want := map[string]string{
		"found:site:admin.example.com": "not_configured",
		"found:pool:www":               "not_configured",
		"found:database:mysql":         "not_configured",
		"found:app:/var/www/shop":      "no_package",
	}
	for source, reason := range want {
		if found[source] != reason {
			t.Errorf("%s: got %q, want %q (all: %v)", source, found[source], reason, found)
		}
	}
	// What is already configured, and the app that has the package, must not be listed.
	for _, unwanted := range []string{"found:site:shop.example.com", "found:cache:6379", "found:app:/var/www/admin", "found:site:slowpoke.example.com"} {
		if _, ok := found[unwanted]; ok {
			t.Errorf("%s should not be reported: it is covered", unwanted)
		}
	}
	// The guide needs enough to write the exact lines.
	if f := facts["found:site:admin.example.com"]; f["path"] != "/var/log/nginx/admin.example.com.slowpoke.log" || f["access_log"] != "/var/log/nginx/admin.log" {
		t.Errorf("site facts: %v", f)
	}
	if f := facts["found:database:mysql"]; f["engine"] != "mysql" || f["slow_log"] != "/var/log/mysql/slow.log" {
		t.Errorf("database facts: %v", f)
	}
	if f := facts["found:app:/var/www/shop"]; f["framework"] != "Laravel" || f["site"] != "shop.example.com" {
		t.Errorf("app facts: %v", f)
	}
}

// The runner looks at the machine on its own, without anybody running detect by hand, and does
// it rarely: a scan reads a few hundred small files and must never become a background load.
func TestTheRunnerScansTheMachineItselfAndNotTooOften(t *testing.T) {
	r, err := NewRunner(&Config{Server: "http://127.0.0.1:1", Token: "t", Host: "web-1", StateDir: t.TempDir(),
		Sources: []Source{{Type: "host"}}})
	if err != nil {
		t.Fatal(err)
	}
	r.scanRoot = "detect/testdata/staging"
	scans := 0
	r.scan = func(root string) detect.Report {
		scans++
		return detect.Scan(root)
	}
	now := time.Date(2026, 9, 18, 4, 0, 0, 0, time.UTC)
	first := r.machineChecks(now)
	if scans != 1 || len(first) == 0 {
		t.Fatalf("scans=%d checks=%d: the machine was not looked at", scans, len(first))
	}
	again := r.machineChecks(now.Add(5 * time.Minute))
	if scans != 1 {
		t.Fatalf("scanned again after 5 minutes: %d scans", scans)
	}
	if len(again) != len(first) {
		t.Fatalf("the answer between scans changed: %d then %d", len(first), len(again))
	}
	if r.machineChecks(now.Add(2 * time.Hour)); scans != 2 {
		t.Fatalf("never scanned again: %d scans", scans)
	}
}

// The config that detect writes has to be one the agent accepts: the two are written together
// and drift apart silently, so the round trip is a test.
func TestGeneratedConfigLoads(t *testing.T) {
	raw := detect.Scan("detect/testdata/staging").Config("https://slowpoke.example.test", "spa_test")
	file := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(file)
	if err != nil {
		t.Fatalf("the agent refuses the generated config: %v\n%s", err, raw)
	}
	if cfg.Host != "staging" || len(cfg.Sources) == 0 {
		t.Fatalf("config: %+v", cfg)
	}
}

// End to end through a payload: the second check carries what the agent costs the machine.
func TestTheCostReachesThePayload(t *testing.T) {
	var last protocol.Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := gzip.NewReader(r.Body)
		_ = json.NewDecoder(body).Decode(&last)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	r, err := NewRunner(&Config{Server: srv.URL, Token: "spa_x", Host: "web-1", StateDir: t.TempDir(),
		Interval: time.Second, Sources: []Source{{Type: "host"}}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	r.Round(context.Background(), now)
	r.Round(context.Background(), now.Add(11*time.Minute))

	for _, c := range last.Checks {
		if c.Source != "host:host" {
			continue
		}
		if c.Facts["agent_cpu_percent"] == "" || c.Facts["agent_memory_mb"] == "" {
			t.Fatalf("the machine is not told what the agent costs it: %v", c.Facts)
		}
		return
	}
	t.Fatalf("no host check in the payload: %+v", last.Checks)
}
