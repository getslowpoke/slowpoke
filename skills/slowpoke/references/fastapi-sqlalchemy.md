# FastAPI / SQLAlchemy 2

## Where a slow query comes from
Without the `slowpoke` package a slow query has no file and no line. One command, and every query carries
the application line that ran it, plus the N+1 per route and the traces of Celery tasks and cron commands:

```sh
pip install slowpoke-python
```

Then the two lines for this framework: see packages/python/README.md.

## Count queries in a test
```python
from sqlalchemy import event

def test_list_orders_query_count(client, engine):
    statements = []
    event.listen(engine, "before_cursor_execute", lambda *a: statements.append(a[2]))
    client.get("/orders")
    assert len(statements) <= 3
```

## N+1
- Many-to-one: `select(Order).options(joinedload(Order.customer))`.
- One-to-many / many-to-many: `selectinload(Order.items)` (one extra `IN` query instead of N).
- Pydantic `from_attributes` models trigger lazy loads while serializing: load relations explicitly first.
- Async sessions: lazy loading raises; always declare loader options.

## Heavy queries
- `load_only(Order.id, Order.total)` for lists; `yield_per(1000)` for big iterations.
- Avoid `func.lower(col)` in WHERE without a matching functional index.
- Connection pool sized for the workers (`pool_size`, `max_overflow`); set `pool_pre_ping=True`.
- Sync DB calls inside `async def` endpoints block the event loop: use `def` endpoints or an async driver.

## Indexes (Alembic)
```python
def upgrade():
    with op.get_context().autocommit_block():
        op.create_index("ix_orders_customer_status", "orders", ["customer_id", "status"], postgresql_concurrently=True)
```
