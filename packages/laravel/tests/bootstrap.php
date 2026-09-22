<?php

// bin/test keeps one vendor directory per PHP/Laravel combination.
require __DIR__ . '/../' . (getenv('COMPOSER_VENDOR_DIR') ?: 'vendor') . '/autoload.php';
