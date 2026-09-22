<script setup>
defineProps({ level: { type: String, required: true } })
const styles = {
  critical: 'bg-bad/12 text-bad border-bad/30',
  warning: 'bg-warn/10 text-warn border-warn/30',
  info: 'bg-info/10 text-info border-info/30',
}
</script>

<template>
  <span class="inline-flex items-center rounded-full border px-2 py-0.5 text-[11px] font-medium" :class="styles[level] || styles.info">{{ level }}</span>
</template>
