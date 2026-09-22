<?php

namespace Slowpoke\Laravel;

/**
 * The name a queued job goes by on the Jobs page: the work it does, not the wrapper carrying it.
 *
 * Artisan::queue('google-sheet:update', [...]) runs as Illuminate\Foundation\Console\QueuedCommand.
 * Laravel before 10 names it after the wrapper, 10 and later after the command string, which may
 * hold arguments ('report:send 42 --to=someone@example.com'). Either way the page must read
 * "google-sheet:update" and never an argument: they may carry personal data. The other wrappers
 * (mailables, notifications, queued listeners, broadcasts, closures) already name the inner class
 * in every supported Laravel.
 */
final class JobName
{
    private const QUEUED_COMMAND = 'Illuminate\\Foundation\\Console\\QueuedCommand';

    /** @param array<string, mixed>|null $payload */
    public static function of(string $resolved, ?array $payload): string
    {
        $data = is_array($payload) && isset($payload['data']) && is_array($payload['data']) ? $payload['data'] : [];
        if ($resolved !== self::QUEUED_COMMAND && ($data['commandName'] ?? null) !== self::QUEUED_COMMAND) {
            return $resolved;
        }
        $command = is_string($data['command'] ?? null) ? self::queuedCommand($data['command']) : null;
        if ($command === null && $resolved !== self::QUEUED_COMMAND) {
            $command = $resolved; // Laravel 10+: displayName() is the command string
        }
        $name = $command === null ? '' : (string) strtok(trim($command), " \t\r\n");
        return $name !== '' ? $name : self::QUEUED_COMMAND;
    }

    /**
     * Reads the command out of serialize(new QueuedCommand([$command, $parameters])) without
     * unserializing it: the payload comes from the queue, and only its first string is needed.
     */
    private static function queuedCommand(string $serialized): ?string
    {
        if (!preg_match('/s:7:"\x00\*\x00data";a:\d+:\{i:0;s:(\d+):"/', $serialized, $m, PREG_OFFSET_CAPTURE)) {
            return null;
        }
        $length = (int) $m[1][0];
        $start = $m[0][1] + strlen($m[0][0]);
        if ($length < 1 || $length > 512 || substr($serialized, $start + $length, 2) !== '";') {
            return null;
        }
        return substr($serialized, $start, $length);
    }
}
