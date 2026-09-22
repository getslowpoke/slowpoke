import assert from 'node:assert'
import { test } from 'node:test'

// Il modulo tocca localStorage e navigator: qui non ci sono, e il pannello deve reggerlo lo stesso.
const store = new Map()
globalThis.localStorage = {
  getItem: (k) => (store.has(k) ? store.get(k) : null),
  setItem: (k, v) => store.set(k, String(v)),
}
// In Node 22 navigator esiste già ed è di sola lettura: si sostituisce la proprietà, non l'oggetto.
Object.defineProperty(globalThis, 'navigator', { value: { language: 'it-IT' }, configurable: true })

const { locale, setLocale, setServerLocale, t } = await import('../src/lib/i18n.js')

test('senza scelte, la lingua è quella del browser', () => {
  assert.strictEqual(locale.value, 'it')
  assert.strictEqual(t('Overview'), 'Panoramica')
})

test('il server può dichiarare la lingua di chi non ne ha ancora scelta una', () => {
  setServerLocale('en')
  assert.strictEqual(locale.value, 'en')
  assert.strictEqual(t('Overview'), 'Overview')
})

test('la scelta di chi guarda vince su quella del server, e resta', () => {
  setLocale('it')
  setServerLocale('en')
  assert.strictEqual(locale.value, 'it', 'il server non deve sovrascrivere una scelta esplicita')
})

test('una lingua che non conosciamo non cambia niente', () => {
  store.clear()
  setServerLocale('de')
  assert.notStrictEqual(locale.value, 'de')
  setServerLocale('')
  assert.notStrictEqual(locale.value, '')
})
