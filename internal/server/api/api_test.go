package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
	"github.com/getslowpoke/slowpoke/internal/server/tasks"
)

type client struct {
	t    *testing.T
	base string
	http *http.Client
	srv  *Server
}

func newClient(t *testing.T) *client {
	t.Helper()
	db := store.OpenTest(t)
	srv := New(db, fstest.MapFS{"index.html": {Data: []byte("<html>slowpoke</html>")}, "assets/app.js": {Data: []byte("js")}})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: ts.URL, http: &http.Client{Jar: jar}, srv: srv}
}

func (c *client) do(method, path string, body any, headers ...string) (int, map[string]any) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Slowpoke", "1")
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			req.Header.Del(headers[i])
		} else {
			req.Header.Set(headers[i], headers[i+1])
		}
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func (c *client) login() {
	c.t.Helper()
	setupCode, err := c.srv.Auth.SetupCode(context.Background())
	if err != nil {
		c.t.Fatal(err)
	}
	if code, body := c.do("POST", "/api/setup", map[string]string{"name": "Ada", "email": "ada@example.com", "password": "correct horse battery", "setup_code": setupCode}); code != 200 {
		c.t.Fatalf("setup %d %v", code, body)
	}
}

func (c *client) project() map[string]any {
	c.t.Helper()
	code, body := c.do("POST", "/api/projects", map[string]string{"name": "Shop", "timezone": "Europe/Rome"})
	if code != 201 {
		c.t.Fatalf("create project %d %v", code, body)
	}
	return body["project"].(map[string]any)
}

func TestSetupHappensOnceAndLoginWorks(t *testing.T) {
	c := newClient(t)
	if _, body := c.do("GET", "/api/setup", nil); body["needs_setup"] != true {
		t.Fatalf("%v", body)
	}
	c.login()
	if code, _ := c.do("POST", "/api/setup", map[string]string{"name": "Eve", "email": "eve@example.com", "password": "correct horse battery"}); code != 409 {
		t.Fatalf("second setup must be refused, got %d", code)
	}
	if code, body := c.do("GET", "/api/me", nil); code != 200 || body["user"].(map[string]any)["email"] != "ada@example.com" {
		t.Fatalf("%d %v", code, body)
	}
	c.do("POST", "/api/logout", nil)
	if code, _ := c.do("GET", "/api/me", nil); code != 401 {
		t.Fatalf("logged out: %d", code)
	}
	if code, _ := c.do("POST", "/api/login", map[string]string{"email": "ada@example.com", "password": "nope nope nope"}); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	if code, _ := c.do("POST", "/api/login", map[string]string{"email": "ada@example.com", "password": "correct horse battery"}); code != 200 {
		t.Fatalf("login: %d", code)
	}
}

func TestProtectedRoutesAndCSRFHeader(t *testing.T) {
	c := newClient(t)
	if code, _ := c.do("GET", "/api/projects", nil); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	c.login()
	if code, _ := c.do("POST", "/api/projects", map[string]string{"name": "X"}, "X-Slowpoke", ""); code != 403 {
		t.Fatalf("mutation without the custom header must be refused (CSRF), got %d", code)
	}
}

func gzipJSON(t *testing.T, v any) *bytes.Buffer {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_ = json.NewEncoder(zw).Encode(v)
	_ = zw.Close()
	return &buf
}

func TestIngestWithProjectToken(t *testing.T) {
	c := newClient(t)
	c.login()
	p := c.project()
	token := p["token"].(string)

	minute := time.Now().Add(-5*time.Minute).Unix() / 60 * 60
	payload := protocol.Payload{BatchID: "abc", Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent", Sources: []string{"nginx:app", "mysql:main"}},
		HTTP: []protocol.HTTPBucket{{Source: "nginx:app", Minute: minute, N: 7, Hist: make([]int, 15)}},
		Checks: []protocol.SourceCheck{
			{Source: "nginx:app", Items: []protocol.CheckItem{{Key: "readable", Status: "ok"}, {Key: "duration", Status: "ok"}}},
			{Source: "mysql:main", Items: []protocol.CheckItem{{Key: "connection", Status: "ok"}, {Key: "plans", Status: "off", Optional: true, Reason: "no_select"}},
				Facts: map[string]string{"user": "slowpoke@127.0.0.1"}},
		}}

	post := func(tok string) int {
		req, _ := http.NewRequest("POST", c.base+"/api/v1/ingest", gzipJSON(t, payload))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Encoding", "gzip")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := post("spk_wrong"); code != 401 {
		t.Fatalf("wrong token: %d", code)
	}
	if code := post(token); code != 202 {
		t.Fatalf("ingest: %d", code)
	}
	if err := c.srv.Worker.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}

	code, body := c.do("GET", "/api/projects/shop/hosts", nil)
	hosts := body["hosts"].([]any)
	if code != 200 || len(hosts) != 1 || hosts[0].(map[string]any)["online"] != true {
		t.Fatalf("%d %v", code, body)
	}
	hostID := int(hosts[0].(map[string]any)["id"].(float64))
	if summary, _ := hosts[0].(map[string]any)["integrations"].(map[string]any); summary["total"] != float64(2) || summary["complete"] != float64(1) || summary["partial"] != float64(1) {
		t.Fatalf("integrations summary %v", hosts[0])
	}
	code, detail := c.do("GET", fmt.Sprintf("/api/projects/shop/hosts/%d?window=24h", hostID), nil)
	if code != 200 || detail["host"] == nil {
		t.Fatalf("host detail %d %v", code, detail)
	}
	if checks, _ := detail["checks"].([]any); len(checks) != 2 || checks[0].(map[string]any)["status"] == nil {
		t.Fatalf("host checks %v", detail["checks"])
	}
	// The map can be renamed by the team.
	if code, _ := c.do("PUT", "/api/projects/shop/topology/labels", map[string]any{"node_id": "host:web-1", "label": "Macchina legacy"}); code != 200 {
		t.Fatalf("rename: %d", code)
	}
	_, graph := c.do("GET", "/api/projects/shop/topology", nil)
	renamed := false
	for _, n := range graph["nodes"].([]any) {
		m := n.(map[string]any)
		if m["id"] == "host:web-1" && m["name"] == "Macchina legacy" && m["default_name"] == "web-1" {
			renamed = true
		}
	}
	if !renamed {
		t.Fatalf("renamed node missing: %v", graph["nodes"])
	}
	if code, _ := c.do("PUT", "/api/projects/shop/topology/labels", map[string]any{"node_id": "", "label": "x"}); code != 400 {
		t.Fatalf("empty node id must be refused: %d", code)
	}
	if code, _ := c.do("GET", "/api/projects/shop/hosts/99999", nil); code != 404 {
		t.Fatalf("unknown host: %d", code)
	}

	// Deploy marker from CI.
	req, _ := http.NewRequest("POST", c.base+"/api/v1/deploys", strings.NewReader(`{"version":"v1.2.0","commit":"abcdef1234","author":"ada"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("deploy: %d", res.StatusCode)
	}
	_, deploys := c.do("GET", "/api/projects/shop/deploys", nil)
	if list := deploys["deploys"].([]any); len(list) != 1 || list[0].(map[string]any)["version"] != "v1.2.0" {
		t.Fatalf("%v", deploys)
	}
}

func TestSettingsAreValidated(t *testing.T) {
	c := newClient(t)
	c.login()
	p := c.project()
	settings := p["settings"].(map[string]any)
	settings["missions"].(map[string]any)["window_hours"] = 0
	if code, body := c.do("PATCH", "/api/projects/shop", map[string]any{"settings": settings}); code != 422 || !strings.Contains(body["error"].(string), "missions") {
		t.Fatalf("%d %v", code, body)
	}
	settings["missions"].(map[string]any)["window_hours"] = 1
	if code, body := c.do("PATCH", "/api/projects/shop", map[string]any{"settings": settings, "name": "Shop EU"}); code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	_, body := c.do("GET", "/api/projects/shop", nil)
	if body["project"].(map[string]any)["name"] != "Shop EU" {
		t.Fatalf("%v", body)
	}
}

func TestOverviewSlowAndQueryDoctor(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")

	sql := "select * from orders where note like '%gift%'"
	fp := fingerprint.Of(sql)
	hash := fingerprint.Hash(fp)
	minute := time.Now().Add(-10*time.Minute).Unix() / 60 * 60
	hist := make([]int, 15)
	hist[2] = 100
	_, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{
		BatchID: "x", Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent", Sources: []string{"nginx:app", "mysql-slowlog:mysql-slowlog"}},
		HTTP: []protocol.HTTPBucket{{Source: "nginx:app", Minute: minute, N: 100, N2: 3, Sum: 30, Max: 4, Hist: hist,
			Endpoints: map[string]protocol.EndpointStat{"GET /search": {N: 100, Sum: 30, Max: 4, Slow: 3, SlowSum: 10}}}},
		DB: []protocol.DBBucket{{Source: "mysql-slowlog", Minute: minute, SlowN: 2, SlowSum: 6, SlowMax: 4,
			Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, Sample: sql, N: 2, Sum: 6, Max: 4, Rows: 500000}}}},
		Explains: []protocol.Explain{},
		Metrics:  []protocol.MetricBucket{{Minute: minute, Name: "invoices.pdf", Kind: "timer", Count: 2, Sum: 3, Min: 1, Max: 2, Last: 2, Hist: hist}},
		Host:     []protocol.HostSample{{Minute: minute, CPU: 30, MemUsed: 1, MemTotal: 4}},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.srv.DB.Q().Exec(ctx, `UPDATE query_fingerprints SET explain = ? WHERE hash = ?`,
		`{"engine":"mysql","plan":[{"table":"orders","type":"ALL","rows":250000}],"tables":{"orders":{"rows":250000,"columns":[{"name":"note","type":"text"}],"indexes":[]}}}`, hash)

	code, overview := c.do("GET", "/api/projects/shop/overview", nil)
	if code != 200 {
		t.Fatalf("overview %d %v", code, overview)
	}
	for _, key := range []string{"live", "today", "chart", "badges", "points", "missions", "debt", "activity", "hosts"} {
		if _, ok := overview[key]; !ok {
			t.Errorf("overview misses %q", key)
		}
	}
	// The panel names the season in the reader's language: it needs the month, not an English name.
	if start, _ := overview["points"].(map[string]any)["season_start"].(string); !regexp.MustCompile(`^\d{4}-\d{2}$`).MatchString(start) {
		t.Errorf("points.season_start = %q, want YYYY-MM", start)
	}

	code, slow := c.do("GET", "/api/projects/shop/slow?window=24h", nil)
	if code != 200 || len(slow["endpoints"].([]any)) != 1 || len(slow["queries"].([]any)) != 1 {
		t.Fatalf("slow %d %v", code, slow)
	}

	code, detail := c.do("GET", "/api/projects/shop/queries/"+hash, nil)
	if code != 200 {
		t.Fatalf("query %d %v", code, detail)
	}
	diagnoses := detail["diagnoses"].([]any)
	if len(diagnoses) == 0 || diagnoses[0].(map[string]any)["code"] != "full_scan" {
		t.Fatalf("doctor %v", diagnoses)
	}
	if !strings.Contains(detail["prompt"].(string), sql) {
		t.Fatal("prompt")
	}
	if code, _ := c.do("GET", "/api/projects/shop/queries/0000000000000000", nil); code != 404 {
		t.Fatalf("unknown query: %d", code)
	}

	code, m := c.do("GET", "/api/projects/shop/metrics?window=24h", nil)
	if list := m["metrics"].([]any); code != 200 || len(list) != 1 || len(list[0].(map[string]any)["series"].([]any)) != 1 {
		t.Fatalf("metrics %d %v", code, m)
	}
	for _, path := range []string{"missions", "heatmap", "jobs", "deploys", "debt-report", "metrics?window=7d", "topology"} {
		if code, body := c.do("GET", "/api/projects/shop/"+path, nil); code != 200 {
			t.Errorf("%s: %d %v", path, code, body)
		}
	}
}

func TestSPAFallbackAndHealth(t *testing.T) {
	c := newClient(t)
	for path, want := range map[string]string{"/": "slowpoke", "/projects/shop/missions": "slowpoke", "/assets/app.js": "js"} {
		res, err := http.Get(c.base + path)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if !strings.Contains(string(raw), want) {
			t.Errorf("%s: %q", path, raw)
		}
		if res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: security headers missing", path)
		}
	}
	if res, _ := http.Get(c.base + "/assets/missing-123.js"); res.StatusCode != 404 {
		t.Fatalf("missing asset must 404, got %d", res.StatusCode)
	}
	res, _ := http.Get(c.base + "/api/nope")
	if res.StatusCode != 404 {
		t.Fatalf("unknown api route must 404, got %d", res.StatusCode)
	}
	res, _ = http.Get(c.base + "/healthz")
	if res.StatusCode != 200 {
		t.Fatal("health")
	}
}

func TestIngestBackpressure(t *testing.T) {
	c := newClient(t)
	c.login()
	token := c.project()["token"].(string)
	c.srv.MaxQueue = 2
	codes := []int{}
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest("POST", c.base+"/api/v1/ingest", strings.NewReader(`{"batch_id":"x"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		codes = append(codes, res.StatusCode)
		time.Sleep(1100 * time.Millisecond) // depth cache
	}
	if codes[0] != 202 || codes[1] != 202 || codes[2] != 429 {
		t.Fatalf("codes %v", codes)
	}
}

func TestMissionDetailAndTaskCreation(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")

	var issueBody map[string]any
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&issueBody)
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"number":7,"html_url":"https://github.com/acme/shop/issues/7"}`)
	}))
	defer tracker.Close()

	p.Settings.Integrations.Tasks = []tasks.Integration{{ID: "gh", Name: "Shop repo", Provider: "github", Token: "ghp_secret", Repo: "acme/shop", BaseURL: tracker.URL}}
	if err := c.srv.Projects.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	id, _ := c.srv.DB.Q().InsertID(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, points, status, good_windows, person, opened_at, checked_at)
		VALUES (?, 'mission', 'endpoint', 'nginx:app GET /search', 'GET /search', 120, 30, 'open', 0, '', ?, ?)`, p.ID, time.Now().Unix(), time.Now().Unix())

	// Secrets never reach the browser, and saving the masked value keeps them.
	_, got := c.do("GET", "/api/projects/shop", nil)
	settings := got["project"].(map[string]any)["settings"].(map[string]any)
	if tok := settings["integrations"].(map[string]any)["tasks"].([]any)[0].(map[string]any)["token"]; tok != project.SecretMask {
		t.Fatalf("token leaked: %v", tok)
	}
	if code, body := c.do("PATCH", "/api/projects/shop", map[string]any{"settings": settings}); code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	stored, _ := c.srv.Projects.BySlug(ctx, "shop")
	if stored.Settings.Integrations.Tasks[0].Token != "ghp_secret" {
		t.Fatal("masked save must keep the stored token")
	}

	code, detail := c.do("GET", fmt.Sprintf("/api/projects/shop/missions/%d", id), nil)
	if code != 200 || !strings.Contains(detail["markdown"].(string), "GET /search") || len(detail["integrations"].([]any)) != 1 {
		t.Fatalf("%d %v", code, detail)
	}

	code, created := c.do("POST", fmt.Sprintf("/api/projects/shop/missions/%d/tasks", id), map[string]string{"integration": "gh", "assignee": "ada"})
	if code != 201 || created["url"] != "https://github.com/acme/shop/issues/7" {
		t.Fatalf("%d %v", code, created)
	}
	if issueBody["title"] != "[Slowpoke] GET /search" || issueBody["assignees"].([]any)[0] != "ada" {
		t.Fatalf("issue %v", issueBody)
	}
	_, detail = c.do("GET", fmt.Sprintf("/api/projects/shop/missions/%d", id), nil)
	if len(detail["context"].(map[string]any)["tasks"].([]any)) != 1 {
		t.Fatal("task not linked to the mission")
	}
	if code, _ := c.do("POST", fmt.Sprintf("/api/projects/shop/missions/%d/tasks", id), map[string]string{"integration": "nope"}); code != 422 {
		t.Fatalf("unknown integration: %d", code)
	}
	if code, _ := c.do("GET", "/api/projects/shop/missions/999999", nil); code != 404 {
		t.Fatal("unknown mission")
	}
}

func TestAgentTokensAndHostRemoval(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()

	code, created := c.do("POST", "/api/projects/shop/tokens", map[string]string{"kind": "agent", "name": "api-1"})
	if code != 201 || !strings.HasPrefix(created["secret"].(string), "spa_") {
		t.Fatalf("%d %v", code, created)
	}
	secret := created["secret"].(string)

	minute := time.Now().Add(-5*time.Minute).Unix() / 60 * 60
	payload := protocol.Payload{BatchID: "t1", Agent: protocol.AgentInfo{Host: "api-1", Kind: "agent"}, HTTP: []protocol.HTTPBucket{{Source: "nginx:api", Minute: minute, N: 3, Hist: make([]int, 15)}}}
	post := func(batch string) int {
		payload.BatchID = batch
		req, _ := http.NewRequest("POST", c.base+"/api/v1/ingest", gzipJSON(t, payload))
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Encoding", "gzip")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := post("t1"); code != 202 {
		t.Fatalf("agent token ingest: %d", code)
	}
	if err := c.srv.Worker.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, hosts := c.do("GET", "/api/projects/shop/hosts", nil)
	hostID := int(hosts["hosts"].([]any)[0].(map[string]any)["id"].(float64))

	code, removed := c.do("DELETE", fmt.Sprintf("/api/projects/shop/hosts/%d", hostID), nil)
	if code != 200 || removed["token_revoked"] != true {
		t.Fatalf("%d %v", code, removed)
	}
	if code := post("t2"); code != 401 {
		t.Fatalf("revoked agent must be rejected, got %d", code)
	}
	_, list := c.do("GET", "/api/projects/shop/tokens", nil)
	if tok := list["tokens"].([]any)[0].(map[string]any); tok["revoked_at"] == nil || tok["prefix"] == nil {
		t.Fatalf("%v", tok)
	}
}

func TestEffortROIGoalDeliveryAndHotspots(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")
	now := time.Now().Unix()
	id, _ := c.srv.DB.Q().InsertID(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, points, status, good_windows, person, opened_at, checked_at)
		VALUES (?, 'mission', 'php', 'curl_exec()', 'curl_exec()', 400, 10, 'open', 0, '', ?, ?)`, p.ID, now, now)

	_, list := c.do("GET", "/api/projects/shop/missions", nil)
	m := list["open"].([]any)[0].(map[string]any)
	if m["effort"] != "s" || m["effort_auto"] != true || m["roi"].(float64) != 100 || list["goal"] == nil {
		t.Fatalf("%v", m)
	}
	if code, _ := c.do("PATCH", fmt.Sprintf("/api/projects/shop/missions/%d", id), map[string]string{"effort": "huge"}); code != 422 {
		t.Fatalf("invalid effort: %d", code)
	}
	if code, _ := c.do("PATCH", fmt.Sprintf("/api/projects/shop/missions/%d", id), map[string]string{"effort": "l"}); code != 200 {
		t.Fatal("set effort")
	}
	_, list = c.do("GET", "/api/projects/shop/missions", nil)
	m = list["open"].([]any)[0].(map[string]any)
	if m["effort"] != "l" || m["effort_auto"] != false || m["roi"].(float64) != 10 {
		t.Fatalf("%v", m)
	}
	_, detail := c.do("GET", fmt.Sprintf("/api/projects/shop/missions/%d", id), nil)
	if !strings.Contains(detail["markdown"].(string), "Effort: L (set by the team") {
		t.Fatalf("%s", detail["markdown"])
	}
	if code, deploys := c.do("GET", "/api/projects/shop/deploys", nil); code != 200 || deploys["health"] == nil {
		t.Fatalf("%d %v", code, deploys)
	}
	if code, h := c.do("GET", "/api/projects/shop/hotspots", nil); code != 200 || h["hotspots"] == nil {
		t.Fatalf("%d %v", code, h)
	}
	if code, r := c.do("GET", "/api/projects/shop/debt-report", nil); code != 200 || r["delivery"] == nil || r["goal"] == nil {
		t.Fatalf("%d %v", code, r)
	}
}

// The same metric name from two machines is two readings in the panel, never one made-up number.
func TestMetricsAreShownPerMachine(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")
	minute := time.Now().Add(-10*time.Minute).Unix() / 60 * 60
	for i, host := range []string{"cache-1", "cache-2"} {
		memory := float64(100 * (i + 1))
		_, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{
			BatchID: host, Agent: protocol.AgentInfo{Host: host, Kind: "agent"},
			Metrics: []protocol.MetricBucket{
				{Minute: minute, Name: "redis.cache.memory_bytes", Kind: "gauge", Count: 1, Sum: memory, Min: memory, Max: memory, Last: memory},
				{Minute: minute, Name: "orders.created", Kind: "counter", Count: 2, Sum: 2},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	code, body := c.do("GET", "/api/projects/shop/metrics?window=24h", nil)
	if code != 200 {
		t.Fatalf("metrics %d %v", code, body)
	}
	byName := map[string]map[string]any{}
	for _, m := range body["metrics"].([]any) {
		item := m.(map[string]any)
		byName[item["name"].(string)] = item
	}
	gauge := byName["redis.cache.memory_bytes"]
	hosts, _ := gauge["hosts"].([]any)
	if len(hosts) != 2 {
		t.Fatalf("a gauge of two machines is read per machine: %v", gauge)
	}
	first := hosts[0].(map[string]any)
	second := hosts[1].(map[string]any)
	if first["host"] != "cache-1" || first["last"].(float64) != 100 || second["host"] != "cache-2" || second["last"].(float64) != 200 {
		t.Fatalf("hosts %v", hosts)
	}
	if len(first["series"].([]any)) != 1 {
		t.Fatalf("one chart per machine: %v", first)
	}
	if _, ok := gauge["last"]; ok {
		t.Fatalf("no single last value for two machines: %v", gauge)
	}
	if _, ok := gauge["series"]; ok {
		t.Fatalf("no merged chart for two machines: %v", gauge)
	}
	counter := byName["orders.created"]
	if counter["count"].(float64) != 4 || len(counter["series"].([]any)) != 1 {
		t.Fatalf("counters of many machines are one total: %v", counter)
	}
	if len(counter["hosts"].([]any)) != 2 {
		t.Fatalf("a counter still says which machines it comes from: %v", counter)
	}
}

// Machines can be arranged on the map by hand, and the arrangement is saved.
func TestTopologyPlacesAreSavedAndReset(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")
	_, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{
		BatchID: "b1", Agent: protocol.AgentInfo{Host: "db-1", Kind: "agent", Sources: []string{"mysql:main"}},
		Network: &protocol.NetworkSnapshot{Addresses: []string{"10.0.0.1"}, Listening: []protocol.ListeningPort{{Port: 3306, Service: "mysql"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	place := func() (float64, float64, bool) {
		code, body := c.do("GET", "/api/projects/shop/topology", nil)
		if code != 200 {
			t.Fatalf("topology %d %v", code, body)
		}
		for _, raw := range body["nodes"].([]any) {
			n := raw.(map[string]any)
			if n["id"] == "host:db-1" {
				x, ok := n["x"].(float64)
				y, _ := n["y"].(float64)
				return x, y, ok
			}
		}
		t.Fatal("machine missing from the map")
		return 0, 0, false
	}
	if _, _, moved := place(); moved {
		t.Fatal("a machine nobody moved has no place of its own")
	}

	if code, body := c.do("PUT", "/api/projects/shop/topology/places", map[string]any{"node_id": "host:db-1", "x": 640, "y": 220}); code != 200 {
		t.Fatalf("save %d %v", code, body)
	}
	if x, y, moved := place(); !moved || x != 640 || y != 220 {
		t.Fatalf("saved place: %v %v %v", x, y, moved)
	}
	// Renaming the machine keeps where it was put.
	if code, _ := c.do("PUT", "/api/projects/shop/topology/labels", map[string]string{"node_id": "host:db-1", "label": "Main database"}); code != 200 {
		t.Fatal("rename")
	}
	if x, _, moved := place(); !moved || x != 640 {
		t.Fatal("rename lost the place")
	}

	if code, body := c.do("DELETE", "/api/projects/shop/topology/places", nil); code != 200 {
		t.Fatalf("reset %d %v", code, body)
	}
	if _, _, moved := place(); moved {
		t.Fatal("the whole map is back to the automatic layout")
	}
	if code, body := c.do("PUT", "/api/projects/shop/topology/places", map[string]any{"node_id": "", "x": 1, "y": 2}); code != 400 {
		t.Fatalf("a place needs a node: %d %v", code, body)
	}
}

// The wins are counted by Slowpoke itself: the window against the one before it.
func TestWinsAndComparisonWithThePreviousWindow(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")
	now := time.Now()
	fp := "select * from orders where note like ?"
	hash := fingerprint.Hash(fp)
	hist := make([]int, 15)
	hist[2] = 10
	send := func(batch string, hoursAgo float64, querySeconds, endpointSeconds float64) {
		minute := now.Add(-time.Duration(hoursAgo*float64(time.Hour))).Unix() / 60 * 60
		_, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{
			BatchID: batch, Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent", Sources: []string{"nginx:app", "mysql-slowlog:mysql-slowlog"}},
			DB: []protocol.DBBucket{{Source: "mysql-slowlog", Minute: minute, SlowN: 1, SlowSum: querySeconds, SlowMax: querySeconds,
				Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, Sample: fp, N: 1, Sum: querySeconds, Max: querySeconds}}}},
			HTTP: []protocol.HTTPBucket{{Source: "nginx:app", Minute: minute, N: 10, Sum: endpointSeconds, Max: 2, Hist: hist,
				Endpoints: map[string]protocol.EndpointStat{"GET /search": {N: 10, Sum: endpointSeconds, Slow: 2, SlowSum: endpointSeconds}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	send("old", 50, 1, 1)
	send("before", 36, 120, 20)
	send("now", 6, 30, 60)

	code, body := c.do("GET", "/api/projects/shop/slow?window=24h", nil)
	if code != 200 {
		t.Fatalf("slow %d %v", code, body)
	}
	compare := body["compare"].(map[string]any)
	if compare["comparable"] != true {
		t.Fatalf("two days of data are comparable: %v", compare)
	}
	query := body["queries"].([]any)[0].(map[string]any)
	if query["before_seconds_per_day"].(float64) != 120 || query["saved_seconds_per_day"].(float64) != 90 {
		t.Fatalf("the query that was fixed: %v", query)
	}
	endpoint := body["endpoints"].([]any)[0].(map[string]any)
	if endpoint["saved_seconds_per_day"].(float64) != -40 {
		t.Fatalf("the endpoint that got worse: %v", endpoint)
	}
	wins := body["wins"].(map[string]any)
	if len(wins["improved"].([]any)) != 1 || wins["improved"].([]any)[0].(map[string]any)["target"] != hash {
		t.Fatalf("improved: %v", wins)
	}
	if len(wins["worse"].([]any)) != 1 || wins["saved_seconds_per_day"].(float64) != 50 {
		t.Fatalf("wins: %v", wins)
	}

	// The overview carries the same wins over the last seven days, for the game.
	code, overview := c.do("GET", "/api/projects/shop/overview", nil)
	if code != 200 {
		t.Fatalf("overview %d", code)
	}
	if _, ok := overview["wins"]; !ok {
		t.Fatal("the overview has no wins")
	}
}

// With seventeen machines, "slow things" has to be readable one machine at a time.
func TestSlowThingsFilteredByMachine(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")
	minute := time.Now().Add(-10*time.Minute).Unix() / 60 * 60
	hist := make([]int, 15)
	hist[2] = 10
	send := func(host, site string, seconds float64) {
		_, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{
			BatchID: host, Agent: protocol.AgentInfo{Host: host, Kind: "agent", Sources: []string{"nginx:" + site, "php-fpm:www"}},
			HTTP: []protocol.HTTPBucket{{Source: "nginx:" + site, Minute: minute, N: 10, Sum: seconds, Max: 2, Hist: hist,
				Endpoints: map[string]protocol.EndpointStat{"GET /orders": {N: 10, Sum: seconds, Slow: 2, SlowSum: seconds}}}},
			PHP: []protocol.PHPBucket{{Source: "php-fpm:www", Minute: minute, N: 1, Stalls: []protocol.PHPStall{{Frame: "curl_exec()", Code: "app/X.php:1", N: 1}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	send("web-1", "shop.example.com", 30)
	send("web-2", "api.example.com", 90)

	// Each machine also has its own database, with its own slow statement.
	query := func(host, db, sql string, seconds float64) {
		hash := fingerprint.Hash(fingerprint.Of(sql))
		_, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{
			// A real agent always reports every source it has: the list replaces the previous one.
			BatchID: host + db, Agent: protocol.AgentInfo{Host: host, Kind: "agent",
				Sources: []string{"nginx:" + map[string]string{"web-1": "shop.example.com", "web-2": "api.example.com"}[host], "php-fpm:www", "mysql-slowlog:mysql-slowlog"}},
			DB: []protocol.DBBucket{{Source: "mysql-slowlog", Minute: minute, SlowN: 1, SlowSum: seconds, SlowMax: seconds,
				Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fingerprint.Of(sql), Sample: sql, Database: db, N: 1, Sum: seconds, Max: seconds}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	query("web-1", "shop", "select * from orders where note like 'x'", 12)
	query("web-2", "billing", "select * from invoices where token = 'y'", 20)

	all := func(path string) map[string]any {
		code, body := c.do("GET", path, nil)
		if code != 200 {
			t.Fatalf("%s: %d %v", path, code, body)
		}
		return body
	}
	if n := len(all("/api/projects/shop/slow?window=24h")["endpoints"].([]any)); n != 2 {
		t.Fatalf("without a filter both machines are there: %d", n)
	}

	body := all("/api/projects/shop/slow?window=24h&host=web-2")
	endpoints := body["endpoints"].([]any)
	if len(endpoints) != 1 || endpoints[0].(map[string]any)["source"] != "nginx:api.example.com" {
		t.Fatalf("only the machine asked for: %v", endpoints)
	}
	if body["host"] != "web-2" {
		t.Fatalf("the answer says which machine it is about: %v", body["host"])
	}
	// A machine with no measures of its own answers empty, not everything.
	if n := len(all("/api/projects/shop/slow?window=24h&host=nobody")["endpoints"].([]any)); n != 0 {
		t.Fatalf("unknown machine: %d endpoints", n)
	}
	// The queries are the machine's too, and the answer lists the databases to filter by.
	queries := body["queries"].([]any)
	if len(queries) != 1 || !strings.Contains(queries[0].(map[string]any)["fingerprint"].(string), "invoices") {
		t.Fatalf("only the statements that machine saw: %v", queries)
	}
	if queries[0].(map[string]any)["db"] != "billing" {
		t.Fatalf("every statement says which database it belongs to: %v", queries[0])
	}
	dbs := body["databases"].([]any)
	if len(dbs) != 2 {
		t.Fatalf("the databases to filter by: %v", dbs)
	}
	// And by database alone, without naming a machine.
	byDB := all("/api/projects/shop/slow?window=24h&db=shop")["queries"].([]any)
	if len(byDB) != 1 || !strings.Contains(byDB[0].(map[string]any)["fingerprint"].(string), "orders") {
		t.Fatalf("filtered by database: %v", byDB)
	}
}

// The jobs page is about work nobody waits for: what it costs a day, how often it fails, and
// whether it got slower than it was last week - which the average of this week alone never says.
func TestJobsCarryFailureRateCostPerDayAndTheWeekBefore(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")

	// Last week: 100 runs of 1 s. This week: 100 runs of 2 s, 10 of them failed.
	past := time.Now().Add(-10 * 24 * time.Hour).Unix() / 60 * 60
	nowish := time.Now().Add(-2 * time.Hour).Unix() / 60 * 60
	for _, b := range []struct {
		minute       int64
		n, failed    int
		sum, max     float64
	}{{past, 100, 0, 100, 2}, {nowish, 100, 10, 200, 9}} {
		if _, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{
			BatchID: fmt.Sprintf("jobs-%d", b.minute), Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent"},
			Jobs: []protocol.JobBucket{{Minute: b.minute, Kind: "job", Name: "App\\Jobs\\SendInvoice", N: b.n, Failed: b.failed, Sum: b.sum, Max: b.max}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	code, body := c.do("GET", "/api/projects/shop/jobs", nil)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	list := body["jobs"].([]any)
	if len(list) != 1 {
		t.Fatalf("jobs %v", list)
	}
	job := list[0].(map[string]any)
	if job["runs"].(float64) != 100 || job["failed"].(float64) != 10 {
		t.Fatalf("runs and failures: %v", job)
	}
	if job["failure_rate"].(float64) != 10 {
		t.Fatalf("one run in ten failed, the page says %v", job["failure_rate"])
	}
	if got := job["seconds_per_day"].(float64); got < 28 || got > 29 {
		t.Fatalf("200 s over a week is about 28.6 s a day, got %v", got)
	}
	if job["avg"].(float64) != 2 || job["was_avg"].(float64) != 1 {
		t.Fatalf("this week against the one before: %v", job)
	}
	if job["avg_change"].(float64) != 100 {
		t.Fatalf("it takes twice as long as last week: %v", job["avg_change"])
	}
	if job["last_at"].(float64) <= 0 {
		t.Fatalf("no last run: %v", job["last_at"])
	}
}

// Production, 2026-10-01 01:37 in Rome: the panel showed 100 "today so far" out of 1.3 hours of
// night, +32.5 against the day before. The big number is the last 24 hours; the calendar day is a footnote.
func TestOverviewIndexIsTheLast24HoursNotTheNight(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")
	now := time.Now().In(p.Location())
	today, yesterday := now.Format("2006-01-02"), now.AddDate(0, 0, -1).Format("2006-01-02")
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO score_days (project_id, day, score, partial, closed, hours, components, updated_at) VALUES (?, ?, 67.5, FALSE, TRUE, 22.6, '[]', 0)", []any{p.ID, yesterday}},
		{"INSERT INTO score_days (project_id, day, score, partial, closed, hours, components, updated_at) VALUES (?, ?, 100, TRUE, FALSE, 1.3, '[]', 0)", []any{p.ID, today}},
		{`INSERT INTO score_window (project_id, score, partial, hours, components, updated_at) VALUES (?, 66, FALSE, 23.9, '[{"key":"p95","weight":20,"points":12,"lost":8,"available":true}]', 0)`, []any{p.ID}},
	} {
		if _, err := c.srv.DB.Q().Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}

	_, overview := c.do("GET", "/api/projects/shop/overview", nil)
	idx := overview["today"].(map[string]any)
	if idx["score"] != 66.0 || idx["hours"] != 23.9 || idx["window"] != "24h" {
		t.Fatalf("the index must be the last 24 hours: %v", idx)
	}
	if idx["delta"] != -1.5 {
		t.Fatalf("delta against the last closed day: %v", idx["delta"])
	}
	if lvl := idx["level"].(map[string]any); lvl["name"] == "Platinum" {
		t.Fatalf("level from the window, not the night: %v", lvl)
	}
	if comps := idx["components"].([]any); len(comps) != 1 {
		t.Fatalf("components from the window: %v", comps)
	}
	soFar := idx["so_far"].(map[string]any)
	if soFar["score"] != 100.0 || soFar["hours"] != 1.3 {
		t.Fatalf("today so far stays available as a footnote: %v", soFar)
	}

	// The project list and the assistant read the same number.
	_, list := c.do("GET", "/api/projects", nil)
	if s := list["projects"].([]any)[0].(map[string]any)["score"]; s != 66.0 {
		t.Fatalf("project list score %v", s)
	}
}
