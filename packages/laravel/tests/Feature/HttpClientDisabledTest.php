<?php

namespace Slowpoke\Laravel\Tests\Feature;

use Illuminate\Support\Facades\Http;
use Slowpoke\Laravel\Tests\Fixtures\App\PaymentController;

class HttpClientDisabledTest extends TestCase
{
    protected function getEnvironmentSetUp($app)
    {
        parent::getEnvironmentSetUp($app);
        $app['config']->set('slowpoke.http_client', false);
        $app['router']->post('/pay', [PaymentController::class, 'charge']);
    }

    public function testTheSwitchTurnsOutboundCallsOff(): void
    {
        if (!class_exists('Illuminate\\Http\\Client\\Events\\RequestSending') || !class_exists('GuzzleHttp\\Client')) {
            $this->markTestSkipped('the HTTP client sends events from Laravel 8, and needs Guzzle');
        }
        Http::fake(['api.stripe.com/*' => Http::response('', 200)]);
        $this->post('/pay')->assertStatus(200);

        [, $client] = $this->sender->onlyTrace();
        $this->assertSame([], $client);
    }
}
