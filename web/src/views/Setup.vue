<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AuthShell from '../components/AuthShell.vue'
import { api } from '../lib/api.js'
import { refreshSession } from '../lib/session.js'
import { t } from '../lib/i18n.js'

const router = useRouter()
// The installer prints a link that carries the code, so the field is usually already filled.
const form = ref({ setup_code: String(useRoute().query.code || ''), name: '', email: '', password: '' })
const error = ref('')
const busy = ref(false)

async function submit() {
  busy.value = true
  error.value = ''
  try {
    await api.post('/api/setup', form.value)
    await refreshSession()
    router.push('/')
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <AuthShell>
    <h2 class="text-lg font-medium">{{ t('Welcome to Slowpoke') }}</h2>
    <p class="mt-1 text-sm text-ink-400">{{ t('Create the admin account') }}</p>
    <form class="mt-5 space-y-3" @submit.prevent="submit">
      <div>
        <label class="label" for="setup-code">{{ t('Setup code') }}</label>
        <input id="setup-code" v-model="form.setup_code" required class="input mt-1 font-mono uppercase tracking-wider" autocomplete="off" spellcheck="false" placeholder="XXXX-XXXX-XXXX" />
        <p class="mt-1.5 text-xs leading-relaxed text-ink-400">
          {{ t('The installer printed it at the end. Lost it? On the server:') }}
          <code class="mt-1 block rounded border border-ink-700 bg-ink-950 px-2 py-1 font-mono text-[11px] text-ink-200">slowpoke-server setup-code</code>
          <span class="mt-1 block">{{ t('With Docker, in the installation directory:') }}</span>
          <code class="mt-1 block rounded border border-ink-700 bg-ink-950 px-2 py-1 font-mono text-[11px] text-ink-200">docker compose exec server slowpoke-server setup-code</code>
        </p>
      </div>
      <div><label class="label">{{ t('Name') }}</label><input v-model="form.name" class="input mt-1" autocomplete="name" /></div>
      <div><label class="label">{{ t('Email') }}</label><input v-model="form.email" type="email" required class="input mt-1" autocomplete="email" /></div>
      <div>
        <label class="label">{{ t('Password') }}</label>
        <input v-model="form.password" type="password" required minlength="10" class="input mt-1" autocomplete="new-password" />
      </div>
      <p v-if="error" class="text-sm text-bad">{{ error }}</p>
      <button class="btn-primary w-full" :disabled="busy">{{ t('Get started') }} →</button>
    </form>
  </AuthShell>
</template>
