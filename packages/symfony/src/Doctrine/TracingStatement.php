<?php

namespace Slowpoke\Symfony\Doctrine;

use Doctrine\DBAL\Driver\Middleware\AbstractStatementMiddleware;
use Doctrine\DBAL\Driver\Result;
use Doctrine\DBAL\Driver\Statement;
use Slowpoke\Symfony\Tracer;

class TracingStatement extends AbstractStatementMiddleware
{
    /** @var Tracer */
    private $tracer;
    /** @var string */
    private $sql;
    /** @var string */
    private $driver;

    public function __construct(Statement $statement, Tracer $tracer, string $sql, string $driver)
    {
        parent::__construct($statement);
        $this->tracer = $tracer;
        $this->sql = $sql;
        $this->driver = $driver;
    }

    /** DBAL 3 may still pass parameters here, DBAL 4 never does. Only the SQL is recorded. */
    public function execute($params = null): Result
    {
        $started = microtime(true);
        $result = $params === null ? parent::execute() : parent::execute($params);
        $this->tracer->recordQuery($this->sql, (microtime(true) - $started) * 1000, $this->driver);
        return $result;
    }
}
