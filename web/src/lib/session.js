import { reactive } from 'vue'
import { api } from './api.js'
import { setServerLocale } from './i18n.js'

export const session = reactive({ user: null, checked: false, needsSetup: false, version: '', demo: null })

export async function refreshSession() {
  try {
    const me = await api.get('/api/me')
    session.user = me.user
    session.version = me.version
    setServerLocale(me.locale)
    session.needsSetup = false
  } catch {
    session.user = null
    try {
      const s = await api.get('/api/setup')
      session.needsSetup = s.needs_setup
      session.version = s.version
      session.demo = s.demo || null // a public demo says how to get in
      setServerLocale(s.locale) // anche la pagina di accesso si apre nella lingua giusta
    } catch {
      session.needsSetup = false
    }
  }
  session.checked = true
}
