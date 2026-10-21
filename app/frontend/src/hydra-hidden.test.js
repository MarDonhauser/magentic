import test from 'node:test';
import assert from 'node:assert/strict';

import { HYDRA_HIDDEN_KEY, createHydraHidden } from './hydra-hidden.js';

function memoryStorage(initial = {}) {
  const data = { ...initial };
  return {
    data,
    getItem: key => (key in data ? data[key] : null),
    setItem: (key, value) => { data[key] = String(value); },
  };
}

test('Hydra hides a session by its id and survives a restart', () => {
  const storage = memoryStorage();
  const hidden = createHydraHidden(storage);
  hidden.hide('session-1');
  assert.equal(hidden.has('session-1'), true);
  assert.equal(hidden.has('session-2'), false);
  assert.equal(createHydraHidden(storage).has('session-1'), true);
});

test('Hydra shows a hidden session again', () => {
  const storage = memoryStorage();
  const hidden = createHydraHidden(storage);
  hidden.hide('session-1');
  hidden.show('session-1');
  assert.equal(hidden.has('session-1'), false);
  assert.deepEqual(JSON.parse(storage.data[HYDRA_HIDDEN_KEY]), []);
});

test('Hydra ignores empty ids and a broken stored entry', () => {
  const hidden = createHydraHidden(memoryStorage({ [HYDRA_HIDDEN_KEY]: '{kaputt' }));
  hidden.hide('');
  hidden.hide(undefined);
  assert.equal(hidden.has(''), false);
  assert.equal(hidden.has(undefined), false);
});

test('Hydra keeps working without storage', () => {
  const hidden = createHydraHidden(null);
  hidden.hide('session-1');
  assert.equal(hidden.has('session-1'), true);
});
