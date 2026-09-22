<script setup>
import { computed } from 'vue'
import { api } from '../lib/api.js'
import { useLoad } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, duration, dateTime, day } from '../lib/format.js'
import State from '../components/State.vue'
import { deliveryWords, effortInWords } from '../lib/settingsWords.js'
import LineChart from '../components/LineChart.vue'
import Severity from '../components/Severity.vue'
import { kindWords, reasonInWords } from '../lib/exclusions.js'

const props = defineProps({ slug: String })
const { data, error, loading, reload } = useLoad(() => api.get(`/api/projects/${props.slug}/debt-report`))
const trend = computed(() => {
  const d = data.value?.debt
  if (!d?.previous_seconds_per_day) return null
  return ((d.seconds_per_day - d.previous_seconds_per_day) / d.previous_seconds_per_day) * 100
})
const hoursPerMonth = computed(() => ((data.value?.debt?.seconds_per_day || 0) * 30) / 3600)
</script>

<template>
  <div class="mx-auto max-w-4xl px-5 py-10 print:py-0">
    <State :loading="loading" :error="error" @retry="reload">
      <div class="no-print mb-6 flex items-center justify-between">
        <router-link :to="`/projects/${slug}`" class="text-sm text-ink-400 hover:text-ink-200">← {{ data.project.name }}</router-link>
        <button class="btn-primary" @click="print()">{{ t('Print') }}</button>
      </div>

      <header class="border-b border-ink-800 pb-6">
        <div class="label">🐢 Slowpoke · {{ t('Debt report') }}</div>
        <h1 class="mt-2 text-4xl font-semibold tracking-tight">{{ data.project.name }}</h1>
        <p class="mt-1 text-sm text-ink-400">{{ t('Generated') }} {{ dateTime(data.generated_at) }}</p>
      </header>

      <section class="mt-8 grid gap-4 sm:grid-cols-3">
        <div class="card p-5">
          <div class="label">{{ t('Technical debt') }}</div>
          <div class="num mt-1 text-3xl font-semibold text-poke-400">{{ duration(data.debt.seconds_per_day) }}</div>
          <div class="text-sm text-ink-400">{{ t('of waiting per day') }}</div>
          <div class="mt-2 text-xs text-ink-500">{{ Math.round(hoursPerMonth) === 1 ? t('about one hour of waiting every 30 days') : t('about {n} hours of waiting every 30 days', { n: num(hoursPerMonth, 0) }) }}</div>
        </div>
        <div class="card p-5">
          <div class="label">{{ t('Already repaid') }}</div>
          <div class="num mt-1 text-3xl font-semibold text-good">{{ duration(data.debt.repaid_seconds_per_day) }}</div>
          <div class="text-sm text-ink-400">{{ t('per day') }} · {{ data.repaid.length === 1 ? t('one mission') : t('{n} missions', { n: data.repaid.length }) }}</div>
        </div>
        <div class="card p-5">
          <div class="label">{{ t('Speed index') }}</div>
          <div class="num mt-1 text-3xl font-semibold">{{ num(data.score.first, 1) }} → {{ num(data.score.last, 1) }}</div>
          <div v-if="trend !== null" class="text-sm" :class="trend <= 0 ? 'text-good' : 'text-bad'">{{ t('Technical debt') }} {{ trend <= 0 ? '▼' : '▲' }} {{ num(Math.abs(trend), 0) }}%</div>
        </div>
      </section>

      <section class="mt-4 grid gap-4 sm:grid-cols-3">
        <div class="card p-5">
          <div class="label">🎯 {{ t('Weekly debt budget') }}</div>
          <div class="num mt-1 text-2xl font-semibold" :class="data.goal.reached ? 'text-good' : ''">{{ num(data.goal.progress * 100, 0) }}%</div>
          <div class="text-xs text-ink-400">{{ duration(data.goal.repaid_seconds_per_day) }} / {{ duration(data.goal.goal_seconds_per_day) }} · {{ data.goal.capacity_percent }}% {{ t('of each sprint') }}</div>
        </div>
        <div class="card p-5">
          <div class="label">{{ t('Delivery health') }}</div>
          <div class="mt-1 text-2xl font-semibold capitalize" :title="t(deliveryWords[data.delivery.rating] || '')">{{ t(data.delivery.rating) }}</div>
          <div class="text-xs text-ink-400">{{ num(data.delivery.per_week, 1) }} {{ t('deploys per week') }} · {{ t('{n}% of them hurt', { n: num(data.delivery.change_failure_rate, 0) }) }}</div>
        </div>
        <div class="card p-5">
          <div class="label">🔥 {{ t('Hotspots') }}</div>
          <div v-for="h in data.hotspots.slice(0, 3)" :key="h.file" class="mt-1 flex justify-between gap-2 text-xs"><span class="truncate font-mono">{{ h.file }}</span><span class="num shrink-0 text-poke-400">{{ duration(h.seconds_per_day) }}/{{ t('day') }}</span></div>
          <div v-if="!data.hotspots.length" class="mt-1 text-xs text-ink-500">—</div>
        </div>
      </section>

      <section class="card mt-4 p-5">
        <div class="label">{{ t('Speed index trend') }}</div>
        <LineChart class="mt-3" :height="200" :y-min="0" :y-max="100" :labels="data.score.days.map((d) => day(d.day))"
          :datasets="[{ label: t('Speed index'), data: data.score.days.map((d) => d.score), color: '#ff6f8e', points: true }]" />
      </section>

      <!-- What the team left out stays in the report: a debt that hides it is a debt nobody can trust. -->
      <section v-if="data.excluded?.length" class="card mt-4 p-5">
        <div class="flex flex-wrap items-baseline justify-between gap-2">
          <div class="label">🙈 {{ t('Left out by the team') }}</div>
          <div class="num text-sm text-ink-300">{{ duration(data.debt.excluded_seconds_per_day) }} {{ t('per day') }} · {{ t('not in the debt above') }}</div>
        </div>
        <div v-for="x in data.excluded" :key="x.target" class="mt-2 flex items-center justify-between gap-3 text-sm">
          <span class="min-w-0 truncate"><span class="text-[11px] uppercase tracking-wider text-ink-500">{{ t(kindWords[x.kind] || '') }}</span>
            <span class="font-mono text-xs">{{ x.label || x.endpoint }}</span> <span class="text-xs text-ink-500">· {{ reasonInWords(x.reason) }}</span></span>
          <span class="num shrink-0 text-ink-400">{{ duration(x.seconds_per_day) }}/{{ t('day') }}</span>
        </div>
      </section>

      <section class="mt-8">
        <h2 class="text-xl font-semibold">{{ t('Main findings') }}</h2>
        <div class="mt-3 space-y-3">
          <div v-for="f in data.findings" :key="f.hash" class="card p-4">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <div class="flex items-center gap-2"><Severity :level="f.diagnosis.severity" /><span class="font-medium">{{ f.diagnosis.title }}</span></div>
              <span class="num text-sm text-poke-400">{{ duration(f.seconds_per_day) }} {{ t('per day') }}</span>
            </div>
            <div class="mt-2 truncate font-mono text-xs text-ink-400">{{ f.fingerprint }}</div>
            <p class="mt-2 text-sm text-ink-300">{{ f.diagnosis.fix || f.diagnosis.detail }}</p>
          </div>
        </div>
      </section>

      <section class="mt-8 grid gap-6 md:grid-cols-2">
        <div>
          <h2 class="text-xl font-semibold">{{ t('Repayment plan') }} <span class="text-sm font-normal text-ink-400">· {{ t('quick wins first') }}</span></h2>
          <div class="mt-3 space-y-2">
            <div v-for="m in data.plan" :key="m.id" class="flex items-center justify-between gap-3 rounded-xl border border-ink-800 px-3 py-2 text-sm">
              <span class="truncate font-mono text-xs">{{ m.type === 'boss' ? '👾 ' : '' }}{{ m.label }}</span>
              <span class="num shrink-0 text-ink-400">{{ duration(m.cost_seconds_per_day) }}/{{ t('day') }} · {{ effortInWords(m.effort) }}</span>
            </div>
          </div>
        </div>
        <div>
          <h2 class="text-xl font-semibold">{{ t('Already repaid') }}</h2>
          <div class="mt-3 space-y-2">
            <div v-for="m in data.repaid" :key="m.id" class="flex items-center justify-between gap-3 rounded-xl border border-ink-800 px-3 py-2 text-sm">
              <span class="truncate font-mono text-xs">{{ m.label }}</span>
              <span v-if="m.saved_seconds_per_day >= 1" class="num shrink-0 text-good">−{{ duration(m.saved_seconds_per_day) }}/{{ t('day') }}</span>
              <span v-else class="shrink-0 text-xs text-good">{{ m.type === 'boss' ? t('regression fixed') : t('done') }}</span>
            </div>
          </div>
        </div>
      </section>
    </State>
  </div>
</template>
