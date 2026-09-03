import test from 'node:test';
import assert from 'node:assert/strict';

import { renderMarkdown } from './conversation-markdown.js';

test('Rohes HTML wird als sichtbarer Text gerendert, nicht als Markup', () => {
  const html = renderMarkdown('Vorsicht: <script>alert(1)</script> und <b>fett</b>');
  assert.equal(html.includes('<script>'), false);
  assert.equal(html.includes('<b>'), false);
  assert.equal(html.includes('&lt;script&gt;'), true);
  assert.equal(html.includes('&lt;b&gt;fett&lt;/b&gt;'), true);
});

test('Code-Fences, Listen und Links werden gerendert', () => {
  const html = renderMarkdown([
    'Siehe [die Doku](https://example.com/doku):',
    '',
    '- erster Punkt',
    '- zweiter Punkt',
    '',
    '```go',
    'func main() {}',
    '```',
  ].join('\n'));

  assert.match(html, /<a href="https:\/\/example\.com\/doku"[^>]*>die Doku<\/a>/);
  assert.match(html, /<ul>/);
  assert.match(html, /<li>erster Punkt<\/li>/);
  assert.match(html, /<pre><code class="language-go">/);
  assert.match(html, /func main\(\) \{\}/);
});

test('Leerer Text ergibt kein Markup', () => {
  assert.equal(renderMarkdown(''), '');
  assert.equal(renderMarkdown('   '), '');
  assert.equal(renderMarkdown(null), '');
});
