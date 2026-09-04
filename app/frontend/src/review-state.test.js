import test from 'node:test';
import assert from 'node:assert/strict';

import {
  comparisonModeLabel,
  lineRef,
  commentAnchorText,
  sortComments,
  diffFileState,
  canCommentOnFile,
  cappedNotice,
  diffSummary,
  newLineCount,
  unavailableText,
  sendDisabledReason,
  commentedPaths,
} from './review-state.js';

test('Comparison modes and line references read as whole sentences', () => {
  assert.equal(comparisonModeLabel('branch'), 'Branch gegen Basis-Branch');
  assert.equal(comparisonModeLabel('working_tree'), 'Arbeitsverzeichnis gegen HEAD');
  assert.equal(comparisonModeLabel(''), 'Arbeitsverzeichnis gegen HEAD');

  assert.equal(lineRef({ newStart: 12, newEnd: 12 }), 'Zeile 12');
  assert.equal(lineRef({ newStart: 12, newEnd: 14 }), 'Zeilen 12–14');
  assert.equal(lineRef({ oldStart: 7, oldEnd: 7 }), 'Zeile 7 (entfernte Zeile)');
  assert.equal(lineRef({ oldStart: 7, oldEnd: 9 }), 'Zeilen 7–9 (entfernte Zeilen)');
  assert.equal(commentAnchorText({ path: 'app/a.go', newStart: 30, newEnd: 32 }), 'app/a.go, Zeilen 30–32');
});

test('Comments sort by file then line regardless of creation order', () => {
  const sorted = sortComments([
    { id: 'c2', path: 'app/b.go', newStart: 3 },
    { id: 'c1', path: 'app/a.go', newStart: 30 },
    { id: 'c3', path: 'app/a.go', newStart: 5 },
  ]);
  assert.deepEqual(sorted.map((comment) => comment.id), ['c3', 'c1', 'c2']);
});

test('File states separate commentable content from listed-only entries', () => {
  assert.equal(diffFileState({ path: 'a.go', hunks: [{ lines: [] }] }), 'ok');
  assert.equal(diffFileState({ path: 'bild.png', binary: true }), 'binary');
  assert.equal(diffFileState({ path: 'gross.go', capped: true, hunks: [] }), 'capped');
  assert.equal(diffFileState({ path: 'run.sh' }), 'empty');

  assert.equal(canCommentOnFile({ path: 'a.go', hunks: [{ lines: [] }] }), true);
  assert.equal(canCommentOnFile({ path: 'bild.png', binary: true }), false);
  assert.equal(canCommentOnFile({ path: 'gross.go', capped: true }), false);
  assert.equal(canCommentOnFile({ path: 'run.sh' }), false);

  assert.match(cappedNotice({ path: 'gross.go', capped: true }), /vorhanden, wird aber nicht dargestellt/);
  assert.equal(cappedNotice({ path: 'a.go', hunks: [] }), '');
});

test('Empty and unavailable diffs never read as clean', () => {
  assert.equal(diffSummary({ mode: 'working_tree', files: [] }), 'Es gibt in diesem Vergleich nichts zu reviewen.');
  assert.equal(diffSummary({ mode: 'branch', files: [{ path: 'a.go' }] }), 'Eine Datei geändert (Branch gegen Basis-Branch).');
  assert.equal(
    unavailableText({ operation: 'diff_working_tree', message: 'exit status 128' }),
    'Der Diff ist nicht verfügbar (diff_working_tree: exit status 128). Kommentieren und Senden sind für diesen Stand deaktiviert.',
  );
});

test('Send availability and commented paths follow the open Review', () => {
  assert.equal(sendDisabledReason({ comments: [] }), 'Das Review enthält noch keine Kommentare.');
  assert.equal(sendDisabledReason(null), 'Das Review enthält noch keine Kommentare.');
  assert.equal(sendDisabledReason({ comments: [{ id: 'c1' }] }), '');

  assert.deepEqual(
    commentedPaths({ comments: [{ path: 'app/b.go' }, { path: 'app/a.go' }, { path: '' }] }),
    ['app/a.go', 'app/b.go'],
  );
});

test('Changed-line counts cover added and removed lines', () => {
  const file = {
    path: 'a.go',
    hunks: [{ lines: [{ kind: 'context' }, { kind: 'added' }, { kind: 'removed' }, { kind: 'added' }] }],
  };
  assert.equal(newLineCount(file), 3);
});
