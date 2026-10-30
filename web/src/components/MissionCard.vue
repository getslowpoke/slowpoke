<script setup>
import { computed } from 'vue'
import { t } from '../lib/i18n.js'
import { duration, ago } from '../lib/format.js'
import { missionInWords, missionStatusNames, missionStatusWhy, returnInWords } from '../lib/settingsWords.js'
import { stageChip, toneClass } from '../lib/missionLife.js'
import { phpWait } from '../lib/phpWaits.js'

const props = defineProps({
  mission: { type: Object, required: true },
  slug: String,
  // How this project plays: the card needs it to say what "won" means here.
  settings: { type: Object, default: () => ({}) },
  // Whether the card offers to leave the thing out of the score or wave a regression away.
  quiet: { type: Boolean, default: false },
})
const emit = defineEmits(['exclude', 'dismiss'])
const m = computed(() => props.mission)
const isBoss = computed(() => m.value.type === 'boss')
const stage = computed(() => stageChip(m.value))
// Whoever is on it is said by the stage; a closed mission still says who did it.
const showPerson = computed(() => m.value.person && !['claimed', 'review', 'shipped'].includes(m.value.stage))
const kindIcon = { query: '⛁', endpoint: '⇄', php: '⏳', metric: '∿' }
const link = computed(() => `/projects/${props.slug}/missions/${m.value.id}`)
const perDay = (s) => (s * 24) / (m.value.window_hours || 24)
const hoursLeft = computed(() => (m.value.due_at ? Math.max(0, (m.value.due_at - Date.now() / 1000) / 3600) : null))
const statusStyle = {
  completed: 'text-good', defeated: 'text-good', escaped: 'text-bad', expired: 'text-ink-400', excluded: 'text-ink-400',
  dismissed: 'text-ink-400', open: 'text-ink-300',
}
</script>

<template>
  <div
    class="card relative overflow-hidden p-4 transition hover:border-ink-600"
    :class="isBoss && m.status === 'open' ? 'border-bad/40 shadow-[0_0_40px_-18px_rgb(255_122_122/0.6)]' : ''"
  >
    <div v-if="isBoss && m.status === 'open'" class="pointer-events-none absolute inset-0 bg-gradient-to-br from-bad/8 to-transparent" />
    <div class="relative flex items-start gap-3">
      <div class="grid h-9 w-9 shrink-0 place-items-center rounded-xl border text-base"
        :class="isBoss ? 'border-bad/40 bg-bad/10' : 'border-ink-700 bg-ink-850'">
        {{ isBoss ? '👾' : kindIcon[m.kind] || '•' }}
      </div>
      <div class="min-w-0 flex-1">
        <component :is="link ? 'router-link' : 'div'" :to="link" class="block truncate font-mono text-[13px] text-ink-100 hover:text-poke-300">
          <template v-if="m.kind === 'php'"><span class="font-sans">{{ phpWait(m.label).icon }} {{ phpWait(m.label).words }}</span> <span class="text-ink-500">· {{ m.label }}</span></template>
          <template v-else>{{ m.label }}</template>
        </component>
        <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-ink-400">
          <span v-if="m.status === 'open'">{{ missionInWords({ ...m, current: perDay(m.current ?? m.baseline), baseline: perDay(m.baseline) }, settings) }}</span>
          <span v-else :class="statusStyle[m.status]" :title="t(missionStatusWhy[m.status] || '')">{{ t(missionStatusNames[m.status] || m.status) }}<template v-if="m.saved_seconds_per_day"> · −{{ duration(m.saved_seconds_per_day) }} {{ t('removed per day') }}</template></span>
          <span v-if="isBoss && m.status === 'open' && hoursLeft !== null" class="text-bad">⏱ {{ Math.round(hoursLeft) }}h</span>
          <span v-if="m.effort && m.status === 'open'" class="chip">⚒ {{ returnInWords(m) }}</span>
          <span v-if="m.deploy" class="chip">{{ t('caused by') }} {{ m.deploy }}</span>
          <span v-if="stage" class="chip" :class="toneClass[stage.tone]">{{ stage.icon }} {{ stage.text }}</span>
          <span v-if="showPerson" class="chip">@{{ m.person.split('@')[0] }}</span>
          <span v-if="m.closed_at">{{ ago(m.closed_at) }}</span>
        </div>
        <div v-if="!isBoss && m.status === 'open'" class="mt-3 h-1.5 overflow-hidden rounded-full bg-ink-800">
          <div class="h-full rounded-full bg-gradient-to-r from-poke-600 to-poke-400 transition-all duration-700" :style="{ width: Math.max(4, m.progress * 100) + '%' }" />
        </div>
      </div>
      <div class="flex shrink-0 flex-col items-end gap-1 text-right">
        <div class="num font-semibold" :class="isBoss ? 'text-bad' : 'text-poke-400'">+{{ m.points }}</div>
        <div class="text-[10px] uppercase tracking-wider text-ink-500">pts</div>
        <router-link :to="link" class="mt-1 rounded-lg border border-ink-700 px-2 py-0.5 text-[11px] text-ink-300 hover:border-poke-500/50 hover:text-poke-300">{{ t('Details') }} →</router-link>
        <template v-if="quiet && m.status === 'open' && m.kind !== 'metric'">
          <button class="rounded-lg px-2 py-0.5 text-[11px] text-ink-500 hover:text-ink-200" :title="t('It is slow by design and always will be: stop counting it')"
            @click="emit('exclude', m)">🙈 {{ t('Leave it out') }}</button>
          <button v-if="isBoss" class="rounded-lg px-2 py-0.5 text-[11px] text-ink-500 hover:text-ink-200" :title="t('A one-off spike: close it and keep it quiet for a while')"
            @click="emit('dismiss', m)">🤫 {{ t('Wave it away') }}</button>
        </template>
      </div>
    </div>
  </div>
</template>
