import assert from 'node:assert'
import { test } from 'node:test'

import { pageKey, pageNames, projectSlug, titleFor } from '../src/lib/pageTitle.js'

test('the tab says which page, of which project', () => {
  assert.strictEqual(titleFor('/projects/negozio/slow', 'Negozio'), 'Slow things · Negozio · Slowpoke')
  assert.strictEqual(titleFor('/projects/negozio', 'Negozio'), 'Overview · Negozio · Slowpoke')
  assert.strictEqual(titleFor('/projects/negozio/queries/abc123', 'Negozio'), 'Query · Negozio · Slowpoke')
  assert.strictEqual(titleFor('/projects/negozio/hosts/12', 'Negozio'), 'Machines · Negozio · Slowpoke')
  assert.strictEqual(titleFor('/'), 'Projects · Slowpoke')
  assert.strictEqual(titleFor('/login'), 'Log in · Slowpoke')
})

test('a project whose name has not loaded yet leaves the page name alone', () => {
  assert.strictEqual(titleFor('/projects/negozio/missions'), 'Missions · Slowpoke')
})

test('a path nobody planned for still says what this is', () => {
  assert.strictEqual(titleFor('/nonsense/here'), 'Slowpoke')
  assert.strictEqual(pageKey('/projects/x/nonsense'), 'nonsense')
  assert.strictEqual(projectSlug('/tv/negozio'), 'negozio')
})

test('every page of the sidebar has a name', () => {
  for (const key of ['', 'map', 'slow', 'missions', 'deploys', 'hosts', 'metrics', 'jobs', 'settings', 'report']) {
    assert.ok(pageNames[key], `${key} has no name`)
  }
})
