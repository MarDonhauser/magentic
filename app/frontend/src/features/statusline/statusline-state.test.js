import { test } from 'node:test';
import assert from 'node:assert/strict';
import { statusLineItems, contextTone } from './statusline-state.js';

const line = { model: 'Fable 5.1', effort: 'xhigh', contextPercent: 10, contextWindow: 1000000, contextTokens: 103276, costUsd: 2.38, version: '2.1.258' };

test('clean trunk checkout with a running agent shows the full line', () => {
  const items = statusLineItems({
    checkout: { branch: 'main', clean: true, changesKnown: true, divergenceKnown: true, ahead: 0, behind: 0 },
    mainBranch: 'main', statusLine: line,
  });
  assert.deepEqual(items.map(i => i.key), ['git', 'context', 'model', 'effort', 'cost']);
  assert.equal(items[0].text, '✅ clear');
  assert.equal(items[1].meter, 10);
  assert.equal(items[1].tone, 'good');
  assert.match(items[1].title, /103k von 1M Tokens/);
  assert.equal(items[2].text, '🤖 Fable 5.1');
  assert.equal(items[3].text, '⚡⚡⚡⚡ xhigh');
  assert.equal(items[4].text, '$2.38');
});

test('dirty checkout counts its changes and reports divergence', () => {
  const items = statusLineItems({
    checkout: { branch: 'agent/foo', clean: false, changesKnown: true, divergenceKnown: true, staged: 1, modified: 2, untracked: 1, ahead: 3, behind: 1 },
    mainBranch: 'main',
  });
  assert.deepEqual(items.map(i => i.text), ['📝 uncommitted 4', '⇡ 3', '⇣ 1']);
  assert.equal(items[0].tone, 'warn');
});

test('clean feature branch is committed, not clear', () => {
  const items = statusLineItems({
    checkout: { branch: 'agent/foo', clean: true, changesKnown: true },
    mainBranch: 'main',
  });
  assert.equal(items[0].text, '💾 committed');
  assert.equal(items[0].tone, 'info');
});

test('project main branch counts as trunk even if it is not called main', () => {
  const items = statusLineItems({
    checkout: { branch: 'trunk', clean: true, changesKnown: true },
    mainBranch: 'trunk',
  });
  assert.equal(items[0].text, '✅ clear');
});

test('unknown facts produce no chips instead of invented ones', () => {
  assert.deepEqual(statusLineItems(null), []);
  assert.deepEqual(statusLineItems({ checkout: { checkoutKnown: false, changesKnown: true, clean: true } }), []);
  assert.deepEqual(statusLineItems({ checkout: { changesKnown: false, divergenceKnown: false } }), []);
});

test('a gone session keeps git chips but drops the stale run facts', () => {
  const items = statusLineItems({
    checkout: { branch: 'main', clean: true, changesKnown: true }, mainBranch: 'main', statusLine: line,
  }, { gone: true });
  assert.deepEqual(items.map(i => i.key), ['git']);
});

test('context tone follows the 60 and 85 percent thresholds', () => {
  assert.equal(contextTone(59), 'good');
  assert.equal(contextTone(60), 'warn');
  assert.equal(contextTone(85), 'bad');
  const full = statusLineItems({ statusLine: { contextPercent: 140 } });
  assert.equal(full[0].meter, 100);
  assert.equal(full[0].text, '🧠 100%');
});

test('fast mode and unknown effort still render sensibly', () => {
  const items = statusLineItems({ statusLine: { model: 'Opus 5', fastMode: true, effort: 'turbo', costUsd: 0 } });
  assert.deepEqual(items.map(i => i.text), ['🧠 0%', '🤖 Opus 5 · fast', '⚡ turbo']);
});
