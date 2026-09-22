<?php

namespace App\Controller;

use Symfony\Component\HttpFoundation\JsonResponse;
use Symfony\Component\HttpFoundation\Response;
use Symfony\Contracts\HttpClient\HttpClientInterface;

/** Outbound calls, each tagged "// @query <name>" so that the tests look its line up. */
class PaymentController
{
    /** @var HttpClientInterface */
    private $http;

    public function __construct(HttpClientInterface $http)
    {
        $this->http = $http;
    }

    public function charge(): Response
    {
        $response = $this->http->request('POST', 'https://API.Stripe.com/v1/charges?customer=cus_secret', ['auth_bearer' => 'sk_live_secret_token', 'json' => ['amount' => 4200]]); // @query charge
        return new JsonResponse($response->toArray());
    }

    public function flaky(): Response
    {
        return new Response((string) $this->http->request('GET', 'https://flaky.example:8443/status')->getStatusCode()); // @query flaky
    }

    public function down(): Response
    {
        try {
            $this->http->request('GET', 'https://down.example/v1/ping?token=zzz')->getContent(); // @query down
        } catch (\Throwable $e) {
            return new Response('failed');
        }
        return new Response('ok');
    }

    /** Concurrent calls, read the way Symfony reads them: one stream() over all of them. */
    public function lazy(): Response
    {
        $responses = [];
        for ($i = 0; $i < 3; $i++) {
            $responses[] = $this->http->request('GET', 'https://partner.example/feed/' . $i); // @query lazy
        }
        foreach ($this->http->stream($responses) as $response => $chunk) {
            // streaming, the way concurrent requests are read
        }
        return new Response('streamed');
    }

    /** Fire and forget: the response is dropped unread, its destructor still waits for the headers. */
    public function dropped(): Response
    {
        $this->http->request('POST', 'https://hooks.example/notify'); // @query dropped
        return new Response('sent');
    }

    public function many(): Response
    {
        for ($i = 0; $i < 5; $i++) {
            $this->http->request('GET', 'https://partner.example/item/' . $i)->getStatusCode();
        }
        return new Response('ok');
    }
}
