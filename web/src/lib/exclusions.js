// Why something was left out of the score: the words a report shows next to its time.
import { t } from './i18n.js'

export const reasonWords = {
  external: 'It waits on somebody else\'s service',
  third_party: 'It comes from code we do not own',
  streaming: 'It stays open on purpose (streaming, long polling)',
  export: 'A heavy export or download',
  webhook: 'A webhook or a callback from outside',
  other: 'Another reason',
}

export function reasonInWords(reason) {
  return t(reasonWords[reason] || reasonWords.other)
}

/** What each kind is called where the team reads it. */
export const kindWords = { endpoint: 'Endpoint', query: 'Query', php: 'PHP waiting' }

/**
 * What to send as the target of an exclusion. An endpoint mission or cost carries its source
 * ("nginx:shop POST /checkout"), which is not part of what the team leaves out.
 */
export function excludeTarget(kind, target) {
  if (kind !== 'endpoint') return target
  const i = String(target).indexOf(' ')
  return i < 0 ? target : String(target).slice(i + 1)
}

/** How long an exclusion lasts: a date, or until somebody removes it. */
export const durations = [
  { days: 30, label: 'For 30 days' },
  { days: 90, label: 'For 90 days' },
  { days: 0, label: 'Until we remove it' },
]
