<?php

namespace Slowpoke\Laravel\Tests\Fixtures;

use Slowpoke\Laravel\Sender;

class ThrowingSender implements Sender
{
    public function send(string $json): bool
    {
        throw new \RuntimeException('agent exploded');
    }
}
