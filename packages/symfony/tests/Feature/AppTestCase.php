<?php

namespace Slowpoke\Symfony\Tests\Feature;

use App\Kernel;
use PHPUnit\Framework\TestCase;
use Slowpoke\Symfony\Tests\Fixtures\FakeSender;
use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\HttpFoundation\Response;

abstract class AppTestCase extends TestCase
{
    /** @var Kernel|null */
    protected $kernel;
    /** @var string[] */
    private $env = [];

    protected function boot(string $variant = 'test'): Kernel
    {
        $this->kernel = new Kernel($variant);
        $this->kernel->boot();
        $db = $this->kernel->getContainer()->get('test.db');
        $db->executeStatement('CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT, email TEXT)');
        $db->executeStatement('CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER, status TEXT)');
        for ($i = 1; $i <= 6; $i++) {
            $db->executeStatement('INSERT INTO customers (id, name, email) VALUES (?, ?, ?)', [$i, "Customer $i", "c$i@example.com"]);
            $db->executeStatement('INSERT INTO orders (id, customer_id, status) VALUES (?, ?, ?)', [$i, $i, 'paid']);
        }
        return $this->kernel;
    }

    protected function tearDown(): void
    {
        if ($this->kernel !== null) {
            $this->kernel->shutdown();
            $this->kernel = null;
        }
        foreach ($this->env as $name) {
            unset($_SERVER[$name], $_ENV[$name]);
            putenv($name);
        }
        $this->env = [];
    }

    /** The way Symfony reads it: $_SERVER / $_ENV, as Dotenv or the web server set them. */
    protected function setEnv(string $name, string $value): void
    {
        $this->env[] = $name;
        $_SERVER[$name] = $_ENV[$name] = $value;
        putenv("$name=$value");
    }

    protected function sender(): FakeSender
    {
        return $this->kernel->getContainer()->get('slowpoke.sender');
    }

    /** Handles a request the way public/index.php does: handle, send, terminate. */
    protected function request(string $method, string $uri, ?callable $beforeTerminate = null): Response
    {
        $request = Request::create($uri, $method);
        $response = $this->kernel->handle($request);
        if ($beforeTerminate !== null) {
            $beforeTerminate($response);
        }
        $this->kernel->terminate($request, $response);
        return $response;
    }

    /** Line of the statement tagged "// @query <name>" in a file of the test app. */
    protected static function line(string $file, string $name): string
    {
        foreach (file(dirname(__DIR__) . '/App/' . $file) as $i => $text) {
            if (substr(rtrim($text), -strlen("// @query $name")) === "// @query $name") {
                return (string) ($i + 1);
            }
        }
        throw new \LogicException("no @query $name in $file");
    }
}
