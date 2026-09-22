<?php

namespace Slowpoke\Symfony\EventListener;

use Slowpoke\Symfony\TracerProvider;
use Symfony\Component\Console\ConsoleEvents;
use Symfony\Component\Console\Event\ConsoleCommandEvent;
use Symfony\Component\Console\Event\ConsoleTerminateEvent;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;

/**
 * One trace per console command, which on a server means cron: nobody waits for a nightly import,
 * which is why nobody notices when it takes four minutes instead of forty seconds.
 *
 * Commands that never end are left alone. A worker traced as one command would hold a single trace
 * open for hours and swallow the trace of every message it handles.
 */
class ConsoleSubscriber implements EventSubscriberInterface
{
    /** The workers shipped with Symfony: long-running by design. */
    public const LONG_RUNNING = ['messenger:consume', 'messenger:failed:retry', 'server:run', 'server:start'];

    /** @var TracerProvider */
    private $provider;
    /** @var string[] */
    private $skip;
    /** @var bool whether this process has a command trace of its own open */
    private $tracing = false;

    /** @param string[] $skip command names to leave alone, on top of the long-running ones */
    public function __construct(TracerProvider $provider, array $skip = [])
    {
        $this->provider = $provider;
        $this->skip = array_merge(self::LONG_RUNNING, array_values(array_filter($skip)));
    }

    /** @return array<string, array{0: string, 1: int}> */
    public static function getSubscribedEvents(): array
    {
        return [
            ConsoleEvents::COMMAND => ['onCommand', 4096],
            ConsoleEvents::TERMINATE => ['onTerminate', -4096],
        ];
    }

    public function onCommand(ConsoleCommandEvent $event): void
    {
        try {
            $command = $event->getCommand();
            $name = $command !== null ? (string) $command->getName() : '';
            if ($name === '' || in_array($name, $this->skip, true)) {
                return;
            }
            if (($tracer = $this->provider->tracer()) !== null) {
                $tracer->startCommand($name);
                $this->tracing = true;
            }
        } catch (\Throwable $e) {
            // never let observability break the command
        }
    }

    public function onTerminate(ConsoleTerminateEvent $event): void
    {
        // Without this the worker's own terminate would close the trace of the last message it
        // handled, and report it as the command.
        if (!$this->tracing) {
            return;
        }
        $this->tracing = false;
        if (($tracer = $this->provider->tracer()) !== null) {
            $tracer->finishCommand($event->getExitCode() !== 0);
            $tracer->flush(); // nothing is waiting for a response here
        }
    }
}
