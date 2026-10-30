import { t, locale } from './i18n.js'

// The server writes the activity feed as English sentences and keeps them as written: rows from
// last month are still in the database. The panel recognizes each shape and says it again in the
// reader's language; a sentence it does not know is shown as it is.
const shapes = [
  [/^Deploy (.+) made the speed index better \((\+?-?[\d.]+)\)$/, 'Deploy {a} made the speed index better ({b})'],
  [/^Deploy (.+) made the speed index worse \((\+?-?[\d.]+)\)$/, 'Deploy {a} made the speed index worse ({b})'],
  [/^Deploy (.+): speed index unchanged$/, 'Deploy {a}: speed index unchanged'],
  [/^New level reached: (.+) \(([\d.]+)\)$/, 'New level reached: {a} ({b})'],
  [/^Badge unlocked: (.+)$/, 'Badge unlocked: {a}', (a) => [badgeTitle(a)]],
  [/^Debt repaid: (.+)$/s, 'Debt repaid: {a}'],
  [/^Regression fixed: (.+)$/s, 'Regression fixed: {a}'],
  [/^A regression escaped: (.+)$/s, 'A regression escaped: {a}'],
  [/^A regression appeared: (.+)$/s, 'A regression appeared: {a}'],
  [/^Fix confirmed: (.+)$/s, 'Fix confirmed: {a}'],
  [/^Came back: (.+)$/s, 'Came back: {a}'],
  [/^Going down after (\S+): (.+)$/s, 'Going down after {a}: {b}'],
  [/^Weekly debt budget kept: (.+) of waiting per day repaid$/, 'Weekly debt budget kept: {a} of waiting per day repaid'],
  [/^(.+) runs on (\S+) and (\S+) at the same time$/, '{a} runs on {b} and {c} at the same time'],
  [/^Pull request for mission #(\d+): (.+)$/s, 'Pull request for mission #{a}: {b}'],
  [/^(.+?) took: (.+)$/s, '{a} took: {b}'],
  [/^Online, being verified: (.+)$/s, 'Online, being verified: {a}'],
  [/^Waved away: (.+)$/s, 'Waved away: {a}'],
  [/^Escapes in 2 hours: (.+)$/s, 'Escapes in 2 hours: {a}'],
  [/^(.+?) overtakes (.+?): (\d+) to (\d+) points this month$/, '{a} overtakes {b}: {c} to {d} points this month'],
]

export function activityTitle(activity) {
  const title = String(activity?.title || '')
  for (const [re, sentence, map] of shapes) {
    const m = title.match(re)
    if (!m) continue
    const [a, b, c, d] = map ? map(...m.slice(1)) : m.slice(1)
    return t(sentence, { a, b, c, d })
  }
  return title
}

// Badge names and how to earn them, as defined in internal/server/game/days.go.
function badgeTitle(title) {
  return t(title)
}

export function badgeInWords(badge) {
  const title = badgeTitle(badge?.title || '')
  const description = String(badge?.description || '')
  const level = description.match(/^A full day at (\d+) points or more\.$/)
  return {
    title,
    description: level ? t('A full day at {n} points or more.', { n: level[1] }) : t(description),
  }
}

/** "2026-09" as the month and year in the reader's language; older servers only send the English name. */
export function seasonName(start, fallback = '') {
  const m = String(start || '').match(/^(\d{4})-(\d{2})$/)
  if (!m) return fallback
  try {
    return new Intl.DateTimeFormat(locale.value === 'it' ? 'it-IT' : 'en-GB', { month: 'long', year: 'numeric', timeZone: 'UTC' })
      .format(new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, 1)))
  } catch {
    return fallback
  }
}
