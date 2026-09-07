import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const app = await readFile(new URL('../wailsjs/go/main/App.js', import.meta.url), 'utf8');
const main = await readFile(new URL('./main.js', import.meta.url), 'utf8');

function wailsjsExports(source) {
  const names = new Set();
  for (const match of source.matchAll(/export function ([A-Za-z0-9]+)\(/g)) {
    names.add(match[1]);
  }
  return names;
}

function wailsjsImports(source) {
  const block = source.match(/import \{([^}]*)\} from '\.\.\/wailsjs\/go\/main\/App'/)?.[1] ?? '';
  return block
    .split(',')
    .map(name => name.trim())
    .filter(Boolean);
}

// Jeder Klick im Frontend landet auf einem Binding: Ein importierter, aber
// nicht gebundener Name stirbt erst zur Laufzeit mit einem TypeError.
test('main.js ruft nur gebundene App-Methoden auf', () => {
  const exported = wailsjsExports(app);
  assert.ok(exported.size > 0, 'keine Bindings in App.js gefunden');
  for (const name of wailsjsImports(main)) {
    assert.ok(exported.has(name), `${name} ist importiert, aber nicht gebunden`);
  }
});
