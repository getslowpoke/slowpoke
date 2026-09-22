<?php

namespace App\Command;

use Doctrine\DBAL\Connection;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;

/** The kind of command a server runs from cron every night. */
class CloseInvoicesCommand extends Command
{
    /** @var Connection */
    private $db;

    public function __construct(Connection $db)
    {
        parent::__construct();
        $this->db = $db;
    }

    protected function configure(): void
    {
        $this->setName('app:close-invoices')
            ->setDescription('Closes the invoices of paid orders')
            ->addOption('fail', null, InputOption::VALUE_NONE, 'exit with an error, the way a broken night job does');
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $this->db->fetchAllAssociative('SELECT id FROM orders WHERE status = ?', ['paid']); // @query invoices

        return $input->getOption('fail') ? 1 : 0;
    }
}
