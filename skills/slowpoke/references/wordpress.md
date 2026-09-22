# WordPress / WooCommerce

## Measure
- Query Monitor plugin in staging; in tests `$wpdb->num_queries` before/after, with `SAVEQUERIES` enabled.

## Usual debt
- `WP_Query` without `no_found_rows => true` when pagination is not needed (extra `SQL_CALC_FOUND_ROWS`).
- `'fields' => 'ids'` when only ids are needed; `update_post_meta_cache => false` / `update_post_term_cache => false` when not used.
- `meta_query` on `wp_postmeta` values scans the table: store searchable values in a custom table or taxonomy; `meta_key` + `meta_value` index only helps equality on short values.
- `wp_options` with `autoload = 'yes'` too big: every request loads it. Find the biggest:
  `SELECT option_name, LENGTH(option_value) FROM wp_options WHERE autoload IN ('yes','on') ORDER BY 2 DESC LIMIT 20;`
- Transients in the options table without object cache: add Redis object cache.
- WooCommerce: enable HPOS (custom order tables); avoid `wc_get_orders` in loops.
- Slow plugins: remote HTTP calls on every page (`wp_remote_get` without cache and timeout).

## Fix pattern
Cache expensive fragments with `wp_cache_get/set` (persistent object cache) or transients with a sensible TTL; move cron-like work to real cron (`DISABLE_WP_CRON` + system cron).
