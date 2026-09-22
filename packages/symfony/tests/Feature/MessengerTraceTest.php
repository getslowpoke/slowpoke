<?php

namespace Slowpoke\Symfony\Tests\Feature;

use App\Message\SendInvoices;
use Slowpoke\Symfony\Tests\Fixtures\FakeSender;
use Symfony\Component\Console\Messenger\RunCommandMessage;
use Symfony\Component\Messenger\Envelope;
use Symfony\Component\Messenger\EventListener\StopWorkerOnMessageLimitListener;
use Symfony\Component\Messenger\Worker;

class MessengerTraceTest extends AppTestCase
{
    protected function setUp(): void
    {
        $this->boot();
    }

    /** What messenger:consume async --limit=1 does. */
    private function consumeOne(): void
    {
        $container = $this->kernel->getContainer();
        $dispatcher = $container->get('event_dispatcher');
        $stop = new StopWorkerOnMessageLimitListener(1);
        $dispatcher->addSubscriber($stop);
        try {
            (new Worker(['async' => $container->get('test.transport')], $container->get('test.bus'), $dispatcher))->run(['sleep' => 1000]);
        } finally {
            $dispatcher->removeSubscriber($stop);
        }
    }

    public function testAHandledMessageIsATraceOfItsOwn(): void
    {
        $this->kernel->getContainer()->get('test.bus')->dispatch(new SendInvoices());
        $this->assertSame([], $this->sender()->payloads, 'dispatching outside a request records nothing');

        $this->consumeOne();

        [$root, $queries] = $this->sender()->onlyTrace();
        $this->assertSame(5, $root['kind']);
        $this->assertSame(SendInvoices::class, $root['name']);
        $this->assertSame('async', FakeSender::attr($root, 'messaging.destination.name'));
        $this->assertArrayNotHasKey('status', $root);
        $this->assertCount(1, $queries);
        $this->assertSame('src/MessageHandler/SendInvoicesHandler.php', FakeSender::attr($queries[0], 'code.file.path'));
        $this->assertSame(self::line('src/MessageHandler/SendInvoicesHandler.php', 'invoices'), FakeSender::attr($queries[0], 'code.line.number'));
    }

    public function testAFailedMessageIsAnErrorWithoutItsMessage(): void
    {
        $this->kernel->getContainer()->get('test.bus')->dispatch(new SendInvoices(true));
        $this->consumeOne();

        [$root, $queries] = $this->sender()->onlyTrace();
        $this->assertSame(2, $root['status']['code']);
        $this->assertCount(1, $queries);
        $this->assertStringNotContainsString('mail server', $this->sender()->payloads[0]);
    }

    /** Found in production on Laravel's twin: a queued command showed as its wrapper class. */
    public function testAQueuedCommandIsNamedAfterTheCommand(): void
    {
        require_once dirname(__DIR__) . '/Fixtures/Wrappers/load.php';
        $this->kernel->getContainer()->get('test.transport')->send(new Envelope(new RunCommandMessage('app:missing --owner=mario.rossi@example.com')));
        $this->consumeOne();

        [$root] = $this->sender()->onlyTrace();
        $this->assertSame('app:missing', $root['name']);
        $this->assertStringNotContainsString('mario.rossi', $this->sender()->payloads[0]);
    }

    public function testEachMessageIsSentAsSoonAsItIsHandled(): void
    {
        $bus = $this->kernel->getContainer()->get('test.bus');
        $bus->dispatch(new SendInvoices());
        $bus->dispatch(new SendInvoices());
        $this->consumeOne();
        $this->assertCount(1, $this->sender()->payloads);
        $this->consumeOne();
        $this->assertCount(2, $this->sender()->payloads);
    }
}
