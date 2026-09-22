<?php

namespace Slowpoke\Symfony\Routing;

use Symfony\Component\Config\ConfigCache;
use Symfony\Component\HttpKernel\CacheWarmer\CacheWarmerInterface;

/**
 * Route name => path template (/orders/{id}). The router only knows the matched route's name at
 * runtime, and loading the whole RouteCollection on every request would cost far more than the trace:
 * the map is written once to the cache directory, like the router's own matcher, and refreshed with it.
 */
class RouteTemplates implements CacheWarmerInterface
{
    /** @var object|null Symfony\Component\Routing\RouterInterface */
    private $router;
    /** @var string */
    private $cacheDir;
    /** @var bool */
    private $debug;
    /** @var array<string, string>|null */
    private $map;

    /** @param object|null $router the application router, when it has one */
    public function __construct($router, string $cacheDir, bool $debug)
    {
        $this->router = $router;
        $this->cacheDir = $cacheDir;
        $this->debug = $debug;
    }

    public function template(string $name): ?string
    {
        if ($this->map === null) {
            $this->map = $this->load($this->cacheDir);
        }
        return $this->map[$name] ?? null;
    }

    public function isOptional(): bool
    {
        return true;
    }

    /**
     * @param string $cacheDir
     * @param string|null $buildDir
     * @return string[]
     */
    public function warmUp($cacheDir, $buildDir = null): array
    {
        $this->map = $this->load($cacheDir);
        return [];
    }

    /** @return array<string, string> */
    private function load(string $cacheDir): array
    {
        if ($this->router === null || !method_exists($this->router, 'getRouteCollection')) {
            return [];
        }
        try {
            $cache = new ConfigCache($cacheDir . '/slowpoke/route_templates.php', $this->debug);
            if (!$cache->isFresh()) {
                $collection = $this->router->getRouteCollection();
                $map = [];
                foreach ($collection->all() as $name => $route) {
                    $map[$name] = $route->getPath();
                }
                try {
                    $cache->write('<?php return ' . var_export($map, true) . ";\n", $collection->getResources());
                } catch (\Throwable $e) {
                    return $map; // read-only cache directory: keep it in memory for this process
                }
            }
            $map = require $cache->getPath();
            return is_array($map) ? $map : [];
        } catch (\Throwable $e) {
            return [];
        }
    }
}
