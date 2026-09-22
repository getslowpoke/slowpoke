<?php

namespace Slowpoke\Laravel;

use Illuminate\Contracts\Http\Kernel as HttpKernel;
use Illuminate\Database\Events\QueryExecuted;
use Illuminate\Queue\Events\JobExceptionOccurred;
use Illuminate\Queue\Events\JobFailed;
use Illuminate\Queue\Events\JobProcessed;
use Illuminate\Queue\Events\JobProcessing;
use Illuminate\Support\ServiceProvider;
use Slowpoke\Laravel\Http\TraceRequest;

class SlowpokeServiceProvider extends ServiceProvider
{
    public function register()
    {
        $this->mergeConfigFrom(__DIR__ . '/../config/slowpoke.php', 'slowpoke');
    }

    /** @return void */
    public function boot()
    {
        if ($this->app->runningInConsole()) {
            $this->publishes([__DIR__ . '/../config/slowpoke.php' => $this->app->configPath('slowpoke.php')], 'slowpoke-config');
        }
        try {
            if (!filter_var($this->app['config']->get('slowpoke.enabled', true), FILTER_VALIDATE_BOOLEAN)) {
                return;
            }
            $this->wire();
        } catch (\Throwable $e) {
            // a monitoring package must never stop the application from booting
        }
    }

    private function wire(): void
    {
        $app = $this->app;
        $config = $app['config'];

        $app->bindIf(Sender::class, function () use ($config) {
            return HttpSender::fromUrl((string) $config->get('slowpoke.endpoint'), (float) $config->get('slowpoke.timeout', 0.1)) ?: new NullSender();
        }, true);

        $app->singleton(Tracer::class, function ($app) use ($config) {
            $root = $config->get('slowpoke.code_root') ?: $app->basePath();
            return new Tracer(
                new OriginFinder($root, (int) $config->get('slowpoke.backtrace_limit', 60), [__DIR__]),
                function () use ($app) { return $app->make(Sender::class); },
                [
                    'service' => (string) ($config->get('slowpoke.service') ?: $config->get('app.name') ?: 'laravel'),
                    'max_queries' => (int) $config->get('slowpoke.max_queries', 500),
                    'max_sql_length' => (int) $config->get('slowpoke.max_sql_length', 10000),
                    'max_http_calls' => (int) $config->get('slowpoke.max_http_calls', 200),
                    'agent_endpoint' => (string) $config->get('slowpoke.endpoint'),
                ]
            );
        });
        $tracer = $app->make(Tracer::class);
        $events = $app['events'];

        $events->listen(QueryExecuted::class, function ($event) use ($tracer) {
            $tracer->recordQuery((string) $event->sql, (float) $event->time, (string) $event->connection->getDriverName());
        });

        $this->traceRequests($tracer);
        if (filter_var($config->get('slowpoke.http_client', true), FILTER_VALIDATE_BOOLEAN)) {
            $this->traceHttpClient($tracer);
        }
        if (filter_var($config->get('slowpoke.jobs', true), FILTER_VALIDATE_BOOLEAN)) {
            $this->traceJobs($tracer);
        }
        if (filter_var($config->get('slowpoke.schedule', true), FILTER_VALIDATE_BOOLEAN)) {
            $this->traceSchedule($tracer);
        }
    }

    private function traceRequests(Tracer $tracer): void
    {
        $prepend = function ($kernel) {
            if (method_exists($kernel, 'hasMiddleware') && $kernel->hasMiddleware(TraceRequest::class)) {
                return;
            }
            if (method_exists($kernel, 'prependMiddleware')) {
                $kernel->prependMiddleware(TraceRequest::class);
            }
        };
        if ($this->app->resolved(HttpKernel::class)) {
            $prepend($this->app->make(HttpKernel::class));
        } else {
            $this->app->afterResolving(HttpKernel::class, $prepend);
        }
        // Terminating callbacks run after the response reached the client (fastcgi_finish_request).
        $this->app->terminating(function () use ($tracer) {
            $tracer->flush();
        });
    }

    /**
     * Scheduled commands, by event name: the classes exist from Laravel 5.8 (finished) and later
     * (failed), and listening by name is harmless where one of them does not exist yet.
     */
    private function traceSchedule(Tracer $tracer): void
    {
        $events = $this->app['events'];
        $events->listen('Illuminate\\Console\\Events\\ScheduledTaskStarting', function ($event) use ($tracer) {
            $tracer->startCommand($this->commandName($event));
        });
        $finish = function ($failed) use ($tracer) {
            return function () use ($tracer, $failed) {
                $tracer->finishCommand($failed);
                $tracer->flush(); // nothing is waiting for a response here
            };
        };
        $events->listen('Illuminate\\Console\\Events\\ScheduledTaskFinished', $finish(false));
        $events->listen('Illuminate\\Console\\Events\\ScheduledTaskFailed', $finish(true));
    }

    /**
     * The name a person would recognise: "invoices:close", not the php binary that ran it.
     *
     * @param object $event one of Laravel's ScheduledTask events, whatever version it is
     */
    private function commandName($event): string
    {
        $task = isset($event->task) ? $event->task : null;
        if ($task === null) {
            return 'scheduled task';
        }
        $name = '';
        if (isset($task->description) && is_string($task->description) && $task->description !== '') {
            $name = $task->description;
        } elseif (isset($task->command) && is_string($task->command)) {
            // "'/usr/bin/php8.3' 'artisan' invoices:close > /dev/null"
            $name = (string) preg_replace(['/^.*?artisan.\s*/', '/\s*(>|2>).*$/'], '', $task->command);
        }
        $name = trim($name);

        return $name === '' ? 'scheduled task' : $name;
    }

    /**
     * Outbound calls made with Laravel's HTTP client (the Http facade), by event name: the events
     * exist from Laravel 8, and listening to a class that does not exist is harmless. Calls made
     * with Guzzle or cURL directly send no event and are not seen. The package's own sender uses
     * a raw socket, never this client, and the tracer skips the agent's address anyway.
     */
    private function traceHttpClient(Tracer $tracer): void
    {
        $events = $this->app['events'];
        $describe = function ($request): array {
            // Illuminate\Http\Client\Request: method() and url() exist in every version with events.
            return [(string) $request->method(), (string) $request->url()];
        };
        $events->listen('Illuminate\\Http\\Client\\Events\\RequestSending', function ($event) use ($tracer, $describe) {
            try {
                [$method, $url] = $describe($event->request);
                $tracer->startHttpCall(spl_object_id($event->request), $method, $url);
            } catch (\Throwable $e) {
            }
        });
        $events->listen('Illuminate\\Http\\Client\\Events\\ResponseReceived', function ($event) use ($tracer, $describe) {
            try {
                [$method, $url] = $describe($event->request);
                $tracer->finishHttpCall(spl_object_id($event->request), (int) $event->response->status(), $method, $url);
            } catch (\Throwable $e) {
            }
        });
        $events->listen('Illuminate\\Http\\Client\\Events\\ConnectionFailed', function ($event) use ($tracer, $describe) {
            try {
                [$method, $url] = $describe($event->request);
                $tracer->finishHttpCall(spl_object_id($event->request), null, $method, $url);
            } catch (\Throwable $e) {
            }
        });
    }

    private function traceJobs(Tracer $tracer): void
    {
        $events = $this->app['events'];
        $events->listen(JobProcessing::class, function ($event) use ($tracer) {
            if ($event->connectionName === 'sync') {
                return; // runs inside whatever dispatched it
            }
            $payload = method_exists($event->job, 'payload') ? $event->job->payload() : null;
            $tracer->startJob(JobName::of((string) $event->job->resolveName(), $payload), (string) $event->job->getQueue());
        });
        $finish = function ($failed) use ($tracer) {
            return function ($event) use ($tracer, $failed) {
                if ($event->connectionName === 'sync') {
                    return;
                }
                $tracer->finishJob($failed);
                // A worker has no response to wait for: send now.
                $tracer->flush();
            };
        };
        $events->listen(JobProcessed::class, $finish(false));
        $events->listen(JobFailed::class, $finish(true));
        $events->listen(JobExceptionOccurred::class, $finish(true));
    }
}
