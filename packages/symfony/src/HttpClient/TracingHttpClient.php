<?php

namespace Slowpoke\Symfony\HttpClient;

use Slowpoke\Symfony\Tracer;
use Slowpoke\Symfony\TracerProvider;
use Symfony\Component\HttpClient\AsyncDecoratorTrait;
use Symfony\Component\HttpClient\Response\AsyncContext;
use Symfony\Component\HttpClient\Response\AsyncResponse;
use Symfony\Contracts\HttpClient\ChunkInterface;
use Symfony\Contracts\HttpClient\HttpClientInterface;
use Symfony\Contracts\HttpClient\ResponseInterface;
use Symfony\Contracts\HttpClient\ResponseStreamInterface;

/**
 * Records each outbound call as a span of the trace that is open: method, host, status, how long the
 * application waited, the line that made it. Never the URL path, the query string, headers or bodies.
 *
 * Symfony responses are lazy: request() returns at once and the call ends whenever the application
 * reads the response, streams it, or drops it (the destructor still waits for the headers). Instead
 * of wrapping every way of reading a response, this is an async decorator, like Symfony's own
 * RetryableHttpClient: every chunk of the response passes through one callback, however the
 * application consumes it, so the last chunk or the error chunk marks the end exactly once. The end
 * time is the client's own measure (start_time + total_time) when it has one. A response dropped
 * before its last chunk ends when it is destroyed, with the status it had; one still open when the
 * trace ends is closed there as an error by the tracer.
 */
class TracingHttpClient implements HttpClientInterface
{
    use AsyncDecoratorTrait {
        stream as private asyncStream;
    }

    /** @var TracerProvider */
    private $provider;
    /** @var int */
    private $calls = 0;
    /** @var bool|null whether responses are wrapped: decided once, so that stream() never sees a mix */
    private $tracing;

    public function __construct(HttpClientInterface $client, TracerProvider $provider)
    {
        $this->client = $client;
        $this->provider = $provider;
    }

    /** @param array<string, mixed> $options */
    public function request(string $method, string $url, array $options = []): ResponseInterface
    {
        $tracer = $this->tracer();
        if ($tracer === null) {
            return $this->client->request($method, $url, $options);
        }
        $call = null;
        try {
            $call = new HttpCall($tracer, ++$this->calls, $method, self::absolute($url, $options));
        } catch (\Throwable $e) {
            // never let observability break the call the application is making
        }
        if ($call === null) {
            return new AsyncResponse($this->client, $method, $url, $options);
        }
        $passthru = static function (ChunkInterface $chunk, AsyncContext $context) use ($call): \Generator {
            $call->observe($chunk, $context);
            yield $chunk;
        };
        try {
            return new AsyncResponse($this->client, $method, $url, $options, $passthru);
        } catch (\Throwable $e) {
            $call->end(null, null); // invalid options, a URL the client refuses: the call failed
            throw $e;
        }
    }

    /**
     * @param ResponseInterface|iterable<ResponseInterface> $responses
     */
    public function stream($responses, ?float $timeout = null): ResponseStreamInterface
    {
        return $this->tracer() !== null ? $this->asyncStream($responses, $timeout) : $this->client->stream($responses, $timeout);
    }

    private function tracer(): ?Tracer
    {
        if ($this->tracing === null) {
            try {
                $this->tracing = $this->provider->httpTracer() !== null;
            } catch (\Throwable $e) {
                $this->tracing = false;
            }
        }
        return $this->tracing ? $this->provider->httpTracer() : null;
    }

    /**
     * A relative URL (a scoped client resolves it later) only names its host through base_uri.
     *
     * @param array<string, mixed> $options
     */
    private static function absolute(string $url, array $options): string
    {
        if (strpos($url, '://') === false && isset($options['base_uri']) && is_string($options['base_uri'])) {
            return $options['base_uri'];
        }
        return $url;
    }
}
