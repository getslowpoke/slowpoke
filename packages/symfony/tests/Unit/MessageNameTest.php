<?php

namespace Slowpoke\Symfony\Tests\Unit;

use App\Message\SendInvoices;
use PHPUnit\Framework\TestCase;
use Slowpoke\Symfony\MessageName;
use Symfony\Component\Console\Messenger\RunCommandMessage;
use Symfony\Component\Messenger\Envelope;
use Symfony\Component\Messenger\Message\RedispatchMessage;
use Symfony\Component\Process\Messenger\RunProcessMessage;
use Symfony\Component\Scheduler\Messenger\ServiceCallMessage;

require_once dirname(__DIR__) . '/Fixtures/Wrappers/load.php';

/**
 * A message that only carries the real work is named after that work on the Jobs page: "app:report"
 * rather than RunCommandMessage, 17 minutes a day that nobody can place otherwise. Arguments,
 * options and command lines never reach the name: they may hold personal data or secrets.
 */
class MessageNameTest extends TestCase
{
    public function testAnApplicationMessageIsItsClass(): void
    {
        $this->assertSame(SendInvoices::class, MessageName::of(new SendInvoices()));
    }

    public function testAQueuedConsoleCommandIsTheCommandName(): void
    {
        $this->assertSame('app:google-sheet:update', MessageName::of(new RunCommandMessage('app:google-sheet:update --owner=mario.rossi@example.com 42')));
        $this->assertSame('app:report', MessageName::of(new RunCommandMessage('--no-interaction "app:report" mario.rossi@example.com')));
        $this->assertSame(RunCommandMessage::class, MessageName::of(new RunCommandMessage('  ')));
    }

    public function testARedispatchedMessageIsTheMessageInside(): void
    {
        $this->assertSame(SendInvoices::class, MessageName::of(new RedispatchMessage(new Envelope(new SendInvoices()), ['async'])));
        $this->assertSame(SendInvoices::class, MessageName::of(new RedispatchMessage(new SendInvoices(), 'async')));
        $this->assertSame('app:report', MessageName::of(new RedispatchMessage(new Envelope(new RunCommandMessage('app:report --to=x')))));
    }

    public function testAProcessIsItsExecutableNeverItsCommandLine(): void
    {
        $this->assertSame('process pg_dump', MessageName::of(new RunProcessMessage(['/usr/bin/pg_dump', '--dbname=postgresql://app:s3cret@db/app'])));
        $this->assertSame('process mysqldump', MessageName::of(RunProcessMessage::fromShellCommandline('MYSQL_PWD=s3cret mysqldump -u app shop > /tmp/x.sql')));
        $this->assertSame('process', MessageName::of(new RunProcessMessage([])));
    }

    public function testAScheduledServiceCallIsTheServiceAndMethod(): void
    {
        $this->assertSame('App\\Service\\Reports::nightly', MessageName::of(new ServiceCallMessage('App\\Service\\Reports', 'nightly', ['mario.rossi@example.com'])));
        $this->assertSame('App\\Service\\Cleanup', MessageName::of(new ServiceCallMessage('App\\Service\\Cleanup')));
    }
}
