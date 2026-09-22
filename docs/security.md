# Security and data

Slowpoke reads production systems, so it is built on a few strict rules. This page says what leaves your machines,
what the agent is allowed to do, and how the server protects what it stores.

## What leaves a machine

The agent sends **per-minute aggregates**, not events:

- per endpoint: request counts, durations, histogram, errors;
- per query: the normalized statement (fingerprint), counts, total and maximum time, rows, and one **redacted** sample;
- PHP stalls: the stack's file and line, no variables;
- machine metrics and connection counts per remote address and port;
- your custom metrics;
- `EXPLAIN` plans and the structure of the tables involved (columns, types, indexes, estimated rows), never their
  content.

### Redaction

Before anything is sent, the agent removes values from SQL:

```
select * from users where email = 'ada@example.com' and id = 42 limit 20
select * from users where email = 's' and id = 0 limit 20
```

Strings become `'s'` (keeping `%` wildcards so `LIKE '%…'` is still visible), numbers become `0`, and `LIMIT`,
`OFFSET` and `$1`-style placeholders are kept. The real statement stays in the agent's memory on that machine, where
it is used for `EXPLAIN`.

`share_samples: true` in `agent.yaml` sends real samples instead. Leave it off unless the server is as trusted as the
database.

OpenTelemetry traces received by the agent never leave the machine as traces: only aggregates with redacted
statements do. The framework packages send statements without bindings, to the local agent only.

## What the agent may do

- **Read-only.** Files, `/proc`, database statistics.
- **`EXPLAIN` only on statements seen on that machine.** The server can ask for a plan by hash; it can never send SQL
  to run, unless `share_samples` is on and the machine has a single database.
- **Only single read-only `SELECT` statements** are explained, in a read-only transaction with a 10 s timeout:
  anything with writes, `FOR UPDATE`, `INTO OUTFILE`, `SLEEP` or several statements is refused.
- **Least privilege** in the database: `PROCESS, SELECT` on MySQL; `pg_read_all_stats` (and `pg_read_all_data` for
  plans) on PostgreSQL. See [Install the agent](install/agent.md).
- **Bounded resources:** one CPU core, 64 MB soft memory limit, bounded buffers, backpressure instead of growth.

## In transit

- The agent sends to `https://` servers. Plain `http://` is refused unless the server is on localhost, a private IP,
  `*.internal`, `*.local` or a single-label host, or `allow_insecure: true` is set.
- Redirects are never followed, so a token cannot be sent somewhere else.
- The framework packages only send to local or private addresses over plain HTTP, never across the internet.

## On the server

- **Tokens** are stored as SHA-256 hashes, shown once, revocable, with last use recorded. See [Tokens](api.md#tokens).
- **Passwords** are hashed with bcrypt. A login for an unknown email takes as long as a wrong password.
- **Sessions** last 30 days, stored hashed; the cookie is `HttpOnly`, `SameSite=Lax`, and `Secure` when
  `SLOWPOKE_PUBLIC_URL` is HTTPS.
- **Cross-site requests:** every non-GET panel request needs the `X-Slowpoke` header.
- **Headers:** `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: same-origin`.
- **Login rate limit:** 10 failed attempts per address per 15 minutes.
- **Secrets in settings** (task integration tokens, bot tokens) are write-only and returned masked.
- **The server image** runs as an unprivileged user in a distroless image without a shell.

### Access

Every user can see and manage every project on the server; there are no per-project roles yet. Create accounts only
for people you would give access to all projects, or run one server per team.

## Agents that update themselves

`auto_update` is off unless you ask for it, and it is a decision worth making on purpose: an agent
allowed to replace its own binary will run whatever its server hands out for that platform, so a
compromised server becomes code running as the unprivileged `slowpoke` user on every machine that
follows it. What the agent checks first: the binary comes from the same server the measures go to
(no internet), its checksum matches what that server publishes, and it runs and says the expected
version before it is put in place. What it can write is one directory, `/usr/local/lib/slowpoke`,
opened in the unit; the rest of the machine stays read-only for it, with no new privileges. Inside
a container it never replaces itself.

## Retention

Data is removed automatically every hour:

| Data | Kept |
|---|---|
| Per-minute requests and queries | 14 days |
| Machines (CPU, memory, disk, network) and custom metrics, per minute | 26 hours, then one row per hour |
| Machines and custom metrics, per hour | 31 days |
| Endpoints, queries and PHP stalls, per hour | 22 days, then one row per day |
| Endpoints, queries and PHP stalls, per day | 120 days |
| Routes, outbound calls and jobs, per hour | 31 days |
| Scheduled command runs | 30 days |
| Processed agent batches | 48 hours |
| Expired sessions | removed |

Missions, bosses, deploys, badges and points are kept.

Past the first day nothing reads machines and custom metrics minute by minute (longer charts step by the hour), so an
hour of minutes becomes one row with the same sums, averages, maxima and histograms, and the highest CPU minute of the
hour. In the same way, past three weeks (a deploy is compared with the same hour up to 14 days before it) a day of
endpoint, query and stall hours becomes one row with the same totals. With SQLite the freed space goes back to the disk: the first run after an upgrade compacts the file once, which
takes a few seconds and needs as much free disk as the file itself.

## Reporting a vulnerability

Please report security issues privately to the maintainers instead of opening a public issue.
