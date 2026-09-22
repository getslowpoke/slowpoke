# Slowpoke

Self-hosted, open source tool that turns slow requests and slow queries into **technical debt with a price**
(seconds of waiting per day) and pays it back as a game: missions, bosses from regressions, badges, a debt
report for management. Target: CTOs and senior devs of fast-growing startups, often just hired. Promise:
free, runs on your machines, value in under 15 minutes.

Not an APM clone: no session replay, log management, status pages or on-call. No AI runs inside Slowpoke;
the user's own coding assistant reads missions through MCP.

## Map

```
cmd/slowpoke-server     serve | migrate | seed-demo | create-user | create-project | healthcheck
cmd/slowpoke-agent      runs on each machine, -config /etc/slowpoke/agent.yaml
cmd/slowpoke-loadtest   ingest stress test          cmd/slowpoke-skillgen  generates integrations/*
internal/agent          log tailers (nginx/apache/caddy, php-fpm slowlog, MySQL slow log), MySQL/Postgres
                        polling + EXPLAIN, host /proc, StatsD, Prometheus, OTLP receiver, aggregator, sender
internal/protocol       agent -> server payload (the contract between the two binaries)
internal/fingerprint    SQL normalization + redaction, shared with the future PHP package via spec/*.json
internal/sqlsafe        what may be EXPLAINed
internal/server/        store (SQLite + Postgres, one dialect, migrations.go), ingest (durable queue),
                        metrics (costs, snapshots), score, doctor (query diagnoses), game (missions, bosses,
                        badges), debt (effort, ROI, budget, DORA), api, auth, mcp, tasks (GitHub/Linear/Jira...),
                        notify, tokens, topology, retention, demo
internal/server/web     embeds web/ build output (dist/ is generated: never read or edit it)
web/                    Vue 3 + Vite + Tailwind 4 UI, views in web/src/views, i18n it/en in web/src/lib/i18n.js
e2e/                    headless smoke test of every page (desktop + mobile)
lab/                    four real apps + agent + server in Docker: the end-to-end truth (see lab/README.md)
spec/                   fixtures shared across languages     skills/ integrations/ docs/  assistant + user docs
```

## Commands (everything runs in Docker: no Go/Node/PHP toolchain on the Mac)

```sh
./scripts/go test ./internal/agent/ -run TestName   # one package / one test: use this while iterating
./scripts/go vet ./...
./scripts/test                                      # full suite: SQLite, then a throwaway Postgres of this checkout
./scripts/go build -o bin/slowpoke-server ./cmd/slowpoke-server   # same for slowpoke-agent
docker run --rm -v $(pwd):/src -w /src/web node:22-alpine npm run build   # UI -> embedded in the server
./scripts/go run ./cmd/slowpoke-skillgen            # after editing skills/ (an alignment test checks it)
./scripts/check-stores                              Redis, RabbitMQ, Elasticsearch, MongoDB: real
                                                    servers with sample data, the agent against them
./lab/up   /   ./lab/down                           # lab UI http://localhost:8089 lab@slowpoke.local / slowpoke-lab-2026
```

Demo with 30 days of realistic data: `docker start slowpoke-ui`, http://localhost:8088,
`demo@slowpoke.local` / `slowpoke-demo-2026` (recreate: see the project memory).
E2E: build `e2e/Dockerfile` as `slowpoke-e2e`, run `node smoke.mjs` against the demo (`BASE`, `SLUG` env).

## The machine is small

Docker Desktop has **2 CPUs and 4 GB**. The Mac heats up.
- One heavy thing at a time: never run `./scripts/test`, image builds or the e2e while the lab has traffic.
  `docker compose --profile traffic stop` in `lab/` first.
- Prefer `-run` on a single package while iterating; the full suite once before committing.
- Stop what you started: `./lab/down`, `docker stop "$(./scripts/test --pg-name)"`, `docker stop slowpoke-ui`.

## Parallel work (Orca worktrees)

- One worktree per task, created by Orca; `orca.yaml` checks Docker and shares `web/node_modules`.
- `./scripts/test` starts a Postgres per checkout (name and port from the path), so two worktrees can test
  at the same time. With 2 Docker CPUs, still avoid two full suites at once: iterate with `-run`.
- The lab (`slowpoke-lab`, ports 8088-8090) and the demo container are **shared by all worktrees**: one lab at
  a time; coordinate before `./lab/up` or `./lab/down`.
- Split tasks so workers touch different packages; merge back to `main` one worktree at a time and run the full
  suite on `main` after each merge.

## How to work here

1. **Tests first.** Write the failing test, watch it fail, then the code. Bugs found in the lab get a
   regression test named after the real case (see `internal/agent/lab_findings_test.go`).
2. **Green on both databases** before a commit: SQL must work on SQLite and Postgres (`?` placeholders,
   the store rewrites them; `ON CONFLICT ... DO UPDATE`; no engine-specific functions without a branch).
3. **Prove it on real data** when touching the agent or ingestion: `./lab/up`, then read the API
   (`/api/projects/lab/slow`, `/overview`, `/queries/{hash}`) and check numbers make sense. Unit tests alone
   missed nine bugs that the lab found in one morning.
4. Commit with a descriptive English message (what changed and why, grouped by area), ending with the
   `Co-Authored-By` line. No remote yet: do not push.

## Invariants (breaking one is a bug, whatever the tests say)

- **Never slow the monitored app or server.** Agent: GOMAXPROCS=1, 64 MB soft limit, bounded maps
  everywhere (cardinality caps, ring buffers), backpressure instead of growth. Apps never wait on Slowpoke.
- **The agent never runs SQL chosen by the server.** EXPLAIN only on statements seen on that machine
  (`SampleStore`). Real values stay local: samples are redacted before sending unless `share_samples`.
- **Plain http only to private hosts**; tokens are hashed and revocable; secrets masked in API responses.
- **Every count counted once.** One request seen by nginx and by OpenTelemetry, one statement reported by an
  ORM and its driver, one query from the app and from performance_schema: dedupe, never sum.
  Query source precedence lives in `metrics.querySourceRank`.
- **Per-day figures use the observed period** (`metrics.ObservedDays`), never a fixed week: a project
  installed 10 minutes ago must not show "2 calls per day".
- **Fingerprints are a cross-language contract.** Change `internal/fingerprint` only together with a new case
  in `spec/fingerprint_fixtures.json`. Hashes change stored data: say so in the commit.
- **`internal/protocol` is a wire contract** between agents and servers of different versions: add fields,
  never rename or change their meaning.
- **Ignore housekeeping** (`SET NAMES`, `BEGIN`, the agent's own `pg_stat_*` queries): `agent/housekeeping.go`.
- **Code origins point at application code**, never `vendor/`, `node_modules/`, `site-packages/`.

## Facts learned the hard way

- OpenTelemetry auto-instrumentation gives **no usable query origin** in any language (Laravel points into
  `vendor/`, Python/Node/Go send nothing). Origins need our own framework packages.
- SQLAlchemy + psycopg2 nest two spans per statement; `otelhttp` (0.71) sends no `http.route`;
  Laravel's instrumentation sends routes without a leading slash (`orders/{id}`).
- php-fpm slowlog in Docker needs `cap_add: [SYS_PTRACE]`. Laravel 13 ships Guzzle 8: no `guzzle7-adapter`.
- performance_schema writes `COUNT ( * )`; psycopg2 writes `%(id)s`; pgx writes `$1`.
- UI: no `<transition mode="out-in">` on routes; popups always `<Teleport to="body">` (page transforms break
  `position: fixed`); never name a custom class like an existing Tailwind utility (`table-row`); chunk names
  change on every build, the app reloads on a failed dynamic import.
- macOS `sed` is BSD: no `--`, `-i ''` needs the empty string.

## Keep the context small

- Never read or grep `internal/server/web/dist/`, `web/node_modules/`, `bin/`, or lab container logs
  without `tail`/`grep`: one minified bundle is tens of thousands of tokens.
- Search with a path (`grep -rn X internal/agent`), read files by range, pipe test output through
  `tail`/`grep -E '^(FAIL|---|panic)'`.
- The project memory holds status, roadmap and decisions; this file holds rules. Update the memory, not
  this file, when the plan changes.

## Style

- Go: standard library first, small packages, comments explain *why*. English in code, comments, commits, docs.
- UI copy: plain words for a busy CTO, Italian and English in `i18n.js`; warm modern dark theme, no robotic
  look. Numbers always with units ("12 s lost per day").
- The name "Slowpoke" is a Pokémon trademark: to be checked before any public release.
