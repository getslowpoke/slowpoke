# Test image only: the Mac has no PHP. PHP=7.4 or PHP=8.3.
ARG PHP=8.3
FROM php:${PHP}-cli-alpine
RUN apk add --no-cache unzip git
COPY --from=composer:2 /usr/bin/composer /usr/bin/composer
ENV COMPOSER_ALLOW_SUPERUSER=1 COMPOSER_NO_INTERACTION=1
WORKDIR /repo/packages/symfony
