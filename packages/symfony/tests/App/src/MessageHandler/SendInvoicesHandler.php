<?php

namespace App\MessageHandler;

use App\Message\SendInvoices;
use Doctrine\DBAL\Connection;

class SendInvoicesHandler
{
    /** @var Connection */
    private $db;

    public function __construct(Connection $db)
    {
        $this->db = $db;
    }

    public function __invoke(SendInvoices $message): void
    {
        $this->db->fetchAllAssociative('SELECT id FROM orders WHERE status = ?', ['unbilled']); // @query invoices
        if ($message->fail) {
            throw new \RuntimeException('mail server down');
        }
    }
}
