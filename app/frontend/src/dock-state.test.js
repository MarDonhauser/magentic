import test from 'node:test';
import assert from 'node:assert/strict';

import { dockRefKey, normalizeDockState, resolveLegacyDockRefs } from './dock-state.js';

test('Dock state keeps stable IDs and recognizes the legacy active name', () => {
  const state = normalizeDockState({
    open: true,
    height: 310,
    tabs: [{ id: 'session-a', name: 'term alpha' }, 'term legacy', { id: 'session-a', name: 'renamed label' }],
    active: 'term legacy',
  }, 280);

  assert.deepEqual(state, {
    open: true,
    height: 310,
    tabs: [{ id: 'session-a', name: 'term alpha' }, { id: '', name: 'term legacy' }],
    active: 'legacy:term legacy',
  });
  assert.equal(dockRefKey(state.tabs[0]), 'session:session-a');
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
