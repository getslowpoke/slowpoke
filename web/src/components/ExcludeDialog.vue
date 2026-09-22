<script setup>
import { ref } from 'vue'
import { api } from '../lib/api.js'
import { t } from '../lib/i18n.js'
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

async function save() {
  busy.value = true
  failed.value = ''
  try {
    const res = await api.post(`/api/projects/${props.slug}/exclusions`, { kind: props.kind, target: props.target, reason: reason.value, note: note.value, days: days.value })
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
    <div class="fixed inset-0 z-50 grid place-items-center bg-ink-950/80 p-4 backdrop-blur-sm" @click.self="emit('cancel')">
      <form class="card w-full max-w-md p-6" @submit.prevent="save">
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
