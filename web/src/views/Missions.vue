<script setup>
import { computed, ref } from 'vue'
import { api } from '../lib/api.js'
import { useGet } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, duration, ago } from '../lib/format.js'
import State from '../components/State.vue'
import PageHeader from '../components/PageHeader.vue'
import MissionCard from '../components/MissionCard.vue'
import { missionsInWords } from '../lib/settingsWords.js'
import { busyInWords } from '../lib/busyHours.js'
import ExcludeDialog from '../components/ExcludeDialog.vue'
import DismissDialog from '../components/DismissDialog.vue'
import { excludeTarget } from '../lib/exclusions.js'
import { seasonName } from '../lib/activityWords.js'

const props = defineProps({ project: Object })
const busy = ref(null)
api.get(`/api/projects/${props.project.slug}/busy-hours`).then((b) => (busy.value = b)).catch(() => {})
const { data, error, loading, reload } = useGet(() => `/api/projects/${props.project.slug}/missions`, { every: 60000 })
const bosses = computed(() => (data.value?.open || []).filter((m) => m.type === 'boss'))
const sortBy = ref('roi')
const missions = computed(() => (data.value?.open || []).filter((m) => m.type !== 'boss').slice().sort((a, b) => (sortBy.value === 'roi' ? b.roi - a.roi : b.cost_seconds_per_day - a.cost_seconds_per_day)))
const medals = ['🥇', '🥈', '🥉']
// What nobody will act on: out of the score for good, or quiet for a while.
const excluding = ref(null)
const dismissing = ref(null)
const onExclude = (m) => (excluding.value = { kind: m.kind, target: excludeTarget(m.kind, m.target), label: m.label })
const onDismiss = (m) => (dismissing.value = m)
</script>

<template>
  <State :loading="loading" :error="error" @retry="reload">
    <PageHeader :title="t('Missions')" :subtitle="`${t('Technical debt')}: ${duration(data.debt.seconds_per_day)} ${t('of waiting per day')}`">
      <div class="card flex items-center gap-3 px-4 py-2">
        <div class="num text-2xl font-semibold text-poke-400">{{ num(data.points.season) }}</div>
        <div class="text-xs leading-tight text-ink-400">pts<br />{{ seasonName(data.points.season_start, data.points.season_name) }}</div>
      </div>
    </PageHeader>

    <div class="grid gap-4 xl:grid-cols-[1.5fr_1fr]">
      <div class="space-y-4">
        <section v-if="bosses.length">
          <div class="mb-2 text-sm font-medium text-bad">👾 {{ t('Regressions to defeat') }}</div>
          <div class="space-y-2.5"><MissionCard v-for="m in bosses" :key="m.id" :mission="m" :slug="project.slug" :settings="project.settings?.missions || {}" quiet
            @exclude="onExclude" @dismiss="onDismiss" /></div>
        </section>
        <section class="card relative overflow-hidden p-5">
          <div class="flex flex-wrap items-end justify-between gap-3">
            <div>
              <div class="label">🎯 {{ t('Weekly debt budget') }}</div>
              <div class="mt-1 text-sm text-ink-300">
                <span class="num text-xl font-semibold" :class="data.goal.reached ? 'text-good' : 'text-ink-100'">{{ duration(data.goal.repaid_seconds_per_day) }}</span>
                / {{ duration(data.goal.goal_seconds_per_day) }} {{ t('of waiting per day repaid in the last 7 days') }}
              </div>
            </div>
            <div class="text-right text-xs text-ink-400">{{ data.goal.capacity_percent }}% {{ t('of each sprint reserved for debt') }}</div>
          </div>
          <div class="mt-3 h-2 overflow-hidden rounded-full bg-ink-800">
            <div class="h-full rounded-full transition-all duration-700" :class="data.goal.reached ? 'bg-good' : 'bg-gradient-to-r from-poke-600 to-poke-400'" :style="{ width: Math.max(2, data.goal.progress * 100) + '%' }" />
          </div>
          <p v-if="data.goal.reached" class="mt-2 text-xs text-good">✓ {{ t('Budget kept this week') }} · +50 pts</p>
        </section>

        <section>
          <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
            <div class="text-sm font-medium">{{ t('Open missions') }}</div>
            <div class="inline-flex rounded-xl border border-ink-700 bg-ink-900 p-0.5 text-xs">
              <button class="rounded-[10px] px-3 py-1" :class="sortBy === 'roi' ? 'bg-ink-700 text-ink-100' : 'text-ink-400'" @click="sortBy = 'roi'">⚡ {{ t('Quick wins first') }}</button>
              <button class="rounded-[10px] px-3 py-1" :class="sortBy === 'cost' ? 'bg-ink-700 text-ink-100' : 'text-ink-400'" @click="sortBy = 'cost'">{{ t('Biggest cost first') }}</button>
            </div>
          </div>
          <div class="space-y-2.5">
            <MissionCard v-for="m in missions" :key="m.id" :mission="m" :slug="project.slug" :settings="project.settings?.missions || {}" quiet
              @exclude="onExclude" @dismiss="onDismiss" />
            <p v-if="!missions.length" class="card card-pad text-center text-sm text-ink-400">🧘 {{ t('No open missions: the debt is under control.') }}</p>
          </div>
          <p class="mt-2 text-xs text-ink-500">
            {{ missionsInWords(data.settings) }}
            <template v-if="busy"> {{ t('Judged per request on the busy hours: {hours}.', { hours: busyInWords(busy) }) }}</template>
          </p>
        </section>
        <section v-if="data.closed.length">
          <div class="mb-2 text-sm font-medium text-ink-300">{{ t('Closed') }}</div>
          <div class="space-y-2"><MissionCard v-for="m in data.closed.slice(0, 15)" :key="m.id" :mission="m" :slug="project.slug" :settings="project.settings?.missions || {}" /></div>
        </section>
      </div>

      <div class="space-y-4">
        <section class="card card-pad">
          <div class="label">🏛 {{ t('Hall of fame') }}</div>
          <div class="mt-3 space-y-2">
            <div v-for="(m, i) in data.hall_of_fame" :key="m.id" class="flex items-center gap-3">
              <span class="w-6 text-center text-lg">{{ medals[i] || '·' }}</span>
              <div class="min-w-0 flex-1">
                <div class="truncate font-mono text-xs text-ink-200">{{ m.label }}</div>
                <div class="text-[11px] text-ink-500"><span v-if="m.person">@{{ m.person }} · </span>{{ ago(m.closed_at) }}</div>
              </div>
              <div class="num text-sm font-medium text-good">−{{ duration(m.saved_seconds_per_day) }}</div>
            </div>
            <p v-if="!data.hall_of_fame.length" class="text-sm text-ink-500">{{ t('Nothing yet.') }}</p>
          </div>
        </section>

        <section v-if="data.leaderboard_enabled" class="card card-pad">
          <div class="label">{{ t('Leaderboard') }} · {{ seasonName(data.points.season_start, data.points.season_name) }}</div>
          <div class="mt-3 space-y-2">
            <div v-for="(p, i) in data.leaderboard" :key="p.person" class="flex items-center gap-3">
              <span class="w-6 text-center">{{ medals[i] || i + 1 }}</span>
              <span class="flex-1 text-sm">@{{ p.person }}</span>
              <span class="num text-sm font-medium text-poke-400">{{ num(p.points) }}</span>
            </div>
            <p v-if="!data.leaderboard.length" class="text-sm text-ink-500">{{ t('Nothing yet.') }}</p>
          </div>
        </section>

        <section class="card card-pad">
          <div class="label">{{ t('Points ledger') }}</div>
          <div class="mt-3 max-h-96 space-y-2 overflow-auto pr-1">
            <div v-for="(e, i) in data.ledger" :key="i" class="flex items-start gap-3 text-sm">
              <span class="num w-12 shrink-0 text-right font-medium" :class="e.points >= 0 ? 'text-good' : 'text-bad'">{{ e.points > 0 ? '+' : '' }}{{ e.points }}</span>
              <div class="min-w-0">
                <div class="truncate text-ink-200">{{ e.reason }}</div>
                <div class="text-[11px] text-ink-500"><span v-if="e.person">@{{ e.person }} · </span>{{ ago(e.created_at) }}</div>
              </div>
            </div>
          </div>
        </section>
      </div>
    </div>
    <ExcludeDialog v-if="excluding" :slug="project.slug" :kind="excluding.kind" :target="excluding.target" :label="excluding.label"
      :suggested="excluding.kind === 'query' ? 'third_party' : 'external'" @cancel="excluding = null" @done="excluding = null; reload()" />
    <DismissDialog v-if="dismissing" :slug="project.slug" :mission="dismissing" @cancel="dismissing = null" @done="dismissing = null; reload()" />
  </State>
</template>
