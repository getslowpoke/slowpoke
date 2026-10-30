# Notifications

Slowpoke tells the team what matters where the team already talks: a boss after a deploy, a mission completed, a new
level, and a weekly digest of the debt. Configure it under **Settings → Notifications** and send a test message from
there.

## Connect Slack in two minutes

**Settings → Notifications → Connect Slack** walks through it:

1. **Create the app on Slack** opens Slack with the Slowpoke app already described (name, description, colour, the
   permission to post). Choose the workspace and press *Create*.
2. In the app, open **Incoming Webhooks**, press **Add New Webhook**, choose the channel and allow.
3. Copy the webhook URL, add a Slack channel in Slowpoke, paste it, save, and press **Send a test**.

For the app icon, download the logo from the same box and upload it under *Basic Information → App icon*.

Slack messages are cards: what happened and on what, the cost before and after for missions, when a boss escapes (in
each reader's own time zone), the points and who earned them, a button that opens that mission, and the Slowpoke logo.
A win is green, a boss is red, work in progress is blue. The plain text of the template goes along for the phone
notification. A template written by the team for an event is shown as it is, without the details.

## Channels

| Type | What it needs | Notes |
|---|---|---|
| `slack` | Incoming webhook `url`, or a `bot_token` (`xoxb-…`) | The bot token also enables direct messages. |
| `discord` | Webhook `url` | |
| `teams` | Webhook `url` | |
| `telegram` | `bot_token` and `chat_id` | Direct messages to mapped people too. |
| `webhook` | `url` | Receives JSON, see below. |

## Rules

A rule sends some events to one channel:

```json
{ "events": ["boss", "deploy_worse"], "channel": "dev-alerts", "direct_message": true }
```

| Event | When |
|---|---|
| `level_up` | A new level is reached |
| `badge` | A badge is earned |
| `mission_completed` | A mission is completed |
| `mission_claimed` | Someone (or their coding assistant) takes a mission |
| `pull_request` | A pull request is linked to a mission |
| `boss` | A regression appears |
| `boss_defeated`, `boss_escaped` | A boss is defeated or its deadline passes |
| `boss_deadline` | A boss escapes in two hours: once per boss, while there is still time to ship a fix |
| `leaderboard` | Somebody passes a colleague in the points of the month |
| `mission_shipped`, `mission_confirmed`, `mission_relapsed` | A fix goes online, holds for a month, or comes back |
| `deploy_better`, `deploy_worse`, `deploy_neutral` | A deploy verdict |
| `command_overlap` | The same command runs on two machines at once |
| `debt_goal` | The weekly debt goal is reached |
| `*` | Everything |

With `direct_message`, the person behind the event (the deploy author) also gets a direct message, when they are
mapped under **people**:

```json
{ "people": { "ada@example.com": { "slack_user_id": "U0123ABCD", "telegram_chat_id": "123456789" } } }
```

Direct messages need a Slack bot token or a Telegram bot.

## Templates

Each event can have its own message, written as a Go template. The default is:

```
{{.Icon}} *{{.Project}}* · {{.Title}}{{if .Points}} · +{{.Points}} pts{{end}}{{if .Saved}} · {{.Saved}} less waiting{{end}}{{if .Mention}} · {{.Mention}}{{end}}{{if .URL}}
{{.URL}}{{end}}
```

Available fields: `.Event`, `.Project`, `.Icon`, `.Title`, `.Person`, `.Mention`, `.Points`, `.Saved` (already
"per day", as in `7m 12s per day`), `.URL` (the mission itself when there is one), `.Data`.

## Quiet hours

With quiet hours on (from 22:00 to 08:00 by default, in the project's time zone, across midnight if needed),
notifications wait until the morning instead of being dropped. The weekly digest ignores quiet hours.

## Weekly digest

Once a week, on the chosen day and hour (Monday at 09:00 by default), one message with:

- the average speed index of the week, compared with the week before;
- the debt per day, compared with the day before;
- missions completed and points earned;
- open bosses;
- the three most expensive debt items.

## Webhook payload

```json
{
  "event": "boss",
  "project": "Shop",
  "title": "Regression on GET /orders/{id}",
  "icon": "👾",
  "text": "…the rendered message…",
  "person": "ada@example.com",
  "points": 0,
  "url": "https://slowpoke.example.com/projects/shop/missions",
  "data": { },
  "sent_at": 1789635600
}
```

## Delivery

Notifications are checked every 30 seconds. A failed delivery is retried a minute later. Bot tokens and webhook
URLs are write-only: the API returns bot tokens masked and webhook URLs with only their host
(`https://hooks.slack.com/••••••••`); saving the settings with the mask keeps the stored value.
