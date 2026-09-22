package integrations

import (
	"strings"
	"testing"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

func codes(steps []Step) string {
	var b strings.Builder
	for _, s := range steps {
		b.WriteString(s.Kind + ": " + s.Code + "\n")
	}
	return b.String()
}

// The staging database: statistics only. The guide must use the agent's real MySQL user and
// only the listed database, and say how to undo each step.
func TestMySQLGuidesUseTheRealUserAndSchemas(t *testing.T) {
	c := build("mysql:main", `[
		{"key":"connection","status":"ok"},
		{"key":"statistics","status":"ok","optional":true},
		{"key":"slow_log","status":"off","optional":true,"reason":"slow_log_off"},
		{"key":"plans","status":"off","optional":true,"reason":"no_select","detail":"shop_testing"}]`,
		`{"user":"slowpoke@127.0.0.1","schemas":"shop_testing","version":"5.7.42"}`, 0)

	plans := codes(c.Guides["plans"].Enable)
	if !strings.Contains(plans, "GRANT SELECT ON `shop_testing`.* TO 'slowpoke'@'127.0.0.1';") {
		t.Fatalf("plans enable:\n%s", plans)
	}
	slow := codes(c.Guides["slow_log"].Enable)
	for _, want := range []string{"SET GLOBAL slow_query_log = ON;", "long_query_time = 1", "type: mysql-slowlog", "schemas: [shop_testing]", "systemctl restart slowpoke-agent"} {
		if !strings.Contains(slow, want) {
			t.Errorf("slow log enable misses %q:\n%s", want, slow)
		}
	}
	stats := codes(c.Guides["statistics"].Disable)
	if !strings.Contains(stats, "REVOKE SELECT ON performance_schema.* FROM 'slowpoke'@'127.0.0.1';") || !strings.Contains(stats, "digests: false") {
		t.Fatalf("statistics disable:\n%s", stats)
	}
	if len(c.Guides["plans"].Disable) != 0 {
		t.Fatal("plans are off: nothing to disable")
	}
	remove := codes(c.Remove)
	for _, want := range []string{"DROP USER IF EXISTS 'slowpoke'@'127.0.0.1';", "SET GLOBAL slow_query_log = OFF;", "systemctl restart slowpoke-agent"} {
		if !strings.Contains(remove, want) {
			t.Errorf("remove misses %q:\n%s", want, remove)
		}
	}
}

func TestMySQLGuidesPlansOnAndOff(t *testing.T) {
	c := build("mysql:main", `[{"key":"connection","status":"ok"},{"key":"plans","status":"ok","optional":true}]`,
		`{"user":"slowpoke@localhost","schemas":"shop,billing"}`, 0)
	off := codes(c.Guides["plans"].Disable)
	if !strings.Contains(off, "REVOKE SELECT ON `shop`.* FROM 'slowpoke'@'localhost';") || !strings.Contains(off, "REVOKE SELECT ON `billing`.* FROM") || !strings.Contains(off, "explain: false") {
		t.Fatalf("plans disable:\n%s", off)
	}
}

func TestGuidesQuoteIdentifiersFromTheAgent(t *testing.T) {
	c := build("mysql:main", `[{"key":"plans","status":"off","optional":true,"reason":"no_select","detail":"a`+"`"+`b"}]`,
		`{"user":"x'y@127.0.0.1","schemas":"a`+"`"+`b"}`, 0)
	plans := codes(c.Guides["plans"].Enable)
	if !strings.Contains(plans, "GRANT SELECT ON `a``b`.* TO 'x''y'@'127.0.0.1';") {
		t.Fatalf("identifiers must be quoted:\n%s", plans)
	}
}

func TestPostgresAndFileGuides(t *testing.T) {
	c := build("postgres:billing", `[{"key":"connection","status":"ok"},
		{"key":"statistics","status":"off","optional":true,"reason":"pg_stat_statements_missing"},
		{"key":"plans","status":"off","optional":true,"reason":"no_select"}]`, `{"user":"slowpoke","version":"170002"}`, 0)
	if s := codes(c.Guides["statistics"].Enable); !strings.Contains(s, "shared_preload_libraries = 'pg_stat_statements'") || !strings.Contains(s, "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;") {
		t.Fatalf("pg statistics:\n%s", s)
	}
	if s := codes(c.Guides["plans"].Enable); !strings.Contains(s, `GRANT pg_read_all_data TO "slowpoke";`) {
		t.Fatalf("pg plans:\n%s", s)
	}

	web := build("nginx:shop", `[{"key":"readable","status":"ok"},{"key":"duration","status":"error","reason":"no_duration"}]`, `{"path":"/var/log/nginx/shop.log"}`, 0)
	if s := codes(web.Guides["duration"].Enable); !strings.Contains(s, "rt=$request_time") || !strings.Contains(s, "slowpoke-agent detect") {
		t.Fatalf("nginx duration:\n%s", s)
	}
	unreadable := build("php-fpm:www", `[{"key":"readable","status":"error","reason":"unreadable","detail":"permission denied"}]`, `{"path":"/var/log/php8.3-fpm-www.slow.log"}`, 0)
	if s := codes(unreadable.Guides["readable"].Enable); !strings.Contains(s, "CAP_DAC_READ_SEARCH") {
		t.Fatalf("unreadable file:\n%s", s)
	}
	otlp := build("otlp:otlp", `[{"key":"receiving","status":"waiting","reason":"no_traces_yet"}]`, `{"listen":"127.0.0.1:4318"}`, 0)
	if s := codes(otlp.Guides["receiving"].Enable); !strings.Contains(s, "composer require slowpoke/laravel") {
		t.Fatalf("otlp:\n%s", s)
	}
	_ = protocol.CheckItem{}
}

func TestRedisGuides(t *testing.T) {
	c := build("redis:cache", `[{"key":"connection","status":"ok"},{"key":"metrics","status":"ok","optional":true},
		{"key":"slow_log","status":"off","optional":true,"reason":"slow_log_off"}]`, `{"address":"127.0.0.1:6379","version":"7.2.5"}`, 0)
	on := codes(c.Guides["slow_log"].Enable)
	for _, want := range []string{"CONFIG SET slowlog-log-slower-than 10000", "CONFIG REWRITE"} {
		if !strings.Contains(on, want) {
			t.Errorf("enable misses %q:\n%s", want, on)
		}
	}
	if s := codes(c.Remove); !strings.Contains(s, "redis source") {
		t.Fatalf("remove:\n%s", s)
	}
	off := build("redis:cache", `[{"key":"connection","status":"ok"},{"key":"slow_log","status":"ok","optional":true}]`, `{}`, 0)
	if s := codes(off.Guides["slow_log"].Disable); !strings.Contains(s, "slowlog-log-slower-than -1") {
		t.Fatalf("disable:\n%s", s)
	}
}

func TestHostProcessesGuide(t *testing.T) {
	off := build("host:host", `[{"key":"readable","status":"ok"},{"key":"processes","status":"off","optional":true,"reason":"needs_capabilities"}]`, `{"root":"/"}`, 0)
	on := codes(off.Guides["processes"].Enable)
	for _, want := range []string{"CAP_SYS_PTRACE", "CAP_DAC_READ_SEARCH", "systemctl daemon-reload"} {
		if !strings.Contains(on, want) {
			t.Errorf("enable misses %q:\n%s", want, on)
		}
	}
	ok := build("host:host", `[{"key":"readable","status":"ok"},{"key":"processes","status":"ok","optional":true}]`, `{}`, 0)
	if s := codes(ok.Guides["processes"].Disable); !strings.Contains(s, "CAP_DAC_READ_SEARCH") || strings.Contains(s, "CAP_SYS_PTRACE\n") {
		t.Fatalf("disable must leave only the read capability:\n%s", s)
	}
}

// The agent reports a database it could not open because the password line is still a placeholder:
// the panel must answer with the SQL to create the user and the exact dsn line to write back.
func TestDatabaseWaitingForItsPasswordGetsTheFullRecipe(t *testing.T) {
	s := build("mysql:main", `[{"key":"connection","status":"off","reason":"needs_password"}]`, `{"address":"10.0.0.9:3306"}`, 0)
	code := codes(s.Guides["connection"].Enable)
	for _, want := range []string{"CREATE USER", "GRANT PROCESS", "10.0.0.9:3306", "${SLOWPOKE_DB_PASSWORD}"} {
		if !strings.Contains(code, want) {
			t.Errorf("missing %q in:\n%s", want, code)
		}
	}
}

// The log is not there yet: the panel must say how to create it, not how to fix permissions.
func TestNotCreatedYetGetsTheLineThatCreatesTheFile(t *testing.T) {
	php := build("php-fpm:www", `[{"key":"readable","status":"waiting","reason":"not_created_yet"}]`, `{"path":"/var/log/php7.4-fpm-www.slow.log"}`, 0)
	code := codes(php.Guides["readable"].Enable)
	for _, want := range []string{"request_slowlog_timeout", "slowlog = /var/log/php7.4-fpm-www.slow.log", "reload php"} {
		if !strings.Contains(code, want) {
			t.Errorf("php-fpm guide misses %q:\n%s", want, code)
		}
	}
	web := build("nginx:shop", `[{"key":"readable","status":"waiting","reason":"not_created_yet"}]`, `{"path":"/var/log/nginx/shop.slowpoke.log"}`, 0)
	if code := codes(web.Guides["readable"].Enable); !strings.Contains(code, "access_log /var/log/nginx/shop.slowpoke.log slowpoke;") {
		t.Errorf("nginx guide misses the access_log line:\n%s", code)
	}
	slow := build("mysql-slowlog:mysql-slowlog", `[{"key":"readable","status":"waiting","reason":"not_created_yet"}]`, `{"path":"/var/log/mysql/slow.log"}`, 0)
	if code := codes(slow.Guides["readable"].Enable); !strings.Contains(code, "SET GLOBAL slow_query_log = ON;") {
		t.Errorf("mysql slow log guide misses the switch:\n%s", code)
	}
	// A file that exists but is closed to the agent still gets the permission recipe.
	denied := build("nginx:shop", `[{"key":"readable","status":"error","reason":"unreadable"}]`, `{"path":"/var/log/nginx/shop.log"}`, 0)
	if code := codes(denied.Guides["readable"].Enable); !strings.Contains(code, "CAP_DAC_READ_SEARCH") {
		t.Errorf("permission guide lost:\n%s", code)
	}
}
