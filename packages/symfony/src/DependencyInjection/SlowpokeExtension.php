<?php

namespace Slowpoke\Symfony\DependencyInjection;

use Slowpoke\Symfony\Doctrine\TracingMiddleware;
use Slowpoke\Symfony\EventListener\ConsoleSubscriber;
use Slowpoke\Symfony\EventListener\MessengerSubscriber;
use Slowpoke\Symfony\EventListener\RequestSubscriber;
use Slowpoke\Symfony\Routing\RouteTemplates;
use Slowpoke\Symfony\Sender;
use Slowpoke\Symfony\TracerProvider;
use Symfony\Component\DependencyInjection\Argument\ServiceClosureArgument;
use Symfony\Component\DependencyInjection\ContainerBuilder;
use Symfony\Component\DependencyInjection\ContainerInterface;
use Symfony\Component\DependencyInjection\Definition;
use Symfony\Component\DependencyInjection\Reference;
use Symfony\Component\HttpKernel\DependencyInjection\Extension;

class SlowpokeExtension extends Extension
{
    /** @param array<int, array<string, mixed>> $configs */
    public function load(array $configs, ContainerBuilder $container): void
    {
        $config = $this->processConfiguration(new Configuration(), $configs);
        // An environment variable is only a placeholder here, neither true nor false: it is read at
        // runtime and the services decide. Only a literal false in the configuration removes them.
        if (filter_var($config['enabled'], FILTER_VALIDATE_BOOLEAN, FILTER_NULL_ON_FAILURE) === false) {
            return;
        }

        $container->register('slowpoke.sender', Sender::class)
            ->setFactory([TracerProvider::class, 'sender'])
            ->setArguments([$config['endpoint'], $config['timeout']]);

        $container->register('slowpoke.tracer_provider', TracerProvider::class)->setArguments([
            [
                'enabled' => $config['enabled'],
                'service' => $config['service'],
                'max_queries' => $config['max_queries'],
                'max_sql_length' => $config['max_sql_length'],
                'http_client' => $config['http_client'],
                'max_http_calls' => $config['max_http_calls'],
                'endpoint' => $config['endpoint'],
                'backtrace_limit' => $config['backtrace_limit'],
                'code_root' => $config['code_root'],
                // Generated code is nobody's line to fix: the container, Doctrine proxies. Twig
                // templates compiled there are resolved to their source before this applies.
                'skip_dirs' => [dirname(__DIR__), '%kernel.cache_dir%', '%kernel.build_dir%'],
            ],
            new ServiceClosureArgument(new Reference('slowpoke.sender')),
        ]);

        $routes = null;
        if (interface_exists('Symfony\Component\Routing\RouterInterface')) {
            $container->register('slowpoke.route_templates', RouteTemplates::class)
                ->setArguments([new Reference('router', ContainerInterface::IGNORE_ON_INVALID_REFERENCE), '%kernel.cache_dir%', '%kernel.debug%'])
                ->addTag('kernel.cache_warmer');
            $routes = new Reference('slowpoke.route_templates');
        }
        $container->register('slowpoke.request_subscriber', RequestSubscriber::class)
            ->setArguments([new Reference('slowpoke.tracer_provider'), $routes])
            ->addTag('kernel.event_subscriber');

        if ($config['messenger'] && class_exists('Symfony\Component\Messenger\Event\WorkerMessageReceivedEvent')) {
            $container->register('slowpoke.messenger_subscriber', MessengerSubscriber::class)
                ->setArguments([new Reference('slowpoke.tracer_provider')])
                ->addTag('kernel.event_subscriber');
        }

        if ($config['commands'] && class_exists('Symfony\Component\Console\ConsoleEvents')) {
            $container->register('slowpoke.console_subscriber', ConsoleSubscriber::class)
                ->setArguments([new Reference('slowpoke.tracer_provider'), $config['skip_commands']])
                ->addTag('kernel.event_subscriber');
        }

        // Outbound calls: the decorator is placed by HttpClientPass, once FrameworkBundle has said
        // which client service exists. An environment variable turning it off is read at runtime.
        if (filter_var($config['http_client'], FILTER_VALIDATE_BOOLEAN, FILTER_NULL_ON_FAILURE) !== false
            && trait_exists('Symfony\Component\HttpClient\AsyncDecoratorTrait')) {
            $container->setParameter('slowpoke.http_client', true);
        }

        if (interface_exists('Doctrine\DBAL\Driver\Middleware')) {
            // DoctrineBundle 2.6+ applies it to every connection.
            $container->setDefinition('slowpoke.doctrine_middleware', (new Definition(TracingMiddleware::class))
                ->setArguments([new Reference('slowpoke.tracer_provider')])
                ->addTag('doctrine.middleware'));
        }
    }
}
