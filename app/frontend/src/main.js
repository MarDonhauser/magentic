import './style.css';
import './overview.css';
import '@xterm/xterm/css/xterm.css';

import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import {
  Overview, Projects,
  NewSession, NewTermSession, NewTermSessionFor, DoneAgent, Cleanup, Merge, Deploy, RemoveWorktree, SetMainBranch,
  OpenTerm, WriteTerm, ResizeTerm, CloseTerm, KillSession, LaterSession, ReopenSession, SendSkill, HandoffSession,
  DeployStatus, AzLogin, ArgoLogin, AzAccounts, AzSetSubscription,
  WorktreeDiff, SessionPreview, SearchTranscripts, SessionLinks, SetActiveTerm,
  PickFolder, AddProject, RemoveProject, ReorderProjects, SaveImage, Timeline,
  Zeitgeist, ZeitgeistStart, ZeitgeistPause, ZeitgeistResume, ZeitgeistStop,
  MarkSeen, GitGraph, Board, BoardArchive, Stats, StartBoardItem, NewDockSession, BuildInfo,
  Breaks, BreakHeartbeat, TakeBreak, EndBreak, SnoozeBreak, BreakConfig, SetBreakConfig, BreakOver,
} from '../wailsjs/go/main/App';
import { EventsOn, EventsOff, BrowserOpenURL, ClipboardSetText } from '../wailsjs/runtime/runtime';
import {
  developerIcon,
  mountDeveloperIcons,
  providerIcon,
  robotAvatar,
  sessionToolIcon,
  sessionToolKey,
  sessionToolLabel,
} from './avatar.js';
import { renderGitGraph } from './gitgraph.js';
import { renderBoard } from './board.js';
import { renderStats } from './stats.js';
import { mountDock, toggleDock, isDockOpen, openInDock, closeDockTab, dockTabs, refitDock } from './dock.js';
import { mountBreaks, updateBreaks, openBreak, openBreakSettings, isBreakOpen } from './breaks.js';
import { initThemeToggle, onThemeChange, terminalTheme } from './theme.js';
import { createHydraHandoff } from './hydra-handoff.js';
mountDeveloperIcons();

const STATUS = {
  running: { color: 'var(--good)', label: 'läuft' },
  agents:  { color: 'var(--info)', label: 'Agents' },
  shell:   { color: 'var(--accent)', label: 'Shell läuft' },
  blocked: { color: 'var(--warning)', label: 'wartet' },
  idle:    { color: 'var(--muted)', label: 'idle' },
  term:    { color: 'var(--info)', label: 'Terminal' },
  exited:  { color: 'var(--ink-2)', label: 'beendet' },
  dead:    { color: 'var(--critical)', label: 'tot' },
  unknown: { color: 'var(--muted)', label: '?' },
};

const PHASE = {
  deploy:    { color: 'var(--accent)',  ico: 'rocket', label: 'deployt' },
  merge:     { color: 'var(--info)',    ico: 'merge', label: 'merge' },
  cleanup:   { color: 'var(--info)',    ico: 'broom', label: 'cleanup' },
  committed: { color: 'var(--good)',    ico: 'check', label: '' },
  pipeline:  { color: 'var(--accent)',  ico: 'hourglass', label: 'Pipeline' },
};

function normName(s) {
  return String(s ?? '').toLowerCase().replace(/[^a-z0-9]/g, '');
}

function pipelineRunningFor(project) {
  const running = (deployStatus?.builds || []).filter(b => b.status === 'inProgress' || b.status === 'notStarted');
  if (!running.length) return false;
  const pn = normName(project);
  if (!pn) return false;
  return running.some(b => {
    const rn = normName(b.repo);
    return rn && (rn.includes(pn) || pn.includes(rn));
  });
}

function agentVisual(a, project) {
  const proj = project ?? a?.project;
  const st = STATUS[a?.status] || STATUS.unknown;
  const alive = ['running', 'agents', 'blocked', 'idle', 'term'].includes(a?.status);
  if (alive && (a?.phase === 'deploy' || a?.deployed) && pipelineRunningFor(proj)) {
    const p = PHASE.pipeline;
    return { color: p.color, ico: p.ico, label: p.label };
  }
  const ph = PHASE[a?.phase];
  if (ph && !['blocked', 'dead', 'exited'].includes(a?.status)) {
    const label = ph.label && a.phaseLabel ? `${ph.label} ${a.phaseLabel}` : (a.phaseLabel || ph.label);
    return { color: ph.color, ico: ph.ico, label };
  }
  if (a?.status === 'blocked' && a?.detail) {
    return { color: st.color, ico: 'lock', label: a.detail };
  }
  if (['idle', 'exited', 'term'].includes(a?.status) && a?.known) {
    if (a.ownDirty > 0) return { color: 'var(--warning)', label: `± ${a.ownDirty} uncommitted` };
    if (a.ownCommits > 0) return { color: 'var(--good)', ico: 'check', label: 'committed' };
  }
  return { color: st.color, label: a?.detail || st.label };
}

const $ = id => document.getElementById(id);
const sessionsEl = $('sessions'), usageBoxEl = $('usage-box'), zgBoxEl = $('zg-box');
const overviewEl = $('overview'), termsEl = $('terms'), deployBadgeEl = $('deploy-badge');
initThemeToggle($('theme-toggle'));

let view = 'overview';
let activeTerm = null;
let ov = null;
let projects = [];
let overviewSync = { kind: 'loading', error: '', lastOkAt: '' };
let confirmRemove = null;
let confirmRemoveProject = null;
let editingMain = null;
let sidebarSessions = [];
let hydraProject = null;
let pdrag = null;
let suppressHeadClick = false;

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

function errorText(err) {
  return String(err ?? 'Unbekannter Fehler')
    .replace(/^Error:\s*/i, '')
    .replace(/^Fehler:\s*/i, '')
    .trim() || 'Unbekannter Fehler';
}

const ICONS = {
  search: '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
  clock: '<circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>',
  rocket: '<path d="M4.5 16.5c-1.5 1.26-2 5-2 5s3.74-.5 5-2c.71-.84.7-2.13-.09-2.91a2.18 2.18 0 0 0-2.91-.09z"/><path d="m12 15-3-3a22 22 0 0 1 2-3.95A12.88 12.88 0 0 1 22 2c0 2.72-.78 7.5-6 11a22.35 22.35 0 0 1-4 2z"/><path d="M9 12H4s.55-3.03 2-4c1.62-1.08 5 0 5 0"/><path d="M12 15v5s3.03-.55 4-2c1.08-1.62 0-5 0-5"/>',
  merge: '<circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M6 21V9a9 9 0 0 0 9 9"/>',
  hourglass: '<path d="M5 22h14"/><path d="M5 2h14"/><path d="M17 22v-4.172a2 2 0 0 0-.586-1.414L12 12l-4.414 4.414A2 2 0 0 0 7 17.828V22"/><path d="M7 2v4.172a2 2 0 0 0 .586 1.414L12 12l4.414-4.414A2 2 0 0 0 17 6.172V2"/>',
  lock: '<rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
  link: '<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>',
  broom: '<path d="m16 22-1-4"/><path d="M19 13.99a1 1 0 0 0 1-1V12a2 2 0 0 0-2-2h-3a1 1 0 0 1-1-1V4a2 2 0 0 0-4 0v5a1 1 0 0 1-1 1H6a2 2 0 0 0-2 2v.99a1 1 0 0 0 1 1"/><path d="M5 14h14l1.973 6.767A1 1 0 0 1 20 22H4a1 1 0 0 1-.973-1.233z"/><path d="m8 22 1-4"/>',
  check: '<path d="M20 6 9 17l-5-5"/>',
  x: '<path d="M18 6 6 18"/><path d="m6 6 12 12"/>',
  play: '<polygon points="6 3 20 12 6 21 6 3"/>',
  pencil: '<path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"/><path d="m15 5 4 4"/>',
  trash: '<path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>',
  square: '<rect x="3" y="3" width="18" height="18" rx="2"/>',
  terminal: '<polyline points="4 17 10 11 4 5"/><line x1="12" x2="20" y1="19" y2="19"/>',
  gitbranch: '<line x1="6" x2="6" y1="3" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>',
  chart: '<path d="M3 3v16a2 2 0 0 0 2 2h16"/><path d="M7 16v-5"/><path d="M12 16V8"/><path d="M17 16v-3"/>',
  cloud: '<path d="M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z"/>',
  warn: '<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3Z"/><path d="M12 9v4"/><path d="M12 17h.01"/>',
  magnet: '<path d="M6 8v4a6 6 0 0 0 12 0V8"/><path d="M4 3h4v5H4z"/><path d="M16 3h4v5h-4z"/>',
};

function icon(name) {
  return `<svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICONS[name]}</svg>`;
}

function sessionToolMark(session) {
  const label = sessionToolLabel(session);
  const tool = sessionToolKey(session);
  const toolIcon = sessionToolIcon(session);
  if (!toolIcon) return '';
  return `<span class="dev-tool-mark" data-agent-tool="${tool}" role="img" aria-label="${label}" title="${label}">${toolIcon}</span>`;
}

function agentPortrait(name, size, session) {
  const label = sessionToolLabel(session);
  const tool = sessionToolKey(session);
  const toolIcon = sessionToolIcon(session);
  return `<span class="agent-portrait" style="--agent-avatar-size:${Number(size) || 18}px">` +
    `${robotAvatar(name, size)}${toolIcon ? `<span class="agent-provider-badge" data-agent-tool="${tool}" role="img" aria-label="${label}" title="${label}">${toolIcon}</span>` : ''}</span>`;
}

function visHtml(v) {
  return (v.ico ? icon(v.ico) + ' ' : '') + esc(v.label);
}

function shortSub(s) {
  s = String(s ?? '');
  return s.length > 30 ? s.slice(0, 29) + '…' : s;
}

let toastTimer;
function toast(msg, isErr) {
  const t = $('toast');
  t.textContent = msg;
  t.className = (isErr ? 'err ' : '') + 'show';
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.className = ''; }, 5000);
}

async function act(promise, okMsg) {
  try {
    const res = await promise;
    toast(typeof okMsg === 'function' ? okMsg(res) : okMsg);
    await refresh(true);
    return res;
  } catch (err) {
    toast('Aktion fehlgeschlagen: ' + errorText(err), true);
    throw err;
  }
}

const enc = new TextEncoder();
function toB64(str) {
  const bytes = enc.encode(str);
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}
function fromB64(b64) {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

async function blobToB64(blob) {
  const bytes = new Uint8Array(await blob.arrayBuffer());
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}

const terms = new Map();

onThemeChange(theme => {
  const nextTheme = terminalTheme(theme);
  for (const entry of terms.values()) entry.term.options.theme = nextTheme;
});

function makeTerm(name) {
  const wrap = document.createElement('div');
  wrap.className = 'term-wrap';
  const inner = document.createElement('div');
  inner.className = 'term-inner';
  wrap.appendChild(inner);
  termsEl.appendChild(wrap);
  const term = new Terminal({
    fontSize: 13,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    scrollback: 20000,
    scrollSensitivity: 5,
    fastScrollSensitivity: 12,
    cursorBlink: true,
    macOptionIsMeta: true,
    theme: terminalTheme(),
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new WebLinksAddon((e, uri) => BrowserOpenURL(uri)));
  term.open(inner);
  term.onData(d => WriteTerm(name, toB64(d)));
  term.onResize(({ cols, rows }) => ResizeTerm(name, cols, rows));

  let lastSel = '';
  let lastSelAt = 0;
  term.onSelectionChange(() => {
    const s = term.getSelection();
    if (s) { lastSel = s; lastSelAt = Date.now(); }
  });
  term.attachCustomKeyEventHandler(e => {
    if (e.type === 'keydown' && e.key === 'Enter' && e.shiftKey && !e.metaKey && !e.ctrlKey && !e.altKey) {
      WriteTerm(name, toB64('\x1b\r'));
      e.preventDefault();
      return false;
    }
    if (e.type === 'keydown' && e.metaKey && !e.ctrlKey && !e.altKey && e.key.toLowerCase() === 'c') {
      const s = term.getSelection() || (Date.now() - lastSelAt < 30000 ? lastSel : '');
      if (s) {
        ClipboardSetText(s);
        toast('kopiert ✓');
        e.preventDefault();
        return false;
      }
    }
    return true;
  });

  wrap.addEventListener('paste', async e => {
    const items = e.clipboardData?.items || [];
    for (const it of items) {
      if (it.kind === 'file' && it.type.startsWith('image/')) {
        e.preventDefault();
        e.stopPropagation();
        const blob = it.getAsFile();
        if (!blob) return;
        try {
          const path = await SaveImage(await blobToB64(blob));
          WriteTerm(name, toB64(path + ' '));
        } catch (err) {
          toast('Bild konnte nicht eingefügt werden: ' + err, true);
        }
        return;
      }
    }
  }, true);
  EventsOn('term:data:' + name, b64 => term.write(fromB64(b64)));
  EventsOn('term:closed:' + name, () => term.write('\r\n\x1b[31m— Verbindung beendet —\x1b[0m\r\n'));

  const sb = document.createElement('button');
  sb.className = 'scroll-bottom';
  sb.textContent = '↓';
  sb.title = 'Ans Live-Ende springen';
  sb.onclick = () => { term.scrollToBottom(); term.focus(); };
  wrap.appendChild(sb);
  const updateSb = () => {
    const b = term.buffer.active;
    sb.classList.toggle('show', b.viewportY < b.baseY);
  };
  term.onScroll(updateSb);
  term.onWriteParsed(updateSb);

  const t = { term, fit, wrap, name };
  terms.set(name, t);
  return t;
}

const termBarEl = $('term-bar');
const termStateEl = $('term-session-state');
const termStateIconEl = $('term-state-icon');
const termStateTitleEl = $('term-state-title');
const termStateDetailEl = $('term-state-detail');
const termComposerEl = $('term-composer');
const termPromptEl = $('term-prompt');
const termSendEl = $('term-send');
const termAttachEl = $('term-attach');
const termImageEl = $('term-image');
const termComposeHintEl = $('term-compose-hint');
let composerBusy = false;
let composerHintTimer = null;

function agentInfo(name) {
  for (const p of ov?.projects || []) {
    for (const wt of p.worktrees || []) {
      for (const a of wt.agents || []) {
        if (a.name === name) return { ...a, project: p.name };
      }
    }
  }
  return null;
}

function updateTermBar() {
  if (view !== 'term' || !activeTerm) return;
  const a = agentInfo(activeTerm);
  const v = agentVisual(a, a?.project);
  const gone = !a || ['exited', 'dead'].includes(a.status);
  const claudeActions = a?.term ? '' :
    `<button class="btn tiny" id="tb-done"${gone ? ' disabled' : ''} title="/done in diese Session senden — committen und auf dev bringen">${icon('check')} done</button>` +
    `<button class="btn tiny" id="tb-deploy"${gone ? ' disabled' : ''} title="/deploy in diese Session senden">${icon('rocket')} deploy</button>` +
    `<button class="btn tiny" id="tb-dd"${gone ? ' disabled' : ''} title="/done senden und danach automatisch /deploy">${icon('check')}+${icon('rocket')} beides</button>`;
  termBarEl.innerHTML =
    `<button class="btn tiny" id="tb-back" title="Übersicht (⌘0)">‹ Übersicht</button>` +
    `<span class="tb-avatar">${agentPortrait(activeTerm, 24, a)}</span>` +
    `<span class="dot" style="background:${v.color}"></span>` +
    `<span class="tb-name">${esc(activeTerm)}</span>` +
    (a?.branch ? `<span class="tb-branch${a.worktree ? ' wt' : ''}" title="Branch ${esc(a.branch)}${a.worktree ? ' · eigener Worktree' : ''}">${icon('gitbranch')}${esc(a.branch)}</span>` : '') +
    `<span class="tb-st">${visHtml(v)}</span>` +
    (a?.project && a.project !== '(ohne Projekt)' ? `<span class="tb-proj">${esc(a.project)}</span>` : '') +
    `<span class="tb-actions">` + claudeActions +
    `<button class="btn tiny" id="tb-links" title="Links aus dieser Session anzeigen — Klick öffnet im Browser, ⌥-Klick kopiert">${icon('link')} links</button>` +
    `<button class="btn tiny" id="tb-later" title="Für später schließen (⌘⇧W) — bleibt in der Seitenleiste und lässt sich wieder öffnen">${icon('clock')}</button>` +
    `<button class="btn tiny danger" id="tb-kill" title="Session endgültig beenden">${icon('x')}</button></span>`;
  $('tb-back').onclick = showOverview;
  if (!a?.term) {
    $('tb-done').onclick = () =>
      act(DoneAgent(activeTerm), `/done an „${activeTerm}" gesendet — Plan in der Session bestätigen`).catch(() => {});
    $('tb-deploy').onclick = () =>
      act(SendSkill(activeTerm, '/deploy '), `/deploy an „${activeTerm}" gesendet — Plan in der Session bestätigen`)
        .then(startDeployWatch).catch(() => {});
    $('tb-dd').onclick = () =>
      act(SendSkill(activeTerm, '/done und sobald done komplett abgeschlossen ist, führe direkt /deploy aus '),
        `/done + /deploy an „${activeTerm}" gesendet — Plan in der Session bestätigen`)
        .then(startDeployWatch).catch(() => {});
  }
  $('tb-links').onclick = e => openLinksMenu(e.currentTarget);
  $('tb-later').onclick = () => parkSession(activeTerm);
  $('tb-kill').onclick = e => {
    const b = e.currentTarget;
    if (b.dataset.confirm) { killSession(activeTerm); return; }
    b.dataset.confirm = '1';
    b.innerHTML = icon('x') + ' wirklich?';
    setTimeout(() => {
      if (b.isConnected) { delete b.dataset.confirm; b.innerHTML = icon('x'); }
    }, 3000);
  };
  updateTermComposer(a, v, gone);
}

function updateComposerControls(gone) {
  const unavailable = composerBusy || gone || !activeTerm;
  termPromptEl.disabled = unavailable;
  termAttachEl.disabled = unavailable;
  termSendEl.disabled = unavailable || !termPromptEl.value.trim();
}

function setComposerHint(message, reset = true) {
  clearTimeout(composerHintTimer);
  termComposeHintEl.textContent = message;
  if (!reset) return;
  composerHintTimer = setTimeout(() => {
    termComposeHintEl.textContent = '⌘/Strg + ↵ senden · Bilder einfügen';
  }, 2600);
}

function updateTermComposer(a, visual, gone) {
  const activeStatus = a?.status;
  const hideState = !gone
    && (a?.term || activeStatus === 'term')
    && !['blocked', 'running', 'agents'].includes(activeStatus);
  termStateEl.className = hideState ? 'is-hidden' : '';
  termsEl.classList.toggle('without-session-state', hideState);
  let stateIcon = 'check';
  let title = 'Bereit für deine nächste Nachricht';
  let detail = 'Nutze den Composer oder arbeite direkt im Terminal weiter.';

  if (gone) {
    termStateEl.className = 'is-ended';
    stateIcon = 'x';
    title = 'Session beendet';
    detail = 'Der Verlauf bleibt lesbar. Öffne die Session über die Seitenleiste erneut, um weiterzuarbeiten.';
  } else if (a?.status === 'blocked') {
    termStateEl.className = 'is-waiting';
    stateIcon = 'warn';
    title = 'Deine Eingabe wird benötigt';
    detail = visual?.label && visual.label !== 'wartet' ? visual.label : 'Die Session wartet auf deine Entscheidung oder Bestätigung.';
  } else if (a?.status === 'running' || a?.status === 'agents') {
    termStateEl.className = 'is-running';
    stateIcon = 'clock';
    title = 'Session arbeitet';
    detail = 'Neue Nachrichten werden an dieselbe laufende Session gesendet.';
  }

  termStateIconEl.innerHTML = icon(stateIcon);
  termStateTitleEl.textContent = title;
  termStateDetailEl.textContent = detail;
  termPromptEl.placeholder = activeTerm ? `Nachricht an ${activeTerm} …` : 'Nachricht an die Session …';
  updateComposerControls(gone);
}

async function sendComposerMessage() {
  const message = termPromptEl.value.trim();
  const sessionName = activeTerm;
  const t = sessionName && terms.get(sessionName);
  if (!message || !t || composerBusy) return;

  composerBusy = true;
  updateComposerControls(false);
  termComposerEl.setAttribute('aria-busy', 'true');
  setComposerHint('Wird gesendet …', false);
  try {
    const normalized = message.replace(/\r?\n/g, '\r');
    const pasted = t.term.modes.bracketedPasteMode
      ? `\x1b[200~${normalized}\x1b[201~`
      : normalized;
    await WriteTerm(sessionName, toB64(pasted + '\r'));
    termPromptEl.value = '';
    t.term.scrollToBottom();
    setComposerHint(`An ${sessionName} gesendet`);
  } catch (err) {
    setComposerHint('Senden fehlgeschlagen', false);
    toast('Nachricht konnte nicht gesendet werden: ' + err, true);
  } finally {
    composerBusy = false;
    termComposerEl.removeAttribute('aria-busy');
    const a = agentInfo(activeTerm);
    updateComposerControls(!a || ['exited', 'dead'].includes(a.status));
    termPromptEl.focus();
  }
}

async function insertComposerImage(file) {
  if (!file || composerBusy || !activeTerm) return;
  termAttachEl.disabled = true;
  setComposerHint('Bild wird angehängt …', false);
  try {
    const path = await SaveImage(await blobToB64(file));
    const start = termPromptEl.selectionStart ?? termPromptEl.value.length;
    const end = termPromptEl.selectionEnd ?? start;
    const before = termPromptEl.value.slice(0, start);
    const after = termPromptEl.value.slice(end);
    const prefix = before && !/\s$/.test(before) ? ' ' : '';
    const suffix = after && !/^\s/.test(after) ? ' ' : '';
    termPromptEl.value = before + prefix + path + suffix + after;
    const caret = (before + prefix + path + suffix).length;
    termPromptEl.setSelectionRange(caret, caret);
    setComposerHint('Bild angehängt');
    termPromptEl.dispatchEvent(new Event('input'));
    termPromptEl.focus();
  } catch (err) {
    setComposerHint('Bild konnte nicht angehängt werden', false);
    toast('Bild konnte nicht eingefügt werden: ' + err, true);
  } finally {
    termAttachEl.disabled = false;
  }
}

termComposerEl.addEventListener('submit', e => {
  e.preventDefault();
  sendComposerMessage();
});
termPromptEl.addEventListener('input', () => {
  const a = agentInfo(activeTerm);
  updateComposerControls(!a || ['exited', 'dead'].includes(a.status));
});
termPromptEl.addEventListener('keydown', e => {
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
    e.preventDefault();
    termComposerEl.requestSubmit();
  }
});
termPromptEl.addEventListener('paste', e => {
  const imageItem = [...(e.clipboardData?.items || [])].find(item => item.kind === 'file' && item.type.startsWith('image/'));
  if (!imageItem) return;
  e.preventDefault();
  insertComposerImage(imageItem.getAsFile());
});
termAttachEl.onclick = () => termImageEl.click();
termImageEl.onchange = () => {
  insertComposerImage(termImageEl.files?.[0]);
  termImageEl.value = '';
};

function markSeen(name) {
  if (name) MarkSeen(name).catch(() => {});
}

async function openSession(name) {
  hydraHandoff.leave();
  view = 'term';
  hydraProject = null;
  termsEl.classList.remove('hydra');
  if (dockTabs().includes(name)) closeDockTab(name);
  if (activeTerm && activeTerm !== name) markSeen(activeTerm);
  markSeen(name);
  activeTerm = name;
  SetActiveTerm(name);
  showPanel('terms');
  let t = terms.get(name);
  const fresh = !t;
  if (!t) t = makeTerm(name);
  t.term.options.fontSize = 14;
  t.term.options.lineHeight = 1.3;
  if (t.wrap.parentElement !== termsEl) termsEl.appendChild(t.wrap);
  for (const [n, o] of terms) o.wrap.classList.toggle('active', n === name);
  t.fit.fit();
  if (fresh) {
    try { await OpenTerm(name, t.term.cols, t.term.rows); }
    catch (err) { t.term.write('\x1b[31m' + err + '\x1b[0m\r\n'); }
  } else {
    ResizeTerm(name, t.term.cols, t.term.rows);
  }
  t.term.focus();
  renderSidebar();
  updateTermBar();
}

const PANELS = ['overview', 'search-view', 'terms', 'graph-view', 'board-view', 'stats-view'];
const NAV_FOR = { overview: 'nav-overview', 'search-view': 'nav-search', 'graph-view': 'nav-graph', 'board-view': 'nav-board', 'stats-view': 'nav-stats' };

function showPanel(id) {
  for (const p of PANELS) {
    const el = $(p);
    if (el) el.style.display = p === id ? 'block' : 'none';
  }
  for (const [panel, nav] of Object.entries(NAV_FOR)) {
    const navEl = $(nav);
    if (!navEl) continue;
    const current = panel === id;
    navEl.classList.toggle('on', current);
    if (current) navEl.setAttribute('aria-current', 'page');
    else navEl.removeAttribute('aria-current');
  }
}

function leaveTerm() {
  hydraHandoff.leave();
  markSeen(activeTerm);
  activeTerm = null;
  SetActiveTerm('');
  hydraProject = null;
  termsEl.classList.remove('hydra');
}

function showOverview() {
  view = 'overview';
  leaveTerm();
  showPanel('overview');
  renderAll();
}

function showSearch() {
  view = 'search';
  leaveTerm();
  showPanel('search-view');
  $('search-input').focus();
  renderSidebar();
}

let graphProject = null, boardProject = null, boardArchive = false, statsProject = '', statsRange = 30;
let graphBusy = false, boardBusy = false, statsBusy = false;

function projectNames() {
  return (ov?.projects || []).filter(p => p.path).map(p => p.name);
}

function pickProject(current) {
  const names = projectNames();
  return current && names.includes(current) ? current : (names[0] || '');
}

function projectTabs(active, act) {
  const names = projectNames();
  if (!names.length) return '';
  return `<div class="proj-tabs">` + names.map(n =>
    `<button class="ptab${n === active ? ' on' : ''}" data-act="${act}" data-project="${esc(n)}">${esc(n)}</button>`
  ).join('') + `</div>`;
}

function sessionAvatar(name) {
  return agentPortrait(name, 18, agentInfo(name));
}

async function showGraph(project) {
  view = 'graph';
  leaveTerm();
  graphProject = pickProject(project ?? graphProject);
  showPanel('graph-view');
  renderSidebar();
  await loadGraph();
}

async function loadGraph() {
  const el = $('graph-view');
  const head = `<div class="view-head"><h2>${developerIcon('git')} Git-Graph</h2>` +
    projectTabs(graphProject, 'graphproj') +
    `<button class="btn tiny" data-act="graphreload" title="Graph neu laden">↻</button></div>`;
  if (!graphProject) { el.innerHTML = head + `<div class="none" style="padding:24px">Kein Projekt registriert.</div>`; return; }
  if (graphBusy) return;
  graphBusy = true;
  el.innerHTML = head + `<div class="none" style="padding:24px">lade Graph…</div>`;
  let g = null;
  try { g = await GitGraph(graphProject, 120); }
  catch (err) { g = { err: String(err), commits: [], branches: [] }; }
  graphBusy = false;
  if (view !== 'graph') return;
  el.innerHTML = head + `<div id="graph-body"></div>`;
  renderGitGraph($('graph-body'), g, {
    avatar: sessionAvatar,
    onCommit: hash => { ClipboardSetText(hash); toast('Commit-Hash kopiert'); },
    onBranch: name => toast(`Branch ${name}`),
  });
}

async function showBoard(project) {
  view = 'board';
  leaveTerm();
  boardProject = pickProject(project ?? boardProject);
  showPanel('board-view');
  renderSidebar();
  await loadBoard();
}

async function loadBoard() {
  const el = $('board-view');
  const head = `<div class="view-head"><h2>${developerIcon('markdown')} Board</h2>` +
    projectTabs(boardProject, 'boardproj') +
    `<button class="btn tiny${boardArchive ? ' on' : ''}" data-act="boardarchive" aria-pressed="${boardArchive}" title="Archivierte Specs ${boardArchive ? 'ausblenden' : 'einblenden'}">Archiv</button>` +
    `<button class="btn tiny" data-act="boardreload" title="Specs neu einlesen">↻</button></div>`;
  if (!boardProject) { el.innerHTML = head + `<div class="none" style="padding:24px">Kein Projekt registriert.</div>`; return; }
  if (boardBusy) return;
  boardBusy = true;
  el.innerHTML = head + `<div class="none" style="padding:24px">${boardArchive ? 'lese aktuelle und archivierte Specs' : 'lese aktuelle Specs'}…</div>`;
  let b = null;
  try { b = boardArchive ? await BoardArchive(boardProject, 25) : await Board(boardProject); }
  catch (err) { b = { err: String(err), items: [], kind: 'none' }; }
  boardBusy = false;
  if (view !== 'board') return;
  el.innerHTML = head + `<div id="board-body"></div>`;
  renderBoard($('board-body'), b, {
    includeArchived: boardArchive,
    avatar: sessionAvatar,
    onOpenSession: name => openSession(name),
    onStart: async item => {
      try {
        const name = await act(StartBoardItem(boardProject, item.startToken),
          n => `Session „${n}" für „${item.title}" gestartet`);
        if (name) setTimeout(() => openSession(name), 400);
      } catch { /* toast zeigt den Fehler */ }
    },
  });
}

async function showStats(project = '') {
  view = 'stats';
  leaveTerm();
  statsProject = project ? pickProject(project) : '';
  showPanel('stats-view');
  renderSidebar();
  await loadStats();
}

async function loadStats() {
  const el = $('stats-view');
  if (statsBusy) return;
  statsBusy = true;
  if (!el.dataset.filled) el.innerHTML = `<div class="none" style="padding:24px">rechne Statistiken…</div>`;
  let s = null;
  try { s = await Stats(statsRange); }
  catch (err) { s = { err: String(err), days: [], projects: [], models: [], heatmap: [], hours: [], totals: {} }; }
  statsBusy = false;
  if (view !== 'stats') return;
  el.dataset.filled = '1';
  const render = () => renderStats(el, s, {
    project: statsProject,
    onRange: days => { statsRange = days; loadStats(); },
    onProject: project => {
      statsProject = project ? pickProject(project) : '';
      render();
    },
  });
  render();
}

BuildInfo()
  .then(at => { if (at) $('sidebar-head').title = `magentic · Build vom ${at}`; })
  .catch(() => {});

$('nav-overview').onclick = showOverview;
$('sidebar-head').onclick = showOverview;
$('nav-search').onclick = showSearch;
$('nav-graph').onclick = () => showGraph();
$('nav-board').onclick = () => showBoard();
$('nav-stats').onclick = () => showStats();

const sidebarToolsEl = $('sidebar-tools');
sidebarToolsEl.addEventListener('click', e => {
  if (e.target.closest('.nav-btn')) sidebarToolsEl.open = false;
});
document.addEventListener('mousedown', e => {
  if (sidebarToolsEl.open && !sidebarToolsEl.contains(e.target)) sidebarToolsEl.open = false;
  document.querySelectorAll('.project-more[open]').forEach(menu => {
    if (!menu.contains(e.target)) menu.open = false;
  });
});

for (const id of ['graph-view', 'board-view']) {
  $(id).addEventListener('click', e => {
    const b = e.target.closest('button[data-act]');
    if (!b) return;
    if (b.dataset.act === 'graphproj') showGraph(b.dataset.project);
    if (b.dataset.act === 'boardproj') showBoard(b.dataset.project);
    if (b.dataset.act === 'graphreload') loadGraph();
    if (b.dataset.act === 'boardreload') loadBoard();
    if (b.dataset.act === 'boardarchive') {
      boardArchive = !boardArchive;
      loadBoard();
    }
  });
}

const hydraGridEl = $('hydra-grid');
const hydraHandoff = createHydraHandoff({
  root: hydraGridEl,
  statusElement: () => $('hydra-handoff-status'),
  submit: (sourceId, targetId) => HandoffSession(sourceId, targetId),
  notify: toast,
  renderIcon: () => icon('magnet'),
  formatError: errorText,
});

function hydraAgents() {
  const p = (ov?.projects || []).find(x => x.name === hydraProject);
  if (!p) return [];
  const out = [];
  for (const wt of p.worktrees || []) {
    for (const a of wt.agents || []) {
      if (a.status !== 'dead' && !a.dock) out.push(a);
    }
  }
  return out.slice(0, 6);
}

function enterHydra(project) {
  hydraHandoff.leave();
  view = 'hydra';
  markSeen(activeTerm);
  activeTerm = null;
  SetActiveTerm('');
  hydraProject = project;
  showPanel('terms');
  termsEl.classList.add('hydra');
  updateHydraBar();
  syncHydra();
  renderSidebar();
}

function updateHydraBar() {
  if (view !== 'hydra') return;
  const agents = hydraAgents();
  termBarEl.innerHTML =
    `<button class="btn tiny" id="tb-back" title="Übersicht (⌘0)">‹ Übersicht</button>` +
    `<span class="dot" style="background:var(--accent)"></span>` +
    `<span class="tb-name">${developerIcon('claude')} Hydra · ${esc(hydraProject)}</span>` +
    `<span class="tb-st" id="hydra-handoff-status" role="status" aria-live="polite" aria-atomic="true"></span>` +
    `<span class="tb-actions">` +
    `<button class="btn tiny" id="tb-add" title="Neue Session in ${esc(hydraProject)} — erscheint direkt im Raster">${developerIcon('claude')} Session</button>` +
    `<button class="btn tiny" id="tb-term" title="Reines Terminal in ${esc(hydraProject)} — Shell statt Claude">${developerIcon('bash')} Terminal</button></span>`;
  $('tb-back').onclick = showOverview;
  $('tb-add').onclick = async () => {
    try {
      const n2 = await act(NewSession(hydraProject, false, ''), x => `Session „${x}" gestartet`);
      if (n2) await focusHydraSession(n2);
    } catch { /* toast zeigt den Fehler */ }
  };
  $('tb-term').onclick = async () => {
    try {
      const n2 = await act(NewTermSession(hydraProject, false, ''), x => `Terminal „${x}" geöffnet`);
      if (n2) await focusHydraSession(n2);
    } catch { /* toast zeigt den Fehler */ }
  };
  hydraHandoff.reconcile(agents);
}

async function focusHydraSession(name) {
  await syncHydra();
  const t = terms.get(name);
  if (t?.wrap.parentElement === hydraGridEl) t.term.focus();
  else toast(`Raster zeigt max. 6 Sessions — „${name}" läuft, ist aber nicht im Hydra-Raster`, true);
}

function ensureHydraHead(t) {
  if (t.head) return;
  const head = document.createElement('div');
  head.className = 'hydra-head';
  head.innerHTML =
    `<span class="hh-avatar"></span>` +
    `<span class="dot"></span><span class="hh-name">${esc(t.name)}</span>` +
    `<span class="hh-status"></span>` +
    `<button type="button" class="hh-magnet" aria-label="Kontext aus Session ${esc(t.name)} weitergeben" aria-pressed="false">${icon('magnet')}</button>` +
    `<button type="button" class="hh-max" aria-label="Session ${esc(t.name)} groß öffnen" title="Session groß öffnen">⤢</button>` +
    `<button type="button" class="hh-kill" aria-label="Session ${esc(t.name)} beenden" title="Session beenden">✕</button>`;
  head.querySelector('.hh-max').onclick = () => openSession(t.name);
  head.querySelector('.hh-kill').onclick = e => {
    const b = e.currentTarget;
    if (b.dataset.confirm) { killSession(t.name); return; }
    b.dataset.confirm = '1';
    b.textContent = '✕ wirklich?';
    setTimeout(() => {
      if (b.isConnected) { delete b.dataset.confirm; b.textContent = '✕'; }
    }, 3000);
  };
  head.onclick = e => { if (!e.target.closest('.hh-magnet, .hh-max, .hh-kill')) t.term.focus(); };
  t.wrap.appendChild(head);
  t.head = head;
  t.wrap.addEventListener('focusin', () => {
    if (view !== 'hydra') return;
    activeTerm = t.name;
    SetActiveTerm(t.name);
    for (const w of hydraGridEl.querySelectorAll('.term-wrap')) {
      w.classList.toggle('focused', w === t.wrap);
    }
  });
  t.wrap.addEventListener('mouseenter', e => {
    if (view !== 'hydra' || e.buttons) return;
    if (activeTerm !== t.name) t.term.focus();
  });
}

function updateHydraHead(t, agent) {
  const portraitKey = `${agent?.id || ''}:${sessionToolKey(agent)}`;
  if (t.hydraPortraitKey !== portraitKey) {
    t.head.querySelector('.hh-avatar').innerHTML = agentPortrait(t.name, 18, agent);
    t.hydraPortraitKey = portraitKey;
  }
}

async function syncHydra() {
  if (view !== 'hydra') return;
  const agents = hydraAgents();
  const names = new Set(agents.map(a => a.name));
  for (const [n, t] of terms) {
    if (t.wrap.parentElement === hydraGridEl && !names.has(n)) {
      termsEl.appendChild(t.wrap);
      t.wrap.classList.remove('focused');
    }
  }
  hydraGridEl.querySelector('.none')?.remove();
  if (!agents.length) {
    hydraHandoff.reconcile([]);
    hydraGridEl.innerHTML = `<div class="none">Keine aktiven Sessions in ${esc(hydraProject)} — oben mit „+ Session" eine starten</div>`;
    updateHydraBar();
    return;
  }
  const fresh = [];
  for (const a of agents) {
    let t = terms.get(a.name);
    if (!t) { t = makeTerm(a.name); fresh.push(a.name); }
    t.term.options.fontSize = 13;
    t.term.options.lineHeight = 1;
    ensureHydraHead(t);
    if (t.wrap.parentElement !== hydraGridEl) hydraGridEl.appendChild(t.wrap);
    t.wrap.dataset.termName = t.name;
    t.wrap.dataset.sessionId = String(a.id || '');
    updateHydraHead(t, a);
    const v = agentVisual(a, hydraProject);
    t.head.querySelector('.dot').style.background = v.color;
    t.head.querySelector('.hh-status').innerHTML = `${visHtml(v)} · ${esc(a.age)}`;
  }
  hydraGridEl.classList.toggle('single', agents.length === 1);
  hydraGridEl.classList.toggle('odd', agents.length % 2 === 1 && agents.length > 1);
  hydraHandoff.reconcile(agents);
  for (const a of agents) {
    const t = terms.get(a.name);
    if (!t) continue;
    t.fit.fit();
    if (fresh.includes(a.name)) {
      try { await OpenTerm(a.name, t.term.cols, t.term.rows); }
      catch (err) { t.term.write('\x1b[31m' + err + '\x1b[0m\r\n'); }
    } else {
      ResizeTerm(a.name, t.term.cols, t.term.rows);
    }
  }
}


function makeProjDraggable(head, name) {
  head.classList.add('draggable');
  head.dataset.proj = name;
  head.addEventListener('pointerdown', e => {
    if (e.button !== 0 || e.target.closest('.proj-add')) return;
    pdrag = { name, head, startY: e.clientY, active: false };
    window.addEventListener('pointermove', onProjPointerMove);
    window.addEventListener('pointerup', onProjPointerUp, { once: true });
  });
}

function onProjPointerMove(e) {
  if (!pdrag) return;
  if (!pdrag.active) {
    if (Math.abs(e.clientY - pdrag.startY) < 4) return;
    pdrag.active = true;
    pdrag.head.classList.add('dragging');
    document.body.classList.add('reordering');
  }
  markDropAt(dropIndexAt(e.clientY));
  autoScrollSidebar(e.clientY);
}

function onProjPointerUp(e) {
  window.removeEventListener('pointermove', onProjPointerMove);
  const d = pdrag;
  pdrag = null;
  document.body.classList.remove('reordering');
  if (!d) return;
  d.head.classList.remove('dragging');
  clearDropMarks();
  if (d.active) {
    suppressHeadClick = true;
    setTimeout(() => { suppressHeadClick = false; }, 0);
    reorderProjects(d.name, dropIndexAt(e.clientY));
  }
}

function autoScrollSidebar(clientY) {
  const r = sessionsEl.getBoundingClientRect();
  if (clientY < r.top + 24) sessionsEl.scrollTop -= 6;
  else if (clientY > r.bottom - 24) sessionsEl.scrollTop += 6;
}

function projHeads() {
  return [...sessionsEl.querySelectorAll('.proj-head.draggable')];
}

function dropIndexAt(clientY) {
  const heads = projHeads();
  for (let i = 0; i < heads.length; i++) {
    const r = heads[i].getBoundingClientRect();
    if (clientY < r.top + r.height / 2) return i;
  }
  return heads.length;
}

function markDropAt(idx) {
  clearDropMarks();
  const heads = projHeads();
  if (!heads.length) return;
  if (idx < heads.length) heads[idx].classList.add('drop-before');
  else heads[heads.length - 1].classList.add('drop-after');
}

function clearDropMarks() {
  for (const el of sessionsEl.querySelectorAll('.proj-head.drop-before, .proj-head.drop-after')) {
    el.classList.remove('drop-before', 'drop-after');
  }
}

async function reorderProjects(dragged, idx) {
  if (!dragged) return;
  const order = (ov?.projects || []).filter(p => p.path).map(p => p.name);
  const from = order.indexOf(dragged);
  if (from < 0) return;
  order.splice(from, 1);
  if (from < idx) idx -= 1;
  order.splice(idx, 0, dragged);
  try {
    await ReorderProjects(order);
    await refresh(true);
  } catch (err) {
    toast('Fehler: ' + err, true);
  }
}

function branchChip(a) {
  if (!a.branch) return '';
  const wt = a.worktree ? ' wt' : '';
  return `<span class="sbranch${wt}" title="Branch ${esc(a.branch)}${a.worktree ? ' · eigener Worktree' : ''}">` +
    icon('gitbranch') + esc(a.branch) + '</span>';
}

function liveSessions() {
  const sessions = [];
  for (const p of ov?.projects || []) {
    for (const wt of p.worktrees || []) {
      for (const a of wt.agents || []) {
        if (a.dock || ['dead', 'exited'].includes(a.status)) continue;
        sessions.push({ ...a, project: p.name, branch: a.branch || wt.branch || '' });
      }
    }
  }
  return sessions;
}

function attentionState() {
  const sessions = liveSessions();
  return {
    waiting: sessions.filter(a => a.status === 'blocked'),
    active: sessions.filter(a => ['running', 'agents', 'shell', 'term'].includes(a.status)),
    unread: sessions.filter(a => a.unread && a.status !== 'blocked'),
    unknown: sessions.filter(a => !a.status || a.status === 'unknown'),
  };
}

function attentionBar() {
  const { waiting } = attentionState();
  const bar = $('attention');
  if (!waiting.length) {
    bar.className = '';
    bar.innerHTML = '';
    bar.onclick = null;
    bar.removeAttribute('title');
    return;
  }
  const label = waiting.length === 1
    ? `${esc(waiting[0].name)} wartet auf dich`
    : `${waiting.length} Sessions warten auf dich`;
  bar.className = 'wait';
  bar.innerHTML = `<span class="at-wait">${icon('lock')} ${label}</span>`;
  bar.title = 'Nächste wartende Session öffnen';
  bar.onclick = () => openSession(waiting[0].name);
}

function renderSidebar() {
  sessionsEl.innerHTML = '';
  sidebarSessions = [];
  let any = false;
  for (const p of ov?.projects || []) {
    const agents = [];
    for (const wt of p.worktrees || []) {
      for (const a of wt.agents || []) {
        if (a.status !== 'dead' && !a.dock) agents.push(a);
      }
    }
    if (!agents.length && !p.path) continue;
    any = true;
    const head = document.createElement('div');
    head.className = 'proj-head';
    const label = document.createElement('span');
    label.textContent = p.name;
    if (p.path) {
      label.className = 'pname';
      label.title = 'Hydra-Modus: alle Sessions von ' + p.name + ' nebeneinander';
      label.onclick = () => { if (!suppressHeadClick) enterHydra(p.name); };
    }
    head.appendChild(label);
    if (p.path) {
      makeProjDraggable(head, p.name);
    }
    if (p.path) {
      const plus = document.createElement('button');
      plus.className = 'proj-add';
      plus.textContent = '+';
      plus.title = 'Neue Claude-Session in ' + p.name + ' (⌥-Klick: in frischem Worktree · ⇧-Klick: reines Terminal)';
      plus.onclick = async e => {
        e.stopPropagation();
        plus.disabled = true;
        try {
          const worktree = e.altKey;
          const name = e.shiftKey
            ? await act(NewTermSession(p.name, false, ''), n => `Terminal „${n}" geöffnet`)
            : await act(NewSession(p.name, worktree, ''),
                n => (worktree ? `Worktree-Session „${n}" gestartet` : `Session „${n}" gestartet`));
          if (!name) return;
          if (view === 'hydra' && hydraProject === p.name) await focusHydraSession(name);
          else openSession(name);
        } catch { /* toast zeigt den Fehler */ }
      };
      head.appendChild(plus);
    }
    sessionsEl.appendChild(head);
    for (const a of agents) {
      const v = agentVisual(a, p.name);
      const idx = sidebarSessions.length;
      sidebarSessions.push(a.name);
      const active = view === 'term' && a.name === activeTerm;
      const div = document.createElement('div');
      div.className = 'session' +
        (active ? ' selected' : '') +
        (a.status === 'blocked' ? ' needs-input' : '') +
        (a.unread && !active ? ' unread' : '');
      const key = idx < 9 ? `<span class="skey">⌘${idx + 1}</span>` : '';
      const dead = ['exited', 'dead'].includes(a.status);
      div.innerHTML =
        `<span class="savatar${dead ? ' dim' : ''}">${agentPortrait(a.name, 26, a)}</span>` +
        `<span class="sbody">` +
          `<span class="srow">` +
            `<span class="sname">${esc(a.name)}</span>` +
            branchChip(a) +
          `</span>` +
          `<span class="srow sub">` +
            `<span class="dot" style="background:${v.color}"></span>` +
            `<span class="sstatus">${visHtml(v)}</span>` +
            `<span class="sage">${esc(a.age)}</span>${key}` +
          `</span>` +
        `</span>` +
        (a.status === 'blocked' ? `<span class="sflag" title="wartet auf deine Eingabe">!</span>` :
          a.unread && !active ? `<span class="sdot" title="neu seit deinem letzten Blick"></span>` : '');
      div.onclick = () => openSession(a.name);
      div.oncontextmenu = e => {
        e.preventDefault();
        showMenu(e.clientX, e.clientY, a.name, a.status);
      };
      attachHover(div, a.name);
      sessionsEl.appendChild(div);
    }
  }
  if (!any) {
    sessionsEl.innerHTML = '<div class="none">Keine aktiven Sessions</div>';
  }

  if (ov?.later?.length) {
    const head = document.createElement('div');
    head.className = 'proj-head';
    head.innerHTML = `<span class="later-label">${icon('clock')} Für später</span>`;
    sessionsEl.appendChild(head);
    for (const l of ov.later) {
      const div = document.createElement('div');
      div.className = 'session later';
      div.title = `„${l.name}" wieder öffnen` + (l.project ? ` · ${l.project}` : '');
      div.innerHTML =
        `<span class="savatar dim">${agentPortrait(l.name, 20, l)}</span>` +
        `<span class="sname">${esc(l.name)}</span>` +
        (l.project ? `<span class="sstatus">${esc(l.project)}</span>` : '') +
        `<span class="sage">${esc(l.age)}</span>`;
      div.onclick = () => reopenLater(l.name);
      div.oncontextmenu = e => {
        e.preventDefault();
        showMenu(e.clientX, e.clientY, l.name, 'later');
      };
      sessionsEl.appendChild(div);
    }
  }

  const u = ov?.usage;
  usageBoxEl.innerHTML = u
    ? `<div class="usage-source">${developerIcon('claude')}<span>Claude-Limits</span></div>` +
      usageBar('5h', u.fiveHour, '↻ ' + u.fiveHourReset) +
      usageBar('7d', u.sevenDay, '↻ ' + u.sevenDayReset)
    : '';
  attentionBar();
  syncDockNav();
}

function usageColor(pct) {
  return pct >= 90 ? 'var(--critical)' : pct >= 70 ? 'var(--warning)' : 'var(--good)';
}

function usageBar(label, pct, reset) {
  const p = Math.round(pct);
  return `<div class="ubar-row" title="Claude-Limit ${label} · ${esc(reset)}">` +
    `<span class="ulabel">${label}</span>` +
    `<div class="ubar"><div class="ufill" style="width:${Math.min(p,100)}%;background:${usageColor(p)}"></div></div>` +
    `<span class="upct">${p}%</span></div>`;
}

let zg = null;
let zgAt = 0;
let zgLoading = false;

function zgDur(sec) {
  const min = Math.round(sec / 60);
  const h = Math.floor(min / 60), m = min % 60;
  return h > 0 ? `${h}h ${m}m` : `${m}m`;
}

function zgEur(n) {
  return n.toFixed(2).replace('.', ',') + ' €';
}

function zgElapsedNow() {
  if (!zg?.active) return 0;
  let sec = zg.elapsedSec;
  if (zg.state === 'running') sec += Math.floor((Date.now() - zgAt) / 1000);
  return sec;
}

async function refreshZg() {
  if (zgLoading) return;
  zgLoading = true;
  try {
    zg = await Zeitgeist();
    zgAt = Date.now();
    renderZg();
  } catch { /* Backend noch nicht bereit */ }
  zgLoading = false;
}

function renderZg() {
  if (!zg || !zg.exists) { zgBoxEl.innerHTML = ''; return; }
  const ae = document.activeElement;
  if (ae && zgBoxEl.contains(ae)) return;
  const today = zg.todaySec > 0 ? `heute ${zgDur(zg.todaySec)} · ${zgEur(zg.todayCash)}` : '';
  if (!zg.active) {
    const opts = (zg.projects || []).map(p =>
      `<option value="${esc(p.id)}"${p.name === zg.lastProject ? ' selected' : ''}>${esc(p.name)}</option>`).join('');
    zgBoxEl.innerHTML =
      `<div class="zg-row"><span class="zg-ic dim">${icon('clock')}</span>` +
      `<select class="inline-input zg-sel" id="zg-proj" title="Zeitgeist-Projekt">${opts}</select>` +
      `<button class="btn tiny" id="zg-start" title="Zeitgeist-Timer starten">${icon('play')}</button></div>` +
      (today ? `<div class="zg-sub">${today}</div>` : '');
    $('zg-start').onclick = async () => {
      const ref = $('zg-proj').value;
      if (!ref) return;
      try {
        const p = await ZeitgeistStart(ref);
        toast(`▶ Zeitgeist-Timer läuft: ${p.name}`);
      } catch (err) { toast('Fehler: ' + err, true); }
      refreshZg();
    };
    return;
  }
  const running = zg.state === 'running';
  const sec = zgElapsedNow();
  zgBoxEl.innerHTML =
    `<div class="zg-row active">` +
    `<span class="zg-ic" style="color:${running ? 'var(--good)' : 'var(--warning)'}">${running ? '▶' : '⏸'}</span>` +
    `<span class="zg-name" title="Zeitgeist · ${esc(zg.project)} · ${zg.rate} €/h">${esc(zg.project)}</span>` +
    `<span class="zg-time" id="zg-time">${zgDur(sec)}</span>` +
    `<button class="btn tiny" id="zg-pause" title="${running ? 'Timer pausieren' : 'Timer fortsetzen'}">${running ? '⏸' : '▶'}</button>` +
    `<button class="btn tiny danger" id="zg-stop" title="Timer stoppen — Session abschließen">■</button></div>` +
    `<div class="zg-sub"><span id="zg-cash">${zgEur(Math.round(sec / 3600 * zg.rate * 100) / 100)}</span>${today ? ' · ' + today : ''}</div>`;
  $('zg-pause').onclick = async () => {
    try { await (running ? ZeitgeistPause() : ZeitgeistResume()); }
    catch (err) { toast('Fehler: ' + err, true); }
    refreshZg();
  };
  $('zg-stop').onclick = e => {
    const b = e.currentTarget;
    if (b.dataset.confirm) {
      ZeitgeistStop('')
        .then(s => toast(`■ ${s.project} abgeschlossen: ${zgDur(s.durationSec)} — ${zgEur(s.earnings)}`))
        .catch(err => toast('Fehler: ' + err, true))
        .finally(refreshZg);
      return;
    }
    b.dataset.confirm = '1';
    b.textContent = 'wirklich?';
    setTimeout(() => {
      if (b.isConnected) { delete b.dataset.confirm; b.textContent = '■'; }
    }, 3000);
  };
}

function agentPill(a, project) {
  const v = agentVisual(a, project);
  const done = (a.status === 'idle' || a.status === 'running') && !a.phase
    ? `<button class="btn tiny" data-act="done" data-agent="${esc(a.name)}" title="/done — Arbeit committen und auf dev bringen">${icon('check')} done</button>`
    : '';
  const open = a.status !== 'dead'
    ? `<button class="btn tiny" data-act="open" data-agent="${esc(a.name)}" title="Terminal öffnen">${developerIcon('bash')}</button>`
    : '';
  return `<span class="pill${a.status === 'blocked' ? ' waiting' : ''}${a.unread ? ' unread' : ''}">` +
    `<span class="pill-avatar">${agentPortrait(a.name, 18, a)}</span>` +
    `<span class="dot" style="background:${v.color}"></span>` +
    `<span class="name">${esc(a.name)}</span>` +
    `<span class="st">${visHtml(v)}</span>` +
    `<span class="age">${esc(a.age)}</span>${open}${done}</span>`;
}

function attentionOverview() {
  const { waiting, active, unread, unknown } = attentionState();
  if (!waiting.length && unknown.length) {
    const label = unknown.length === 1 ? '1 Session ist nicht beobachtbar.' : `${unknown.length} Sessions sind nicht beobachtbar.`;
    return `<section class="attention-summary is-unknown" role="status" aria-label="Session-Status teilweise unbekannt">` +
      `<div class="attention-summary-lead"><span class="attention-summary-icon">${icon('warn')}</span>` +
      `<div><h1>Session-Status teilweise unbekannt</h1><p>${label} Das ist nicht dasselbe wie beendet oder ohne offene Entscheidung.</p></div></div>` +
      `<div class="attention-totals"><span><strong>${active.length}</strong> sicher aktiv</span></div></section>`;
  }
  if (!waiting.length) {
    const activity = active.length === 1
      ? '1 Session arbeitet weiter.'
      : active.length > 1
        ? `${active.length} Sessions arbeiten weiter.`
        : 'Im Moment ist keine Entscheidung offen.';
    const recent = unread.length
      ? `<span><strong>${unread.length}</strong> neu</span>`
      : '';
    return `<section class="attention-summary is-clear" aria-label="Session-Status">` +
      `<div class="attention-summary-lead"><span class="attention-summary-icon">${icon('check')}</span>` +
      `<div><h1>Keine offenen Entscheidungen</h1><p>${activity}</p></div></div>` +
      `<div class="attention-totals"><span><strong>${active.length}</strong> aktiv</span>${recent}</div></section>`;
  }

  const queue = waiting.map(a => {
    const context = [a.project, a.branch].filter(Boolean).join(' · ');
    const status = agentVisual(a, a.project);
    return `<button type="button" class="attention-session" data-act="open" data-agent="${esc(a.name)}">` +
      `<span class="attention-session-avatar">${agentPortrait(a.name, 24, a)}</span>` +
      `<span class="attention-session-copy"><strong>${esc(a.name)}</strong><span>${esc(context)}</span></span>` +
      `<span class="attention-session-status">${visHtml(status)}</span>` +
      `<span class="attention-session-open">Öffnen</span></button>`;
  }).join('');
  const title = waiting.length === 1
    ? '1 Session braucht deine Entscheidung'
    : `${waiting.length} Sessions brauchen deine Entscheidung`;
  return `<section class="attention-summary has-waiting">` +
    `<div class="attention-summary-lead"><span class="attention-summary-icon">${icon('lock')}</span>` +
    `<div><h1>${title}</h1><p>Öffne eine Session, um direkt weiterzumachen.</p></div></div>` +
    `<div class="attention-queue">${queue}</div></section>`;
}

function gitState(p, wt) {
  if (wt.branch === '(kein git)') return '';
  if (!wt.changesKnown) {
    const detail = (wt.problems || []).map(problem => problem?.message).filter(Boolean).join('; ');
    return `<span class="git-state unknown" title="${esc(detail || 'Git-Änderungen konnten nicht ermittelt werden')}">${icon('warn')} Status unbekannt</span>`;
  }
  if (wt.clean) return `<span class="git-state clean">${icon('check')} sauber</span>`;
  const parts = [];
  if (wt.staged) parts.push(`${wt.staged} staged`);
  if (wt.modified) parts.push(`${wt.modified} geändert`);
  if (wt.untracked) parts.push(`${wt.untracked} neu`);
  return `<span class="git-state clickable" data-project="${esc(p.id || p.name)}" data-worktree="${esc(wt.reference)}" title="Diff anzeigen">` +
    `<span style="color:var(--warning);font-weight:700">±</span> ${parts.join(' · ')}</span>`;
}

function worktreeActions(p, wt) {
  if (!p.path) return '';
	const projectRef = p.id || p.name;
  const busy = (wt.agents || []).some(a => !a.dock && ['running', 'agents', 'blocked'].includes(a.status));
  const anySession = (wt.agents || []).some(a => !a.dock && ['running', 'agents', 'blocked', 'idle'].includes(a.status));
  let btns = '';
  if (!busy && wt.checkoutKnown && wt.divergenceKnown && p.mainBranchKnown && wt.ahead > 0 && wt.branch !== p.mainBranch) {
    btns += `<button class="btn" data-act="merge" data-project="${esc(p.name)}" data-source="${esc(wt.branch)}" data-target="${esc(p.mainBranch)}" ` +
      `title="Claude-Session, die diesen Branch merged">${icon('merge')} ${esc(wt.branch)} → ${esc(p.mainBranch)}</button>`;
  }
  if (!wt.isMain && !anySession) {
    if (wt.changesKnown && wt.divergenceKnown && (!wt.clean || wt.ahead > 0)) {
      btns += `<button class="btn" data-act="cleanup" data-project="${esc(projectRef)}" data-worktree="${esc(wt.reference)}" title="Claude-Session zum Committen und Mergen">${icon('broom')} Cleanup</button>`;
    }
    if (!wt.changesKnown) {
      btns += `<button class="btn danger" disabled title="Git-Änderungen sind unbekannt — Entfernen wäre nicht sicher">${icon('trash')} entfernen</button>`;
    } else if (wt.clean) {
      const key = projectRef + '|' + wt.reference;
      btns += confirmRemove === key
        ? `<button class="btn danger confirm" data-act="remove2" data-project="${esc(projectRef)}" data-worktree="${esc(wt.reference)}">wirklich entfernen?</button>`
        : `<button class="btn danger" data-act="remove1" data-project="${esc(projectRef)}" data-worktree="${esc(wt.reference)}">${icon('trash')} entfernen</button>`;
    } else {
      btns += `<button class="btn danger" disabled title="uncommittete Änderungen — erst aufräumen">${icon('trash')} entfernen</button>`;
    }
  }
  return btns ? `<span class="actions">${btns}</span>` : '';
}

function worktreeRow(p, wt, idx, total) {
  const cls = ['row', wt.isMain ? 'main-row' : 'wt-row'];
  const ab = [];
  if (wt.divergenceKnown && wt.ahead) ab.push(`↑${wt.ahead}`);
  if (wt.divergenceKnown && wt.behind) ab.push(`↓${wt.behind}`);
  let abHtml = ab.length ? `<span class="ab" title="gegenüber ${esc(p.mainBranch)}">${ab.join(' ')}</span>` : '';
  if (!wt.divergenceKnown && wt.branch !== '(kein git)') {
    abHtml = `<span class="ab unknown" title="Abstand zum Hauptbranch konnte nicht ermittelt werden">?</span>`;
  } else if (p.mainBranchKnown && !wt.ahead && wt.branch !== p.mainBranch && wt.branch !== '(kein git)' && wt.branch !== '—' && p.path) {
    abHtml += `<span class="git-state" style="color:var(--good)" title="alle Commits sind in ${esc(p.mainBranch)}">${icon('check')} in ${esc(p.mainBranch)}</span>`;
  }
  const agents = (wt.agents || []).filter(a => !a.dock).map(a => agentPill(a, p.name)).join('');
  const warns = (wt.warnings || []).map(w => `<span class="warn"><span class="ic">${icon('warn')}</span>${esc(w)}</span>`).join('');
  const problemText = (wt.problems || []).map(problem => problem?.message).filter(Boolean).join('; ');
  const problem = problemText
    ? `<span class="warn repo-unknown" title="${esc(problemText)}"><span class="ic">${icon('warn')}</span>Git-Fakten unvollständig</span>`
    : '';
  const pathHtml = wt.isMain ? '' : `<span class="wt-path" title="${esc(wt.location)}">${esc(wt.location)}</span>`;
  const last = wt.lastMsg ? `<span class="lastmsg" title="letzter Commit">„${esc(wt.lastMsg)}“</span>` : '';
  const branch = wt.checkoutKnown ? wt.branch : 'Branch unbekannt';
  return `<div class="${cls.join(' ')}">` +
    `<span class="branch${wt.checkoutKnown ? '' : ' unknown'}">${esc(branch)}</span>${abHtml}${gitState(p, wt)}${agents}${warns}${problem}${pathHtml}${last}${worktreeActions(p, wt)}</div>`;
}

function projectCard(p) {
  const rows = (p.worktrees || []).map((wt, i) => worktreeRow(p, wt, i, p.worktrees.length)).join('');
  let projectTools = '';
  if (p.path) {
    const mainCfg = editingMain === p.name
      ? `<div class="project-menu-config"><label for="main-input">Hauptbranch</label>` +
        `<span class="maincfg"><input class="inline-input" id="main-input" value="${esc(p.mainBranch)}" placeholder="main">` +
        `<button class="btn tiny" data-act="mainsave" data-project="${esc(p.name)}" title="Hauptbranch speichern">${icon('check')}</button>` +
        `<button class="btn tiny" data-act="maincancel" title="Änderung verwerfen">${icon('x')}</button></span></div>`
      : `<button class="project-menu-item" data-act="mainedit" data-project="${esc(p.name)}" title="Hauptbranch ändern">` +
        `<span>${icon('gitbranch')} Hauptbranch</span><b>${esc(p.mainBranchKnown ? p.mainBranch : 'unbekannt')}</b></button>`;
    const rmProj = confirmRemoveProject === p.name
      ? `<button class="project-menu-item danger confirm" data-act="rmproj2" data-project="${esc(p.name)}">${icon('x')} Repository wirklich entfernen?</button>`
      : `<button class="project-menu-item danger" data-act="rmproj1" data-project="${esc(p.name)}" title="Repository aus magentic entfernen — löscht keine Dateien">${icon('x')} Repository entfernen</button>`;
    const menuOpen = editingMain === p.name || confirmRemoveProject === p.name ? ' open' : '';
    projectTools = `<div class="project-tools">` +
      `<div class="project-lenses"><span class="project-tools-label">Ansichten</span>` +
      `<button class="project-lens" data-act="showgraph" data-project="${esc(p.name)}" title="Git-Graph dieses Projekts — wo Worktrees abzweigen und zusammenlaufen">${developerIcon('git')} Graph</button>` +
      `<button class="project-lens" data-act="showboard" data-project="${esc(p.name)}" title="Board aus allen Spec-Ordnern — Plan, Tasks und was gerade läuft">${developerIcon('markdown')} Board</button>` +
      `<button class="project-lens" data-act="showstats" data-project="${esc(p.name)}" title="Statistik mit Fokus auf dieses Projekt">${icon('chart')} Statistik</button></div>` +
      `<div class="project-primary-actions">` +
      `<button class="btn primary" data-act="newsession" data-project="${esc(p.name)}" title="Neue Session im Projekt">${icon('play')} Session</button>` +
      `<button class="btn" data-act="newworktree" data-project="${esc(p.name)}" title="${p.repositoryKnowledge === 'known' ? 'Neue Session in eigenem Worktree' : 'Repository-Status ist unbekannt'}"${p.repositoryKnowledge === 'known' ? '' : ' disabled'}>${developerIcon('git')} Worktree</button>` +
      `<details class="project-more"${menuOpen}><summary>Mehr</summary><div class="project-more-menu">` +
      `<button class="project-menu-item" data-act="newterm" data-project="${esc(p.name)}" title="Reines Terminal im Projekt — Shell statt Agent">${developerIcon('bash')} Terminal öffnen</button>` +
      `<button class="project-menu-item" data-act="deploy" data-project="${esc(p.name)}" title="Neue Session, die /deploy ausführt">${icon('rocket')} Deploy starten</button>` +
      `<div class="project-menu-separator"></div>${mainCfg}${rmProj}</div></details></div></div>`;
  }
  const mainBranch = p.path
    ? `<span class="project-main-branch${p.mainBranchKnown ? '' : ' unknown'}" title="${p.mainBranchKnown ? 'Hauptbranch' : 'Hauptbranch konnte nicht ermittelt werden'}">${icon(p.mainBranchKnown ? 'gitbranch' : 'warn')} ${esc(p.mainBranchKnown ? p.mainBranch : 'unbekannt')}</span>`
    : '';
  return `<div class="card project-card"><div class="card-head"><h2>${esc(p.name)}</h2>${mainBranch}` +
    `<span class="path">${esc(p.path || '')}</span></div><div class="rows">${rows}</div>${projectTools}</div>`;
}

let deployStatus = null;
let deployStamp = '';
let deploySync = { kind: 'loading', error: '', lastOkAt: '' };
let argoExpanded = false;
let dsWatchUntil = 0;
let deploySawRunning = false;
let deployTerminalAt = 0;

function startDeployWatch() {
  dsWatchUntil = Date.now() + 15 * 60 * 1000;
  deploySawRunning = false;
  deployTerminalAt = 0;
  refreshDeployStatus();
}

function deployStage() {
  const ds = deployStatus;
  if (!ds) return null;
  const running = (ds.builds || []).filter(b => b.status === 'inProgress' || b.status === 'notStarted');
  if (running.length) {
    deploySawRunning = true;
    deployTerminalAt = 0;
    const b = running[0];
    const extra = running.length > 1 ? ` +${running.length - 1}` : '';
    return { cls: 'db-running', title: 'Build läuft…', sub: `${b.repo} · ${b.branch}${extra}`, age: b.age };
  }
  const prog = (ds.apps || []).filter(a => a.health === 'Progressing');
  if (prog.length) {
    deploySawRunning = true;
    deployTerminalAt = 0;
    const a = prog[0];
    const extra = prog.length > 1 ? ` +${prog.length - 1}` : '';
    return { cls: 'db-running', title: 'Rollout läuft…', sub: `${a.name} · ${a.namespace}${extra}` };
  }
  if (Date.now() >= dsWatchUntil) return null;
  if (!deploySawRunning) {
    return { cls: 'db-running', title: 'Deploy angestoßen…', sub: 'warte auf die Pipeline' };
  }
  if (deployTerminalAt === 0) deployTerminalAt = Date.now();
  if (Date.now() - deployTerminalAt >= 90 * 1000) return null;
  const failed = (ds.builds || []).some(b => b.status === 'completed' && b.result === 'failed');
  if (failed) return { cls: 'db-failed', title: 'Build fehlgeschlagen', sub: 'Details ansehen' };
  return { cls: 'db-done', title: 'Deploy fertig ✓', sub: 'alle Builds & Rollouts durch' };
}

function renderDeployBadge() {
  if (deploySync.kind === 'stale') {
    deployBadgeEl.className = 'db-stale';
    deployBadgeEl.innerHTML =
      `<div class="db-line"><span class="db-title">${icon('warn')} Deploy-Stand unklar</span></div>` +
      `<div class="db-sub">Letzter erfolgreicher Stand bleibt sichtbar</div>`;
    return;
  }
  const s = deployStage();
  if (!s) { deployBadgeEl.className = ''; deployBadgeEl.innerHTML = ''; return; }
  deployBadgeEl.className = s.cls;
  const age = s.age ? `<span class="db-age">${esc(s.age)}</span>` : '';
  deployBadgeEl.innerHTML =
    `<div class="db-line"><span class="db-pulse"></span>` +
    `<span class="db-title">${icon('rocket')} ${esc(s.title)}</span>${age}</div>` +
    (s.sub ? `<div class="db-sub">${esc(s.sub)}</div>` : '');
}

deployBadgeEl.onclick = () => {
  showOverview();
  refreshDeployStatus();
  const card = $('deploy-card');
  if (card) {
    card.scrollIntoView({ behavior: 'smooth', block: 'start' });
    card.classList.add('flash');
    setTimeout(() => card.classList.remove('flash'), 1200);
  }
};

const BUILD_ICON = {
  succeeded: ['✓', 'var(--good)'],
  failed: ['✗', 'var(--critical)'],
  canceled: ['⊘', 'var(--muted)'],
};

function buildRow(b) {
  let icon, color;
  if (b.status === 'inProgress' || b.status === 'notStarted') {
    icon = '●'; color = 'var(--accent)';
  } else {
    [icon, color] = BUILD_ICON[b.result] || ['?', 'var(--muted)'];
  }
  const running = b.status === 'inProgress' ? ' läuft…' : '';
  return `<div class="ds-row" data-url="${esc(b.url)}" title="Build in Azure DevOps öffnen">` +
    `<span class="ds-ic" style="color:${color}">${icon}</span>` +
    `<span class="ds-name">${esc(b.repo)}</span>` +
    `<span class="ds-branch">${esc(b.branch)}</span>` +
    `<span class="ds-info">${esc(b.result || b.status)}${running}</span>` +
    `<span class="ds-age">${esc(b.age)}</span></div>`;
}

function argoRow(a) {
  const healthColor = a.health === 'Healthy' ? 'var(--good)'
    : a.health === 'Progressing' ? 'var(--accent)' : 'var(--critical)';
  const syncColor = a.sync === 'Synced' ? 'var(--good)' : 'var(--warning)';
  return `<div class="ds-row" data-url="${esc(a.url)}" title="App in Argo öffnen (ns ${esc(a.namespace)})">` +
    `<span class="ds-ic" style="color:${healthColor}">●</span>` +
    `<span class="ds-name">${esc(a.name)}</span>` +
    `<span class="ds-branch">${esc(a.namespace)}</span>` +
    `<span class="ds-info" style="color:${syncColor}">${esc(a.sync)}</span>` +
    `<span class="ds-info" style="color:${healthColor}">${esc(a.health)}</span></div>`;
}

function deploySyncState() {
  if (!['stale', 'error'].includes(deploySync.kind)) return '';
  const hasData = !!deployStatus;
  const title = deploySync.kind === 'stale'
    ? 'Pipeline-Status konnte nicht aktualisiert werden'
    : 'Pipeline-Status ist nicht erreichbar';
  const last = deploySync.lastOkAt
    ? ` Letzter erfolgreicher Stand: ${deploySync.lastOkAt}.`
    : '';
  return `<div class="overview-sync ${hasData ? 'is-stale' : 'is-error'}" role="${hasData ? 'status' : 'alert'}">` +
    `<span class="overview-sync-icon">${icon('warn')}</span>` +
    `<span class="overview-sync-copy"><strong>${title}</strong>` +
    `<span>${esc(deploySync.error)}${esc(last)}</span></span>` +
    `<button class="btn" data-act="dsrefresh">Erneut versuchen</button></div>`;
}

function deployCard() {
  const ds = deployStatus;
  if (!ds) {
    if (deploySync.kind === 'error') {
      return `<div class="card" id="deploy-card"><div class="card-head"><h2>${icon('rocket')} Pipelines &amp; Argo</h2></div>` +
        `${deploySyncState()}</div>`;
    }
    return `<div class="card" id="deploy-card"><div class="card-head"><h2>${icon('rocket')} Pipelines &amp; Argo</h2>` +
      `<span class="path">lade…</span></div></div>`;
  }
  const azChip = ds.azOk
    ? `<span class="ds-chip ok">${developerIcon('azure')} Azure ✓</span>`
    : `<span class="ds-chip bad" title="${esc(ds.azErr)}">${developerIcon('azure')} Azure ✗</span>` +
      `<button class="btn tiny" data-act="azlogin">az login</button>`;
  const subChip = ds.azSub
    ? `<button class="ds-chip sub" data-act="azsub" title="Azure-Subscription wechseln · ${esc(ds.azSub)}\n${esc(ds.azSubId)}">${developerIcon('azure')} ${esc(shortSub(ds.azSub))} ▾</button>`
    : '';
  const argoChip = ds.argoOk
    ? `<span class="ds-chip ok" title="${esc(ds.argoServer)}">Argo ✓</span>`
    : `<span class="ds-chip bad" title="${esc(ds.argoErr)}">Argo ✗</span>` +
      `<button class="btn tiny" data-act="argologin">argocd login</button>`;
  const builds = (ds.builds || []).map(buildRow).join('') ||
    (ds.azOk ? '<div class="none">keine Builds</div>' : `<div class="none">${esc(ds.azErr)}</div>`);
  const apps = ds.apps || [];
  const problems = apps.filter(a => a.sync !== 'Synced' || a.health !== 'Healthy');
  const healthy = apps.length - problems.length;
  let argoHtml = problems.map(argoRow).join('');
  if (healthy > 0) {
    argoHtml += argoExpanded
      ? apps.filter(a => a.sync === 'Synced' && a.health === 'Healthy').map(argoRow).join('') +
        `<div class="ds-more" data-act="argoless">▲ einklappen</div>`
      : `<div class="ds-more" data-act="argomore">✓ ${healthy} Apps Synced &amp; Healthy — anzeigen ▾</div>`;
  }
  if (!apps.length && !ds.argoOk) argoHtml = `<div class="none">${esc(ds.argoErr)}</div>`;
  const watching = Date.now() < dsWatchUntil
    ? `<span class="ds-chip watch">${icon('clock')} verfolge Deploy (10s-Takt)</span>` : '';
  return `<div class="card" id="deploy-card"><div class="card-head"><h2>${icon('rocket')} Pipelines &amp; Argo</h2>` +
    `${azChip}${subChip}${argoChip}${watching}` +
    `<span class="actions"><span class="path">${esc(deployStamp)}</span>` +
    `<button class="btn tiny" data-act="dsrefresh" title="Status neu laden">↻</button></span></div>` +
    `${deploySyncState()}<div class="ds-cols"><div class="ds-col"><div class="ds-title">${developerIcon('azure')} Azure DevOps Builds</div>${builds}</div>` +
    `<div class="ds-col"><div class="ds-title">${developerIcon('kubernetes')} ArgoCD</div>${argoHtml}</div></div></div>`;
}

let dsLoading = false;
async function refreshDeployStatus() {
  if (dsLoading) return;
  dsLoading = true;
  try {
    deployStatus = await DeployStatus();
    const now = new Date().toLocaleTimeString('de-DE');
    deployStamp = 'Stand ' + now;
    deploySync = { kind: 'fresh', error: '', lastOkAt: now };
    renderDeployBadge();
    if (view === 'overview') renderOverview();
  } catch (e) {
    const next = {
      kind: deployStatus ? 'stale' : 'error',
      error: errorText(e),
      lastOkAt: deploySync.lastOkAt,
    };
    const changed = next.kind !== deploySync.kind || next.error !== deploySync.error;
    deploySync = next;
    if (changed) {
      renderDeployBadge();
      if (view === 'overview') renderOverview();
    }
  }
  dsLoading = false;
}

EventsOn('login:az', msg => {
  toast(msg === 'ok' ? 'Azure-Login erfolgreich' : 'az login: ' + msg, msg !== 'ok');
  refreshDeployStatus();
});
EventsOn('login:argo', msg => {
  toast(msg === 'ok' ? 'Argo-Login erfolgreich' : 'argocd login: ' + msg, msg !== 'ok');
  refreshDeployStatus();
});

function overviewSyncState() {
  if (!['stale', 'error'].includes(overviewSync.kind)) return '';
  const hasData = !!ov;
  const title = hasData ? 'Aktualisierung unterbrochen' : 'Übersicht ist nicht erreichbar';
  const last = overviewSync.lastOkAt
    ? ` Daten vom letzten erfolgreichen Stand (${overviewSync.lastOkAt}) bleiben sichtbar.`
    : '';
  return `<section class="overview-sync ${hasData ? 'is-stale' : 'is-error'}" role="${hasData ? 'status' : 'alert'}">` +
    `<span class="overview-sync-icon">${icon('warn')}</span>` +
    `<span class="overview-sync-copy"><strong>${title}</strong>` +
    `<span>${esc(overviewSync.error)}${esc(last)}</span></span>` +
    `<button class="btn" data-act="retryoverview">Erneut versuchen</button></section>`;
}

function renderOverview() {
  if (!ov) {
    overviewEl.innerHTML = overviewSync.kind === 'error'
      ? `<div class="overview-initial-error">${overviewSyncState()}</div>`
      : '<div class="none" style="padding:30px">lade…</div>';
    return;
  }
  const ae = document.activeElement;
  if (ae && overviewEl.contains(ae) && ['INPUT', 'SELECT', 'TEXTAREA'].includes(ae.tagName)) {
    return;
  }
  const cards = (ov.projects || []).map(projectCard).join('');
  overviewEl.innerHTML = `${overviewSyncState()}${attentionOverview()}${cards}${deployCard()}` +
    `<div class="add-repo"><button class="btn" data-act="addproject" title="Git-Repository als Projekt hinzufügen">+ Repository hinzufügen…</button></div>` +
    `<div class="stamp">Stand ${esc(ov.generatedAt || '')}</div>`;
}

function renderAll() {
  renderSidebar();
  if (view === 'overview') renderOverview();
  if (view === 'term') updateTermBar();
  if (view === 'hydra') { updateHydraBar(); syncHydra(); }
}

overviewEl.addEventListener('click', async e => {
  const gs = e.target.closest('.git-state[data-worktree]');
  if (gs) {
    showModal('Worktree-Diff', 'lade…', false);
    try {
      const diff = await WorktreeDiff(gs.dataset.project, gs.dataset.worktree);
      showModal('Worktree-Diff', diff, true);
    } catch (err) {
      showModal('Diff', 'Fehler: ' + err, false);
    }
    return;
  }
  const row = e.target.closest('.ds-row[data-url]');
  if (row) { BrowserOpenURL(row.dataset.url); return; }
  const more = e.target.closest('.ds-more[data-act]');
  if (more) {
    argoExpanded = more.dataset.act === 'argomore';
    renderOverview();
    return;
  }
  const b = e.target.closest('button[data-act]');
  if (!b || b.disabled) return;
  const d = b.dataset;
  b.disabled = true;
  try {
    switch (d.act) {
      case 'retryoverview': await refresh(true); break;
      case 'open': await openSession(d.agent); break;
      case 'showgraph': await showGraph(d.project); break;
      case 'showboard': await showBoard(d.project); break;
      case 'showstats': await showStats(d.project); break;
      case 'done': await act(DoneAgent(d.agent), `/done an „${d.agent}" gesendet — Plan in der Session bestätigen`); break;
      case 'cleanup': await act(Cleanup(d.project, d.worktree), n => `Cleanup-Agent „${n}" gestartet`); break;
      case 'merge': await act(Merge(d.project, d.source, d.target), n => `Merge-Agent „${n}" gestartet (${d.source} → ${d.target})`); break;
      case 'deploy':
        await act(Deploy(d.project), n => `Deploy-Agent „${n}" gestartet (/deploy)`);
        startDeployWatch();
        break;
      case 'newsession': await act(NewSession(d.project, false, ''), n => `Session „${n}" gestartet`); break;
      case 'newworktree': await act(NewSession(d.project, true, ''), n => `Worktree-Session „${n}" gestartet`); break;
      case 'newterm': {
        const n = await act(NewTermSession(d.project, false, ''), x => `Terminal „${x}" geöffnet`);
        if (n) openSession(n);
        break;
      }
      case 'addproject': {
        const path = await PickFolder();
        if (path) await act(AddProject(path), n => `Repository „${n}" hinzugefügt`);
        break;
      }
      case 'rmproj1': confirmRemoveProject = d.project; renderOverview(); break;
      case 'rmproj2':
        confirmRemoveProject = null;
        await act(RemoveProject(d.project), `Repository „${d.project}" entfernt`);
        break;
      case 'remove1': confirmRemove = d.project + '|' + d.worktree; renderOverview(); break;
      case 'remove2':
        confirmRemove = null;
        await act(RemoveWorktree(d.project, d.worktree), 'Worktree entfernt');
        break;
      case 'mainedit': editingMain = d.project; renderOverview(); $('main-input')?.focus(); break;
      case 'maincancel': editingMain = null; renderOverview(); break;
      case 'mainsave': {
        const v = $('main-input').value.trim();
        editingMain = null;
        await act(SetMainBranch(d.project, v), v ? `Hauptbranch: ${v}` : 'Hauptbranch: automatisch');
        break;
      }
      case 'dsrefresh': await refreshDeployStatus(); break;
      case 'azsub': await openSubPicker(b); break;
      case 'azlogin': AzLogin(); toast('Browser öffnet sich für den Azure-Login…'); break;
      case 'argologin': ArgoLogin(); toast('Browser öffnet sich für den Argo-SSO-Login…'); break;
    }
  } catch { /* toast zeigt den Fehler */ }
  b.disabled = false;
});

let lastDataKey = '';
let refreshPromise = null;
let refreshQueuedForce = false;

async function refreshOnce(force) {
  const previousKind = overviewSync.kind;
  try {
    const [o, p] = await Promise.all([Overview(!!force), Projects()]);
    ov = o; projects = p || [];
    overviewSync = {
      kind: 'fresh',
      error: '',
      lastOkAt: o.generatedAt || new Date().toLocaleTimeString('de-DE'),
    };
    const key = JSON.stringify([{ ...o, generatedAt: '' }, projects]);
    const recovered = previousKind === 'stale' || previousKind === 'error';
    if (key === lastDataKey && !force && !recovered) {
      const stamp = document.querySelector('.stamp');
      if (stamp) stamp.textContent = 'Stand ' + (o.generatedAt || '');
    } else {
      lastDataKey = key;
      if (editingMain === null || force) renderAll();
      else renderSidebar();
    }
  } catch (e) {
    const next = {
      kind: ov ? 'stale' : 'error',
      error: errorText(e),
      lastOkAt: overviewSync.lastOkAt || ov?.generatedAt || '',
    };
    const changed = next.kind !== overviewSync.kind || next.error !== overviewSync.error;
    overviewSync = next;
    if (changed && view === 'overview') renderOverview();
  }
}

async function refresh(force = false) {
  if (refreshPromise) {
    if (force) refreshQueuedForce = true;
    return refreshPromise;
  }
  refreshPromise = (async () => {
    let nextForce = !!force;
    do {
      refreshQueuedForce = false;
      await refreshOnce(nextForce);
      nextForce = refreshQueuedForce;
    } while (nextForce);
  })();
  try {
    await refreshPromise;
  } finally {
    refreshPromise = null;
  }
}

window.addEventListener('resize', () => {
  if (view === 'hydra') {
    for (const [n, t] of terms) {
      if (t.wrap.parentElement === hydraGridEl) {
        t.fit.fit();
        ResizeTerm(n, t.term.cols, t.term.rows);
      }
    }
    return;
  }
  const t = activeTerm && terms.get(activeTerm);
  if (t) t.fit.fit();
});

const modalEl = document.createElement('div');
modalEl.id = 'modal';
modalEl.innerHTML =
  '<div id="modal-box"><div id="modal-head"><span id="modal-title"></span>' +
  `<button class="btn tiny" id="modal-close">schließen ${icon('x')}</button></div><pre id="modal-pre"></pre></div>`;
document.body.appendChild(modalEl);
$('modal-close').onclick = () => { modalEl.style.display = 'none'; };
modalEl.addEventListener('mousedown', e => { if (e.target === modalEl) modalEl.style.display = 'none'; });

function showModal(title, content, colorizeDiff) {
  $('modal-title').textContent = title;
  const pre = $('modal-pre');
  if (colorizeDiff) {
    pre.innerHTML = content.split('\n').map(l => {
      const el = esc(l);
      if (l.startsWith('diff --git') || l.startsWith('──')) return `<span class="dl-file">${el}</span>`;
      if (l.startsWith('@@')) return `<span class="dl-hunk">${el}</span>`;
      if (l.startsWith('+')) return `<span class="dl-add">${el}</span>`;
      if (l.startsWith('-')) return `<span class="dl-del">${el}</span>`;
      return el;
    }).join('\n');
  } else {
    pre.textContent = content;
  }
  modalEl.style.display = 'flex';
}

const hoverEl = document.createElement('div');
hoverEl.id = 'hoverprev';
document.body.appendChild(hoverEl);
let hoverTimer = null;

function attachHover(div, name) {
  div.addEventListener('mouseenter', () => {
    clearTimeout(hoverTimer);
    hoverTimer = setTimeout(async () => {
      try {
        const txt = await SessionPreview(name);
        if (!txt || !div.isConnected) return;
        const r = div.getBoundingClientRect();
        hoverEl.textContent = txt;
        hoverEl.style.display = 'block';
        hoverEl.style.left = (r.right + 10) + 'px';
        hoverEl.style.top = '0px';
        const top = Math.max(4, Math.min(r.top, window.innerHeight - hoverEl.offsetHeight - 10));
        hoverEl.style.top = top + 'px';
      } catch { /* Session weg */ }
    }, 350);
  });
  div.addEventListener('mouseleave', () => {
    clearTimeout(hoverTimer);
    hoverEl.style.display = 'none';
  });
  div.addEventListener('mousedown', () => {
    clearTimeout(hoverTimer);
    hoverEl.style.display = 'none';
  });
}

let searchHits = [];

function historyCoverageNotice(sources, summary) {
  const degraded = (Array.isArray(sources) ? sources : [])
    .filter(source => ['partial', 'unavailable'].includes(source?.state));
  if (!degraded.length) return { degraded, html: '' };
  const problems = degraded.flatMap(source => Array.isArray(source?.problems) ? source.problems : [])
    .filter(Boolean);
  const detail = problems.length ? ` title="${esc(problems.join(' · '))}"` : '';
  return {
    degraded,
    html: `<div class="tl-coverage" role="status"${detail}>${icon('warn')}<span><strong>${esc(summary)}</strong> ${esc(degraded.map(source => source.source).join(', '))} konnte${degraded.length === 1 ? '' : 'n'} nicht vollständig gelesen werden.</span></div>`,
  };
}

function highlightQuery(text, q) {
  const et = esc(text);
  const eq = esc(q).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  try {
    return et.replace(new RegExp(eq, 'gi'), m => `<mark>${m}</mark>`);
  } catch {
    return et;
  }
}

async function runSearch() {
  const q = $('search-input').value.trim();
  const res = $('search-results');
  if (q.length < 3) { res.innerHTML = '<div class="none">mindestens 3 Zeichen</div>'; return; }
  res.innerHTML = '<div class="none">suche in allen Transkripten…</div>';
  try {
    const result = (await SearchTranscripts(q)) || {};
    searchHits = Array.isArray(result.hits) ? result.hits : [];
    const coverage = historyCoverageNotice(result.sources, 'Suche umfasst nur lesbare Quellen.');
    if (!searchHits.length) {
      res.innerHTML = coverage.html + `<div class="none">${coverage.degraded.length ? 'keine Treffer in den lesbaren Quellen' : 'keine Treffer'}</div>`;
      return;
    }
    res.innerHTML = coverage.html + searchHits.map((h, i) =>
      `<div class="hit" data-hit="${i}">` +
      `<div class="hit-meta"><span class="hit-proj${h.projectKnown ? '' : ' unknown'}" title="${esc(h.attributionProblem || '')}">${esc(h.project)}</span>` +
      `<span class="hit-role ${h.role}">${h.role === 'user' ? 'Du' : `${providerIcon(h.provider)}${esc(h.provider || 'Coding-Agent')}`}</span>` +
      `<span class="hit-time">${esc(h.time)}</span></div>` +
      `<div class="hit-snippet">${highlightQuery(h.snippet, q)}</div></div>`
    ).join('');
  } catch (err) {
    res.innerHTML = `<div class="none">Fehler: ${esc(err)}</div>`;
  }
}

$('search-go').onclick = runSearch;
$('search-input').addEventListener('keydown', e => { if (e.key === 'Enter') runSearch(); });
$('search-results').addEventListener('click', e => {
  const hit = e.target.closest('.hit[data-hit]');
  if (!hit) return;
  const h = searchHits[parseInt(hit.dataset.hit)];
  if (h) showModal(`${h.project} · ${h.role === 'user' ? 'Du' : (h.provider || 'Coding-Agent')} · ${h.time}`, h.full, false);
});

let tlEntries = [];
let tlSources = [];
let tlTimer = null;
let tlLoading = false;

function refitTerms() {
  if (view === 'hydra') {
    for (const [n, t] of terms) {
      if (t.wrap.parentElement === hydraGridEl) { t.fit.fit(); ResizeTerm(n, t.term.cols, t.term.rows); }
    }
  } else if (view === 'term') {
    const t = activeTerm && terms.get(activeTerm);
    if (t) { t.fit.fit(); ResizeTerm(activeTerm, t.term.cols, t.term.rows); }
  }
}

function tlToggle(open) {
  const willOpen = open ?? !document.body.classList.contains('tl-open');
  document.body.classList.toggle('tl-open', willOpen);
  clearInterval(tlTimer);
  tlTimer = null;
  if (willOpen) {
    refreshTimeline();
    tlTimer = setInterval(() => { if (!document.hidden) refreshTimeline(); }, 60000);
  }
  refitTerms();
}

async function refreshTimeline() {
  if (tlLoading) return;
  tlLoading = true;
  try {
    const result = (await Timeline()) || {};
    tlEntries = Array.isArray(result.entries) ? result.entries : [];
    tlSources = Array.isArray(result.sources) ? result.sources : [];
    renderTimeline();
  } catch (err) {
    $('tl-body').innerHTML = `<div class="none">Fehler: ${esc(err)}</div>`;
  }
  tlLoading = false;
}

function renderTimeline() {
  const body = $('tl-body');
  const coverage = historyCoverageNotice(tlSources, 'Verlauf teilweise verfügbar.');
  if (!tlEntries.length) {
    body.innerHTML = coverage.html + `<div class="none">${coverage.degraded.length ? 'keine Prompts in den lesbaren Quellen' : 'keine Prompts aus unterstützten Sessions in den letzten 7 Tagen'}</div>`;
    return;
  }
  let html = '', day = '';
  tlEntries.forEach((en, i) => {
    if (en.day !== day) {
      day = en.day;
      html += `<div class="tl-day">${esc(day)}</div>`;
    }
    const source = en.source ? `<span class="tl-source">${providerIcon(en.source)}${esc(en.source)}</span>` : '';
    const who = en.agent ? `<span class="tl-agent">${esc(en.agent)}</span>` : '';
    html += `<button type="button" class="tl-row" data-i="${i}" title="Session öffnen oder Prompt anzeigen">` +
      `<span class="tl-time">${esc(en.time)}</span>` +
      `<div class="tl-main"><div class="tl-meta">${source}${who}<span class="tl-proj${en.projectKnown ? '' : ' unknown'}" title="${esc(en.attributionProblem || '')}">${esc(en.project)}</span></div>` +
      `<div class="tl-text">${esc(en.text)}</div></div></button>`;
  });
  const st = body.scrollTop;
  body.innerHTML = coverage.html + html;
  body.scrollTop = st;
}

$('nav-timeline').onclick = () => tlToggle();
$('tl-close').onclick = () => tlToggle(false);
$('tl-body').addEventListener('click', e => {
  const row = e.target.closest('.tl-row[data-i]');
  if (!row) return;
  const en = tlEntries[parseInt(row.dataset.i)];
  if (!en) return;
  if (en.agent && agentInfo(en.agent)) openSession(en.agent);
  else showModal(`${en.source ? `${en.source} · ` : ''}${en.project} · ${en.day} ${en.time}`, en.text, false);
});

const menuEl = document.createElement('div');
menuEl.id = 'ctxmenu';
document.body.appendChild(menuEl);
let menuFor = null;

function hideMenu() {
  menuEl.style.display = 'none';
  menuFor = null;
}

function showMenu(x, y, name, status) {
  menuFor = name;
  const session = agentInfo(name) || (ov?.later || []).find(item => item.name === name);
  if (status === 'later') {
    menuEl.innerHTML =
      `<div class="mi-head">${sessionToolMark(session)}${esc(name)}</div>` +
      `<div class="mi" data-mi="reopen">${icon('play')} Wieder öffnen</div>` +
      `<div class="mi danger" data-mi="kill">${icon('x')} Endgültig entfernen</div>`;
  } else {
    const done = ['idle', 'running'].includes(status)
      ? `<div class="mi" data-mi="done">${icon('check')} /done senden</div>` : '';
    menuEl.innerHTML =
      `<div class="mi-head">${sessionToolMark(session)}${esc(name)}</div>` +
      `<div class="mi" data-mi="open">${developerIcon('bash')} Terminal öffnen</div>` + done +
      `<div class="mi" data-mi="later">${icon('clock')} Für später schließen</div>` +
      `<div class="mi danger" data-mi="kill">${icon('x')} Session beenden</div>`;
  }
  menuEl.style.display = 'block';
  menuEl.style.left = Math.min(x, window.innerWidth - 200) + 'px';
  menuEl.style.top = Math.min(y, window.innerHeight - menuEl.offsetHeight - 10) + 'px';
}

async function openTermInContext() {
  let name = null;
  try {
    if (activeTerm) {
      name = await act(NewTermSessionFor(activeTerm), x => `Terminal „${x}" geöffnet`);
    } else if (hydraProject) {
      name = await act(NewTermSession(hydraProject, false, ''), x => `Terminal „${x}" geöffnet`);
    } else {
      toast('⌘T öffnet ein Terminal im Verzeichnis der offenen Session — hier stattdessen den ⌨-Button der Projektkarte nutzen', true);
      return;
    }
  } catch { return; }
  if (!name) return;
  if (view === 'hydra') await focusHydraSession(name);
  else openSession(name);
}

async function afterSessionGone(name) {
  if (dockTabs().includes(name)) closeDockTab(name);
  const t = terms.get(name);
  if (t) {
    EventsOff('term:data:' + name);
    EventsOff('term:closed:' + name);
    try { t.term.dispose(); } catch { /* schon weg */ }
    t.wrap.remove();
    terms.delete(name);
  }
  if (view === 'hydra') {
    if (activeTerm === name) { activeTerm = null; SetActiveTerm(''); }
    await refresh(true);
    syncHydra();
    return;
  }
  if (activeTerm === name) showOverview();
}

async function killSession(name) {
  try {
    await act(KillSession(name), `Session „${name}" beendet`);
  } catch { return; }
  await afterSessionGone(name);
}

async function parkSession(name) {
  try {
    await act(LaterSession(name), `Session „${name}" für später geparkt`);
  } catch { return; }
  await afterSessionGone(name);
}

async function reopenLater(name) {
  try {
    await act(ReopenSession(name), `Session „${name}" wieder geöffnet`);
  } catch { return; }
  openSession(name);
}

menuEl.addEventListener('click', async e => {
  const mi = e.target.closest('.mi');
  if (!mi || !menuFor) return;
  const name = menuFor;
  switch (mi.dataset.mi) {
    case 'open': hideMenu(); openSession(name); break;
    case 'later': hideMenu(); parkSession(name); break;
    case 'reopen': hideMenu(); reopenLater(name); break;
    case 'done':
      hideMenu();
      try { await act(DoneAgent(name), `/done an „${name}" gesendet — Plan in der Session bestätigen`); } catch { }
      break;
    case 'kill':
      if (mi.dataset.confirm) { hideMenu(); killSession(name); }
      else { mi.dataset.confirm = '1'; mi.innerHTML = icon('x') + ' wirklich beenden?'; }
      break;
  }
});
document.addEventListener('mousedown', e => { if (!menuEl.contains(e.target)) hideMenu(); });
window.addEventListener('blur', hideMenu);

const subMenuEl = document.createElement('div');
subMenuEl.id = 'submenu';
document.body.appendChild(subMenuEl);
function hideSubMenu() { subMenuEl.style.display = 'none'; }

function shortUrl(u) {
  u = u.replace(/^https?:\/\//, '');
  return u.length > 64 ? u.slice(0, 61) + '…' : u;
}

async function openLinksMenu(anchor) {
  const name = activeTerm;
  const r = anchor.getBoundingClientRect();
  subMenuEl.innerHTML = `<div class="mi-head">Links</div><div class="mi muted">lade…</div>`;
  subMenuEl.style.display = 'block';
  subMenuEl.style.left = Math.max(8, Math.min(r.left, window.innerWidth - 380)) + 'px';
  subMenuEl.style.top = (r.bottom + 6) + 'px';
  let links = [];
  try { links = (await SessionLinks(name)) || []; } catch { links = []; }
  if (subMenuEl.style.display === 'none' || activeTerm !== name) return;
  if (!links.length) {
    subMenuEl.innerHTML = `<div class="mi-head">Links</div><div class="mi muted">keine Links in dieser Session gefunden</div>`;
    return;
  }
  subMenuEl.innerHTML = `<div class="mi-head">Links — Klick öffnet · ⌥-Klick kopiert</div>` +
    links.map(l =>
      `<div class="mi" data-url="${esc(l.url)}" title="${esc(l.url)}">` +
      `<span class="linkurl">${esc(shortUrl(l.url))}</span>` +
      (l.time ? `<span class="linktime">${esc(l.time)}</span>` : '') +
      `</div>`).join('');
}

async function openSubPicker(anchor) {
  const r = anchor.getBoundingClientRect();
  subMenuEl.innerHTML = `<div class="mi-head">${developerIcon('azure')} Azure-Subscription</div><div class="mi muted">lade…</div>`;
  subMenuEl.style.display = 'block';
  subMenuEl.style.left = Math.max(8, Math.min(r.left, window.innerWidth - 360)) + 'px';
  subMenuEl.style.top = (r.bottom + 6) + 'px';
  let accs = [];
  try { accs = await AzAccounts(); } catch { accs = []; }
  if (subMenuEl.style.display === 'none') return;
  if (!accs.length) {
    subMenuEl.innerHTML = `<div class="mi-head">${developerIcon('azure')} Azure-Subscription</div>` +
      `<div class="mi muted">keine gefunden — erst „az login"</div>`;
    return;
  }
  const cur = deployStatus?.azSubId || '';
  subMenuEl.innerHTML = `<div class="mi-head">${developerIcon('azure')} Subscription wechseln</div>` +
    accs.map(s => {
      const active = s.id === cur || (!cur && s.isDefault);
      return `<div class="mi${active ? ' active' : ''}" data-sub="${esc(s.id)}" title="${esc(s.id)}">` +
        `<span class="submark">${active ? '●' : '○'}</span>` +
        `<span class="subname">${esc(s.name)}</span></div>`;
    }).join('');
}

subMenuEl.addEventListener('click', e => {
  const link = e.target.closest('.mi[data-url]');
  if (!link) return;
  hideSubMenu();
  if (e.altKey) {
    ClipboardSetText(link.dataset.url);
    toast('Link kopiert');
  } else {
    BrowserOpenURL(link.dataset.url);
  }
});

subMenuEl.addEventListener('click', async e => {
  const mi = e.target.closest('.mi[data-sub]');
  if (!mi) return;
  const id = mi.dataset.sub;
  hideSubMenu();
  try {
    await act(AzSetSubscription(id), 'Subscription gewechselt — Status wird neu geladen');
    refreshDeployStatus();
  } catch { /* toast zeigt den Fehler */ }
});
document.addEventListener('mousedown', e => {
  if (!subMenuEl.contains(e.target) && !e.target.closest('[data-act="azsub"]') && !e.target.closest('#tb-links')) hideSubMenu();
});
window.addEventListener('blur', hideSubMenu);

window.addEventListener('keydown', e => {
  if (e.key === 'Escape' && subMenuEl.style.display === 'block') { hideSubMenu(); return; }
  if (e.key === 'Escape' && modalEl.style.display === 'flex') { modalEl.style.display = 'none'; return; }
  if (e.key === 'Escape' && menuEl.style.display === 'block') { hideMenu(); return; }
  const projectMenu = document.querySelector('.project-more[open]');
  if (e.key === 'Escape' && projectMenu) { projectMenu.open = false; return; }
  if (!e.metaKey || e.ctrlKey || e.altKey) return;
  if (e.key >= '1' && e.key <= '9') {
    const name = sidebarSessions[parseInt(e.key) - 1];
    if (name) { e.preventDefault(); openSession(name); }
  } else if (e.key === '0') {
    e.preventDefault();
    showOverview();
  } else if (e.key.toLowerCase() === 'w') {
    e.preventDefault();
    if (e.shiftKey) {
      if (activeTerm) parkSession(activeTerm);
    } else if (view !== 'overview') {
      showOverview();
    }
  } else if (e.key.toLowerCase() === 't') {
    e.preventDefault();
    openTermInContext();
  } else if (e.key.toLowerCase() === 'g') {
    e.preventDefault();
    showGraph();
  } else if (e.key.toLowerCase() === 'b') {
    e.preventDefault();
    showBoard();
  } else if (e.shiftKey && e.key.toLowerCase() === 's') {
    e.preventDefault();
    showStats();
  }
}, true);

function dockContextProject() {
  if (activeTerm) {
    const a = agentInfo(activeTerm);
    if (a?.project && a.project !== '(ohne Projekt)') return a.project;
  }
  if (hydraProject) return hydraProject;
  if (view === 'graph' && graphProject) return graphProject;
  if (view === 'board' && boardProject) return boardProject;
  return projectNames()[0] || '';
}

mountDock({
  attach: (name, cols, rows) => OpenTerm(name, cols, rows),
  write: (name, b64) => WriteTerm(name, b64),
  resize: (name, cols, rows) => ResizeTerm(name, cols, rows),
  close: name => {
    // Dock-Terminals stehen in keiner Liste — bliebe die tmux-Session offen,
    // liefe sie unsichtbar weiter und wäre nur noch über tmux erreichbar.
    CloseTerm(name);
    KillSession(name).catch(() => {});
  },
  onData: (name, cb) => {
    const ev = 'term:data:' + name;
    EventsOn(ev, cb);
    return () => EventsOff(ev);
  },
  onClosed: (name, cb) => {
    const ev = 'term:closed:' + name;
    EventsOn(ev, cb);
    return () => EventsOff(ev);
  },
  newTerminal: async () => {
    const proj = dockContextProject();
    if (!proj) { toast('Kein Projekt registriert', true); return null; }
    try {
      return await act(NewDockSession(proj), n => `Terminal „${n}" im Dock geöffnet`);
    } catch { return null; }
  },
  status: name => {
    const a = agentInfo(name);
    if (!a) return null;
    const v = agentVisual(a, a.project);
    return { color: v.color, label: v.label };
  },
  setClipboard: text => ClipboardSetText(text),
});

let breakBusy = false;
let breakStart = 0;

function breakDur(secs) {
  const s = Math.max(0, Math.round(secs || 0));
  if (s < 60) return `${s} s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  const rest = m % 60;
  return rest ? `${h} h ${rest}` : `${h} h`;
}

// Die Restzeit gehört dorthin, wo man von sich aus nachschaut — der Indikator
// unten rechts taucht ja erst auf, wenn es schon so weit ist.
function renderBreakNav(a) {
  const el = $('nav-break-left');
  if (!el) return;
  if (!a || a.enabled === false) { el.textContent = ''; el.className = 'nav-side'; return; }
  if (a.level === 'resting') { el.textContent = 'läuft'; el.className = 'nav-side good'; return; }
  const next = Math.round(a.nextDueSecs || 0);
  if (next > 0) {
    el.textContent = (a.snoozed ? '↓ ' : '') + breakDur(next);
    el.className = 'nav-side' + (a.level === 'hint' ? ' warn' : '');
    return;
  }
  el.textContent = 'fällig';
  el.className = 'nav-side' + (a.level === 'overdue' ? ' crit' : ' warn');
}

async function refreshBreaks() {
  if (breakBusy) return;
  breakBusy = true;
  try {
    const a = await Breaks();
    updateBreaks(a);
    renderBreakNav(a);
  } catch { /* Backend noch nicht bereit */ }
  breakBusy = false;
}

BreakConfig().then(cfg => {
  mountBreaks({
    config: cfg,
    onTake: () => { breakStart = Date.now(); TakeBreak().catch(() => {}); },
    onEnd: () => {
      breakStart = 0;
      EndBreak().catch(() => {});
      refreshBreaks();
    },
    onFinished: () => { BreakOver().catch(() => {}); },
    onSnooze: () => { SnoozeBreak().catch(() => {}); refreshBreaks(); },
    onConfig: cfg2 => { SetBreakConfig(cfg2).catch(err => toast('Fehler: ' + err, true)); },
  });
  refreshBreaks();
}).catch(() => {});

// Der Herzschlag unterscheidet „sitzt am Rechner" von „ist weg" — ohne ihn
// liefe die Arbeitsuhr weiter, während die Agents allein rechnen.
let lastBeat = 0;
function beat() {
  const now = Date.now();
  if (now - lastBeat < 20000) return;
  lastBeat = now;
  BreakHeartbeat(true).catch(() => {});
}
for (const ev of ['mousemove', 'keydown', 'mousedown', 'wheel', 'focus']) {
  window.addEventListener(ev, beat, { passive: true });
}
document.addEventListener('visibilitychange', () => { if (!document.hidden) beat(); });

setInterval(() => { if (!document.hidden) refreshBreaks(); }, 5000);

function syncDockNav() {
  const nav = $('nav-dock');
  const expanded = isDockOpen();
  nav.classList.toggle('on', expanded);
  nav.setAttribute('aria-pressed', String(expanded));
}

$('nav-break').onclick = () => { if (!isBreakOpen()) openBreak(); };
$('nav-break-cfg').onclick = e => { e.stopPropagation(); openBreakSettings(); };
$('nav-dock').onclick = () => { toggleDock(); syncDockNav(); };
document.addEventListener('keyup', syncDockNav);
syncDockNav();

let refitPending = false;
new ResizeObserver(() => {
  if (refitPending) return;
  refitPending = true;
  requestAnimationFrame(() => { refitPending = false; refitTerms(); });
}).observe($('layout'));

refresh(true);
setInterval(() => { if (!document.hidden) refresh(false); }, 3000);
refreshZg();
setInterval(() => { if (!document.hidden) refreshZg(); }, 5000);
setInterval(() => {
  if (!zg?.active || document.hidden) return;
  const t = $('zg-time'), c = $('zg-cash');
  const sec = zgElapsedNow();
  if (t) t.textContent = zgDur(sec);
  if (c) c.textContent = zgEur(Math.round(sec / 3600 * zg.rate * 100) / 100);
}, 1000);
refreshDeployStatus();
let dsTick = 0;
setInterval(() => {
  dsTick++;
  if (document.hidden && Date.now() >= dsWatchUntil) return;
  if (Date.now() < dsWatchUntil || dsTick % 3 === 0) refreshDeployStatus();
}, 10000);
document.addEventListener('visibilitychange', () => {
  if (!document.hidden) {
    refresh(false);
    refreshZg();
    refreshDeployStatus();
  }
});
