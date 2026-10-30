// The last answer of each page, shown at once while the fresh one loads (stale-while-revalidate).
// Some pages take seconds to compute on a busy install: a blank screen for that long reads as broken.
// sessionStorage keeps it across a reload of the tab and forgets it with the tab; memory is the fallback
// when the storage is full or blocked.
const KEY = 'slowpoke:pages'

export function createPageCache({ storage = safeSession(), now = Date.now, max = 20, maxAge = 30 * 60_000 } = {}) {
  let entries = read(storage) // key -> { at, data }, oldest first

  function recall(key) {
    const e = entries[key]
    if (!e || now() - e.at > maxAge) return undefined
    return e.data
  }

  function remember(key, data) {
    delete entries[key]
    entries[key] = { at: now(), data }
    const keys = Object.keys(entries)
    for (const k of keys.slice(0, Math.max(0, keys.length - max))) delete entries[k]
    try {
      storage?.setItem(KEY, JSON.stringify(entries))
    } catch {
      // full or blocked: memory is enough for this tab
    }
  }

  function clear() {
    entries = {}
    try {
      storage?.removeItem(KEY)
    } catch {
      // nothing to forget
    }
  }

  return { recall, remember, clear }
}

function read(storage) {
  try {
    const parsed = JSON.parse(storage?.getItem(KEY) || '{}')
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

function safeSession() {
  try {
    return typeof sessionStorage === 'undefined' ? null : sessionStorage
  } catch {
    return null
  }
}

export const pageCache = createPageCache()
