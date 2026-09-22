<?php

namespace Slowpoke\Symfony\Tests\Feature;

use Slowpoke\Symfony\Tests\Fixtures\FakeSender;

class HttpClientTraceTest extends AppTestCase
{
    private const CONTROLLER = 'src/Controller/PaymentController.php';

    /** @return array<int, array<string, mixed>> */
    private function calls(): array
    {
        [, $client] = $this->sender()->onlyTrace();
        return array_values(array_filter($client, function ($s) { return FakeSender::attr($s, 'http.request.method') !== null; }));
    }

    public function testAnOutboundCallIsAClientSpanWithTheHostAndTheAppLine(): void
    {
        $this->boot();
        $this->assertSame(200, $this->request('POST', '/pay')->getStatusCode());

        [$root] = $this->sender()->onlyTrace();
        $calls = $this->calls();
        $this->assertCount(1, $calls);
        $c = $calls[0];
        $this->assertSame(3, $c['kind']);
        $this->assertSame($root['spanId'], $c['parentSpanId']);
        $this->assertSame('POST api.stripe.com', $c['name']);
        $this->assertSame('POST', FakeSender::attr($c, 'http.request.method'));
        $this->assertSame('api.stripe.com', FakeSender::attr($c, 'server.address'));
        $this->assertNull(FakeSender::attr($c, 'server.port'));
        $this->assertSame('200', FakeSender::attr($c, 'http.response.status_code'));
        $this->assertSame(self::CONTROLLER, FakeSender::attr($c, 'code.file.path'));
        $this->assertSame(self::line(self::CONTROLLER, 'charge'), FakeSender::attr($c, 'code.line.number'));
        $this->assertArrayNotHasKey('status', $c);
        $this->assertGreaterThanOrEqual((int) $root['startTimeUnixNano'], (int) $c['startTimeUnixNano']);
        $this->assertLessThanOrEqual((int) $root['endTimeUnixNano'], (int) $c['endTimeUnixNano']);
        foreach (['v1/charges', 'cus_secret', 'sk_live', 'amount', 'Bearer', 'https://'] as $secret) {
            $this->assertStringNotContainsString($secret, $this->sender()->payloads[0]);
        }
    }

    public function testA5xxIsAnErrorAndAnUnusualPortIsSent(): void
    {
        $this->boot();
        $this->assertSame('502', $this->request('GET', '/pay/flaky')->getContent());

        $calls = $this->calls();
        $this->assertCount(1, $calls);
        $this->assertSame(['code' => 2], $calls[0]['status']);
        $this->assertSame('502', FakeSender::attr($calls[0], 'http.response.status_code'));
        $this->assertSame('8443', FakeSender::attr($calls[0], 'server.port'));
    }

    public function testAFailedCallIsAnErrorSpanAndTheApplicationStillGetsItsException(): void
    {
        $this->boot();
        $this->assertSame('failed', $this->request('GET', '/pay/down')->getContent());

        $calls = $this->calls();
        $this->assertCount(1, $calls);
        $this->assertSame('GET down.example', $calls[0]['name']);
        $this->assertSame(['code' => 2], $calls[0]['status']);
        $this->assertNull(FakeSender::attr($calls[0], 'http.response.status_code'));
        $this->assertSame(self::line(self::CONTROLLER, 'down'), FakeSender::attr($calls[0], 'code.line.number'));
        $this->assertStringNotContainsString('zzz', $this->sender()->payloads[0]);
    }

    public function testConcurrentCallsReadWithStreamAreOneSpanEach(): void
    {
        $this->boot();
        $this->request('GET', '/pay/lazy');

        $calls = $this->calls();
        $this->assertCount(3, $calls);
        foreach ($calls as $c) {
            $this->assertSame('GET partner.example', $c['name']);
            $this->assertSame('200', FakeSender::attr($c, 'http.response.status_code'));
            $this->assertArrayNotHasKey('status', $c);
        }
    }

    public function testAResponseDroppedUnreadStillEndsItsCall(): void
    {
        $this->boot();
        $this->request('GET', '/pay/dropped');

        $calls = $this->calls();
        $this->assertCount(1, $calls);
        $this->assertSame('POST hooks.example', $calls[0]['name']);
        $this->assertSame('200', FakeSender::attr($calls[0], 'http.response.status_code'));
        $this->assertArrayNotHasKey('status', $calls[0]);
        $this->assertSame(self::line(self::CONTROLLER, 'dropped'), FakeSender::attr($calls[0], 'code.line.number'));
    }

    public function testCallsOutsideARequestAreNotRecorded(): void
    {
        $this->boot();
        $this->kernel->getContainer()->get('test.http_client')->request('GET', 'https://api.example/outside')->getContent();
        $this->assertSame([], $this->sender()->payloads);
    }

    public function testTheCapCountsTheRest(): void
    {
        $this->boot('httpcap');
        $this->request('GET', '/pay/many');

        [$root] = $this->sender()->onlyTrace();
        $this->assertCount(2, $this->calls());
        $this->assertSame('3', FakeSender::attr($root, 'slowpoke.dropped_http_calls'));
    }

    public function testTheSwitchInTheConfigurationLeavesTheClientAlone(): void
    {
        $this->boot('nohttp');
        $this->request('POST', '/pay');

        $this->assertSame([], $this->calls());
        $this->assertFalse($this->kernel->getContainer()->has('slowpoke.http_client'));
    }

    public function testTheSwitchInTheEnvironmentTurnsItOff(): void
    {
        $this->setEnv('SLOWPOKE_HTTP_CLIENT', 'false');
        $this->boot();
        $this->request('POST', '/pay');

        $this->assertSame([], $this->calls());
    }
}
