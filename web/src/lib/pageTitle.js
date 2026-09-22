import { t } from './i18n.js'

/**
 * What the browser tab says. The name of the page is the one in the sidebar, the project is the
 * one you are looking at: with four tabs open on three projects, the tab has to answer both
 * "where" and "whose" without being read twice.
 */
export const pageNames = {
  '': 'Overview',
  map: 'Infrastructure',
  slow: 'Slow things',
  queries: 'Query',
  endpoint: 'Endpoint',
  missions: 'Missions',
  deploys: 'Deploys',
  hosts: 'Machines',
  metrics: 'Metrics',
  jobs: 'Jobs & cron',
  settings: 'Settings',
  report: 'Debt report',
  tv: 'TV mode',
  login: 'Log in',
  setup: 'Welcome',
  projects: 'Projects',
}

/** The key of the page a path leads to, from the segment that names it. */
export function pageKey(path) {
  const parts = String(path || '/').split('?')[0].split('/').filter(Boolean)
  if (parts.length === 0) return 'projects'
  // A page nobody planned for has no name: better a bare "Slowpoke" than the wrong word.
  if (parts[0] !== 'projects') return parts[0] in pageNames ? parts[0] : null
  // /projects/<slug>[/<page>[/...]]
  return parts.length < 3 ? '' : parts[2]
}

/** The slug in the path, when the path is inside a project. */
export function projectSlug(path) {
  const parts = String(path || '/').split('?')[0].split('/').filter(Boolean)
  if (parts[0] === 'projects' && parts[1]) return parts[1]
  if (parts[0] === 'tv' && parts[1]) return parts[1]
  return ''
}

/** "Slow things · Negozio · Slowpoke", with whatever of the three is known. */
export function titleFor(path, project = '') {
  const key = pageKey(path)
  const name = pageNames[key] ? t(pageNames[key]) : ''
  const inProject = projectSlug(path) ? project || '' : ''
  return [name, inProject, 'Slowpoke'].filter(Boolean).join(' · ')
}

let project = ''

/** The project of the page being shown: the layout knows it only once it has loaded. */
export function setProjectName(name) {
  project = String(name || '')
  applyTitle(window.location.pathname)
}

export function applyTitle(path) {
  document.title = titleFor(path, project)
}
