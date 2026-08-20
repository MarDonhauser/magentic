import test from 'node:test';
import assert from 'node:assert/strict';

import { WorktreeDiff } from '../wailsjs/go/main/App.js';

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
