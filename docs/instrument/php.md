# PHP: Laravel, Symfony, Slim, WordPress, plain PDO

> **Laravel and Symfony have a package of their own**, and it is the only way to get the file and line of
> every query — auto-instrumentation points inside `vendor/`, which tells you nothing:
>
> ```sh
> composer require slowpoke/laravel    # see ../../packages/laravel/README.md
> composer require slowpoke/symfony    # see ../../packages/symfony/README.md
> ```
>
> Both also trace queued work and cron. The steps below are for the other PHP applications.

```bash
pecl install opentelemetry          # enable with extension=opentelemetry.so
composer require open-telemetry/sdk open-telemetry/exporter-otlp \
  open-telemetry/opentelemetry-auto-pdo
# plus one of:
composer require open-telemetry/opentelemetry-auto-slim
composer require open-telemetry/opentelemetry-auto-wordpress
```

php-fpm pool (`/etc/php/8.3/fpm/pool.d/www.conf`):

```ini
env[OTEL_PHP_AUTOLOAD_ENABLED] = true
env[OTEL_SERVICE_NAME] = shop
env[OTEL_TRACES_EXPORTER] = otlp
env[OTEL_EXPORTER_OTLP_ENDPOINT] = http://127.0.0.1:4318
env[OTEL_EXPORTER_OTLP_PROTOCOL] = http/protobuf
env[OTEL_METRICS_EXPORTER] = none
env[OTEL_LOGS_EXPORTER] = none
```

Reload php-fpm. The PDO instrumentation reports every statement; the framework
instrumentation names the route.

php-fpm in Docker: the slowlog reads the stack of slow workers with ptrace, which containers
forbid by default. Add `cap_add: [SYS_PTRACE]` to the php-fpm service, or the log stays empty
and php-fpm prints `failed to ptrace(ATTACH) child`.

Behind nginx each request is seen twice, in the access log and as a trace: on the same machine
the agent pairs them and counts it once, from the access log. When the web server runs somewhere
else - a load balancer, an ingress, a container elsewhere - nobody sees both sides: the package
sends the host it answered for (`server.address`) and the server counts the web server's line,
which is the whole wait. Give the web source a `site:` in `agent.yaml` when its name is not the
host itself. Keep the php-fpm slowlog source enabled in the agent too:
it shows where PHP waits (external calls, locks) even without traces.
