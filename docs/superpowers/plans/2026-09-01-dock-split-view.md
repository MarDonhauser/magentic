# Split View für das Terminal-Dock Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das Terminal-Dock erlaubt, Terminal-Tabs per Drag & Drop oder Rechtsklick beliebig horizontal und vertikal in mehrere gleichzeitig sichtbare Panes zu splitten (VS-Code-artig), mit eigenem Tab-Streifen pro Pane, Resize-Grips und persistiertem Layout.

**Architecture:** Ein neues, DOM-freies Modul `dock-tree.js` bildet das Split-Layout als rekursiven Binärbaum ab (Blatt = Tab-Streifen; Split = zwei Kinder + Richtung + Verhältnis) und stellt reine Funktionen dafür bereit. `dock-state.js` nutzt dieses Modul, um den persistierten `localStorage`-Zustand zu normalisieren (inkl. Migration von altem Flat-Zustand). `dock.js` rendert den Baum rekursiv ins DOM, wobei bestehende Tab-/Terminal-DOM-Knoten bei Strukturänderungen per `appendChild` in die richtige Pane verschoben werden (nicht neu erzeugt), damit laufende xterm-Instanzen samt Scrollback erhalten bleiben.

**Tech Stack:** Vanilla JS (ES-Module), xterm.js (bestehend), `node:test` für die reine Baum-Logik.

**Spec:** `docs/superpowers/specs/2026-09-01-dock-split-view-design.md`

## Global Constraints

- Kein Split außerhalb des Terminal-Docks.
- Ein Tab gehört immer genau einem Blatt (kein gleichzeitiges Anzeigen desselben Tabs in zwei Panes).
- Keine neue Test-Infrastruktur — nur `node --test` (bereits vorhanden über `npm test` in `app/frontend`).
- Öffentliches API von `dock.js` bleibt unverändert: `mountDock(opts)`, `toggleDock(next)`, `isDockOpen()`, `closeDockTab(value)`, `dockTabs()`, `refitDock()` — `main.js` wird nicht angefasst.
- Minimum-/Maximum-Ratio für Splits: 0.15 / 0.85 (aus der Spec).
- Drop-Zonen-Randbreite: 25 % der jeweiligen Kantenlänge (aus der Spec).

---

### Task 1: Split-Baum als reines Datenmodul (`dock-tree.js`)

**Files:**
- Create: `app/frontend/src/dock-tree.js`
- Test: `app/frontend/src/dock-tree.test.js`

**Interfaces:**
- Produces (für Task 2 und Task 3):
  - `normalizeDockRef(value): {id, name} | null`
  - `dockRefKey(value): string`
  - `createLeaf(tabs = [], activeKey = null): LeafNode`
  - `createSplit(dir: 'row'|'column', a: Node, b: Node, ratio = 0.5): SplitNode`
  - `listLeaves(node, out = []): LeafNode[]` (Reihenfolge: `a` vor `b`)
  - `findLeafByTabKey(node, key: string): LeafNode | null`
  - `getNode(node, id: string): Node | null`
  - `removeTab(root, key: string): Node` (entfernt Tab, kollabiert leere Blätter automatisch)
  - `addTabToLeaf(root, leafId: string, ref): Node`
  - `setActiveTab(root, leafId: string, key: string): Node`
  - `resizeSplit(root, splitId: string, ratio: number): Node` (clamped 0.15–0.85)
  - `splitLeafWithTab(root, targetLeafId: string, ref, edge: 'left'|'right'|'top'|'bottom'): Node`
  - `moveTabToEdge(root, ref, targetLeafId: string, edge): Node`
  - `serializeTree(node): PlainObject`
  - `normalizeLayout(raw): Node | null`

- [ ] **Step 1: Write the failing tests**

```js
// app/frontend/src/dock-tree.test.js
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd app/frontend && node --test src/dock-tree.test.js`
Expected: FAIL — `Cannot find module './dock-tree.js'` (module doesn't exist yet).

- [ ] **Step 3: Implement `dock-tree.js`**

```js
// app/frontend/src/dock-tree.js
let uid = 0;
function nextId(prefix) { return `${prefix}${++uid}`; }

export function normalizeDockRef(value) {
  if (typeof value === 'string') {
    const name = value.trim();
    return name ? { id: '', name } : null;
  }
  if (!value || typeof value !== 'object') return null;
  const id = typeof value.id === 'string' ? value.id.trim() : '';
  const name = typeof value.name === 'string' ? value.name.trim() : '';
  if (!name) return null;
  return { id, name };
}

export function dockRefKey(value) {
  const ref = normalizeDockRef(value);
  if (!ref) return '';
  return ref.id ? `session:${ref.id}` : `legacy:${ref.name}`;
}

export function createLeaf(tabs = [], activeKey = null) {
  return { type: 'leaf', id: nextId('leaf-'), tabs: [...tabs], activeKey };
}

export function createSplit(dir, a, b, ratio = 0.5) {
  return { type: 'split', id: nextId('split-'), dir, ratio, a, b };
}

export function listLeaves(node, out = []) {
  if (node.type === 'leaf') { out.push(node); return out; }
  listLeaves(node.a, out);
  listLeaves(node.b, out);
  return out;
}

export function findLeafByTabKey(node, key) {
  if (node.type === 'leaf') return node.tabs.some(t => dockRefKey(t) === key) ? node : null;
  return findLeafByTabKey(node.a, key) || findLeafByTabKey(node.b, key);
}

export function getNode(node, id) {
  if (node.id === id) return node;
  if (node.type === 'leaf') return null;
  return getNode(node.a, id) || getNode(node.b, id);
}

function rebuild(node, id, fn) {
  if (node.id === id) return fn(node);
  if (node.type === 'leaf') return node;
  const a = rebuild(node.a, id, fn);
  const b = rebuild(node.b, id, fn);
  if (a === node.a && b === node.b) return node;
  return { ...node, a, b };
}

function collapse(node) {
  if (node.type === 'leaf') return node;
  const a = collapse(node.a);
  const b = collapse(node.b);
  if (a.type === 'leaf' && a.tabs.length === 0) return b;
  if (b.type === 'leaf' && b.tabs.length === 0) return a;
  if (a === node.a && b === node.b) return node;
  return { ...node, a, b };
}

export function removeTab(root, key) {
  const leaf = findLeafByTabKey(root, key);
  if (!leaf) return root;
  const tabs = leaf.tabs.filter(t => dockRefKey(t) !== key);
  let activeKey = leaf.activeKey;
  if (activeKey === key) {
    const idx = leaf.tabs.findIndex(t => dockRefKey(t) === key);
    const fallback = tabs[idx] || tabs[idx - 1] || tabs[0];
    activeKey = fallback ? dockRefKey(fallback) : null;
  }
  const next = rebuild(root, leaf.id, () => ({ ...leaf, tabs, activeKey }));
  return collapse(next);
}

export function addTabToLeaf(root, leafId, ref) {
  const key = dockRefKey(ref);
  if (!key) return root;
  return rebuild(root, leafId, leaf => {
    if (leaf.tabs.some(t => dockRefKey(t) === key)) return { ...leaf, activeKey: key };
    return { ...leaf, tabs: [...leaf.tabs, ref], activeKey: key };
  });
}

export function setActiveTab(root, leafId, key) {
  return rebuild(root, leafId, leaf => ({ ...leaf, activeKey: key }));
}

export function resizeSplit(root, splitId, ratio) {
  const clamped = Math.min(Math.max(ratio, 0.15), 0.85);
  return rebuild(root, splitId, node => ({ ...node, ratio: clamped }));
}

export function splitLeafWithTab(root, targetLeafId, ref, edge) {
  const dir = edge === 'left' || edge === 'right' ? 'row' : 'column';
  const before = edge === 'left' || edge === 'top';
  const key = dockRefKey(ref);
  return rebuild(root, targetLeafId, target => {
    const newLeaf = createLeaf([ref], key);
    return before ? createSplit(dir, newLeaf, target) : createSplit(dir, target, newLeaf);
  });
}

export function moveTabToEdge(root, ref, targetLeafId, edge) {
  const key = dockRefKey(ref);
  const sourceLeaf = findLeafByTabKey(root, key);
  if (sourceLeaf && sourceLeaf.id === targetLeafId && sourceLeaf.tabs.length === 1) return root;
  return splitLeafWithTab(removeTab(root, key), targetLeafId, ref, edge);
}

export function serializeTree(node) {
  if (node.type === 'leaf') {
    return { type: 'leaf', tabs: node.tabs.map(t => ({ id: t.id, name: t.name })), active: node.activeKey };
  }
  return { type: 'split', dir: node.dir, ratio: node.ratio, a: serializeTree(node.a), b: serializeTree(node.b) };
}

export function normalizeLayout(raw) {
  const seen = new Set();
  function build(n) {
    if (!n || typeof n !== 'object') return null;
    if (n.type === 'leaf') {
      const tabs = [];
      for (const v of Array.isArray(n.tabs) ? n.tabs : []) {
        const ref = normalizeDockRef(v);
        const key = dockRefKey(ref);
        if (!key || seen.has(key)) continue;
        seen.add(key);
        tabs.push(ref);
      }
      if (!tabs.length) return null;
      const activeRef = tabs.find(t => dockRefKey(t) === n.active);
      return createLeaf(tabs, activeRef ? dockRefKey(activeRef) : dockRefKey(tabs[0]));
    }
    if (n.type === 'split' && (n.dir === 'row' || n.dir === 'column')) {
      const a = build(n.a);
      const b = build(n.b);
      if (!a && !b) return null;
      if (!a) return b;
      if (!b) return a;
      const ratio = Number.isFinite(n.ratio) ? Math.min(Math.max(n.ratio, 0.15), 0.85) : 0.5;
      return createSplit(n.dir, a, b, ratio);
    }
    return null;
  }
  return build(raw);
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd app/frontend && node --test src/dock-tree.test.js`
Expected: PASS (10 tests)

- [ ] **Step 5: Commit**

```bash
git add app/frontend/src/dock-tree.js app/frontend/src/dock-tree.test.js
git commit -m "feat: Split-Baum-Datenmodell für das Terminal-Dock"
```

---

### Task 2: `dock-state.js` auf den Split-Baum umstellen

**Files:**
- Modify: `app/frontend/src/dock-state.js`
- Test: `app/frontend/src/dock-state.test.js`

**Interfaces:**
- Consumes: alle Exporte aus Task 1 (`createLeaf`, `dockRefKey`, `normalizeDockRef`, `normalizeLayout`).
- Produces (für Task 3): `normalizeDockState(raw, defaultHeight): { open: boolean, height: number, layout: Node } | null` (Rückgabeform geändert — `tabs`/`active` entfallen zugunsten von `layout`). `resolveLegacyDockRefs(tabs, resolved)` bleibt unverändert (Signatur und Verhalten). `dockRefKey`/`normalizeDockRef` werden weiterhin aus `dock-state.js` re-exportiert.

- [ ] **Step 1: Update the test file for the new `normalizeDockState` contract**

```js
// app/frontend/src/dock-state.test.js
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd app/frontend && node --test src/dock-state.test.js`
Expected: FAIL — `state.layout` is `undefined` (old implementation still returns `tabs`/`active`).

- [ ] **Step 3: Update `dock-state.js`**

```js
// app/frontend/src/dock-state.js
import { createLeaf, dockRefKey, normalizeDockRef, normalizeLayout } from './dock-tree.js';

export { dockRefKey, normalizeDockRef };

export function normalizeDockState(raw, defaultHeight) {
  if (!raw || typeof raw !== 'object') return null;
  let layout = normalizeLayout(raw.layout);
  if (!layout) {
    const tabs = [];
    const seen = new Set();
    for (const value of Array.isArray(raw.tabs) ? raw.tabs : []) {
      const ref = normalizeDockRef(value);
      const key = dockRefKey(ref);
      if (!key || seen.has(key)) continue;
      seen.add(key);
      tabs.push(ref);
    }
    const requested = typeof raw.active === 'string' ? raw.active : '';
    const activeRef = tabs.find(ref => dockRefKey(ref) === requested || ref.name === requested);
    layout = createLeaf(tabs, activeRef ? dockRefKey(activeRef) : (tabs[0] ? dockRefKey(tabs[0]) : null));
  }
  return {
    open: !!raw.open,
    height: Number.isFinite(raw.height) ? raw.height : defaultHeight,
    layout,
  };
}

// resolveLegacyDockRefs performs the one-time persisted-name migration. A
// successful Registry lookup removes names that no longer identify a Dock
// Session; every resolved tab is persisted with its durable ID afterwards.
export function resolveLegacyDockRefs(tabs, resolved) {
  const byName = new Map();
  for (const value of Array.isArray(resolved) ? resolved : []) {
    const ref = normalizeDockRef(value);
    if (ref?.id) byName.set(ref.name, ref);
  }
  const result = [];
  const seen = new Set();
  for (const value of Array.isArray(tabs) ? tabs : []) {
    const ref = normalizeDockRef(value);
    const stable = ref?.id ? ref : byName.get(ref?.name);
    const key = dockRefKey(stable);
    if (!key || seen.has(key)) continue;
    seen.add(key);
    result.push(stable);
  }
  return result;
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd app/frontend && node --test src/dock-state.test.js`
Expected: PASS (3 tests)

- [ ] **Step 5: Run the full frontend test suite to check for regressions**

Run: `cd app/frontend && npm test`
Expected: PASS — no other file imports the old `{tabs, active}` shape of `normalizeDockState` (only `dock.js` does, updated in Task 3).

- [ ] **Step 6: Commit**

```bash
git add app/frontend/src/dock-state.js app/frontend/src/dock-state.test.js
git commit -m "feat: Dock-Zustand auf den Split-Baum umstellen"
```

---

### Task 3: `dock.js` und `dock.css` — Rendering, Drag & Drop, Kontextmenü, Resize

**Files:**
- Modify: `app/frontend/src/dock.js` (vollständige Neufassung, 603 → ~520 Zeilen)
- Modify: `app/frontend/src/dock.css` (Ergänzungen für Split-Layout, Grips, Drop-Overlay, Kontextmenü)

**Interfaces:**
- Consumes: alle Exporte aus Task 1 (`dock-tree.js`) und Task 2 (`normalizeDockState`, `resolveLegacyDockRefs` aus `dock-state.js`).
- Produces: unverändertes öffentliches API (`mountDock`, `toggleDock`, `isDockOpen`, `closeDockTab`, `dockTabs`, `refitDock`) — `main.js` bleibt unangetastet.

- [ ] **Step 1: Replace `app/frontend/src/dock.js`**

```js
import './dock.css';

import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { BrowserOpenURL } from '../wailsjs/runtime/runtime';
import { developerIcon } from './avatar.js';
import { onThemeChange, terminalTheme } from './theme.js';
import { TERMINAL_OPTIONS, setUpTerminal } from './terminal-setup.js';
import {
  createLeaf, listLeaves, findLeafByTabKey, getNode, removeTab, addTabToLeaf,
  setActiveTab, resizeSplit, moveTabToEdge, serializeTree, dockRefKey, normalizeDockRef,
} from './dock-tree.js';
import { normalizeDockState, resolveLegacyDockRefs } from './dock-state.js';

const STORE_KEY = 'magentic.dock';
const DEFAULT_HEIGHT = 280;
const MIN_HEIGHT = 120;
const MAX_RATIO = 0.8;
const DOCK_ICONS = {
  plus: '<path d="M12 5v14M5 12h14"/>',
  down: '<path d="m6 9 6 6 6-6"/>',
  close: '<path d="M18 6 6 18M6 6l12 12"/>',
};

function dockIcon(name) {
  return `<svg class="dk-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${DOCK_ICONS[name]}</svg>`;
}

const enc = new TextEncoder();

function toB64(s) {
  let bin = '';
  for (const b of enc.encode(s)) bin += String.fromCharCode(b);
  return btoa(bin);
}

function fromB64(b64) {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

const tabs = new Map();
const leafDom = new Map();
const nodeDom = new Map();

onThemeChange(theme => {
  const nextTheme = terminalTheme(theme);
  for (const tab of tabs.values()) {
    if (tab.term) tab.term.options.theme = nextTheme;
  }
});

let cb = {};
let mounted = false;
let open = false;
let height = DEFAULT_HEIGHT;
let rootNode = createLeaf();
let focusedLeafId = rootNode.id;
let dockEl = null;
let topBarEl = null;
let emptyTitleEl = null;
let treeEl = null;
let fitPending = false;
let selfResize = false;
let dragTab = null;
let dropOverlayEl = null;
let menuEl = null;

function maxHeight() {
  return Math.max(MIN_HEIGHT, Math.round(window.innerHeight * MAX_RATIO));
}

function readState() {
  try {
    const raw = JSON.parse(localStorage.getItem(STORE_KEY) || 'null');
    return normalizeDockState(raw, DEFAULT_HEIGHT);
  } catch {
    return null;
  }
}

function focusedLeaf() {
  const found = getNode(rootNode, focusedLeafId);
  if (found && found.type === 'leaf') return found;
  const first = listLeaves(rootNode)[0];
  focusedLeafId = first.id;
  return first;
}

function activeTab() {
  const leaf = focusedLeaf();
  return leaf.activeKey ? tabs.get(leaf.activeKey) : null;
}

function persist() {
  try {
    localStorage.setItem(STORE_KEY, JSON.stringify({
      open,
      height,
      layout: serializeTree(rootNode),
      focused: activeTab()?.key || null,
    }));
  } catch { /* Speicher voll oder gesperrt — Zustand ist dann eben flüchtig */ }
}

function clampHeight(v) {
  return Math.min(Math.max(Math.round(v), MIN_HEIGHT), maxHeight());
}

function applyHeight() {
  document.body.style.setProperty('--dk-h', clampHeight(height) + 'px');
}

function applyOpen() {
  document.body.classList.toggle('dk-open', open);
}

function openURL(uri) {
  try { BrowserOpenURL(uri); } catch { window.open(uri, '_blank'); }
}

function fallbackCopy(text) {
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.cssText = 'position:fixed;top:0;left:0;opacity:0;pointer-events:none';
  document.body.appendChild(ta);
  ta.select();
  try { document.execCommand('copy'); } catch { /* ohne Zwischenablage-Rechte nicht möglich */ }
  ta.remove();
  activeTab()?.term?.focus();
}

// Über die Wails-Runtime statt navigator.clipboard: WebKit blendet für den
// Lesezugriff eine Bestätigung („paste") ein, die bei jedem Einfügen erscheint.
// Der Weg über Go umgeht sie.
function copyText(text) {
  if (cb.setClipboard) {
    Promise.resolve(cb.setClipboard(text)).catch(() => fallbackCopy(text));
    return;
  }
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).catch(() => fallbackCopy(text));
    return;
  }
  fallbackCopy(text);
}

function isToggleKey(e) {
  return e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey && (e.code === 'Backquote' || e.key === '`');
}

function isTabNavKey(e) {
  return e.metaKey && e.altKey && !e.ctrlKey && (e.key === 'ArrowLeft' || e.key === 'ArrowRight');
}

function fitNow(t) {
  if (!open || !t?.fit || !t.term) return;
  const dom = leafDom.get(t.leafId);
  if (!dom) return;
  const box = dom.bodyEl.getBoundingClientRect();
  if (box.width < 2 || box.height < 2) return;
  try { t.fit.fit(); } catch { return; }
  try { cb.resize?.(t.ref, t.term.cols, t.term.rows); } catch { /* Backend meldet sich beim nächsten Versuch */ }
}

function scheduleFit() {
  if (fitPending) return;
  fitPending = true;
  requestAnimationFrame(() => {
    fitPending = false;
    for (const leaf of listLeaves(rootNode)) {
      const t = leaf.activeKey ? tabs.get(leaf.activeKey) : null;
      if (t) fitNow(t);
    }
  });
}

function notifyLayout() {
  scheduleFit();
  selfResize = true;
  try { window.dispatchEvent(new Event('resize')); } finally { selfResize = false; }
}

function bindKeys(term, t) {
  let lastSel = '';
  let lastSelAt = 0;
  term.onSelectionChange(() => {
    const s = term.getSelection();
    if (s) { lastSel = s; lastSelAt = Date.now(); }
  });
  term.attachCustomKeyEventHandler(e => {
    if (e.type !== 'keydown') return true;
    if (isToggleKey(e) || isTabNavKey(e)) return false;
    if (!e.metaKey || e.ctrlKey || e.altKey) return true;
    const k = e.key.toLowerCase();
    if (k === 'c') {
      const sel = term.getSelection() || (Date.now() - lastSelAt < 30000 ? lastSel : '');
      if (sel) {
        copyText(sel);
        e.preventDefault();
        return false;
      }
    }
    // Cmd+V bewusst durchlassen: xterm.js nimmt das native Einfügen entgegen
    // und liefert den Text über onData. Läse man die Zwischenablage selbst
    // aus, verlangte WebKit bei jedem Einfügen eine Bestätigung.
    return true;
  });
}

function syncDot(t) {
  let color = 'var(--muted)';
  let label = '';
  try {
    const s = cb.status?.(t.ref);
    if (s) {
      color = s.color || color;
      label = s.label || '';
    }
  } catch { /* Statusquelle darf das Dock nicht mitreißen */ }
  if (t.closed) {
    color = 'var(--critical)';
    label = label || 'beendet';
  }
  t.dot.style.background = color;
  t.el.title = label ? `${t.name} — ${label}` : t.name;
}

function updateStatuses() {
  for (const t of tabs.values()) syncDot(t);
}

function updateBlank() {
  const blank = tabs.size === 0;
  dockEl.classList.toggle('dk-blank', blank);
  document.body.classList.toggle('dk-no-tabs', blank);
}

function buildTabEl(t) {
  const el = document.createElement('div');
  el.className = 'dk-tab';
  el.dataset.key = t.key;
  el.draggable = true;

  const dot = document.createElement('span');
  dot.className = 'dk-dot';

  const label = document.createElement('span');
  label.className = 'dk-name';
  label.textContent = t.name;

  const tool = document.createElement('span');
  tool.className = 'dk-tool';
  tool.setAttribute('aria-hidden', 'true');
  tool.innerHTML = developerIcon('bash');

  const x = document.createElement('button');
  x.className = 'dk-x';
  x.type = 'button';
  x.innerHTML = dockIcon('close');
  x.setAttribute('aria-label', `Tab ${t.name} schließen`);
  x.title = 'Tab schließen';

  el.append(dot, tool, label, x);
  t.el = el;
  t.dot = dot;
}

function addTab(ref) {
  const key = dockRefKey(ref);
  if (!key) return null;
  if (tabs.has(key)) return tabs.get(key);
  const name = ref.name;

  const pane = document.createElement('div');
  pane.className = 'dk-pane';
  const host = document.createElement('div');
  host.className = 'dk-term';
  pane.appendChild(host);

  const t = { ref, key, name, el: null, dot: null, pane, host, term: null, fit: null, offData: null, offClosed: null, live: false, closed: false, leafId: null };
  buildTabEl(t);
  tabs.set(key, t);
  syncDot(t);
  return t;
}

function ensureLive(t) {
  if (t.live) return;
  t.live = true;

  const term = new Terminal({
    ...TERMINAL_OPTIONS,
    fontSize: 13,
    lineHeight: 1.1,
    scrollback: 10000,
    theme: terminalTheme(),
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new WebLinksAddon((e, uri) => openURL(uri)));
  term.open(t.host);
  setUpTerminal(term, () => fit.fit());
  bindKeys(term, t);

  term.onData(d => cb.write?.(t.ref, toB64(d)));
  term.onResize(({ cols, rows }) => cb.resize?.(t.ref, cols, rows));

  t.term = term;
  t.fit = fit;

  try { t.offData = cb.onData?.(t.ref, b64 => term.write(fromB64(b64))) || null; } catch { t.offData = null; }
  try {
    t.offClosed = cb.onClosed?.(t.ref, () => {
      t.closed = true;
      term.write('\r\n\x1b[31m— Verbindung beendet —\x1b[0m\r\n');
      syncDot(t);
    }) || null;
  } catch { t.offClosed = null; }

  try { fit.fit(); } catch { /* Pane noch ohne Maße */ }
  Promise.resolve(cb.attach?.(t.ref, term.cols, term.rows))
    .catch(err => term.write('\x1b[31m' + err + '\x1b[0m\r\n'));
}

function activate(key) {
  const t = tabs.get(key);
  if (!t) return;
  const leaf = getNode(rootNode, t.leafId);
  if (!leaf) return;
  rootNode = setActiveTab(rootNode, leaf.id, key);
  focusedLeafId = leaf.id;
  for (const ref of leaf.tabs) {
    const other = tabs.get(dockRefKey(ref));
    if (!other) continue;
    const on = dockRefKey(ref) === key;
    other.el.classList.toggle('dk-active', on);
    other.pane.classList.toggle('dk-on', on);
  }
  t.el.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  if (open) {
    ensureLive(t);
    fitNow(t);
    t.term?.focus();
  }
  try { cb.onActive?.(t.ref); } catch { /* optionaler Callback */ }
  persist();
}

function stepTab(dir) {
  const leaf = focusedLeaf();
  const list = leaf.tabs.map(dockRefKey);
  if (list.length < 2) return;
  const i = list.indexOf(leaf.activeKey);
  const next = list[((i < 0 ? 0 : i) + dir + list.length) % list.length];
  activate(next);
}

async function spawnTerminal() {
  if (!cb.newTerminal) return;
  let ref = null;
  try { ref = await cb.newTerminal(); } catch { return; }
  if (ref) openDockTab(ref);
}

function edgeForPoint(el, x, y) {
  const box = el.getBoundingClientRect();
  const rx = (x - box.left) / box.width;
  const ry = (y - box.top) / box.height;
  const margin = 0.25;
  if (rx < margin) return 'left';
  if (rx > 1 - margin) return 'right';
  if (ry < margin) return 'top';
  if (ry > 1 - margin) return 'bottom';
  return null;
}

function showDropOverlay(bodyEl, x, y) {
  const edge = edgeForPoint(bodyEl, x, y);
  if (!dropOverlayEl) {
    dropOverlayEl = document.createElement('div');
    dropOverlayEl.className = 'dk-drop-overlay';
  }
  if (dropOverlayEl.parentElement !== bodyEl) bodyEl.appendChild(dropOverlayEl);
  dropOverlayEl.className = 'dk-drop-overlay' + (edge ? ` dk-drop-${edge}` : ' dk-drop-hidden');
}

function clearDropOverlay() {
  dropOverlayEl?.remove();
}

function applySplit(tabKey, targetLeafId, edge) {
  const t = tabs.get(tabKey);
  if (!t) return;
  rootNode = moveTabToEdge(rootNode, t.ref, targetLeafId, edge);
  focusedLeafId = findLeafByTabKey(rootNode, tabKey)?.id || focusedLeafId;
  renderTree();
  activate(tabKey);
}

function closeSplitMenu() {
  menuEl?.remove();
  menuEl = null;
}

function showSplitMenu(x, y, tabKey, leafId) {
  closeSplitMenu();
  const leaf = getNode(rootNode, leafId);
  if (!leaf || leaf.tabs.length < 2) return;
  menuEl = document.createElement('div');
  menuEl.className = 'dk-menu';
  menuEl.style.left = x + 'px';
  menuEl.style.top = y + 'px';
  const items = [
    ['left', 'Nach links teilen'],
    ['right', 'Nach rechts teilen'],
    ['top', 'Nach oben teilen'],
    ['bottom', 'Nach unten teilen'],
  ];
  for (const [edge, label] of items) {
    const item = document.createElement('button');
    item.type = 'button';
    item.className = 'dk-menu-item';
    item.textContent = label;
    item.addEventListener('click', () => {
      closeSplitMenu();
      applySplit(tabKey, leafId, edge);
    });
    menuEl.appendChild(item);
  }
  document.body.appendChild(menuEl);
  requestAnimationFrame(() => {
    window.addEventListener('click', closeSplitMenu, { once: true });
    window.addEventListener('contextmenu', closeSplitMenu, { once: true });
  });
}

function bindLeafEvents(leafId, tabsEl, bodyEl) {
  tabsEl.addEventListener('click', e => {
    const tabEl = e.target.closest('.dk-tab');
    if (!tabEl) return;
    if (e.target.closest('.dk-x')) { closeDockTab(tabEl.dataset.key); return; }
    focusedLeafId = leafId;
    activate(tabEl.dataset.key);
  });
  tabsEl.addEventListener('mousedown', e => {
    if (e.button === 1) e.preventDefault();
  });
  tabsEl.addEventListener('auxclick', e => {
    if (e.button !== 1) return;
    const tabEl = e.target.closest('.dk-tab');
    if (!tabEl) return;
    e.preventDefault();
    closeDockTab(tabEl.dataset.key);
  });
  tabsEl.addEventListener('contextmenu', e => {
    const tabEl = e.target.closest('.dk-tab');
    if (!tabEl) return;
    e.preventDefault();
    showSplitMenu(e.clientX, e.clientY, tabEl.dataset.key, leafId);
  });
  tabsEl.addEventListener('dragstart', e => {
    const tabEl = e.target.closest('.dk-tab');
    if (!tabEl) return;
    dragTab = tabEl.dataset.key;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', dragTab);
  });
  tabsEl.addEventListener('dragend', () => { dragTab = null; clearDropOverlay(); });

  bodyEl.addEventListener('dragover', e => {
    if (!dragTab) return;
    e.preventDefault();
    showDropOverlay(bodyEl, e.clientX, e.clientY);
  });
  bodyEl.addEventListener('dragleave', e => {
    if (e.target === bodyEl) clearDropOverlay();
  });
  bodyEl.addEventListener('drop', e => {
    if (!dragTab) return;
    e.preventDefault();
    const edge = edgeForPoint(bodyEl, e.clientX, e.clientY);
    clearDropOverlay();
    if (edge) applySplit(dragTab, leafId, edge);
    dragTab = null;
  });
}

function buildLeafDom(leafId) {
  const rootEl = document.createElement('div');
  rootEl.className = 'dk-leaf';

  const bar = document.createElement('div');
  bar.className = 'dk-bar';
  const tabsEl = document.createElement('div');
  tabsEl.className = 'dk-tabs';
  bar.appendChild(tabsEl);

  const bodyEl = document.createElement('div');
  bodyEl.className = 'dk-body';

  const empty = document.createElement('div');
  empty.className = 'dk-empty';
  const emptyIcon = document.createElement('div');
  emptyIcon.className = 'dk-empty-icon';
  emptyIcon.setAttribute('aria-hidden', 'true');
  emptyIcon.innerHTML = developerIcon('bash');
  const emptyCopy = document.createElement('div');
  emptyCopy.className = 'dk-empty-copy';
  const emptyText = document.createElement('strong');
  emptyText.textContent = 'Noch kein Terminal';
  const emptyDetail = document.createElement('span');
  emptyDetail.textContent = 'Starte eine Shell im aktuellen Projekt.';
  emptyCopy.append(emptyText, emptyDetail);
  const emptyBtn = document.createElement('button');
  emptyBtn.className = 'dk-empty-btn';
  emptyBtn.type = 'button';
  emptyBtn.textContent = 'Terminal starten';
  emptyBtn.addEventListener('click', spawnTerminal);
  const hint = document.createElement('div');
  hint.className = 'dk-hint';
  hint.textContent = '⌃` ein-/ausblenden';
  empty.append(emptyIcon, emptyCopy, emptyBtn, hint);
  bodyEl.appendChild(empty);

  rootEl.append(bar, bodyEl);
  bindLeafEvents(leafId, tabsEl, bodyEl);

  return { rootEl, tabsEl, bodyEl };
}

function reconcileLeafDom(leaf) {
  let dom = leafDom.get(leaf.id);
  if (!dom) {
    dom = buildLeafDom(leaf.id);
    leafDom.set(leaf.id, dom);
  }
  for (const ref of leaf.tabs) {
    const t = tabs.get(dockRefKey(ref));
    if (!t) continue;
    t.leafId = leaf.id;
    dom.tabsEl.appendChild(t.el);
    dom.bodyEl.appendChild(t.pane);
    const on = dockRefKey(ref) === leaf.activeKey;
    t.el.classList.toggle('dk-active', on);
    t.pane.classList.toggle('dk-on', on);
    if (on && open) ensureLive(t);
  }
  return dom.rootEl;
}

function startSplitDrag(e, splitId) {
  if (e.button !== 0) return;
  e.preventDefault();
  const dom = nodeDom.get(splitId);
  if (!dom) return;
  const node = getNode(rootNode, splitId);
  const horizontal = node.dir === 'row';
  document.body.classList.add('dk-dragging-split', horizontal ? 'dk-dragging-split-row' : 'dk-dragging-split-column');
  let raf = 0;
  let ratio = node.ratio;
  const move = ev => {
    const box = dom.rootEl.getBoundingClientRect();
    ratio = horizontal ? (ev.clientX - box.left) / box.width : (ev.clientY - box.top) / box.height;
    if (raf) return;
    raf = requestAnimationFrame(() => {
      raf = 0;
      rootNode = resizeSplit(rootNode, splitId, ratio);
      const n = getNode(rootNode, splitId);
      dom.aEl.style.flexGrow = String(n.ratio);
      dom.bEl.style.flexGrow = String(1 - n.ratio);
      scheduleFit();
    });
  };
  const up = () => {
    window.removeEventListener('mousemove', move, true);
    window.removeEventListener('mouseup', up, true);
    if (raf) cancelAnimationFrame(raf);
    document.body.classList.remove('dk-dragging-split', 'dk-dragging-split-row', 'dk-dragging-split-column');
    persist();
    notifyLayout();
  };
  window.addEventListener('mousemove', move, true);
  window.addEventListener('mouseup', up, true);
}

function renderNode(node) {
  if (node.type === 'leaf') return reconcileLeafDom(node);

  let dom = nodeDom.get(node.id);
  if (!dom) {
    const rootEl = document.createElement('div');
    rootEl.className = 'dk-split';
    const aEl = document.createElement('div');
    aEl.className = 'dk-split-pane';
    const grip = document.createElement('div');
    grip.className = 'dk-split-grip';
    grip.addEventListener('mousedown', e => startSplitDrag(e, node.id));
    const bEl = document.createElement('div');
    bEl.className = 'dk-split-pane';
    rootEl.append(aEl, grip, bEl);
    dom = { rootEl, aEl, grip, bEl };
    nodeDom.set(node.id, dom);
  }
  dom.rootEl.classList.toggle('dk-split-row', node.dir === 'row');
  dom.rootEl.classList.toggle('dk-split-column', node.dir === 'column');
  dom.aEl.style.flexGrow = String(node.ratio);
  dom.bEl.style.flexGrow = String(1 - node.ratio);

  const aChild = renderNode(node.a);
  if (aChild.parentElement !== dom.aEl) dom.aEl.appendChild(aChild);
  const bChild = renderNode(node.b);
  if (bChild.parentElement !== dom.bEl) dom.bEl.appendChild(bChild);

  return dom.rootEl;
}

function renderTree() {
  const seen = new Set();
  (function mark(node) {
    seen.add(node.id);
    if (node.type === 'split') { mark(node.a); mark(node.b); }
  })(rootNode);
  for (const [id, dom] of [...nodeDom]) {
    if (!seen.has(id)) { dom.rootEl.remove(); nodeDom.delete(id); }
  }
  for (const [id, dom] of [...leafDom]) {
    if (!seen.has(id)) { dom.rootEl.remove(); leafDom.delete(id); }
  }
  const top = renderNode(rootNode);
  if (top.parentElement !== treeEl) treeEl.appendChild(top);
  updateBlank();
}

function startDrag(e) {
  if (e.button !== 0) return;
  e.preventDefault();
  document.body.classList.add('dk-dragging');
  let raf = 0;
  let target = height;
  const move = ev => {
    target = window.innerHeight - ev.clientY;
    if (raf) return;
    raf = requestAnimationFrame(() => {
      raf = 0;
      height = clampHeight(target);
      applyHeight();
      notifyLayout();
    });
  };
  const up = () => {
    window.removeEventListener('mousemove', move, true);
    window.removeEventListener('mouseup', up, true);
    if (raf) cancelAnimationFrame(raf);
    document.body.classList.remove('dk-dragging');
    applyHeight();
    persist();
    notifyLayout();
  };
  window.addEventListener('mousemove', move, true);
  window.addEventListener('mouseup', up, true);
}

function buildDom() {
  dockEl = document.createElement('div');
  dockEl.className = 'dk-dock dk-blank';
  dockEl.id = 'dk-dock';

  const grip = document.createElement('div');
  grip.className = 'dk-resize';
  grip.title = 'Höhe ziehen — Doppelklick setzt zurück';
  grip.addEventListener('mousedown', startDrag);
  grip.addEventListener('dblclick', () => {
    height = DEFAULT_HEIGHT;
    applyHeight();
    persist();
    notifyLayout();
  });

  topBarEl = document.createElement('div');
  topBarEl.className = 'dk-bar dk-topbar';

  emptyTitleEl = document.createElement('div');
  emptyTitleEl.className = 'dk-empty-title';
  emptyTitleEl.innerHTML = `${developerIcon('bash')}<span>Terminal-Dock</span>`;

  const actions = document.createElement('div');
  actions.className = 'dk-actions';

  const plus = document.createElement('button');
  plus.className = 'dk-btn';
  plus.type = 'button';
  plus.innerHTML = dockIcon('plus');
  plus.setAttribute('aria-label', 'Neues Terminal starten');
  plus.title = 'Neues Terminal';
  plus.addEventListener('click', spawnTerminal);

  const hide = document.createElement('button');
  hide.className = 'dk-btn';
  hide.type = 'button';
  hide.innerHTML = dockIcon('down');
  hide.setAttribute('aria-label', 'Terminal-Dock einklappen');
  hide.title = 'Dock einklappen (⌃`)';
  hide.addEventListener('click', () => toggleDock(false));

  actions.append(plus, hide);
  topBarEl.append(emptyTitleEl, actions);

  treeEl = document.createElement('div');
  treeEl.className = 'dk-tree';

  dockEl.append(grip, topBarEl, treeEl);
  document.body.appendChild(dockEl);
  renderTree();
}

function onWindowResize() {
  if (selfResize) return;
  applyHeight();
  scheduleFit();
}

function onKeyDown(e) {
  if (isToggleKey(e)) {
    e.preventDefault();
    e.stopPropagation();
    toggleDock();
    return;
  }
  if (isTabNavKey(e)) {
    if (!open) return;
    e.preventDefault();
    e.stopPropagation();
    stepTab(e.key === 'ArrowRight' ? 1 : -1);
  }
}

export function mountDock(opts) {
  if (mounted) return;
  mounted = true;
  cb = opts || {};

  const saved = readState();
  height = saved?.height ?? DEFAULT_HEIGHT;
  open = !!saved?.open;

  applyHeight();
  applyOpen();
  buildDom();

  const restore = async () => {
    let layout = saved?.layout || createLeaf();
    const allRefs = listLeaves(layout).flatMap(l => l.tabs);
    const legacyNames = allRefs.filter(ref => !ref.id).map(ref => ref.name);
    if (legacyNames.length && cb.migrateLegacy) {
      try {
        const resolved = resolveLegacyDockRefs(allRefs, await cb.migrateLegacy(legacyNames));
        layout = createLeaf(resolved, resolved[0] ? dockRefKey(resolved[0]) : null);
      } catch { /* bei transientem Registry-Fehler bleibt der explizite Legacy-Pfad erhalten */ }
    }
    rootNode = layout;
    for (const leaf of listLeaves(rootNode)) {
      for (const ref of leaf.tabs) addTab(ref);
    }
    focusedLeafId = listLeaves(rootNode)[0].id;
    renderTree();

    const requestedKey = typeof saved?.focused === 'string' ? saved.focused : null;
    const target = requestedKey && tabs.has(requestedKey) ? tabs.get(requestedKey) : null;
    if (target) {
      focusedLeafId = target.leafId;
      activate(target.key);
    } else {
      const first = listLeaves(rootNode)[0];
      if (first.activeKey) activate(first.activeKey);
      else persist();
    }
  };
  restore();

  window.addEventListener('resize', onWindowResize);
  window.addEventListener('keydown', onKeyDown, true);
  setInterval(() => {
    if (open && !document.hidden) updateStatuses();
  }, 3000);

  if (open) requestAnimationFrame(notifyLayout);
}

export function toggleDock(next) {
  if (!mounted) return;
  const want = next === undefined ? !open : !!next;
  if (want === open) return;
  open = want;
  applyOpen();
  persist();
  notifyLayout();
  if (!open) return;
  const leaf = focusedLeaf();
  if (leaf.activeKey) activate(leaf.activeKey);
  else updateStatuses();
}

export function isDockOpen() {
  return open;
}

function openDockTab(value) {
  const ref = normalizeDockRef(value);
  const key = dockRefKey(ref);
  if (!mounted || !ref || !key) return;
  if (!tabs.has(key)) {
    addTab(ref);
    const leaf = focusedLeaf();
    rootNode = addTabToLeaf(rootNode, leaf.id, ref);
  }
  if (!open) {
    open = true;
    applyOpen();
    notifyLayout();
  }
  renderTree();
  activate(key);
}

export function closeDockTab(value) {
  const key = typeof value === 'string' && tabs.has(value) ? value : dockRefKey(value);
  const t = tabs.get(key);
  if (!t) return;

  const leaf = getNode(rootNode, t.leafId);
  const order = leaf ? leaf.tabs.map(dockRefKey) : [];
  const i = order.indexOf(key);
  const wasFocused = focusedLeafId === t.leafId;

  tabs.delete(key);
  rootNode = removeTab(rootNode, key);

  try { t.offData?.(); } catch { /* Listener war schon weg */ }
  try { t.offClosed?.(); } catch { /* Listener war schon weg */ }
  if (t.live) {
    try { cb.close?.(t.ref); } catch { /* Backend hat die Sitzung evtl. selbst beendet */ }
  }
  try { t.term?.dispose(); } catch { /* bereits entsorgt */ }
  t.el.remove();
  t.pane.remove();

  renderTree();

  if (!wasFocused) { persist(); return; }

  const stillThere = getNode(rootNode, t.leafId);
  if (stillThere) {
    focusedLeafId = t.leafId;
    const rest = order.filter(k => k !== key);
    const next = rest[i] || rest[i - 1] || null;
    if (next) { activate(next); return; }
    persist();
    return;
  }
  const first = listLeaves(rootNode)[0];
  focusedLeafId = first.id;
  if (first.activeKey) activate(first.activeKey);
  else persist();
}

export function dockTabs() {
  return [...tabs.values()].map(tab => ({ ...tab.ref }));
}

export function refitDock() {
  if (!mounted) return;
  applyHeight();
  scheduleFit();
}
```

- [ ] **Step 2: Extend `app/frontend/src/dock.css`**

Replace the `.dk-bar` block (previously the single global tab bar) and everything from `.dk-body` down to (not including) `.dk-hint` with the following, and append the split/drag/menu rules at the end of the file. The `.dk-bar`/`.dk-tabs`/`.dk-tab`/`.dk-dot`/`.dk-tool`/`.dk-name`/`.dk-x`/`.dk-actions`/`.dk-btn`/`.dk-ico`/`.dk-empty-title` rules are reused unchanged — only the structural rules around them change.

```css
/* Replace the block starting at ".dk-bar {" through ".dk-tabs::-webkit-scrollbar { height: 0; }" — keep both rules verbatim, they're reused by the per-leaf bar and the topbar. */

.dk-topbar .dk-empty-title { flex: 1; }

.dk-dock.dk-blank .dk-leaf .dk-bar { display: none; }

.dk-tree {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
}

.dk-leaf {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.dk-split {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
}

.dk-split-row { flex-direction: row; }
.dk-split-column { flex-direction: column; }

.dk-split-pane {
  min-width: 0;
  min-height: 0;
  flex-basis: 0;
  overflow: hidden;
  display: flex;
}

.dk-split-pane > * { flex: 1; min-width: 0; min-height: 0; }

.dk-split-grip {
  flex: none;
  position: relative;
  z-index: 2;
}

.dk-split-row > .dk-split-grip { width: 7px; margin: 0 -3px; cursor: ew-resize; }
.dk-split-column > .dk-split-grip { height: 7px; margin: -3px 0; cursor: ns-resize; }

.dk-split-grip::after {
  content: '';
  position: absolute;
  background: transparent;
  transition: background .12s ease;
}
.dk-split-row > .dk-split-grip::after { left: 3px; top: 0; bottom: 0; width: 1px; }
.dk-split-column > .dk-split-grip::after { top: 3px; left: 0; right: 0; height: 1px; }

.dk-split-grip:hover::after,
body.dk-dragging-split .dk-split-grip::after { background: var(--accent); }

body.dk-dragging-split { user-select: none; }
body.dk-dragging-split-row { cursor: ew-resize; }
body.dk-dragging-split-column { cursor: ns-resize; }

.dk-tab { cursor: grab; }
.dk-tab:active { cursor: grabbing; }

.dk-drop-overlay {
  position: absolute;
  inset: 0;
  background: color-mix(in srgb, var(--accent) 16%, transparent);
  border: 2px solid var(--accent);
  pointer-events: none;
  z-index: 5;
}

.dk-drop-overlay.dk-drop-hidden { display: none; }
.dk-drop-overlay.dk-drop-left { clip-path: inset(0 75% 0 0); }
.dk-drop-overlay.dk-drop-right { clip-path: inset(0 0 0 75%); }
.dk-drop-overlay.dk-drop-top { clip-path: inset(0 0 75% 0); }
.dk-drop-overlay.dk-drop-bottom { clip-path: inset(75% 0 0 0); }

.dk-menu {
  position: fixed;
  z-index: 20;
  display: flex;
  flex-direction: column;
  min-width: 170px;
  padding: 4px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 8px 24px var(--shadow);
}

.dk-menu-item {
  padding: 7px 10px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--ink);
  font-size: var(--fs-label);
  font-family: inherit;
  text-align: left;
  cursor: pointer;
}

.dk-menu-item:hover { background: var(--raise); }
```

Concretely: keep `.dk-dock`, `.dk-resize` and its variants, `.dk-bar`, `.dk-tabs`, `.dk-tabs::-webkit-scrollbar`, `.dk-empty-title` (and its `.dk-dock:not(.dk-blank) .dk-empty-title` rule), `.dk-tab` through `.dk-x:hover`, `.dk-actions`, `.dk-btn`, `.dk-ico`, `.dk-pane`/`.dk-term`/xterm rules, `.dk-empty*` rules, `.dk-hint`, `body.dk-dragging`, and the media queries exactly as they are today. Remove nothing except relying on the fact that `.dk-body { position: relative; flex: 1; min-height: 0; background: var(--term-bg); }` now applies to a per-leaf `.dk-body` instead of the single global one (no CSS change needed there). Insert the new rules above anywhere after `.dk-empty-title` and before `body.dk-dragging`.

- [ ] **Step 3: Run the full frontend test suite**

Run: `cd app/frontend && npm test`
Expected: PASS (all files, including `dock-tree.test.js` and `dock-state.test.js`)

- [ ] **Step 4: Manual QA in the running app**

Run: `cd app/frontend && npm run dev` (or launch the full Wails app if backend calls are needed for real terminals) and verify:

1. Öffne das Dock, starte zwei Terminals — beide Tabs erscheinen im selben Tab-Streifen wie bisher.
2. Ziehe einen Tab auf den rechten Rand des Pane-Bereichs → Drop-Overlay erscheint rechts, Drop erzeugt zwei nebeneinanderliegende Panes, der gezogene Tab landet rechts, beide Terminals bleiben funktionsfähig (Tippen, Scrollback erhalten).
3. Rechtsklick auf einen Tab in einem Pane mit ≥2 Tabs → Menü mit vier Split-Richtungen erscheint; "Nach unten teilen" erzeugt eine vertikale Aufteilung.
4. Verschachtele einen weiteren Split innerhalb eines bestehenden Split-Panes (Split-in-Split) → Layout bleibt korrekt, alle Terminals bleiben interaktiv.
5. Ziehe einen Split-Grip → Verhältnis ändert sich live, beide Terminals passen ihre Größe an (`fitNow` pro sichtbarem Pane).
6. Schließe den letzten Tab in einem Split-Pane → das Pane verschwindet, das Nachbar-Pane füllt den Platz, Fokus wandert dorthin.
7. Schließe die App und starte sie neu (oder lade die Wails-Dev-Ansicht neu) → das gespeicherte Split-Layout samt Panes und Tabs wird korrekt wiederhergestellt.
8. Test einer alten `localStorage`-Version: In der DevTools-Konsole `localStorage.setItem('magentic.dock', JSON.stringify({open:true,height:280,tabs:[{id:'x',name:'alt-tab'}],active:'session:x'}))`, Seite neu laden → Migration zu einem Einzel-Blatt-Layout funktioniert, kein Fehler in der Konsole.
9. `⌘⌥←/→` navigiert weiterhin nur innerhalb des zuletzt fokussierten Panes zwischen dessen Tabs.
10. `⌃\`` blendet das Dock weiterhin ein/aus, unabhängig davon wie viele Panes offen sind.

- [ ] **Step 5: Commit**

```bash
git add app/frontend/src/dock.js app/frontend/src/dock.css
git commit -m "feat: Split View im Terminal-Dock per Drag & Drop und Kontextmenü"
```
