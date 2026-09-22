<?php

namespace Slowpoke\Symfony\Tests\Feature;

use Slowpoke\Symfony\Tests\Fixtures\FakeSender;

class RequestTraceTest extends AppTestCase
{
    private const CONTROLLER = 'src/Controller/OrderController.php';

    protected function setUp(): void
    {
        $this->boot();
    }

    public function testBootAndSeedingOutsideARequestSendNothing(): void
    {
        $this->assertSame([], $this->sender()->payloads);
    }

    public function testRouteTemplateMethodAndStatus(): void
    {
        $this->request('GET', '/orders/3');
        [$root, $queries] = $this->sender()->onlyTrace();
        $this->assertSame(2, $root['kind']);
        $this->assertSame('GET /orders/{id}', $root['name']);
        $this->assertSame('/orders/{id}', FakeSender::attr($root, 'http.route'), 'inline requirements are not part of the template');
        $this->assertSame('GET', FakeSender::attr($root, 'http.request.method'));
        $this->assertSame('200', FakeSender::attr($root, 'http.response.status_code'));
        $this->assertNull(FakeSender::attr($root, 'url.path'));
        $this->assertCount(1, $queries);
        $this->assertSame('sqlite', FakeSender::attr($queries[0], 'db.system.name'));
        $this->assertSame('SELECT id, status FROM orders WHERE id = ?', FakeSender::attr($queries[0], 'db.query.text'));
        $this->assertGreaterThanOrEqual((int) $root['startTimeUnixNano'], (int) $queries[0]['startTimeUnixNano']);
        $this->assertLessThanOrEqual((int) $root['endTimeUnixNano'], (int) $queries[0]['endTimeUnixNano']);
    }

    public function testOriginIsTheApplicationLineNotVendorNorTheBundle(): void
    {
        $this->request('GET', '/orders');
        [, $queries] = $this->sender()->onlyTrace();
        $this->assertCount(7, $queries);
        foreach ($queries as $q) {
            $this->assertSame(self::CONTROLLER, FakeSender::attr($q, 'code.file.path'), FakeSender::attr($q, 'db.query.text'));
        }
        $this->assertSame(self::line(self::CONTROLLER, 'list'), FakeSender::attr($queries[0], 'code.line.number'));
    }

    public function testNPlusOneShowsAsRepeatedQueriesFromOneLine(): void
    {
        $this->request('GET', '/orders');
        [$root, $queries] = $this->sender()->onlyTrace();
        $byStatement = [];
        foreach ($queries as $q) {
            $this->assertSame($root['traceId'], $q['traceId']);
            $this->assertSame($root['spanId'], $q['parentSpanId']);
            $byStatement[FakeSender::attr($q, 'db.query.text')][] = FakeSender::attr($q, 'code.line.number');
        }
        $this->assertSame([self::line(self::CONTROLLER, 'n+1')], array_values(array_unique($byStatement['SELECT name FROM customers WHERE id = ?'])));
        $this->assertCount(6, $byStatement['SELECT name FROM customers WHERE id = ?']);
    }

    public function testAQueryInATwigTemplatePointsAtTheTemplateLine(): void
    {
        $this->request('GET', '/orders/report');
        [, $queries] = $this->sender()->onlyTrace();
        $this->assertCount(7, $queries);
        $this->assertSame(self::CONTROLLER, FakeSender::attr($queries[0], 'code.file.path'));
        $this->assertSame(self::line(self::CONTROLLER, 'report'), FakeSender::attr($queries[0], 'code.line.number'));
        foreach (array_slice($queries, 1) as $q) {
            $this->assertSame('templates/orders/report.html.twig', FakeSender::attr($q, 'code.file.path'));
            $this->assertSame('3', FakeSender::attr($q, 'code.line.number'));
        }
    }

    public function testStatementsWithoutParametersAreRecordedToo(): void
    {
        $this->request('POST', '/orders');
        $this->request('DELETE', '/orders/purge');
        $this->assertCount(2, $this->sender()->payloads);
        $create = json_decode($this->sender()->payloads[0], true)['resourceSpans'][0]['scopeSpans'][0]['spans'];
        $this->assertSame('POST /orders', $create[0]['name']);
        $this->assertSame('INSERT', $create[1]['name']);
        $this->assertSame(self::line(self::CONTROLLER, 'create'), FakeSender::attr($create[1], 'code.line.number'));
        $this->sender()->payloads = [$this->sender()->payloads[1]];
        [$root, $queries] = $this->sender()->onlyTrace();
        $this->assertSame('/orders/purge', FakeSender::attr($root, 'http.route'));
        $this->assertSame(["DELETE FROM orders WHERE status = 'cancelled'", 'SELECT COUNT(*) FROM orders'], array_map(function ($q) {
            return FakeSender::attr($q, 'db.query.text');
        }, $queries));
        $this->assertSame([self::line(self::CONTROLLER, 'purge'), self::line(self::CONTROLLER, 'count')], array_map(function ($q) {
            return FakeSender::attr($q, 'code.line.number');
        }, $queries));
    }

    public function testRootRoute(): void
    {
        $this->request('GET', '/');
        [$root] = $this->sender()->onlyTrace();
        $this->assertSame('/', FakeSender::attr($root, 'http.route'));
    }

    public function testNotFoundInsideAMatchedRouteKeepsTheTemplate(): void
    {
        $this->assertSame(404, $this->request('GET', '/orders/999')->getStatusCode());
        [$root] = $this->sender()->onlyTrace();
        $this->assertSame('/orders/{id}', FakeSender::attr($root, 'http.route'));
        $this->assertSame('404', FakeSender::attr($root, 'http.response.status_code'));
    }

    public function testUnmatchedPathWithoutQueryString(): void
    {
        $this->assertSame(404, $this->request('GET', '/nope?token=abc')->getStatusCode());
        [$root] = $this->sender()->onlyTrace();
        $this->assertNull(FakeSender::attr($root, 'http.route'));
        $this->assertSame('/nope', FakeSender::attr($root, 'url.path'));
        $this->assertStringNotContainsString('abc', $this->sender()->payloads[0]);
    }

    public function testServerErrorIsMarkedWithoutTheExceptionMessage(): void
    {
        $this->assertSame(500, $this->request('GET', '/broken')->getStatusCode());
        [$root, $queries] = $this->sender()->onlyTrace();
        $this->assertSame('500', FakeSender::attr($root, 'http.response.status_code'));
        $this->assertSame(2, $root['status']['code']);
        $this->assertCount(1, $queries);
        $this->assertStringNotContainsString('secret failure', $this->sender()->payloads[0]);
    }

    public function testSubRequestsBelongToTheMainRequest(): void
    {
        $this->request('GET', '/orders/2/forward');
        [$root, $queries] = $this->sender()->onlyTrace();
        $this->assertSame('/orders/{id}/forward', FakeSender::attr($root, 'http.route'));
        $this->assertCount(1, $queries);
        $this->assertSame(self::line(self::CONTROLLER, 'show'), FakeSender::attr($queries[0], 'code.line.number'));
    }

    public function testBindingValuesNeverLeaveTheApp(): void
    {
        $this->request('GET', '/customers/lookup?email=' . urlencode('ada@secret.example'));
        [, $queries] = $this->sender()->onlyTrace();
        $this->assertSame('SELECT id FROM customers WHERE email = ?', FakeSender::attr($queries[0], 'db.query.text'));
        $this->assertStringNotContainsString('ada@secret', $this->sender()->payloads[0]);
    }

    public function testNothingIsSentBeforeTheResponseIsOnItsWay(): void
    {
        $this->request('GET', '/orders/1', function () {
            $this->assertSame([], $this->sender()->payloads);
        });
        $this->assertCount(1, $this->sender()->payloads);
    }

    public function testOneTracePerRequest(): void
    {
        $this->request('GET', '/orders/1');
        $this->request('GET', '/orders/2');
        $this->assertCount(2, $this->sender()->payloads);
        $a = json_decode($this->sender()->payloads[0], true)['resourceSpans'][0];
        $b = json_decode($this->sender()->payloads[1], true)['resourceSpans'][0];
        $this->assertNotSame($a['scopeSpans'][0]['spans'][0]['traceId'], $b['scopeSpans'][0]['spans'][0]['traceId']);
        $this->assertSame('symfony', FakeSender::attr($a['resource'], 'service.name'));
        $this->assertSame('slowpoke-symfony', FakeSender::attr($a['resource'], 'telemetry.sdk.name'));
    }

    public function testRouteTemplatesAreBuiltByCacheWarmup(): void
    {
        // What bin/console cache:warmup does before a deploy: requests then never load the route
        // collection. The bundle's own warmer is asked directly - running every optional warmer of
        // the application would also run Twig's, and symfony/twig-bundle 5.4 with a current Twig
        // dies in there, which is their argument and not this bundle's.
        $cacheDir = $this->kernel->getCacheDir();
        $file = $cacheDir . '/slowpoke/route_templates.php';
        @unlink($file);
        $warmer = $this->kernel->getContainer()->get('test.route_templates');
        $this->assertTrue($warmer->isOptional(), 'a warmer that is not optional runs on every request');
        $warmer->warmUp($cacheDir);
        $this->assertFileExists($file);
        $this->assertSame('/orders/{id}', (require $file)['order_show']);
    }

    public function testServiceNameAndQueryCapFromTheEnvironment(): void
    {
        $this->setEnv('SLOWPOKE_SERVICE', 'shop');
        $this->setEnv('SLOWPOKE_MAX_QUERIES', '2');
        $this->kernel->shutdown();
        $this->boot();
        $this->request('GET', '/orders');
        [$root, $queries] = $this->sender()->onlyTrace();
        $this->assertCount(2, $queries);
        $this->assertSame('5', FakeSender::attr($root, 'slowpoke.dropped_queries'));
        $resource = json_decode($this->sender()->payloads[0], true)['resourceSpans'][0]['resource'];
        $this->assertSame('shop', FakeSender::attr($resource, 'service.name'));
    }
}
