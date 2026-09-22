<?php

namespace Slowpoke\Symfony\Tests\Unit;

use PHPUnit\Framework\TestCase;
use Slowpoke\Symfony\OriginFinder;
use Slowpoke\Symfony\Tests\Fixtures\FakeSender;
use Slowpoke\Symfony\Tracer;

/** Outbound HTTP calls: time spent waiting on Stripe, a partner API, another service. */
class HttpCallTest extends TestCase
{
    /** @var FakeSender */
    private $sender;
    /** @var float */
    private $now = 1760000000.5;

    protected function setUp(): void
    {
        $this->sender = new FakeSender();
    }

    private function tracer(array $options = []): Tracer
    {
        $root = dirname(__DIR__, 2);
        $sender = $this->sender;
        return new Tracer(
            new OriginFinder($root, 40, [$root . '/src']),
            function () use ($sender) { return $sender; },
            $options + ['service' => 'shop'],
            function () { return $this->now; }
        );
    }

    /** @return array<int, array<string, mixed>> */
    private function httpSpans(): array
    {
        [, $client] = $this->sender->onlyTrace();
        return array_values(array_filter($client, function ($s) { return FakeSender::attr($s, 'http.request.method') !== null; }));
    }

    public function testACallInsideARequestIsOneClientSpanWithTheHostOnly(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('POST', null);
        $this->now = 1760000000.6;
        $tracer->startHttpCall(7, 'post', 'https://API.Stripe.com/v1/charges?customer=cus_secret#frag'); $line = __LINE__;
        $this->now = 1760000000.9;
        $tracer->finishHttpCall(7, 200, 'POST', 'https://api.stripe.com/v1/charges?customer=cus_secret');
        $this->now = 1760000001.0;
        $tracer->finishRequest('/pay', '/pay', 200);
        $tracer->flush();

        [$root] = $this->sender->onlyTrace();
        $calls = $this->httpSpans();
        $this->assertCount(1, $calls);
        $c = $calls[0];
        $this->assertSame(3, $c['kind']);
        $this->assertSame('POST api.stripe.com', $c['name']);
        $this->assertSame($root['spanId'], $c['parentSpanId']);
        $this->assertSame($root['traceId'], $c['traceId']);
        $this->assertSame('POST', FakeSender::attr($c, 'http.request.method'));
        $this->assertSame('api.stripe.com', FakeSender::attr($c, 'server.address'));
        $this->assertNull(FakeSender::attr($c, 'server.port'), 'the default port is not sent');
        $this->assertSame('200', FakeSender::attr($c, 'http.response.status_code'));
        $this->assertSame('tests/Unit/HttpCallTest.php', FakeSender::attr($c, 'code.file.path'));
        $this->assertSame((string) $line, FakeSender::attr($c, 'code.line.number'));
        $this->assertSame('1760000000600000000', $c['startTimeUnixNano']);
        $this->assertSame('1760000000900000000', $c['endTimeUnixNano']);
        $this->assertArrayNotHasKey('status', $c);
        $this->assertStringNotContainsString('charges', $this->sender->payloads[0]);
        $this->assertStringNotContainsString('cus_secret', $this->sender->payloads[0]);
        $this->assertStringNotContainsString('https://', $this->sender->payloads[0]);
    }

    public function testANonDefaultPortIsSent(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('GET', null);
        $tracer->startHttpCall(1, 'GET', 'http://billing.internal:8080/x');
        $tracer->finishHttpCall(1, 204, 'GET', 'http://billing.internal:8080/x');
        $tracer->startHttpCall(2, 'GET', 'https://partner.example:443/x');
        $tracer->finishHttpCall(2, 204, 'GET', 'https://partner.example:443/x');
        $tracer->finishRequest('/', '/', 200);
        $tracer->flush();

        $calls = $this->httpSpans();
        $this->assertSame('8080', FakeSender::attr($calls[0], 'server.port'));
        $this->assertNull(FakeSender::attr($calls[1], 'server.port'));
    }

    public function testAFailedCallAndA5xxAreErrors(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('GET', null);
        $tracer->startHttpCall(1, 'GET', 'https://down.example/a');
        $tracer->finishHttpCall(1, null, 'GET', 'https://down.example/a');
        $tracer->startHttpCall(2, 'GET', 'https://flaky.example/a');
        $tracer->finishHttpCall(2, 503, 'GET', 'https://flaky.example/a');
        $tracer->startHttpCall(3, 'GET', 'https://ok.example/a');
        $tracer->finishHttpCall(3, 404, 'GET', 'https://ok.example/a');
        $tracer->finishRequest('/', '/', 200);
        $tracer->flush();

        [$failed, $flaky, $missing] = $this->httpSpans();
        $this->assertSame(['code' => 2], $failed['status']);
        $this->assertNull(FakeSender::attr($failed, 'http.response.status_code'));
        $this->assertSame(['code' => 2], $flaky['status']);
        $this->assertSame('503', FakeSender::attr($flaky, 'http.response.status_code'));
        $this->assertArrayNotHasKey('status', $missing, 'a 404 is an answer, not a failure');
    }

    public function testAFailureReportedForAnotherRequestObjectFindsItsCall(): void
    {
        // Laravel 9+ wraps the PSR request of a ConnectionFailed in a new object.
        $tracer = $this->tracer();
        $tracer->startRequest('GET', null);
        $tracer->startHttpCall(1, 'GET', 'https://down.example/a');
        $tracer->finishHttpCall(99, null, 'GET', 'https://down.example/a');
        $tracer->finishRequest('/', '/', 200);
        $tracer->flush();

        $calls = $this->httpSpans();
        $this->assertCount(1, $calls);
        $this->assertSame(['code' => 2], $calls[0]['status']);
    }

    public function testACallThatNeverFinishedEndsWithItsTraceAsAnError(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('GET', null);
        $tracer->startHttpCall(1, 'GET', 'https://slow.example/a');
        $this->now = 1760000001.5;
        $tracer->finishRequest('/', '/', 500);
        $tracer->flush();

        $calls = $this->httpSpans();
        $this->assertSame('1760000001500000000', $calls[0]['endTimeUnixNano']);
        $this->assertSame(['code' => 2], $calls[0]['status']);
    }

    public function testCallsOutsideATraceAreIgnored(): void
    {
        $tracer = $this->tracer();
        $tracer->startHttpCall(1, 'GET', 'https://api.example/a');
        $tracer->finishHttpCall(1, 200, 'GET', 'https://api.example/a');
        $tracer->flush();
        $this->assertSame([], $this->sender->payloads);

        $tracer->startRequest('GET', null);
        $tracer->finishRequest('/', '/', 200);
        $tracer->startHttpCall(2, 'GET', 'https://api.example/a'); // after the response
        $tracer->flush();
        $this->assertSame([], $this->httpSpans());
    }

    public function testCapPerTrace(): void
    {
        $tracer = $this->tracer(['max_http_calls' => 2]);
        $tracer->startRequest('GET', null);
        for ($i = 0; $i < 5; $i++) {
            $tracer->startHttpCall($i, 'GET', 'https://api.example/a');
            $tracer->finishHttpCall($i, 200, 'GET', 'https://api.example/a');
        }
        $tracer->finishRequest('/', '/', 200);
        $tracer->flush();

        [$root] = $this->sender->onlyTrace();
        $this->assertCount(2, $this->httpSpans());
        $this->assertSame('3', FakeSender::attr($root, 'slowpoke.dropped_http_calls'));
    }

    public function testCallsToTheAgentAreNeverTraced(): void
    {
        $tracer = $this->tracer(['agent_endpoint' => 'http://127.0.0.1:4318/v1/traces']);
        $tracer->startRequest('GET', null);
        $tracer->startHttpCall(1, 'POST', 'http://127.0.0.1:4318/v1/traces');
        $tracer->finishHttpCall(1, 200, 'POST', 'http://127.0.0.1:4318/v1/traces');
        $tracer->startHttpCall(2, 'GET', 'http://127.0.0.1:8000/other');
        $tracer->finishHttpCall(2, 200, 'GET', 'http://127.0.0.1:8000/other');
        $tracer->finishRequest('/', '/', 200);
        $tracer->flush();

        $calls = $this->httpSpans();
        $this->assertCount(1, $calls);
        $this->assertSame('8000', FakeSender::attr($calls[0], 'server.port'));
    }

    public function testAJobTracesItsCallsToo(): void
    {
        $tracer = $this->tracer();
        $tracer->startJob('App\\Jobs\\Sync', 'default');
        $tracer->startHttpCall(1, 'GET', 'https://partner.example/feed');
        $tracer->finishHttpCall(1, 200, 'GET', 'https://partner.example/feed');
        $tracer->finishJob(false);
        $tracer->flush();

        $this->assertCount(1, $this->httpSpans());
    }

    public function testGarbageNeverThrows(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('GET', null);
        $tracer->startHttpCall(1, '', 'not a url');
        $tracer->finishHttpCall(1, 200, '', 'not a url');
        $tracer->finishRequest('/', '/', 200);
        $tracer->flush();
        $this->assertCount(1, $this->sender->payloads);
        $this->assertSame([], $this->httpSpans(), 'a call without a host says nothing useful');
    }

    public function testTheEndMeasuredByTheClientIsKeptWhenItMakesSense(): void
    {
        $tracer = $this->tracer();
        $tracer->startRequest('GET', null);
        $tracer->startHttpCall(1, 'GET', 'https://api.example/a');
        $this->now = 1760000000.9;
        $tracer->finishHttpCall(1, 200, 'GET', 'https://api.example/a', 1760000000.7);
        $tracer->startHttpCall(2, 'GET', 'https://api.example/b');
        $tracer->finishHttpCall(2, 200, 'GET', 'https://api.example/b', 1760000009.0); // in the future: ignored
        $tracer->finishRequest('/', '/', 200);
        $tracer->flush();

        [$a, $b] = $this->httpSpans();
        $this->assertSame('1760000000700000000', $a['endTimeUnixNano']);
        $this->assertSame('1760000000900000000', $b['endTimeUnixNano']);
    }
}
