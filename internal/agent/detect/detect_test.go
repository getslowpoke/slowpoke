package detect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A copy of a real staging server, where detect was first needed: nginx with the
// default log format and a Reverb WebSocket on /app, PHP-FPM without a slow log, a Laravel
// app deployed by Deployer whose database lives on another machine.
func TestDetectStagingServer(t *testing.T) {
	r := Scan("testdata/staging")

	if r.Host != "staging" || !strings.Contains(r.OS, "Ubuntu 24.04") {
		t.Fatalf("host %q os %q", r.Host, r.OS)
	}

	if len(r.Sites) != 1 {
		t.Fatalf("sites: %+v", r.Sites)
	}
	site := r.Sites[0]
	if site.Name != "api-dev.example.test" || site.Root != "/var/www/shop-api/current/public" {
		t.Fatalf("site: %+v", site)
	}
	if site.HasDuration || site.AccessLog != "/var/log/nginx/access.log" {
		t.Fatalf("the inherited default log has no duration: %+v", site)
	}
	if site.LogPath() != "/var/log/nginx/api-dev.example.test.slowpoke.log" {
		t.Fatalf("planned log: %s", site.LogPath())
	}
	if len(site.LongPaths) != 1 || site.LongPaths[0] != "/app*" {
		t.Fatalf("the WebSocket location must be ignored: %+v", site.LongPaths)
	}
	if site.Pool != "www" {
		t.Fatalf("site not linked to its PHP-FPM pool: %+v", site)
	}

	if len(r.Pools) != 1 || r.Pools[0].Name != "www" || r.Pools[0].Version != "8.3" || r.Pools[0].SlowLogOn {
		t.Fatalf("pools: %+v", r.Pools)
	}
	if r.Pools[0].SlowLogPath() != "/var/log/php8.3-fpm-www.slow.log" {
		t.Fatalf("planned slow log: %s", r.Pools[0].SlowLogPath())
	}

	if len(r.Apps) != 1 {
		t.Fatalf("apps: %+v", r.Apps)
	}
	app := r.Apps[0]
	if app.Framework != "Laravel" || app.Version != "12.67.0" || app.Dir != "/var/www/shop-api/current" || app.Site != "api-dev.example.test" {
		t.Fatalf("app: %+v", app)
	}
	if app.DB.Engine != "mysql" || app.DB.Host != "10.20.30.40" || app.DB.Local || app.DB.Name != "shop_testing" {
		t.Fatalf("database of the app: %+v", app.DB)
	}
	if len(r.Databases) != 0 {
		t.Fatalf("no database runs here: %+v", r.Databases)
	}

	text := r.Text()
	for _, want := range []string{
		"api-dev.example.test",
		"no request duration",
		"log_format slowpoke",
		"access_log /var/log/nginx/api-dev.example.test.slowpoke.log slowpoke;",
		// Found on staging: a server-level access_log replaces the inherited one, so the
		// existing log must be repeated or it stops receiving this site's requests.
		"access_log /var/log/nginx/access.log;",
		"nginx -t && systemctl reload nginx",
		"request_slowlog_timeout = 1s",
		"slowlog = /var/log/php8.3-fpm-www.slow.log",
		"php-fpm8.3 -t && systemctl reload php8.3-fpm",
		"Laravel 12.67.0",
		"composer require slowpoke/laravel",
		"10.20.30.40",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report misses %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "must-never-appear") {
		t.Fatal("a password from .env leaked into the report")
	}

	// That the agent accepts this config is checked in internal/agent (TestGeneratedConfigLoads):
	// the agent reads detect to say what it is not reading, so the dependency only goes one way.
	raw := r.Config("https://slowpoke.example.test", "spa_test")
	cfg := parseConfig(t, raw)
	if cfg.Server != "https://slowpoke.example.test" || cfg.Token != "spa_test" || cfg.Host != "staging" {
		t.Fatalf("config header: %+v", cfg)
	}
	want := map[string]string{
		"host":    "",
		"nginx":   "/var/log/nginx/api-dev.example.test.slowpoke.log",
		"php-fpm": "/var/log/php8.3-fpm-www.slow.log",
		"otlp":    "",
	}
	if len(cfg.Sources) != len(want) {
		t.Fatalf("sources: %+v", cfg.Sources)
	}
	for _, s := range cfg.Sources {
		path, ok := want[s.Type]
		if !ok || s.Path != path {
			t.Errorf("source %+v", s)
		}
		if s.Type == "nginx" && (len(s.IgnorePaths) != 1 || s.IgnorePaths[0] != "/app*") {
			t.Errorf("nginx ignore_paths: %+v", s.IgnorePaths)
		}
	}
}

// A server already set up well: detect must not ask for changes, must reuse existing logs,
// read local MySQL, notice Laravel traced twice, and find apps no site points to.
func TestDetectTunedServer(t *testing.T) {
	r := Scan("testdata/tuned")

	if len(r.Sites) != 2 {
		t.Fatalf("sites: %+v", r.Sites)
	}
	for _, s := range r.Sites {
		if !s.HasDuration || s.LogPath() != s.AccessLog {
			t.Errorf("existing timed log must be reused: %+v", s)
		}
	}
	if len(r.Pools) != 1 || !r.Pools[0].SlowLogOn || r.Pools[0].SlowLogPath() != "/var/log/php-fpm/shop-slow.log" {
		t.Fatalf("pools: %+v", r.Pools)
	}
	if len(r.Databases) != 1 || r.Databases[0].Engine != "mysql" || r.Databases[0].SlowLog != "/var/log/mysql/mysql-slow.log" {
		t.Fatalf("databases: %+v", r.Databases)
	}

	frameworks := map[string]App{}
	for _, a := range r.Apps {
		frameworks[a.Framework] = a
	}
	if a := frameworks["Laravel"]; a.Version != "11.44.2" || !a.HasSlowpoke || !a.HasOpenTelemetry || a.Site != "shop.example.test" || !a.DB.Local {
		t.Fatalf("laravel: %+v", a)
	}
	if a := frameworks["Django"]; a.Version != "5.1.4" || a.Dir != "/var/www/blog" || a.Language != "Python" {
		t.Fatalf("django: %+v", a)
	}
	if a := frameworks["Next.js"]; a.Version != "15.1.0" || a.Language != "Node.js" {
		t.Fatalf("next: %+v", a)
	}

	text := r.Text()
	if strings.Contains(text, "no request duration") || strings.Contains(text, "request_slowlog_timeout = 1s") {
		t.Errorf("nothing to change, but the report asks for changes:\n%s", text)
	}
	for _, want := range []string{"traced twice", "GRANT PROCESS, SELECT", "docs/instrument/python.md", "docs/instrument/node.md"} {
		if !strings.Contains(text, want) {
			t.Errorf("report misses %q:\n%s", want, text)
		}
	}

	cfg := parseConfig(t, r.Config("", ""))
	if cfg.Token != "${SLOWPOKE_TOKEN}" {
		t.Errorf("token placeholder: %q", cfg.Token)
	}
	types := map[string]int{}
	for _, s := range cfg.Sources {
		types[s.Type]++
	}
	if types["nginx"] != 2 || types["php-fpm"] != 1 || types["mysql-slowlog"] != 1 || types["mysql"] != 1 || types["otlp"] != 1 {
		t.Fatalf("sources: %+v", cfg.Sources)
	}
}

type testConfig struct {
	Server  string `yaml:"server"`
	Token   string `yaml:"token"`
	Host    string `yaml:"host"`
	Sources []struct {
		Type        string   `yaml:"type"`
		Name        string   `yaml:"name"`
		Path        string   `yaml:"path"`
		IgnorePaths []string `yaml:"ignore_paths"`
	} `yaml:"sources"`
}

func parseConfig(t *testing.T, raw string) testConfig {
	t.Helper()
	var cfg testConfig
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("generated config is not valid YAML: %v\n%s", err, raw)
	}
	return cfg
}

func TestNginxParserKeepsQuotedSemicolonsAndComments(t *testing.T) {
	blocks := parseNginx(`http { add_header X "a; b" always; # comment ; {
  server { server_name a.test; location /ws { proxy_pass http://x; } } }`)
	if len(blocks) != 1 || blocks[0].name != "http" {
		t.Fatalf("%+v", blocks)
	}
	http := blocks[0].block
	if len(http) != 2 || http[0].args[1] != "a; b" || http[1].name != "server" {
		t.Fatalf("%+v", http)
	}
}

// On staging the Slowpoke UI itself is an nginx site: measuring it would only add noise.
func TestConfigSkipsTheSlowpokeSite(t *testing.T) {
	r := Report{Host: "staging", Sites: []Site{
		{Name: "api-dev.example.test", AccessLog: "/var/log/nginx/api.log", HasDuration: true},
		{Name: "slowpoke-dev.example.test", Upstreams: []string{"http://127.0.0.1:8090"}},
	}}
	for _, server := range []string{"https://slowpoke-dev.example.test", "http://127.0.0.1:8090"} {
		cfg := parseConfig(t, r.Config(server, "spa_x"))
		for _, s := range cfg.Sources {
			if s.Name == "slowpoke-dev.example.test" {
				t.Errorf("server %s: the Slowpoke site is measured", server)
			}
		}
		if !strings.Contains(r.Config(server, "spa_x"), "api-dev.example.test") {
			t.Errorf("server %s: the application site is missing", server)
		}
	}
}

// Redis runs on both those machines and the agent can read it without a user.
func TestDetectFindsRedis(t *testing.T) {
	r := Scan("testdata/tuned")
	var redis *Cache
	for i := range r.Caches {
		if r.Caches[i].Engine == "redis" {
			redis = &r.Caches[i]
		}
	}
	if redis == nil || redis.Port != 6380 || !redis.Password {
		t.Fatalf("caches: %+v", r.Caches)
	}
	text := r.Text()
	for _, want := range []string{"Redis", "slowlog-log-slower-than"} {
		if !strings.Contains(text, want) {
			t.Errorf("report misses %q:\n%s", want, text)
		}
	}
	cfg := parseConfig(t, r.Config("", ""))
	found := false
	for _, s := range cfg.Sources {
		if s.Type == "redis" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sources: %+v", cfg.Sources)
	}
}

// Two PHP versions on one machine usually both call their pool "www": if the generated config
// gives them the same name their checks land on the same key and one of the two disappears.
func TestPoolsWithTheSameNameAreToldApart(t *testing.T) {
	r := Report{Host: "web-1", Pools: []Pool{
		{Name: "www", Version: "7.4", SlowLogOn: true, SlowLog: "/var/log/php7.4-fpm-www.slow.log"},
		{Name: "www", Version: "8.3", SlowLogOn: true, SlowLog: "/var/log/php8.3-fpm-www.slow.log"},
		{Name: "api", Version: "8.3", SlowLogOn: true, SlowLog: "/var/log/php8.3-fpm-api.slow.log"},
	}}
	cfg := r.Config("https://s", "spa_x")
	for _, want := range []string{"name: \"www 7.4\"", "name: \"www 8.3\"", "name: api"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config misses %q:\n%s", want, cfg)
		}
	}
}

// A virtualenv under /var/www is not an application: on a real spider machine "/var/www/venv"
// was reported as a Django app next to the real one.
func TestAVirtualenvIsNotAnApplication(t *testing.T) {
	root := t.TempDir()
	write := func(p, body string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("var/www/spider/manage.py", "import django")
	write("var/www/spider/requirements.txt", "django==4.2\n")
	write("var/www/venv/pyvenv.cfg", "home = /usr/bin\n")
	write("var/www/venv/requirements.txt", "django==4.2\n")
	write("var/www/front/node_modules/express/package.json", `{"dependencies":{"express":"5.0.0"}}`)

	r := Scan(root)
	var dirs []string
	for _, a := range r.Apps {
		dirs = append(dirs, a.Dir)
	}
	if len(dirs) != 1 || !strings.HasSuffix(dirs[0], "/var/www/spider") {
		t.Fatalf("applications found: %v", dirs)
	}
}

// A backup left in sites-enabled is loaded by nginx like any other file: it can shadow the real
// site, and the report must name the real file, not the copy.
func TestABackupInSitesEnabledIsNamedAsSuch(t *testing.T) {
	root := t.TempDir()
	site := "server {\n    server_name spider.example.com;\n    access_log /var/log/nginx/access.log;\n    location / { proxy_pass http://127.0.0.1:8001; }\n}\n"
	for _, name := range []string{"spider", "spider.bak.20260902"} {
		full := filepath.Join(root, "etc/nginx/sites-enabled", name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(site), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc/nginx/nginx.conf"), []byte("http { include /etc/nginx/sites-enabled/*; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Scan(root)
	if len(r.Sites) != 1 {
		t.Fatalf("one site, seen twice: %+v", r.Sites)
	}
	if !strings.HasSuffix(r.Sites[0].File, "/sites-enabled/spider") {
		t.Errorf("the report points at the backup: %s", r.Sites[0].File)
	}
	if len(r.Warnings) == 0 || !strings.Contains(strings.Join(r.Warnings, " "), "spider.bak.20260902") {
		t.Errorf("nginx loads a backup and nobody says so: %v", r.Warnings)
	}
}

// "server_name 203.0.113.24 spider2.example.com;" is how these machines are configured: the name
// people use is the second one, and it is the one the panel, the log file and the map should show.
func TestASiteIsNamedAfterItsHostnameNotItsIP(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "etc/nginx/sites-enabled/spider")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	site := "server {\n    server_name 203.0.113.24 spider2.example.com;\n    location / { proxy_pass http://127.0.0.1:8001; }\n}\n"
	if err := os.WriteFile(full, []byte(site), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/nginx/nginx.conf"), []byte("http { include /etc/nginx/sites-enabled/*; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Scan(root)
	if len(r.Sites) != 1 || r.Sites[0].Name != "spider2.example.com" {
		t.Fatalf("site name: %+v", r.Sites)
	}
	if got := r.Sites[0].LogPath(); got != "/var/log/nginx/spider2.example.com.slowpoke.log" {
		t.Fatalf("log path: %s", got)
	}
}

// scanner6, 2026-09-18: a Node app behind one "location /" that carries the WebSocket headers.
// The whole site was marked "long by design", so the generated configuration ignored every path
// of it: a machine that looked monitored and reported not a single request. Upgrades are
// already recognised by their 101 status in the parser, so a location that covers the whole
// site must never become an ignore rule.
func TestTheWholeSiteIsNeverIgnored(t *testing.T) {
	root := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("etc/nginx/nginx.conf", "http { include /etc/nginx/sites-enabled/*; }")
	write("etc/nginx/sites-enabled/spider.example.com", `server {
	server_name spider.example.com;
	access_log /var/log/nginx/access.log;
	location / {
		proxy_pass http://127.0.0.1:3000;
		proxy_set_header Upgrade $http_upgrade;
		proxy_set_header Connection "upgrade";
	}
	location /events {
		proxy_pass http://127.0.0.1:3000;
		proxy_buffering off;
	}
}`)

	r := Scan(root)
	if len(r.Sites) != 1 {
		t.Fatalf("sites: %+v", r.Sites)
	}
	site := r.Sites[0]
	for _, p := range site.LongPaths {
		if p == "*" {
			t.Fatalf("the whole site would be ignored: %+v", site.LongPaths)
		}
	}
	// The streaming sub-path is still worth ignoring: it answers 200 and stays open.
	if len(site.LongPaths) != 1 || site.LongPaths[0] != "/events*" {
		t.Fatalf("long paths: %+v", site.LongPaths)
	}
	if strings.Contains(r.Config("https://x", "spa_1"), `ignore_paths: ["*"]`) {
		t.Fatalf("configuration ignores everything:\n%s", r.Config("https://x", "spa_1"))
	}
}
