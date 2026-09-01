// Auslöser- und Einfügelogik des Composer-Menüs, ohne DOM, damit sie prüfbar
// bleibt. Der Composer selbst hängt nur die Darstellung daran.

const TRIGGERS = { '@': 'file', '/': 'command' };

// completionTrigger sucht von der Schreibmarke aus rückwärts nach einem
// Auslöser. Ein Leerzeichen dazwischen beendet die Suche: wer weitergeschrieben
// hat, meint keine Vervollständigung mehr.
export function completionTrigger(text, caret) {
  for (let i = caret - 1; i >= 0; i--) {
    const char = text[i];
    if (char === ' ' || char === '\n' || char === '\t') return null;
    const kind = TRIGGERS[char];
    if (!kind) continue;
    // Ein Schrägstrich zählt nur als erstes Zeichen der Nachricht; der Agent
    // deutet ihn auch nur dort als Befehl. Überall sonst ist er ein gewöhnliches
    // Pfadzeichen und darf die Suche nach einem @ davor nicht abbrechen.
    if (kind === 'command' && i !== 0) continue;
    // Ein @ braucht eine Wortgrenze davor, sonst trifft es jede Mailadresse.
    const previous = i > 0 ? text[i - 1] : '';
    if (kind === 'file' && previous && !' \n\t('.includes(previous)) return null;
    return { kind, query: text.slice(i + 1, caret), start: i };
  }
  return null;
}

// applyCompletion ersetzt den Auslöserbereich durch den gewählten Wert. Danach
// steht genau ein Leerzeichen, und die Schreibmarke steht dahinter — egal ob
// eines nachrückte oder schon da war, damit man in beiden Fällen gleich
// weitertippt. Der Text dahinter bleibt unangetastet.
export function applyCompletion(text, trigger, value) {
  if (!trigger) return { text, caret: text.length };
  const marker = text[trigger.start];
  const head = text.slice(0, trigger.start);
  const tail = text.slice(trigger.start + 1 + trigger.query.length);
  const rest = tail.startsWith(' ') ? tail.slice(1) : tail;
  const inserted = `${marker}${value}`;
  return {
    text: `${head}${inserted} ${rest}`,
    caret: head.length + inserted.length + 1,
  };
}
