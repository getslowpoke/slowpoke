<script setup>
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../lib/api.js'
import { useLoad } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, latency, compact, clock, day, duration, dateTime } from '../lib/format.js'
import { reasonInWords } from '../lib/exclusions.js'
import State from '../components/State.vue'
import LineChart from '../components/LineChart.vue'
import ExcludeDialog from '../components/ExcludeDialog.vue'

const props = defineProps({ project: Object })
const route = useRoute()
const query = computed(() => new URLSearchParams({ source: route.query.source || '', endpoint: route.query.endpoint || '' }).toString())
const { data, error, loading, reload } = useLoad(() => api.get(`/api/projects/${props.project.slug}/endpoints?${query.value}`), { watchSources: [query] })

const excluding = ref(false)
const restoring = ref(false)
async function restore() {
  restoring.value = true
  try {
    await api.del(`/api/projects/${props.project.slug}/exclusions/${data.value.exclusion.id}`)
    await reload()
  } finally {
    restoring.value = false
  }
}
// More than half of its time waiting on other services: the reason offered first is that one.
const mostlyOutside = computed(() => (data.value?.outbound?.share || 0) >= 0.5)

const chart = computed(() => {
  const points = data.value?.timeline || []
  return {
    labels: points.map((p) => `${day(new Date(p.hour * 1000).toISOString().slice(0, 10))} ${clock(p.hour)}`),
    datasets: [
      { label: t('average'), data: points.map((p) => p.avg), color: '#ff6f8e' },
      { label: 'max', data: points.map((p) => p.max), color: '#f5c76a', fill: false, dashed: true },
    ],
  }
})
</script>

<template>
  <State :loading="loading" :error="error" @retry="reload">
    <router-link :to="`/projects/${project.slug}/slow`" class="text-sm text-ink-400 hover:text-ink-200">← {{ t('Slow things') }}</router-link>
    <div class="mt-3 flex flex-wrap items-center gap-3">
      <span class="chip font-mono !text-sm">{{ data.endpoint.split(' ')[0] }}</span>
      <h1 class="font-mono text-xl text-ink-100">{{ data.endpoint.split(' ').slice(1).join(' ') }}</h1>
      <span class="chip">{{ data.source }}</span>
      <span v-if="data.exclusion" class="chip !border-ink-600 !text-ink-300">🙈 {{ t('left out') }}</span>
      <button v-else class="btn-ghost ml-auto !py-1 text-xs" @click="excluding = true">🙈 {{ t('Leave it out of the score') }}</button>
    </div>
    <div v-if="data.exclusion" class="card mt-4 flex flex-wrap items-center justify-between gap-3 border-ink-700 p-4 text-sm">
      <div>
        <div class="text-ink-200">{{ t('Left out of the speed index, the debt and the missions.') }}</div>
        <div class="mt-0.5 text-xs text-ink-400">
          {{ reasonInWords(data.exclusion.reason) }}<span v-if="data.exclusion.note"> · {{ data.exclusion.note }}</span>
          · <span v-if="data.exclusion.until">{{ t('until {date}', { date: dateTime(data.exclusion.until) }) }}</span><span v-else>{{ t('until we remove it') }}</span>
        </div>
      </div>
      <button class="btn-ghost !py-1 text-xs" :disabled="restoring" @click="restore">{{ t('Count it again') }}</button>
    </div>
    <ExcludeDialog v-if="excluding" :slug="project.slug" kind="endpoint" :target="data.endpoint" :label="data.endpoint" :suggested="mostlyOutside ? 'external' : 'other'"
      @cancel="excluding = false" @done="excluding = false; reload()" />
    <div class="mt-4 grid grid-cols-2 gap-3 md:grid-cols-4">
      <div class="card p-4"><div class="label">{{ t('requests') }} · 7d</div><div class="num mt-1 text-xl font-semibold">{{ compact(data.requests) }}</div></div>
      <div class="card p-4"><div class="label">{{ t('average') }}</div><div class="num mt-1 text-xl font-semibold">{{ latency(data.avg) }}</div></div>
      <div class="card p-4"><div class="label">{{ t('slow requests') }}</div><div class="num mt-1 text-xl font-semibold text-warn">{{ compact(data.slow) }}</div></div>
      <div class="card p-4"><div class="label">5xx</div><div class="num mt-1 text-xl font-semibold" :class="data.e5 ? 'text-bad' : 'text-good'">{{ num(data.e5) }}</div></div>
    </div>
    <section class="card mt-4 p-5">
      <LineChart :labels="chart.labels" :datasets="chart.datasets" :height="240" :format-y="latency" legend />
    </section>
    <section v-if="data.outbound?.hosts?.length" class="card mt-4 overflow-hidden">
      <div class="px-5 pt-5">
        <div class="label">{{ t('Waiting on other services') }}</div>
        <p class="mt-1 text-sm text-ink-300">
          {{ t('{share}% of its time goes on calls to other services: {time} per day.', { share: num(data.outbound.share * 100, 0), time: duration(data.outbound_seconds_per_day) }) }}
          <span v-if="mostlyOutside" class="text-ink-400">{{ t('The database is not the problem here: a short timeout, a cache or a queued job usually are the fix.') }}</span>
        </p>
        <div class="mt-3 h-1.5 overflow-hidden rounded-full bg-ink-800" role="img" :aria-label="t('{share}% waiting on other services', { share: num(data.outbound.share * 100, 0) })">
          <div class="h-full bg-warn" :style="{ width: data.outbound.share * 100 + '%' }" />
        </div>
      </div>
      <div v-for="h in data.outbound.hosts" :key="h.host" class="mt-3 flex flex-col gap-2 px-5 py-3 first-of-type:mt-4 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
        <div class="min-w-0">
          <div class="truncate font-mono text-[13px] text-ink-100">{{ h.host }}</div>
          <div v-if="h.origin" class="mt-0.5 truncate font-mono text-[11px] text-poke-300">↳ {{ h.origin }}</div>
        </div>
        <div class="flex shrink-0 flex-wrap items-center gap-2 text-sm sm:justify-end">
          <span v-if="h.errors" class="chip !border-bad/30 !text-bad">{{ t('{n} failed', { n: num(h.errors) }) }}</span>
          <span class="num text-ink-400">{{ num(h.per_request, 1) }}× {{ t('per request') }}</span>
          <span class="num text-ink-300">{{ latency(h.avg) }} {{ t('average') }}</span>
          <span class="num text-ink-400">max {{ latency(h.max) }}</span>
        </div>
      </div>
      <div class="h-4" />
    </section>
    <section v-if="data.queries.length" class="card mt-4 overflow-hidden">
      <div class="label px-5 pt-5">{{ t('Queries') }}</div>
      <router-link v-for="q in data.queries" :key="q.hash + q.route" :to="`/projects/${project.slug}/queries/${q.hash}`" class="row-hover mt-3 flex items-center justify-between gap-4 px-5 py-3">
        <div class="min-w-0">
          <div class="truncate font-mono text-[12.5px] text-ink-200">{{ q.fingerprint }}</div>
          <div class="mt-0.5 text-[11px] text-ink-500">{{ q.route }}</div>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <span v-if="q.n_plus_one" class="chip !border-bad/30 !text-bad">N+1</span>
          <span class="num text-sm" :class="q.per_request > 5 ? 'text-bad' : 'text-ink-300'">{{ num(q.per_request, 1) }}× {{ t('per request') }}</span>
        </div>
      </router-link>
    </section>
  </State>
</template>
