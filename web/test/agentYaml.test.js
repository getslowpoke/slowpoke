import assert from 'node:assert'
import { test } from 'node:test'

import { agentYaml, passwordVariables } from '../src/lib/agentYaml.js'

const config = (roles, extra = {}) => agentYaml({ server: 'https://slowpoke.example.com', roles, ...extra })

test('a machine with nothing chosen still reports itself', () => {
  const yaml = config([])
  assert.match(yaml, /^server: https:\/\/slowpoke\.example\.com$/m)
  assert.match(yaml, /^token: \$\{SLOWPOKE_TOKEN\}$/m, 'the token is read from the environment unless given')
  assert.match(yaml, /sources:\n {2}- type: host/)
})

test('the name and the token given in the wizard end up in the file', () => {
  const yaml = config([], { token: 'spa_abc', host: 'web-9' })
  assert.match(yaml, /^token: spa_abc$/m)
  assert.match(yaml, /^host: web-9$/m)
})

test('every source the wizard offers is one the agent knows how to read', () => {
  const types = { web: 'nginx', apache: 'apache', caddy: 'caddy', php: 'php-fpm', mysql: 'mysql', postgres: 'postgres', redis: 'redis', rabbitmq: 'rabbitmq', elasticsearch: 'elasticsearch', mongodb: 'mongodb', statsd: 'statsd', prometheus: 'prometheus' }
  for (const [role, type] of Object.entries(types)) {
    assert.match(config([role]), new RegExp(`- type: ${type}\\b`), `${role} does not produce a ${type} source`)
  }
})

test('Redis is read with no user to create and no password in the file', () => {
  const yaml = config(['redis'])
  assert.match(yaml, /- type: redis/)
  assert.match(yaml, /addr: 127\.0\.0\.1:6379/)
  assert.match(yaml, /password: "\$\{SLOWPOKE_REDIS_PASSWORD\}"/, 'the password comes from the environment')
  assert.deepStrictEqual(passwordVariables(['redis']), ['SLOWPOKE_REDIS_PASSWORD'])
  assert.deepStrictEqual(passwordVariables(['mysql', 'redis']), ['SLOWPOKE_DB_PASSWORD', 'SLOWPOKE_REDIS_PASSWORD'])
  assert.deepStrictEqual(passwordVariables([]), [])
})

test('a language without a package of ours gets one receiver, not two', () => {
  const both = config(['node', 'statsd'])
  assert.strictEqual(both.match(/- type: statsd/g).length, 1, 'the same receiver was declared twice')
  assert.match(config(['python']), /- type: statsd/)
})

test('MySQL brings its slow log with it', () => {
  const yaml = config(['mysql'])
  assert.match(yaml, /- type: mysql-slowlog/)
  assert.match(yaml, /- type: mysql\n {4}dsn: "slowpoke:\$\{SLOWPOKE_DB_PASSWORD\}/)
})

test('RabbitMQ is read through its management API, with a user of its own', () => {
  const yaml = config(['rabbitmq'])
  assert.match(yaml, /- type: rabbitmq/)
  assert.match(yaml, /url: http:\/\/127\.0\.0\.1:15672/)
  assert.match(yaml, /user: slowpoke/)
  assert.match(yaml, /password: "\$\{SLOWPOKE_RABBITMQ_PASSWORD\}"/)
  assert.deepStrictEqual(passwordVariables(['rabbitmq']), ['SLOWPOKE_RABBITMQ_PASSWORD'])
})

test('Elasticsearch is read over http, with the login only if there is one', () => {
  const yaml = config(['elasticsearch'])
  assert.match(yaml, /- type: elasticsearch/)
  assert.match(yaml, /url: http:\/\/127\.0\.0\.1:9200/)
  assert.match(yaml, /# user: slowpoke/, 'a cluster without security needs no user: the line is a comment')
  assert.deepStrictEqual(passwordVariables(['elasticsearch']), [], 'nothing is required, so nothing is asked for')
})

test('MongoDB is read from its own log, with nothing to install', () => {
  const yaml = config(['mongodb'])
  assert.match(yaml, /- type: mongodb/)
  assert.match(yaml, /path: \/var\/log\/mongodb\/mongod\.log/)
  assert.match(yaml, /slowms/, 'the line that decides what counts as slow is worth saying')
  assert.deepStrictEqual(passwordVariables(['mongodb']), [], 'no user, no password')
})

test('a web source carries the line that links it to an application on another machine', () => {
  const yaml = config(['web'])
  assert.match(yaml, /# site: shop\.example\.com/, 'the hint is missing')
  // Commented out: a wrong host would pair two things that are not the same requests.
  for (const line of yaml.split('\n')) {
    if (line.includes('site:')) assert.match(line.trim(), /^#/, `site is not commented out: ${line}`)
  }
})
