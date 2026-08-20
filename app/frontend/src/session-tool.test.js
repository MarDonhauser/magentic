import test from 'node:test';
import assert from 'node:assert/strict';

import { resolveSessionToolKey, sessionToolCandidates, sessionToolIdentity } from './session-tool.js';

test('a detected agent takes precedence over the original terminal kind', () => {
  assert.equal(sessionToolIdentity({ term: true, tool: 'codex' }), 'codex');
  assert.equal(sessionToolIdentity({ term: true, provider: 'gemini' }), 'gemini');
  assert.deepEqual(sessionToolCandidates({ term: true, tool: 'codex', source: 'claude' }), ['codex', 'claude']);
  assert.equal(
    resolveSessionToolKey(
      { term: true, tool: 'codex', source: 'claude' },
      value => ({ codex: 'openai', claude: 'claude' })[value] || '',
    ),
    'openai',
  );
});

test('a pure terminal still falls back to Bash', () => {
  assert.equal(sessionToolIdentity({ term: true }), 'bash');
  assert.equal(sessionToolIdentity({ term: false }), '');
});
