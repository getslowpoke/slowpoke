// Package detect looks at a machine and says what Slowpoke can read on it: web sites, PHP-FPM
// pools, databases and applications with their framework. It only reads files, so it is safe to
// run on a production server, and it tells exactly what to change and what to install.
package detect

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Report struct {
	Host      string
	OS        string
	Warnings  []string // things about the machine itself worth saying, in plain words
	Nginx     bool
	ConfD     bool // nginx includes conf.d/*.conf in its http block
	Sites     []Site
	Pools     []Pool
	Databases []Database
	Caches    []Cache
	Apps      []App
}

// Cache is a Redis (or compatible) server running on the machine: no user needed, the agent
// reads INFO and the slow command log.
type Cache struct {
	Engine   string
	Port     int
	Password bool
	SlowLog  string // slowlog-log-slower-than as configured, "" when not set
}

// Site is one nginx server block (redirect-only blocks are merged away).
type Site struct {
	Name        string
	File        string
	Root        string
	AccessLog   string
	Format      string
	HasDuration bool
	// InheritedLog: the access log comes from the http block. Adding one to the server block
	// replaces it for this site, so the report repeats it.
	InheritedLog bool
	FastCGI      string
	Pool         string
	Upstreams    []string
	LongPaths    []string // WebSocket and streaming locations, long by design
}

// LogPath is the access log the agent should read: the existing one when it has the request
// duration, otherwise a dedicated log added next to it.
func (s Site) LogPath() string {
	if s.HasDuration {
		return s.AccessLog
	}
	return "/var/log/nginx/" + s.Name + ".slowpoke.log"
}

type Pool struct {
	Name        string
	Version     string
	File        string
	Listen      string
	SlowLog     string
	SlowTimeout string
	SlowLogOn   bool
}

func (p Pool) SlowLogPath() string {
	if p.SlowLogOn {
		return p.SlowLog
	}
	return "/var/log/php" + p.Version + "-fpm-" + p.Name + ".slow.log"
}

func (p Pool) binary() string { return "php-fpm" + p.Version }

func (p Pool) service() string {
	if p.Version == "" {
		return "php-fpm"
	}
	return "php" + p.Version + "-fpm"
}

type Database struct {
	Engine  string // mysql | postgres
	Process string
	SlowLog string // MySQL slow query log, when enabled in the configuration
}

type App struct {
	Dir              string
	Language         string
	Framework        string
	Version          string
	Site             string
	DB               DBRef
	HasSlowpoke      bool
	HasOpenTelemetry bool
}

type DBRef struct {
	Engine, Host, Port, Name string
	Local                    bool
}

// Scan reads the machine whose filesystem starts at root ("/" on a real server).
func Scan(root string) Report {
	s := scanner{root: root}
	r := Report{Host: strings.TrimSpace(s.read("/etc/hostname")), OS: osName(s.read("/etc/os-release"))}
	s.nginx(&r)
	s.pools(&r)
	s.linkPools(&r)
	s.databases(&r)
	s.caches(&r)
	s.apps(&r)
	return r
}

type scanner struct{ root string }

func (s scanner) path(p string) string { return filepath.Join(s.root, p) }

func (s scanner) read(p string) string {
	raw, err := os.ReadFile(s.path(p))
	if err != nil {
		return ""
	}
	return string(raw)
}

func (s scanner) exists(p string) bool {
	_, err := os.Stat(s.path(p))
	return err == nil
}

func (s scanner) glob(pattern string) []string {
	matches, _ := filepath.Glob(s.path(pattern))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		rel, err := filepath.Rel(s.root, m)
		if err != nil {
			continue
		}
		out = append(out, "/"+filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

func osName(release string) string {
	for _, line := range strings.Split(release, "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

// --- nginx ---

func (s scanner) nginx(r *Report) {
	main := s.read("/etc/nginx/nginx.conf")
	if main == "" {
		return
	}
	r.Nginx = true
	formats := map[string]string{}
	httpLog, httpFormat := "", ""
	var serverFiles []string

	for _, d := range parseNginx(main) {
		if d.name != "http" {
			continue
		}
		for _, h := range d.block {
			switch h.name {
			case "log_format":
				addFormat(formats, h.args)
			case "access_log":
				httpLog, httpFormat = accessLog(h.args)
			case "include":
				if len(h.args) > 0 {
					if strings.Contains(h.args[0], "conf.d") {
						r.ConfD = true
					}
					serverFiles = append(serverFiles, s.glob(h.args[0])...)
				}
			}
		}
	}
	if len(serverFiles) == 0 {
		serverFiles = append(s.glob("/etc/nginx/conf.d/*.conf"), s.glob("/etc/nginx/sites-enabled/*")...)
		r.ConfD = true
	}

	for _, f := range serverFiles {
		if looksLikeBackup(f) {
			r.Warnings = append(r.Warnings, "nginx loads "+f+": it looks like a leftover copy, and a copy with the same server_name shadows nothing but confuses everybody. Move it out of sites-enabled.")
		}
	}

	// Formats first: a site may use a format defined in a file read after it.
	parsed := map[string][]directive{}
	for _, f := range serverFiles {
		parsed[f] = parseNginx(s.read(f))
		for _, d := range parsed[f] {
			if d.name == "log_format" {
				addFormat(formats, d.args)
			}
		}
	}

	byName := map[string]int{}
	for _, f := range serverFiles {
		for _, d := range parsed[f] {
			if d.name != "server" {
				continue
			}
			site := Site{File: f, AccessLog: httpLog, Format: httpFormat, InheritedLog: true}
			redirectOnly := false
			walk(d.block, func(x directive, depth int) {
				switch x.name {
				case "server_name":
					// "server_name 203.0.113.24 spider2.example.com;": the name people type is
					// the hostname, and it names the site, its log file and its box on the map.
					if site.Name == "" {
						site.Name = pickName(x.args)
					}
				case "root":
					if depth == 0 && len(x.args) > 0 {
						site.Root = x.args[0]
					}
				case "access_log":
					if depth == 0 {
						site.AccessLog, site.Format = accessLog(x.args)
						site.InheritedLog = false
					}
				case "fastcgi_pass":
					if len(x.args) > 0 {
						site.FastCGI = x.args[0]
					}
				case "proxy_pass":
					if len(x.args) > 0 {
						site.Upstreams = append(site.Upstreams, x.args[0])
					}
				case "return":
					if depth == 0 {
						redirectOnly = true
					}
				}
			})
			for _, loc := range d.block {
				if loc.name == "location" && isLongLived(loc) {
					// "location /" with the WebSocket headers is the usual shape of a Node app
					// behind nginx: ignoring it would ignore the whole site and leave a machine
					// that looks monitored and reports nothing. The upgrades themselves are
					// recognised by their 101 status, wherever they happen.
					if p := locationPath(loc.args); p != "" && p != "*" {
						site.LongPaths = append(site.LongPaths, p)
					}
				}
			}
			if site.Name == "" || site.AccessLog == "off" {
				continue
			}
			if redirectOnly && site.Root == "" && site.FastCGI == "" && len(site.Upstreams) == 0 {
				continue
			}
			site.HasDuration = strings.Contains(formats[site.Format], "$request_time") || strings.Contains(formats[site.Format], "$upstream_response_time")
			if i, seen := byName[site.Name]; seen {
				// nginx keeps the first block for a name and ignores the later ones, unless the
				// first comes from a file that is plainly a leftover copy.
				if looksLikeBackup(r.Sites[i].File) && !looksLikeBackup(site.File) {
					r.Sites[i] = site
				}
				continue
			}
			byName[site.Name] = len(r.Sites)
			r.Sites = append(r.Sites, site)
		}
	}
}

func addFormat(formats map[string]string, args []string) {
	if len(args) < 2 {
		return
	}
	body := args[1:]
	if strings.HasPrefix(body[0], "escape=") && len(body) > 1 {
		body = body[1:]
	}
	formats[args[0]] = strings.Join(body, " ")
}

func accessLog(args []string) (string, string) {
	if len(args) == 0 {
		return "", ""
	}
	if args[0] == "off" {
		return "off", ""
	}
	format := "combined"
	if len(args) > 1 && !strings.Contains(args[1], "=") {
		format = args[1]
	}
	return args[0], format
}

// isLongLived tells WebSocket, SSE and long-polling locations: their requests last minutes
// by design and would look like the slowest endpoints of the application.
func isLongLived(loc directive) bool {
	long := false
	walk(loc.block, func(x directive, _ int) {
		v := strings.ToLower(strings.Join(x.args, " "))
		if x.name == "proxy_set_header" && strings.Contains(v, "upgrade") {
			long = true
		}
		if x.name == "proxy_buffering" && v == "off" {
			long = true
		}
	})
	return long
}

func locationPath(args []string) string {
	if len(args) == 0 {
		return ""
	}
	p := args[len(args)-1]
	if len(args) > 1 && args[0] != "^~" {
		return "" // regular expressions and exact matches: not a prefix we can express
	}
	if !strings.HasPrefix(p, "/") {
		return ""
	}
	return strings.TrimSuffix(p, "/") + "*"
}

// --- PHP-FPM ---

var fpmVersionRe = regexp.MustCompile(`/php/(\d+\.\d+)/fpm/`)

func (s scanner) pools(r *Report) {
	files := s.glob("/etc/php/*/fpm/pool.d/*.conf")
	files = append(files, s.glob("/etc/php-fpm.d/*.conf")...)
	files = append(files, s.glob("/usr/local/etc/php-fpm.d/*.conf")...)
	for _, f := range files {
		version := ""
		if m := fpmVersionRe.FindStringSubmatch(f); m != nil {
			version = m[1]
		}
		var pool *Pool
		flush := func() {
			if pool != nil {
				pool.SlowLogOn = pool.SlowLog != "" && pool.SlowTimeout != "" && strings.TrimRight(pool.SlowTimeout, "s") != "0"
				r.Pools = append(r.Pools, *pool)
			}
		}
		sc := bufio.NewScanner(strings.NewReader(s.read(f)))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				flush()
				name := strings.Trim(line, "[]")
				if name == "global" {
					pool = nil
					continue
				}
				pool = &Pool{Name: name, Version: version, File: f}
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok || pool == nil {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), `"`)
			switch strings.TrimSpace(key) {
			case "listen":
				pool.Listen = value
			case "slowlog":
				pool.SlowLog = strings.ReplaceAll(value, "$pool", pool.Name)
			case "request_slowlog_timeout":
				pool.SlowTimeout = value
			}
		}
		flush()
	}
}

func (s scanner) linkPools(r *Report) {
	for i := range r.Sites {
		target := strings.TrimPrefix(r.Sites[i].FastCGI, "unix:")
		if target == "" {
			continue
		}
		for _, p := range r.Pools {
			if p.Listen == target || strings.HasSuffix(target, ":"+strings.TrimPrefix(p.Listen, "127.0.0.1:")) && !strings.Contains(p.Listen, "/") {
				r.Sites[i].Pool = p.Name
				break
			}
		}
	}
}

// --- databases ---

func (s scanner) databases(r *Report) {
	seen := map[string]bool{}
	for _, f := range s.glob("/proc/*/comm") {
		comm := strings.TrimSpace(s.read(f))
		engine := map[string]string{"mysqld": "mysql", "mariadbd": "mysql", "postgres": "postgres"}[comm]
		if engine == "" || seen[engine] {
			continue
		}
		seen[engine] = true
		db := Database{Engine: engine, Process: comm}
		if engine == "mysql" {
			db.SlowLog = s.mysqlSlowLog()
		}
		r.Databases = append(r.Databases, db)
	}
}

// caches looks for Redis: its process, then the configuration for port, password and slow log.
func (s scanner) caches(r *Report) {
	running := false
	for _, f := range s.glob("/proc/*/comm") {
		if comm := strings.TrimSpace(s.read(f)); comm == "redis-server" || comm == "valkey-server" {
			running = true
		}
	}
	if !running {
		return
	}
	c := Cache{Engine: "redis", Port: 6379}
	for _, f := range append(s.glob("/etc/redis/redis.conf"), s.glob("/etc/redis.conf")...) {
		for _, line := range strings.Split(s.read(f), "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
			if !ok || strings.HasPrefix(key, "#") {
				continue
			}
			value = strings.TrimSpace(value)
			switch key {
			case "port":
				if n, err := strconv.Atoi(value); err == nil && n > 0 {
					c.Port = n
				}
			case "requirepass":
				c.Password = value != ""
			case "slowlog-log-slower-than":
				c.SlowLog = value
			}
		}
	}
	r.Caches = append(r.Caches, c)
}

func (s scanner) mysqlSlowLog() string {
	on, file := false, ""
	files := append(s.glob("/etc/mysql/*.cnf"), s.glob("/etc/mysql/*/*.cnf")...)
	files = append(files, s.glob("/etc/my.cnf")...)
	files = append(files, s.glob("/etc/my.cnf.d/*.cnf")...)
	for _, f := range files {
		for _, line := range strings.Split(s.read(f), "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok || strings.HasPrefix(key, "#") {
				continue
			}
			key = strings.ReplaceAll(strings.TrimSpace(key), "-", "_")
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			switch key {
			case "slow_query_log":
				on = value == "1" || strings.EqualFold(value, "on")
			case "slow_query_log_file":
				file = value
			}
		}
	}
	if !on {
		return ""
	}
	return file
}

// --- applications ---

func (s scanner) apps(r *Report) {
	found := map[string]bool{}
	for _, site := range r.Sites {
		if site.Root == "" {
			continue
		}
		dir := site.Root
		if base := path.Base(dir); base == "public" || base == "web" || base == "pub" {
			dir = path.Dir(dir)
		}
		if app, ok := s.app(dir); ok {
			app.Site = site.Name
			found[dir] = true
			r.Apps = append(r.Apps, app)
		}
	}
	// Applications behind a proxy_pass have no root: look where apps usually live.
	for _, candidate := range append(s.glob("/var/www/*"), s.glob("/srv/*")...) {
		dir := candidate
		if s.exists(candidate + "/current") {
			dir = candidate + "/current"
		}
		if found[dir] {
			continue
		}
		if app, ok := s.app(dir); ok {
			found[dir] = true
			r.Apps = append(r.Apps, app)
		}
	}
}

// pickName takes the first real hostname of a server_name line, falling back to whatever is
// there (an IP address) when there is nothing better.
func pickName(args []string) string {
	fallback := ""
	for _, name := range args {
		if name == "_" || strings.HasPrefix(name, "~") || strings.HasPrefix(name, "*") {
			continue
		}
		if fallback == "" {
			fallback = name
		}
		if net.ParseIP(name) == nil {
			return name
		}
	}
	return fallback
}

// looksLikeBackup tells a file kept "just in case" from a real site: nginx loads everything in
// sites-enabled, so these are live configuration nobody thinks about.
func looksLikeBackup(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(name, "~") {
		return true
	}
	for _, mark := range []string{".bak", ".old", ".save", ".orig", ".disabled", ".dpkg-dist", ".dpkg-old", ".rpmsave", ".copy"} {
		if strings.Contains(name, mark) {
			return true
		}
	}
	return false
}

// notAnApplication: a virtualenv, a dependency directory or a build output that happens to have
// a requirements.txt or a package.json inside it.
func (s scanner) notAnApplication(dir string) bool {
	if s.exists(dir + "/pyvenv.cfg") {
		return true
	}
	switch strings.ToLower(path.Base(dir)) {
	case "venv", ".venv", "env", "virtualenv", "node_modules", "vendor", "site-packages", "dist", "build":
		return true
	}
	return strings.Contains(dir, "/node_modules/") || strings.Contains(dir, "/site-packages/") || strings.Contains(dir, "/vendor/")
}

func (s scanner) app(dir string) (App, bool) {
	app := App{Dir: dir}
	if s.notAnApplication(dir) {
		return app, false
	}
	switch {
	case s.exists(dir + "/composer.json"):
		app.Language = "PHP"
		lock := composerLock(s.read(dir + "/composer.lock"))
		require := composerRequire(s.read(dir + "/composer.json"))
		has := func(pkg string) bool { _, a := lock[pkg]; _, b := require[pkg]; return a || b }
		switch {
		case has("laravel/framework"):
			app.Framework, app.Version = "Laravel", strings.TrimPrefix(lock["laravel/framework"], "v")
			app.DB = laravelDB(s.read(dir + "/.env"))
		case has("symfony/framework-bundle"):
			app.Framework, app.Version = "Symfony", strings.TrimPrefix(lock["symfony/framework-bundle"], "v")
		case s.exists(dir + "/wp-config.php"):
			app.Framework = "WordPress"
		default:
			app.Framework = "PHP"
		}
		app.HasSlowpoke = has("slowpoke/laravel") || has("slowpoke/symfony")
		for pkg := range lock {
			if strings.HasPrefix(pkg, "open-telemetry/opentelemetry-auto-") {
				app.HasOpenTelemetry = true
			}
		}
	case s.exists(dir + "/wp-config.php"):
		app.Language, app.Framework = "PHP", "WordPress"
	case s.exists(dir+"/manage.py") || s.exists(dir+"/requirements.txt") || s.exists(dir+"/pyproject.toml"):
		app.Language = "Python"
		deps := strings.ToLower(s.read(dir+"/requirements.txt") + "\n" + s.read(dir+"/pyproject.toml"))
		for _, fw := range []struct{ pkg, name string }{{"django", "Django"}, {"fastapi", "FastAPI"}, {"flask", "Flask"}} {
			if v, ok := pythonDep(deps, fw.pkg); ok {
				app.Framework, app.Version = fw.name, v
				break
			}
		}
		if app.Framework == "" && s.exists(dir+"/manage.py") {
			app.Framework = "Django"
		}
		if app.Framework == "" {
			app.Framework = "Python"
		}
	case s.exists(dir + "/package.json"):
		app.Language = "Node.js"
		deps := packageDeps(s.read(dir + "/package.json"))
		for _, fw := range []struct{ pkg, name string }{{"next", "Next.js"}, {"@nestjs/core", "NestJS"}, {"fastify", "Fastify"}, {"express", "Express"}, {"koa", "Koa"}} {
			if v, ok := deps[fw.pkg]; ok {
				app.Framework, app.Version = fw.name, strings.TrimLeft(v, "^~>=")
				break
			}
		}
		if app.Framework == "" {
			return app, false // front-end tooling or a static site
		}
	case s.exists(dir + "/go.mod"):
		app.Language, app.Framework = "Go", "Go"
	case s.exists(dir+"/Gemfile") && strings.Contains(s.read(dir+"/Gemfile"), "rails"):
		app.Language, app.Framework = "Ruby", "Rails"
	default:
		return app, false
	}
	return app, true
}

func composerLock(raw string) map[string]string {
	var lock struct {
		Packages []struct{ Name, Version string } `json:"packages"`
	}
	out := map[string]string{}
	if json.Unmarshal([]byte(raw), &lock) == nil {
		for _, p := range lock.Packages {
			out[p.Name] = p.Version
		}
	}
	return out
}

func composerRequire(raw string) map[string]string {
	var c struct {
		Require map[string]string `json:"require"`
	}
	_ = json.Unmarshal([]byte(raw), &c)
	return c.Require
}

func packageDeps(raw string) map[string]string {
	var p struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	_ = json.Unmarshal([]byte(raw), &p)
	return p.Dependencies
}

var pythonReqRe = regexp.MustCompile(`(?m)^\s*"?([a-z0-9_.-]+)\s*(?:\[[^\]]*\])?\s*(?:[=~<>!]=?\s*([0-9][0-9a-z.]*))?`)

func pythonDep(deps, pkg string) (string, bool) {
	for _, m := range pythonReqRe.FindAllStringSubmatch(deps, -1) {
		if m[1] == pkg {
			return m[2], true
		}
	}
	return "", false
}

// laravelDB reads where the database is from .env. Only the connection, host, port and
// name are kept: passwords are never read into the report.
func laravelDB(env string) DBRef {
	values := map[string]string{}
	for _, line := range strings.Split(env, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "DB_CONNECTION", "DB_HOST", "DB_PORT", "DB_DATABASE", "DB_SOCKET":
			values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	engine := map[string]string{"mysql": "mysql", "mariadb": "mysql", "pgsql": "postgres"}[values["DB_CONNECTION"]]
	if engine == "" {
		return DBRef{}
	}
	host := values["DB_HOST"]
	local := host == "" || host == "127.0.0.1" || host == "localhost" || host == "::1" || values["DB_SOCKET"] != ""
	return DBRef{Engine: engine, Host: host, Port: values["DB_PORT"], Name: values["DB_DATABASE"], Local: local}
}
