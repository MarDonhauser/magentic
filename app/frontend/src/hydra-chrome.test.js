import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const main = await readFile(new URL('./main.js', import.meta.url), 'utf8');
const css = await readFile(new URL('./style.css', import.meta.url), 'utf8');

function functionSource(source, name) {
  const start = source.indexOf(`function ${name}(`);
  assert.notEqual(start, -1, `${name} fehlt`);
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}' && --depth === 0) return source.slice(start, i + 1);
  }
  throw new Error(`${name} ist nicht geschlossen`);
}

test('Der Hydra-Modus schaltet die Titelleiste ab und jeder Ausgang schaltet sie wieder ein', () => {
  assert.ok(functionSource(main, 'enterHydra').includes('setHydraChrome(true)'));
  assert.ok(functionSource(main, 'leaveTerm').includes('setHydraChrome(false)'));
  assert.ok(functionSource(main, 'openSession').includes('setHydraChrome(false)'));
  assert.equal(main.includes("termsEl.classList.add('hydra')"), false, 'die Klasse wird nur über setHydraChrome gesetzt');
  assert.equal(main.includes("termsEl.classList.remove('hydra')"), false, 'die Klasse wird nur über setHydraChrome entfernt');
});

test('Ohne Titelleiste bleibt das Fenster über Seitenleiste und Hydra-Leiste ziehbar', () => {
  assert.match(css, /body\.hydra #titlebar \{ display: none; \}/);
  assert.match(css, /#sidebar-drag \{[^}]*--wails-draggable: drag/);
  assert.match(css, /#terms\.hydra #term-bar \{[^}]*--wails-draggable: drag/);
  assert.match(css, /#terms\.hydra #term-bar \.btn \{ --wails-draggable: no-drag; \}/);
});
