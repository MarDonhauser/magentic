import test from 'node:test';
import assert from 'node:assert/strict';

import { createVendorSwitchCoordinator } from './vendor-switch.js';

function deferred() {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
}

const request = {
  sessionId: 'session-id',
  sessionName: 'Build',
  sourceVendor: 'codex',
  targetVendor: 'claude',
};

test('provider switch waits for the history decision and exposes progress', async () => {
  const choice = deferred();
  const states = [];
  const calls = [];
  const coordinator = createVendorSwitchCoordinator({
    chooseContext: () => choice.promise,
    switchVendor: async (...args) => { calls.push(args); },
    reconnect: async () => {},
    onChange: state => { states.push(state.kind); },
  });

  const running = coordinator.request(request);
  assert.equal(coordinator.snapshot().kind, 'confirming');
  assert.deepEqual(calls, []);

  choice.resolve('with-history');
  assert.deepEqual(await running, { ok: true, mode: 'with-history' });
  assert.deepEqual(calls, [['session-id', 'claude', true]]);
  assert.deepEqual(states, ['confirming', 'switching', 'reconnecting', 'complete']);
});

test('provider switch can continue without history and reconnects after failure', async () => {
  const reconnects = [];
  const coordinator = createVendorSwitchCoordinator({
    chooseContext: async () => 'without-history',
    switchVendor: async (_id, _vendor, includeHistory) => {
      assert.equal(includeHistory, false);
      throw new Error('start failed');
    },
    reconnect: async (_request, outcome) => { reconnects.push(outcome); },
  });

  const result = await coordinator.request(request);

  assert.equal(result.ok, false);
  assert.match(result.error.message, /start failed/);
  assert.deepEqual(reconnects, [{ switched: false }]);
  assert.equal(coordinator.snapshot().kind, 'error');
});

test('cancelling the history question leaves the running provider untouched', async () => {
  let switches = 0;
  let disconnects = 0;
  const coordinator = createVendorSwitchCoordinator({
    chooseContext: async () => null,
    switchVendor: async () => { switches += 1; },
    disconnect: () => { disconnects += 1; },
  });

  assert.deepEqual(await coordinator.request(request), { ok: false, cancelled: true });
  assert.equal(switches, 0);
  assert.equal(disconnects, 0);
  assert.equal(coordinator.snapshot().kind, 'idle');
});
