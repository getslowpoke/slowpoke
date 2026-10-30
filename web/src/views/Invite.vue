<script setup>
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import AuthShell from '../components/AuthShell.vue'
import { api } from '../lib/api.js'
import { refreshSession } from '../lib/session.js'
import { t } from '../lib/i18n.js'

const props = defineProps({ token: { type: String, required: true } })
const router = useRouter()
const invite = ref(null)
const gone = ref(false)
const loadError = ref('')
const form = ref({ name: '', password: '' })
const error = ref('')
const busy = ref(false)

onMounted(async () => {
  try {
    invite.value = await api.get(`/api/invites/${encodeURIComponent(props.token)}`)
  } catch (e) {
    if (e.status === 410) gone.value = true
    else loadError.value = e.message
  }
})

async function accept() {
  busy.value = true
  error.value = ''
  try {
    await api.post(`/api/invites/${encodeURIComponent(props.token)}/accept`, form.value)
    await refreshSession()
    router.push('/')
  } catch (e) {
    if (e.status === 410) gone.value = true
    else error.value = e.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <AuthShell>
    <template v-if="gone">
      <h2 class="text-lg font-medium">{{ t('This invitation no longer works') }}</h2>
      <p class="mt-2 text-sm text-ink-400">{{ t('It was already used, it expired or it was cancelled. Ask whoever invited you for a new one.') }}</p>
      <router-link to="/login" class="btn-ghost mt-5 w-full justify-center">{{ t('Log in') }}</router-link>
    </template>
    <p v-else-if="loadError" class="text-sm text-bad">{{ loadError }}</p>
    <p v-else-if="!invite" class="text-sm text-ink-400">…</p>
    <template v-else>
      <h2 class="text-lg font-medium">
        {{ invite.invited_by ? t('{who} invited you to the team', { who: invite.invited_by }) : t('You are invited to the team') }}
      </h2>
      <p class="mt-1 text-sm text-ink-400">{{ t('Choose a name and a password to join.') }}</p>
      <form class="mt-5 space-y-3" @submit.prevent="accept">
        <div><label class="label">{{ t('Email') }}</label><input :value="invite.email" disabled class="input mt-1 opacity-70" /></div>
        <div><label class="label">{{ t('Name') }}</label><input v-model="form.name" required class="input mt-1" autocomplete="name" /></div>
        <div>
          <label class="label">{{ t('Password') }}</label>
          <input v-model="form.password" type="password" required minlength="10" class="input mt-1" autocomplete="new-password" />
          <p class="mt-1 text-xs text-ink-500">{{ t('At least 10 characters.') }}</p>
        </div>
        <p v-if="error" class="text-sm text-bad">{{ error }}</p>
        <button class="btn-primary w-full" :disabled="busy">{{ t('Join the team') }} →</button>
      </form>
    </template>
  </AuthShell>
</template>
