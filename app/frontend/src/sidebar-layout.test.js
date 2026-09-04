import test from 'node:test';
import assert from 'node:assert/strict';

import { buildSidebar, flattenSidebar, canPlace, planMove } from './sidebar-layout.js';

function overview(sidebar) {
  return {
    sidebar,
    projects: [
      {
        id: 'p1', name: 'navi', path: '/tmp/navi',
        worktrees: [{ branch: 'main', agents: [{ id: 's1', name: 'navi', status: 'idle' , live: true }] }],
      },
      {
        id: 'p2', name: 'magentic', path: '/tmp/magentic',
        worktrees: [{
          branch: 'main',
          agents: [
            { id: 's2', name: 'magentic', status: 'idle' , live: true },
            { id: 's3', name: 'magentic-2', status: 'idle' , live: true },
            { id: 's4', name: 'dock-term', status: 'idle', dock: true , live: true },
            { id: 's5', name: 'altlast', status: 'dead' , live: false },
          ],
        }],
      },
    ],
  };
}

const shape = items => items.map(i => `${i.kind}:${i.ref}` +
  (i.children?.length ? `(${shape(i.children).join(',')})` : ''));

test('Ohne Ablage steht jedes Projekt oben und jede Session unter ihrem Projekt', () => {
  const tree = buildSidebar(overview([]));
  assert.deepEqual(shape(tree), ['project:p1(session:s1)', 'project:p2(session:s2,session:s3)']);
});

test('Dock-Terminals und beendete Sessions bleiben aus der Liste', () => {
  const refs = flattenSidebar(buildSidebar(overview([]))).map(r => r.ref);
  assert.ok(!refs.includes('s4'));
  assert.ok(!refs.includes('s5'));
});

test('Die Ablage bestimmt die Reihenfolge, Unbekanntes hängt hinten an', () => {
  const tree = buildSidebar(overview([
    { kind: 'project', ref: 'p2', children: [{ kind: 'session', ref: 's3' }] },
  ]));
  assert.deepEqual(shape(tree), ['project:p2(session:s3,session:s2)', 'project:p1(session:s1)']);
});

test('Eine Session kann aus ihrem Projekt in einen Divider wandern', () => {
  const tree = buildSidebar(overview([
    { kind: 'divider', ref: 'd1', name: 'Recherche', children: [{ kind: 'session', ref: 's3' }] },
  ]));
  assert.deepEqual(shape(tree),
    ['divider:d1(session:s3)', 'project:p1(session:s1)', 'project:p2(session:s2)']);
});

test('Ein zugeklappter Divider verbirgt seine Zeilen', () => {
  const tree = buildSidebar(overview([
    { kind: 'divider', ref: 'd1', name: 'Recherche', collapsed: true, children: [{ kind: 'session', ref: 's3' }] },
  ]));
  const rows = flattenSidebar(tree);
  assert.deepEqual(rows.filter(r => r.ref === 's3'), []);
  assert.equal(rows[0].ref, 'd1');
});

test('Eine Ablage auf ein verschwundenes Projekt wird ignoriert', () => {
  const tree = buildSidebar(overview([{ kind: 'project', ref: 'weg' }, { kind: 'project', ref: 'p2' }]));
  assert.deepEqual(shape(tree), ['project:p2(session:s2,session:s3)', 'project:p1(session:s1)']);
});

test('Die Verschachtelung bleibt flach und Sessions bleiben bei ihrem Projekt', () => {
  const session = { kind: 'session', ref: 's3', agent: { projectId: 'p2' } };
  assert.equal(canPlace({ kind: 'divider', ref: 'd1' }, '', ''), true);
  assert.equal(canPlace({ kind: 'divider', ref: 'd1' }, 'divider', 'd2'), false);
  assert.equal(canPlace({ kind: 'project', ref: 'p1' }, 'divider', 'd1'), true);
  assert.equal(canPlace({ kind: 'project', ref: 'p1' }, 'project', 'p2'), false);
  assert.equal(canPlace(session, 'project', 'p2'), true);
  assert.equal(canPlace(session, 'project', 'p1'), false);
  assert.equal(canPlace(session, 'divider', 'd1'), true);
});

test('Ein Zug schickt die vollständige neue Reihenfolge der Zielebene', () => {
  const tree = buildSidebar(overview([]));
  const project = tree.find(i => i.ref === 'p2');
  const move = planMove(tree, project.children[1], 'project', 'p2', 0);
  assert.deepEqual(move, {
    kind: 'session', ref: 's3', parentKind: 'project', parent: 'p2',
    order: [{ kind: 'session', ref: 's3' }, { kind: 'session', ref: 's2' }],
  });
});

test('Ein Zug nach unten rechnet die eigene Zeile heraus', () => {
  const tree = buildSidebar(overview([]));
  const project = tree.find(i => i.ref === 'p2');
  const move = planMove(tree, project.children[0], 'project', 'p2', 2);
  assert.deepEqual(move.order, [{ kind: 'session', ref: 's3' }, { kind: 'session', ref: 's2' }]);
});

test('Ein Zug auf die eigene Stelle und ein verbotenes Ziel ergeben keinen Zug', () => {
  const tree = buildSidebar(overview([]));
  const project = tree.find(i => i.ref === 'p2');
  assert.equal(planMove(tree, project.children[0], 'project', 'p2', 0), null);
  assert.equal(planMove(tree, project.children[0], 'project', 'p1', 0), null);
});

test('Ein Zug in einen Divider nennt den Divider als Ebene', () => {
  const tree = buildSidebar(overview([{ kind: 'divider', ref: 'd1', name: 'Recherche' }]));
  const project = tree.find(i => i.ref === 'p2');
  const move = planMove(tree, project.children[0], 'divider', 'd1', 0);
  assert.deepEqual(move, {
    kind: 'session', ref: 's2', parentKind: 'divider', parent: 'd1',
    order: [{ kind: 'session', ref: 's2' }],
  });
});
