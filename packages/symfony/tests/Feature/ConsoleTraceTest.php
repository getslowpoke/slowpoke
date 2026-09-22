<?php

namespace Slowpoke\Symfony\Tests\Feature;

use Slowpoke\Symfony\Tests\Fixtures\FakeSender;
use Symfony\Bundle\FrameworkBundle\Console\Application;
use Symfony\Component\Console\Input\ArrayInput;
use Symfony\Component\Console\Output\NullOutput;

/**
 * Cron is where the slow work hides: nobody waits for the nightly job, so nobody notices when it
 * takes four minutes instead of forty seconds. One trace per command, with its queries.
 */
class ConsoleTraceTest extends AppTestCase
{
    protected function setUp(): void
    {
        $this->boot();
    }

    private function console(array $input): int
    {
        $app = new Application($this->kernel);
        $app->setAutoExit(false);

        return $app->run(new ArrayInput($input), new NullOutput());
    }

    public function testACommandIsATraceWithItsQueries(): void
    {
        $this->assertSame(0, $this->console(['command' => 'app:close-invoices']));

        [$root, $queries] = $this->sender()->onlyTrace();
        $this->assertSame(5, $root['kind'], 'a command is nobody waiting: same kind as a message');
        $this->assertSame('app:close-invoices', $root['name']);
        $this->assertSame('command', FakeSender::attr($root, 'slowpoke.kind'));
        $this->assertArrayNotHasKey('status', $root);
        $this->assertCount(1, $queries);
        $this->assertSame('src/Command/CloseInvoicesCommand.php', FakeSender::attr($queries[0], 'code.file.path'));
        $this->assertSame(self::line('src/Command/CloseInvoicesCommand.php', 'invoices'), FakeSender::attr($queries[0], 'code.line.number'));
    }

    public function testACommandThatExitsWithAnErrorIsMarkedFailed(): void
    {
        $this->assertSame(1, $this->console(['command' => 'app:close-invoices', '--fail' => true]));

        [$root] = $this->sender()->onlyTrace();
        $this->assertSame(2, $root['status']['code']);
    }

    /**
     * A worker never ends. Traced as a command it would hold one trace open for hours and swallow
     * the trace of every message it handles, which is the one thing the Jobs page is there for.
     */
    public function testLongRunningCommandsAreLeftAlone(): void
    {
        $this->assertSame(0, $this->console(['command' => 'messenger:consume', 'receivers' => ['async'], '--limit' => 1, '--time-limit' => 1]));

        foreach ($this->sender()->payloads as $payload) {
            $trace = json_decode($payload, true);
            $root = $trace['resourceSpans'][0]['scopeSpans'][0]['spans'][0];
            $this->assertNotSame('messenger:consume', $root['name'], 'the worker was traced as one command');
        }
    }
}
