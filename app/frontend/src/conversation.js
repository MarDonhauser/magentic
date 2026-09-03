import './conversation.css';

import { renderMarkdown } from './conversation-markdown.js';
import {
  applyReading, applyUpdate, emptyConversationState, renderModel, scrollDecision,
} from './conversation-state.js';

const KIND_LABELS = {
  'developer-prompt': 'Eingabe',
  'agent-message': 'Antwort',
  reasoning: 'Überlegung',
  plan: 'Plan',
  'command-execution': 'Befehl',
  'file-change': 'Datei geändert',
  'file-read': 'Gelesen',
  'tool-call': 'Werkzeug',
  'web-search': 'Web',
  'delegated-task': 'Delegiert',
  'context-compaction': 'Kontext',
  error: 'Fehler',
  unknown: 'Unbekannt',
};

function esc(text) {
  return String(text ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function rowElement(row, expanded, toggle) {
  const el = document.createElement('div');
  el.className = 'cv-row cv-' + row.kind;
  el.dataset.id = row.id;
  if (row.failed) el.classList.add('cv-failed');
  if (row.awaiting) el.classList.add('cv-awaiting');
  if (row.delegated) el.classList.add('cv-delegated');

  if (!row.collapsed) {
    const head = document.createElement('div');
    head.className = 'cv-kind';
    head.textContent = KIND_LABELS[row.kind] || row.kind;
    const body = document.createElement('div');
    body.className = 'cv-prose';
    body.innerHTML = renderMarkdown(row.detail || row.title);
    el.append(head, body);
    return el;
  }

  const line = document.createElement('button');
  line.className = 'cv-line';
  line.type = 'button';
  const state = row.failed ? 'fehlgeschlagen' : row.awaiting ? 'läuft noch' : '';
  line.innerHTML =
    `<span class="cv-kind">${esc(KIND_LABELS[row.kind] || row.kind)}</span>` +
    `<span class="cv-title">${esc(row.title)}</span>` +
    (state ? `<span class="cv-state">${esc(state)}</span>` : '');
  if (row.expandable) {
    line.addEventListener('click', () => toggle(row.id));
    line.setAttribute('aria-expanded', expanded ? 'true' : 'false');
  } else {
    line.disabled = true;
  }
  el.appendChild(line);

  if (row.expandable && expanded) {
    const detail = document.createElement('pre');
    detail.className = 'cv-detail';
    detail.textContent = row.detail;
    el.appendChild(detail);
  }
  return el;
}

// createConversationView renders a Session's Conversation into one host. It
// reads only: nothing here starts, answers or writes to a Session.
export function createConversationView({ host, onOpenTerminal } = {}) {
  const root = host || document.createElement('div');
  root.classList.add('cv-surface');
  let state = emptyConversationState();
  let waiting = false;
  const expanded = new Set();

  function toggle(id) {
    if (expanded.has(id)) expanded.delete(id);
    else expanded.add(id);
    draw(false);
  }

  function noticeElement(model) {
    const el = document.createElement('div');
    el.className = 'cv-notice';
    const headline = document.createElement('strong');
    headline.textContent = model.headline;
    el.appendChild(headline);
    if (model.reason) {
      const reason = document.createElement('span');
      reason.textContent = model.reason;
      el.appendChild(reason);
    }
    if (model.vendor && model.availability === 'no-normalizer') {
      const vendor = document.createElement('span');
      vendor.textContent = 'Agent: ' + model.vendor;
      el.appendChild(vendor);
    }
    return el;
  }

  function actionsElement(model) {
    if (!model.actions.length) return null;
    const el = document.createElement('div');
    el.className = 'cv-actions';
    for (const action of model.actions) {
      const button = document.createElement('button');
      button.className = 'cv-action';
      button.type = 'button';
      button.textContent = action.label;
      button.addEventListener('click', () => onOpenTerminal?.());
      el.appendChild(button);
    }
    return el;
  }

  function draw(hasNewItems) {
    const decision = scrollDecision({
      scrollTop: root.scrollTop, scrollHeight: root.scrollHeight,
      clientHeight: root.clientHeight, hasNewItems,
    });
    const model = renderModel(state, { waiting });
    root.replaceChildren();

    if (model.waiting) {
      const el = document.createElement('div');
      el.className = 'cv-waiting';
      const headline = document.createElement('strong');
      headline.textContent = model.waiting.headline;
      const detail = document.createElement('span');
      detail.textContent = model.waiting.detail;
      el.append(headline, detail);
      root.appendChild(el);
    }
    if (model.kind !== 'items') {
      root.appendChild(noticeElement(model));
    }
    for (const row of model.rows) {
      const el = rowElement(row, expanded.has(row.id), toggle);
      if (row.children.length) {
        const nested = document.createElement('div');
        nested.className = 'cv-children';
        for (const child of row.children) {
          nested.appendChild(rowElement(child, expanded.has(child.id), toggle));
        }
        el.appendChild(nested);
      }
      root.appendChild(el);
    }
    const actions = actionsElement(model);
    if (actions) root.appendChild(actions);

    if (decision.follow || (!hasNewItems && decision.atBottom)) {
      root.scrollTop = root.scrollHeight;
    }
  }

  return {
    element: root,
    setReading(result) {
      state = applyReading(result);
      expanded.clear();
      draw(false);
    },
    applyUpdate(event) {
      state = applyUpdate(state, event);
      draw(true);
    },
    setWaiting(next) {
      if (waiting === !!next) return;
      waiting = !!next;
      draw(false);
    },
  };
}
