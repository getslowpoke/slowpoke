<?php

namespace Slowpoke\Symfony\HttpClient;

use Slowpoke\Symfony\Tracer;
use Symfony\Component\HttpClient\Response\AsyncContext;
use Symfony\Contracts\HttpClient\ChunkInterface;

/**
 * One outbound call, from request() to its last chunk, its error, or the destruction of its response.
 * Only the tracer keeps the URL's host; this object holds the URL just long enough to hand it over.
 *
 * @internal
 */
final class HttpCall
{
    /** @var Tracer */
    private $tracer;
    /** @var int */
    private $key;
    /** @var string */
    private $method;
    /** @var string */
    private $url;
    /** @var int|null */
    private $status;
    /** @var bool */
    private $done = false;

    public function __construct(Tracer $tracer, int $key, string $method, string $url)
    {
        $this->tracer = $tracer;
        $this->key = $key;
        $this->method = $method;
        $this->url = $url;
        $tracer->startHttpCall($key, $method, $url);
    }

    public function observe(ChunkInterface $chunk, AsyncContext $context): void
    {
        if ($this->done) {
            return;
        }
        try {
            // getError() and isLast() on a healthy chunk never throw; isLast() on an error chunk
            // would, and would also mark the error as seen: the application must still get it.
            if ($chunk->getError() !== null) {
                $this->end(null, $context->getInfo());
                return;
            }
            if ($this->status === null && $chunk->isFirst()) {
                $this->status = (int) $context->getStatusCode();
            }
            if ($chunk->isLast()) {
                $this->end((int) $context->getStatusCode(), $context->getInfo());
            }
        } catch (\Throwable $e) {
            // never let observability break the call the application is making
        }
    }

    /**
     * @param mixed $info the response's getInfo(), for the time the client measured
     */
    public function end(?int $status, $info): void
    {
        if ($this->done) {
            return;
        }
        $this->done = true;
        try {
            $end = null;
            if (is_array($info) && isset($info['start_time'], $info['total_time']) && $info['total_time'] > 0) {
                $end = (float) $info['start_time'] + (float) $info['total_time'];
            }
            $this->tracer->finishHttpCall($this->key, $status !== null && $status > 0 ? $status : null, $this->method, $this->url, $end);
        } catch (\Throwable $e) {
            // never let observability break the call the application is making
        }
    }

    /** A response dropped before its last chunk: it ended here, with whatever status it had. */
    public function __destruct()
    {
        $this->end($this->status, null);
    }
}
