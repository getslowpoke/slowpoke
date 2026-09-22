# Django

## Where a slow query comes from
Without the `slowpoke` package a slow query has no file and no line. One command, and every query carries
the application line that ran it, plus the N+1 per route and the traces of Celery tasks and cron commands:

```sh
pip install slowpoke-python
```

Then the two lines for this framework: see packages/python/README.md.

## Count queries in a test
```python
from django.test import TestCase

class OrdersPageTests(TestCase):
    def test_orders_page_query_count(self):
        OrderFactory.create_batch(30)
        self.client.force_login(self.user)
        with self.assertNumQueries(4):
            self.client.get("/account/orders/")
```
pytest-django: `django_assert_max_num_queries(5)`.

## N+1
- ForeignKey / OneToOne used in the loop: `select_related("customer")`.
- Reverse FK / ManyToMany: `prefetch_related("items__product")`, with `Prefetch(queryset=...)` to filter/select columns.
- Counts per row: `annotate(items_count=Count("items"))`.
- Django REST Framework: put `select_related/prefetch_related` in `get_queryset()`; nested serializers are the classic source of N+1.

## Heavy queries
- `only()`/`defer()` or `values()` for lists; never iterate `Model.objects.all()` on big tables: `iterator(chunk_size=2000)`.
- `exists()` instead of `count() > 0`; `count()` on huge tables per request: cache it.
- `__icontains` on big tables is a full scan: PostgreSQL trigram index (`GinIndex(opclasses=["gin_trgm_ops"])`) or search.
- Pagination deep pages: keyset pagination (e.g. `CursorPagination` in DRF).

## Indexes
```python
class Migration(migrations.Migration):
    atomic = False  # required for CONCURRENTLY
    operations = [
        AddIndexConcurrently("order", models.Index(fields=["customer", "status"], name="order_customer_status")),
    ]
```
(`from django.contrib.postgres.operations import AddIndexConcurrently`). Declare the index in `Meta.indexes` too.

## Waiting on external calls
`requests.get(url, timeout=(2, 5))`; move to Celery/RQ when the response is not needed synchronously.
