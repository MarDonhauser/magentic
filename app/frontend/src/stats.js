import './stats.css';

const SERIES = ['#02ac9c', '#d97530', '#0f9fdc', '#c08a01', '#c863e4', '#71a547'];
const RAMP = ['#213d38', '#27544e', '#2b6d64', '#2e877c', '#2fa294', '#2fbdad', '#2bd9c6'];
const DIM = '#2e877c';
const FADED = '#2b6d64';
const ZERO_CELL = 'rgba(255,255,255,0.05)';
const WEEKDAYS = ['Mo', 'Di', 'Mi', 'Do', 'Fr', 'Sa', 'So'];
const PAD_L = 52;

const TOKEN_SERIES = [
  { key: 'input', label: 'Input', color: SERIES[0] },
  { key: 'output', label: 'Output', color: SERIES[1] },
  { key: 'cacheRead', label: 'Cache-Read', color: SERIES[2] },
  { key: 'cacheWrite', label: 'Cache-Write', color: SERIES[3] },
];

const nf0 = new Intl.NumberFormat('de-DE');
const nf1 = new Intl.NumberFormat('de-DE', { minimumFractionDigits: 1, maximumFractionDigits: 1 });
const nf2 = new Intl.NumberFormat('de-DE', { minimumFractionDigits: 2, maximumFractionDigits: 2 });

const ESCAPES = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
function esc(v) {
  return String(v == null ? '' : v).replace(/[&<>"']/g, (c) => ESCAPES[c]);
}

function num(v) {
  const n = typeof v === 'number' ? v : parseFloat(v);
  return Number.isFinite(n) ? n : 0;
}

function compact(v) {
  const n = num(v);
  const a = Math.abs(n);
  if (a < 1000) return nf0.format(Math.round(n));
  if (a < 1e6) return nf1.format(n / 1e3) + 'k';
  if (a < 1e9) return nf1.format(n / 1e6) + ' Mio';
  return nf1.format(n / 1e9) + ' Mrd';
}

function money(v) {
  const n = num(v);
  if (Math.abs(n) >= 10000) return '$' + nf0.format(Math.round(n));
  return '$' + nf2.format(n);
}

function pct(v, digits) {
  return (digits ? nf1 : nf0).format(num(v)) + ' %';
}

function shortDate(iso) {
  const p = String(iso || '').split('-');
  if (p.length !== 3) return String(iso || '');
  return `${+p[2]}.${+p[1]}.`;
}

function longDate(iso) {
  const p = String(iso || '').split('-');
  if (p.length !== 3) return String(iso || '');
  return `${p[2]}.${p[1]}.${p[0]}`;
}

function modelLabel(id) {
  const raw = String(id == null ? '' : id);
  const a = /^claude-(opus|sonnet|haiku)-(\d+)(?:-(\d+))?$/.exec(raw);
  if (a) return a[1][0].toUpperCase() + a[1].slice(1) + ' ' + a[2] + (a[3] ? '.' + a[3] : '');
  const b = /^claude-(\d+)-(\d+)-(opus|sonnet|haiku)(?:-\d+)?$/.exec(raw);
  if (b) return b[3][0].toUpperCase() + b[3].slice(1) + ' ' + b[1] + '.' + b[2];
  return raw || 'ohne Modell';
}

const FAMILY_RANK = { opus: 0, sonnet: 1, haiku: 2 };
function familyRank(id) {
  const m = /(opus|sonnet|haiku)/.exec(String(id || ''));
  return m ? FAMILY_RANK[m[1]] : 3;
}

function niceStep(max, count) {
  if (!(max > 0)) return 1;
  const raw = max / Math.max(1, count);
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const norm = raw / mag;
  const mult = norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 2.5 ? 2.5 : norm <= 5 ? 5 : 10;
  return mult * mag;
}

function axisScale(max, count) {
  const step = niceStep(max, count);
  const top = Math.max(step, Math.ceil(max / step) * step);
  const ticks = [];
  for (let v = 0; v <= top + step * 0.001; v += step) ticks.push(v);
  return { top, ticks };
}

function topRound(x, y, w, h, r) {
  const rr = Math.max(0, Math.min(r, w / 2, h));
  if (h <= 0 || w <= 0) return '';
  if (rr <= 0.4) return `M${x} ${y}h${w}v${h}h${-w}Z`;
  return `M${x} ${y + h}V${y + rr}a${rr} ${rr} 0 0 1 ${rr} ${-rr}h${w - 2 * rr}a${rr} ${rr} 0 0 1 ${rr} ${rr}V${y + h}Z`;
}

function rightRound(x, y, w, h, r) {
  const rr = Math.max(0, Math.min(r, h / 2, w));
  if (h <= 0 || w <= 0) return '';
  if (rr <= 0.4) return `M${x} ${y}h${w}v${h}h${-w}Z`;
  return `M${x} ${y}h${w - rr}a${rr} ${rr} 0 0 1 ${rr} ${rr}v${h - 2 * rr}a${rr} ${rr} 0 0 1 ${-rr} ${rr}H${x}Z`;
}

function tipRow(name, value, color) {
  const key = color ? `<i class="st-tip-k" style="background:${color}"></i>` : '<i class="st-tip-k"></i>';
  return `<div class="st-tip-row">${key}<span class="st-tip-n">${name}</span><b class="st-tip-v">${value}</b></div>`;
}

function normalize(raw) {
  const src = raw && typeof raw === 'object' ? raw : {};
  const days = (Array.isArray(src.days) ? src.days : []).filter(Boolean).map((d) => ({
    date: String(d.date || ''),
    weekday: String(d.weekday || ''),
    prompts: num(d.prompts),
    turns: num(d.turns),
    input: num(d.input),
    output: num(d.output),
    cacheRead: num(d.cacheRead),
    cacheWrite: num(d.cacheWrite),
    cost: num(d.cost),
    sessions: num(d.sessions),
    commits: num(d.commits),
  }));

  const heatmap = [];
  for (let r = 0; r < 7; r++) {
    const row = Array.isArray(src.heatmap) && Array.isArray(src.heatmap[r]) ? src.heatmap[r] : [];
    const out = [];
    for (let h = 0; h < 24; h++) out.push(num(row[h]));
    heatmap.push(out);
  }

  let hours = Array.isArray(src.hours) && src.hours.length ? src.hours.map(num) : null;
  if (!hours) hours = heatmap.reduce((acc, row) => acc.map((v, h) => v + row[h]), new Array(24).fill(0));
  while (hours.length < 24) hours.push(0);
  hours = hours.slice(0, 24);

  const projects = (Array.isArray(src.projects) ? src.projects : []).filter(Boolean).map((p) => ({
    name: String(p.name || ''),
    tokens: num(p.tokens),
    cost: num(p.cost),
    prompts: num(p.prompts),
    sessions: num(p.sessions),
    commits: num(p.commits),
    active: num(p.active),
  }));

  const models = (Array.isArray(src.models) ? src.models : []).filter(Boolean).map((m) => ({
    model: String(m.model || ''),
    turns: num(m.turns),
    input: num(m.input),
    output: num(m.output),
    cacheRead: num(m.cacheRead),
    cacheWrite: num(m.cacheWrite),
    cost: num(m.cost),
  }));

  const sum = (k) => days.reduce((a, d) => a + d[k], 0);
  const t = src.totals && typeof src.totals === 'object' ? src.totals : {};
  const input = t.input != null ? num(t.input) : sum('input');
  const output = t.output != null ? num(t.output) : sum('output');
  const cacheRead = t.cacheRead != null ? num(t.cacheRead) : sum('cacheRead');
  const cacheWrite = t.cacheWrite != null ? num(t.cacheWrite) : sum('cacheWrite');
  const totals = {
    days: t.days != null ? num(t.days) : days.length,
    prompts: t.prompts != null ? num(t.prompts) : sum('prompts'),
    turns: t.turns != null ? num(t.turns) : sum('turns'),
    sessions: t.sessions != null ? num(t.sessions) : sum('sessions'),
    input,
    output,
    cacheRead,
    cacheWrite,
    tokens: t.tokens != null ? num(t.tokens) : input + output + cacheRead + cacheWrite,
    cost: t.cost != null ? num(t.cost) : sum('cost'),
    commits: t.commits != null ? num(t.commits) : sum('commits'),
    cacheHit: t.cacheHit != null ? num(t.cacheHit) : (input + cacheRead > 0 ? (cacheRead / (input + cacheRead)) * 100 : 0),
    busiestDay: String(t.busiestDay || ''),
    streak: num(t.streak),
  };

  return {
    range: num(src.range) || days.length,
    err: src.err ? String(src.err) : '',
    days,
    heatmap,
    hours,
    projects,
    models,
    totals,
  };
}

function measure(host, min) {
  const w = host.getBoundingClientRect().width;
  return Math.max(min || 260, Math.round(w || min || 260));
}

function bindPlot(host, svg, tips) {
  host.innerHTML = svg;
  host._tips = tips;
}

function dateTicks(days, plotW) {
  const n = days.length;
  if (!n) return [];
  const want = Math.max(2, Math.min(n, Math.floor(plotW / 78)));
  const step = Math.max(1, Math.round(n / want));
  const out = [];
  for (let i = n - 1; i >= 0; i -= step) out.unshift(i);
  if (out.length > 1 && out[1] - out[0] < step * 0.6) out.shift();
  return out;
}

function drawActivity(host, days) {
  const W = measure(host, 320);
  const padL = PAD_L;
  const padR = 10;
  const padT = 20;
  const mainH = 118;
  const gapH = 26;
  const subH = 54;
  const axisH = 20;
  const H = padT + mainH + gapH + subH + axisH;
  const plotW = Math.max(40, W - padL - padR);
  const n = days.length;
  if (!n) return bindPlot(host, emptyPlot(W, H), []);

  const { top, ticks } = axisScale(Math.max(1, ...days.map((d) => d.prompts)), 3);
  const subTop = Math.max(1, ...days.map((d) => d.turns));
  const band = plotW / n;
  const cx = (i) => padL + band * (i + 0.5);
  const cy = (v) => padT + mainH - (v / top) * mainH;
  const subY0 = padT + mainH + gapH;
  const cys = (v) => subY0 + subH - (v / subTop) * subH;

  const area = (key, yb, fy) => {
    let p = `M${cx(0).toFixed(1)} ${yb.toFixed(1)}`;
    for (let i = 0; i < n; i++) p += `L${cx(i).toFixed(1)} ${fy(days[i][key]).toFixed(1)}`;
    return p + `L${cx(n - 1).toFixed(1)} ${yb.toFixed(1)}Z`;
  };
  const line = (key, fy) => days.map((d, i) => `${i ? 'L' : 'M'}${cx(i).toFixed(1)} ${fy(d[key]).toFixed(1)}`).join('');

  let s = `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" role="img">`;
  s += `<text class="st-ax-s" x="${padL}" y="6">Prompts von dir</text>`;
  for (const v of ticks) {
    const y = cy(v).toFixed(1);
    s += `<line class="st-gl" x1="${padL}" y1="${y}" x2="${W - padR}" y2="${y}"/>`;
    s += `<text class="st-ax" x="${padL - 8}" y="${y}" text-anchor="end" dominant-baseline="middle">${esc(compact(v))}</text>`;
  }
  s += `<rect class="st-band" x="0" y="${padT}" width="${band}" height="${mainH + gapH + subH}" rx="3"/>`;
  s += `<path d="${area('prompts', padT + mainH, cy)}" fill="${SERIES[0]}" fill-opacity="0.14"/>`;
  s += `<path d="${line('prompts', cy)}" fill="none" stroke="${SERIES[0]}" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`;
  s += `<line class="st-base" x1="${padL}" y1="${padT + mainH}" x2="${W - padR}" y2="${padT + mainH}"/>`;

  s += `<text class="st-ax-s" x="${padL}" y="${subY0 - 8}">Turns der Agents</text>`;

  s += `<text class="st-ax" x="${W - padR}" y="${subY0 - 8}" text-anchor="end">max ${esc(nf0.format(subTop))}</text>`;
  s += `<path d="${area('turns', subY0 + subH, cys)}" fill="${SERIES[1]}" fill-opacity="0.12"/>`;
  s += `<path d="${line('turns', cys)}" fill="none" stroke="${SERIES[1]}" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`;
  s += `<line class="st-gl" x1="${padL}" y1="${subY0 + subH}" x2="${W - padR}" y2="${subY0 + subH}"/>`;

  for (const i of dateTicks(days, plotW)) {
    s += `<text class="st-ax" x="${cx(i).toFixed(1)}" y="${subY0 + subH + 14}" text-anchor="middle">${esc(shortDate(days[i].date))}</text>`;
  }

  s += `<g class="st-cross"><line x1="0" y1="${padT}" x2="0" y2="${subY0 + subH}"/>` +
    `<circle r="4.5" fill="${SERIES[0]}" stroke="var(--surface)" stroke-width="2"/>` +
    `<circle r="4.5" fill="${SERIES[1]}" stroke="var(--surface)" stroke-width="2"/></g>`;

  const tips = [];
  for (let i = 0; i < n; i++) {
    const d = days[i];
    tips.push({
      html: `<div class="st-tip-t">${esc(d.weekday)}, ${esc(longDate(d.date))}</div>` +
        tipRow('Prompts', esc(nf0.format(d.prompts)), SERIES[0]) +
        tipRow('Turns', esc(nf0.format(d.turns)), SERIES[1]) +
        `<div class="st-tip-sep"></div>` +
        tipRow('Sessions', esc(nf0.format(d.sessions))) +
        tipRow('Commits', esc(nf0.format(d.commits))),
      band: { x: padL + band * i, y: padT, w: band, h: mainH + gapH + subH },
      cross: { x: cx(i), pts: [cy(d.prompts), cys(d.turns)] },
    });
    s += `<rect class="st-hit" data-t="${i}" x="${padL + band * i}" y="${padT}" width="${band}" height="${mainH + gapH + subH}"/>`;
  }
  bindPlot(host, s + '</svg>', tips);
}

function drawTokens(host, days, mode) {
  const W = measure(host, 320);
  const padL = PAD_L;
  const padR = 10;
  const padT = 12;
  const plotH = 168;
  const axisH = 20;
  const H = padT + plotH + axisH;
  const plotW = Math.max(40, W - padL - padR);
  const n = days.length;
  if (!n) return bindPlot(host, emptyPlot(W, H), []);

  const keys = mode === 'nocache'
    ? TOKEN_SERIES.filter((s) => s.key !== 'cacheRead')
    : TOKEN_SERIES;
  const share = mode === 'share';
  const dayTotal = (d) => keys.reduce((a, s) => a + d[s.key], 0);
  const max = share ? 1 : Math.max(1, ...days.map(dayTotal));
  const { top, ticks } = share
    ? { top: 1, ticks: [0, 0.25, 0.5, 0.75, 1] }
    : axisScale(max, 4);

  const band = plotW / n;
  const barW = Math.max(1.5, Math.min(24, band - 2));
  const bx = (i) => padL + band * i + (band - barW) / 2;
  const cy = (v) => padT + plotH - (v / top) * plotH;

  let s = `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" role="img">`;
  for (const v of ticks) {
    const y = cy(v).toFixed(1);
    s += `<line class="st-gl" x1="${padL}" y1="${y}" x2="${W - padR}" y2="${y}"/>`;
    const lbl = share ? nf0.format(v * 100) + ' %' : compact(v);
    s += `<text class="st-ax" x="${padL - 8}" y="${y}" text-anchor="end" dominant-baseline="middle">${esc(lbl)}</text>`;
  }
  s += `<rect class="st-band" x="0" y="${padT}" width="${band}" height="${plotH}" rx="3"/>`;

  const tips = [];
  for (let i = 0; i < n; i++) {
    const d = days[i];
    const total = dayTotal(d);
    const scale = share ? (total > 0 ? 1 / total : 0) : 1;
    let acc = 0;
    const segs = [];
    for (const ser of keys) {
      const v = d[ser.key] * scale;
      if (v > 0) segs.push({ ser, v, from: acc });
      acc += v;
    }
    for (let k = 0; k < segs.length; k++) {
      const seg = segs[k];
      const yTop = cy(seg.from + seg.v);
      const yBot = cy(seg.from);
      const isTop = k === segs.length - 1;
      const gap = yBot - yTop > 5 && k > 0 ? 2 : 0;
      const h = Math.max(0.7, yBot - yTop - gap);
      const path = isTop ? topRound(bx(i), yTop, barW, h, 4) : `M${bx(i)} ${yTop}h${barW}v${h}h${-barW}Z`;
      s += `<path d="${path}" fill="${seg.ser.color}"/>`;
    }
    const rows = keys.map((ser) => tipRow(esc(ser.label), esc(compact(d[ser.key])), ser.color)).join('');
    tips.push({
      html: `<div class="st-tip-t">${esc(d.weekday)}, ${esc(longDate(d.date))}</div>` + rows +
        `<div class="st-tip-sep"></div>` +
        tipRow('Summe', esc(compact(dayTotal(d)))),
      band: { x: padL + band * i, y: padT, w: band, h: plotH },
    });
    s += `<rect class="st-hit" data-t="${i}" x="${padL + band * i}" y="${padT}" width="${band}" height="${plotH}"/>`;
  }

  for (const i of dateTicks(days, plotW)) {
    s += `<text class="st-ax" x="${(padL + band * (i + 0.5)).toFixed(1)}" y="${padT + plotH + 14}" text-anchor="middle">${esc(shortDate(days[i].date))}</text>`;
  }
  s += `<line class="st-base" x1="${padL}" y1="${padT + plotH}" x2="${W - padR}" y2="${padT + plotH}"/>`;
  bindPlot(host, s + '</svg>', tips);
}

function drawCost(host, days) {
  const W = measure(host, 320);
  const padL = PAD_L;
  const padR = 10;
  const padT = 12;
  const mainH = 112;
  const gapH = 26;
  const cumH = 48;
  const axisH = 20;
  const H = padT + mainH + gapH + cumH + axisH;
  const plotW = Math.max(40, W - padL - padR);
  const n = days.length;
  if (!n) return bindPlot(host, emptyPlot(W, H), []);

  const max = Math.max(0.0001, ...days.map((d) => d.cost));
  const { top, ticks } = axisScale(max, 3);
  const cum = [];
  let run = 0;
  for (const d of days) { run += d.cost; cum.push(run); }
  const cumTop = Math.max(0.0001, run);

  const band = plotW / n;
  const barW = Math.max(1.5, Math.min(24, band - 2));
  const bx = (i) => padL + band * i + (band - barW) / 2;
  const cy = (v) => padT + mainH - (v / top) * mainH;
  const cumY0 = padT + mainH + gapH;
  const cyc = (v) => cumY0 + cumH - (v / cumTop) * cumH;
  const cxc = (i) => padL + band * (i + 0.5);

  let s = `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" role="img">`;
  for (const v of ticks) {
    const y = cy(v).toFixed(1);
    s += `<line class="st-gl" x1="${padL}" y1="${y}" x2="${W - padR}" y2="${y}"/>`;
    s += `<text class="st-ax" x="${padL - 8}" y="${y}" text-anchor="end" dominant-baseline="middle">${esc(money(v))}</text>`;
  }
  s += `<rect class="st-band" x="0" y="${padT}" width="${band}" height="${mainH + gapH + cumH}" rx="3"/>`;
  for (let i = 0; i < n; i++) {
    const h = padT + mainH - cy(days[i].cost);
    if (h > 0.2) s += `<path d="${topRound(bx(i), cy(days[i].cost), barW, h, 4)}" fill="${SERIES[0]}"/>`;
  }
  s += `<line class="st-base" x1="${padL}" y1="${padT + mainH}" x2="${W - padR}" y2="${padT + mainH}"/>`;

  let area = `M${cxc(0).toFixed(1)} ${(cumY0 + cumH).toFixed(1)}`;
  let line = '';
  for (let i = 0; i < n; i++) {
    area += `L${cxc(i).toFixed(1)} ${cyc(cum[i]).toFixed(1)}`;
    line += `${i ? 'L' : 'M'}${cxc(i).toFixed(1)} ${cyc(cum[i]).toFixed(1)}`;
  }
  area += `L${cxc(n - 1).toFixed(1)} ${(cumY0 + cumH).toFixed(1)}Z`;
  s += `<text class="st-ax-s" x="${padL}" y="${cumY0 - 8}">kumuliert</text>`;
  s += `<text class="st-ax" x="${W - padR}" y="${cumY0 - 8}" text-anchor="end">${esc(money(run))}</text>`;
  s += `<line class="st-gl" x1="${padL}" y1="${cumY0 + cumH}" x2="${W - padR}" y2="${cumY0 + cumH}"/>`;
  s += `<path d="${area}" fill="${SERIES[0]}" fill-opacity="0.12"/>`;
  s += `<path d="${line}" fill="none" stroke="${SERIES[0]}" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`;
  s += `<circle cx="${cxc(n - 1).toFixed(1)}" cy="${cyc(cum[n - 1]).toFixed(1)}" r="4" fill="${SERIES[0]}" stroke="var(--surface)" stroke-width="2"/>`;

  for (const i of dateTicks(days, plotW)) {
    s += `<text class="st-ax" x="${cxc(i).toFixed(1)}" y="${cumY0 + cumH + 14}" text-anchor="middle">${esc(shortDate(days[i].date))}</text>`;
  }

  s += `<g class="st-cross"><line x1="0" y1="${padT}" x2="0" y2="${cumY0 + cumH}"/>` +
    `<circle r="4.5" fill="${SERIES[0]}" stroke="var(--surface)" stroke-width="2"/></g>`;

  const tips = [];
  for (let i = 0; i < n; i++) {
    const d = days[i];
    tips.push({
      html: `<div class="st-tip-t">${esc(d.weekday)}, ${esc(longDate(d.date))}</div>` +
        tipRow('Kosten', esc(money(d.cost)), SERIES[0]) +
        tipRow('kumuliert', esc(money(cum[i]))) +
        `<div class="st-tip-sep"></div>` +
        tipRow('Turns', esc(nf0.format(d.turns))),
      band: { x: padL + band * i, y: padT, w: band, h: mainH + gapH + cumH },
      cross: { x: cxc(i), pts: [cyc(cum[i])] },
    });
    s += `<rect class="st-hit" data-t="${i}" x="${padL + band * i}" y="${padT}" width="${band}" height="${mainH + gapH + cumH}"/>`;
  }
  bindPlot(host, s + '</svg>', tips);
}

function heatColor(v, max) {
  if (v <= 0) return ZERO_CELL;
  const t = Math.sqrt(v / max);
  const idx = Math.min(RAMP.length - 1, Math.max(0, Math.round(t * (RAMP.length - 1))));
  return RAMP[Math.max(1, idx)];
}

function drawHeatmap(host, heatmap, hours) {
  const W = measure(host, 360);
  const padL = 30;
  const padR = 86;
  const padT = 6;
  const gridW = Math.max(120, W - padL - padR);
  const cw = gridW / 24;
  const ch = Math.max(16, Math.min(cw, 30));
  const gridH = ch * 7;
  const histGap = 10;
  const histH = 40;
  const H = padT + gridH + histGap + histH + 18;

  const max = Math.max(1, ...heatmap.map((r) => Math.max(...r)));
  const hourMax = Math.max(1, ...hours);
  const rowTotals = heatmap.map((r) => r.reduce((a, b) => a + b, 0));
  const rowMax = Math.max(1, ...rowTotals);

  let s = `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" role="img">`;
  const tips = [];
  for (let r = 0; r < 7; r++) {
    const y = padT + r * ch;
    s += `<text class="st-ax" x="${padL - 8}" y="${(y + ch / 2).toFixed(1)}" text-anchor="end" dominant-baseline="middle">${WEEKDAYS[r]}</text>`;
    for (let h = 0; h < 24; h++) {
      const v = heatmap[r][h];
      const x = padL + h * cw;
      const i = tips.length;
      tips.push({
        html: `<div class="st-tip-t">${WEEKDAYS[r]} ${String(h).padStart(2, '0')}:00–${String(h).padStart(2, '0')}:59</div>` +
          tipRow('Prompts', esc(nf0.format(v)), v > 0 ? heatColor(v, max) : 'var(--grid)'),
      });
      s += `<rect data-t="${i}" x="${(x + 1).toFixed(1)}" y="${(y + 1).toFixed(1)}" width="${Math.max(1, cw - 2).toFixed(1)}" height="${Math.max(1, ch - 2).toFixed(1)}" rx="3" fill="${heatColor(v, max)}"/>`;
    }
    const bw = 44;
    const bx = W - padR + 10;
    const w = (rowTotals[r] / rowMax) * bw;
    if (w > 0.5) s += `<path d="${rightRound(bx, y + ch / 2 - 4, w, 8, 4)}" fill="${SERIES[0]}"/>`;
    s += `<text class="st-ax" x="${bx + bw + 6}" y="${(y + ch / 2).toFixed(1)}" dominant-baseline="middle">${esc(compact(rowTotals[r]))}</text>`;
  }

  const histY = padT + gridH + histGap;
  const hbW = Math.max(2, Math.min(24, cw - 3));
  for (let h = 0; h < 24; h++) {
    const hh = (hours[h] / hourMax) * histH;
    const x = padL + h * cw + (cw - hbW) / 2;
    if (hh > 0.4) s += `<path d="${topRound(x, histY + histH - hh, hbW, hh, 3)}" fill="${DIM}"/>`;
    if (h % 6 === 0) {
      s += `<text class="st-ax" x="${(padL + h * cw + cw / 2).toFixed(1)}" y="${histY + histH + 14}" text-anchor="middle">${h}</text>`;
    }
  }
  s += `<line class="st-base" x1="${padL}" y1="${histY + histH}" x2="${padL + gridW}" y2="${histY + histH}"/>`;
  s += `<text class="st-ax" x="${padL + gridW + 10}" y="${histY + histH}" dominant-baseline="middle">Uhr</text>`;
  bindPlot(host, s + '</svg>', tips);
}

function drawProjects(host, projects) {
  const W = measure(host, 300);
  const rowH = 34;
  const padT = 18;
  const H = padT + Math.max(1, projects.length) * rowH + 4;
  if (!projects.length) return bindPlot(host, emptyPlot(W, 120), []);

  const narrow = W < 470;
  const nameW = Math.min(180, Math.max(84, Math.round(W * 0.24)));
  const commitsX = W;
  const promptsX = W - 54;
  const costX = narrow ? W - 54 : W - 116;
  const metaLeft = costX - 62;
  const barX = nameW + 14;
  const valW = 58;
  const barW = Math.max(24, metaLeft - 10 - valW - barX);
  const max = Math.max(1, ...projects.map((p) => p.tokens));

  let s = `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" role="img">`;
  s += `<rect class="st-band" x="0" y="0" width="${W}" height="${rowH}" rx="5"/>`;
  s += `<text class="st-ax" x="${barX}" y="10">Tokens</text>`;
  s += `<text class="st-ax" x="${costX}" y="10" text-anchor="end">Kosten</text>`;
  if (!narrow) s += `<text class="st-ax" x="${promptsX}" y="10" text-anchor="end">Prompts</text>`;
  s += `<text class="st-ax" x="${commitsX}" y="10" text-anchor="end">${narrow ? 'Comm.' : 'Commits'}</text>`;

  const tips = [];
  projects.forEach((p, i) => {
    const y = padT + i * rowH;
    const active = p.active > 0;
    const w = Math.max(2, (p.tokens / max) * barW);
    const label = p.name || 'ohne Projekt';
    const maxChars = Math.max(8, Math.floor(nameW / 6.6));
    const clipped = label.length > maxChars ? label.slice(0, maxChars - 1) + '…' : label;
    s += `<text class="st-ax-s" x="0" y="${y + rowH / 2}" dominant-baseline="middle" fill="${active ? 'var(--ink)' : 'var(--ink-2)'}">${esc(clipped)}</text>`;
    if (active) s += `<circle cx="${nameW + 4}" cy="${y + rowH / 2}" r="3" fill="var(--accent)"/>`;
    s += `<path d="${rightRound(barX, y + rowH / 2 - 7, w, 14, 4)}" fill="${active ? SERIES[0] : FADED}"/>`;
    s += `<text class="st-ax" x="${barX + w + 7}" y="${y + rowH / 2}" dominant-baseline="middle">${esc(compact(p.tokens))}</text>`;
    s += `<text class="st-ax" x="${costX}" y="${y + rowH / 2}" text-anchor="end" dominant-baseline="middle" fill="var(--ink-2)">${esc(money(p.cost))}</text>`;
    if (!narrow) s += `<text class="st-ax" x="${promptsX}" y="${y + rowH / 2}" text-anchor="end" dominant-baseline="middle">${esc(compact(p.prompts))}</text>`;
    s += `<text class="st-ax" x="${commitsX}" y="${y + rowH / 2}" text-anchor="end" dominant-baseline="middle">${esc(nf0.format(p.commits))}</text>`;
    tips.push({
      html: `<div class="st-tip-t">${esc(label)}${active ? ' · aktive Session' : ''}</div>` +
        tipRow('Tokens', esc(compact(p.tokens)), active ? SERIES[0] : FADED) +
        tipRow('Kosten', esc(money(p.cost))) +
        tipRow('Prompts', esc(nf0.format(p.prompts))) +
        tipRow('Sessions', esc(nf0.format(p.sessions))) +
        tipRow('Commits', esc(nf0.format(p.commits))),
      band: { x: 0, y, w: W, h: rowH },
    });
  });
  projects.forEach((p, i) => {
    s += `<rect class="st-hit" data-t="${i}" x="0" y="${padT + i * rowH}" width="${W}" height="${rowH}"/>`;
  });
  bindPlot(host, s + '</svg>', tips);
}

function drawModelBar(host, allModels, slotOf) {
  const W = measure(host, 220);
  const H = 26;
  const total = allModels.reduce((a, m) => a + m.cost, 0);
  if (!allModels.length || total <= 0) return bindPlot(host, emptyPlot(W, H), []);

  const models = allModels.slice().sort((a, b) => slotOf(a.model) - slotOf(b.model));
  let s = `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" role="img">`;
  const tips = [];
  let x = 0;
  models.forEach((m) => {
    const w = (m.cost / total) * W;
    if (w <= 0) return;
    const gap = w > 6 && x > 0 ? 2 : 0;
    const ww = Math.max(1, w - gap);
    s += `<rect data-t="${tips.length}" x="${(x + gap).toFixed(1)}" y="4" width="${ww.toFixed(1)}" height="18" rx="3" fill="${SERIES[slotOf(m.model)]}"/>`;
    tips.push({
      html: `<div class="st-tip-t">${esc(modelLabel(m.model))}</div>` +
        tipRow('Kosten', esc(money(m.cost)), SERIES[slotOf(m.model)]) +
        tipRow('Anteil', esc(pct((m.cost / total) * 100, true))) +
        tipRow('Turns', esc(nf0.format(m.turns))),
    });
    x += w;
  });
  bindPlot(host, s + '</svg>', tips);
}

function drawSpark(host, days, key, color) {
  const W = measure(host, 60);
  const H = 40;
  const n = days.length;
  if (!n) return bindPlot(host, emptyPlot(W, H), []);
  const max = Math.max(1, ...days.map((d) => d[key]));
  const band = W / n;
  const bw = Math.max(1, Math.min(6, band - 1));
  let s = `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" role="img">`;
  const tips = [];
  for (let i = 0; i < n; i++) {
    const h = (days[i][key] / max) * (H - 4);
    const x = band * i + (band - bw) / 2;
    if (h > 0.3) s += `<path d="${topRound(x, H - h, bw, h, Math.min(2, bw / 2))}" fill="${color}"/>`;
    s += `<rect class="st-hit" data-t="${i}" x="${band * i}" y="0" width="${band}" height="${H}"/>`;
    tips.push({
      html: `<div class="st-tip-t">${esc(days[i].weekday)}, ${esc(longDate(days[i].date))}</div>` +
        tipRow('Commits', esc(nf0.format(days[i][key])), color),
    });
  }
  s += `<line class="st-base" x1="0" y1="${H}" x2="${W}" y2="${H}"/>`;
  bindPlot(host, s + '</svg>', tips);
}

function emptyPlot(W, H) {
  return `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" role="img">` +
    `<text class="st-ax" x="${W / 2}" y="${H / 2}" text-anchor="middle" dominant-baseline="middle">keine Daten im Zeitraum</text></svg>`;
}

function legend(items, shape) {
  return `<div class="st-legend">` + items.map((it) =>
    `<span class="st-lg"><i class="${shape === 'line' ? 'st-ln' : 'st-sw'}" style="background:${it.color}"></i>${esc(it.label)}</span>`).join('') + `</div>`;
}

const TILE_HINT = {
  Kosten: 'Zu API-Listenpreisen hochgerechnet. Mit einem Max-Abo zahlst du diesen Betrag nicht — die Zahl zeigt, was die Arbeit über die API gekostet hätte.',
  Tokens: 'Inklusive Cache-Read: bei jedem Turn wird der gesamte bisherige Kontext erneut gelesen. Das summiert sich schnell in die Milliarden und ist kein zusätzlicher Verbrauch. „ohne Cache" zeigt die tatsächlich neu verarbeiteten Tokens.',
  Turns: 'Antworten der Agents, inklusive aller Subagents — deshalb ein Vielfaches deiner Prompts.',
};

function tileHtml(label, value, note, unit) {
  const hint = TILE_HINT[label];
  return `<div class="st-tile"${hint ? ` title="${esc(hint)}"` : ''}><div class="k">${esc(label)}${hint ? '<span class="st-q">?</span>' : ''}</div>` +
    `<div class="v">${esc(value)}${unit ? `<small>${esc(unit)}</small>` : ''}</div>` +
    `<div class="n">${esc(note || '')}</div></div>`;
}

export function renderStats(el, stats, opts = {}) {
  if (!el) return;
  if (typeof el.__statsCleanup === 'function') el.__statsCleanup();
  el.__statsCleanup = null;

  const data = normalize(stats);
  const d = data.days;
  const t = data.totals;
  const state = { tokenMode: 'abs' };

  el.classList.add('st-root');

  const rangeBtns = typeof opts.onRange === 'function'
    ? `<div class="st-range">${[7, 30, 90].map((r) =>
        `<button type="button" data-range="${r}" class="${r === data.range ? 'on' : ''}">${r} T</button>`).join('')}</div>`
    : '';

  const first = d.length ? longDate(d[0].date) : '';
  const last = d.length ? longDate(d[d.length - 1].date) : '';
  const busiest = d.find((x) => x.date === t.busiestDay);
  const head =
    `<div class="st-head"><div><h1>Statistik</h1>` +
    `<div class="st-sub">${d.length ? `${esc(first)} – ${esc(last)} · <b>${esc(nf0.format(t.days))}</b> ${t.days === 1 ? 'Tag' : 'Tage'}` : 'kein Zeitraum'}` +
    `${busiest ? ` · aktivster Tag <b>${esc(busiest.weekday)}, ${esc(longDate(busiest.date))}</b> (${esc(nf0.format(busiest.prompts))} Prompts)` : ''}</div></div>` +
    `<div class="st-head-r">${rangeBtns}</div></div>` +
    (data.err ? `<div class="st-err">${esc(data.err)}</div>` : '');

  if (t.prompts <= 0) {
    el.innerHTML = head +
      `<div class="st-empty"><b>Noch keine Aktivität im Zeitraum</b>` +
      `Sobald du Sessions startest und Prompts schickst, füllen sich hier Verlauf, Arbeitsrhythmus, Projekte und Kosten.</div>`;
    wireRange(el, opts);
    el.__statsCleanup = () => {};
    return;
  }

  const modelsSorted = data.models.slice().sort((a, b) => b.cost - a.cost);
  const slotOrder = data.models.slice().sort((a, b) => {
    const fr = familyRank(a.model) - familyRank(b.model);
    return fr || String(a.model).localeCompare(String(b.model));
  }).map((m) => m.model);
  const slotOf = (id) => {
    const i = slotOrder.indexOf(id);
    return i < 0 ? SERIES.length - 1 : i % SERIES.length;
  };
  const modelCost = modelsSorted.reduce((a, m) => a + m.cost, 0);
  const projects = data.projects.slice().sort((a, b) => b.tokens - a.tokens);
  const activeCount = projects.filter((p) => p.active > 0).length;
  const avgPrompts = t.days > 0 ? t.prompts / t.days : 0;
  const perPrompt = t.prompts > 0 ? t.cost / t.prompts : 0;

  el.innerHTML = head +
    `<div class="st-tiles">` +
    tileHtml('Prompts', compact(t.prompts), `Ø ${nf1.format(avgPrompts)} pro Tag`) +
    tileHtml('Turns', compact(t.turns), t.prompts > 0 ? `${nf1.format(t.turns / t.prompts)} pro Prompt` : '') +
    tileHtml('Sessions', compact(t.sessions), activeCount ? `${nf0.format(activeCount)} Projekte gerade aktiv` : 'keine aktive Session') +
    tileHtml('Tokens', compact(t.tokens), `${compact(t.input + t.output)} ohne Cache`) +
    tileHtml('Kosten', money(t.cost), `hochgerechnet · ${money(perPrompt)} pro Prompt`) +
    tileHtml('Cache-Treffer', pct(t.cacheHit, true), `${compact(t.cacheRead)} gelesen`) +
    tileHtml('Serie', nf0.format(t.streak), `${t.streak === 1 ? 'Tag' : 'Tage'} in Folge aktiv`) +
    `<div class="st-tile wide" title="Nur Commits, die unter deiner git-Identität stehen — fremde Commits im selben Repository zählen nicht mit."><div class="tx"><div class="k">Commits</div>` +
      `<div class="v">${esc(compact(t.commits))}</div><div class="n">nur deine · ${esc(nf1.format(t.days > 0 ? t.commits / t.days : 0))} pro Tag</div></div>` +
      `<div class="st-plot" data-plot="spark"></div></div>` +
    `</div>` +

    `<div class="st-card"><div class="st-card-head"><h2>Aktivität pro Tag</h2>` +
      `<span class="st-note">deine Prompts oben, die Turns der Agents darunter — gleiche Zeitachse, eigene Skala</span>` +
      `<div class="r">${legend([{ label: 'Prompts', color: SERIES[0] }, { label: 'Turns', color: SERIES[1] }], 'line')}</div></div>` +
      `<div class="st-plot" data-plot="activity"></div></div>` +

    `<div class="st-card"><div class="st-card-head"><h2>Token-Verlauf</h2>` +
      `<span class="st-note" data-note="tokens"></span>` +
      `<div class="r">${legend(TOKEN_SERIES.map((s) => ({ label: s.label, color: s.color })))}` +
      `<div class="st-seg" data-seg="tokens">` +
        `<button type="button" data-mode="abs" class="on">absolut</button>` +
        `<button type="button" data-mode="share">Anteil</button>` +
        `<button type="button" data-mode="nocache">ohne Cache-Read</button>` +
      `</div></div></div>` +
      `<div class="st-plot" data-plot="tokens"></div></div>` +

    `<div class="st-card"><div class="st-card-head"><h2>Kosten pro Tag</h2>` +
      `<span class="st-note">Tagesbalken oben, kumulierter Verlauf unten — gleiche Zeitachse, eigene Skala</span></div>` +
      `<div class="st-plot" data-plot="cost"></div></div>` +

    `<div class="st-card"><div class="st-card-head"><h2>Arbeitsrhythmus</h2>` +
      `<span class="st-note">Prompts nach Wochentag und Stunde</span>` +
      `<div class="r"><span class="st-scale">0` +
        RAMP.slice(1).map((c) => `<i style="background:${c}"></i>`).join('') +
        `${esc(nf0.format(Math.max(...data.heatmap.map((r) => Math.max(...r)))))} Prompts/Std.</span></div></div>` +
      `<div class="st-plot" data-plot="heatmap"></div></div>` +

    `<div class="st-cols">` +
      `<div class="st-card"><div class="st-card-head"><h2>Projekte</h2>` +
        `<span class="st-note">nach Tokens${activeCount ? ` · <span style="color:var(--accent)">●</span> aktive Session` : ''}</span></div>` +
        `<div class="st-plot" data-plot="projects"></div></div>` +
      `<div class="st-card"><div class="st-card-head"><h2>Modelle</h2>` +
        `<span class="st-note">Kostenanteil</span></div>` +
        `<div class="st-plot" data-plot="models"></div>` +
        modelTable(modelsSorted, modelCost, slotOf) +
      `</div>` +
    `</div>`;

  const note = el.querySelector('[data-note="tokens"]');
  const noteText = {
    abs: 'gestapelt, echte Größenordnung — Cache-Read dominiert',
    share: 'Tagesanteile — kleine Serien bleiben sichtbar',
    nocache: 'ohne Cache-Read — Input, Output und Cache-Write auf voller Achse',
  };

  const tip = document.createElement('div');
  tip.className = 'st-tip';
  el.appendChild(tip);

  const plots = {};
  el.querySelectorAll('[data-plot]').forEach((p) => { plots[p.getAttribute('data-plot')] = p; });

  function draw() {
    if (plots.spark) drawSpark(plots.spark, d, 'commits', SERIES[0]);
    if (plots.activity) drawActivity(plots.activity, d);
    if (plots.tokens) drawTokens(plots.tokens, d, state.tokenMode);
    if (plots.cost) drawCost(plots.cost, d);
    if (plots.heatmap) drawHeatmap(plots.heatmap, data.heatmap, data.hours);
    if (plots.projects) drawProjects(plots.projects, projects);
    if (plots.models) drawModelBar(plots.models, modelsSorted, slotOf);
    if (note) note.textContent = noteText[state.tokenMode] || '';
  }
  draw();

  const seg = el.querySelector('[data-seg="tokens"]');
  if (seg) {
    seg.addEventListener('click', (e) => {
      const b = e.target.closest('button[data-mode]');
      if (!b) return;
      state.tokenMode = b.getAttribute('data-mode');
      seg.querySelectorAll('button').forEach((x) => x.classList.toggle('on', x === b));
      drawTokens(plots.tokens, d, state.tokenMode);
      if (note) note.textContent = noteText[state.tokenMode] || '';
    });
  }
  wireRange(el, opts);

  function hideTip() {
    tip.classList.remove('on');
    el.querySelectorAll('.st-band').forEach((b) => { b.style.opacity = '0'; });
    el.querySelectorAll('.st-cross').forEach((c) => { c.style.opacity = '0'; });
  }

  function onMove(e) {
    const mark = e.target.closest ? e.target.closest('[data-t]') : null;
    const host = mark && mark.closest('.st-plot');
    const entry = host && host._tips && host._tips[+mark.getAttribute('data-t')];
    if (!entry) { hideTip(); return; }

    tip.innerHTML = entry.html;
    tip.classList.add('on');
    const w = tip.offsetWidth;
    const h = tip.offsetHeight;
    let x = e.clientX + 16;
    let y = e.clientY - h - 14;
    if (x + w > window.innerWidth - 8) x = e.clientX - w - 16;
    if (y < 8) y = e.clientY + 20;
    tip.style.transform = `translate(${Math.max(8, x)}px, ${Math.max(8, y)}px)`;

    el.querySelectorAll('.st-band').forEach((b) => { b.style.opacity = '0'; });
    el.querySelectorAll('.st-cross').forEach((c) => { c.style.opacity = '0'; });
    if (entry.band) {
      const b = host.querySelector('.st-band');
      if (b) {
        b.setAttribute('x', entry.band.x);
        b.setAttribute('y', entry.band.y);
        b.setAttribute('width', entry.band.w);
        b.setAttribute('height', entry.band.h);
        b.style.opacity = '1';
      }
    }
    if (entry.cross) {
      const g = host.querySelector('.st-cross');
      if (g) {
        const ln = g.querySelector('line');
        ln.setAttribute('x1', entry.cross.x);
        ln.setAttribute('x2', entry.cross.x);
        g.querySelectorAll('circle').forEach((c, i) => {
          c.setAttribute('cx', entry.cross.x);
          c.setAttribute('cy', entry.cross.pts[i] == null ? -20 : entry.cross.pts[i]);
        });
        g.style.opacity = '1';
      }
    }
  }

  el.addEventListener('pointermove', onMove);
  el.addEventListener('pointerleave', hideTip);
  el.addEventListener('scroll', hideTip, { passive: true });

  let raf = 0;
  let lastW = el.clientWidth;
  const onResize = () => {
    if (el.clientWidth === lastW) return;
    lastW = el.clientWidth;
    cancelAnimationFrame(raf);
    raf = requestAnimationFrame(() => { hideTip(); draw(); });
  };
  window.addEventListener('resize', onResize);

  el.__statsCleanup = () => {
    cancelAnimationFrame(raf);
    window.removeEventListener('resize', onResize);
    el.removeEventListener('pointermove', onMove);
    el.removeEventListener('pointerleave', hideTip);
    el.removeEventListener('scroll', hideTip);
    if (tip.parentNode) tip.parentNode.removeChild(tip);
  };
}

function modelTable(models, total, slotOf) {
  if (!models.length) return '<div class="st-note" style="padding:10px 0;color:var(--muted)">keine Modelldaten</div>';
  const rows = models.map((m) => {
    const tokens = m.input + m.output + m.cacheRead + m.cacheWrite;
    return `<tr><td><span class="st-mn"><i class="st-sw" style="background:${SERIES[slotOf(m.model)]}"></i>${esc(modelLabel(m.model))}</span></td>` +
      `<td>${esc(nf0.format(m.turns))}</td><td>${esc(compact(tokens))}</td>` +
      `<td class="num-strong">${esc(money(m.cost))}</td>` +
      `<td>${esc(total > 0 ? pct((m.cost / total) * 100, true) : '–')}</td></tr>`;
  }).join('');
  return `<table class="st-table"><thead><tr><th>Modell</th><th>Turns</th><th>Tokens</th><th>Kosten</th><th>Anteil</th></tr></thead><tbody>${rows}</tbody></table>`;
}

function wireRange(el, opts) {
  if (typeof opts.onRange !== 'function') return;
  const box = el.querySelector('.st-range');
  if (!box) return;
  box.addEventListener('click', (e) => {
    const b = e.target.closest('button[data-range]');
    if (!b) return;
    box.querySelectorAll('button').forEach((x) => x.classList.toggle('on', x === b));
    opts.onRange(+b.getAttribute('data-range'));
  });
}

export default renderStats;
