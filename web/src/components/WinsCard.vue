<script setup>
// What Slowpoke itself noticed: things that cost less time than in the window before, and
// things that cost more. Nobody has to remember what was slow last week.
import { computed } from 'vue'
import { t } from '../lib/i18n.js'
import { duration } from '../lib/format.js'

const props = defineProps({ wins: Object, title: String, note: String, slug: String })

const icons = { query: '⛁', endpoint: '⇄', php: '🐘', metric: '∿' }
const improved = computed(() => props.wins?.improved || [])
const worse = computed(() => props.wins?.worse || [])
const net = computed(() => props.wins?.saved_seconds_per_day || 0)
const linkOf = (item) => (item.kind === 'query' ? `/projects/${props.slug}/queries/${item.target}` : null)
</script>

<template>
  <section v-if="wins?.comparable && (improved.length || worse.length)" class="card p-5">
    <div class="flex flex-wrap items-baseline justify-between gap-2">
      <div class="label">🏆 {{ title || t('Wins') }}</div>
      <div class="text-sm font-medium" :class="net >= 0 ? 'text-good' : 'text-bad'">
        {{ net >= 0 ? t('{time} of waiting saved per day', { time: duration(net) }) : t('{time} of waiting added per day', { time: duration(net) }) }}
      </div>
    </div>
    <p v-if="note" class="mt-0.5 text-xs text-ink-500">{{ note }}</p>

    <div class="mt-4 grid gap-5 md:grid-cols-2">
      <div>
        <div class="label mb-2 !text-good">{{ t('Better') }}</div>
        <ul class="space-y-2">
          <li v-for="item in improved" :key="item.kind + item.target" class="flex items-start gap-2 text-sm">
            <span class="shrink-0">{{ icons[item.kind] || '•' }}</span>
            <component :is="linkOf(item) ? 'router-link' : 'span'" :to="linkOf(item)" class="min-w-0 flex-1 truncate" :class="item.kind === 'query' ? 'font-mono text-[12px] text-ink-300 hover:text-poke-300' : 'text-ink-300'" :title="item.label">
              {{ item.label }}
            </component>
            <span class="num shrink-0 text-good">−{{ duration(item.saved_seconds_per_day) }}<span class="text-ink-500">/{{ t('day') }}</span></span>
          </li>
          <li v-if="!improved.length" class="text-sm text-ink-500">{{ t('Nothing got faster yet.') }}</li>
        </ul>
      </div>
      <div>
        <div class="label mb-2 !text-bad">{{ t('Worse') }}</div>
        <ul class="space-y-2">
          <li v-for="item in worse" :key="item.kind + item.target" class="flex items-start gap-2 text-sm">
            <span class="shrink-0">{{ icons[item.kind] || '•' }}</span>
            <component :is="linkOf(item) ? 'router-link' : 'span'" :to="linkOf(item)" class="min-w-0 flex-1 truncate" :class="item.kind === 'query' ? 'font-mono text-[12px] text-ink-300 hover:text-poke-300' : 'text-ink-300'" :title="item.label">
              {{ item.label }}
              <span v-if="item.new" class="chip ml-1 !py-0 !text-[10px]">{{ t('new') }}</span>
            </component>
            <span class="num shrink-0 text-bad">+{{ duration(item.saved_seconds_per_day) }}<span class="text-ink-500">/{{ t('day') }}</span></span>
          </li>
          <li v-if="!worse.length" class="text-sm text-ink-500">{{ t('Nothing got slower: keep it up.') }}</li>
        </ul>
      </div>
    </div>
  </section>
</template>
