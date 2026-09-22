<?php

namespace Slowpoke\Symfony\EventListener;

use Slowpoke\Symfony\Routing\RouteTemplates;
use Slowpoke\Symfony\TracerProvider;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Symfony\Component\HttpKernel\Event\RequestEvent;
use Symfony\Component\HttpKernel\Event\ResponseEvent;
use Symfony\Component\HttpKernel\Event\TerminateEvent;
use Symfony\Component\HttpKernel\KernelEvents;

/**
 * One trace per main request: started before anything else runs, closed when the response is final,
 * sent on kernel.terminate, which under php-fpm runs after Response::send() called
 * fastcgi_finish_request(): the client already has its response. Sub-requests belong to the main one.
 */
class RequestSubscriber implements EventSubscriberInterface
{
    /** @var TracerProvider */
    private $provider;
    /** @var RouteTemplates|null */
    private $routes;

    public function __construct(TracerProvider $provider, ?RouteTemplates $routes = null)
    {
        $this->provider = $provider;
        $this->routes = $routes;
    }

    public static function getSubscribedEvents(): array
    {
        return [
            KernelEvents::REQUEST => ['onRequest', 4096],
            KernelEvents::RESPONSE => ['onResponse', -4096],
            KernelEvents::TERMINATE => ['onTerminate', -4096],
        ];
    }

    public function onRequest(RequestEvent $event): void
    {
        if (!$event->isMainRequest()) {
            return;
        }
        try {
            $tracer = $this->provider->tracer();
            if ($tracer !== null) {
                $request = $event->getRequest();
                $started = $request->server->get('REQUEST_TIME_FLOAT');
                $tracer->startRequest($request->getMethod(), is_numeric($started) ? (float) $started : null);
            }
        } catch (\Throwable $e) {
            // never let observability break the request
        }
    }

    public function onResponse(ResponseEvent $event): void
    {
        if (!$event->isMainRequest() || ($tracer = $this->provider->tracer()) === null) {
            return;
        }
        try {
            $request = $event->getRequest();
            $name = $request->attributes->get('_route');
            $route = is_string($name) && $this->routes !== null ? $this->routes->template($name) : null;
            $tracer->finishRequest($route, $request->getPathInfo(), $event->getResponse()->getStatusCode(), $request->getHost());
        } catch (\Throwable $e) {
            $tracer->reset();
        }
    }

    public function onTerminate(TerminateEvent $event): void
    {
        if (($tracer = $this->provider->tracer()) !== null) {
            $tracer->flush();
        }
    }
}
