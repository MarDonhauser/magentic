import './style.css';
import '@xterm/xterm/css/xterm.css';

import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import {
  Overview, Todos, Projects, AddTodo, UpdateTodo, DeleteTodo, StartTodoSession,
  NewSession, DoneAgent, Cleanup, Merge, Deploy, RemoveWorktree, SetMainBranch,
  OpenTerm, WriteTerm, ResizeTerm, KillSession, SendSkill,
  DeployStatus, AzLogin, ArgoLogin,
} from '../wailsjs/go/main/App';
import { EventsOn, EventsOff, BrowserOpenURL } from '../wailsjs/runtime/runtime';

const STATUS = {
  running: { color: 'var(--good)', icon: '●', label: 'läuft' },
  blocked: { color: 'var(--warning)', icon: '◆', label: 'wartet' },
  idle:    { color: 'var(--muted)', icon: '○', label: 'idle' },
  exited:  { color: 'var(--ink-2)', icon: '▪', label: 'beendet' },
  dead:    { color: 'var(--critical)', icon: '✗', label: 'tot' },
  unknown: { color: 'var(--muted)', icon: '?', label: '?' },
};

const $ = id => document.getElementById(id);
const sessionsEl = $('sessions'), sideTodosEl = $('side-todos'), usageBoxEl = $('usage-box');
const overviewEl = $('overview'), termsEl = $('terms');

let view = 'overview';
let activeTerm = null;
let ov = null;
let todos = [];
let projects = [];
let editingTodo = -1;
let confirmRemove = null;
let editingMain = null;
let sidebarSessions = [];

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
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
    toast('Fehler: ' + err, true);
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

const terms = new Map();

function makeTerm(name) {
  const wrap = document.createElement('div');
  wrap.className = 'term-wrap';
  termsEl.appendChild(wrap);
  const term = new Terminal({
    fontSize: 13,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    scrollback: 20000,
    cursorBlink: true,
    macOptionIsMeta: true,
    theme: { background: '#0d0d0d', foreground: '#e8e6df', cursor: '#e8e6df', selectionBackground: 'rgba(58,135,229,0.35)' },
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new WebLinksAddon((e, uri) => BrowserOpenURL(uri)));
  term.open(wrap);
  term.onData(d => WriteTerm(name, toB64(d)));
  term.onResize(({ cols, rows }) => ResizeTerm(name, cols, rows));
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
  const st = STATUS[a?.status] || STATUS.unknown;
  const gone = !a || ['exited', 'dead'].includes(a.status);
  termBarEl.innerHTML =
    `<button class="btn tiny" id="tb-back" title="Übersicht (⌘0)">‹ Übersicht</button>` +
    `<span class="dot" style="background:${st.color}"></span>` +
    `<span class="tb-name">${esc(activeTerm)}</span>` +
    `<span class="tb-st">${esc(st.label)}</span>` +
    (a?.project && a.project !== '(ohne Projekt)' ? `<span class="tb-proj">${esc(a.project)}</span>` : '') +
    `<span class="tb-actions">` +
    `<button class="btn tiny" id="tb-done"${gone ? ' disabled' : ''} title="/done in diese Session senden — committen und auf dev bringen">✓ done</button>` +
    `<button class="btn tiny" id="tb-deploy"${gone ? ' disabled' : ''} title="/deploy in diese Session senden">🚀 deploy</button>` +
    `<button class="btn tiny danger" id="tb-kill" title="Session beenden (⌘⇧W)">✗</button></span>`;
  $('tb-back').onclick = showOverview;
  $('tb-done').onclick = () =>
    act(DoneAgent(activeTerm), `/done an „${activeTerm}" gesendet — Plan in der Session bestätigen`).catch(() => {});
  $('tb-deploy').onclick = () =>
    act(SendSkill(activeTerm, '/deploy '), `/deploy an „${activeTerm}" gesendet — Plan in der Session bestätigen`).catch(() => {});
  $('tb-kill').onclick = e => {
    const b = e.currentTarget;
    if (b.dataset.confirm) { killSession(activeTerm); return; }
    b.dataset.confirm = '1';
    b.textContent = '✗ wirklich?';
    setTimeout(() => {
      if (b.isConnected) { delete b.dataset.confirm; b.textContent = '✗'; }
    }, 3000);
  };
}

async function openSession(name) {
  view = 'term';
  activeTerm = name;
  overviewEl.style.display = 'none';
  termsEl.style.display = 'block';
  let t = terms.get(name);
  const fresh = !t;
  if (!t) t = makeTerm(name);
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

function showOverview() {
  view = 'overview';
  activeTerm = null;
  termsEl.style.display = 'none';
  overviewEl.style.display = 'block';
  renderAll();
}

$('nav-overview').onclick = showOverview;
$('sidebar-head').onclick = showOverview;


function renderSidebar() {
  sessionsEl.innerHTML = '';
  sidebarSessions = [];
  let any = false;
  for (const p of ov?.projects || []) {
    const agents = [];
    for (const wt of p.worktrees || []) {
      for (const a of wt.agents || []) {
        if (a.status !== 'dead') agents.push(a);
      }
    }
    if (!agents.length && !p.path) continue;
    any = true;
    const head = document.createElement('div');
    head.className = 'proj-head';
    const label = document.createElement('span');
    label.textContent = p.name;
    head.appendChild(label);
    if (p.path) {
      const plus = document.createElement('button');
      plus.className = 'proj-add';
      plus.textContent = '+';
      plus.title = 'Neue Claude-Session in ' + p.name + ' (⌥-Klick: in frischem Worktree)';
      plus.onclick = async e => {
        e.stopPropagation();
        plus.disabled = true;
        try {
          const worktree = e.altKey;
          const name = await act(NewSession(p.name, worktree, ''),
            n => (worktree ? `Worktree-Session „${n}" gestartet` : `Session „${n}" gestartet`));
          if (name) openSession(name);
        } catch { /* toast zeigt den Fehler */ }
      };
      head.appendChild(plus);
    }
    sessionsEl.appendChild(head);
    for (const a of agents) {
      const st = STATUS[a.status] || STATUS.unknown;
      const idx = sidebarSessions.length;
      sidebarSessions.push(a.name);
      const div = document.createElement('div');
      div.className = 'session' + (view === 'term' && a.name === activeTerm ? ' selected' : '');
      const key = idx < 9 ? `<span class="skey">⌘${idx + 1}</span>` : '';
      div.innerHTML =
        `<span class="dot" style="background:${st.color}"></span>` +
        `<span class="sname">${esc(a.name)}</span>` +
        `<span class="sstatus">${esc(st.label)}</span>` +
        `<span class="sage">${esc(a.age)}</span>${key}`;
      div.onclick = () => openSession(a.name);
      div.oncontextmenu = e => {
        e.preventDefault();
        showMenu(e.clientX, e.clientY, a.name, a.status);
      };
      sessionsEl.appendChild(div);
    }
  }
  if (!any) {
    sessionsEl.innerHTML = '<div class="none">Keine aktiven Sessions</div>';
  }

  sideTodosEl.innerHTML = '';
  for (const t of todos.slice(0, 6)) {
    const div = document.createElement('div');
    div.className = 'side-todo';
    div.title = t.text;
    div.innerHTML = `<span class="tmark">☐</span><span class="ttext">${esc(t.text)}</span>`;
    div.onclick = showOverview;
    sideTodosEl.appendChild(div);
  }
  if (!todos.length) {
    sideTodosEl.innerHTML = '<div class="none">keine</div>';
  }

  const u = ov?.usage;
  usageBoxEl.innerHTML = u ? usageBar('5h', u.fiveHour, '↻ ' + u.fiveHourReset) + usageBar('7d', u.sevenDay, '↻ ' + u.sevenDayReset) : '';
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

function tile(value, label, dotColor, hollow) {
  const dot = dotColor ? `<span class="dot${hollow ? ' hollow' : ''}" style="background:${dotColor}"></span>` : '';
  return `<div class="tile"><div class="val">${value}</div><div class="lbl">${dot}${esc(label)}</div></div>`;
}

function agentPill(a) {
  const st = STATUS[a.status] || STATUS.unknown;
  const done = (a.status === 'idle' || a.status === 'running')
    ? `<button class="btn tiny" data-act="done" data-agent="${esc(a.name)}" title="/done — Arbeit committen und auf dev bringen">✓ done</button>`
    : '';
  const open = a.status !== 'dead'
    ? `<button class="btn tiny" data-act="open" data-agent="${esc(a.name)}" title="Terminal öffnen">⌨</button>`
    : '';
  return `<span class="pill"><span class="dot" style="background:${st.color}"></span>` +
    `<span class="name">${esc(a.name)}</span>` +
    `<span class="st">${st.icon} ${esc(st.label)}</span>` +
    `<span class="age">${esc(a.age)}</span>${open}${done}</span>`;
}

function gitState(wt) {
  if (wt.branch === '(kein git)') return '';
  if (wt.clean) return `<span class="git-state clean">✓ sauber</span>`;
  const parts = [];
  if (wt.staged) parts.push(`${wt.staged} staged`);
  if (wt.modified) parts.push(`${wt.modified} geändert`);
  if (wt.untracked) parts.push(`${wt.untracked} neu`);
  return `<span class="git-state"><span style="color:var(--warning);font-weight:700">±</span> ${parts.join(' · ')}</span>`;
}

function worktreeActions(p, wt) {
  if (!p.path) return '';
  const busy = (wt.agents || []).some(a => ['running', 'blocked'].includes(a.status));
  const anySession = (wt.agents || []).some(a => ['running', 'blocked', 'idle'].includes(a.status));
  let btns = '';
  if (!busy && wt.ahead > 0 && wt.branch !== p.mainBranch) {
    btns += `<button class="btn" data-act="merge" data-project="${esc(p.name)}" data-source="${esc(wt.branch)}" data-target="${esc(p.mainBranch)}" ` +
      `title="Claude-Session, die diesen Branch merged">🔀 ${esc(wt.branch)} → ${esc(p.mainBranch)}</button>`;
  }
  if (!wt.isMain && !anySession) {
    if (!wt.clean || wt.ahead > 0) {
      btns += `<button class="btn" data-act="cleanup" data-path="${esc(wt.path)}" data-main="${esc(p.mainBranch)}" title="Claude-Session zum Committen und Mergen">✨ Cleanup</button>`;
    }
    if (wt.clean) {
      const key = p.name + '|' + wt.path;
      btns += confirmRemove === key
        ? `<button class="btn danger confirm" data-act="remove2" data-project="${esc(p.name)}" data-path="${esc(wt.path)}">wirklich entfernen?</button>`
        : `<button class="btn danger" data-act="remove1" data-project="${esc(p.name)}" data-path="${esc(wt.path)}">⌫ entfernen</button>`;
    } else {
      btns += `<button class="btn danger" disabled title="uncommittete Änderungen — erst aufräumen">⌫ entfernen</button>`;
    }
  }
  return btns ? `<span class="actions">${btns}</span>` : '';
}

function worktreeRow(p, wt, idx, total) {
  const cls = ['row', wt.isMain ? 'main-row' : 'wt-row'];
  const ab = [];
  if (wt.ahead) ab.push(`↑${wt.ahead}`);
  if (wt.behind) ab.push(`↓${wt.behind}`);
  let abHtml = ab.length ? `<span class="ab" title="gegenüber ${esc(p.mainBranch)}">${ab.join(' ')}</span>` : '';
  if (!wt.ahead && wt.branch !== p.mainBranch && wt.branch !== '(kein git)' && wt.branch !== '—' && p.path) {
    abHtml += `<span class="git-state" style="color:var(--good)" title="alle Commits sind in ${esc(p.mainBranch)}">✓ in ${esc(p.mainBranch)}</span>`;
  }
  const agents = (wt.agents || []).map(agentPill).join('');
  const warns = (wt.warnings || []).map(w => `<span class="warn"><span class="ic">⚠</span>${esc(w)}</span>`).join('');
  const pathHtml = wt.isMain ? '' : `<span class="wt-path" title="${esc(wt.path)}">${esc(wt.shortPath)}</span>`;
  const last = wt.lastMsg ? `<span class="lastmsg" title="letzter Commit">„${esc(wt.lastMsg)}“</span>` : '';
  return `<div class="${cls.join(' ')}">` +
    `<span class="branch">${esc(wt.branch)}</span>${abHtml}${gitState(wt)}${agents}${warns}${pathHtml}${last}${worktreeActions(p, wt)}</div>`;
}

function projectCard(p) {
  const rows = (p.worktrees || []).map((wt, i) => worktreeRow(p, wt, i, p.worktrees.length)).join('');
  let mainCfg = '';
  if (p.path) {
    mainCfg = editingMain === p.name
      ? `<span class="maincfg"><input class="inline-input" id="main-input" value="${esc(p.mainBranch)}" placeholder="main">` +
        `<button class="btn tiny" data-act="mainsave" data-project="${esc(p.name)}">✓</button>` +
        `<button class="btn tiny" data-act="maincancel">✗</button></span>`
      : `<span class="maincfg">Hauptbranch <b>${esc(p.mainBranch)}</b></span>` +
        `<button class="btn tiny" data-act="mainedit" data-project="${esc(p.name)}" title="Hauptbranch ändern">✎</button>`;
    mainCfg += `<span class="actions">` +
      `<button class="btn" data-act="newsession" data-project="${esc(p.name)}" title="Neue Claude-Session im Projekt">+ Session</button>` +
      `<button class="btn" data-act="newworktree" data-project="${esc(p.name)}" title="Neue Session in eigenem Worktree">⑂ Worktree</button>` +
      `<button class="btn" data-act="deploy" data-project="${esc(p.name)}" title="Neue Claude-Session, die /deploy ausführt">🚀 deploy</button></span>`;
  }
  return `<div class="card"><div class="card-head"><h2>${esc(p.name)}</h2>` +
    `<span class="path">${esc(p.path || '')}</span>${mainCfg}</div><div class="rows">${rows}</div></div>`;
}

function todoSection() {
  let rows = todos.map(t => {
    if (editingTodo === t.index) {
      const opts = ['<option value="">— Projekt —</option>']
        .concat(projects.map(p => `<option value="${esc(p)}"${p === t.project ? ' selected' : ''}>${esc(p)}</option>`)).join('');
      return `<div class="todo-row editing">` +
        `<input class="inline-input wide" id="todo-edit-text" value="${esc(t.text)}">` +
        `<select class="inline-input" id="todo-edit-proj">${opts}</select>` +
        `<button class="btn tiny" data-act="todosave" data-idx="${t.index}">✓ speichern</button>` +
        `<button class="btn tiny" data-act="todocancel">✗</button></div>`;
    }
    return `<div class="todo-row">` +
      `<span class="tmark">☐</span>` +
      `<span class="ttext">${esc(t.text)}</span>` +
      (t.project ? `<span class="tproj">[${esc(t.project)}]</span>` : '<span class="tproj dim">ohne Projekt</span>') +
      `<span class="tage">${esc(t.age)}</span>` +
      `<span class="actions">` +
      `<button class="btn tiny" data-act="todostart" data-idx="${t.index}" title="Session starten — Text landet im Eingabefeld">▶ Session</button>` +
      `<button class="btn tiny" data-act="todoedit" data-idx="${t.index}">✎</button>` +
      `<button class="btn tiny danger" data-act="tododelete" data-idx="${t.index}">⌫</button></span></div>`;
  }).join('');
  if (!todos.length) rows = '<div class="none" style="padding:8px 2px">Keine Todos — unten eins anlegen</div>';
  const opts = ['<option value="">— Projekt —</option>'].concat(projects.map(p => `<option value="${esc(p)}">${esc(p)}</option>`)).join('');
  return `<div class="card"><div class="card-head"><h2>Todos</h2></div>${rows}` +
    `<div class="todo-add"><input class="inline-input wide" id="todo-new" placeholder="Neues Todo…">` +
    `<select class="inline-input" id="todo-new-proj">${opts}</select>` +
    `<button class="btn" data-act="todoadd">+ hinzufügen</button></div></div>`;
}

let deployStatus = null;
let deployStamp = '';
let argoExpanded = false;

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

function deployCard() {
  const ds = deployStatus;
  if (!ds) {
    return `<div class="card"><div class="card-head"><h2>🚀 Pipelines &amp; Argo</h2>` +
      `<span class="path">lade…</span></div></div>`;
  }
  const azChip = ds.azOk
    ? `<span class="ds-chip ok">Azure ✓</span>`
    : `<span class="ds-chip bad" title="${esc(ds.azErr)}">Azure ✗</span>` +
      `<button class="btn tiny" data-act="azlogin">az login</button>`;
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
  return `<div class="card"><div class="card-head"><h2>🚀 Pipelines &amp; Argo</h2>` +
    `${azChip}${argoChip}` +
    `<span class="actions"><span class="path">${esc(deployStamp)}</span>` +
    `<button class="btn tiny" data-act="dsrefresh" title="Status neu laden">↻</button></span></div>` +
    `<div class="ds-cols"><div class="ds-col"><div class="ds-title">Azure DevOps Builds</div>${builds}</div>` +
    `<div class="ds-col"><div class="ds-title">ArgoCD</div>${argoHtml}</div></div></div>`;
}

let dsLoading = false;
async function refreshDeployStatus() {
  if (dsLoading) return;
  dsLoading = true;
  try {
    deployStatus = await DeployStatus();
    deployStamp = 'Stand ' + new Date().toLocaleTimeString('de-DE');
    if (view === 'overview') renderOverview();
  } catch (e) { /* Backend nicht bereit */ }
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

function renderOverview() {
  if (!ov) { overviewEl.innerHTML = '<div class="none" style="padding:30px">lade…</div>'; return; }
  const ae = document.activeElement;
  if (ae && overviewEl.contains(ae) && ['INPUT', 'SELECT', 'TEXTAREA'].includes(ae.tagName)) {
    return;
  }
  const savedText = $('todo-new')?.value ?? '';
  const savedProj = $('todo-new-proj')?.value ?? '';
  const c = ov.counts || {};
  const u = ov.usage;
  const tiles =
    tile(c.running || 0, 'läuft', 'var(--good)') +
    tile(c.blocked || 0, 'wartet auf Input', 'var(--warning)') +
    tile(c.idle || 0, 'idle', 'var(--muted)', true) +
    tile(c.dirty || 0, 'Worktrees mit Änderungen', 'var(--warning)') +
    tile(c.warnings || 0, 'Warnungen', 'var(--serious)') +
    (u ? tile(`${Math.round(u.fiveHour)}%`, `5h-Limit · ↻ ${esc(u.fiveHourReset)}`, usageColor(u.fiveHour)) +
         tile(`${Math.round(u.sevenDay)}%`, `Wochenlimit · ↻ ${esc(u.sevenDayReset)}`, usageColor(u.sevenDay)) : '');
  const cards = (ov.projects || []).map(projectCard).join('');
  overviewEl.innerHTML = `<div class="tiles">${tiles}</div>${deployCard()}${todoSection()}${cards}` +
    `<div class="stamp">Stand ${esc(ov.generatedAt || '')}</div>`;
  if (savedText) $('todo-new').value = savedText;
  if (savedProj) $('todo-new-proj').value = savedProj;
}

function renderAll() {
  renderSidebar();
  if (view === 'overview') renderOverview();
  if (view === 'term') updateTermBar();
}

overviewEl.addEventListener('click', async e => {
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
      case 'open': await openSession(d.agent); break;
      case 'done': await act(DoneAgent(d.agent), `/done an „${d.agent}" gesendet — Plan in der Session bestätigen`); break;
      case 'cleanup': await act(Cleanup(d.path, d.main), n => `Cleanup-Agent „${n}" gestartet`); break;
      case 'merge': await act(Merge(d.project, d.source, d.target), n => `Merge-Agent „${n}" gestartet (${d.source} → ${d.target})`); break;
      case 'deploy': await act(Deploy(d.project), n => `Deploy-Agent „${n}" gestartet (/deploy)`); break;
      case 'newsession': await act(NewSession(d.project, false, ''), n => `Session „${n}" gestartet`); break;
      case 'newworktree': await act(NewSession(d.project, true, ''), n => `Worktree-Session „${n}" gestartet`); break;
      case 'remove1': confirmRemove = d.project + '|' + d.path; renderOverview(); break;
      case 'remove2':
        confirmRemove = null;
        await act(RemoveWorktree(d.project, d.path), 'Worktree entfernt');
        break;
      case 'mainedit': editingMain = d.project; renderOverview(); $('main-input')?.focus(); break;
      case 'maincancel': editingMain = null; renderOverview(); break;
      case 'mainsave': {
        const v = $('main-input').value.trim();
        editingMain = null;
        await act(SetMainBranch(d.project, v), v ? `Hauptbranch: ${v}` : 'Hauptbranch: automatisch');
        break;
      }
      case 'todoadd': {
        const text = $('todo-new').value.trim();
        const proj = $('todo-new-proj').value;
        if (!text) break;
        $('todo-new').value = '';
        $('todo-new-proj').value = '';
        await act(AddTodo(text, proj), 'Todo angelegt');
        break;
      }
      case 'todoedit': editingTodo = parseInt(d.idx); renderOverview(); $('todo-edit-text')?.focus(); break;
      case 'todocancel': editingTodo = -1; renderOverview(); break;
      case 'todosave': {
        const text = $('todo-edit-text').value.trim();
        const proj = $('todo-edit-proj').value;
        editingTodo = -1;
        await act(UpdateTodo(parseInt(d.idx), text, proj), 'Todo gespeichert');
        break;
      }
      case 'tododelete': await act(DeleteTodo(parseInt(d.idx)), 'Todo gelöscht'); break;
      case 'dsrefresh': refreshDeployStatus(); break;
      case 'azlogin': AzLogin(); toast('Browser öffnet sich für den Azure-Login…'); break;
      case 'argologin': ArgoLogin(); toast('Browser öffnet sich für den Argo-SSO-Login…'); break;
      case 'todostart': {
        const name = await act(StartTodoSession(parseInt(d.idx)), n => `Todo → Session „${n}" — Text steht im Eingabefeld, Enter schickt ihn ab`);
        if (name) setTimeout(() => openSession(name), 400);
        break;
      }
    }
  } catch { /* toast zeigt den Fehler */ }
  b.disabled = false;
});

overviewEl.addEventListener('keydown', e => {
  if (e.key === 'Enter' && e.target.id === 'todo-new') {
    overviewEl.querySelector('button[data-act="todoadd"]')?.click();
  }
});

let refreshing = false;
async function refresh(force) {
  if (refreshing && !force) return;
  refreshing = true;
  try {
    const [o, t, p] = await Promise.all([Overview(), Todos(), Projects()]);
    ov = o; todos = t || []; projects = p || [];
    if (editingTodo < 0 && editingMain === null || force) renderAll();
    else renderSidebar();
  } catch (e) { /* Backend noch nicht bereit */ }
  refreshing = false;
}

window.addEventListener('resize', () => {
  const t = activeTerm && terms.get(activeTerm);
  if (t) t.fit.fit();
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
  const done = ['idle', 'running'].includes(status)
    ? `<div class="mi" data-mi="done">✓ /done senden</div>` : '';
  menuEl.innerHTML =
    `<div class="mi-head">${esc(name)}</div>` +
    `<div class="mi" data-mi="open">⌨ Terminal öffnen</div>` + done +
    `<div class="mi danger" data-mi="kill">✗ Session beenden</div>`;
  menuEl.style.display = 'block';
  menuEl.style.left = Math.min(x, window.innerWidth - 200) + 'px';
  menuEl.style.top = Math.min(y, window.innerHeight - menuEl.offsetHeight - 10) + 'px';
}

async function killSession(name) {
  try {
    await act(KillSession(name), `Session „${name}" beendet`);
  } catch { return; }
  const t = terms.get(name);
  if (t) {
    EventsOff('term:data:' + name);
    EventsOff('term:closed:' + name);
    try { t.term.dispose(); } catch { /* schon weg */ }
    t.wrap.remove();
    terms.delete(name);
  }
  if (activeTerm === name) showOverview();
}

menuEl.addEventListener('click', async e => {
  const mi = e.target.closest('.mi');
  if (!mi || !menuFor) return;
  const name = menuFor;
  switch (mi.dataset.mi) {
    case 'open': hideMenu(); openSession(name); break;
    case 'done':
      hideMenu();
      try { await act(DoneAgent(name), `/done an „${name}" gesendet — Plan in der Session bestätigen`); } catch { }
      break;
    case 'kill':
      if (mi.dataset.confirm) { hideMenu(); killSession(name); }
      else { mi.dataset.confirm = '1'; mi.textContent = '✗ wirklich beenden?'; }
      break;
  }
});
document.addEventListener('mousedown', e => { if (!menuEl.contains(e.target)) hideMenu(); });
window.addEventListener('blur', hideMenu);

window.addEventListener('keydown', e => {
  if (e.key === 'Escape' && menuEl.style.display === 'block') { hideMenu(); return; }
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
      if (activeTerm) killSession(activeTerm);
    } else if (view === 'term') {
      showOverview();
    }
  }
}, true);

refresh(true);
setInterval(() => refresh(false), 3000);
refreshDeployStatus();
setInterval(refreshDeployStatus, 30000);
