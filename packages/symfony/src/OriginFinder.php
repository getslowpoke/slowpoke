<?php

namespace Slowpoke\Symfony;

/**
 * Copied from slowpoke/laravel, to be extracted into a shared package: keep the two in step.
 *
 * Finds the application line that issued a query. OpenTelemetry's instrumentation points into
 * vendor/, which tells nobody what to fix: the answer is the first frame of the app's own code.
 */
class OriginFinder
{
    /** @var string */
    private $root;
    /** @var int */
    private $limit;
    /** @var string[] */
    private $skip;
    /** @var array<string, array{0: string, 1: array<int, int>}|null> compiled Twig class => template, bounded */
    private $templates = [];

    /** @param string[] $skipDirs this bundle and generated code (the kernel cache: container, proxies) */
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
     * @param array<int, array<string, mixed>> $frames as debug_backtrace() returns them
     * @return array{0: string, 1: int|null}|null
     */
    public function fromFrames(array $frames): ?array
    {
        foreach ($frames as $i => $frame) {
            if (!isset($frame['file']) || $this->thirdParty($frame['file'])) {
                continue;
            }
            $line = isset($frame['line']) ? (int) $frame['line'] : null;
            // The code at this frame runs inside the function named by the next one. A compiled Twig
            // template line means nothing to a person: name the template and its own line instead.
            if (isset($frames[$i + 1]['class']) && strpos($frames[$i + 1]['class'], '__TwigTemplate_') === 0) {
                $template = $this->template($frames[$i + 1]['class']);
                if ($template !== null) {
                    if ($this->thirdParty($template[0])) {
                        continue; // a bundle's own template (an admin panel): keep looking for the app
                    }
                    return [$this->relative($template[0]), $line === null ? null : self::templateLine($template[1], $line)];
                }
            }
            if ($this->generated($frame['file'])) {
                continue;
            }
            $relative = $this->relative($frame['file']);
            if (strpos($relative, 'public/') === 0 || $relative === 'bin/console') {
                continue; // front controllers are on every stack
            }
            return [$relative, $line];
        }
        return null;
    }

    private function thirdParty(string $file): bool
    {
        return strpos($file, '/vendor/') !== false || strpos($file, '/node_modules/') !== false;
    }

    private function generated(string $file): bool
    {
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

    /** @return array{0: string, 1: array<int, int>}|null template path (or name) and PHP line => template line */
    private function template(string $class): ?array
    {
        if (array_key_exists($class, $this->templates)) {
            return $this->templates[$class];
        }
        if (count($this->templates) >= 256) {
            $this->templates = [];
        }
        $template = null;
        try {
            if (is_subclass_of($class, 'Twig\Template')) {
                // Both methods only return constants compiled into the class: no constructor needed.
                $compiled = (new \ReflectionClass($class))->newInstanceWithoutConstructor();
                $source = $compiled->getSourceContext();
                $lines = $compiled->getDebugInfo();
                krsort($lines);
                $template = [$source->getPath() !== '' ? $source->getPath() : $source->getName(), $lines];
            }
        } catch (\Throwable $e) {
            $template = null;
        }
        return $this->templates[$class] = $template;
    }

    /**
     * Twig's own rule (Twig\Error\Error): the template line of the closest compiled line at or above.
     *
     * @param array<int, int|string> $lines compiled line => template line, as Twig's debug info holds them
     */
    private static function templateLine(array $lines, int $line): ?int
    {
        foreach ($lines as $codeLine => $templateLine) {
            if ($codeLine <= $line) {
                return (int) $templateLine;
            }
        }
        return null;
    }
}
