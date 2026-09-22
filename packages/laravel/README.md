<h1 align="center">slowpoke/laravel</h1>

<p align="center"><b>Which line of your code is slow. Not which query — which line.</b></p>

<p align="center">
<a href="https://github.com/christiancannata/slowpoke-laravel/actions/workflows/tests.yml"><img alt="tests" src="https://github.com/christiancannata/slowpoke-laravel/actions/workflows/tests.yml/badge.svg"></a>
<a href="https://github.com/christiancannata/slowpoke-laravel/actions/workflows/static.yml"><img alt="static analysis" src="https://github.com/christiancannata/slowpoke-laravel/actions/workflows/static.yml/badge.svg"></a>
<a href="https://github.com/christiancannata/slowpoke-laravel/actions/workflows/codeql.yml"><img alt="CodeQL" src="https://github.com/christiancannata/slowpoke-laravel/actions/workflows/codeql.yml/badge.svg"></a>
<a href="#performance"><img alt="runtime dependencies: 0" src="https://img.shields.io/badge/runtime%20dependencies-0-brightgreen"></a>
<a href="https://packagist.org/packages/slowpoke/laravel"><img alt="Packagist" src="https://img.shields.io/packagist/v/slowpoke/laravel"></a>
<a href="https://packagist.org/packages/slowpoke/laravel"><img alt="PHP" src="https://img.shields.io/packagist/dependency-v/slowpoke/laravel/php"></a>
<a href="https://scorecard.dev/viewer/?uri=github.com/christiancannata/slowpoke-laravel"><img alt="OpenSSF Scorecard" src="https://api.scorecard.dev/projects/github.com/christiancannata/slowpoke-laravel/badge"></a>
<a href="LICENSE"><img alt="MIT" src="https://img.shields.io/packagist/l/slowpoke/laravel"></a>
</p>

---

A slow query tells you *what* is slow. It never tells you **where**, and a tool that points at
`vendor/laravel/framework/src/Illuminate/Database/Connection.php:365` has told you nothing at all.

This package sends [Slowpoke](https://github.com/christiancannata/slowpoke) the file and line of **your**
code behind every query — for every request, every queued job and every scheduled command:

```
GET /orders/{id}                                    820 ms · 34 queries
  select * from orders where id = ?                   4 ms   app/Http/Controllers/OrderController.php:42
  select * from users where id = ?                    3 ms   app/Models/Order.php:88   ← ×31, one per order
  select sum(total) from invoices where order_id = ?  9 ms   resources/views/orders/show.blade.php:17
```

That last column is the whole point. Slowpoke turns it into N+1 detection and missions that name a file,
each with a price in seconds of waiting per day — so the argument about what to fix first is over.

**Jobs and cron too.** A queued job and a scheduled command are not endpoints, and the package does not
pretend they are: they go to the Jobs page with how long they took, how often they failed and the same
`file:line` for their queries. Nobody is waiting for them, which is exactly why nobody notices when they
get slower.

## Install

```sh
composer require slowpoke/laravel
```

That is all. The service provider is auto-discovered and the package talks to the Slowpoke agent on the
same machine, which needs one line in `/etc/slowpoke/agent.yaml`:

```yaml
sources:
  - type: otlp          # listens on 127.0.0.1:4318
```

No OpenTelemetry SDK, no PHP extension beyond the defaults, no code to change, no key to carry, no account
anywhere. `php artisan vendor:publish --tag=slowpoke-config` if you want the config file in your repo.

## Performance

The rule this package is built on is the one the whole project follows: **never make the application
slower**. Measured, not claimed, and you can run it yourself with `./bin/bench` — everything the package
does *while a request is running*: the query listener, the line behind each query, building the trace.

| | PHP 7.4 | PHP 8.3 |
|---|---|---|
| per query | 2.0 µs | 1.9 µs |
| **a request with 50 queries** | **0.10 ms** | **0.09 ms** |

For scale: a request that spends 800 ms in your code and your database pays about **one ten-thousandth** of
that to be measured. Everything else happens **after** your visitor already has the page:

| | |
|---|---|
| **Sent after the response** | from a terminating callback: under php-fpm the response has already reached the client (`fastcgi_finish_request`), so the send is on nobody's clock. Queue workers send right after each job |
| **Never waits** | a hard time budget on the socket (`SLOWPOKE_TIMEOUT`, 0.1 s) and every error swallowed: an agent that is missing, slow or broken costs one trace, never a request |
| **Never copies your data** | `debug_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS)` with a bounded depth: no argument, ever |
| **Bounded** | 500 queries and 200 outbound calls described per request at most, the rest counted; statements over 10 000 characters cut |
| **Quiet when idle** | queries outside a request, a job or a scheduled command — a worker polling its queue, an artisan command you ran by hand — are not recorded at all |

834 lines of PHP. Three Laravel contracts. No runtime dependency of its own.

## What is sent, and what never is

Sent, as OTLP/JSON, only to the agent on your machine or private network:

- **per request** — method, route template (`/orders/{id}`), status code, start and end time. When no route
  matched, the path without its query string;
- **per job** — the job class, the queue name, whether it failed;
- **per scheduled command** — the command as you wrote it in the scheduler (`invoices:close`), how long it
  took, whether it failed;
- **per query** — the SQL **with placeholders**, exactly as Laravel hands it to PDO, the database engine,
  the real duration, and the first line of your own code on the stack, outside `vendor/` and outside this
  package. For a query issued from a Blade view: the template, not the compiled file;
- **per outbound HTTP call** made with the `Http` facade (Stripe, a partner API, another service) — the
  method, the remote **host** (and its port when it is not 80/443), the response status, how long the
  request waited, whether it failed, and the line of your code that made the call. Never the URL path,
  the query string, headers or bodies: they carry tokens and personal data. Calls made with Guzzle or cURL
  directly send no event and are not seen; the package's own delivery to the agent is never traced.

**Never sent** — binding values, request parameters, headers, cookies, session, the user, exception
messages. If you write literal values into raw SQL yourself (`DB::select("… where email = 'a@b.c'")`) they
are part of the statement, and the agent redacts them before anything leaves the machine.

## Configuration

Everything has a default that works. Nothing has to be set.

| Variable | Default | |
|---|---|---|
| `SLOWPOKE_ENABLED` | `true` | `false` turns everything off: no listener, no middleware, nothing sent |
| `SLOWPOKE_OTLP_ENDPOINT` | `http://127.0.0.1:4318/v1/traces` | plain http to a local or private host only (private ranges, `localhost`, a Docker service name, `.local`/`.internal`); anything else is ignored |
| `SLOWPOKE_TIMEOUT` | `0.1` | seconds to connect, then to hand the trace over; past that it is dropped |
| `SLOWPOKE_SERVICE` | `APP_NAME` | the name of this app in Slowpoke |
| `SLOWPOKE_JOBS` | `true` | trace queued jobs too |
| `SLOWPOKE_SCHEDULE` | `true` | trace scheduled commands (`app/Console/Kernel.php`) |
| `SLOWPOKE_MAX_QUERIES` | `500` | queries described per request or job; the rest are counted |
| `SLOWPOKE_HTTP_CLIENT` | `true` | record outbound calls made with the `Http` facade (Laravel 8 and later) |
| `SLOWPOKE_MAX_HTTP_CALLS` | `200` | outbound calls described per request or job; the rest are counted |
| `SLOWPOKE_MAX_SQL_LENGTH` | `10000` | longer statements are cut |
| `SLOWPOKE_BACKTRACE_LIMIT` | `60` | stack frames inspected to find your line |
| `SLOWPOKE_CODE_ROOT` | `base_path()` | file paths are sent relative to it |

## Compatibility

| PHP | Laravel |
|---|---|
| 7.4 | 5.8, 6, 7, 8 |
| 8.0 | 9 |
| 8.1 | 10 |
| 8.2 | 11 |
| 8.3 | 12 |
| 8.4, 8.5 | 13 |

Every combination in that table runs the full suite on each push and every week. Other combinations Laravel
itself allows (PHP 8.3 with Laravel 10, say) work too: the package is written in PHP 7.4 syntax and uses
only APIs present since Laravel 5.8. **A ten-year-old application gets the same answers as a new one** —
which is the point, because those are the ones nobody can see inside. Laravel 5.8 has advisories of its own
that were never patched there, and [SECURITY.md](SECURITY.md) says what that means for this package.

## Quality

| | |
|---|---|
| **49 tests, 170 assertions** | on every PHP and Laravel combination above, on each push and weekly |
| **Same wire, both sides** | `spec/laravel_otlp_fixtures.json` in the Slowpoke repository holds payloads exactly as this package sends them, with what the agent must read from each. The agent's Go tests replay that file: a change here the agent cannot read fails there |
| **PHPStan level 6** and `composer audit` | in CI, on every push |
| **CodeQL** and **OpenSSF Scorecard** | on the code and on the workflows, which are pinned by commit |
| **Signed provenance** | every release archive carries a Sigstore attestation |

```sh
./bin/test 8.3                                # PHP 8.3, latest Laravel
./bin/test 7.4                                # PHP 7.4, Laravel 8
./bin/test 7.4 'orchestra/testbench:3.8.*'    # PHP 7.4, Laravel 5.8
./bin/test 8.3 --filter OriginFinder          # extra arguments go to PHPUnit
./bin/bench                                   # the numbers in Performance, on your machine
```

Everything runs in Docker. Nothing is installed on your machine.

## Security

It reads no request data, sends nothing outside your machine or private network, and cannot break or slow a
request. What it does and never does, how to report a vulnerability and how to verify a release are in
[SECURITY.md](SECURITY.md):

```sh
gh attestation verify slowpoke-laravel-v0.1.2.zip --repo christiancannata/slowpoke-laravel
```

## License

MIT, see [LICENSE](LICENSE). Slowpoke itself is free and self-hosted: the measures stay on your machines,
and nothing about your application ever leaves them.
