import assert from 'node:assert'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

// The overview of the first real install was half English on an Italian panel: the score rows, the
// activity feed and the badges all came from the server as English sentences.
Object.defineProperty(globalThis, 'navigator', { value: { language: 'it-IT' }, configurable: true })
globalThis.localStorage = { getItem: () => null, setItem: () => {} }

const { componentName, missionInWords } = await import('../src/lib/settingsWords.js')
const { activityTitle, badgeInWords, seasonName } = await import('../src/lib/activityWords.js')

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..')
const read = (p) => fs.readFileSync(path.join(ROOT, p), 'utf8')

test('the score rows speak Italian even with the labels a project saved in English', () => {
  assert.strictEqual(componentName({ label: 'Time lost on slow queries' }), 'Tempo perso in query lente')
  assert.strictEqual(componentName({ label: 'Slow query seconds per hour' }), 'Tempo perso in query lente')
  assert.strictEqual(componentName({ label: 'Checkout, end to end' }), 'Checkout, end to end', 'a label the team wrote stays as it is')
})

test('the activity feed of a real installation reads in Italian', () => {
  const cases = [
    ['Deploy acme@f100cbe4 made the speed index better (+5.7)', 'Il deploy acme@f100cbe4 ha migliorato l\'indice di velocità (+5.7)'],
    ['Deploy api@1 made the speed index worse (-3.2)', 'Il deploy api@1 ha peggiorato l\'indice di velocità (-3.2)'],
    ['Deploy api@1: speed index unchanged', 'Deploy api@1: indice di velocità invariato'],
    ['A regression escaped: GET /api/scanner-immobile/{id}/estimation', 'Una regressione è scappata: GET /api/scanner-immobile/{id}/estimation'],
    ['A regression appeared: POST /login-ade', 'È comparsa una regressione: POST /login-ade'],
    ['Regression fixed: POST /login-ade', 'Regressione sconfitta: POST /login-ade'],
    ['Debt repaid: select id from users', 'Debito ripagato: select id from users'],
    ['Fix confirmed: POST /api/signature', 'Correzione confermata: POST /api/signature'],
    ['Came back: POST /api/signature', 'È tornata: POST /api/signature'],
    ['Going down after api@2: POST /api/signature', 'In calo dopo api@2: POST /api/signature'],
    ['Badge unlocked: Green deploy', 'Traguardo sbloccato: Deploy verde'],
    ['New level reached: Silver (71.2)', 'Nuovo livello raggiunto: Silver (71.2)'],
    ['Weekly debt budget kept: 19m 5s of waiting per day repaid', 'Obiettivo della settimana raggiunto: 19m 5s di attesa al giorno ripagati'],
    ['queue:work runs on web1 and web2 at the same time', 'queue:work gira su web1 e web2 nello stesso momento'],
    ['Pull request for mission #12: Faster PDF', 'Pull request per la missione #12: Faster PDF'],
    ['mario@acme.example took: POST /login-ade', 'mario@acme.example ha preso: POST /login-ade'],
    ['Online, being verified: POST /login-ade', 'Online, in verifica: POST /login-ade'],
    ['Waved away: GET /cart', 'Messa da parte: GET /cart'],
    ['Escapes in 2 hours: GET /contract/{id}/pdf', 'Scappa tra 2 ore: GET /contract/{id}/pdf'],
    ['mario overtakes luca: 340 to 325 points this month', 'mario supera luca: 340 a 325 punti questo mese'],
  ]
  for (const [en, it] of cases) assert.strictEqual(activityTitle({ title: en }), it)
  assert.strictEqual(activityTitle({ title: 'Something new the server learned to say' }), 'Something new the server learned to say')
})

test('every sentence the server writes in the feed has its Italian', () => {
  // A new e.activity(...) with a sentence nobody taught the panel would show English again.
  const sources = ['internal/server/game/engine.go', 'internal/server/game/deploys.go', 'internal/server/game/days.go', 'internal/server/game/missions.go',
    'internal/server/game/lifecycle.go', 'internal/server/game/commands.go', 'internal/server/game/goal.go', 'internal/server/mcp/mcp.go']
  const found = []
  for (const f of sources) {
    // The sentence is the first quoted string with a space in it: kinds and icons have none.
    for (const m of read(f).matchAll(/[aA]ctivity\(ctx, .*$/gm)) {
      const sentence = [...m[0].matchAll(/"([^"]*)"/g)].map((q) => q[1]).find((q) => q.includes(' '))
      if (sentence) found.push(sentence)
    }
    for (const m of read(f).matchAll(/title = "[^"]*", "[^"]*", "([^"]+)"/g)) found.push(m[1])
  }
  assert.ok(found.length >= 14, `found only ${found.length} sentences: the pattern no longer matches the Go code`)
  for (const sentence of found) {
    // "Debt repaid: "+m.Label is a prefix: the label follows.
    const sample = (sentence.endsWith(' ') ? sentence + 'GET /x' : sentence).replace(/%\+?\.1f/g, '1.5').replace(/%d/g, '3').replace(/%s/g, 'web1')
    assert.notStrictEqual(activityTitle({ title: sample }), sample, `no Italian for "${sentence}"`)
  }
})

test('badges say their name and how to get them in Italian', () => {
  const days = read('internal/server/game/days.go')
  const defs = [...days.matchAll(/BadgeDef\{"([a-z_]+)", "[^"]+", "([^"]+)", "([^"]+)"\}/g)]
  assert.ok(defs.length >= 15)
  for (const [, key, title, description] of defs) {
    const b = badgeInWords({ key, title, description })
    assert.notStrictEqual(b.title, title, `no Italian title for ${key}`)
    assert.notStrictEqual(b.description, description, `no Italian description for ${key}`)
  }
  // Level badges are named after the level and carry its threshold.
  assert.deepStrictEqual(badgeInWords({ key: 'level_gold', title: 'Gold', description: 'A full day at 85 points or more.' }),
    { title: 'Gold', description: 'Un giorno intero a 85 punti o più.' })
})

test('the season is the month in the reader\'s language', () => {
  assert.strictEqual(seasonName('2026-09'), 'settembre 2026')
  assert.strictEqual(seasonName(undefined, 'September 2026'), 'September 2026', 'an older server only sends the English name')
})

test('a mission is judged against what it cost when it opened, not what it costs now', () => {
  // POST /login-ade on a real installation: opened at about 1h 25m a day, fixed, now 7m 12s. The card said
  // "won under 3m 36s", half of today, while the server was checking against half of the start.
  const fixed = { baseline: 5100, current: 432, need_windows: 3, good_windows: 2 }
  assert.strictEqual(
    missionInWords(fixed, { target_ratio: 0.5, window_hours: 24 }),
    'Costava 1h 25m di attesa al giorno, ora 7m 12s. È vinta se resta sotto 42m 30s per 3 giorni (2 giorni finora).',
  )
  // Nothing changed yet: one cost is enough.
  assert.strictEqual(
    missionInWords({ baseline: 551, current: 551, need_windows: 3, good_windows: 0 }, { target_ratio: 0.5, window_hours: 24 }),
    'Costa 9m 11s di attesa al giorno. È vinta se resta sotto 4m 36s per 3 giorni (0 giorni finora).',
  )
})
