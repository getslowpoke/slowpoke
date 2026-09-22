import assert from 'node:assert'
import { test } from 'node:test'

globalThis.localStorage = { getItem: () => null, setItem: () => {} }
const { phpWait } = await import('../src/lib/phpWaits.js')

test('a stalled frame says what PHP was waiting on', () => {
  assert.strictEqual(phpWait('curl_exec() vendor/guzzlehttp/guzzle/src/Handler/CurlHandler.php:85').kind, 'http')
  assert.strictEqual(phpWait('curl_multi_select() vendor/guzzlehttp/guzzle/src/Handler/CurlMultiHandler.php:986').kind, 'http')
  assert.strictEqual(phpWait('exec() vendor/mikehaertl/php-shellcommand/src/Command.php:395').kind, 'program')
  assert.strictEqual(phpWait('exec() app/Services/PdfCompressService.php:314').kind, 'program')
  assert.strictEqual(phpWait('PDOStatement::execute() vendor/laravel/framework/src/Illuminate/Database/Connection.php:423').kind, 'db')
  assert.strictEqual(phpWait('usleep() app/Jobs/Retry.php:12').kind, 'sleep')
  assert.strictEqual(phpWait('array_map() app/Reports/Big.php:9').kind, 'code')
})
