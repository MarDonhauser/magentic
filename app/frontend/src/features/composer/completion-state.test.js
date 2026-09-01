import test from 'node:test';
import assert from 'node:assert/strict';
import { completionTrigger, applyCompletion } from './completion-state.js';

test('@ öffnet das Dateimenü an einer Wortgrenze', () => {
  const trigger = completionTrigger('lies @core/tm', 13);
  assert.deepEqual(trigger, { kind: 'file', query: 'core/tm', start: 5 });
});

test('@ mitten in einem Wort löst nicht aus', () => {
  assert.equal(completionTrigger('mail@example', 12), null);
});

test('/ löst nur als erstes Zeichen der Nachricht aus', () => {
  assert.deepEqual(completionTrigger('/rev', 4), { kind: 'command', query: 'rev', start: 0 });
  assert.equal(completionTrigger('bitte /rev', 10), null);
});

test('ein Leerzeichen nach dem Auslöser schliesst das Menü', () => {
  assert.equal(completionTrigger('lies @core ', 11), null);
  assert.equal(completionTrigger('/review jetzt', 13), null);
});

test('ohne Auslöser gibt es kein Menü', () => {
  assert.equal(completionTrigger('einfacher Text', 14), null);
  assert.equal(completionTrigger('', 0), null);
});

test('der Auslöser richtet sich nach der Schreibmarke, nicht nach dem Ende', () => {
  assert.deepEqual(completionTrigger('@core und mehr', 5), { kind: 'file', query: 'core', start: 0 });
});

test('Übernehmen ersetzt nur den Auslöserbereich und setzt die Marke dahinter', () => {
  const trigger = completionTrigger('lies @core/tm', 13);
  const result = applyCompletion('lies @core/tm', trigger, 'core/tmux.go');
  assert.equal(result.text, 'lies @core/tmux.go ');
  assert.equal(result.caret, 19);
});

test('Übernehmen eines Befehls behält den Rest der Zeile', () => {
  const trigger = completionTrigger('/rev danach', 4);
  const result = applyCompletion('/rev danach', trigger, 'review');
  assert.equal(result.text, '/review danach');
  assert.equal(result.caret, 8);
});
