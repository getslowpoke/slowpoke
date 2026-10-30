import { createApp, watch } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import './style.css'
import { session, refreshSession } from './lib/session.js'
import { applyTitle } from './lib/pageTitle.js'
import { pageCache } from './lib/pageCache.js'

const routes = [
  { path: '/setup', component: () => import('./views/Setup.vue'), meta: { public: true } },
  { path: '/login', component: () => import('./views/Login.vue'), meta: { public: true } },
  { path: '/invite/:token', component: () => import('./views/Invite.vue'), props: true, meta: { public: true } },
  { path: '/', component: () => import('./views/Projects.vue') },
  { path: '/team', component: () => import('./views/Team.vue') },
  { path: '/email', component: () => import('./views/Mail.vue') },
  { path: '/tv/:slug', component: () => import('./views/Tv.vue'), props: true },
  { path: '/projects/:slug/report', component: () => import('./views/DebtReport.vue'), props: true },
  {
    path: '/projects/:slug',
    component: () => import('./views/ProjectLayout.vue'),
    props: true,
    children: [
      { path: '', component: () => import('./views/Overview.vue') },
      { path: 'map', component: () => import('./views/InfraMap.vue') },
      { path: 'slow', component: () => import('./views/Slow.vue') },
      { path: 'queries/:hash', component: () => import('./views/QueryDetail.vue'), props: true },
      { path: 'endpoint', component: () => import('./views/EndpointDetail.vue') },
      { path: 'missions', component: () => import('./views/Missions.vue') },
      { path: 'missions/:id', component: () => import('./views/MissionDetail.vue'), props: true },
      { path: 'deploys', component: () => import('./views/Deploys.vue') },
      { path: 'hosts', component: () => import('./views/Hosts.vue') },
      { path: 'hosts/:id', component: () => import('./views/HostDetail.vue'), props: true },
      { path: 'metrics', component: () => import('./views/Metrics.vue') },
      { path: 'jobs', component: () => import('./views/Jobs.vue') },
      { path: 'settings', component: () => import('./views/Settings.vue') },
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior: () => ({ top: 0 }),
})

router.beforeEach(async (to) => {
  if (!session.checked) await refreshSession()
  if (session.needsSetup && to.path !== '/setup') return '/setup'
  if (!session.needsSetup && to.path === '/setup') return '/'
  if (!to.meta.public && !session.user) return { path: '/login', query: to.fullPath !== '/' ? { next: to.fullPath } : {} }
  if (to.path === '/login' && session.user) return '/'
})

// The tab says where you are: with four of them open, the title is the only label there is.
router.afterEach((to) => applyTitle(to.path))

// Pages remembered for one user are never shown to the next one on the same tab.
watch(() => session.user?.id, (now, before) => before !== undefined && now !== before && pageCache.clear())

window.addEventListener('slowpoke:unauthorized', () => {
  session.user = null
  router.push('/login')
})

// After an upgrade the old page asks for chunks that no longer exist: reload once to get the new version.
function reloadForNewVersion(error) {
  const key = 'slowpoke:chunk-reload'
  let last = 0
  try {
    last = Number(sessionStorage.getItem(key) || 0)
    if (Date.now() - last < 10000) return false
    sessionStorage.setItem(key, String(Date.now()))
  } catch {
    /* storage unavailable: still reload */
  }
  console.warn('Slowpoke was updated, reloading', error)
  window.location.reload()
  return true
}
window.addEventListener('vite:preloadError', (event) => {
  if (reloadForNewVersion(event.payload)) event.preventDefault()
})
router.onError((error, to) => {
  if (/dynamically imported module|Importing a module script failed|Failed to fetch/i.test(String(error?.message))) {
    try {
      sessionStorage.setItem('slowpoke:chunk-target', to.fullPath)
    } catch {
      /* ignore */
    }
    if (!reloadForNewVersion(error)) console.error(error)
  }
})

createApp(App).use(router).mount('#app')
