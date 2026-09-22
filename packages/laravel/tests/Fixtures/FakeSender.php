<?php

namespace Slowpoke\Laravel\Tests\Fixtures;

use Slowpoke\Laravel\Sender;

class FakeSender implements Sender
{
    /** @var string[] */
    public $payloads = [];

    public function send(string $json): bool
    {
        $this->payloads[] = $json;
        return true;
    }

    /** Spans of the only trace sent, keyed by kind: [server-or-consumer span, query spans]. */
    public function onlyTrace(): array
    {
        if (count($this->payloads) !== 1) {
            throw new \RuntimeException(count($this->payloads) . ' payloads sent, expected 1');
        }
        $spans = json_decode($this->payloads[0], true)['resourceSpans'][0]['scopeSpans'][0]['spans'];
        $root = array_values(array_filter($spans, function ($s) { return $s['kind'] !== 3; }))[0];
        $queries = array_values(array_filter($spans, function ($s) { return $s['kind'] === 3; }));
        return [$root, $queries];
    }

    public static function attr(array $span, string $key)
    {
        foreach ($span['attributes'] as $kv) {
            if ($kv['key'] === $key) {
                return array_values($kv['value'])[0];
            }
        }
        return null;
    }
}
