import { ref, watch, onBeforeUnmount } from 'vue'
import { api } from './api.js'
import { pageCache } from './pageCache.js'

// How many pages are waiting for an answer the user asked for (not the silent refreshes): the thin bar
// at the top of the layout says "working on it" even when the old numbers are already on screen.
export const pending = ref(0)

// Loads data from a function, reloads when the sources change and optionally every `every` ms.
// With `cacheKey` the last answer is shown at once and refreshed behind it.
export function useLoad(fn, { watchSources = [], every = 0, cacheKey = null } = {}) {
  const data = ref(null)
  const error = ref(null)
  const loading = ref(true)
  let timer = null
  let seq = 0

  async function load(silent = false) {
    const mine = ++seq
    const key = cacheKey ? cacheKey() : null
    const cached = key && !silent ? pageCache.recall(key) : undefined
    if (cached !== undefined) {
      data.value = cached
      error.value = null
      loading.value = false
    } else if (!silent) loading.value = true
    if (!silent) pending.value++
    try {
      const result = await fn()
      if (mine === seq) {
        data.value = result
        error.value = null
        if (key) pageCache.remember(key, result)
      }
    } catch (e) {
      if (mine === seq) error.value = e
    } finally {
      if (mine === seq) loading.value = false
      if (!silent) pending.value--
    }
  }

  if (watchSources.length) watch(watchSources, () => load(), { immediate: true })
  else load()

  if (every > 0) timer = setInterval(() => document.visibilityState === 'visible' && load(true), every)
  onBeforeUnmount(() => timer && clearInterval(timer))

  return { data, error, loading, reload: load }
}

// A GET page: the URL is both the request and the cache key.
export function useGet(url, options = {}) {
  return useLoad(() => api.get(url()), { ...options, cacheKey: url })
}
