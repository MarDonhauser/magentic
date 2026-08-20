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

export function normalizeDockState(raw, defaultHeight) {
  if (!raw || typeof raw !== 'object') return null;
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
  return {
    open: !!raw.open,
    height: Number.isFinite(raw.height) ? raw.height : defaultHeight,
    tabs,
    active: activeRef ? dockRefKey(activeRef) : null,
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
