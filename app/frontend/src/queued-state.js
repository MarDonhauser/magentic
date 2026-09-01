// Projection of the Outbox entries an Overview agent carries, shaped for the
// session row. Kind only informs the wording — a queued handoff reads
// differently from a typed message, but neither gets its own decoration.

function describeQueued(kind, preview) {
  if (kind === 'handoff') {
    return preview ? `Handoff mit dem Kontext „${preview}“` : 'Handoff aus einer anderen Session';
  }
  if (kind === 'skill') return preview || 'Skill-Befehl ohne Text';
  if (kind === 'automation') return preview || 'Automatisierte Anweisung ohne Text';
  return preview || 'Nachricht ohne Text';
}

// queuedMessages normalizes the raw payload so the renderer never has to guard
// against missing fields.
export function queuedMessages(agent) {
  const raw = Array.isArray(agent?.queued) ? agent.queued : [];
  const out = [];
  for (const message of raw) {
    const id = typeof message?.id === 'string' ? message.id.trim() : '';
    if (!id) continue;
    const kind = typeof message.kind === 'string' ? message.kind.trim() : '';
    const preview = String(message.preview ?? '').trim();
    out.push({
      id,
      kind,
      age: String(message.age ?? '').trim(),
      stuck: message.stuck === true,
      text: describeQueued(kind, preview),
    });
  }
  return out;
}

// queuedHeadline states in one sentence how many messages wait and how many of
// them have an unknown delivery outcome.
export function queuedHeadline(sessionName, messages) {
  const list = Array.isArray(messages) ? messages : [];
  if (!list.length) return '';
  const name = String(sessionName ?? '').trim();
  const target = name ? `„${name}“` : 'diese Session';
  const waiting = list.length === 1
    ? `Eine Nachricht wartet auf ${target}.`
    : `${list.length} Nachrichten warten auf ${target}.`;
  const stuck = list.filter(message => message.stuck).length;
  if (!stuck) return waiting;
  const unknown = stuck === 1
    ? 'Bei einer davon ist ungewiss, ob die Session sie erhalten hat.'
    : `Bei ${stuck} davon ist ungewiss, ob die Session sie erhalten hat.`;
  return `${waiting} ${unknown}`;
}
