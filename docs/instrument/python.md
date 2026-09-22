# Python: Django, FastAPI, Flask

> **There is a package of our own**, and it is the only way to get the file and line of every query —
> auto-instrumentation points inside `site-packages/`, which tells you nothing. It also traces Celery
> tasks and the management commands cron runs:
>
> ```sh
> pip install slowpoke-python
> ```
>
> Two lines per framework, in [packages/python/README.md](../../packages/python/README.md). The steps
> below are only for an application it does not cover.

```bash
pip install opentelemetry-distro opentelemetry-exporter-otlp-proto-http
opentelemetry-bootstrap -a install     # installs instrumentations for the libraries it finds
```

Run the app through `opentelemetry-instrument` with the common settings:

```bash
export OTEL_SERVICE_NAME=shop OTEL_TRACES_EXPORTER=otlp \
  OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
  OTEL_METRICS_EXPORTER=none OTEL_LOGS_EXPORTER=none

# Django (gunicorn)
DJANGO_SETTINGS_MODULE=shop.settings opentelemetry-instrument gunicorn shop.wsgi
# FastAPI (uvicorn; one worker process per instrumented process)
opentelemetry-instrument uvicorn app.main:app --host 0.0.0.0
# Flask
opentelemetry-instrument gunicorn app:app
```

gunicorn with `--preload`: initialise OpenTelemetry in `post_fork` instead (exporters do not
survive fork). Celery workers: `opentelemetry-instrument celery -A shop worker`.

Database spans come from the psycopg/psycopg2, mysqlclient/PyMySQL, SQLAlchemy and Django
instrumentations installed by `opentelemetry-bootstrap`. With SQLAlchemy on psycopg2 both
report the same statement, one inside the other: the agent counts it once.
