import test from 'node:test';
import assert from 'node:assert/strict';

import { HYDRA_MAX_TILES, hydraColumns } from './hydra-layout.js';

test('Ein einzelnes Terminal bekommt die ganze Breite', () => {
  assert.equal(hydraColumns(1, 3000), 1);
  assert.equal(hydraColumns(0, 3000), 1);
});

test('Bis vier Terminals stehen in zwei Spalten, bis neun in drei, darüber in vier', () => {
  assert.equal(hydraColumns(2, 3000), 2);
  assert.equal(hydraColumns(4, 3000), 2);
  assert.equal(hydraColumns(5, 3000), 3);
  assert.equal(hydraColumns(9, 3000), 3);
  assert.equal(hydraColumns(10, 3000), 4);
  assert.equal(hydraColumns(HYDRA_MAX_TILES, 3000), 4);
});

test('Die Breite begrenzt die Spalten, damit kein Terminal unter die Mindestbreite fällt', () => {
  assert.equal(hydraColumns(6, 1740), 3);
  assert.equal(hydraColumns(6, 1200), 2);
  assert.equal(hydraColumns(6, 600), 1);
  assert.equal(hydraColumns(12, 1740), 3);
});
