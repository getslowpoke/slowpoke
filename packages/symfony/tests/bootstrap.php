<?php

// bin/test keeps one vendor directory per PHP/Symfony combination.
$vendor = getenv('COMPOSER_VENDOR_DIR') ?: 'vendor';
require __DIR__ . '/../' . $vendor . '/autoload.php';

// The test app's container is compiled once per run: never reuse one built from older code.
(new Symfony\Component\Filesystem\Filesystem())->remove(__DIR__ . '/App/var/cache/' . basename($vendor));
