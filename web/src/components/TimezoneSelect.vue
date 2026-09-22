<script setup>
import { computed } from 'vue'

// A select of IANA time zones grouped by region, each with its current UTC offset, so nobody
// has to type "Europe/Rome" by hand. The current value is kept even if the browser lacks it.
const model = defineModel({ type: String, default: 'UTC' })

function offset(zone) {
  try {
    const part = new Intl.DateTimeFormat('en-US', { timeZone: zone, timeZoneName: 'longOffset' })
      .formatToParts(new Date()).find((p) => p.type === 'timeZoneName')
    return part ? part.value.replace('GMT', 'UTC') : ''
  } catch {
    return ''
  }
}

const groups = computed(() => {
  let zones = []
  try {
    zones = Intl.supportedValuesOf('timeZone')
  } catch {
    zones = []
  }
  const all = new Set(['UTC', ...zones])
  if (model.value) all.add(model.value)
  const byRegion = new Map()
  for (const zone of [...all].sort()) {
    const region = zone.includes('/') ? zone.split('/')[0] : 'UTC'
    if (!byRegion.has(region)) byRegion.set(region, [])
    const off = offset(zone)
    byRegion.get(region).push({ zone, label: `${zone.replaceAll('_', ' ')}${off && off !== 'UTC' ? ` (${off})` : ''}` })
  }
  return [...byRegion.entries()]
})
</script>

<template>
  <select v-model="model" class="input font-mono text-xs">
    <optgroup v-for="[region, zones] in groups" :key="region" :label="region">
      <option v-for="z in zones" :key="z.zone" :value="z.zone">{{ z.label }}</option>
    </optgroup>
  </select>
</template>
