<?php

namespace Slowpoke\Laravel\Tests\Feature;

use Illuminate\Foundation\Console\QueuedCommand;
use Illuminate\Queue\Events\JobFailed;
use Illuminate\Queue\Events\JobProcessed;
use Illuminate\Queue\Events\JobProcessing;
use Slowpoke\Laravel\Tests\Fixtures\App\SendInvoices;
use Slowpoke\Laravel\Tests\Fixtures\FakeSender;
use Slowpoke\Laravel\Tests\Fixtures\QueuedJob;

class JobTraceTest extends TestCase
{
    private function job(): QueuedJob
    {
        $payload = json_encode(['displayName' => SendInvoices::class, 'job' => 'Illuminate\\Queue\\CallQueuedHandler@call', 'data' => []]);
        return new QueuedJob($this->app, $payload, 'redis', 'invoices');
    }

    public function testQueuedJobIsATraceOfItsOwn(): void
    {
        $job = $this->job();
        $this->app['events']->dispatch(new JobProcessing('redis', $job));
        (new SendInvoices())->handle();
        $this->app['events']->dispatch(new JobProcessed('redis', $job));

        [$root, $queries] = $this->sender->onlyTrace();
        $this->assertSame(5, $root['kind']);
        $this->assertSame(SendInvoices::class, $root['name']);
        $this->assertSame('invoices', FakeSender::attr($root, 'messaging.destination.name'));
        $this->assertCount(1, $queries);
        $this->assertSame('tests/Fixtures/App/SendInvoices.php', FakeSender::attr($queries[0], 'code.file.path'));
        $this->assertSame((string) SendInvoices::QUERY_LINE, FakeSender::attr($queries[0], 'code.line.number'));
    }

    /** A job as the worker pulls it, with the payload Laravel builds for $command. */
    private function queued($command, ?string $displayName = null): QueuedJob
    {
        $payload = json_encode([
            'displayName' => $displayName ?? get_class($command),
            'job' => 'Illuminate\\Queue\\CallQueuedHandler@call',
            'data' => ['commandName' => get_class($command), 'command' => serialize($command)],
        ]);
        return new QueuedJob($this->app, $payload, 'redis', 'default');
    }

    private function nameOf(QueuedJob $job): string
    {
        $this->app['events']->dispatch(new JobProcessing('redis', $job));
        $this->app['events']->dispatch(new JobProcessed('redis', $job));
        [$root] = $this->sender->onlyTrace();
        $this->assertStringNotContainsString('mario.rossi', $this->sender->payloads[0], 'arguments stay out of the name');
        return $root['name'];
    }

    /**
     * Found in production: Artisan::queue('google-sheet:update', [...]) showed on the Jobs page as
     * Illuminate\Foundation\Console\QueuedCommand, 17 minutes a day of work nobody could name.
     * Laravel before 10 puts the wrapper in displayName; the command is only in the serialized job.
     */
    public function testQueuedArtisanCommandIsNamedAfterTheCommand(): void
    {
        $command = new QueuedCommand(['google-sheet:update', ['--owner' => 'mario.rossi@example.com']]);
        $this->assertSame('google-sheet:update', $this->nameOf($this->queued($command, QueuedCommand::class)));
    }

    public function testQueuedCommandGivenAsAClassKeepsTheClass(): void
    {
        $command = new QueuedCommand(['App\\Console\\Commands\\SendGetReceiptsEvent', []]);
        $this->assertSame('App\\Console\\Commands\\SendGetReceiptsEvent', $this->nameOf($this->queued($command, QueuedCommand::class)));
    }

    /** Laravel 10+ names it after the command string itself, which may carry arguments. */
    public function testArgumentsWrittenInTheCommandStringAreLeftOut(): void
    {
        $command = new QueuedCommand(['report:send 42 --to=mario.rossi@example.com', []]);
        $this->assertSame('report:send', $this->nameOf($this->queued($command, 'report:send 42 --to=mario.rossi@example.com')));
        $this->sender->payloads = [];
        $this->assertSame('report:send', $this->nameOf($this->queued($command, QueuedCommand::class)));
    }

    public function testAnUnreadableQueuedCommandKeepsTheWrapperName(): void
    {
        $payload = json_encode([
            'displayName' => QueuedCommand::class,
            'job' => 'Illuminate\\Queue\\CallQueuedHandler@call',
            'data' => ['commandName' => QueuedCommand::class, 'command' => 'encrypted-or-garbage'],
        ]);
        $this->assertSame(QueuedCommand::class, $this->nameOf(new QueuedJob($this->app, $payload, 'redis', 'default')));
    }

    public function testOtherWrappersAlreadyCarryTheRealName(): void
    {
        // SendQueuedMailable, SendQueuedNotifications, CallQueuedListener and BroadcastEvent put the
        // inner class in displayName in every supported Laravel: nothing to unwrap, nothing to touch.
        $this->assertSame('App\\Mail\\OrderShipped', $this->nameOf($this->queued(new SendInvoices(), 'App\\Mail\\OrderShipped')));
    }

    public function testFailedJob(): void
    {
        $job = $this->job();
        $this->app['events']->dispatch(new JobProcessing('redis', $job));
        $this->app['events']->dispatch(new JobFailed('redis', $job, new \RuntimeException('boom')));
        [$root] = $this->sender->onlyTrace();
        $this->assertSame(2, $root['status']['code']);
        $this->assertStringNotContainsString('boom', $this->sender->payloads[0]);
    }

    public function testSyncJobsInsideARequestAreNotSeparateTraces(): void
    {
        $job = $this->job();
        $this->app['events']->dispatch(new JobProcessing('sync', $job));
        (new SendInvoices())->handle();
        $this->app['events']->dispatch(new JobProcessed('sync', $job));
        $this->assertSame([], $this->sender->payloads);
    }

    public function testWorkerQueriesBetweenJobsAreIgnored(): void
    {
        (new SendInvoices())->handle();
        $this->app->terminate();
        $this->assertSame([], $this->sender->payloads);
    }
}
