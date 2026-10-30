<script setup>
import { computed, ref } from 'vue'
import { api } from '../lib/api.js'
import { useGet } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, latency, duration, compact, dateTime, ago } from '../lib/format.js'
import State from '../components/State.vue'
import PageHeader from '../components/PageHeader.vue'
import Empty from '../components/Empty.vue'

const props = defineProps({ project: Object })
const { data, error, loading, reload } = useGet(() => `/api/projects/${props.project.slug}/jobs`)

const filter = ref('')
const sort = ref(['seconds_per_day', -1])
function sortBy(key) {
  sort.value = sort.value[0] === key ? [key, -sort.value[1]] : [key, -1]
}
function arrow(key) {
  return sort.value[0] === key ? (sort.value[1] < 0 ? '↓' : '↑') : ''
}
function matches(text) {
  const q = filter.value.trim().toLowerCase()
  return !q || String(text).toLowerCase().includes(q)
}
const jobs = computed(() => {
  const [key, dir] = sort.value
  return (data.value?.jobs || [])
    .filter((j) => matches(j.name + ' ' + j.kind))
    .slice()
    .sort((a, b) => (typeof a[key] === 'string' ? String(a[key]).localeCompare(String(b[key])) : (a[key] ?? 0) - (b[key] ?? 0)) * dir)
})
const runs = computed(() => (data.value?.runs || []).filter((r) => matches(r.name + ' ' + r.host)))

// What the page is about, in three numbers: the work nobody is waiting for still costs the machine.
const totals = computed(() => {
  const list = data.value?.jobs || []
  return {
    perDay: list.reduce((sum, j) => sum + (j.seconds_per_day || 0), 0),
    runs: list.reduce((sum, j) => sum + (j.runs || 0), 0),
    failed: list.reduce((sum, j) => sum + (j.failed || 0), 0),
  }
})
const failureRate = computed(() => (totals.value.runs ? (totals.value.failed / totals.value.runs) * 100 : 0))
// A job that takes twice as long as last week is the thing to see first.
const slower = computed(() => (data.value?.jobs || []).filter((j) => (j.avg_change || 0) >= 25 && j.runs >= 5))

const kindNames = { job: 'queued job', command: 'command' }
function kindInWords(kind) {
  return kindNames[kind] ? t(kindNames[kind]) : kind
}
</script>

<template>
  <State :loading="loading" :error="error" @retry="reload">
    <PageHeader :title="t('Jobs & cron')" :subtitle="t('Work nobody is waiting for: that is why nobody notices when it gets slower.')">
      <input v-model="filter" class="input !h-8 !w-56 !py-0 text-xs" :placeholder="t('Filter…')" />
    </PageHeader>

    <section class="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
      <div class="card p-4">
        <div class="label" :title="t('Time these jobs take every day, added up.')">{{ t('time per day') }}</div>
        <div class="num mt-1 text-xl font-semibold text-poke-400">{{ duration(totals.perDay) }}</div>
      </div>
      <div class="card p-4">
        <div class="label">{{ t('runs') }} · 7d</div>
        <div class="num mt-1 text-xl font-semibold">{{ compact(totals.runs) }}</div>
      </div>
      <div class="card p-4">
        <div class="label" :title="t('Out of 100 runs, how many ended badly.')">{{ t('runs that failed') }}</div>
        <div class="num mt-1 text-xl font-semibold" :class="failureRate > 5 ? 'text-bad' : failureRate > 0 ? 'text-warn' : 'text-good'">{{ num(failureRate, 1) }}%</div>
        <div class="text-[11px] text-ink-500">{{ num(totals.failed) }} {{ t('of') }} {{ compact(totals.runs) }}</div>
      </div>
      <div class="card p-4">
        <div class="label" :title="t('Jobs taking at least a quarter longer than the week before.')">{{ t('slower than last week') }}</div>
        <div class="num mt-1 text-xl font-semibold" :class="slower.length ? 'text-warn' : 'text-good'">{{ num(slower.length) }}</div>
      </div>
    </section>

    <section v-if="data.overlaps.length" class="card mb-4 border-warn/40 p-5">
      <div class="label !text-warn">👯 {{ t('Commands running twice') }}</div>
      <div v-for="o in data.overlaps" :key="o.id" class="mt-2 text-sm text-ink-200">{{ o.title }} <span class="text-xs text-ink-500">· {{ ago(o.created_at) }}</span></div>
    </section>

    <div class="grid gap-4 xl:grid-cols-[1.35fr_1fr]">
      <section class="card overflow-hidden">
        <div class="label px-5 pt-5">{{ t('Queue jobs & commands') }} · 7d</div>
        <p class="px-5 pt-1 text-xs text-ink-500">{{ t('Click a column to sort. "Time per day" is what each one costs the machine every day.') }}</p>
        <Empty v-if="!jobs.length" icon="⟳" :title="filter ? t('Nothing matches.') : t('Nothing yet.')" class="m-4" />
        <table v-else class="mt-3 w-full text-sm">
          <thead>
            <tr class="text-left text-[11px] uppercase tracking-wider text-ink-500">
              <th class="px-5 py-2 font-medium"><button class="hover:text-ink-200" @click="sortBy('name')">{{ t('name') }} {{ arrow('name') }}</button></th>
              <th class="px-3 py-2 text-right font-medium"><button class="hover:text-ink-200" @click="sortBy('seconds_per_day')">{{ t('time per day') }} {{ arrow('seconds_per_day') }}</button></th>
              <th class="hidden px-3 py-2 text-right font-medium sm:table-cell"><button class="hover:text-ink-200" @click="sortBy('runs')">{{ t('runs') }} {{ arrow('runs') }}</button></th>
              <th class="px-3 py-2 text-right font-medium"><button class="hover:text-ink-200" @click="sortBy('failure_rate')">{{ t('failed') }} {{ arrow('failure_rate') }}</button></th>
              <th class="hidden px-3 py-2 text-right font-medium md:table-cell"><button class="hover:text-ink-200" @click="sortBy('avg')">{{ t('each') }} {{ arrow('avg') }}</button></th>
              <th class="hidden px-3 py-2 text-right font-medium lg:table-cell"><button class="hover:text-ink-200" @click="sortBy('max')">{{ t('worst') }} {{ arrow('max') }}</button></th>
              <th class="hidden px-5 py-2 text-right font-medium lg:table-cell"><button class="hover:text-ink-200" @click="sortBy('last_at')">{{ t('last run') }} {{ arrow('last_at') }}</button></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="j in jobs" :key="j.kind + j.name" class="row-hover">
              <td class="w-full max-w-0 px-5 py-2.5">
                <div class="truncate font-mono text-[12.5px] text-ink-200" :title="j.name">{{ j.name }}</div>
                <div class="text-[11px] text-ink-500">
                  {{ kindInWords(j.kind) }}
                  <span v-if="j.avg_change >= 25 && j.runs >= 5" class="ml-1 text-warn" :title="t('It took {was} each last week.', { was: latency(j.was_avg) })">
                    ▲ {{ num(j.avg_change, 0) }}% {{ t('vs last week') }}
                  </span>
                </div>
              </td>
              <td class="num whitespace-nowrap px-3 py-2.5 text-right font-medium">{{ duration(j.seconds_per_day) }}</td>
              <td class="num hidden whitespace-nowrap px-3 py-2.5 text-right text-ink-300 sm:table-cell">{{ compact(j.runs) }}</td>
              <td class="num whitespace-nowrap px-3 py-2.5 text-right" :class="j.failed ? 'text-bad' : 'text-ink-500'">
                {{ j.failed ? num(j.failure_rate, 1) + '%' : '—' }}
                <span v-if="j.failed" class="text-[11px] text-ink-500">({{ num(j.failed) }})</span>
              </td>
              <td class="num hidden whitespace-nowrap px-3 py-2.5 text-right text-ink-300 md:table-cell">{{ latency(j.avg) }}</td>
              <td class="num hidden whitespace-nowrap px-3 py-2.5 text-right text-ink-400 lg:table-cell">{{ latency(j.max) }}</td>
              <td class="hidden whitespace-nowrap px-5 py-2.5 text-right text-[11px] text-ink-500 lg:table-cell">{{ j.last_at ? ago(j.last_at) : '—' }}</td>
            </tr>
          </tbody>
        </table>
      </section>

      <section class="card overflow-hidden">
        <div class="label px-5 pt-5">{{ t('Recent runs') }} · 24h</div>
        <p class="px-5 pt-1 text-xs text-ink-500">{{ t('Commands cron started, as they happened. A dot still beating is one still running.') }}</p>
        <div class="mt-3 max-h-[480px] overflow-auto">
          <div v-for="(r, i) in runs" :key="i" class="row-hover flex items-center justify-between gap-3 px-5 py-2.5 text-sm">
            <div class="min-w-0">
              <div class="truncate font-mono text-[12.5px] text-ink-200">{{ r.name }}</div>
              <div class="text-[11px] text-ink-500">{{ r.host }} · {{ dateTime(r.started_at) }}</div>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <span v-if="r.exit_code" class="chip !border-bad/30 !text-bad" :title="t('It ended with the code {code}.', { code: r.exit_code })">{{ t('failed') }}</span>
              <span class="num text-xs text-ink-400">{{ r.seconds !== undefined ? duration(r.seconds) : '…' }}</span>
              <span class="h-2 w-2 rounded-full" :class="r.exit_code === null ? 'bg-info animate-pulse' : r.exit_code === 0 ? 'bg-good' : 'bg-bad'" />
            </div>
          </div>
          <p v-if="!runs.length" class="px-5 pb-5 text-sm text-ink-500">{{ filter ? t('Nothing matches.') : t('Nothing yet.') }}</p>
        </div>
      </section>
    </div>
  </State>
</template>
