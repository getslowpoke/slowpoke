<?php

namespace Slowpoke\Laravel;

/**
 * Finds the application line that issued a query. OpenTelemetry's Laravel instrumentation points
 * into vendor/, which tells nobody what to fix: the answer is the first frame of the app's own code.
 */
class OriginFinder
{
    /** @var string */
    private $root;
    /** @var int */
    private $limit;
    /** @var string[] */
    private $skip;
    /** @var array<string, string|null> compiled Blade file => template, bounded */
    private $views = [];

    /** @param string[] $skipDirs */
    public function __construct(string $codeRoot, int $limit, array $skipDirs)
    {
        $this->root = rtrim($codeRoot, '/') . '/';
        $this->limit = max(1, $limit);
        $this->skip = array_map(function ($d) { return rtrim($d, '/') . '/'; }, $skipDirs);
    }

    /** @return array{0: string, 1: int|null}|null */
    public function find(): ?array
    {
        // No arguments: cheaper, and no application values are ever copied.
        return $this->fromFrames(debug_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS, $this->limit));
    }

    /**
     * @param array<int, array<string, mixed>> $frames
     * @return array{0: string, 1: int|null}|null
     */
    public function fromFrames(array $frames): ?array
    {
        foreach ($frames as $frame) {
            if (!isset($frame['file']) || $this->thirdParty($frame['file'])) {
                continue;
            }
            $file = $frame['file'];
            $line = isset($frame['line']) ? (int) $frame['line'] : null;
            if (strpos($file, '/storage/framework/views/') !== false) {
                // A compiled view line means nothing to a person: name the template instead.
                $template = $this->template($file);
                if ($template !== null) {
                    $file = $template;
                    $line = null;
                }
            }
            $relative = $this->relative($file);
            if (strpos($relative, 'public/') === 0 || $relative === 'artisan') {
                continue; // front controllers are on every stack
            }
            return [$relative, $line];
        }
        return null;
    }

    private function thirdParty(string $file): bool
    {
        if (strpos($file, '/vendor/') !== false || strpos($file, '/node_modules/') !== false) {
            return true;
        }
        foreach ($this->skip as $dir) {
            if (strpos($file, $dir) === 0) {
                return true;
            }
        }
        return false;
    }

    private function relative(string $file): string
    {
        return strpos($file, $this->root) === 0 ? substr($file, strlen($this->root)) : $file;
    }

    private function template(string $compiled): ?string
    {
        if (array_key_exists($compiled, $this->views)) {
            return $this->views[$compiled];
        }
        if (count($this->views) >= 256) {
            $this->views = [];
        }
        $template = null;
        // Blade appends /**PATH <template> ENDPATH**/ to every compiled view.
        $size = @filesize($compiled);
        $tail = $size ? @file_get_contents($compiled, false, null, max(0, $size - 1024)) : false;
        if ($tail !== false && preg_match('#/\*\*PATH (.+?) ENDPATH\*\*/#', $tail, $m)) {
            $template = $m[1];
        }
        return $this->views[$compiled] = $template;
    }
}
