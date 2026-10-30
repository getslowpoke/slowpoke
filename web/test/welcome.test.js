import assert from 'node:assert'
import { test } from 'node:test'

import { createWelcome } from '../src/lib/welcome.js'

function fakeStorage() {
  const m = new Map()
  return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)) }
}

test('the tour opens on the first visit and never again in that browser', () => {
  const storage = fakeStorage()
  const w = createWelcome({ storage })
  assert.strictEqual(w.due(), true)
  w.done()
  assert.strictEqual(w.due(), false)
  assert.strictEqual(createWelcome({ storage }).due(), false, 'a reload remembers it')
})

test('a new tour is shown again to people who saw the old one', () => {
  const storage = fakeStorage()
  createWelcome({ storage, version: 1 }).done()
  assert.strictEqual(createWelcome({ storage, version: 2 }).due(), true)
})

test('a blocked storage shows the tour once per page, not on every click', () => {
  // A private window on the public demo: better once per visit than never, never on every page.
  const blocked = { getItem() { throw new Error('blocked') }, setItem() { throw new Error('blocked') } }
  const w = createWelcome({ storage: blocked })
  assert.strictEqual(w.due(), true)
  w.done()
  assert.strictEqual(w.due(), false)
})
