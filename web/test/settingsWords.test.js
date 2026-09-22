import assert from 'node:assert'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

import {
  applyPreset, bossesInWords, channelInWords, channelNames, componentInWords, currentPreset,
  deployWindowInWords, eventNames, gamePresets, integrationInWords, liveTileWords, metricUnits,
  componentName, componentNames, deliveryWords, heldInWords, measureKindInWords, measureKindWords, metricWords, verdictWords, missionsInWords, providerNames, effortInWords, missionInWords, missionStatusNames,
  missionStatusWhy, pointsToGain, componentNow, queryBadgeWords, returnInWords, ruleInWords,
  scoreRowInWords, scoreTrend, slowRequestInWords,
  slowTabWords, valueInWords,
} from '../src/lib/settingsWords.js'

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..')

test('a value is read in the words of what it measures', () => {
  // 0.002 of a share is what nobody can read; two requests in a thousand is what they mean.
  assert.strictEqual(valueInWords('http_slow_ratio', 0.002), '2 out of 1000')
  assert.strictEqual(valueInWords('http_slow_ratio', 0.02), '20 out of 1000')
  assert.strictEqual(valueInWords('http_p95', 0.3), '300 ms')
  assert.strictEqual(valueInWords('http_p95', 1.5), '1.5 s')
  assert.strictEqual(valueInWords('db_slow_per_hour', 900), '900 s')
  assert.strictEqual(valueInWords('http_error_rate', 20), '20')
  assert.strictEqual(valueInWords('http_p95', 'nonsense'), '—')
})

test('a component of the index says what it weighs and where it turns red', () => {
  const components = [
    { metric: 'http_slow_ratio', weight: 25, good: 0.002, bad: 0.02 },
    { metric: 'http_p95', weight: 25, good: 0.3, bad: 1.5 },
    { metric: 'db_worst', weight: 50, good: 5, bad: 60 },
  ]
  assert.strictEqual(
    componentInWords(components[0], components),
    'Worth 25 points out of 100. Full marks up to 2 out of 1000, zero from 20 out of 1000.',
  )
  // The share is of the weights actually in use, not of a hundred that may not add up.
  assert.strictEqual(componentInWords(components[2], components).startsWith('Worth 50 points'), true)
})

test('the missions settings read as a sentence', () => {
  assert.strictEqual(
    missionsInWords({ target_ratio: 0.5, good_windows: 3, window_hours: 24 }),
    'A mission is won when the cost drops by 50% and stays there for 3 days.',
  )
  assert.strictEqual(
    missionsInWords({ target_ratio: 0.35, good_windows: 4, window_hours: 12 }),
    'A mission is won when the cost drops by 65% and stays there for 2 days.',
  )
})

test('the bosses settings read as a sentence', () => {
  assert.strictEqual(
    bossesInWords({ growth: 2, deadline_hours: 72 }),
    'A boss appears when something gets 2 times worse, and the team has 3 days to beat it.',
  )
  assert.match(bossesInWords({ growth: 1.5, deadline_hours: 24 }), /1\.5 times worse.*one day/)
})

test('three ways of playing, and the page knows which one is on', () => {
  assert.deepStrictEqual(gamePresets.map((p) => p.key), ['calm', 'normal', 'sharp'])
  for (const preset of gamePresets) {
    const settings = { missions: {}, bosses: {} }
    applyPreset(settings, preset.key)
    assert.strictEqual(currentPreset(settings), preset.key, `${preset.key} is not recognised after being applied`)
    // Every knob the page stops showing must be set by every preset, or it would keep whatever
    // was there before and the sentence would lie.
    for (const key of ['window_hours', 'good_windows', 'target_ratio', 'min_seconds', 'max_open']) {
      assert.ok(settings.missions[key] !== undefined, `${preset.key} leaves missions.${key} unset`)
    }
    for (const key of ['growth', 'min_seconds', 'deadline_hours']) {
      assert.ok(settings.bosses[key] !== undefined, `${preset.key} leaves bosses.${key} unset`)
    }
  }
  const tweaked = applyPreset({ missions: {}, bosses: {} }, 'normal')
  tweaked.missions.target_ratio = 0.42
  assert.strictEqual(currentPreset(tweaked), 'custom', 'a hand-tuned setting must not claim to be a preset')
  assert.strictEqual(applyPreset({ missions: {}, bosses: {} }, 'nonsense').missions.target_ratio, undefined)
})

test('the presets go from gentle to sharp, in that order', () => {
  const [calm, normal, sharp] = gamePresets
  assert.ok(calm.missions.target_ratio > normal.missions.target_ratio)
  assert.ok(normal.missions.target_ratio > sharp.missions.target_ratio)
  assert.ok(calm.bosses.deadline_hours > normal.bosses.deadline_hours)
  assert.ok(normal.bosses.deadline_hours > sharp.bosses.deadline_hours)
  assert.ok(calm.missions.max_open < sharp.missions.max_open)
})

test('every activity a rule can carry has a name a person can read', () => {
  // The server's list, as the panel receives it: none of these may reach a person as a key.
  const fromServer = ['level_up', 'badge', 'mission_completed', 'boss', 'boss_defeated', 'boss_escaped',
    'deploy_better', 'deploy_worse', 'deploy_neutral', 'command_overlap', 'debt_goal', 'mission_claimed',
    'pull_request', 'mission_shipped', 'mission_confirmed', 'mission_relapsed']
  for (const event of fromServer) {
    assert.ok(eventNames[event], `${event} has no name`)
    assert.ok(!/_/.test(eventNames[event]), `${event} is still spelled like a database column`)
  }
})

test('a rule says what it does', () => {
  const channels = [{ id: 'c1', name: 'Slack #perf' }]
  assert.strictEqual(
    ruleInWords({ channel: 'c1', events: ['boss', 'deploy_worse'] }, channels),
    'Slack #perf is told when: a boss appears, a deploy made things slower.',
  )
  assert.strictEqual(ruleInWords({ channel: 'c1', events: ['*'] }, channels), 'Slack #perf is told about everything.')
  assert.match(ruleInWords({ channel: 'nope', events: ['boss'] }, channels), /nowhere yet/)
  assert.match(ruleInWords({ channel: 'c1', events: [] }, channels), /about nothing yet/)
})

test('a channel says what it will do, and what is missing before it can', () => {
  assert.strictEqual(
    channelInWords({ type: 'slack', url: 'https://hooks.slack.com/x' }),
    'Slack: messages go to the channel of the webhook. With a bot token it can write to people too.',
  )
  assert.match(channelInWords({ type: 'slack', url: 'https://hooks.slack.com/x', bot_token: 'xoxb-1' }), /and to people in private/)
  assert.match(channelInWords({ type: 'discord' }), /it needs the address/)
  assert.match(channelInWords({ type: 'telegram', bot_token: 'x' }), /needs the bot token and the chat id/)
  assert.match(channelInWords({ type: 'telegram', bot_token: 'x', chat_id: '1' }), /messages go to that chat/)
  // Every type the panel offers has a name a person would use.
  for (const type of ['slack', 'discord', 'teams', 'telegram', 'webhook']) {
    assert.ok(channelNames[type], `${type} has no name`)
  }
})

test('a task tracker says where a mission ends up', () => {
  assert.strictEqual(
    integrationInWords({ provider: 'github', token: 'ghp_x', repo: 'acme/shop' }),
    'A mission becomes an issue in GitHub, on acme/shop.',
  )
  assert.match(integrationInWords({ provider: 'jira', token: 't', project_key: 'SHOP' }), /Jira, on SHOP/)
  assert.match(integrationInWords({ provider: 'linear', token: '' }), /needs a token/)
  assert.match(integrationInWords({ provider: 'linear', token: 't' }), /where to open them/)
  for (const provider of ['github', 'gitlab', 'linear', 'jira', 'asana', 'trello', 'clickup']) {
    assert.ok(providerNames[provider], `${provider} has no name`)
    assert.match(providerNames[provider], /^[A-Z]/, `${provider} is not written the way its makers write it`)
  }
})

test('the two numbers of the general tab read back as sentences', () => {
  assert.strictEqual(
    slowRequestInWords({ http: { slow_seconds: 2 } }),
    'A request over 2 s counts as slow, and that is what the waiting time is measured against.',
  )
  assert.strictEqual(
    deployWindowInWords({ deploys: { compare_minutes: 30 } }),
    'After a deploy, the 30 minutes before it are compared with the 30 minutes after.',
  )
  // Nothing set yet: a sentence with a zero in it beats a crash.
  assert.match(slowRequestInWords({}), /over 0 s/)
  assert.match(deployWindowInWords({}), /the 0 minutes/)
})

test('a mission says what it costs, what it has to reach, and how far along it is', () => {
  const mission = { current: 1042, need_windows: 2, good_windows: 0, roi: 65, effort: 'm' }
  assert.strictEqual(
    missionInWords(mission, { target_ratio: 0.5, window_hours: 24 }),
    'It costs 17m 22s of waiting per day. Won when it stays under 8m 41s for 2 days (0 days so far).',
  )
  // Windows shorter than a day are counted as checks, because "half a day so far" reads worse.
  assert.match(
    missionInWords({ ...mission, need_windows: 3, good_windows: 1 }, { target_ratio: 0.5, window_hours: 8 }),
    /for one day \(one check so far\)/,
  )
  // A boss has no windows to complete: it still says what it costs.
  assert.strictEqual(missionInWords({ current: 120 }, {}), 'It costs 2m of waiting per day.')
})

test('the work and the return are said in hours a person can picture', () => {
  assert.strictEqual(effortInWords('xs'), 'about an hour of work')
  assert.strictEqual(effortInWords('m'), 'about two days of work')
  assert.strictEqual(effortInWords('nonsense'), '')
  assert.strictEqual(
    returnInWords({ effort: 'm', roi: 65 }),
    'about two days of work, every hour of it removes 1m 5s of waiting per day',
  )
  assert.strictEqual(returnInWords({ effort: 'm' }), 'about two days of work')
  assert.match(returnInWords({ roi: 65 }), /^every hour of it/)
})

test('every state a mission can end in has a word on the card and a reason behind it', () => {
  for (const state of ['open', 'completed', 'defeated', 'escaped', 'expired', 'failed', 'excluded']) {
    assert.ok(missionStatusNames[state], `${state} has no word`)
    // Short enough to sit on a card next to the rest.
    assert.ok(missionStatusNames[state].length < 16, `${state} is a paragraph, not a label`)
  }
  for (const state of ['completed', 'defeated', 'escaped', 'expired', 'failed', 'excluded']) {
    assert.match(missionStatusWhy[state], /\.$/, `${state} has no sentence explaining it`)
  }
  assert.match(missionStatusWhy.escaped, /stopped being slow on its own/)
})


test('every metric the speed index can be built on says what it measures', () => {
  // The server's list is the one that counts: a metric nobody explained reaches a person as a key.
  const file = path.join(ROOT, 'internal', 'server', 'project', 'settings.go')
  if (!fs.existsSync(file)) return // the package is built without the Go sources around
  const block = fs.readFileSync(file, 'utf8').split('var Metrics = map[string]string{')[1].split('}')[0]
  const keys = [...block.matchAll(/"([a-z0-9_]+)":/g)].map((m) => m[1])
  assert.ok(keys.length >= 10, 'the metrics of the server were not found')
  for (const key of keys) {
    assert.ok(metricWords[key], `${key} has no words`)
    assert.ok(metricUnits[key], `${key} has no unit`)
    assert.match(metricWords[key], /\.$/, `${key} is not a sentence`)
  }
})

test('a row of the speed index says what it measures and what it is worth', () => {
  const components = [
    { key: 'http_slow', metric: 'http_slow_ratio', weight: 25, good: 0.002, bad: 0.02 },
    { key: 'errors', metric: 'http_error_rate', weight: 75, good: 1, bad: 20 },
  ]
  const words = scoreRowInWords(components[0], components)
  assert.match(words, /Out of 100 requests/)
  assert.match(words, /Worth 25 points out of 100/)
  assert.match(words, /2 out of 1000/)
})

test('the short labels of the pages people read have a sentence behind them', () => {
  for (const tile of ['requests', 'p95', 'errors', 'db_slow', 'php_stalls', 'machines']) {
    assert.match(liveTileWords[tile] || '', /\.$/, `${tile} has no sentence`)
  }
  for (const tab of ['queries', 'endpoints', 'php', 'hotspots']) {
    assert.match(slowTabWords[tab] || '', /\.$/, `${tab} has no sentence`)
  }
  for (const badge of ['n_plus_one', 'explained']) {
    assert.match(queryBadgeWords[badge] || '', /\.$/, `${badge} has no sentence`)
  }
  // N+1 and EXPLAIN are names, not explanations: the sentence must not simply repeat them.
  assert.doesNotMatch(queryBadgeWords.n_plus_one, /N\+1/)
  assert.doesNotMatch(queryBadgeWords.explained, /EXPLAIN/)
})

test('the labels the server ships with are already the plain ones', () => {
  const file = path.join(ROOT, 'internal', 'server', 'project', 'settings.go')
  if (!fs.existsSync(file)) return
  const block = fs.readFileSync(file, 'utf8').split('s.Score.Components = []Component{')[1].split('\n\t}')[0]
  const labels = [...block.matchAll(/Label: "([^"]+)"/g)].map((m) => m[1])
  assert.strictEqual(labels.length, 6)
  const plain = Object.values(componentNames)
  for (const label of labels) {
    assert.ok(plain.includes(label), `${label} is not one of the words this page knows`)
    // A label already plain must survive untouched, never be translated into another one.
    assert.strictEqual(componentName({ label }), label)
  }
  // A project that was created before, and one whose team named it themselves.
  assert.strictEqual(componentName({ label: 'Response time p95' }), 'Response time for most people')
  assert.strictEqual(componentName({ label: 'Checkout, end to end' }), 'Checkout, end to end')
})

test('a mission says how long it has held, not how many windows were good', () => {
  assert.strictEqual(heldInWords(1, 2, 24), '1 of 2 days')
  assert.strictEqual(heldInWords(0, 1, 24), '0 of one day')
  assert.strictEqual(heldInWords(2, 4, 12), '2 of 2 days')
  assert.strictEqual(heldInWords(1, 3, 5), '1 of 3 checks')
})

test('a deploy, a delivery band and a measure of your own all have words', () => {
  for (const verdict of ['better', 'worse', 'neutral', 'no_data']) {
    assert.match(verdictWords[verdict] || '', /\.$/, `${verdict} has no sentence`)
  }
  // The bands are the server's: a new one must not reach a person as a bare adjective.
  for (const band of ['elite', 'high', 'medium', 'low']) {
    assert.match(deliveryWords[band] || '', /\.$/, `${band} has no sentence`)
  }
  for (const kind of ['timer', 'gauge', 'counter']) {
    assert.ok(measureKindWords[kind], `${kind} has no words`)
  }
  assert.strictEqual(measureKindInWords('timer'), 'a time')
  assert.strictEqual(measureKindInWords('whatever'), 'whatever')
})

test('a row of the speed index says what fixing it would give back', () => {
  const row = { available: true, metric: 'http_p95', value: 1.2, good: 0.3, bad: 1.5, weight: 20, lost: 4.23 }
  assert.strictEqual(pointsToGain(row), '+4.2 points')
  assert.strictEqual(componentNow(row), 'today 1.2 s · full marks up to 300 ms')
  assert.strictEqual(pointsToGain({ ...row, lost: 0 }), 'full marks')
  assert.strictEqual(pointsToGain({ ...row, available: false }), '')
  assert.match(componentNow({ ...row, available: false }), /nobody is measuring/)
})

test('the days behind today: the record, the week, and where it is going', () => {
  const days = [
    { day: '2026-09-09', score: 50, closed: true },
    { day: '2026-09-10', score: 47, closed: true },
    { day: '2026-09-11', score: 33, closed: true },
    { day: '2026-09-12', score: 88, closed: true },
    { day: '2026-09-13', score: 88, closed: true },
    { day: '2026-09-14', score: 54, closed: true },
    { day: '2026-09-15', score: 67, closed: true },
    { day: '2026-09-16', score: 62, closed: true },
    { day: '2026-09-17', score: 60, closed: true },
    { day: '2026-09-18', score: 61, closed: true },
    { day: '2026-09-19', score: 91.8, closed: true },
    { day: '2026-09-20', score: 87, closed: false }, // today, still being measured
  ]
  const trend = scoreTrend(days)
  assert.deepStrictEqual(trend.record, { score: 91.8, day: '2026-09-19' }, 'the record is the best day that closed')
  assert.strictEqual(trend.week, 69.1, 'the seven closed days before today')
  // Before those seven there are only four days: they are averaged as they are, which is
  // better than saying nothing while a project is young.
  assert.strictEqual(trend.change, 14.6)
  assert.deepStrictEqual(scoreTrend([]), { record: null, week: null, change: null })
})
