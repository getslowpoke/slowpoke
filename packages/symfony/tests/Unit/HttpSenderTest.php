<?php

namespace Slowpoke\Symfony\Tests\Unit;

use PHPUnit\Framework\TestCase;
use Slowpoke\Symfony\HttpSender;

class HttpSenderTest extends TestCase
{
    public function testPostsJsonToTheAgent(): void
    {
        $server = stream_socket_server('tcp://127.0.0.1:0', $errno, $errstr);
        $port = (int) substr(strrchr(stream_socket_get_name($server, false), ':'), 1);

        // Nobody answers: the kernel accepts the connection, the sender writes and gives up waiting.
        $sender = HttpSender::fromUrl("http://127.0.0.1:$port/v1/traces", 0.05);
        $started = microtime(true);
        $sender->send('{"resourceSpans":[]}');
        $this->assertLessThan(0.5, microtime(true) - $started);

        $conn = stream_socket_accept($server, 1);
        stream_set_timeout($conn, 1);
        $request = stream_get_contents($conn);
        $this->assertStringStartsWith("POST /v1/traces HTTP/1.1\r\n", $request);
        $this->assertStringContainsString("\r\nContent-Type: application/json\r\n", $request);
        $this->assertStringContainsString("\r\nContent-Length: 20\r\n", $request);
        $this->assertStringEndsWith("\r\n\r\n" . '{"resourceSpans":[]}', $request);
    }

    public function testTrueWhenTheAgentAccepts(): void
    {
        // A tiny agent in another process: accepts one request and answers like the Go receiver.
        $code = '$s = stream_socket_server("tcp://127.0.0.1:0"); echo stream_socket_get_name($s, false), "\\n"; '
            . '$c = stream_socket_accept($s, 5); fread($c, 65536); fwrite($c, "HTTP/1.1 200 OK\\r\\nContent-Length: 2\\r\\n\\r\\n{}"); fclose($c);';
        $proc = proc_open([PHP_BINARY, '-r', $code], [1 => ['pipe', 'w']], $pipes);
        $address = trim(fgets($pipes[1]));

        $sender = HttpSender::fromUrl("http://$address/v1/traces", 1.0);
        $this->assertTrue($sender->send('{}'));
        proc_close($proc);
    }

    public function testClosedPortDoesNotThrow(): void
    {
        $sender = HttpSender::fromUrl('http://127.0.0.1:1/v1/traces', 0.05);
        $started = microtime(true);
        $this->assertFalse($sender->send('{}'));
        $this->assertLessThan(0.5, microtime(true) - $started);
    }

    public function testUnknownHostDoesNotThrow(): void
    {
        $sender = HttpSender::fromUrl('http://slowpoke-agent-that-does-not-exist:4318/v1/traces', 0.05);
        $this->assertFalse($sender->send('{}'));
    }

    public function testOnlyPlainHttpToLocalOrPrivateHosts(): void
    {
        foreach ([
            'http://127.0.0.1:4318/v1/traces', 'http://localhost:4318/v1/traces', 'http://agent:4318/v1/traces',
            'http://10.0.3.7:4318/v1/traces', 'http://192.168.1.20:4318/v1/traces', 'http://[::1]:4318/v1/traces',
            'http://slowpoke.internal:4318/v1/traces',
        ] as $url) {
            $this->assertNotNull(HttpSender::fromUrl($url, 0.05), $url);
        }
        foreach ([
            'https://127.0.0.1:4318/v1/traces', 'http://8.8.8.8:4318/v1/traces', 'http://collector.example.com/v1/traces',
            'ftp://127.0.0.1/x', 'not a url', '',
        ] as $url) {
            $this->assertNull(HttpSender::fromUrl($url, 0.05), $url);
        }
    }

    public function testDefaultPortAndPath(): void
    {
        $sender = HttpSender::fromUrl('http://127.0.0.1', 0.05);
        $this->assertSame(['127.0.0.1', 80, '/v1/traces'], [$sender->host(), $sender->port(), $sender->path()]);
    }
}
