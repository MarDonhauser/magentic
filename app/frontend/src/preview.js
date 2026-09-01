// Nur zur Sichtprüfung: rendert die alte und die neue Terminal-Darstellung
// nebeneinander durch denselben Code-Pfad wie die Anwendung.
import { Terminal } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import './style.css';
import { terminalTheme, applyTheme } from './theme.js';
import { TERMINAL_OPTIONS, setUpTerminal } from './terminal-setup.js';
import { CLAUDE_SAMPLE, DEMO_SAMPLE, BOX_SAMPLE } from './preview-sample.js';

const OLD_THEME = {
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
    blue: '#356fae', magenta: '#7d5aa2', cyan: '#167f82', white: '#68737f',
    brightBlack: '#66717d', brightRed: '#b83246', brightGreen: '#1f754b', brightYellow: '#875500',
    brightBlue: '#2166ad', brightMagenta: '#764b99', brightCyan: '#087578', brightWhite: '#1f252b',
  },
};

const params = new URLSearchParams(location.search);
const mode = params.get('theme') === 'light' ? 'light' : 'dark';
applyTheme(mode, { notify: false });

function mount(hostId, variant) {
  const host = document.getElementById(hostId);
  const term = new Terminal(variant === 'neu'
    ? { ...TERMINAL_OPTIONS, fontSize: 14, lineHeight: 1.25, theme: terminalTheme(mode) }
    : {
        fontSize: 14, lineHeight: 1.3,
        fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
        cursorBlink: false, theme: OLD_THEME[mode],
      });
  term.open(host);
  if (variant === 'neu') { term.options.cursorBlink = false; setUpTerminal(term, () => {}); }
  term.write(CLAUDE_SAMPLE + DEMO_SAMPLE + BOX_SAMPLE);
  return term;
}

mount('alt', 'alt');
mount('neu', 'neu');
