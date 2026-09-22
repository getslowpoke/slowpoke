# Missions, bosses and badges

Paying back technical debt is slow, unglamorous work that rarely gets a sprint. Slowpoke makes it visible and
rewarding: the most expensive debt becomes missions, regressions become bosses with a deadline, and progress earns
points and badges. Nothing is marked done by hand: the numbers decide.

The game engine runs every minute on the server.

## Missions

At the end of every window (24 hours by default, aligned to UTC), Slowpoke looks at what each item cost in that window
and opens missions for the most expensive ones:

- only items that cost at least `missions.min_seconds` (60 s by default) in the window;
- not items that already have an open mission, or had one closed in the last 24 hours;
- at most `missions.max_open` (5) open at the same time.

A mission remembers its **starting cost**. It is **completed** when the cost stays at or below half of it
(`missions.target_ratio`) for 3 windows in a row (`missions.good_windows`), with data in each window. A bad window
resets the count. A mission nobody finishes **expires** after 30 days.

A mission is worth `cost per day ÷ 60` points, between 10 and 500. If nobody took the mission, the points go to the
author of the latest deploy in that period.

Each mission page has what a developer needs to start: the history of the cost, the query or endpoint details, the
doctor's findings, the related deploy and a Markdown brief. From there:

- **Create task** opens an issue in GitHub, GitLab, Linear, Jira, Asana, Trello or ClickUp ([Task integrations](tasks.md));
- a coding assistant can claim the mission, link its pull request and check the result through MCP
  ([Coding assistants](assistants.md)).

### Judged on busy hours, per request

The waiting of every hour counts in the debt, but whether a fix worked is judged per request on
the project's **busy hours**: the hours of the week that carry most of its traffic, learnt from
the last four weeks (or set by hand in Settings → Missions & bosses). A quiet weekend does not win
a mission, a busy Monday does not open a boss, and a window with too little traffic in its busy
hours (a holiday) decides nothing. A project younger than a week, or with no HTTP traffic
measured, is judged on every hour in seconds, as before.

After it is completed a mission stays under watch for 30 windows: it is **confirmed** if it holds,
and marked **came back** - reopened as a boss linked to it - if its cost per request goes back
over three quarters of where it started for two windows.

## Bosses

A boss is a regression. When an item costs at least twice its average of the previous 7 windows (`bosses.growth`), and
at least 120 s (`bosses.min_seconds`), a boss appears. Items need 4 windows of history first, and an item that cost
nothing before needs 3 times the minimum.

- The boss is linked to the latest deploy of the last two windows; its author becomes the owner.
- It must be **defeated** within 72 hours (`bosses.deadline_hours`): its cost back to at most 1.5 times the old
  baseline.
- Otherwise it **escapes**, and stays in the history.

A boss is worth `cost per day ÷ 30` points, between 25 and 1,000.

## Points

| Event | Points |
|---|---|
| A new level reached | 100 |
| A badge | 25 |
| A deploy judged "better" | 30 |
| Weekly debt goal reached | 50 |
| Mission completed | cost per day ÷ 60, from 10 to 500 |
| Boss defeated | cost per day ÷ 30, from 25 to 1,000 |

Points are recorded in an append-only ledger. The overview shows total, current season (calendar month) and last 7
days. The per-person leaderboard is off by default (`people.leaderboard`): turn it on only if your team wants it.

The **hall of fame** lists the missions that saved the most seconds per day.

## Levels

A new level is reached on the first full day the speed index reaches a level never reached before (see
[Levels](concepts.md#levels)).

## Badges

Unless noted, a badge needs a **full** day (all components with data and at least 20 hours).

| Badge | How to earn it |
|---|---|
| Kick-off | The first day with data |
| One badge per level | A full day at that level |
| Half a second | p95 under 0.5 s |
| Zero 500 | Requests and not a single 5xx |
| Never over a minute | No query over 60 s |
| Never over 10 seconds | No query over 10 s |
| Light database | Less than 5 minutes of slow queries |
| Smooth PHP | Fewer than 50 PHP stalls |
| Comeback | +10 index points over the previous full day |
| Three in a row | A better day than the day before, three times in a row |
| Silver week | 7 days in a row at 70 or more |
| First repayment | The first mission completed |
| Debt hunter | 10 missions completed |
| Boss slayer | A boss defeated before its deadline |
| Green deploy | 1 deploy judged "better" |
| Debt cutter | One hour per day of waiting repaid with missions |
| Budget kept | The weekly debt goal reached once |
| Steady payer | The weekly debt goal reached 4 times |

## Overlapping commands

If the same scheduled command runs at the same time on two machines (a cron installed twice), Slowpoke reports it once
a day: it usually means double emails, double charges or lock contention.
