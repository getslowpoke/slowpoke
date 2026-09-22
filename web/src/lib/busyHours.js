// The hours a project is judged on, as a person says them: "Mon-Fri 9-19", "lun-ven 9-19".
import { t, locale } from './i18n.js'

const names = {
  en: ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'],
  it: ['dom', 'lun', 'mar', 'mer', 'gio', 'ven', 'sab'],
}

export function dayName(d) {
  return (names[locale.value] || names.en)[d]
}

/** The busy hours in words, from what the server sends ({ known, manual, groups }). */
export function busyInWords(b) {
  if (!b || !b.known) return t('every hour, until there are enough days of traffic to learn them')
  const text = (b.groups || []).map((g) => (g.from_day === g.to_day ? dayName(g.from_day) : `${dayName(g.from_day)}-${dayName(g.to_day)}`) + ' ' + g.hours).join('; ')
  return (text || t('no hour')) + ' · ' + (b.manual ? t('set by hand by the team') : t('found by Slowpoke in the traffic'))
}
