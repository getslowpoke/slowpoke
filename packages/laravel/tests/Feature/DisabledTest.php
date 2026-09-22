<?php

namespace Slowpoke\Laravel\Tests\Feature;

class DisabledTest extends TestCase
{
    protected function setUp(): void
    {
        putenv('SLOWPOKE_ENABLED=false');
        parent::setUp();
    }

    protected function tearDown(): void
    {
        putenv('SLOWPOKE_ENABLED');
        parent::tearDown();
    }

    public function testNothingIsRecordedNorSent(): void
    {
        $this->assertFalse($this->app['config']->get('slowpoke.enabled'));
        $this->get('/orders')->assertStatus(200);
        $this->assertSame([], $this->sender->payloads);
        $this->assertFalse($this->app->bound(\Slowpoke\Laravel\Tracer::class));
    }
}
