# What Slowpoke checks, and what each thing costs

Every few minutes each agent reports what it can see on its machine. The panel (**Machines → a
machine**) and the MCP tool `list_integrations` show the same thing: per source, one line per
capability, and for anything missing the exact lines to change. This page is the full list, so
you can decide before installing anything.

Nothing here is required. An agent with `- type: host` alone already gives CPU, memory, disk and
the map of connections. Everything else is a choice, and every choice has its price written next
to it.

## The four states

| State | Meaning |
|---|---|
| **Complete** | The source gives everything it can. |
| **Partial** | It works; an optional capability is off, because somebody chose so. |
| **Problem** | Something that should work does not: a file that cannot be read, a user whose password is wrong. |
| **Waiting** | Configured and correct, no data yet (no requests, no traces, a log not created yet). |
| **Also on this machine** | Not a fault: something the agent found and nobody reads. |

## Per source

### `host` — the machine itself

| Check | Gives | Cost |
|---|---|---|
| `readable` | CPU with iowait and steal, load, memory, swap, OOM kills, disk, network, top processes, and the connections between machines | Reading `/proc` once a minute. |
| `processes` *(optional)* | Each connection attributed to the service that opened it (`php-fpm: api`, a queue worker) instead of the machine as a whole | Needs `CAP_SYS_PTRACE` next to `CAP_DAC_READ_SEARCH`: the capability `ss -p` uses, which also allows reading other processes' memory. The agent reads `fd`, `comm` and `cmdline`, bounded to 20,000 links and 300 ms. |

### `nginx` / `apache` / `caddy` — web logs

| Check | Gives | Cost |
|---|---|---|
| `readable` | The agent follows the log from where it stopped, rotation included | Nothing: the file is written anyway. |
| `duration` | Slow requests per endpoint, the cost per day, the effect of each deploy | A second log line per request (about 150 bytes) when the existing format has no `$request_time`. The existing log is never replaced. |

WebSocket and streaming paths are ignored by design: a connection open for an hour is not a slow
request.

### `php-fpm` — where PHP waits

| Check | Gives | Cost |
|---|---|---|
| `readable` | The file and line where workers wait: `sleep`, an external call, a lock | PHP writes a backtrace only for requests over `request_slowlog_timeout` (1 s by default): on a healthy site, a handful a day. Set `process_control_timeout = 10s` first, so a reload does not cut requests that are still running. |

### `mysql` / `postgres` — the database, read-only user

| Check | Gives | Cost |
|---|---|---|
| `connection` | The agent can ask the server about itself | One connection, one question a minute. `PROCESS` and `SELECT ON performance_schema.*` are enough. |
| `statistics` *(optional)* | Every query with calls and total time, not only the slow ones | MySQL: `performance_schema` keeps its statistics in memory (a few hundred MB) and needs a restart to turn on. PostgreSQL: `pg_stat_statements` in `shared_preload_libraries`, also a restart. |
| `plans` *(optional)* | `EXPLAIN` of a slow query, the index that is missing, the columns to put in it | Needs `SELECT` on the application tables: some teams prefer not to grant it. On MySQL 8 and PostgreSQL 16 the plan is computed without running the query; the agent only ever explains statements it has seen on that machine, never SQL sent by the server. |
| `slow_log` *(optional)* | The slow queries with their real values | One line per query over `long_query_time`: a few MB a day on a busy database, rotated by logrotate. |

### `mysql-slowlog` — the slow log as a file

| Check | Gives | Cost |
|---|---|---|
| `readable` | The slow queries with their values, without any database user at all | Nothing beyond the log itself. This is the cheapest way to see slow queries. |

### `redis` — cache

| Check | Gives | Cost |
|---|---|---|
| `connection` | Reads `INFO`, no user needed | Two commands a minute, both O(1). |
| `metrics` *(optional)* | Hit rate, memory, evictions, connected clients | Included in `INFO`. |
| `slow_log` *(optional)* | The commands that blocked Redis, with their time | Redis keeps the last 128 in memory (`slowlog-log-slower-than`). |

### `otlp` — traces from the applications

| Check | Gives | Cost |
|---|---|---|
| `receiving` | The file and line of every query, queries per route, N+1 seen from the inside | The framework package measures during the request and sends **after** the response, with a budget of 0.1 s. If the agent is down the application does not notice. |

Framework packages: `composer require slowpoke/laravel` (Laravel), or OpenTelemetry for the other
stacks (`docs/instrument/`). Auto-instrumentation alone gives no usable query origin in any
language, which is why the packages exist.

## What the agent finds and nobody reads

Once an hour the agent reads the machine the same way `slowpoke-agent detect` does — a few hundred
small configuration files, nothing else — and reports what Slowpoke is **not** reading: a database
with no source, a site whose requests are invisible, a PHP-FPM pool without its slow log, a Redis,
an application without the framework package. Each one appears under **Also on this machine** with
what it would add, what it costs, and the lines that turn it into a source.

Your own coding assistant sees exactly the same list through `list_integrations` and can apply it:
the Slowpoke skill tells it to state the cost first, to reload rather than restart, to test the
configuration before reloading, and never to restart a database without an explicit yes.
