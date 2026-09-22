<?php

namespace Slowpoke\Symfony\Tests\Feature;

use Slowpoke\Symfony\EventListener\RequestSubscriber;

class DisabledTest extends AppTestCase
{
    public function testDisabledFromTheEnvironmentRecordsAndSendsNothing(): void
    {
        $this->setEnv('SLOWPOKE_ENABLED', 'false');
        $this->boot();
        $this->assertSame(200, $this->request('GET', '/orders')->getStatusCode());
        $this->assertSame([], $this->sender()->payloads);
        $driver = $this->kernel->getContainer()->get('test.db')->getDriver();
        $this->assertStringNotContainsString('Slowpoke', get_class($driver), 'queries are not even wrapped');
    }

    public function testDisabledInTheConfigurationRegistersNothing(): void
    {
        $this->boot('off');
        $this->assertSame(200, $this->request('GET', '/orders')->getStatusCode());
        $container = $this->kernel->getContainer();
        $this->assertFalse($container->has('slowpoke.sender'));
        foreach ($container->get('event_dispatcher')->getListeners() as $listeners) {
            foreach ($listeners as $listener) {
                $this->assertFalse(is_array($listener) && $listener[0] instanceof RequestSubscriber);
            }
        }
    }
}
