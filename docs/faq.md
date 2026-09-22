# FAQ

### Is Slowpoke an APM?

No. It measures performance, but its job is to turn slowness into a prioritized, verifiable debt the team pays back.
There is no session replay, log management, status page or on-call. It works well next to an APM you already have.

### Does it slow down my application?

It is designed not to. The agent reads files and statistics that already exist, uses one CPU core and 64 MB at most by
default, and never sits in the request path. The framework packages collect in memory and send after the response,
with a 0.1 s budget; if the agent does not answer, the data is dropped, never the request.

### Do I need to change my code?

No, to start: access logs, slow logs and database statistics cover endpoints, queries and PHP stalls. Add a framework
package (or OpenTelemetry) when you want queries per route, N+1 and the file and line of each query.

### Does any data leave my infrastructure?

Only to your own Slowpoke server, which you host. Query values are removed before sending, and the real statements
used for `EXPLAIN` stay on the machine. See [Security and data](security.md).

### Is there AI inside?

No. Your own coding assistant (Claude Code, Cursor, Codex, Copilot, Windsurf) reads missions through MCP, with your
model and your subscription. See [Coding assistants](assistants.md).

### Which databases are supported?

MySQL 5.7 and 8, MariaDB, and PostgreSQL (plans for statements without values need PostgreSQL 16 or newer). Both
SQLite and PostgreSQL can store the server's own data.

### Which languages and frameworks?

Any backend, through access logs and databases. For queries per route with code origins: the Laravel package today,
Python (Django, FastAPI, Flask) and Symfony packages in progress, and OpenTelemetry for everything else.

### Why "seconds per day" and not milliseconds?

Because it adds up. A 40 ms query that runs 90,000 times a day costs more waiting than a 30 s report that runs once, and
seconds per day show it. It is also a number you can explain to someone who has never seen a query plan.

### Why is a project installed today already showing "per day" costs?

Per-day figures are divided by the time actually observed (at least 15 minutes), so they are honest from the first
hour. They become more stable as days of data accumulate.

### A mission was completed but nobody worked on it

Missions are verified by numbers: if the cost stayed down for three windows, the debt is gone, whatever the reason
(a code change, an index someone added, less traffic). The history shows what changed and when.

### A regression I cannot fix keeps coming back

Two answers, both on the card of the regression and on its page. **Leave it out** takes the thing itself out of the
speed index, the debt and the missions, with a reason: for something slow by design, a query of a library, a PHP frame
inside a vendor's code. **Wave it away** closes only that regression, with no points, and keeps it quiet for 7 to 90
days: for a one-off spike (an import, a deploy already rolled back), while what it measures keeps counting.
Everything left out is listed in Settings and in the debt report, with its time, and can be counted again.

### An endpoint is slow because it waits on an external API: it ruins the score

Open its page: if a framework package traces outbound calls, it shows how much of the time is spent on the other
service and the line that calls it; a timeout, a cache or a queued job often fixes it. If it is slow by design, leave it
out of the score with a reason (from its page, from the list of slow things or from its regression): it stops counting
in the speed index, the debt and the missions, and the debt report still shows it apart. See [Debt register](debt.md#endpoints-left-out).

### A machine keeps reporting problems nobody will fix

A staging box, or a server somebody else runs, can be muted from its page (**Mute machine**, with a note). The agent
stays installed and its numbers keep counting in the speed index and the debt; its problems, an old agent and what it
could still read stop asking for attention, it goes last in the lists, and the coding assistant is told to leave it
alone. **Turn it back on** undoes it.

### The Doctor suggests something we cannot do

An index on a vendor's table, a schema the team does not own: on the query page, **Mute** that suggestion with a note.
It leaves the AI prompt, the debt report and the mission brief for that query only (the same suggestion on other
queries, or on another table of the same query, still shows), and stays listed under *Muted by the team* with who muted
it and why. The time the query costs keeps counting.

### Can several teams share a server?

Yes, with one project per application. Every user currently sees every project, so for teams that must not see each
other's data, run one server per team.

### How do I run it on Kubernetes?

Run the server image as a Deployment in front of PostgreSQL, and the agent image as a DaemonSet with `/proc` and the
log directories mounted read-only and `SLOWPOKE_HOST_ROOT` set. There is no Helm chart yet.
