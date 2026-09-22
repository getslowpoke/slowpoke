package integrations

import "strings"

// foundGuide answers what the agent found on the machine and nobody reads: from nothing to a
// working source, in the order somebody would do it — turn the thing on, then tell the agent.
// Every step is a line to paste, because the person reading this has just installed Slowpoke.
func foundGuide(c *Check) Guide {
	f := c.Facts
	switch c.Kind {
	case "database":
		if f["engine"] == "postgres" {
			return Guide{Cost: "pg_stat_statements keeps its table in shared memory (a few MB) and is read once a minute. EXPLAIN uses a generic plan on PostgreSQL 16, so the query is never run.", Enable: []Step{
				{Kind: "text", Where: "What you get", Code: "Every statement with its time, the plans, and the indexes that are missing."},
				{Kind: "sql", Where: wherePostgres, Code: "CREATE ROLE slowpoke LOGIN PASSWORD 'a-long-random-password';\nGRANT pg_read_all_stats TO slowpoke;"},
				{Kind: "yaml", Where: whereAgent + ", in sources", Code: "- type: postgres\n  dsn: \"postgres://slowpoke:THE-PASSWORD@" + address(f, "127.0.0.1:5432") + "/postgres\""},
				restartAgent,
			}}
		}
		steps := []Step{
			{Kind: "text", Where: "What you get", Code: "The slow queries with their real values, the plans, and which index is missing."},
		}
		if path := f["slow_log"]; path != "" {
			steps = append(steps,
				Step{Kind: "text", Where: "The slow log is already on", Code: "Slowpoke only has to read " + path + "."},
				Step{Kind: "yaml", Where: whereAgent + ", in sources", Code: "- type: mysql-slowlog\n  path: " + path})
		} else {
			steps = append(steps,
				Step{Kind: "sql", Where: whereMySQL, Code: "SET GLOBAL slow_query_log_file = '/var/log/mysql/mariadb-slow.log';\nSET GLOBAL long_query_time = 1;\nSET GLOBAL slow_query_log = ON;"},
				Step{Kind: "ini", Where: whereMyCnf + " (so it survives a restart)", Code: "[mysqld]\nslow_query_log = ON\nslow_query_log_file = /var/log/mysql/mariadb-slow.log\nlong_query_time = 1"},
				Step{Kind: "yaml", Where: whereAgent + ", in sources", Code: "- type: mysql-slowlog\n  path: /var/log/mysql/mariadb-slow.log"})
		}
		steps = append(steps,
			Step{Kind: "sql", Where: whereMySQL + ": a read-only user, for plans and statistics", Code: "CREATE USER IF NOT EXISTS 'slowpoke'@'127.0.0.1' IDENTIFIED BY 'Replace-with-a-long-Random-passw0rd';\nGRANT PROCESS ON *.* TO 'slowpoke'@'127.0.0.1';\nGRANT SELECT ON performance_schema.* TO 'slowpoke'@'127.0.0.1';"},
			Step{Kind: "yaml", Where: whereAgent + ", in sources", Code: "- type: mysql\n  dsn: \"slowpoke:THE-PASSWORD@tcp(" + address(f, "127.0.0.1:3306") + ")/\""},
			restartAgent)
		return Guide{Cost: "The slow log writes one entry per query slower than a second: a few MB a day on a busy database, and logrotate takes care of it. The agent's user is read-only and asks MySQL once a minute.", Enable: steps}

	case "site":
		path := f["path"]
		if f["has_duration"] == "yes" {
			return Guide{Cost: "Nothing: the log is already written, the agent only follows it.", Enable: []Step{
				{Kind: "text", Where: "What you get", Code: "This site already logs the request duration: the agent only has to read it."},
				{Kind: "yaml", Where: whereAgent + ", in sources", Code: "- type: nginx\n  name: " + f["site"] + "\n  path: " + path},
				restartAgent,
			}}
		}
		site := []Step{
			{Kind: "text", Where: "What you get", Code: "Slow requests per endpoint, and which deploy made them slower."},
			{Kind: "nginx", Where: "/etc/nginx/conf.d/slowpoke.conf, once per machine", Code: `log_format slowpoke '$remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent '
                    '"$http_referer" "$http_user_agent" rt=$request_time';`},
		}
		lines := ""
		if f["inherited_log"] == "yes" && f["access_log"] != "" {
			lines = "access_log " + f["access_log"] + ";\n"
		}
		lines += "access_log " + path + " slowpoke;"
		where := "The server block of " + f["file"]
		if f["file"] == "" {
			where = "The site's server block"
		}
		if f["inherited_log"] == "yes" {
			where += " (the first line repeats the inherited log, or this site stops writing it)"
		}
		site = append(site,
			Step{Kind: "nginx", Where: where, Code: lines},
			Step{Kind: "shell", Where: whereMachine, Code: "sudo nginx -t && sudo systemctl reload nginx"},
			Step{Kind: "yaml", Where: whereAgent + ", in sources", Code: "- type: nginx\n  name: " + f["site"] + "\n  path: " + path},
			restartAgent)
		return Guide{Cost: "nginx writes a second line per request, around 150 bytes, with no measurable CPU. The existing log is untouched.", Enable: site}

	case "pool":
		version := f["version"]
		if version == "" {
			version = "<version>"
		}
		steps := []Step{
			{Kind: "text", Where: "What you get", Code: "Where PHP waits: the file and line of the slow part of a request, not only how long it took."},
		}
		if f["slow_log_on"] != "yes" {
			steps = append(steps,
				Step{Kind: "ini", Where: "The [" + f["pool"] + "] pool, in /etc/php/" + version + "/fpm/pool.d/" + f["pool"] + ".conf", Code: "request_slowlog_timeout = 1s\nslowlog = " + f["path"]},
				Step{Kind: "ini", Where: "[global] in /etc/php/" + version + "/fpm/php-fpm.conf, so a reload does not cut running requests", Code: "process_control_timeout = 10s"},
				Step{Kind: "shell", Where: whereMachine, Code: "sudo php-fpm" + version + " -t && sudo systemctl reload php" + version + "-fpm"})
		}
		steps = append(steps,
			Step{Kind: "yaml", Where: whereAgent + ", in sources", Code: "- type: php-fpm\n  name: " + f["pool"] + "\n  path: " + f["path"]},
			restartAgent)
		return Guide{Cost: "PHP writes a backtrace only for requests over 1 s: on a healthy site a handful a day. Raise the timeout if your requests are normally slower.", Enable: steps}

	case "cache":
		port := f["port"]
		if port == "" {
			port = "6379"
		}
		code := "- type: redis\n  name: cache\n  addr: \"127.0.0.1:" + port + "\""
		if f["password"] == "yes" {
			code += "\n  password: ${SLOWPOKE_REDIS_PASSWORD}"
		}
		return Guide{Cost: "Two Redis commands per minute (INFO and SLOWLOG GET), both O(1).", Enable: []Step{
			{Kind: "text", Where: "What you get", Code: "Hit rate, memory, evictions and the slow commands. No user needed."},
			{Kind: "yaml", Where: whereAgent + ", in sources", Code: code},
			restartAgent,
		}}

	case "app":
		if pkg := frameworkPackage(f["framework"]); pkg != "" {
			return Guide{Cost: "The package measures during the request and sends after the response, with a budget of 0.1 s: the visitor never waits for Slowpoke, and if the agent is down the request is unaffected.", Enable: []Step{
				{Kind: "text", Where: "What you get", Code: "The file and line of every query, the N+1 per route, and the deploy that changed them."},
				{Kind: "shell", Where: f["dir"], Code: pkg},
				{Kind: "text", Where: "Then deploy", Code: "The package sends to the agent on 127.0.0.1:4318 after the response, with a budget of 0.1 s: the request never waits for Slowpoke."},
			}}
		}
		return Guide{Cost: "Traces are sent after the response: the visitor never waits for Slowpoke.", Enable: []Step{
			{Kind: "text", Where: "What you get", Code: "The file and line of every query, and the N+1 per route."},
			{Kind: "text", Where: f["dir"] + " (" + f["framework"] + ")", Code: "No package for this framework yet: docs/instrument/README.md has the lines that send the traces to 127.0.0.1:4318."},
		}}
	}
	return Guide{}
}

// frameworkPackage is the one command that gives this framework the file and line of every query.
// Frameworks with no package of ours yet get the generic recipe instead.
func frameworkPackage(framework string) string {
	switch strings.ToLower(framework) {
	case "laravel":
		return "composer require slowpoke/laravel"
	case "symfony":
		return "composer require slowpoke/symfony"
	case "django", "fastapi", "flask", "starlette", "sqlalchemy":
		return "pip install slowpoke-python"
	case "express", "fastify", "nestjs", "node.js", "node":
		return "npm install @slowpokedev/node"
	}
	return ""
}

func address(f map[string]string, fallback string) string {
	if f["address"] != "" {
		return f["address"]
	}
	return fallback
}
