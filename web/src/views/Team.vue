<script setup>
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { useGet } from '../lib/useLoad.js'
import { session } from '../lib/session.js'
import { t, locale, setLocale } from '../lib/i18n.js'
import { ago } from '../lib/format.js'
import State from '../components/State.vue'
import CopyButton from '../components/CopyButton.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'

const router = useRouter()
const { data, error, loading, reload } = useGet(() => '/api/team')
const email = ref('')
const busy = ref(false)
const formError = ref('')
// What happened to the last invitation: sent by email, or a link to pass along by hand.
const result = ref(null)
const removing = ref(null)
const removeBusy = ref(false)
const rowBusy = ref(0)

const members = computed(() => data.value?.users || [])
const invites = computed(() => data.value?.invites || [])

function initial(name) {
  return (String(name || '?').trim()[0] || '?').toUpperCase()
}

async function send(path, target) {
  const res = await api.post(path, { email: target, locale: locale.value })
  result.value = { email: res.invite.email, sent: res.sent, link: res.link, reason: res.mail_error }
  await reload()
}

async function invite() {
  busy.value = true
  formError.value = ''
  result.value = null
  try {
    await send('/api/team/invites', email.value)
    email.value = ''
  } catch (e) {
    formError.value = e.message
  } finally {
    busy.value = false
  }
}

async function resend(inv) {
  rowBusy.value = inv.id
  result.value = null
  try {
    await send(`/api/team/invites/${inv.id}/resend`, inv.email)
  } catch (e) {
    formError.value = e.message
  } finally {
    rowBusy.value = 0
  }
}

async function revoke(inv) {
  rowBusy.value = inv.id
  try {
    await api.del(`/api/team/invites/${inv.id}`)
    if (result.value?.email === inv.email) result.value = null
    await reload()
  } finally {
    rowBusy.value = 0
  }
}

async function remove() {
  removeBusy.value = true
  try {
    await api.del(`/api/team/users/${removing.value.id}`)
    removing.value = null
    await reload()
  } catch (e) {
    formError.value = e.message
    removing.value = null
  } finally {
    removeBusy.value = false
  }
}

async function logout() {
  await api.post('/api/logout')
  session.user = null
  router.push('/login')
}
</script>

<template>
  <div class="mx-auto max-w-3xl px-5 py-10">
    <header class="flex items-center justify-between">
      <router-link to="/" class="flex items-center gap-3">
        <div class="grid h-10 w-10 place-items-center rounded-xl border border-poke-500/30 bg-poke-500/10 text-xl">🐢</div>
        <div class="font-semibold tracking-tight">Slowpoke</div>
      </router-link>
      <div class="flex items-center gap-2 text-sm">
        <button class="btn-ghost !py-1.5 text-xs" @click="setLocale(locale === 'it' ? 'en' : 'it')">{{ locale === 'it' ? 'EN' : 'IT' }}</button>
        <button class="btn-ghost !py-1.5 text-xs" @click="logout">{{ t('Log out') }}</button>
      </div>
    </header>

    <router-link to="/" class="mt-10 inline-block text-sm text-ink-400 hover:text-ink-200">← {{ t('Projects') }}</router-link>
    <h1 class="mt-2 text-3xl font-semibold tracking-tight">{{ t('Team') }}</h1>
    <p class="mt-2 text-sm text-ink-400">{{ t('Everybody in the team sees every project and can invite other people.') }}</p>

    <State :loading="loading" :error="error" @retry="reload">
      <div v-if="data?.demo" class="card card-pad mt-6 text-sm text-ink-400">{{ t('This is the demo: on your own Slowpoke you invite your team from here.') }}</div>
      <form v-else class="card card-pad mt-6" @submit.prevent="invite">
        <div class="font-medium">{{ t('Invite a person') }}</div>
        <p class="mt-1 text-sm text-ink-400">
          {{ data?.mail ? t('They get an email with a link to choose a name and a password.') : t('You get a link to send them: this server has no email set up.') }}
        </p>
        <div class="mt-4 flex flex-col gap-2 sm:flex-row">
          <input v-model="email" type="email" required class="input flex-1" :placeholder="t('name@company.com')" autocomplete="off" />
          <button class="btn-primary shrink-0" :disabled="busy || !email">{{ busy ? '…' : data?.mail ? t('Send invitation') : t('Create invitation') }}</button>
        </div>
        <p v-if="formError" class="mt-3 text-sm text-bad">{{ formError }}</p>

        <div v-if="result?.sent" class="mt-4 rounded-xl border border-good/30 bg-good/10 px-4 py-3 text-sm text-good">
          ✓ {{ t('Invitation sent to {email}. The link works for 7 days.', { email: result.email }) }}
        </div>
        <div v-else-if="result" class="mt-4 rounded-xl border border-ink-700 bg-ink-900 px-4 py-3 text-sm">
          <div class="text-ink-200">
            {{ result.reason ? t('The email to {email} could not be sent. Send them this link yourself:', { email: result.email }) : t('Send this link to {email}:', { email: result.email }) }}
          </div>
          <div class="mt-2 flex items-center gap-2">
            <code class="min-w-0 flex-1 truncate rounded-lg bg-ink-950 px-2.5 py-1.5 font-mono text-xs text-ink-300">{{ result.link }}</code>
            <CopyButton :text="result.link" />
          </div>
          <div class="mt-2 text-xs text-ink-500">{{ t('It works for 7 days and only once.') }}</div>
          <div v-if="result.reason" class="mt-1 break-words font-mono text-xs text-ink-500">{{ result.reason }}</div>
        </div>

        <p class="mt-4 text-sm text-ink-400">
          <router-link to="/email" class="text-poke-400 hover:text-poke-300">{{ data?.mail ? t('Email settings') : t('Set up email so invitations arrive by themselves') }} →</router-link>
        </p>
      </form>

      <section class="mt-8">
        <h2 class="text-sm font-medium uppercase tracking-wide text-ink-500">{{ t('People') }} · {{ members.length }}</h2>
        <ul class="card mt-3 divide-y divide-ink-800">
          <li v-for="m in members" :key="m.id" class="flex items-center gap-3 px-4 py-3">
            <div class="grid h-9 w-9 shrink-0 place-items-center rounded-full border border-poke-500/30 bg-poke-500/10 text-sm font-medium text-poke-300">{{ initial(m.name) }}</div>
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span class="truncate font-medium">{{ m.name }}</span>
                <span v-if="m.id === data.me" class="chip">{{ t('you') }}</span>
              </div>
              <div class="truncate text-xs text-ink-500">{{ m.email }} · {{ t('joined {when}', { when: ago(m.created_at) }) }}</div>
            </div>
            <button v-if="m.id !== data.me && !data.demo" class="btn-ghost !py-1.5 text-xs hover:text-bad" @click="removing = m">{{ t('Remove') }}</button>
          </li>
        </ul>
      </section>

      <section v-if="invites.length" class="mt-8">
        <h2 class="text-sm font-medium uppercase tracking-wide text-ink-500">{{ t('Waiting to join') }} · {{ invites.length }}</h2>
        <ul class="card mt-3 divide-y divide-ink-800">
          <li v-for="inv in invites" :key="inv.id" class="flex flex-wrap items-center gap-3 px-4 py-3">
            <div class="grid h-9 w-9 shrink-0 place-items-center rounded-full border border-dashed border-ink-600 text-sm text-ink-500">✉</div>
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span class="truncate">{{ inv.email }}</span>
                <span v-if="inv.expired" class="chip text-warn">{{ t('expired') }}</span>
              </div>
              <div class="truncate text-xs text-ink-500">
                {{ inv.invited_by ? t('invited by {who} {when}', { who: inv.invited_by, when: ago(inv.created_at) }) : t('invited {when}', { when: ago(inv.created_at) }) }}
                <template v-if="!inv.expired"> · {{ t('expires {when}', { when: ago(inv.expires_at) }) }}</template>
              </div>
            </div>
            <div v-if="!data.demo" class="flex gap-2">
              <button class="btn-ghost !py-1.5 text-xs" :disabled="rowBusy === inv.id" @click="resend(inv)">{{ data.mail ? t('Send again') : t('New link') }}</button>
              <button class="btn-ghost !py-1.5 text-xs hover:text-bad" :disabled="rowBusy === inv.id" @click="revoke(inv)">{{ t('Cancel invitation') }}</button>
            </div>
          </li>
        </ul>
      </section>
    </State>

    <ConfirmDialog
      v-if="removing"
      :title="t('Remove {name} from the team?', { name: removing.name })"
      :confirm-label="t('Remove')"
      :busy="removeBusy"
      @confirm="remove"
      @cancel="removing = null"
    >
      {{ t('{email} is logged out at once and their personal tokens stop working. The missions they took stay as they are.', { email: removing.email }) }}
    </ConfirmDialog>
  </div>
</template>
