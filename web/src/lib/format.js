import { locale } from './i18n.js'

const nf = (opts) => new Intl.NumberFormat(locale.value, opts)

export function num(v, digits = 0) {
  if (v === null || v === undefined || Number.isNaN(v)) return '—'
  return nf({ maximumFractionDigits: digits, minimumFractionDigits: 0 }).format(v)
}

export function compact(v) {
  if (v === null || v === undefined) return '—'
  return nf({ notation: 'compact', maximumFractionDigits: 1 }).format(v)
}

export function pct(v, digits = 1) {
  if (v === null || v === undefined) return '—'
  return `${num(v, digits)}%`
}

// Seconds of a single operation: 0.0042 -> "4.2 ms".
export function latency(seconds) {
  if (seconds === null || seconds === undefined) return '—'
  if (seconds < 0.001) return `${num(seconds * 1e6, 0)} µs`
  if (seconds < 1) return `${num(seconds * 1000, seconds < 0.01 ? 1 : 0)} ms`
  if (seconds < 60) return `${num(seconds, seconds < 10 ? 2 : 1)} s`
  return duration(seconds)
}

// Accumulated waiting time: 7260 -> "2h 1m".
export function duration(seconds) {
  if (seconds === null || seconds === undefined) return '—'
  const s = Math.round(Math.abs(seconds))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60 ? (s % 60) + 's' : ''}`.trim()
  if (s < 86400) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`
  return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`
}

export function bytes(v) {
  if (v === null || v === undefined) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let n = v
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${num(n, n < 10 && i > 0 ? 1 : 0)} ${units[i]}`
}

export function ago(unix) {
  if (!unix) return '—'
  const diff = Math.round(Date.now() / 1000 - unix)
  const rtf = new Intl.RelativeTimeFormat(locale.value, { numeric: 'auto' })
  if (Math.abs(diff) < 60) return rtf.format(-diff, 'second')
  if (Math.abs(diff) < 3600) return rtf.format(-Math.round(diff / 60), 'minute')
  if (Math.abs(diff) < 86400) return rtf.format(-Math.round(diff / 3600), 'hour')
  return rtf.format(-Math.round(diff / 86400), 'day')
}

export function dateTime(unix) {
  if (!unix) return '—'
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(unix * 1000))
}

export function day(iso) {
  if (!iso) return '—'
  return new Intl.DateTimeFormat(locale.value, { day: 'numeric', month: 'short' }).format(new Date(iso + 'T12:00:00'))
}

export function clock(unix) {
  return new Intl.DateTimeFormat(locale.value, { hour: '2-digit', minute: '2-digit' }).format(new Date(unix * 1000))
}

export function scoreColor(score) {
  if (score === null || score === undefined) return 'var(--color-ink-500)'
  if (score >= 85) return 'var(--color-good)'
  if (score >= 70) return '#b8d98a'
  if (score >= 50) return 'var(--color-warn)'
  return 'var(--color-bad)'
}
