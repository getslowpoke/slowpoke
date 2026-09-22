# Fix missions with your coding assistant

Slowpoke runs no AI. Your assistant (Claude Code, Cursor, Codex, Copilot, Windsurf) uses its
own model and your subscription; Slowpoke gives it the measured context through MCP.

## 1. Personal token

Slowpoke → Settings → Access → **Create personal token**. It can read missions and
measures, claim missions and link pull requests. Revocable at any time.

## 2. Connect the MCP server

Claude Code:

```bash
claude mcp add --transport http slowpoke https://slowpoke.example.com/mcp \
  --header "Authorization: Bearer spu_..."
```

Cursor (`.cursor/mcp.json`), Windsurf and other clients:

```json
{ "mcpServers": { "slowpoke": { "url": "https://slowpoke.example.com/mcp", "headers": { "Authorization": "Bearer spu_..." } } } }
```

Codex (`~/.codex/config.toml`):

```toml
[mcp_servers.slowpoke]
url = "https://slowpoke.example.com/mcp"
http_headers = { Authorization = "Bearer spu_..." }
```

## 3. Install the skill (the method)

| Assistant | Copy |
|---|---|
| Claude Code | `skills/slowpoke` → `~/.claude/skills/slowpoke` (or `.claude/skills/slowpoke` in the repo) |
| Codex | `integrations/codex/AGENTS.md` → repository root (append if you have one) |
| Cursor | `integrations/cursor/slowpoke.mdc` → `.cursor/rules/` |
| Copilot | `integrations/copilot/copilot-instructions.md` → `.github/copilot-instructions.md` |
| Windsurf | `integrations/windsurf/slowpoke.md` → `.windsurf/rules/` |

## 4. Use it

> Fix Slowpoke mission 42

The assistant reads the brief, opens only the files it points to, writes a failing test,
fixes, opens a pull request and links it. After the deploy Slowpoke measures the result and
closes the mission, with the points to whoever did it.

Tools available: `project_overview`, `list_missions`, `list_hotspots`, `list_integrations`, `get_mission`, `claim_mission`, `mark_shipped`,
`link_pull_request`, `verify_mission`, `list_slow_queries`, `get_query`, `list_slow_endpoints`.
