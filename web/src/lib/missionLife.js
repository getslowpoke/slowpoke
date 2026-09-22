// Where a mission stands, in words: who is on it, whether the fix is online, whether it held.
// The server says the stage; this turns it into the chip on a card and the steps on the page.
import { t } from './i18n.js'
import { ago, dateTime } from './format.js'
import { heldInWords } from './settingsWords.js'

/** A person as the team calls them: the part of the email before the @. */
export function who(person) {
  return String(person || '').split('@')[0] || t('somebody')
}

/** The chip of a mission's stage, or null when there is nothing to say (open, nobody on it). */
export function stageChip(m) {
  const hours = m.window_hours || 24
  switch (m.stage) {
    case 'improving':
      return { icon: '📉', tone: 'good', text: t('Already going down: {held}', { held: heldInWords(m.good_windows, m.need_windows, hours) }) }
    case 'claimed':
      return { icon: '🙋', tone: 'info', text: t('Taken by {who} {when}', { who: who(m.person), when: ago(m.claimed_at) }) }
    case 'review':
      return { icon: '🔀', tone: 'info', text: t('Pull request by {who}', { who: who(m.person) }) }
    case 'shipped':
      return { icon: '🚀', tone: 'warn', text: t('Online, being verified: {held}', { held: heldInWords(m.good_windows, m.need_windows, hours) }) }
    case 'holding':
      return { icon: '🛡', tone: 'good', text: t('Holding: {held}', { held: heldInWords(m.held_windows, m.watch_windows, hours) }) }
    case 'confirmed':
      return { icon: '🏅', tone: 'good', text: t('Confirmed: it held') }
    case 'relapsed':
      return { icon: '🔁', tone: 'bad', text: t('It came back') }
  }
  if (m.relapse_of) return { icon: '🔁', tone: 'bad', text: t('A fix that came back') }
  return null
}

export const toneClass = {
  good: '!border-good/30 !text-good',
  info: '!border-info/30 !text-info',
  warn: '!border-warn/30 !text-warn',
  bad: '!border-bad/30 !text-bad',
}

/** The steps of a mission, from taken to confirmed, each done or not, with its date. */
export function steps(m, life) {
  const hours = m.window_hours || 24
  const closed = m.status === 'completed'
  const out = [
    { key: 'claimed', icon: '🙋', done: !!life.claimed_at || closed, label: life.claimed_at ? t('Taken by {who}', { who: who(m.person) }) : t('Nobody took it'), when: life.claimed_at },
    { key: 'review', icon: '🔀', done: (life.pull_requests || []).length > 0, label: (life.pull_requests || []).length ? t('Pull request linked') : t('No pull request linked'), link: (life.pull_requests || []).at(-1)?.url },
    { key: 'shipped', icon: '🚀', done: !!life.shipped_at || closed, label: life.shipped_at ? t('Online with {deploy}', { deploy: life.shipped_deploy || t('a deploy') }) : t('Not online yet'), when: life.shipped_at },
    { key: 'verified', icon: '📏', done: closed, label: closed ? t('Measured under the target: done') : t('Measured under the target: {held}', { held: heldInWords(m.good_windows, m.need_windows, hours) }), when: closed ? m.closed_at : null },
  ]
  if (closed) {
    if (life.stage === 'relapsed') out.push({ key: 'watch', icon: '🔁', done: true, bad: true, label: t('It came back: a regression reopened it'), when: life.verdict_at, boss: life.relapsed_by })
    else if (life.stage === 'confirmed') out.push({ key: 'watch', icon: '🏅', done: true, label: t('Confirmed: it held for the whole watch'), when: life.verdict_at })
    else out.push({ key: 'watch', icon: '🛡', done: false, label: t('Still watched: {held}', { held: heldInWords(life.held_windows, life.watch_windows, hours) }) })
  }
  return out.map((s) => ({ ...s, whenText: s.when ? dateTime(s.when) : '' }))
}
