<?php

namespace Slowpoke\Laravel\Tests\Fixtures\App;

class SendInvoices
{
    public const QUERY_LINE = 12;

    public function handle(): void
    {
        // Keep QUERY_LINE in sync.
        Order::where('status', 'unpaid')->count();
    }
}
