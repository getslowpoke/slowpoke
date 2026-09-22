# Security

## Reporting a vulnerability

Please do not open a public issue. Report it privately through
[GitHub security advisories](https://github.com/christiancannata/slowpoke-laravel/security/advisories/new),
or write to christiancannata@gmail.com. Reports are answered as soon as possible, and fixed releases credit the reporter unless asked otherwise.

## Supported versions

The latest 0.x release receives security fixes.

## What this package does, and what it never does

It runs inside your application, so it is kept small and easy to read (about 700 lines in `src/`).

- It only **reads** what Laravel already exposes: the matched route, the response status, the SQL of each query as
  passed to PDO, and a stack trace **without arguments** to find the application file and line.
- Of the request it reads the method, the route template, the response status and the **host** it was
  addressed to (the name in the `Host` header, without the port: `shop.example.com`), which is what lets
  a web server in front of the application on another machine be recognised as reporting the same
  requests instead of counting them twice.
- It **never reads** binding values, request parameters, any other header, cookies, sessions, users or
  exception messages.
- It **sends** one OTLP/JSON trace per request or job, after the response, only to a plain `http://` address on the
  same machine or a private network (`127.0.0.1:4318` by default). Public addresses are refused.
- It has a hard 0.1 s budget and swallows every error: a missing or broken agent can never break or slow a request.
- It does not write files, run shell commands, load remote code or phone home. Its only runtime dependencies are
  `illuminate/*` packages your application already has.

## The two alerts you will see on this repository

GitHub reports two advisories against `illuminate/database`, and they will stay there on purpose.
Both say the same thing: *any version below 6.20.26*. Laravel 5.8 went out of support in 2019 and
those holes were never patched there, so allowing 5.8 at all is enough to raise them.

This package supports Laravel 5.8 anyway, and neither advisory is in its code. An application still
on 5.8 already carries them, whatever it installs next; refusing to run there would take away the
one thing that can show what is slow in it, and change nothing about its security. The floors are at
the patched release of every branch that has one — 6.20.26, 7.30.6, 8.75 — so the only way to end up
with an affected version is to already be on one.

If your own policy says otherwise, require the package with a floor of your choice:
`composer require slowpoke/laravel illuminate/database:^8.75`.

## How releases can be verified

- Every push runs the test suite on PHP 7.4 to 8.5 and Laravel 5.8 to 13, PHPStan and `composer audit`.
- The repository is scored by [OpenSSF Scorecard](https://scorecard.dev/viewer/?uri=github.com/christiancannata/slowpoke-laravel).
- Release archives carry a signed build provenance (Sigstore). To verify one:

  ```sh
  gh attestation verify slowpoke-laravel-v0.1.2.zip --repo christiancannata/slowpoke-laravel
  ```
