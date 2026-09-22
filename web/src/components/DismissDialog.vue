<script setup>
import { ref } from 'vue'
import { api } from '../lib/api.js'
import { t } from '../lib/i18n.js'

// Waving a regression away is not the same as leaving its target out of the score: the endpoint
// or the query keeps counting, only this one regression goes quiet, and for a while.
const props = defineProps({ slug: String, mission: Object })
const emit = defineEmits(['done', 'cancel'])
const note = ref('')
const days = ref(14)
const busy = ref(false)
const failed = ref('')

async function save() {
  busy.value = true
  failed.value = ''
  try {
    emit('done', await api.post(`/api/projects/${props.slug}/missions/${props.mission.id}/dismiss`, { note: note.value, days: days.value }))
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
        <h2 class="text-lg font-semibold">{{ t('Wave this regression away') }}</h2>
        <p class="mt-1 break-words font-mono text-sm text-ink-300">{{ mission.label }}</p>
        <p class="mt-3 text-sm leading-relaxed text-ink-400">
          {{ t('It closes with no points and does not come back for the days you choose. What it measures keeps counting in the score: for something slow by design, leave it out instead.') }}
        </p>
        <label class="mt-4 block">
          <span class="label">{{ t('A note for the team (optional)') }}</span>
          <input v-model="note" class="input mt-1" maxlength="500" :placeholder="t('e.g. a one-off import, already over')" />
        </label>
        <label class="mt-4 block">
          <span class="label">{{ t('Quiet for') }}</span>
          <select v-model.number="days" class="input mt-1">
            <option v-for="d in [7, 14, 30, 90]" :key="d" :value="d">{{ t('{n} days', { n: d }) }}</option>
          </select>
        </label>
        <p v-if="failed" class="mt-3 text-sm text-bad" role="alert">{{ failed }}</p>
        <div class="mt-6 flex justify-end gap-2">
          <button type="button" class="btn-ghost" @click="emit('cancel')">{{ t('Cancel') }}</button>
          <button type="submit" class="btn-primary" :disabled="busy">{{ busy ? '…' : t('Wave it away') }}</button>
        </div>
      </form>
    </div>
  </Teleport>
</template>
