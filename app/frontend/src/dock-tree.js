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
