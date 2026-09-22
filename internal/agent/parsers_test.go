package agent

import (
	"bufio"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"os"
	"testing"
	"time"
)

func feedFile(t *testing.T, p LineParser, file string) {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		p.Feed(s.Text())
	}
	if f, ok := p.(Flusher); ok {
		f.Flush()
	}
}

func drainAll(a *Aggregator) Drained {
	return a.Drain(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
}

func TestNginxFormats(t *testing.T) {
	lines := []string{
		// a common style: rt= token
		`1.2.3.4 - - [16/Sep/2026:12:00:03 +0200] "GET /api/orders/15?x=1 HTTP/1.1" 200 512 "-" "curl" rt=0.250 uct=0.001`,
		// combined + $request_time as last field
		`1.2.3.4 - - [16/Sep/2026:12:00:04 +0200] "POST /cart HTTP/2.0" 500 12 "https://x/" "Mozilla/5.0 (X)" 2.500`,
		// JSON
		`{"time_iso8601":"2026-09-16T10:00:05+00:00","request_method":"GET","request_uri":"/api/orders/16","status":"200","request_time":"0.750"}`,
		// garbage and requests without time are ignored
		`not a log line`,
		`1.2.3.4 - - [16/Sep/2026:12:00:04 +0200] "GET / HTTP/1.1" 200 12 "-" "curl"`,
	}
	a := NewAggregator()
	p := NewNginxParser(Source{Name: "app", Format: "auto", SlowSeconds: 1}, a)
	for _, l := range lines {
		p.Feed(l)
	}
	d := drainAll(a)
	if len(d.HTTP) != 1 {
		t.Fatalf("want 1 bucket, got %d", len(d.HTTP))
	}
	b := d.HTTP[0]
	if b.Source != "nginx:app" || b.Minute != time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("bucket %+v", b)
	}
	if b.N != 3 || b.E5 != 1 || b.N2 != 1 {
		t.Fatalf("counts %+v", b)
	}
	if e := b.Endpoints["GET /api/orders/{id}"]; e.N != 2 {
		t.Fatalf("endpoints %+v", b.Endpoints)
	}
	if e := b.Endpoints["POST /cart"]; e.Slow != 1 || e.E5 != 1 {
		t.Fatalf("slow endpoint %+v", e)
	}
}

func TestMySQL8SlowLog(t *testing.T) {
	a := NewAggregator()
	feedFile(t, NewMySQLSlowParser(Source{IgnoreUsers: []string{"backup"}}, a), "testdata/mysql8-slow.log")
	d := drainAll(a)
	if len(d.DB) != 2 {
		t.Fatalf("want 2 minutes, got %d: %+v", len(d.DB), d.DB)
	}
	first := d.DB[0]
	if first.SlowN != 1 || first.SlowSum != 1.5 {
		t.Fatalf("backup user must be ignored: %+v", first)
	}
	for _, q := range first.Queries {
		if q.Fingerprint != "select * from orders where note like ?" || q.Database != "shop" || q.User != "app" || q.Rows != 100000 {
			t.Fatalf("query %+v", q)
		}
		if q.Sample != "select * from orders where note like '%s%'" {
			t.Fatalf("sample %q", q.Sample)
		}
	}
	// Same fingerprint in the next minute.
	for hash := range d.DB[1].Queries {
		if _, ok := first.Queries[hash]; !ok {
			t.Fatalf("same query must share the hash")
		}
	}
}

func TestMariaDBSlowLog(t *testing.T) {
	a := NewAggregator()
	feedFile(t, NewMySQLSlowParser(Source{}, a), "testdata/mariadb-slow.log")
	d := drainAll(a)
	if len(d.DB) != 1 || d.DB[0].SlowMax != 4 {
		t.Fatalf("db %+v", d.DB)
	}
	for _, q := range d.DB[0].Queries {
		if q.Database != "shop" || q.Rows != 12760000 || q.Fingerprint != "select count(*) as aggregate from credits where user_id = ? and is_used = ?" {
			t.Fatalf("query %+v", q)
		}
	}
}

func TestPHPFpmSlowlog(t *testing.T) {
	a := NewAggregator()
	feedFile(t, NewFpmParser(Source{Name: "www", Timezone: "Europe/Rome"}, a), "testdata/fpm-slow.log")
	d := drainAll(a)
	if len(d.PHP) != 1 {
		t.Fatalf("php %+v", d.PHP)
	}
	b := d.PHP[0]
	if b.Source != "php-fpm:www" || b.N != 4 || b.Minute != time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("bucket %+v", b)
	}
	want := map[string]string{
		"sleep() app/Http/Controllers/SlowController.php:12":                  "app/Http/Controllers/SlowController.php:12",
		"curl_exec() vendor/guzzlehttp/guzzle/src/Handler/CurlHandler.php:44": "app/Services/Pricing.php:88",
		// Project deployed in a folder named like a Laravel directory (/var/www/app).
		"usleep() routes/web.php:40": "routes/web.php:40",
		// Only framework frames: no application code to point at.
		`Composer\Autoload\{closure}() vendor/composer/ClassLoader.php:427`: "",
	}
	for _, s := range b.Stalls {
		if code, ok := want[s.Frame]; !ok || code != s.Code {
			t.Errorf("stall %+v", s)
		}
	}
	if len(b.Stalls) != 4 {
		t.Fatalf("stalls %+v", b.Stalls)
	}
}

func TestLongLivedConnectionsDoNotCountAsSlowRequests(t *testing.T) {
	a := NewAggregator()
	p := NewNginxParser(Source{Name: "app", SlowSeconds: 1, IgnorePaths: []string{"/events/*", "/broadcasting/auth"}}, a)
	for _, l := range []string{
		`1.2.3.4 - - [16/Sep/2026:12:00:03 +0200] "GET /app/reverb-key?protocol=7 HTTP/1.1" 101 0 "-" "ws" rt=3600.000`,
		`1.2.3.4 - - [16/Sep/2026:12:00:03 +0200] "GET /events/stream HTTP/1.1" 200 90 "-" "sse" rt=900.000`,
		`1.2.3.4 - - [16/Sep/2026:12:00:03 +0200] "GET /orders HTTP/1.1" 200 90 "-" "x" rt=0.100`,
	} {
		p.Feed(l)
	}
	d := drainAll(a)
	if len(d.HTTP) != 1 || d.HTTP[0].N != 1 || d.HTTP[0].Max != 0.1 {
		t.Fatalf("only the normal request must count: %+v", d.HTTP)
	}
	var upgrades float64
	for _, m := range d.Metrics {
		if m.Name == "nginx.app.websocket_connections" {
			upgrades = m.Sum
		}
	}
	if upgrades != 1 {
		t.Fatalf("websocket connections must be counted apart: %+v", d.Metrics)
	}
}

func TestApacheAndCaddyLogs(t *testing.T) {
	a := NewAggregator()
	apache := NewNginxParser(Source{Type: "apache", Name: "site", SlowSeconds: 1}, a)
	// LogFormat "%h %l %u %t \"%r\" %>s %b \"%{Referer}i\" \"%{User-Agent}i\" %D" (microseconds)
	apache.Feed(`10.0.0.1 - - [16/Sep/2026:12:00:03 +0200] "GET /shop/cart HTTP/1.1" 200 512 "-" "Mozilla" 2350000`)
	apache.Feed(`10.0.0.1 - - [16/Sep/2026:12:00:04 +0200] "GET /shop/items/7 HTTP/1.1" 200 512 "-" "Mozilla" 120000`)

	caddy := NewNginxParser(Source{Type: "caddy", Name: "shop", SlowSeconds: 1}, a)
	caddy.Feed(`{"level":"info","ts":1789552805.12,"logger":"http.log.access","msg":"handled request","request":{"method":"POST","uri":"/api/orders/99?x=1","host":"shop"},"duration":0.75,"status":503}`)

	d := drainAll(a)
	byName := map[string]protocol.HTTPBucket{}
	for _, b := range d.HTTP {
		byName[b.Source] = b
	}
	site := byName["apache:site"]
	if site.N != 2 || site.N2 != 1 || site.Endpoints["GET /shop/items/{id}"].N != 1 || site.Max != 2.35 {
		t.Fatalf("apache %+v", site)
	}
	c := byName["caddy:shop"]
	if c.N != 1 || c.E5 != 1 || c.Endpoints["POST /api/orders/{id}"].N != 1 {
		t.Fatalf("caddy %+v", c)
	}
}

// A virtual host claims the host it answers for only when it really has one: "site:" as
// configured, or a name detect took from a server_name. A generic name is not a host, and
// claiming it would shadow an application answering for a container called the same.
func TestAWebSourceClaimsOnlyARealHost(t *testing.T) {
	cases := []struct{ name, site, want string }{
		{"app.example.com", "", "app.example.com"},      // named by detect after its server_name
		{"nginx", "", ""},                               // the name of the web server, not a host
		{"web", "shop.example.com", "shop.example.com"}, // written by hand in the configuration
		{"nginx", "Shop.Example.com:8443", "shop.example.com"},
	}
	for _, c := range cases {
		a := NewAggregator()
		p := NewNginxParser(Source{Type: "nginx", Name: c.name, Site: c.site}, a)
		p.Feed(`1.2.3.4 - - [16/Sep/2026:12:00:03 +0200] "GET /orders HTTP/1.1" 200 512 "-" "-" rt=0.100`)
		var got []string
		for _, b := range a.Drain(time.Now().Add(time.Hour)).HTTP {
			got = b.Sites
		}
		if (c.want == "" && len(got) > 0) || (c.want != "" && (len(got) != 1 || got[0] != c.want)) {
			t.Errorf("name %q site %q: hosts %v, want %q", c.name, c.site, got, c.want)
		}
	}
}
