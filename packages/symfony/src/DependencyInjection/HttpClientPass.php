<?php

namespace Slowpoke\Symfony\DependencyInjection;

use Slowpoke\Symfony\HttpClient\TracingHttpClient;
use Symfony\Component\DependencyInjection\Compiler\CompilerPassInterface;
use Symfony\Component\DependencyInjection\ContainerBuilder;
use Symfony\Component\DependencyInjection\Reference;

/**
 * Decorates the client that really sends: http_client.transport where it exists (Symfony 6.3+),
 * which every scoped client is built on, http_client otherwise. On the transport a retried call is
 * one span per attempt, which is what the application waited for. Priority -20: outside the
 * MockHttpClient that framework.http_client.mock_response_factory puts at -10, so tests see it too.
 * Runs before the container resolves decorations.
 */
class HttpClientPass implements CompilerPassInterface
{
    public function process(ContainerBuilder $container): void
    {
        if (!$container->hasParameter('slowpoke.http_client') || !$container->has('slowpoke.tracer_provider')) {
            return;
        }
        $target = $container->has('http_client.transport') ? 'http_client.transport' : ($container->has('http_client') ? 'http_client' : null);
        if ($target === null) {
            return; // FrameworkBundle's client is off or not installed
        }
        $container->register('slowpoke.http_client', TracingHttpClient::class)
            ->setDecoratedService($target, null, -20)
            ->setArguments([new Reference('slowpoke.http_client.inner'), new Reference('slowpoke.tracer_provider')]);
    }
}
