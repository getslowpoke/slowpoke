<script setup>
import { computed } from 'vue'
const props = defineProps({
  values: { type: Array, default: () => [] },
  color: { type: String, default: '#ff6f8e' },
  width: { type: Number, default: 120 },
  height: { type: Number, default: 32 },
})
const path = computed(() => {
  const v = props.values.map((x) => x ?? 0)
  if (v.length < 2) return ''
  const max = Math.max(...v, 1e-9)
  const step = props.width / (v.length - 1)
  return v.map((y, i) => `${i ? 'L' : 'M'}${(i * step).toFixed(1)},${(props.height - 2 - (y / max) * (props.height - 4)).toFixed(1)}`).join(' ')
})
</script>

<template>
  <svg :width="width" :height="height" class="overflow-visible">
    <path v-if="path" :d="path + ` L${width},${height} L0,${height} Z`" :fill="color" fill-opacity="0.08" />
    <path v-if="path" :d="path" fill="none" :stroke="color" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round" />
  </svg>
</template>
