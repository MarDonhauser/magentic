import test from 'node:test';
import assert from 'node:assert/strict';

import {
  deliveryLabel,
  excerptLabel,
  inboxEntries,
  inboxHeadline,
  inboxState,
  waitingKindLabel,
  waitingTimeLabel,
} from './inbox-state.js';

test('the planned order survives the projection', () => {
  const entries = inboxEntries({
    state: 'complete',
    entries: [
      { sessionId: 'b', session: 'docs', kind: 'review' },
      { sessionId: 'a', session: 'auth-fix', kind: 'needs-input' },
      { session: 'ohne id', kind: 'needs-input' },
    ],
  });
  assert.deepEqual(entries.map(entry => entry.sessionId), ['b', 'a']);
  assert.equal(entries[0].kind, 'review');
});

test('an unavailable list is never reported as an empty one', () => {
  assert.equal(inboxState({ state: 'unavailable', entries: [] }), 'unavailable');
  assert.equal(
    inboxHeadline({ state: 'unavailable', entries: [] }),
    'Die wartenden Sessions konnten gerade nicht gelesen werden.',
  );
  assert.equal(
    inboxHeadline({ state: 'complete', entries: [] }),
    'Im Moment wartet keine Session auf dich.',
  );
});

test('a partial list says that it is incomplete and still names what it knows', () => {
  const headline = inboxHeadline({
    state: 'incomplete',
    entries: [{ sessionId: 'a', session: 'auth-fix', kind: 'needs-input' }],
  });
  assert.match(headline, /Eine Session wartet auf dich\./);
  assert.match(headline, /nicht bekannt, ob sie warten/);
});

test('a wait without a known start reads as a lower bound', () => {
  assert.equal(waitingTimeLabel({ age: '12m', waitingSinceKnown: true }), 'Wartet seit 12m');
  assert.equal(waitingTimeLabel({ age: '12m', waitingSinceKnown: false }), 'Wartet mindestens seit 12m');
});

test('unknown content is marked instead of shown as empty', () => {
  assert.equal(excerptLabel({ excerptKnown: true, excerpt: 'Darf ich?' }).text, 'Darf ich?');
  assert.equal(excerptLabel({ excerptKnown: false, excerpt: 'alt' }).known, false);
  assert.match(excerptLabel({ excerptKnown: false }).text, /nicht bekannt/);
});

test('the waiting kind and a pending answer are named in full sentences', () => {
  assert.equal(waitingKindLabel('needs-input'), 'Frage oder Freigabe offen');
  assert.equal(waitingKindLabel('review'), 'Ergebnis wartet auf einen Blick');
  assert.equal(deliveryLabel({ awaitingDelivery: false }), '');
  assert.equal(
    deliveryLabel({ awaitingDelivery: true, queued: [{ id: '1' }] }),
    'Eine Antwort wartet auf die Zustellung.',
  );
  assert.match(
    deliveryLabel({ awaitingDelivery: true, queued: [{ id: '1', stuck: true }] }),
    /ungewiss/,
  );
});
