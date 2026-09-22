<?php

namespace Slowpoke\Symfony\Tests\Unit;

use PHPUnit\Framework\TestCase;
use Slowpoke\Symfony\OriginFinder;

class OriginFinderTest extends TestCase
{
    private function frames(array $files): array
    {
        return array_map(function ($f) {
            return $f === null ? ['function' => 'call_user_func'] : ['file' => $f[0], 'line' => $f[1], 'function' => 'x'];
        }, $files);
    }

    public function testFirstApplicationFrameSkippingVendorAndThePackage(): void
    {
        $finder = new OriginFinder('/var/www/app', 40, ['/opt/slowpoke/src']);
        $origin = $finder->fromFrames($this->frames([
            ['/opt/slowpoke/src/Doctrine/TracingStatement.php', 40],
            ['/var/www/app/vendor/doctrine/dbal/src/Connection.php', 1110],
            null,
            ['/var/www/app/vendor/doctrine/orm/src/Persisters/Entity/BasicEntityPersister.php', 750],
            ['/var/www/app/src/Repository/OrderRepository.php', 21],
            ['/var/www/app/src/Controller/OrderController.php', 10],
        ]));
        $this->assertSame(['src/Repository/OrderRepository.php', 21], $origin);
    }

    public function testGeneratedCodeIsSkipped(): void
    {
        // Doctrine proxies and the compiled container live in the cache directory: not code anyone edits.
        $finder = new OriginFinder('/var/www/app', 40, ['/var/www/app/var/cache/prod']);
        $origin = $finder->fromFrames($this->frames([
            ['/var/www/app/vendor/doctrine/orm/src/UnitOfWork.php', 3000],
            ['/var/www/app/var/cache/prod/doctrine/orm/Proxies/__CG__AppEntityCustomer.php', 77],
            ['/var/www/app/src/Service/Invoicing.php', 33],
        ]));
        $this->assertSame(['src/Service/Invoicing.php', 33], $origin);
    }

    public function testNoApplicationFrame(): void
    {
        $finder = new OriginFinder('/var/www/app', 40, []);
        $this->assertNull($finder->fromFrames($this->frames([
            ['/var/www/app/vendor/doctrine/dbal/src/Connection.php', 816],
            ['/var/www/app/bin/console', 13],
        ])));
    }

    public function testFrontControllerIsNotAnOrigin(): void
    {
        $finder = new OriginFinder('/var/www/app', 40, []);
        $this->assertNull($finder->fromFrames($this->frames([
            ['/var/www/app/vendor/symfony/http-kernel/HttpKernel.php', 181],
            ['/var/www/app/vendor/autoload_runtime.php', 29],
            ['/var/www/app/public/index.php', 5],
        ])));
    }

    public function testFileOutsideTheCodeRootStaysAbsolute(): void
    {
        $finder = new OriginFinder('/var/www/app', 40, []);
        $this->assertSame(['/srv/shared/Legacy.php', 7], $finder->fromFrames($this->frames([['/srv/shared/Legacy.php', 7]])));
    }

    public function testCodeRootIsNotMatchedAsAPrefixOfASiblingDirectory(): void
    {
        $finder = new OriginFinder('/var/www/app', 40, []);
        $this->assertSame(['/var/www/app2/Foo.php', 3], $finder->fromFrames($this->frames([['/var/www/app2/Foo.php', 3]])));
    }

    public function testAFrameRunningInsideAnOrdinaryClassIsNotATemplate(): void
    {
        $finder = new OriginFinder('/var/www/app', 40, []);
        $frames = $this->frames([['/var/www/app/src/Controller/OrderController.php', 12], ['/var/www/app/vendor/symfony/http-kernel/HttpKernel.php', 181]]);
        $frames[1]['class'] = self::class;
        $this->assertSame(['src/Controller/OrderController.php', 12], $finder->fromFrames($frames));
    }

    /** Renders a real Twig template that asks the finder where it is called from. */
    private function originInTemplate(string $root, string $templates, string $source): ?array
    {
        mkdir($templates, 0777, true);
        file_put_contents($templates . '/page.html.twig', $source);
        // The Twig function below stands in for Doctrine: this test file is not the app.
        $finder = new OriginFinder($root, 60, [dirname(__DIR__, 2) . '/src', __DIR__]);
        $origin = null;
        $twig = new \Twig\Environment(new \Twig\Loader\FilesystemLoader($templates), ['cache' => $root . '/var/cache/twig']);
        $twig->addFunction(new \Twig\TwigFunction('query', function () use ($finder, &$origin) {
            $origin = $finder->find();
            return '';
        }));
        $twig->render('page.html.twig');
        return $origin;
    }

    public function testCompiledTwigTemplatePointsAtTheTemplateAndItsLine(): void
    {
        $root = sys_get_temp_dir() . '/slowpoke-' . bin2hex(random_bytes(4));
        $origin = $this->originInTemplate($root, $root . '/templates/orders', "<ul>\n{% for i in 1..2 %}\n\n  <li>{{ query() }}</li>\n{% endfor %}\n");
        $this->assertSame(['templates/orders/page.html.twig', 4], $origin);
    }

    public function testATemplateShippedInVendorIsNotAnOrigin(): void
    {
        $root = sys_get_temp_dir() . '/slowpoke-' . bin2hex(random_bytes(4));
        $this->assertNull($this->originInTemplate($root, $root . '/vendor/acme/admin-bundle/templates', '{{ query() }}'));
    }

    public function testLiveBacktraceFindsTheCaller(): void
    {
        $finder = new OriginFinder(dirname(__DIR__, 2), 40, [dirname(__DIR__, 2) . '/src']);
        $origin = $finder->find();
        $this->assertSame('tests/Unit/OriginFinderTest.php', $origin[0]);
        $this->assertSame(__LINE__ - 2, $origin[1]);
    }
}
