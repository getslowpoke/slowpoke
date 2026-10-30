package api

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// On the production install a slow endpoint opened from /slow had no mission (five were already
// open) and so no brief to hand to the coding assistant. The team can now open one from there:
// past the cap, below the slow line, never twice for the same thing.
func TestAMissionIsOpenedByHandFromASlowEndpoint(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")
	minute := time.Now().Add(-10*time.Minute).Unix() / 60 * 60
	hist := make([]int, 15)
	hist[protocol.HistogramIndex(8)] = 10
	slow := protocol.EndpointStat{N: 10, Sum: 80, Max: 9, Slow: 10, SlowSum: 80, Hist: hist}
	quick := protocol.EndpointStat{N: 10, Sum: 1, Max: 0.2, Hist: make([]int, 15)}
	_, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{BatchID: "1", Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent", Sources: []string{"nginx:shop"}},
		HTTP: []protocol.HTTPBucket{{Source: "nginx:shop", Minute: minute, N: 20, Sum: 81, Max: 9, N5: 20, Hist: hist,
			Endpoints: map[string]protocol.EndpointStat{"POST /api/scanner/tax-data": slow, "GET /health": quick}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The daily engine already filled its five slots.
	for i := 0; i < 5; i++ {
		_, _ = c.srv.DB.Q().Exec(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, points, status, good_windows, person, opened_at, checked_at)
			VALUES (?, 'mission', 'query', ?, 'SELECT ?', 100, 10, 'open', 0, '', ?, ?)`, p.ID, fmt.Sprintf("h%d", i), time.Now().Unix(), time.Now().Unix())
	}

	target := "nginx:shop POST /api/scanner/tax-data"
	code, body := c.do("POST", "/api/projects/shop/missions", map[string]string{"kind": "endpoint", "target": target})
	if code != 201 || body["created"] != true {
		t.Fatalf("open %d %v", code, body)
	}
	id := int64(body["id"].(float64))

	code, detail := c.do("GET", fmt.Sprintf("/api/projects/shop/missions/%d", id), nil)
	if code != 200 || detail["markdown"] == "" {
		t.Fatalf("detail %d %v", code, detail)
	}
	m := detail["context"].(map[string]any)["mission"].(map[string]any)
	if m["label"] != "POST /api/scanner/tax-data" || m["status"] != "open" {
		t.Fatalf("mission %v", m)
	}
	var baseline float64
	_ = c.srv.DB.Q().Row(ctx, "SELECT baseline FROM missions WHERE id = ?", id).Scan(&baseline)
	if baseline <= 0 {
		t.Fatalf("baseline %v: it starts from what the endpoint costs now", baseline)
	}

	// Asking again leads to the same mission.
	code, again := c.do("POST", "/api/projects/shop/missions", map[string]string{"kind": "endpoint", "target": target})
	if code != 200 || again["created"] != false || int64(again["id"].(float64)) != id {
		t.Fatalf("again %d %v", code, again)
	}

	// The endpoint page shows it.
	_, ep := c.do("GET", "/api/projects/shop/endpoints?source=nginx:shop&endpoint="+url.QueryEscape("POST /api/scanner/tax-data"), nil)
	if ms := ep["missions"].([]any); len(ms) != 1 || int64(ms[0].(map[string]any)["id"].(float64)) != id {
		t.Fatalf("endpoint missions %v", ep["missions"])
	}

	// Below the slow line is the team's call.
	if code, body := c.do("POST", "/api/projects/shop/missions", map[string]string{"kind": "endpoint", "target": "nginx:shop GET /health"}); code != 201 {
		t.Fatalf("quick endpoint %d %v", code, body)
	}

	// Something never seen, or not a kind of mission, is refused.
	if code, _ := c.do("POST", "/api/projects/shop/missions", map[string]string{"kind": "endpoint", "target": "nginx:shop GET /nowhere"}); code != 404 {
		t.Fatalf("unknown endpoint %d", code)
	}
	if code, _ := c.do("POST", "/api/projects/shop/missions", map[string]string{"kind": "query", "target": "nohash"}); code != 404 {
		t.Fatalf("unknown query %d", code)
	}
	if code, _ := c.do("POST", "/api/projects/shop/missions", map[string]string{"kind": "boss", "target": target}); code != 422 {
		t.Fatalf("bad kind %d", code)
	}
}
