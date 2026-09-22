<?php

namespace Slowpoke\Symfony;

/**
 * Builds the Tracer on first use, from settings that come from environment variables resolved at
 * runtime (null when a variable is not set: the defaults live here). Null when disabled, and null rather than an exception if anything is wrong: a monitoring
 * bundle must never stop the application.
 */
class TracerProvider
{
    public const ENDPOINT = 'http://127.0.0.1:4318/v1/traces';

    /** @var array<string, mixed> */
    private $settings;
    /** @var callable(): Sender */
    private $sender;
    /** @var Tracer|null */
    private $tracer;
    /** @var bool */
    private $built = false;

    /**
     * @param array<string, mixed> $settings
     * @param callable(): Sender $sender resolved only when a trace is sent
     */
    public function __construct(array $settings, callable $sender)
    {
        $this->settings = $settings;
        $this->sender = $sender;
    }

    public function tracer(): ?Tracer
    {
        if ($this->built) {
            return $this->tracer;
        }
        $this->built = true;
        try {
            $s = $this->settings;
            $enabled = $s['enabled'] ?? null;
            if ($enabled !== null && $enabled !== '' && !filter_var($enabled, FILTER_VALIDATE_BOOLEAN)) {
                return null;
            }
            $root = (string) ($s['code_root'] ?? '');
            $this->tracer = new Tracer(
                new OriginFinder($root, self::number($s['backtrace_limit'] ?? null, 60), array_filter((array) ($s['skip_dirs'] ?? []))),
                $this->sender,
                [
                    'service' => (string) ($s['service'] ?? '') !== '' ? (string) $s['service'] : 'symfony',
                    'max_queries' => self::number($s['max_queries'] ?? null, 500),
                    'max_sql_length' => self::number($s['max_sql_length'] ?? null, 10000),
                    'max_http_calls' => self::number($s['max_http_calls'] ?? null, 200),
                    'agent_endpoint' => (string) ($s['endpoint'] ?? '') !== '' ? (string) $s['endpoint'] : self::ENDPOINT,
                ]
            );
        } catch (\Throwable $e) {
            $this->tracer = null;
        }
        return $this->tracer;
    }

    /** The tracer, when outbound HTTP calls are to be recorded too (SLOWPOKE_HTTP_CLIENT, default on). */
    public function httpTracer(): ?Tracer
    {
        $enabled = $this->settings['http_client'] ?? null;
        if ($enabled !== null && $enabled !== '' && !filter_var($enabled, FILTER_VALIDATE_BOOLEAN)) {
            return null;
        }
        return $this->tracer();
    }

    /**
     * Service factory: plain http to a local or private host, or nothing is sent at all.
     *
     * @param mixed $endpoint as the configuration holds it, which may be an unresolved variable
     * @param mixed $timeout
     */
    public static function sender($endpoint, $timeout): Sender
    {
        try {
            $endpoint = $endpoint === null || $endpoint === '' ? self::ENDPOINT : (string) $endpoint;
            $timeout = is_numeric($timeout) && $timeout > 0 ? (float) $timeout : 0.1;
            return HttpSender::fromUrl($endpoint, $timeout) ?: new NullSender();
        } catch (\Throwable $e) {
            return new NullSender();
        }
    }

    /** @param mixed $value */
    private static function number($value, int $default): int
    {
        return is_numeric($value) && $value >= 0 ? (int) $value : $default;
    }
}
