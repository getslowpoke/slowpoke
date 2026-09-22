# Costs and the speed index

Slowpoke answers two questions: **where does the time go**, and **is the application getting faster or slower**. The
first is the cost of each debt item; the second is the speed index.

## Cost: seconds of waiting per day

Every slow thing gets a price in seconds of waiting per day, the time your users and your workers spend waiting on
it. Seconds per day compare across very different things, add up, and make sense to someone who never opened a query
plan.

| Debt item | Its cost |
|---|---|
| **Query** | Total time of all its executions. A query that takes 40 ms and runs 90,000 times a day costs 3,600 s per day, more than a 30 s report that runs once. |
| **Endpoint** | Total time of its slow requests (at least `slow_seconds`, 1 s by default, per source). Fast requests cost nothing. |
| **PHP stall** | Each entry in the PHP-FPM slow log counts as 1 s at the application line where the worker waited. |
| **Custom timer** | The sum of its values, from StatsD or Prometheus. |

### Every count counted once

The same work is often seen twice: a request by nginx and by OpenTelemetry, a query by `performance_schema` and by the
slow log, a statement by an ORM and by its driver. Slowpoke matches them and keeps one. For queries, statement
statistics from the database win over application traces, which win over the slow log.

### Per observed day

Per-day figures are divided by the time Slowpoke has actually observed, not by a fixed week. A project installed ten
minutes ago shows what those ten minutes cost per day (with a floor of 15 minutes), not "2 calls per day".

### Debt per day

The debt per day on the overview is the total cost of all items over the last 24 hours, compared with the 24 hours
before. The 20 most expensive items are listed with their share.

## The speed index

One number from 0 to 100 for the whole application, computed per day in the project's time zone.

It is made of **components**. Each component reads one metric and has two thresholds:

- at or better than **good** it earns all its points,
- at or worse than **bad** it earns none,
- in between, points fall on a logarithmic scale, so the first improvements from a bad value are visible.

The index is the share of points earned, over the components that have data. If a component has no data (no PHP
source, for example) it is left out and the day is marked **partial**.

Default components:

| Component | Metric | Weight | Good | Bad |
|---|---|---|---|---|
| Slow requests | share of requests over 2 s | 25 | 0.2% | 2% |
| Response time | p95 of all requests | 20 | 0.3 s | 1.5 s |
| PHP stalls | slow log entries per 1,000 requests | 15 | 0.5 | 10 |
| Database time | seconds of slow queries per hour | 20 | 30 s | 900 s |
| Worst query | slowest query | 10 | 5 s | 60 s |
| Errors | 5xx per 10,000 requests | 10 | 1 | 20 |

Weights and thresholds are project settings. You can restrict a component to one web source, or build components from
your own metrics (p95, average, maximum or hourly total of a StatsD or Prometheus series), for example the p95 of a
checkout timer.

### Levels

| Level | Minimum index |
|---|---|
| Platinum | 95 |
| Gold | 85 |
| Silver | 70 |
| Bronze | 50 |
| Climbing | 0 |

A day counts for levels and badges when it is **full**: all components had data and it has at least 20 hours of data.
A day closes ten minutes after midnight in the project's time zone; today and the three days before are recomputed as
late data arrives.

## Where to go next

- [Query doctor](query-doctor.md): why a query is expensive and what to change.
- [Missions, bosses and badges](game.md): how costs become work and how that work is verified.
- [Debt register](debt.md): effort, return per hour and the weekly budget.
