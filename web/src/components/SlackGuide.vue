<script setup>
import { computed } from 'vue'
import { t } from '../lib/i18n.js'

// Connecting Slack in three clicks: the app comes prefilled from a manifest (name, description,
// colour, the permission to post), the team picks the channel and pastes the webhook here.
defineProps({ open: Boolean })
const emit = defineEmits(['add'])

const manifest = {
  display_information: {
    name: 'Slowpoke',
    description: 'Missions, bosses and badges from your performance debt',
    background_color: '#1a1016',
  },
  features: { bot_user: { display_name: 'Slowpoke', always_online: false } },
  // incoming-webhook posts in the channel; chat:write lets the bot token send direct messages.
  oauth_config: { scopes: { bot: ['incoming-webhook', 'chat:write'] } },
  settings: { org_deploy_enabled: false, socket_mode_enabled: false, token_rotation_enabled: false },
}
const createUrl = `https://api.slack.com/apps?new_app=1&manifest_json=${encodeURIComponent(JSON.stringify(manifest))}`

const steps = computed(() => [
  { n: 1, title: t('Create the Slowpoke app'), text: t('It opens Slack with everything filled in: choose your workspace and press Create.') },
  { n: 2, title: t('Pick the channel'), text: t('In the app, open Incoming Webhooks, press "Add New Webhook", choose the channel and allow.') },
  { n: 3, title: t('Paste the webhook here'), text: t('Copy the webhook URL, add a Slack channel below, paste it, save and send a test.') },
])
</script>

<template>
  <details class="card group overflow-hidden" :open="open">
    <summary class="flex cursor-pointer list-none items-center gap-3 p-4 sm:p-5">
      <img src="/slowpoke-logo.png" alt="" class="h-10 w-10 rounded-xl" />
      <div class="min-w-0 flex-1">
        <div class="font-medium">{{ t('Connect Slack') }}</div>
        <div class="text-xs text-ink-400">{{ t('Missions won, bosses and badges in your channel, with the Slowpoke logo. Two minutes.') }}</div>
      </div>
      <span class="text-ink-500 transition group-open:rotate-90">›</span>
    </summary>

    <div class="border-t border-ink-800 p-4 sm:p-5">
      <ol class="grid gap-3 md:grid-cols-3">
        <li v-for="s in steps" :key="s.n" class="rounded-xl border border-ink-800 bg-ink-850/50 p-3.5">
          <div class="flex items-center gap-2">
            <span class="grid h-6 w-6 place-items-center rounded-lg bg-poke-500/15 text-xs font-semibold text-poke-300">{{ s.n }}</span>
            <span class="text-sm font-medium">{{ s.title }}</span>
          </div>
          <p class="mt-1.5 text-xs leading-relaxed text-ink-400">{{ s.text }}</p>
        </li>
      </ol>

      <div class="mt-4 flex flex-wrap items-center gap-2">
        <a :href="createUrl" target="_blank" rel="noopener" class="btn-primary">{{ t('Create the app on Slack') }} ↗</a>
        <button class="btn-ghost" @click="emit('add')">+ {{ t('Add a Slack channel') }}</button>
        <a href="/slowpoke-logo-512.png" download="slowpoke-logo.png" class="ml-auto text-xs text-ink-400 underline hover:text-poke-300">{{ t('Download the logo for the app icon') }}</a>
      </div>
      <p class="mt-3 text-xs text-ink-500">{{ t('The webhook URL is a password: whoever has it can post in that channel. Slowpoke keeps it and never shows it again.') }}</p>
    </div>
  </details>
</template>
