<script setup>
import { computed, ref } from 'vue'
import { api } from '../lib/api.js'
import { useLoad } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, latency, duration, compact, ago, clock, day } from '../lib/format.js'
import State from '../components/State.vue'
import LineChart from '../components/LineChart.vue'
import CopyButton from '../components/CopyButton.vue'
import Severity from '../components/Severity.vue'
import MissionCard from '../components/MissionCard.vue'
import MuteDialog from '../components/MuteDialog.vue'
import ExcludeDialog from '../components/ExcludeDialog.vue'
import { reasonInWords } from '../lib/exclusions.js'

const props = defineProps({ project: Object, hash: String })
const { data, error, loading, reload } = useLoad(() => api.get(`/api/projects/${props.project.slug}/queries/${props.hash}`), { watchSources: [() => props.hash] })

// Advice the team cannot follow (a vendor's table, an index the DBA refuses) is muted for this
// query: it leaves the prompt, the report and the missions, and stays readable below.
const muting = ref(null)
// A query of a library nobody can change: out of the score, the debt and the missions.
const excluding = ref(false)
const restoring = ref(false)
async function countAgain() {
  restoring.value = true
  try {
    await api.del(`/api/projects/${props.project.slug}/exclusions/${data.value.exclusion.id}`)
    await reload()
  } finally {
    restoring.value = false
  }
}
const unmuting = ref(0)
async function unmute(m) {
  unmuting.value = m.mute.id
  try {
    await api.del(`/api/projects/${props.project.slug}/queries/${props.hash}/mutes/${m.mute.id}`)
    await reload()
  } finally {
    unmuting.value = 0
  }
}

const timeline = computed(() => {
  const byHour = new Map()
  for (const p of data.value?.timeline || []) {
    // One line per hour: the most complete source wins, as on the server.
    const rank = { 'mysql-digest': 3, 'pg-statements': 3, app: 2, 'mysql-slowlog': 1 }[p.source] || 0
    const cur = byHour.get(p.hour)
    if (!cur || rank > cur.rank) byHour.set(p.hour, { ...p, rank })
  }
  const points = [...byHour.values()].sort((a, b) => a.hour - b.hour)
  return {
    labels: points.map((p) => `${day(new Date(p.hour * 1000).toISOString().slice(0, 10))} ${clock(p.hour)}`),
    datasets: [
      { label: t('time lost'), data: points.map((p) => p.seconds), color: '#ff6f8e', type: 'bar' },
      { label: t('calls'), data: points.map((p) => p.calls), color: '#8ab4ff', axis: 'y', fill: false, dashed: true },
    ],
  }
})

const planRows = computed(() => {
  const plan = data.value?.explain?.plan
  return Array.isArray(plan) ? plan : null
})
</script>

<template>
  <State :loading="loading" :error="error" @retry="reload">
    <router-link :to="`/projects/${project.slug}/slow`" class="text-sm text-ink-400 hover:text-ink-200">← {{ t('Slow things') }}</router-link>

    <div class="mt-3 flex flex-wrap items-start justify-between gap-4">
      <div class="min-w-0 flex-1">
        <div class="label">{{ t('Query doctor') }} · <span class="font-mono normal-case tracking-normal">{{ hash }}</span></div>
        <pre class="code mt-2 whitespace-pre-wrap break-words !text-[13px] !text-ink-100">{{ data.fingerprint }}</pre>
      </div>
      <button v-if="!data.exclusion" class="btn-ghost !py-1 text-xs" @click="excluding = true">🙈 {{ t('Leave it out of the score') }}</button>
    </div>
    <div v-if="data.exclusion" class="card mt-4 flex flex-wrap items-center justify-between gap-3 border-ink-700 p-4 text-sm">
      <div>
        <div class="text-ink-200">🙈 {{ t('Left out of the speed index, the debt and the missions.') }}</div>
        <div class="mt-0.5 text-xs text-ink-400">
          {{ reasonInWords(data.exclusion.reason) }}<span v-if="data.exclusion.note"> · {{ data.exclusion.note }}</span>
          <span v-if="data.exclusion.created_by"> · {{ data.exclusion.created_by }}</span>
        </div>
      </div>
      <button class="btn-ghost !py-1 text-xs" :disabled="restoring" @click="countAgain">{{ t('Count it again') }}</button>
    </div>
    <ExcludeDialog v-if="excluding" :slug="project.slug" kind="query" :target="hash" :label="data.fingerprint" suggested="third_party"
      @cancel="excluding = false" @done="excluding = false; reload()" />

    <div class="mt-4 grid grid-cols-2 gap-3 md:grid-cols-4">
      <div class="card p-4"><div class="label">{{ t('time lost') }}</div><div class="num mt-1 text-xl font-semibold text-poke-400">{{ duration(data.stats.seconds_per_day) }}</div><div class="text-xs text-ink-500">{{ t('per day') }}</div></div>
      <div class="card p-4"><div class="label">{{ t('calls') }}</div><div class="num mt-1 text-xl font-semibold">{{ compact(data.stats.calls_per_day) }}</div><div class="text-xs text-ink-500">{{ t('per day') }}</div></div>
      <div class="card p-4"><div class="label">{{ t('average') }}</div><div class="num mt-1 text-xl font-semibold">{{ latency(data.stats.avg_seconds) }}</div></div>
      <div class="card p-4"><div class="label">{{ t('rows') }}</div><div class="num mt-1 text-xl font-semibold">{{ compact(data.stats.rows_examined_avg) }}</div><div class="text-xs text-ink-500">{{ t('per request') }}</div></div>
    </div>

    <div class="mt-4 grid gap-4 xl:grid-cols-[1.3fr_1fr]">
      <section class="card p-5">
        <div class="flex items-center justify-between gap-3">
          <div class="label">{{ t('Diagnosis') }}</div>
          <CopyButton :text="data.prompt" :label="t('Copy the AI prompt')" />
        </div>
        <div class="mt-4 space-y-3">
          <div v-for="d in data.diagnoses" :key="d.code + d.table" class="rounded-xl border border-ink-800 bg-ink-850/50 p-4">
            <div class="flex flex-wrap items-center gap-2">
              <Severity :level="d.severity" />
              <div class="font-medium">{{ d.title }}</div>
              <button class="btn-ghost ml-auto !px-2 !py-0.5 text-xs" :title="t('We cannot fix this one: stop suggesting it')" @click="muting = d">🔕 {{ t('Mute') }}</button>
            </div>
            <p class="mt-2 text-sm leading-relaxed text-ink-300">{{ d.detail }}</p>
            <div v-if="d.fix" class="mt-3">
              <div class="mb-1 flex items-center justify-between">
                <div class="text-xs font-medium text-good">{{ t('How to fix it') }}</div>
                <CopyButton v-if="/^(ALTER|CREATE)/.test(d.fix)" :text="d.fix" />
              </div>
              <pre v-if="/^(ALTER|CREATE)/.test(d.fix)" class="code whitespace-pre-wrap break-words !text-good/90">{{ d.fix }}</pre>
              <p v-else class="text-sm text-ink-200">{{ d.fix }}</p>
            </div>
          </div>
          <p v-if="!data.diagnoses.length && !data.muted_diagnoses?.length" class="py-6 text-center text-sm text-ink-400">✨ {{ t('No problems found.') }}</p>
          <p v-else-if="!data.diagnoses.length" class="py-4 text-center text-sm text-ink-400">{{ t('Nothing left to suggest: the rest is muted.') }}</p>
          <details v-if="data.muted_diagnoses?.length" class="rounded-xl border border-ink-800 p-3 text-sm">
            <summary class="cursor-pointer text-ink-400">🔕 {{ t('Muted by the team') }} ({{ data.muted_diagnoses.length }})</summary>
            <div v-for="m in data.muted_diagnoses" :key="m.mute.id" class="mt-3 flex flex-wrap items-start justify-between gap-2">
              <div class="min-w-0">
                <div class="text-ink-300">{{ m.title }}</div>
                <div class="mt-0.5 text-xs text-ink-500"><span v-if="m.mute.note">{{ m.mute.note }} · </span>{{ m.mute.created_by }} · {{ ago(m.mute.created_at) }}</div>
              </div>
              <button class="btn-ghost !px-2 !py-0.5 text-xs" :disabled="unmuting === m.mute.id" @click="unmute(m)">{{ t('Turn it back on') }}</button>
            </div>
          </details>
          <MuteDialog v-if="muting" :title="t('Mute this suggestion')" :subject="muting.title"
            :explain="t('It stops showing for this query, in the AI prompt, in the debt report and in the missions read by the coding assistant. The same suggestion on other queries still shows. The time the query costs keeps counting.')"
            :placeholder="t('e.g. vendor table, we cannot add indexes')" :url="`/api/projects/${project.slug}/queries/${hash}/mutes`"
            :payload="{ code: muting.code, table: muting.table || '' }" @cancel="muting = null" @done="muting = null; reload()" />
          <p v-if="data.explain_status === 'waiting'" class="text-xs text-ink-500">⧗ {{ t('Waiting for EXPLAIN from the agent.') }}</p>
          <p v-if="data.explain_status === 'requested'" class="text-xs text-ink-500">⧗ {{ t('EXPLAIN requested: the agent will answer within a minute.') }}</p>
          <p v-if="data.explain_status === 'error'" class="text-xs text-warn">EXPLAIN: {{ data.explain_error }}</p>
        </div>
      </section>

      <section class="space-y-4">
        <div class="card p-5">
          <div class="label">{{ t('Real statement (redacted)') }}</div>
          <pre class="code mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words">{{ data.sample || '—' }}</pre>
          <div class="mt-3 flex flex-wrap gap-2 text-xs">
            <span v-if="data.database" class="chip">db {{ data.database }}</span>
            <span v-if="data.db_user" class="chip">user {{ data.db_user }}</span>
            <span class="chip">{{ t('last seen') }} {{ ago(data.last_seen_at) }}</span>
          </div>
          <div v-if="data.origin" class="mt-4">
            <div class="label">{{ t('Called from') }}</div>
            <div class="mt-1 break-all font-mono text-sm text-poke-300">{{ data.origin }}</div>
            <div v-if="data.n_plus_one" class="mt-1 text-xs text-bad">N+1 · {{ num(data.n_plus_one) }} {{ t('requests') }}</div>
          </div>
        </div>

        <div v-if="data.routes.length" class="card p-5">
          <div class="label">{{ t('Routes running it') }}</div>
          <div class="mt-2 space-y-1.5">
            <div v-for="r in data.routes" :key="r.route" class="flex items-center justify-between gap-3 text-sm">
              <span class="truncate font-mono text-[12.5px] text-ink-200">{{ r.route }}</span>
              <span class="num shrink-0 text-xs" :class="r.per_request > 5 ? 'text-bad' : 'text-ink-400'">{{ num(r.per_request, 1) }}× {{ t('per request') }}</span>
            </div>
          </div>
        </div>

        <MissionCard v-for="m in data.missions" :key="m.id" :mission="m" :slug="project.slug" :settings="project.settings?.missions || {}" />
      </section>
    </div>

    <section class="card mt-4 p-5">
      <div class="label">7d</div>
      <LineChart class="mt-3" :labels="timeline.labels" :datasets="timeline.datasets" :height="220" :format-y="(v) => num(v, 0)" legend />
    </section>

    <section v-if="data.explain" class="card mt-4 p-5">
      <div class="label">EXPLAIN · {{ data.explain.engine }}</div>
      <p v-if="data.explain.generic" class="mt-1 text-xs text-ink-500">{{ t('Generic plan: PostgreSQL only keeps the query without its values, so row counts are the planner\'s estimates.') }}</p>
      <div v-if="planRows" class="mt-3 overflow-auto">
        <table class="w-full font-mono text-xs">
          <thead><tr class="text-left text-ink-500"><th v-for="k in Object.keys(planRows[0] || {})" :key="k" class="px-2 py-1.5 font-normal">{{ k }}</th></tr></thead>
          <tbody>
            <tr v-for="(row, i) in planRows" :key="i" class="border-t border-ink-800">
              <td v-for="k in Object.keys(planRows[0] || {})" :key="k" class="px-2 py-1.5" :class="k === 'type' && row[k] === 'ALL' ? 'text-bad' : 'text-ink-300'">{{ row[k] ?? '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <pre v-else class="code mt-3 max-h-80 overflow-auto">{{ JSON.stringify(data.explain.plan, null, 2) }}</pre>

      <div class="mt-5 grid gap-3 md:grid-cols-2">
        <div v-for="(def, name) in data.explain.tables" :key="name" class="rounded-xl border border-ink-800 p-4">
          <div class="flex items-center justify-between">
            <div class="font-mono text-sm text-ink-100">{{ name }}</div>
            <div class="num text-xs text-ink-400">{{ compact(def.rows) }} {{ t('rows') }}</div>
          </div>
          <div class="mt-3 text-xs text-ink-500">{{ def.columns?.length }} {{ t('columns') }}</div>
          <div class="mt-1 flex flex-wrap gap-1">
            <span v-for="c in def.columns" :key="c.name" class="chip font-mono">{{ c.name }} <span class="text-ink-500">{{ c.type }}</span></span>
          </div>
          <div class="mt-3 text-xs text-ink-500">{{ t('indexes') }}</div>
          <div class="mt-1 space-y-1">
            <div v-for="ix in def.indexes" :key="ix.name" class="font-mono text-xs text-ink-300">{{ ix.name }} ({{ ix.columns.join(', ') }}){{ ix.unique ? ' · unique' : '' }}</div>
          </div>
        </div>
      </div>
    </section>
  </State>
</template>
