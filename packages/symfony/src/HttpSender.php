<?php

namespace Slowpoke\Symfony;

/**
 * Posts OTLP/JSON to the Slowpoke agent on the same machine or private network, with a hard time
 * budget. Raw sockets instead of cURL or Guzzle: no extension to install, no dependency to clash with.
 */
class HttpSender implements Sender
{
    /** @var string */
    private $host;
    /** @var int */
    private $port;
    /** @var string */
    private $path;
    /** @var float */
    private $timeout;

    private function __construct(string $host, int $port, string $path, float $timeout)
    {
        $this->host = $host;
        $this->port = $port;
        $this->path = $path;
        $this->timeout = $timeout;
    }

    /** Null when the URL is not plain http to a local or private host: queries must not travel the internet. */
    public static function fromUrl(string $url, float $timeout): ?self
    {
        $parts = @parse_url($url);
        if (!is_array($parts) || ($parts['scheme'] ?? '') !== 'http' || empty($parts['host'])) {
            return null;
        }
        $host = trim($parts['host'], '[]');
        if (!self::privateHost($host)) {
            return null;
        }
        $path = $parts['path'] ?? '';
        return new self($host, (int) ($parts['port'] ?? 80), $path === '' || $path === '/' ? '/v1/traces' : $path, max(0.001, $timeout));
    }

    private static function privateHost(string $host): bool
    {
        if (filter_var($host, FILTER_VALIDATE_IP)) {
            // Private and reserved ranges (loopback included) fail this filter.
            return filter_var($host, FILTER_VALIDATE_IP, FILTER_FLAG_NO_PRIV_RANGE | FILTER_FLAG_NO_RES_RANGE) === false;
        }
        $host = strtolower($host);
        if ($host === 'localhost' || strpos($host, '.') === false) {
            return true; // single-label names: Docker services, /etc/hosts entries
        }
        foreach (['.localhost', '.local', '.internal', '.lan', '.home.arpa'] as $suffix) {
            if (substr($host, -strlen($suffix)) === $suffix) {
                return true;
            }
        }
        return false;
    }

    public function host(): string
    {
        return $this->host;
    }

    public function port(): int
    {
        return $this->port;
    }

    public function path(): string
    {
        return $this->path;
    }

    public function send(string $json): bool
    {
        $socket = null;
        try {
            $target = strpos($this->host, ':') !== false ? '[' . $this->host . ']' : $this->host;
            $socket = @stream_socket_client("tcp://$target:{$this->port}", $errno, $errstr, $this->timeout);
            if (!$socket) {
                return false;
            }
            $deadline = microtime(true) + $this->timeout;
            stream_set_blocking($socket, false);
            $data = "POST {$this->path} HTTP/1.1\r\nHost: $target:{$this->port}\r\nContent-Type: application/json\r\n"
                . 'Content-Length: ' . strlen($json) . "\r\nConnection: close\r\nUser-Agent: slowpoke-symfony\r\n\r\n" . $json;
            while ($data !== '') {
                $written = @fwrite($socket, $data);
                if ($written === false) {
                    return false;
                }
                $data = (string) substr($data, $written);
                if ($data !== '' && !$this->wait($socket, $deadline, true)) {
                    return false; // agent too slow to read: drop, never hold the worker
                }
            }
            if (!$this->wait($socket, $deadline, false)) {
                return false;
            }
            $status = @fread($socket, 32);
            return is_string($status) && preg_match('#^HTTP/1\.[01] 2\d\d#', $status) === 1;
        } catch (\Throwable $e) {
            return false;
        } finally {
            if (is_resource($socket)) {
                @fclose($socket);
            }
        }
    }

    /** @param resource $socket */
    private function wait($socket, float $deadline, bool $write): bool
    {
        $left = $deadline - microtime(true);
        if ($left <= 0) {
            return false;
        }
        $read = $write ? null : [$socket];
        $writes = $write ? [$socket] : null;
        $except = null;
        return (bool) @stream_select($read, $writes, $except, 0, (int) ($left * 1e6));
    }
}
