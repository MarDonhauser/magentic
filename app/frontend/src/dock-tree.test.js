import test from 'node:test';
import assert from 'node:assert/strict';
import {
  createLeaf, createSplit, listLeaves, removeTab, resizeSplit,
  splitLeafWithTab, moveTabToEdge, serializeTree, normalizeLayout, dockRefKey,
} from './dock-tree.js';

test('listLeaves walks a split tree in visual order', () => {
  const leafA = createLeaf([{ id: 'a', name: 'A' }], 'session:a');
  const leafB = createLeaf([{ id: 'b', name: 'B' }], 'session:b');
  const root = createSplit('row', leafA, leafB);
  assert.deepEqual(listLeaves(root).map(l => l.id), [leafA.id, leafB.id]);
});

test('moveTabToEdge splits the target leaf and removes the tab from its origin', () => {
  const ref = { id: 'a', name: 'A' };
  const other = { id: 'b', name: 'B' };
  const leaf = createLeaf([ref, other], 'session:a');
  const root = moveTabToEdge(leaf, ref, leaf.id, 'right');

  assert.equal(root.type, 'split');
  assert.equal(root.dir, 'row');
  assert.deepEqual(root.a.tabs, [other]);
  assert.deepEqual(root.b.tabs, [ref]);
});

test('moveTabToEdge onto a different leaf collapses the now-empty origin', () => {
  const ref = { id: 'a', name: 'A' };
  const leafA = createLeaf([ref], 'session:a');
  const leafB = createLeaf([{ id: 'b', name: 'B' }], 'session:b');
  const root = createSplit('row', leafA, leafB);

  const next = moveTabToEdge(root, ref, leafB.id, 'bottom');
  assert.equal(next.type, 'split');
  assert.equal(next.dir, 'column');
  assert.equal(next.a.id, leafB.id);
  assert.deepEqual(next.b.tabs, [ref]);
});

test("moveTabToEdge is a no-op when dragging a leaf's only tab onto itself", () => {
  const ref = { id: 'a', name: 'A' };
  const leaf = createLeaf([ref], 'session:a');
  const next = moveTabToEdge(leaf, ref, leaf.id, 'left');
  assert.equal(next, leaf);
});

test('removeTab collapses an empty leaf into its sibling', () => {
  const ref = { id: 'a', name: 'A' };
  const leafA = createLeaf([ref], 'session:a');
  const leafB = createLeaf([{ id: 'b', name: 'B' }], 'session:b');
  const root = createSplit('row', leafA, leafB);

  const next = removeTab(root, dockRefKey(ref));
  assert.equal(next, leafB);
});

test('removeTab re-picks the active tab when the active tab is removed', () => {
  const a = { id: 'a', name: 'A' };
  const b = { id: 'b', name: 'B' };
  const leaf = createLeaf([a, b], dockRefKey(a));
  const next = removeTab(leaf, dockRefKey(a));
  assert.equal(next.activeKey, dockRefKey(b));
});

test('resizeSplit clamps the ratio', () => {
  const root = createSplit('row', createLeaf(), createLeaf());
  assert.equal(resizeSplit(root, root.id, 0.02).ratio, 0.15);
  assert.equal(resizeSplit(root, root.id, 0.99).ratio, 0.85);
});

test('splitLeafWithTab wraps the target leaf in a new split with the given edge', () => {
  const target = createLeaf([{ id: 'x', name: 'X' }], 'session:x');
  const newRef = { id: 'y', name: 'Y' };
  const root = splitLeafWithTab(target, target.id, newRef, 'top');
  assert.equal(root.dir, 'column');
  assert.deepEqual(root.a.tabs, [newRef]);
  assert.equal(root.b, target);
});

test('serializeTree and normalizeLayout round-trip a split tree', () => {
  const a = { id: 'a', name: 'A' };
  const b = { id: 'b', name: 'B' };
  const root = createSplit('column', createLeaf([a], dockRefKey(a)), createLeaf([b], dockRefKey(b)), 0.4);
  const restored = normalizeLayout(serializeTree(root));
  assert.equal(restored.type, 'split');
  assert.equal(restored.ratio, 0.4);
  assert.deepEqual(listLeaves(restored).map(l => l.tabs), [[a], [b]]);
});

test('normalizeLayout drops duplicate tab keys and invalid nodes', () => {
  const raw = {
    type: 'split', dir: 'row', ratio: 0.5,
    a: { type: 'leaf', tabs: [{ id: 'a', name: 'A' }], active: 'session:a' },
    b: { type: 'leaf', tabs: [{ id: 'a', name: 'A' }, { id: 'b', name: 'B' }], active: 'session:b' },
  };
  const layout = normalizeLayout(raw);
  assert.equal(layout.type, 'split');
  assert.deepEqual(layout.a.tabs, [{ id: 'a', name: 'A' }]);
  assert.deepEqual(layout.b.tabs, [{ id: 'b', name: 'B' }]);
});

test('normalizeLayout returns null for garbage or fully-empty input', () => {
  assert.equal(normalizeLayout(null), null);
  assert.equal(normalizeLayout({ type: 'leaf', tabs: [] }), null);
});
