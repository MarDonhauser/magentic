import './conversation.css';

import { renderMarkdown } from './conversation-markdown.js';
import {
  applyReading, applyUpdate, emptyConversationState, renderModel, rowSignature, fnv1a, scrollDecision,
} from './conversation-state.js';

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
    head.textContent = row.label || row.kind;
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
    `<span class="cv-kind">${esc(row.label || row.kind)}</span>` +
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
  const rendered = new Map();

  const headEl = document.createElement('div');
  headEl.className = 'cv-head';
  const listEl = document.createElement('div');
  listEl.className = 'cv-list';
  const footEl = document.createElement('div');
  footEl.className = 'cv-foot';
  root.replaceChildren(headEl, listEl, footEl);

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

  function buildRow(row) {
    const el = rowElement(row, expanded.has(row.id), toggle);
    if (row.children.length) {
      const nested = document.createElement('div');
      nested.className = 'cv-children';
      for (const child of row.children) {
        nested.appendChild(rowElement(child, expanded.has(child.id), toggle));
      }
      el.appendChild(nested);
    }
    return el;
  }

  // reconcile places exactly the rows the model asks for, reusing every
  // element whose row did not change and touching the DOM only where the
  // order actually differs.
  function reconcile(rows) {
    const wanted = rows.map(row => {
      const signature = rowSignature(row, expanded);
      const held = rendered.get(row.id);
      if (held && held.signature === signature) return held.el;
      const el = buildRow(row);
      rendered.set(row.id, { signature, el });
      return el;
    });
    const live = new Set(rows.map(row => row.id));
    for (const id of rendered.keys()) {
      if (!live.has(id)) rendered.delete(id);
    }
    wanted.forEach((el, index) => {
      if (listEl.childNodes[index] !== el) listEl.insertBefore(el, listEl.childNodes[index] || null);
    });
    while (listEl.childNodes.length > wanted.length) listEl.removeChild(listEl.lastChild);
  }

  function draw(hasNewItems) {
    const decision = scrollDecision({
      scrollTop: root.scrollTop, scrollHeight: root.scrollHeight,
      clientHeight: root.clientHeight, hasNewItems,
    });
    const model = renderModel(state, { waiting });

    const head = [];
    if (model.waiting) {
      const el = document.createElement('div');
      el.className = 'cv-waiting';
      const headline = document.createElement('strong');
      headline.textContent = model.waiting.headline;
      const detail = document.createElement('span');
      detail.textContent = model.waiting.detail;
      el.append(headline, detail);
      head.push(el);
    }
    if (model.kind !== 'items') head.push(noticeElement(model));
    headEl.replaceChildren(...head);

    reconcile(model.rows);

    const foot = [];
    for (const action of model.actions) {
      const button = document.createElement('button');
      button.className = 'cv-action';
      button.type = 'button';
      button.textContent = action.label;
      button.addEventListener('click', () => onOpenTerminal?.());
      foot.push(button);
    }
    footEl.replaceChildren(...foot);

    if (decision.follow || (!hasNewItems && decision.atBottom)) {
      root.scrollTop = root.scrollHeight;
    }
  }

  return {
    element: root,
    setReading(result) {
      state = applyReading(result);
      expanded.clear();
      rendered.clear();
      listEl.replaceChildren();
      draw(false);
    },
    applyUpdate(event) {
      if (event?.replaced) {
        rendered.clear();
        listEl.replaceChildren();
      }
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
