# Debt register

The debt register turns the list of slow things into decisions: what to fix first, how much work it is, how much the
team repays each week, and whether delivery is getting healthier. It is the page to bring to a planning meeting, and
the printable **debt report** is the one to bring to management.

## Principal and interest

- **Interest** is what an item costs every day, in seconds of waiting ([Costs](concepts.md)).
- **Principal** is the work needed to fix it, as an effort size.

| Effort | Hours |
|---|---|
| `xs` | 1 |
| `s` | 4 |
| `m` | 16 |
| `l` | 40 |

Slowpoke estimates the effort from the query doctor's findings (adding an index is small, rewriting pagination is
bigger). Without findings, a PHP stall is `s` and anything else `m`; a boss is at most `s`, since reverting or fixing a
recent change is usually quick. The team can set the effort of any mission; its choice wins over the estimate.

## Return per hour of work

**Return = seconds per day ÷ effort hours.** An index that removes 1,200 s per day in one hour of work returns
1,200 s/day per hour; a 40-hour refactor that removes the same returns 30. Sorting by return gives the **quick wins**.

## Weekly budget

Debt gets paid when it has a standing allocation, not when a refactoring sprint happens to fit.

- `debt.weekly_goal_seconds` (600 by default): the seconds of waiting per day the team aims to repay each week.
- Repaid this week: the drop in daily cost (starting cost minus current cost) of missions completed and bosses
  defeated in the last 7 days.
- Reaching the goal is celebrated, earns 50 points and counts towards the **Budget kept** and **Steady payer** badges.
- `debt.capacity_percent` (20%) records the share of each sprint the team reserves for debt, shown in the report.

## Delivery health

Over the last 28 days, in the spirit of the DORA metrics:

| Measure | Definition |
|---|---|
| Deploy frequency | Deploys per week |
| Change failure rate | Share of measured deploys judged "worse" or linked to a boss |
| Time to restore | Average time from a boss appearing to its defeat (last 90 days) |
| Open regressions | Bosses not yet defeated |

| Rating | Deploys per week | Change failure rate | Time to restore |
|---|---|---|---|
| Elite | 5 or more | 15% or less | under 24 h, or no regressions |
| High | 1 or more | 30% or less | |
| Medium | 0.25 or more | 45% or less | |
| Low | otherwise | | |

Delivery health needs deploys: see [Deploys](deploys.md).

## Hotspots

Hotspots group the last 7 days of cost by **source file**: queries by the line of code that issued them, PHP stalls by
the application frame. A file that appears in several debt items is where a refactor pays most. Up to 30 files, each
with its most expensive lines.

Query origins need a framework package or traces: see [Instrument your application](instrument/README.md).

## Endpoints left out

Some endpoints are slow by design or wait on someone else: a long poll, a streaming response, a heavy export, a
webhook, a call to a payment provider. From the endpoint's page, **Leave it out of the score** takes it out of the
speed index, the debt and the missions, with a reason and for 30 days, 90 days or until someone removes it
(Settings → Speed index lists them all).

Nothing is deleted. The measures stay, the endpoint's page still shows them, and the debt report lists the excluded
time apart with its reason, so whoever reads the report sees what was left out and why. Its open missions and bosses
close as *left out*, with no points and no penalty. Removing the exclusion counts it again from the next evaluation.

## Time spent waiting on other services

When the framework package (or OpenTelemetry) traces the outbound HTTP calls of a request, the endpoint's page says
what share of its time goes on them, which hosts, how many calls per request, how many failed and the line of code
that makes each call. The list of slow things marks the endpoints that mostly wait outside. This time is part of the
endpoint's cost, never added to it: it explains it. Before leaving such an endpoint out, the usual repairs are a short
timeout, a cache of the answer, parallel calls, or moving the call to a queued job.

Only the host leaves the machine: never the path, the query string, headers or bodies.
