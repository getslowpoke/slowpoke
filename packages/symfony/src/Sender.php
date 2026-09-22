<?php

namespace Slowpoke\Symfony;

interface Sender
{
    /** Delivers one OTLP/JSON payload. Never throws for network problems; false when not delivered. */
    public function send(string $json): bool;
}
