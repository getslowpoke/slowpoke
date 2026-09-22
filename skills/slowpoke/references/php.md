# PHP without a framework (PDO / mysqli)

## Count queries in a test
Wrap PDO in a small decorator used in tests:
```php
final class CountingPDO extends PDO {
    public int $count = 0;
    public function prepare(string $query, array $options = []): PDOStatement|false { $this->count++; return parent::prepare($query, $options); }
    public function query(string $query, ?int $fetchMode = null, mixed ...$args): PDOStatement|false { $this->count++; return parent::query($query, $fetchMode, ...$args); }
}
```
Assert `$pdo->count` after calling the function that renders the page.

## N+1
Replace the per-item query with one `WHERE id IN (?, ?, ...)` built from the collected ids, then index the result by id in PHP.

## Heavy queries
- Always prepared statements with bound types (`PDO::PARAM_STR` for string keys: a number against a varchar disables the index).
- Select only needed columns; paginate with keyset (`WHERE id > ? ORDER BY id LIMIT ?`).
- Unbuffered queries for big exports (`PDO::MYSQL_ATTR_USE_BUFFERED_QUERY => false`).

## PHP waiting
`curl_setopt($ch, CURLOPT_TIMEOUT, 5)` and `CURLOPT_CONNECTTIMEOUT`; cache stable responses (APCu/Redis).
