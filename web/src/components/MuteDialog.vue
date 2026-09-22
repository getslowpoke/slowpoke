<script setup>
import { ref } from 'vue'
import { api } from '../lib/api.js'
import { t } from '../lib/i18n.js'

// One dialog for everything the team can mute: a machine, a piece of the doctor's advice.
// The note is what the next person reads before unmuting it.
const props = defineProps({ title: String, subject: String, explain: String, placeholder: String, url: String, payload: { type: Object, default: () => ({}) } })
const emit = defineEmits(['done', 'cancel'])
const note = ref('')
const busy = ref(false)
const failed = ref('')

async function save() {
  busy.value = true
  failed.value = ''
  try {
    emit('done', await api.post(props.url, { ...props.payload, note: note.value }))
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
        <h2 class="text-lg font-semibold">{{ title }}</h2>
        <p v-if="subject" class="mt-1 break-words font-mono text-sm text-ink-300">{{ subject }}</p>
        <p class="mt-3 text-sm leading-relaxed text-ink-400">{{ explain }}</p>
        <label class="mt-4 block">
          <span class="label">{{ t('Why (the team reads it before turning it back on)') }}</span>
          <input v-model="note" class="input mt-1" maxlength="500" :placeholder="placeholder" />
        </label>
        <p v-if="failed" class="mt-3 text-sm text-bad" role="alert">{{ failed }}</p>
        <div class="mt-6 flex justify-end gap-2">
          <button type="button" class="btn-ghost" @click="emit('cancel')">{{ t('Cancel') }}</button>
          <button type="submit" class="btn-primary" :disabled="busy">{{ busy ? '…' : t('Mute') }}</button>
        </div>
      </form>
    </div>
  </Teleport>
</template>
