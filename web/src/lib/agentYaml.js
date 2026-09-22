// The /etc/slowpoke/agent.yaml the wizard shows: one place, so it can be read and tested without
// a browser. Every source here is one the agent knows how to read (internal/agent/runner.go).

/**
 * @param {{server: string, token?: string, host?: string, roles?: string[]}} choices
 * @returns {string} the file to put on the machine
 */
export function agentYaml({ server, token, host, roles = [] }) {
  const has = (role) => roles.includes(role)
  const lines = [`server: ${server}`, `token: ${token || '${SLOWPOKE_TOKEN}'}`]
  if (host) lines.push(`host: ${host}`)
  lines.push('sources:', '  - type: host')

  if (has('web')) lines.push('  - type: nginx', '    name: app', '    path: /var/log/nginx/access.log   # log_format with rt=$request_time', '    # site: shop.example.com   # the host it answers for, if the application runs on another machine')
  if (has('apache')) lines.push('  - type: apache', '    name: site', '    path: /var/log/apache2/access.log   # LogFormat ... %D', '    # site: shop.example.com   # the host it answers for, if the application runs on another machine')
  if (has('caddy')) lines.push('  - type: caddy', '    name: site', '    path: /var/log/caddy/access.log   # JSON access log', '    # site: shop.example.com   # the host it answers for, if the application runs on another machine')
  if (has('php')) lines.push('  - type: php-fpm', '    name: www', '    path: /var/log/php-fpm/www-slow.log   # request_slowlog_timeout = 1s')
  // A language with no package of ours yet sends its own numbers; one receiver is enough.
  if (['node', 'python', 'otherlang'].some(has) && !has('statsd')) lines.push('  - type: statsd', '    listen: 127.0.0.1:8125')
  if (has('prometheus')) lines.push('  - type: prometheus', '    name: app', '    url: http://127.0.0.1:9090/metrics')
  if (has('mysql')) {
    lines.push('  - type: mysql-slowlog', '    path: /var/log/mysql/mysql-slow.log',
      '  - type: mysql', '    dsn: "slowpoke:${SLOWPOKE_DB_PASSWORD}@tcp(127.0.0.1:3306)/"')
  }
  if (has('postgres')) {
    lines.push('  - type: postgres', '    dsn: "postgres://slowpoke:${SLOWPOKE_DB_PASSWORD}@127.0.0.1:5432/postgres"')
  }
  // Redis says everything about itself: INFO for memory, evictions and hit rate, SLOWLOG for the
  // commands that took too long. No user to create, and the password never goes in this file.
  if (has('redis')) {
    lines.push('  - type: redis', '    name: cache', '    addr: 127.0.0.1:6379',
      '    password: "${SLOWPOKE_REDIS_PASSWORD}"   # remove this line if Redis has no password')
  }
  // RabbitMQ answers through the management plugin it ships with: how deep every queue is, how
  // fast it drains, how many consumers are on it. Read-only, two requests a minute.
  if (has('rabbitmq')) {
    lines.push('  - type: rabbitmq', '    name: broker', '    url: http://127.0.0.1:15672',
      '    user: slowpoke', '    password: "${SLOWPOKE_RABBITMQ_PASSWORD}"')
  }
  // Elasticsearch and OpenSearch answer the same way: how long searching took, what was
  // rejected, how the cluster is. Nothing is read from an index.
  if (has('elasticsearch')) {
    lines.push('  - type: elasticsearch', '    name: search', '    url: http://127.0.0.1:9200',
      '    # user: slowpoke', '    # password: "${SLOWPOKE_ELASTIC_PASSWORD}"')
  }
  // MongoDB writes every slow operation in its own log, with what it scanned and whether it had
  // an index: nothing to enable but the threshold, and no user to create.
  if (has('mongodb')) {
    lines.push('  - type: mongodb', '    name: main', '    path: /var/log/mongodb/mongod.log',
      '    # db.setProfilingLevel(0, { slowms: 100 }) to choose what counts as slow')
  }
  if (has('statsd')) lines.push('  - type: statsd', '    listen: 127.0.0.1:8125')
  return lines.join('\n')
}

/** The passwords the chosen sources need, as the installer takes them: --env NAME=value. */
export function passwordVariables(roles = []) {
  const names = []
  if (roles.includes('mysql') || roles.includes('postgres')) names.push('SLOWPOKE_DB_PASSWORD')
  if (roles.includes('redis')) names.push('SLOWPOKE_REDIS_PASSWORD')
  if (roles.includes('rabbitmq')) names.push('SLOWPOKE_RABBITMQ_PASSWORD')
  return names
}
