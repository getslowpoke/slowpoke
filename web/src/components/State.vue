<script setup>
import { t } from '../lib/i18n.js'
defineProps({ loading: Boolean, error: { type: Object, default: null } })
defineEmits(['retry'])
</script>

<template>
  <div v-if="loading" class="space-y-4">
    <div class="h-8 w-48 animate-pulse rounded-lg bg-ink-800" />
    <div class="grid gap-4 md:grid-cols-3">
      <div v-for="i in 3" :key="i" class="h-36 animate-pulse rounded-2xl border border-ink-800 bg-ink-850" />
    </div>
    <div class="h-64 animate-pulse rounded-2xl border border-ink-800 bg-ink-850" />
  </div>
  <div v-else-if="error" class="card card-pad max-w-lg">
    <div class="text-lg font-medium">{{ t('Something went wrong') }}</div>
    <p class="mt-1 text-sm text-ink-400">{{ error.message }}</p>
    <button class="btn-ghost mt-4" @click="$emit('retry')">{{ t('Retry') }}</button>
  </div>
  <slot v-else />
</template>
