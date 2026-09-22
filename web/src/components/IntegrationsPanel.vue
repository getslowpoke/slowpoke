<script setup>
import { computed, ref } from 'vue'
import { t } from '../lib/i18n.js'
import { ago } from '../lib/format.js'
import CopyButton from './CopyButton.vue'

// What each source of a machine can do, and how to change it. Optional capabilities left off
// are choices (some teams do not want a database user or the slow log), not errors.
const props = defineProps({ checks: { type: Array, required: true } })

const open = ref({})
// What is configured, and what the agent found on the machine that nobody reads yet.
const configured = computed(() => props.checks.filter((c) => c.status !== 'missing'))
const missing = computed(() => props.checks.filter((c) => c.status === 'missing'))
const working = computed(() => configured.value.filter((c) => c.status === 'complete').length)
const kindIcons = { database: '🗄️', site: '🌐', pool: '🐘', cache: '⚡', app: '📦' }
const kindTitle = (c) => {
  const f = c.facts || {}
  switch (c.kind) {
    case 'database': return f.engine === 'postgres' ? 'PostgreSQL' : 'MySQL / MariaDB'
    case 'site': return c.name
    case 'pool': return `PHP-FPM ${f.pool}${f.version ? ' ' + f.version : ''}`
    case 'cache': return `Redis :${f.port || 6379}`
    case 'app': return `${f.framework || 'App'}${f.version ? ' ' + f.version : ''}`
    default: return c.name
  }
}
const kindDetail = (c) => {
  const f = c.facts || {}
  if (c.kind === 'app') return f.dir
  if (c.kind === 'database') return f.slow_log ? `${t('slow log on')}: ${f.slow_log}` : t('slow log off')
  if (c.kind === 'site') return f.has_duration === 'yes' ? t('the log already has the duration') : f.access_log
  if (c.kind === 'pool') return f.slow_log_on === 'yes' ? `${t('slow log on')}: ${f.path}` : t('slow log off')
  return ''
}
const missingText = {
  database: 'Slowpoke is not reading this database: no slow queries, no plans, no index suggestions from it.',
  site: 'The requests of this site are invisible: no slow endpoints, no cost per day.',
  pool: 'Where PHP waits is invisible: only how long a request took, not which line.',
  cache: 'This Redis is not read: no hit rate, no evictions, no slow commands.',
  app: 'Without the package a slow query has no file and no line: you see what is slow, not where it is.',
}
const toggle = (key) => (open.value = { ...open.value, [key]: !open.value[key] })

const typeLabels = { nginx: 'nginx', apache: 'Apache', caddy: 'Caddy', 'php-fpm': 'PHP-FPM', mysql: 'MySQL / MariaDB', 'mysql-slowlog': 'MySQL slow log', postgres: 'PostgreSQL', otlp: 'Your applications', host: 'Machine', statsd: 'StatsD', prometheus: 'Prometheus' }
const title = (c) => {
  const label = typeLabels[c.type] || c.type
  return c.name && c.name !== c.type ? `${t(label)} · ${c.name}` : t(label)
}
const typeIcons = { nginx: '🟩', apache: '🪶', caddy: '🔒', 'php-fpm': '🐘', mysql: '🐬', 'mysql-slowlog': '🐬', postgres: '🐘', otlp: '📦', host: '🖥️', statsd: '∿', prometheus: '🔥' }
const statusStyle = {
  complete: { label: 'Complete', cls: '!border-good/30 !text-good', dot: 'bg-good' },
  partial: { label: 'Partial', cls: '!border-warn/30 !text-warn', dot: 'bg-warn' },
  problem: { label: 'Problem', cls: '!border-bad/30 !text-bad', dot: 'bg-bad' },
  waiting: { label: 'Waiting for data', cls: '!text-ink-400', dot: 'bg-ink-500' },
}
const itemLabels = {
  connection: 'Connection', statistics: 'Query statistics', slow_log: 'Slow query log', plans: 'Query plans (EXPLAIN)',
  readable: 'File readable', duration: 'Request duration in the log', receiving: 'Traces received',
}
const okText = {
  statistics: 'Every query with calls and time.', slow_log: 'Slow queries with their real values.', plans: 'EXPLAIN plans and index suggestions.',
  duration: 'Slow requests per endpoint.', receiving: 'Routes, queries per request and code origins.',
}
const reasons = {
  connect_failed: 'The agent cannot connect with its database user.',
  disabled: 'Turned off in agent.yaml.',
  performance_schema_off: 'performance_schema is off: MySQL needs it on, and a restart, to keep statement statistics.',
  no_performance_schema_grant: "The agent's user cannot read performance_schema.",
  slow_log_off: 'Optional. Without it Slowpoke still has the statistics; with it, the real slow queries with their values.',
  not_read: 'The slow log is on, but no mysql-slowlog source reads it.',
  no_select: "Optional. Plans need read access to the application's tables: some teams prefer not to grant it.",
  needs_slow_log: 'Optional. On MySQL 5.7 plans use the queries of the slow log.',
  pg_stat_statements_missing: 'pg_stat_statements is not loaded.',
  no_read_all_stats: 'The agent only sees its own statements.',
  no_stats_grant: "The agent's user cannot read pg_stat_statements.",
  needs_postgres_16: 'Plans for statements without values need PostgreSQL 16 or newer.',
  unreadable: 'The agent cannot read this file.',
  not_created_yet: 'This log does not exist yet: the lines below create it.',
  needs_password: 'The source is in agent.yaml, but its password line is still a placeholder.',
  needs_capabilities: 'Optional. Without it the connections belong to the machine, not to the service that opened them.',
  no_duration: 'The log has no request duration: slow requests cannot be told apart.',
  no_requests_yet: 'No requests in the log yet.',
  no_traces_yet: 'Nothing received yet: install the package of your framework.',
  no_recent_traces: 'No traces in the last hour.',
  cannot_listen: 'The receiver cannot listen on its address.',
}
const itemDot = (it) => (it.status === 'ok' ? 'bg-good' : it.status === 'error' ? 'bg-bad' : it.status === 'waiting' ? 'bg-ink-500' : it.optional ? 'bg-ink-500' : 'bg-warn')
</script>

<template>
  <div class="space-y-3">
    <p v-if="checks.length" class="text-xs text-ink-500">
      {{ working }} {{ t('of') }} {{ configured.length }} {{ t('sources complete') }}<template v-if="missing.length">
      · <span class="text-warn">{{ missing.length }} {{ t('things on this machine nobody reads') }}</span></template>
    </p>
    <div v-for="c in configured" :key="c.source" class="rounded-2xl border border-ink-800 p-4">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div class="flex min-w-0 items-center gap-2">
          <span>{{ typeIcons[c.type] || '•' }}</span>
          <span class="truncate text-sm font-medium text-ink-100">{{ title(c) }}</span>
          <span v-if="c.facts?.path" class="hidden truncate font-mono text-xs text-ink-500 sm:inline">{{ c.facts.path }}</span>
          <span v-else-if="c.facts?.agent_cpu_percent" class="hidden text-xs text-ink-500 sm:inline">
            · {{ t('the agent costs') }} {{ c.facts.agent_cpu_percent }}% {{ t('of one core') }}, {{ c.facts.agent_memory_mb }} MB
          </span>
        </div>
        <div class="flex items-center gap-2 text-xs text-ink-500">
          <span>{{ t('checked') }} {{ ago(c.checked_at) }}</span>
          <span class="chip" :class="statusStyle[c.status]?.cls">{{ t(statusStyle[c.status]?.label || c.status) }}</span>
        </div>
      </div>

      <div class="mt-3 divide-y divide-ink-800/70">
        <div v-for="it in c.items" :key="it.key" class="py-2.5">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div class="flex min-w-0 items-start gap-2.5">
              <span class="mt-1.5 h-2 w-2 shrink-0 rounded-full" :class="itemDot(it)" />
              <div class="min-w-0">
                <div class="text-sm text-ink-100">
                  {{ t(itemLabels[it.key] || it.key) }}
                  <span v-if="it.optional" class="ml-1 text-xs text-ink-500">· {{ t('optional') }}</span>
                </div>
                <div class="mt-0.5 text-xs text-ink-400">
                  {{ it.status === 'ok' ? t(okText[it.key] || 'Active.') : t(reasons[it.reason] || it.reason || '') }}
                  <span v-if="it.detail" class="font-mono text-ink-500"> {{ it.detail }}</span>
                </div>
              </div>
            </div>
            <button v-if="c.guides?.[it.key]?.enable && it.status !== 'ok'" class="btn-ghost !px-2.5 !py-1 text-xs" @click="toggle(c.source + it.key)">
              {{ open[c.source + it.key] ? t('Hide') : t('How to enable') }}
            </button>
            <button v-else-if="c.guides?.[it.key]?.disable && it.status === 'ok'" class="text-xs text-ink-500 hover:text-ink-300" data-guide="disable" @click="toggle(c.source + it.key)">
              {{ open[c.source + it.key] ? t('Hide') : t('How to turn off') }}
            </button>
          </div>
          <ol v-if="open[c.source + it.key]" class="mt-3 space-y-3 pl-5">
            <li v-if="c.guides[it.key].cost && it.status !== 'ok'" class="list-none rounded-xl bg-ink-900/60 px-3 py-2 text-xs text-ink-400">
              <span class="text-ink-300">{{ t('What it costs the machine') }}:</span> {{ t(c.guides[it.key].cost) }}
            </li>
            <li v-for="(step, i) in (it.status === 'ok' ? c.guides[it.key].disable : c.guides[it.key].enable)" :key="i">
              <div class="flex items-center justify-between gap-2">
                <span class="text-xs text-ink-400">{{ i + 1 }}. {{ t(step.where) }}</span>
                <CopyButton v-if="step.kind !== 'text'" :text="step.code" />
              </div>
              <pre v-if="step.kind !== 'text'" class="code mt-1.5 overflow-x-auto whitespace-pre">{{ step.code }}</pre>
              <p v-else class="mt-1 text-sm text-ink-300">{{ t(step.code) }}</p>
            </li>
            <li class="list-none text-xs text-ink-500">{{ t('The agent checks again within 10 minutes, or right away when restarted.') }}</li>
          </ol>
        </div>
      </div>

      <div v-if="c.remove?.length && c.type !== 'host'" class="mt-2 border-t border-ink-800/70 pt-2">
        <button class="text-xs text-ink-500 hover:text-bad" @click="toggle(c.source + ':remove')">
          {{ open[c.source + ':remove'] ? t('Hide') : t('Remove this integration') }}
        </button>
        <ol v-if="open[c.source + ':remove']" class="mt-3 space-y-3 pl-5">
          <li v-for="(step, i) in c.remove" :key="i">
            <div class="flex items-center justify-between gap-2">
              <span class="text-xs text-ink-400">{{ i + 1 }}. {{ t(step.where) }}</span>
              <CopyButton v-if="step.kind !== 'text'" :text="step.code" />
            </div>
            <pre v-if="step.kind !== 'text'" class="code mt-1.5 overflow-x-auto whitespace-pre">{{ step.code }}</pre>
            <p v-else class="mt-1 text-sm text-ink-300">{{ t(step.code) }}</p>
          </li>
        </ol>
      </div>
    </div>

    <!-- What the agent found on the machine and nobody reads: an opportunity, not a fault. -->
    <div v-if="missing.length" class="rounded-2xl border border-warn/25 bg-warn/[0.04] p-4">
      <div class="flex items-center gap-2">
        <span>🔎</span>
        <span class="text-sm font-medium text-ink-100">{{ t('Also on this machine') }}</span>
        <span class="chip !border-warn/30 !text-warn">{{ missing.length }}</span>
      </div>
      <p class="mt-1 text-xs text-ink-400">{{ t('The agent found these while reading the machine. Slowpoke is not reading them: here is what each one would add, and what it costs.') }}</p>
      <div class="mt-3 space-y-2.5">
        <div v-for="c in missing" :key="c.source" class="rounded-xl border border-ink-800 bg-ink-950/40 p-3">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div class="flex min-w-0 items-start gap-2.5">
              <span class="mt-0.5">{{ kindIcons[c.kind] || '•' }}</span>
              <div class="min-w-0">
                <div class="text-sm text-ink-100">{{ kindTitle(c) }}</div>
                <div v-if="kindDetail(c)" class="truncate font-mono text-xs text-ink-500">{{ kindDetail(c) }}</div>
                <p class="mt-1 text-xs text-ink-400">{{ t(missingText[c.kind] || '') }}</p>
              </div>
            </div>
            <button class="btn-ghost !px-2.5 !py-1 text-xs" @click="toggle(c.source)">
              {{ open[c.source] ? t('Hide') : t('How to enable') }}
            </button>
          </div>
          <ol v-if="open[c.source]" class="mt-3 space-y-3 pl-5">
            <li v-if="c.guides?.configured?.cost" class="list-none rounded-xl bg-ink-900/60 px-3 py-2 text-xs text-ink-400">
              <span class="text-ink-300">{{ t('What it costs the machine') }}:</span> {{ t(c.guides.configured.cost) }}
            </li>
            <li v-for="(step, i) in (c.guides?.configured?.enable || [])" :key="i">
              <div class="flex items-center justify-between gap-2">
                <span class="text-xs text-ink-400">{{ i + 1 }}. {{ t(step.where) }}</span>
                <CopyButton v-if="step.kind !== 'text'" :text="step.code" />
              </div>
              <pre v-if="step.kind !== 'text'" class="code mt-1.5 overflow-x-auto whitespace-pre">{{ step.code }}</pre>
              <p v-else class="mt-1 text-sm text-ink-300">{{ t(step.code) }}</p>
            </li>
            <li class="list-none text-xs text-ink-500">{{ t('Or ask your assistant: it reads all of this through MCP and can apply it.') }}</li>
          </ol>
        </div>
      </div>
    </div>
  </div>
</template>
