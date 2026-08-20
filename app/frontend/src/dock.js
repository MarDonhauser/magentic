import './dock.css';

import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { BrowserOpenURL } from '../wailsjs/runtime/runtime';
import { developerIcon } from './avatar.js';
import { onThemeChange, terminalTheme } from './theme.js';

const STORE_KEY = 'magentic.dock';
const DEFAULT_HEIGHT = 280;
const MIN_HEIGHT = 120;
const MAX_RATIO = 0.8;
const DOCK_ICONS = {
  plus: '<path d="M12 5v14M5 12h14"/>',
  down: '<path d="m6 9 6 6 6-6"/>',
  close: '<path d="M18 6 6 18M6 6l12 12"/>',
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

onThemeChange(theme => {
  const nextTheme = terminalTheme(theme);
  for (const tab of tabs.values()) {
    if (tab.term) tab.term.options.theme = nextTheme;
  }
});

let cb = {};
let mounted = false;
let open = false;
let height = DEFAULT_HEIGHT;
let activeName = null;
let dockEl = null;
let tabsEl = null;
let bodyEl = null;
let fitPending = false;
let selfResize = false;

function maxHeight() {
  return Math.max(MIN_HEIGHT, Math.round(window.innerHeight * MAX_RATIO));
}

function readState() {
  try {
    const raw = JSON.parse(localStorage.getItem(STORE_KEY) || 'null');
    if (!raw || typeof raw !== 'object') return null;
    return {
      open: !!raw.open,
      height: Number.isFinite(raw.height) ? raw.height : DEFAULT_HEIGHT,
      tabs: Array.isArray(raw.tabs) ? raw.tabs.filter(n => typeof n === 'string' && n) : [],
      active: typeof raw.active === 'string' ? raw.active : null,
    };
  } catch {
    return null;
  }
}

function persist() {
  try {
    localStorage.setItem(STORE_KEY, JSON.stringify({ open, height, tabs: [...tabs.keys()], active: activeName }));
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

function activeTab() {
  return activeName ? tabs.get(activeName) : null;
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
  const box = bodyEl.getBoundingClientRect();
  if (box.width < 2 || box.height < 2) return;
  try { t.fit.fit(); } catch { return; }
  try { cb.resize?.(t.name, t.term.cols, t.term.rows); } catch { /* Backend meldet sich beim nächsten Versuch */ }
}

function scheduleFit() {
  if (fitPending) return;
  fitPending = true;
  requestAnimationFrame(() => {
    fitPending = false;
    fitNow(activeTab());
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
    const s = cb.status?.(t.name);
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

function addTab(name) {
  const el = document.createElement('div');
  el.className = 'dk-tab';
  el.dataset.name = name;

  const dot = document.createElement('span');
  dot.className = 'dk-dot';

  const label = document.createElement('span');
  label.className = 'dk-name';
  label.textContent = name;

  const tool = document.createElement('span');
  tool.className = 'dk-tool';
  tool.setAttribute('aria-hidden', 'true');
  tool.innerHTML = developerIcon('bash');

  const x = document.createElement('button');
  x.className = 'dk-x';
  x.type = 'button';
  x.innerHTML = dockIcon('close');
  x.setAttribute('aria-label', `Tab ${name} schließen`);
  x.title = 'Tab schließen';

  el.append(dot, tool, label, x);
  tabsEl.appendChild(el);

  const pane = document.createElement('div');
  pane.className = 'dk-pane';
  const host = document.createElement('div');
  host.className = 'dk-term';
  pane.appendChild(host);
  bodyEl.appendChild(pane);

  const t = { name, el, dot, pane, host, term: null, fit: null, offData: null, offClosed: null, live: false, closed: false };
  tabs.set(name, t);
  syncDot(t);
  updateBlank();
  return t;
}

function ensureLive(t) {
  if (t.live) return;
  t.live = true;

  const term = new Terminal({
    fontSize: 13,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    scrollback: 10000,
    scrollSensitivity: 5,
    fastScrollSensitivity: 12,
    cursorBlink: true,
    macOptionIsMeta: true,
    theme: terminalTheme(),
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new WebLinksAddon((e, uri) => openURL(uri)));
  term.open(t.host);
  bindKeys(term, t);

  term.onData(d => cb.write?.(t.name, toB64(d)));
  term.onResize(({ cols, rows }) => cb.resize?.(t.name, cols, rows));

  t.term = term;
  t.fit = fit;

  try { t.offData = cb.onData?.(t.name, b64 => term.write(fromB64(b64))) || null; } catch { t.offData = null; }
  try {
    t.offClosed = cb.onClosed?.(t.name, () => {
      t.closed = true;
      term.write('\r\n\x1b[31m— Verbindung beendet —\x1b[0m\r\n');
      syncDot(t);
    }) || null;
  } catch { t.offClosed = null; }

  try { fit.fit(); } catch { /* Pane noch ohne Maße */ }
  Promise.resolve(cb.attach?.(t.name, term.cols, term.rows))
    .catch(err => term.write('\x1b[31m' + err + '\x1b[0m\r\n'));
}

function activate(name) {
  const t = tabs.get(name);
  if (!t) return;
  activeName = name;
  for (const [n, other] of tabs) {
    const on = n === name;
    other.el.classList.toggle('dk-active', on);
    other.pane.classList.toggle('dk-on', on);
  }
  t.el.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  if (open) {
    ensureLive(t);
    fitNow(t);
    t.term?.focus();
  }
  try { cb.onActive?.(name); } catch { /* optionaler Callback */ }
  persist();
}

function stepTab(dir) {
  const list = [...tabs.keys()];
  if (list.length < 2) return;
  const i = list.indexOf(activeName);
  const next = list[((i < 0 ? 0 : i) + dir + list.length) % list.length];
  activate(next);
}

async function spawnTerminal() {
  if (!cb.newTerminal) return;
  let name = null;
  try { name = await cb.newTerminal(); } catch { return; }
  if (name) openInDock(name);
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

  const bar = document.createElement('div');
  bar.className = 'dk-bar';

  tabsEl = document.createElement('div');
  tabsEl.className = 'dk-tabs';

  const emptyTitle = document.createElement('div');
  emptyTitle.className = 'dk-empty-title';
  emptyTitle.innerHTML = `${developerIcon('bash')}<span>Terminal-Dock</span>`;
  tabsEl.appendChild(emptyTitle);

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
  bar.append(tabsEl, actions);

  bodyEl = document.createElement('div');
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

  dockEl.append(grip, bar, bodyEl);
  document.body.appendChild(dockEl);
  updateBlank();

  tabsEl.addEventListener('click', e => {
    const tab = e.target.closest('.dk-tab');
    if (!tab) return;
    if (e.target.closest('.dk-x')) {
      closeDockTab(tab.dataset.name);
      return;
    }
    activate(tab.dataset.name);
  });
  tabsEl.addEventListener('mousedown', e => {
    if (e.button === 1) e.preventDefault();
  });
  tabsEl.addEventListener('auxclick', e => {
    if (e.button !== 1) return;
    const tab = e.target.closest('.dk-tab');
    if (!tab) return;
    e.preventDefault();
    closeDockTab(tab.dataset.name);
  });
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
    if (!open || tabs.size < 2) return;
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

  for (const name of saved?.tabs || []) {
    if (!tabs.has(name)) addTab(name);
  }
  const wanted = saved?.active && tabs.has(saved.active) ? saved.active : [...tabs.keys()][0] || null;
  if (wanted) activate(wanted);

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
  if (!activeName || !tabs.has(activeName)) activeName = [...tabs.keys()][0] || null;
  if (activeName) activate(activeName);
  else updateStatuses();
}

export function isDockOpen() {
  return open;
}

export function openInDock(name) {
  if (!mounted || !name) return;
  if (!tabs.has(name)) addTab(name);
  if (!open) {
    open = true;
    applyOpen();
    notifyLayout();
  }
  activate(name);
}

export function closeDockTab(name) {
  const t = tabs.get(name);
  if (!t) return;

  const order = [...tabs.keys()];
  const i = order.indexOf(name);
  tabs.delete(name);

  try { t.offData?.(); } catch { /* Listener war schon weg */ }
  try { t.offClosed?.(); } catch { /* Listener war schon weg */ }
  if (t.live) {
    try { cb.close?.(name); } catch { /* Backend hat die Sitzung evtl. selbst beendet */ }
  }
  try { t.term?.dispose(); } catch { /* bereits entsorgt */ }
  t.term = null;
  t.fit = null;
  t.offData = null;
  t.offClosed = null;
  t.el.remove();
  t.pane.remove();

  updateBlank();

  if (activeName === name) {
    activeName = null;
    const rest = order.filter(n => n !== name);
    const next = rest[i] || rest[i - 1] || null;
    if (next) activate(next);
  }
  persist();
}

export function dockTabs() {
  return [...tabs.keys()];
}

export function refitDock() {
  if (!mounted) return;
  applyHeight();
  scheduleFit();
}
