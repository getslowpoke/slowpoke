<?php

namespace Slowpoke\Laravel\Tests\Feature;

use Slowpoke\Laravel\Tests\Fixtures\App\SendInvoices;
use Slowpoke\Laravel\Tests\Fixtures\FakeSender;

/**
 * Scheduled commands are the work nobody waits for, which is why nobody notices when they get
 * slower. They are traced like a job, with the file:line of their queries, and the panel puts
 * them next to the jobs instead of among the routes.
 *
 * The events are dispatched by name: the classes appear in different Laravel versions, and the
 * package must not depend on any of them existing.
 */
class ScheduleTraceTest extends TestCase
{
    /** @param object|null $task */
    private function start($task): void
    {
        $this->app['events']->dispatch('Illuminate\Console\Events\ScheduledTaskStarting', [(object) ['task' => $task]]);
    }

    private function finish(bool $failed = false): void
    {
        $event = $failed ? 'Illuminate\Console\Events\ScheduledTaskFailed' : 'Illuminate\Console\Events\ScheduledTaskFinished';
        $this->app['events']->dispatch($event, [(object) ['task' => null]]);
    }

    public function testScheduledCommandIsATraceOfItsOwn(): void
    {
        $this->start((object) ['description' => 'invoices:close', 'command' => "'/usr/bin/php8.3' 'artisan' invoices:close > /dev/null 2>&1"]);
        (new SendInvoices())->handle();
        $this->finish();

        [$root, $queries] = $this->sender->onlyTrace();
        $this->assertSame(5, $root['kind']);
        $this->assertSame('invoices:close', $root['name']);
        $this->assertSame('command', FakeSender::attr($root, 'slowpoke.kind'));
        $this->assertCount(1, $queries);
        $this->assertSame('tests/Fixtures/App/SendInvoices.php', FakeSender::attr($queries[0], 'code.file.path'));
    }

    public function testTheNameIsTheCommandNotThePhpBinaryThatRanIt(): void
    {
        $this->start((object) ['command' => "'/usr/bin/php8.3' 'artisan' invoices:close --force >> '/var/log/cron.log' 2>&1"]);
        $this->finish();
        [$root] = $this->sender->onlyTrace();
        $this->assertSame('invoices:close --force', $root['name']);
    }

    public function testAFailedCommandIsMarkedAndItsReasonIsNotSent(): void
    {
        $this->start((object) ['description' => 'invoices:close']);
        $this->finish(true);
        [$root] = $this->sender->onlyTrace();
        $this->assertSame(2, $root['status']['code']);
        $this->assertSame('command', FakeSender::attr($root, 'slowpoke.kind'));
    }

    public function testATaskWithoutANameIsStillSomething(): void
    {
        $this->start(null);
        $this->finish();
        [$root] = $this->sender->onlyTrace();
        $this->assertSame('scheduled task', $root['name']);
    }
}
