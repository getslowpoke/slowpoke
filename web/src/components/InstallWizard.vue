<script setup>
import { computed, onBeforeUnmount, ref } from 'vue'
import { api } from '../lib/api.js'
import { agentYaml, passwordVariables } from '../lib/agentYaml.js'
import { t } from '../lib/i18n.js'
import { ago } from '../lib/format.js'
import CopyButton from './CopyButton.vue'

const props = defineProps({ project: Object })
const emit = defineEmits(['close'])

const step = ref(1)
const roles = ref(['web', 'php'])
const platform = ref('linux')
const hostName = ref('')
const token = ref('')
const tokenError = ref('')
async function prepareInstall() {
  step.value = 2
  if (token.value) return
  try {
    const res = await api.post(`/api/projects/${props.project.slug}/tokens`, { kind: 'agent', name: hostName.value || t('new machine') })
    token.value = res.secret
  } catch (e) {
    tokenError.value = e.message
  }
}
const known = ref(null)
const connected = ref(null)
let timer = null

const catalog = computed(() => [
  { group: t('Web servers'), items: [
    { key: 'web', icon: '🟩', title: 'Nginx', text: t('Slow requests, endpoints, 5xx') },
    { key: 'apache', icon: '🪶', title: 'Apache', text: t('Slow requests, endpoints, 5xx') },
    { key: 'caddy', icon: '🔒', title: 'Caddy', text: t('Slow requests, endpoints, 5xx') },
  ] },
  { group: t('Runtimes'), items: [
    { key: 'php', icon: '🐘', title: 'PHP-FPM', text: t('Where PHP waits (slowlog)') },
    { key: 'node', icon: '🟢', title: 'Node.js', text: t('Custom metrics via StatsD / Prometheus'), note: t('Package coming') },
    { key: 'python', icon: '🐍', title: 'Python', text: t('Custom metrics via StatsD / Prometheus'), note: t('Package coming') },
    { key: 'otherlang', icon: '🧩', title: 'Go · Ruby · Java · .NET', text: t('Custom metrics via StatsD / Prometheus'), note: t('Package coming') },
  ] },
  { group: t('Databases'), items: [
    { key: 'mysql', icon: '🐬', title: 'MySQL / MariaDB', text: t('Slow queries, EXPLAIN, index suggestions') },
    { key: 'postgres', icon: '🐘', title: 'PostgreSQL', text: 'pg_stat_statements, EXPLAIN' },
    { key: 'mongodb', icon: '🍃', title: 'MongoDB', text: t('Slow operations and collection scans') },
  ] },
  { group: t('Cache, search & queues'), items: [
    { key: 'redis', icon: '⚡', title: 'Redis', text: 'SLOWLOG, memory, hit rate' },
    { key: 'elasticsearch', icon: '🔎', title: 'Elasticsearch / OpenSearch', text: t('Search time, write queues, heap') },
    { key: 'rabbitmq', icon: '📬', title: 'RabbitMQ', text: t('Queue depth and consumers') },
  ] },
  { group: t('Custom'), items: [
    { key: 'statsd', icon: '∿', title: 'StatsD / SDK', text: t('Your custom metrics') },
    { key: 'prometheus', icon: '🔥', title: t('Prometheus endpoint'), text: 'Reverb, Soketi, Centrifugo, exporters' },
  ] },
])
const platforms = [
  { key: 'linux', icon: '🐧', title: 'Linux · systemd' },
  { key: 'docker', icon: '🐳', title: 'Docker' },
  { key: 'k8s', icon: '☸️', title: 'Kubernetes', soon: true },
]

function toggle(item) {
  if (item.soon) return
  const r = item.key
  roles.value = roles.value.includes(r) ? roles.value.filter((x) => x !== r) : [...roles.value, r]
}

const config = computed(() => agentYaml({
  server: location.origin,
  token: token.value,
  host: hostName.value,
  roles: roles.value,
}))

// The passwords the chosen sources need: the installer takes them, the configuration only names them.
const passwords = computed(() => passwordVariables(roles.value))

const nameArg = computed(() => (hostName.value ? ` --name ${hostName.value}` : ''))
const commands = computed(() => {
  if (platform.value === 'docker') {
    return `git clone https://github.com/getslowpoke/slowpoke.git && cd slowpoke
docker build --target agent -t slowpoke/agent .

sudo mkdir -p /etc/slowpoke
docker run --rm -v /:/host:ro -v /etc/slowpoke:/etc/slowpoke slowpoke/agent \\
  detect -root /host -server ${location.origin} -token ${token.value || '<token>'} -write /etc/slowpoke/agent.yaml

docker run -d --name slowpoke-agent --restart unless-stopped \\
  --pid host --network host \\
  -v /proc:/host/proc:ro -v /var/log:/var/log:ro -v /etc/slowpoke:/etc/slowpoke:ro \\
  -e SLOWPOKE_HOST_ROOT=/host -v slowpoke-agent:/var/lib/slowpoke \\
  slowpoke/agent`
  }
  const env = passwords.value.map((name) => ` \\\n  --env ${name}=…`).join('')
  return `curl -fsSL ${location.origin}/install.sh | sudo sh -s -- --token ${token.value || '<token>'}${nameArg.value}${env}`
})

const grants = computed(() => {
  if (roles.value.includes('mysql')) {
    return `CREATE USER 'slowpoke'@'127.0.0.1' IDENTIFIED BY '…';
GRANT PROCESS, SELECT ON *.* TO 'slowpoke'@'127.0.0.1';`
  }
  if (roles.value.includes('postgres')) {
    return `CREATE USER slowpoke WITH PASSWORD '…';
GRANT pg_read_all_stats TO slowpoke;
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;`
  }
  return ''
})

async function waitForAgent() {
  step.value = 3
  const first = await api.get(`/api/projects/${props.project.slug}/hosts`)
  known.value = new Map(first.hosts.map((h) => [h.id, h.last_seen_at]))
  timer = setInterval(async () => {
    const res = await api.get(`/api/projects/${props.project.slug}/hosts`)
    const fresh = res.hosts.find((h) => h.online && (!known.value.has(h.id) || h.last_seen_at > known.value.get(h.id) + 60))
    if (fresh) {
      connected.value = fresh
      clearInterval(timer)
    }
  }, 4000)
}
onBeforeUnmount(() => clearInterval(timer))


</script>

<template>
  <Teleport to="body">
  <div class="fixed inset-0 z-50 grid place-items-center overflow-y-auto bg-ink-950/80 p-4 backdrop-blur-sm" @click.self="emit('close')">
    <div class="card my-8 w-full max-w-3xl p-6 sm:p-8">
      <div class="flex items-start justify-between gap-4">
        <div>
          <div class="label">{{ t('Add agent') }} · {{ step }}/3</div>
          <h2 class="mt-1 text-2xl font-semibold tracking-tight">
            {{ [t('Which machine?'), t('One command'), t('Waiting for the machine')][step - 1] }}
          </h2>
        </div>
        <button class="btn-ghost !px-2 !py-1 text-xs" @click="emit('close')">✕</button>
      </div>
      <div class="mt-4 flex gap-1.5">
        <div v-for="i in 3" :key="i" class="h-1 flex-1 rounded-full transition" :class="i <= step ? 'bg-poke-500' : 'bg-ink-800'" />
      </div>

      <!-- 1 · the machine. Nothing to choose: the agent reads the machine and decides by itself. -->
      <div v-if="step === 1" class="mt-6">
        <p class="text-sm text-ink-300">{{ t('The agent looks at the machine and works out by itself what it can read: web server, PHP pools, databases, cache, applications. It changes nothing: whatever is still off is listed on the machine page, with the line that turns it on and what it costs.') }}</p>
        <div class="mt-5 grid gap-3 sm:grid-cols-3">
          <button v-for="p in platforms" :key="p.key" class="rounded-2xl border p-5 text-center transition"
            :class="p.soon ? 'cursor-not-allowed border-ink-800 bg-ink-900 opacity-55' : platform === p.key ? 'border-poke-500/60 bg-poke-500/10' : 'border-ink-700 bg-ink-850/50 hover:border-ink-600'"
            @click="p.soon || (platform = p.key)">
            <div class="text-3xl">{{ p.icon }}</div>
            <div class="mt-2 font-medium">{{ p.title }}</div>
            <div v-if="p.soon" class="mt-1 text-[10px] text-ink-400">{{ t('soon') }}</div>
          </button>
        </div>
        <div class="mt-5">
          <label class="label">{{ t('Machine name (optional)') }}</label>
          <input v-model="hostName" class="input mt-1 font-mono" placeholder="api-1" />
          <p class="mt-1 text-xs text-ink-500">{{ t('Empty: the machine uses its own hostname.') }}</p>
        </div>
        <p class="mt-4 text-xs text-ink-500">{{ t('CPU, memory, disk, network and the infrastructure map are always included.') }}</p>
        <div class="mt-6 flex justify-end"><button class="btn-primary" @click="prepareInstall">{{ t('Next') }} →</button></div>
      </div>

      <!-- 2 · one command, with the token of this machine inside it. -->
      <div v-else-if="step === 2" class="mt-6 space-y-5">
        <div class="rounded-xl border border-good/30 bg-good/10 p-3 text-sm text-good">
          🔑 {{ t('A dedicated token was created for this machine: you can revoke it at any time from Settings → Access. It is shown only now.') }}
        </div>
        <p v-if="tokenError" class="text-sm text-bad">{{ tokenError }}</p>

        <div>
          <div class="mb-1.5 flex items-center justify-between">
            <span class="label">{{ t('Run this on the machine, as root') }}</span><CopyButton :text="commands" />
          </div>
          <pre class="code overflow-auto whitespace-pre-wrap">{{ commands }}</pre>
          <p v-if="platform === 'linux'" class="mt-2 text-xs text-ink-500">{{ t('It installs the agent, writes its configuration from what it finds here, and starts it with systemd. Run it again to upgrade; add --uninstall to remove everything.') }}</p>
          <p v-else class="mt-2 text-xs text-ink-500">{{ t('Published images are not available yet: the image is built from source, then the agent reads the machine and writes its own configuration.') }}</p>
        </div>

        <details class="rounded-xl border border-ink-700 bg-ink-850/50 p-3">
          <summary class="cursor-pointer text-sm text-ink-300">{{ t('Prefer to write the configuration yourself?') }}</summary>
          <div class="mt-4 space-y-4">
            <div class="grid gap-2.5 sm:grid-cols-2">
              <button v-for="r in catalog.flatMap((c) => c.items)" :key="r.key" class="flex items-start gap-3 rounded-2xl border p-3 text-left transition"
                :class="r.soon ? 'cursor-not-allowed border-ink-800 bg-ink-900 opacity-55' : roles.includes(r.key) ? 'border-poke-500/60 bg-poke-500/10' : 'border-ink-700 bg-ink-850/50 hover:border-ink-600'" @click="toggle(r)">
                <span class="text-lg">{{ r.icon }}</span>
                <span class="min-w-0 flex-1">
                  <span class="flex flex-wrap items-center gap-1.5 text-sm font-medium">{{ r.title }}
                    <span v-if="r.soon" class="rounded-md bg-ink-800 px-1.5 py-px text-[10px] font-normal text-ink-400">{{ t('soon') }}</span>
                  </span>
                  <span class="mt-0.5 block text-xs text-ink-400">{{ r.text }}</span>
                </span>
              </button>
            </div>
            <div>
              <div class="mb-1.5 flex items-center justify-between"><span class="label">/etc/slowpoke/agent.yaml</span><CopyButton :text="config" /></div>
              <pre class="code max-h-64 overflow-auto">{{ config }}</pre>
            </div>
            <div v-if="grants">
              <div class="mb-1.5 flex items-center justify-between"><span class="label">{{ t('Read-only database user') }}</span><CopyButton :text="grants" /></div>
              <pre class="code overflow-auto">{{ grants }}</pre>
            </div>
          </div>
        </details>

        <div class="flex flex-wrap gap-2 text-xs text-ink-400">
          <span class="chip">🔒 {{ t('only outbound HTTPS') }}</span>
          <span class="chip">🙈 {{ t('query values never leave the machine') }}</span>
          <span class="chip">🪶 1 core · 64 MB</span>
        </div>
        <div class="flex justify-between"><button class="btn-ghost" @click="step = 1">← {{ t('Back') }}</button><button class="btn-primary" @click="waitForAgent">{{ t('I started it') }} →</button></div>
      </div>

      <!-- 3 · the machine calls in. -->
      <div v-else class="mt-8 text-center">
        <template v-if="!connected">
          <div class="relative mx-auto h-24 w-24">
            <span class="absolute inset-0 animate-ping rounded-full bg-poke-500/20" />
            <span class="relative grid h-24 w-24 place-items-center rounded-full border border-poke-500/40 bg-poke-500/10 text-4xl">📡</span>
          </div>
          <p class="mt-6 text-lg">{{ t('Listening for the agent…') }}</p>
          <p class="mt-1 text-sm text-ink-400">{{ t('It shows up within a minute from the start. If not, read the log on the machine: journalctl -u slowpoke-agent -f') }}</p>
        </template>
        <template v-else>
          <div class="mx-auto grid h-24 w-24 place-items-center rounded-full border border-good/40 bg-good/10 text-5xl">🎉</div>
          <p class="mt-6 text-2xl font-semibold">{{ connected.name }} {{ t('is connected') }}</p>
          <p class="mt-1 text-sm text-ink-400">{{ connected.sources?.join(' · ') }} · {{ ago(connected.last_seen_at) }}</p>
          <p class="mt-2 text-sm text-ink-400">{{ t('The machine page says what it reads, what it costs and what is still off.') }}</p>
        </template>
        <div class="mt-8 flex justify-center gap-2">
          <button v-if="!connected" class="btn-ghost" @click="step = 2">← {{ t('Back') }}</button>
          <button class="btn-primary" @click="emit('close')">{{ connected ? t('Go to the dashboard') : t('Close') }}</button>
        </div>
      </div>
    </div>
  </div>
  </Teleport>
</template>
