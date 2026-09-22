<h1 align="center">slowpoke/symfony</h1>

<p align="center"><b>Which line of your code is slow. Not which query — which line.</b></p>

<p align="center">
<a href="https://github.com/christiancannata/slowpoke-symfony/actions/workflows/tests.yml"><img alt="tests" src="https://github.com/christiancannata/slowpoke-symfony/actions/workflows/tests.yml/badge.svg"></a>
<a href="https://github.com/christiancannata/slowpoke-symfony/actions/workflows/static.yml"><img alt="static analysis" src="https://github.com/christiancannata/slowpoke-symfony/actions/workflows/static.yml/badge.svg"></a>
<a href="https://github.com/christiancannata/slowpoke-symfony/actions/workflows/codeql.yml"><img alt="CodeQL" src="https://github.com/christiancannata/slowpoke-symfony/actions/workflows/codeql.yml/badge.svg"></a>
<a href="#performance"><img alt="runtime dependencies: 0" src="https://img.shields.io/badge/runtime%20dependencies-0-brightgreen"></a>
<a href="https://packagist.org/packages/slowpoke/symfony"><img alt="Packagist" src="https://img.shields.io/packagist/v/slowpoke/symfony"></a>
<a href="https://packagist.org/packages/slowpoke/symfony"><img alt="PHP" src="https://img.shields.io/packagist/dependency-v/slowpoke/symfony/php"></a>
<a href="https://scorecard.dev/viewer/?uri=github.com/christiancannata/slowpoke-symfony"><img alt="OpenSSF Scorecard" src="https://api.scorecard.dev/projects/github.com/christiancannata/slowpoke-symfony/badge"></a>
<a href="LICENSE"><img alt="MIT" src="https://img.shields.io/packagist/l/slowpoke/symfony"></a>
</p>

---

A slow query tells you *what* is slow. It never tells you **where**, and a tool that points at
`vendor/doctrine/dbal/src/Connection.php:1124` has told you nothing at all.

This bundle sends [Slowpoke](https://github.com/christiancannata/slowpoke) the file and line of **your**
code behind every Doctrine query — for every request, every Messenger message and every console command:

```
GET /orders/{id}                                    820 ms · 34 queries
  SELECT t0.id, t0.status FROM orders t0 WHERE …      4 ms   src/Controller/OrderController.php:42
  SELECT t0.name FROM customer t0 WHERE t0.id = ?     3 ms   src/Entity/Order.php:88   ← ×31, one per order
  SELECT SUM(total) FROM invoices WHERE order_id = ?  9 ms   templates/orders/show.html.twig:17
```

That last column is the whole point. Slowpoke turns it into N+1 detection and missions that name a file,
each with a price in seconds of waiting per day — so the argument about what to fix first is over.

**A query run from a Twig template points at the template**, at the line you wrote, not at the compiled
file in `var/cache`. That is usually where the N+1 is hiding.

**Cron and workers too.** A console command and a handled message are not endpoints, and the bundle does
not pretend they are: they go to the Jobs page with how long they took, how often they failed and the same
`file:line` for their queries. Nobody is waiting for them, which is exactly why nobody notices when they
get slower.

## Install

```sh
composer require slowpoke/symfony
```

There is no Flex recipe yet, so enable the bundle in `config/bundles.php`:

```php
return [
    // ...
    Slowpoke\Symfony\SlowpokeBundle::class => ['all' => true],
];
```

That is all. No SDK, no PHP extension beyond the defaults, no code to change, no key to carry, no account
anywhere. The bundle talks to the Slowpoke agent on the same machine, which needs one line in
`/etc/slowpoke/agent.yaml`:

```yaml
sources:
  - type: otlp          # the agent listens on 127.0.0.1:4318
```

Optional: copy [`config/packages/slowpoke.yaml`](config/packages/slowpoke.yaml) into your application to
change a setting. Without it the bundle behaves exactly the same.

## Performance

The rule this bundle is built on is the one the whole project follows: **never make the application
slower**. Measured, not claimed, and you can run it yourself with `./bin/bench` — everything the bundle
does *while a request is running*: the Doctrine middleware, the line behind each query, building the trace.

| | PHP 7.4 | PHP 8.3 |
|---|---|---|
| per query | 2.1 µs | 2.0 µs |
| **a request with 50 queries** | **0.11 ms** | **0.10 ms** |

For scale: a request that spends 800 ms in your code and your database pays about **one ten-thousandth** of
that to be measured. Everything else happens **after** your visitor already has the page:

| | |
|---|---|
| **Sent on `kernel.terminate`** | under php-fpm the response has already reached the client (`fastcgi_finish_request`), so the send is on nobody's clock. Workers and commands send as soon as they are done |
| **Never waits** | a hard time budget on the socket (`SLOWPOKE_TIMEOUT`, 0.1 s) and every error swallowed: an agent that is missing, slow or broken costs one trace, never a request |
| **Never copies your data** | `debug_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS)` with a bounded depth: no argument, ever |
| **Bounded** | 500 queries and 200 outbound calls described per request at most, the rest counted; statements over 10 000 characters cut |
| **Quiet when idle** | queries outside a request, a message or a command — a worker polling its transport, a `cache:clear` you ran by hand — are not recorded at all |
| **Nothing when off** | `enabled: false` in the configuration registers no listener and no middleware at all: Doctrine is not even wrapped |

1640 lines of PHP. Three Symfony components, all of which your application already has. No runtime
dependency of its own.

## What is sent, and what never is

Sent only to the agent on your machine or private network:

- **per request** — method, route template (`/orders/{id}`), status code, start and end time. When no route
  matched, the path without its query string;
- **per message** — the message class (for `RunCommandMessage` and the other Symfony wrappers, the command or message inside), the transport it came from, whether it failed;
- **per console command** — the command name (`app:invoices:close`), how long it took, whether it exited
  with an error;
- **per query** — the SQL **with placeholders**, exactly as the Doctrine driver received it, the database
  engine, the real duration, and the first line of your own code on the stack, outside `vendor/`, outside
  the kernel cache and outside this bundle;
- **per outbound HTTP call** made with Symfony's HTTP client (Stripe, a partner API, another service) —
  the method, the remote **host** (and its port when it is not 80/443), the response status, how long it
  took, whether it failed (a transport error or a 5xx), and the line of your code that made the call.
  Never the URL path, the query string, headers or bodies: they carry tokens and personal data. Only when
  `symfony/http-client` is installed; the bundle's own delivery to the agent is never traced.

**Never sent** — bound parameter values, request parameters, headers, cookies, session, the user, exception
messages. If you write literal values into raw SQL yourself (`$conn->executeQuery("… WHERE email =
'a@b.c'")`) they are part of the statement, and the agent redacts them before anything leaves the machine.

## Configuration

Everything has a default that works. Nothing has to be set.

| Variable | Default | |
|---|---|---|
| `SLOWPOKE_ENABLED` | `true` | `false` turns everything off: nothing recorded, nothing sent |
| `SLOWPOKE_OTLP_ENDPOINT` | `http://127.0.0.1:4318/v1/traces` | plain http to a local or private host only (private ranges, `localhost`, a Docker service name, `.local`/`.internal`); anything else is ignored |
| `SLOWPOKE_TIMEOUT` | `0.1` | seconds to connect, then to hand the trace over; past that it is dropped |
| `SLOWPOKE_SERVICE` | `symfony` | the name of this application in Slowpoke |
| `SLOWPOKE_MAX_QUERIES` | `500` | queries described per request, message or command; the rest are counted |
| `SLOWPOKE_HTTP_CLIENT` | `true` | record outbound calls made with Symfony's HTTP client |
| `SLOWPOKE_MAX_HTTP_CALLS` | `200` | outbound calls described per request, message or command; the rest are counted |

In `config/packages/slowpoke.yaml`, on top of those: `messenger` and `commands` (both `true`) turn each
kind of trace off, `skip_commands` adds command names to leave alone, `max_sql_length` (10 000),
`backtrace_limit` (60) and `code_root` (`%kernel.project_dir%`, paths are sent relative to it).

**Outbound calls.** The bundle decorates `http_client.transport` (Symfony 6.3+, which every scoped client
is built on) or `http_client` (5.4). Responses are lazy, so the decorator is an async one, like Symfony's
`RetryableHttpClient`: every chunk passes through it however you read the response — `getContent()`,
`toArray()`, `stream()` over many, or not at all (the destructor) — and the last chunk or the error ends
the span, with the time the client itself measured. On 6.3+ a retried call is one span per attempt.
`http_client: false` in the configuration does not even decorate the client; the environment variable
leaves the decorator in place but lets every call through untouched.

Workers are never traced as a command — `messenger:consume` and friends would hold one trace open for
hours and swallow the trace of every message they handle.

## Compatibility

| PHP | Symfony | Doctrine DBAL |
|---|---|---|
| 7.4, 8.0, 8.1 | 5.4 LTS | 3.3+ |
| 8.2 | 6.4 LTS | 3.3+ or 4 |
| 8.3, 8.4 | 7.x | 4 |

Every combination in that table runs the full suite on each push and every week, DBAL 3 and DBAL 4 both.
DoctrineBundle 2.7+ registers the middleware; **DBAL 2 is not supported** and Composer refuses it rather
than installing something that silently records nothing.

## Quality

| | |
|---|---|
| **66 tests, 254 assertions** | unit tests, and integration tests on a real Symfony application with DoctrineBundle, Twig and Messenger, on every combination above |
| **Same wire, both sides** | `spec/symfony_otlp_fixtures.json` in the Slowpoke repository holds payloads exactly as this bundle sends them, with what the agent must read from each. The agent's Go tests replay that file: a change here the agent cannot read fails there |
| **PHPStan level 6** and `composer audit` | in CI, on every push, against the oldest supported set |
| **CodeQL** and **OpenSSF Scorecard** | on the code and on the workflows, which are pinned by commit |
| **Signed provenance** | every release archive carries a Sigstore attestation |

```sh
./bin/test 8.3                                   # PHP 8.3, Symfony 7.x, DBAL 4
./bin/test 7.4                                   # PHP 7.4, Symfony 5.4, DBAL 3
./bin/test 8.2 'doctrine/dbal:^3.9'              # constraints instead of the defaults
./bin/test 8.3 --filter OriginFinder             # extra arguments go to PHPUnit
./bin/stan                                       # PHPStan level 6
./bin/bench                                      # the numbers in Performance, on your machine
```

Everything runs in Docker. Nothing is installed on your machine.

## Security

It reads no request data, sends nothing outside your machine or private network, and cannot break or slow a
request. What it does and never does, how to report a vulnerability and how to verify a release are in
[SECURITY.md](SECURITY.md):

```sh
gh attestation verify slowpoke-symfony-v0.1.0.zip --repo christiancannata/slowpoke-symfony
```

## License

MIT, see [LICENSE](LICENSE). Slowpoke itself is free and self-hosted: the measures stay on your machines,
and nothing about your application ever leaves them.
