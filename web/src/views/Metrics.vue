<script setup>
import { ref, computed } from 'vue'
import { api } from '../lib/api.js'
import { useLoad } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, latency, compact, clock, day } from '../lib/format.js'
import State from '../components/State.vue'
import PageHeader from '../components/PageHeader.vue'
import WindowPicker from '../components/WindowPicker.vue'
import LineChart from '../components/LineChart.vue'
import { measureKindInWords, liveTileInWords } from '../lib/settingsWords.js'
import Empty from '../components/Empty.vue'

const props = defineProps({ project: Object })
const windowSel = ref('24h')
const { data, error, loading, reload } = useLoad(() => api.get(`/api/projects/${props.project.slug}/metrics?window=${windowSel.value}`), { watchSources: [windowSel] })
const heat = useLoad(() => api.get(`/api/projects/${props.project.slug}/heatmap`))

const weekdays = computed(() => {
  const base = new Date(Date.UTC(2024, 0, 1)) // a Monday
  return Array.from({ length: 7 }, (_, i) => new Intl.DateTimeFormat(undefined, { weekday: 'short' }).format(new Date(base.getTime() + i * 86400000)))
})
// Relative scale between the best and the worst hour, so differences are visible.
const heatRange = computed(() => {
  const values = (heat.data.value?.grid || []).flat().filter((c) => c.requests_per_hour).map((c) => c.avg)
  return values.length ? [Math.min(...values), Math.max(...values)] : [0, 1]
})
function cellStyle(c) {
  if (!c.requests_per_hour) return { background: 'var(--color-ink-900)' }
  const [lo, hi] = heatRange.value
  const x = hi > lo ? (c.avg - lo) / (hi - lo) : 0
  return { background: `rgb(255 111 142 / ${0.06 + Math.pow(x, 1.4) * 0.9})` }
}
const label = (p) => (windowSel.value === '1h' || windowSel.value === '24h' ? clock(p.t) : `${day(new Date(p.t * 1000).toISOString().slice(0, 10))} ${clock(p.t)}`)

// One colour per machine: a metric with the same name on four machines is four lines, never a sum.
const hostColors = ['#8ab4ff', '#ff6f8e', '#7ee0b8', '#ffd479', '#c6a3ff', '#7adcf0']
// A gauge read on several machines has no single value: the card lists the machines instead.
const perMachine = (m) => m.kind === 'gauge' && !!m.hosts
function chartOf(m) {
  const type = m.kind === 'counter' ? 'bar' : 'line'
  const value = (p) => (m.kind === 'counter' ? p.sum : p.avg)
  if (!m.hosts) {
    return {
      labels: m.series.map(label),
      datasets: [{ label: m.kind === 'counter' ? t('count') : t('average'), data: m.series.map(value), color: m.kind === 'timer' ? '#ff6f8e' : '#8ab4ff', type }],
    }
  }
  const stamps = [...new Set(m.hosts.flatMap((h) => h.series.map((p) => p.t)))].sort((a, b) => a - b)
  return {
    labels: stamps.map((stamp) => label({ t: stamp })),
    datasets: m.hosts.map((h, i) => {
      const byStamp = new Map(h.series.map((p) => [p.t, p]))
      return {
        label: h.host || '—',
        data: stamps.map((stamp) => (byStamp.has(stamp) ? value(byStamp.get(stamp)) : null)),
        color: hostColors[i % hostColors.length],
        type,
      }
    }),
  }
}
</script>

<template>
  <div>
    <PageHeader :title="t('Metrics')">
      <WindowPicker v-model="windowSel" />
    </PageHeader>

    <section class="card mb-4 p-5">
      <div class="label">🔥 {{ t('When it hurts') }}</div>
      <p class="mt-1 text-xs text-ink-500">{{ t('The hours of the week when people wait the most, averaged over the last 28 days. Hover a square to see the hour.') }}</p>
      <div v-if="heat.data.value" class="mt-4 overflow-x-auto">
        <div class="grid min-w-[640px] grid-cols-[40px_repeat(24,1fr)] gap-[3px] text-[10px] text-ink-500">
          <div />
          <div v-for="h in 24" :key="h" class="text-center">{{ (h - 1) % 3 === 0 ? h - 1 : '' }}</div>
          <template v-for="(row, d) in heat.data.value.grid" :key="d">
            <div class="flex items-center">{{ weekdays[d] }}</div>
            <div v-for="(c, h) in row" :key="h" class="aspect-square rounded-[4px] transition hover:scale-125" :style="cellStyle(c)"
              :title="`${weekdays[d]} ${h}:00 · ${latency(c.avg)} · ${num(c.requests_per_hour)} req/h · ${num(c.slow_ratio, 1)}% slow`" />
          </template>
        </div>
      </div>
    </section>

    <State :loading="loading" :error="error" @retry="reload">
      <Empty v-if="!data.metrics.length" icon="∿" :title="t('No custom metrics yet.')">
        <pre class="code text-left">Slowpoke::measure('invoices.pdf', fn () => $pdf->render());
Slowpoke::increment('checkout.failed');
echo "queue.backlog:42|g" | nc -u -w0 127.0.0.1 8125</pre>
      </Empty>
      <div v-else class="grid gap-4 lg:grid-cols-2">
        <section v-for="m in data.metrics" :key="m.name" class="card p-5">
          <div class="flex items-start justify-between gap-3">
            <div>
              <div class="font-mono text-sm text-ink-100">{{ m.name }}</div>
              <div class="mt-0.5 text-xs text-ink-500">
                {{ measureKindInWords(m.kind) }} · {{ t('{n} values', { n: compact(m.count) }) }}<template v-if="m.hosts"> · {{ t('{n} machines', { n: m.hosts.length }) }}</template>
              </div>
            </div>
            <div v-if="perMachine(m)" class="space-y-0.5 text-right text-xs">
              <div v-for="(h, i) in m.hosts" :key="h.host" class="flex items-baseline justify-end gap-2">
                <span class="text-ink-500">{{ h.host || '—' }}</span>
                <span class="num font-semibold" :style="{ color: hostColors[i % hostColors.length] }">{{ num(h.last, 2) }}</span>
              </div>
            </div>
            <div v-else class="text-right">
              <div class="num text-xl font-semibold text-poke-400">
                {{ m.kind === 'timer' ? latency(m.p95) : m.kind === 'gauge' ? num(m.last, 2) : compact(m.sum) }}
              </div>
              <div class="text-[11px] text-ink-500" :title="m.kind === 'timer' ? liveTileInWords('p95') : ''">{{ m.kind === 'timer' ? t('worst case') : m.kind === 'gauge' ? t('last') : t('total') }}</div>
            </div>
          </div>
          <div v-if="m.hosts" class="mt-2 text-[11px] text-ink-500">{{ t('Same name on several machines: one line each, never a sum.') }}</div>
          <LineChart class="mt-3" :height="150" :labels="chartOf(m).labels" :datasets="chartOf(m).datasets" :legend="!!m.hosts"
            :format-y="m.kind === 'timer' ? latency : (v) => num(v, 1)" />
        </section>
      </div>
    </State>
  </div>
</template>
