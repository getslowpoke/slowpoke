<script setup>
import { computed, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { useGet } from '../lib/useLoad.js'
import { session } from '../lib/session.js'
import { t } from '../lib/i18n.js'
import { mailProviders, providerFor } from '../lib/mailProviders.js'
import State from '../components/State.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'

const { data, error, loading, reload } = useGet(() => '/api/mail')
const provider = ref('brevo')
const empty = { provider: 'brevo', host: '', port: '587', username: '', password: '', api_key: '', domain: '', region: '', account_id: '', from: '' }
const form = ref({ ...empty })
const saving = ref(false)
const saveError = ref('')
const saved = ref(false)
const testing = ref(false)
const test = ref(null)
const clearing = ref(false)
const confirmClear = ref(false)

const locked = computed(() => data.value?.source === 'env')
const current = computed(() => mailProviders.find((p) => p.id === provider.value))

// What the panel knows fills the form once; after that the form is the person's.
watch(data, (d) => {
  if (!d) return
  form.value = { ...empty }
  for (const k of Object.keys(empty)) if (d[k]) form.value[k] = d[k]
  const known = providerFor(d)
  if (known) provider.value = known.id
  else pick('brevo')
}, { immediate: true })

function pick(id) {
  const p = mailProviders.find((x) => x.id === id)
  if (!p) return
  const before = provider.value
  provider.value = id
  // A key belongs to one company: switching provider never carries it along.
  if (before !== id) form.value.api_key = ''
  form.value.provider = p.api ? p.id : 'smtp'
  if (!p.api) {
    form.value.host = p.host
    form.value.port = p.port
  }
}

const problems = {
  auth: 'The provider refused the key or the password. Check it, and for Gmail use an app password.',
  sender: 'The provider does not allow sending from this address. Use an address on the domain you verified with them.',
  connect: 'Slowpoke cannot reach the mail server. Check the server name and the port, and that the firewall lets it out.',
  tls: 'The secure connection failed. Port 465 wants TLS from the start, port 587 upgrades it: check that the port matches the provider.',
  other: 'The mail server said no.',
}

async function save() {
  saving.value = true
  saveError.value = ''
  saved.value = false
  test.value = null
  try {
    await api.put('/api/mail', form.value)
    saved.value = true
    await reload()
  } catch (e) {
    saveError.value = e.message
  } finally {
    saving.value = false
  }
}

async function sendTest() {
  testing.value = true
  test.value = null
  try {
    test.value = await api.post('/api/mail/test')
  } catch (e) {
    test.value = { sent: false, problem: 'other', error: e.message }
  } finally {
    testing.value = false
  }
}

async function clear() {
  clearing.value = true
  try {
    await api.del('/api/mail')
    confirmClear.value = false
    test.value = null
    saved.value = false
    await reload()
  } finally {
    clearing.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-3xl px-5 py-10">
    <router-link to="/team" class="inline-block text-sm text-ink-400 hover:text-ink-200">← {{ t('Team') }}</router-link>
    <h1 class="mt-2 text-3xl font-semibold tracking-tight">{{ t('Email') }}</h1>
    <p class="mt-2 text-sm text-ink-400">{{ t('The account team invitations leave from. Without one, you get a link to send yourself.') }}</p>

    <State :loading="loading" :error="error" @retry="reload">
      <div v-if="locked" class="card card-pad mt-6 text-sm">
        <div class="font-medium">✓ {{ t('Set on the server') }}</div>
        <p class="mt-1 text-ink-400">{{ t('This server reads its email account from SLOWPOKE_SMTP_*: change it there and restart.') }}</p>
        <dl class="mt-4 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 font-mono text-xs text-ink-300">
          <dt class="text-ink-500">{{ t('Server') }}</dt><dd>{{ data.host }}:{{ data.port }}</dd>
          <dt class="text-ink-500">{{ t('Sender') }}</dt><dd>{{ data.from }}</dd>
        </dl>
      </div>

      <form v-else class="card card-pad mt-6" @submit.prevent="save">
        <div class="flex items-center justify-between gap-2">
          <div class="font-medium">{{ t('Your email provider') }}</div>
          <span v-if="data?.source === 'panel'" class="chip text-good">✓ {{ t('configured') }}</span>
        </div>
        <div class="mt-3 flex flex-wrap gap-2">
          <button
            v-for="p in mailProviders" :key="p.id" type="button"
            class="rounded-full border px-3 py-1 text-xs transition"
            :class="provider === p.id ? 'border-poke-500/60 bg-poke-500/15 text-poke-300' : 'border-ink-700 text-ink-400 hover:border-ink-600 hover:text-ink-200'"
            @click="pick(p.id)"
          >{{ p.id === 'smtp' ? t('Other SMTP server') : p.name }}</button>
        </div>
        <p v-if="current?.hint" class="mt-3 text-sm text-ink-400">{{ t(current.hint) }}</p>

        <template v-if="current?.api">
          <label class="mt-4 block"><span class="label">{{ t('API key') }}</span><input v-model="form.api_key" type="password" required class="input mt-1 text-sm" autocomplete="new-password" /></label>
          <div v-if="current.needs?.includes('domain')" class="mt-3 grid gap-3 sm:grid-cols-[1fr_9rem]">
            <label class="block"><span class="label">{{ t('Sending domain') }}</span><input v-model="form.domain" required class="input mt-1 font-mono text-sm" placeholder="mg.example.com" autocomplete="off" /></label>
            <label class="block">
              <span class="label">{{ t('Region') }}</span>
              <select v-model="form.region" class="input mt-1 text-sm"><option value="">US</option><option value="eu">EU</option></select>
            </label>
          </div>
          <label v-if="current.needs?.includes('account_id')" class="mt-3 block"><span class="label">{{ t('Account ID') }}</span><input v-model="form.account_id" required class="input mt-1 font-mono text-sm" autocomplete="off" /></label>
        </template>
        <template v-else>
          <div class="mt-4 grid gap-3 sm:grid-cols-[1fr_7rem]">
            <label class="block"><span class="label">{{ t('Server') }}</span><input v-model="form.host" required class="input mt-1 font-mono text-sm" placeholder="smtp.example.com" autocomplete="off" /></label>
            <label class="block"><span class="label">{{ t('Port') }}</span><input v-model="form.port" required inputmode="numeric" class="input mt-1 font-mono text-sm" /></label>
          </div>
          <div class="mt-3 grid gap-3 sm:grid-cols-2">
            <label class="block"><span class="label">{{ t('Username') }}</span><input v-model="form.username" class="input mt-1 text-sm" autocomplete="off" /></label>
            <label class="block"><span class="label">{{ t('Password') }}</span><input v-model="form.password" type="password" class="input mt-1 text-sm" autocomplete="new-password" /></label>
          </div>
        </template>
        <label class="mt-3 block">
          <span class="label">{{ t('Sender') }}</span>
          <input v-model="form.from" required class="input mt-1 text-sm" placeholder="Slowpoke <slowpoke@slowpoke.example.com>" autocomplete="off" />
        </label>
        <p class="mt-2 text-xs text-ink-500">
          {{ t('Tip: send from a subdomain (slowpoke.yourcompany.com) verified only for this. The company mail and its DNS records stay exactly as they are.') }}
        </p>

        <p v-if="saveError" class="mt-3 text-sm text-bad">{{ saveError }}</p>
        <div class="mt-5 flex flex-wrap items-center gap-2">
          <button class="btn-primary" :disabled="saving">{{ saving ? '…' : t('Save') }}</button>
          <button v-if="data?.source === 'panel'" type="button" class="btn-ghost" :disabled="testing" @click="sendTest">{{ testing ? '…' : t('Send a test email') }}</button>
          <button v-if="data?.source === 'panel'" type="button" class="btn-ghost ml-auto hover:text-bad" @click="confirmClear = true">{{ t('Stop sending email') }}</button>
          <span v-if="saved && !test" class="text-sm text-good">✓ {{ t('Saved. Send a test to be sure.') }}</span>
        </div>
      </form>

      <div v-if="locked" class="mt-4">
        <button class="btn-ghost" :disabled="testing" @click="sendTest">{{ testing ? '…' : t('Send a test email') }}</button>
      </div>

      <div v-if="test?.sent" class="mt-4 rounded-xl border border-good/30 bg-good/10 px-4 py-3 text-sm text-good">
        ✓ {{ t('Sent to {email}: check the inbox (and the spam folder, the first time).', { email: test.to || session.user?.email }) }}
      </div>
      <div v-else-if="test" class="mt-4 rounded-xl border border-bad/30 bg-bad/10 px-4 py-3 text-sm">
        <div class="text-bad">{{ t(problems[test.problem] || problems.other) }}</div>
        <div v-if="test.error" class="mt-1 break-words font-mono text-xs text-ink-400">{{ test.error }}</div>
      </div>
    </State>

    <ConfirmDialog
      v-if="confirmClear" :title="t('Stop sending email?')" :confirm-label="t('Stop')" :busy="clearing"
      @confirm="clear" @cancel="confirmClear = false"
    >
      {{ t('The saved account is deleted. Invitations will give you a link to send yourself.') }}
    </ConfirmDialog>
  </div>
</template>
