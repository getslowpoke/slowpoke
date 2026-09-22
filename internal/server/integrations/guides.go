package integrations

import (
	"fmt"
	"strings"
)

// Step is one thing to do, ready to copy: SQL to run as an administrator, a shell command,
// a piece of agent.yaml or of a server configuration, or a plain note.
type Step struct {
	Kind  string `json:"kind"` // sql | shell | yaml | ini | nginx | text
	Where string `json:"where"`
	Code  string `json:"code"`
}

// Guide turns one capability on (when it is not working) or off (when it is optional and on).
type Guide struct {
	// Cost says, in one honest line, what this costs the monitored machine. Empty when it costs
	// nothing measurable: silence here means "no impact", so it must never be left out by mistake.
	Cost string `json:"cost,omitempty"`
	Enable  []Step `json:"enable,omitempty"`
	Disable []Step `json:"disable,omitempty"`
}

const (
	whereMySQL    = "MySQL, as an administrator"
	wherePostgres = "PostgreSQL, as a superuser"
	whereAgent    = "/etc/slowpoke/agent.yaml"
	whereMachine  = "On the machine"
	whereMyCnf    = "/etc/mysql/conf.d/slowpoke.cnf (kept after a MySQL restart)"
	whereSystemd  = "/etc/systemd/system/slowpoke-agent.service, [Service] section"
	defaultSlow   = "/var/log/mysql/mysql-slow.log"
)

var restartAgent = Step{Kind: "shell", Where: whereMachine, Code: "sudo systemctl restart slowpoke-agent"}

// guides fills the instructions of a check from its items and facts. Everything that comes
// from the agent (user names, schemas, paths) is quoted for the language it lands in.
func guides(c *Check) {
	c.Guides = map[string]Guide{}
	for _, it := range c.Items {
		var g Guide
		switch c.Type {
		case "mysql":
			g = mysqlGuide(c, it.Key, it.Status, it.Reason, it.Detail, it.Optional)
		case "postgres":
			g = postgresGuide(c, it.Key, it.Status, it.Reason, it.Optional)
		case "redis":
			g = redisGuide(c, it.Key, it.Status, it.Reason)
		case "host":
			g = hostGuide(it.Key, it.Status)
		case "nginx", "apache", "caddy", "php-fpm", "mysql-slowlog":
			g = fileGuide(c, it.Key, it.Status, it.Reason)
		case "otlp":
			g = otlpGuide(c, it.Status, it.Reason)
		case "found":
			g = foundGuide(c)
		}
		if len(g.Enable) > 0 || len(g.Disable) > 0 {
			c.Guides[it.Key] = g
		}
	}
	c.Remove = removeGuide(c)
}

func sqlString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func mysqlIdent(s string) string {
	if s == "*" {
		return "*"
	}
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}
func pgIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func (c *Check) schemas() []string {
	var out []string
	for _, s := range strings.Split(c.Facts["schemas"], ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func yamlList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = s
		if strings.ContainsAny(s, ":#[]{},&*!|>'\"%@` ") {
			quoted[i] = `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
		}
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// mysqlGrantee is the agent's account as MySQL writes it: 'user'@'host'.
func (c *Check) mysqlGrantee() string {
	user := c.Facts["user"]
	if user == "" {
		name := c.Facts["dsn_user"]
		if name == "" {
			name = "slowpoke"
		}
		user = name + "@127.0.0.1"
	}
	name, host := user, "%"
	if i := strings.LastIndex(user, "@"); i >= 0 {
		name, host = user[:i], user[i+1:]
	}
	return sqlString(name) + "@" + sqlString(host)
}

func (c *Check) pgRole() string {
	user := c.Facts["user"]
	if user == "" {
		user = c.Facts["dsn_user"]
	}
	if user == "" {
		user = "slowpoke"
	}
	return pgIdent(user)
}

func (c *Check) sourceYAML(extra string) string {
	name := c.Name
	lines := fmt.Sprintf("- type: %s\n  name: %s\n", c.Type, name)
	return lines + extra
}

func mysqlSlowLogSteps(c *Check, path string) []Step {
	yaml := "- type: mysql-slowlog\n  path: " + path
	if s := c.schemas(); len(s) > 0 {
		yaml += "\n  schemas: " + yamlList(s)
	}
	return []Step{
		{Kind: "sql", Where: whereMySQL, Code: "SET GLOBAL slow_query_log_file = " + sqlString(path) + ";\nSET GLOBAL long_query_time = 1;\nSET GLOBAL slow_query_log = ON;"},
		{Kind: "ini", Where: whereMyCnf, Code: "[mysqld]\nslow_query_log = ON\nslow_query_log_file = " + path + "\nlong_query_time = 1"},
		{Kind: "yaml", Where: whereAgent + ", in sources", Code: yaml},
		restartAgent,
	}
}

func mysqlGuide(c *Check, key, status, reason, detail string, optional bool) Guide {
	who := c.mysqlGrantee()
	if status == "ok" {
		if !optional {
			return Guide{}
		}
		switch key {
		case "statistics":
			return Guide{Disable: []Step{
				{Kind: "yaml", Where: whereAgent + ", in the mysql source", Code: "digests: false"}, restartAgent,
				{Kind: "sql", Where: whereMySQL, Code: "REVOKE SELECT ON performance_schema.* FROM " + who + ";"},
			}}
		case "slow_log":
			return Guide{Disable: []Step{
				{Kind: "sql", Where: whereMySQL, Code: "SET GLOBAL slow_query_log = OFF;"},
				{Kind: "text", Where: whereMyCnf, Code: "Remove the slow_query_log lines, or the whole file if Slowpoke created it."},
				{Kind: "text", Where: whereAgent, Code: "Remove the mysql-slowlog source."}, restartAgent,
			}}
		case "plans":
			steps := []Step{{Kind: "yaml", Where: whereAgent + ", in the mysql source", Code: "explain: false"}, restartAgent}
			var revoke []string
			for _, s := range c.schemas() {
				revoke = append(revoke, "REVOKE SELECT ON "+mysqlIdent(s)+".* FROM "+who+";")
			}
			if len(revoke) > 0 {
				steps = append(steps, Step{Kind: "sql", Where: whereMySQL, Code: strings.Join(revoke, "\n")})
			}
			return Guide{Disable: steps}
		}
		return Guide{}
	}

	switch key {
	case "connection":
		address := c.Facts["address"]
		if address == "" {
			address = "127.0.0.1:3306"
		}
		return Guide{Enable: []Step{
			{Kind: "sql", Where: whereMySQL, Code: "-- A password with upper and lower case, digits and a symbol: servers with validate_password refuse simpler ones.\nCREATE USER IF NOT EXISTS " + who + " IDENTIFIED BY 'Replace-with-a-long-Random-passw0rd';\nGRANT PROCESS ON *.* TO " + who + ";\nGRANT SELECT ON performance_schema.* TO " + who + ";"},
			{Kind: "yaml", Where: whereAgent + ", in the mysql source", Code: `dsn: "` + strings.Trim(strings.Split(who, "@")[0], "'") + `:${SLOWPOKE_DB_PASSWORD}@tcp(` + address + `)/"`},
			restartAgent,
		}}
	case "statistics":
		switch reason {
		case "performance_schema_off":
			return Guide{Cost: "performance_schema keeps its statistics in memory, a few hundred MB depending on the tuning, and turning it on needs a restart of MySQL.", Enable: []Step{
				{Kind: "ini", Where: "MySQL configuration, [mysqld] section", Code: "[mysqld]\nperformance_schema = ON"},
				{Kind: "shell", Where: whereMachine + " (restarts MySQL: plan a quiet moment)", Code: "sudo systemctl restart mysql"},
			}}
		case "disabled":
			return Guide{Enable: []Step{{Kind: "yaml", Where: whereAgent + ", in the mysql source", Code: "digests: true"}, restartAgent}}
		default:
			return Guide{Enable: []Step{{Kind: "sql", Where: whereMySQL, Code: "GRANT SELECT ON performance_schema.* TO " + who + ";"}}}
		}
	case "slow_log":
		if reason == "not_read" {
			path := c.Facts["slow_log_file"]
			if path == "" {
				path = defaultSlow
			}
			yaml := "- type: mysql-slowlog\n  path: " + path
			if s := c.schemas(); len(s) > 0 {
				yaml += "\n  schemas: " + yamlList(s)
			}
			return Guide{Enable: []Step{{Kind: "yaml", Where: whereAgent + ", in sources", Code: yaml}, restartAgent}}
		}
		return Guide{Enable: mysqlSlowLogSteps(c, defaultSlow)}
	case "plans":
		switch reason {
		case "disabled":
			return Guide{Enable: []Step{{Kind: "yaml", Where: whereAgent + ", in the mysql source", Code: "explain: true"}, restartAgent}}
		case "needs_slow_log":
			return Guide{Enable: append([]Step{{Kind: "text", Where: "MySQL 5.7", Code: "MySQL 5.7 keeps no statement with its values: plans use the queries of the slow log."}}, mysqlSlowLogSteps(c, defaultSlow)...)}
		default:
			missing := strings.Split(detail, ",")
			if strings.TrimSpace(detail) == "" {
				missing = c.schemas()
			}
			var grants []string
			for _, s := range missing {
				if s = strings.TrimSpace(s); s != "" {
					grants = append(grants, "GRANT SELECT ON "+mysqlIdent(s)+".* TO "+who+";")
				}
			}
			if len(grants) == 0 {
				grants = []string{"GRANT SELECT ON *.* TO " + who + ";"}
			}
			return Guide{Enable: []Step{{Kind: "sql", Where: whereMySQL, Code: strings.Join(grants, "\n")}}}
		}
	}
	return Guide{}
}

func postgresGuide(c *Check, key, status, reason string, optional bool) Guide {
	role := c.pgRole()
	if status == "ok" {
		if !optional {
			return Guide{}
		}
		switch key {
		case "statistics":
			return Guide{Disable: []Step{{Kind: "yaml", Where: whereAgent + ", in the postgres source", Code: "digests: false"}, restartAgent,
				{Kind: "sql", Where: wherePostgres, Code: "REVOKE pg_read_all_stats FROM " + role + ";"}}}
		case "plans":
			return Guide{Disable: []Step{{Kind: "yaml", Where: whereAgent + ", in the postgres source", Code: "explain: false"}, restartAgent,
				{Kind: "sql", Where: wherePostgres, Code: "REVOKE pg_read_all_data FROM " + role + ";"}}}
		}
		return Guide{}
	}
	switch key {
	case "connection":
		return Guide{Enable: []Step{
			{Kind: "sql", Where: wherePostgres, Code: "CREATE ROLE " + role + " LOGIN PASSWORD 'a-long-random-password';\nGRANT pg_read_all_stats TO " + role + ";"},
			restartAgent,
		}}
	case "statistics":
		switch reason {
		case "pg_stat_statements_missing":
			return Guide{Enable: []Step{
				{Kind: "ini", Where: "postgresql.conf", Code: "shared_preload_libraries = 'pg_stat_statements'"},
				{Kind: "shell", Where: whereMachine + " (restarts PostgreSQL: plan a quiet moment)", Code: "sudo systemctl restart postgresql"},
				{Kind: "sql", Where: wherePostgres + ", in the database of the agent", Code: "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;"},
			}}
		case "disabled":
			return Guide{Enable: []Step{{Kind: "yaml", Where: whereAgent + ", in the postgres source", Code: "digests: true"}, restartAgent}}
		default:
			return Guide{Enable: []Step{{Kind: "sql", Where: wherePostgres, Code: "GRANT pg_read_all_stats TO " + role + ";"}}}
		}
	case "plans":
		switch reason {
		case "disabled":
			return Guide{Enable: []Step{{Kind: "yaml", Where: whereAgent + ", in the postgres source", Code: "explain: true"}, restartAgent}}
		case "needs_postgres_16":
			return Guide{Enable: []Step{{Kind: "text", Where: "PostgreSQL", Code: "pg_stat_statements keeps statements without values: plans for them need PostgreSQL 16 or newer. Statistics keep working without plans."}}}
		default:
			grant := "GRANT pg_read_all_data TO " + role + ";"
			if v := c.Facts["version"]; v != "" && v < "140000" && len(v) == 6 {
				grant = "GRANT SELECT ON ALL TABLES IN SCHEMA public TO " + role + ";"
			}
			return Guide{Enable: []Step{{Kind: "sql", Where: wherePostgres, Code: grant}}}
		}
	}
	return Guide{}
}

func fileGuide(c *Check, key, status, reason string) Guide {
	if status == "ok" {
		return Guide{}
	}
	switch {
	// The file is simply not there yet: what is missing is the line that writes it, not a permission.
	case key == "readable" && reason == "not_created_yet":
		path := c.Facts["path"]
		switch c.Type {
		case "php-fpm":
			pool := c.Name
			if pool == "" {
				pool = "www"
			}
			return Guide{Enable: []Step{
				{Kind: "ini", Where: "The [" + pool + "] pool, in /etc/php/<version>/fpm/pool.d/" + pool + ".conf", Code: "request_slowlog_timeout = 1s\nslowlog = " + path},
				{Kind: "ini", Where: "Same file: without it a reload can cut requests that are still running", Code: "process_control_timeout = 10s"},
				{Kind: "shell", Where: whereMachine, Code: "sudo systemctl reload php<version>-fpm"},
				restartAgent,
			}}
		case "mysql-slowlog":
			if path == "" {
				path = "/var/log/mysql/slow.log"
			}
			return Guide{Enable: mysqlSlowLogSteps(c, path)}
		default:
			return webLogGuide(c, path)
		}
	case key == "readable":
		return Guide{Enable: []Step{
			{Kind: "ini", Where: whereSystemd, Code: "SupplementaryGroups=adm\nAmbientCapabilities=CAP_DAC_READ_SEARCH\nCapabilityBoundingSet=CAP_DAC_READ_SEARCH"},
			{Kind: "shell", Where: whereMachine, Code: "sudo systemctl daemon-reload && sudo systemctl restart slowpoke-agent"},
			{Kind: "text", Where: "Docker", Code: "Mount the directory of " + c.Facts["path"] + " read-only in the agent container."},
		}}
	case key == "duration" && reason == "no_duration" && c.Type == "apache":
		return Guide{Enable: []Step{
			{Kind: "nginx", Where: "Apache configuration", Code: "LogFormat \"%h %l %u %t \\\"%r\\\" %>s %b \\\"%{Referer}i\\\" \\\"%{User-Agent}i\\\" %D\" slowpoke\nCustomLog " + c.Facts["path"] + " slowpoke"},
			{Kind: "shell", Where: whereMachine, Code: "sudo apachectl configtest && sudo systemctl reload apache2"},
		}}
	case key == "duration" && reason == "no_duration":
		return webLogGuide(c, "")
	}
	return Guide{}
}

// webLogGuide is the recipe for a timed nginx log: the format, the line in the site, the reload.
// `path` is the file the agent already waits for, empty when it has to be proposed.
func webLogGuide(c *Check, path string) Guide {
	timed := path
	if timed == "" {
		timed = strings.TrimSuffix(c.Facts["path"], ".log") + ".slowpoke.log"
		if c.Facts["path"] == "" {
			timed = "/var/log/nginx/site.slowpoke.log"
		}
	}
	return Guide{Enable: []Step{
		{Kind: "shell", Where: whereMachine + ": prints the exact lines for each site", Code: "sudo slowpoke-agent detect"},
		{Kind: "nginx", Where: "/etc/nginx/conf.d/slowpoke.conf", Code: `log_format slowpoke '$remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent '
                    '"$http_referer" "$http_user_agent" rt=$request_time';`},
		{Kind: "nginx", Where: "The site's server block (repeat an inherited access_log, or it stops receiving requests)", Code: "access_log " + timed + " slowpoke;"},
		{Kind: "shell", Where: whereMachine, Code: "sudo nginx -t && sudo systemctl reload nginx"},
		{Kind: "yaml", Where: whereAgent + ", in the " + c.Type + " source", Code: "path: " + timed},
		restartAgent,
	}}
}

func otlpGuide(c *Check, status, reason string) Guide {
	switch {
	case status == "ok":
		return Guide{}
	case reason == "cannot_listen":
		return Guide{Enable: []Step{{Kind: "text", Where: whereAgent, Code: "Another process uses " + c.Facts["listen"] + ": set a free address in listen for this source, and the same in the application."}}}
	default:
		return Guide{Enable: []Step{
			{Kind: "shell", Where: "Laravel application", Code: "composer require slowpoke/laravel"},
			{Kind: "shell", Where: "Symfony application", Code: "composer require slowpoke/symfony"},
			{Kind: "shell", Where: "Django, FastAPI, Flask", Code: "pip install slowpoke-python"},
			{Kind: "shell", Where: "Express, Fastify", Code: "npm install @slowpokedev/node"},
			{Kind: "text", Where: "Other frameworks", Code: "The lines that send the traces to " + c.Facts["listen"] + " are in docs/instrument/README.md"},
		}}
	}
}

// hostGuide covers the machine itself: reading which process holds each socket is optional and
// costs a capability, so the team decides.
func hostGuide(key, status string) Guide {
	if key != "processes" {
		return Guide{}
	}
	if status == "ok" {
		return Guide{Disable: []Step{
			{Kind: "ini", Where: whereSystemd, Code: "AmbientCapabilities=CAP_DAC_READ_SEARCH\nCapabilityBoundingSet=CAP_DAC_READ_SEARCH"},
			{Kind: "shell", Where: whereMachine, Code: "sudo systemctl daemon-reload && sudo systemctl restart slowpoke-agent"},
		}}
	}
	return Guide{Enable: []Step{
		{Kind: "text", Where: "What it gives, and what it costs", Code: "Every connection is attributed to the service that opened it (a PHP-FPM pool, a queue worker), instead of the machine as a whole. CAP_SYS_PTRACE is what ss -p and netstat -p need: it also lets the agent read the memory of other processes, so grant it only if that is acceptable. Slowpoke only reads /proc/<pid>/fd, comm and cmdline."},
		{Kind: "ini", Where: whereSystemd, Code: "AmbientCapabilities=CAP_DAC_READ_SEARCH CAP_SYS_PTRACE\nCapabilityBoundingSet=CAP_DAC_READ_SEARCH CAP_SYS_PTRACE"},
		{Kind: "shell", Where: whereMachine, Code: "sudo systemctl daemon-reload && sudo systemctl restart slowpoke-agent"},
		{Kind: "text", Where: "Docker", Code: "The agent container already runs as root: add --cap-add SYS_PTRACE and --pid host."},
	}}
}

func redisGuide(c *Check, key, status, reason string) Guide {
	const whereRedis = "On the machine, with redis-cli"
	switch {
	case key == "slow_log" && status == "ok":
		return Guide{Disable: []Step{{Kind: "shell", Where: whereRedis, Code: "redis-cli CONFIG SET slowlog-log-slower-than -1\nredis-cli CONFIG REWRITE"}}}
	case key == "slow_log" && reason == "disabled":
		return Guide{Enable: []Step{{Kind: "yaml", Where: whereAgent + ", in the redis source", Code: "slowlog: true"}, restartAgent}}
	case key == "slow_log":
		return Guide{Enable: []Step{{Kind: "shell", Where: whereRedis + " (10000 microseconds = 10 ms)", Code: "redis-cli CONFIG SET slowlog-log-slower-than 10000\nredis-cli CONFIG REWRITE"}}}
	case key == "connection" && status != "ok":
		return Guide{Enable: []Step{
			{Kind: "yaml", Where: whereAgent + ", in the redis source", Code: "addr: " + valueOr(c.Facts["address"], "127.0.0.1:6379") + "\npassword: ${SLOWPOKE_REDIS_PASSWORD}   # only if requirepass is set"},
			restartAgent,
		}}
	case key == "metrics" && status == "ok":
		return Guide{Disable: []Step{{Kind: "text", Where: whereAgent, Code: "Remove the redis source."}, restartAgent}}
	}
	return Guide{}
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func removeGuide(c *Check) []Step {
	switch c.Type {
	case "mysql":
		return []Step{
			{Kind: "sql", Where: whereMySQL, Code: "DROP USER IF EXISTS " + c.mysqlGrantee() + ";\nSET GLOBAL slow_query_log = OFF;"},
			{Kind: "text", Where: whereMyCnf, Code: "Delete the file if Slowpoke created it."},
			{Kind: "text", Where: whereAgent, Code: "Remove the mysql and mysql-slowlog sources."}, restartAgent,
		}
	case "postgres":
		return []Step{
			{Kind: "sql", Where: wherePostgres, Code: "DROP ROLE IF EXISTS " + c.pgRole() + ";"},
			{Kind: "text", Where: whereAgent, Code: "Remove the postgres source."}, restartAgent,
		}
	case "nginx":
		return []Step{
			{Kind: "text", Where: "nginx", Code: "Remove the access_log … slowpoke; line (and /etc/nginx/conf.d/slowpoke.conf if no site uses it), then: sudo nginx -t && sudo systemctl reload nginx"},
			{Kind: "text", Where: whereAgent, Code: "Remove the nginx source."}, restartAgent,
		}
	case "redis":
		return []Step{
			{Kind: "text", Where: whereAgent, Code: "Remove the redis source."},
			{Kind: "shell", Where: "On the machine, only if you turned it on for Slowpoke", Code: "redis-cli CONFIG SET slowlog-log-slower-than -1 && redis-cli CONFIG REWRITE"}, restartAgent,
		}
	case "php-fpm":
		return []Step{
			{Kind: "text", Where: "PHP-FPM pool", Code: "Remove request_slowlog_timeout and slowlog, then reload PHP-FPM."},
			{Kind: "text", Where: whereAgent, Code: "Remove the php-fpm source."}, restartAgent,
		}
	}
	return []Step{{Kind: "text", Where: whereAgent, Code: "Remove the " + c.Type + " source."}, restartAgent}
}
