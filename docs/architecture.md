# Architecture

```
 your machines                                                     your Slowpoke server
┌──────────────────────────────────────────────┐                ┌───────────────────────────────┐
│ nginx / Apache / Caddy logs                  │                │  HTTP: UI, panel API, MCP     │
│ php-fpm slow log                             │   HTTPS, gzip  │  /api/v1/ingest ──► queue     │
│ MySQL slow log, performance_schema  ──►  agent ─────────────► │  ingest workers ──► database  │
│ PostgreSQL pg_stat_statements                │  per minute,   │  game engine (every minute)   │
│ /proc, StatsD, Prometheus                    │  redacted      │  notifications (every 30 s)   │
│ app ──OTLP / framework package──►            │ ◄───────────── │  retention (every hour)       │
│                                              │ EXPLAIN by hash│                               │
└──────────────────────────────────────────────┘                └──────────────┬────────────────┘
                                                                                │
                                              coding assistants ◄── MCP ────────┤
                                     Slack, Teams, Telegram, … ◄── notifications┤
                                  GitHub, Linear, Jira, … ◄── tasks ────────────┘
```

## The agent

A single static Go binary per machine. Each source (log tailer, database poller, `/proc` reader, StatsD listener,
Prometheus scraper, OTLP receiver) feeds one aggregator that keeps per-minute buckets with hard caps on cardinality.
Every interval (30 s) closed minutes are compressed and sent; if the server is unreachable they wait in a bounded
buffer.

Queries are identified by a **fingerprint**: the statement with values replaced by placeholders and lists collapsed,
hashed. The same rules are shared by every language Slowpoke supports through fixtures in `spec/`, so a query seen by
the MySQL slow log, `performance_schema` and a framework package is the same debt item.

The agent keeps the slowest real statement of each fingerprint in memory. When the server asks for a plan, the agent
runs `EXPLAIN` on that local statement: SQL never travels from the server to your database.

## The server

One static Go binary with the web UI embedded.

- **Ingest** accepts a payload, stores it in a durable queue table and answers `202` immediately. Workers claim
  batches (`SKIP LOCKED` on PostgreSQL), write minute and hour tables with multi-row upserts, and retry with backoff.
  When the queue is full the server answers `429`: agents slow down instead of the server running out of memory.
- **Metrics** compute costs, deduplicating what several sources saw, and per-day figures over the observed period.
- **Score** computes the speed index per day in each project's time zone.
- **Doctor** reads plans and statements and produces findings.
- **Game** opens and closes missions, spawns and resolves bosses, judges deploys, awards badges and points into an
  append-only ledger.
- **Debt** estimates effort and return, the weekly budget, delivery health and hotspots.
- **Notify**, **tasks** and **MCP** connect all of this to the team's tools.
- **Retention** removes old data every hour, and turns the minutes of machines and custom metrics older than a day
  into one row per hour.

Background jobs take database locks, so several `worker` instances can run safely, and `web` instances scale
horizontally. Both SQLite and PostgreSQL are supported with the same SQL.

## Versions

The agent protocol only gains optional fields: an older agent works with a newer server and the other way around.
Database migrations run at server start.
