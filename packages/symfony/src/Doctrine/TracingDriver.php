<?php

namespace Slowpoke\Symfony\Doctrine;

use Doctrine\DBAL\Driver;
use Doctrine\DBAL\Driver\Connection as DriverConnection;
use Doctrine\DBAL\Driver\Middleware\AbstractDriverMiddleware;
use Slowpoke\Symfony\Tracer;

class TracingDriver extends AbstractDriverMiddleware
{
    /** @var Tracer */
    private $tracer;

    public function __construct(Driver $driver, Tracer $tracer)
    {
        parent::__construct($driver);
        $this->tracer = $tracer;
    }

    // The attribute sits on its own line: PHP 7.4 reads it as a comment.
    public function connect(
        #[\SensitiveParameter]
        array $params
    ): DriverConnection {
        $connection = parent::connect($params);
        return new TracingConnection($connection, $this->tracer, self::driverName($params, $connection));
    }

    /**
     * Doctrine's name (pdo_mysql), or PDO's when the app configured a driver class instead.
     *
     * @param array<string, mixed> $params
     */
    private static function driverName(array $params, DriverConnection $connection): string
    {
        if (isset($params['driver']) && is_string($params['driver'])) {
            return $params['driver'];
        }
        try {
            $native = method_exists($connection, 'getNativeConnection') ? $connection->getNativeConnection() : null;
            if ($native instanceof \PDO) {
                return (string) $native->getAttribute(\PDO::ATTR_DRIVER_NAME);
            }
        } catch (\Throwable $e) {
            // fall through
        }
        return 'other_sql';
    }
}
