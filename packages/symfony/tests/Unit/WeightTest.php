<?php

namespace Slowpoke\Symfony\Tests\Unit;

use PHPUnit\Framework\TestCase;

/**
 * This bundle runs inside other people's applications. Weight is a promise, so it is a test: the
 * day somebody reaches for a helper library, or ships the test suite to Packagist, this fails.
 */
class WeightTest extends TestCase
{
    /** @return array<string, mixed> */
    private function composer(): array
    {
        $json = json_decode((string) file_get_contents(dirname(__DIR__, 2) . '/composer.json'), true);
        self::assertIsArray($json);

        return $json;
    }

    public function testItInstallsNothingOfItsOwn(): void
    {
        foreach (array_keys($this->composer()['require']) as $package) {
            $this->assertTrue(
                $package === 'php' || str_starts_with($package, 'symfony/'),
                "$package would be installed in every application that uses this bundle"
            );
        }
    }

    public function testAnInstalledCopyIsSourceAndDocumentationOnly(): void
    {
        $ignored = file_get_contents(dirname(__DIR__, 2) . '/.gitattributes');
        foreach (['/tests', '/docker', '/bin', '/phpunit.xml', '/phpstan.neon.dist'] as $left) {
            $this->assertStringContainsString("$left ", (string) $ignored, "$left ends up in installed copies");
        }
    }

    public function testItIsSmallEnoughToRead(): void
    {
        $lines = 0;
        $files = new \RecursiveIteratorIterator(new \RecursiveDirectoryIterator(dirname(__DIR__, 2) . '/src'));
        foreach ($files as $file) {
            if ($file->isFile() && $file->getExtension() === 'php') {
                $lines += count(file($file->getPathname()) ?: []);
            }
        }
        // Room to grow, and a wall before it becomes a library nobody reads.
        $this->assertLessThan(1800, $lines, "$lines lines in src/, the limit is 1800");
    }

    public function testItSaysWhichPhpAndWhichLicence(): void
    {
        $composer = $this->composer();
        $this->assertStringStartsWith('^7.4', $composer['require']['php'], 'the oldest PHP is part of the contract');
        $this->assertSame('MIT', $composer['license']);
    }
}
