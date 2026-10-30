import { ref } from 'vue'

// The welcome tour opens once per browser: on the public demo many people log in with the same
// user, so "seen" belongs to the browser, not to the account. Bump the version when the tour changes
// enough to be worth showing again.
const VERSION = 1

export function createWelcome({ storage = safeLocal(), version = VERSION } = {}) {
  const key = `slowpoke:welcome:v${version}`
  let seen = false // memory, for when the storage says no
  return {
    due() {
      if (seen) return false
      try {
        return storage?.getItem(key) === null || storage?.getItem(key) === undefined
      } catch {
        return true
      }
    },
    done() {
      seen = true
      try {
        storage?.setItem(key, String(Date.now()))
      } catch {
        // blocked: memory covers the rest of this visit
      }
    },
  }
}

function safeLocal() {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage
  } catch {
    return null
  }
}

export const welcome = createWelcome()
// Open from anywhere (the sidebar link) without passing events around.
export const tourOpen = ref(false)
