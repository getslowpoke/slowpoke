<?php

namespace Slowpoke\Laravel\Tests\Feature;

use Illuminate\Support\Facades\Http;
use Slowpoke\Laravel\Tests\Fixtures\App\PaymentController;
use Slowpoke\Laravel\Tests\Fixtures\FakeSender;

class HttpClientTraceTest extends TestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        if (!class_exists('Illuminate\\Http\\Client\\Events\\RequestSending') || !class_exists('GuzzleHttp\\Client')) {
            $this->markTestSkipped('the HTTP client sends events from Laravel 8, and needs Guzzle');
        }
    }

    protected function getEnvironmentSetUp($app)
    {
        parent::getEnvironmentSetUp($app);
        $app['router']->post('/pay', [PaymentController::class, 'charge']);
        $app['router']->get('/down', [PaymentController::class, 'down']);
    }

    /** @return array<int, array<string, mixed>> */
    private function httpSpans(): array
    {
        [, $client] = $this->sender->onlyTrace();
        return array_values(array_filter($client, function ($s) { return FakeSender::attr($s, 'http.request.method') !== null; }));
    }

    public function testAnOutboundCallIsAClientSpanWithTheHostAndTheAppLine(): void
    {
        Http::fake(['api.stripe.com/*' => Http::response(['id' => 'ch_1'], 200)]);
        $this->post('/pay')->assertStatus(200);

        $calls = $this->httpSpans();
        $this->assertCount(1, $calls);
        $this->assertSame('POST api.stripe.com', $calls[0]['name']);
        $this->assertSame('api.stripe.com', FakeSender::attr($calls[0], 'server.address'));
        $this->assertSame('200', FakeSender::attr($calls[0], 'http.response.status_code'));
        $this->assertSame('tests/Fixtures/App/PaymentController.php', FakeSender::attr($calls[0], 'code.file.path'));
        $this->assertSame((string) PaymentController::CHARGE_LINE, FakeSender::attr($calls[0], 'code.line.number'));
        foreach (['v1/charges', 'cus_secret', 'sk_live', 'amount', 'Bearer'] as $secret) {
            $this->assertStringNotContainsString($secret, $this->sender->payloads[0]);
        }
    }

    public function testA5xxIsAnError(): void
    {
        Http::fake(['api.stripe.com/*' => Http::response('', 502)]);
        $this->post('/pay')->assertStatus(200);

        $calls = $this->httpSpans();
        $this->assertSame(['code' => 2], $calls[0]['status']);
        $this->assertSame('502', FakeSender::attr($calls[0], 'http.response.status_code'));
    }

    public function testAConnectionThatFailsIsAnErrorSpan(): void
    {
        $this->get('/down')->assertStatus(200)->assertJson(['failed' => true]);

        $calls = $this->httpSpans();
        $this->assertCount(1, $calls);
        $this->assertSame('GET 127.0.0.1', $calls[0]['name']);
        $this->assertSame('1', FakeSender::attr($calls[0], 'server.port'));
        $this->assertSame(['code' => 2], $calls[0]['status']);
        $this->assertNull(FakeSender::attr($calls[0], 'http.response.status_code'));
        $this->assertSame((string) PaymentController::DOWN_LINE, FakeSender::attr($calls[0], 'code.line.number'));
        $this->assertStringNotContainsString('zzz', $this->sender->payloads[0]);
    }

    public function testACallOutsideARequestIsNotRecorded(): void
    {
        Http::fake();
        Http::get('https://api.example/outside');
        $this->app->terminate();
        $this->assertSame([], $this->sender->payloads);
    }
}
