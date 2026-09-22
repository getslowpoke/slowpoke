<?php

namespace Slowpoke\Symfony\Tests\Fixtures;

use Slowpoke\Symfony\Sender;

class ThrowingSender implements Sender
{
    public function send(string $json): bool
    {
        throw new \RuntimeException('agent exploded');
    }
}
