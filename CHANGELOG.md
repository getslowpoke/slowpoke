# Changelog

## 0.2.0 - 2026-10-30

**The first administrator needs a setup code.** Until somebody has set the panel up, the server
makes a one-time code, logs it at every start and prints it with `slowpoke-server setup-code`;
the installer prints it at the end with a link that fills it in. Before this, whoever reached a
fresh panel first became its administrator, and the installer publishes port 8080 on every
interface. Ten wrong codes from one address block it for 15 minutes; `create-user` on the
command line still needs none.

**A team, not a single login.** A Team page shows who is in, invites people by email and removes
them. Email goes out through SMTP or straight through a provider's API (Brevo, Resend, Postmark,
SendGrid, Mailgun, Cloudflare), chosen and tested from the panel, with a test message to prove it.

**Act from where you look.** A slow endpoint or query becomes a mission from its own page, and
the leave-out dialog ticks every related item at once.

**A panel that feels faster.** Pages open at once with a loading skeleton and the last answer
while the fresh one arrives; missions on the overview state their real target, the live tiles
say their time, and the activity feed is translated like the rest. A short welcome tour explains
what Slowpoke is on the first visit.

**Slack**: cards with the logo, a guide to connect it, and two new moments worth a message.

**Demos from real numbers.** `export-profile` reads the measures of a project (never users, tokens,
settings or samples), `rename-profile` replaces every name with a dictionary and refuses to write
while a denied word survives, and `seed-demo -profile` rebuilds it onto today hour by hour, with
missions, bosses and badges born when they would have been. The public demo can offer more than
one project (`/demo?project=`), and a replay keeps an imported project alive without agents.

## 0.1.0 - 2026-09-22

First public release. Slowpoke turns slow requests and slow queries into technical debt with a
price - seconds of waiting per day - and pays it back as a game.

**The panel**: a speed index from 0 to 100 with levels, streaks and badges, ranked by where the
next points are; the technical debt in seconds of waiting per day with a weekly repayment budget;
missions born from what costs the most and bosses born from regressions; slow endpoints, slow
queries with the Doctor's diagnosis and the line of your code behind each one; PHP stalls; an
infrastructure map drawn from the connections each machine really has; deploys with a verdict;
jobs and cron; a debt report for whoever signs the budget; a TV mode.

**The agent**: one per machine, reads nginx, Apache and Caddy access logs, the php-fpm slow log,
MySQL and PostgreSQL (slow log, performance_schema, pg_stat_statements, EXPLAIN on what it saw),
Redis, RabbitMQ, Elasticsearch, MongoDB, StatsD, Prometheus and OpenTelemetry. It looks at the
machine and writes its own configuration; whatever it cannot read is listed in the panel with the
line that turns it on and what that costs. One core, 64 MB, read-only, and it never lets the
application wait for it.

**The framework packages**, optional and with no runtime dependencies of their own, for the file
and line behind every query: `slowpoke/laravel`, `slowpoke/symfony`, `slowpoke-python` (Django,
Flask, FastAPI, Celery), `@slowpokedev/node` (Express, Fastify, BullMQ).

**For your coding assistant**: an MCP server so Claude Code, Cursor or Copilot can read the
missions, the diagnosis and the code origin, and open the pull request that closes them. No AI
runs inside Slowpoke, and nothing leaves your machines.

**Anything can be left out of the score, from where you see it.** Not only endpoints: a query of a
library nobody can change and a PHP frame inside a vendor's code can be left out too, with a reason,
and they stop counting in the speed index, the debt and the missions while their time stays readable
apart. The button is now on the list of slow things, on the query page and on every mission and
regression, instead of only on the endpoint's page. And a regression from a one-off spike can be
**waved away** on its own: it closes with no points and stays quiet for 7 to 90 days, while what it
measures keeps counting.

**A database that stops growing.** Machines and custom metrics were stored minute by minute for
14 days, and on a real installation (17 machines with Redis) they were 170 of 200 MB after four days.
Past the first day an hour of minutes now becomes one row with the same sums, averages, maxima and
histograms, and the busiest CPU minute is kept apart so the spikes still show. Those hours are kept
a month, so the 30-day metric charts are full. With SQLite the freed space goes back to the disk:
the first start after the upgrade compacts the file once. On the 30-day demo: 126 MB to 22 MB, with
every daily and hourly total unchanged. Past three weeks, a day of endpoint, query and PHP stall
hours becomes one row too (kept 120 days), and routes, outbound calls and jobs, never read past a
month, are kept 31 days instead of 120.

**Mute what nobody will fix.** A machine whose agent stays installed (staging, a server somebody
else runs) can be muted with a note: its numbers keep counting, but its problems, an old agent and
what it could still read stop asking for attention, and the coding assistant leaves it alone. One
suggestion of the Doctor on one query (an index on a vendor's table) can be muted too: it leaves the
AI prompt, the debt report and the mission brief, and stays readable apart with who muted it and why.

**Leave an endpoint out.** A long poll, an export, a webhook or a call that waits on somebody
else's API can be left out of the speed index, the debt and the missions, with a reason and for
30 days, 90 days or until the team says otherwise. Nothing is deleted: its time is still measured
and the debt report shows it apart, with the reason. Its open missions and bosses close as "left
out", with no points.

**Time spent waiting on other services.** When a framework package or OpenTelemetry traces the
outbound HTTP calls of a request, the endpoint's page says how much of its time goes on them, which
hosts, how many calls per request, how many failed and the line of code that makes them; the list
of slow things marks the endpoints that mostly wait outside, and the mission brief read by the
coding assistant points at timeouts, caches and queues instead of the database. Only the host
leaves the machine, never the path or the query string.

The agent sends each endpoint's own response time histogram (a new optional field of the wire
format), so that an excluded endpoint can be taken out of the percentiles exactly; data from older
agents is estimated from the totals.

Install: `curl -fsSL https://getslowpoke.com/install.sh | sh`
