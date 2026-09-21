import test from 'node:test';
import assert from 'node:assert/strict';

import { createHydraOrder, placeInOrder } from './hydra-order.js';

const names = agents => agents.map(a => a.name);
const agent = name => ({ name, id: `id-${name}` });

test('Hydra order keeps the first-seen order when the backend later reports the sessions differently', () => {
  const order = createHydraOrder();
  assert.deepEqual(names(order.order('alpha', [agent('a-1'), agent('a-2'), agent('a-3')])), ['a-1', 'a-2', 'a-3']);
  assert.deepEqual(names(order.order('alpha', [agent('a-3'), agent('a-1'), agent('a-2')])), ['a-1', 'a-2', 'a-3']);
});

test('Hydra order survives a visit to another project and appends newcomers at the end', () => {
  const order = createHydraOrder();
  order.order('alpha', [agent('a-1'), agent('a-2')]);
  assert.deepEqual(names(order.order('beta', [agent('b-2'), agent('b-1')])), ['b-2', 'b-1']);
  assert.deepEqual(names(order.order('alpha', [agent('a-3'), agent('a-2'), agent('a-1')])), ['a-1', 'a-2', 'a-3']);
  assert.deepEqual(names(order.order('beta', [agent('b-1'), agent('b-2')])), ['b-2', 'b-1']);
});

test('Hydra order gives a session that disappears and returns its old slot back', () => {
  const order = createHydraOrder();
  order.order('alpha', [agent('a-1'), agent('a-2'), agent('a-3')]);
  assert.deepEqual(names(order.order('alpha', [agent('a-1'), agent('a-3')])), ['a-1', 'a-3']);
  assert.deepEqual(names(order.order('alpha', [agent('a-3'), agent('a-2'), agent('a-1')])), ['a-1', 'a-2', 'a-3']);
});

test('Hydra order ignores malformed entries and duplicate names, and forgets a project on request', () => {
  const order = createHydraOrder();
  assert.deepEqual(names(order.order('alpha', [agent('a-2'), null, { id: 'x' }, agent('a-2'), agent('a-1')])), ['a-2', 'a-1']);
  order.forget('alpha');
  assert.deepEqual(names(order.order('alpha', [agent('a-1'), agent('a-2')])), ['a-1', 'a-2']);
});

function fakeContainer(initial) {
  const container = { children: [...initial] };
  Object.defineProperty(container, 'firstElementChild', { get: () => container.children[0] || null });
  for (const child of initial) attach(container, child);
  container.insertBefore = (element, before) => {
    const from = container.children.indexOf(element);
    if (from >= 0) container.children.splice(from, 1);
    const at = before ? container.children.indexOf(before) : container.children.length;
    container.children.splice(at, 0, element);
    attach(container, element);
  };
  return container;
}

function attach(container, element) {
  Object.defineProperty(element, 'nextElementSibling', {
    configurable: true,
    get: () => container.children[container.children.indexOf(element) + 1] || null,
  });
}

test('placeInOrder reorders existing children and inserts missing ones without touching already-correct slots', () => {
  const a = { id: 'a' }, b = { id: 'b' }, c = { id: 'c' }, d = { id: 'd' };
  const container = fakeContainer([c, a, b]);
  let moves = 0;
  const raw = container.insertBefore;
  container.insertBefore = (el, before) => { moves++; raw(el, before); };
  placeInOrder(container, [a, b, c, d]);
  assert.deepEqual(container.children.map(x => x.id), ['a', 'b', 'c', 'd']);
  assert.equal(moves, 3);
  moves = 0;
  placeInOrder(container, [a, b, c, d]);
  assert.equal(moves, 0);
});
