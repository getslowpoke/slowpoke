# Task integrations

Missions belong in the tracker the team already uses. On a mission page, **Create task** opens an issue with the
mission's brief: the cost, the history, the query or endpoint, the doctor's findings and a link back to Slowpoke.

Add integrations under **Settings → Integrations**. Tokens are write-only: the API returns them masked.

| Provider | Fields | Token |
|---|---|---|
| **GitHub** | `repo` (`owner/name`) | Fine-grained token with Issues: read and write |
| **GitLab** | `repo` (`group/project`), `base_url` for self-managed | Access token with `api` scope |
| **Linear** | `team_id` | Personal API key |
| **Jira** | `base_url`, `email`, `project_key`, `issue_type` (default `Task`) | API token of that email |
| **Asana** | `project_id` | Personal access token |
| **Trello** | `list_id` | `key:token` |
| **ClickUp** | `list_id` | Personal API token |

Every integration also takes an `id`, an optional `name` and optional `labels`.

Issues are titled `[Slowpoke] <item>` and labelled `performance` and `tech-debt`, plus `regression` for bosses.
