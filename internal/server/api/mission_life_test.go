package api

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Everybody sees who is on a mission and whether the fix is online, and nobody takes over a
// mission somebody else is on without being told.
func TestAMissionShowsWhoIsOnItAndWhetherItIsOnline(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")
	now := time.Now().Unix()
	id, _ := c.srv.DB.Q().InsertID(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, points, status, good_windows, person, opened_at, checked_at)
		VALUES (?, 'mission', 'endpoint', 'nginx:app GET /search', 'GET /search', 120, 30, 'open', 0, '', ?, ?)`, p.ID, now, now)
	stage := func() map[string]any {
		t.Helper()
		_, list := c.do("GET", "/api/projects/shop/missions", nil)
		for _, m := range list["open"].([]any) {
			if int64(m.(map[string]any)["id"].(float64)) == id {
				return m.(map[string]any)
			}
		}
		t.Fatalf("mission not listed: %v", list)
		return nil
	}
	if s := stage(); s["stage"] != "open" {
		t.Fatalf("stage %v", s["stage"])
	}

	path := fmt.Sprintf("/api/projects/shop/missions/%d", id)
	if code, body := c.do("POST", path+"/claim", nil); code != 200 {
		t.Fatalf("claim %d %v", code, body)
	}
	if s := stage(); s["stage"] != "claimed" || s["person"] != "ada@example.com" || s["claimed_at"] == nil {
		t.Fatalf("claimed %v", s)
	}
	// Somebody else took it meanwhile: taking it again says by whom, and only a second, explicit
	// request takes it over.
	_, _ = c.srv.DB.Q().Exec(ctx, "UPDATE missions SET person = 'linus@example.com' WHERE id = ?", id)
	code, body := c.do("POST", path+"/claim", nil)
	if code != 409 || body["taken_by"] != "linus@example.com" || body["since"] == nil {
		t.Fatalf("conflict %d %v", code, body)
	}
	if code, _ := c.do("POST", path+"/claim", map[string]any{"force": true}); code != 200 {
		t.Fatalf("take over %d", code)
	}

	if code, body := c.do("POST", path+"/shipped", nil); code != 200 {
		t.Fatalf("shipped %d %v", code, body)
	}
	if s := stage(); s["stage"] != "shipped" || s["shipped_at"] == nil {
		t.Fatalf("shipped %v", s)
	}
	_, detail := c.do("GET", path, nil)
	if detail["context"].(map[string]any)["life"].(map[string]any)["stage"] != "shipped" {
		t.Fatalf("detail %v", detail["context"])
	}

	if code, _ := c.do("DELETE", path+"/claim", nil); code != 200 {
		t.Fatalf("release %d", code)
	}
	if s := stage(); s["stage"] != "open" || s["person"] != "" {
		t.Fatalf("released %v", s)
	}
}
