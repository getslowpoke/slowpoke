# NestJS with Prisma or TypeORM

## Where a slow query comes from
Without `@slowpokedev/node` a slow query has no file and no line. One command, and every query carries the
application line that ran it (it wraps `pg` and `mysql2`, so TypeORM, Sequelize, Knex and Drizzle are
covered), plus the N+1 per route and the traces of BullMQ jobs and cron scripts:

```sh
npm install @slowpokedev/node
```

Then two lines in the app: see packages/node/README.md. Prisma is not covered yet.

## Count queries in a test
- Prisma: `new PrismaClient({ log: [{ emit: 'event', level: 'query' }] })` and `prisma.$on('query', () => count++)`.
- TypeORM: a custom `Logger` whose `logQuery` increments a counter.
Use `@nestjs/testing` + supertest to call the endpoint and assert the count.

## N+1
- Prisma: `include`/`select` nested relations in one call; avoid `findUnique` per item inside `map` (use `findMany({ where: { id: { in: ids } } })`); `relationLoadStrategy: 'join'` where supported.
- TypeORM: `relations: { customer: true }` or `leftJoinAndSelect`; never lazy relations in serializers.
- GraphQL (Nest): DataLoader scoped per request.

## Heavy queries
- `select` only needed fields (Prisma returns every scalar by default).
- Pagination: cursor-based (`cursor` + `take`) instead of `skip` on deep pages.
- `count()` on big tables per request: cache.

## Indexes
Prisma: `@@index([customerId, status])` in the schema; for PostgreSQL big tables create it with a manual migration using `CREATE INDEX CONCURRENTLY` (edit the generated SQL, Prisma runs each migration in a transaction unless the file contains only that statement).
