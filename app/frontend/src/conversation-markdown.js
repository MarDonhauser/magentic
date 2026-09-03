import { marked } from 'marked';

// Agent messages are markdown. Raw HTML stays disabled, so file content an
// agent quotes cannot inject markup — the text is escaped and shown as text.
// Nothing here produces HTML from Item text beyond what markdown itself means,
// so no sanitizer is needed.
const options = { gfm: true, breaks: true };

function escapeHtml(text) {
  return String(text ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// renderMarkdown turns an Item's text into HTML. Raw HTML in the source is
// escaped first, so a `<script>` an agent quoted renders as visible text.
export function renderMarkdown(text) {
  const source = String(text ?? '');
  if (!source.trim()) return '';
  return marked.parse(escapeHtml(source), options).trim();
}
