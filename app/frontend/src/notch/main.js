import './notch.css';
import { mountNotchOverlay } from './NotchOverlay.js';

mountNotchOverlay(document.getElementById('notch-root'));

// A URL-only preview keeps visual QA independent from the live Attention
// pipeline. The native window never supplies this query parameter.
const previewKind = new URLSearchParams(window.location.search).get('preview');
if (['permission', 'question', 'review'].includes(previewKind)) {
  const copy = {
    permission: ['Atlas braucht eine Freigabe', 'Shell-Freigabe – direkt entscheiden oder die Session öffnen.'],
    question: ['Atlas wartet auf deine Antwort', 'Die Rückfrage ist im Terminal geöffnet.'],
    review: ['Atlas ist bereit zur Review', 'Agent ist fertig – bereit für den nächsten Prompt.'],
  }[previewKind];
  const options = previewKind === 'permission'
    ? [
        { id: 'deny', label: 'Ablehnen', tone: 'deny' },
        { id: 'open', label: 'Session öffnen', tone: 'neutral' },
        { id: 'allow', label: 'Erlauben', tone: 'allow' },
      ]
    : [
        { id: 'later', label: 'Später', tone: 'neutral' },
        { id: 'open', label: previewKind === 'review' ? 'Review öffnen' : 'Session öffnen', tone: 'allow' },
      ];
  window.setTimeout(() => window.__magenticNotchDispatch('notch://event', {
    id: `preview:${previewKind}`,
    kind: previewKind,
    title: copy[0],
    detail: copy[1],
    options,
    sessionId: 'preview',
  }), 0);
}
