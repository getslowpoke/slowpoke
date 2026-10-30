<script setup>
import { computed, ref } from 'vue'
import { api } from '../lib/api.js'
import { useGet } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, pct, bytes, clock, day, ago } from '../lib/format.js'
import State from '../components/State.vue'
import LineChart from '../components/LineChart.vue'
import WindowPicker from '../components/WindowPicker.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import IntegrationsPanel from '../components/IntegrationsPanel.vue'
import MuteDialog from '../components/MuteDialog.vue'
import { useRouter } from 'vue-router'

const props = defineProps({ project: Object, id: String })
const windowSel = ref('24h')
const { data, error, loading, reload } = useGet(() => `/api/projects/${props.project.slug}/hosts/${props.id}?window=${windowSel.value}`, { watchSources: [windowSel] })

const labels = computed(() => (data.value?.series || []).map((p) => (windowSel.value === '1h' || windowSel.value === '24h' ? clock(p.t) : `${day(new Date(p.t * 1000).toISOString().slice(0, 10))} ${clock(p.t)}`)))
const s = (k) => (data.value?.series || []).map((p) => p[k])
const bps = (v) => bytes(v) + '/s'
const router = useRouter()
const confirmRemove = ref(false)
const busy = ref(false)
// Muted: the machine keeps counting, the panel and the assistant stop asking about it.
const muting = ref(false)
async function unmute() {
  busy.value = true
  try {
    await api.del(`/api/projects/${props.project.slug}/hosts/${props.id}/mute`)
    await reload()
  } finally {
    busy.value = false
  }
}
async function remove() {
  busy.value = true
  await api.del(`/api/projects/${props.project.slug}/hosts/${props.id}`)
  router.push(`/projects/${props.project.slug}/hosts`)
}
</script>

<template>
  <State :loading="loading" :error="error" @retry="reload">
    <router-link :to="`/projects/${project.slug}/hosts`" class="text-sm text-ink-400 hover:text-ink-200">← {{ t('Machines') }}</router-link>
    <div class="mt-3 flex flex-wrap items-center justify-between gap-3">
      <div class="flex items-center gap-3">
        <span class="h-2.5 w-2.5 rounded-full" :class="data.host.online ? 'bg-good shadow-[0_0_12px_var(--color-good)]' : 'bg-ink-500'" />
        <h1 class="text-2xl font-semibold tracking-tight">{{ data.host.name }}</h1>
        <span class="chip">{{ data.host.os }} · {{ data.host.kind }} {{ data.host.version }}</span>
      </div>
      <div class="flex items-center gap-2">
        <WindowPicker v-model="windowSel" :options="['1h', '24h', '7d']" />
        <button v-if="!data.muted" class="btn-ghost" @click="muting = true">🔕 {{ t('Mute machine') }}</button>
        <button class="btn-ghost !border-bad/30 !text-bad" @click="confirmRemove = true">{{ t('Remove machine') }}</button>
      </div>
    </div>
    <div class="mt-2 flex flex-wrap gap-1">
      <span v-for="src in data.host.sources" :key="src" class="chip font-mono">{{ src }}</span>
      <span v-if="data.oom_kills" class="chip !border-bad/30 !text-bad">💥 {{ num(data.oom_kills) }} {{ t('Out of memory kills') }}</span>
    </div>

    <div v-if="data.muted" class="card mt-4 flex flex-wrap items-center justify-between gap-3 border-ink-700 p-4 text-sm">
      <div>
        <div class="text-ink-200">🔕 {{ t('Muted: its numbers still count, but nobody is asked to fix what it reports.') }}</div>
        <div class="mt-0.5 text-xs text-ink-400">
          <span v-if="data.muted.note">{{ data.muted.note }} · </span>{{ data.muted.by }} · {{ ago(data.muted.at) }}
        </div>
      </div>
      <button class="btn-ghost !py-1 text-xs" :disabled="busy" @click="unmute">{{ t('Turn it back on') }}</button>
    </div>
    <MuteDialog v-if="muting" :title="t('Mute this machine')" :subject="data.host.name"
      :explain="t('The agent stays installed and its numbers keep counting in the speed index and the debt. Its problems, an old agent and what it could still read stop asking for attention, and the coding assistant leaves it alone.')"
      :placeholder="t('e.g. staging, nobody will fix it')" :url="`/api/projects/${project.slug}/hosts/${id}/mute`"
      @cancel="muting = false" @done="muting = false; reload()" />

    <section class="card mt-4 p-5">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div>
          <div class="label">{{ t('Integrations') }}</div>
          <p class="mt-1 text-sm text-ink-400">{{ t('What Slowpoke sees on this machine. Optional parts can stay off: each one says what it adds and how to turn it on or off.') }}</p>
        </div>
        <span v-if="data.integrations?.total" class="chip">{{ data.integrations.complete }}/{{ data.integrations.total }} {{ t('complete') }}</span>
      </div>
      <IntegrationsPanel v-if="data.checks?.length" class="mt-4" :checks="data.checks" />
      <p v-else class="mt-3 text-sm text-ink-500">{{ t('This agent does not report its integrations yet: upgrade it to see them.') }}</p>
    </section>

    <div class="mt-4 grid gap-4 xl:grid-cols-2">
      <section class="card p-5">
        <div class="label">CPU · iowait · steal</div>
        <LineChart class="mt-3" :labels="labels" :height="200" :y-min="0" :y-max="100" :format-y="(v) => pct(v, 0)" legend
          :datasets="[{ label: 'CPU', data: s('cpu'), color: '#ff6f8e' }, { label: 'iowait', data: s('iowait'), color: '#f5c76a', fill: false }, { label: 'steal', data: s('steal'), color: '#ff7a7a', fill: false, dashed: true }]" />
      </section>
      <section class="card p-5">
        <div class="label">{{ t('Memory') }} · {{ t('Swap') }}</div>
        <LineChart class="mt-3" :labels="labels" :height="200" :y-min="0" :y-max="100" :format-y="(v) => pct(v, 0)" legend
          :datasets="[{ label: t('Memory'), data: s('mem_percent'), color: '#8ab4ff' }, { label: t('Swap'), data: s('swap_percent'), color: '#f5c76a', fill: false }]" />
      </section>
      <section class="card p-5">
        <div class="label">{{ t('Network') }}</div>
        <LineChart class="mt-3" :labels="labels" :height="200" :format-y="bps" legend
          :datasets="[{ label: 'rx', data: s('net_rx_bps'), color: '#7ee0a1' }, { label: 'tx', data: s('net_tx_bps'), color: '#8ab4ff', fill: false }]" />
      </section>
      <section class="card p-5">
        <div class="label">{{ t('Disk') }} I/O · {{ t('Load') }}</div>
        <LineChart class="mt-3" :labels="labels" :height="200" :format-y="bps" legend
          :datasets="[{ label: 'read', data: s('disk_read_bps'), color: '#b69cff' }, { label: 'write', data: s('disk_write_bps'), color: '#ff8fa6', fill: false }]" />
      </section>
    </div>

    <section class="card mt-4 p-5">
      <div class="label">{{ t('Top processes') }}</div>
      <div class="mt-3 space-y-2">
        <div v-for="p in data.processes" :key="p.name" class="grid grid-cols-[1fr_auto_auto] items-center gap-4 text-sm">
          <div class="min-w-0">
            <div class="truncate font-mono text-ink-200">{{ p.name }} <span v-if="p.n > 1" class="text-ink-500">×{{ p.n }}</span></div>
            <div class="mt-1 h-1 overflow-hidden rounded-full bg-ink-800"><div class="h-full rounded-full bg-info" :style="{ width: (p.mem_percent || 0) + '%' }" /></div>
          </div>
          <div class="num w-20 text-right text-ink-300">{{ bytes(p.rss) }}</div>
          <div class="num w-16 text-right" :class="p.cpu > 80 ? 'text-warn' : 'text-ink-400'">{{ pct(p.cpu, 0) }}</div>
        </div>
      </div>
    </section>
    <ConfirmDialog v-if="confirmRemove" :title="`${t('Remove machine')} ${data.host.name}?`" :busy="busy" :confirm-label="t('Remove')" @cancel="confirmRemove = false" @confirm="remove">
      <p>{{ t('Its measures (CPU, memory, connections) are deleted and its agent token is revoked. Project statistics, missions and points stay.') }}</p>
      <p class="mt-2 text-warn">{{ t('Stop the agent on the machine first:') }} <code class="font-mono">docker rm -f slowpoke-agent</code></p>
    </ConfirmDialog>
  </State>
</template>
