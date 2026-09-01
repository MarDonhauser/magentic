import test from 'node:test';
import assert from 'node:assert/strict';
import { promptCoverRows } from './prompt-cover.js';

const claudePane = [
  'irgendeine Ausgabe',
  '────────────────',
  '❯ ',
  '────────────────',
  '                /rc',
  '  ⏵⏵ auto mode on',
];

test('verdeckt von der Zeile über dem Prompt bis zum unteren Rand', () => {
  assert.equal(promptCoverRows(claudePane, '❯'), 5);
});

test('ohne Muster wird nichts verdeckt', () => {
  assert.equal(promptCoverRows(claudePane, ''), 0);
});

test('ein nicht gefundenes Muster verdeckt nichts', () => {
  assert.equal(promptCoverRows(claudePane, '>>>'), 0);
});

test('die unterste Prompt-Zeile gewinnt', () => {
  const lines = ['❯ alt', 'Ausgabe', '────', '❯ neu', '────'];
  assert.equal(promptCoverRows(lines, '❯'), 3);
});

test('ein leerer Puffer verdeckt nichts', () => {
  assert.equal(promptCoverRows([], '❯'), 0);
});
