<script setup>
import { computed, ref } from 'vue'
import { api } from '../lib/api.js'
import { useLoad } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, latency, duration, ago, day, clock, compact, scoreColor } from '../lib/format.js'
import State from '../components/State.vue'
import ScoreRing from '../components/ScoreRing.vue'
import LineChart from '../components/LineChart.vue'
import Sparkline from '../components/Sparkline.vue'
import MissionCard from '../components/MissionCard.vue'
import WinsCard from '../components/WinsCard.vue'
import Empty from '../components/Empty.vue'
import Popover from '../components/Popover.vue'
import InstallWizard from '../components/InstallWizard.vue'
import { componentName, componentNow, liveTileInWords, pointsToGain, scoreRowInWords, scoreTrend } from '../lib/settingsWords.js'

const props = defineProps({ project: Object })
const wizard = ref(false)
const { data, error, loading, reload } = useLoad(() => api.get(`/api/projects/${props.project.slug}/overview`), { every: 30000 })

// The fleet, offline first: those are the ones worth seeing without opening anything. Muted
// machines go last, offline or not: the team chose not to hear about them.
const allHosts = ref(false)
const hostRank = (h) => (h.muted ? 2 : h.online ? 1 : 0)
const sortedHosts = computed(() => [...(data.value?.hosts || [])].sort((a, b) => hostRank(a) - hostRank(b) || String(a.name).localeCompare(String(b.name))))
const shownHosts = computed(() => sortedHosts.value.slice(0, 5))
const hiddenHosts = computed(() => Math.max(0, sortedHosts.value.length - shownHosts.value.length))
const onlineHosts = computed(() => sortedHosts.value.filter((h) => h.online).length)

// Il voto di oggi da solo non dice se si sta migliorando: il record e la media dei sette giorni si.
const trend = computed(() => scoreTrend(data.value?.chart || []))

const today = computed(() => data.value?.today || {})
const level = computed(() => today.value.level)
const hasData = computed(() => (data.value?.chart?.length || 0) > 0 || (data.value?.live?.series?.length || 0) > 0)

const chart = computed(() => {
  const days = data.value?.chart || []
  const labels = days.map((d) => day(d.day))
  const markers = []
  for (const d of data.value?.deploys || []) {
    const i = days.findIndex((x) => x.day === d.day)
    if (i >= 0) {
      const v = d.impact?.verdict
      markers.push({ index: i, label: d.version || d.commit?.slice(0, 7), color: v === 'better' ? '#7ee0a1' : v === 'worse' ? '#ff7a7a' : '#8b8b95' })
    }
  }
  return {
    labels,
    markers,
    datasets: [
      // Le soglie dei livelli, cosi il grafico dice anche "quanto manca", non solo "come va".
      ...(data.value?.levels || [])
        .filter((l) => l.min > 0)
        .map((l) => ({ label: `${l.icon} ${l.name}`, data: days.map(() => l.min), color: l.color, fill: false, dashed: true, points: false })),
      { label: t('Speed index'), data: days.map((d) => d.score), color: '#ff6f8e', points: true, pointColors: days.map((d) => d.color || '#ff6f8e') },
    ],
  }
})

const live = computed(() => data.value?.live || {})
const debt = computed(() => data.value?.debt || {})
const debtDelta = computed(() => {
  const d = debt.value
  if (!d.previous_seconds_per_day) return null
  return ((d.seconds_per_day - d.previous_seconds_per_day) / d.previous_seconds_per_day) * 100
})
const bosses = computed(() => (data.value?.missions || []).filter((m) => m.type === 'boss'))
const missions = computed(() => (data.value?.missions || []).filter((m) => m.type !== 'boss'))
const unlocked = computed(() => (data.value?.badges || []).filter((b) => b.unlocked).length)
const kindIcon = { query: '⛁', endpoint: '⇄', php: '⏳', metric: '∿' }

function itemLink(item) {
  if (item.kind === 'query') return `/projects/${props.project.slug}/queries/${item.target}`
  if (item.kind === 'endpoint') {
    const [source, ...rest] = item.target.split(' ')
    return `/projects/${props.project.slug}/endpoint?source=${encodeURIComponent(source)}&endpoint=${encodeURIComponent(rest.join(' '))}`
  }
  return `/projects/${props.project.slug}/slow`
}
</script>

<template>
  <State :loading="loading && !data" :error="error" @retry="reload">
    <div class="mb-6 flex flex-wrap items-end justify-between gap-3">
      <div>
        <div class="label">{{ project.timezone }}</div>
        <h1 class="mt-1 text-2xl font-semibold tracking-tight sm:text-[28px]">{{ project.name }}</h1>
      </div>
      <div class="flex items-center gap-3 text-xs text-ink-400">
        <button class="btn-primary !py-1.5 text-xs" @click="wizard = true">+ {{ t('Add agent') }}</button>
        <span class="relative flex h-2 w-2"><span class="absolute inline-flex h-full w-full animate-ping rounded-full bg-good opacity-60" /><span class="relative inline-flex h-2 w-2 rounded-full bg-good" /></span>
        {{ t('Live') }} · {{ t('last 15 minutes') }}
      </div>
    </div>

    <section v-if="!hasData" class="card relative overflow-hidden px-6 py-14 text-center sm:px-12">
      <div class="pointer-events-none absolute left-1/2 top-0 h-80 w-[40rem] -translate-x-1/2 rounded-full bg-poke-500/15 blur-3xl" />
      <div class="relative mx-auto max-w-2xl">
        <div class="mx-auto grid h-20 w-20 place-items-center rounded-3xl border border-poke-500/30 bg-poke-500/10 text-5xl shadow-[0_0_60px_-10px_rgb(255_111_142/0.6)]">🐢</div>
        <h2 class="mt-6 text-3xl font-semibold tracking-tight sm:text-4xl">{{ t('Connect your first machine') }}</h2>
        <p class="mx-auto mt-3 max-w-xl text-ink-300">{{ t('One small agent per server: in a minute you see slow endpoints, heavy queries, your infrastructure map and the first missions to repay technical debt.') }}</p>
        <button class="btn-primary mt-8 !px-6 !py-3 text-base" @click="wizard = true">+ {{ t('Add the first agent') }}</button>
        <div class="mt-10 grid gap-3 text-left sm:grid-cols-3">
          <div v-for="step in [
            ['1', t('Choose what to watch'), t('Web server, PHP, MySQL, PostgreSQL or just the machine.')],
            ['2', t('Copy one command'), t('Docker, Linux or Kubernetes. Read-only, 1 core, 64 MB.')],
            ['3', t('Play'), t('Speed index, badges and missions start with the first data.')],
          ]" :key="step[0]" class="rounded-2xl border border-ink-700 bg-ink-850/60 p-4">
            <div class="grid h-7 w-7 place-items-center rounded-lg bg-poke-500/15 text-sm font-semibold text-poke-300">{{ step[0] }}</div>
            <div class="mt-3 font-medium">{{ step[1] }}</div>
            <div class="mt-1 text-sm text-ink-400">{{ step[2] }}</div>
          </div>
        </div>
      </div>
    </section>

    <template v-else>
      <!-- Hero: index + live -->
      <div class="grid gap-4 xl:grid-cols-[1.1fr_1fr]">
        <section class="card relative overflow-hidden p-6">
          <div class="pointer-events-none absolute -right-24 -top-24 h-72 w-72 rounded-full opacity-20 blur-3xl" :style="{ background: level?.color || '#ff6f8e' }" />
          <div class="relative flex flex-col gap-6 sm:flex-row sm:items-center">
            <ScoreRing :score="today.score ?? null" :color="level?.color || '#ff6f8e'" :size="176" :label="`${t('Today')} · ${t('so far')}`" />
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-1.5">
                <span class="label">{{ t('Speed index') }}</span>
                <span v-for="l in (data.levels || []).filter((x) => x.min > 0)" :key="l.name" class="chip !py-0 !text-[10px]"
                  :class="l.name === level?.name ? '!border-poke-500/50' : ''" :style="{ color: l.color }">{{ l.icon }} {{ l.name }} {{ l.min }}</span>
              </div>
              <div class="mt-1 flex items-center gap-2 text-2xl font-semibold tracking-tight">
                <span>{{ level?.icon }}</span><span :style="{ color: level?.color }">{{ level?.name || '—' }}</span>
              </div>
              <div class="mt-2 flex flex-wrap gap-2 text-sm">
                <span v-if="today.delta !== undefined" class="chip num" :class="today.delta >= 0 ? '!text-good' : '!text-bad'">
                  {{ today.delta >= 0 ? '▲' : '▼' }} {{ num(Math.abs(today.delta), 1) }} {{ t('vs yesterday') }}
                </span>
                <span v-if="data.streak?.days" class="chip">🔥 {{ data.streak.days }} {{ t('days without getting worse') }}</span>
                <span v-if="trend.record" class="chip" :title="t('The best day there has been, out of the days that finished.')">🏆 {{ t('record') }} <span class="num">{{ num(trend.record.score, 1) }}</span> · {{ day(trend.record.day) }}</span>
                <span v-if="trend.week !== null" class="chip" :title="t('The average of the last seven days that finished, against the seven before them.')">
                  {{ t('seven days') }} <span class="num">{{ num(trend.week, 1) }}</span>
                  <span v-if="trend.change !== null" class="num" :class="trend.change >= 0 ? 'text-good' : 'text-bad'">{{ trend.change >= 0 ? '+' : '' }}{{ num(trend.change, 1) }}</span>
                </span>
              </div>
              <div v-if="today.next_level" class="mt-4">
                <div class="flex justify-between text-xs text-ink-400">
                  <span>{{ level?.name }}</span>
                  <span class="num">{{ num(today.next_level.missing, 1) }} pt {{ t('to') }} {{ today.next_level.icon }} {{ today.next_level.name }}</span>
                </div>
                <div class="mt-1.5 h-2 overflow-hidden rounded-full bg-ink-800">
                  <div class="h-full rounded-full transition-all duration-1000"
                    :style="{ width: Math.min(100, Math.max(3, ((today.score - (level?.min || 0)) / (today.next_level.min - (level?.min || 0))) * 100)) + '%', background: `linear-gradient(90deg, ${level?.color}, ${today.next_level ? '#ff8fa6' : level?.color})` }" />
                </div>
              </div>
              <div v-else-if="today.score !== null && today.score !== undefined" class="mt-4 text-sm text-good">✨ {{ t('Top level reached') }}</div>
            </div>
          </div>

          <div v-if="today.components?.length" class="relative mt-6 border-t border-ink-800 pt-5">
            <div class="label mb-1">{{ t('Where to gain points') }}</div>
            <p class="mb-3 text-xs text-ink-500">{{ t('From the measure that loses the most today. The server sends them already in this order.') }}</p>
            <div class="grid gap-2">
              <div v-for="c in today.components" :key="c.key" class="rounded-xl bg-ink-850/60 px-3 py-2"
                :title="scoreRowInWords(c, today.components)">
                <div class="flex items-center justify-between gap-2 text-xs">
                  <span class="truncate text-ink-300">{{ componentName(c) }}</span>
                  <span v-if="!c.available" class="shrink-0 text-ink-500">{{ t('not measured') }}</span>
                  <span v-else-if="c.lost >= 0.05" class="num chip shrink-0 !border-warn/30 !py-0 !text-warn">{{ pointsToGain(c) }}</span>
                  <span v-else class="chip shrink-0 !border-good/30 !py-0 !text-good">{{ pointsToGain(c) }} ✓</span>
                </div>
                <div class="mt-1.5 h-1 overflow-hidden rounded-full bg-ink-800">
                  <div class="h-full rounded-full" :style="{ width: (c.available ? (c.points / c.weight) * 100 : 0) + '%', background: c.lost > c.weight / 2 ? 'var(--color-bad)' : c.lost > 0.5 ? 'var(--color-warn)' : 'var(--color-good)' }" />
                </div>
                <div class="mt-1 flex items-center justify-between gap-2 text-[11px] text-ink-500">
                  <span class="truncate">{{ componentNow(c) }}</span>
                  <span v-if="c.available" class="num shrink-0">{{ num(c.points, 1) }}/{{ c.weight }}</span>
                </div>
              </div>
            </div>
          </div>
        </section>

        <section class="grid gap-4">
          <div class="grid grid-cols-2 gap-4 sm:grid-cols-3">
            <div class="card p-4">
              <div class="label" :title="liveTileInWords('requests')">{{ t('requests/min') }}</div>
              <div class="num mt-1 text-2xl font-semibold">{{ compact(live.requests_per_minute) }}</div>
              <Sparkline class="mt-2" :values="(live.series || []).map((s) => s.requests)" color="#8ab4ff" :width="140" />
            </div>
            <div class="card p-4">
              <div class="label" :title="liveTileInWords('p95')">{{ t('response time') }}</div>
              <div class="num mt-1 text-2xl font-semibold" :class="live.p95 > 1 ? 'text-warn' : ''">{{ live.requests_per_minute ? latency(live.p95) : '—' }}</div>
              <Sparkline class="mt-2" :values="(live.series || []).map((s) => s.avg)" color="#ff6f8e" :width="140" />
            </div>
            <div class="card p-4">
              <div class="label" :title="liveTileInWords('errors')">{{ t('failed requests') }}</div>
              <div class="num mt-1 text-2xl font-semibold" :class="live.errors ? 'text-bad' : 'text-good'">{{ num(live.errors) }}</div>
              <Sparkline class="mt-2" :values="(live.series || []).map((s) => s.e5)" color="#ff7a7a" :width="140" />
            </div>
            <div class="card p-4">
              <div class="label" :title="liveTileInWords('db_slow')">{{ t('slow queries') }}</div>
              <div class="num mt-1 text-2xl font-semibold">{{ duration(live.db_slow_seconds) }}</div>
            </div>
            <div class="card p-4">
              <div class="label" :title="liveTileInWords('php_stalls')">{{ t('PHP stuck') }}</div>
              <div class="num mt-1 text-2xl font-semibold">{{ num(live.php_stalls) }}</div>
            </div>
            <!-- A fleet is a list, not a wall: a few names, the rest one click away. Offline
                 machines come first, because those are the ones worth seeing without asking. -->
            <div class="card p-4 transition hover:border-ink-600">
              <router-link :to="`/projects/${project.slug}/hosts`" class="label block hover:text-ink-200" :title="liveTileInWords('machines')">{{ t('Machines') }}</router-link>
              <div class="mt-2 flex flex-wrap gap-1.5">
                <router-link v-for="h in shownHosts" :key="h.id" :to="`/projects/${project.slug}/hosts/${h.id}`" class="chip hover:border-ink-500" :class="h.muted ? 'opacity-60' : ''" :title="h.muted ? `${h.name} · ${t('muted')}` : h.name">
                  <span class="h-1.5 w-1.5 rounded-full" :class="h.online ? 'bg-good' : 'bg-ink-500'" />{{ h.name }}
                </router-link>
                <button v-if="hiddenHosts" class="chip hover:border-ink-500" @click="allHosts = true">+{{ hiddenHosts }}</button>
              </div>
            </div>
          </div>

          <div class="card relative overflow-hidden p-5">
            <div class="flex items-start justify-between gap-4">
              <div>
                <div class="label">{{ t('Technical debt') }}</div>
                <div class="num mt-1 text-3xl font-semibold tracking-tight">{{ duration(debt.seconds_per_day) }}</div>
                <div class="text-sm text-ink-400">{{ t('of waiting per day') }}</div>
              </div>
              <div class="text-right text-sm">
                <div v-if="debtDelta !== null" class="num font-medium" :class="debtDelta <= 0 ? 'text-good' : 'text-bad'">
                  {{ debtDelta <= 0 ? '▼' : '▲' }} {{ num(Math.abs(debtDelta), 0) }}%
                </div>
                <div class="text-xs text-ink-500">{{ t('vs the day before') }}</div>
                <div v-if="debt.repaid_seconds_per_day" class="mt-2 text-xs text-good">✂ {{ duration(debt.repaid_seconds_per_day) }} {{ t('repaid per day') }}</div>
              </div>
            </div>
            <router-link v-if="data.goal" :to="`/projects/${project.slug}/missions`" class="mt-3 block">
              <div class="flex justify-between text-[11px] text-ink-400"><span>🎯 {{ t('Weekly debt budget') }}</span><span class="num">{{ duration(data.goal.repaid_seconds_per_day) }} / {{ duration(data.goal.goal_seconds_per_day) }}</span></div>
              <div class="mt-1 h-1.5 overflow-hidden rounded-full bg-ink-800"><div class="h-full rounded-full" :class="data.goal.reached ? 'bg-good' : 'bg-poke-500'" :style="{ width: Math.max(2, data.goal.progress * 100) + '%' }" /></div>
            </router-link>
            <div class="mt-4 space-y-1.5">
              <router-link v-for="item in (debt.items || []).slice(0, 4)" :key="item.kind + item.target" :to="itemLink(item)"
                class="group flex items-center gap-3 rounded-lg px-2 py-1.5 transition hover:bg-ink-850">
                <span class="w-4 text-center text-ink-500">{{ kindIcon[item.kind] }}</span>
                <span class="min-w-0 flex-1 truncate font-mono text-xs text-ink-300 group-hover:text-ink-100">{{ item.label }}</span>
                <span class="num text-xs text-ink-400">{{ duration(item.seconds) }}</span>
                <span class="h-1 w-16 overflow-hidden rounded-full bg-ink-800"><span class="block h-full bg-poke-500" :style="{ width: (item.seconds / (debt.items[0]?.seconds || 1)) * 100 + '%' }" /></span>
              </router-link>
            </div>
          </div>
        </section>
      </div>

      <!-- Bosses -->
      <section v-if="bosses.length" class="mt-4">
        <div class="mb-2 flex items-center gap-2 text-sm font-medium text-bad">👾 {{ t('Regressions to defeat') }}</div>
        <div class="grid gap-3 md:grid-cols-2">
          <MissionCard v-for="m in bosses" :key="m.id" :mission="m" :slug="project.slug" :settings="project.settings?.missions || {}" />
        </div>
      </section>

      <div class="mt-4 grid gap-4 xl:grid-cols-[1.4fr_1fr]">
        <section class="card p-5">
          <div class="flex items-center justify-between">
            <div class="label">{{ t('Last 30 days') }}</div>
            <div class="text-xs text-ink-500">▎{{ t('Deploys') }}</div>
          </div>
          <LineChart class="mt-3" :labels="chart.labels" :datasets="chart.datasets" :markers="chart.markers" :y-min="0" :y-max="100" :height="240" :format-y="(v) => num(v, 0)" />
        </section>

        <section class="card p-5">
          <div class="flex items-center justify-between">
            <div class="label">{{ t('Open missions') }}</div>
            <router-link :to="`/projects/${project.slug}/missions`" class="text-xs text-ink-400 hover:text-poke-300">{{ t('Missions') }} →</router-link>
          </div>
          <div class="mt-3 space-y-2.5">
            <MissionCard v-for="m in missions.slice(0, 4)" :key="m.id" :mission="m" :slug="project.slug" :settings="project.settings?.missions || {}" />
            <p v-if="!missions.length" class="py-6 text-center text-sm text-ink-400">🧘 {{ t('No open missions: the debt is under control.') }}</p>
          </div>
        </section>
      </div>

      <WinsCard class="mt-4" :wins="data.wins" :slug="project.slug" :title="t('Wins of the week')"
        :note="t('The last seven days against the seven before, counted by Slowpoke.')" />

      <div class="mt-4 grid gap-4 xl:grid-cols-3">
        <section class="card p-5">
          <div class="label">{{ t('Points') }}</div>
          <div class="mt-2 flex items-end gap-2">
            <div class="num text-4xl font-semibold tracking-tight text-poke-400">{{ num(data.points?.season) }}</div>
            <div class="pb-1 text-sm text-ink-400">{{ t('this season') }} · {{ data.points?.season_name }}</div>
          </div>
          <div class="mt-4 grid grid-cols-2 gap-3 text-sm">
            <div class="rounded-xl bg-ink-850/60 p-3"><div class="num text-lg font-medium">{{ num(data.points?.last_7_days) }}</div><div class="text-xs text-ink-400">{{ t('last 7 days') }}</div></div>
            <div class="rounded-xl bg-ink-850/60 p-3"><div class="num text-lg font-medium">{{ num(data.points?.total) }}</div><div class="text-xs text-ink-400">{{ t('total') }}</div></div>
          </div>
        </section>

        <section class="card p-5">
          <div class="flex items-center justify-between">
            <div class="label">{{ t('Badges') }}</div>
            <div class="num text-xs text-ink-400">{{ unlocked }}/{{ data.badges?.length }}</div>
          </div>
          <p class="mt-1 text-xs text-ink-500">{{ t('Unlocked by making the platform faster. Hover to see how.') }}</p>
          <div class="mt-3 grid grid-cols-5 gap-2">
            <Popover v-for="(b, i) in data.badges" :key="b.key" :width="230" :align="i % 5 === 0 ? 'start' : i % 5 === 4 ? 'end' : 'center'">
              <div class="grid aspect-square cursor-pointer place-items-center rounded-xl border text-xl transition"
                :class="b.unlocked ? 'border-ink-700 bg-ink-850 hover:-translate-y-0.5 hover:border-poke-500/50 hover:shadow-[0_0_24px_-6px_rgb(255_111_142/0.5)]' : 'border-ink-800 bg-ink-900 opacity-35 grayscale hover:opacity-70'">
                {{ b.icon }}
              </div>
              <template #content>
                <div class="flex items-center gap-2.5">
                  <span class="text-2xl" :class="b.unlocked ? '' : 'grayscale opacity-60'">{{ b.icon }}</span>
                  <div>
                    <div class="text-sm font-medium text-ink-100">{{ b.title }}</div>
                    <div class="text-[11px]" :class="b.unlocked ? 'text-good' : 'text-ink-500'">
                      {{ b.unlocked ? `✓ ${t('Unlocked')} · ${day(b.day)}` : `🔒 ${t('Locked')}` }}
                    </div>
                  </div>
                </div>
                <p class="mt-2 text-xs leading-relaxed text-ink-300">{{ b.description }}</p>
                <div class="mt-2 text-[11px] text-poke-300">+25 pts</div>
              </template>
            </Popover>
          </div>
        </section>

        <section class="card p-5">
          <div class="label">{{ t('Activity') }}</div>
          <div class="mt-3 max-h-64 space-y-2.5 overflow-auto pr-1">
            <div v-for="a in data.activity" :key="a.id" class="flex gap-3 text-sm">
              <span class="grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-ink-850">{{ a.icon || '•' }}</span>
              <div class="min-w-0">
                <div class="line-clamp-2 text-ink-200">{{ a.title }}</div>
                <div class="text-xs text-ink-500">{{ ago(a.created_at) }}</div>
              </div>
            </div>
            <p v-if="!data.activity?.length" class="text-sm text-ink-500">{{ t('Nothing yet.') }}</p>
          </div>
        </section>
      </div>
    </template>
    <InstallWizard v-if="wizard" :project="project" @close="wizard = false; reload()" />

    <Teleport to="body">
      <div v-if="allHosts" class="fixed inset-0 z-50 grid place-items-center bg-ink-950/80 p-4 backdrop-blur-sm" @click.self="allHosts = false">
        <div class="card max-h-[80vh] w-full max-w-lg overflow-y-auto p-6">
          <div class="flex items-start justify-between gap-4">
            <div>
              <h2 class="text-lg font-semibold">{{ t('Machines') }}</h2>
              <p class="mt-1 text-sm text-ink-400">{{ t('{n} sending data, {off} silent', { n: onlineHosts, off: (data.hosts || []).length - onlineHosts }) }}</p>
            </div>
            <button class="btn-ghost !px-2 !py-1 text-xs" @click="allHosts = false">✕</button>
          </div>
          <div class="mt-4 grid gap-1.5 sm:grid-cols-2">
            <router-link v-for="h in sortedHosts" :key="h.id" :to="`/projects/${project.slug}/hosts/${h.id}`"
              class="row-hover flex items-center gap-2 rounded-xl px-3 py-2 text-sm" @click="allHosts = false">
              <span class="h-1.5 w-1.5 shrink-0 rounded-full" :class="h.online ? 'bg-good' : 'bg-ink-500'" />
              <span class="truncate">{{ h.name }}</span>
              <span v-if="!h.online" class="ml-auto shrink-0 text-[11px] text-ink-500">{{ t('silent') }}</span>
            </router-link>
          </div>
        </div>
      </div>
    </Teleport>
  </State>
</template>
