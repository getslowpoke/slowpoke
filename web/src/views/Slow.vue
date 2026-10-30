<script setup>
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { useGet } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, latency, duration, compact, pct } from '../lib/format.js'
import State from '../components/State.vue'
import PageHeader from '../components/PageHeader.vue'
import WindowPicker from '../components/WindowPicker.vue'
import Empty from '../components/Empty.vue'
import WinsCard from '../components/WinsCard.vue'
import { slowTabInWords, queryBadgeInWords } from '../lib/settingsWords.js'
import { phpWait } from '../lib/phpWaits.js'
import ExcludeDialog from '../components/ExcludeDialog.vue'

const props = defineProps({ project: Object })
const windowSel = ref('24h')
const tab = ref('queries')
// What the team cannot fix stops counting, from the list where it is looked at.
const excluding = ref(null)
const leaveOut = (kind, target, label) => (excluding.value = { kind, target, label })
// Straight to a mission from the list: the server opens one, or answers with the one already open.
const router = useRouter()
const opening = ref('')
async function makeMission(kind, target) {
  opening.value = kind + ' ' + target
  try {
    const res = await api.post(`/api/projects/${props.project.slug}/missions`, { kind, target })
    router.push(`/projects/${props.project.slug}/missions/${res.id}`)
  } catch {
    opening.value = ''
  }
}
// One machine at a time: with a dozen of them, everything together is unreadable.
const machine = ref('')
const machines = ref([])
const database = ref('')
api.get(`/api/projects/${props.project.slug}/hosts`).then((h) => (machines.value = (h.hosts || []).filter((m) => m.kind === 'agent').map((m) => m.name))).catch(() => {})
const { data, error, loading, reload } = useGet(
  () => `/api/projects/${props.project.slug}/slow?window=${windowSel.value}` +
    `${machine.value ? `&host=${encodeURIComponent(machine.value)}` : ''}${database.value ? `&db=${encodeURIComponent(database.value)}` : ''}`,
  { watchSources: [windowSel, machine, database] },
)

const filter = ref('')
const sort = ref({ queries: ['seconds', -1], endpoints: ['slow_seconds', -1], php: ['count', -1], hotspots: ['seconds_per_day', -1] })
function sortBy(key) {
  const [cur, dir] = sort.value[tab.value]
  sort.value = { ...sort.value, [tab.value]: [key, cur === key ? -dir : -1] }
}
function arrow(key) {
  const [cur, dir] = sort.value[tab.value]
  return cur === key ? (dir < 0 ? '↓' : '↑') : ''
}
function rows(list, fields) {
  const q = filter.value.trim().toLowerCase()
  const [key, dir] = sort.value[tab.value]
  return (list || [])
    .filter((r) => !q || fields.some((f) => String(r[f] ?? '').toLowerCase().includes(q)))
    .slice()
    .sort((a, b) => (typeof a[key] === 'string' ? a[key].localeCompare(b[key]) : (a[key] ?? 0) - (b[key] ?? 0)) * dir)
}
const queryRows = computed(() => rows(data.value?.queries, ['fingerprint', 'origin']))
const endpointRows = computed(() => rows(data.value?.endpoints, ['endpoint', 'source']))
const phpRows = computed(() => rows(data.value?.php, ['frame', 'code']))
const maxQuery = computed(() => Math.max(1e-9, ...(data.value?.queries || []).map((q) => q.seconds)))
const maxEndpoint = computed(() => Math.max(1e-9, ...(data.value?.endpoints || []).map((e) => e.slow_seconds)))
const maxPhp = computed(() => Math.max(1, ...(data.value?.php || []).map((p) => p.count)))
const hotspots = useGet(() => `/api/projects/${props.project.slug}/hotspots`)
const maxHot = computed(() => Math.max(1e-9, ...(hotspots.data.value?.hotspots || []).map((h) => h.seconds_per_day)))
// The same row, a window earlier: half a second a day is noise, not news.
function delta(row) {
  const saved = row.saved_seconds_per_day
  if (saved == null || Math.abs(saved) < 0.5) return null
  return { better: saved > 0, text: (saved > 0 ? '−' : '+') + duration(saved) }
}
const windowLabel = computed(() => ({ '1h': t('the hour before'), '24h': t('the day before'), '7d': t('the seven days before'), '30d': t('the thirty days before') }[windowSel.value] || t('the window before')))
const tabs = computed(() => [
  { key: 'queries', label: t('Queries'), n: data.value?.queries?.length },
  { key: 'endpoints', label: t('Endpoints'), n: data.value?.endpoints?.length },
  { key: 'php', label: t('PHP waiting'), n: data.value?.php?.length },
  { key: 'hotspots', label: '🔥 ' + t('Hotspots'), n: hotspots.data.value?.hotspots?.length },
])
</script>

<template>
  <div>
    <PageHeader :title="t('Slow things')" :subtitle="t('Find slow code, turn it into missions, level up.')">
      <select v-model="machine" class="input !h-8 !w-auto !py-0 text-xs">
        <option value="">{{ t('All machines') }}</option>
        <option v-for="m in machines" :key="m" :value="m">{{ m }}</option>
      </select>
      <select v-if="(data?.databases || []).length > 1" v-model="database" class="input !h-8 !w-auto !py-0 text-xs">
        <option value="">{{ t('All databases') }}</option>
        <option v-for="d in data.databases" :key="d" :value="d">{{ d }}</option>
      </select>
      <WindowPicker v-model="windowSel" />
    </PageHeader>

    <div class="mb-4 flex flex-wrap items-end justify-between gap-3 border-b border-ink-800">
      <div class="flex gap-1">
      <button v-for="tb in tabs" :key="tb.key" class="-mb-px border-b-2 px-3 py-2 text-sm transition"
        :class="tab === tb.key ? 'border-poke-500 text-ink-100' : 'border-transparent text-ink-400 hover:text-ink-200'" @click="tab = tb.key">
        {{ tb.label }} <span v-if="tb.n" class="num ml-1 text-xs text-ink-500">{{ tb.n }}</span>
      </button>
      </div>
      <input v-model="filter" class="input mb-2 !w-64 !py-1.5" :placeholder="t('Filter…')" />
    </div>

    <State :loading="loading" :error="error" @retry="reload">
      <!-- The wins are the project's: with a machine or a database chosen, they would look like
           that machine's and they are not. -->
      <WinsCard v-if="!machine && !database" class="mb-4" :wins="data.wins" :slug="project.slug"
        :note="t('Against {window}, counted on what everything costs per day.', { window: windowLabel })" />

      <!-- Queries -->
      <p v-if="machine && tab === 'queries' && data.queries_without_machine" class="mb-2 text-xs text-ink-500">
        {{ t('{n} hours of statements were recorded before machines were kept apart: they have no machine and are not shown here.', { n: data.queries_without_machine }) }}
      </p>
      <p class="mb-3 text-xs text-ink-500">{{ slowTabInWords(tab) }}</p>

      <div v-if="tab === 'queries'" class="card overflow-hidden">
        <Empty v-if="!data.queries.length" icon="⛁" :title="t('No problems found.')" class="m-4" />
        <table v-else class="w-full table-fixed text-sm">
          <thead>
            <tr class="text-left text-[11px] uppercase tracking-wider text-ink-500">
              <th class="w-auto px-4 py-3 font-medium"><button class="hover:text-ink-200" @click="sortBy('fingerprint')">{{ t('Queries') }} {{ arrow('fingerprint') }}</button></th>
              <th class="w-28 px-3 py-3 text-right font-medium" :title="t('Waiting this cost in the period, added up.')"><button class="hover:text-ink-200" @click="sortBy('seconds')">{{ t('time lost') }} {{ arrow('seconds') }}</button></th>
              <th class="hidden w-24 px-3 py-3 text-right font-medium md:table-cell"><button class="hover:text-ink-200" @click="sortBy('calls')">{{ t('calls') }} {{ arrow('calls') }}</button></th>
              <th class="hidden w-24 px-3 py-3 text-right font-medium md:table-cell"><button class="hover:text-ink-200" @click="sortBy('avg')">{{ t('average') }} {{ arrow('avg') }}</button></th>
              <th class="hidden w-24 px-4 py-3 text-right font-medium lg:table-cell"><button class="hover:text-ink-200" @click="sortBy('max')">{{ t('worst') }} {{ arrow('max') }}</button></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="q in queryRows" :key="q.hash" class="row-hover cursor-pointer" :class="q.excluded ? 'opacity-50' : ''" @click="$router.push(`/projects/${project.slug}/queries/${q.hash}`)">
              <td class="px-4 py-3">
                <div class="truncate font-mono text-[12.5px] text-ink-200" :title="q.fingerprint">{{ q.fingerprint }}</div>
                <div class="mt-1.5 flex items-center gap-2">
                  <div class="h-1 w-40 overflow-hidden rounded-full bg-ink-800"><div class="h-full bg-poke-500" :style="{ width: (q.seconds / maxQuery) * 100 + '%' }" /></div>
                  <span v-if="delta(q)" class="chip" :class="delta(q).better ? '!border-good/30 !text-good' : '!border-bad/30 !text-bad'"
                    :title="queryBadgeInWords(delta(q).better ? 'saved' : 'lost') + ' ' + t('Against {window}', { window: windowLabel })">{{ delta(q).text }}/{{ t('day') }}</span>
                  <span v-if="q.n_plus_one" class="chip !border-bad/30 !text-bad" :title="queryBadgeInWords('n_plus_one')">N+1</span>
                  <span v-if="q.explained" class="chip" :title="queryBadgeInWords('explained')">{{ t('we know why') }} ✓</span>
                  <span v-if="q.db" class="chip !py-0 !text-[10px]" :title="t('database')">⛁ {{ q.db }}</span>
                  <span v-if="q.excluded" class="chip" :title="t('Left out of the speed index, the debt and the missions.')">🙈 {{ t('left out') }}</span>
                  <span v-if="q.origin" class="hidden truncate font-mono text-[11px] text-ink-500 md:inline">{{ q.origin }}</span>
                  <button v-if="!q.excluded" class="chip hover:border-ink-500" :title="t('Leave it out of the score')" @click.stop="leaveOut('query', q.hash, q.fingerprint)">🙈</button>
                  <button class="chip hover:border-ink-500" :title="t('Make it a mission')" :disabled="opening === 'query ' + q.hash" @click.stop="makeMission('query', q.hash)">🎯</button>
                </div>
              </td>
              <td class="num whitespace-nowrap px-3 py-3 text-right font-medium">{{ duration(q.seconds) }}</td>
              <td class="num hidden px-3 py-3 text-right text-ink-300 md:table-cell">{{ compact(q.calls) }}</td>
              <td class="num hidden px-3 py-3 text-right text-ink-300 md:table-cell">{{ latency(q.avg) }}</td>
              <td class="num hidden px-4 py-3 text-right text-ink-400 lg:table-cell">{{ latency(q.max) }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Endpoints -->
      <div v-else-if="tab === 'endpoints'" class="card overflow-hidden">
        <Empty v-if="!data.endpoints.length" icon="⇄" :title="t('No problems found.')" class="m-4" />
        <table v-else class="w-full table-fixed text-sm">
          <thead>
            <tr class="text-left text-[11px] uppercase tracking-wider text-ink-500">
              <th class="px-4 py-3 font-medium"><button class="hover:text-ink-200" @click="sortBy('endpoint')">{{ t('Endpoints') }} {{ arrow('endpoint') }}</button></th>
              <th class="w-28 px-3 py-3 text-right font-medium" :title="t('Waiting over the slow line in the period, added up.')"><button class="hover:text-ink-200" @click="sortBy('slow_seconds')">{{ t('time lost') }} {{ arrow('slow_seconds') }}</button></th>
              <th class="hidden w-24 px-3 py-3 text-right font-medium md:table-cell"><button class="hover:text-ink-200" @click="sortBy('requests')">{{ t('requests') }} {{ arrow('requests') }}</button></th>
              <th class="hidden w-24 px-3 py-3 text-right font-medium md:table-cell"><button class="hover:text-ink-200" @click="sortBy('avg')">{{ t('average') }} {{ arrow('avg') }}</button></th>
              <th class="hidden w-24 px-3 py-3 text-right font-medium lg:table-cell" :title="t('Share of requests over the slow line.')"><button class="hover:text-ink-200" @click="sortBy('slow_ratio')">{{ t('slow') }} {{ arrow('slow_ratio') }}</button></th>
              <th class="hidden w-20 px-4 py-3 text-right font-medium lg:table-cell"><button class="hover:text-ink-200" @click="sortBy('e5')">{{ t('errors') }} {{ arrow('e5') }}</button></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="e in endpointRows" :key="e.source + e.endpoint" class="row-hover cursor-pointer" :class="e.excluded ? 'opacity-50' : ''"
              @click="$router.push({ path: `/projects/${project.slug}/endpoint`, query: { source: e.source, endpoint: e.endpoint } })">
              <td class="px-4 py-3">
                <div class="flex items-center gap-2">
                  <span class="chip font-mono">{{ e.endpoint.split(' ')[0] }}</span>
                  <span class="truncate font-mono text-[12.5px] text-ink-200">{{ e.endpoint.split(' ').slice(1).join(' ') }}</span>
                </div>
                <div class="mt-1.5 flex items-center gap-2">
                  <div class="h-1 w-40 overflow-hidden rounded-full bg-ink-800"><div class="h-full bg-info" :style="{ width: (e.slow_seconds / maxEndpoint) * 100 + '%' }" /></div>
                  <span v-if="delta(e)" class="chip" :class="delta(e).better ? '!border-good/30 !text-good' : '!border-bad/30 !text-bad'"
                    :title="queryBadgeInWords(delta(e).better ? 'saved' : 'lost') + ' ' + t('Against {window}', { window: windowLabel })">{{ delta(e).text }}/{{ t('day') }}</span>
                  <span v-if="e.excluded" class="chip" :title="t('Left out of the speed index, the debt and the missions.')">🙈 {{ t('left out') }}</span>
                  <span v-if="e.outbound_share >= 0.2" class="chip !border-warn/30 !text-warn" :title="t('Share of its time spent waiting on other services.')">⇢ {{ t('{share}% outside', { share: num(e.outbound_share * 100, 0) }) }}</span>
                  <span class="text-[11px] text-ink-500">{{ e.source }}</span>
                  <button v-if="!e.excluded" class="chip hover:border-ink-500" :title="t('Leave it out of the score')" @click.stop="leaveOut('endpoint', e.endpoint, e.endpoint)">🙈</button>
                  <button class="chip hover:border-ink-500" :title="t('Make it a mission')" :disabled="opening === 'endpoint ' + e.source + ' ' + e.endpoint" @click.stop="makeMission('endpoint', e.source + ' ' + e.endpoint)">🎯</button>
                </div>
              </td>
              <td class="num whitespace-nowrap px-3 py-3 text-right font-medium">{{ duration(e.slow_seconds) }}</td>
              <td class="num hidden px-3 py-3 text-right text-ink-300 md:table-cell">{{ compact(e.requests) }}</td>
              <td class="num hidden px-3 py-3 text-right text-ink-300 md:table-cell">{{ latency(e.avg) }}</td>
              <td class="num hidden px-3 py-3 text-right lg:table-cell" :class="e.slow_ratio > 5 ? 'text-warn' : 'text-ink-400'">{{ pct(e.slow_ratio) }}</td>
              <td class="num hidden px-4 py-3 text-right lg:table-cell" :class="e.e5 ? 'text-bad' : 'text-ink-500'">{{ num(e.e5) }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Hotspots -->
      <div v-else-if="tab === 'hotspots'" class="card overflow-hidden">
        <Empty v-if="!hotspots.data.value?.hotspots?.length" icon="🔥" :title="t('No hotspots yet')" :text="t('Code locations come from the package of your framework.')" class="m-4" />
        <div v-for="h in rows(hotspots.data.value?.hotspots, ['file'])" :key="h.file" class="row-hover mt-2 px-5 py-3.5">
          <div class="flex items-center justify-between gap-4">
            <div class="min-w-0">
              <div class="truncate font-mono text-[13px] text-ink-100">{{ h.file }}</div>
              <div class="mt-0.5 text-[11px] text-ink-500">{{ h.kind }} · {{ h.items }} · {{ t('lines') }} {{ h.lines?.join(', ') }}</div>
            </div>
            <div class="num shrink-0 font-medium text-poke-400">{{ duration(h.seconds_per_day) }}/{{ t('day') }}</div>
          </div>
          <div class="mt-2 h-1 overflow-hidden rounded-full bg-ink-800"><div class="h-full bg-gradient-to-r from-warn to-bad" :style="{ width: (h.seconds_per_day / maxHot) * 100 + '%' }" /></div>
        </div>
      </div>

      <!-- PHP -->
      <div v-else class="card overflow-hidden">
        <Empty v-if="!data.php.length" icon="⏳" :title="t('No problems found.')" class="m-4" />
        <div class="flex items-center justify-between px-5 pt-4 text-[11px] uppercase tracking-wider text-ink-500">
          <button class="hover:text-ink-200" @click="sortBy('frame')">{{ t('PHP waiting') }} {{ arrow('frame') }}</button>
          <button class="hover:text-ink-200" @click="sortBy('count')">× {{ arrow('count') }}</button>
        </div>
        <div v-for="p in phpRows" :key="p.frame + p.code" class="row-hover mt-2 px-5 py-3.5 first-of-type:mt-3" :class="p.excluded ? 'opacity-50' : ''">
          <div class="flex items-center justify-between gap-4">
            <div class="min-w-0">
              <div class="text-sm text-ink-100">{{ phpWait(p.frame).icon }} {{ phpWait(p.frame).words }}</div>
              <div v-if="p.code" class="mt-1 truncate font-mono text-[12.5px] text-poke-300">{{ p.code }}</div>
              <div class="mt-0.5 truncate font-mono text-[11px] text-ink-500" :title="p.frame">{{ p.frame }}</div>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <span v-if="p.excluded" class="chip" :title="t('Left out of the speed index, the debt and the missions.')">🙈 {{ t('left out') }}</span>
              <button v-else class="chip hover:border-ink-500" :title="t('Leave it out of the score')" @click.stop="leaveOut('php', p.frame, phpWait(p.frame).words)">🙈</button>
              <button class="chip hover:border-ink-500" :title="t('Make it a mission')" :disabled="opening === 'php ' + p.frame" @click.stop="makeMission('php', p.frame)">🎯</button>
              <div class="num font-medium">{{ num(p.count) }}×</div>
            </div>
          </div>
          <div class="mt-2 h-1 overflow-hidden rounded-full bg-ink-800"><div class="h-full bg-warn" :style="{ width: (p.count / maxPhp) * 100 + '%' }" /></div>
        </div>
      </div>
      <ExcludeDialog v-if="excluding" :slug="project.slug" :kind="excluding.kind" :target="excluding.target" :label="excluding.label"
        :suggested="excluding.kind === 'query' ? 'third_party' : 'external'" @cancel="excluding = null" @done="excluding = null; reload()" />
    </State>
  </div>
</template>
