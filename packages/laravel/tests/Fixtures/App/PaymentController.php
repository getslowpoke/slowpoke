<?php

namespace Slowpoke\Laravel\Tests\Fixtures\App;

use Illuminate\Routing\Controller;
use Illuminate\Support\Facades\Http;

class PaymentController extends Controller
{
    public const CHARGE_LINE = 16;
    public const DOWN_LINE = 23;

    public function charge()
    {
        // Keep the line constants above in sync with these lines.
        $status = Http::withToken('sk_live_secret_token')->post('https://api.stripe.com/v1/charges?customer=cus_secret', ['amount' => 4200])->status();
        return ['status' => $status];
    }

    public function down()
    {
        try {
            Http::timeout(1)->get('http://127.0.0.1:1/unreachable?token=zzz');
        } catch (\Throwable $e) {
            return ['failed' => true];
        }
        return ['failed' => false];
    }
}
