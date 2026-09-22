<script setup>
import { computed, ref } from 'vue'
import { api } from '../lib/api.js'
import { useLoad } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, latency, duration, dateTime, ago, day, clock } from '../lib/format.js'
import State from '../components/State.vue'
import LineChart from '../components/LineChart.vue'
import CopyButton from '../components/CopyButton.vue'
import Severity from '../components/Severity.vue'
import { heldInWords, liveTileInWords, measureKindInWords, missionStatusNames, missionStatusWhy } from '../lib/settingsWords.js'
import { steps, stageChip, toneClass, who } from '../lib/missionLife.js'
import { phpWait } from '../lib/phpWaits.js'
import { session } from '../lib/session.js'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import ExcludeDialog from '../components/ExcludeDialog.vue'
import DismissDialog from '../components/DismissDialog.vue'
import { excludeTarget } from '../lib/exclusions.js'

const props = defineProps({ project: Object, id: String })
const { data, error, loading, reload } = useLoad(() => api.get(`/api/projects/${props.project.slug}/missions/${props.id}`), { watchSources: [() => props.id] })

const c = computed(() => data.value?.context)
const m = computed(() => c.value?.mission)
const isBoss = computed(() => m.value?.type === 'boss')
const integration = ref('')
const assignee = ref('')
const creating = ref(false)
const taskError = ref('')
const assistantCommand = computed(() => `/slowpoke fix ${props.id}`)

const history = computed(() => {
  const h = c.value?.history || []
  const perDay = 24 / (m.value?.window_hours || 24)
  return {
    labels: h.map((w) => (m.value?.window_hours >= 24 ? day(new Date(w.from * 1000).toISOString().slice(0, 10)) : clock(w.from))),
    datasets: [
      { label: t('time lost'), data: h.map((w) => w.seconds * perDay), color: isBoss.value ? '#ff7a7a' : '#ff6f8e', type: 'bar' },
      { label: t('target'), data: h.map(() => c.value.target_seconds_per_day), color: '#7ee0a1', fill: false, dashed: true },
    ],
  }
})

async function setEffort(effort) {
  await api.patch(`/api/projects/${props.project.slug}/missions/${props.id}`, { effort })
  await reload()
}

async function createTask() {
  creating.value = true
  taskError.value = ''
  try {
    await api.post(`/api/projects/${props.project.slug}/missions/${props.id}/tasks`, { integration: integration.value, assignee: assignee.value })
    await reload()
  } catch (e) {
    taskError.value = e.message
  } finally {
    creating.value = false
  }
}
// Who is on it: taking a mission somebody else took is said, never done silently.
const life = computed(() => c.value?.life || {})
const lifeSteps = computed(() => (m.value ? steps(m.value, life.value) : []))
const chip = computed(() => (m.value ? stageChip({ ...m.value, ...life.value }) : null))
const mine = computed(() => {
  const email = (session.user?.email || '').toLowerCase()
  const person = (m.value?.person || '').toLowerCase()
  return !!email && (person === email || person === email.split('@')[0])
})
const takenByOther = computed(() => !!life.value.claimed_at && !!m.value?.person && !mine.value)
const busy = ref(false)
const lifeError = ref('')
const takeOver = ref(null) // { by, since } when somebody else has it
async function lifeAction(method, path, body) {
  busy.value = true
  lifeError.value = ''
  try {
    await api[method](`/api/projects/${props.project.slug}/missions/${props.id}/${path}`, body)
    takeOver.value = null
    await reload()
  } catch (e) {
    if (e.status === 409 && path === 'claim' && !body?.force) takeOver.value = { by: m.value.person }
    lifeError.value = e.message
  } finally {
    busy.value = false
  }
}
// Nobody will fix it: out of the score for good, or, for a one-off spike, quiet for a while.
const excluding = ref(null)
const dismissing = ref(null)
const leaveOut = () => (excluding.value = { kind: m.value.kind, target: excludeTarget(m.value.kind, m.value.target), label: m.value.label })
const claim = (force = false) => lifeAction('post', 'claim', force ? { force: true } : {})
const release = () => lifeAction('del', 'claim')
const shipped = () => lifeAction('post', 'shipped')
const providerIcon = { github: '🐙', gitlab: '🦊', linear: '◭', jira: '🔷', asana: '🌸', trello: '📋', clickup: '✔️' }
const statusStyle = { open: 'text-ink-200 border-ink-600', completed: 'text-good border-good/40', defeated: 'text-good border-good/40', escaped: 'text-bad border-bad/40', expired: 'text-ink-400 border-ink-700', excluded: 'text-ink-400 border-ink-700' }
</script>

<template>
  <State :loading="loading" :error="error" @retry="reload">
    <router-link :to="`/projects/${project.slug}/missions`" class="text-sm text-ink-400 hover:text-ink-200">← {{ t('Missions') }}</router-link>

    <div class="mt-3 flex flex-wrap items-start justify-between gap-4">
      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-2">
          <span class="chip" :class="isBoss ? '!border-bad/40 !text-bad' : ''">{{ isBoss ? '👾 ' + t('Regression') : '🧹 ' + t('Debt mission') }} · #{{ m.id }}</span>
          <span class="chip" :class="statusStyle[m.status]" :title="t(missionStatusWhy[m.status] || '')">{{ t(missionStatusNames[m.status] || m.status) }}</span>
          <span v-if="chip" class="chip" :class="toneClass[chip.tone]">{{ chip.icon }} {{ chip.text }}</span>
          <span v-else-if="m.person" class="chip">@{{ who(m.person) }}</span>
        </div>
        <h1 v-if="m.kind === 'php'" class="mt-2 text-xl text-ink-100 sm:text-2xl">{{ phpWait(m.label).icon }} {{ phpWait(m.label).words }}</h1>
        <h1 class="break-words font-mono text-ink-100" :class="m.kind === 'php' ? 'mt-1 text-sm text-ink-400' : 'mt-2 text-xl sm:text-2xl'">{{ m.label }}</h1>
        <p class="mt-1 text-sm text-ink-400">{{ t('Opened') }} {{ ago(m.opened_at) }}<template v-if="m.due_at && m.status === 'open'"> · ⏱ {{ t('deadline') }} {{ dateTime(m.due_at) }}</template></p>
      </div>
      <div class="card flex items-center gap-3 px-4 py-2">
        <div class="num text-3xl font-semibold" :class="isBoss ? 'text-bad' : 'text-poke-400'">+{{ m.points }}</div>
        <div class="text-xs leading-tight text-ink-400">pts<br />{{ t('when done') }}</div>
      </div>
    </div>

    <div class="mt-5 grid grid-cols-2 gap-3 md:grid-cols-5">
      <div class="card p-4"><div class="label">{{ t('Cost now') }}</div><div class="num mt-1 text-xl font-semibold text-poke-400">{{ duration(c.cost_seconds_per_day) }}</div><div class="text-xs text-ink-500">{{ t('per day') }}</div></div>
      <div class="card p-4"><div class="label">{{ t('Target') }}</div><div class="num mt-1 text-xl font-semibold text-good">&lt; {{ duration(c.target_seconds_per_day) }}</div><div class="text-xs text-ink-500">{{ t('per day') }}</div></div>
      <div class="card p-4"><div class="label">{{ t('held so far') }}</div><div class="num mt-1 text-xl font-semibold">{{ heldInWords(m.good_windows, m.need_windows, m.window_hours) }}</div><div class="text-xs text-ink-500">{{ t('under the target') }}</div>
        <div v-if="life.current_rate != null && life.baseline_rate != null" class="mt-1 text-[11px] text-ink-500">{{ t('per request in busy hours: {now} (it started at {base})', { now: latency(life.current_rate), base: latency(life.baseline_rate) }) }}</div></div>
      <div class="card p-4">
        <div class="label">{{ t('Effort') }}</div>
        <div class="mt-1.5 flex gap-1">
          <button v-for="e in ['xs', 's', 'm', 'l']" :key="e" class="rounded-lg border px-2 py-0.5 text-xs uppercase transition"
            :class="c.effort === e ? (c.effort_auto ? 'border-poke-500/40 bg-poke-500/10 text-poke-300' : 'border-poke-500 bg-poke-500 text-ink-950') : 'border-ink-700 text-ink-400 hover:border-ink-600'"
            @click="setEffort(c.effort === e && !c.effort_auto ? '' : e)">{{ e }}</button>
        </div>
        <div class="mt-1.5 text-xs text-ink-500">{{ c.effort_auto ? t('guessed by Slowpoke') : t('set by the team') }}<template v-if="c.roi >= 1"> · {{ t('every hour of work removes {x} of waiting per day', { x: duration(c.roi) }) }}</template></div>
      </div>
      <div class="card p-4 md:col-span-1"><div class="label">{{ t('Tasks') }}</div><div class="num mt-1 text-xl font-semibold">{{ c.tasks.length }}</div><div class="text-xs text-ink-500">{{ c.tasks.length ? t('linked') : t('none yet') }}</div></div>
    </div>

    <div class="mt-4 grid gap-4 xl:grid-cols-[1.4fr_1fr]">
      <div class="space-y-4">
        <section class="card p-5">
          <div class="label">{{ m.window_hours >= 24 ? t('Cost per day') : t('Cost per {n} hours', { n: m.window_hours }) }}</div>
          <LineChart class="mt-3" :labels="history.labels" :datasets="history.datasets" :height="220" :format-y="duration" legend />
        </section>

        <section v-if="c.query" class="card p-5">
          <div class="flex items-center justify-between gap-3">
            <div class="label">{{ t('Diagnosis') }}</div>
            <router-link :to="`/projects/${project.slug}/queries/${c.query.hash}`" class="text-xs text-ink-400 hover:text-poke-300">{{ t('Query doctor') }} →</router-link>
          </div>
          <pre class="code mt-3 whitespace-pre-wrap break-words">{{ c.query.sample || c.query.fingerprint }}</pre>
          <div class="mt-3 flex flex-wrap gap-2 text-xs">
            <span class="chip">{{ num(c.query.stats.calls_per_day) }} {{ t('calls') }}/{{ t('day') }}</span>
            <span class="chip">{{ latency(c.query.stats.avg_seconds) }} {{ t('average') }}</span>
            <span v-if="c.query.origin" class="chip font-mono !text-poke-300">{{ c.query.origin }}</span>
            <span v-if="c.query.n_plus_one" class="chip !border-bad/30 !text-bad">N+1</span>
          </div>
          <div class="mt-4 space-y-3">
            <div v-for="d in c.query.diagnoses" :key="d.code" class="rounded-xl border border-ink-800 bg-ink-850/50 p-4">
              <div class="flex flex-wrap items-center gap-2"><Severity :level="d.severity" /><span class="font-medium">{{ d.title }}</span></div>
              <p class="mt-2 text-sm text-ink-300">{{ d.detail }}</p>
              <pre v-if="/^(ALTER|CREATE)/.test(d.fix)" class="code mt-3 whitespace-pre-wrap break-words !text-good/90">{{ d.fix }}</pre>
              <p v-else-if="d.fix" class="mt-2 text-sm text-ink-200">→ {{ d.fix }}</p>
            </div>
          </div>
        </section>

        <section v-if="c.endpoint" class="card p-5">
          <div class="flex items-center justify-between">
            <div class="label">{{ t('Endpoint') }}</div>
            <router-link :to="{ path: `/projects/${project.slug}/endpoint`, query: { source: c.endpoint.source, endpoint: c.endpoint.endpoint } }" class="text-xs text-ink-400 hover:text-poke-300">{{ t('Details') }} →</router-link>
          </div>
          <div class="mt-2 font-mono text-ink-100">{{ c.endpoint.endpoint }} <span class="text-xs text-ink-500">{{ c.endpoint.source }}</span></div>
          <div class="mt-2 flex flex-wrap gap-2 text-xs">
            <span class="chip">{{ num(c.endpoint.requests) }} {{ t('requests') }} · {{ t('in 7 days') }}</span>
            <span class="chip">{{ latency(c.endpoint.avg) }} {{ t('average') }}</span>
            <span class="chip">max {{ latency(c.endpoint.max) }}</span>
            <span v-if="c.endpoint.e5" class="chip !text-bad">{{ num(c.endpoint.e5) }} 5xx</span>
          </div>
          <div v-if="c.endpoint.queries.length" class="mt-4">
            <div class="label">{{ t('Queries it runs') }}</div>
            <router-link v-for="q in c.endpoint.queries" :key="q.hash + q.route" :to="`/projects/${project.slug}/queries/${q.hash}`" class="row-hover mt-2 flex items-center justify-between gap-3 rounded-lg px-3 py-2">
              <div class="min-w-0">
                <div class="truncate font-mono text-xs text-ink-200">{{ q.fingerprint }}</div>
                <div v-if="q.origin" class="truncate font-mono text-[11px] text-poke-300">{{ q.origin }}</div>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <span v-if="q.n_plus_one" class="chip !border-bad/30 !text-bad">N+1</span>
                <span class="num text-xs" :class="q.per_request > 5 ? 'text-bad' : 'text-ink-400'">{{ num(q.per_request, 1) }}× {{ t('per request') }}</span>
              </div>
            </router-link>
          </div>
          <p v-else class="mt-3 text-xs text-ink-500">{{ t('Install the package of your framework to see which queries this endpoint runs and from which line of code.') }}</p>
        </section>

        <section v-if="c.metric" class="card p-5">
          <div class="flex items-center justify-between">
            <div class="label">{{ t('Your own measure') }}</div>
            <router-link :to="`/projects/${project.slug}/metrics`" class="text-xs text-ink-400 hover:text-poke-300">{{ t('Metrics') }} →</router-link>
          </div>
          <div class="mt-2 font-mono text-ink-100">{{ c.metric.name }} <span class="text-xs text-ink-500">{{ measureKindInWords(c.metric.kind) }}</span></div>
          <div class="mt-3 grid grid-cols-3 gap-2 text-sm">
            <div class="rounded-xl bg-ink-850/50 p-3" :title="liveTileInWords('p95')"><div class="text-xs text-ink-500">{{ t('worst case') }}</div><div class="num text-poke-400">{{ latency(c.metric.p95) }}</div></div>
            <div class="rounded-xl bg-ink-850/50 p-3"><div class="text-xs text-ink-500">{{ t('average') }}</div><div class="num">{{ latency(c.metric.avg) }}</div></div>
            <div class="rounded-xl bg-ink-850/50 p-3"><div class="text-xs text-ink-500">{{ t('in 7 days') }}</div><div class="num">{{ num(c.metric.count) }}</div></div>
          </div>
        </section>

        <section v-if="c.stalls?.length" class="card p-5">
          <div class="label">{{ t('PHP waiting') }}</div>
          <div v-for="s in c.stalls" :key="s.code" class="mt-3 text-sm">
            <div class="text-ink-100">{{ phpWait(s.frame).icon }} {{ phpWait(s.frame).words }}</div>
            <div class="font-mono text-xs text-poke-300">{{ s.code }} · {{ num(s.count) }}×</div>
            <div class="truncate font-mono text-[11px] text-ink-500">{{ s.frame }}</div>
          </div>
        </section>
      </div>

      <div class="space-y-4">
        <section class="card p-5">
          <div class="flex items-center justify-between gap-3">
            <div class="label">{{ t('Who is on it') }}</div>
            <span v-if="chip" class="chip" :class="toneClass[chip.tone]">{{ chip.icon }} {{ chip.text }}</span>
          </div>
          <ol class="mt-3 space-y-2">
            <li v-for="s in lifeSteps" :key="s.key" class="flex items-start gap-3 text-sm" :class="s.done ? (s.bad ? 'text-bad' : 'text-ink-100') : 'text-ink-500'">
              <span class="mt-0.5 w-5 shrink-0 text-center" :class="s.done ? '' : 'opacity-40 grayscale'">{{ s.icon }}</span>
              <span class="min-w-0 flex-1">
                {{ s.label }}
                <a v-if="s.link" :href="s.link" target="_blank" rel="noopener" class="ml-1 text-poke-300 hover:text-poke-400">↗</a>
                <router-link v-if="s.boss" :to="`/projects/${project.slug}/missions/${s.boss}`" class="ml-1 text-poke-300 hover:text-poke-400">#{{ s.boss }} →</router-link>
                <span v-if="s.whenText" class="block text-[11px] text-ink-500">{{ s.whenText }}</span>
              </span>
            </li>
          </ol>
          <div v-if="m.status === 'open'" class="mt-4 flex flex-wrap gap-2">
            <button v-if="!life.claimed_at" class="btn-primary" :disabled="busy" @click="claim()">🙋 {{ t('I will take it') }}</button>
            <template v-else-if="mine">
              <button v-if="!life.shipped_at" class="btn-primary" :disabled="busy" @click="shipped">🚀 {{ t('It is online') }}</button>
              <button class="btn-ghost" :disabled="busy" @click="release">{{ t('Let it go') }}</button>
            </template>
            <button v-else class="btn-ghost" :disabled="busy" @click="takeOver = { by: m.person }">{{ t('Take it over') }}</button>
            <button v-if="m.kind !== 'metric'" class="btn-ghost ml-auto !py-1 text-xs" :title="t('It is slow by design and always will be: stop counting it')" @click="leaveOut">
              🙈 {{ t('Leave it out') }}
            </button>
            <button v-if="isBoss" class="btn-ghost !py-1 text-xs" :title="t('A one-off spike: close it and keep it quiet for a while')" @click="dismissing = m">
              🤫 {{ t('Wave it away') }}
            </button>
          </div>
          <p v-if="m.status === 'open' && mine && !life.shipped_at" class="mt-2 text-xs text-ink-500">{{ t('A deploy of yours after taking it marks it online by itself, when your deploys reach Slowpoke.') }}</p>
          <p v-if="takenByOther && m.status === 'open'" class="mt-2 text-xs text-ink-400">{{ t('{who} is on it: ask before starting, so the same fix is not written twice.', { who: who(m.person) }) }}</p>
          <p v-if="lifeError && !takeOver" class="mt-2 text-sm text-bad" role="alert">{{ lifeError }}</p>
          <ExcludeDialog v-if="excluding" :slug="project.slug" :kind="excluding.kind" :target="excluding.target" :label="excluding.label"
            :suggested="excluding.kind === 'query' ? 'third_party' : 'external'" @cancel="excluding = null" @done="excluding = null; reload()" />
          <DismissDialog v-if="dismissing" :slug="project.slug" :mission="dismissing" @cancel="dismissing = null" @done="dismissing = null; reload()" />
          <ConfirmDialog v-if="takeOver" :title="t('Take over this mission?')" :danger="false" :busy="busy" :confirm-label="t('Take it over')" @cancel="takeOver = null" @confirm="claim(true)">
            {{ t('{who} took it {when}. Taking it over moves it to you: tell them, so the same fix is not written twice.', { who: who(takeOver.by), when: life.claimed_at ? dateTime(life.claimed_at) : '' }) }}
          </ConfirmDialog>
        </section>

        <section class="card relative overflow-hidden p-5">
          <div class="pointer-events-none absolute -right-16 -top-16 h-40 w-40 rounded-full bg-poke-500/15 blur-3xl" />
          <div class="label">🤖 {{ t('Fix it with your coding assistant') }}</div>
          <p class="mt-2 text-sm text-ink-300">{{ t('Claude Code, Cursor, Codex or Copilot read this mission through Slowpoke MCP, write a failing test, fix the code and open a pull request. Tokens are yours: Slowpoke runs no AI.') }}</p>
          <div class="mt-3 flex items-center gap-2">
            <code class="code flex-1 !py-2">{{ assistantCommand }}</code>
            <CopyButton :text="assistantCommand" />
          </div>
          <div class="mt-3 flex flex-wrap gap-2">
            <CopyButton :text="data.markdown" :label="t('Copy brief (Markdown)')" />
          </div>
        </section>

        <section class="card p-5">
          <div class="label">{{ t('Tasks') }}</div>
          <div v-if="c.tasks.length" class="mt-3 space-y-2">
            <a v-for="tk in c.tasks" :key="tk.id" :href="tk.url" target="_blank" rel="noopener" class="row-hover flex items-center justify-between gap-3 rounded-xl border border-ink-800 px-3 py-2 text-sm">
              <span class="flex min-w-0 items-center gap-2"><span>{{ providerIcon[tk.provider] }}</span><span class="truncate">{{ tk.external_id }}</span><span v-if="tk.assignee" class="chip">@{{ tk.assignee }}</span></span>
              <span class="shrink-0 text-xs text-ink-500">{{ ago(tk.created_at) }} ↗</span>
            </a>
          </div>
          <div v-if="data.integrations.length" class="mt-4 space-y-2">
            <select v-model="integration" class="input">
              <option value="" disabled>{{ t('Choose a tracker') }}</option>
              <option v-for="i in data.integrations" :key="i.id" :value="i.id">{{ providerIcon[i.provider] }} {{ i.name || i.id }} · {{ i.provider }}</option>
            </select>
            <input v-model="assignee" class="input" :placeholder="t('Assignee (optional: login or user id)')" />
            <button class="btn-primary w-full" :disabled="!integration || creating" @click="createTask">{{ creating ? '…' : t('Create task') }}</button>
            <p v-if="taskError" class="text-sm text-bad">{{ taskError }}</p>
          </div>
          <div v-else class="mt-3 text-sm text-ink-400">
            {{ t('Connect GitHub, GitLab, Linear, Jira, Asana, Trello or ClickUp to create a task with the full brief.') }}
            <router-link :to="`/projects/${project.slug}/settings?tab=integrations`" class="mt-3 block text-poke-300 hover:text-poke-400">{{ t('Settings') }} → {{ t('Integrations') }}</router-link>
          </div>
        </section>

        <section v-if="c.deploy" class="card p-5">
          <div class="label">{{ t('Caused by deploy') }}</div>
          <div class="mt-2 font-mono">{{ c.deploy.version || c.deploy.commit.slice(0, 7) }} <span v-if="c.deploy.author" class="chip">@{{ c.deploy.author }}</span></div>
          <p class="mt-1 text-sm text-ink-400">{{ c.deploy.message }} · {{ dateTime(c.deploy.deployed_at) }}</p>
        </section>
      </div>
    </div>
  </State>
</template>
