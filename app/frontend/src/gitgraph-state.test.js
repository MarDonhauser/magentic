import test from 'node:test';
import assert from 'node:assert/strict';

import { branchMergeState } from './gitgraph-state.js';

test('unknown merge knowledge is not presented as an unmerged branch', () => {
  assert.equal(branchMergeState({ merged: false, mergedKnown: false }), 'unknown');
  assert.equal(branchMergeState({ merged: false, mergedKnown: true }), 'not-merged');
  assert.equal(branchMergeState({ merged: true, mergedKnown: true }), 'merged');
  assert.equal(branchMergeState({ merged: true, mergedKnown: false }), 'unknown');
  assert.equal(branchMergeState({ merged: true, mergedKnown: false }, true), 'not-applicable');
});
