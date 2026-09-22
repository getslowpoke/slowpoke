<script setup>
import { computed, ref } from 'vue'
import { api } from '../lib/api.js'
import { useLoad } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, pct, ago } from '../lib/format.js'
import State from '../components/State.vue'
import PageHeader from '../components/PageHeader.vue'
import CopyButton from '../components/CopyButton.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import InstallWizard from '../components/InstallWizard.vue'

const props = defineProps({ project: Object })
const { data, error, loading, reload } = useLoad(() => api.get(`/api/projects/${props.project.slug}/hosts`), { every: 20000 })
const removing = ref(null)
const busy = ref(false)
const wizard = ref(false)
async function remove() {
  busy.value = true
  try {
    await api.del(`/api/projects/${props.project.slug}/hosts/${removing.value.id}`)
    removing.value = null
    await reload()
  } finally {
    busy.value = false
  }
}
const method = ref('linux')
const origin = location.origin
// An agent older than the server it talks to: the panel says so, and how to fix it.
const serverVersion = ref('')
// The machines this project talks to and nobody measures: one agent sees its neighbours.
const dark = ref([])
api.text(`/api/projects/${props.project.slug}/fleet`)
  .then((t) => (dark.value = String(t || '').trim().split('\n').filter(Boolean).map((l) => l.split('\t'))))
  .catch(() => {})
api.get('/api/meta').then((m) => (serverVersion.value = m.version)).catch(() => {})
const outdated = computed(() => (data.value?.hosts || []).filter((h) => h.kind === 'agent' && h.version && serverVersion.value && h.version !== serverVersion.value))
const isOutdated = (h) => !h.muted && outdated.value.some((o) => o.id === h.id)
// Muted machines go last and stay quiet: their numbers count, their warnings do not.
const hosts = computed(() => [...(data.value?.hosts || [])].sort((a, b) => Number(!!a.muted) - Number(!!b.muted)))

// One command per machine: the agent looks at what runs there and writes its own configuration.
const install = computed(() => {
  const token = data.value?.token || 'spk_…'
  if (method.value === 'docker') {
    return `git clone https://github.com/getslowpoke/slowpoke.git && cd slowpoke
docker build --target agent -t slowpoke/agent .

sudo mkdir -p /etc/slowpoke
docker run --rm -v /:/host:ro -v /etc/slowpoke:/etc/slowpoke slowpoke/agent \\
  detect -root /host -server ${location.origin} -token ${token} -write /etc/slowpoke/agent.yaml

docker run -d --name slowpoke-agent --restart unless-stopped \\
  --pid host --network host \\
  -v /proc:/host/proc:ro -v /var/log:/var/log:ro -v /etc/slowpoke:/etc/slowpoke:ro \\
  -e SLOWPOKE_HOST_ROOT=/host -v slowpoke-agent:/var/lib/slowpoke \\
  slowpoke/agent`
  }
  return `curl -fsSL ${location.origin}/install.sh | sudo sh -s -- --token ${token}`
})
</script>

<template>
  <State :loading="loading" :error="error" @retry="reload">
    <PageHeader :title="t('Machines')" :subtitle="t('Every agent you install shows up here.')">
      <button class="btn-primary" @click="wizard = true">+ {{ t('Add agent') }}</button>
    </PageHeader>

    <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
      <router-link v-for="h in hosts" :key="h.id" :to="`/projects/${project.slug}/hosts/${h.id}`" class="card p-4 transition hover:-translate-y-0.5 hover:border-ink-600" :class="h.muted ? 'opacity-60 hover:opacity-100' : ''">
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="h-2 w-2 rounded-full" :class="h.online ? 'bg-good shadow-[0_0_10px_var(--color-good)]' : 'bg-ink-500'" />
              <span class="truncate font-medium">{{ h.name }}</span>
            </div>
            <div class="mt-1 text-xs text-ink-500">
              {{ h.kind }} {{ h.version }}<span v-if="isOutdated(h)" class="chip ml-1 !border-warn/30 !py-0 !text-[10px] !text-warn">{{ t('to update') }}</span> · {{ h.online ? t('online') : `${t('last seen')} ${ago(h.last_seen_at)}` }}</div>
          </div>
          <div class="flex shrink-0 items-center gap-1.5">
            <div v-if="h.cpus" class="chip">{{ h.cpus }} vCPU</div>
            <button class="grid h-7 w-7 place-items-center rounded-lg text-ink-500 hover:bg-bad/15 hover:text-bad" :title="t('Remove machine')" @click.prevent.stop="removing = h">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6" /></svg>
            </button>
          </div>
        </div>
        <div v-if="h.cpu !== undefined" class="mt-4 grid grid-cols-3 gap-2 text-xs">
          <div v-for="m in [{ l: 'CPU', v: h.cpu }, { l: t('Memory'), v: h.mem_percent }, { l: t('Disk'), v: h.disk_percent }]" :key="m.l">
            <div class="flex justify-between text-ink-400"><span>{{ m.l }}</span><span class="num text-ink-200">{{ pct(m.v, 0) }}</span></div>
            <div class="mt-1 h-1 overflow-hidden rounded-full bg-ink-800">
              <div class="h-full rounded-full" :style="{ width: (m.v || 0) + '%', background: m.v > 85 ? 'var(--color-bad)' : m.v > 65 ? 'var(--color-warn)' : 'var(--color-good)' }" />
            </div>
          </div>
        </div>
        <div class="mt-3 flex flex-wrap gap-1">
          <span v-for="s in h.sources" :key="s" class="chip font-mono">{{ s }}</span>
          <span v-if="h.muted" class="chip" :title="h.muted.note">🔕 {{ t('muted') }}</span>
          <template v-else>
          <span v-if="h.integrations?.problems" class="chip !border-bad/30 !text-bad">{{ h.integrations.problems }} {{ t(h.integrations.problems === 1 ? 'problem' : 'problems') }}</span>
          <span v-else-if="h.integrations?.total" class="chip" :class="h.integrations.complete === h.integrations.total ? '!border-good/30 !text-good' : '!border-warn/30 !text-warn'">{{ h.integrations.complete }}/{{ h.integrations.total }} {{ t('complete') }}</span>
          <span v-if="h.cpu_steal > 5" class="chip !border-warn/30 !text-warn">steal {{ pct(h.cpu_steal, 0) }}</span>
          <span v-if="h.swap_percent > 20" class="chip !border-warn/30 !text-warn">swap {{ pct(h.swap_percent, 0) }}</span>
          </template>
        </div>
      </router-link>
    </div>

    <section class="card mt-6 p-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <div class="text-lg font-medium">{{ t('Install the agent') }}</div>
          <p class="text-sm text-ink-400">HTTPS · 1 core · 64 MB · read only</p>
        </div>
        <div class="inline-flex rounded-xl border border-ink-700 bg-ink-900 p-0.5 text-xs">
          <button v-for="m in ['linux', 'docker']" :key="m" class="rounded-[10px] px-3 py-1"
            :class="method === m ? 'bg-ink-700 text-ink-100' : 'text-ink-400'" @click="method = m">{{ m }}</button>
        </div>
      </div>
      <div class="mt-4">
        <div class="mb-1 flex items-center justify-between">
          <span class="label">{{ t('Run this on the machine, as root') }}</span><CopyButton :text="install" />
        </div>
        <pre class="code overflow-auto whitespace-pre-wrap">{{ install }}</pre>
        <p class="mt-2 text-sm text-ink-400">{{ t('The agent looks at the machine and writes its own configuration. This command carries the project token: use Add agent for a token this machine only can use.') }}</p>
      </div>
    </section>
    <ConfirmDialog v-if="removing" :title="`${t('Remove machine')} ${removing.name}?`" :busy="busy" :confirm-label="t('Remove')" @cancel="removing = null" @confirm="remove">
      <p>{{ t('Its measures (CPU, memory, connections) are deleted and its agent token is revoked. Project statistics, missions and points stay.') }}</p>
      <p class="mt-2 text-warn">{{ t('Stop the agent on the machine first:') }} <code class="font-mono">docker rm -f slowpoke-agent</code> {{ t('or') }} <code class="font-mono">systemctl disable --now slowpoke-agent</code></p>
    </ConfirmDialog>
    <InstallWizard v-if="wizard" :project="project" @close="wizard = false; reload()" />
  </State>
</template>
