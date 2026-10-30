<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { t } from '../lib/i18n.js'
import ScoreRing from './ScoreRing.vue'

// Four slides, one idea each: a big picture, a headline, one line. It sells the game in ten
// seconds and ends on a real mission, not on a wall of text. Opened once per browser.
const props = defineProps({ slug: String })
const emit = defineEmits(['close'])
const router = useRouter()
const i = ref(0)
const direction = ref(1)

const slides = computed(() => [
  { key: 'price', title: t('Slow code has a price.'), text: t('Seconds of waiting per day, turned into a game.') },
  { key: 'score', title: t('One score a day.'), text: t('Climb from Bronze to Platinum.') },
  { key: 'bosses', title: t('Beat the bosses.'), text: t('Fix the slowest things, earn points and badges.') },
  { key: 'go', title: t('Your first mission is waiting.'), text: t('Your coding assistant can take it too, through MCP.') },
])
const slide = computed(() => slides.value[i.value])
const last = computed(() => i.value === slides.value.length - 1)

// Numbers that move: the waiting counts up, the ring fills. Reduced motion gets the final values.
const still = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches
const waiting = ref(0)
const ring = ref(0)
let frame = 0
function countUp(target, to, ms) {
  cancelAnimationFrame(frame)
  if (still) return (target.value = to)
  const start = performance.now()
  const step = (now) => {
    const p = Math.min(1, (now - start) / ms)
    target.value = to * (1 - Math.pow(1 - p, 3))
    if (p < 1) frame = requestAnimationFrame(step)
  }
  target.value = 0
  frame = requestAnimationFrame(step)
}
watch(() => slide.value.key, (key) => {
  if (key === 'price') countUp(waiting, 6120, 1400)
  if (key === 'score') countUp(ring, 72.4, 1200)
}, { immediate: true })
const waitingText = computed(() => `${Math.floor(waiting.value / 3600)}h ${String(Math.floor((waiting.value % 3600) / 60)).padStart(2, '0')}m`)

function go(n) {
  const next = Math.max(0, Math.min(slides.value.length - 1, n))
  direction.value = next >= i.value ? 1 : -1
  i.value = next
}
function finish() {
  emit('close')
  if (props.slug) router.push(`/projects/${props.slug}/missions`)
}
function onKey(e) {
  if (e.key === 'Escape') emit('close')
  else if (e.key === 'ArrowRight') last.value ? finish() : go(i.value + 1)
  else if (e.key === 'ArrowLeft') go(i.value - 1)
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  cancelAnimationFrame(frame)
})
</script>

<template>
  <Teleport to="body">
    <div class="tour-backdrop fixed inset-0 z-50 grid place-items-center bg-ink-950/80 p-4 backdrop-blur-md" @click.self="emit('close')">
      <div class="tour-card card relative w-full max-w-md overflow-hidden" role="dialog" aria-modal="true" :aria-label="t('Welcome to Slowpoke')">
        <button class="absolute right-3 top-3 z-10 rounded-lg px-2 py-1 text-xs text-ink-500 hover:text-ink-100" @click="emit('close')">{{ t('Skip') }}</button>

        <div class="relative grid h-72 place-items-center">
          <div class="tour-glow pointer-events-none absolute left-1/2 top-1/2 h-80 w-80 -translate-x-1/2 -translate-y-1/2 rounded-full blur-3xl"
            :class="slide.key === 'bosses' ? 'bg-bad/25' : slide.key === 'score' ? 'bg-ink-400/20' : 'bg-poke-500/25'" />

          <div :key="slide.key" class="tour-art relative" :class="direction > 0 ? 'from-right' : 'from-left'">
            <div v-if="slide.key === 'price'" class="flex flex-col items-center">
              <div class="tour-float text-7xl drop-shadow-[0_0_40px_rgb(255_111_142/0.6)]">🐢</div>
              <div class="num mt-4 text-5xl font-semibold tracking-tight text-poke-300">{{ waitingText }}</div>
              <div class="mt-1 text-sm text-ink-400">{{ t('of waiting per day') }}</div>
            </div>

            <div v-else-if="slide.key === 'score'" class="flex flex-col items-center">
              <ScoreRing :score="ring" color="#94a3b8" :size="170" :stroke="14" />
              <div class="mt-3 flex items-center gap-2 text-2xl">
                <span class="opacity-40">🥉</span><span class="tour-pop text-3xl" style="animation-delay: 1s">🥈</span><span class="opacity-25 grayscale">🥇</span><span class="opacity-25 grayscale">💎</span>
              </div>
            </div>

            <div v-else-if="slide.key === 'bosses'" class="flex flex-col items-center">
              <div class="tour-shake text-7xl drop-shadow-[0_0_40px_rgb(255_122_122/0.7)]">👾</div>
              <div class="mt-6 flex gap-3 text-2xl">
                <span v-for="(b, n) in ['⚔️', '🏆', '🔥', '✂️']" :key="b" class="tour-pop grid h-12 w-12 place-items-center rounded-2xl border border-ink-700 bg-ink-850"
                  :style="{ animationDelay: `${0.25 + n * 0.12}s` }">{{ b }}</span>
              </div>
            </div>

            <div v-else class="w-80 max-w-[74vw]">
              <div class="tour-pop card flex items-center gap-3 !border-poke-500/40 p-4 shadow-[0_0_50px_-15px_rgb(255_111_142/0.7)]">
                <span class="grid h-10 w-10 shrink-0 place-items-center rounded-xl border border-ink-700 bg-ink-850 text-lg">⛁</span>
                <div class="min-w-0 flex-1">
                  <div class="truncate font-mono text-xs text-ink-100">select * from orders where…</div>
                  <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-ink-800"><div class="tour-progress h-full rounded-full bg-gradient-to-r from-poke-600 to-poke-400" /></div>
                </div>
                <span class="num text-lg font-semibold text-poke-400">+85</span>
              </div>
              <div class="tour-pop mt-3 flex justify-center" style="animation-delay: 0.35s">
                <span class="chip !border-poke-500/30 !text-xs">🤖 MCP</span>
              </div>
            </div>
          </div>
        </div>

        <div class="relative px-7 pb-7 text-center">
          <div :key="slide.key" class="tour-text">
            <h2 class="text-2xl font-semibold tracking-tight">{{ slide.title }}</h2>
            <p class="mt-1.5 text-sm text-ink-400">{{ slide.text }}</p>
          </div>

          <button v-if="!last" class="btn-primary mt-6 w-full !py-2.5" @click="go(i + 1)">{{ t('Next') }} →</button>
          <button v-else class="btn-primary tour-cta mt-6 w-full !py-2.5" @click="finish">{{ t('Show me') }} →</button>

          <div class="mt-5 flex justify-center gap-1.5">
            <button v-for="(s, n) in slides" :key="s.key" class="h-1.5 rounded-full transition-all duration-300"
              :class="n === i ? 'w-6 bg-poke-400' : 'w-1.5 bg-ink-700 hover:bg-ink-500'" :aria-label="`${n + 1} / ${slides.length}`" @click="go(n)" />
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>
