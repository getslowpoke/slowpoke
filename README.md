<p align="center">
  <a href="https://getslowpoke.com"><img src="docs/assets/banner.png" alt="Slowpoke: know what to fix first. Measure the daily cost of performance debt, prioritise the work and verify the result." width="100%"></a>
</p>

<h1 align="center">🐢 Slowpoke</h1>

<p align="center">
  <b>Know which performance debt to fix first.</b><br>
  Measure the daily cost. Give your team a starting point. Show what improved.
</p>

<p align="center">
  <a href="https://demo.getslowpoke.com/demo"><b>Try the live demo →</b></a> ·
  <a href="#quick-start"><b>Install Slowpoke</b></a> ·
  <a href="docs/README.md">Documentation</a> ·
  <a href="https://getslowpoke.com">Website</a>
</p>

<p align="center">
  <a href="https://github.com/getslowpoke/slowpoke/actions/workflows/ci.yml"><img alt="CI status" src="https://github.com/getslowpoke/slowpoke/actions/workflows/ci.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="Server: AGPL-3.0" src="https://img.shields.io/badge/server-AGPL--3.0-blue"></a>
  <a href="LICENSE-MIT"><img alt="Agent and packages: MIT" src="https://img.shields.io/badge/agent%20%26%20packages-MIT-blue"></a>
  <img alt="Free and self-hosted" src="https://img.shields.io/badge/self--hosted-free-ff7a90">
</p>

---

Your app works. The backlog grows. Every sprint, performance work loses to the next feature because
nobody can explain **which fix matters most**.

Slowpoke measures the cumulative time cost of slow requests, queries and jobs on your own
infrastructure. It turns costly issues into missions and tracks whether their cost stays down.
Built for CTOs and engineering teams who need a clear priority and evidence that the work helped.

> **Small delays add up.** A 40 ms query running 90,000 times a day consumes **one hour of query
> execution every day**. Slowpoke makes that recurring cost visible.
>
> This is accumulated execution time, not a claim about lost revenue or an hour saved for every user.

**Free. Open source. Self-hosted.** The [live demo](https://demo.getslowpoke.com/demo) needs no sign-up
and uses sample data. The product screenshots above and below show a demonstration project.

## From “it feels slow” to a clear next step

| Prioritise | Investigate | Verify |
| :--- | :--- | :--- |
| Rank performance debt by its daily cost, so frequent small delays get attention too. | Follow an issue to a query plan, diagnosis and application code origin where available. | Track missions after a change and share a debt report with the people funding the work. |

The workflow is simple: **measure → choose a mission → ship a fix → check the result**.

- **Query doctor:** local `EXPLAIN` plans with findings such as full scans, N+1 queries and missing indexes.
- **Missions and regressions:** measurable targets, deploy comparisons, and missions that close when costs stay down.
- **A report for the business:** daily debt, effort estimates and progress to support the next sprint’s priorities.
- **A little motivation:** levels, badges and regression “bosses” make progress visible to the team.

Lower traffic can also reduce daily cost. Read the measurement history alongside the change before
attributing an improvement to a fix. [How costs work →](docs/concepts.md)

## Quick start

You need **Docker for the server** and access to the machines you want to monitor.

### 1. Start Slowpoke

```sh
curl -fsSL https://getslowpoke.com/install.sh | sh
```

[Read the installer](install.sh). It starts the panel and PostgreSQL and generates the database
password. Open the link it prints at the end, which carries the one-time setup code, create the first
user and add a project.
For a public deployment, follow the [HTTPS setup](docs/install/server.md).

### 2. Connect your first machine

Open **Machines → Add agent** and run the command the panel generates. It includes that machine’s
token and downloads the agent from your own Slowpoke server.

The agent discovers supported services and writes its configuration. Coverage checks show what it
cannot read yet, with instructions for enabling the missing data sources.

### 3. Find your first priority

Let the application serve real traffic, then open the ranked debt list. Inspect a costly item,
choose a mission and start with the evidence.

[Full walkthrough](docs/quickstart.md) · [Server installation](docs/install/server.md) ·
[Agent installation](docs/install/agent.md)

<details>
<summary><b>See your infrastructure, too</b></summary>

Slowpoke maps the machines and connections its agents observe, helping you understand the
infrastructure behind a slow application.

<p align="center">
  <img src="docs/assets/map.webp" alt="Sample infrastructure map showing machines, their services and the connections between them." width="100%">
</p>

</details>

## Fits the stack you already run

Start with access logs and database statistics. Add a framework package when you want application
context and query origins.

| Connect | Examples |
| :--- | :--- |
| Web servers | nginx, Apache, Caddy |
| SQL databases | MySQL, MariaDB, PostgreSQL |
| Caches, queues and search | Redis, RabbitMQ, MongoDB, Elasticsearch / OpenSearch |
| Applications and hosts | PHP-FPM, jobs, scheduled commands and machine metrics |
| Additional signals | OpenTelemetry, StatsD, Prometheus |

<details>
<summary><b>Optional framework packages</b></summary>

| Framework | Install | Documentation |
| :--- | :--- | :--- |
| Laravel | `composer require slowpoke/laravel` | [Laravel](packages/laravel/README.md) |
| Symfony | `composer require slowpoke/symfony` | [Symfony](packages/symfony/README.md) |
| Django, FastAPI, Flask, Celery | `pip install slowpoke-python` | [Python](packages/python/README.md) |
| Express, Fastify, pg, mysql2, BullMQ | `npm i @slowpokedev/node` | [Node.js](packages/node/README.md) |

See each package’s documentation for supported versions and configuration. Without a package,
you can still measure supported logs and database statistics; code origins depend on the source.

</details>

## Give your coding assistant a useful starting point

A mission can carry the query, its plan and the file and line behind it. Your coding assistant
reads that context over MCP and helps prepare a fix for you to review.

> “Investigate my most expensive Slowpoke mission and propose a fix with a test.”

Slowpoke runs no AI model internally. You choose the assistant and control its access; its own
provider and settings govern where the context it reads is sent.

[Connect your coding assistant →](docs/assistants.md)

Prefer your existing workflow? Create issues in GitHub, GitLab, Linear, Jira, Asana, Trello or
ClickUp, and send updates through Slack, Discord, Teams, Telegram or webhooks.
[Task integrations](docs/tasks.md) · [Notifications](docs/notifications.md)

## Your infrastructure. Your data.

One server stores the measurements and serves the panel. An agent on each monitored machine
collects observations and sends them to that server.

- **Self-hosted storage:** your deployment controls the data and retention.
- **Redacted query values by default:** original statements used for query plans stay on the machine that observed them.
- **Local query plans:** the agent only explains statements it has already seen; the server cannot send it arbitrary SQL.
- **Bounded collection:** buffers and a soft memory target keep overhead controlled; the agent reports its own resource use.

[Security and data handling](docs/security.md) · [Architecture](docs/architecture.md)

## Is Slowpoke for you?

**Use it when** your team needs to prioritise recurring performance debt, understand the causes and
show progress after shipping fixes.

**Keep your APM** for incident response, distributed tracing, log management or session replay.
Slowpoke focuses on the cost of performance debt and what to work on next.
[Explore the comparisons →](https://getslowpoke.com/vs/)

The software is free; you provide the infrastructure. No per-host, seat or event fees.

## Operations reference

The commands below are here when you need them. Start with the quick start above.

<details>
<summary><b>The server</b></summary>

| What you want | Command |
|---|---|
| Install (or update: re-running is safe) | `curl -fsSL https://getslowpoke.com/install.sh \| sh` |
| Use another port | `... \| sh -s -- --port 9000` |
| Try it without a database container | `... \| sh -s -- --sqlite` |
| Install somewhere other than `/opt/slowpoke` | `... \| sh -s -- --dir /srv/slowpoke` |
| Check it is up | `curl -s http://localhost:8080/healthz` |
| Create a user from the terminal | `docker compose exec server slowpoke-server create-user -email you@example.com -name "Your Name" -password '...'` |
| Reset a forgotten password | `docker compose exec server slowpoke-server set-password -email you@example.com` |
| Load 30 days of demo data | `docker compose exec server slowpoke-server seed-demo -days 30 -name "Demo Shop"` |
| Back up the database | `docker compose exec postgres pg_dump -U slowpoke -Fc slowpoke > slowpoke-$(date +%F).dump` |

Run the `docker compose` commands from the install directory (`/opt/slowpoke` by default).
</details>

<details>
<summary><b>The agent (one per machine)</b></summary>

| What you want | Command |
|---|---|
| Install | `curl -fsSL http://your-server:8080/install.sh \| sudo sh -s -- --token spa_...` |
| Give the machine a name | add `--name web-1` |
| Keep it on the same version as the server, by itself | add `--auto-update` |
| Remove it | `curl -fsSL http://your-server:8080/install.sh \| sudo sh -s -- --uninstall` |
| See what it would read on this machine | `slowpoke-agent detect` |
| Make nginx log how long each request takes | `sudo slowpoke-agent enable-duration --dry-run`, then without `--dry-run` |
| Undo that change | `sudo slowpoke-agent enable-duration --undo` |
| Print the version | `slowpoke-agent -version` |

`enable-duration` changes nginx logging configuration. It tests the configuration
with `nginx -t`, reloads, checks your sites still answer and puts everything back if they don't.
</details>

<details>
<summary><b>The whole fleet at once, from your laptop</b></summary>

One agent sees its neighbours through the live connections of its own machine, so the panel already
knows about the machines you have not installed anything on. This installs all of them, with your own
ssh keys (the panel never gets access to anything):

```sh
curl -fsSL http://your-server:8080/fleet.sh | sh -s -- --token spu_... --project shop --dry-run
```

Drop `--dry-run` when the list looks right. `--only web-2,db-1` picks the machines yourself,
`--user deploy` sets the ssh user, `--auto-update` keeps the agents on the server's version.
The `spu_` token is a personal one: **Settings → Access → Create personal token**.
</details>


## Documentation

| I want to… | Start here |
| :--- | :--- |
| Install and configure Slowpoke | [Quick start](docs/quickstart.md) · [Configuration reference](docs/reference/agent.md) |
| Understand the numbers | [Costs and speed index](docs/concepts.md) · [Debt report](docs/debt.md) |
| Investigate and fix an issue | [Query doctor](docs/query-doctor.md) · [Missions](docs/game.md) |
| Connect the team’s workflow | [Deploys](docs/deploys.md) · [Tasks](docs/tasks.md) · [Assistants](docs/assistants.md) |
| Build on Slowpoke | [API and MCP](docs/api.md) · [Architecture](docs/architecture.md) |

[Browse all documentation →](docs/README.md)

## Status and contributing

**Alpha, actively developed.** Review the [changelog](CHANGELOG.md) and installation documentation
before upgrading. Found a wrong number, a missing integration or a rough edge?
[Open an issue](https://github.com/getslowpoke/slowpoke/issues).

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow,
checks and project conventions.

## License

- **Server and panel:** [AGPL-3.0-or-later](LICENSE).
- **Agent and framework packages:** [MIT](LICENSE-MIT).

See the license files for the terms that apply to each part of the project.

---

<p align="center">
  <b>Give the next performance sprint a clear starting point.</b><br><br>
  <a href="https://demo.getslowpoke.com/demo">Try the live demo →</a> ·
  <a href="#quick-start">Install Slowpoke</a>
</p>
