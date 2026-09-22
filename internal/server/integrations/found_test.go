package integrations

import (
	"strings"
	"testing"
)

// What the agent found on the machine and nobody reads is not a fault, it is the next step: its
// own status, and a guide that goes from nothing to a working source in a handful of lines.
func TestWhatTheMachineHasAndSlowpokeDoesNotRead(t *testing.T) {
	db := build("found:database:mysql", `[{"key":"configured","status":"off","reason":"not_configured"}]`,
		`{"engine":"mysql","process":"mariadbd","slow_log":"/var/log/mysql/mariadb-slow.log","address":"127.0.0.1:3306"}`, 0)
	if db.Status != Missing || db.Kind != "database" || db.Name != "mysql" {
		t.Fatalf("status=%q kind=%q name=%q", db.Status, db.Kind, db.Name)
	}
	code := codes(db.Guides["configured"].Enable)
	for _, want := range []string{"CREATE USER", "GRANT PROCESS", "- type: mysql", "mysql-slowlog", "/var/log/mysql/mariadb-slow.log"} {
		if !strings.Contains(code, want) {
			t.Errorf("database guide misses %q:\n%s", want, code)
		}
	}

	site := build("found:site:admin.example.com", `[{"key":"configured","status":"off","reason":"not_configured"}]`,
		`{"site":"admin.example.com","path":"/var/log/nginx/admin.example.com.slowpoke.log","access_log":"/var/log/nginx/admin.log","inherited_log":"yes","has_duration":"no"}`, 0)
	code = codes(site.Guides["configured"].Enable)
	for _, want := range []string{"log_format slowpoke", "access_log /var/log/nginx/admin.log;", "access_log /var/log/nginx/admin.example.com.slowpoke.log slowpoke;", "- type: nginx"} {
		if !strings.Contains(code, want) {
			t.Errorf("site guide misses %q:\n%s", want, code)
		}
	}

	// A site that already logs the duration only needs the source in agent.yaml.
	ready := build("found:site:shop.example.com", `[{"key":"configured","status":"off","reason":"not_configured"}]`,
		`{"site":"shop.example.com","path":"/var/log/nginx/shop.log","access_log":"/var/log/nginx/shop.log","has_duration":"yes"}`, 0)
	code = codes(ready.Guides["configured"].Enable)
	if strings.Contains(code, "log_format") || !strings.Contains(code, "path: /var/log/nginx/shop.log") {
		t.Errorf("a log that already has the duration needs no nginx change:\n%s", code)
	}

	pool := build("found:pool:www", `[{"key":"configured","status":"off","reason":"not_configured"}]`,
		`{"pool":"www","version":"8.3","path":"/var/log/php8.3-fpm-www.slow.log","slow_log_on":"no"}`, 0)
	code = codes(pool.Guides["configured"].Enable)
	for _, want := range []string{"request_slowlog_timeout", "process_control_timeout", "reload php8.3-fpm", "- type: php-fpm"} {
		if !strings.Contains(code, want) {
			t.Errorf("pool guide misses %q:\n%s", want, code)
		}
	}

	app := build("found:app:/var/www/shop", `[{"key":"configured","status":"off","reason":"no_package"}]`,
		`{"framework":"Laravel","version":"12.1","dir":"/var/www/shop","site":"shop.example.com","language":"php"}`, 0)
	if code := codes(app.Guides["configured"].Enable); !strings.Contains(code, "composer require slowpoke/laravel") {
		t.Errorf("app guide misses the package:\n%s", code)
	}

	// The summary counts them apart: they are not problems, they are what is left to do.
	s := Summarize([]Check{db, site, pool, app, build("nginx:ok", `[{"key":"readable","status":"ok"}]`, `{}`, 0)})
	if s.Missing != 4 || s.Problems != 0 || s.Complete != 1 {
		t.Fatalf("summary %+v", s)
	}
}

// Anything that writes on the monitored machine has to say what it costs, in one line, before
// somebody pastes it: whoever installs Slowpoke is protecting a production server.
func TestEveryGuideThatTouchesTheMachineDeclaresItsCost(t *testing.T) {
	cases := []struct {
		what   string
		check  Check
		phrase string
	}{
		{"database", build("found:database:mysql", `[{"key":"configured","status":"off","reason":"not_configured"}]`, `{"engine":"mysql"}`, 0), "slow log"},
		{"pool", build("found:pool:www", `[{"key":"configured","status":"off","reason":"not_configured"}]`, `{"pool":"www","version":"8.3","slow_log_on":"no"}`, 0), "over 1 s"},
		{"site", build("found:site:shop", `[{"key":"configured","status":"off","reason":"not_configured"}]`, `{"site":"shop","has_duration":"no"}`, 0), "line per request"},
		{"app", build("found:app:/var/www/shop", `[{"key":"configured","status":"off","reason":"no_package"}]`, `{"framework":"Laravel","dir":"/var/www/shop"}`, 0), "never waits"},
		{"cache", build("found:cache:6379", `[{"key":"configured","status":"off","reason":"not_configured"}]`, `{"port":"6379"}`, 0), "per minute"},
		{"performance_schema", build("mysql:main", `[{"key":"statistics","status":"off","optional":true,"reason":"performance_schema_off"}]`, `{}`, 0), "restart"},
	}
	for _, c := range cases {
		var g Guide
		for key := range c.check.Guides {
			g = c.check.Guides[key]
		}
		if g.Cost == "" {
			t.Errorf("%s: no cost declared", c.what)
			continue
		}
		if !strings.Contains(strings.ToLower(g.Cost), c.phrase) {
			t.Errorf("%s: cost %q does not mention %q", c.what, g.Cost, c.phrase)
		}
	}
}

// The panel talks about the person's own stack. The protocol the agent listens to is plumbing:
// it has no name in the app, no recipe for it, and a Symfony application gets its own package
// instead of a link to a specification.
func TestRecipesNameThePackageAndNeverTheProtocol(t *testing.T) {
	symfony := build("found:app:/var/www/api", `[{"key":"configured","status":"off","reason":"no_package"}]`,
		`{"framework":"Symfony","version":"7.1","dir":"/var/www/api","language":"php"}`, 0)
	if code := codes(symfony.Guides["configured"].Enable); !strings.Contains(code, "composer require slowpoke/symfony") {
		t.Errorf("a Symfony application must be offered its own package:\n%s", code)
	}

	checks := []Check{
		symfony,
		build("found:app:/var/www/shop", `[{"key":"configured","status":"off","reason":"no_package"}]`,
			`{"framework":"Laravel","dir":"/var/www/shop"}`, 0),
		build("found:app:/srv/api", `[{"key":"configured","status":"off","reason":"no_package"}]`,
			`{"framework":"Django","dir":"/srv/api","language":"python"}`, 0),
		build("otlp:app", `[{"key":"receiving","status":"off","reason":"no_traces_yet"}]`,
			`{"listen":"127.0.0.1:4318"}`, 0),
	}
	for _, c := range checks {
		for key, g := range c.Guides {
			text := g.Cost + "\n" + codes(g.Enable) + codes(g.Disable)
			for _, jargon := range []string{"OpenTelemetry", "OTLP", "otlp"} {
				if strings.Contains(text, jargon) {
					t.Errorf("%s/%s says %q to the person reading the panel:\n%s", c.Source, key, jargon, text)
				}
			}
		}
	}
}
