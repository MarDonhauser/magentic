import test from 'node:test';
import assert from 'node:assert/strict';
import {
  clearNotchState,
  idleNotchState,
  receiveNotchEvent,
  resolveNotchState,
} from './features/notch/state.js';

const event = {
  id: 'permission:session-1:1',
  kind: 'permission',
  title: 'Freigabe benötigt',
  options: [{ id: 'allow', label: 'Erlauben', tone: 'allow' }],
};

test('notch state follows idle, expanded, resolved, idle', () => {
  const idle = idleNotchState();
  const expanded = receiveNotchEvent(idle, event);
  assert.equal(expanded.phase, 'expanded');
  assert.equal(expanded.event, event);

  const resolved = resolveNotchState(expanded, 'allow');
  assert.equal(resolved.phase, 'resolved');
  assert.equal(resolved.resolvedOptionId, 'allow');
  assert.deepEqual(clearNotchState(resolved, event.id), idleNotchState());
});

test('clear for another event does not disturb the active event', () => {
  const expanded = receiveNotchEvent(idleNotchState(), event);
  assert.equal(clearNotchState(expanded, 'another-event'), expanded);
});
