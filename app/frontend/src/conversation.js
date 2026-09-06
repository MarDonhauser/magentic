import './conversation.css';

import { renderMarkdown } from './conversation-markdown.js';
import {
  applyReading, applyUpdate, emptyConversationState, renderModel, rowSignature, fnv1a, scrollDecision,
} from './conversation-state.js';
import {
  applyManagedSessionState, emptyManagedSessionState, managedControlModel,
} from './managed-session-state.js';

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
  if (row.inProgress) el.classList.add('cv-in-progress');
  if (row.delegated) el.classList.add('cv-delegated');

  if (!row.collapsed) {
    const head = document.createElement('div');
    head.className = 'cv-kind';
    const label = document.createElement('span');
    label.textContent = row.label || row.kind;
    head.appendChild(label);
    if (row.inProgress) {
      const live = document.createElement('span');
      live.className = 'cv-live';
      live.textContent = 'schreibt gerade';
      head.appendChild(live);
    }
    const body = document.createElement('div');
    body.className = 'cv-prose';
    body.innerHTML = renderMarkdown(row.detail || row.title);
    el.append(head, body);
    return el;
  }

  const line = document.createElement('button');
  line.className = 'cv-line';
  line.type = 'button';
  const state = row.failed ? 'fehlgeschlagen' : row.inProgress ? 'läuft' : row.awaiting ? 'wartet' : '';
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
export function createConversationView({
  host, onOpenTerminal, onInterrupt, onPermissionDecision,
} = {}) {
  const root = host || document.createElement('div');
  root.classList.add('cv-surface');
  let state = emptyConversationState();
  let waiting = false;
  let terminalAvailable = true;
  let managedState = emptyManagedSessionState();
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

    function managedControlElement(model) {
      const el = document.createElement('section');
      el.className = `cv-control cv-control-${model.tone}`;
      el.setAttribute('role', model.tone === 'permission' ? 'alert' : 'status');
      const copy = document.createElement('span');
      copy.className = 'cv-control-copy';
      const headline = document.createElement('strong');
      headline.textContent = model.title;
      const detail = document.createElement('span');
      detail.textContent = model.detail;
      copy.append(headline, detail);
      const actions = document.createElement('span');
      actions.className = 'cv-control-actions';
      for (const action of model.actions) {
        const button = document.createElement('button');
        button.type = 'button';
        button.className = `cv-control-action cv-control-action-${action.tone}`;
        button.textContent = action.label;
        button.addEventListener('click', () => {
          if (action.kind === 'interrupt-turn') onInterrupt?.();
          if (action.kind === 'allow-permission') onPermissionDecision?.(model.requestID, 'allow');
          if (action.kind === 'deny-permission') onPermissionDecision?.(model.requestID, 'deny');
        });
        actions.appendChild(button);
      }
      el.append(copy, actions);
      return el;
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
    const model = renderModel(state, { waiting, terminalAvailable });
    const controls = managedControlModel(managedState);

    const head = [];
    if (controls.visible) head.push(managedControlElement(controls));
    if (model.waiting) {
      const el = document.createElement('div');
      el.className = 'cv-waiting';
      el.setAttribute('role', 'status');
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
    setTerminalAvailable(next) {
      if (terminalAvailable === !!next) return;
      terminalAvailable = !!next;
      draw(false);
    },
    setManagedState(next) {
      managedState = applyManagedSessionState(next);
      draw(false);
    },
    // setControlBusy sperrt die Managed-Aktionen während eine Entscheidung
    // unterwegs ist, damit kein Doppelklick zwei Antworten auslöst. Der
    // nächste draw (z. B. nach dem Refresh) gibt sie wieder frei.
    setControlBusy(busy) {
      for (const button of root.querySelectorAll('.cv-control-action')) {
        button.disabled = !!busy;
      }
    },
  };
}
