# Symfony / Doctrine

## Where a slow query comes from
Without `slowpoke/symfony` a slow query has no file and no line. One command, and every query carries the
application line that ran it (the Twig template, when it comes from a template), plus the N+1 per route and
the traces of cron commands and Messenger messages:

```sh
composer require slowpoke/symfony
```

Then enable `Slowpoke\Symfony\SlowpokeBundle` in `config/bundles.php`. Nothing else to change.

## Count queries in a test
```php
public function testOrdersPageQueryCount(): void
{
    $client = static::createClient();
    $client->enableProfiler();
    $client->request('GET', '/account/orders');

    $db = $client->getProfile()->getCollector('db');
    self::assertLessThanOrEqual(5, $db->getQueryCount());
}
```

## N+1
- Fetch join in DQL/QueryBuilder: `->leftJoin('o.customer', 'c')->addSelect('c')`.
- `fetch: 'EAGER'` only for relations always needed; prefer explicit joins per query.
- Collections: `EXTRA_LAZY` for `count()`/`contains()` on big collections.
- Batch loading: `$em->getRepository(X::class)->findBy(['id' => $ids])`.

## Heavy queries
- Partial hydration / DTO: `SELECT NEW App\Dto\OrderRow(o.id, o.total) FROM ...`.
- Big iterations: `toIterable()` + `$em->clear()` every N rows.
- Array hydration for read-only lists: `getArrayResult()`.

## Indexes
Doctrine Migrations: `$this->addSql('CREATE INDEX CONCURRENTLY ...')` with
`public function isTransactional(): bool { return false; }` on PostgreSQL;
MySQL: `ALGORITHM=INPLACE, LOCK=NONE`. Declare it also in the mapping (`#[ORM\Index]`).
