package api

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// A checkout that waits on a payment provider: the page of the endpoint says how much of its
// time is spent outside and where, the team excludes it with a reason, and it leaves the debt
// while its time stays readable apart.
func TestAnEndpointIsExcludedAndItsOutboundTimeIsShown(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")
	minute := time.Now().Add(-10*time.Minute).Unix() / 60 * 60
	hist := make([]int, 15)
	hist[protocol.HistogramIndex(8)] = 10
	_, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{BatchID: "1", Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent", Sources: []string{"nginx:shop"}},
		HTTP: []protocol.HTTPBucket{{Source: "nginx:shop", Minute: minute, N: 10, Sum: 80, Max: 9, N5: 10, Hist: hist,
			Endpoints: map[string]protocol.EndpointStat{"POST /checkout": {N: 10, Sum: 80, Max: 9, Slow: 10, SlowSum: 80, Hist: hist}}}},
		App: []protocol.AppBucket{{Minute: minute, Routes: map[string]protocol.RouteStat{"POST /checkout": {N: 10, Sum: 80, ExtSum: 60, ExtRequests: 10,
			Ext: map[string]protocol.ExtCall{"api.stripe.com": {N: 10, Sum: 60, Max: 7, Requests: 10, Origin: "app/Services/Stripe.php:88"}}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	code, ep := c.do("GET", "/api/projects/shop/endpoints?source=nginx:shop&endpoint=POST%20/checkout", nil)
	if code != 200 {
		t.Fatalf("endpoint %d %v", code, ep)
	}
	out := ep["outbound"].(map[string]any)
	if out["share"].(float64) != 0.75 || len(out["hosts"].([]any)) != 1 || out["hosts"].([]any)[0].(map[string]any)["host"] != "api.stripe.com" {
		t.Fatalf("outbound %v", out)
	}
	if ep["exclusion"] != nil {
		t.Fatalf("not excluded yet: %v", ep["exclusion"])
	}
	_, slow := c.do("GET", "/api/projects/shop/slow?window=24h", nil)
	row := slow["endpoints"].([]any)[0].(map[string]any)
	if row["outbound_share"].(float64) != 0.75 || row["excluded"] == true {
		t.Fatalf("slow row %v", row)
	}

	if code, body := c.do("POST", "/api/projects/shop/exclusions", map[string]any{"endpoint": "POST /checkout", "reason": "bogus"}); code != 422 {
		t.Fatalf("bogus reason: %d %v", code, body)
	}
	code, body := c.do("POST", "/api/projects/shop/exclusions", map[string]any{"endpoint": "POST /checkout", "reason": "external", "note": "Stripe", "days": 30})
	if code != 201 {
		t.Fatalf("exclude %d %v", code, body)
	}
	ex := body["exclusion"].(map[string]any)
	if ex["created_by"] != "ada@example.com" || ex["until"] == nil {
		t.Fatalf("exclusion %v", ex)
	}

	_, list := c.do("GET", "/api/projects/shop/exclusions", nil)
	items := list["exclusions"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["seconds_per_day"].(float64) <= 0 || len(list["reasons"].([]any)) == 0 {
		t.Fatalf("list %v", list)
	}
	_, slow = c.do("GET", "/api/projects/shop/slow?window=24h", nil)
	if row := slow["endpoints"].([]any)[0].(map[string]any); row["excluded"] != true {
		t.Fatalf("the list says it is excluded: %v", row)
	}
	_, ep = c.do("GET", "/api/projects/shop/endpoints?source=nginx:shop&endpoint=POST%20/checkout", nil)
	if ep["exclusion"] == nil {
		t.Fatal("the endpoint page does not say it is excluded")
	}
	_, report := c.do("GET", "/api/projects/shop/debt-report", nil)
	d := report["debt"].(map[string]any)
	if d["seconds_per_day"].(float64) != 0 || d["excluded_seconds_per_day"].(float64) <= 0 {
		t.Fatalf("debt %v", d)
	}
	if n := len(report["excluded"].([]any)); n != 1 {
		t.Fatalf("report excluded %v", report["excluded"])
	}

	id := int64(items[0].(map[string]any)["id"].(float64))
	if code, _ := c.do("DELETE", "/api/projects/shop/exclusions/"+strconv.FormatInt(id, 10), nil); code != 200 {
		t.Fatalf("remove %d", code)
	}
	_, report = c.do("GET", "/api/projects/shop/debt-report", nil)
	if report["debt"].(map[string]any)["seconds_per_day"].(float64) <= 0 {
		t.Fatalf("back in the debt: %v", report["debt"])
	}
}
