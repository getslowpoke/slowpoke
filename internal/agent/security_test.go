package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

func writeConfig(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlainHTTPToPublicServerIsRefused(t *testing.T) {
	cases := map[string]bool{
		"https://slowpoke.example.com": true,
		"http://slowpoke.example.com":  false,
		"http://localhost:8080":        true,
		"http://127.0.0.1:8080":        true,
		"http://10.0.0.5:8080":         true,
		"http://slowpoke:8080":         true, // docker service name
		"ftp://example.com":            false,
	}
	for server, ok := range cases {
		path := writeConfig(t, "server: "+server+"\ntoken: spk_x\n", 0o600)
		_, err := LoadConfig(path)
		if (err == nil) != ok {
			t.Errorf("%s: err=%v", server, err)
		}
	}
	path := writeConfig(t, "server: http://slowpoke.example.com\ntoken: spk_x\nallow_insecure: true\n", 0o600)
	if _, err := LoadConfig(path); err != nil {
		t.Errorf("allow_insecure must allow it: %v", err)
	}
}

func TestWorldReadableConfigIsReported(t *testing.T) {
	if w := ConfigWarnings(writeConfig(t, "x", 0o644)); len(w) != 1 {
		t.Fatalf("warnings %v", w)
	}
	if w := ConfigWarnings(writeConfig(t, "x", 0o640)); len(w) != 0 {
		t.Fatalf("warnings %v", w)
	}
}

func TestSecretsComeFromEnvironment(t *testing.T) {
	t.Setenv("SLOWPOKE_TOKEN", "spk_from_env")
	path := writeConfig(t, "server: https://x.example.com\ntoken: ${SLOWPOKE_TOKEN}\n", 0o600)
	cfg, err := LoadConfig(path)
	if err != nil || cfg.Token != "spk_from_env" {
		t.Fatalf("%v %v", cfg, err)
	}
}

func TestSenderDoesNotFollowRedirects(t *testing.T) {
	var leaked bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked = true
		}
	}))
	defer evil.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	s := NewSender(srv.URL, "spk_secret")
	_, _ = s.Send(context.Background(), protocol.Payload{BatchID: "1"})
	if leaked {
		t.Fatal("token followed a redirect")
	}
}

// A compromised server must not be able to make the agent run arbitrary SQL.
func TestAgentNeverExecutesServerProvidedSQL(t *testing.T) {
	var explains []protocol.Explain
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var p protocol.Payload
		_ = decodeGzip(r, &p)
		explains = append(explains, p.Explains...)
		resp := protocol.IngestResponse{OK: true}
		if calls == 1 {
			resp.Explain = []protocol.ExplainRequest{{Hash: "0123456789abcdef", SQL: "select * from users"}}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := &Config{Server: srv.URL, Token: "spk_x", Host: "h", StateDir: dir, Interval: time.Second}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Pretend there is a database source without opening a connection.
	off := false
	r.dbs = []*dbSource{{src: Source{Name: "db", Status: &off, Digests: &off}, engine: "mysql"}}
	now := time.Now()
	r.Round(context.Background(), now)
	time.Sleep(50 * time.Millisecond)
	r.Round(context.Background(), now.Add(time.Minute))

	if len(explains) != 1 || explains[0].Error == "" || !strings.Contains(explains[0].Error, "no local sample") {
		t.Fatalf("explains %+v", explains)
	}
}
