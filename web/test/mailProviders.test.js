import assert from 'node:assert'
import fs from 'node:fs'
import { test } from 'node:test'

import { mailProviders, providerFor } from '../src/lib/mailProviders.js'

test('a saved account is recognised by its provider, or by its server name', () => {
  assert.strictEqual(providerFor({ provider: 'brevo' }).id, 'brevo')
  assert.strictEqual(providerFor({ provider: 'cloudflare' }).id, 'cloudflare')
  assert.strictEqual(providerFor({ provider: 'smtp', host: 'SMTP.GMAIL.COM' }).id, 'google')
  assert.strictEqual(providerFor({ provider: 'smtp', host: 'email-smtp.us-east-1.amazonaws.com' }).id, 'ses')
  assert.strictEqual(providerFor({ provider: 'smtp', host: 'mail.mycompany.com' }).id, 'smtp')
  // Saved before providers existed: no provider field, only a server.
  assert.strictEqual(providerFor({ host: 'smtp.gmail.com' }).id, 'google')
  assert.strictEqual(providerFor({}), null)
})

test('the providers the server speaks are the ones offered as API', () => {
  const api = mailProviders.filter((p) => p.api).map((p) => p.id).sort()
  const file = new URL('../../internal/server/mail/api.go', import.meta.url)
  if (!fs.existsSync(file)) return // the package is built without the Go sources around
  const go = fs.readFileSync(file, 'utf8')
  const spoken = go.split('var Providers = []string{')[1].split('}')[0].match(/"([a-z]+)"/g).map((q) => q.slice(1, -1)).sort()
  assert.deepStrictEqual(api, spoken)
})

test('every SMTP preset has a port the server accepts', () => {
  for (const p of mailProviders.filter((x) => !x.api)) {
    assert.ok(['465', '587'].includes(p.port), `${p.id}: ${p.port}`)
  }
})
