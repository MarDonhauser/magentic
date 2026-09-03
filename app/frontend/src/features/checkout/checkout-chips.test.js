import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkoutChips } from './checkout-chips.js';

test('clean trunk checkout is clear', () => {
  const chips = checkoutChips({
    checkout: { branch: 'main', clean: true, changesKnown: true, divergenceKnown: true, ahead: 0, behind: 0 },
    mainBranch: 'main',
  });
  assert.deepEqual(chips.map(c => c.key), ['git']);
  assert.equal(chips[0].text, '✅ clear');
  assert.equal(chips[0].tone, 'good');
});

test('dirty checkout counts its changes and reports divergence', () => {
  const chips = checkoutChips({
    checkout: { branch: 'agent/foo', clean: false, changesKnown: true, divergenceKnown: true, staged: 1, modified: 2, untracked: 1, ahead: 3, behind: 1 },
    mainBranch: 'main',
  });
  assert.deepEqual(chips.map(c => c.text), ['📝 uncommitted 4', '⇡ 3', '⇣ 1']);
  assert.deepEqual(chips.map(c => c.tone), ['warn', 'plain', 'plain']);
});

test('clean feature branch is committed, not clear', () => {
  const chips = checkoutChips({
    checkout: { branch: 'agent/foo', clean: true, changesKnown: true },
    mainBranch: 'main',
  });
  assert.equal(chips[0].text, '💾 committed');
  assert.equal(chips[0].tone, 'info');
});

test('project main branch counts as trunk even if it is not called main', () => {
  const chips = checkoutChips({
    checkout: { branch: 'trunk', clean: true, changesKnown: true },
    mainBranch: 'trunk',
  });
  assert.equal(chips[0].text, '✅ clear');
});

test('unknown facts produce no chips instead of invented ones', () => {
  assert.deepEqual(checkoutChips(null), []);
  assert.deepEqual(checkoutChips({}), []);
  assert.deepEqual(checkoutChips({ checkout: { checkoutKnown: false, changesKnown: true, clean: true } }), []);
  assert.deepEqual(checkoutChips({ checkout: { changesKnown: false, divergenceKnown: false } }), []);
});
