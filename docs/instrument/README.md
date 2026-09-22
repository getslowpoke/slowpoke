# Application-level data for any backend language

Access logs, slow logs and the machine are read by the agent with no code change.
To also see **which queries each route runs, how many per request (N+1) and from which
file and line**, the application sends OpenTelemetry traces to the agent on the same
machine. Every major language has official auto-instrumentation.

```
app ──OTLP/HTTP──▶ slowpoke-agent (127.0.0.1:4318) ──aggregated, redacted──▶ Slowpoke server
```

Raw traces never leave the machine: the agent keeps per-minute statistics, redacts SQL values
and drops everything else.

## Framework packages or OpenTelemetry?

OpenTelemetry auto-instrumentation gives routes and queries, but in no language does it give a useful origin for a
query: it points inside the framework or ORM, or nowhere. Slowpoke's framework packages send the same kind of traces
with the **file and line of your code** that ran each query, and nothing else to install.

| Framework | Package | Status |
|---|---|---|
| Laravel 5.8–13 (PHP 7.4+) | [`slowpoke/laravel`](../../packages/laravel/README.md) | Available |
| Symfony 5.4, 6.4, 7.x (PHP 7.4+) | [`slowpoke/symfony`](../../packages/symfony/README.md) | Available |
| Django 4.2–6, FastAPI, Flask, SQLAlchemy, Celery (Python 3.9+) | [`slowpoke-python`](../../packages/python/README.md) (pip) | Available |
| Express, Fastify, pg, mysql2, BullMQ (Node 18+) | [`@slowpokedev/node`](../../packages/node/README.md) (npm) | Available |
| Anything else | OpenTelemetry, below | Available |

Use one or the other for an application, not both: the same queries would be reported twice.

Both also measure the **outbound HTTP calls** a request makes (a payment provider, a partner API, another service),
so the endpoint's page can say how much of its time is spent waiting outside. The packages trace the usual clients
(Laravel's `Http`, Symfony HttpClient, `requests` and `httpx`, `fetch` and `http`/`https`) and give the line of code
that made each call; OpenTelemetry's HTTP client instrumentations work too, without the line. Only the host reaches
the server: the agent reads the URL to find it and drops the rest. Turn it off with `SLOWPOKE_HTTP_CLIENT=false`.

## 1. Enable the receiver in the agent

```yaml
# /etc/slowpoke/agent.yaml
sources:
  - type: otlp
    listen: 127.0.0.1:4318
    code_root: /var/www/shop/current/   # stripped from file paths
```

## 2. Instrument the application

| Language | Frameworks covered | Guide |
|---|---|---|
| PHP | Laravel, Symfony, Slim, WordPress, plain PDO | [php.md](php.md) |
| Python | Django, FastAPI, Flask, SQLAlchemy, psycopg, mysqlclient | [python.md](python.md) |
| Node.js | Express, Fastify, NestJS, Koa, pg, mysql2, Prisma, mongoose | [node.md](node.md) |
| Go | net/http, Gin, Echo, database/sql, GORM, pgx | [go.md](go.md) |
| Ruby, Java, .NET | Rails, Spring, ASP.NET | official OpenTelemetry agents, same endpoint |

Common settings for every language:

```
OTEL_SERVICE_NAME=shop-api
OTEL_TRACES_EXPORTER=otlp
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_METRICS_EXPORTER=none
OTEL_LOGS_EXPORTER=none
```

Keep sampling at 100% unless the service handles thousands of requests per second: counts in
Slowpoke come from the traces the agent receives.

## What you get

- Routes as the framework names them (`GET /orders/{id}`), with queries per request.
- N+1 detection: the same query 5+ times in one request.
- Code origin of queries only when the instrumentation sets `code.file.path`/`code.line.number`
  on an application file. Tested in September 2026, no automatic instrumentation does it for
  queries (Laravel points at `vendor/`, Python, Node.js and Go send nothing): for PHP the
  php-fpm slowlog still shows the application line where a request waits.
- Services behind nginx, Apache or Caddy on the same machine are counted once: the agent pairs
  each trace with its access log line by time, status and duration.
- When the web server runs on another machine, the host links them instead: the packages send the
  one they answered for, a web source says its own with `site:`, and the server counts the web
  server's line - the whole wait - while routes, queries and code origins keep coming from the
  application.
- An endpoint view for services without nginx/apache in front (Go, Node).
