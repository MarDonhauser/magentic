import './gitgraph.css';

const ROW_H = 30;
const LANE_W = 16;
const LANE_PAD = 13;
const MAX_LANES = 12;
const DOT_R = 4;
const DOT_R_REF = 5.5;
const MERGE_R = 5;

const PALETTE = ['#c678dd', '#e0b25e', '#5eb7e8', '#98c379', '#e2915f', '#7aa2f7', '#e06c75'];

const ICONS = {
  branch: '<line x1="6" x2="6" y1="3" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>',
  merge: '<circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M6 21V9a9 9 0 0 0 9 9"/>',
  tag: '<path d="M12.586 2.586A2 2 0 0 0 11.172 2H4a2 2 0 0 0-2 2v7.172a2 2 0 0 0 .586 1.414l8.704 8.704a2.426 2.426 0 0 0 3.42 0l6.58-6.58a2.426 2.426 0 0 0 0-3.42z"/><circle cx="7.5" cy="7.5" r="1.5"/>',
  folder: '<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>',
  cloud: '<path d="M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z"/>',
  user: '<path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>',
  warn: '<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3Z"/><path d="M12 9v4"/><path d="M12 17h.01"/>',
};

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function ico(name) {
  return `<svg class="gg-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICONS[name] || ''}</svg>`;
}

function clampLane(v) {
  const n = Math.round(Number(v));
  if (!Number.isFinite(n) || n < 0) return 0;
  return Math.min(n, MAX_LANES - 1);
}

function laneX(lane) {
  return LANE_PAD + lane * LANE_W;
}

function rowY(i) {
  return i * ROW_H + ROW_H / 2;
}

function laneColor(lane, mainLane) {
  if (lane === mainLane) return 'var(--accent)';
  const i = lane > mainLane ? lane - 1 : lane;
  return PALETTE[i % PALETTE.length];
}

function baseName(path) {
  const parts = String(path ?? '').split('/').filter(Boolean);
  return parts.length ? parts[parts.length - 1] : '';
}

function mainLaneOf(branches, commits, mainName) {
  const b = branches.find(x => x.isMain) || branches.find(x => x.name === mainName);
  if (b) return clampLane(b.lane);
  const c = commits.find(x => (x.refs || []).some(r => r && r.name === mainName));
  return c ? clampLane(c.lane) : 0;
}

function edgePath(x1, y1, x2, y2, turnAtTop) {
  if (x1 === x2) return `M${x1},${y1}L${x2},${y2}`;
  const bend = Math.min(ROW_H, Math.max(8, Math.abs(y2 - y1)));
  if (turnAtTop) {
    const ye = y1 + bend, ym = y1 + bend / 2;
    return `M${x1},${y1}C${x1},${ym} ${x2},${ym} ${x2},${ye}L${x2},${y2}`;
  }
  const ys = y2 - bend, ym = y2 - bend / 2;
  return `M${x1},${y1}L${x1},${ys}C${x1},${ym} ${x2},${ym} ${x2},${y2}`;
}

function agentChip(name, opts) {
  const svg = typeof opts.avatar === 'function' ? opts.avatar(name) : null;
  const inner = svg
    ? `<span class="gg-av">${svg}</span>`
    : ico('user');
  return `<span class="gg-agent${svg ? '' : ' is-text'}" title="Session ${esc(name)} läuft hier">${inner}<span class="gg-agent-name">${esc(name)}</span></span>`;
}

function refRank(r) {
  if (r.kind === 'head') return 0;
  if (r.kind === 'branch') return r.worktree ? 1 : 2;
  if (r.kind === 'tag') return 3;
  return 4;
}

function refChip(r, mainName, color, byName) {
  const name = esc(r.name);
  if (r.kind === 'head') return `<span class="gg-chip gg-headref" title="HEAD">HEAD</span>`;
  if (r.kind === 'tag') return `<span class="gg-chip gg-tag" title="Tag ${name}">${ico('tag')}<span class="gg-chip-label">${name}</span></span>`;
  if (r.kind === 'remote') return `<span class="gg-chip gg-remote" title="Remote-Ref ${name}">${ico('cloud')}<span class="gg-chip-label">${name}</span></span>`;
  const isMain = r.name === mainName;
  const worktree = r.worktree || (byName.get(r.name) || {}).worktree || '';
  const cls = ['gg-chip', 'gg-branch'];
  if (isMain) cls.push('is-main');
  if (worktree) cls.push('is-wt');
  const title = worktree
    ? `${r.name} — ausgecheckt in ${worktree}`
    : (isMain ? `Hauptbranch ${r.name}` : `Branch ${r.name}`);
  const lead = worktree ? ico('folder') : ico('branch');
  return `<span class="${cls.join(' ')}" style="--gg-c:${color}" data-branch="${name}" title="${esc(title)}">${lead}<span class="gg-chip-label">${name}</span></span>`;
}

function mergeSource(commit, parentLane, laneBranch) {
  const m = /Merge (?:remote-tracking )?branch '([^']+)'/.exec(String(commit.subject ?? ''));
  if (m) return m[1].replace(/^origin\//, '');
  const b = laneBranch.get(parentLane);
  return b ? b.name : '';
}

function legendHtml(branches, mainLane, mainName, opts) {
  const rows = branches.map(b => {
    const lane = clampLane(b.lane);
    const color = laneColor(lane, mainLane);
    const isMain = !!b.isMain || b.name === mainName;
    const ahead = Number(b.ahead) || 0;
    const behind = Number(b.behind) || 0;
    const ab = isMain
      ? ''
      : `<span class="gg-ab">${ahead ? `<span class="gg-ahead" title="${ahead} Commits vor ${esc(mainName)}">↑${ahead}</span>` : ''}${behind ? `<span class="gg-behind" title="${behind} Commits hinter ${esc(mainName)}">↓${behind}</span>` : ''}${!ahead && !behind ? '<span title="deckungsgleich mit ' + esc(mainName) + '">=</span>' : ''}</span>`;
    const merged = b.merged && !isMain ? `<span class="gg-badge gg-merged">merged</span>` : '';
    const wt = b.worktree
      ? `<span class="gg-wt-hint" title="Worktree ${esc(b.worktree)}">${ico('folder')}${esc(baseName(b.worktree))}</span>`
      : '';
    const agents = (b.agents || []).map(a => agentChip(a, opts)).join('');
    return `<div class="gg-leg-row${isMain ? ' is-main' : ''}" data-branch="${esc(b.name)}" data-lane="${lane}">`
      + `<span class="gg-leg-dot" style="--gg-c:${color}"></span>`
      + `<span class="gg-leg-name">${esc(b.name)}</span>`
      + (isMain ? '<span class="gg-badge">Hauptbranch</span>' : '')
      + merged
      + `<span class="gg-leg-spacer"></span>`
      + agents + wt + ab
      + `</div>`;
  });
  return `<div class="gg-legend">${rows.join('')}</div>`;
}

function graphHtml(commits, mainLane, mainName, laneBranch, byName, opts) {
  const index = new Map();
  commits.forEach((c, i) => { if (c.hash) index.set(c.hash, i); });

  const height = commits.length * ROW_H;
  const laneMax = commits.reduce((m, c) => Math.max(m, clampLane(c.lane)), 0);
  const width = laneX(laneMax) + LANE_PAD;

  const mergeEdges = [];
  const mainEdges = [];
  const dots = [];
  const rows = [];

  commits.forEach((c, i) => {
    const lane = clampLane(c.lane);
    const color = laneColor(lane, mainLane);
    const x = laneX(lane);
    const y = rowY(i);
    const parents = Array.isArray(c.parents) ? c.parents.filter(Boolean) : [];
    let inLane = -1;

    parents.forEach((ph, pi) => {
      const j = index.get(ph);
      if (j === undefined || j <= i) {
        mainEdges.push(`<path class="gg-edge gg-edge-open" data-lane="${lane}" style="stroke:${color};stroke-width:1.6" d="M${x},${y}L${x},${height}"/>`);
        return;
      }
      const plane = clampLane(commits[j].lane);
      const px = laneX(plane);
      const py = rowY(j);
      if (pi === 0) {
        mainEdges.push(`<path class="gg-edge" data-lane="${lane}" style="stroke:${color};stroke-width:2.1" d="${edgePath(x, y, px, py, false)}"/>`);
      } else {
        if (inLane < 0 && plane !== lane) inLane = plane;
        const pc = laneColor(plane, mainLane);
        mergeEdges.push(`<path class="gg-edge gg-edge-merge" data-lane="${plane}" style="stroke:${pc};stroke-width:1.6" d="${edgePath(x, y, px, py, true)}"/>`);
      }
    });

    const refs = (Array.isArray(c.refs) ? c.refs.filter(Boolean) : []).slice().sort((a, b) => refRank(a) - refRank(b));
    const hasBranch = refs.some(r => r.kind === 'branch' || r.kind === 'head');
    const agentSet = new Set((Array.isArray(c.agents) ? c.agents : []).filter(Boolean));
    refs.forEach(r => {
      if (r.kind !== 'branch') return;
      ((byName.get(r.name) || {}).agents || []).forEach(a => { if (a) agentSet.add(a); });
    });
    const agents = [...agentSet];
    const ringColor = c.merge && inLane >= 0 ? laneColor(inLane, mainLane) : color;

    let dot;
    if (c.merge) {
      dot = `<circle class="gg-dot" data-lane="${lane}" cx="${x}" cy="${y}" r="${MERGE_R}" style="fill:var(--page);stroke:${ringColor};stroke-width:2.2"/>`;
    } else {
      const r = hasBranch ? DOT_R_REF : DOT_R;
      dot = `<circle class="gg-dot" data-lane="${lane}" cx="${x}" cy="${y}" r="${r}" style="fill:${color};stroke:var(--page);stroke-width:${hasBranch ? 1.5 : 0}"/>`;
    }
    if (agents.length) {
      dots.push(`<circle class="gg-dot gg-halo" data-lane="${lane}" cx="${x}" cy="${y}" r="8.5" style="fill:none;stroke:${color};stroke-width:1.6"/>`);
    }
    dots.push(dot);

    const chips = [];
    refs.forEach(r => chips.push(refChip(r, mainName, color, byName)));
    if (c.merge && inLane >= 0) {
      const src = mergeSource(c, inLane, laneBranch);
      const toMain = lane === mainLane;
      const into = toMain ? ` → ${mainName}` : '';
      chips.push(`<span class="gg-chip gg-mergein${toMain ? ' to-main' : ''}" style="--gg-c:${laneColor(inLane, mainLane)}" title="${esc(src ? `${src} läuft hier wieder zusammen` : 'Merge')}">${ico('merge')}<span class="gg-chip-label">${esc((src || 'merge') + into)}</span></span>`);
    }
    agents.forEach(a => chips.push(agentChip(a, opts)));

    rows.push(`<div class="gg-row${c.merge ? ' is-merge' : ''}" data-lane="${lane}" data-hash="${esc(c.hash)}">`
      + (chips.length ? `<span class="gg-chips">${chips.join('')}</span>` : '')
      + `<span class="gg-msg" title="${esc(c.subject)}">${esc(c.subject)}</span>`
      + `<span class="gg-meta"><span class="gg-author" title="${esc(c.author)}">${esc(c.author)}</span>`
      + `<span class="gg-age">${esc(c.age)}</span>`
      + `<span class="gg-hash">${esc(c.short || String(c.hash ?? '').slice(0, 7))}</span></span>`
      + `</div>`);
  });

  return {
    width,
    html: `<div class="gg-graph">`
      + `<svg class="gg-svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" aria-hidden="true">`
      + mergeEdges.join('') + mainEdges.join('') + dots.join('')
      + `</svg>`
      + `<div class="gg-rows">${rows.join('')}</div>`
      + `</div>`,
  };
}

function wire(el, opts) {
  const call = (fn, arg) => { if (typeof fn === 'function' && arg) fn(arg); };

  el.addEventListener('click', e => {
    const chip = e.target.closest('[data-branch]');
    if (chip) { call(opts.onBranch, chip.dataset.branch); return; }
    const row = e.target.closest('.gg-row');
    if (row) call(opts.onCommit, row.dataset.hash);
  });

  const graph = el.querySelector('.gg-graph');
  if (!graph) return;
  const dim = lane => {
    if (lane === null) {
      graph.classList.remove('gg-dim');
      graph.querySelectorAll('.gg-on').forEach(n => n.classList.remove('gg-on'));
      return;
    }
    graph.querySelectorAll(`[data-lane="${lane}"]`).forEach(n => n.classList.add('gg-on'));
    graph.classList.add('gg-dim');
  };
  el.querySelectorAll('.gg-leg-row').forEach(row => {
    row.addEventListener('mouseenter', () => dim(row.dataset.lane));
    row.addEventListener('mouseleave', () => dim(null));
  });
}

export function renderGitGraph(el, graph, opts = {}) {
  if (!el) return;
  const g = graph || {};
  const commits = (Array.isArray(g.commits) ? g.commits : []).filter(Boolean);
  const branches = (Array.isArray(g.branches) ? g.branches : []).filter(Boolean);
  const mainName = g.main || 'main';
  const mainLane = mainLaneOf(branches, commits, mainName);

  const laneBranch = new Map();
  const byName = new Map();
  branches.forEach(b => {
    const lane = clampLane(b.lane);
    if (!laneBranch.has(lane) || (!laneBranch.get(lane).isMain && b.isMain)) laneBranch.set(lane, b);
    if (b.name) byName.set(b.name, b);
  });

  const laneCount = Math.max(1, Math.min(MAX_LANES, Number(g.lanes) || 0), commits.reduce((m, c) => Math.max(m, clampLane(c.lane) + 1), 0));

  el.classList.add('gg');
  el.innerHTML = '';

  const parts = [];
  parts.push(`<div class="gg-head">`
    + `<span class="gg-title">${esc(g.project || 'Git-Graph')}</span>`
    + `<span class="gg-chip gg-branch is-main" style="--gg-c:var(--accent)" data-branch="${esc(mainName)}">${ico('branch')}<span class="gg-chip-label">${esc(mainName)}</span></span>`
    + (commits.length ? `<span class="gg-head-meta">${commits.length} Commits · ${laneCount} ${laneCount === 1 ? 'Spur' : 'Spuren'}</span>` : '')
    + `</div>`);

  if (g.err) {
    parts.push(`<div class="gg-note gg-err">${ico('warn')}<span>Git-Graph konnte nicht gelesen werden.</span><span class="gg-err-msg">${esc(g.err)}</span></div>`);
  }
  if (branches.length) parts.push(legendHtml(branches, mainLane, mainName, opts));

  let width = 0;
  if (!commits.length) {
    if (!g.err) parts.push(`<div class="gg-note">keine Commits</div>`);
  } else {
    const built = graphHtml(commits, mainLane, mainName, laneBranch, byName, opts);
    width = built.width;
    parts.push(built.html);
    if (g.truncated) parts.push(`<div class="gg-more">ältere Commits ausgeblendet</div>`);
  }

  el.style.setProperty('--gg-w', `${width}px`);
  el.innerHTML = parts.join('');
  wire(el, opts);
}
