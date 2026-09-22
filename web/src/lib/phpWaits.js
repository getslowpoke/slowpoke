// What PHP was waiting on, in words: "curl_exec() vendor/guzzlehttp/.../CurlHandler.php:85" is an
// HTTP call to another service, and the line of the application that made it is the one to read.
import { t } from './i18n.js'

const kinds = [
  { kind: 'http', icon: '🌐', words: 'Waiting for an HTTP call to another service', re: /\bcurl_(multi_)?(exec|select)\b|\bstream_select\b.*guzzle|\bSoapClient\b/i },
  { kind: 'db', icon: '⛁', words: 'Waiting for the database', re: /\bPDO(Statement)?::|\bmysqli_|\bpg_(query|execute|fetch)|\bmysql_query\b/i },
  { kind: 'redis', icon: '⚡', words: 'Waiting for Redis', re: /\bRedis(Cluster)?::|predis/i },
  { kind: 'program', icon: '⚙️', words: 'Waiting for an external program', re: /\b(exec|shell_exec|proc_open|proc_close|system|passthru|popen|pclose)\(\)/i },
  { kind: 'sleep', icon: '💤', words: 'Sleeping on purpose', re: /\b(u?sleep|time_nanosleep|time_sleep_until)\(\)/i },
  { kind: 'file', icon: '📄', words: 'Reading or writing a file or a stream', re: /\b(file_get_contents|file_put_contents|fread|fwrite|fgets|fopen|stream_get_contents)\(\)/i },
]

/** What a stalled PHP frame was waiting on. */
export function phpWait(frame) {
  const f = String(frame || '')
  const found = kinds.find((k) => k.re.test(f))
  if (found) return { kind: found.kind, icon: found.icon, words: t(found.words) }
  return { kind: 'code', icon: '⏳', words: t('Busy in PHP code') }
}
