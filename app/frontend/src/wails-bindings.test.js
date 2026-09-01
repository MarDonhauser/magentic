import test from 'node:test';
import assert from 'node:assert/strict';

import {
  Board,
  BoardArchive,
  GitGraph,
  Inbox,
  KillSession,
  MigrateDockSessions,
  NewDockSession,
  OpenTerm,
  RemoveProject,
  MoveSidebarItem,
  SendMessage,
  SendSkill,
  SetMainBranch,
  SwitchSessionVendor,
  WorktreeDiff,
} from '../wailsjs/go/main/App.js';

test('WorktreeDiff forwards the Project and opaque WorktreeRef', async (t) => {
  const calls = [];
  const previousWindow = globalThis.window;
  t.after(() => { globalThis.window = previousWindow; });
  globalThis.window = {
    go: {
      main: {
        App: {
          WorktreeDiff: (...args) => {
            calls.push(args);
            return 'known diff';
          },
        },
      },
    },
  };

  assert.equal(await WorktreeDiff('project-id', 'worktree-ref'), 'known diff');
  assert.deepEqual(calls, [['project-id', 'worktree-ref']]);
});

test('session and project actions forward stable IDs through the Wails bridge', async (t) => {
  const calls = [];
  const previousWindow = globalThis.window;
  t.after(() => { globalThis.window = previousWindow; });
  const record = method => (...args) => { calls.push([method, ...args]); };
  globalThis.window = {
    go: {
      main: {
        App: {
          KillSession: record('KillSession'),
          OpenTerm: record('OpenTerm'),
          RemoveProject: record('RemoveProject'),
          MoveSidebarItem: record('MoveSidebarItem'),
          SendSkill: record('SendSkill'),
          SetMainBranch: record('SetMainBranch'),
          SwitchSessionVendor: record('SwitchSessionVendor'),
        },
      },
    },
  };

  await OpenTerm('session-id', 'Display name', 120, 40);
  await SendSkill('session-id', '/done ');
  await KillSession('session-id', 'Display name');
  await SetMainBranch('project-id', 'main');
  await SwitchSessionVendor('session-id', 'codex', true);
  await RemoveProject('project-id');
  await MoveSidebarItem('session', 'session-id', 'divider', 'divider-id', [{ kind: 'session', ref: 'session-id' }]);

  assert.deepEqual(calls, [
    ['OpenTerm', 'session-id', 'Display name', 120, 40],
    ['SendSkill', 'session-id', '/done '],
    ['KillSession', 'session-id', 'Display name'],
    ['SetMainBranch', 'project-id', 'main'],
    ['SwitchSessionVendor', 'session-id', 'codex', true],
    ['RemoveProject', 'project-id'],
    ['MoveSidebarItem', 'session', 'session-id', 'divider', 'divider-id', [{ kind: 'session', ref: 'session-id' }]],
  ]);
});

test('project queries and Dock restoration forward stable IDs at the bridge', async (t) => {
  const calls = [];
  const previousWindow = globalThis.window;
  t.after(() => { globalThis.window = previousWindow; });
  const record = method => (...args) => { calls.push([method, ...args]); };
  globalThis.window = {
    go: {
      main: {
        App: {
          Board: record('Board'),
          BoardArchive: record('BoardArchive'),
          GitGraph: record('GitGraph'),
          MigrateDockSessions: record('MigrateDockSessions'),
          NewDockSession: record('NewDockSession'),
        },
      },
    },
  };

  await GitGraph('project-id', 120);
  await Board('project-id');
  await BoardArchive('project-id', 25);
  await NewDockSession('project-id');
  await MigrateDockSessions(['legacy dock']);

  assert.deepEqual(calls, [
    ['GitGraph', 'project-id', 120],
    ['Board', 'project-id'],
    ['BoardArchive', 'project-id', 25],
    ['NewDockSession', 'project-id'],
    ['MigrateDockSessions', ['legacy dock']],
  ]);
});

test('the inbox reads its entries and answers a Session through the bridge', async (t) => {
  const calls = [];
  const previousWindow = globalThis.window;
  t.after(() => { globalThis.window = previousWindow; });
  const planned = {
    state: 'complete',
    entries: [{
      sessionId: 'session-id', session: 'auth-fix', project: 'magentic',
      kind: 'needs-input', age: '12m', waitingSinceKnown: true,
      excerpt: 'Darf ich die Datei schreiben?', excerptKnown: true, awaitingDelivery: false,
    }],
  };
  globalThis.window = {
    go: {
      main: {
        App: {
          Inbox: () => planned,
          SendMessage: (...args) => { calls.push(['SendMessage', ...args]); },
        },
      },
    },
  };

  const inbox = await Inbox();
  assert.equal(inbox.state, 'complete');
  assert.equal(inbox.entries[0].sessionId, 'session-id');
  await SendMessage(inbox.entries[0].sessionId, 'ja, bitte');
  assert.deepEqual(calls, [['SendMessage', 'session-id', 'ja, bitte']]);
});
