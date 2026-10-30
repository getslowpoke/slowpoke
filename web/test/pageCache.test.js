import assert from 'node:assert'
import { test } from 'node:test'

import { createPageCache } from '../src/lib/pageCache.js'

function fakeStorage() {
  const m = new Map()
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => m.set(k, String(v)),
    removeItem: (k) => m.delete(k),
    key: (i) => [...m.keys()][i] ?? null,
    get length() { return m.size },
  }
}

test('a page opened again shows what it showed last time', () => {
  const c = createPageCache({ storage: fakeStorage(), now: () => 1000 })
  assert.equal(c.recall('/api/projects/acme/overview'), undefined)
  c.remember('/api/projects/acme/overview', { score: 71 })
  assert.deepEqual(c.recall('/api/projects/acme/overview'), { score: 71 })
})

test('a reload of the browser tab still finds it', () => {
  const storage = fakeStorage()
  createPageCache({ storage, now: () => 1000 }).remember('k', { a: 1 })
  assert.deepEqual(createPageCache({ storage, now: () => 2000 }).recall('k'), { a: 1 })
})

test('an answer older than the limit is not shown: an empty skeleton beats yesterday\'s numbers', () => {
  let t = 0
  const c = createPageCache({ storage: fakeStorage(), now: () => t, maxAge: 60_000 })
  c.remember('k', { a: 1 })
  t = 60_001
  assert.equal(c.recall('k'), undefined)
})

test('the cache stays small: the oldest pages go first', () => {
  const c = createPageCache({ storage: fakeStorage(), now: () => 1, max: 2 })
  c.remember('a', 1)
  c.remember('b', 2)
  c.remember('c', 3)
  assert.equal(c.recall('a'), undefined)
  assert.equal(c.recall('b'), 2)
  assert.equal(c.recall('c'), 3)
})

test('a full or blocked storage never breaks the page', () => {
  const broken = { getItem() { throw new Error('blocked') }, setItem() { throw new Error('quota') }, removeItem() { throw new Error('x') }, key() { return null }, length: 0 }
  const c = createPageCache({ storage: broken, now: () => 1 })
  c.remember('k', { a: 1 })
  assert.deepEqual(c.recall('k'), { a: 1 }, 'memory still works')
  assert.doesNotThrow(() => c.clear())
})

test('logging out forgets every page', () => {
  const storage = fakeStorage()
  const c = createPageCache({ storage, now: () => 1 })
  c.remember('k', { a: 1 })
  c.clear()
  assert.equal(c.recall('k'), undefined)
  assert.equal(createPageCache({ storage, now: () => 1 }).recall('k'), undefined)
})
