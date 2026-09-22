package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/ingest"
	"github.com/getslowpoke/slowpoke/internal/server/mute"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
	"github.com/getslowpoke/slowpoke/internal/server/tokens"
)

type harness struct {
	t       *testing.T
	url     string
	token   string
	db      *store.DB
	p       project.Project
	id      int
	mission int64
}

func newHarness(t *testing.T) *harness {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	_, plain, _ := tokens.Service{DB: db}.Create(ctx, p.ID, tokens.KindPersonal, "Ada · Claude Code", 0)
	now := time.Now()
	mission, _ := db.Q().InsertID(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, current, points, status, good_windows, person, opened_at, checked_at)
		VALUES (?, 'mission', 'endpoint', 'nginx:app GET /search', 'GET /search', 300, 280, 120, 'open', 0, '', ?, ?)`, p.ID, now.Add(-48*time.Hour).Unix(), now.Unix())
	srv := httptest.NewServer(Handler(db, "1.2.3", "https://slowpoke.example.com"))
	t.Cleanup(srv.Close)
	return &harness{t: t, url: srv.URL, token: plain, db: db, p: p, mission: mission}
}

func (h *harness) rpc(method string, params any, token string) (int, map[string]any) {
	h.t.Helper()
	h.id++
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": h.id, "method": method, "params": params})
	req, _ := http.NewRequest("POST", h.url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func (h *harness) call(tool string, args map[string]any) (string, bool) {
	h.t.Helper()
	code, out := h.rpc("tools/call", map[string]any{"name": tool, "arguments": args}, h.token)
	if code != 200 || out["error"] != nil {
		h.t.Fatalf("%s: %d %v", tool, code, out)
	}
	result := out["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	isErr, _ := result["isError"].(bool)
	return text, isErr
}

func TestHandshakeAndTools(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.rpc("initialize", map[string]any{}, ""); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	if code, _ := h.rpc("initialize", map[string]any{}, "spa_agent_token_is_not_enough"); code != 401 {
		t.Fatalf("agent token: %d", code)
	}

	code, init := h.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": "claude-code"}}, h.token)
	result := init["result"].(map[string]any)
	if code != 200 || result["protocolVersion"] != "2025-06-18" || result["serverInfo"].(map[string]any)["name"] != "slowpoke" || !strings.Contains(result["instructions"].(string), "Shop") {
		t.Fatalf("%d %v", code, init)
	}

	// Notifications get 202 and no body.
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	req, _ := http.NewRequest("POST", h.url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+h.token)
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 202 {
		t.Fatalf("notification: %d", res.StatusCode)
	}

	_, list := h.rpc("tools/list", nil, h.token)
	tools := list["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		m := tl.(map[string]any)
		names[m["name"].(string)] = true
		if m["inputSchema"].(map[string]any)["type"] != "object" || m["description"] == "" {
			t.Fatalf("tool schema %v", m)
		}
	}
	for _, want := range []string{"project_overview", "list_missions", "get_mission", "claim_mission", "mark_shipped", "link_pull_request", "verify_mission", "list_slow_queries", "get_query", "list_slow_endpoints", "list_hotspots", "list_integrations"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}

	_, bad := h.rpc("no/such/method", nil, h.token)
	if bad["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("%v", bad)
	}
}

func TestMissionWorkflow(t *testing.T) {
	h := newHarness(t)
	idArg := map[string]any{"id": h.mission}

	text, _ := h.call("list_missions", map[string]any{})
	if !strings.Contains(text, "GET /search") || !strings.Contains(text, "effort M") {
		t.Fatalf("list: %s", text)
	}
	text, _ = h.call("get_mission", idArg)
	if !strings.Contains(text, "# Slow endpoint: GET /search") || !strings.Contains(text, "Definition of done") {
		t.Fatalf("brief: %s", text)
	}
	text, _ = h.call("claim_mission", map[string]any{"id": h.mission, "person": "ada"})
	if !strings.Contains(text, "ada") {
		t.Fatalf("claim: %s", text)
	}
	var person string
	_ = h.db.Q().Row(context.Background(), "SELECT person FROM missions WHERE id = ?", h.mission).Scan(&person)
	if person != "ada" {
		t.Fatalf("person %q", person)
	}
	text, isErr := h.call("link_pull_request", map[string]any{"id": h.mission, "url": "javascript:alert(1)"})
	if !isErr {
		t.Fatalf("invalid url accepted: %s", text)
	}
	h.call("link_pull_request", map[string]any{"id": h.mission, "url": "https://github.com/acme/shop/pull/12", "title": "Index for search"})
	var tasks int
	_ = h.db.Q().Row(context.Background(), "SELECT COUNT(*) FROM mission_tasks WHERE mission_id = ? AND provider = 'pull_request'", h.mission).Scan(&tasks)
	if tasks != 1 {
		t.Fatal("pull request not linked")
	}
	text, _ = h.call("verify_mission", idArg)
	if !strings.Contains(text, "target") {
		t.Fatalf("verify: %s", text)
	}
	if _, isErr := h.call("get_mission", map[string]any{"id": 999999}); !isErr {
		t.Fatal("unknown mission must be a tool error")
	}
	if _, isErr := h.call("get_query", map[string]any{"hash": "0000000000000000"}); !isErr {
		t.Fatal("unknown query must be a tool error")
	}
	h.call("project_overview", map[string]any{})
	h.call("list_slow_queries", map[string]any{"window": "7d"})
	h.call("list_slow_endpoints", map[string]any{})
	h.call("list_hotspots", map[string]any{})
}

// An assistant asked to fix a slow query must know when plans are off and how to turn them on.
func TestListIntegrationsExplainsWhatIsMissing(t *testing.T) {
	h := newHarness(t)
	text, _ := h.call("list_integrations", map[string]any{})
	if !strings.Contains(text, "No agent has reported") {
		t.Fatalf("empty: %s", text)
	}
	_, err := ingest.New(h.db).Write(context.Background(), h.p, protocol.Payload{BatchID: "c1", Agent: protocol.AgentInfo{Host: "db-1", Kind: "agent"},
		Checks: []protocol.SourceCheck{{Source: "mysql:main", Items: []protocol.CheckItem{
			{Key: "connection", Status: "ok"}, {Key: "statistics", Status: "ok", Optional: true},
			{Key: "plans", Status: "off", Optional: true, Reason: "no_select", Detail: "shop"}},
			Facts: map[string]string{"user": "slowpoke@127.0.0.1", "schemas": "shop"}}}})
	if err != nil {
		t.Fatal(err)
	}
	text, _ = h.call("list_integrations", map[string]any{})
	for _, want := range []string{"db-1", "mysql:main", "partial", "plans", "GRANT SELECT ON `shop`.* TO 'slowpoke'@'127.0.0.1';"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestProjectIsolation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	other, _ := project.Repo{DB: h.db}.Create(ctx, "Other", "UTC")
	_, otherToken, _ := tokens.Service{DB: h.db}.Create(ctx, other.ID, tokens.KindPersonal, "x", 0)
	h.token = otherToken
	if _, isErr := h.call("get_mission", map[string]any{"id": h.mission}); !isErr {
		t.Fatal("a token must only see its own project")
	}
}

// The assistant of the team must be able to finish the installation by itself: what the agent
// found on the machine and nobody reads comes with the lines that close each gap.
func TestListIntegrationsAlsoSaysWhatIsNotReadYet(t *testing.T) {
	h := newHarness(t)
	_, err := ingest.New(h.db).Write(context.Background(), h.p, protocol.Payload{BatchID: "c2", Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent"},
		Checks: []protocol.SourceCheck{
			{Source: "nginx:shop", Items: []protocol.CheckItem{{Key: "readable", Status: "ok"}, {Key: "duration", Status: "ok"}}},
			{Source: "found:database:mysql", Items: []protocol.CheckItem{{Key: "configured", Status: "off", Reason: "not_configured"}},
				Facts: map[string]string{"engine": "mysql", "slow_log": "/var/log/mysql/slow.log", "address": "127.0.0.1:3306"}},
			{Source: "found:app:/var/www/shop", Items: []protocol.CheckItem{{Key: "configured", Status: "off", Reason: "no_package"}},
				Facts: map[string]string{"framework": "Laravel", "dir": "/var/www/shop"}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	text, _ := h.call("list_integrations", map[string]any{})
	for _, want := range []string{
		"Not read yet",              // its own section, not mixed with the working sources
		"database mysql",            // named in words, not as a source key
		"composer require slowpoke/laravel",
		"- type: mysql-slowlog",
		"1 source working",          // the coverage line an assistant can act on
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

// An assistant asked to fix a mission must not start one somebody else already took, and must
// see when the fix is already online and only waiting to be verified.
func TestAnAssistantSeesWhoIsOnAMissionAndDoesNotTakeItOver(t *testing.T) {
	h := newHarness(t)
	if text, _ := h.call("claim_mission", map[string]any{"id": h.mission, "person": "linus@example.com"}); !strings.Contains(text, "taken by linus@example.com") {
		t.Fatalf("claim: %s", text)
	}
	text, _ := h.call("claim_mission", map[string]any{"id": h.mission, "person": "ada@example.com"})
	if !strings.Contains(text, "already taken by linus@example.com") || !strings.Contains(text, "Nothing changed") {
		t.Fatalf("second claim: %s", text)
	}
	h.call("mark_shipped", map[string]any{"id": h.mission})
	text, _ = h.call("list_missions", map[string]any{})
	if !strings.Contains(text, "is online") || !strings.Contains(text, "being verified") {
		t.Fatalf("list: %s", text)
	}
	if text, _ = h.call("get_mission", map[string]any{"id": h.mission}); !strings.Contains(text, "Where it stands: the fix by linus@example.com is online") {
		t.Fatalf("brief: %s", text)
	}
}

// A machine the team muted stays installed on purpose: the assistant must not try to finish
// its installation or fix what it reports.
func TestListIntegrationsLeavesAMutedMachineAlone(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := ingest.New(h.db).Write(ctx, h.p, protocol.Payload{BatchID: "c3", Agent: protocol.AgentInfo{Host: "staging", Kind: "agent"},
		Checks: []protocol.SourceCheck{{Source: "mysql:main", Items: []protocol.CheckItem{
			{Key: "connection", Status: "ok"}, {Key: "plans", Status: "off", Optional: true, Reason: "no_select", Detail: "shop"}},
			Facts: map[string]string{"user": "slowpoke@127.0.0.1", "schemas": "shop"}}}})
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	_ = h.db.Q().Row(ctx, "SELECT id FROM hosts WHERE project_id = ? AND name = 'staging'", h.p.ID).Scan(&id)
	if err := mute.SetHost(ctx, h.db, h.p.ID, id, "ada@example.com", "staging, nobody fixes it", time.Now()); err != nil {
		t.Fatal(err)
	}
	text, _ := h.call("list_integrations", map[string]any{})
	if !strings.Contains(text, "## staging") || !strings.Contains(text, "Muted by the team") || !strings.Contains(text, "staging, nobody fixes it") {
		t.Fatalf("the muted machine is not named as muted:\n%s", text)
	}
	if strings.Contains(text, "GRANT SELECT") {
		t.Fatalf("the assistant is still asked to fix a muted machine:\n%s", text)
	}
}
