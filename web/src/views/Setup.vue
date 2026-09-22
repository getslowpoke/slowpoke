<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import AuthShell from '../components/AuthShell.vue'
import { api } from '../lib/api.js'
import { refreshSession } from '../lib/session.js'
import { t } from '../lib/i18n.js'

const router = useRouter()
const form = ref({ name: '', email: '', password: '' })
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
