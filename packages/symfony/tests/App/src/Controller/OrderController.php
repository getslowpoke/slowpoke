<?php

namespace App\Controller;

use Doctrine\DBAL\Connection;
use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\HttpFoundation\Response;
use Symfony\Component\HttpKernel\Exception\NotFoundHttpException;
use Symfony\Component\HttpKernel\HttpKernelInterface;
use Twig\Environment;

/** Each query carries a marker comment: the tests look up its line instead of hard-coding it. */
class OrderController
{
    /** @var Connection */
    private $db;
    /** @var Environment */
    private $twig;
    /** @var HttpKernelInterface */
    private $kernel;

    public function __construct(Connection $db, Environment $twig, HttpKernelInterface $kernel)
    {
        $this->db = $db;
        $this->twig = $twig;
        $this->kernel = $kernel;
    }

    public function index(): Response
    {
        $orders = $this->db->fetchAllAssociative('SELECT id, customer_id FROM orders WHERE status = ?', ['paid']); // @query list
        $names = [];
        foreach ($orders as $order) {
            $names[] = $this->db->fetchOne('SELECT name FROM customers WHERE id = ?', [$order['customer_id']]); // @query n+1
        }
        return new Response(implode(', ', $names));
    }

    public function report(): Response
    {
        $orders = $this->db->fetchAllAssociative('SELECT id, customer_id FROM orders'); // @query report
        return new Response($this->twig->render('orders/report.html.twig', ['orders' => $orders, 'db' => $this->db]));
    }

    public function show(int $id): Response
    {
        $order = $this->db->fetchAssociative('SELECT id, status FROM orders WHERE id = ?', [$id]); // @query show
        if ($order === false) {
            throw new NotFoundHttpException();
        }
        return new Response($order['status']);
    }

    public function forward(Request $request, int $id): Response
    {
        $sub = $request->duplicate([], null, ['_controller' => [$this, 'show'], 'id' => $id]);
        return $this->kernel->handle($sub, HttpKernelInterface::SUB_REQUEST);
    }

    public function create(): Response
    {
        $this->db->executeStatement('INSERT INTO orders (customer_id, status) VALUES (?, ?)', [1, 'new']); // @query create
        return new Response('', 201);
    }

    public function purge(): Response
    {
        // No parameters: DBAL skips prepare() and goes straight to the driver's exec() and query().
        $this->db->executeStatement("DELETE FROM orders WHERE status = 'cancelled'"); // @query purge
        $left = $this->db->executeQuery('SELECT COUNT(*) FROM orders')->fetchOne(); // @query count
        return new Response((string) $left);
    }

    public function lookup(Request $request): Response
    {
        $id = $this->db->fetchOne('SELECT id FROM customers WHERE email = ?', [(string) $request->query->get('email')]); // @query lookup
        return new Response((string) $id);
    }

    public function broken(): Response
    {
        $this->db->fetchOne('SELECT 1 FROM orders'); // @query broken
        throw new \RuntimeException('secret failure detail');
    }

    public function home(): Response
    {
        return new Response('home');
    }
}
