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
    focused: typeof raw.focused === 'string' ? raw.focused : null,
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
