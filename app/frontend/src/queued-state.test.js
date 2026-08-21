import test from 'node:test';
import assert from 'node:assert/strict';

import { queuedMessages, queuedHeadline } from './queued-state.js';

test('Queued messages normalize kind wording and drop entries without an ID', () => {
  const messages = queuedMessages({
    queued: [
      { id: 'a', kind: 'message', preview: 'Bitte die Tests grün machen', age: 'vor 3m' },
      { id: '', kind: 'message', preview: 'ohne ID' },
      { id: 'b', kind: 'handoff', preview: 'Kontext aus review-1', age: 'vor 1m', stuck: true },
      { id: 'c', kind: 'skill', preview: '/deploy' },
      { id: 'd', kind: 'message' },
    ],
  });

  assert.deepEqual(messages, [
    { id: 'a', kind: 'message', age: 'vor 3m', stuck: false, text: 'Bitte die Tests grün machen' },
    { id: 'b', kind: 'handoff', age: 'vor 1m', stuck: true, text: 'Handoff mit dem Kontext „Kontext aus review-1“' },
    { id: 'c', kind: 'skill', age: '', stuck: false, text: '/deploy' },
    { id: 'd', kind: 'message', age: '', stuck: false, text: 'Nachricht ohne Text' },
  ]);
});

test('Agents without an Outbox produce no queue and no headline', () => {
  assert.deepEqual(queuedMessages({}), []);
  assert.deepEqual(queuedMessages(null), []);
  assert.equal(queuedHeadline('alpha', []), '');
});

test('Queue headline counts waiting messages and names the uncertain ones', () => {
  const one = queuedMessages({ queued: [{ id: 'a', kind: 'message', preview: 'Hallo' }] });
  assert.equal(queuedHeadline('alpha', one), 'Eine Nachricht wartet auf „alpha“.');

  const many = queuedMessages({
    queued: [
      { id: 'a', kind: 'message', preview: 'Hallo', stuck: true },
      { id: 'b', kind: 'message', preview: 'Noch etwas' },
    ],
  });
  assert.equal(
    queuedHeadline('alpha', many),
    '2 Nachrichten warten auf „alpha“. Bei einer davon ist ungewiss, ob die Session sie erhalten hat.',
  );

  const bothStuck = queuedMessages({
    queued: [
      { id: 'a', kind: 'message', preview: 'Hallo', stuck: true },
      { id: 'b', kind: 'handoff', preview: 'Kontext', stuck: true },
    ],
  });
  assert.equal(
    queuedHeadline('', bothStuck),
    '2 Nachrichten warten auf diese Session. Bei 2 davon ist ungewiss, ob die Session sie erhalten hat.',
  );
});
