<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { useGet } from '../lib/useLoad.js'
import { session } from '../lib/session.js'
import { t, locale, setLocale } from '../lib/i18n.js'
import { num, ago } from '../lib/format.js'
import State from '../components/State.vue'
import Empty from '../components/Empty.vue'
import TimezoneSelect from '../components/TimezoneSelect.vue'

const router = useRouter()
const { data, error, loading, reload } = useGet(() => '/api/projects')
const name = ref('')
const timezone = ref(Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC')
const creating = ref(false)
const createError = ref('')

async function create() {
  creating.value = true
  createError.value = ''
  try {
    const res = await api.post('/api/projects', { name: name.value, timezone: timezone.value })
    router.push(`/projects/${res.project.slug}/hosts`)
  } catch (e) {
    createError.value = e.message
  } finally {
    creating.value = false
  }
}

async function logout() {
  await api.post('/api/logout')
  session.user = null
  router.push('/login')
}
</script>

<template>
  <div class="mx-auto max-w-5xl px-5 py-10">
    <header class="flex items-center justify-between">
      <div class="flex items-center gap-3">
        <div class="grid h-10 w-10 place-items-center rounded-xl border border-poke-500/30 bg-poke-500/10 text-xl">🐢</div>
        <div>
          <div class="font-semibold tracking-tight">Slowpoke</div>
          <div class="text-xs text-ink-500">v{{ session.version }}</div>
        </div>
      </div>
      <div class="flex items-center gap-2 text-sm">
        <button class="btn-ghost !py-1.5 text-xs" @click="setLocale(locale === 'it' ? 'en' : 'it')">{{ locale === 'it' ? 'EN' : 'IT' }}</button>
        <router-link to="/team" class="btn-ghost !py-1.5 text-xs">{{ t('Team') }}</router-link>
        <span class="hidden text-ink-400 sm:inline">{{ session.user?.email }}</span>
        <button class="btn-ghost !py-1.5 text-xs" @click="logout">{{ t('Log out') }}</button>
      </div>
    </header>

    <h1 class="mt-10 text-3xl font-semibold tracking-tight">{{ t('Projects') }}</h1>

    <State :loading="loading" :error="error" @retry="reload">
      <div class="mt-6 grid gap-4 sm:grid-cols-2">
        <router-link
          v-for="p in data?.projects" :key="p.id" :to="`/projects/${p.slug}`"
          class="card card-pad group transition hover:-translate-y-0.5 hover:border-ink-600"
        >
          <div class="flex items-start justify-between">
            <div>
              <div class="text-lg font-medium">{{ p.name }}</div>
              <div class="mt-0.5 font-mono text-xs text-ink-500">{{ p.slug }} · {{ p.timezone }}</div>
            </div>
            <div v-if="p.score !== null && p.score !== undefined" class="text-right">
              <div class="num text-2xl font-semibold" :style="{ color: p.level?.color }">{{ num(p.score, 1) }}</div>
              <div class="text-xs text-ink-400">{{ p.level?.icon }} {{ p.level?.name }}</div>
            </div>
            <div v-else class="chip">{{ t('No data yet') }}</div>
          </div>
          <div class="mt-6 text-xs text-ink-500 transition group-hover:text-poke-400">{{ t('Overview') }} →</div>
        </router-link>

        <form class="card card-pad border-dashed" @submit.prevent="create">
          <div class="text-lg font-medium">{{ t('New project') }}</div>
          <div class="mt-4 space-y-3">
            <input v-model="name" required class="input" placeholder="Acme API" />
            <label class="block">
              <span class="label">{{ t('Timezone') }}</span>
              <TimezoneSelect v-model="timezone" class="mt-1" />
            </label>
            <p v-if="createError" class="text-sm text-bad">{{ createError }}</p>
            <button class="btn-primary w-full" :disabled="creating || !name">{{ t('Create') }}</button>
          </div>
        </form>
      </div>
      <Empty v-if="!data?.projects?.length" class="mt-6" icon="🌱" :title="t('No data yet')" :text="t('Install an agent on your servers and data shows up within a minute.')" />
    </State>
  </div>
</template>
