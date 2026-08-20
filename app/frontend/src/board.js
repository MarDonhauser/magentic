import './board.css';
import { developerIcon } from './avatar.js';

const COLUMNS = [
  { key: 'backlog', label: 'Geplant' },
  { key: 'active', label: 'In Arbeit' },
  { key: 'review', label: 'Zur Abnahme' },
  { key: 'done', label: 'Erledigt' },
];

const KIND_LABEL = { openspec: 'openspec', speckit: 'spec-kit', kiro: 'kiro', 'agent-os': 'agent-os' };
const MAX_VISIBLE_TASKS = 40;

const expanded = new Set();
const hiddenKinds = new Set();
const bound = new WeakMap();

function kindLabel(kind) {
  return KIND_LABEL[kind] || kind || '—';
}

const ICONS = {
  spec: '<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7z"/><path d="M14 2v5h5"/>',
  plan: '<path d="M9 3 3 6v15l6-3 6 3 6-3V3l-6 3z"/><path d="M9 3v15"/><path d="M15 6v15"/>',
  clock: '<circle cx="12" cy="12" r="9"/><polyline points="12 7 12 12 15 14"/>',
  branch: '<line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>',
  play: '<polygon points="6 3 20 12 6 21 6 3"/>',
  folder: '<path d="M4 20a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2z"/>',
  chevron: '<polyline points="9 6 15 12 9 18"/>',
  empty: '<path d="M3 7a2 2 0 0 1 2-2h4l2 3h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><line x1="9" y1="14" x2="15" y2="14"/>',
  warn: '<path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/>',
};

function icon(name) {
  return `<svg class="bd-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICONS[name] || ''}</svg>`;
}

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function relTime(iso) {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const secs = Math.round((Date.now() - t) / 1000);
  if (secs < 90) return 'gerade eben';
  const mins = Math.round(secs / 60);
  if (mins < 60) return `vor ${mins} Min.`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return hours === 1 ? 'vor 1 Stunde' : `vor ${hours} Stunden`;
  const days = Math.round(hours / 24);
  if (days < 7) return days === 1 ? 'vor 1 Tag' : `vor ${days} Tagen`;
  if (days < 31) {
    const weeks = Math.round(days / 7);
    return weeks === 1 ? 'vor 1 Woche' : `vor ${weeks} Wochen`;
  }
  if (days < 365) {
    const months = Math.round(days / 30.4);
    return months === 1 ? 'vor 1 Monat' : `vor ${months} Monaten`;
  }
  const years = Math.round(days / 365);
  return years === 1 ? 'vor 1 Jahr' : `vor ${years} Jahren`;
}

function humanize(id) {
  return String(id ?? '')
    .replace(/^\d+[-_]/, '')
    .replace(/[-_]+/g, ' ')
    .trim();
}

function pct(done, total) {
  if (!total) return 0;
  return Math.max(0, Math.min(100, Math.round((done / total) * 100)));
}

function list(v) {
  return Array.isArray(v) ? v : [];
}

function stamp(item) {
  const t = Date.parse(item?.updated || '');
  return Number.isNaN(t) ? 0 : t;
}

function sortItems(items) {
  return items.slice().sort((a, b) => {
    const al = list(a.agents).length ? 1 : 0;
    const bl = list(b.agents).length ? 1 : 0;
    if (al !== bl) return bl - al;
    return stamp(b) - stamp(a);
  });
}

function agentsHtml(item, opts) {
  const agents = list(item.agents).filter(Boolean);
  if (!agents.length) return '';
  const clickable = typeof opts.onOpenSession === 'function';
  const chips = agents.map(name => {
    const art = typeof opts.avatar === 'function' ? opts.avatar(name) : '';
    const face = art ? `<span class="bd-agent-face">${art}</span>` : '';
    const tag = clickable ? 'button' : 'span';
    const attrs = clickable ? ` type="button" data-act="agent" data-agent="${esc(name)}" title="Session ${esc(name)} öffnen"` : '';
    return `<${tag} class="bd-agent${face ? '' : ' bd-agent-plain'}"${attrs}>${face}<span class="bd-agent-name">${esc(name)}</span></${tag}>`;
  }).join('');
  return `<div class="bd-agents"><span class="bd-live-dot" aria-hidden="true"></span>${chips}</div>`;
}

function badgesHtml(item, multi) {
  const out = [];
  if (multi && item.kind) {
    out.push(`<span class="bd-badge bd-badge-kind" title="Quelle: ${esc(item.kind)}">${developerIcon('markdown')}${esc(kindLabel(item.kind))}</span>`);
  }
  const specs = Number(item.specs) || 0;
  if (specs > 0) out.push(`<span class="bd-badge" title="${specs} Spec-Datei(en) in diesem Change">${developerIcon('markdown')}${specs}</span>`);
  if (item.hasPlan) out.push(`<span class="bd-badge" title="${item.kind === 'speckit' ? 'plan.md vorhanden' : 'design.md vorhanden'}">${icon('plan')}Plan</span>`);
  const branches = list(item.branches).filter(Boolean);
  if (branches.length) {
    out.push(`<span class="bd-badge" title="${esc(branches.join('\n'))}">${developerIcon('git')}${branches.length}</span>`);
  }
  const age = relTime(item.updated);
  if (age) out.push(`<span class="bd-badge bd-badge-age" title="zuletzt geändert ${esc(item.updated)}">${icon('clock')}${esc(age)}</span>`);
  if (!out.length) return '';
  return `<div class="bd-badges">${out.join('')}</div>`;
}

function actionsHtml(item, opts) {
  const out = [];
  if (typeof opts.onStart === 'function') {
    out.push(`<button type="button" class="bd-act bd-act-start" data-act="start">${developerIcon('claude')}Arbeiten</button>`);
  }
  if (typeof opts.onReveal === 'function' && item.path) {
    out.push(`<button type="button" class="bd-act" data-act="reveal" title="Ordner im Finder zeigen">${icon('folder')}</button>`);
  }
  if (!out.length) return '';
  return `<div class="bd-actions">${out.join('')}</div>`;
}

function progressHtml(item) {
  const total = Number(item.total) || 0;
  const done = Math.min(Number(item.done) || 0, total);
  if (!total) {
    return `<div class="bd-progress bd-progress-none">ohne tasks.md</div>`;
  }
  const p = pct(done, total);
  return `<div class="bd-progress">
    <div class="bd-bar" aria-hidden="true"><i style="width:${p}%"></i></div>
    <span class="bd-progress-num">${done}/${total}<span class="bd-progress-pct">${p}%</span></span>
  </div>`;
}

function tasksHtml(item) {
  const tasks = list(item.tasks);
  if (!tasks.length) {
    return `<div class="bd-tasks-empty">Keine Tasks gefunden — dieser Change hat keine <code>tasks.md</code>.</div>`;
  }
  const shown = tasks.slice(0, MAX_VISIBLE_TASKS);
  const rest = tasks.length - shown.length;
  const groups = [];
  for (const t of shown) {
    const section = t?.section || '';
    const last = groups[groups.length - 1];
    if (!last || last.section !== section) groups.push({ section, rows: [t] });
    else last.rows.push(t);
  }
  const body = groups.map(g => {
    const head = g.section ? `<div class="bd-task-sec">${esc(g.section)}</div>` : '';
    const rows = g.rows.map(t => {
      const done = !!t?.done;
      return `<li class="bd-task${done ? ' is-done' : ''}"><span class="bd-task-box" aria-hidden="true">${done ? '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>' : ''}</span><span class="bd-task-text">${esc(t?.text)}</span></li>`;
    }).join('');
    return `${head}<ul class="bd-task-list">${rows}</ul>`;
  }).join('');
  const more = rest > 0 ? `<div class="bd-tasks-more">+ ${rest} weitere Task${rest === 1 ? '' : 's'} in <code>tasks.md</code></div>` : '';
  return body + more;
}

function cardHtml(item, opts, multi) {
  const id = String(item.id ?? '');
  const key = String(item.key ?? id);
  const open = expanded.has(key);
  const live = list(item.agents).filter(Boolean).length > 0;
  const title = item.title || humanize(id) || id;
  const summary = item.summary
    ? `<p class="bd-card-sum">${esc(item.summary)}</p>`
    : '';
  return `<article class="bd-card${live ? ' is-live' : ''}${open ? ' is-open' : ''}" data-key="${esc(key)}" tabindex="0" role="button" aria-expanded="${open}">
    <div class="bd-card-head">
      <span class="bd-caret" aria-hidden="true">${icon('chevron')}</span>
      <div class="bd-card-titles">
        <h3 class="bd-card-title">${esc(title)}</h3>
        <div class="bd-card-id">${esc(id)}</div>
      </div>
    </div>
    ${summary}
    ${agentsHtml(item, opts)}
    ${progressHtml(item)}
    <div class="bd-card-foot">${badgesHtml(item, multi)}${actionsHtml(item, opts)}</div>
    <div class="bd-tasks"${open ? '' : ' hidden'}>${tasksHtml(item)}</div>
  </article>`;
}

function columnHtml(col, items, opts, multi) {
  const cards = items.length
    ? items.map(it => cardHtml(it, opts, multi)).join('')
    : `<div class="bd-col-empty">nichts hier</div>`;
  return `<section class="bd-col" data-col="${col.key}">
    <header class="bd-col-head">
      <span class="bd-col-dot" aria-hidden="true"></span>
      <h2 class="bd-col-label">${esc(col.label)}</h2>
      <span class="bd-col-count">${items.length}</span>
    </header>
    <div class="bd-col-body">${cards}</div>
  </section>`;
}

function sourcesHtml(sources) {
  if (sources.length < 2) {
    const one = sources[0];
    return `<span class="bd-kind">${developerIcon('markdown')}${esc(kindLabel(one?.kind))}</span>` +
      (one?.root ? `<span class="bd-root" title="${esc(one.root)}">${esc(one.root)}</span>` : '');
  }
  const chips = sources.map(s => {
    const off = hiddenKinds.has(s.kind);
    return `<button type="button" class="bd-kind bd-kind-btn${off ? ' is-off' : ''}" data-act="kind" data-kind="${esc(s.kind)}"` +
      ` title="${esc(s.root)}\n\nKlick blendet diese Quelle ${off ? 'wieder ein' : 'aus'}">` +
      `${developerIcon('markdown')}${esc(kindLabel(s.kind))}<span class="bd-kind-n">${Number(s.items) || 0}</span></button>`;
  }).join('');
  return `<div class="bd-kinds">${chips}</div>`;
}

function headHtml(board, items, sources) {
  const total = items.reduce((n, it) => n + (Number(it.total) || 0), 0);
  const done = items.reduce((n, it) => n + Math.min(Number(it.done) || 0, Number(it.total) || 0), 0);
  const p = pct(done, total);
  const running = items.filter(it => list(it.agents).filter(Boolean).length).length;
  const liveStat = running
    ? `<div class="bd-stat bd-stat-live"><div class="bd-stat-val">${running}</div><div class="bd-stat-lbl">mit Agent</div></div>`
    : '';
  return `<header class="bd-head">
    <div class="bd-head-top">
      <h1 class="bd-project">${esc(board.project)}</h1>
      ${sourcesHtml(sources)}
    </div>
    <div class="bd-stats">
      <div class="bd-stat bd-stat-wide">
        <div class="bd-stat-val">${done}<span class="bd-stat-sub">/${total}</span><span class="bd-stat-pct">${p}%</span></div>
        <div class="bd-bar bd-bar-lg"><i style="width:${p}%"></i></div>
        <div class="bd-stat-lbl">Tasks erledigt</div>
      </div>
      <div class="bd-stat"><div class="bd-stat-val">${items.length}</div><div class="bd-stat-lbl">Changes offen</div></div>
      <div class="bd-stat"><div class="bd-stat-val">${Number(board.archived) || 0}</div><div class="bd-stat-lbl">archiviert</div></div>
      <div class="bd-stat"><div class="bd-stat-val">${Number(board.specs) || 0}</div><div class="bd-stat-lbl">Spec-Dateien</div></div>
      ${liveStat}
    </div>
  </header>`;
}

function noneHtml(board) {
  return `<div class="bd bd-none">
    <div class="bd-none-box">
      <span class="bd-none-ico">${developerIcon('markdown')}</span>
      <h2>Keine Spec-Ordner in ${esc(board.project)}</h2>
      <p>Keines der bekannten Spec-Layouts gefunden — deshalb gibt es hier nichts zu zeigen.</p>
      <p class="bd-none-hint">Lege einen dieser Ordner an, dann erscheinen die Changes automatisch als Karten. Mehrere Systeme nebeneinander sind kein Problem — das Board liest alle.</p>
      <div class="bd-none-paths"><code>openspec/changes/&lt;name&gt;/</code><code>specs/&lt;NNN-name&gt;/</code><code>.kiro/specs/&lt;name&gt;/</code><code>.agent-os/specs/&lt;name&gt;/</code></div>
    </div>
  </div>`;
}

function findItem(board, key) {
  return list(board?.items).find(it => String(it.key ?? it.id ?? '') === key) || null;
}

function toggle(card) {
  const key = card.dataset.key;
  const open = !expanded.has(key);
  if (open) expanded.add(key); else expanded.delete(key);
  card.classList.toggle('is-open', open);
  card.setAttribute('aria-expanded', String(open));
  const panel = card.querySelector('.bd-tasks');
  if (panel) panel.hidden = !open;
}

function bind(el) {
  if (bound.has(el)) return;
  bound.set(el, {});
  el.addEventListener('click', ev => {
    const ctx = bound.get(el) || {};
    const opts = ctx.opts || {};
    const act = ev.target.closest('[data-act]');
    if (act && el.contains(act)) {
      ev.stopPropagation();
      if (act.dataset.act === 'kind') {
        const kind = act.dataset.kind;
        if (hiddenKinds.has(kind)) hiddenKinds.delete(kind); else hiddenKinds.add(kind);
        renderBoard(el, ctx.board, opts);
        return;
      }
      const card = act.closest('.bd-card');
      const item = findItem(ctx.board, card?.dataset.key);
      if (act.dataset.act === 'agent') opts.onOpenSession?.(act.dataset.agent);
      else if (act.dataset.act === 'start' && item) opts.onStart?.(item);
      else if (act.dataset.act === 'reveal' && item) opts.onReveal?.(item.path);
      return;
    }
    const card = ev.target.closest('.bd-card');
    if (card && el.contains(card)) toggle(card);
  });
  el.addEventListener('keydown', ev => {
    if (ev.key !== 'Enter' && ev.key !== ' ') return;
    const card = ev.target.closest?.('.bd-card');
    if (!card || !el.contains(card) || ev.target.closest('[data-act]')) return;
    ev.preventDefault();
    toggle(card);
  });
}

export function renderBoard(el, board, opts = {}) {
  if (!el) return;
  const data = board || {};
  bind(el);
  bound.set(el, { board: data, opts });

  if (data.kind === 'none' && !list(data.items).length) {
    el.innerHTML = noneHtml(data);
    return;
  }

  const all = list(data.items);
  const known = new Set(all.map(it => String(it.key ?? it.id ?? '')));
  for (const key of [...expanded]) if (!known.has(key)) expanded.delete(key);

  const sources = list(data.sources).length
    ? list(data.sources)
    : [{ kind: data.kind, root: data.root, items: all.length }];
  const kinds = new Set(sources.map(s => s.kind));
  for (const k of [...hiddenKinds]) if (!kinds.has(k)) hiddenKinds.delete(k);

  const multi = sources.length > 1;
  const items = multi ? all.filter(it => !hiddenKinds.has(it.kind)) : all;

  const err = data.err
    ? `<div class="bd-err">${icon('warn')}<span>${esc(data.err)}</span></div>`
    : '';

  const cols = COLUMNS.map(col => {
    const inCol = sortItems(items.filter(it => (it.column || 'backlog') === col.key));
    return columnHtml(col, inCol, opts, multi);
  }).join('');

  el.innerHTML = `<div class="bd">${headHtml(data, items, sources)}${err}<div class="bd-cols">${cols}</div></div>`;
}
