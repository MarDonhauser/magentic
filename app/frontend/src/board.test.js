import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const boardSrc = await readFile(new URL('./board.js', import.meta.url), 'utf8');

function extractFunction(src, name) {
  const match = src.match(new RegExp(`(?:export\\s+)?function\\s+${name}\\s*\\([^)]*\\)\\s*\\{([\\s\\S]*?)\\n\\}`));
  assert.ok(match, `Funktion ${name} nicht gefunden`);
  return new Function(src.match(new RegExp(`(?:export\\s+)?function\\s+${name}\\s*\\(([^)]*)\\)`))[1], match[1]);
}

test('itemColumn bildet Stages auf bekannte Spalten ab und fällt auf backlog zurück', () => {
  const itemColumn = extractFunction(boardSrc, 'itemColumn');
  assert.equal(itemColumn({ column: 'active' }), 'active');
  assert.equal(itemColumn({ column: 'review' }), 'review');
  assert.equal(itemColumn({ column: 'done' }), 'done');
  assert.equal(itemColumn({ column: 'backlog' }), 'backlog');
  assert.equal(itemColumn({ column: 'unknown' }), 'backlog');
  assert.equal(itemColumn({ column: '' }), 'backlog');
  assert.equal(itemColumn(null), 'backlog');
});

test('Arbeiten-Button ist agentenagnostisch und verwendet Play-Icon statt Claude-Icon', () => {
  assert.ok(boardSrc.includes("${icon('play')}Arbeiten"), 'Arbeiten-Button verwendet icon(play)');
  assert.ok(!boardSrc.includes("${developerIcon('claude')}Arbeiten"), 'Arbeiten-Button darf kein Claude-Icon hardcoden');
});

test('Klicks auf Aufgaben und Bedienelemente klappen Karten nicht zusammen', () => {
  assert.ok(
    boardSrc.includes("ev.target.closest('button, a, input, select, textarea, .bd-tasks, .bd-card-foot')"),
    'Interaktive Elemente und Task-Details sind vor versehentlichem Einklappen geschützt',
  );
});

test('Spalten-Filter berücksichtigt itemColumn für unbekannte Stages', () => {
  assert.ok(
    boardSrc.includes('itemColumn(it) === col.key'),
    'Karten mit unbekannter Stage landen in Geplant statt zu verschwinden',
  );
});
