// Render model for the Conversation surface. It holds no DOM: the desktop app
// renders what this module decided, and the unit tests read the same model.

const AVAILABLE = 'available';

const TOOL_KINDS = new Set([
  'command-execution', 'file-change', 'file-read', 'tool-call',
  'web-search', 'delegated-task', 'plan', 'context-compaction',
  'reasoning', 'error', 'unknown',
]);

// Prose belongs to the developer and is never hidden behind a toggle.
const PROSE_KINDS = new Set(['agent-message', 'developer-prompt']);

export function emptyConversationState() {
  return { availability: '', vendor: '', reason: '', itemsKnown: false, items: [] };
}

// applyReading takes the answer to "what is this Session's Conversation".
// An unavailable reading carries no Items at all, so it can never be mistaken
// for a run that has done nothing.
export function applyReading(result) {
  const state = emptyConversationState();
  if (!result || typeof result !== 'object') return state;
  state.availability = typeof result.availability === 'string' ? result.availability : '';
  state.vendor = typeof result.vendor === 'string' ? result.vendor : '';
  state.reason = typeof result.reason === 'string' ? result.reason : '';
  state.itemsKnown = state.availability === AVAILABLE && !!result.itemsKnown;
  state.items = state.itemsKnown && Array.isArray(result.items) ? result.items.slice() : [];
  return state;
}

// applyUpdate applies the Items one Observation pass produced. A superseded
// Item replaces its predecessor where it stands; a full re-reading replaces
// everything that was held.
export function applyUpdate(state, event) {
  const base = state && typeof state === 'object' ? state : emptyConversationState();
  const items = Array.isArray(event?.items) ? event.items : [];
  if (event?.replaced) {
    return { ...base, availability: AVAILABLE, itemsKnown: true, reason: '', items: items.slice() };
  }
  const next = base.items.slice();
  for (const item of items) {
    if (!item?.id) continue;
    const at = next.findIndex(held => held.id === item.id);
    if (at >= 0) next[at] = item;
    else next.push(item);
  }
  return { ...base, availability: AVAILABLE, itemsKnown: true, items: next };
}

export function isCollapsible(item) {
  return !PROSE_KINDS.has(item?.kind) && TOOL_KINDS.has(item?.kind);
}

function toRow(item) {
  const detail = typeof item.detail === 'string' ? item.detail : '';
  const collapsible = isCollapsible(item);
  return {
    id: item.id,
    kind: item.kind,
    role: item.role,
    title: item.title || '',
    detail,
    // Prose is shown in full; tool activity is one line until it is expanded.
    collapsed: collapsible,
    expandable: collapsible && detail !== '',
    failed: !!item.failed,
    awaiting: !!item.awaitingResult,
    delegated: !!item.delegated,
    children: [],
  };
}

const NOTICES = {
  'not-applicable': {
    headline: 'Diese Session hostet keinen Coding-Agenten.',
    terminal: true,
  },
  'no-normalizer': {
    headline: 'Die Conversations dieses Agenten können noch nicht gelesen werden.',
    terminal: true,
  },
  'record-not-found': {
    headline: 'Die Aufzeichnung dieses Laufs wurde nicht gefunden.',
    terminal: true,
  },
  'record-unreadable': {
    headline: 'Die Aufzeichnung dieses Laufs konnte nicht gelesen werden.',
    terminal: true,
  },
};

// EMPTY_HEADLINE is deliberately different from every unavailable wording: an
// available Conversation without Items means the run has produced nothing yet.
const EMPTY_HEADLINE = 'Dieser Lauf hat noch nichts hervorgebracht.';

// renderModel is what the surface draws. It groups delegated work under its
// task, states an unavailable reading with its reason, and — when the agent is
// waiting — offers the way to the terminal and nothing that claims to answer
// a permission prompt.
export function renderModel(state, context = {}) {
  const current = state && typeof state === 'object' ? state : emptyConversationState();
  const actions = [];
  const waiting = !!context.waiting;
  if (waiting) {
    actions.push({ kind: 'open-terminal', label: 'Zum Terminal dieser Session' });
  }

  if (current.availability !== AVAILABLE || !current.itemsKnown) {
    const notice = NOTICES[current.availability] || {
      headline: 'Diese Conversation ist derzeit nicht verfügbar.',
      terminal: true,
    };
    if (notice.terminal && !actions.some(action => action.kind === 'open-terminal')) {
      actions.push({ kind: 'open-terminal', label: 'Zum Terminal dieser Session' });
    }
    return {
      kind: 'notice',
      availability: current.availability || 'unknown',
      headline: notice.headline,
      reason: current.reason || '',
      vendor: current.vendor || '',
      terminalReachable: true,
      rows: [],
      waiting: waitingModel(waiting),
      actions,
    };
  }

  const rows = groupRows(current.items);
  return {
    kind: rows.length ? 'items' : 'empty',
    availability: AVAILABLE,
    headline: rows.length ? '' : EMPTY_HEADLINE,
    reason: '',
    vendor: current.vendor || '',
    terminalReachable: true,
    rows,
    waiting: waitingModel(waiting),
    actions,
  };
}

function waitingModel(waiting) {
  if (!waiting) return null;
  return {
    waiting: true,
    headline: 'Der Agent wartet auf dich.',
    // The prompt itself is never recorded in a Conversation, so it can only be
    // answered in the Session's terminal.
    detail: 'Antworten lässt er sich nur im Terminal dieser Session.',
  };
}

const ORPHAN_GROUP = 'delegated:unknown';

// groupRows places delegated Items under the task they belong to, and gathers
// delegated Items whose parent is unknown into one group of their own, so they
// are visible without being mistaken for the run's primary activity.
function groupRows(items) {
  const rows = [];
  const byItemId = new Map();
  let orphans = null;

  for (const item of Array.isArray(items) ? items : []) {
    if (!item?.id) continue;
    const row = toRow(item);
    if (!row.delegated) {
      rows.push(row);
      byItemId.set(row.id, row);
      continue;
    }
    const parent = item.parentTaskId ? byItemId.get(item.parentTaskId) : null;
    if (parent) {
      parent.children.push(row);
      byItemId.set(row.id, row);
      continue;
    }
    if (!orphans) {
      orphans = {
        id: ORPHAN_GROUP,
        kind: 'delegated-task',
        role: 'agent',
        title: 'Delegierte Arbeit ohne bekannte Aufgabe',
        detail: '',
        collapsed: true,
        expandable: false,
        failed: false,
        awaiting: false,
        delegated: true,
        orphaned: true,
        children: [],
      };
      rows.push(orphans);
    }
    orphans.children.push(row);
    byItemId.set(row.id, row);
  }
  return rows;
}

// scrollDecision keeps the surface where the developer put it. It follows new
// Items only while the view is at the live end.
export function scrollDecision({ scrollTop = 0, scrollHeight = 0, clientHeight = 0, hasNewItems = false }, threshold = 24) {
  const distance = scrollHeight - clientHeight - scrollTop;
  const atBottom = distance <= threshold;
  return {
    atBottom,
    follow: atBottom && hasNewItems,
    showJumpToEnd: !atBottom,
  };
}
