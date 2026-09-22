<?php

namespace Slowpoke\Laravel\Tests\Unit;

use PHPUnit\Framework\TestCase;
use Slowpoke\Laravel\OriginFinder;

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
            ['/opt/slowpoke/src/SlowpokeServiceProvider.php', 60],
            ['/var/www/app/vendor/laravel/framework/src/Illuminate/Events/Dispatcher.php', 441],
            null,
            ['/var/www/app/vendor/laravel/framework/src/Illuminate/Database/Connection.php', 816],
            ['/var/www/app/vendor/laravel/framework/src/Illuminate/Database/Eloquent/Model.php', 2334],
            ['/var/www/app/app/Http/Controllers/OrderController.php', 21],
            ['/var/www/app/routes/web.php', 10],
        ]));
        $this->assertSame(['app/Http/Controllers/OrderController.php', 21], $origin);
    }

    public function testNoApplicationFrame(): void
    {
        $finder = new OriginFinder('/var/www/app', 40, []);
        $this->assertNull($finder->fromFrames($this->frames([
            ['/var/www/app/vendor/laravel/framework/src/Illuminate/Database/Connection.php', 816],
            ['/var/www/app/artisan', 13],
        ])));
    }

    public function testFrontControllerIsNotAnOrigin(): void
    {
        $finder = new OriginFinder('/var/www/app', 40, []);
        $this->assertNull($finder->fromFrames($this->frames([
            ['/var/www/app/vendor/laravel/framework/src/Illuminate/Pipeline/Pipeline.php', 816],
            ['/var/www/app/public/index.php', 52],
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

    public function testCompiledBladeViewPointsAtTheTemplate(): void
    {
        $dir = sys_get_temp_dir() . '/slowpoke-' . bin2hex(random_bytes(4)) . '/storage/framework/views';
        mkdir($dir, 0777, true);
        $compiled = $dir . '/0f3c2a.php';
        file_put_contents($compiled, str_repeat("<?php echo 1; ?>\n", 100)
            . '<?php /**PATH /var/www/app/resources/views/orders/index.blade.php ENDPATH**/ ?>');

        $finder = new OriginFinder('/var/www/app', 40, []);
        $this->assertSame(['resources/views/orders/index.blade.php', null], $finder->fromFrames($this->frames([
            ['/var/www/app/vendor/laravel/framework/src/Illuminate/Database/Eloquent/Model.php', 2334],
            [$compiled, 12],
        ])));
    }

    public function testLiveBacktraceFindsTheCaller(): void
    {
        $finder = new OriginFinder(dirname(__DIR__, 2), 40, [dirname(__DIR__, 2) . '/src']);
        $origin = $finder->find();
        $this->assertSame('tests/Unit/OriginFinderTest.php', $origin[0]);
        $this->assertSame(__LINE__ - 2, $origin[1]);
    }
}
