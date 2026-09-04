import './dock.css';

import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { BrowserOpenURL } from '../wailsjs/runtime/runtime';
import { developerIcon } from './avatar.js';
import { onThemeChange, terminalTheme, terminalContrastFloor } from './theme.js';
import { TERMINAL_OPTIONS, setUpTerminal } from './terminal-setup.js';
import {
  createLeaf, listLeaves, findLeafByTabKey, getNode, removeTab, addTabToLeaf,
  setActiveTab, resizeSplit, moveTabToEdge, serializeTree, dockRefKey, normalizeDockRef,
} from './dock-tree.js';
import { normalizeDockState, resolveLegacyDockRefs } from './dock-state.js';
import { createConversationView } from './conversation.js';

const STORE_KEY = 'magentic.dock';
const DEFAULT_HEIGHT = 280;
const MIN_HEIGHT = 120;
const MAX_RATIO = 0.8;
const DOCK_ICONS = {
  plus: '<path d="M12 5v14M5 12h14"/>',
  down: '<path d="m6 9 6 6 6-6"/>',
  close: '<path d="M18 6 6 18M6 6l12 12"/>',
  terminal: '<path d="m4 17 6-5-6-5M12 19h8"/>',
  conversation: '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>',
};

function dockIcon(name) {
  return `<svg class="dk-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${DOCK_ICONS[name]}</svg>`;
}

const enc = new TextEncoder();

function toB64(s) {
  let bin = '';
  for (const b of enc.encode(s)) bin += String.fromCharCode(b);
  return btoa(bin);
}

function fromB64(b64) {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

const tabs = new Map();
const leafDom = new Map();
const nodeDom = new Map();

onThemeChange(theme => {
  const nextTheme = terminalTheme(theme);
  const nextFloor = terminalContrastFloor(theme);
  for (const tab of tabs.values()) {
    if (!tab.term) continue;
    tab.term.options.theme = nextTheme;
    tab.term.options.minimumContrastRatio = nextFloor;
  }
});

let cb = {};
let mounted = false;
let open = false;
let height = DEFAULT_HEIGHT;
let rootNode = createLeaf();
let focusedLeafId = rootNode.id;
let dockEl = null;
let topBarEl = null;
let emptyTitleEl = null;
let treeEl = null;
let fitPending = false;
let selfResize = false;
let dragTab = null;
let dropOverlayEl = null;
let menuEl = null;
let menuAbort = null;

function maxHeight() {
  return Math.max(MIN_HEIGHT, Math.round(window.innerHeight * MAX_RATIO));
}

function readState() {
  try {
    const raw = JSON.parse(localStorage.getItem(STORE_KEY) || 'null');
    return normalizeDockState(raw, DEFAULT_HEIGHT);
  } catch {
    return null;
  }
}

function focusedLeaf() {
  const found = getNode(rootNode, focusedLeafId);
  if (found && found.type === 'leaf') return found;
  const first = listLeaves(rootNode)[0];
  focusedLeafId = first.id;
  return first;
}

function activeTab() {
  const leaf = focusedLeaf();
  return leaf.activeKey ? tabs.get(leaf.activeKey) : null;
}

function persist() {
  try {
    localStorage.setItem(STORE_KEY, JSON.stringify({
      open,
      height,
      layout: serializeTree(rootNode),
      focused: activeTab()?.key || null,
    }));
  } catch { /* Speicher voll oder gesperrt — Zustand ist dann eben flüchtig */ }
}

function clampHeight(v) {
  return Math.min(Math.max(Math.round(v), MIN_HEIGHT), maxHeight());
}

function applyHeight() {
  document.body.style.setProperty('--dk-h', clampHeight(height) + 'px');
}

function applyOpen() {
  document.body.classList.toggle('dk-open', open);
}

function openURL(uri) {
  try { BrowserOpenURL(uri); } catch { window.open(uri, '_blank'); }
}

function fallbackCopy(text) {
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.cssText = 'position:fixed;top:0;left:0;opacity:0;pointer-events:none';
  document.body.appendChild(ta);
  ta.select();
  try { document.execCommand('copy'); } catch { /* ohne Zwischenablage-Rechte nicht möglich */ }
  ta.remove();
  activeTab()?.term?.focus();
}

// Über die Wails-Runtime statt navigator.clipboard: WebKit blendet für den
// Lesezugriff eine Bestätigung („paste") ein, die bei jedem Einfügen erscheint.
// Der Weg über Go umgeht sie.
function copyText(text) {
  if (cb.setClipboard) {
    Promise.resolve(cb.setClipboard(text)).catch(() => fallbackCopy(text));
    return;
  }
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).catch(() => fallbackCopy(text));
    return;
  }
  fallbackCopy(text);
}

function isToggleKey(e) {
  return e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey && (e.code === 'Backquote' || e.key === '`');
}

function isTabNavKey(e) {
  return e.metaKey && e.altKey && !e.ctrlKey && (e.key === 'ArrowLeft' || e.key === 'ArrowRight');
}

function fitNow(t) {
  if (!open || !t?.fit || !t.term) return;
  const dom = leafDom.get(t.leafId);
  if (!dom) return;
  const box = dom.bodyEl.getBoundingClientRect();
  if (box.width < 2 || box.height < 2) return;
  try { t.fit.fit(); } catch { return; }
  try { cb.resize?.(t.ref, t.term.cols, t.term.rows); } catch { /* Backend meldet sich beim nächsten Versuch */ }
}

function scheduleFit() {
  if (fitPending) return;
  fitPending = true;
  requestAnimationFrame(() => {
    fitPending = false;
    for (const leaf of listLeaves(rootNode)) {
      const t = leaf.activeKey ? tabs.get(leaf.activeKey) : null;
      if (t) fitNow(t);
    }
  });
}

function notifyLayout() {
  scheduleFit();
  selfResize = true;
  try { window.dispatchEvent(new Event('resize')); } finally { selfResize = false; }
}

function bindKeys(term, t) {
  let lastSel = '';
  let lastSelAt = 0;
  term.onSelectionChange(() => {
    const s = term.getSelection();
    if (s) { lastSel = s; lastSelAt = Date.now(); }
  });
  term.attachCustomKeyEventHandler(e => {
    if (e.type !== 'keydown') return true;
    if (isToggleKey(e) || isTabNavKey(e)) return false;
    if (!e.metaKey || e.ctrlKey || e.altKey) return true;
    const k = e.key.toLowerCase();
    if (k === 'c') {
      const sel = term.getSelection() || (Date.now() - lastSelAt < 30000 ? lastSel : '');
      if (sel) {
        copyText(sel);
        e.preventDefault();
        return false;
      }
    }
    // Cmd+V bewusst durchlassen: xterm.js nimmt das native Einfügen entgegen
    // und liefert den Text über onData. Läse man die Zwischenablage selbst
    // aus, verlangte WebKit bei jedem Einfügen eine Bestätigung.
    return true;
  });
}

function syncDot(t) {
  let color = 'var(--muted)';
  let label = '';
  try {
    const s = cb.status?.(t.ref);
    if (s) {
      color = s.color || color;
      label = s.label || '';
    }
  } catch { /* Statusquelle darf das Dock nicht mitreißen */ }
  if (t.closed) {
    color = 'var(--critical)';
    label = label || 'beendet';
  }
  t.dot.style.background = color;
  t.el.title = label ? `${t.name} — ${label}` : t.name;
}

function updateStatuses() {
  for (const t of tabs.values()) syncDot(t);
}

function updateBlank() {
  const blank = tabs.size === 0;
  dockEl.classList.toggle('dk-blank', blank);
  document.body.classList.toggle('dk-no-tabs', blank);
}

function buildTabEl(t) {
  const el = document.createElement('div');
  el.className = 'dk-tab';
  el.dataset.key = t.key;
  el.draggable = true;

  const dot = document.createElement('span');
  dot.className = 'dk-dot';

  const label = document.createElement('span');
  label.className = 'dk-name';
  label.textContent = t.name;

  const tool = document.createElement('span');
  tool.className = 'dk-tool';
  tool.setAttribute('aria-hidden', 'true');
  tool.innerHTML = developerIcon('bash');

  const surface = document.createElement('button');
  surface.className = 'dk-surface';
  surface.type = 'button';
  surface.addEventListener('click', e => {
    e.stopPropagation();
    showSurface(t, t.surface === 'conversation' ? 'terminal' : 'conversation');
  });

  const x = document.createElement('button');
  x.className = 'dk-x';
  x.type = 'button';
  x.innerHTML = dockIcon('close');
  x.setAttribute('aria-label', `Tab ${t.name} schließen`);
  x.title = 'Tab schließen';

  el.append(dot, tool, label, surface, x);
  t.el = el;
  t.dot = dot;
  t.surfaceBtn = surface;
  syncSurfaceButton(t);
}

function addTab(ref) {
  const key = dockRefKey(ref);
  if (!key) return null;
  if (tabs.has(key)) return tabs.get(key);
  const name = ref.name;

  const pane = document.createElement('div');
  pane.className = 'dk-pane';
  const host = document.createElement('div');
  host.className = 'dk-term';
  const convHost = document.createElement('div');
  pane.append(host, convHost);

  const t = { ref, key, name, el: null, dot: null, pane, host, convHost, conv: null, surface: 'terminal', term: null, fit: null, offData: null, offClosed: null, live: false, closed: false, leafId: null };
  buildTabEl(t);
  tabs.set(key, t);
  syncDot(t);
  return t;
}

function ensureLive(t) {
  if (t.live) return;
  t.live = true;

  const term = new Terminal({
    ...TERMINAL_OPTIONS,
    fontSize: 13,
    lineHeight: 1.1,
    // Nur der jeweils sichtbare Tab je Blatt kostet einen Terminal-Puffer:
    // versteckte Tabs erzeugen ihr Terminal erst bei der Aktivierung
    // (ensureLive). Deshalb darf der sichtbare Verlauf hier großzügig sein.
    scrollback: 10000,
    theme: terminalTheme(),
    minimumContrastRatio: terminalContrastFloor(),
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new WebLinksAddon((e, uri) => openURL(uri)));
  term.open(t.host);
  setUpTerminal(term, () => fit.fit());
  bindKeys(term, t);

  term.onData(d => cb.write?.(t.ref, toB64(d)));
  term.onResize(({ cols, rows }) => cb.resize?.(t.ref, cols, rows));

  t.term = term;
  t.fit = fit;

  try { t.offData = cb.onData?.(t.ref, b64 => term.write(fromB64(b64))) || null; } catch { t.offData = null; }
  try {
    t.offClosed = cb.onClosed?.(t.ref, () => {
      t.closed = true;
      term.write('\r\n\x1b[31m— Verbindung beendet —\x1b[0m\r\n');
      syncDot(t);
    }) || null;
  } catch { t.offClosed = null; }

  try { fit.fit(); } catch { /* Pane noch ohne Maße */ }
  Promise.resolve(cb.attach?.(t.ref, term.cols, term.rows))
    .catch(err => term.write('\x1b[31m' + err + '\x1b[0m\r\n'));
}

// showSurface switches one tab between its terminal and its Conversation. It
// changes nothing about the Session: no lifecycle call, no runtime command,
// and the selected tab stays the selected tab.
function showSurface(t, surface) {
  if (!t || t.surface === surface) return;
  t.surface = surface;
  t.pane.classList.toggle('cv-showing', surface === 'conversation');
  syncSurfaceButton(t);
  if (surface !== 'conversation') {
    cb.watchConversation?.(null);
    fitNow(t);
    t.term?.focus();
    return;
  }
  ensureConversation(t);
  cb.watchConversation?.(t.ref);
  Promise.resolve(cb.readConversation?.(t.ref))
    .then(reading => { if (t.surface === 'conversation') t.conv.setReading(reading); })
    .catch(() => { /* Die Lesung selbst nennt ihren Grund. */ });
}

function ensureConversation(t) {
  if (t.conv) return t.conv;
  t.conv = createConversationView({
    host: t.convHost,
    onOpenTerminal: () => showSurface(t, 'terminal'),
  });
  return t.conv;
}

function syncSurfaceButton(t) {
  if (!t?.surfaceBtn) return;
  const showsConversation = t.surface === 'conversation';
  t.surfaceBtn.innerHTML = dockIcon(showsConversation ? 'terminal' : 'conversation');
  t.surfaceBtn.title = showsConversation ? 'Terminal zeigen' : 'Verlauf zeigen';
  t.surfaceBtn.setAttribute('aria-label', t.surfaceBtn.title);
}

// applyConversationUpdate hands the Items an Observation pass produced to the
// tab that is currently showing that Session's Conversation.
export function applyConversationUpdate(event) {
  for (const t of tabs.values()) {
    if (t.surface !== 'conversation' || !t.conv) continue;
    if (String(t.ref?.id || '') !== String(event?.sessionId || '')) continue;
    t.conv.applyUpdate(event);
  }
}

// setConversationWaiting states in the surface that the agent is waiting. The
// surface only points at the terminal; it never offers to answer a prompt.
export function setConversationWaiting(sessionID, waiting) {
  for (const t of tabs.values()) {
    if (!t.conv || String(t.ref?.id || '') !== String(sessionID || '')) continue;
    t.conv.setWaiting(waiting);
  }
}

function activate(key) {
  const t = tabs.get(key);
  if (!t) return;
  const leaf = getNode(rootNode, t.leafId);
  if (!leaf) return;
  rootNode = setActiveTab(rootNode, leaf.id, key);
  focusedLeafId = leaf.id;
  for (const ref of leaf.tabs) {
    const other = tabs.get(dockRefKey(ref));
    if (!other) continue;
    const on = dockRefKey(ref) === key;
    other.el.classList.toggle('dk-active', on);
    other.pane.classList.toggle('dk-on', on);
  }
  t.el.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  if (open) {
    ensureLive(t);
    fitNow(t);
    t.term?.focus();
  }
  try { cb.onActive?.(t.ref); } catch { /* optionaler Callback */ }
  persist();
}

function stepTab(dir) {
  const leaf = focusedLeaf();
  const list = leaf.tabs.map(dockRefKey);
  if (list.length < 2) return;
  const i = list.indexOf(leaf.activeKey);
  const next = list[((i < 0 ? 0 : i) + dir + list.length) % list.length];
  activate(next);
}

async function spawnTerminal() {
  if (!cb.newTerminal) return;
  let ref = null;
  try { ref = await cb.newTerminal(); } catch { return; }
  if (ref) openDockTab(ref);
}

function edgeForPoint(el, x, y) {
  const box = el.getBoundingClientRect();
  const rx = (x - box.left) / box.width;
  const ry = (y - box.top) / box.height;
  const margin = 0.25;
  if (rx < margin) return 'left';
  if (rx > 1 - margin) return 'right';
  if (ry < margin) return 'top';
  if (ry > 1 - margin) return 'bottom';
  return null;
}

function showDropOverlay(bodyEl, x, y) {
  const edge = edgeForPoint(bodyEl, x, y);
  if (!dropOverlayEl) {
    dropOverlayEl = document.createElement('div');
    dropOverlayEl.className = 'dk-drop-overlay';
  }
  if (dropOverlayEl.parentElement !== bodyEl) bodyEl.appendChild(dropOverlayEl);
  dropOverlayEl.className = 'dk-drop-overlay' + (edge ? ` dk-drop-${edge}` : ' dk-drop-hidden');
}

function clearDropOverlay() {
  dropOverlayEl?.remove();
}

function applySplit(tabKey, targetLeafId, edge) {
  const t = tabs.get(tabKey);
  if (!t) return;
  rootNode = moveTabToEdge(rootNode, t.ref, targetLeafId, edge);
  focusedLeafId = findLeafByTabKey(rootNode, tabKey)?.id || focusedLeafId;
  renderTree();
  activate(tabKey);
}

function closeSplitMenu() {
  menuAbort?.abort();
  menuAbort = null;
  menuEl?.remove();
  menuEl = null;
}

function showSplitMenu(x, y, tabKey, leafId) {
  closeSplitMenu();
  const leaf = getNode(rootNode, leafId);
  if (!leaf || leaf.tabs.length < 2) return;
  menuEl = document.createElement('div');
  menuEl.className = 'dk-menu';
  menuEl.style.left = x + 'px';
  menuEl.style.top = y + 'px';
  const items = [
    ['left', 'Nach links teilen'],
    ['right', 'Nach rechts teilen'],
    ['top', 'Nach oben teilen'],
    ['bottom', 'Nach unten teilen'],
  ];
  for (const [edge, label] of items) {
    const item = document.createElement('button');
    item.type = 'button';
    item.className = 'dk-menu-item';
    item.textContent = label;
    item.addEventListener('click', () => {
      closeSplitMenu();
      applySplit(tabKey, leafId, edge);
    });
    menuEl.appendChild(item);
  }
  document.body.appendChild(menuEl);
  menuAbort = new AbortController();
  requestAnimationFrame(() => {
    window.addEventListener('click', closeSplitMenu, { signal: menuAbort.signal });
    window.addEventListener('contextmenu', closeSplitMenu, { signal: menuAbort.signal });
  });
}

function bindLeafEvents(leafId, tabsEl, bodyEl) {
  tabsEl.addEventListener('click', e => {
    const tabEl = e.target.closest('.dk-tab');
    if (!tabEl) return;
    if (e.target.closest('.dk-x')) { closeDockTab(tabEl.dataset.key); return; }
    focusedLeafId = leafId;
    activate(tabEl.dataset.key);
  });
  tabsEl.addEventListener('mousedown', e => {
    if (e.button === 1) e.preventDefault();
  });
  tabsEl.addEventListener('auxclick', e => {
    if (e.button !== 1) return;
    const tabEl = e.target.closest('.dk-tab');
    if (!tabEl) return;
    e.preventDefault();
    closeDockTab(tabEl.dataset.key);
  });
  tabsEl.addEventListener('contextmenu', e => {
    const tabEl = e.target.closest('.dk-tab');
    if (!tabEl) return;
    e.preventDefault();
    showSplitMenu(e.clientX, e.clientY, tabEl.dataset.key, leafId);
  });
  tabsEl.addEventListener('dragstart', e => {
    const tabEl = e.target.closest('.dk-tab');
    if (!tabEl) return;
    dragTab = tabEl.dataset.key;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', dragTab);
  });
  tabsEl.addEventListener('dragend', () => { dragTab = null; clearDropOverlay(); });

  bodyEl.addEventListener('dragover', e => {
    if (!dragTab) return;
    e.preventDefault();
    showDropOverlay(bodyEl, e.clientX, e.clientY);
  });
  bodyEl.addEventListener('dragleave', e => {
    if (e.target === bodyEl) clearDropOverlay();
  });
  bodyEl.addEventListener('drop', e => {
    if (!dragTab) return;
    e.preventDefault();
    const edge = edgeForPoint(bodyEl, e.clientX, e.clientY);
    clearDropOverlay();
    if (edge) applySplit(dragTab, leafId, edge);
    dragTab = null;
  });
}

function buildLeafDom(leafId) {
  const rootEl = document.createElement('div');
  rootEl.className = 'dk-leaf';

  const bar = document.createElement('div');
  bar.className = 'dk-bar';
  const tabsEl = document.createElement('div');
  tabsEl.className = 'dk-tabs';
  bar.appendChild(tabsEl);

  const bodyEl = document.createElement('div');
  bodyEl.className = 'dk-body';

  const empty = document.createElement('div');
  empty.className = 'dk-empty';
  const emptyIcon = document.createElement('div');
  emptyIcon.className = 'dk-empty-icon';
  emptyIcon.setAttribute('aria-hidden', 'true');
  emptyIcon.innerHTML = developerIcon('bash');
  const emptyCopy = document.createElement('div');
  emptyCopy.className = 'dk-empty-copy';
  const emptyText = document.createElement('strong');
  emptyText.textContent = 'Noch kein Terminal';
  const emptyDetail = document.createElement('span');
  emptyDetail.textContent = 'Starte eine Shell im aktuellen Projekt.';
  emptyCopy.append(emptyText, emptyDetail);
  const emptyBtn = document.createElement('button');
  emptyBtn.className = 'dk-empty-btn';
  emptyBtn.type = 'button';
  emptyBtn.textContent = 'Terminal starten';
  emptyBtn.addEventListener('click', spawnTerminal);
  const hint = document.createElement('div');
  hint.className = 'dk-hint';
  hint.textContent = '⌃` ein-/ausblenden';
  empty.append(emptyIcon, emptyCopy, emptyBtn, hint);
  bodyEl.appendChild(empty);

  rootEl.append(bar, bodyEl);
  bindLeafEvents(leafId, tabsEl, bodyEl);

  return { rootEl, tabsEl, bodyEl };
}

function reconcileLeafDom(leaf) {
  let dom = leafDom.get(leaf.id);
  if (!dom) {
    dom = buildLeafDom(leaf.id);
    leafDom.set(leaf.id, dom);
  }
  for (const ref of leaf.tabs) {
    const t = tabs.get(dockRefKey(ref));
    if (!t) continue;
    t.leafId = leaf.id;
    dom.tabsEl.appendChild(t.el);
    dom.bodyEl.appendChild(t.pane);
    const on = dockRefKey(ref) === leaf.activeKey;
    t.el.classList.toggle('dk-active', on);
    t.pane.classList.toggle('dk-on', on);
    if (on && open) ensureLive(t);
  }
  return dom.rootEl;
}

function startSplitDrag(e, splitId) {
  if (e.button !== 0) return;
  e.preventDefault();
  const dom = nodeDom.get(splitId);
  if (!dom) return;
  const node = getNode(rootNode, splitId);
  const horizontal = node.dir === 'row';
  document.body.classList.add('dk-dragging-split', horizontal ? 'dk-dragging-split-row' : 'dk-dragging-split-column');
  let raf = 0;
  let ratio = node.ratio;
  const move = ev => {
    const box = dom.rootEl.getBoundingClientRect();
    ratio = horizontal ? (ev.clientX - box.left) / box.width : (ev.clientY - box.top) / box.height;
    if (raf) return;
    raf = requestAnimationFrame(() => {
      raf = 0;
      rootNode = resizeSplit(rootNode, splitId, ratio);
      const n = getNode(rootNode, splitId);
      dom.aEl.style.flexGrow = String(n.ratio);
      dom.bEl.style.flexGrow = String(1 - n.ratio);
      scheduleFit();
    });
  };
  const up = () => {
    window.removeEventListener('mousemove', move, true);
    window.removeEventListener('mouseup', up, true);
    if (raf) cancelAnimationFrame(raf);
    document.body.classList.remove('dk-dragging-split', 'dk-dragging-split-row', 'dk-dragging-split-column');
    persist();
    notifyLayout();
  };
  window.addEventListener('mousemove', move, true);
  window.addEventListener('mouseup', up, true);
}

function renderNode(node) {
  if (node.type === 'leaf') return reconcileLeafDom(node);

  let dom = nodeDom.get(node.id);
  if (!dom) {
    const rootEl = document.createElement('div');
    rootEl.className = 'dk-split';
    const aEl = document.createElement('div');
    aEl.className = 'dk-split-pane';
    const grip = document.createElement('div');
    grip.className = 'dk-split-grip';
    grip.addEventListener('mousedown', e => startSplitDrag(e, node.id));
    const bEl = document.createElement('div');
    bEl.className = 'dk-split-pane';
    rootEl.append(aEl, grip, bEl);
    dom = { rootEl, aEl, grip, bEl };
    nodeDom.set(node.id, dom);
  }
  dom.rootEl.classList.toggle('dk-split-row', node.dir === 'row');
  dom.rootEl.classList.toggle('dk-split-column', node.dir === 'column');
  dom.aEl.style.flexGrow = String(node.ratio);
  dom.bEl.style.flexGrow = String(1 - node.ratio);

  const aChild = renderNode(node.a);
  if (aChild.parentElement !== dom.aEl) dom.aEl.appendChild(aChild);
  const bChild = renderNode(node.b);
  if (bChild.parentElement !== dom.bEl) dom.bEl.appendChild(bChild);

  return dom.rootEl;
}

function renderTree() {
  const seen = new Set();
  (function mark(node) {
    seen.add(node.id);
    if (node.type === 'split') { mark(node.a); mark(node.b); }
  })(rootNode);
  for (const [id, dom] of [...nodeDom]) {
    if (!seen.has(id)) { dom.rootEl.remove(); nodeDom.delete(id); }
  }
  for (const [id, dom] of [...leafDom]) {
    if (!seen.has(id)) { dom.rootEl.remove(); leafDom.delete(id); }
  }
  const top = renderNode(rootNode);
  if (top.parentElement !== treeEl) treeEl.appendChild(top);
  updateBlank();
}

function startDrag(e) {
  if (e.button !== 0) return;
  e.preventDefault();
  document.body.classList.add('dk-dragging');
  let raf = 0;
  let target = height;
  const move = ev => {
    target = window.innerHeight - ev.clientY;
    if (raf) return;
    raf = requestAnimationFrame(() => {
      raf = 0;
      height = clampHeight(target);
      applyHeight();
      notifyLayout();
    });
  };
  const up = () => {
    window.removeEventListener('mousemove', move, true);
    window.removeEventListener('mouseup', up, true);
    if (raf) cancelAnimationFrame(raf);
    document.body.classList.remove('dk-dragging');
    applyHeight();
    persist();
    notifyLayout();
  };
  window.addEventListener('mousemove', move, true);
  window.addEventListener('mouseup', up, true);
}

function buildDom() {
  dockEl = document.createElement('div');
  dockEl.className = 'dk-dock dk-blank';
  dockEl.id = 'dk-dock';

  const grip = document.createElement('div');
  grip.className = 'dk-resize';
  grip.title = 'Höhe ziehen — Doppelklick setzt zurück';
  grip.addEventListener('mousedown', startDrag);
  grip.addEventListener('dblclick', () => {
    height = DEFAULT_HEIGHT;
    applyHeight();
    persist();
    notifyLayout();
  });

  topBarEl = document.createElement('div');
  topBarEl.className = 'dk-bar dk-topbar';

  emptyTitleEl = document.createElement('div');
  emptyTitleEl.className = 'dk-empty-title';
  emptyTitleEl.innerHTML = `${developerIcon('bash')}<span>Terminal-Dock</span>`;

  const actions = document.createElement('div');
  actions.className = 'dk-actions';

  const plus = document.createElement('button');
  plus.className = 'dk-btn';
  plus.type = 'button';
  plus.innerHTML = dockIcon('plus');
  plus.setAttribute('aria-label', 'Neues Terminal starten');
  plus.title = 'Neues Terminal';
  plus.addEventListener('click', spawnTerminal);

  const hide = document.createElement('button');
  hide.className = 'dk-btn';
  hide.type = 'button';
  hide.innerHTML = dockIcon('down');
  hide.setAttribute('aria-label', 'Terminal-Dock einklappen');
  hide.title = 'Dock einklappen (⌃`)';
  hide.addEventListener('click', () => toggleDock(false));

  actions.append(plus, hide);
  topBarEl.append(emptyTitleEl, actions);

  treeEl = document.createElement('div');
  treeEl.className = 'dk-tree';

  dockEl.append(grip, topBarEl, treeEl);
  document.body.appendChild(dockEl);
  renderTree();
}

function onWindowResize() {
  if (selfResize) return;
  applyHeight();
  scheduleFit();
}

function onKeyDown(e) {
  if (isToggleKey(e)) {
    e.preventDefault();
    e.stopPropagation();
    toggleDock();
    return;
  }
  if (isTabNavKey(e)) {
    if (!open) return;
    e.preventDefault();
    e.stopPropagation();
    stepTab(e.key === 'ArrowRight' ? 1 : -1);
  }
}

export function mountDock(opts) {
  if (mounted) return;
  mounted = true;
  cb = opts || {};

  const saved = readState();
  height = saved?.height ?? DEFAULT_HEIGHT;
  open = !!saved?.open;

  applyHeight();
  applyOpen();
  buildDom();

  const restore = async () => {
    let layout = saved?.layout || createLeaf();
    const allRefs = listLeaves(layout).flatMap(l => l.tabs);
    const legacyNames = allRefs.filter(ref => !ref.id).map(ref => ref.name);
    if (legacyNames.length && cb.migrateLegacy) {
      try {
        const resolved = resolveLegacyDockRefs(allRefs, await cb.migrateLegacy(legacyNames));
        layout = createLeaf(resolved, resolved[0] ? dockRefKey(resolved[0]) : null);
      } catch { /* bei transientem Registry-Fehler bleibt der explizite Legacy-Pfad erhalten */ }
    }
    rootNode = layout;
    for (const leaf of listLeaves(rootNode)) {
      for (const ref of leaf.tabs) addTab(ref);
    }
    focusedLeafId = listLeaves(rootNode)[0].id;
    renderTree();

    const requestedKey = typeof saved?.focused === 'string' ? saved.focused : null;
    const target = requestedKey && tabs.has(requestedKey) ? tabs.get(requestedKey) : null;
    if (target) {
      focusedLeafId = target.leafId;
      activate(target.key);
    } else {
      const first = listLeaves(rootNode)[0];
      if (first.activeKey) activate(first.activeKey);
      else persist();
    }
  };
  restore();

  window.addEventListener('resize', onWindowResize);
  window.addEventListener('keydown', onKeyDown, true);
  setInterval(() => {
    if (open && !document.hidden) updateStatuses();
  }, 3000);

  if (open) requestAnimationFrame(notifyLayout);
}

export function toggleDock(next) {
  if (!mounted) return;
  const want = next === undefined ? !open : !!next;
  if (want === open) return;
  open = want;
  applyOpen();
  persist();
  notifyLayout();
  if (!open) return;
  renderTree();
  const leaf = focusedLeaf();
  if (leaf.activeKey) activate(leaf.activeKey);
  else updateStatuses();
}

export function isDockOpen() {
  return open;
}

function openDockTab(value) {
  const ref = normalizeDockRef(value);
  const key = dockRefKey(ref);
  if (!mounted || !ref || !key) return;
  if (!tabs.has(key)) {
    addTab(ref);
    const leaf = focusedLeaf();
    rootNode = addTabToLeaf(rootNode, leaf.id, ref);
  }
  if (!open) {
    open = true;
    applyOpen();
    notifyLayout();
  }
  renderTree();
  activate(key);
}

export function closeDockTab(value) {
  const key = typeof value === 'string' && tabs.has(value) ? value : dockRefKey(value);
  const t = tabs.get(key);
  if (!t) return;

  const leaf = getNode(rootNode, t.leafId);
  const order = leaf ? leaf.tabs.map(dockRefKey) : [];
  const i = order.indexOf(key);
  const wasFocused = focusedLeafId === t.leafId;
  const wasActive = leaf?.activeKey === key;

  tabs.delete(key);
  rootNode = removeTab(rootNode, key);

  try { t.offData?.(); } catch { /* Listener war schon weg */ }
  try { t.offClosed?.(); } catch { /* Listener war schon weg */ }
  if (t.live) {
    try { cb.close?.(t.ref); } catch { /* Backend hat die Sitzung evtl. selbst beendet */ }
  }
  try { t.term?.dispose(); } catch { /* bereits entsorgt */ }
  t.el.remove();
  t.pane.remove();

  renderTree();

  if (!wasFocused || !wasActive) { persist(); return; }

  const stillThere = getNode(rootNode, t.leafId);
  if (stillThere) {
    focusedLeafId = t.leafId;
    const rest = order.filter(k => k !== key);
    const next = rest[i] || rest[i - 1] || null;
    if (next) { activate(next); return; }
    persist();
    return;
  }
  const first = listLeaves(rootNode)[0];
  focusedLeafId = first.id;
  if (first.activeKey) activate(first.activeKey);
  else persist();
}

export function dockTabs() {
  return [...tabs.values()].map(tab => ({ ...tab.ref }));
}

export function refitDock() {
  if (!mounted) return;
  applyHeight();
  scheduleFit();
}
