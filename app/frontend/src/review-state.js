// Render model of the diff review surface. Pure functions only: the renderer
// calls them with the structured diff and the open Review the Wails bindings
// return, so every sentence on screen is decided here and unit-testable
// without a running app.

export function comparisonModeLabel(mode) {
  if (mode === 'branch') return 'Branch gegen Basis-Branch';
  return 'Arbeitsverzeichnis gegen HEAD';
}

function anchorOf(comment) {
  const pick = (start, end) => {
    const s = Number(start) || 0;
    let e = Number(end) || 0;
    if (e < s) e = s;
    return [s, e];
  };
  let [start, end] = pick(comment?.newStart, comment?.newEnd);
  let removed = false;
  if (!start && !end) {
    [start, end] = pick(comment?.oldStart, comment?.oldEnd);
    removed = true;
  }
  return { start, end, removed };
}

// lineRef states the anchored lines of one comment the way the prompt quotes
// them, so the diff marking and the sent text agree.
export function lineRef(comment) {
  const { start, end, removed } = anchorOf(comment);
  if (!start) return 'ohne Zeilenangabe';
  const ref = end === start ? `Zeile ${start}` : `Zeilen ${start}–${end}`;
  if (!removed) return ref;
  return end === start ? `${ref} (entfernte Zeile)` : `${ref} (entfernte Zeilen)`;
}

export function commentAnchorText(comment) {
  const path = String(comment?.path ?? '').trim() || '(unbekannte Datei)';
  return `${path}, ${lineRef(comment)}`;
}

// sortComments keeps the comment list in file-then-line order, mirroring the
// Registry ordering the rendered prompt uses.
export function sortComments(comments) {
  const list = Array.isArray(comments) ? [...comments] : [];
  const anchorLine = (comment) => anchorOf(comment).start;
  return list.sort((a, b) => {
    const pathA = String(a?.path ?? '');
    const pathB = String(b?.path ?? '');
    if (pathA !== pathB) return pathA < pathB ? -1 : 1;
    if (anchorLine(a) !== anchorLine(b)) return anchorLine(a) - anchorLine(b);
    return String(a?.id ?? '') < String(b?.id ?? '') ? -1 : 1;
  });
}

// diffFileState names how one file entry renders: binary and capped files are
// listed without line content, entries without hunks (mode changes, renames
// without content) carry nothing to anchor a comment to.
export function diffFileState(file) {
  if (file?.capped === true) return 'capped';
  if (file?.binary === true) return 'binary';
  if (!Array.isArray(file?.hunks) || file.hunks.length === 0) return 'empty';
  return 'ok';
}

export function canCommentOnFile(file) {
  return diffFileState(file) === 'ok';
}

export function cappedNotice(file) {
  if (diffFileState(file) !== 'capped') return '';
  return 'Diese Datei ist vorhanden, wird aber nicht dargestellt und kann nicht kommentiert werden.';
}

// diffSummary states in one sentence what the comparison holds.
export function diffSummary(diff) {
  const files = Array.isArray(diff?.files) ? diff.files : [];
  if (files.length === 0) {
    return 'Es gibt in diesem Vergleich nichts zu reviewen.';
  }
  const label = comparisonModeLabel(diff?.mode);
  return files.length === 1
    ? `Eine Datei geändert (${label}).`
    : `${files.length} Dateien geändert (${label}).`;
}

export function newLineCount(file) {
  let count = 0;
  for (const hunk of file?.hunks ?? []) {
    for (const line of hunk?.lines ?? []) {
      if (line?.kind === 'added' || line?.kind === 'removed') count += 1;
    }
  }
  return count;
}

// unavailableText reports unreadable Git knowledge as unavailable, never as a
// clean diff, and names the failing operation.
export function unavailableText(problem) {
  const operation = String(problem?.operation ?? '').trim();
  const message = String(problem?.message ?? '').trim();
  const detail = operation && message ? `${operation}: ${message}` : (message || operation || 'unbekannte Ursache');
  return `Der Diff ist nicht verfügbar (${detail}). Kommentieren und Senden sind für diesen Stand deaktiviert.`;
}

// sendDisabledReason is empty when the open Review can be sent, otherwise it
// carries the sentence the send action shows.
export function sendDisabledReason(review) {
  const comments = Array.isArray(review?.comments) ? review.comments : [];
  if (comments.length === 0) return 'Das Review enthält noch keine Kommentare.';
  return '';
}

// commentedPaths collects the file paths that carry at least one comment, so
// the diff view can mark commented lines.
export function commentedPaths(review) {
  const paths = new Set();
  for (const comment of review?.comments ?? []) {
    const path = String(comment?.path ?? '').trim();
    if (path) paths.add(path);
  }
  return [...paths].sort();
}
