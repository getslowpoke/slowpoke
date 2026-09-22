# syntax=docker/dockerfile:1
FROM php:8.3-fpm-alpine

RUN docker-php-ext-install pdo_mysql opcache

COPY --from=composer:2 /usr/bin/composer /usr/bin/composer
# The slowpoke/laravel package from this repository (build context "slowpoke" in compose.yaml):
# it replaces opentelemetry-auto-laravel, whose query origins point into vendor/.
COPY --from=slowpoke --exclude=vendor . /opt/slowpoke-laravel
WORKDIR /var/www
RUN composer create-project laravel/laravel app --no-interaction --prefer-dist --no-progress \
 && cd app \
 && composer config repositories.slowpoke '{"type": "path", "url": "/opt/slowpoke-laravel", "options": {"symlink": false}}' \
 && composer require --no-interaction --no-progress slowpoke/laravel:@dev

RUN mkdir -p /logs/php /logs/nginx
COPY src/ /var/www/app/
COPY fpm.conf /usr/local/etc/php-fpm.d/zz-slowpoke.conf
RUN chown -R www-data:www-data /var/www/app/storage /var/www/app/bootstrap/cache
