<h1 align="center">Slowpoke</h1>

<p align="center"><b>Your technical debt, with a price on it: seconds of waiting per day.</b></p>

<p align="center">
Self-hosted · one agent per machine · nothing about your code ever leaves them
</p>

<p align="center">
<a href="https://github.com/christiancannata/slowpoke/actions/workflows/ci.yml"><img alt="tests" src="https://github.com/christiancannata/slowpoke/actions/workflows/ci.yml/badge.svg"></a>
<a href="LICENSE"><img alt="AGPL-3.0" src="https://img.shields.io/badge/server-AGPL--3.0-blue"></a>
<a href="LICENSE-MIT"><img alt="MIT" src="https://img.shields.io/badge/agent%20and%20packages-MIT-blue"></a>
<img alt="Go" src="https://img.shields.io/github/go-mod/go-version/christiancannata/slowpoke">
<a href="#it-must-never-slow-what-it-measures"><img alt="agent: 7-17 MB" src="https://img.shields.io/badge/agent-7--17%20MB%20of%20memory-brightgreen"></a>
</p>

---

Every team knows the app is "a bit slow". Nobody can say **how much** that costs, **where** it
comes from, or **what to fix first** — so it never gets fixed, and the backlog wins.

Slowpoke answers those three questions with one number, and then makes paying it back something a
team actually wants to do.

```
TECHNICAL DEBT                                          ▼ 6% vs yesterday
2h 50m of waiting per day                        ✂ 20m 40s repaid per day

  ⛁  select count(*) from invoices where customer_id = ?   1h 5m/day   N+1
     app/Models/Order.php:88 · 31 times per request
  ⇄  GET /account/orders                                  26m 28s/day
  ∿  invoices.pdf                                         24m 58s/day

SPEED INDEX  59.7  🥉 Bronze          🔥 3 days without getting worse
🏆 WINS          10m 16s of waiting saved per day, since yesterday
```

Those are real numbers from a real installation, not a mockup: seventeen machines, four
applications, and the line of code behind every slow query.

## What you get in fifteen minutes

**The panel**, on your own server:

```sh
curl -fsSL https://getslowpoke.com/install.sh | sh
```

It brings up the server and its PostgreSQL, generates the database password itself and asks
nothing. All you need on that machine is Docker. Add `--sqlite` to try it without a database
container, `--port 9000` to put it somewhere else.

**Every machine**, with one command that asks you nothing:

```sh
curl -fsSL https://slowpoke.example.com/install.sh | sudo sh -s -- --token spa_…
```

The agent looks at the machine and works out by itself what it can read — nginx sites, PHP-FPM
pools, MySQL or PostgreSQL, Redis, the applications and their framework — writes its own
configuration, starts, and tells you what it found and what is still off, with the exact line
that turns each thing on and what it costs.

And because one agent sees its neighbours through the live connections of its own machine, the
panel knows about machines nobody has installed anything on yet. From your laptop, with your own
ssh keys:

```sh
curl -fsSL https://slowpoke.example.com/fleet.sh | sh -s -- --token spu_… --project shop
```

## What it measures

| | |
|---|---|
| **Requests** | nginx, Apache, Caddy access logs — endpoints normalized (`/orders/{id}`), p95, 5xx, time lost per day. `slowpoke-agent enable-duration` makes nginx log the duration for you, tests the configuration, reloads, checks your sites still answer, and puts everything back if they don't |
| **Queries** | MySQL/MariaDB and PostgreSQL: slow log, `performance_schema`, `pg_stat_statements`. `EXPLAIN` on the machine itself, with findings in plain words ("full table scan on `orders`") and the index to add |
| **Your code** | the framework packages give the `file:line` behind every query, and the N+1 that hides in a loop — for requests, queued work and cron. [slowpoke/laravel](https://github.com/christiancannata/slowpoke-laravel), [slowpoke/symfony](packages/symfony/README.md) [slowpoke for Python](packages/python/README.md) (Django, FastAPI, Flask, Celery) and [@slowpokedev/node](packages/node/README.md) (Express, Fastify, pg, mysql2, BullMQ) today; Go and Ruby next |
| **PHP** | where PHP-FPM stalls, with the frame and the application line |
| **Jobs and cron** | queued jobs and scheduled commands: nobody waits for them, which is why nobody notices when they get slower |
| **The machines** | CPU (with iowait and steal), memory, swap *and the pages actually written to disk*, disk, network, the processes behind each connection |
| **Anything you want** | StatsD, Prometheus endpoints, OpenTelemetry, or three lines in your code |
| **The map** | who talks to whom, drawn from the live TCP connections of every machine — including the services nobody is measuring yet |

## What makes it different from an APM

**Debt, not dashboards.** Everything is priced the same way: seconds of waiting per day. A query,
an endpoint, a stalled PHP worker — all comparable, all in one number a CTO can take to a meeting.

**It is a game.** A speed index from 0 to 100 with levels, missions with points, bosses born from
your own regressions, badges, and a weekly debt budget. Not decoration: it is what makes a team
open the panel on Monday.

**Your assistant does the work.** Missions are exposed over MCP, so Claude Code or Cursor picks up
"this query costs 1h 5m per day, here is the plan, here is the file" and fixes it.

**No AI inside.** Slowpoke computes; the thinking happens in your assistant, with your code.

**No session replay, no log management, no status pages, no on-call.** It does one thing.

## It must never slow what it measures

That is the rule the whole project is built on, and it is measured, not claimed:

| | |
|---|---|
| **The agent** | 7–17 MB of memory and 0.2 s of CPU per hour on a one-core VPS. `GOMAXPROCS=1`, a 64 MB soft limit, bounded maps everywhere: under pressure it drops detail, it never grows |
| **The Laravel package** | 0.09 ms on a request with fifty queries, about 2 µs each. The trace is sent after the response has reached the client; a missing or slow agent costs one trace, never a request |
| **Your database** | read-only user, `EXPLAIN` only on statements seen on that machine, inside a read-only transaction with a timeout |
| **Your services** | the agent reads what already exists. Nothing is changed unless you run a command that says exactly what it will write, tests it, and can undo itself |

## Where your data stays

On your machines. The agent sends aggregated measures to **your** server over outbound HTTPS —
no inbound port, no cloud, no account. Query values are redacted before anything leaves the
machine (`where email = 'a@b.c'` becomes `where email = ?`), and the panel never connects to your
databases: the agent does, from the loopback.

## How it is put together

```
  your machines                          your server                    your team
 ┌──────────────┐   aggregated,     ┌──────────────────┐            ┌──────────────┐
 │ slowpoke-    │   once a minute   │ slowpoke-server  │            │   panel      │
 │ agent        │ ────HTTPS──────►  │ SQLite/Postgres  │ ─────────► │   MCP        │
 │ (one binary) │   outbound only   │ (one binary)     │            │   webhooks   │
 └──────────────┘                   └──────────────────┘            └──────────────┘
   logs, /proc,                      durable queue, scoring,
   DB statistics,                    missions, debt, retention
   OTLP receiver
```

Two static binaries, no runtime dependencies. The server carries the panel inside it and hands
out the agent it was built with, so a machine installs from your own server with no registry in
the middle, and an agent is never older than the server it talks to.

## Documentation

[Quick start](docs/quickstart.md) · [Install the server](docs/install/server.md) ·
[Install the agent](docs/install/agent.md) · [Concepts](docs/concepts.md) ·
[The game](docs/game.md) · [Debt](docs/debt.md) · [Query doctor](docs/query-doctor.md) ·
[Instrument your app](docs/instrument/) · [Coding assistants](docs/assistants.md) ·
[Security](docs/security.md) · [API](docs/api.md) · [Architecture](docs/architecture.md)

## Status

Alpha, and honest about it: it runs in production on a real fleet, the tests run on SQLite and
PostgreSQL on every change, and the wire between agent and server is a contract with fixtures on
both sides. What is not there yet: a Helm chart and a Kubernetes DaemonSet, Docker containers as
first-class citizens, roles beyond "logged in", and packages for frameworks other than Laravel.

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Two licences, because the project has two halves.

**The agent and the framework packages are MIT.** They run on your machines and inside your
applications; nothing about them should ever need a legal conversation. That covers
`cmd/slowpoke-agent`, `internal/agent`, `internal/protocol`, `internal/fingerprint`,
`internal/sqlsafe`, `packages/` and `spec/` — see [LICENSE-MIT](LICENSE-MIT).

**The server and the panel are [AGPL-3.0-or-later](LICENSE).** Run them in your company, modify
them, keep the changes to yourself: the licence asks nothing of you. It only asks something of
whoever offers Slowpoke to other people as a hosted service — then the changes have to be
published, like these are.

The two halves share no code: the server does not import a single package of the agent, and a
test keeps it that way.

See [CONTRIBUTING.md](CONTRIBUTING.md) to send a patch — contributions are welcome.

## The name

"Slowpoke" is a Pokémon trademark. The name works while this is a project on GitHub; it has to be
checked, and probably changed, before anything is sold or publicly launched under it.
