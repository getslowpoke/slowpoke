<script setup>
import { api } from '../lib/api.js'
import { useGet } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, latency, pct, dateTime, duration } from '../lib/format.js'
import State from '../components/State.vue'
import PageHeader from '../components/PageHeader.vue'
import Empty from '../components/Empty.vue'
import CopyButton from '../components/CopyButton.vue'
import { deliveryWords, verdictWords } from '../lib/settingsWords.js'

const props = defineProps({ project: Object })
const { data, error, loading, reload } = useGet(() => `/api/projects/${props.project.slug}/deploys`)
const verdict = {
  better: { icon: '✅', cls: 'text-good border-good/30 bg-good/10' },
  worse: { icon: '🐢', cls: 'text-bad border-bad/30 bg-bad/10' },
  neutral: { icon: '🚀', cls: 'text-ink-300 border-ink-700 bg-ink-850' },
  no_data: { icon: '…', cls: 'text-ink-400 border-ink-700 bg-ink-850' },
}
const curl = () => `curl -X POST ${location.origin}/api/v1/deploys \\
  -H "Authorization: Bearer $SLOWPOKE_TOKEN" \\
  -d '{"version":"'$GIT_TAG'","commit":"'$GIT_SHA'","author":"'$GIT_AUTHOR'"}'`
</script>

<template>
  <State :loading="loading" :error="error" @retry="reload">
    <PageHeader :title="t('Deploys')" :subtitle="t('Every deploy is judged on the {n} minutes after it, against the same {n} minutes of the days before (the {n} minutes before it while there is little history).', { n: data.compare_minutes })" />
    <section class="mb-5 grid grid-cols-2 gap-3 lg:grid-cols-5">
      <div class="card p-4">
        <div class="label" :title="t('How often you ship, how often shipping hurts, and how fast a regression is fixed.')">{{ t('Delivery health') }}</div>
        <div class="mt-1 text-xl font-semibold capitalize" :class="{ elite: 'text-good', high: 'text-good', medium: 'text-warn', low: 'text-bad' }[data.health.rating]"
          :title="t(deliveryWords[data.health.rating] || '')">{{ t(data.health.rating) }}</div>
        <div class="text-xs text-ink-500">{{ t('over the last {n} days', { n: data.health.days }) }}</div>
      </div>
      <div class="card p-4"><div class="label">{{ t('Deploys per week') }}</div><div class="num mt-1 text-xl font-semibold">{{ num(data.health.per_week, 1) }}</div></div>
      <div class="card p-4"><div class="label" :title="t('Out of 100 deploys, how many leave the app slower or open a regression.')">{{ t('Deploys that hurt') }}</div><div class="num mt-1 text-xl font-semibold" :class="data.health.change_failure_rate > 30 ? 'text-bad' : ''">{{ num(data.health.change_failure_rate, 0) }}%</div><div class="text-xs text-ink-500">{{ t('made it slower or caused a regression') }}</div></div>
      <div class="card p-4"><div class="label" :title="t('From the moment a regression appears to the moment it is beaten.')">{{ t('Time to fix a regression') }}</div><div class="num mt-1 text-xl font-semibold">{{ data.health.mean_hours_to_restore ? num(data.health.mean_hours_to_restore, 1) + 'h' : '—' }}</div></div>
      <div class="card p-4"><div class="label">{{ t('Open regressions') }}</div><div class="num mt-1 text-xl font-semibold" :class="data.health.open_regressions ? 'text-bad' : 'text-good'">{{ data.health.open_regressions }}</div></div>
    </section>
    <Empty v-if="!data.deploys.length" icon="⇪" :title="t('No deploys yet. Send them from CI:')">
      <div class="text-left">
        <pre class="code whitespace-pre-wrap">{{ curl() }}</pre>
        <div class="mt-2 text-right"><CopyButton :text="curl()" /></div>
      </div>
    </Empty>
    <div v-else class="relative space-y-3 before:absolute before:bottom-2 before:left-[19px] before:top-2 before:w-px before:bg-ink-800">
      <div v-for="d in data.deploys" :key="d.id" class="relative flex gap-4">
        <div class="z-10 grid h-10 w-10 shrink-0 place-items-center rounded-xl border text-base" :class="verdict[d.impact?.verdict || 'no_data'].cls">
          {{ verdict[d.impact?.verdict || 'no_data'].icon }}
        </div>
        <div class="card min-w-0 flex-1 p-4">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-mono font-medium">{{ d.version || d.commit?.slice(0, 7) }}</span>
              <span v-if="d.author" class="chip">@{{ d.author }}</span>
              <span v-if="d.impact" class="chip" :class="verdict[d.impact.verdict].cls" :title="t(verdictWords[d.impact.verdict] || '')">{{ t(d.impact.verdict === 'no_data' ? 'no data' : d.impact.verdict) }}
                <template v-if="d.impact.delta !== null"> {{ d.impact.delta > 0 ? '+' : '' }}{{ num(d.impact.delta, 1) }}</template>
              </span>
            </div>
            <span class="text-xs text-ink-500">{{ dateTime(d.deployed_at) }}<template v-if="d.impact?.compared"> · {{ d.impact.compared === 'same_hour' ? t('against the same hour of the days before') : t('against the hour before') }}</template></span>
          </div>
          <p v-if="d.message" class="mt-1 text-sm text-ink-300">{{ d.message }}</p>
          <div v-if="d.impact && d.impact.verdict !== 'no_data'" class="mt-3 grid grid-cols-2 gap-2 text-xs sm:grid-cols-4">
            <div v-for="m in [
              { k: 'score', label: t('Speed index'), f: (v) => num(v, 1), higher: true },
              { k: 'p95', label: t('response time'), f: latency },
              { k: 'slow_ratio', label: t('slow requests'), f: (v) => pct(v * 100, 2) },
              { k: 'db_slow_seconds', label: t('slow queries'), f: duration },
            ]" :key="m.k" class="rounded-lg bg-ink-850/60 px-3 py-2">
              <div class="text-ink-500">{{ m.label }}</div>
              <div class="num mt-0.5 text-ink-200">
                {{ m.f(d.impact.before[m.k]) }} → <span :class="(m.higher ? d.impact.after[m.k] >= d.impact.before[m.k] : d.impact.after[m.k] <= d.impact.before[m.k]) ? 'text-good' : 'text-bad'">{{ m.f(d.impact.after[m.k]) }}</span>
              </div>
            </div>
          </div>
          <div v-if="d.fixed?.length || d.caused?.length" class="mt-3 flex flex-wrap gap-2 text-xs">
            <router-link v-for="f in d.fixed" :key="'f' + f.id" :to="`/projects/${project.slug}/missions/${f.id}`" class="chip !border-good/30 !text-good hover:!border-good/60"
              :title="t('The cost of this went down right after this deploy.')">
              {{ f.status === 'open' ? '📉' : '✅' }} {{ t('Most likely fixed') }}: <span class="max-w-[22rem] truncate font-mono">{{ f.label }}</span>
            </router-link>
            <router-link v-for="c in d.caused" :key="'c' + c.id" :to="`/projects/${project.slug}/missions/${c.id}`" class="chip !border-bad/30 !text-bad hover:!border-bad/60"
              :title="t('This regression appeared after this deploy.')">
              👾 {{ t('Caused') }}: <span class="max-w-[22rem] truncate font-mono">{{ c.label }}</span>
            </router-link>
          </div>
        </div>
      </div>
    </div>
  </State>
</template>
