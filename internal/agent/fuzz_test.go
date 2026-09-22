package agent

import (
	"strings"
	"testing"
	"time"
)

// Log lines are untrusted input: parsers must never panic or grow without bounds.

func FuzzNginx(f *testing.F) {
	f.Add(`1.2.3.4 - - [16/Sep/2026:12:00:03 +0200] "GET /api/orders/15 HTTP/1.1" 200 512 "-" "curl" rt=0.250`)
	f.Add(`{"time_iso8601":"2026-09-16T10:00:05+00:00","request_method":"GET","request_uri":"/x","status":"200","request_time":"0.750"}`)
	f.Add(`{"time":1,"request_time":[1],"status":{}}`)
	f.Fuzz(func(t *testing.T, line string) {
		a := NewAggregator()
		NewNginxParser(Source{Name: "app", SlowSeconds: 1}, a).Feed(line)
		for _, b := range a.Drain(time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)).HTTP {
			for e := range b.Endpoints {
				if len(e) > 200 {
					t.Fatalf("endpoint too long: %d", len(e))
				}
			}
		}
	})
}

func FuzzMySQLSlowLog(f *testing.F) {
	f.Add("# User@Host: app[app] @ x\n# Query_time: 1.5 Rows_examined: 9\nSET timestamp=1789552803;\nselect 1;")
	f.Add("# User@Host: \n# Query_time: nan\nSET timestamp=99999999999999999999;\n;")
	f.Fuzz(func(t *testing.T, content string) {
		a := NewAggregator()
		p := NewMySQLSlowParser(Source{}, a)
		for _, line := range strings.Split(content, "\n") {
			p.Feed(line)
		}
		a.Drain(time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC))
	})
}

func FuzzFpm(f *testing.F) {
	f.Add("[16-Sep-2026 10:00:03]  [pool www] pid 1\n[0x0] sleep() /var/www/app/X.php:1\n")
	f.Fuzz(func(t *testing.T, content string) {
		a := NewAggregator()
		p := NewFpmParser(Source{Name: "www"}, a)
		for _, line := range strings.Split(content, "\n") {
			p.Feed(line)
		}
		p.(Flusher).Flush()
	})
}

func FuzzStatsDAndPrometheus(f *testing.F) {
	f.Add("a.b:1|c|@0.1\nq:2|g", "# TYPE x counter\nx{a=\"b\"} 1e309")
	f.Fuzz(func(t *testing.T, statsd, prom string) {
		a := NewAggregator()
		for _, line := range strings.Split(statsd, "\n") {
			ParseStatsD(a, time.Now, line)
		}
		newPromScraper(Source{Name: "p"}).ingest(a, time.Now(), prom)
	})
}
