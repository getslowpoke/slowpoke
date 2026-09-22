<script setup>
defineProps({ modelValue: String, options: { type: Array, default: () => ['1h', '24h', '7d', '30d'] } })
defineEmits(['update:modelValue'])
</script>

<template>
  <div class="inline-flex rounded-xl border border-ink-700 bg-ink-900 p-0.5">
    <button v-for="o in options" :key="o" class="rounded-[10px] px-3 py-1 text-xs font-medium transition"
      :class="modelValue === o ? 'bg-ink-700 text-ink-100' : 'text-ink-400 hover:text-ink-200'" @click="$emit('update:modelValue', o)">{{ o }}</button>
  </div>
</template>
