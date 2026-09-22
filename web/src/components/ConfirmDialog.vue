<script setup>
import { t } from '../lib/i18n.js'
defineProps({ title: String, danger: { type: Boolean, default: true }, busy: Boolean, confirmLabel: String })
defineEmits(['confirm', 'cancel'])
</script>

<template>
  <Teleport to="body">
    <div class="fixed inset-0 z-50 grid place-items-center bg-ink-950/80 p-4 backdrop-blur-sm" @click.self="$emit('cancel')">
      <div class="card w-full max-w-md p-6">
        <h2 class="text-lg font-semibold">{{ title }}</h2>
        <div class="mt-2 text-sm leading-relaxed text-ink-300"><slot /></div>
        <div class="mt-6 flex justify-end gap-2">
          <button class="btn-ghost" @click="$emit('cancel')">{{ t('Cancel') }}</button>
          <button class="btn" :class="danger ? 'border border-bad/50 bg-bad/15 text-bad hover:bg-bad/25' : 'btn-primary'" :disabled="busy" @click="$emit('confirm')">
            {{ busy ? '…' : confirmLabel || t('Confirm') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
