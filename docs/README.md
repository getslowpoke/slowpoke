# Slowpoke documentation

Slowpoke turns slow requests and slow queries into technical debt with a price, measured in seconds of waiting per
day, and helps your team pay it back. Everything runs on your machines.

## Get started

1. [Quick start](quickstart.md): server, first project, first agent, first debt items. About 15 minutes.
2. [Install the server](install/server.md): Docker, Docker Compose with PostgreSQL, single binary, reverse proxy,
   backups and upgrades.
3. [Install the agent](install/agent.md): on each machine, with Docker or systemd, plus what your web server and
   databases need to share.

## Understand the numbers

- [Costs and the speed index](concepts.md): how a slow query becomes "seconds per day", and how the 0–100 index and
  its levels are computed.
- [Query doctor](query-doctor.md): `EXPLAIN` plans, every finding and the fix it suggests.
- [Missions, bosses and badges](game.md): how work is proposed, verified and rewarded.
- [Debt register](debt.md): effort, return per hour of work, weekly budget, delivery health, hotspots.

## Connect your workflow

- [Instrument your application](instrument/README.md): framework packages and OpenTelemetry, for routes and the file
  and line of every query.
- [Deploys](deploys.md): record deploys and get a verdict on each one.
- [Notifications](notifications.md): Slack, Discord, Teams, Telegram, webhooks, weekly digest.
- [Task integrations](tasks.md): turn missions into issues.
- [Fix missions with your coding assistant](assistants.md): MCP server and skill for Claude Code, Cursor, Codex,
  Copilot and Windsurf.

## Reference

- [Agent configuration](reference/agent.md): every source and option of `agent.yaml`.
- [Checks and their cost](reference/checks.md): what Slowpoke can see, what each capability gives and what it costs the monitored machine.
- [Server configuration](reference/server.md): environment variables, roles, commands.
- [API and MCP](api.md): machine API, tokens, MCP tools.
- [Security and data](security.md): what leaves your machines and how Slowpoke protects it.
- [Architecture](architecture.md): how the pieces fit together.
- [FAQ](faq.md)
