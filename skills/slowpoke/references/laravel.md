# Laravel

## Count queries in a test
```php
use Illuminate\Support\Facades\DB;

public function test_orders_page_does_not_run_n_plus_one(): void
{
    Order::factory()->count(30)->for(Customer::factory())->create();

    DB::enableQueryLog();
    $this->actingAs($user)->get('/account/orders')->assertOk();

    $this->assertLessThanOrEqual(5, count(DB::getQueryLog()));
}
```
Prevent regressions globally in local/test: `Model::preventLazyLoading(! app()->isProduction());`

## N+1
- `->with(['customer', 'items.product'])` on the query that feeds the loop.
- In Blade/resources, check accessors and `$appends` that run queries: an accessor calling a
  relation or a count runs one query per model serialized.
- Counts: `withCount('items')` instead of `$order->items()->count()` in a loop.
- `loadMissing()` when the relation may already be loaded.

## Heavy queries
- `select(['id', 'status', ...])` on wide tables (JSON/TEXT columns): never serialize full models into lists.
- Big loops: `chunkById(1000, ...)` or `lazyById()`; never `->get()` on unbounded tables.
- `whereIn` with thousands of ids: chunk it.
- Comparing a string column with an int (`where('entity_id', $id)`) disables the index: cast `(string) $id`.
- `whereJsonContains`/`->where('params->foo', ...)` cannot use a normal index: add a generated column + index.
- Counts shown on every page: `Cache::remember("key", 300, fn () => ...)`.

## Indexes
```php
return new class extends Migration {
    public function up(): void
    {
        DB::statement('ALTER TABLE `orders` ADD INDEX `idx_orders_customer_status` (`customer_id`, `status`), ALGORITHM=INPLACE, LOCK=NONE');
    }
    public function down(): void
    {
        Schema::table('orders', fn (Blueprint $t) => $t->dropIndex('idx_orders_customer_status'));
    }
};
```
PostgreSQL: `public $withinTransaction = false;` and `CREATE INDEX CONCURRENTLY`.

## Slow external calls (PHP waiting in curl_exec)
- Always set timeouts: `Http::timeout(5)->connectTimeout(2)`.
- Move to a queued job when the user does not need the result immediately.
- Cache stable responses.

## Jobs and commands
- Heavy work in `ShouldQueue` jobs; `$tries`, `$backoff`, `$timeout` set explicitly.
- Scheduled commands running on several servers: `->onOneServer()->withoutOverlapping()`.
