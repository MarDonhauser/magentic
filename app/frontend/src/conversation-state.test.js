import test from 'node:test';
import assert from 'node:assert/strict';

import {
  applyReading, applyUpdate, emptyConversationState, renderModel, scrollDecision,
} from './conversation-state.js';

function item(id, kind, extra = {}) {
  return { id, kind, role: 'agent', title: id, ...extra };
}

function available(items) {
  return applyReading({ availability: 'available', vendor: 'claude', itemsKnown: true, items });
}

test('Items erscheinen in der Reihenfolge der Conversation', () => {
  const model = renderModel(available([
    item('a', 'developer-prompt'), item('b', 'agent-message'), item('c', 'command-execution'),
  ]));
  assert.equal(model.kind, 'items');
  assert.deepEqual(model.rows.map(row => row.id), ['a', 'b', 'c']);
});

test('Ein ersetztes Item tritt an die Stelle seines Vorgängers statt hinten anzuhängen', () => {
  let state = available([
    item('t1', 'command-execution', { title: 'ls core', awaitingResult: true }),
    item('m1', 'agent-message', { title: 'weiter' }),
  ]);
  state = applyUpdate(state, {
    items: [item('t1', 'command-execution', { title: 'ls core', detail: 'timeline.go' })],
  });

  assert.equal(state.items.length, 2);
  assert.equal(state.items[0].id, 't1');
  assert.equal(state.items[0].detail, 'timeline.go');
  assert.equal(!!state.items[0].awaitingResult, false);

  const model = renderModel(state);
  assert.deepEqual(model.rows.map(row => row.id), ['t1', 'm1']);
});

test('Eine vollständige Neulesung ersetzt alles, was gehalten wurde', () => {
  let state = available([item('alt', 'agent-message')]);
  state = applyUpdate(state, { replaced: true, items: [item('neu', 'agent-message')] });
  assert.deepEqual(state.items.map(entry => entry.id), ['neu']);
});

test('Werkzeugaktivität ist auf ihren Titel eingeklappt und lässt sich ausklappen', () => {
  const model = renderModel(available([
    item('t1', 'command-execution', { title: 'go test ./core/', detail: 'ok  magentic/core' }),
  ]));
  const row = model.rows[0];
  assert.equal(row.collapsed, true);
  assert.equal(row.expandable, true);
  assert.equal(row.title, 'go test ./core/');
});

test('Ein Fehlschlag ist schon in der eingeklappten Zeile zu sehen', () => {
  const model = renderModel(available([
    item('t1', 'command-execution', { title: 'go build', detail: 'exit 1', failed: true }),
  ]));
  assert.equal(model.rows[0].failed, true);
  assert.equal(model.rows[0].collapsed, true);
});

test('Ein Aufruf ohne Detail bleibt eine Zeile ohne ausklappbaren Rumpf', () => {
  const model = renderModel(available([
    item('t1', 'command-execution', { title: 'sleep 900', awaitingResult: true }),
  ]));
  assert.equal(model.rows[0].collapsed, true);
  assert.equal(model.rows[0].expandable, false);
  assert.equal(model.rows[0].awaiting, true);
});

test('Agent-Nachrichten und Entwickler-Eingaben stehen vollständig da, ohne Umschalter', () => {
  const model = renderModel(available([
    item('m1', 'agent-message', { title: 'Erledigt', detail: 'Erledigt.\n\nDetails folgen.' }),
    item('p1', 'developer-prompt', { title: 'Bau das ein', detail: 'Bau das ein\nund teste es.' }),
  ]));
  for (const row of model.rows) {
    assert.equal(row.collapsed, false, `${row.id} darf nicht eingeklappt sein`);
    assert.equal(row.expandable, false, `${row.id} braucht keinen Umschalter`);
  }
});

test('Delegierte Items stehen unter ihrer Aufgabe', () => {
  const model = renderModel(available([
    item('task', 'delegated-task', { title: 'Repo prüfen' }),
    item('sub1', 'command-execution', { delegated: true, parentTaskId: 'task' }),
    item('sub2', 'agent-message', { delegated: true, parentTaskId: 'task' }),
    item('next', 'agent-message'),
  ]));
  assert.deepEqual(model.rows.map(row => row.id), ['task', 'next']);
  assert.deepEqual(model.rows[0].children.map(row => row.id), ['sub1', 'sub2']);
});

test('Delegierte Arbeit ohne bekannte Aufgabe erscheint als solche und nicht als primäre Aktivität', () => {
  const model = renderModel(available([
    item('m1', 'agent-message'),
    item('sub1', 'command-execution', { delegated: true }),
  ]));
  assert.deepEqual(model.rows.map(row => row.id), ['m1', 'delegated:unknown']);
  const group = model.rows[1];
  assert.equal(group.orphaned, true);
  assert.match(group.title, /ohne bekannte Aufgabe/);
  assert.deepEqual(group.children.map(row => row.id), ['sub1']);
});

test('Eine leere, verfügbare Conversation sagt etwas anderes als eine fehlende Aufzeichnung', () => {
  const empty = renderModel(available([]));
  const missing = renderModel(applyReading({
    availability: 'record-not-found', vendor: 'claude',
    reason: 'Das Aufzeichnungs-File dieses Laufs wurde nicht gefunden.',
  }));

  assert.equal(empty.kind, 'empty');
  assert.equal(missing.kind, 'notice');
  assert.notEqual(empty.headline, missing.headline);
  assert.match(empty.headline, /noch nichts/);
  assert.equal(missing.reason, 'Das Aufzeichnungs-File dieses Laufs wurde nicht gefunden.');
  assert.equal(missing.terminalReachable, true);
});

test('Jede nicht verfügbare Lesung nennt ihre eigene Wortwahl, den Grund und den Vendor', () => {
  const headlines = new Set();
  for (const availability of ['not-applicable', 'no-normalizer', 'record-not-found', 'record-unreadable']) {
    const model = renderModel(applyReading({
      availability, vendor: 'codex', reason: 'Grund für ' + availability,
    }));
    assert.equal(model.kind, 'notice');
    assert.equal(model.availability, availability);
    assert.equal(headlines.has(model.headline), false, `${availability} teilt seine Wortwahl`);
    headlines.add(model.headline);
    assert.equal(model.reason, 'Grund für ' + availability);
    assert.equal(model.vendor, 'codex');
    assert.equal(model.terminalReachable, true);
    assert.deepEqual(model.rows, []);
  }
});

test('Ein wartender Agent wird benannt und der Weg zu seinem Terminal angeboten', () => {
  const model = renderModel(available([item('m1', 'agent-message')]), { waiting: true });
  assert.equal(model.waiting.waiting, true);
  assert.match(model.waiting.headline, /wartet/);
  assert.deepEqual(model.actions.map(action => action.kind), ['open-terminal']);
});

test('Die Oberfläche bietet keine Bedienung an, die eine Berechtigungsfrage beantworten würde', () => {
  for (const context of [{}, { waiting: true }]) {
    const model = renderModel(available([
      item('t1', 'command-execution', { title: 'rm -rf build' }),
    ]), context);
    const kinds = model.actions.map(action => action.kind);
    for (const forbidden of ['approve', 'deny', 'allow', 'reject', 'permission']) {
      assert.equal(kinds.includes(forbidden), false, `${forbidden} darf nicht angeboten werden`);
    }
    for (const row of model.rows) {
      assert.equal('actions' in row, false, 'Zeilen bieten keine Bedienung an');
    }
  }
});

test('Die Bildlaufposition bleibt, wo der Entwickler sie gelassen hat', () => {
  const atEnd = scrollDecision({ scrollTop: 800, scrollHeight: 1000, clientHeight: 200, hasNewItems: true });
  assert.equal(atEnd.atBottom, true);
  assert.equal(atEnd.follow, true);
  assert.equal(atEnd.showJumpToEnd, false);

  const scrolledBack = scrollDecision({ scrollTop: 120, scrollHeight: 1000, clientHeight: 200, hasNewItems: true });
  assert.equal(scrolledBack.atBottom, false);
  assert.equal(scrolledBack.follow, false);
  assert.equal(scrolledBack.showJumpToEnd, true);

  const quiet = scrollDecision({ scrollTop: 800, scrollHeight: 1000, clientHeight: 200, hasNewItems: false });
  assert.equal(quiet.follow, false);
});

test('Ein leerer Zustand ist kein verfügbarer Zustand', () => {
  const model = renderModel(emptyConversationState());
  assert.equal(model.kind, 'notice');
  assert.deepEqual(model.rows, []);
});
