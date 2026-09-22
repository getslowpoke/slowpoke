package detect

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const nginxFormat = `log_format slowpoke '$remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent '
                    '"$http_referer" "$http_user_agent" rt=$request_time';`

// Text is the report for a person: what was found, what is already fine, and for everything
// else the exact change to make. Nothing is changed by detect itself.
func (r Report) Text() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	title := "Slowpoke detect"
	if r.Host != "" {
		title += " on " + r.Host
	}
	if r.OS != "" {
		title += " (" + r.OS + ")"
	}
	w("%s", title)
	w("Read-only: nothing on this machine was changed.")

	w("")
	w("WEB SERVER")
	if !r.Nginx {
		w("  · nginx not found (Apache and Caddy: see docs/install/agent.md#web-servers)")
	}
	missingFormat := false
	for _, s := range r.Sites {
		kind := "PHP"
		if s.FastCGI == "" {
			kind = "static"
			if len(s.Upstreams) > 0 {
				kind = "proxy to " + strings.Join(s.Upstreams, ", ")
			}
		}
		target := s.Root
		if target == "" {
			target = kind
		} else {
			target += " (" + kind + ")"
		}
		w("  ✓ nginx site %s → %s", s.Name, target)
		if s.HasDuration {
			w("    ✓ access log %s has the request duration", s.AccessLog)
		} else {
			missingFormat = true
			w("    ! access log %s has no request duration (format %q)", s.AccessLog, s.Format)
			if s.InheritedLog && s.AccessLog != "" {
				w("      In the server block of %s, add these two lines (an access_log in a server block", s.File)
				w("      replaces the inherited one, so the existing log is repeated to keep receiving requests):")
				if s.Format == "combined" {
					w("        access_log %s;", s.AccessLog)
				} else {
					w("        access_log %s %s;", s.AccessLog, s.Format)
				}
			} else {
				w("      In the server block of %s, next to the existing access_log, add a second one:", s.File)
			}
			w("        access_log %s slowpoke;", s.LogPath())
		}
		if len(s.LongPaths) > 0 {
			w("    · %s stays open by design (WebSocket or streaming): ignored so it does not look slow", strings.Join(s.LongPaths, ", "))
		}
	}
	if missingFormat {
		w("")
		if r.ConfD {
			w("  → Define the log format once, in /etc/nginx/conf.d/slowpoke.conf:")
		} else {
			w("  → Define the log format once, in the http block of /etc/nginx/nginx.conf:")
		}
		for _, line := range strings.Split(nginxFormat, "\n") {
			w("      %s", line)
		}
		w("    then: nginx -t && systemctl reload nginx")
		w("    Existing logs are untouched: the new one only adds the duration for Slowpoke.")
	}
	if missingFormat {
		w("")
		w("  → Or let the agent do all of it, and undo it if anything is off:")
		w("      sudo slowpoke-agent enable-duration --dry-run   # what it would write")
		w("      sudo slowpoke-agent enable-duration             # writes it, tests it, reloads, checks the sites answer")
		w("      sudo slowpoke-agent enable-duration --undo      # back the way it was")
	}

	if len(r.Pools) > 0 {
		w("")
		w("PHP-FPM")
		type service struct{ binary, unit string }
		services := map[service]bool{}
		for _, p := range r.Pools {
			label := "PHP-FPM"
			if p.Version != "" {
				label += " " + p.Version
			}
			w("  ✓ %s pool %s (%s)", label, p.Name, p.File)
			if p.SlowLogOn {
				w("    ✓ slow log on: %s", p.SlowLog)
				continue
			}
			services[service{p.binary(), p.service()}] = true
			w("    ! slow log is off: PHP stalls (where workers wait, file and line) are invisible")
			w("      Add to the [%s] pool in %s:", p.Name, p.File)
			w("        request_slowlog_timeout = 1s")
			w("        slowlog = %s", p.SlowLogPath())
		}
		for svc := range services {
			w("    then: %s -t && systemctl reload %s", svc.binary, svc.unit)
		}
	}

	w("")
	w("DATABASES ON THIS MACHINE")
	if len(r.Databases) == 0 {
		w("  · none running here")
	}
	for _, d := range r.Databases {
		switch d.Engine {
		case "mysql":
			w("  ✓ MySQL / MariaDB (%s)", d.Process)
			if d.SlowLog != "" {
				w("    ✓ slow query log: %s", d.SlowLog)
			} else {
				w("    · slow query log off: SET GLOBAL slow_query_log = 1; SET GLOBAL long_query_time = 1;")
			}
			w("    → create a read-only user for the agent:")
			w("        CREATE USER 'slowpoke'@'127.0.0.1' IDENTIFIED BY 'a-long-password';")
			w("        GRANT PROCESS, SELECT ON *.* TO 'slowpoke'@'127.0.0.1';")
		case "postgres":
			w("  ✓ PostgreSQL")
			w("    → needs pg_stat_statements and a read-only user: see docs/install/agent.md#postgresql")
		}
	}

	for _, c := range r.Caches {
		w("")
		w("CACHE")
		w("  ✓ Redis on port %d%s", c.Port, map[bool]string{true: " (password set)", false: ""}[c.Password])
		w("    · read with no user: INFO counters (hit rate, memory, evictions) and the slow command log")
		switch c.SlowLog {
		case "-1":
			w("    ! the slow command log is off (slowlog-log-slower-than -1). To turn it on:")
			w("        redis-cli CONFIG SET slowlog-log-slower-than 10000 && redis-cli CONFIG REWRITE")
		case "":
			w("    · slow command log at the Redis default (slowlog-log-slower-than 10000, i.e. 10 ms)")
		default:
			w("    ✓ slow command log on: slowlog-log-slower-than %s microseconds", c.SlowLog)
		}
		if c.Password {
			w("    → put the password in the redis source of agent.yaml, as ${SLOWPOKE_REDIS_PASSWORD}")
		}
	}

	if len(r.Apps) > 0 {
		w("")
		w("APPLICATIONS")
	}
	remote := map[string]bool{}
	for _, a := range r.Apps {
		name := a.Framework
		if a.Version != "" {
			name += " " + a.Version
		}
		where := a.Dir
		if a.Site != "" {
			where += ", site " + a.Site
		}
		w("  ✓ %s (%s)", name, where)
		switch {
		case a.HasSlowpoke && a.HasOpenTelemetry:
			w("    ! both slowpoke/%s and OpenTelemetry auto-instrumentation are installed: queries would be traced twice", strings.ToLower(a.Framework))
			w("      Remove the open-telemetry/opentelemetry-auto-* packages.")
		case a.HasSlowpoke:
			w("    ✓ slowpoke/%s installed: the file and line of every query reach the agent", strings.ToLower(a.Framework))
		case a.Framework == "Laravel":
			w("    → file and line of every query, N+1 per route:")
			w("        composer require slowpoke/laravel")
			w("      and deploy. It sends to the agent on 127.0.0.1:4318 after each response (budget 0.1 s).")
		case a.Framework == "Symfony":
			w("    → file and line of every query, N+1 per route:")
			w("        composer require slowpoke/symfony")
			w("      and enable the bundle in config/bundles.php. It sends to the agent on 127.0.0.1:4318")
			w("      after each response (budget 0.1 s), and traces cron commands and Messenger too.")
		case a.Language == "PHP":
			w("    → routes and queries: docs/instrument/php.md")
		case a.Language == "Python":
			w("    → file and line of every query, N+1 per route:")
			w("        pip install slowpoke-python")
			w("      then two lines for the framework: docs/instrument/python.md. It also traces Celery")
			w("      tasks and the management commands cron runs.")
		case a.Language == "Node.js":
			w("    → file and line of every query, N+1 per route:")
			w("        npm install @slowpokedev/node")
			w("      then two lines in the app: docs/instrument/node.md. It also traces BullMQ jobs")
			w("      and the scripts cron runs.")
		case a.Language == "Go":
			w("    → routes and queries: docs/instrument/go.md")
		default:
			w("    → routes and queries: docs/instrument/README.md")
		}
		if a.DB.Engine != "" {
			if a.DB.Local {
				w("    · database %s %q on this machine", a.DB.Engine, a.DB.Name)
			} else if !remote[a.DB.Host] {
				remote[a.DB.Host] = true
				w("    · database %s %q on %s, another machine: install an agent there too to read its slow queries and plans", a.DB.Engine, a.DB.Name, a.DB.Host)
			}
		}
	}

	if len(r.Warnings) > 0 {
		w("")
		w("WORTH KNOWING")
		for _, warning := range r.Warnings {
			w("  ! %s", warning)
		}
	}

	w("")
	w("NEXT")
	w("  1. Make the changes marked with ! (or skip them: the agent reads what exists).")
	w("  2. Write the configuration: slowpoke-agent detect -server https://your-slowpoke -token spa_… -write /etc/slowpoke/agent.yaml")
	w("  3. Start the agent: systemctl enable --now slowpoke-agent")
	return b.String()
}

// Config is an agent.yaml for this machine. Paths that the report asks to create (a timed
// nginx log, a PHP-FPM slow log) are already used, so the agent picks them up once they exist.
func (r Report) Config(server, token string) string {
	if server == "" {
		server = "https://slowpoke.example.com"
	}
	if token == "" {
		token = "${SLOWPOKE_TOKEN}"
	}
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("# Generated by slowpoke-agent detect. Review it, then: systemctl restart slowpoke-agent")
	w("# Reference: docs/reference/agent.md")
	w("server: %s", quote(server))
	w("token: %s", quote(token))
	if r.Host != "" {
		w("host: %s", quote(r.Host))
	}
	w("sources:")
	w("  - type: host")

	logs := map[string]bool{}
	for _, s := range r.Sites {
		p := s.LogPath()
		if logs[p] || servesSlowpoke(s, server) {
			continue
		}
		logs[p] = true
		w("  - type: nginx")
		w("    name: %s", quote(s.Name))
		w("    path: %s", quote(p))
		if len(s.LongPaths) > 0 {
			quoted := make([]string, len(s.LongPaths))
			for i, lp := range s.LongPaths {
				quoted[i] = quote(lp)
			}
			w("    ignore_paths: [%s]", strings.Join(quoted, ", "))
		}
	}
	// Two PHP versions usually both call their pool "www": the name carries the version so the
	// two are separate sources, separate checks and separate boxes on the map.
	shared := map[string]int{}
	for _, p := range r.Pools {
		shared[p.Name]++
	}
	for _, p := range r.Pools {
		name := p.Name
		if shared[name] > 1 && p.Version != "" {
			name += " " + p.Version
		}
		w("  - type: php-fpm")
		w("    name: %s", quote(name))
		w("    path: %s", quote(p.SlowLogPath()))
	}
	for _, d := range r.Databases {
		switch d.Engine {
		case "mysql":
			if d.SlowLog != "" {
				w("  - type: mysql-slowlog")
				w("    path: %s", quote(d.SlowLog))
			}
			w("  - type: mysql")
			w(`    dsn: "slowpoke:${SLOWPOKE_DB_PASSWORD}@tcp(127.0.0.1:3306)/"`)
		case "postgres":
			w("  - type: postgres")
			w(`    dsn: "postgres://slowpoke:${SLOWPOKE_DB_PASSWORD}@127.0.0.1:5432/postgres"`)
		}
	}
	for _, c := range r.Caches {
		w("  - type: redis")
		w("    name: cache")
		w("    addr: %s", quote("127.0.0.1:"+strconv.Itoa(c.Port)))
		if c.Password {
			w(`    password: ${SLOWPOKE_REDIS_PASSWORD}`)
		}
	}
	if len(r.Apps) > 0 {
		w("  # Traces from the applications (framework packages or OpenTelemetry)")
		w("  - type: otlp")
		w("    listen: 127.0.0.1:4318")
	}
	return b.String()
}

// servesSlowpoke tells the site in front of the Slowpoke server itself: by name, or by the
// local address it proxies to.
// ServesSlowpoke tells the site that publishes the Slowpoke panel itself, by name or by the
// local address it proxies to: nobody wants missions about their own monitoring.
func ServesSlowpoke(s Site, server string) bool { return servesSlowpoke(s, server) }

func servesSlowpoke(s Site, server string) bool {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return false
	}
	if s.Name == u.Hostname() {
		return true
	}
	for _, up := range s.Upstreams {
		if target, err := url.Parse(up); err == nil && target.Host == u.Host {
			return true
		}
	}
	return false
}

func quote(s string) string {
	if s != "" && !strings.ContainsAny(s, ":#{}[],&*?|<>=!%@`'\"\\ ") && !strings.HasPrefix(s, "$") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
