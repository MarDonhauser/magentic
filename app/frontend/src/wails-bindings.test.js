import test from 'node:test';
import assert from 'node:assert/strict';

import {
  Board,
  BoardArchive,
  GitGraph,
  KillSession,
  MigrateDockSessions,
  NewDockSession,
  OpenTerm,
  RemoveProject,
  ReorderProjects,
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
          ReorderProjects: record('ReorderProjects'),
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
  await ReorderProjects(['project-b', 'project-a']);

  assert.deepEqual(calls, [
    ['OpenTerm', 'session-id', 'Display name', 120, 40],
    ['SendSkill', 'session-id', '/done '],
    ['KillSession', 'session-id', 'Display name'],
    ['SetMainBranch', 'project-id', 'main'],
    ['SwitchSessionVendor', 'session-id', 'codex', true],
    ['RemoveProject', 'project-id'],
    ['ReorderProjects', ['project-b', 'project-a']],
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
