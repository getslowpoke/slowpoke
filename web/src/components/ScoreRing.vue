<script setup>
import { computed } from 'vue'
import { num } from '../lib/format.js'

const props = defineProps({
  score: { type: Number, default: null },
  color: { type: String, default: '#ff6f8e' },
  size: { type: Number, default: 180 },
  stroke: { type: Number, default: 12 },
  label: { type: String, default: '' },
})

const r = computed(() => (props.size - props.stroke) / 2)
const c = computed(() => 2 * Math.PI * r.value)
const offset = computed(() => c.value * (1 - Math.max(0, Math.min(100, props.score ?? 0)) / 100))
const gid = `ring-${Math.random().toString(36).slice(2)}`
</script>

<template>
  <div class="relative inline-grid place-items-center" :style="{ width: size + 'px', height: size + 'px' }">
    <svg :width="size" :height="size" class="-rotate-90">
      <defs>
        <linearGradient :id="gid" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" :stop-color="color" stop-opacity="0.65" />
          <stop offset="100%" :stop-color="color" />
        </linearGradient>
      </defs>
      <circle :cx="size / 2" :cy="size / 2" :r="r" fill="none" stroke="var(--color-ink-800)" :stroke-width="stroke" />
      <circle
        :cx="size / 2" :cy="size / 2" :r="r" fill="none" :stroke="`url(#${gid})`" :stroke-width="stroke" stroke-linecap="round"
        :stroke-dasharray="c" :stroke-dashoffset="offset"
        style="transition: stroke-dashoffset 1.1s cubic-bezier(0.22, 1, 0.36, 1); filter: drop-shadow(0 0 10px rgb(255 111 142 / 0.18))"
      />
    </svg>
    <div class="absolute inset-0 grid place-items-center text-center">
      <div>
        <div class="num font-semibold tracking-tight" :style="{ fontSize: size * 0.24 + 'px', color }">{{ score === null ? '—' : num(score, 1) }}</div>
        <div v-if="label" class="mt-0.5 text-xs text-ink-400">{{ label }}</div>
      </div>
    </div>
  </div>
</template>
