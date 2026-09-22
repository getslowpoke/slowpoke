<script setup>
import { ref } from 'vue'
import { t } from '../lib/i18n.js'
const props = defineProps({ text: { type: String, required: true }, label: { type: String, default: '' } })
const done = ref(false)
async function copy() {
  try {
    await navigator.clipboard.writeText(props.text)
  } catch {
    const area = document.createElement('textarea')
    area.value = props.text
    document.body.appendChild(area)
    area.select()
    document.execCommand('copy')
    area.remove()
  }
  done.value = true
  setTimeout(() => (done.value = false), 1600)
}
</script>

<template>
  <button class="btn-ghost !px-2.5 !py-1.5 text-xs" @click="copy">
    <span v-if="done">✓ {{ t('Copied') }}</span>
    <span v-else class="inline-flex items-center gap-1.5">
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></svg>
      {{ label || t('Copy') }}
    </span>
  </button>
</template>
