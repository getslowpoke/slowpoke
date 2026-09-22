<?php

namespace Slowpoke\Symfony\Tests\Unit;

use PHPUnit\Framework\TestCase;
use Slowpoke\Symfony\EventListener\ConsoleSubscriber;
use Slowpoke\Symfony\Tests\Fixtures\FakeSender;
use Slowpoke\Symfony\TracerProvider;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Event\ConsoleCommandEvent;
use Symfony\Component\Console\Event\ConsoleTerminateEvent;
use Symfony\Component\Console\Input\ArrayInput;
use Symfony\Component\Console\Output\NullOutput;

class ConsoleSubscriberTest extends TestCase
{
    /** @var FakeSender */
    private $sender;
    /** @var TracerProvider */
    private $provider;

    private function subscriber(array $skip = []): ConsoleSubscriber
    {
        $this->sender = new FakeSender();
        $sender = $this->sender;
        $this->provider = new TracerProvider(['code_root' => '/app'], function () use ($sender) { return $sender; });

        return new ConsoleSubscriber($this->provider, $skip);
    }

    private function runCommand(ConsoleSubscriber $subscriber, string $name, int $exitCode = 0): void
    {
        $command = (new Command($name));
        $input = new ArrayInput([]);
        $output = new NullOutput();
        $subscriber->onCommand(new ConsoleCommandEvent($command, $input, $output));
        $subscriber->onTerminate(new ConsoleTerminateEvent($command, $input, $output, $exitCode));
    }

    public function testEveryCommandIsOneTrace(): void
    {
        $subscriber = $this->subscriber();
        $this->runCommand($subscriber, 'app:import');
        $this->runCommand($subscriber, 'app:import', 3);

        $this->assertCount(2, $this->sender->payloads);
        [$first] = $this->sender->trace(0);
        [$second] = $this->sender->trace(1);
        $this->assertSame('app:import', $first['name']);
        $this->assertArrayNotHasKey('status', $first);
        $this->assertSame(2, $second['status']['code'], 'a non-zero exit code is a failed run');
    }

    public function testWorkersAndTheNamesTheApplicationExcludesAreNotTraced(): void
    {
        $subscriber = $this->subscriber(['app:watch']);
        foreach (ConsoleSubscriber::LONG_RUNNING as $worker) {
            $this->runCommand($subscriber, $worker);
        }
        $this->runCommand($subscriber, 'app:watch');

        $this->assertSame([], $this->sender->payloads);
    }

    /**
     * The worker's own terminate must not close the trace of the message it just handled: that
     * message would be reported as the command, and the Jobs page would show one run of
     * messenger:consume instead of a thousand messages.
     */
    public function testAWorkerDoesNotStealTheTraceOfItsLastMessage(): void
    {
        $subscriber = $this->subscriber();
        $command = new Command('messenger:consume');
        $input = new ArrayInput([]);
        $output = new NullOutput();
        $subscriber->onCommand(new ConsoleCommandEvent($command, $input, $output));

        // The worker picks up a message and is still handling it when the command ends.
        $tracer = $this->provider->tracer();
        $tracer->startJob('App\\Message\\SendInvoices', 'async');
        $subscriber->onTerminate(new ConsoleTerminateEvent($command, $input, $output, 0));
        $this->assertSame([], $this->sender->payloads, 'the worker sent the message trace as its own');

        // The message finishes the way it always does, and is sent as a message.
        $tracer->finishJob(false);
        $tracer->flush();
        [$root] = $this->sender->onlyTrace();
        $this->assertSame('App\\Message\\SendInvoices', $root['name']);
    }
}
