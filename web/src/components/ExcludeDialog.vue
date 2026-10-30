<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../lib/api.js'
import { t } from '../lib/i18n.js'
import { duration } from '../lib/format.js'
import { reasonWords, durations } from '../lib/exclusions.js'

const props = defineProps({
  slug: String,
  kind: { type: String, default: 'endpoint' },
  target: String,
  // What the person sees: the route, the statement, the PHP frame.
  label: String,
  suggested: { type: String, default: 'external' },
})
const emit = defineEmits(['done', 'cancel'])
const reason = ref(props.suggested)
const note = ref('')
const days = ref(30)
const busy = ref(false)
const failed = ref('')
// What goes with it: the same path under another method (ticked, it is almost always meant) and
// the PHP waits on other services (not ticked: they are the whole app's, not this endpoint's).
const related = ref([])
const also = ref([])

onMounted(async () => {
  if (props.kind !== 'endpoint') return
  try {
    const res = await api.get(`/api/projects/${props.slug}/exclusions/related?kind=endpoint&target=${encodeURIComponent(props.target)}`)
    related.value = res.related || []
    also.value = related.value.filter((r) => r.why === 'same_path').map((r) => r.kind + ' ' + r.target)
  } catch {
    related.value = [] // the dialog works without it
  }
})

const allTicked = computed(() => related.value.length > 0 && also.value.length === related.value.length)
function tickAll() {
  also.value = allTicked.value ? [] : related.value.map((r) => r.kind + ' ' + r.target)
}

async function save() {
  busy.value = true
  failed.value = ''
  try {
    const body = { reason: reason.value, note: note.value, days: days.value }
    const res = await api.post(`/api/projects/${props.slug}/exclusions`, { ...body, kind: props.kind, target: props.target })
    for (const r of related.value.filter((r) => also.value.includes(r.kind + ' ' + r.target))) {
      const more = await api.post(`/api/projects/${props.slug}/exclusions`, { ...body, kind: r.kind, target: r.target })
      res.closed_missions = (res.closed_missions || 0) + (more.closed_missions || 0)
    }
    emit('done', res)
  } catch (e) {
    failed.value = e.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Teleport to="body">
    <div class="fixed inset-0 z-50 grid place-items-center overflow-y-auto bg-ink-950/80 p-4 backdrop-blur-sm" @click.self="emit('cancel')">
      <form class="card max-h-[calc(100dvh-2rem)] w-full max-w-md overflow-y-auto overscroll-contain p-6" @submit.prevent="save">
        <h2 class="text-lg font-semibold">{{ t('Leave this out of the score') }}</h2>
        <p class="mt-1 break-words font-mono text-sm text-ink-300">{{ label || target }}</p>
        <p class="mt-3 text-sm leading-relaxed text-ink-400">
          {{ t('It stops counting in the speed index, the debt and the missions. Its time is still measured and shown apart, with the reason, in the debt report.') }}
        </p>
        <fieldset class="mt-4 space-y-2">
          <legend class="label mb-1">{{ t('Why') }}</legend>
          <label v-for="(words, key) in reasonWords" :key="key" class="flex cursor-pointer items-center gap-2 text-sm text-ink-200">
            <input v-model="reason" type="radio" name="reason" :value="key" class="accent-poke-500" /> {{ t(words) }}
          </label>
        </fieldset>
        <fieldset v-if="related.length" class="mt-4 space-y-2">
          <legend class="label mb-1 flex w-full items-center justify-between gap-2">
            <span>{{ t('Leave out with it') }}</span>
            <button v-if="related.length > 1" type="button" class="text-xs normal-case tracking-normal text-poke-400 hover:text-poke-300" @click="tickAll">
              {{ allTicked ? t('Untick all') : t('Tick all') }}
            </button>
          </legend>
          <label v-for="r in related" :key="r.kind + ' ' + r.target" class="flex cursor-pointer items-start gap-2 text-sm text-ink-200">
            <input v-model="also" type="checkbox" :value="r.kind + ' ' + r.target" class="mt-1 shrink-0 accent-poke-500" />
            <span class="min-w-0">
              <span class="block break-words font-mono text-xs">{{ r.target }}</span>
              <span class="block text-xs text-ink-400">
                {{ r.why === 'same_path' ? t('The same address with another method') : t('PHP waiting on another service, in the whole app') }}
                · {{ duration(r.seconds_per_day) }} {{ t('of waiting per day') }}
              </span>
            </span>
          </label>
        </fieldset>
        <label class="mt-4 block">
          <span class="label">{{ t('A note for the team (optional)') }}</span>
          <input v-model="note" class="input mt-1" maxlength="500" :placeholder="t('e.g. waits on the payment provider')" />
        </label>
        <label class="mt-4 block">
          <span class="label">{{ t('For how long') }}</span>
          <select v-model.number="days" class="input mt-1">
            <option v-for="d in durations" :key="d.days" :value="d.days">{{ t(d.label) }}</option>
          </select>
        </label>
        <p v-if="failed" class="mt-3 text-sm text-bad" role="alert">{{ failed }}</p>
        <div class="mt-6 flex justify-end gap-2">
          <button type="button" class="btn-ghost" @click="emit('cancel')">{{ t('Cancel') }}</button>
          <button type="submit" class="btn-primary" :disabled="busy">{{ busy ? '…' : t('Leave it out') }}</button>
        </div>
      </form>
    </div>
  </Teleport>
</template>
