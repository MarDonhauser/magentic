import test from 'node:test';
import assert from 'node:assert/strict';

import {
  createHandoffCoordinator,
  createHydraHandoff,
  handoffSourceReason,
} from './hydra-handoff.js';

const agent = (id, name, extra = {}) => ({
  id,
  name,
  term: false,
  status: 'idle',
  handoffSource: true,
  handoffTarget: true,
  ...extra,
});

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

test('a stale request cannot overwrite a newer handoff after a forced reset', async () => {
  const first = deferred();
  const second = deferred();
  const requests = [];
  const coordinator = createHandoffCoordinator({
    submit(sourceId, targetId) {
      requests.push([sourceId, targetId]);
      return targetId === 'target-a' ? first.promise : second.promise;
    },
  });
  coordinator.reconcile([
    agent('source', 'Quelle'),
    agent('target-a', 'Ziel A'),
    agent('target-b', 'Ziel B'),
  ]);

  coordinator.arm('source');
  const requestA = coordinator.submitTarget('target-a');
  coordinator.cancel({ force: true });
  coordinator.arm('source');
  const requestB = coordinator.submitTarget('target-b');

  first.resolve();
  assert.deepEqual(await requestA, { ok: false, stale: true });
  assert.equal(coordinator.snapshot().kind, 'submitting');
  assert.equal(coordinator.snapshot().target.id, 'target-b');

  second.resolve();
  assert.deepEqual(await requestB, { ok: true });
  assert.equal(coordinator.snapshot().kind, 'idle');
  assert.equal(coordinator.snapshot().feedback.kind, 'success');
  assert.deepEqual(requests, [['source', 'target-a'], ['source', 'target-b']]);
});

test('leaving and reconciling cannot unlock a request that is still in flight', async () => {
  const running = deferred();
  const requests = [];
  const coordinator = createHandoffCoordinator({
    submit(sourceId, targetId) {
      requests.push([sourceId, targetId]);
      return running.promise;
    },
  });
  coordinator.reconcile([agent('source-a', 'Quelle A'), agent('target-a', 'Ziel A')]);
  coordinator.arm('source-a');
  const request = coordinator.submitTarget('target-a');

  assert.deepEqual(coordinator.leave(), { ok: false, busy: true });
  coordinator.reconcile([agent('source-b', 'Quelle B'), agent('target-b', 'Ziel B')]);
  assert.equal(coordinator.snapshot().kind, 'submitting');
  assert.equal(coordinator.snapshot().source.id, 'source-a');
  assert.deepEqual(await coordinator.activate('source-b'), { ok: false, busy: true });
  assert.deepEqual(requests, [['source-a', 'target-a']]);

  running.resolve();
  assert.deepEqual(await request, { ok: true });
  assert.equal(coordinator.snapshot().kind, 'idle');
  assert.deepEqual(requests, [['source-a', 'target-a']]);
});

test('a failed request keeps its source armed for a one-click retry', async () => {
  const coordinator = createHandoffCoordinator({
    submit: async () => { throw new Error('Ziel nicht bereit'); },
  });
  coordinator.reconcile([agent('source', 'Quelle'), agent('target', 'Ziel')]);
  coordinator.arm('source');

  const result = await coordinator.submitTarget('target');

  assert.equal(result.ok, false);
  assert.equal(coordinator.snapshot().kind, 'armed');
  assert.equal(coordinator.snapshot().source.id, 'source');
  assert.match(coordinator.snapshot().feedback.message, /Ziel nicht bereit/);
});

test('renames update display names while requests keep stable IDs', async () => {
  const calls = [];
  const coordinator = createHandoffCoordinator({
    submit: async (...ids) => { calls.push(ids); },
  });
  coordinator.reconcile([agent('source-id', 'Alt'), agent('target-id', 'Ziel')]);
  coordinator.arm('source-id');
  coordinator.reconcile([agent('source-id', 'Neu'), agent('target-id', 'Ziel neu')]);

  await coordinator.submitTarget('target-id');

  assert.deepEqual(calls, [['source-id', 'target-id']]);
  assert.equal(coordinator.snapshot().feedback.source.name, 'Neu');
  assert.equal(coordinator.snapshot().feedback.target.name, 'Ziel neu');
});

test('a missing stable ID is never accepted as a handoff source', () => {
  assert.match(handoffSourceReason(agent('', 'Legacy')), /stabile Session-ID/);
});

class FakeClassList {
  constructor() { this.values = new Set(); }
  add(...names) { names.forEach(name => this.values.add(name)); }
  remove(...names) { names.forEach(name => this.values.delete(name)); }
  contains(name) { return this.values.has(name); }
  toggle(name, force) {
    const enabled = force ?? !this.values.has(name);
    if (enabled) this.values.add(name);
    else this.values.delete(name);
    return enabled;
  }
}

class FakeTarget {
  constructor() { this.listeners = new Map(); }
  addEventListener(type, listener) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type).add(listener);
  }
  removeEventListener(type, listener) { this.listeners.get(type)?.delete(listener); }
  emit(type, event = {}) {
    for (const listener of [...(this.listeners.get(type) || [])]) listener(event);
  }
  listenerCount(type) { return this.listeners.get(type)?.size || 0; }
}

class FakeElement extends FakeTarget {
  constructor() {
    super();
    this.classList = new FakeClassList();
    this.dataset = {};
    this.style = {};
    this.children = [];
    this.isConnected = true;
    this.firstElementChild = null;
  }
  appendChild(child) { this.children.push(child); child.parent = this; return child; }
  remove() { this.isConnected = false; }
  replaceChildren(...children) { this.children = children; }
  setAttribute() {}
  removeAttribute() {}
  focus() { this.focused = true; }
}

test('lost pointer capture removes drag visuals and transient listeners', () => {
  const fakeWindow = new FakeTarget();
  fakeWindow.setTimeout = callback => { callback(); return 1; };
  fakeWindow.clearTimeout = () => {};
  const body = new FakeElement();
  const fakeDocument = {
    defaultView: fakeWindow,
    body,
    createElement: () => new FakeElement(),
    elementFromPoint: () => null,
  };
  const root = new FakeElement();
  root.ownerDocument = fakeDocument;
  root.contains = () => true;
  root.querySelectorAll = () => [];

  const wrap = new FakeElement();
  wrap.dataset.sessionId = 'source';
  wrap.dataset.termName = 'Quelle';
  const button = new FakeElement();
  button.closest = selector => selector === '.hh-magnet' ? button : (selector === '.term-wrap' ? wrap : null);
  button.captures = new Set();
  button.setPointerCapture = id => button.captures.add(id);
  button.hasPointerCapture = id => button.captures.has(id);
  button.releasePointerCapture = id => button.captures.delete(id);

  const controller = createHydraHandoff({
    root,
    statusElement: () => null,
    submit: async () => {},
  });
  controller.reconcile([agent('source', 'Quelle')]);
  root.emit('pointerdown', {
    target: button,
    button: 0,
    pointerId: 7,
    clientX: 0,
    clientY: 0,
    stopPropagation() {},
  });
  fakeWindow.emit('pointermove', {
    pointerId: 7,
    pointerType: 'mouse',
    buttons: 1,
    clientX: 10,
    clientY: 0,
    preventDefault() {},
  });
  assert.equal(body.classList.contains('session-magnet-dragging'), true);
  assert.equal(fakeWindow.listenerCount('pointermove'), 1);

  button.emit('lostpointercapture', { pointerId: 7 });

  assert.equal(body.classList.contains('session-magnet-dragging'), false);
  assert.equal(button.captures.has(7), false);
  assert.equal(fakeWindow.listenerCount('pointermove'), 0);
  assert.equal(fakeWindow.listenerCount('pointerup'), 0);
  assert.equal(fakeWindow.listenerCount('pointercancel'), 0);
  controller.dispose();
});
