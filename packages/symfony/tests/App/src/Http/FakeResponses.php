<?php

namespace App\Http;

use Symfony\Component\HttpClient\Response\MockResponse;

/** What the outside world answers in the test app (framework.http_client.mock_response_factory). */
class FakeResponses
{
    public function __invoke(string $method, string $url, array $options = []): MockResponse
    {
        $host = (string) parse_url($url, PHP_URL_HOST);
        switch ($host) {
            case 'flaky.example':
                return new MockResponse('bad gateway', ['http_code' => 502]);
            case 'down.example':
                return new MockResponse('', ['error' => 'Could not resolve host: down.example']);
            default:
                return new MockResponse('{"id":"ch_1"}', ['http_code' => 200, 'response_headers' => ['content-type' => 'application/json']]);
        }
    }
}
