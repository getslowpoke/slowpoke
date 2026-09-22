package api

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// A query of a library and a regression on an endpoint that is slow by design: the team leaves
// them out from the lists it actually looks at, and the panel keeps showing them, marked.
func TestAQueryIsLeftOutOfTheScoreAndARegressionIsWavedAway(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")
	sql := "select payload from failed_jobs where uuid = ?"
	fp := fingerprint.Of(sql)
	hash := fingerprint.Hash(fp)
	frame := "curl_exec() vendor/guzzlehttp/guzzle/src/Handler/CurlHandler.php:85"
	minute := time.Now().Add(-10*time.Minute).Unix() / 60 * 60
	_, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{BatchID: "q1",
		Agent: protocol.AgentInfo{Host: "db-1", Kind: "agent", Sources: []string{"mysql-slowlog:mysql-slowlog", "php-fpm:shop"}},
		DB: []protocol.DBBucket{{Source: "mysql-slowlog", Minute: minute, SlowN: 20, SlowSum: 100, SlowMax: 9,
			Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, Sample: sql, N: 20, Sum: 100, Max: 9}}}},
		PHP: []protocol.PHPBucket{{Source: "php-fpm:shop", Minute: minute, Stalls: []protocol.PHPStall{{Frame: frame, Code: "app/Jobs/Send.php:20", N: 5}}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, slow := c.do("GET", "/api/projects/shop/slow?window=24h", nil)
	if q := slow["queries"].([]any)[0].(map[string]any); q["hash"] != hash || q["excluded"] == true {
		t.Fatalf("query row %v", q)
	}
	_, report := c.do("GET", "/api/projects/shop/debt-report", nil)
	debtBefore := report["debt"].(map[string]any)["seconds_per_day"].(float64)

	if code, body := c.do("POST", "/api/projects/shop/exclusions", map[string]any{"kind": "query", "target": hash, "reason": "nonsense"}); code != 422 {
		t.Fatalf("bogus reason: %d %v", code, body)
	}
	code, body := c.do("POST", "/api/projects/shop/exclusions", map[string]any{"kind": "query", "target": hash, "reason": "third_party", "note": "vendor migration"})
	if code != 201 {
		t.Fatalf("exclude query: %d %v", code, body)
	}
	if code, body := c.do("POST", "/api/projects/shop/exclusions", map[string]any{"kind": "php", "target": frame, "reason": "external"}); code != 201 {
		t.Fatalf("exclude php: %d %v", code, body)
	}

	// Still in the list, marked, and out of the debt.
	_, slow = c.do("GET", "/api/projects/shop/slow?window=24h", nil)
	q := slow["queries"].([]any)[0].(map[string]any)
	if q["hash"] != hash || q["excluded"] != true {
		t.Fatalf("query row after: %v", q)
	}
	if php := slow["php"].([]any)[0].(map[string]any); php["excluded"] != true {
		t.Fatalf("php row after: %v", php)
	}
	_, report = c.do("GET", "/api/projects/shop/debt-report", nil)
	if after := report["debt"].(map[string]any)["seconds_per_day"].(float64); after >= debtBefore {
		t.Fatalf("debt %v -> %v", debtBefore, after)
	}
	if left := report["excluded"].([]any); len(left) != 2 {
		t.Fatalf("the report must show what was left out: %v", left)
	}
	_, list := c.do("GET", "/api/projects/shop/exclusions", nil)
	kinds := map[string]bool{}
	for _, e := range list["exclusions"].([]any) {
		kinds[e.(map[string]any)["kind"].(string)] = true
	}
	if !kinds["query"] || !kinds["php"] {
		t.Fatalf("exclusions %v", list["exclusions"])
	}
	_, detail := c.do("GET", "/api/projects/shop/queries/"+hash, nil)
	if detail["exclusion"] == nil {
		t.Fatal("the query page does not say it is left out")
	}

	// A regression waved away: closed with no points, and its target still counts.
	id, err := c.srv.DB.Q().InsertID(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, points, status, opened_at, checked_at)
		VALUES (?, 'boss', 'endpoint', 'nginx:shop POST /scanner', 'POST /scanner', 300, 120, 'open', ?, ?)`, p.ID, time.Now().Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if code, body := c.do("POST", fmt.Sprintf("/api/projects/shop/missions/%d/dismiss", id), map[string]any{"note": "one-off import", "days": 7}); code != 200 {
		t.Fatalf("dismiss %d %v", code, body)
	}
	_, mission := c.do("GET", fmt.Sprintf("/api/projects/shop/missions/%d", id), nil)
	if m := mission["context"].(map[string]any)["mission"].(map[string]any); m["status"] != "dismissed" {
		t.Fatalf("mission %v", m)
	}
	if code, _ := c.do("POST", fmt.Sprintf("/api/projects/shop/missions/%d/dismiss", id), map[string]any{}); code != 409 {
		t.Fatalf("a closed mission cannot be dismissed twice: %d", code)
	}
}
