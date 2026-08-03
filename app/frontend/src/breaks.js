import './breaks.css';

// Alle Impulse holen vom Stuhl — Sitzenbleiben ist genau das, was hier nicht
// helfen soll.
const IMPULSES = [
  'Steh auf und geh ein paar Schritte. Mehr braucht es nicht.',
  'Aufstehen und strecken — so weit es geht.',
  'Hol dir ein Glas Wasser. Der Weg dahin ist der Punkt.',
  'Steh auf und schau aus dem Fenster, so weit du sehen kannst.',
  'Aufstehen und die Schultern ein paar Mal kreisen lassen.',
  'Mach das Fenster auf und bleib einen Moment davor stehen.',
  'Geh einmal um den Schreibtisch herum. Ohne Telefon.',
];

const QUICK_LENGTHS = [2, 5, 10];
const DEFAULT_MINUTES = 5;
const PHASES = ['Einatmen', 'Halten', 'Ausatmen'];
const BREATH_IN = 4;
const BREATH_HOLD = 4;
const BREATH_OUT = 6;
const BREATH_CYCLE = BREATH_IN + BREATH_HOLD + BREATH_OUT;
const PHASE_ENDS = [BREATH_IN, BREATH_IN + BREATH_HOLD, BREATH_CYCLE];
const TICK_MS = 200;
const IMPULSE_KEY = 'magentic.breaks.impulse';

const CFG_GROUPS = [
  {
    title: 'Wie lange',
    fields: [{ key: 'breakMins', label: 'Eine Pause dauert', min: 1, max: 120 }],
  },
  {
    title: 'Wie oft',
    hint: 'gemessen an ununterbrochener Arbeit',
    fields: [
      { key: 'hintAfter', label: 'Leiser Hinweis nach', min: 5, max: 240 },
      { key: 'dueAfter', label: 'Pause fällig nach', min: 5, max: 300 },
      { key: 'overdueAfter', label: 'Hartnäckig ab', min: 5, max: 480 },
    ],
  },
  {
    title: 'Feinheiten',
    fields: [
      { key: 'snoozeMins', label: '„Später" verschiebt um', min: 1, max: 120 },
      { key: 'minBreak', label: 'Zählt als Pause ab', min: 1, max: 60 },
      { key: 'idleResets', label: 'Abwesenheit erkannt nach', min: 1, max: 120 },
    ],
  },
];

const CFG_FIELDS = CFG_GROUPS.flatMap(g => g.fields);

const GEAR = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="3"/><path d="M19.1 14.6a1.5 1.5 0 0 0 .3 1.7l.1.1a1.9 1.9 0 1 1-2.6 2.6l-.1-.1a1.5 1.5 0 0 0-1.7-.3 1.5 1.5 0 0 0-.9 1.4v.2a1.9 1.9 0 1 1-3.8 0v-.1a1.5 1.5 0 0 0-1-1.4 1.5 1.5 0 0 0-1.7.3l-.1.1a1.9 1.9 0 1 1-2.6-2.6l.1-.1a1.5 1.5 0 0 0 .3-1.7 1.5 1.5 0 0 0-1.4-.9h-.2a1.9 1.9 0 1 1 0-3.8h.1a1.5 1.5 0 0 0 1.4-1 1.5 1.5 0 0 0-.3-1.7l-.1-.1a1.9 1.9 0 1 1 2.6-2.6l.1.1a1.5 1.5 0 0 0 1.7.3h.1a1.5 1.5 0 0 0 .9-1.4v-.2a1.9 1.9 0 1 1 3.8 0v.1a1.5 1.5 0 0 0 .9 1.4 1.5 1.5 0 0 0 1.7-.3l.1-.1a1.9 1.9 0 1 1 2.6 2.6l-.1.1a1.5 1.5 0 0 0-.3 1.7v.1a1.5 1.5 0 0 0 1.4.9h.2a1.9 1.9 0 1 1 0 3.8h-.1a1.5 1.5 0 0 0-1.4.9z"/></svg>';

let cb = {};
// Muss zu DefaultBreakConfig() in core/breaks.go passen — gilt nur, solange
// das Backend die echten Werte noch nicht geliefert hat.
let cfg = { enabled: true, hintAfter: 40, dueAfter: 55, overdueAfter: 80, minBreak: 4, idleResets: 6, snoozeMins: 10, breakMins: 5 };
let advice = null;
let mounted = false;
let open = false;
let impulseIdx = -1;

let ind = null;
let indTxt = null;
let indGood = null;
let indCta = null;
let indX = null;
let indSig = '';

let overlay = null;
let sheet = null;
let orbEl = null;
let phaseEl = null;
let impulseEl = null;
let barEl = null;
let lenEls = [];
let lensEl = null;
let agentsEl = null;
let primaryEl = null;
let settingsEl = null;

let startedAt = 0;
let breathStart = 0;
let deadline = 0;
let minutes = DEFAULT_MINUTES;
let doneShown = false;
let lastBarPct = -1;
let lastTick = 0;
let raf = 0;
let prevFocus = null;
let sheetSig = '';
let quietTimer = 0;
let settingsOnly = false;

const reducedMotion = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function num(v, fallback = 0) {
  const n = Number(v);
  return Number.isFinite(n) ? n : fallback;
}

function setText(el, s) {
  if (!el || el.__brT === s) return;
  el.__brT = s;
  el.textContent = s;
}

function setHtml(el, s) {
  if (!el || el.__brH === s) return;
  el.__brH = s;
  el.innerHTML = s;
}

function fmtDur(secs) {
  const s = Math.max(0, Math.round(num(secs)));
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  const rest = m % 60;
  return rest ? `${h} h ${rest} min` : `${h} h`;
}

function fmtClock(secs) {
  const s = Math.max(0, Math.round(num(secs)));
  const m = Math.floor(s / 60);
  const r = s % 60;
  return `${m}:${String(r).padStart(2, '0')}`;
}

function nextImpulse() {
  if (impulseIdx < 0) {
    let stored = null;
    try { stored = localStorage.getItem(IMPULSE_KEY); } catch { stored = null; }
    impulseIdx = stored === null ? -1 : Math.max(-1, Math.round(num(stored, -1)));
  }
  impulseIdx = (impulseIdx + 1) % IMPULSES.length;
  try { localStorage.setItem(IMPULSE_KEY, String(impulseIdx)); } catch { /* Speicher gesperrt — dann rotiert es nur innerhalb der Sitzung */ }
  return IMPULSES[impulseIdx];
}

function buildIndicator() {
  ind = document.createElement('div');
  ind.className = 'br-ind';
  ind.innerHTML = `
    <button class="br-ind-main" type="button" data-act="open">
      <span class="br-ind-dot"></span>
      <span class="br-ind-txt"></span>
      <span class="br-ind-good">guter Moment</span>
      <span class="br-ind-cta">Pause</span>
    </button>
    <button class="br-ind-x" type="button" data-act="snooze" aria-label="Später erinnern" title="Später erinnern">×</button>`;
  indTxt = ind.querySelector('.br-ind-txt');
  indGood = ind.querySelector('.br-ind-good');
  indCta = ind.querySelector('.br-ind-cta');
  indX = ind.querySelector('.br-ind-x');
  ind.addEventListener('click', e => {
    const act = e.target.closest('[data-act]')?.dataset.act;
    if (act === 'snooze') {
      e.stopPropagation();
      try { cb.onSnooze?.(); } catch { /* Backend meldet sich beim nächsten Refresh */ }
      return;
    }
    if (act === 'open') openBreak();
  });
  document.body.appendChild(ind);
}

function settingsHtml() {
  const groups = CFG_GROUPS.map(g => `
    <div class="br-sgroup">
      <div class="br-shead">${g.title}${g.hint ? `<span class="br-shint">${g.hint}</span>` : ''}</div>
      ${g.fields.map(f => `
        <label class="br-srow">
          <span class="br-slabel">${f.label}</span>
          <input class="br-snum" type="number" inputmode="numeric" data-cfg="${f.key}" min="${f.min}" max="${f.max}" step="1">
          <span class="br-sunit">Min.</span>
        </label>`).join('')}
    </div>`).join('');
  return `
    <label class="br-srow br-scheck">
      <input type="checkbox" data-cfg="enabled">
      <span class="br-slabel">Pausen vorschlagen</span>
    </label>
    ${groups}`;
}

function buildOverlay() {
  overlay = document.createElement('div');
  overlay.className = 'br-overlay';
  overlay.setAttribute('role', 'dialog');
  overlay.setAttribute('aria-modal', 'true');
  overlay.setAttribute('aria-label', 'Pause');
  overlay.innerHTML = `
    <div class="br-scrim" data-act="close"></div>
    <div class="br-sheet" tabindex="-1">
      <div class="br-top">
        <span class="br-kicker">Pause</span>
        <button class="br-gear" type="button" data-act="settings" aria-label="Einstellungen" title="Einstellungen">${GEAR}</button>
      </div>
      <div class="br-breath">
        <div class="br-orb">
          <span class="br-ring"></span>
          <span class="br-core"></span>
          <span class="br-phase">Einatmen</span>
        </div>
      </div>
      <p class="br-impulse"></p>
      <div class="br-timer">
        <div class="br-bar"><i class="br-bar-fill"></i></div>
        <div class="br-lens"></div>
      </div>
      <div class="br-done-line">Fertig — bereit weiterzumachen?</div>
      <div class="br-agents"></div>
      <div class="br-actions"><button class="br-primary" type="button" data-act="close">Zurück an die Arbeit</button></div>
      <div class="br-settings" hidden>${settingsHtml()}</div>
    </div>`;

  sheet = overlay.querySelector('.br-sheet');
  orbEl = overlay.querySelector('.br-orb');
  phaseEl = overlay.querySelector('.br-phase');
  impulseEl = overlay.querySelector('.br-impulse');
  barEl = overlay.querySelector('.br-bar-fill');
  lensEl = overlay.querySelector('.br-lens');
  agentsEl = overlay.querySelector('.br-agents');
  primaryEl = overlay.querySelector('.br-primary');
  settingsEl = overlay.querySelector('.br-settings');

  overlay.addEventListener('click', onOverlayClick);
  settingsEl.addEventListener('change', onSettingsChange);
  document.body.appendChild(overlay);
}

function onOverlayClick(e) {
  const hit = e.target.closest('[data-act]');
  if (!hit || !overlay.contains(hit)) return;
  const act = hit.dataset.act;
  if (act === 'close') closeBreak();
  else if (act === 'settings') toggleSettings();
  else if (act === 'len') {
    setLength(num(hit.dataset.min, DEFAULT_MINUTES));
    if (cfg.breakMins !== minutes) {
      cfg = { ...cfg, breakMins: minutes };
      renderLengths();
      try { cb.onConfig?.({ ...cfg }); } catch { /* Backend übernimmt beim nächsten Start */ }
    }
  }
}

function onSettingsChange(e) {
  const el = e.target.closest('[data-cfg]');
  if (!el) return;
  const key = el.dataset.cfg;
  if (key === 'enabled') {
    cfg.enabled = el.checked;
    renderIndicator(advice, true);
    emitConfig();
    return;
  }
  const field = CFG_FIELDS.find(f => f.key === key);
  if (!field) return;
  const raw = el.value.trim() === '' ? NaN : Number(el.value);
  if (!Number.isFinite(raw)) {
    el.value = String(cfg[key]);
    return;
  }
  cfg[key] = Math.min(field.max, Math.max(field.min, Math.round(raw)));
  el.value = String(cfg[key]);
  emitConfig();
}

function emitConfig() {
  const out = { enabled: !!cfg.enabled };
  for (const f of CFG_FIELDS) out[f.key] = num(cfg[f.key], f.min);
  try { cb.onConfig?.(out); } catch { /* Backend übernimmt beim nächsten Öffnen */ }
}

function fillSettings() {
  const box = settingsEl.querySelector('[data-cfg="enabled"]');
  if (box) box.checked = !!cfg.enabled;
  for (const f of CFG_FIELDS) {
    const el = settingsEl.querySelector(`[data-cfg="${f.key}"]`);
    if (el) el.value = String(Math.min(f.max, Math.max(f.min, Math.round(num(cfg[f.key], f.min)))));
  }
}

function toggleSettings() {
  const show = settingsEl.hasAttribute('hidden');
  if (show) fillSettings();
  settingsEl.toggleAttribute('hidden', !show);
  if (show) {
    requestAnimationFrame(() => settingsEl.scrollIntoView({ block: 'end', behavior: reducedMotion ? 'auto' : 'smooth' }));
  }
}

function agentsHtml(a) {
  const waiting = Math.max(0, Math.round(num(a?.waiting)));
  const busy = Math.max(0, Math.round(num(a?.busy)));
  if (waiting > 0) {
    const who = waiting === 1 ? 'Eine Session wartet' : `${esc(waiting)} Sessions warten`;
    const rest = busy > 0 ? ` ${esc(busy)} ${busy === 1 ? 'rechnet' : 'rechnen'} weiter.` : '';
    return `<span class="br-agents-txt">${who} auf deine Antwort.${rest}</span><button class="br-agents-btn" type="button" data-act="close">Pause beenden</button>`;
  }
  if (busy > 0) {
    const verb = busy === 1 ? 'Agent rechnet' : 'Agents rechnen';
    return `<span class="br-agents-txt">${esc(busy)} ${verb} noch, keiner wartet auf dich.</span>`;
  }
  return '<span class="br-agents-txt">Gerade rechnet nichts und nichts wartet auf dich.</span>';
}

// Während der Pause soll nichts zu lesen dasein — nur wenn wirklich jemand auf
// eine Antwort wartet, ist der Hinweis die Unterbrechung wert.
function renderSheetData(a) {
  const waiting = Math.round(num(a?.waiting));
  const sig = String(waiting);
  if (sig === sheetSig) return;
  sheetSig = sig;
  setHtml(agentsEl, waiting > 0 ? agentsHtml(a) : '');
  agentsEl.classList.toggle('br-waiting', waiting > 0);
}

function indicatorText(a) {
  const level = a?.level;
  if (level === 'resting') return `Pause läuft · ${fmtDur(a?.restingSecs)}`;
  if (a?.snoozed) {
    const next = Math.round(num(a?.nextDueSecs));
    return next > 0 ? `vertagt · wieder in ${fmtDur(next)}` : 'vertagt';
  }
  // Solange noch Zeit ist, ist die Restzeit die nützlichere Zahl; danach
  // sagt die Dauer am Stück mehr.
  const next = Math.round(num(a?.nextDueSecs));
  if (next > 0) return `Pause in ${fmtDur(next)}`;
  return `${fmtDur(a?.workedSecs)} am Stück`;
}

function renderIndicator(a, force) {
  if (!ind) return;
  const level = typeof a?.level === 'string' ? a.level : 'none';
  const on = !!a && a.enabled !== false && cfg.enabled !== false && level !== 'none';
  const txt = on ? indicatorText(a) : '';
  const good = on && !!a.goodMoment && level !== 'resting';
  const snoozed = on && !!a.snoozed && level !== 'resting';
  const cta = level === 'resting' ? 'Öffnen' : 'Pause';
  const sig = [on, level, txt, good, snoozed, cta, a?.message].join('|');
  if (!force && sig === indSig) return;
  indSig = sig;

  ind.classList.toggle('br-on', on);
  if (!on) return;
  ind.classList.toggle('br-l-hint', level === 'hint');
  ind.classList.toggle('br-l-due', level === 'due');
  ind.classList.toggle('br-l-overdue', level === 'overdue');
  ind.classList.toggle('br-l-resting', level === 'resting');
  ind.classList.toggle('br-good', good);
  ind.classList.toggle('br-snoozed', snoozed);
  setText(indTxt, txt);
  setText(indCta, cta);
  indGood.hidden = !good || snoozed;
  indCta.hidden = snoozed;
  indX.hidden = snoozed || level === 'resting';
  ind.querySelector('.br-ind-main').title = String(a.message || '');
}

function renderLengths() {
  if (!lensEl) return;
  const opts = [...new Set([...QUICK_LENGTHS, configuredMinutes()])].sort((a, b) => a - b);
  lensEl.innerHTML = opts
    .map(m => `<button class="br-len" type="button" data-act="len" data-min="${m}">${m} Min.</button>`)
    .join('');
  lenEls = [...lensEl.querySelectorAll('.br-len')];
  for (const el of lenEls) el.classList.toggle('br-len-on', num(el.dataset.min) === minutes);
}

function configuredMinutes() {
  return Math.min(120, Math.max(1, Math.round(num(cfg.breakMins, DEFAULT_MINUTES))));
}

function setLength(mins) {
  const m = Math.min(120, Math.max(1, Math.round(num(mins, DEFAULT_MINUTES))));
  minutes = m;
  deadline = startedAt + m * 60000;
  lastBarPct = -1;
  for (const el of lenEls) el.classList.toggle('br-len-on', num(el.dataset.min) === m);
  if (open) frame(Date.now());
}

function updateBreath(now) {
  const elapsed = (now - breathStart) / 1000;
  const pos = elapsed % BREATH_CYCLE;
  const phase = pos < BREATH_IN ? 0 : pos < BREATH_IN + BREATH_HOLD ? 1 : 2;
  const left = Math.max(1, Math.ceil(PHASE_ENDS[phase] - pos));
  setText(phaseEl, reducedMotion ? `${PHASES[phase]} · ${left} s` : PHASES[phase]);
  const n = Math.floor(elapsed / BREATH_CYCLE) + 1;
}

function updateCountdown(now) {
  const left = Math.max(0, Math.round((deadline - now) / 1000));
  const total = Math.max(1, minutes * 60);
  const pct = Math.max(0, Math.min(100, Math.round(((total - left) / total) * 100)));
  if (pct !== lastBarPct) {
    lastBarPct = pct;
    barEl.style.width = pct + '%';
  }
  const done = left <= 0;
  if (done !== doneShown) {
    doneShown = done;
    sheet.classList.toggle('br-done', done);
    setText(primaryEl, done ? 'Weiter geht’s' : 'Zurück an die Arbeit');
    // Damit er wegschauen kann, ohne auf die Uhr zu sehen.
    if (done) {
      try { cb.onFinished?.(); } catch { /* ohne Benachrichtigung ist es nur stiller */ }
    }
  }
}

function frame(now) {
  updateBreath(now);
  updateCountdown(now);
}

function loop() {
  if (!open) {
    raf = 0;
    return;
  }
  raf = requestAnimationFrame(loop);
  const now = Date.now();
  if (now - lastTick < TICK_MS) return;
  lastTick = now;
  frame(now);
}

function focusables() {
  return [...sheet.querySelectorAll('button, input, [tabindex]:not([tabindex="-1"])')]
    .filter(el => !el.disabled && !el.hidden && el.offsetParent !== null);
}

function onKeyDown(e) {
  if (!open) return;
  if (e.key === 'Escape') {
    e.preventDefault();
    e.stopPropagation();
    closeBreak();
    return;
  }
  if (e.key !== 'Tab') return;
  const items = focusables();
  if (!items.length) return;
  const first = items[0];
  const last = items[items.length - 1];
  const cur = document.activeElement;
  const inside = sheet.contains(cur);
  if (e.shiftKey && (!inside || cur === first)) {
    e.preventDefault();
    last.focus();
  } else if (!e.shiftKey && (!inside || cur === last)) {
    e.preventDefault();
    first.focus();
  }
}

function closeBreak() {
  if (settingsOnly) {
    settingsOnly = false;
    overlay.classList.remove('br-on', 'br-cfg-only');
    settingsEl.setAttribute('hidden', '');
    const back = prevFocus;
    prevFocus = null;
    if (back && typeof back.focus === 'function' && document.contains(back)) back.focus();
    return;
  }
  if (!open) return;
  open = false;
  const secs = Math.max(0, Math.round((Date.now() - startedAt) / 1000));
  clearTimeout(quietTimer);
  quietTimer = 0;
  overlay.classList.remove('br-on', 'br-quiet');
  orbEl.classList.remove('br-anim');
  settingsEl.setAttribute('hidden', '');
  if (raf) {
    cancelAnimationFrame(raf);
    raf = 0;
  }
  const back = prevFocus;
  prevFocus = null;
  if (back && typeof back.focus === 'function' && document.contains(back)) back.focus();
  try { cb.onEnd?.(secs); } catch { /* Backend zählt beim nächsten Refresh nach */ }
}

export function mountBreaks(opts) {
  if (mounted) return;
  mounted = true;
  cb = opts || {};
  if (cb.config && typeof cb.config === 'object') cfg = { ...cfg, ...cb.config };
  buildIndicator();
  buildOverlay();
  fillSettings();
  window.addEventListener('keydown', onKeyDown, true);
}

export function updateBreaks(next) {
  advice = next && typeof next === 'object' ? next : null;
  renderIndicator(advice, false);
  if (open) renderSheetData(advice);
}

export function openBreak(mins) {
  if (!mounted) return;
  if (open) {
    if (mins) setLength(Math.round(num(mins, DEFAULT_MINUTES)));
    return;
  }
  open = true;
  startedAt = Date.now();
  breathStart = startedAt;
  lastTick = startedAt;
  doneShown = false;
  lastBarPct = -1;
  sheetSig = '';
  prevFocus = document.activeElement;

  setText(impulseEl, nextImpulse());
  // Der Impuls ist zum Loslegen da, nicht zum Lesen — danach bleibt nur der Atem.
  overlay.classList.remove('br-quiet');
  clearTimeout(quietTimer);
  quietTimer = setTimeout(() => overlay?.classList.add('br-quiet'), 9000);
  renderSheetData(advice);
  setLength(mins ? Math.round(num(mins, DEFAULT_MINUTES)) : configuredMinutes());
  renderLengths();
  setText(primaryEl, 'Zurück an die Arbeit');
  sheet.classList.remove('br-done');
  overlay.classList.add('br-on');

  orbEl.classList.remove('br-anim');
  void orbEl.offsetWidth; // erzwingt den Neustart der Atem-Keyframes, sonst läuft die alte Phase weiter
  orbEl.classList.add('br-anim');

  frame(Date.now());
  if (!raf) raf = requestAnimationFrame(loop);
  sheet.focus({ preventScroll: true });
  try { cb.onTake?.(); } catch { /* Backend erfährt es beim nächsten Refresh */ }
}

// Die Einstellungen müssen auch erreichbar sein, ohne dafür eine Pause zu
// starten — sonst findet man sie nicht.
export function openBreakSettings() {
  if (!mounted) return;
  if (open) {
    settingsEl.removeAttribute('hidden');
    fillSettings();
    return;
  }
  settingsOnly = true;
  overlay.classList.add('br-cfg-only');
  overlay.classList.add('br-on');
  prevFocus = document.activeElement;
  sheetSig = '';
  renderSheetData(advice);
  settingsEl.removeAttribute('hidden');
  fillSettings();
  setText(primaryEl, 'Fertig');
  sheet.focus({ preventScroll: true });
}

export function isBreakOpen() {
  return open;
}
