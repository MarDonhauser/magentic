import test from 'node:test';
import assert from 'node:assert/strict';

import { usagePages, clampUsagePage } from './usage-state.js';

test('Usage pages normalize windows and drop pages without usable bars', () => {
  const pages = usagePages({
    usage: [
      {
        provider: 'claude', label: 'Claude-Limits', windows: [
          { label: '5h', percent: 17.6, reset: '14:30' },
          { label: '7d', percent: 19, reset: 'Do' },
        ],
      },
      { provider: 'codex', label: 'Codex-Limits', windows: [{ label: '7d', percent: 140 }] },
      { provider: 'gemini', label: 'Gemini-Limits', windows: [] },
      { provider: '', label: 'ohne Anbieter', windows: [{ label: '5h', percent: 3 }] },
    ],
  });

  assert.deepEqual(pages, [
    {
      provider: 'claude', label: 'Claude-Limits', windows: [
        { label: '5h', percent: 18, reset: '14:30' },
        { label: '7d', percent: 19, reset: 'Do' },
      ],
    },
    { provider: 'codex', label: 'Codex-Limits', windows: [{ label: '7d', percent: 100, reset: '' }] },
  ]);
});

test('Usage pages tolerate a missing payload', () => {
  assert.deepEqual(usagePages(null), []);
  assert.deepEqual(usagePages({ usage: 'kaputt' }), []);
});

test('A remembered page index survives providers appearing and disappearing', () => {
  assert.equal(clampUsagePage(1, 2), 1);
  assert.equal(clampUsagePage(1, 1), 0);
  assert.equal(clampUsagePage(-3, 2), 0);
  assert.equal(clampUsagePage(0, 0), 0);
});
