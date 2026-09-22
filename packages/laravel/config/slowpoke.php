<?php

return [
    // Turn everything off without touching code: no listener, no middleware, nothing sent.
    'enabled' => env('SLOWPOKE_ENABLED', true),

    // The Slowpoke agent's OTLP receiver. Plain http to a local or private host only.
    'endpoint' => env('SLOWPOKE_OTLP_ENDPOINT', 'http://127.0.0.1:4318/v1/traces'),

    // Seconds to connect, and then to hand over the trace. Past that the trace is dropped.
    'timeout' => (float) env('SLOWPOKE_TIMEOUT', 0.1),

    // Name of this application in Slowpoke. Defaults to APP_NAME.
    'service' => env('SLOWPOKE_SERVICE'),

    // Trace queued jobs as well as HTTP requests.
    'jobs' => env('SLOWPOKE_JOBS', true),

    // Trace scheduled commands (the ones in app/Console/Kernel.php), with their queries.
    'schedule' => env('SLOWPOKE_SCHEDULE', true),

    // Queries kept per request or job; the rest are counted, not described.
    'max_queries' => (int) env('SLOWPOKE_MAX_QUERIES', 500),

    // Record outbound calls made with the Http facade: method, remote host, status, duration and
    // the line that made them. Never the URL path, the query string, headers or bodies.
    'http_client' => env('SLOWPOKE_HTTP_CLIENT', true),

    // Outbound calls kept per request or job; the rest are counted, not described.
    'max_http_calls' => (int) env('SLOWPOKE_MAX_HTTP_CALLS', 200),

    // Longer statements are cut (huge IN lists, bulk inserts).
    'max_sql_length' => (int) env('SLOWPOKE_MAX_SQL_LENGTH', 10000),

    // Stack frames inspected to find the application line that ran a query.
    'backtrace_limit' => (int) env('SLOWPOKE_BACKTRACE_LIMIT', 60),

    // Code paths are sent relative to this directory. Defaults to base_path().
    'code_root' => env('SLOWPOKE_CODE_ROOT'),
];
