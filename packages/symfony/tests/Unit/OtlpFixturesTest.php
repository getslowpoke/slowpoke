<?php

namespace Slowpoke\Symfony\Tests\Unit;

use PHPUnit\Framework\TestCase;
use Slowpoke\Symfony\MessageName;
use Slowpoke\Symfony\OriginFinder;
use Symfony\Component\Console\Messenger\RunCommandMessage;
use Slowpoke\Symfony\Tests\Fixtures\FakeSender;
use Slowpoke\Symfony\Tracer;

/**
 * spec/symfony_otlp_fixtures.json holds payloads exactly as this package sends them, with what the
 * agent must read from each one. The Go receiver test replays them: change both together.
 * Regenerate with UPDATE_FIXTURES=1 ./bin/test 8.3 --filter OtlpFixtures
 */
class OtlpFixturesTest extends TestCase
{
    private function file(): string
    {
        return dirname(__DIR__, 4) . '/spec/symfony_otlp_fixtures.json';
    }

    private function scenarios(): array
    {
        $out = [];

        [$tracer, $sender, $clock, $origin] = $this->tracer();
        $tracer->startRequest('GET', $clock->now);
        $clock->now += 0.045;
        $origin->at = ['src/Controller/OrderController.php', 18];
        $tracer->recordQuery('SELECT id, customer_id FROM orders WHERE status = ? ORDER BY created_at DESC LIMIT 25', 41.2, 'pdo_mysql');
        $origin->at = ['templates/orders/index.html.twig', 7];
        for ($i = 0; $i < 6; $i++) {
            $clock->now += 0.002;
            $tracer->recordQuery('SELECT t0.id AS id_1, t0.name AS name_2 FROM customer t0 WHERE t0.id = ?', 0.9, 'pdo_mysql');
        }
        $clock->now += 0.01;
        // Written as a browser sends it: the host is normalised, so a web server logging
        // "shop.example.com" on another machine is recognised as the same requests.
        $tracer->finishRequest('/orders', '/orders', 200, 'Shop.Example.com:8443');
        $tracer->flush();
        $out[] = [
            'name' => 'request with an N+1 in a Twig template',
            'payload' => json_decode($sender->payloads[0], true),
            'expect' => [
                'route' => 'GET /orders', 'status' => 200, 'requests' => 1, 'source' => 'otlp:shop', 'site' => 'shop.example.com',
                'queries' => [
                    ['statement' => 'SELECT id, customer_id FROM orders WHERE status = ? ORDER BY created_at DESC LIMIT 25', 'n' => 1, 'origin' => 'src/Controller/OrderController.php:18', 'n_plus_one' => false],
                    ['statement' => 'SELECT t0.id AS id_1, t0.name AS name_2 FROM customer t0 WHERE t0.id = ?', 'n' => 6, 'origin' => 'templates/orders/index.html.twig:7', 'n_plus_one' => true],
                ],
            ],
        ];

        [$tracer, $sender, $clock, $origin] = $this->tracer();
        $tracer->startRequest('PUT', $clock->now);
        $origin->at = ['src/Controller/Api/OrderController.php', 40];
        $clock->now += 0.02;
        $tracer->recordQuery('UPDATE orders SET status = ?, updated_at = ? WHERE id = ?', 3.5, 'pdo_pgsql');
        $clock->now += 0.001;
        $tracer->finishRequest('/api/orders/{id}', '/api/orders/981', 500);
        $tracer->flush();
        $out[] = [
            'name' => 'route template with a parameter, server error, Postgres',
            'payload' => json_decode($sender->payloads[0], true),
            'expect' => [
                'route' => 'PUT /api/orders/{id}', 'status' => 500, 'requests' => 1, 'source' => 'otlp:shop',
                'queries' => [
                    ['statement' => 'UPDATE orders SET status = ?, updated_at = ? WHERE id = ?', 'n' => 1, 'origin' => 'src/Controller/Api/OrderController.php:40', 'n_plus_one' => false],
                ],
            ],
        ];

        [$tracer, $sender, $clock, $origin] = $this->tracer();
        $tracer->startRequest('GET', $clock->now);
        $clock->now += 0.001;
        $tracer->finishRequest(null, '/.env', 404);
        $tracer->flush();
        $out[] = [
            'name' => 'no matching route: the path stands in for the route',
            'payload' => json_decode($sender->payloads[0], true),
            'expect' => ['route' => 'GET /.env', 'status' => 404, 'requests' => 1, 'source' => 'otlp:shop', 'queries' => []],
        ];

        [$tracer, $sender, $clock, $origin] = $this->tracer();
        $tracer->startJob('App\\Message\\SendInvoices', 'async');
        $origin->at = ['src/MessageHandler/SendInvoicesHandler.php', 31];
        $clock->now += 0.3;
        $tracer->recordQuery('SELECT * FROM invoices WHERE sent_at IS NULL', 250.0, 'pdo_mysql');
        $origin->at = null; // a query issued from framework code only
        $clock->now += 0.01;
        $tracer->recordQuery('DELETE FROM messenger_messages WHERE id = ?', 1.0, 'pdo_mysql');
        $clock->now += 0.1;
        $tracer->finishJob(false);
        $tracer->flush();
        $out[] = [
            'name' => 'Messenger message: queries without an HTTP request',
            'payload' => json_decode($sender->payloads[0], true),
            'expect' => [
                'route' => 'job App\\Message\\SendInvoices', 'status' => 0, 'requests' => 1, 'source' => '',
                'job' => ['kind' => 'job', 'name' => 'App\\Message\\SendInvoices', 'runs' => 1, 'failed' => 0],
                'queries' => [
                    ['statement' => 'SELECT * FROM invoices WHERE sent_at IS NULL', 'n' => 1, 'origin' => 'src/MessageHandler/SendInvoicesHandler.php:31', 'n_plus_one' => false],
                    ['statement' => 'DELETE FROM messenger_messages WHERE id = ?', 'n' => 1, 'origin' => '', 'n_plus_one' => false],
                ],
            ],
        ];

        // A console command queued as a message: the Jobs page reads the command, never RunCommandMessage
        // nor its arguments.
        require_once dirname(__DIR__) . '/Fixtures/Wrappers/load.php';
        [$tracer, $sender, $clock, $origin] = $this->tracer();
        $tracer->startJob(MessageName::of(new RunCommandMessage('app:google-sheet:update --owner=mario@example.com')), 'async');
        $origin->at = ['src/Command/UpdateGoogleSheetCommand.php', 52];
        $clock->now += 1.2;
        $tracer->recordQuery('SELECT * FROM orders WHERE created_at >= ?', 900.0, 'pdo_mysql');
        $clock->now += 0.3;
        $tracer->finishJob(false);
        $tracer->flush();
        $out[] = [
            'name' => 'queued console command: named after the command, not the RunCommandMessage wrapper',
            'payload' => json_decode($sender->payloads[0], true),
            'expect' => [
                'route' => 'job app:google-sheet:update', 'status' => 0, 'requests' => 1, 'source' => '',
                'job' => ['kind' => 'job', 'name' => 'app:google-sheet:update', 'runs' => 1, 'failed' => 0],
                'queries' => [
                    ['statement' => 'SELECT * FROM orders WHERE created_at >= ?', 'n' => 1, 'origin' => 'src/Command/UpdateGoogleSheetCommand.php:52', 'n_plus_one' => false],
                ],
            ],
        ];

        // Cron: nobody is waiting for it, so nobody notices when it doubles.
        [$tracer, $sender, $clock, $origin] = $this->tracer();
        $tracer->startCommand('app:invoices:close');
        $origin->at = ['src/Command/CloseInvoicesCommand.php', 52];
        $clock->now += 1.2;
        $tracer->recordQuery('UPDATE invoices SET closed_at = ? WHERE closed_at IS NULL AND due_at < ?', 1180.0, 'pdo_mysql');
        $clock->now += 0.05;
        $tracer->finishCommand(true);
        $tracer->flush();
        $out[] = [
            'name' => 'console command run by cron, and it failed',
            'payload' => json_decode($sender->payloads[0], true),
            'expect' => [
                'route' => 'command app:invoices:close', 'status' => 0, 'requests' => 1, 'source' => '',
                'job' => ['kind' => 'command', 'name' => 'app:invoices:close', 'runs' => 1, 'failed' => 1],
                'queries' => [
                    ['statement' => 'UPDATE invoices SET closed_at = ? WHERE closed_at IS NULL AND due_at < ?', 'n' => 1, 'origin' => 'src/Command/CloseInvoicesCommand.php:52', 'n_plus_one' => false],
                ],
            ],
        ];

        [$tracer, $sender, $clock, $origin] = $this->tracer();
        $tracer->startRequest('POST', $clock->now);
        $clock->now += 0.005;
        $origin->at = ['src/Controller/CheckoutController.php', 27];
        $tracer->recordQuery('SELECT id, total FROM cart WHERE id = ?', 2.0, 'pdo_mysql');
        $clock->now += 0.001;
        $origin->at = ['src/Service/Stripe.php', 88];
        $tracer->startHttpCall(1, 'POST', 'https://api.stripe.com/v1/payment_intents?expand=customer');
        $clock->now += 0.42;
        $tracer->finishHttpCall(1, 200, 'POST', 'https://api.stripe.com/v1/payment_intents?expand=customer');
        $clock->now += 0.002;
        $origin->at = null; // a call made from framework code only
        $tracer->startHttpCall(2, 'GET', 'http://Partner.Example.com:8080/stock?sku=A1');
        $clock->now += 1.5;
        $tracer->finishHttpCall(2, null, 'GET', 'http://Partner.Example.com:8080/stock?sku=A1');
        $clock->now += 0.003;
        $tracer->finishRequest('/checkout', '/checkout', 200);
        $tracer->flush();
        $out[] = [
            'name' => 'request with outbound calls: one to Stripe, one failed to a partner',
            'payload' => json_decode($sender->payloads[0], true),
            'expect' => [
                'route' => 'POST /checkout', 'status' => 200, 'requests' => 1, 'source' => 'otlp:shop',
                'queries' => [
                    ['statement' => 'SELECT id, total FROM cart WHERE id = ?', 'n' => 1, 'origin' => 'src/Controller/CheckoutController.php:27', 'n_plus_one' => false],
                ],
                'outbound' => [
                    ['host' => 'api.stripe.com', 'n' => 1, 'errors' => 0, 'origin' => 'src/Service/Stripe.php:88'],
                    ['host' => 'partner.example.com:8080', 'n' => 1, 'errors' => 1, 'origin' => ''],
                ],
            ],
        ];

        return $out;
    }

    private function tracer(): array
    {
        $sender = new FakeSender();
        $clock = new \stdClass();
        $clock->now = 1760000000.0;
        $origin = new class('/var/www/app', 40, []) extends OriginFinder {
            public $at = null;
            public function find(): ?array { return $this->at; }
        };
        $n = 0;
        $ids = function (int $bytes) use (&$n) {
            $n++;
            return str_pad(dechex($n), $bytes * 2, $bytes === 16 ? 'a' : 'b', STR_PAD_LEFT);
        };
        $tracer = new Tracer($origin, function () use ($sender) { return $sender; }, ['service' => 'shop', 'version' => 'fixture'],
            function () use ($clock) { return $clock->now; }, $ids);
        return [$tracer, $sender, $clock, $origin];
    }

    public function testPayloadsMatchTheSharedFixtures(): void
    {
        $scenarios = $this->scenarios();
        if (getenv('UPDATE_FIXTURES')) {
            file_put_contents($this->file(), json_encode($scenarios, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE) . "\n");
        }
        if (!is_file($this->file())) {
            $this->markTestSkipped('spec/ is only in the Slowpoke repository');
        }
        $this->assertSame(json_decode(file_get_contents($this->file()), true), $scenarios);
    }
}
