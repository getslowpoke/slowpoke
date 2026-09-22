import assert from 'node:assert'
import { test } from 'node:test'

globalThis.localStorage = { getItem: () => null, setItem: () => {} }
Object.defineProperty(globalThis, 'navigator', { value: { language: 'it-IT' }, configurable: true })
const { busyInWords } = await import('../src/lib/busyHours.js')

test('busy hours read as a person says them', () => {
  const b = { known: true, manual: false, groups: [{ from_day: 1, to_day: 5, hours: '9-19' }, { from_day: 6, to_day: 6, hours: '9-13' }] }
  assert.strictEqual(busyInWords(b), 'lun-ven 9-19; sab 9-13 · trovate da Slowpoke nel traffico')
  assert.match(busyInWords({ known: false }), /ogni ora/)
})
