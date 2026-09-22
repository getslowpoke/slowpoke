<?php

namespace Slowpoke\Symfony;

/** Used when the configured endpoint is not allowed: traces are dropped. */
class NullSender implements Sender
{
    public function send(string $json): bool
    {
        return false;
    }
}
