import test from 'node:test';
import assert from 'node:assert/strict';
import { updateView } from './updates.js';
import { Updates, CheckForUpdates } from '../wailsjs/go/main/App.js';

test('only a configured, ready updater offers an update check', () => {
  assert.equal(updateView(null).disabled, true);
  const unavailable = updateView({ enabled: false, reason: 'Entwicklungsbuild' });
  assert.equal(unavailable.disabled, true);
  assert.equal(unavailable.message, 'Entwicklungsbuild');
  const ready = { version: '1.2.3', enabled: true, canCheck: true };
  assert.equal(updateView(ready).disabled, false);
  assert.equal(updateView(ready, true).disabled, true);
  assert.equal(updateView({ ...ready, canCheck: false }).disabled, true);
});

test('an existing native update flow can be brought back to the foreground', () => {
  const state = updateView({ enabled: true, canCheck: true, sessionInProgress: true });
  assert.equal(state.label, 'Update-Fenster öffnen');
  assert.equal(state.disabled, false);
});

test('a failed check explains the failure and allows retry', () => {
  const state = updateView({ enabled: true, canCheck: true }, false, 'Keine Verbindung.');
  assert.equal(state.message, 'Keine Verbindung.');
  assert.equal(state.error, true);
  assert.equal(state.disabled, false);
});

test('a bridge failure does not leave a permanent loading message', () => {
  const state = updateView(null, false, 'Der Update-Status konnte nicht geladen werden.');
  assert.equal(state.version, 'Version nicht verfügbar');
  assert.equal(state.disabled, true);
});

test('the bridge invokes only the local update operations, without paths or URLs', async t => {
  const previous = globalThis.window;
  t.after(() => { globalThis.window = previous; });
  const calls = [];
  globalThis.window = { go: { main: { App: {
    Updates: (...args) => { calls.push(['Updates', ...args]); return { version: '1.2.3' }; },
    CheckForUpdates: (...args) => { calls.push(['CheckForUpdates', ...args]); },
  } } } };
  assert.equal((await Updates()).version, '1.2.3');
  await CheckForUpdates();
  assert.deepEqual(calls, [['Updates'], ['CheckForUpdates']]);
});
