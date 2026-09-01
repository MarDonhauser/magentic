import test from 'node:test';
import assert from 'node:assert/strict';

import { dockRefKey, normalizeDockState, resolveLegacyDockRefs } from './dock-state.js';

test('Dock state builds a single-leaf layout from legacy flat tabs and keeps the active tab', () => {
  const state = normalizeDockState({
    open: true,
    height: 310,
    tabs: [{ id: 'session-a', name: 'term alpha' }, 'term legacy', { id: 'session-a', name: 'renamed label' }],
    active: 'term legacy',
  }, 280);

  assert.equal(state.open, true);
  assert.equal(state.height, 310);
  assert.equal(state.layout.type, 'leaf');
  assert.deepEqual(state.layout.tabs, [{ id: 'session-a', name: 'term alpha' }, { id: '', name: 'term legacy' }]);
  assert.equal(state.layout.activeKey, 'legacy:term legacy');
  assert.equal(dockRefKey(state.layout.tabs[0]), 'session:session-a');
});

test('Dock state prefers a persisted split layout over legacy flat tabs', () => {
  const state = normalizeDockState({
    open: false,
    height: 200,
    layout: {
      type: 'split', dir: 'row', ratio: 0.6,
      a: { type: 'leaf', tabs: [{ id: 'a', name: 'A' }], active: 'session:a' },
      b: { type: 'leaf', tabs: [{ id: 'b', name: 'B' }], active: 'session:b' },
    },
    tabs: [{ id: 'ignored', name: 'ignored' }],
  }, 280);

  assert.equal(state.layout.type, 'split');
  assert.equal(state.layout.ratio, 0.6);
  assert.deepEqual(state.layout.a.tabs, [{ id: 'a', name: 'A' }]);
});

test('Legacy Dock names migrate once and unknown names do not remain targets', () => {
  const migrated = resolveLegacyDockRefs(
    ['term known', 'term removed', { id: 'session-current', name: 'term current' }],
    [{ id: 'session-known', name: 'term known' }],
  );

  assert.deepEqual(migrated, [
    { id: 'session-known', name: 'term known' },
    { id: 'session-current', name: 'term current' },
  ]);
});
