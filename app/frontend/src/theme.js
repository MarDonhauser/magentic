import {
  WindowSetBackgroundColour,
  WindowSetDarkTheme,
  WindowSetLightTheme,
} from '../wailsjs/runtime/runtime';

const STORAGE_KEY = 'magentic.theme';
const THEME_EVENT = 'magentic:themechange';
const THEMES = new Set(['light', 'dark']);
const listeners = new Set();
const controls = new Set();

const WINDOW_COLORS = {
  dark: { hex: '#20242b', rgba: [32, 36, 43, 255] },
  light: { hex: '#f7f8fa', rgba: [247, 248, 250, 255] },
};

// Das Terminal ist kein Gastfenster, sondern die Inhaltsfläche der Anwendung.
// Jede ANSI-Farbe ist deshalb die Projektion einer Rolle aus style.css und keine
// eigenständige Terminal-Palette. Die hellen Varianten entstehen aus derselben
// Farbe in OKLCH: im Dunklen eine Stufe heller bei leicht reduzierter Sättigung,
// im Hellen eine Stufe dunkler. ANSI 0 trägt die Struktur (--grid bzw. --ink),
// ANSI 8 den gedämpften Text (--muted). Alle Werte außer ANSI 0 im Dunklen
// erreichen mindestens 4.5:1 auf dem jeweiligen Terminal-Hintergrund.
const TERMINAL_THEMES = {
  dark: {
    background: '#282d35',                              // --term-bg
    foreground: '#e4e8ee',                              // --ink
    cursor: '#37cfbd',                                  // --accent
    cursorAccent: '#282d35',
    selectionBackground: 'rgba(55,207,189,0.26)',       // --accent
    selectionForeground: '#f3f7fd',
    black: '#3a4149',                                   // --grid
    // --critical ist gegen --page abgestimmt und verliert auf dem helleren
    // Terminal-Grund Kontrast; eine OKLCH-Stufe heller bringt es auf 4.59:1.
    red: '#e57179',                                     // --critical +1
    green: '#98c379',                                   // --good
    yellow: '#e0b25e',                                  // --warning
    blue: '#5eb7e8',                                    // --info
    magenta: '#c678dd',                                 // --graph-series-1
    cyan: '#37cfbd',                                    // --accent
    white: '#e4e8ee',                                   // --ink
    brightBlack: '#909aa6',                             // --muted
    brightRed: '#fa8d92', brightGreen: '#b2da96', brightYellow: '#f6cb7f',
    brightBlue: '#7ecffd', brightMagenta: '#dc93f1', brightCyan: '#67e6d4',
    brightWhite: '#f3f7fd',
  },
  light: {
    background: '#fcfcfd',                              // --term-bg
    foreground: '#272c33',                              // --ink
    cursor: '#117a70',                                  // --accent
    cursorAccent: '#fcfcfd',
    selectionBackground: 'rgba(17,122,112,0.16)',       // --accent
    selectionForeground: '#1f252b',
    // Auf hellem Grund kann ANSI 7 kein Weiß sein; die beiden hellen Stufen
    // tragen hier den lesbaren Sekundär- und den maximalen Kontrast.
    black: '#272c33',                                   // --ink
    red: '#bb4651',                                     // --critical
    green: '#287a50',                                   // --good
    yellow: '#91631b',                                  // --warning
    blue: '#356fae',                                    // --info
    magenta: '#8754a6',                                 // --graph-series-1
    cyan: '#117a70',                                    // --accent
    white: '#59636f',                                   // --ink-2
    brightBlack: '#68737f',                             // --muted
    brightRed: '#a02738', brightGreen: '#006238', brightYellow: '#784a00',
    brightBlue: '#165695', brightMagenta: '#6f3a8d', brightCyan: '#006258',
    brightWhite: '#272c33',                             // --ink
  },
};

// Claude Code zeichnet seine Oberfläche nicht aus den sechzehn ANSI-Farben,
// sondern aus dem festen 256er-Würfel: in rund 2900 Zeichen aus vier laufenden
// Sitzungen stammte jede einzelne Farbe aus dem Bereich 16-255. Die Palette oben
// trägt damit die Shell im Dock und alles, was einfaches ANSI schreibt; für die
// Agentenansicht entscheidet der Würfel.
//
// Der Farbwürfel 16-231 bleibt unverändert, das sind fremde Produktfarben. Nur
// die Graustufenrampe 232-255 läuft neu auf der Neutralachse dieser Oberfläche
// (OKLCH-Farbton -103°, gemittelt über --page bis --ink). Die Helligkeit jeder
// Stufe bleibt exakt erhalten, der Kontrast also auch; die Rahmen und der
// gedämpfte Text der Agenten stehen danach im selben Blaugrau wie die Anwendung
// statt in einem neutralen Grau, das zu nichts im Fenster gehört. Lesbar macht
// das den hellen Modus nicht — dafür sorgt terminalContrastFloor().
const GREY_RAMP = [
  '#06080b', '#0f1217', '#181c22', '#22262d', '#2b3138', '#343b43',
  '#3e454e', '#484f59', '#515963', '#5b636d', '#656d78', '#6f7782',
  '#79818b', '#838b95', '#8e959f', '#989fa8', '#a2a9b1', '#adb3bb',
  '#b7bdc4', '#c2c7cd', '#cdd0d6', '#d7dadf', '#e2e4e7', '#edeef0',
];

const EXTENDED_ANSI = (() => {
  const step = v => (v === 0 ? 0 : 55 + 40 * v);
  const hex = v => v.toString(16).padStart(2, '0');
  const cube = [];
  for (let r = 0; r < 6; r++)
    for (let g = 0; g < 6; g++)
      for (let b = 0; b < 6; b++)
        cube.push('#' + hex(step(r)) + hex(step(g)) + hex(step(b)));
  return [...cube, ...GREY_RAMP];
})();

let initialised = false;

function storedTheme() {
  try {
    const value = localStorage.getItem(STORAGE_KEY);
    return THEMES.has(value) ? value : null;
  } catch {
    return null;
  }
}

function systemTheme() {
  return window.matchMedia?.('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
}

function normaliseTheme(theme) {
  return THEMES.has(theme) ? theme : systemTheme();
}

function syncControl(control, theme) {
  const dark = theme === 'dark';
  control.setAttribute('aria-pressed', String(dark));
  control.title = dark ? 'Zum hellen Modus wechseln' : 'Zum dunklen Modus wechseln';
}

function syncNativeWindow(theme) {
  const windowColor = WINDOW_COLORS[theme];
  try {
    if (theme === 'dark') WindowSetDarkTheme();
    else WindowSetLightTheme();
    WindowSetBackgroundColour(...windowColor.rgba);
  } catch {
    // Im Browser-Preview ist die Wails-Runtime nicht vorhanden.
  }
}

export function currentTheme() {
  return normaliseTheme(document.documentElement.dataset.theme || storedTheme());
}

export function terminalTheme(theme = currentTheme()) {
  return { ...TERMINAL_THEMES[normaliseTheme(theme)], extendedAnsi: [...EXTENDED_ANSI] };
}

// Der 256er-Würfel, aus dem Claude Code malt, ist für ein schwarzes Terminal
// entworfen. Auf unserem dunklen Grund geht das auf: der schwächste Wert, den
// die Agenten tatsächlich benutzen, ist Grau 244 mit 3.51:1, und das ist eine
// bewusst gedämpfte Rahmenfarbe — sie anzuheben würde Claudes eigene Hierarchie
// einebnen. Auf hellem Grund bricht dagegen fast alles weg: Gelb 220 liegt bei
// 1.37:1, Grün 114 bei 1.69:1, das Logo-Rosa bei 2.57:1.
//
// Diese 240 Farben lassen sich nicht sinnvoll pro Theme umschreiben, weil der
// Würfel auch Hintergründe trägt. xterm.js kann stattdessen die Vordergrundfarbe
// je Zelle gegen den echten Hintergrund anheben, und nur dort, wo sie durchfällt.
// Deshalb bleibt der Boden im Dunklen aus und greift nur im Hellen.
export function terminalContrastFloor(theme = currentTheme()) {
  return normaliseTheme(theme) === 'light' ? 4.5 : 1;
}

export function applyTheme(theme, { persist = false, notify = true } = {}) {
  const next = normaliseTheme(theme);
  const root = document.documentElement;
  const changed = root.dataset.theme !== next;

  root.dataset.theme = next;
  root.style.colorScheme = next;
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', WINDOW_COLORS[next].hex);
  for (const control of controls) syncControl(control, next);
  syncNativeWindow(next);

  if (persist) {
    try { localStorage.setItem(STORAGE_KEY, next); } catch { /* Theme bleibt für diese Sitzung aktiv. */ }
  }

  if (notify && changed) {
    for (const listener of listeners) listener(next);
    document.dispatchEvent(new CustomEvent(THEME_EVENT, { detail: { theme: next } }));
  }
  return next;
}

export function onThemeChange(listener) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function initThemeToggle(control) {
  if (control && !controls.has(control)) {
    controls.add(control);
    control.addEventListener('click', () => {
      applyTheme(currentTheme() === 'dark' ? 'light' : 'dark', { persist: true });
    });
  }

  const initial = applyTheme(currentTheme(), { notify: false });
  if (initialised) return initial;
  initialised = true;

  const media = window.matchMedia?.('(prefers-color-scheme: dark)');
  media?.addEventListener?.('change', event => {
    if (!storedTheme()) applyTheme(event.matches ? 'dark' : 'light');
  });

  window.addEventListener('storage', event => {
    if (event.key !== STORAGE_KEY) return;
    applyTheme(THEMES.has(event.newValue) ? event.newValue : systemTheme(), { notify: true });
  });

  return initial;
}
