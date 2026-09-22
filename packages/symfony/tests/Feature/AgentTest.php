<?php

namespace Slowpoke\Symfony\Tests\Feature;

/**
 * The app configured by the shipped config/packages/slowpoke.yaml, talking HTTP to an agent that
 * works, that does not exist, or that never answers.
 */
class AgentTest extends AppTestCase
{
    public function testTheTraceReachesARealAgent(): void
    {
        // A tiny agent in another process: accepts one request, prints it, answers like the Go receiver.
        $code = '$s = stream_socket_server("tcp://127.0.0.1:0"); echo stream_socket_get_name($s, false), "\\n"; '
            . '$c = stream_socket_accept($s, 10); stream_set_timeout($c, 5); $in = ""; '
            . 'while (strpos($in, "}]}]}]}") === false && !feof($c)) { $in .= fread($c, 65536); } '
            . 'fwrite($c, "HTTP/1.1 200 OK\\r\\nContent-Length: 2\\r\\n\\r\\n{}"); fclose($c); echo $in;';
        $proc = proc_open([PHP_BINARY, '-r', $code], [1 => ['pipe', 'w']], $pipes);
        $address = trim(fgets($pipes[1]));
        $this->setEnv('SLOWPOKE_OTLP_ENDPOINT', "http://$address/v1/traces");
        $this->setEnv('SLOWPOKE_SERVICE', 'shop');
        $this->boot('agent');

        $this->assertSame(200, $this->request('GET', '/orders/4')->getStatusCode());

        $received = stream_get_contents($pipes[1]);
        proc_close($proc);
        $this->assertStringStartsWith("POST /v1/traces HTTP/1.1\r\n", $received);
        $body = json_decode(substr($received, strpos($received, "\r\n\r\n") + 4), true);
        $spans = $body['resourceSpans'][0]['scopeSpans'][0]['spans'];
        $this->assertSame('GET /orders/{id}', $spans[0]['name']);
        $this->assertSame('src/Controller/OrderController.php', $spans[1]['attributes'][2]['value']['stringValue']);
    }

    public function testAnUnreachableAgentNeverBreaksNorSlowsTheRequest(): void
    {
        $this->setEnv('SLOWPOKE_OTLP_ENDPOINT', 'http://127.0.0.1:1/v1/traces');
        $this->boot('agent');
        $this->request('GET', '/orders/1'); // warm: container services, route templates
        $started = microtime(true);
        $this->assertSame(200, $this->request('GET', '/orders')->getStatusCode());
        $this->assertLessThan(0.5, microtime(true) - $started);
    }

    public function testAnAgentThatNeverAnswersCostsAtMostTheTimeout(): void
    {
        // The kernel accepts the connection into the backlog; nobody ever reads or answers.
        $server = stream_socket_server('tcp://127.0.0.1:0');
        $this->setEnv('SLOWPOKE_OTLP_ENDPOINT', 'http://' . stream_socket_get_name($server, false) . '/v1/traces');
        $this->boot('agent');
        $this->request('GET', '/orders/1');
        $handled = null;
        $started = microtime(true);
        $response = $this->request('GET', '/orders', function () use ($started, &$handled) {
            $handled = microtime(true) - $started;
        });
        $total = microtime(true) - $started;
        fclose($server);
        $this->assertSame(200, $response->getStatusCode());
        $this->assertGreaterThan(0.09, $total - $handled, 'terminate waited for the agent, up to SLOWPOKE_TIMEOUT');
        $this->assertLessThan(0.4, $total - $handled, 'and not a moment longer');
    }

    public function testAPublicEndpointIsRefused(): void
    {
        $this->setEnv('SLOWPOKE_OTLP_ENDPOINT', 'http://collector.example.com/v1/traces');
        $this->boot('agent');
        $started = microtime(true);
        $this->assertSame(200, $this->request('GET', '/orders')->getStatusCode());
        $this->assertLessThan(0.5, microtime(true) - $started, 'no DNS lookup, no connection');
    }

    public function testNonsenseSettingsNeverBreakTheApp(): void
    {
        $this->setEnv('SLOWPOKE_TIMEOUT', 'soon');
        $this->setEnv('SLOWPOKE_MAX_QUERIES', 'lots');
        $this->setEnv('SLOWPOKE_ENABLED', 'maybe');
        $this->setEnv('SLOWPOKE_OTLP_ENDPOINT', 'not a url');
        $this->boot('agent');
        $this->assertSame(200, $this->request('GET', '/orders')->getStatusCode());
    }
}
