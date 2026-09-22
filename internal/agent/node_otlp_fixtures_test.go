package agent

// Contract test between the @slowpokedev/node npm package and the OTLP receiver.

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
)

func TestNodePackageFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../spec/node_otlp_fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload"`
		Expect  struct {
			Route    string `json:"route"`
			Status   int    `json:"status"`
			Requests int    `json:"requests"`
			Source   string `json:"source"`
			// The host the application answered for: what lets a server recognise the access log
			// of a web server on another machine as the same requests.
			Site string `json:"site"`
			Queries  []struct {
				Statement string `json:"statement"`
				N         int    `json:"n"`
				Origin    string `json:"origin"`
				NPlusOne  bool   `json:"n_plus_one"`
			} `json:"queries"`
			// A job or a scheduled command belongs to the Jobs page, not to the endpoints.
			// Outbound HTTP calls the request made, per host.
			Outbound []expectedCall `json:"outbound"`
			Job *struct {
				Kind   string `json:"kind"`
				Name   string `json:"name"`
				Runs   int    `json:"runs"`
				Failed int    `json:"failed"`
			} `json:"job"`
		} `json:"expect"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no fixtures")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			a := NewAggregator()
			r := newOTLPReceiver(a).withCodeRoot("/srv/app/")
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/v1/traces", bytes.NewReader(c.Payload))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("status %d %s", rec.Code, rec.Body.String())
			}
			r.flush(time.Now().Add(time.Hour))
			d := a.Drain(time.Now().Add(time.Hour))

			if len(d.App) != 1 {
				t.Fatalf("app buckets %+v", d.App)
			}
			if want := c.Expect.Job; want != nil {
				var got *protocol.JobBucket
				for i := range d.Jobs {
					if d.Jobs[i].Kind == want.Kind && d.Jobs[i].Name == want.Name {
						got = &d.Jobs[i]
					}
				}
				if got == nil || got.N != want.Runs || got.Failed != want.Failed {
					t.Fatalf("jobs %+v, want %+v", d.Jobs, want)
				}
				if got.Sum <= 0 {
					t.Fatalf("a job with no duration: %+v", got)
				}
			} else if len(d.Jobs) > 0 {
				t.Fatalf("this is not a job: %+v", d.Jobs)
			}
			route, ok := d.App[0].Routes[c.Expect.Route]
			if !ok || route.N != c.Expect.Requests {
				t.Fatalf("routes %+v, want %q with %d requests", d.App[0].Routes, c.Expect.Route, c.Expect.Requests)
			}
			checkOutbound(t, route, c.Expect.Outbound)
			if want := 0; c.Expect.Status >= 500 {
				want = 1
				if route.E5 != want {
					t.Fatalf("e5 %d", route.E5)
				}
			}
			if len(d.App[0].Queries) != len(c.Expect.Queries) {
				t.Fatalf("queries %+v", d.App[0].Queries)
			}
			for _, want := range c.Expect.Queries {
				q, ok := d.App[0].Queries[fingerprint.Hash(fingerprint.Of(want.Statement))]
				if !ok {
					t.Fatalf("query %q missing in %+v", want.Statement, d.App[0].Queries)
				}
				if q.N != want.N || q.Origin != want.Origin || (q.NPlusOne > 0) != want.NPlusOne {
					t.Fatalf("query %q = %+v, want %+v", want.Statement, q, want)
				}
			}

			requests := 0
			for _, h := range d.HTTP {
				if h.Source != c.Expect.Source {
					t.Fatalf("http source %q, want %q", h.Source, c.Expect.Source)
				}
				if got := strings.Join(h.Sites, ","); got != c.Expect.Site {
					t.Fatalf("hosts %q, want %q", got, c.Expect.Site)
				}
				requests += h.N
				if c.Expect.Requests > 0 && c.Expect.Job == nil {
					if _, ok := h.Endpoints[c.Expect.Route]; !ok {
						t.Fatalf("endpoints %+v, want %q", h.Endpoints, c.Expect.Route)
					}
				}
			}
			// A job is a run, not a request: it must leave the HTTP statistics alone.
			wantRequests := c.Expect.Requests
			if c.Expect.Job != nil {
				wantRequests = 0
			}
			if requests != wantRequests {
				t.Fatalf("http requests %d, want %d", requests, wantRequests)
			}
		})
	}
}
