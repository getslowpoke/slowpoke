# Node.js: Express, Fastify, NestJS, Koa

> **There is a package of our own**, and it is the only way to get the file and line of every query —
> auto-instrumentation points inside `node_modules`, which tells you nothing. It sits on `pg` and
> `mysql2`, so TypeORM, Sequelize, Knex and Drizzle are covered too, and it traces BullMQ jobs and
> whatever cron runs:
>
> ```sh
> npm install @slowpokedev/node
> ```
>
> Two lines per framework, in [packages/node/README.md](../../packages/node/README.md). The steps below
> are only for an application it does not cover.

```bash
npm install @opentelemetry/api @opentelemetry/auto-instrumentations-node
```

Start the process with the auto-instrumentation registered before your code:

```bash
export OTEL_SERVICE_NAME=shop-api OTEL_TRACES_EXPORTER=otlp \
  OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
  OTEL_METRICS_EXPORTER=none OTEL_LOGS_EXPORTER=none \
  OTEL_NODE_RESOURCE_DETECTORS=env,host
node --require @opentelemetry/auto-instrumentations-node/register server.js
```

PM2: put the variables in `ecosystem.config.js` and `node_args: '--require @opentelemetry/auto-instrumentations-node/register'`.

Covered: http, express, fastify, koa, NestJS, pg, mysql2, mongodb/mongoose, redis/ioredis.
Prisma: add `@prisma/instrumentation` (`new PrismaInstrumentation()` in a small `tracing.js`
using `@opentelemetry/sdk-node`).
