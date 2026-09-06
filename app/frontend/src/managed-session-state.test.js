import test from 'node:test';
import assert from 'node:assert/strict';

import {
  applyManagedSessionState,
  emptyManagedSessionState,
  managedControlModel,
} from './managed-session-state.js';

test('Eine offene Permission zeigt die konkrete Frage sowie Erlauben und Ablehnen', () => {
  const state = applyManagedSessionState({
    availability: 'available',
    turnRunning: true,
    permission: { id: 'permission-1', asked: 'Darf npm publish ausgeführt werden?', open: true },
  });
  const model = managedControlModel(state);
  assert.equal(model.tone, 'permission');
  assert.equal(model.detail, 'Darf npm publish ausgeführt werden?');
  assert.deepEqual(model.actions.map(action => action.kind), ['deny-permission', 'allow-permission']);
});

test('Ein laufender Turn kann unterbrochen werden, ohne die Session zu beenden', () => {
  const model = managedControlModel(applyManagedSessionState({
    availability: 'available',
    turnRunning: true,
  }));
  assert.deepEqual(model.actions.map(action => action.kind), ['interrupt-turn']);
});

test('Eine geschlossene Permission bietet keine zweite Entscheidung an', () => {
  const model = managedControlModel(applyManagedSessionState({
    availability: 'available',
    permission: { id: 'permission-1', asked: 'Frage', open: false, outcome: 'allowed' },
  }));
  assert.equal(model.visible, false);
  assert.deepEqual(model.actions, []);
});

test('Eine unerreichbare Agent-Steuerung nennt ihren Grund und bietet keine Aktion an', () => {
  const model = managedControlModel(applyManagedSessionState({
    availability: 'unavailable',
    reason: 'Agent-Host nicht erreichbar',
  }));
  assert.equal(model.tone, 'unavailable');
  assert.match(model.detail, /nicht erreichbar/);
  assert.deepEqual(model.actions, []);
});

test('Eine tmux-Session erhält keine Managed-Steuerung', () => {
  assert.equal(managedControlModel(emptyManagedSessionState()).visible, false);
});
