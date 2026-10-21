import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { isDragRegion, installDragRegionZoom } from './window-zoom.js';

const styleOf = (el) => ({ getPropertyValue: () => el.drag });

function fakeTarget() {
  const listeners = {};
  return {
    addEventListener: (type, fn) => { listeners[type] = fn; },
    fire: (type, e) => listeners[type](e),
  };
}

test('Nur Flächen mit --wails-draggable: drag gelten als Titelleiste', () => {
  assert.equal(isDragRegion({ drag: ' drag' }, styleOf), true);
  assert.equal(isDragRegion({ drag: 'no-drag' }, styleOf), false);
  assert.equal(isDragRegion({ drag: '' }, styleOf), false);
  assert.equal(isDragRegion(null, styleOf), false);
});

test('Doppelklick auf eine Zieh-Fläche schaltet das Fenster groß', () => {
  const target = fakeTarget();
  let toggles = 0;
  installDragRegionZoom(target, styleOf, () => toggles++);
  const drag = { drag: 'drag' };

  target.fire('mousedown', { button: 0, detail: 1, target: drag });
  assert.equal(toggles, 0);
  target.fire('mousedown', { button: 0, detail: 2, target: drag });
  assert.equal(toggles, 1);
  target.fire('mousedown', { button: 0, detail: 2, target: { drag: 'no-drag' } });
  target.fire('mousedown', { button: 2, detail: 2, target: drag });
  assert.equal(toggles, 1);
});

test('main.js hängt den Doppelklick ans Fenster', async () => {
  const main = await readFile(new URL('./main.js', import.meta.url), 'utf8');
  assert.match(main, /installDragRegionZoom\(window, getComputedStyle, WindowToggleMaximise\)/);
});
