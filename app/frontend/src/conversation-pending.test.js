import test from 'node:test';
import assert from 'node:assert/strict';

import {
  addPending, applyReading, applyUpdate, reconcilePending, renderModel, rowSignature,
} from './conversation-state.js';

const prompt = (id, text) => ({ id, role: 'developer', kind: 'developer-prompt', label: 'Eingabe', title: text, detail: '' });
const reading = items => applyReading({ availability: 'available', itemsKnown: true, items });
const pendingRows = model => model.rows.filter(row => row.pending);

test('Eine gesendete Nachricht steht sofort als Eingabe am Ende, bis die Aufzeichnung sie zeigt', () => {
  let state = addPending(reading([prompt('u1', 'erste Frage')]), { id: 'p1', text: 'bau die app neu', sentAt: 1000 });
  let model = renderModel(state);
  assert.equal(model.kind, 'items');
  assert.deepEqual(model.rows.map(row => row.id), ['u1', 'pending:p1']);
  assert.equal(pendingRows(model)[0].pendingState, 'sent');
  assert.equal(pendingRows(model)[0].title, 'bau die app neu');

  state = reconcilePending(applyUpdate(state, { items: [prompt('u2', 'bau die app neu')] }), 2000);
  model = renderModel(state);
  assert.deepEqual(model.rows.map(row => row.id), ['u1', 'u2']);
});

test('Eine gleichlautende Eingabe von vorher löst die gesendete Nachricht nicht auf', () => {
  const state = addPending(reading([prompt('u1', 'nochmal')]), { id: 'p1', text: 'nochmal', sentAt: 1000 });
  assert.equal(reconcilePending(state, 2000).pending.length, 1);
  const later = reconcilePending(applyUpdate(state, { items: [prompt('u2', 'nochmal')] }), 2000);
  assert.equal(later.pending.length, 0);
});

test('Solange die Outbox die Nachricht führt, liest sie als wartend, bei ungewisser Zustellung als ungewiss', () => {
  const text = 'Bitte prüfe die Tests und melde dich, wenn alles grün ist.\nDanach committen.';
  const state = addPending(reading([]), { id: 'p1', text, sentAt: 1000 });
  const queued = [{ id: 'q1', kind: 'message', text: 'Bitte prüfe die Tests und melde dich, wenn alles grün ist. Danach committen.', stuck: false }];
  let row = pendingRows(renderModel(state, { queued }))[0];
  assert.equal(row.id, 'pending:p1');
  assert.equal(row.pendingState, 'queued');
  assert.equal(row.detail, text);
  row = pendingRows(renderModel(state, { queued: [{ ...queued[0], stuck: true }] }))[0];
  assert.equal(row.pendingState, 'stuck');
  assert.equal(pendingRows(renderModel(state, { queued: [] }))[0].pendingState, 'sent');
});

test('Eine eingereihte Nachricht ohne lokale Kopie erscheint aus ihrer Vorschau', () => {
  const queued = [{ id: 'q9', kind: 'message', text: 'Aus einer anderen Session eingereiht…', stuck: false }];
  const model = renderModel(reading([prompt('u1', 'hallo')]), { queued });
  assert.deepEqual(model.rows.map(row => row.id), ['u1', 'queued:q9']);
  assert.equal(model.rows[1].pendingState, 'queued');
  assert.equal(model.rows[1].title, 'Aus einer anderen Session eingereiht…');
});

test('Gesendete Nachrichten stehen vor noch wartenden, und eine zu alte wird verworfen', () => {
  let state = addPending(reading([]), { id: 'old', text: 'längst zugestellt', sentAt: 0 });
  state = addPending(state, { id: 'new', text: 'wartet noch', sentAt: 5 * 60 * 1000 });
  const queued = [{ id: 'q1', kind: 'message', text: 'wartet noch', stuck: false }];
  assert.deepEqual(renderModel(state, { queued }).rows.map(row => `${row.id}:${row.pendingState}`), ['pending:old:sent', 'pending:new:queued']);
  const expired = reconcilePending(state, 31 * 60 * 1000);
  assert.deepEqual(expired.pending.map(entry => entry.id), ['new']);
});

test('Die Zeilensignatur ändert sich mit dem Zustellstand', () => {
  const state = addPending(reading([]), { id: 'p1', text: 'hi', sentAt: 1000 });
  const queued = [{ id: 'q1', kind: 'message', text: 'hi', stuck: false }];
  const waiting = renderModel(state, { queued }).rows[0];
  const sent = renderModel(state, { queued: [] }).rows[0];
  assert.notEqual(rowSignature(waiting, new Set()), rowSignature(sent, new Set()));
});
