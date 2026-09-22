<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AuthShell from '../components/AuthShell.vue'
import { api } from '../lib/api.js'
import { refreshSession, session } from '../lib/session.js'
import { t } from '../lib/i18n.js'

const route = useRoute()
const router = useRouter()
const form = ref({ email: '', password: '' })
const error = ref('')
const busy = ref(false)
// Slowpoke sends no email, so there is no reset link to click: the way back in is on the server.
const forgotten = ref(false)

async function submit() {
  busy.value = true
  error.value = ''
  try {
    await api.post('/api/login', form.value)
    await refreshSession()
    const next = typeof route.query.next === 'string' && route.query.next.startsWith('/') ? route.query.next : '/'
    router.push(next)
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <AuthShell>
    <!-- A public demo: the way in is the first thing on the page, not something to guess. -->
    <div v-if="session.demo" class="mb-5 rounded-xl border border-poke-500/30 bg-poke-500/10 p-4 text-sm">
      <div class="font-medium text-ink-100">{{ t('This is the public demo') }}</div>
      <p class="mt-1 text-ink-300">{{ t('Thirty days of a small online shop. Change anything: it is rebuilt every night.') }}</p>
      <a :href="session.demo.url" class="btn-primary mt-3 w-full justify-center">{{ t('Enter the demo') }}</a>
      <dl class="mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs text-ink-400">
        <dt>{{ t('Email') }}</dt><dd class="font-mono text-ink-200">{{ session.demo.email }}</dd>
        <dt>{{ t('Password') }}</dt><dd class="font-mono text-ink-200">{{ session.demo.password }}</dd>
      </dl>
    </div>
    <form class="space-y-3" @submit.prevent="submit">
      <div><label class="label">{{ t('Email') }}</label><input v-model="form.email" type="email" required class="input mt-1" autocomplete="email" autofocus /></div>
      <div>
        <label class="label">{{ t('Password') }}</label>
        <input v-model="form.password" type="password" required class="input mt-1" autocomplete="current-password" />
      </div>
      <p v-if="error" class="text-sm text-bad">{{ error }}</p>
      <button class="btn-primary w-full" :disabled="busy">{{ t('Log in') }}</button>
    </form>
    <p class="mt-4 text-center text-xs text-ink-500">
      <button type="button" class="underline underline-offset-2 hover:text-ink-200" @click="forgotten = !forgotten">
        {{ t('Forgotten your password?') }}
      </button>
    </p>
    <p v-if="forgotten" class="mt-2 text-xs leading-relaxed text-ink-400">
      {{ t('Slowpoke sends no email, so there is nothing to click here. On the server, as root:') }}
      <code class="mt-1 block rounded border border-ink-700 bg-ink-950 px-2 py-1 font-mono text-[11px] text-ink-200">slowpoke-server set-password -email {{ form.email || 'you@example.com' }}</code>
      {{ t('It asks for the new password twice and closes every open session of that account.') }}
    </p>
  </AuthShell>
</template>
