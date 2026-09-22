<?php

namespace Slowpoke\Symfony\Doctrine;

use Doctrine\DBAL\Driver;
use Doctrine\DBAL\Driver\Middleware;
use Slowpoke\Symfony\TracerProvider;

/**
 * Doctrine DBAL 3.3+ and 4 middleware: sees every statement with its placeholders, never the values
 * bound to it. When Slowpoke is disabled the driver is returned untouched.
 */
class TracingMiddleware implements Middleware
{
    /** @var TracerProvider */
    private $provider;

    public function __construct(TracerProvider $provider)
    {
        $this->provider = $provider;
    }

    public function wrap(Driver $driver): Driver
    {
        try {
            $tracer = $this->provider->tracer();
        } catch (\Throwable $e) {
            $tracer = null;
        }
        return $tracer === null ? $driver : new TracingDriver($driver, $tracer);
    }
}
