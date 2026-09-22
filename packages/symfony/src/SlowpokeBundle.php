<?php

namespace Slowpoke\Symfony;

use Slowpoke\Symfony\DependencyInjection\HttpClientPass;
use Symfony\Component\DependencyInjection\ContainerBuilder;
use Symfony\Component\HttpKernel\Bundle\Bundle;

class SlowpokeBundle extends Bundle
{
    /** @return void */
    public function build(ContainerBuilder $container)
    {
        parent::build($container);
        $container->addCompilerPass(new HttpClientPass());
    }
}
