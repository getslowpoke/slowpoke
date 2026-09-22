<script setup>
defineProps({ icon: { type: String, default: '🐢' }, title: String, text: String })
</script>

<template>
  <div class="grid place-items-center rounded-2xl border border-dashed border-ink-700 px-6 py-12 text-center">
    <div class="text-4xl">{{ icon }}</div>
    <div class="mt-3 font-medium">{{ title }}</div>
    <p v-if="text" class="mt-1 max-w-md text-sm text-ink-400">{{ text }}</p>
    <div class="mt-4"><slot /></div>
  </div>
</template>
