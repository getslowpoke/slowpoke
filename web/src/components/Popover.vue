<script setup>
import { ref, onBeforeUnmount } from 'vue'

// Shows the "content" slot on hover (desktop) or tap (touch), positioned above the trigger.
defineProps({ width: { type: Number, default: 240 }, align: { type: String, default: 'center' } })
const open = ref(false)
const root = ref(null)
let timer = null
function show() {
  clearTimeout(timer)
  open.value = true
}
function hide() {
  timer = setTimeout(() => (open.value = false), 80)
}
function outside(e) {
  if (root.value && !root.value.contains(e.target)) open.value = false
}
document.addEventListener('click', outside)
onBeforeUnmount(() => document.removeEventListener('click', outside))
</script>

<template>
  <div ref="root" class="relative" :class="open ? 'z-50' : ''" @mouseenter="show" @mouseleave="hide" @click="open = !open">
    <slot />
    <transition name="fade">
      <div v-if="open" class="absolute bottom-full z-50 mb-2" :class="align === 'start' ? 'left-0' : align === 'end' ? 'right-0' : 'left-1/2 -translate-x-1/2'" :style="{ width: width + 'px' }" @mouseenter="show" @mouseleave="hide">
        <div class="rounded-2xl border border-ink-600 bg-ink-850 p-3.5 text-left shadow-[0_18px_50px_-12px_rgb(0_0_0/0.8)] backdrop-blur">
          <slot name="content" />
        </div>
        <div class="-mt-1.5 h-3 w-3 rotate-45 border-b border-r border-ink-600 bg-ink-850" :class="align === 'start' ? 'ml-5' : align === 'end' ? 'ml-auto mr-5' : 'mx-auto'" />
      </div>
    </transition>
  </div>
</template>
