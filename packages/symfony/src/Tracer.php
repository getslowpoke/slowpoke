<?php

namespace Slowpoke\Symfony;

/**
 * Copied from slowpoke/laravel, to be extracted into a shared package: keep the two in step.
 *
 * Collects one trace per HTTP request, handled message or console command and encodes it for the agent:
 * a SERVER (or CONSUMER) span and one CLIENT span per query, with the SQL as the driver received
 * it (placeholders, never binding values) and the application line that ran it, plus one CLIENT
 * span per outbound HTTP call, which names the remote host and nothing else of the URL.
 */
class Tracer
{
    public const VERSION = '0.1.5';

    private const SERVER = 2;
    private const CLIENT = 3;
    private const CONSUMER = 5;

    /** @var OriginFinder */
    private $origin;
    /** @var callable(): Sender */
    private $sender;
    /** @var callable(): float */
    private $clock;
    /** @var callable(int): string */
    private $ids;
    /** @var string */
    private $service;
    /** @var string */
    private $version;
    /** @var int */
    private $maxQueries;
    /** @var int */
    private $maxSqlLength;
    /** @var int */
    private $maxHttpCalls;
    /** @var string|null "host:port" of the agent: the package never traces its own delivery */
    private $agent;
    /** @var array<string, mixed>|null the trace being recorded */
    private $trace;

    /**
     * @param callable(): Sender $sender resolved only when a trace is sent
     * @param array{service?: string, version?: string, max_queries?: int, max_sql_length?: int, max_http_calls?: int, agent_endpoint?: string} $options
     */
    public function __construct(OriginFinder $origin, callable $sender, array $options, ?callable $clock = null, ?callable $ids = null)
    {
        $this->origin = $origin;
        $this->sender = $sender;
        $this->service = (string) ($options['service'] ?? 'symfony');
        $this->version = (string) ($options['version'] ?? self::VERSION);
        $this->maxQueries = max(0, (int) ($options['max_queries'] ?? 500));
        $this->maxSqlLength = max(1, (int) ($options['max_sql_length'] ?? 10000));
        $this->maxHttpCalls = max(0, (int) ($options['max_http_calls'] ?? 200));
        $agent = isset($options['agent_endpoint']) ? self::target((string) $options['agent_endpoint']) : null;
        $this->agent = $agent === null ? null : $agent[0] . ':' . $agent[1];
        $this->clock = $clock ?: function () { return microtime(true); };
        $this->ids = $ids ?: function (int $bytes) { return bin2hex(random_bytes($bytes)); };
    }

    public function startRequest(string $method, ?float $requestStart): void
    {
        try {
            $now = ($this->clock)();
            // php-fpm gives the moment the request arrived, which is what nginx measures too.
            // Long-running servers keep a stale value: fall back to now.
            $start = $requestStart !== null && $requestStart <= $now && $now - $requestStart < 300 ? $requestStart : $now;
            $this->trace = $this->newTrace(self::SERVER, strtoupper($method), $start);
            $this->trace['method'] = strtoupper($method);
        } catch (\Throwable $e) {
            $this->trace = null;
        }
    }

    public function finishRequest(?string $route, string $path, int $status, ?string $host = null): void
    {
        if (!$this->running(self::SERVER)) {
            return;
        }
        try {
            $t = &$this->trace;
            $t['end'] = ($this->clock)();
            $t['attributes'][] = self::kv('http.request.method', $t['method']);
            if ($route !== null && $route !== '') {
                $route = '/' . ltrim($route, '/');
                $t['name'] = $t['method'] . ' ' . $route;
                $t['attributes'][] = self::kv('http.route', $route);
            } else {
                $t['attributes'][] = self::kv('url.path', '/' . ltrim((string) strtok($path, '?'), '/'));
            }
            $t['attributes'][] = self::kv('http.response.status_code', $status);
            // The host this request was answered for. With a web server in front on another
            // machine, it is the only thing that says its access log and this trace are the
            // same requests, so that nobody counts them twice.
            if ($host !== null && $host !== '') {
                $t['attributes'][] = self::kv('server.address', strtolower($host));
            }
            $t['error'] = $status >= 500;
        } catch (\Throwable $e) {
            $this->trace = null;
        }
    }

    public function startJob(string $name, string $queue): void
    {
        if ($this->trace !== null && $this->trace['end'] === null) {
            return; // a job run inside a request or another job belongs to it
        }
        try {
            $this->trace = $this->newTrace(self::CONSUMER, $name, ($this->clock)());
            $this->trace['attributes'][] = self::kv('slowpoke.kind', 'job');
            $this->trace['attributes'][] = self::kv('messaging.destination.name', $queue);
        } catch (\Throwable $e) {
            $this->trace = null;
        }
    }

    public function finishJob(bool $failed): void
    {
        if (!$this->running(self::CONSUMER)) {
            return;
        }
        $this->trace['end'] = ($this->clock)();
        $this->trace['error'] = $failed;
    }

    /**
     * A console command, which on a server means cron: nobody is waiting for it, which is exactly
     * why nobody notices when it gets slower. Same trace as a message, and the queries inside it
     * keep their file:line.
     */
    public function startCommand(string $name): void
    {
        if ($this->trace !== null && $this->trace['end'] === null) {
            return; // a command run inside something else belongs to it
        }
        try {
            $this->trace = $this->newTrace(self::CONSUMER, $name, ($this->clock)());
            $this->trace['attributes'][] = self::kv('slowpoke.kind', 'command');
        } catch (\Throwable $e) {
            $this->trace = null;
        }
    }

    public function finishCommand(bool $failed): void
    {
        $this->finishJob($failed);
    }

    /** @param float $milliseconds as measured around the statement; $driver as Doctrine names it (pdo_pgsql) */
    public function recordQuery(string $sql, float $milliseconds, string $driver): void
    {
        if ($this->trace === null || $this->trace['end'] !== null) {
            return; // outside requests and jobs (commands, worker polling), or after the response
        }
        try {
            $end = ($this->clock)();
            if (count($this->trace['queries']) >= $this->maxQueries) {
                $this->trace['dropped']++;
                return;
            }
            $this->trace['queries'][] = [
                'sql' => strlen($sql) > $this->maxSqlLength ? substr($sql, 0, $this->maxSqlLength) : $sql,
                'system' => self::dbSystem($driver),
                'start' => max($this->trace['start'], $end - max(0.0, $milliseconds) / 1000),
                'end' => $end,
                'origin' => $this->origin->find(),
            ];
        } catch (\Throwable $e) {
            // never let observability break the query that was just run
        }
    }

    /**
     * An outbound HTTP call leaves the application. $key pairs it with its end (the id of the
     * request object); the URL is reduced to scheme, host and port here and never kept.
     *
     * @param int|string $key
     */
    public function startHttpCall($key, string $method, string $url): void
    {
        if ($this->trace === null || $this->trace['end'] !== null) {
            return; // same rule as queries: outside requests and jobs nothing is recorded
        }
        try {
            $target = self::target($url);
            if ($target === null || $target[0] . ':' . $target[1] === $this->agent) {
                return;
            }
            if (count($this->trace['http']) >= $this->maxHttpCalls) {
                $this->trace['droppedHttp']++;
                return;
            }
            if (count($this->trace['pending']) >= 64) {
                $this->trace['pending'] = []; // ends that never came: they close with the trace
            }
            $this->trace['http'][] = [
                'method' => strtoupper($method) ?: 'GET', 'host' => $target[0], 'port' => $target[2] ? null : $target[1],
                'start' => ($this->clock)(), 'end' => null, 'status' => null, 'error' => false,
                'origin' => $this->origin->find(),
            ];
            $this->trace['pending'][(string) $key] = count($this->trace['http']) - 1;
        } catch (\Throwable $e) {
            // never let observability break the call the application is making
        }
    }

    /**
     * The call ended: a response ($status), or a failure (null). When $key is unknown - Laravel 9+
     * wraps the request of a failed connection in a new object - the latest open call to the same
     * method and host is the one that ended. $end, when the client measured it, is more exact than
     * the moment the application looked at the response.
     *
     * @param int|string $key
     */
    public function finishHttpCall($key, ?int $status, string $method, string $url, ?float $end = null): void
    {
        if ($this->trace === null || $this->trace['end'] !== null) {
            return;
        }
        try {
            $key = (string) $key;
            $i = $this->trace['pending'][$key] ?? null;
            if ($i === null && ($target = self::target($url)) !== null) {
                $method = strtoupper($method);
                foreach (array_reverse($this->trace['pending'], true) as $k => $candidate) {
                    $call = $this->trace['http'][$candidate];
                    if ($call['host'] === $target[0] && $call['method'] === $method) {
                        [$key, $i] = [(string) $k, $candidate];
                        break;
                    }
                }
            }
            if ($i === null) {
                return;
            }
            unset($this->trace['pending'][$key]);
            $call = &$this->trace['http'][$i];
            $now = ($this->clock)();
            $call['end'] = $end !== null && $end >= $call['start'] && $end <= $now ? $end : $now;
            $call['status'] = $status;
            $call['error'] = $status === null || $status >= 500;
        } catch (\Throwable $e) {
            // never let observability break the call the application is making
        }
    }

    /** Sends the finished trace, if any, and forgets it. */
    public function flush(): void
    {
        if ($this->trace === null || $this->trace['end'] === null) {
            return;
        }
        $trace = $this->trace;
        $this->trace = null;
        try {
            ($this->sender)()->send($this->encode($trace));
        } catch (\Throwable $e) {
            // the agent is optional: a missing or broken one costs a trace, nothing else
        }
    }

    public function reset(): void
    {
        $this->trace = null;
    }

    private function running(int $kind): bool
    {
        return $this->trace !== null && $this->trace['kind'] === $kind && $this->trace['end'] === null;
    }

    /** @return array<string, mixed> */
    private function newTrace(int $kind, string $name, float $start): array
    {
        return [
            'kind' => $kind, 'name' => $name, 'start' => $start, 'end' => null, 'error' => false,
            'traceId' => ($this->ids)(16), 'spanId' => ($this->ids)(8),
            'attributes' => [], 'queries' => [], 'dropped' => 0,
            'http' => [], 'pending' => [], 'droppedHttp' => 0,
        ];
    }

    /** @param array<string, mixed> $t */
    private function encode(array $t): string
    {
        $root = [
            'traceId' => $t['traceId'],
            'spanId' => $t['spanId'],
            'name' => $t['name'],
            'kind' => $t['kind'],
            'startTimeUnixNano' => self::nanos($t['start']),
            'endTimeUnixNano' => self::nanos($t['end']),
            'attributes' => $t['attributes'],
        ];
        if ($t['dropped'] > 0) {
            $root['attributes'][] = self::kv('slowpoke.dropped_queries', $t['dropped']);
        }
        if ($t['droppedHttp'] > 0) {
            $root['attributes'][] = self::kv('slowpoke.dropped_http_calls', $t['droppedHttp']);
        }
        if ($t['error']) {
            $root['status'] = ['code' => 2];
        }
        $spans = [$root];
        foreach ($t['http'] as $c) {
            $attributes = [self::kv('http.request.method', $c['method']), self::kv('server.address', $c['host'])];
            if ($c['port'] !== null) {
                $attributes[] = self::kv('server.port', $c['port']);
            }
            if ($c['status'] !== null) {
                $attributes[] = self::kv('http.response.status_code', $c['status']);
            }
            $spans[] = $this->clientSpan($t, $c['method'] . ' ' . $c['host'], $c['start'], $c['end'] ?? $t['end'], $attributes, $c['origin'], $c['end'] === null || $c['error']);
        }
        foreach ($t['queries'] as $q) {
            $attributes = [self::kv('db.system.name', $q['system']), self::kv('db.query.text', $q['sql'])];
            $spans[] = $this->clientSpan($t, strtoupper((string) strtok(ltrim($q['sql']), " \t\r\n(")), $q['start'], $q['end'], $attributes, $q['origin'], false);
        }
        $payload = ['resourceSpans' => [[
            'resource' => ['attributes' => [
                self::kv('service.name', $this->service),
                self::kv('telemetry.sdk.name', 'slowpoke-symfony'),
                self::kv('telemetry.sdk.language', 'php'),
                self::kv('telemetry.sdk.version', $this->version),
            ]],
            'scopeSpans' => [[
                'scope' => ['name' => 'slowpoke/symfony', 'version' => $this->version],
                'spans' => $spans,
            ]],
        ]]];
        $json = json_encode($payload, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_INVALID_UTF8_SUBSTITUTE);
        if ($json === false) {
            throw new \RuntimeException('cannot encode trace');
        }
        return $json;
    }

    /**
     * @param array<string, mixed> $t
     * @param array<int, array{key: string, value: array<string, mixed>}> $attributes
     * @param array{0: string, 1: int|null}|null $origin
     * @return array<string, mixed>
     */
    private function clientSpan(array $t, string $name, float $start, float $end, array $attributes, ?array $origin, bool $error): array
    {
        if ($origin !== null) {
            $attributes[] = self::kv('code.file.path', $origin[0]);
            if ($origin[1] !== null) {
                $attributes[] = self::kv('code.line.number', $origin[1]);
            }
        }
        $span = [
            'traceId' => $t['traceId'],
            'spanId' => ($this->ids)(8),
            'parentSpanId' => $t['spanId'],
            'name' => $name,
            'kind' => self::CLIENT,
            'startTimeUnixNano' => self::nanos($start),
            'endTimeUnixNano' => self::nanos($end),
            'attributes' => $attributes,
        ];
        if ($error) {
            $span['status'] = ['code' => 2];
        }
        return $span;
    }

    /**
     * Host (lower-case, no brackets), port, and whether that port is the scheme's default.
     * Path, query string and credentials are never read.
     *
     * @return array{0: string, 1: int, 2: bool}|null
     */
    private static function target(string $url): ?array
    {
        $parts = @parse_url($url);
        if (!is_array($parts) || empty($parts['host'])) {
            return null;
        }
        $scheme = strtolower((string) ($parts['scheme'] ?? 'http'));
        $default = $scheme === 'https' ? 443 : 80;
        $port = isset($parts['port']) ? (int) $parts['port'] : $default;
        return [strtolower(trim($parts['host'], '[]')), $port, $port === $default];
    }

    /** 64-bit integers travel as strings. Microsecond precision is all PHP measures. */
    private static function nanos(float $seconds): string
    {
        // floor(x + 0.5), not round(): PHP 8.4 changed round() for values like 1760000000409999.8,
        // and the same trace must not depend on the PHP version.
        return ((int) floor($seconds * 1e6 + 0.5)) . '000';
    }

    /**
     * @param string|int|float|bool $value
     * @return array{key: string, value: array<string, mixed>}
     */
    private static function kv(string $key, $value): array
    {
        return ['key' => $key, 'value' => is_int($value) ? ['intValue' => (string) $value] : ['stringValue' => (string) $value]];
    }

    private static function dbSystem(string $driver): string
    {
        $driver = strtolower($driver);
        if (strpos($driver, 'pdo_') === 0) {
            $driver = substr($driver, 4);
        }
        switch ($driver) {
            case 'pgsql':
                return 'postgresql';
            case 'mysqli':
                return 'mysql';
            case 'sqlite3':
                return 'sqlite';
            case 'sqlsrv':
                return 'microsoft.sql_server';
            case 'oci8':
            case 'oci':
                return 'oracle';
            case 'ibm_db2':
                return 'db2';
            default:
                return $driver;
        }
    }
}
