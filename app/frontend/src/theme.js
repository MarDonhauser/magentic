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

const TERMINAL_THEMES = {
  dark: {
    background: '#282d35', foreground: '#dbe0e6', cursor: '#5eead4', cursorAccent: '#282d35',
    selectionBackground: 'rgba(55,207,189,0.30)', selectionForeground: '#f2f5f7',
    black: '#414852', red: '#e06c75', green: '#98c379', yellow: '#e5c07b',
    blue: '#61afef', magenta: '#c678dd', cyan: '#56b6c2', white: '#dbe0e6',
    brightBlack: '#77818d', brightRed: '#ef7d86', brightGreen: '#add18d', brightYellow: '#efd08c',
    brightBlue: '#78bdf5', brightMagenta: '#d58be8', brightCyan: '#70c7d1', brightWhite: '#f2f5f7',
  },
  light: {
    background: '#fcfcfd', foreground: '#30343b', cursor: '#178f83', cursorAccent: '#fcfcfd',
    selectionBackground: 'rgba(23,143,131,0.18)', selectionForeground: '#1f252b',
    black: '#4f5964', red: '#bb4651', green: '#287a50', yellow: '#91631b',
    blue: '#356fae', magenta: '#7d5aa2', cyan: '#167f82', white: '#727b86',
    brightBlack: '#8a939d', brightRed: '#d1545f', brightGreen: '#329665', brightYellow: '#ad7825',
    brightBlue: '#4b84c3', brightMagenta: '#956db8', brightCyan: '#229b9e', brightWhite: '#1f252b',
  },
};

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
  return { ...TERMINAL_THEMES[normaliseTheme(theme)] };
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
