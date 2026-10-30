package api

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// In a real install the team left out "GET /buy-sell-contract/" and the API calls it could not touch,
// and two regressions stayed open: the POST of the same path, and the php-fpm stalls waiting in
// curl_exec() on that same API. Leaving something out now offers what goes with it.
func TestLeavingOutAnEndpointOffersItsOtherMethodsAndTheWaitsOnOtherServices(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	ctx := context.Background()
	p, _ := c.srv.Projects.BySlug(ctx, "shop")
	minute := time.Now().Add(-10*time.Minute).Unix() / 60 * 60
	hist := make([]int, 15)
	hist[protocol.HistogramIndex(8)] = 10
	stat := protocol.EndpointStat{N: 10, Sum: 80, Max: 9, Slow: 10, SlowSum: 80, Hist: hist}
	curl := "curl_exec() vendor/guzzlehttp/guzzle/src/Handler/CurlHandler.php:85"
	_, err := c.srv.Ingest.Ingest(ctx, p, protocol.Payload{BatchID: "1", Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent", Sources: []string{"nginx:shop", "php-fpm:shop"}},
		HTTP: []protocol.HTTPBucket{{Source: "nginx:shop", Minute: minute, N: 40, Sum: 320, Max: 9, N5: 40, Hist: hist,
			Endpoints: map[string]protocol.EndpointStat{
				"GET /buy-sell-contract/": stat, "POST /buy-sell-contract/": stat, "DELETE /buy-sell-contract/": stat, "GET /orders": stat,
			}}},
		PHP: []protocol.PHPBucket{{Source: "php-fpm:shop", Minute: minute, Stalls: []protocol.PHPStall{
			{Frame: curl, Code: "app/Services/Ade.php:40", N: 5},
			{Frame: "PDOStatement->execute() vendor/laravel/framework/src/Illuminate/Database/Connection.php:421", Code: "app/Http/Orders.php:9", N: 5},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := c.do("POST", "/api/projects/shop/exclusions", map[string]any{"target": "DELETE /buy-sell-contract/", "reason": "external"}); code != 201 {
		t.Fatalf("exclude %d %v", code, body)
	}

	related := func(target string) map[string]string {
		code, body := c.do("GET", "/api/projects/shop/exclusions/related?kind=endpoint&target="+url.QueryEscape(target), nil)
		if code != 200 {
			t.Fatalf("related %d %v", code, body)
		}
		out := map[string]string{}
		for _, r := range body["related"].([]any) {
			m := r.(map[string]any)
			if m["seconds_per_day"].(float64) <= 0 {
				t.Fatalf("related without a cost: %v", m)
			}
			out[m["kind"].(string)+" "+m["target"].(string)] = m["why"].(string)
		}
		return out
	}
	got := related("GET /buy-sell-contract/")
	want := map[string]string{"endpoint POST /buy-sell-contract/": "same_path", "php " + curl: "outbound"}
	if len(got) != len(want) {
		t.Fatalf("related %v, want %v (an excluded method, another path or a database wait are not offered)", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("related %v, want %v", got, want)
		}
	}

	// A query or a PHP frame has nothing that goes with it.
	code, body := c.do("GET", "/api/projects/shop/exclusions/related?kind=php&target="+url.QueryEscape(curl), nil)
	if code != 200 || len(body["related"].([]any)) != 0 {
		t.Fatalf("php related %d %v", code, body)
	}
}
