<?php

namespace Slowpoke\Symfony;

use Symfony\Component\Messenger\Envelope;

/**
 * The name a Messenger message goes by on the Jobs page: the work it does, not the wrapper carrying
 * it. RunCommandMessage becomes the command ("app:report"), RedispatchMessage the message inside,
 * RunProcessMessage "process <executable>", a scheduled ServiceCallMessage "Service::method".
 * Arguments, options and command lines never reach the name: they may hold personal data or
 * secrets (a password in a mysqldump line). The classes are matched by name, so none of them has
 * to exist: Symfony 5.4 ships none, and process and scheduler are optional components.
 */
final class MessageName
{
    public static function of(object $message, int $depth = 0): string
    {
        $class = get_class($message);
        $name = '';
        try {
            switch ($class) {
                case 'Symfony\\Component\\Console\\Messenger\\RunCommandMessage':
                    $name = self::firstWord((string) ($message->input ?? ''));
                    break;
                case 'Symfony\\Component\\Messenger\\Message\\RedispatchMessage':
                    $inner = $message->envelope ?? null;
                    $inner = $inner instanceof Envelope ? $inner->getMessage() : $inner;
                    return is_object($inner) && $depth < 3 ? self::of($inner, $depth + 1) : $class;
                case 'Symfony\\Component\\Process\\Messenger\\RunProcessMessage':
                    $command = $message->command ?? [];
                    $executable = is_array($command) && isset($command[0]) && is_string($command[0])
                        ? $command[0]
                        : self::firstWord((string) ($message->commandLine ?? ''));
                    $executable = basename(trim($executable, '\'"'));
                    return $executable !== '' && strlen($executable) <= 100 ? "process $executable" : 'process';
                case 'Symfony\\Component\\Scheduler\\Messenger\\ServiceCallMessage':
                    $name = (string) $message->getServiceId();
                    $method = (string) $message->getMethod();
                    if ($name !== '' && $method !== '' && $method !== '__invoke') {
                        $name .= "::$method";
                    }
                    break;
            }
        } catch (\Throwable $e) {
            // a wrapper whose shape changed keeps its own name
        }
        return $name !== '' && strlen($name) <= 200 ? $name : $class;
    }

    /** The first word of a command line that is neither an option nor a VAR=value assignment. */
    private static function firstWord(string $line): string
    {
        foreach (preg_split('/\s+/', trim($line)) ?: [] as $word) {
            $word = trim($word, '\'"');
            if ($word !== '' && $word[0] !== '-' && strpos($word, '=') === false) {
                return $word;
            }
        }
        return '';
    }
}
