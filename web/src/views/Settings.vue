<script setup>
import { inject, ref, watch, computed } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { t } from '../lib/i18n.js'
import PageHeader from '../components/PageHeader.vue'
import CopyButton from '../components/CopyButton.vue'
import TimezoneSelect from '../components/TimezoneSelect.vue'
import { busyInWords, dayName } from '../lib/busyHours.js'
import ExclusionsPanel from '../components/ExclusionsPanel.vue'
import { ago } from '../lib/format.js'
import {
  applyPreset, bossesInWords, channelInWords, channelNames, componentInWords, currentPreset,
  deployWindowInWords, eventNames, gamePresets, integrationInWords, missionsInWords, providerNames,
  ruleInWords, slowRequestInWords,
} from '../lib/settingsWords.js'

const props = defineProps({ project: Object })
const reloadProject = inject('reloadProject')
const router = useRouter()

const form = ref(null)
const meta = ref(null)
const saving = ref(false)
const message = ref('')
const errorMsg = ref('')
const tab = ref(new URLSearchParams(location.search).get('tab') || 'general')
const testResult = ref({})

watch(() => props.project, (p) => {
  form.value = JSON.parse(JSON.stringify({ name: p.name, timezone: p.timezone, settings: p.settings }))
  const n = form.value.settings.notifications
  n.channels ||= []
  n.rules ||= []
  n.people ||= {}
  n.templates ||= {}
  form.value.settings.integrations ||= { tasks: [] }
  form.value.settings.debt ||= { weekly_goal_seconds: 600, capacity_percent: 20 }
  form.value.settings.integrations.tasks ||= []
}, { immediate: true })
api.get('/api/meta').then((m) => (meta.value = m))

// Updates. The page itself asks nobody: the button does, and only when pressed.
const updates = ref(null)
// Muted machines are left as they are on purpose: listed apart, never counted as behind.
const heardMachines = computed(() => (updates.value?.machines || []).filter((m) => !m.muted))
const mutedMachines = computed(() => (updates.value?.machines || []).filter((m) => m.muted).length)
const check = ref(null)
const checking = ref(false)
api.get('/api/updates').then((u) => (updates.value = u)).catch(() => {})
async function checkForUpdates() {
  checking.value = true
  check.value = null
  try {
    check.value = await api.post('/api/updates/check', {})
  } catch (e) {
    check.value = { error: e.message }
  } finally {
    checking.value = false
  }
}
const serverCommand = computed(() =>
  updates.value?.in_container
    ? 'docker compose pull && docker compose up -d'
    : './scripts/deploy ' + (location.hostname || '<server>'))

const s = computed(() => form.value.settings)
// Busy hours: learnt from the traffic unless the team sets them.
const busy = ref(null)
api.get(`/api/projects/${props.project.slug}/busy-hours`).then((b) => (busy.value = b)).catch(() => {})
const manualBusy = computed(() => (s.value?.traffic?.busy_days || []).length > 0)
function toggleManualBusy(on) {
  form.value.settings.traffic = on ? { busy_days: [1, 2, 3, 4, 5], busy_from: 9, busy_to: 19 } : { busy_days: [], busy_from: 0, busy_to: 0 }
}
function toggleBusyDay(d) {
  const days = s.value.traffic.busy_days
  s.value.traffic.busy_days = days.includes(d) ? days.filter((x) => x !== d) : [...days, d].sort()
}

// The knobs are there for whoever needs them, and out of the way of whoever does not.
const fineTuning = ref(false)
const preset = computed(() => currentPreset(s.value))
function choosePreset(key) {
  applyPreset(s.value, key)
}
const peopleList = ref([])
watch(() => form.value?.settings.notifications.people, (people) => {
  peopleList.value = Object.entries(people || {}).map(([name, v]) => ({ name, ...v }))
}, { immediate: true })

async function save() {
  saving.value = true
  message.value = ''
  errorMsg.value = ''
  const people = {}
  for (const p of peopleList.value) if (p.name) people[p.name] = { slack_user_id: p.slack_user_id || '', telegram_chat_id: p.telegram_chat_id || '' }
  s.value.notifications.people = people
  for (const c of s.value.score.components) for (const k of ['weight', 'good', 'bad']) c[k] = Number(c[k])
  for (const l of s.value.score.levels) l.min = Number(l.min)
  try {
    await api.patch(`/api/projects/${props.project.slug}`, form.value)
    await reloadProject()
    message.value = t('Saved')
    setTimeout(() => (message.value = ''), 2500)
  } catch (e) {
    errorMsg.value = e.message
  } finally {
    saving.value = false
  }
}

function addComponent() {
  s.value.score.components.push({ key: `custom_${s.value.score.components.length + 1}`, label: 'Invoice PDF time', metric: 'custom_p95', name: 'invoices.pdf', source: '', weight: 10, good: 1, bad: 8 })
}
function addChannel() {
  s.value.notifications.channels.push({ id: `channel-${s.value.notifications.channels.length + 1}`, name: '', type: 'slack', url: '', bot_token: '', chat_id: '' })
}
const accessTokens = ref([])
const newSecret = ref(null)
const personalName = ref('')
async function loadTokens() {
  accessTokens.value = (await api.get(`/api/projects/${props.project.slug}/tokens`)).tokens
}
watch(tab, (v) => v === 'access' && loadTokens(), { immediate: true })
async function createPersonal() {
  const res = await api.post(`/api/projects/${props.project.slug}/tokens`, { kind: 'personal', name: personalName.value || 'Coding assistant' })
  newSecret.value = res.secret
  personalName.value = ''
  loadTokens()
}
async function revokeToken(tk) {
  if (!confirm(`${t('Revoke')} ${tk.name}?`)) return
  await api.del(`/api/projects/${props.project.slug}/tokens/${tk.id}`)
  loadTokens()
}
const mcpConfig = computed(() => JSON.stringify({ mcpServers: { slowpoke: { type: 'http', url: `${location.origin}/mcp`, headers: { Authorization: `Bearer ${newSecret.value || 'spu_…'}` } } } }, null, 2))
const providerFields = {
  github: [['repo', 'owner/repo']],
  gitlab: [['repo', 'group/project'], ['base_url', 'https://gitlab.com']],
  linear: [['team_id', 'Team ID']],
  jira: [['base_url', 'https://acme.atlassian.net'], ['email', 'you@acme.io'], ['project_key', 'SHOP'], ['issue_type', 'Task']],
  asana: [['project_id', 'Project gid']],
  trello: [['list_id', 'List id']],
  clickup: [['list_id', 'List id']],
}
const tokenHint = { github: 'ghp_… (issues: write)', gitlab: 'glpat-…', linear: 'lin_api_…', jira: 'API token', asana: 'Personal access token', trello: 'key:token', clickup: 'pk_…' }
function addIntegration() {
  s.value.integrations.tasks.push({ id: `tracker-${s.value.integrations.tasks.length + 1}`, name: '', provider: 'github', token: '', repo: '', labels: [] })
}
function addRule() {
  s.value.notifications.rules.push({ events: ['mission_completed', 'boss'], channel: s.value.notifications.channels[0]?.id || '', direct_message: false })
}
function toggleEvent(rule, e) {
  const i = rule.events.indexOf(e)
  if (i >= 0) rule.events.splice(i, 1)
  else rule.events.push(e)
}
async function sendTest(id) {
  testResult.value = { ...testResult.value, [id]: '…' }
  try {
    await api.post(`/api/projects/${props.project.slug}/notifications/test`, { channel: id })
    testResult.value = { ...testResult.value, [id]: '✓' }
  } catch (e) {
    testResult.value = { ...testResult.value, [id]: e.message }
  }
}
async function rotate() {
  if (!confirm(t('Rotate token') + '?')) return
  await api.post(`/api/projects/${props.project.slug}/rotate-token`)
  await reloadProject()
}
async function remove() {
  if (prompt(`${t('Delete project')}: ${props.project.slug}`) !== props.project.slug) return
  await api.del(`/api/projects/${props.project.slug}`)
  router.push('/')
}

const tabs = computed(() => [
  ['general', t('General')], ['score', t('Speed index')], ['missions', t('Missions & bosses')], ['notifications', t('Notifications')], ['integrations', t('Integrations')], ['access', t('Access')],
])
const weekdays = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
const templateEvents = computed(() => meta.value?.notification_events || [])
</script>

<template>
  <div v-if="form">
    <PageHeader :title="t('Settings')">
      <span v-if="message" class="text-sm text-good">✓ {{ message }}</span>
      <button class="btn-primary" :disabled="saving" @click="save">{{ t('Save') }}</button>
    </PageHeader>
    <p v-if="errorMsg" class="mb-4 whitespace-pre-line rounded-xl border border-bad/30 bg-bad/10 p-3 text-sm text-bad">{{ errorMsg }}</p>

    <div class="mb-5 flex gap-1 overflow-x-auto border-b border-ink-800">
      <button v-for="[k, l] in tabs" :key="k" class="-mb-px whitespace-nowrap border-b-2 px-3 py-2 text-sm"
        :class="tab === k ? 'border-poke-500 text-ink-100' : 'border-transparent text-ink-400 hover:text-ink-200'" @click="tab = k">{{ l }}</button>
    </div>

    <!-- General -->
    <div v-if="tab === 'general'" class="grid gap-4 lg:grid-cols-2">
      <section v-if="updates" class="card card-pad lg:col-span-2">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <div class="label">⬆︎ {{ t('Updates') }}</div>
            <div class="mt-1 text-sm text-ink-300">{{ t('This server') }} <span class="num font-medium text-ink-100">{{ updates.version }}</span></div>
          </div>
          <button v-if="updates.can_check" class="btn-ghost text-xs" :disabled="checking" @click="checkForUpdates">
            {{ checking ? t('Checking…') : t('Check for a new version') }}
          </button>
        </div>

        <p v-if="updates.can_check && !check" class="mt-2 text-xs text-ink-500">
          {{ t('Nothing is sent about you or your machines: pressing it makes one request to {feed}.', { feed: updates.feed }) }}
        </p>
        <div v-if="check" class="mt-3 rounded-xl border p-3 text-sm"
          :class="check.error ? 'border-bad/30 bg-bad/10 text-bad' : check.newer ? 'border-warn/30 bg-warn/10' : 'border-good/30 bg-good/10 text-good'">
          <template v-if="check.error">{{ check.error }}</template>
          <template v-else-if="check.newer">
            <div class="font-medium text-warn">{{ t('Version {version} is out', { version: check.latest }) }}</div>
            <a v-if="check.url" :href="check.url" target="_blank" rel="noopener" class="text-xs text-ink-400 underline hover:text-poke-300">{{ t('What changed') }} →</a>
            <pre class="code mt-2 overflow-auto whitespace-pre-wrap">{{ serverCommand }}</pre>
          </template>
          <template v-else>{{ t('This is the latest version.') }}</template>
        </div>

        <div class="mt-4 border-t border-ink-800 pt-3">
          <div class="text-sm">
            <template v-if="updates.behind">
              <span class="text-warn">{{ t('{n} of {total} machines run an older agent', { n: updates.behind, total: heardMachines.length }) }}</span>
            </template>
            <template v-else-if="heardMachines.length">
              <span class="text-good">{{ t('All {total} machines run this same version', { total: heardMachines.length }) }}</span>
            </template>
            <template v-else-if="!mutedMachines">{{ t('No machine yet.') }}</template>
            <span v-if="mutedMachines" class="text-ink-500"> · 🔕 {{ t('{n} muted, left as they are', { n: mutedMachines }) }}</span>
          </div>
          <div v-if="updates.behind" class="mt-1 font-mono text-[11px] text-ink-500">
            {{ heardMachines.filter((m) => m.outdated).map((m) => `${m.name} (${m.version})`).join(' · ') }}
          </div>
          <p v-if="updates.behind" class="mt-2 text-xs text-ink-400">
            {{ updates.serves_agent
              ? t('Run the install command again on those machines, or set auto_update in agent.yaml and they will follow this server by themselves.')
              : t('This server does not carry the agent binaries: put them in SLOWPOKE_AGENT_DIR so the machines can update from here.') }}
          </p>
        </div>
      </section>
      <section class="card card-pad space-y-3">
        <div><label class="label">{{ t('Name') }}</label><input v-model="form.name" class="input mt-1" /></div>
        <div><label class="label">{{ t('Timezone') }}</label><TimezoneSelect v-model="form.timezone" class="mt-1" /></div>
        <div>
          <label class="label">{{ t('A request is slow from (seconds)') }}</label>
          <input v-model.number="s.http.slow_seconds" type="number" step="0.1" class="input mt-1" />
          <p class="mt-1 text-xs text-ink-500">{{ slowRequestInWords(s) }}</p>
        </div>
        <div>
          <label class="label">{{ t('Before and after a deploy (minutes)') }}</label>
          <input v-model.number="s.deploys.compare_minutes" type="number" class="input mt-1" />
          <p class="mt-1 text-xs text-ink-500">{{ deployWindowInWords(s) }}</p>
        </div>
        <label class="flex items-center gap-2 text-sm"><input v-model="s.people.leaderboard" type="checkbox" class="accent-poke-500" /> {{ t('Personal leaderboard') }}</label>
        <p class="text-xs text-ink-500">{{ t('Who closed which mission, on the overview. Off if your team would rather not have it.') }}</p>
      </section>
      <section class="card card-pad">
        <div class="label">Token</div>
        <p class="mt-1 text-sm text-ink-400">{{ t('The token the agents of this project use to send their measures.') }}</p>
        <div class="mt-2 flex items-center gap-2">
          <code class="code flex-1 truncate">{{ project.token }}</code>
          <CopyButton :text="project.token" />
        </div>
        <div class="mt-8 rounded-xl border border-bad/30 p-4">
          <div class="text-sm font-medium text-bad">{{ t('Danger zone') }}</div>
          <p class="mt-1 text-xs text-ink-400">{{ t('Rotating the token stops every agent of this project until each one gets the new one. Deleting takes the project and all its measures away, and there is no undo.') }}</p>
          <div class="mt-3 flex flex-wrap gap-2">
            <button class="btn-ghost" @click="rotate">{{ t('Rotate token') }}</button>
            <button class="btn-ghost !border-bad/40 !text-bad" @click="remove">{{ t('Delete project') }}</button>
          </div>
        </div>
      </section>
    </div>

    <!-- Score -->
    <div v-if="tab === 'score'" class="space-y-4">
      <section class="card card-pad">
        <div class="label">{{ t('What the index is made of') }}</div>
        <p class="mt-1 text-sm text-ink-400">
          {{ t('One number from 0 to 100, made of these measures. Each one is worth some of the points, and goes from full marks to zero between the two values on its line.') }}
        </p>
        <div class="mt-3 space-y-2">
          <div v-for="(c, i) in s.score.components" :key="i" class="rounded-xl bg-ink-850/50 p-3">
            <div class="flex flex-wrap items-center gap-2">
              <input v-model="c.label" class="input w-full sm:w-64 sm:shrink-0" :placeholder="t('Name')" />
              <span class="flex-1 text-sm text-ink-300">{{ componentInWords(c, s.score.components) }}</span>
              <button class="btn-ghost !px-2 text-xs" :title="t('Remove')" @click="s.score.components.splice(i, 1)">✕</button>
            </div>
            <div v-if="fineTuning" class="mt-3 grid grid-cols-2 gap-2 md:grid-cols-[1.4fr_1.2fr_90px_110px_110px]">
              <div>
                <label class="text-[11px] text-ink-500">{{ t('Measure') }}</label>
                <select v-model="c.metric" class="input mt-1">
                  <option v-for="(desc, key) in meta?.metrics" :key="key" :value="key">{{ desc }}</option>
                </select>
              </div>
              <div>
                <label class="text-[11px] text-ink-500">{{ c.metric?.startsWith('custom_') ? t('Metric name') : t('Source') }}</label>
                <input v-if="c.metric?.startsWith('custom_')" v-model="c.name" class="input mt-1 font-mono" placeholder="invoices.pdf" />
                <input v-else v-model="c.source" class="input mt-1 font-mono" :placeholder="t('All of them')" />
              </div>
              <div><label class="text-[11px] text-ink-500">{{ t('Points') }}</label><input v-model="c.weight" type="number" class="input mt-1" /></div>
              <div><label class="text-[11px] text-ink-500">{{ t('Full marks up to') }}</label><input v-model="c.good" type="number" step="any" class="input mt-1" /></div>
              <div><label class="text-[11px] text-ink-500">{{ t('Zero from') }}</label><input v-model="c.bad" type="number" step="any" class="input mt-1" /></div>
            </div>
          </div>
        </div>
        <div class="mt-3 flex items-center gap-3">
          <button class="btn-ghost !py-1 text-xs" @click="fineTuning = !fineTuning">
            {{ fineTuning ? t('Hide the numbers') : t('Fine tuning') }}
          </button>
          <button v-if="fineTuning" class="btn-ghost !py-1 text-xs" @click="addComponent">+ {{ t('Add') }}</button>
        </div>
      </section>
      <section class="card card-pad">
        <div class="label">{{ t('Levels') }}</div>
        <p class="mt-1 text-sm text-ink-400">{{ t('The badge the project wears, by its index. This is the part people screenshot.') }}</p>
        <div class="mt-3 grid gap-2 sm:grid-cols-2 lg:grid-cols-5">
          <div v-for="(l, i) in s.score.levels" :key="i" class="space-y-2 rounded-xl bg-ink-850/50 p-3">
            <div class="flex gap-2"><input v-model="l.icon" class="input w-14 text-center" /><input v-model="l.name" class="input" /></div>
            <div class="flex gap-2"><input v-model="l.min" type="number" class="input" /><input v-model="l.color" type="color" class="h-9 w-12 cursor-pointer rounded-lg border border-ink-700 bg-transparent" /></div>
          </div>
        </div>
      </section>
      <ExclusionsPanel :slug="project.slug" />
    </div>

    <!-- Missions -->
    <div v-if="tab === 'missions'" class="grid gap-4 lg:grid-cols-2">
      <section class="card card-pad lg:col-span-2">
        <div class="label">{{ t('How hard do you want to play?') }}</div>
        <div class="mt-3 grid gap-2 md:grid-cols-3">
          <button
            v-for="p in gamePresets" :key="p.key" type="button"
            class="rounded-xl border p-3 text-left transition"
            :class="preset === p.key ? 'border-poke-500/60 bg-poke-500/10' : 'border-ink-700 bg-ink-850/50 hover:border-ink-600'"
            @click="choosePreset(p.key)">
            <div class="font-medium">{{ t(p.title) }}</div>
            <div class="mt-1 text-xs text-ink-400">{{ t(p.summary) }}</div>
          </button>
        </div>
        <p class="mt-3 text-sm text-ink-300">{{ missionsInWords(s.missions) }}</p>
        <p class="mt-1 text-sm text-ink-300">{{ bossesInWords(s.bosses) }}</p>
        <p class="mt-1 text-xs text-ink-500">
          {{ t('At most {n} missions are open at a time, and something that costs less than {s} s of waiting per day is left alone.', { n: s.missions.max_open, s: s.missions.min_seconds }) }}
          <span v-if="preset === 'custom'"> · {{ t('These numbers were set by hand.') }}</span>
        </p>
      </section>

      <section class="card card-pad lg:col-span-2">
        <div class="label">🕘 {{ t('Busy hours') }}</div>
        <p class="mt-1 text-sm text-ink-300">{{ busyInWords(busy) }}</p>
        <p class="mt-1 text-xs text-ink-500">{{ t('Missions, bosses and deploys are judged per request on these hours: nights, weekends and holidays still count in the debt, but do not decide whether a fix worked.') }}</p>
        <label class="mt-3 flex items-center gap-2 text-sm text-ink-200">
          <input type="checkbox" class="accent-poke-500" :checked="manualBusy" @change="toggleManualBusy($event.target.checked)" /> {{ t('Set them by hand') }}
        </label>
        <div v-if="manualBusy" class="mt-3 flex flex-wrap items-end gap-3">
          <div class="flex flex-wrap gap-1">
            <button v-for="d in [1, 2, 3, 4, 5, 6, 0]" :key="d" type="button" class="rounded-lg border px-2.5 py-1 text-xs capitalize transition"
              :class="s.traffic.busy_days.includes(d) ? 'border-poke-500 bg-poke-500/15 text-poke-300' : 'border-ink-700 text-ink-400 hover:border-ink-600'"
              :aria-pressed="s.traffic.busy_days.includes(d)" @click="toggleBusyDay(d)">{{ dayName(d) }}</button>
          </div>
          <label class="text-xs text-ink-400">{{ t('from') }} <input v-model.number="s.traffic.busy_from" type="number" min="0" max="23" class="input mt-1 !w-20" /></label>
          <label class="text-xs text-ink-400">{{ t('to') }} <input v-model.number="s.traffic.busy_to" type="number" min="1" max="24" class="input mt-1 !w-20" /></label>
        </div>
      </section>

      <section class="card card-pad grid grid-cols-2 gap-3">
        <div class="col-span-2 label">🎯 {{ t('Weekly debt budget') }}</div>
        <div><label class="text-xs text-ink-400">{{ t('Seconds of waiting per day to repay each week') }}</label><input v-model.number="s.debt.weekly_goal_seconds" type="number" class="input mt-1" /></div>
        <div><label class="text-xs text-ink-400">{{ t('Share of each sprint for debt (%)') }}</label><input v-model.number="s.debt.capacity_percent" type="number" min="0" max="100" class="input mt-1" /></div>
        <p class="col-span-2 text-xs text-ink-500">{{ t('A standing 15-20% of every sprint beats occasional refactoring sprints.') }}</p>
      </section>

      <section class="card card-pad">
        <div class="flex items-center justify-between">
          <div class="label">{{ t('Fine tuning') }}</div>
          <button class="btn-ghost !py-1 text-xs" @click="fineTuning = !fineTuning">{{ fineTuning ? t('Hide') : t('Show') }}</button>
        </div>
        <p v-if="!fineTuning" class="mt-1 text-xs text-ink-500">{{ t('The numbers behind the three choices above. You should not need them.') }}</p>
        <div v-else class="mt-3 grid grid-cols-2 gap-3">
          <div><label class="text-xs text-ink-400">{{ t('Measured every (hours)') }}</label><input v-model.number="s.missions.window_hours" type="number" class="input mt-1" /></div>
          <div><label class="text-xs text-ink-400">{{ t('Good windows in a row') }}</label><input v-model.number="s.missions.good_windows" type="number" class="input mt-1" /></div>
          <div><label class="text-xs text-ink-400">{{ t('Cost left when it is won (0.5 = half)') }}</label><input v-model.number="s.missions.target_ratio" type="number" step="0.05" class="input mt-1" /></div>
          <div><label class="text-xs text-ink-400">{{ t('Ignore anything under (seconds per day)') }}</label><input v-model.number="s.missions.min_seconds" type="number" class="input mt-1" /></div>
          <div><label class="text-xs text-ink-400">{{ t('Missions open at a time') }}</label><input v-model.number="s.missions.max_open" type="number" class="input mt-1" /></div>
          <div class="col-span-2 mt-1 text-xs uppercase tracking-wide text-ink-500">👾 Boss</div>
          <div><label class="text-xs text-ink-400">{{ t('Times worse before a boss appears') }}</label><input v-model.number="s.bosses.growth" type="number" step="0.1" class="input mt-1" /></div>
          <div><label class="text-xs text-ink-400">{{ t('Ignore anything under (seconds per day)') }}</label><input v-model.number="s.bosses.min_seconds" type="number" class="input mt-1" /></div>
          <div><label class="text-xs text-ink-400">{{ t('Hours to beat it') }}</label><input v-model.number="s.bosses.deadline_hours" type="number" class="input mt-1" /></div>
        </div>
      </section>
    </div>

    <!-- Access -->
    <div v-if="tab === 'access'" class="space-y-4">
      <section class="card card-pad">
        <div class="label">🤖 {{ t('Coding assistant (MCP)') }}</div>
        <p class="mt-1 text-sm text-ink-400">{{ t('A personal token lets Claude Code, Cursor, Codex or Copilot read missions, claim them and link pull requests. Read-only on measures.') }}</p>
        <div class="mt-3 flex flex-wrap gap-2">
          <input v-model="personalName" class="input !w-64" :placeholder="t('e.g. Ada · Claude Code')" />
          <button class="btn-primary" @click="createPersonal">{{ t('Create personal token') }}</button>
        </div>
        <div v-if="newSecret" class="mt-4 space-y-2">
          <div class="rounded-xl border border-warn/30 bg-warn/10 p-3 text-sm text-warn">{{ t('Copy it now: it will not be shown again.') }}</div>
          <div class="flex items-center gap-2"><code class="code flex-1 truncate">{{ newSecret }}</code><CopyButton :text="newSecret" /></div>
          <div class="mb-1 mt-3 flex items-center justify-between"><span class="label">.mcp.json</span><CopyButton :text="mcpConfig" /></div>
          <pre class="code overflow-auto">{{ mcpConfig }}</pre>
        </div>
      </section>
      <section class="card overflow-hidden">
        <div class="label px-5 pt-5">{{ t('Tokens') }}</div>
        <div class="mt-3">
          <div v-for="tk in accessTokens" :key="tk.id" class="row-hover flex flex-wrap items-center justify-between gap-3 px-5 py-3 text-sm" :class="tk.revoked_at ? 'opacity-50' : ''">
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <span>{{ tk.kind === 'agent' ? '🖥️' : '🤖' }}</span>
                <span class="truncate font-medium">{{ tk.name }}</span>
                <code class="chip font-mono">{{ tk.prefix }}…</code>
              </div>
              <div class="mt-0.5 text-xs text-ink-500">
                {{ tk.kind === 'agent' ? t('agent') : t('personal') }} · {{ t('created') }} {{ ago(tk.created_at) }} ·
                {{ tk.last_used_at ? `${t('last used')} ${ago(tk.last_used_at)}` : t('never used') }}
                <template v-if="tk.revoked_at"> · {{ t('revoked') }}</template>
              </div>
            </div>
            <button v-if="!tk.revoked_at" class="btn-ghost !border-bad/30 !py-1 text-xs !text-bad" @click="revokeToken(tk)">{{ t('Revoke') }}</button>
          </div>
          <p v-if="!accessTokens.length" class="px-5 pb-5 text-sm text-ink-500">{{ t('Nothing yet.') }}</p>
        </div>
      </section>
    </div>

    <!-- Integrations -->
    <div v-if="tab === 'integrations'" class="space-y-4">
      <section class="card card-pad">
        <div class="flex items-center justify-between">
          <div>
            <div class="label">{{ t('Task trackers') }}</div>
            <p class="mt-1 text-sm text-ink-400">{{ t('Missions become issues with the full brief: cost, diagnosis, fix and link.') }}</p>
          </div>
          <button class="btn-ghost !py-1 text-xs" @click="addIntegration">+ {{ t('Add') }}</button>
        </div>
        <div class="mt-4 space-y-3">
          <div v-for="(it, i) in s.integrations.tasks" :key="i" class="rounded-xl bg-ink-850/50 p-3">
            <div class="grid gap-2 md:grid-cols-[140px_1fr_140px_auto]">
              <input v-model="it.id" class="input" :placeholder="t('Name')" />
              <input v-model="it.name" class="input" :placeholder="t('What to call it in the panel')" />
              <select v-model="it.provider" class="input">
                <option v-for="(name, pv) in providerNames" :key="pv" :value="pv">{{ name }}</option>
              </select>
              <button class="btn-ghost !px-2 text-xs" @click="s.integrations.tasks.splice(i, 1)">✕</button>
            </div>
            <div class="mt-2 grid gap-2 md:grid-cols-2">
              <input v-model="it.token" type="password" class="input font-mono" :placeholder="tokenHint[it.provider]" autocomplete="off" />
              <input v-for="[field, ph] in providerFields[it.provider]" :key="field" v-model="it[field]" class="input font-mono" :placeholder="ph" />
            </div>
            <p class="mt-2 text-xs text-ink-400">{{ integrationInWords(it) }}</p>
          </div>
          <p v-if="!s.integrations.tasks.length" class="text-sm text-ink-500">{{ t('Nothing yet.') }}</p>
        </div>
      </section>
    </div>

    <!-- Notifications -->
    <div v-if="tab === 'notifications'" class="space-y-4">
      <section class="card card-pad">
        <div class="flex items-center justify-between"><div class="label">{{ t('Channels') }}</div><button class="btn-ghost !py-1 text-xs" @click="addChannel">+ {{ t('Add') }}</button></div>
        <div class="mt-3 space-y-2">
          <div v-for="(c, i) in s.notifications.channels" :key="i" class="grid gap-2 rounded-xl bg-ink-850/50 p-3 md:grid-cols-[140px_150px_1fr_auto]">
            <input v-model="c.id" class="input" :placeholder="t('Name')" />
            <select v-model="c.type" class="input">
              <option v-for="(name, ty) in channelNames" :key="ty" :value="ty">{{ t(name) }}</option>
            </select>
            <div class="grid gap-2 sm:grid-cols-2">
              <input v-if="c.type !== 'telegram'" v-model="c.url" class="input font-mono" placeholder="https://hooks…" :class="c.type === 'slack' ? '' : 'sm:col-span-2'" />
              <input v-if="c.type === 'slack' || c.type === 'telegram'" v-model="c.bot_token" class="input font-mono" :placeholder="c.type === 'slack' ? 'xoxb-… (DM)' : 'bot token'" />
              <input v-if="c.type === 'telegram'" v-model="c.chat_id" class="input font-mono" placeholder="chat id" />
            </div>
            <div class="flex items-center gap-2">
              <button class="btn-ghost !py-1.5 text-xs" @click="sendTest(c.id)">{{ t('Send a test') }}</button>
              <button class="btn-ghost !px-2 text-xs" @click="s.notifications.channels.splice(i, 1)">✕</button>
            </div>
            <p class="text-xs text-ink-400 md:col-span-4">{{ channelInWords(c) }}</p>
            <div v-if="testResult[c.id]" class="text-xs md:col-span-4" :class="testResult[c.id] === '✓' ? 'text-good' : 'text-warn'">{{ testResult[c.id] }}</div>
          </div>
        </div>
        <p class="mt-2 text-xs text-ink-500">{{ t('Save') }} → {{ t('Send a test') }}</p>
      </section>

      <section class="card card-pad">
        <div class="flex items-center justify-between"><div class="label">{{ t('Rules') }}</div><button class="btn-ghost !py-1 text-xs" @click="addRule">+ {{ t('Add') }}</button></div>
        <div class="mt-3 space-y-2">
          <div v-for="(r, i) in s.notifications.rules" :key="i" class="rounded-xl bg-ink-850/50 p-3">
            <div class="flex flex-wrap items-center gap-2">
              <select v-model="r.channel" class="input !w-48"><option v-for="c in s.notifications.channels" :key="c.id" :value="c.id">{{ c.id }}</option></select>
              <label class="flex items-center gap-2 text-sm"><input v-model="r.direct_message" type="checkbox" class="accent-poke-500" /> {{ t('Direct message') }}</label>
              <button class="btn-ghost ml-auto !px-2 text-xs" @click="s.notifications.rules.splice(i, 1)">✕</button>
            </div>
            <p class="mt-2 text-sm text-ink-300">{{ ruleInWords(r, s.notifications.channels) }}</p>
            <div class="mt-2 flex flex-wrap gap-1.5">
              <button v-for="e in ['*', ...(meta?.notification_events || [])]" :key="e" class="chip cursor-pointer transition"
                :class="r.events.includes(e) ? '!border-poke-500/50 !bg-poke-500/15 !text-poke-300' : ''" @click="toggleEvent(r, e)">{{ t(eventNames[e] || e) }}</button>
            </div>
          </div>
        </div>
      </section>

      <div class="grid gap-4 lg:grid-cols-2">
        <section class="card card-pad">
          <div class="flex items-center justify-between"><div class="label">{{ t('People') }}</div><button class="btn-ghost !py-1 text-xs" @click="peopleList.push({ name: '', slack_user_id: '', telegram_chat_id: '' })">+ {{ t('Add') }}</button></div>
          <div class="mt-3 space-y-2">
            <div v-for="(p, i) in peopleList" :key="i" class="grid grid-cols-2 gap-2 sm:grid-cols-[1fr_1fr_1fr_auto]">
              <input v-model="p.name" class="input" placeholder="git author" />
              <input v-model="p.slack_user_id" class="input font-mono" placeholder="Slack U…" />
              <input v-model="p.telegram_chat_id" class="input font-mono" placeholder="Telegram" />
              <button class="btn-ghost !px-2 text-xs" @click="peopleList.splice(i, 1)">✕</button>
            </div>
          </div>
        </section>
        <section class="card card-pad space-y-3">
          <div class="label">{{ t('Quiet hours') }} · {{ t('Weekly digest') }}</div>
          <div class="flex flex-wrap items-center gap-2 text-sm">
            <input v-model="s.notifications.quiet_hours.enabled" type="checkbox" class="accent-poke-500" />
            <input v-model="s.notifications.quiet_hours.from" class="input !w-24 font-mono" /> → <input v-model="s.notifications.quiet_hours.to" class="input !w-24 font-mono" />
          </div>
          <div class="flex flex-wrap items-center gap-2 text-sm">
            <input v-model="s.notifications.weekly.enabled" type="checkbox" class="accent-poke-500" />
            <select v-model="s.notifications.weekly.channel" class="input !w-40"><option v-for="c in s.notifications.channels" :key="c.id" :value="c.id">{{ c.id }}</option></select>
            <select v-model.number="s.notifications.weekly.weekday" class="input !w-24"><option v-for="(d, i) in weekdays" :key="d" :value="i">{{ d }}</option></select>
            <input v-model.number="s.notifications.weekly.hour" type="number" min="0" max="23" class="input !w-20" />:00
          </div>
        </section>
      </div>

      <section class="card card-pad">
        <div class="label">{{ t('Templates') }}</div>
        <p class="mt-1 font-mono text-xs text-ink-500" v-pre>{{.Icon}} {{.Project}} {{.Title}} {{.Person}} {{.Mention}} {{.Points}} {{.Saved}} {{.URL}}</p>
        <div class="mt-3 grid gap-2 lg:grid-cols-2">
          <div v-for="e in templateEvents" :key="e">
            <label class="text-xs text-ink-400">{{ t(eventNames[e] || e) }}</label>
            <input v-model="s.notifications.templates[e]" class="input mt-1 font-mono text-xs" :placeholder="e === 'mission_completed' ? '🎉 {{.Person}} +{{.Points}} pts: {{.Title}}' : 'default'" />
          </div>
        </div>
      </section>
    </div>
  </div>
</template>
