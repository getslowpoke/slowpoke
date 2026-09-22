<script setup>
import { t } from '../lib/i18n.js'
</script>

<template>
  <div class="grid min-h-screen place-items-center px-4 py-10">
    <div class="w-full max-w-md">
      <div class="mb-8 text-center">
        <div class="mx-auto grid h-14 w-14 place-items-center rounded-2xl border border-poke-500/30 bg-poke-500/10 text-3xl shadow-[0_0_40px_-10px_rgb(255_111_142/0.5)]">🐢</div>
        <h1 class="mt-4 text-2xl font-semibold tracking-tight">Slowpoke</h1>
        <p class="mt-1 text-sm text-ink-400">{{ t('Find slow code, turn it into missions, level up.') }}</p>
      </div>
      <div class="card card-pad">
        <slot />
      </div>
    </div>
  </div>
</template>
