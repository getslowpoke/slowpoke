// The settings page used to show the engine's knobs in the engine's words: "target ratio 0.5",
// "good windows 3", "http_slow_ratio | 25 | 0.002 | 0.02". This turns those numbers into the
// sentence a person would say out loud, and offers three ways of playing instead of nine fields.

import { duration } from './format.js'
import { t } from './i18n.js'

/** How a metric is read by a human: what its numbers mean and how to write one. */
export const metricUnits = {
  http_slow_ratio: { kind: 'ratio', per: 1000, noun: 'requests over 2 s' },
  http_error_rate: { kind: 'count', noun: '5xx every 10,000 requests' },
  http_p95: { kind: 'seconds', noun: 'the page that keeps people waiting (p95)' },
  php_stalls: { kind: 'count', noun: 'PHP stalls every 1,000 requests' },
  db_slow_per_hour: { kind: 'seconds', noun: 'seconds of slow queries per hour' },
  db_worst: { kind: 'seconds', noun: 'the slowest query' },
  custom_p95: { kind: 'seconds', noun: 'your own measure (p95)' },
  custom_avg: { kind: 'number', noun: 'your own measure (average)' },
  custom_max: { kind: 'number', noun: 'your own measure (highest)' },
  custom_per_hour: { kind: 'number', noun: 'your own counter, per hour' },
}

/** A value in the words of its metric: 0.002 of a ratio is "2 out of 1000". */
export function valueInWords(metric, value) {
  const unit = metricUnits[metric] || { kind: 'number' }
  const n = Number(value)
  if (!Number.isFinite(n)) return '—'
  switch (unit.kind) {
    case 'ratio': {
      const per = unit.per || 1000
      return t('{n} out of {per}', { n: round(n * per), per })
    }
    case 'seconds':
      return n >= 1 ? t('{n} s', { n: round(n) }) : t('{n} ms', { n: round(n * 1000) })
    default:
      return String(round(n))
  }
}

/** The line under one component of the speed index: what it weighs and where green turns red. */
export function componentInWords(component, components = []) {
  const total = components.reduce((sum, c) => sum + (Number(c.weight) || 0), 0) || 100
  const share = Math.round((Number(component.weight) || 0) / total * 100)
  return t('Worth {share} points out of 100. Full marks up to {good}, zero from {bad}.', {
    share,
    good: valueInWords(component.metric, component.good),
    bad: valueInWords(component.metric, component.bad),
  })
}

/** The line under the missions settings, in the order a person thinks about it. */
export function missionsInWords(missions) {
  const drop = Math.round((1 - Number(missions.target_ratio || 0)) * 100)
  const windows = Number(missions.good_windows) || 1
  const hours = Number(missions.window_hours) || 24
  return t('A mission is won when the cost drops by {drop}% and stays there for {time}.', {
    drop,
    time: hoursInWords(windows * hours),
  })
}

/** The line under the bosses settings. */
export function bossesInWords(bosses) {
  const times = Number(bosses.growth) || 2
  return t('A boss appears when something gets {times} times worse, and the team has {time} to beat it.', {
    times: round(times),
    time: hoursInWords(Number(bosses.deadline_hours) || 72),
  })
}

/**
 * Three ways of playing. Nobody should have to invent "good windows 3": they should say whether
 * they want the game gentle or sharp, and the numbers follow.
 */
export const gamePresets = [
  {
    key: 'calm',
    title: 'Calm',
    summary: 'Few missions, generous deadlines. For a team that is measuring for the first time.',
    missions: { window_hours: 24, good_windows: 3, target_ratio: 0.7, min_seconds: 120, max_open: 3 },
    bosses: { growth: 3, min_seconds: 300, deadline_hours: 168 },
  },
  {
    key: 'normal',
    title: 'Normal',
    summary: 'The pace most teams keep: half the cost gone, three days on a boss.',
    missions: { window_hours: 24, good_windows: 3, target_ratio: 0.5, min_seconds: 60, max_open: 5 },
    bosses: { growth: 2, min_seconds: 120, deadline_hours: 72 },
  },
  {
    key: 'sharp',
    title: 'Sharp',
    summary: 'More missions at once and a day to answer a regression. For a team that has the habit.',
    missions: { window_hours: 12, good_windows: 4, target_ratio: 0.35, min_seconds: 30, max_open: 8 },
    bosses: { growth: 1.5, min_seconds: 60, deadline_hours: 24 },
  },
]

/** Which preset the current settings are, if any: the buttons show what is on. */
export function currentPreset(settings) {
  const same = (a, b) => Object.keys(b).every((k) => Number(a?.[k]) === Number(b[k]))
  const found = gamePresets.find((p) => same(settings.missions, p.missions) && same(settings.bosses, p.bosses))
  return found ? found.key : 'custom'
}

/** Applies a preset in place, leaving everything else of the settings alone. */
export function applyPreset(settings, key) {
  const preset = gamePresets.find((p) => p.key === key)
  if (!preset) return settings
  Object.assign(settings.missions, preset.missions)
  Object.assign(settings.bosses, preset.bosses)
  return settings
}

function hoursInWords(hours) {
  const h = Number(hours) || 0
  if (h % 24 === 0 && h >= 24) {
    const days = h / 24
    return days === 1 ? t('one day') : t('{n} days', { n: days })
  }
  return h === 1 ? t('one hour') : t('{n} hours', { n: round(h) })
}

function round(n) {
  const v = Number(n)
  if (!Number.isFinite(v)) return 0
  if (Math.abs(v) >= 100) return Math.round(v)
  return Math.round(v * 100) / 100
}

/**
 * The activity a rule can send, in words. The keys are what the server calls them; nobody outside
 * this repository should have to learn that "deploy_neutral" is a deploy that changed nothing.
 */
export const eventNames = {
  '*': 'Everything',
  level_up: 'The project changes level',
  badge: 'A badge is won',
  mission_completed: 'A mission is completed',
  mission_claimed: 'Somebody takes a mission',
  pull_request: 'A pull request is linked to a mission',
  mission_shipped: 'The fix of a mission goes online',
  mission_confirmed: 'A fix held for a month',
  mission_relapsed: 'A fix did not hold',
  boss: 'A boss appears',
  boss_defeated: 'A boss is beaten',
  boss_escaped: 'A boss got away',
  deploy_better: 'A deploy made things faster',
  deploy_worse: 'A deploy made things slower',
  deploy_neutral: 'A deploy changed nothing',
  command_overlap: 'Two scheduled commands run over each other',
  debt_goal: 'The weekly debt goal is reached',
}

/** What a rule does, said out loud: "Slack #perf gets a message when a boss appears." */
export function ruleInWords(rule, channels = []) {
  const channel = channels.find((c) => c.id === rule.channel)
  const where = channel ? (channel.name || channel.id) : t('nowhere yet')
  const events = (rule.events || []).map((e) => t(eventNames[e] || e).toLowerCase())
  if (events.length === 0) return t('{where} is told about nothing yet.', { where })
  if (events.includes(t(eventNames['*']).toLowerCase())) return t('{where} is told about everything.', { where })
  return t('{where} is told when: {events}.', { where, events: events.join(', ') })
}

/** What a person calls these places, instead of the lowercase key the server stores. */
export const channelNames = {
  slack: 'Slack',
  discord: 'Discord',
  teams: 'Microsoft Teams',
  telegram: 'Telegram',
  webhook: 'Any address (webhook)',
}

export const providerNames = {
  github: 'GitHub',
  gitlab: 'GitLab',
  linear: 'Linear',
  jira: 'Jira',
  asana: 'Asana',
  trello: 'Trello',
  clickup: 'ClickUp',
}

/** What a channel will do, and what is still missing before it can. */
export function channelInWords(channel) {
  const where = t(channelNames[channel.type] || channel.type)
  if (channel.type === 'telegram') {
    if (!channel.bot_token || !channel.chat_id) return t('{where}: it needs the bot token and the chat id.', { where })
    return t('{where}: messages go to that chat.', { where })
  }
  if (!channel.url) return t('{where}: it needs the address its webhook gave you.', { where })
  if (channel.type === 'slack' && channel.bot_token) {
    return t('{where}: messages go to the channel of the webhook, and to people in private.', { where })
  }
  if (channel.type === 'slack') {
    return t('{where}: messages go to the channel of the webhook. With a bot token it can write to people too.', { where })
  }
  return t('{where}: messages go to the address you gave.', { where })
}

/** What a task tracker will do with a mission, and what is still missing. */
export function integrationInWords(integration) {
  const where = t(providerNames[integration.provider] || integration.provider)
  const target = integration.repo || integration.project_key || integration.team_id || integration.list_id || integration.project_id
  if (!integration.token) return t('{where}: it needs a token to open issues.', { where })
  if (!target) return t('{where}: it needs to know where to open them.', { where })
  return t('A mission becomes an issue in {where}, on {target}.', { where, target })
}

/** The two numbers of the General tab, read back. */
export function slowRequestInWords(settings) {
  return t('A request over {n} s counts as slow, and that is what the waiting time is measured against.', {
    n: round(Number(settings?.http?.slow_seconds) || 0),
  })
}

export function deployWindowInWords(settings) {
  const minutes = Math.round(Number(settings?.deploys?.compare_minutes) || 0)
  return t('After a deploy, the {n} minutes before it are compared with the {n} minutes after.', { n: minutes })
}

// ------------------------------------------------------------------ missions, as a person reads them

/** The work behind a mission: the sizes the server uses are xs, s, m, l hours of work. */
export const effortHours = { xs: 1, s: 4, m: 16, l: 40 }

export function effortInWords(effort) {
  switch (String(effort || '').toLowerCase()) {
    case 'xs': return t('about an hour of work')
    case 's': return t('half a day of work')
    case 'm': return t('about two days of work')
    case 'l': return t('about a week of work')
    default: return ''
  }
}

/**
 * What a mission asks for, instead of "0/2 good windows": what it costs now, what it has to get
 * under, and how far along it is.
 */
export function missionInWords(mission, settings = {}) {
  const cost = Number(mission.current ?? mission.baseline) || 0
  const ratio = Number(settings.target_ratio ?? 0.5)
  const hours = Number(settings.window_hours ?? 24)
  const need = Number(mission.need_windows) || 0
  const done = Number(mission.good_windows) || 0
  if (!need) {
    return t('It costs {cost} of waiting per day.', { cost: duration(cost) })
  }
  return t('It costs {cost} of waiting per day. Won when it stays under {target} for {time} ({done} so far).', {
    cost: duration(cost),
    target: duration(cost * ratio),
    time: countedWindows(need, hours),
    done: countedWindows(done, hours),
  })
}

/** What an hour spent on this mission gives back. */
export function returnInWords(mission) {
  const roi = Number(mission.roi) || 0
  const effort = effortInWords(mission.effort)
  if (!roi) return effort
  const gain = t('every hour of it removes {n} of waiting per day', { n: duration(roi) })
  return effort ? `${effort}, ${gain}` : gain
}

/**
 * Why a mission is no longer open, in words rather than in the state machine's vocabulary: a short
 * one for the card, and the sentence behind it for whoever hovers.
 */
export const missionStatusNames = {
  open: 'Open',
  completed: 'Done',
  defeated: 'Beaten',
  escaped: 'Got away',
  expired: 'Out of time',
  failed: 'Not made it',
  excluded: 'Left out',
  dismissed: 'Waved away',
}

export const missionStatusWhy = {
  dismissed: 'Nobody will act on it: closed with no points, and quiet for a while.',
  completed: 'The cost came down and stayed down.',
  defeated: 'The regression was answered in time.',
  escaped: 'It stopped being slow on its own, or nobody took it in time.',
  expired: 'Nobody worked on it while it was open.',
  failed: 'The cost did not come down.',
  excluded: 'The team left it out of the score: no points, no penalty.',
}

function countedWindows(n, hours) {
  if (hours === 24) return n === 1 ? t('one day') : t('{n} days', { n })
  const total = n * hours
  if (total % 24 === 0) return total === 24 ? t('one day') : t('{n} days', { n: total / 24 })
  return n === 1 ? t('one check') : t('{n} checks', { n })
}

/**
 * What a metric of the speed index actually measures. metricUnits above says how to write a number
 * for it; this says, to whoever did not choose it, what the number is about.
 */
export const metricWords = {
  http_slow_ratio: 'Out of 100 requests, how many take more than 2 seconds.',
  http_p95: 'Almost everybody waits less than this: only 5 requests out of 100 take longer.',
  http_error_rate: 'How many requests out of 10,000 end in an error the server could not answer.',
  php_stalls: 'How often PHP is caught taking too long, out of 1,000 requests.',
  db_slow_per_hour: 'How many seconds every hour the app spends waiting for slow statements.',
  db_worst: 'How long the single slowest statement took.',
  custom_p95: 'Almost every one of these is faster than this: only 5 out of 100 take longer.',
  custom_avg: 'The average of what you measure yourself.',
  custom_max: 'The highest your own measure got.',
  custom_per_hour: 'How many of these happen in an hour.',
}

export function metricInWords(metric) {
  return metricWords[metric] ? t(metricWords[metric]) : ''
}

/** One row of "where the points go": what it measures, what it weighs, where green turns red. */
export function scoreRowInWords(component, components = []) {
  return [metricInWords(component.metric), componentInWords(component, components)].filter(Boolean).join(' ')
}

/** The live numbers on the overview, which are short by necessity and cryptic by accident. */
export const liveTileWords = {
  requests: 'Requests your app answered in the last minutes.',
  p95: 'Almost everybody waits less than this: only 5 requests out of 100 take longer.',
  errors: 'Requests the server could not answer at all (500 and up).',
  db_slow: 'Time the app spent waiting for slow statements.',
  php_stalls: 'Times PHP was caught taking longer than the slow log allows.',
  machines: 'The machines sending data right now. Green means alive.',
}

export function liveTileInWords(tile) {
  return liveTileWords[tile] ? t(liveTileWords[tile]) : ''
}

/** What each list of slow things holds, said once above it instead of guessed from the columns. */
export const slowTabWords = {
  queries: 'Statements sent to the database, grouped by shape. "Time lost" is the waiting they cost in this period.',
  endpoints: 'Addresses your app answers on. "Time lost" counts only the requests over the slow line.',
  php: 'Where PHP was caught waiting: the line underneath is the code that was running.',
  hotspots: 'The files where the waiting piles up (last 7 days). Working on these pays the most.',
}

export function slowTabInWords(tab) {
  return slowTabWords[tab] ? t(slowTabWords[tab]) : ''
}

/** The badges on a slow statement: three letters that mean nothing until somebody says them. */
export const queryBadgeWords = {
  n_plus_one: 'The same statement run once per row: one would be enough.',
  explained: 'The database told us how it runs this statement. Open it to read why it is slow.',
  saved: 'How much waiting per day this costs less than before.',
  lost: 'How much waiting per day this costs more than before.',
}

export function queryBadgeInWords(badge) {
  return queryBadgeWords[badge] ? t(queryBadgeWords[badge]) : ''
}

/**
 * A project keeps the labels it was created with, so the ones written before this page learned to
 * speak are still out there. Known engine labels are said again in words; a label the team wrote
 * itself is left exactly as it is.
 */
export const componentNames = {
  'Requests over 2 s': 'Requests slower than 2 seconds',
  'Response time p95': 'Response time for most people',
  'PHP stalls every 1,000 requests': 'How often PHP gets stuck',
  'Slow query seconds per hour': 'Time lost on slow queries',
  'Worst query': 'The slowest query of all',
  '5xx errors every 10,000 requests': 'Requests the server failed',
  'Invoice PDF p95': 'Invoice PDF time',
}

export function componentName(component) {
  const label = component?.label || ''
  return componentNames[label] ? t(componentNames[label]) : label
}

/** How long a mission has held so far: "1 of 2 days", never "1/2 good windows". */
export function heldInWords(done, needed, hours = 24) {
  return t('{done} of {total}', { done: Number(done) || 0, total: countedWindows(Number(needed) || 1, Number(hours) || 24) })
}

/** What one deploy did to the speed index. */
export const verdictWords = {
  better: 'The speed index went up after this deploy.',
  worse: 'The speed index went down after this deploy.',
  neutral: 'The speed index barely moved.',
  no_data: 'Not enough traffic around this deploy to tell.',
}

/** The delivery bands, in the numbers that produce them rather than in DORA's names. */
export const deliveryWords = {
  elite: 'Five deploys a week or more, fewer than 15 out of 100 go wrong, and regressions are fixed within a day.',
  high: 'At least one deploy a week, and fewer than 30 out of 100 go wrong.',
  medium: 'A deploy every few weeks, and fewer than 45 out of 100 go wrong.',
  low: 'Deploys are rare, or too many of them leave things slower.',
}

/** What a measure of your own is: the words StatsD and Prometheus use are theirs, not a person's. */
export const measureKindWords = { timer: 'a time', gauge: 'a level', counter: 'a count' }

export function measureKindInWords(kind) {
  return measureKindWords[kind] ? t(measureKindWords[kind]) : kind || ''
}

/**
 * A row of the speed index, read as an opportunity instead of a grade: what it costs today and
 * what fixing it would give back. The server already sorts the rows by what they lose, so the
 * first one is where the next points are.
 */
export function pointsToGain(component) {
  const lost = Number(component?.lost) || 0
  if (!component?.available) return ''
  return lost < 0.05 ? t('full marks') : t('+{n} points', { n: Math.round(lost * 10) / 10 })
}

/** Where this measure stands today against the line where it stops losing points. */
export function componentNow(component) {
  if (!component?.available) return t('nobody is measuring this yet')
  return t('today {now} · full marks up to {goal}', {
    now: valueInWords(component.metric, component.value),
    goal: valueInWords(component.metric, component.good),
  })
}

/**
 * What the last days say beyond today's grade: the best day there has ever been, the average of
 * the last seven, and whether that average is going up or down. Days still being measured are
 * left out of the record: half a day is not a record.
 */
export function scoreTrend(days) {
  const closed = (days || []).filter((d) => d && d.closed && typeof d.score === 'number')
  const mean = (list) => (list.length ? list.reduce((sum, d) => sum + d.score, 0) / list.length : null)
  const best = closed.reduce((top, d) => (top === null || d.score > top.score ? d : top), null)
  const week = mean(closed.slice(-7))
  const before = mean(closed.slice(-14, -7))
  return {
    record: best ? { score: Math.round(best.score * 10) / 10, day: best.day } : null,
    week: week === null ? null : Math.round(week * 10) / 10,
    change: week === null || before === null ? null : Math.round((week - before) * 10) / 10,
  }
}
