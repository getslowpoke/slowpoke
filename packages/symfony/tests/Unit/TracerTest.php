<?php

namespace Slowpoke\Symfony\Tests\Unit;

use PHPUnit\Framework\TestCase;
use Slowpoke\Symfony\OriginFinder;
use Slowpoke\Symfony\Tests\Fixtures\FakeSender;
use Slowpoke\Symfony\Tests\Fixtures\ThrowingSender;
use Slowpoke\Symfony\Tracer;

class TracerTest extends TestCase
{
    /** @var FakeSender */
    private $sender;
    /** @var float */
    private $now = 1760000000.5;

    protected function setUp(): void
    {
        $this->sender = new FakeSender();
    }

    private function tracer(array $options = [], $sender = null): Tracer
    {
        $root = dirname(__DIR__, 2);
        $sender = $sender ?: $this->sender;
        return new Tracer(
            new OriginFinder($root, 40, [$root . '/src']),
            function () use ($sender) { return $sender; },
            $options + ['service' => 'shop'],
            function () { return $this->now; }
        );
    }

    public function testQueriesOutsideATraceAreIgnored(): void
    {
        $tracer = $this->tracer();
        $tracer->recordQuery('select 1', 1.0, 'mysql');
        $tracer->flush();
        $this->assertSame([], $this->sender->payloads);
    }

    public function testRequestTraceWithRouteStatusAndQueries(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('get', $this->now - 0.2);
        $this->now = 1760000000.55;
        $tracer->recordQuery('select * from orders where id = ?', 12.5, 'mysql'); $line = __LINE__;
        $this->now = 1760000000.65;
        $tracer->finishRequest('orders/{id}', '/orders/7', 200);
        $tracer->flush();

        [$root, $queries] = $this->sender->onlyTrace();
        $this->assertSame(2, $root['kind']);
        $this->assertSame('GET /orders/{id}', $root['name']);
        $this->assertSame('GET', FakeSender::attr($root, 'http.request.method'));
        $this->assertSame('/orders/{id}', FakeSender::attr($root, 'http.route'));
        $this->assertSame('200', FakeSender::attr($root, 'http.response.status_code'));
        $this->assertNull(FakeSender::attr($root, 'url.path'), 'a matched route never sends the real path');
        $this->assertSame('1760000000300000000', $root['startTimeUnixNano']);
        $this->assertSame('1760000000650000000', $root['endTimeUnixNano']);

        $this->assertCount(1, $queries);
        $q = $queries[0];
        $this->assertSame($root['traceId'], $q['traceId']);
        $this->assertSame($root['spanId'], $q['parentSpanId']);
        $this->assertSame('mysql', FakeSender::attr($q, 'db.system.name'));
        $this->assertSame('select * from orders where id = ?', FakeSender::attr($q, 'db.query.text'));
        $this->assertSame('tests/Unit/TracerTest.php', FakeSender::attr($q, 'code.file.path'));
        $this->assertSame((string) $line, FakeSender::attr($q, 'code.line.number'));
        $this->assertSame('1760000000537500000', $q['startTimeUnixNano']);
        $this->assertSame('1760000000550000000', $q['endTimeUnixNano']);
        $this->assertSame(1, preg_match('/^[0-9a-f]{32}$/', $root['traceId']));
        $this->assertSame(1, preg_match('/^[0-9a-f]{16}$/', $q['spanId']));
        $this->assertNotSame($root['spanId'], $q['spanId']);
    }

    public function testRootRouteAndLeadingSlash(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('GET', $this->now);
        $tracer->finishRequest('/', '/', 200);
        $tracer->flush();
        [$root] = $this->sender->onlyTrace();
        $this->assertSame('/', FakeSender::attr($root, 'http.route'));
    }

    public function testUnmatchedRequestSendsThePathWithoutQueryString(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('POST', $this->now);
        $tracer->finishRequest(null, '/wp-login.php', 404);
        $tracer->flush();
        [$root] = $this->sender->onlyTrace();
        $this->assertNull(FakeSender::attr($root, 'http.route'));
        $this->assertSame('/wp-login.php', FakeSender::attr($root, 'url.path'));
        $this->assertSame('POST', $root['name']);
        $this->assertSame('404', FakeSender::attr($root, 'http.response.status_code'));
    }

    public function testNothingIsSentBeforeTheRequestFinishes(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('GET', $this->now);
        $tracer->recordQuery('select 1', 1.0, 'mysql');
        $tracer->flush();
        $this->assertSame([], $this->sender->payloads);
    }

    public function testQueriesAfterTheResponseAreNotPartOfTheRequest(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('GET', $this->now);
        $tracer->finishRequest('a', '/a', 200);
        $tracer->recordQuery('select 1', 1.0, 'mysql');
        $tracer->flush();
        [, $queries] = $this->sender->onlyTrace();
        $this->assertSame([], $queries);
    }

    public function testQueryCapPerTrace(): void
    {
        $tracer = $this->tracer(['max_queries' => 3]);
        $tracer->startRequest('GET', $this->now);
        for ($i = 0; $i < 5; $i++) {
            $tracer->recordQuery('select * from customers where id = ?', 1.0, 'mysql');
        }
        $tracer->finishRequest('orders', '/orders', 200);
        $tracer->flush();
        [$root, $queries] = $this->sender->onlyTrace();
        $this->assertCount(3, $queries);
        $this->assertSame('2', FakeSender::attr($root, 'slowpoke.dropped_queries'));
    }

    public function testAQueryNeverStartsBeforeItsTrace(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('GET', $this->now);
        $tracer->recordQuery('select 1 from t', 500.0, 'mysql');
        $tracer->finishRequest('a', '/a', 200);
        $tracer->flush();
        [$root, $queries] = $this->sender->onlyTrace();
        $this->assertSame($root['startTimeUnixNano'], $queries[0]['startTimeUnixNano']);
    }

    public function testLongStatementsAreTruncated(): void
    {
        $tracer = $this->tracer(['max_sql_length' => 20]);
        $tracer->startRequest('GET', $this->now);
        $tracer->recordQuery('select * from orders where id in (?, ?, ?, ?, ?)', 1.0, 'mysql');
        $tracer->finishRequest('orders', '/orders', 200);
        $tracer->flush();
        [, $queries] = $this->sender->onlyTrace();
        $this->assertSame('select * from orders', FakeSender::attr($queries[0], 'db.query.text'));
    }

    public function testDoctrineDriverNamesFollowOpenTelemetry(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('GET', $this->now);
        $drivers = ['pdo_pgsql', 'pgsql', 'pdo_mysql', 'mysqli', 'pdo_sqlite', 'sqlite3', 'pdo_sqlsrv', 'sqlsrv', 'oci8', 'pdo_oci', 'ibm_db2', 'mariadb'];
        foreach ($drivers as $driver) {
            $tracer->recordQuery('select 1 from t', 1.0, $driver);
        }
        $tracer->finishRequest('a', '/a', 200);
        $tracer->flush();
        [, $queries] = $this->sender->onlyTrace();
        $this->assertSame([
            'postgresql', 'postgresql', 'mysql', 'mysql', 'sqlite', 'sqlite', 'microsoft.sql_server', 'microsoft.sql_server',
            'oracle', 'oracle', 'db2', 'mariadb',
        ], array_map(function ($q) {
            return FakeSender::attr($q, 'db.system.name');
        }, $queries));
    }

    public function testJobTrace(): void
    {
        $tracer = $this->tracer();
        $tracer->startJob('App\\Message\\SendInvoices', 'emails');
        $tracer->recordQuery('select * from invoices', 3.0, 'pgsql');
        $this->now += 1;
        $tracer->finishJob(false);
        $tracer->flush();
        [$root, $queries] = $this->sender->onlyTrace();
        $this->assertSame(5, $root['kind']);
        $this->assertSame('App\\Message\\SendInvoices', $root['name']);
        $this->assertSame('emails', FakeSender::attr($root, 'messaging.destination.name'));
        $this->assertNull(FakeSender::attr($root, 'http.route'));
        $this->assertCount(1, $queries);
    }

    public function testFailedJobIsMarkedAsError(): void
    {
        $tracer = $this->tracer();
        $tracer->startJob('App\\Message\\Flaky', 'default');
        $tracer->finishJob(true);
        $tracer->flush();
        [$root] = $this->sender->onlyTrace();
        $this->assertSame(2, $root['status']['code']);
    }

    public function testAJobInsideARequestBelongsToTheRequest(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('GET', $this->now);
        $tracer->startJob('App\\Message\\Inline', 'default');
        $tracer->recordQuery('select 1 from t', 1.0, 'mysql');
        $tracer->finishJob(false);
        $tracer->flush();
        $this->assertSame([], $this->sender->payloads, 'the request is still running');
        $tracer->finishRequest('a', '/a', 200);
        $tracer->flush();
        [$root, $queries] = $this->sender->onlyTrace();
        $this->assertSame(2, $root['kind']);
        $this->assertCount(1, $queries);
    }

    public function testEachTraceIsSentOnceAndStateIsReset(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('GET', $this->now);
        $tracer->finishRequest('a', '/a', 200);
        $tracer->flush();
        $tracer->flush();
        $tracer->recordQuery('select 1 from t', 1.0, 'mysql');
        $tracer->flush();
        $this->assertCount(1, $this->sender->payloads);
    }

    public function testIdentifiesItselfAsTheSymfonyPackage(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('GET', $this->now);
        $tracer->finishRequest('a', '/a', 200);
        $tracer->flush();
        $resource = json_decode($this->sender->payloads[0], true)['resourceSpans'][0];
        $this->assertSame('slowpoke-symfony', FakeSender::attr($resource['resource'], 'telemetry.sdk.name'));
        $this->assertSame('slowpoke/symfony', $resource['scopeSpans'][0]['scope']['name']);
    }

    public function testASenderFailureIsSwallowed(): void
    {
        $tracer = $this->tracer([], new ThrowingSender());
        $tracer->startRequest('GET', $this->now);
        $tracer->finishRequest('a', '/a', 200);
        $tracer->flush();
        $this->addToAssertionCount(1);
    }

    public function testAStaleRequestStartIsIgnored(): void
    {
        // Long-running servers (FrankenPHP worker mode, RoadRunner) keep the first request's time in $_SERVER.
        $tracer = $this->tracer();
        $tracer->startRequest('GET', $this->now - 3600);
        $tracer->finishRequest('a', '/a', 200);
        $tracer->flush();
        [$root] = $this->sender->onlyTrace();
        $this->assertSame($root['endTimeUnixNano'], $root['startTimeUnixNano']);
    }
}
