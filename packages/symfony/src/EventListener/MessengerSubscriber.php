<?php

namespace Slowpoke\Symfony\EventListener;

use Slowpoke\Symfony\MessageName;
use Slowpoke\Symfony\TracerProvider;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Symfony\Component\Messenger\Event\WorkerMessageFailedEvent;
use Symfony\Component\Messenger\Event\WorkerMessageHandledEvent;
use Symfony\Component\Messenger\Event\WorkerMessageReceivedEvent;

/**
 * One trace per message a worker handles, sent as soon as it is done: a worker has no response to wait
 * for. Messages handled synchronously dispatch no worker event and stay part of whatever sent them.
 */
class MessengerSubscriber implements EventSubscriberInterface
{
    /** @var TracerProvider */
    private $provider;

    public function __construct(TracerProvider $provider)
    {
        $this->provider = $provider;
    }

    public static function getSubscribedEvents(): array
    {
        return [
            WorkerMessageReceivedEvent::class => 'onReceived',
            WorkerMessageHandledEvent::class => 'onHandled',
            WorkerMessageFailedEvent::class => 'onFailed',
        ];
    }

    public function onReceived(WorkerMessageReceivedEvent $event): void
    {
        try {
            if (($tracer = $this->provider->tracer()) !== null) {
                $tracer->startJob(MessageName::of($event->getEnvelope()->getMessage()), $event->getReceiverName());
            }
        } catch (\Throwable $e) {
            // never let observability break the worker
        }
    }

    public function onHandled(WorkerMessageHandledEvent $event): void
    {
        $this->finish(false);
    }

    public function onFailed(WorkerMessageFailedEvent $event): void
    {
        $this->finish(true);
    }

    private function finish(bool $failed): void
    {
        if (($tracer = $this->provider->tracer()) !== null) {
            $tracer->finishJob($failed);
            $tracer->flush();
        }
    }
}
