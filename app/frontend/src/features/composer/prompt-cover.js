// Wie viele Zeilen am unteren Rand des Panes zur Eingabe des Agenten gehören.
//
// Gesucht wird die unterste Zeile, die mit dem Muster des Vendors beginnt;
// verdeckt wird von einer Zeile darüber bis zum Rand, weil dort der obere
// Rahmen des Eingabekastens steht. Wird das Muster nicht gefunden, wird nichts
// verdeckt — Inhalt zu verstecken, den wir nicht sicher erkannt haben, wäre
// schlimmer als ein doppeltes Eingabefeld.
export function promptCoverRows(bufferLines, pattern) {
  if (!pattern || !bufferLines?.length) return 0;
  for (let i = bufferLines.length - 1; i >= 0; i--) {
    if (!bufferLines[i].trimStart().startsWith(pattern)) continue;
    const from = Math.max(0, i - 1);
    return bufferLines.length - from;
  }
  return 0;
}
