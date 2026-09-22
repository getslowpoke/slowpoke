# Query doctor

Open any query under **Slow things** and Slowpoke shows its cost over time, where it runs from, its execution plan and
a list of findings, each with a concrete fix. The page also builds a ready-to-paste prompt for your coding assistant.

## Execution plans

Plans come from `EXPLAIN` run by the agent next to your database, never by the server:

1. When an agent with a database source calls in, the server picks up to 3 queries that need a plan: the most
   expensive ones costing at least 30 s per observed day (`db.explain_min_seconds_per_day`), with no recent plan. At
   most one batch of requests per project every 30 s.
2. The agent looks up the statement **it saw itself**, with its real values, and runs `EXPLAIN` in a read-only
   transaction with a 10 s timeout. It never runs SQL chosen by the server.
3. The plan comes back with a description of every table involved: columns, indexes and estimated rows.

A plan is refreshed after 7 days. A failed attempt (database restarting, agent restarted and not yet seeing the query)
is retried after an hour. Only a single read-only `SELECT` is ever explained: no writes, no `FOR UPDATE`, no functions
such as `SLEEP`.

On a machine with several databases, `EXPLAIN` runs on the database that reported the statement.

### PostgreSQL generic plans

`pg_stat_statements` keeps statements with `$1` placeholders instead of values. On PostgreSQL 16 and newer the agent
explains them with `EXPLAIN (GENERIC_PLAN)`, a plan built without values. Row counts in a generic plan are the
planner's estimates for an average value, and the page marks the plan as generic.

## Findings

Big tables start at 1,000 rows (warning) and 50,000 rows (critical).

| Finding | When | Suggested fix |
|---|---|---|
| **Full table scan** | The plan reads a whole table of at least 1,000 rows | The exact statement to add the index, safe for production: `ALTER TABLE … ADD INDEX … ALGORITHM=INPLACE, LOCK=NONE` on MySQL, `CREATE INDEX CONCURRENTLY` on PostgreSQL. If a matching index already exists, check that the compared values have the column's type. |
| **Sorting without an index** | At least 1,000 rows sorted in memory or on disk | Add the `ORDER BY` columns at the end of the index. |
| **Temporary table** | The plan builds a temporary table | Index the `GROUP BY` columns. |
| **Implicit conversion** (critical) | A text column compared with a number, so the index cannot be used | Pass the value as a string. |
| **Function on a column** | `DATE(created_at)`, `LOWER(email)` and similar in `WHERE` | Compare the raw column with a range, or index a generated column. |
| **Leading wildcard** | `LIKE '%term'` | Prefix search, a FULLTEXT index or a trigram index. |
| **OR across columns** | `OR` over two or more columns | Split into `UNION ALL`. |
| **`SELECT *` on a wide table** | At least 25 columns or text, JSON or blob columns | Select only the columns you use. |
| **Deep offset** | `OFFSET` of 10,000 or more | Keyset pagination (`WHERE id > last_id`). |
| **`COUNT` on a huge table** | `COUNT` on a table of 1 million rows or more | Cache the count or keep a counter. |
| **`NOT IN (SELECT …)`** | A `NOT IN` subquery | `NOT EXISTS` or `LEFT JOIN … IS NULL`. |
| **N+1** | The same query at least 5 times in one request (critical from 10 requests) | Eager loading, or one query with `WHERE id IN (…)`. Needs traces, see [Instrument your application](instrument/README.md). |

Critical findings come first.

## Where the query comes from

With a framework package or OpenTelemetry traces, the page shows the routes that run the query and the file and line
of application code that issued it. Frameworks, vendor directories and packages are skipped, so the line is one you
can change. See [Instrument your application](instrument/README.md).

## Values stay on the machine

The server only stores redacted statements: strings become `'s'` and numbers become `0`, keeping the shape of `LIKE`
patterns, `LIMIT` and `OFFSET`, and placeholders. The real statement used for `EXPLAIN` stays in the agent's memory.
See [Security and data](security.md).
