<?php

namespace Slowpoke\Laravel\Tests\Fixtures\App;

use Illuminate\Http\Request;
use Illuminate\Routing\Controller;

class OrderController extends Controller
{
    public const LIST_LINE = 18;
    public const N_PLUS_ONE_LINE = 20;
    public const LOOKUP_LINE = 32;

    public function index()
    {
        $names = [];
        // Keep the line constants above in sync with these lines.
        $orders = Order::where('status', 'paid')->get();
        foreach ($orders as $order) {
            $names[] = $order->customer->name;
        }
        return $names;
    }

    public function show($id)
    {
        return Order::findOrFail($id);
    }

    public function lookup(Request $request)
    {
        return Customer::where('email', $request->query('email'))->where('name', 'Ada Secret')->first();
    }
}
