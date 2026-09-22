<?php

namespace Slowpoke\Laravel\Tests\Feature;

use Slowpoke\Laravel\HttpSender;
use Slowpoke\Laravel\Sender;
use Slowpoke\Laravel\Tests\Fixtures\App\OrderController;
use Slowpoke\Laravel\Tests\Fixtures\FakeSender;

class RequestTraceTest extends TestCase
{
    public function testSeedingOutsideARequestSendsNothing(): void
    {
        $this->app->terminate();
        $this->assertSame([], $this->sender->payloads);
    }

    public function testOriginIsTheApplicationFileNotVendorNorThePackage(): void
    {
        $this->get('/orders')->assertStatus(200);

        [, $queries] = $this->sender->onlyTrace();
        $this->assertNotEmpty($queries);
        foreach ($queries as $q) {
            $file = FakeSender::attr($q, 'code.file.path');
            $this->assertSame('tests/Fixtures/App/OrderController.php', $file, FakeSender::attr($q, 'db.query.text'));
            $this->assertStringNotContainsString('vendor/', $file);
            $this->assertStringNotContainsString('src/', $file);
        }
        $this->assertSame((string) OrderController::LIST_LINE, FakeSender::attr($queries[0], 'code.line.number'));
    }

    public function testNPlusOneShowsAsRepeatedQueriesInOneTrace(): void
    {
        $this->get('/orders')->assertStatus(200);

        [$root, $queries] = $this->sender->onlyTrace();
        $byStatement = [];
        foreach ($queries as $q) {
            $this->assertSame($root['traceId'], $q['traceId']);
            $this->assertSame($root['spanId'], $q['parentSpanId']);
            $byStatement[FakeSender::attr($q, 'db.query.text')][] = FakeSender::attr($q, 'code.line.number');
        }
        $repeated = array_filter($byStatement, function ($lines) { return count($lines) >= 5; });
        $this->assertCount(1, $repeated, json_encode($byStatement));
        $lines = array_values($repeated)[0];
        $this->assertCount(6, $lines);
        $this->assertSame([(string) OrderController::N_PLUS_ONE_LINE], array_values(array_unique($lines)));
    }

    public function testRouteTemplateMethodAndStatus(): void
    {
        $this->get('/orders/3')->assertStatus(200);
        [$root, $queries] = $this->sender->onlyTrace();
        $this->assertSame(2, $root['kind']);
        $this->assertSame('/orders/{id}', FakeSender::attr($root, 'http.route'));
        $this->assertSame('GET', FakeSender::attr($root, 'http.request.method'));
        $this->assertSame('200', FakeSender::attr($root, 'http.response.status_code'));
        $this->assertSame('sqlite', FakeSender::attr($queries[0], 'db.system.name'));
        $this->assertGreaterThanOrEqual((int) $root['startTimeUnixNano'], (int) $queries[0]['startTimeUnixNano']);
        $this->assertLessThanOrEqual((int) $root['endTimeUnixNano'], (int) $queries[0]['endTimeUnixNano']);
    }

    public function testErrorsKeepTheirStatus(): void
    {
        $this->get('/orders/999')->assertStatus(404);
        [$root] = $this->sender->onlyTrace();
        $this->assertSame('/orders/{id}', FakeSender::attr($root, 'http.route'));
        $this->assertSame('404', FakeSender::attr($root, 'http.response.status_code'));
    }

    public function testUnmatchedRoute(): void
    {
        $this->get('/nope?token=zzz-secret-zzz')->assertStatus(404);
        [$root] = $this->sender->onlyTrace();
        $this->assertSame('/nope', FakeSender::attr($root, 'url.path'));
        $this->assertStringNotContainsString('zzz-secret-zzz', $this->sender->payloads[0]);
    }

    public function testBindingValuesNeverLeaveTheApp(): void
    {
        $this->get('/customers/lookup?email=' . urlencode('ada@secret.example'))->assertStatus(200);
        [, $queries] = $this->sender->onlyTrace();
        $this->assertCount(1, $queries);
        $this->assertStringContainsString('"email" = ?', FakeSender::attr($queries[0], 'db.query.text'));
        $this->assertStringNotContainsString('ada@secret', $this->sender->payloads[0]);
        $this->assertStringNotContainsString('Ada Secret', $this->sender->payloads[0]);
    }

    public function testOneTracePerRequest(): void
    {
        $this->get('/orders/1');
        $this->get('/orders/2');
        $this->assertCount(2, $this->sender->payloads);
        $a = json_decode($this->sender->payloads[0], true)['resourceSpans'][0];
        $b = json_decode($this->sender->payloads[1], true)['resourceSpans'][0];
        $this->assertNotSame($a['scopeSpans'][0]['spans'][0]['traceId'], $b['scopeSpans'][0]['spans'][0]['traceId']);
        $this->assertSame('shop', FakeSender::attr($a['resource'], 'service.name'));
    }

    public function testAnUnreachableAgentNeverBreaksTheRequest(): void
    {
        $this->app->instance(Sender::class, HttpSender::fromUrl('http://127.0.0.1:1/v1/traces', 0.05));
        $started = microtime(true);
        $this->get('/orders')->assertStatus(200);
        $this->assertLessThan(1.0, microtime(true) - $started);
    }

    public function testAFailingSenderNeverBreaksTheRequest(): void
    {
        $this->app->instance(Sender::class, new \Slowpoke\Laravel\Tests\Fixtures\ThrowingSender());
        $this->get('/orders')->assertStatus(200);
    }
}
