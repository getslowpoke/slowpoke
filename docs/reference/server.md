# Server configuration reference

The server is configured with environment variables. Everything about a project (the speed index, missions, bosses,
notifications, integrations) is a project setting, stored in the database and edited under **Settings** in the UI.

## Environment variables

| Variable | Default | Meaning |
|---|---|---|
| `SLOWPOKE_DATABASE_URL` | `sqlite://./slowpoke.db` (image: `sqlite:///data/slowpoke.db`) | `postgres://user:pass@host:5432/db?sslmode=…` or `sqlite:///absolute/path.db`. |
| `SLOWPOKE_LISTEN` | `:8080` | HTTP listen address. |
| `SLOWPOKE_PUBLIC_URL` | empty | Public address, e.g. `https://slowpoke.example.com`. With `https://`, session cookies are `Secure`. Used for links in notifications, issues and MCP answers. |
| `SLOWPOKE_TRUSTED_PROXIES` | empty | Reverse proxies allowed to report the visitor's address in `X-Forwarded-For`, as addresses or networks: `127.0.0.1, ::1`. Needed behind a proxy, or the login rate limit counts every visitor as the proxy. |
| `SLOWPOKE_ROLE` | `all` | `all`, `web` (HTTP only) or `worker` (background work only). See [Scaling out](../install/server.md#scaling-out). |
| `SLOWPOKE_WORKERS` | `4` | Ingest workers per process. |
| `SLOWPOKE_MAX_QUEUE` | `20000` | Queued agent batches above which ingest answers `429` and agents retry. |
| `SLOWPOKE_LOG_LEVEL` | `info` | `debug` for more detail. Logs are JSON on standard output. |
| `SLOWPOKE_DEMO_EMAIL`, `SLOWPOKE_DEMO_PASSWORD` | empty | A public demo only: the login page shows these credentials and `GET /demo` logs straight in (into the `SLOWPOKE_DEMO_LIVE` project). Never set them on a server with real data. |
| `SLOWPOKE_DEMO_LIVE` | empty | Slug of a demo project to keep feeding with live data (not on `web` instances). |
| `SLOWPOKE_PASSWORD` | empty | Password for `create-user` and `set-password` when `-password` is not given. |

At start the server waits up to a minute for the database, then applies migrations.

With PostgreSQL the server opens up to 20 connections per process. With SQLite it uses WAL mode and a single connection.

## Commands

```
slowpoke-server [command] [flags]
```

| Command | Flags | What it does |
|---|---|---|
| `serve` (default) | | Runs the server in the configured role. |
| `migrate` | | Applies migrations and exits. |
| `create-user` | `-email`, `-name`, `-password` | Creates a user. |
| `set-password` | `-email`, `-password` | Changes somebody's password and closes every session they have open. With no `-password` and no `SLOWPOKE_PASSWORD` it asks twice at the terminal, without echoing: a password on the command line stays in the shell history and is visible in `ps`. |
| `create-project` | `-name`, `-timezone` (default `UTC`) | Creates a project and prints its token. |
| `list-projects` | | Prints slug, time zone and name of every project. |
| `create-token` | `-project` (slug), `-name` | Creates an agent token for one machine and prints it. Revocable under **Settings → Access**. |
| `seed-demo` | `-days` (30), `-name` ("Demo Shop") | Creates a project with realistic history. |
| `healthcheck` | | Exits 0 when `GET /healthz` answers 200 (used by the Docker image). |
| `version` | | Prints the version. |

## Project settings

Project settings have sensible defaults, so a new project needs no configuration. They are edited under **Settings**
and validated by the server: an invalid value is refused with the reason.

### Speed index

| Setting | Default | Meaning |
|---|---|---|
| `score.components` | 6 components, see [Costs and the speed index](../concepts.md#the-speed-index) | What the index is made of: metric, weight (0–1000), a "good" and a "bad" threshold, optionally restricted to one HTTP source. Custom metrics from StatsD or Prometheus can be components (`custom_p95`, `custom_avg`, `custom_max`, `custom_per_hour`). |
| `score.levels` | Platinum 95, Gold 85, Silver 70, Bronze 50, Climbing 0 | Level names and minimum index. One level must start at 0. |
| `score.min_hours_for_full_day` | `20` | Hours of data a day needs to count for badges and levels. |

### Query doctor

| Setting | Default | Meaning |
|---|---|---|
| `db.explain_min_seconds_per_day` | `30` | A query costing at least this many seconds per observed day gets an `EXPLAIN` plan. |

### Missions and bosses

| Setting | Default | Meaning |
|---|---|---|
| `missions.window_hours` | `24` | Length of a measurement window (1–168 hours). |
| `missions.max_open` | `5` | Missions open at the same time. |
| `missions.min_seconds` | `60` | Minimum cost per window for an item to become a mission. |
| `missions.target_ratio` | `0.5` | A window is good when the cost is at most this share of the starting cost. |
| `missions.good_windows` | `3` | Good windows in a row to complete a mission. |
| `missions.expire_days` | `30` | Open missions expire after this many days. |
| `bosses.growth` | `2` | A cost this many times its recent average spawns a boss. |
| `bosses.min_seconds` | `120` | Minimum cost per window for a boss. |
| `bosses.deadline_hours` | `72` | Time to defeat a boss before it escapes. |
| `deploys.compare_minutes` | `60` | Minutes before and after a deploy compared for its verdict (at least 5). |
| `people.leaderboard` | `false` | Show points per person. |

### Debt budget

| Setting | Default | Meaning |
|---|---|---|
| `debt.weekly_goal_seconds` | `600` | Seconds of waiting per day the team aims to repay each week. |
| `debt.capacity_percent` | `20` | Share of each sprint reserved for debt, shown in the debt report. |

### Notifications and integrations

`notifications` (channels, rules, people, templates, quiet hours, weekly digest) is described in
[Notifications](../notifications.md); `integrations.tasks` in [Task integrations](../tasks.md). Tokens and bot tokens
are write-only: the API returns them masked.
