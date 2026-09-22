<?php

namespace Slowpoke\Laravel\Http;

use Closure;
use Slowpoke\Laravel\Tracer;

/**
 * Prepended to the global middleware: it wraps everything else, so the timing and the status are
 * those of the response the client receives. The trace is sent later, from a terminating callback.
 */
class TraceRequest
{
    /** @var Tracer */
    private $tracer;

    public function __construct(Tracer $tracer)
    {
        $this->tracer = $tracer;
    }

    /**
     * @param \Illuminate\Http\Request|mixed $request
     * @return mixed
     */
    public function handle($request, Closure $next)
    {
        $started = $request->server('REQUEST_TIME_FLOAT');
        $this->tracer->startRequest($request->getMethod(), is_numeric($started) ? (float) $started : null);

        $response = $next($request);

        try {
            $route = $request->route();
            $uri = is_object($route) && method_exists($route, 'uri') ? $route->uri() : null;
            $status = is_object($response) && method_exists($response, 'getStatusCode') ? (int) $response->getStatusCode() : 200;
            $this->tracer->finishRequest($uri, $request->getPathInfo(), $status, $request->getHost());
        } catch (\Throwable $e) {
            $this->tracer->reset();
        }
        return $response;
    }
}
