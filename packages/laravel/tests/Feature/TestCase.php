<?php

namespace Slowpoke\Laravel\Tests\Feature;

use Illuminate\Database\Schema\Blueprint;
use Orchestra\Testbench\TestCase as Testbench;
use Slowpoke\Laravel\Sender;
use Slowpoke\Laravel\SlowpokeServiceProvider;
use Slowpoke\Laravel\Tests\Fixtures\App\Customer;
use Slowpoke\Laravel\Tests\Fixtures\App\Order;
use Slowpoke\Laravel\Tests\Fixtures\App\OrderController;
use Slowpoke\Laravel\Tests\Fixtures\FakeSender;

abstract class TestCase extends Testbench
{
    /** @var FakeSender */
    protected $sender;

    protected function getPackageProviders($app)
    {
        return [SlowpokeServiceProvider::class];
    }

    protected function getEnvironmentSetUp($app)
    {
        $app['config']->set('database.default', 'testing');
        $app['config']->set('database.connections.testing', ['driver' => 'sqlite', 'database' => ':memory:', 'prefix' => '']);
        $app['config']->set('slowpoke.code_root', dirname(__DIR__, 2));
        $app['config']->set('slowpoke.service', 'shop');

        $this->sender = new FakeSender();
        $app->instance(Sender::class, $this->sender);

        $router = $app['router'];
        $router->get('/orders', [OrderController::class, 'index']);
        $router->get('/orders/{id}', [OrderController::class, 'show']);
        $router->get('/customers/lookup', [OrderController::class, 'lookup']);
    }

    protected function setUp(): void
    {
        parent::setUp();
        $schema = $this->app['db']->connection()->getSchemaBuilder();
        $schema->create('customers', function (Blueprint $t) {
            $t->increments('id');
            $t->string('name');
            $t->string('email');
        });
        $schema->create('orders', function (Blueprint $t) {
            $t->increments('id');
            $t->integer('customer_id');
            $t->string('status');
        });
        for ($i = 1; $i <= 6; $i++) {
            Customer::create(['name' => "Customer $i", 'email' => "c$i@example.com"]);
            Order::create(['customer_id' => $i, 'status' => 'paid']);
        }
        $this->sender->payloads = []; // schema and seed queries ran outside any request
    }
}
