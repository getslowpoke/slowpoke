<script setup>
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { t } from '../lib/i18n.js'

// Opens a mission by hand on a slow thing, or leads to the one already open: the mission page
// carries the full brief and the command for the coding assistant.
const props = defineProps({
  slug: String,
  kind: String,
  target: String,
  missions: { type: Array, default: () => [] },
})
const router = useRouter()
const busy = ref(false)
const failed = ref('')
const open = computed(() => props.missions.find((m) => m.status === 'open'))

async function make() {
  busy.value = true
  failed.value = ''
  try {
    const res = await api.post(`/api/projects/${props.slug}/missions`, { kind: props.kind, target: props.target })
    router.push(`/projects/${props.slug}/missions/${res.id}`)
  } catch (e) {
    failed.value = e.message
    busy.value = false
  }
}
</script>

<template>
  <router-link v-if="open" :to="`/projects/${slug}/missions/${open.id}`" class="btn-ghost !py-1 text-xs">🎯 {{ t('Go to the mission') }}</router-link>
  <button v-else type="button" class="btn-primary !py-1 text-xs" :disabled="busy" :title="t('Opens a mission with everything known about it and a brief for your coding assistant')" @click.stop.prevent="make">
    🎯 {{ busy ? '…' : t('Make it a mission') }}
  </button>
  <span v-if="failed" class="text-xs text-bad" role="alert">{{ failed }}</span>
</template>
