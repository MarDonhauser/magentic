import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const main = await readFile(new URL('./main.js', import.meta.url), 'utf8');
const surface = await readFile(new URL('./conversation.js', import.meta.url), 'utf8');

function functionSource(source, name) {
  const start = source.indexOf(`function ${name}(`);
  assert.notEqual(start, -1, `${name} fehlt`);
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}' && --depth === 0) return source.slice(start, i + 1);
  }
  throw new Error(`${name} ist nicht geschlossen`);
}

// Lifecycle- und tmux-Bindings der Anwendung. Der Wechsel der Oberfläche darf
// keines davon anfassen: er ändert nur, was zu sehen ist.
const LIFECYCLE_BINDINGS = [
  'OpenTerm', 'WriteTerm', 'ResizeTerm', 'CloseTerm', 'KillSession',
  'NewSession', 'NewTermSession', 'NewDockSession', 'DoneAgent', 'Deploy',
  'Cleanup', 'Merge', 'LaterSession', 'ReopenSession', 'SendMessage',
  'SendSkill', 'HandoffSession', 'SetActiveTerm', 'MarkSeen',
];

test('Der Wechsel der Oberfläche löst keinen Lifecycle- und keinen tmux-Aufruf aus', () => {
  const source = functionSource(main, 'showTermSurface');
  for (const binding of LIFECYCLE_BINDINGS) {
    assert.equal(source.includes(binding + '('), false, `showTermSurface ruft ${binding} auf`);
  }
  assert.ok(source.includes('WatchConversation('), 'der Kern muss erfahren, welche Session betrachtet wird');
  assert.ok(source.includes('SessionConversation('), 'die Conversation muss gelesen werden');
});

test('Der Wechsel der Oberfläche ändert weder Auswahl noch Ansicht', () => {
  const source = functionSource(main, 'showTermSurface');
  assert.equal(/\bactiveSessionID\s*=[^=]/.test(source), false, 'die Auswahl bleibt unberührt');
  assert.equal(/\bactiveTerm\s*=[^=]/.test(source), false, 'die aktive Session bleibt unberührt');
  assert.equal(/\bview\s*=[^=]/.test(source), false, 'die Ansicht bleibt unberührt');
});

test('Die Oberfläche selbst ruft nichts auf, was eine Session verändern würde', () => {
  for (const binding of LIFECYCLE_BINDINGS) {
    assert.equal(new RegExp('\\b' + binding + '\\s*\\(').test(surface), false,
      `conversation.js ruft ${binding} auf`);
  }
  assert.equal(surface.includes("from '../wailsjs"), false,
    'die Oberfläche bindet keine Anwendungsaufrufe ein');
});

test('Die Oberfläche kennt keine Bedienung für eine Berechtigungsfrage', () => {
  for (const word of ['approve', 'genehmig', 'erlauben', 'ablehnen', 'deny']) {
    assert.equal(surface.toLowerCase().includes(word), false, `conversation.js nennt „${word}"`);
  }
});
