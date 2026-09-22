# API and MCP

Slowpoke has three ways in:

| | For | Authentication |
|---|---|---|
| **Machine API** `/api/v1/*` | Agents, deploy scripts, CI | `Authorization: Bearer` project or agent token |
| **MCP** `/mcp` | Coding assistants | `Authorization: Bearer` personal token |
| **Panel API** `/api/*` | The web UI | Session cookie and the `X-Slowpoke` header |

## Tokens

| Token | Prefix | Created in | Can |
|---|---|---|---|
| Project token | `spk_` | Project settings (can be rotated) | Send data, record deploys |
| Agent token | `spa_` | **Add agent** wizard, or **Settings → Access** | Send data, record deploys. One per machine; removing the machine revokes it. |
| Personal token | `spu_` | **Settings → Access** | Use MCP on behalf of a user |

Tokens are random 24-byte secrets. The server stores only their SHA-256 hash, shows the secret once, records when each
token was last used and lets you revoke it at any time. Prefer one agent token per machine over the project token: a
leaked token can then be revoked without touching the other machines.

## Machine API

### `POST /api/v1/ingest`

Used by the agent: per-minute aggregates of requests, queries, stalls, host metrics, custom metrics and `EXPLAIN`
results. Body up to 20 MB, gzip accepted. The payload format lives in `internal/protocol` and only ever gains fields,
so agents and servers of different versions work together.

| Answer | Meaning |
|---|---|
| `202` | Queued. The body may contain `explain` requests for the agent. |
| `401` | Unknown or revoked token. The agent drops the payload. |
| `429` with `Retry-After: 30` | The ingest queue is full. The agent keeps the payload and retries. |

### `POST /api/v1/deploys`

Records a deploy. See [Deploys](deploys.md).

### `GET /api/v1/status`

Queue status for the token's project: useful to check a token and the server's backlog from a script.

## MCP

`/mcp` is a Model Context Protocol server over streamable HTTP (JSON-RPC 2.0 on `POST`, no server-sent events),
protocol versions `2025-06-18`, `2025-03-26` and `2024-11-05`. Set it up in your assistant with
[Coding assistants](assistants.md).

| Tool | Arguments | Returns |
|---|---|---|
| `project_overview` | | Speed index, debt per day, open missions and bosses |
| `list_missions` | `status` (`open`, `closed`, `all`), `limit` (1–50, default 15) | Missions with cost, effort and return per hour |
| `list_hotspots` | `limit` | Source files ranked by the cost of their queries and stalls |
| `list_integrations` | | Per machine and source, what works (statistics, slow log, plans, request durations, traces) and the exact commands to enable what is missing or turn optional capabilities off |
| `get_mission` | `id` | The full brief: history, query or endpoint, findings, origin, related deploy |
| `claim_mission` | `id`, `person` (default: the token's owner), `force` | Takes the mission; if somebody else has it, says who and since when and changes nothing unless `force` |
| `mark_shipped` | `id` | Says the fix is online, when the deploy does not reach Slowpoke by itself |
| `link_pull_request` | `id`, `url`, `title` | Attaches the pull request to the mission |
| `verify_mission` | `id` | Current cost against the starting cost and the mission's target (read-only) |
| `list_slow_queries` | `window` (`1h`, `24h`, `7d`, `30d`), `limit` | Queries by cost |
| `get_query` | `hash` | Query doctor: plan, findings, origin, routes |
| `list_slow_endpoints` | `window`, `limit` | Endpoints by cost |

## Panel API

The web UI uses a JSON API under `/api/projects/{slug}/…` (overview, slow things, queries, endpoints, missions,
deploys, heatmap, jobs, metrics, debt report, hosts, topology, tokens, settings, excluded endpoints, muted machines and
muted suggestions). It requires a session and an `X-Slowpoke` header on every non-GET request, as protection against
cross-site requests. It is not versioned yet and may change between releases: automate through the machine API and MCP.

`GET /healthz` answers `{"ok": true, "version": "…"}` when the server can reach its database.
