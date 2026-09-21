const live = new Set();

export function trackTerminal(term) {
  if (term) live.add(term);
}

export function trackedTerminals() {
  return live.size;
}

export function clearTextureAtlases() {
  for (const term of live) {
    if (!term.element || term.element.isConnected === false) {
      live.delete(term);
      continue;
    }
    try {
      term.clearTextureAtlas?.();
    } catch {
      live.delete(term);
    }
  }
}
