package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/missionctx"
)

// The doctor asks for an index on a table the team cannot touch: the team mutes that advice on
// that query, and it leaves the query page, the prompt, the debt report and the brief read by
// the coding assistant, while it stays readable apart with who muted it and why.
func TestADiagnosisTheTeamCannotFollowIsMuted(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")

	sql := "select * from orders where note like '%gift%'"
	fp := fingerprint.Of(sql)
	hash := fingerprint.Hash(fp)
	minute := time.Now().Add(-10*time.Minute).Unix() / 60 * 60
	_, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{
		BatchID: "m1", Agent: protocol.AgentInfo{Host: "db-1", Kind: "agent", Sources: []string{"mysql-slowlog:mysql-slowlog"}},
		DB: []protocol.DBBucket{{Source: "mysql-slowlog", Minute: minute, SlowN: 200, SlowSum: 600, SlowMax: 4,
			Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, Sample: sql, N: 200, Sum: 600, Max: 4, Rows: 50000000}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.srv.DB.Q().Exec(ctx, `UPDATE query_fingerprints SET explain = ? WHERE hash = ?`,
		`{"engine":"mysql","plan":[{"table":"orders","type":"ALL","rows":250000}],"tables":{"orders":{"rows":250000,"columns":[{"name":"note","type":"text"}],"indexes":[]}}}`, hash)

	_, detail := c.do("GET", "/api/projects/shop/queries/"+hash, nil)
	first := detail["diagnoses"].([]any)[0].(map[string]any)
	if first["code"] != "full_scan" || first["table"] != "orders" {
		t.Fatalf("doctor %v", first)
	}
	title := first["title"].(string)
	_, report := c.do("GET", "/api/projects/shop/debt-report", nil)
	if f := findingOf(report, hash); f == nil || f["diagnosis"].(map[string]any)["code"] != "full_scan" {
		t.Fatalf("the report leads with the full scan before muting: %v", report["findings"])
	}

	if code, body := c.do("POST", "/api/projects/shop/queries/"+hash+"/mutes", map[string]any{"table": "orders"}); code != 422 {
		t.Fatalf("no code: %d %v", code, body)
	}
	if code, body := c.do("POST", "/api/projects/shop/queries/0000000000000000/mutes", map[string]any{"code": "full_scan"}); code != 404 {
		t.Fatalf("unknown query: %d %v", code, body)
	}
	code, body := c.do("POST", "/api/projects/shop/queries/"+hash+"/mutes", map[string]any{"code": "full_scan", "table": "orders", "note": "vendor schema"})
	if code != 201 {
		t.Fatalf("mute %d %v", code, body)
	}
	muteID := int(body["mute"].(map[string]any)["id"].(float64))

	_, detail = c.do("GET", "/api/projects/shop/queries/"+hash, nil)
	for _, d := range detail["diagnoses"].([]any) {
		if d.(map[string]any)["code"] == "full_scan" {
			t.Fatalf("still speaking: %v", d)
		}
	}
	muted := detail["muted_diagnoses"].([]any)
	if len(muted) != 1 {
		t.Fatalf("muted %v", muted)
	}
	m := muted[0].(map[string]any)
	if m["code"] != "full_scan" || m["mute"].(map[string]any)["note"] != "vendor schema" || m["mute"].(map[string]any)["created_by"] != "ada@example.com" {
		t.Fatalf("muted %v", m)
	}
	if strings.Contains(detail["prompt"].(string), title) {
		t.Fatalf("the prompt still asks for it:\n%s", detail["prompt"])
	}
	_, report = c.do("GET", "/api/projects/shop/debt-report", nil)
	if f := findingOf(report, hash); f != nil && f["diagnosis"].(map[string]any)["code"] == "full_scan" {
		t.Fatalf("the report still leads with it: %v", f)
	}
	q, err := missionctx.LoadQuery(ctx, c.srv.DB, p, hash, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range q.Diagnoses {
		if d.Code == "full_scan" {
			t.Fatal("the brief for the assistant still asks for it")
		}
	}
	if len(q.Muted) != 1 {
		t.Fatalf("the brief does not say what the team muted: %v", q.Muted)
	}

	if code, _ := c.do("DELETE", fmt.Sprintf("/api/projects/shop/queries/%s/mutes/%d", hash, muteID), nil); code != 200 {
		t.Fatalf("unmute %d", code)
	}
	_, detail = c.do("GET", "/api/projects/shop/queries/"+hash, nil)
	if detail["diagnoses"].([]any)[0].(map[string]any)["code"] != "full_scan" || len(detail["muted_diagnoses"].([]any)) != 0 {
		t.Fatalf("unmuted %v", detail)
	}
}

func findingOf(report map[string]any, hash string) map[string]any {
	for _, f := range report["findings"].([]any) {
		if m := f.(map[string]any); m["hash"] == hash {
			return m
		}
	}
	return nil
}

// A staging machine stays installed but nobody will fix what it reports: muted, it keeps
// counting and keeps its numbers, and stops asking for attention (problems, an old agent).
func TestAMutedMachineKeepsCountingAndStopsAskingForAttention(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")
	minute := time.Now().Add(-10*time.Minute).Unix() / 60 * 60
	hist := make([]int, 15)
	hist[protocol.HistogramIndex(3)] = 10
	_, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{BatchID: "h1",
		Agent: protocol.AgentInfo{Host: "staging", Kind: "agent", Version: "0.0.1", Sources: []string{"nginx:shop", "mysql:main"}},
		HTTP: []protocol.HTTPBucket{{Source: "nginx:shop", Minute: minute, N: 10, Sum: 30, Max: 4, N2: 10, Hist: hist,
			Endpoints: map[string]protocol.EndpointStat{"GET /search": {N: 10, Sum: 30, Max: 4, Slow: 10, SlowSum: 30, Hist: hist}}}},
		Checks: []protocol.SourceCheck{{Source: "mysql:main", Items: []protocol.CheckItem{
			{Key: "connection", Status: "ok"}, {Key: "statistics", Status: "error", Reason: "performance_schema_off"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, hosts := c.do("GET", "/api/projects/shop/hosts", nil)
	host := hosts["hosts"].([]any)[0].(map[string]any)
	id := int(host["id"].(float64))
	if host["muted"] != nil {
		t.Fatalf("muted from the start: %v", host)
	}
	_, updates := c.do("GET", "/api/updates", nil)
	if updates["behind"].(float64) != 1 {
		t.Fatalf("the old agent is not reported: %v", updates)
	}
	_, before := c.do("GET", "/api/projects/shop/slow?window=24h", nil)

	if code, _ := c.do("POST", "/api/projects/shop/hosts/999999/mute", map[string]any{}); code != 404 {
		t.Fatalf("unknown machine: %d", code)
	}
	code, body := c.do("POST", fmt.Sprintf("/api/projects/shop/hosts/%d/mute", id), map[string]any{"note": "staging"})
	if code != 200 || body["muted"].(map[string]any)["note"] != "staging" {
		t.Fatalf("mute %d %v", code, body)
	}

	_, hosts = c.do("GET", "/api/projects/shop/hosts", nil)
	host = hosts["hosts"].([]any)[0].(map[string]any)
	if m, _ := host["muted"].(map[string]any); m == nil || m["by"] != "ada@example.com" || m["note"] != "staging" {
		t.Fatalf("hosts %v", host)
	}
	_, one := c.do("GET", fmt.Sprintf("/api/projects/shop/hosts/%d", id), nil)
	if one["muted"] == nil {
		t.Fatalf("the machine page does not say it is muted: %v", one["host"])
	}
	_, overview := c.do("GET", "/api/projects/shop/overview", nil)
	if h := overview["hosts"].([]any)[0].(map[string]any); h["muted"] == nil {
		t.Fatalf("overview %v", h)
	}
	_, updates = c.do("GET", "/api/updates", nil)
	if updates["behind"].(float64) != 0 || updates["machines"].([]any)[0].(map[string]any)["muted"] != true {
		t.Fatalf("a muted machine still asks for an update: %v", updates)
	}
	_, after := c.do("GET", "/api/projects/shop/slow?window=24h", nil)
	if fmt.Sprint(after["endpoints"]) != fmt.Sprint(before["endpoints"]) {
		t.Fatalf("muting changed the numbers:\n%v\n%v", before["endpoints"], after["endpoints"])
	}

	if code, _ := c.do("DELETE", fmt.Sprintf("/api/projects/shop/hosts/%d/mute", id), nil); code != 200 {
		t.Fatalf("unmute %d", code)
	}
	_, hosts = c.do("GET", "/api/projects/shop/hosts", nil)
	if hosts["hosts"].([]any)[0].(map[string]any)["muted"] != nil {
		t.Fatal("still muted")
	}
}
