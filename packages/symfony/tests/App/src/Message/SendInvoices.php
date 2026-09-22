<?php

namespace App\Message;

class SendInvoices
{
    /** @var bool */
    public $fail;

    public function __construct(bool $fail = false)
    {
        $this->fail = $fail;
    }
}
