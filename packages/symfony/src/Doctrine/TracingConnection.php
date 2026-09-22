<?php

namespace Slowpoke\Symfony\Doctrine;

use Doctrine\DBAL\Driver\Connection;
use Doctrine\DBAL\Driver\Middleware\AbstractConnectionMiddleware;
use Doctrine\DBAL\Driver\Result;
use Doctrine\DBAL\Driver\Statement;
use Slowpoke\Symfony\Tracer;

class TracingConnection extends AbstractConnectionMiddleware
{
    /** @var Tracer */
    private $tracer;
    /** @var string */
    private $driver;

    public function __construct(Connection $connection, Tracer $tracer, string $driver)
    {
        parent::__construct($connection);
        $this->tracer = $tracer;
        $this->driver = $driver;
    }

    public function prepare(string $sql): Statement
    {
        return new TracingStatement(parent::prepare($sql), $this->tracer, $sql, $this->driver);
    }

    public function query(string $sql): Result
    {
        $started = microtime(true);
        $result = parent::query($sql);
        $this->tracer->recordQuery($sql, (microtime(true) - $started) * 1000, $this->driver);
        return $result;
    }

    // DBAL 4 declares int|string, which PHP 7.4 cannot parse; int is a valid narrowing and a driver
    // returns a numeric string only past PHP_INT_MAX rows.
    public function exec(string $sql): int
    {
        $started = microtime(true);
        $affected = parent::exec($sql);
        $this->tracer->recordQuery($sql, (microtime(true) - $started) * 1000, $this->driver);
        return (int) $affected;
    }
}
