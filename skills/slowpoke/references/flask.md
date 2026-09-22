# Flask (Flask-SQLAlchemy)

## Where a slow query comes from
Without the `slowpoke` package a slow query has no file and no line. One command, and every query carries
the application line that ran it, plus the N+1 per route and the traces of Celery tasks and cron commands:

```sh
pip install slowpoke-python
```

Then the two lines for this framework: see packages/python/README.md.

## Count queries in a test
Use SQLAlchemy events on `db.engine` (see `fastapi-sqlalchemy.md`) inside the Flask test client request.

## N+1
Same loader options as SQLAlchemy: `joinedload` for many-to-one, `selectinload` for collections; set `lazy="raise"` on relationships in tests to catch hidden lazy loads.

## Usual debt
- Jinja templates accessing relations in loops (lazy loads per row).
- Global `before_request` hooks querying the database on every request: cache the value.
- `requests` calls without timeout in views; heavy work in views instead of Celery/RQ.
