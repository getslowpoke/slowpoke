<script setup>
import { computed, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { useGet } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { num, latency, duration, compact, ago } from '../lib/format.js'
import ScoreRing from '../components/ScoreRing.vue'
import { activityTitle } from '../lib/activityWords.js'

const props = defineProps({ slug: String })
const { data } = useGet(() => `/api/projects/${props.slug}/overview`, { every: 15000 })
const now = ref(new Date())
setInterval(() => (now.value = new Date()), 1000)
const celebrate = ref(null)
const lastActivity = ref(null)
watch(() => data.value?.activity?.[0]?.id, (id) => {
  if (lastActivity.value && id && id !== lastActivity.value) {
    const a = data.value.activity[0]
    if (['mission_completed', 'boss_defeated', 'level_up', 'badge'].includes(a.type)) {
      celebrate.value = a
      setTimeout(() => (celebrate.value = null), 8000)
    }
  }
  lastActivity.value = id
})
const today = computed(() => data.value?.today || {})
const ringSize = Math.min(380, Math.round(window.innerWidth * 0.7))
</script>

<template>
  <div class="grid min-h-screen grid-rows-[auto_1fr] gap-6 overflow-hidden p-4 sm:p-8">
    <header class="flex items-center justify-between">
      <div class="flex items-center gap-3 text-2xl font-semibold"><span>🐢</span>{{ data?.project?.name || slug }}</div>
      <div class="num text-3xl font-light text-ink-300">{{ now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }}</div>
    </header>
    <div v-if="data" class="grid gap-10 xl:grid-cols-[auto_1fr]">
      <div class="flex flex-col items-center justify-center">
        <ScoreRing :score="today.score ?? null" :color="today.level?.color || '#ff6f8e'" :size="ringSize" :stroke="22" />
        <div class="mt-4 text-center text-3xl font-semibold sm:text-4xl" :style="{ color: today.level?.color }">{{ today.level?.icon }} {{ today.level?.name }}</div>
        <div v-if="today.next_level" class="mt-2 text-xl text-ink-400">{{ num(today.next_level.missing, 1) }} pt → {{ today.next_level.icon }} {{ today.next_level.name }}</div>
      </div>
      <div class="grid content-center gap-6">
        <div class="grid gap-6 md:grid-cols-3">
          <div class="card p-6"><div class="label !text-sm">{{ t('requests/min') }}</div><div class="num mt-2 text-4xl font-semibold xl:text-5xl">{{ compact(data.live.requests_per_minute) }}</div></div>
          <div class="card p-6"><div class="label !text-sm">{{ t('response time') }}</div><div class="num mt-2 text-4xl font-semibold xl:text-5xl">{{ latency(data.live.p95) }}</div></div>
          <div class="card p-6"><div class="label !text-sm">{{ t('Technical debt') }}</div><div class="num mt-2 text-4xl font-semibold text-poke-400 xl:text-5xl">{{ duration(data.debt.seconds_per_day) }}</div></div>
        </div>
        <div class="card p-6">
          <div class="label !text-sm">{{ t('Activity') }}</div>
          <div v-for="a in data.activity.slice(0, 5)" :key="a.id" class="mt-3 flex items-center gap-4 text-lg sm:text-2xl">
            <span>{{ a.icon }}</span><span class="truncate">{{ activityTitle(a) }}</span><span class="ml-auto shrink-0 text-base text-ink-500">{{ ago(a.created_at) }}</span>
          </div>
        </div>
      </div>
    </div>
    <transition name="fade">
      <div v-if="celebrate" class="fixed inset-0 z-50 grid place-items-center bg-ink-950/80 backdrop-blur">
        <div class="text-center">
          <div class="animate-bounce text-[140px]">{{ celebrate.icon || '🎉' }}</div>
          <div class="mt-4 max-w-4xl text-5xl font-semibold">{{ celebrate.title }}</div>
          <div v-if="celebrate.data?.points" class="mt-4 text-4xl text-poke-400">+{{ celebrate.data.points }} pts</div>
        </div>
      </div>
    </transition>
  </div>
</template>
