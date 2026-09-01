// Projection of the blocked inbox the attention planner produced. The order
// comes from core and is never changed here; this module only turns the facts
// into the sentences the view prints.

// inboxEntries normalizes the raw payload so the renderer never has to guard
// against missing fields. Entries without a SessionID cannot be acted on and
// are dropped.
export function inboxEntries(inbox) {
  const raw = Array.isArray(inbox?.entries) ? inbox.entries : [];
  const out = [];
  for (const entry of raw) {
    const sessionId = typeof entry?.sessionId === 'string' ? entry.sessionId.trim() : '';
    if (!sessionId) continue;
    out.push({
      sessionId,
      session: String(entry.session ?? '').trim() || sessionId,
      project: String(entry.project ?? '').trim(),
      kind: entry.kind === 'review' ? 'review' : 'needs-input',
      age: String(entry.age ?? '').trim(),
      waitingSinceKnown: entry.waitingSinceKnown === true,
      excerpt: String(entry.excerpt ?? ''),
      excerptKnown: entry.excerptKnown === true,
      awaitingDelivery: entry.awaitingDelivery === true,
      queued: Array.isArray(entry.queued) ? entry.queued : [],
    });
  }
  return out;
}

// inboxState reduces the list state to the three cases the view distinguishes.
export function inboxState(inbox) {
  const state = inbox?.state;
  if (state === 'complete' || state === 'incomplete') return state;
  return 'unavailable';
}

// inboxHeadline says in one sentence how many Sessions wait and how much of
// that answer is actually known. An empty list is only reported as "nothing
// waits" when the list is complete.
export function inboxHeadline(inbox) {
  const state = inboxState(inbox);
  const entries = inboxEntries(inbox);
  if (state === 'unavailable') {
    return 'Die wartenden Sessions konnten gerade nicht gelesen werden.';
  }
  const count = entries.length === 1
    ? 'Eine Session wartet auf dich.'
    : `${entries.length} Sessions warten auf dich.`;
  if (state === 'incomplete') {
    if (!entries.length) return 'Von einem Teil der Sessions ist gerade nicht bekannt, ob sie warten.';
    return `${count} Von weiteren Sessions ist gerade nicht bekannt, ob sie warten.`;
  }
  if (!entries.length) return 'Im Moment wartet keine Session auf dich.';
  return count;
}

// waitingKindLabel names what the Session asks of the developer.
export function waitingKindLabel(kind) {
  return kind === 'review'
    ? 'Ergebnis wartet auf einen Blick'
    : 'Frage oder Freigabe offen';
}

// waitingTimeLabel keeps a wait whose start is unknown recognizable as a lower
// bound instead of presenting it as a wait that just began.
export function waitingTimeLabel(entry) {
  const age = String(entry?.age ?? '').trim();
  if (!age) return 'Wartet seit unbekannter Zeit';
  if (entry?.waitingSinceKnown) return `Wartet seit ${age}`;
  return `Wartet mindestens seit ${age}`;
}

// excerptLabel returns the pane tail that explains the wait, or the sentence
// that says the reason could not be read.
export function excerptLabel(entry) {
  if (!entry?.excerptKnown) {
    return { known: false, text: 'Der Grund ist nicht bekannt — die Ausgabe der Session konnte nicht gelesen werden.' };
  }
  const text = String(entry.excerpt ?? '').trim();
  if (!text) {
    return { known: false, text: 'Die Session zeigt gerade keinen Text, der die Frage erklärt.' };
  }
  return { known: true, text };
}

// deliveryLabel describes an answer that is queued but not yet delivered.
export function deliveryLabel(entry) {
  if (!entry?.awaitingDelivery) return '';
  const queued = Array.isArray(entry.queued) ? entry.queued : [];
  const stuck = queued.filter(message => message?.stuck === true).length;
  const waiting = queued.length === 1
    ? 'Eine Antwort wartet auf die Zustellung.'
    : `${queued.length} Antworten warten auf die Zustellung.`;
  if (!stuck) return waiting;
  return `${waiting} Bei ${stuck === 1 ? 'einer davon' : `${stuck} davon`} ist ungewiss, ob die Session sie erhalten hat.`;
}
