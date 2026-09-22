<?php

namespace Slowpoke\Symfony\DependencyInjection;

use Symfony\Component\Config\Definition\Builder\ArrayNodeDefinition;
use Symfony\Component\Config\Definition\Builder\NodeBuilder;
use Symfony\Component\Config\Definition\Builder\TreeBuilder;
use Symfony\Component\Config\Definition\ConfigurationInterface;

/**
 * Every value is a plain scalar, cast at runtime by TracerProvider: an unset or malformed environment
 * variable falls back to the default there, it never fails the container like %env(int:...)% would.
 * Variable nodes: Symfony 5.4 types %env(default::...)% as possibly an array, which scalar nodes refuse.
 */
class Configuration implements ConfigurationInterface
{
    public function getConfigTreeBuilder(): TreeBuilder
    {
        $tree = new TreeBuilder('slowpoke');
        /** @var ArrayNodeDefinition $root the root of a TreeBuilder always is one */
        $root = $tree->getRootNode();
        /** @var NodeBuilder $children */
        $children = $root->children();
        $env = ['enabled', 'endpoint', 'service', 'timeout', 'max_queries', 'http_client', 'max_http_calls'];
        foreach ($env as $name) {
            // SLOWPOKE_ENABLED, SLOWPOKE_OTLP_ENDPOINT, ... read at runtime, defaults in PHP.
            $variable = $name === 'endpoint' ? 'SLOWPOKE_OTLP_ENDPOINT' : 'SLOWPOKE_' . strtoupper($name);
            $children->variableNode($name)->defaultValue('%env(default::' . $variable . ')%')->end();
        }
        $children->variableNode('max_sql_length')->defaultValue(10000)->end();
        $children->variableNode('backtrace_limit')->defaultValue(60)->end();
        $children->booleanNode('messenger')->defaultTrue()->end();
        $children->booleanNode('commands')->defaultTrue()->end();
        // Commands that never end: a worker traced as one command would hold a trace open for
        // hours. Names given here are added to the ones the bundle already knows.
        /** @var ArrayNodeDefinition $skip */
        $skip = $children->arrayNode('skip_commands');
        $skip->scalarPrototype();
        $skip->defaultValue([]);
        $children->scalarNode('code_root')->defaultValue('%kernel.project_dir%')->end();

        return $tree;
    }
}
