import test from 'node:test';
import assert from 'node:assert/strict';

import { canSendComposer, composeMessage } from './composer-attachments.js';

test('Bildanhänge werden sichtbar getrennt gehalten und beim Senden angefügt', () => {
  const attachments = [
    { name: 'entwurf.png', path: '/tmp/magentic/entwurf.png' },
    { name: 'fehler.jpg', path: '/tmp/magentic/fehler.jpg' },
  ];
  assert.equal(
    composeMessage('Bitte vergleiche diese Bilder.', attachments),
    'Bitte vergleiche diese Bilder.\n/tmp/magentic/entwurf.png\n/tmp/magentic/fehler.jpg',
  );
});

test('Eine Nachricht darf nur aus einem Bildanhang bestehen', () => {
  const attachments = [{ name: 'bild.png', path: '/tmp/magentic/bild.png' }];
  assert.equal(canSendComposer('', attachments), true);
  assert.equal(composeMessage('', attachments), '/tmp/magentic/bild.png');
});

test('Leerer Text ohne Bild bleibt nicht sendbar', () => {
  assert.equal(canSendComposer('   ', []), false);
});
