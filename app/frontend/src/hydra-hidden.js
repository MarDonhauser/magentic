export const HYDRA_HIDDEN_KEY = 'magentic.hydraHidden';

function browserStorage() {
  try { return globalThis.localStorage || null; } catch { return null; }
}

export function createHydraHidden(storage = browserStorage()) {
  const ids = new Set();
  try {
    const stored = JSON.parse(storage?.getItem(HYDRA_HIDDEN_KEY) || '[]');
    if (Array.isArray(stored)) for (const id of stored) if (typeof id === 'string' && id) ids.add(id);
  } catch { /* unlesbarer Eintrag — dann ist nichts ausgeblendet */ }

  const save = () => {
    try { storage?.setItem(HYDRA_HIDDEN_KEY, JSON.stringify([...ids])); } catch { /* Speicher gesperrt — gilt dann nur bis zum Neustart */ }
  };

  return {
    has(id) {
      return ids.has(String(id ?? ''));
    },
    hide(id) {
      const key = String(id ?? '');
      if (!key || ids.has(key)) return;
      ids.add(key);
      save();
    },
    show(id) {
      if (ids.delete(String(id ?? ''))) save();
    },
  };
}
