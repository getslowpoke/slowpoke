<script setup>
defineProps({ title: String, subtitle: String })
</script>

<template>
  <div class="mb-6 flex flex-wrap items-end justify-between gap-4">
    <div>
      <h1 class="text-2xl font-semibold tracking-tight sm:text-[28px]">{{ title }}</h1>
      <p v-if="subtitle" class="mt-1 text-sm text-ink-400">{{ subtitle }}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2"><slot /></div>
  </div>
</template>
