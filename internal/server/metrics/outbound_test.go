package metrics

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/exclude"
	"github.com/getslowpoke/slowpoke/internal/server/ingest"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// The route a framework reports and the endpoint nginx logs are the same thing with two names:
// what the requests waited on other services is read back under the endpoint's name.
func TestOutboundTimeIsReadPerEndpoint(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	base := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	svc := ingest.New(db)
	svc.Now = func() time.Time { return base.Add(2 * time.Hour) }
	_, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: "1", Agent: protocol.AgentInfo{Host: "web", Kind: "agent"},
		App: []protocol.AppBucket{{Minute: base.Unix(), Routes: map[string]protocol.RouteStat{
			"POST /orders/{order}/pay": {N: 10, Sum: 40, ExtSum: 30, ExtRequests: 10, Ext: map[string]protocol.ExtCall{
				"api.stripe.com":        {N: 10, Sum: 25, Max: 6, Requests: 10, Origin: "app/Services/Stripe.php:88"},
				"partner.example.com":   {N: 20, Sum: 6, Max: 1, Errors: 2, Requests: 10},
			}},
			"GET /products": {N: 100, Sum: 20},
		}}}})
	if err != nil {
		t.Fatal(err)
	}
	r := Reader{DB: db}
	out, err := r.Outbound(ctx, p.ID, "POST /orders/{id}/pay", base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if out.Requests != 10 || out.WithCalls != 10 || out.Seconds != 30 || math.Abs(out.Share-0.75) > 0.001 {
		t.Fatalf("outbound %+v", out)
	}
	if len(out.Hosts) != 2 || out.Hosts[0].Host != "api.stripe.com" || out.Hosts[0].Seconds != 25 || out.Hosts[0].PerRequest != 1 ||
		out.Hosts[0].Origin != "app/Services/Stripe.php:88" || out.Hosts[1].Errors != 2 || out.Hosts[1].PerRequest != 2 {
		t.Fatalf("hosts %+v", out.Hosts)
	}
	none, err := r.Outbound(ctx, p.ID, "GET /products", base, base.Add(time.Hour))
	if err != nil || none.Seconds != 0 || len(none.Hosts) != 0 || none.Requests != 100 {
		t.Fatalf("products %+v err %v", none, err)
	}

	all, err := r.OutboundByEndpoint(ctx, p.ID, base, base.Add(time.Hour))
	if err != nil || all[exclude.Key(exclude.KindEndpoint, "POST /orders/{id}/pay")].Seconds != 30 {
		t.Fatalf("by endpoint %+v err %v", all, err)
	}
}
