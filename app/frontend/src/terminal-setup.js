// Gemeinsame Terminal-Grundlagen für die Agentenansicht und das Terminal-Dock.
//
// Zwei Dinge entscheiden darüber, ob das eingebettete Terminal wie ein Teil der
// Anwendung aussieht oder wie ein hineinkopiertes Fremdfenster:
//
// 1. Der Renderer. xterm.js zeichnet ohne Addon über das DOM, und der
//    DOM-Renderer kennt `customGlyphs` nicht. Claude Code baut seine gesamte
//    Oberfläche aus Rahmenzeichen, die dann aus der Schrift kommen und bei
//    jeder Zeilenhöhe über 1 in Striche zerfallen. Der WebGL-Renderer zeichnet
//    diese Glyphen selbst und hält die Linien durchgehend.
// 2. Die Schrift. xterm.js misst die Zellenbreite beim Öffnen. Ist die eigene
//    Schrift zu diesem Zeitpunkt noch nicht geladen, misst es die Ersatzschrift
//    und das Raster sitzt für den Rest der Sitzung schief.

import { WebglAddon } from '@xterm/addon-webgl';

export const TERMINAL_FONT = 'ui-monospace, SFMono-Regular, "Commit Mono", Menlo, monospace';

// Gemeinsame Optionen beider Terminal-Oberflächen. Größe und Zeilenhöhe setzen
// die Aufrufer, weil die Agentenansicht und das Raster unterschiedlich dicht sind.
export const TERMINAL_OPTIONS = {
  fontFamily: TERMINAL_FONT,
  fontWeight: 400,
  fontWeightBold: 700,
  scrollback: 20000,
  scrollSensitivity: 5,
  fastScrollSensitivity: 12,
  cursorBlink: true,
  cursorInactiveStyle: 'outline',
  macOptionIsMeta: true,
  // Fett soll schwerer wirken, nicht die Farbe wechseln. Sonst rutscht jede
  // Überschrift der Agenten in eine andere Palettenstufe als vorgesehen.
  drawBoldTextInBrightColors: false,
  rescaleOverlappingGlyphs: true,
};

const fontReady = (document.fonts
  ? Promise.all([
      document.fonts.load('400 14px "Commit Mono"'),
      document.fonts.load('700 14px "Commit Mono"'),
      document.fonts.load('italic 400 14px "Commit Mono"'),
    ]).then(() => document.fonts.ready)
  : Promise.resolve()
).catch(() => { /* Ohne die Schrift greift der Ersatzstapel. */ });

// Nach `term.open()` aufrufen: hängt den WebGL-Renderer ein und misst das
// Raster neu, sobald die Schrift wirklich da ist.
export function setUpTerminal(term, refit) {
  try {
    const webgl = new WebglAddon();
    // Verliert der Kontext (Ruhezustand, GPU-Wechsel), fällt xterm.js von
    // selbst auf das DOM zurück, sobald das Addon entladen ist.
    webgl.onContextLoss(() => webgl.dispose());
    term.loadAddon(webgl);
  } catch {
    // Ohne WebGL bleibt der DOM-Renderer, das Terminal funktioniert weiter.
  }
  fontReady.then(() => {
    if (!term.element) return;
    term.clearTextureAtlas?.();
    refit?.();
  });
}
