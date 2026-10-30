<script setup>
import { computed, provide, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { session } from '../lib/session.js'
import { t, locale, setLocale } from '../lib/i18n.js'
import { setProjectName } from '../lib/pageTitle.js'
import { pageCache } from '../lib/pageCache.js'
import { pending } from '../lib/useLoad.js'
import State from '../components/State.vue'
import WelcomeTour from '../components/WelcomeTour.vue'
import { welcome, tourOpen } from '../lib/welcome.js'

const props = defineProps({ slug: String })
const route = useRoute()
const router = useRouter()
const project = ref(null)
const projects = ref([])
const open = ref(false)
const failed = ref(null)

// The project seen last time opens the page at once; the fresh one replaces it when it arrives.
async function load() {
  const key = `layout:${props.slug}`
  const cached = pageCache.recall(key)
  if (cached) apply(cached)
  else if (project.value?.slug !== props.slug) project.value = null
  let p, list
  pending.value++
  try {
    ;[p, list] = await Promise.all([api.get(`/api/projects/${props.slug}`), api.get('/api/projects')])
    failed.value = null
  } catch (e) {
    failed.value = e
    return
  } finally {
    pending.value--
  }
  const fresh = { project: p.project, projects: list.projects }
  pageCache.remember(key, fresh)
  apply(fresh)
}
function apply({ project: p, projects: list }) {
  project.value = p
  projects.value = list
  setProjectName(p?.name) // the tab can only say whose page this is once we know
}
watch(() => props.slug, load, { immediate: true })
// The first time this browser sees a project, the tour explains what it is looking at.
if (welcome.due()) tourOpen.value = true
function closeTour() {
  tourOpen.value = false
  welcome.done()
}
provide('project', project)
provide('reloadProject', load)

const nav = computed(() => [
  { to: '', icon: '◎', label: t('Overview') },
  { to: 'map', icon: '⌘', label: t('Infrastructure') },
  { to: 'slow', icon: '🐢', label: t('Slow things') },
  { to: 'missions', icon: '⚔', label: t('Missions') },
  { to: 'deploys', icon: '⇪', label: t('Deploys') },
  { to: 'hosts', icon: '▦', label: t('Machines') },
  { to: 'metrics', icon: '∿', label: t('Metrics') },
  { to: 'jobs', icon: '⟳', label: t('Jobs & cron') },
  { to: 'settings', icon: '⚙', label: t('Settings') },
])

function active(item) {
  const base = `/projects/${props.slug}`
  const target = item.to ? `${base}/${item.to}` : base
  if (!item.to) return route.path === base
  if (item.to === 'slow') return route.path.startsWith(target) || route.path.includes('/queries/') || route.path.endsWith('/endpoint')
  return route.path.startsWith(target)
}

async function logout() {
  await api.post('/api/logout')
  session.user = null
  router.push('/login')
}
</script>

<template>
  <div class="min-h-screen lg:grid lg:grid-cols-[232px_1fr]">
    <aside class="no-print sticky top-0 z-20 border-b border-ink-800 bg-ink-950/85 backdrop-blur lg:h-screen lg:border-b-0 lg:border-r">
      <div class="flex items-center justify-between px-4 py-4 lg:block">
        <router-link to="/" class="flex items-center gap-2.5">
          <span class="grid h-8 w-8 place-items-center rounded-lg border border-poke-500/30 bg-poke-500/10">🐢</span>
          <span class="font-semibold tracking-tight">Slowpoke</span>
        </router-link>
        <button class="btn-ghost !py-1 text-xs lg:hidden" @click="open = !open">☰</button>
      </div>

      <div class="px-3" :class="open ? 'block' : 'hidden lg:block'">
        <select
          class="input mb-4 cursor-pointer !py-1.5 text-sm"
          :value="slug"
          @change="router.push(`/projects/${$event.target.value}`)"
        >
          <option v-for="p in projects" :key="p.slug" :value="p.slug">{{ p.name }}</option>
        </select>
        <nav class="space-y-0.5">
          <router-link
            v-for="item in nav" :key="item.to" :to="item.to ? `/projects/${slug}/${item.to}` : `/projects/${slug}`"
            class="flex items-center gap-3 rounded-xl px-3 py-2 text-sm transition"
            :class="active(item) ? 'bg-ink-850 text-ink-100 shadow-[inset_0_0_0_1px_var(--color-ink-700)]' : 'text-ink-400 hover:bg-ink-900 hover:text-ink-200'"
            @click="open = false"
          >
            <span class="w-4 text-center text-[13px] opacity-80">{{ item.icon }}</span>{{ item.label }}
          </router-link>
        </nav>
        <div class="mt-6 space-y-1 border-t border-ink-800 pt-4">
          <router-link :to="`/projects/${slug}/report`" class="flex items-center gap-3 rounded-xl px-3 py-2 text-sm text-ink-400 hover:bg-ink-900 hover:text-ink-200">
            <span class="w-4 text-center">❏</span>{{ t('Debt report') }}
          </router-link>
          <router-link :to="`/tv/${slug}`" class="flex items-center gap-3 rounded-xl px-3 py-2 text-sm text-ink-400 hover:bg-ink-900 hover:text-ink-200">
            <span class="w-4 text-center">▣</span>{{ t('TV mode') }}
          </router-link>
          <router-link to="/team" class="flex items-center gap-3 rounded-xl px-3 py-2 text-sm text-ink-400 hover:bg-ink-900 hover:text-ink-200">
            <span class="w-4 text-center">☺</span>{{ t('Team') }}
          </router-link>
          <button class="flex w-full items-center gap-3 rounded-xl px-3 py-2 text-left text-sm text-ink-400 hover:bg-ink-900 hover:text-ink-200" @click="tourOpen = true; open = false">
            <span class="w-4 text-center">✦</span>{{ t('Take the tour') }}
          </button>
        </div>
        <div class="mt-6 flex items-center justify-between px-3 pb-4 text-xs text-ink-500 lg:absolute lg:bottom-0 lg:left-0 lg:right-0 lg:px-6">
          <button class="hover:text-ink-300" @click="setLocale(locale === 'it' ? 'en' : 'it')">{{ locale === 'it' ? 'English' : 'Italiano' }}</button>
          <button class="hover:text-ink-300" @click="logout">{{ t('Log out') }}</button>
        </div>
      </div>
    </aside>

    <div class="loading-bar" :class="{ on: pending > 0 }" aria-hidden="true" />
    <main class="min-w-0 px-4 py-6 sm:px-8 lg:py-8" :aria-busy="pending > 0">
      <State v-if="!project" :loading="!failed" :error="failed" @retry="load" />
      <router-view v-else v-slot="{ Component, route: r }">
        <div :key="r.fullPath" class="page-in">
          <component :is="Component" :project="project" />
        </div>
      </router-view>
    </main>
    <WelcomeTour v-if="tourOpen" :slug="slug" @close="closeTour" />
  </div>
</template>
