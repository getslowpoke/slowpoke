# Slowpoke lab

Four real applications with the slow patterns teams really ship, one agent playing a VPS,
one Slowpoke server. It is how the agent is tested end to end against real OpenTelemetry
instrumentations, access logs, slow logs and databases.

| App | Stack | Database | Slow patterns |
|---|---|---|---|
| laravel | Laravel 13, php-fpm, nginx, `slowpoke/laravel` package (packages/laravel) | MariaDB | Eloquent N+1, no index on status/email, `LIKE '%…%'`, report per call, slow gateway (php-fpm slowlog) |
| fastapi | FastAPI, SQLAlchemy, psycopg2, `opentelemetry-instrument` | PostgreSQL | N+1, `ILIKE`, function on column |
| express | Express 4, mysql2, auto-instrumentations-node | MariaDB | deep `OFFSET`, N+1, `OR` + `COUNT` |
| go | net/http, otelsql, pgx | PostgreSQL | `ORDER BY` without index, implicit cast, `NOT IN`, `COUNT(*)` |

The agent reads OTLP from the four apps, the nginx access log, the php-fpm slowlog, the
MariaDB slow log, performance_schema and pg_stat_statements.

```sh
./lab/up      # builds, starts, creates the user and the project (first run: ~10 minutes)
./lab/down    # stops; ./lab/down -v deletes the data too
```

Slowpoke: http://localhost:8089, `lab@slowpoke.local` / `slowpoke-lab-2026`. Laravel through
nginx: http://localhost:8090. Docker Desktop with 2 CPUs is enough: every service is capped.

## What the lab found (September 2026)

- SQLAlchemy and psycopg2 both report each statement: queries counted twice.
- The same query had different fingerprints: `%(id)s` (psycopg2), `COUNT ( * )` (performance_schema) and `lower(?)` mistaken for an `IN` list.
- Driver housekeeping (`SET NAMES`, `BEGIN`, `ROLLBACK`) and the agent's own `pg_stat_database` query listed among slow queries.
- Laravel behind nginx counted twice (access log + trace), doubling traffic and debt.
- Laravel's `code.file.path` sits on the Eloquent span and points into `vendor/`: never an origin.
- php-fpm slowlog needs `SYS_PTRACE` in Docker; deploy folder `/var/www/app` broke relative paths.
- Fresh projects spread minutes of data over 7 days: "2 calls per day" for a query run every 5 s.
- Laravel 13 ships Guzzle 8: `php-http/guzzle7-adapter` no longer installs.
