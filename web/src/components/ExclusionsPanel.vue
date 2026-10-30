<script setup>
import { ref } from 'vue'
import { api } from '../lib/api.js'
import { useGet } from '../lib/useLoad.js'
import { t } from '../lib/i18n.js'
import { duration, dateTime } from '../lib/format.js'
import { kindWords, reasonInWords } from '../lib/exclusions.js'

const props = defineProps({ slug: String })
const { data, reload } = useGet(() => `/api/projects/${props.slug}/exclusions`)
const removing = ref(0)

async function remove(e) {
  removing.value = e.id
  try {
    await api.del(`/api/projects/${props.slug}/exclusions/${e.id}`)
    await reload()
  } finally {
    removing.value = 0
  }
}
</script>

<template>
  <section class="card card-pad">
    <div class="label">🙈 {{ t('Left out of the score') }}</div>
    <p class="mt-1 text-xs text-ink-500">
      {{ t('They do not count in the speed index, the debt or the missions. Leave something out from Slow things or from its regression.') }}
    </p>
    <p v-if="!data?.exclusions?.length" class="mt-3 text-sm text-ink-400">{{ t('None: everything counts.') }}</p>
    <div v-else class="mt-3 divide-y divide-ink-800">
      <div v-for="e in data.exclusions" :key="e.id" class="flex flex-wrap items-center justify-between gap-3 py-3" :class="e.active ? '' : 'opacity-60'">
        <div class="min-w-0">
          <div class="truncate font-mono text-[13px] text-ink-100">{{ e.target }}</div>
          <div class="mt-0.5 text-[11px] uppercase tracking-wider text-ink-500">{{ t(kindWords[e.kind] || e.kind) }}</div>
          <div class="mt-0.5 text-xs text-ink-400">
            {{ reasonInWords(e.reason) }}<span v-if="e.note"> · {{ e.note }}</span>
          </div>
          <div class="mt-0.5 text-[11px] text-ink-500">
            <span v-if="e.created_by">{{ e.created_by }} · </span>
            <span v-if="!e.active">{{ t('expired') }}</span>
            <span v-else-if="e.until">{{ t('until {date}', { date: dateTime(e.until) }) }}</span>
            <span v-else>{{ t('until we remove it') }}</span>
          </div>
        </div>
        <div class="flex shrink-0 items-center gap-3">
          <span v-if="e.active" class="num text-sm text-ink-300" :title="t('What it still costs, out of the debt.')">{{ duration(e.seconds_per_day) }}/{{ t('day') }}</span>
          <button class="btn-ghost !py-1 text-xs" :disabled="removing === e.id" @click="remove(e)">{{ t('Count it again') }}</button>
        </div>
      </div>
    </div>
  </section>
</template>
