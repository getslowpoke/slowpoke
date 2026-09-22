# Changelog

## 0.1.5 - 2026-09-21

- A Messenger message that only carries the real work is named after that work on the Jobs page, not
  after the wrapper: a console command queued as `RunCommandMessage` is the command (`app:report`), a
  `RedispatchMessage` is the message inside it, a scheduled `ServiceCallMessage` is
  `Service::method`, and a `RunProcessMessage` is `process <executable>` (`process pg_dump`).
  Arguments, options and command lines never reach the name: they may hold personal data or
  passwords. The wrappers are recognised by name, so Symfony 5.4 and applications without the process
  or scheduler components are unaffected.

## 0.1.4 - 2026-09-21

- Outbound HTTP calls made with Symfony's HTTP client during a request, a message or a command are now
  part of its trace: one span per call with the method, the remote host (the port only when it is not
  the scheme's default), the status, the time spent waiting and the line of your code that made it, so
  Slowpoke can say "this endpoint waits 8 s on api.stripe.com". A transport error and a 5xx answer are
  errors. The URL path, the query string, headers and bodies are never sent. The client is decorated
  only when `symfony/http-client` is installed; `SLOWPOKE_HTTP_CLIENT=false` (or `http_client: false`)
  turns it off and `SLOWPOKE_MAX_HTTP_CALLS` (200) bounds the calls described per trace, the rest are
  counted (`slowpoke.dropped_http_calls`).

## 0.1.3 - 2026-09-20

- The package says which host it answered for (`server.address`). With a web server in front of the
  application on **another machine** - an ingress, a load balancer, containers elsewhere - no agent
  sees both the access log and the traces, so until now the same requests were counted twice: in the
  traffic, in the speed index and in the technical debt built from them. The host is what links the
  two sides; on one machine the agent already paired single requests and still does.

## 0.1.2 - 2026-09-19

- The version the bundle reports to the agent is the version of the release: 0.1.1 still said 0.1.0.

## 0.1.1 - 2026-09-19

Nothing changes in the bundle itself: the first public run of the test matrix found two things in
the tests and fixed them.

- The test application kept its cache under a directory named after the vendor directory, which in
  a plain install is literally `vendor/`: the origin finder skips everything inside one, so the
  compiled Twig templates were never resolved and a query in a template pointed at the controller.
  The check that a template keeps its own line is back to testing what it says.
- The cache warmup test asked the application for every optional warmer, which on Symfony 5.4 with
  a current Twig dies inside twig-bundle. It asks this bundle's warmer instead, which is its
  subject. `symfony/error-handler` is pinned with the other components in the matrix, or Composer
  installs a newer major next to Symfony 5.4.

## 0.1.0 - 2026-09-18

First release.

- Every request is one trace: the route template (`/orders/{id}`), the status, and each Doctrine
  query with the `file:line` of the application code that ran it — including the Twig template,
  when the query comes from a template. Sent on `kernel.terminate`, after the response has left.
- The N+1 that hides in a loop is visible as what it is: the same statement, counted, with the one
  line behind it.
- Messenger messages and console commands (cron) are traced too, one trace each, with their
  queries. Workers are left alone: `messenger:consume` would hold one trace open for hours.
- Symfony 5.4, 6.4 and 7.x, PHP 7.4 to 8.4, Doctrine DBAL 3.3+ and 4. DBAL 2 is not supported.
- Statement text only, never the bound values. Plain `http` to the agent on the same machine or a
  private network, with a 0.1 s budget: a missing or slow agent costs a trace, never a request.
