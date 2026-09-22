package api

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/tokens"
)

// A command on somebody's laptop needs to ask the panel two things — which machines nobody
// measures, and a token for one of them — with a token, not with a browser session.
func TestTheFleetIsReadableWithAPersonalToken(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := t.Context()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")

	// One machine with an agent, talking to a database nobody measures.
	_, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{
		BatchID: "b1", Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent", Sources: []string{"nginx:app"}},
		Network: &protocol.NetworkSnapshot{
			Addresses:   []string{"10.0.0.5"},
			Connections: []protocol.Connection{{Direction: "out", RemoteIP: "10.0.0.9", RemotePort: 3306, Service: "mysql", Count: 4}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, personal, err := c.srv.Tokens.Create(ctx, p.ID, tokens.KindPersonal, "laptop", 1)
	if err != nil {
		t.Fatal(err)
	}

	// A laptop has no browser session: a plain client, only the token.
	laptop := &http.Client{}
	get := func(path, token string) (int, string) {
		req, _ := http.NewRequest("GET", c.base+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := laptop.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(body)
	}

	// Without a token, nothing.
	if code, _ := get("/api/projects/shop/fleet", ""); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := get("/api/projects/shop/fleet", "spu_nonsense"); code != 401 {
		t.Fatalf("wrong token: %d", code)
	}

	// With a personal token: one line per machine nobody measures, plain text so a shell script
	// needs nothing but read.
	code, body := get("/api/projects/shop/fleet", personal)
	if code != 200 {
		t.Fatalf("fleet %d %s", code, body)
	}
	lines := strings.Fields(strings.TrimSpace(body))
	if len(lines) == 0 || !strings.Contains(body, "10.0.0.9") {
		t.Fatalf("the database nobody measures must be in the list:\n%s", body)
	}
	if strings.Contains(body, "web-1") || strings.Contains(body, "10.0.0.5") {
		t.Fatalf("a machine that already has an agent is not in the list:\n%s", body)
	}

	// And a token for one of them, in plain text, because the script has no JSON reader.
	req, _ := http.NewRequest("POST", c.base+"/api/projects/shop/tokens", strings.NewReader(`{"kind":"agent","name":"db-1"}`))
	req.Header.Set("Authorization", "Bearer "+personal)
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Slowpoke", "1") // the same header the UI sends: a form on another site cannot
	res, err := laptop.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	secret, _ := io.ReadAll(res.Body)
	if res.StatusCode != 201 || !strings.HasPrefix(strings.TrimSpace(string(secret)), "spa_") {
		t.Fatalf("token %d %q", res.StatusCode, secret)
	}
}

// The script that does the installing runs on a laptop: it is public because it carries no
// secret, and it has to be plain shell.
func TestFleetScriptIsServed(t *testing.T) {
	c := newClient(t)
	code, body, header := c.raw("/fleet.sh")
	if code != 200 || !strings.HasPrefix(header.Get("Content-Type"), "text/x-shellscript") {
		t.Fatalf("fleet.sh %d %q", code, header.Get("Content-Type"))
	}
	text := string(body)
	if !strings.Contains(text, c.base) {
		t.Fatal("the script must know its own server")
	}
	for _, step := range []string{"/api/projects/", "/fleet", "install.sh", "--dry-run", "ssh"} {
		if !strings.Contains(text, step) {
			t.Fatalf("the script does not mention %q", step)
		}
	}
	path := filepath.Join(t.TempDir(), "fleet.sh")
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s", err, out)
	}
}

// A machine you named yourself keeps the name you gave it: --only login_spider must not come
// back into the panel as "spider2" because that is what its hostname says. Found by running it
// against a real machine on 2026-09-18, which left a duplicate in the panel.
func TestTheNameYouGiveIsTheNameThatSticks(t *testing.T) {
	c := newClient(t)
	_, body, _ := c.raw("/fleet.sh")
	script := string(body)
	// The rule: a name that is not an address is used as it is.
	if !strings.Contains(script, `*[!0-9.]*) hostname="$address" ;;`) {
		t.Fatalf("the script does not keep the name you gave:\n%s", script)
	}
	if !strings.Contains(script, `*) hostname="$($SSH "$host" 'hostname'`) {
		t.Fatal("a machine found by its address is still named after its own hostname")
	}
}
