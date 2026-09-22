# Agent configuration reference

The agent reads one YAML file, `/etc/slowpoke/agent.yaml` by default:

```sh
slowpoke-agent -config /etc/slowpoke/agent.yaml
slowpoke-agent -version
slowpoke-agent detect [-server URL] [-token spa_…] [-write FILE] [-force] [-root DIR]
```

`detect` inspects the machine and generates this file: see [Install the agent](../install/agent.md#let-the-agent-look-at-the-machine).

`${VARIABLE}` anywhere in the file is replaced with the environment variable, so secrets can stay out of the file:

```yaml
token: ${SLOWPOKE_TOKEN}
```

The file holds the token and database passwords: the agent warns at start if every user can read it (`chmod 640`).

## Top level

```yaml
server: https://slowpoke.example.com
token: ${SLOWPOKE_TOKEN}
host: web-1
interval: 30s
state_dir: /var/lib/slowpoke
share_samples: false
allow_insecure: false
max_procs: 1
memory_limit_mb: 64
sources: []
```

| Key | Default | Meaning |
|---|---|---|
| `server` | required | Server address. `https://`, or plain `http://` only to localhost, loopback, private IPs, `*.internal`, `*.local` or single-label hosts. |
| `token` | required | Agent token (`spa_…`) created by the wizard, or the project token (`spk_…`). |
| `host` | machine hostname | Name of this machine in the UI. |
| `interval` | `30s` | How often sources are read and data is sent. Data is aggregated per minute either way. |
| `state_dir` | `/var/lib/slowpoke` | Where log read positions are kept. Must be writable. |
| `share_samples` | `false` | Send real query samples, values included. Off: the server gets redacted samples and `EXPLAIN` runs here with the real one. See [Security](../security.md). |
| `allow_insecure` | `false` | Allow plain `http://` to a public host. The token then travels in clear. |
| `max_procs` | `1` | CPU cores the agent may use. |
| `memory_limit_mb` | `64` | Soft memory limit. |
| `sources` | none | What to read, see below. |

## Environment variables

| Variable | Meaning |
|---|---|
| `SLOWPOKE_HOST_ROOT` | Where the machine's `/proc` is mounted, e.g. `/host` in a container. Default `/`. |
| `SLOWPOKE_ADDRESSES` | Extra IP addresses of this machine, comma-separated, used to connect machines on the infrastructure map (useful behind NAT). |

## Sources

Every source has a `type` and an optional `name` (default: the type). Use names to tell apart two sources of the same
type, e.g. two nginx sites.

### `host`

CPU (including iowait and steal), load, memory, swap, OOM kills, disk and network throughput, top processes and
network connections, from `/proc`. Linux only. No options.

```yaml
- type: host
```

### `nginx`, `apache`, `caddy`

Access logs: requests per endpoint, slow requests, 4xx and 5xx, latency histogram.

```yaml
- type: nginx
  name: shop
  path: /var/log/nginx/shop.access.log
  slow_seconds: 1
  ignore_paths: ["/events/*", "/healthz"]
  from_start: false
```

| Key | Default | Meaning |
|---|---|---|
| `path` | required | Log file. Rotation (rename and truncate) is followed. |
| `slow_seconds` | `1` | A request at least this long counts as slow for its endpoint. |
| `ignore_paths` | none | Paths that are long by design: exact (`/healthz`) or prefix (`/events/*`). |
| `from_start` | `false` | Read the existing file from the beginning instead of only new lines. |

The format is detected line by line:

- combined log format with `rt=` or `request_time=`, or ending with the duration in seconds (nginx);
- combined log format ending with `%D` in microseconds (`type: apache`);
- JSON lines, including Caddy's structured access log.

URLs are normalized into endpoints (`/orders/123` → `/orders/{id}`), so costs add up per route. Status `101`
(WebSocket upgrades) is not counted as a request.

### `php-fpm`

The PHP-FPM slow log: each entry is a stall, attributed to the first frame of application code.

```yaml
- type: php-fpm
  name: www
  path: /var/log/php-fpm/www-slow.log
  timezone: Europe/Rome
```

| Key | Default | Meaning |
|---|---|---|
| `path` | required | Slow log file. |
| `timezone` | machine local time | Time zone of the slow log's timestamps, which carry no offset. |
| `from_start` | `false` | As above. |

### `mysql-slowlog`

The slow query log of MySQL 5.7, 8 and MariaDB: real statements with their duration, rows and user.

```yaml
- type: mysql-slowlog
  path: /var/log/mysql/mysql-slow.log
  ignore_users: ["backup", "replication"]
```

| Key | Default | Meaning |
|---|---|---|
| `path` | required | Slow log file. |
| `ignore_users` | none | Database users whose statements are skipped (backups, ETL). |
| `schemas` | all | Only statements of these databases. |
| `from_start` | `false` | As above. |

### `mysql` and `postgres`

Polls the database: server status, statement statistics (`performance_schema` digests or `pg_stat_statements`, top
1,000 by time) and `EXPLAIN` on request.

```yaml
- type: mysql
  name: main
  dsn: "slowpoke:${SLOWPOKE_DB_PASSWORD}@tcp(127.0.0.1:3306)/"
  slow_query_seconds: 1

- type: postgres
  name: billing
  dsn: "postgres://slowpoke:${SLOWPOKE_DB_PASSWORD}@127.0.0.1:5432/billing?sslmode=disable"
  explain: true
```

| Key | Default | Meaning |
|---|---|---|
| `dsn` | required | MySQL: Go driver format `user:password@tcp(host:port)/`. PostgreSQL: a `postgres://` URL; `EXPLAIN` runs in the database it names. |
| `status` | `true` | Read server status (queries, rows read, threads or active connections). |
| `digests` | `true` | Read statement statistics. |
| `explain` | `true` | Answer `EXPLAIN` requests for statements seen on this machine. |
| `slow_query_seconds` | `1` | A statement whose average time per call is at least this long counts as slow. |
| `schemas` | all | Only these databases, e.g. `[shop]`. For a server that also hosts databases of other applications or environments: their statements are not sent. Server status (threads, rows read) stays server-wide. |

The agent opens at most 2 connections per database and gives each poll 15 s. When a `mysql-slowlog` source is also
configured, slow totals come from the slow log only.

Permissions and database settings: [MySQL and MariaDB](../install/agent.md#mysql-and-mariadb),
[PostgreSQL](../install/agent.md#postgresql).

### `otlp`

An OpenTelemetry receiver for traces from your applications: routes, queries per route, N+1 and code origins. See
[Instrument your application](../instrument/README.md).

```yaml
- type: otlp
  listen: 127.0.0.1:4318
  code_root: /var/www/app
```

| Key | Default | Meaning |
|---|---|---|
| `listen` | `127.0.0.1:4318` | Address of the OTLP/HTTP receiver (`POST /v1/traces`, protobuf or JSON, up to 8 MB per request). |
| `code_root` | none | Deployment directory, removed from code paths so they read `app/Models/Order.php`. |

Raw traces never leave the machine: the agent turns them into per-minute statistics with redacted statements. A
request is slow from 1 s. A statement repeated at least 5 times in one trace is an N+1. Metrics and logs sent to the
receiver are accepted and ignored.

### `redis`

Reads what Redis already knows about itself. No user to create: `INFO` and the slow command log
are enough, and commands are recorded **by name only** (`GET`, `HGETALL`), never their keys or values.

```yaml
- type: redis
  name: cache
  addr: 127.0.0.1:6379
  password: ${SLOWPOKE_REDIS_PASSWORD}
  slowlog: true
```

| Key | Default | Meaning |
|---|---|---|
| `addr` | `127.0.0.1:6379` | Address of the server. |
| `password` | none | Only if `requirepass` is set. Use an environment variable. |
| `slowlog` | `true` | Read the slow command log (`SLOWLOG GET`). |

Metrics, named `redis.<name>.…`: `ops_per_sec`, `hit_rate` (of the interval), `memory_bytes` and
`memory_percent` of `maxmemory`, `keys`, `connected`, `blocked`, `fragmentation`, and as increases per
minute `commands`, `evicted`, `expired`, `rejected`. Slow commands become timers
`redis.<name>.slow.<COMMAND>`, so their cost per day joins the rest of the debt.

At startup the agent only notes where the slow log is: commands logged before it started are not
counted, or every restart would invent a spike in the past. Redis logs commands slower than
`slowlog-log-slower-than` microseconds (10 ms by default); with `-1` the log is off and the machine
page explains how to turn it on.

### `rabbitmq`

Reads what RabbitMQ already knows about itself, through the management plugin it ships with: how deep
every queue is, how fast it drains, and how many consumers are on it. A queue is where work waits, and
waiting is what Slowpoke puts a price on. Two GETs per round, nothing declared, nothing consumed:
**no message is ever read**, only names and counts.

```yaml
- type: rabbitmq
  name: broker
  url: http://127.0.0.1:15672
  user: slowpoke
  password: ${SLOWPOKE_RABBITMQ_PASSWORD}
```

| Key | Default | Meaning |
|---|---|---|
| `url` | `http://127.0.0.1:15672` | The management API. Enable it with `rabbitmq-plugins enable rabbitmq_management`. |
| `user` | none | A monitoring user is enough: `rabbitmqctl add_user slowpoke …` and `set_user_tags slowpoke monitoring`. |
| `password` | none | Use an environment variable. |

Metrics, named `rabbitmq.<name>.…`: `ready`, `unacked`, `connections`, `channels`, `queues`,
`consumers` for the broker, and per queue `queue.<queue>.ready`, `.unacked`, `.consumers`,
`.publish_rate`, `.deliver_rate` and `.wait_seconds` — how long a message added now would wait at the
rate that queue is draining. A queue nobody consumes reports no waiting time, because the answer would
be infinity: its `consumers` at zero is the thing to look at.

The **deepest twenty-five queues** are reported each round. A broker with a queue per user would
otherwise become thousands of series that no chart can hold, and the queues with work waiting in them
are the ones anybody reads.

### `elasticsearch` (and OpenSearch)

Reads what the cluster already knows about itself: how much time it spent searching, how much
indexing, what it had to reject, and how it is. Two GETs per round, nothing written, **no index
read**: queries and documents never leave the machine, only counters. `opensearch` is the same
source under its own name.

```yaml
- type: elasticsearch
  name: search
  url: http://127.0.0.1:9200
  user: slowpoke          # only where security is on
  password: ${SLOWPOKE_ELASTIC_PASSWORD}
```

| Key | Default | Meaning |
|---|---|---|
| `url` | `http://127.0.0.1:9200` | Any node of the cluster. |
| `user`, `password` | none | Only where security is on. A role that may read `/_cluster/health` and `/_nodes/stats` is enough. |

Metrics, named `elasticsearch.<name>.…`. As the increase of the last minute: `search_queries`,
`search_seconds`, `fetches`, `fetch_seconds`, `indexed`, `indexing_seconds`, `search_rejected`,
`write_rejected`. As they are: `search_avg_ms` (how long a search takes right now), `heap_percent`
(the worst node's, because that is the one that falls over), `docs`, `search_queue`, `write_queue`,
`nodes`, `active_shards`, `unassigned_shards`, and `status` as 0 green, 1 yellow, 2 red.

A node that restarted has its totals back at the beginning: that round reports no increase rather
than a negative minute. Rejections are work the cluster refused to do, which is why they are worth
a chart of their own.

### `mongodb`

Reads the mongod log, which since 4.4 is one JSON object per line, and keeps the operations MongoDB
itself called slow: how long they took, how many documents they had to read, how many they returned,
and whether they had an index to work with. Nothing to install and no user to create.

```yaml
- type: mongodb
  name: main
  path: /var/log/mongodb/mongod.log
```

What counts as slow is MongoDB's own threshold, 100 ms by default:
`db.setProfilingLevel(0, { slowms: 100 })`. In a container mongod logs to standard output instead of
a file; point the source at the file the container writes, or start it with `--logpath`.

Metrics, named `mongodb.<name>.…`. Counted per minute: `slow`, `collscan` (operations that read a
whole collection - the shape of a missing index), `docs_examined`, `keys_examined`, `returned`. Each
collection and operation also gets a timer of its own, `mongodb.<name>.slow.<db>.<collection>.<op>`,
so its cost per day joins the rest of the debt. Fifty of those at most: the counters above hold every
slow operation whatever its namespace, so nothing is lost from the totals.

What mongod and its shell keep for themselves - `admin`, `config`, `local` - is left alone: a slow
query of the shell's own telemetry is nobody's debt. Name one of those databases in `schemas` and it
is read like any other, because then it is what somebody chose to watch.

**The filter is never read.** A log line carries what somebody searched for - an email address, a
document, a name - and none of it is Slowpoke's business: what leaves the machine is the shape of the
operation, the collection, and the numbers.

### `statsd`

A StatsD listener for your own metrics.

```yaml
- type: statsd
  listen: 127.0.0.1:8125
```

| Key | Default | Meaning |
|---|---|---|
| `listen` | `:8125` | UDP address. Prefer `127.0.0.1:8125`. |

Accepted: `name:value|c` (counter), `|g` (gauge), `|ms`, `|h`, `|d` (timers), with an optional `|@rate` sample rate.
At most 500 metric names per minute.

### `prometheus`

Scrapes a Prometheus endpoint every interval: WebSocket servers (Reverb, Soketi, Centrifugo), queues, exporters.

```yaml
- type: prometheus
  name: reverb
  url: http://127.0.0.1:9090/metrics
```

| Key | Default | Meaning |
|---|---|---|
| `url` | required | Metrics endpoint. 10 s timeout, 20 MB at most. |

Counters are sent as increases per minute, gauges as values; other types are ignored. Series are named
`<name>.<series>`.

## A complete example

```yaml
server: https://slowpoke.example.com
token: ${SLOWPOKE_TOKEN}
host: shop-web-1
sources:
  - type: host
  - type: nginx
    name: shop
    path: /var/log/nginx/access.log
    ignore_paths: ["/broadcasting/*"]
  - type: php-fpm
    name: www
    path: /var/log/php-fpm/www-slow.log
    timezone: Europe/Rome
  - type: mysql-slowlog
    path: /var/log/mysql/mysql-slow.log
    ignore_users: ["backup"]
  - type: mysql
    dsn: "slowpoke:${SLOWPOKE_DB_PASSWORD}@tcp(127.0.0.1:3306)/"
  - type: otlp
    code_root: /var/www/shop
  - type: statsd
    listen: 127.0.0.1:8125
  - type: prometheus
    name: reverb
    url: http://127.0.0.1:9090/metrics
```
