# Node.js (Express, Fastify, Knex, Sequelize, mongoose)

## Where a slow query comes from
Without `@slowpokedev/node` a slow query has no file and no line. One command, and every query carries the
application line that ran it (it wraps `pg` and `mysql2`, so TypeORM, Sequelize, Knex and Drizzle are
covered), plus the N+1 per route and the traces of BullMQ jobs and cron scripts:

```sh
npm install @slowpokedev/node
```

Then two lines in the app: see packages/node/README.md. Prisma is not covered yet.

## Count queries in a test
- Knex: `knex.on('query', () => count++)`.
- Sequelize: `new Sequelize(url, { logging: () => count++ })` or `benchmark: true`.
- pg: wrap `pool.query`.
Then call the route with `supertest` and assert the count.

## N+1
- Sequelize: `include: [{ model: Customer, attributes: ['id', 'name'] }]`; for big has-many use `separate: true`.
- Knex/SQL: collect ids, one `whereIn('id', ids)`, map in memory.
- GraphQL resolvers: DataLoader per request (the classic N+1 source).
- `await` inside `for` loops over rows: batch with `Promise.all` on bounded chunks or one query.

## Heavy queries
- `attributes`/`select` only needed columns; `raw: true` for read-only lists.
- Streams/cursors for exports (`knex(...).stream()`, `pg-query-stream`).
- mongoose: `.lean()` for reads, `.select()` fields, indexes matching the query shape (equality, sort, range), check with `.explain('executionStats')`.

## Event loop blocking
Synchronous CPU work (JSON of huge payloads, crypto, image processing, sync fs) stalls every request: move to worker threads or a queue (BullMQ). Always set HTTP client timeouts (`AbortSignal.timeout(5000)`).

## Indexes
Knex migration for PostgreSQL:
```js
exports.config = { transaction: false }
exports.up = (knex) => knex.raw('CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_orders_customer_status ON orders (customer_id, status)')
exports.down = (knex) => knex.raw('DROP INDEX CONCURRENTLY IF EXISTS idx_orders_customer_status')
```
