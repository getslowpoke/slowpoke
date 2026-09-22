import { ref, watch, onBeforeUnmount } from 'vue'

// Loads data from a function, reloads when the sources change and optionally every `every` ms.
export function useLoad(fn, { watchSources = [], every = 0 } = {}) {
  const data = ref(null)
  const error = ref(null)
  const loading = ref(true)
  let timer = null
  let seq = 0

  async function load(silent = false) {
    const mine = ++seq
    if (!silent) loading.value = true
    try {
      const result = await fn()
      if (mine === seq) {
        data.value = result
        error.value = null
      }
    } catch (e) {
      if (mine === seq) error.value = e
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  if (watchSources.length) watch(watchSources, () => load(), { immediate: true })
  else load()

  if (every > 0) timer = setInterval(() => document.visibilityState === 'visible' && load(true), every)
  onBeforeUnmount(() => timer && clearInterval(timer))

  return { data, error, loading, reload: load }
}
