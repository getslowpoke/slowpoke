<?php

namespace Slowpoke\Laravel\Tests\Fixtures;

use Illuminate\Queue\Jobs\SyncJob;

/** A job as a worker pulls it from a real queue, without running Redis or SQS. */
class QueuedJob extends SyncJob
{
    public function getQueue()
    {
        return $this->queue;
    }
}
