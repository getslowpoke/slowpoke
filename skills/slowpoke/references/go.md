# Go (net/http, Gin, Echo, Fiber; database/sql, sqlx, GORM, pgx)

## Count queries in a test
- database/sql: wrap the driver (e.g. a small `driver.Connector` wrapper) or use `sqlmock` expectations for query shape.
- GORM: `db.Callback().Query().After("gorm:query").Register("count", func(*gorm.DB) { n++ })`.
- pgx: a `QueryTracer` counting `TraceQueryStart`.
Call the handler with `httptest.NewRecorder()` and assert the count.

## N+1
- GORM: `Preload("Items.Product")` or `Joins("Customer")`; never query inside `for _, o := range orders`.
- sqlx/pgx: collect ids, one `WHERE id = ANY($1)` (PostgreSQL) or `IN (?)` with `sqlx.In`, map by id.

## Heavy queries
- Always pass a `context.Context` with timeout to queries and HTTP calls (`http.Client{Timeout: 5 * time.Second}`).
- Configure the pool: `SetMaxOpenConns`, `SetMaxIdleConns`, `SetConnMaxLifetime`; too small a pool shows as waiting, not as slow queries.
- Close `rows` (`defer rows.Close()`) and check `rows.Err()`: leaked rows exhaust the pool.
- Select only needed columns; stream big results with `rows.Next()` instead of loading slices.

## Indexes
Migrations (golang-migrate, goose): for PostgreSQL `CREATE INDEX CONCURRENTLY` in its own migration without transaction (`-- +goose NO TRANSACTION`).
