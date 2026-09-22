<?php

use App\Models\Customer;
use App\Models\Order;
use App\Models\Product;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Route;

Route::get('/', fn () => ['app' => 'laravel-shop']);

// Fast: primary key.
Route::get('/orders/{id}', fn ($id) => Order::findOrFail($id));

// N+1: customer and product loaded one by one; no index on status/created_at.
Route::get('/orders', function () {
    return Order::where('status', 'paid')->orderByDesc('created_at')->limit(25)->get()
        ->map(fn ($o) => [
            'id' => $o->id,
            'customer' => $o->customer->name,
            'product' => $o->product->name,
            'total' => $o->total,
        ]);
});

// A route parameter that reads as a word in the URL: "/customers/me/orders" is the route
// "/customers/{customer}/orders". A web server cannot know that "me" is a parameter, which is
// how the same requests were once counted twice, in a real installation.
Route::get('/customers/{customer}/orders', function ($customer) {
    $id = $customer === 'me' ? 1 : (int) $customer;

    return Order::where('customer_id', $id)->orderByDesc('created_at')->limit(10)->get(['id', 'total', 'status']);
});

// Full scan: no index on email.
Route::get('/customers/lookup', fn (Request $r) => Customer::where('email', $r->query('email'))->first());

// LIKE with a leading wildcard on a text column.
Route::get('/products/search', fn (Request $r) => Product::where('description', 'like', '%'.$r->query('q').'%')->limit(10)->get(['id', 'name']));

// Heavy report on every call.
Route::get('/reports/revenue', fn () => DB::select(
    'SELECT DATE(created_at) AS day, SUM(total) AS revenue FROM orders WHERE status = ? GROUP BY DATE(created_at) ORDER BY day DESC LIMIT 30',
    ['paid']
));

// Slow third-party call inside the request: a real HTTP call to a "payment gateway" (the go app),
// so the package traces it as an outbound call and the php-fpm slowlog sees where PHP waits.
// The query string carries a fake card token: it must never reach the server.
Route::get('/checkout', function () {
    $res = \Illuminate\Support\Facades\Http::timeout(5)->post('http://go:8080/gateway/pay?card=tok_4242secret', ['amount' => 1999]);
    return ['paid' => $res->successful()];
});
