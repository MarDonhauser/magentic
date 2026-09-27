import test from 'node:test';
import assert from 'node:assert/strict';

import { clearTextureAtlases, trackTerminal, trackedTerminals } from './texture-atlas.js';

function fakeTerminal({ connected = true, fails = false } = {}) {
  const term = { element: { isConnected: connected }, clears: 0 };
  term.clearTextureAtlas = () => {
    if (fails) throw new Error('disposed');
    term.clears++;
  };
  return term;
}

test('Ein Atlas-Reset trifft jedes lebende Terminal, sonst zeigen die anderen falsche Glyphen', () => {
  // Der WebGL-Renderer teilt den Glyphen-Atlas zwischen Terminals gleicher
  // Konfiguration. Leert nur eines ihn, verweisen die Zeichenmodelle der
  // anderen auf Atlas-Plätze, die inzwischen andere Glyphen tragen.
  const a = fakeTerminal();
  const b = fakeTerminal();
  trackTerminal(a);
  trackTerminal(b);
  trackTerminal(b);
  clearTextureAtlases();
  assert.equal(a.clears, 1);
  assert.equal(b.clears, 1);
});

test('Entsorgte Terminals fallen beim Reset aus der Liste', () => {
  const before = trackedTerminals();
  const gone = fakeTerminal({ connected: false });
  const broken = fakeTerminal({ fails: true });
  const alive = fakeTerminal();
  trackTerminal(gone);
  trackTerminal(broken);
  trackTerminal(alive);
  clearTextureAtlases();
  assert.equal(alive.clears, 1);
  assert.equal(trackedTerminals(), before + 1);
  trackTerminal(null);
  assert.equal(trackedTerminals(), before + 1);
});
