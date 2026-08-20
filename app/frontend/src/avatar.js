import './avatar.css';
import { sessionToolCandidates } from './session-tool.js';

const cache = new Map();

function scramble(h) {
  h ^= h >>> 16;
  h = Math.imul(h, 2246822507);
  h ^= h >>> 13;
  h = Math.imul(h, 3266489909);
  h ^= h >>> 16;
  return h >>> 0;
}

function hashOf(name, seed) {
  let h = seed;
  for (let i = 0; i < name.length; i++) {
    h ^= name.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return scramble(h);
}

function palette(name) {
  const c = hashOf(name, 0x811c9dc5);
  const hue = c % 360;
  const sat = 58 + ((c >>> 10) % 20);
  const lit = 57 + ((c >>> 17) % 11);
  const acc = (hue + 115 + ((c >>> 23) % 130)) % 360;
  return {
    plate: `hsl(${hue},32%,17%)`,
    line: `hsl(${hue},48%,27%)`,
    shell: `hsl(${hue},${sat}%,${lit}%)`,
    glow: `hsl(${hue},${sat}%,${lit + 13}%)`,
    deep: `hsl(${hue},${sat - 12}%,${lit - 15}%)`,
    accent: `hsl(${acc},90%,67%)`,
    ink: `hsl(${hue},55%,15%)`,
  };
}

const HEADS = [
  { parts: [[3, 3, 10, 9]], cuts: [], fx: 4, fw: 8, ey: 6, my: 9 },
  {
    parts: [[3, 3, 10, 9]],
    cuts: [[3, 3, 1, 1], [12, 3, 1, 1], [3, 11, 1, 1], [12, 11, 1, 1]],
    fx: 4, fw: 8, ey: 6, my: 9,
  },
  { parts: [[2, 4, 12, 8]], cuts: [[2, 4, 1, 1], [13, 4, 1, 1]], fx: 3, fw: 10, ey: 6, my: 9 },
  { parts: [[4, 3, 8, 9]], cuts: [], fx: 5, fw: 6, ey: 6, my: 9 },
  { parts: [[3, 3, 10, 7], [5, 10, 6, 2]], cuts: [], fx: 4, fw: 8, ey: 5, my: 8 },
  { parts: [[3, 4, 10, 8], [5, 3, 6, 1]], cuts: [], fx: 4, fw: 8, ey: 6, my: 9 },
  {
    parts: [[3, 3, 10, 9]],
    cuts: [[3, 3, 2, 1], [11, 3, 2, 1], [3, 4, 1, 1], [12, 4, 1, 1], [3, 11, 2, 1], [11, 11, 2, 1]],
    fx: 4, fw: 8, ey: 6, my: 9,
  },
  { parts: [[2, 3, 12, 6], [3, 9, 10, 3]], cuts: [], fx: 3, fw: 10, ey: 5, my: 9 },
];

function build(name) {
  const p = palette(name);
  const f = hashOf(name, 0x2f1b3c7d);
  const head = HEADS[f & 7];
  const antenna = (f >>> 3) & 7;
  const eyes = (f >>> 6) & 7;
  const mouth = (f >>> 9) & 7;
  const chest = (f >>> 12) & 7;
  const ears = (f >>> 15) & 3;

  const ops = [];
  const px = (c, x, y, w, h) => { if (w > 0 && h > 0) ops.push(c, x, y, w, h); };
  const sym = (c, x, y, w, h) => { px(c, x, y, w, h); px(c, 16 - x - w, y, w, h); };

  const { fx, fw, ey, my } = head;
  const ty = Math.min(...head.parts.map(q => q[1]));
  const hx = Math.min(...head.parts.map(q => q[0]));
  const ew = fw >= 8 ? 3 : 2;
  const mx = fx + 1;
  const mw = fw - 2;

  px(p.plate, 0, 0, 16, 16);

  px(p.line, 5, 11, 6, 2);
  px(p.line, 3, 13, 10, 1);
  px(p.line, 0, 14, 16, 2);
  for (const q of head.parts) px(p.line, q[0] - 1, q[1] - 1, q[2] + 2, q[3] + 2);

  px(p.deep, 6, 12, 4, 1);
  px(p.deep, 4, 13, 8, 1);
  px(p.deep, 2, 14, 12, 1);
  px(p.deep, 1, 15, 14, 1);

  for (const q of head.parts) px(p.shell, q[0], q[1], q[2], q[3]);
  const top = head.parts.reduce((a, b) => (b[1] < a[1] ? b : a));
  px(p.glow, top[0] + 1, top[1], top[2] - 2, 1);
  for (const q of head.cuts) px(p.plate, q[0], q[1], q[2], q[3]);

  if (antenna === 1) {
    px(p.shell, 7, ty - 2, 2, 2);
    px(p.accent, 6, ty - 3, 4, 1);
  } else if (antenna === 2) {
    sym(p.shell, 5, ty - 2, 1, 2);
    sym(p.accent, 5, ty - 3, 1, 1);
  } else if (antenna === 3) {
    px(p.shell, 7, ty - 1, 2, 1);
    px(p.shell, 6, ty - 2, 4, 1);
    px(p.accent, 4, ty - 3, 8, 1);
  } else if (antenna === 4) {
    sym(p.accent, 4, ty - 2, 1, 2);
  } else if (antenna === 5) {
    px(p.glow, 4, ty - 1, 8, 1);
    px(p.accent, 6, ty - 3, 4, 2);
  } else if (antenna === 6) {
    sym(p.shell, 3, ty - 1, 2, 1);
    sym(p.accent, 3, ty - 2, 2, 1);
  } else if (antenna === 7) {
    px(p.deep, 5, ty - 1, 6, 1);
    px(p.accent, 7, ty - 3, 2, 2);
  }

  if (ears === 1) {
    sym(p.deep, hx - 2, ey + 1, 2, 2);
  } else if (ears === 2) {
    sym(p.deep, hx - 2, ey, 1, 4);
  } else if (ears === 3) {
    sym(p.deep, hx - 2, ey - 1, 2, 4);
    sym(p.accent, hx - 2, ey, 1, 2);
  }

  if (eyes === 0) {
    sym(p.accent, fx, ey, 2, 2);
  } else if (eyes === 1) {
    sym(p.ink, fx, ey, ew, 1);
    sym(p.accent, fx, ey + 1, ew, 1);
  } else if (eyes === 2) {
    px(p.ink, fx, ey, fw, 2);
    sym(p.accent, fx + 1, ey, 2, 1);
  } else if (eyes === 3) {
    px(p.ink, 5, ey - 1, 6, 4);
    px(p.accent, 6, ey, 4, 2);
    px(p.ink, 7, ey, 2, 1);
  } else if (eyes === 4) {
    sym(p.ink, fx, ey, ew, 2);
    sym(p.accent, fx + 1, ey, 1, 1);
    px(p.ink, fx + ew, ey + 1, fw - 2 * ew, 1);
  } else if (eyes === 5) {
    sym(p.accent, fx, ey, ew, 1);
    sym(p.accent, fx, ey + 1, ew - 1, 1);
  } else if (eyes === 6) {
    sym(p.accent, fx, ey - 1, ew, 3);
    sym(p.ink, fx + 1, ey, 1, 1);
  } else {
    sym(p.accent, fx, ey, ew, 2);
    sym(p.ink, fx + ew - 1, ey, 1, 1);
  }

  if (mouth === 0) {
    px(p.accent, mx, my, mw, 2);
    for (let i = 1; i < mw / 2; i += 2) sym(p.ink, mx + i, my, 1, 2);
  } else if (mouth === 1) {
    px(p.ink, mx, my + 1, mw, 1);
  } else if (mouth === 2) {
    px(p.ink, mx, my, mw, 2);
    sym(p.accent, mx, my, 1, 1);
    sym(p.accent, mx + 1, my + 1, 1, 1);
  } else if (mouth === 3) {
    for (let i = 0; i < mw; i++) px(p.ink, mx + i, my + (i & 1), 1, 1);
  } else if (mouth === 4) {
    px(p.ink, mx + 1, my, mw - 2, 2);
    px(p.accent, mx + 2, my + 1, mw - 4, 1);
  } else if (mouth === 5) {
    px(p.ink, mx + 1, my + 1, mw - 2, 1);
    sym(p.ink, mx, my, 1, 1);
  } else if (mouth === 6) {
    for (let i = 0; i < mw / 2; i += 2) sym(p.ink, mx + i, my, 1, 2);
  } else {
    px(p.accent, mx + 1, my, mw - 2, 1);
  }

  if (chest === 0) {
    sym(p.accent, 4, 14, 2, 1);
  } else if (chest === 1) {
    px(p.ink, 3, 15, 10, 1);
  } else if (chest === 2) {
    px(p.ink, 4, 14, 8, 2);
    px(p.accent, 5, 14, 6, 1);
  } else if (chest === 3) {
    sym(p.accent, 3, 14, 1, 1);
    px(p.accent, 7, 14, 2, 1);
  } else if (chest === 4) {
    sym(p.ink, 3, 14, 1, 2);
    sym(p.ink, 5, 14, 1, 2);
  } else if (chest === 5) {
    px(p.ink, 6, 14, 4, 2);
    px(p.accent, 7, 14, 2, 2);
  } else if (chest === 6) {
    px(p.accent, 7, 14, 2, 1);
    px(p.accent, 5, 15, 6, 1);
  } else {
    px(p.ink, 2, 15, 12, 1);
  }

  return ops;
}

export function avatarColor(name) {
  return palette(name).shell;
}

export function robotAvatar(name, size = 22) {
  const key = name + '|' + size;
  const hit = cache.get(key);
  if (hit) return hit;

  const ops = build(name);
  let out = `<svg class="rbt" width="${size}" height="${size}" viewBox="0 0 16 16" shape-rendering="crispEdges" xmlns="http://www.w3.org/2000/svg">`;
  let cur = null;
  for (let i = 0; i < ops.length; i += 5) {
    if (ops[i] !== cur) {
      if (cur !== null) out += '</g>';
      cur = ops[i];
      out += `<g fill="${cur}">`;
    }
    out += `<rect x="${ops[i + 1]}" y="${ops[i + 2]}" width="${ops[i + 3]}" height="${ops[i + 4]}"/>`;
  }
  out += (cur !== null ? '</g>' : '') + '</svg>';

  cache.set(key, out);
  return out;
}

/*
 * Official developer-icons v7.1.0 artwork, vendored as static SVG markup so
 * the Wails frontend stays framework-free and works without a network.
 * Source: https://github.com/xandemon/developer-icons
 *
 * MIT License
 * Copyright (c) 2024 Sandesh Katwal aka xandemon
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */
const DEVELOPER_ICON_SVG = Object.freeze({
  claude: "<svg xmlns=\"http://www.w3.org/2000/svg\" style=\"flex:none;line-height:1\" viewBox=\"0 0 24 24\"><path fill=\"#D97757\" d=\"m4.709 15.955 4.72-2.647.08-.23-.08-.128H9.2l-.79-.048-2.698-.073-2.339-.097-2.266-.122-.571-.121L0 11.784l.055-.352.48-.321.686.06 1.52.103 2.278.158 1.652.097 2.449.255h.389l.055-.157-.134-.098-.103-.097-2.358-1.596-2.552-1.688-1.336-.972-.724-.491-.364-.462-.158-1.008.656-.722.881.06.225.061.893.686 1.908 1.476 2.491 1.833.365.304.145-.103.019-.073-.164-.274-1.355-2.446-1.446-2.49-.644-1.032-.17-.619a3 3 0 0 1-.104-.729L6.283.134 6.696 0l.996.134.42.364.62 1.414 1.002 2.229 1.555 3.03.456.898.243.832.091.255h.158V9.01l.128-1.706.237-2.095.23-2.695.08-.76.376-.91.747-.492.584.28.48.685-.067.444-.286 1.851-.559 2.903-.364 1.942h.212l.243-.242.985-1.306 1.652-2.064.73-.82.85-.904.547-.431h1.033l.76 1.129-.34 1.166-1.064 1.347-.881 1.142-1.264 1.7-.79 1.36.073.11.188-.02 2.856-.606 1.543-.28 1.841-.315.833.388.091.395-.328.807-1.969.486-2.309.462-3.439.813-.042.03.049.061 1.549.146.662.036h1.622l3.02.225.79.522.474.638-.079.485-1.215.62-1.64-.389-3.829-.91-1.312-.329h-.182v.11l1.093 1.068 2.006 1.81 2.509 2.33.127.578-.322.455-.34-.049-2.205-1.657-.851-.747-1.926-1.62h-.128v.17l.444.649 2.345 3.521.122 1.08-.17.353-.608.213-.668-.122-1.374-1.925-1.415-2.167-1.143-1.943-.14.08-.674 7.254-.316.37-.729.28-.607-.461-.322-.747.322-1.476.389-1.924.315-1.53.286-1.9.17-.632-.012-.042-.14.018-1.434 1.967-2.18 2.945-1.726 1.845-.414.164-.717-.37.067-.662.401-.589 2.388-3.036 1.44-1.882.93-1.086-.006-.158h-.055L4.132 18.56l-1.13.146-.487-.456.061-.746.231-.243 1.908-1.312z\"/></svg>",
  git: "<svg xmlns=\"http://www.w3.org/2000/svg\" fill=\"none\" viewBox=\"0 0 100 100\"><path fill=\"#DE4C36\" d=\"M98.114 45.545 54.454 1.886a6.44 6.44 0 0 0-9.108 0l-9.066 9.067 11.5 11.5a7.65 7.65 0 0 1 7.869 1.834 7.66 7.66 0 0 1 1.817 7.916L68.55 43.289c2.682-.924 5.776-.326 7.918 1.819a7.66 7.66 0 0 1 0 10.836A7.662 7.662 0 0 1 63.96 47.61L53.623 37.272v27.202c.749.37 1.433.86 2.026 1.449a7.663 7.663 0 0 1 0 10.839 7.66 7.66 0 0 1-10.836 0 7.663 7.663 0 0 1 2.508-12.51V36.795a7.6 7.6 0 0 1-2.508-1.673 7.66 7.66 0 0 1-1.651-8.377L31.824 15.407 1.887 45.343a6.44 6.44 0 0 0 0 9.11l43.661 43.66a6.44 6.44 0 0 0 9.108 0l43.458-43.457a6.444 6.444 0 0 0 0-9.111\"/></svg>",
  bash: "<svg xmlns=\"http://www.w3.org/2000/svg\" fill=\"none\" viewBox=\"0 0 100 100\"><path fill=\"#fff\" d=\"M87.356 20.423 55.83 1.694c-3.728-2.203-8.39-2.203-12.203 0L12.102 20.423C8.372 22.626 6 26.779 6 31.27v37.458c0 4.491 2.288 8.56 6.102 10.847l31.525 18.73c1.864 1.1 3.983 1.694 6.102 1.694 2.118 0 4.237-.593 6.101-1.695l31.526-18.729c3.729-2.203 6.102-6.356 6.102-10.847V31.186c0-4.407-2.289-8.56-6.102-10.763\"/><path fill=\"#2A3238\" d=\"M87.356 20.423 55.83 1.694C53.966.592 51.847 0 49.729 0s-4.237.593-6.102 1.695L12.102 20.423C8.372 22.626 6 26.779 6 31.27v37.458c0 4.492 2.288 8.56 6.102 10.847l31.525 18.73c1.864 1.1 3.983 1.694 6.102 1.694 2.118 0 4.237-.593 6.102-1.695l31.525-18.729c3.729-2.203 6.102-6.356 6.102-10.847V31.186c0-4.407-2.288-8.56-6.102-10.763M44.729 96.27 13.203 77.542c-3.05-1.865-5-5.255-5-8.899V31.186c0-3.645 1.95-7.12 5-8.899L44.73 3.558c1.525-.932 3.22-1.355 5-1.355s3.474.508 5 1.355l31.525 18.73c2.627 1.525 4.322 4.237 4.83 7.203-1.016-2.204-3.39-2.882-6.186-1.271L55.153 46.694c-3.73 2.203-6.441 4.576-6.441 9.068v36.78c0 2.711 1.101 4.406 2.712 4.915-.509.085-1.102.17-1.695.17-1.695.084-3.475-.424-5-1.357\"/><path fill=\"#4DA925\" d=\"m79.136 72.287-7.882 4.661c-.17.085-.339.254-.339.509v2.034c0 .254.17.338.34.254l7.965-4.83c.17-.086.255-.34.255-.594v-1.78c0-.254-.17-.339-.34-.254\"/><path fill=\"#fff\" d=\"M62.356 55c.254-.17.423 0 .508.338v2.712c1.102-.424 2.119-.593 3.051-.339.17.085.254.339.17.593l-.594 2.373-.254.509c-.085.084-.085.084-.17.084-.084 0-.169.085-.254 0-.423-.085-1.356-.339-2.881.509-1.61.847-2.203 2.203-2.119 3.22 0 1.271.678 1.61 2.797 1.61 2.881.085 4.153 1.356 4.237 4.238 0 2.88-1.525 6.017-3.898 7.88l.085 2.713c0 .339-.17.678-.424.847L61 83.22c-.254.17-.424 0-.509-.338v-2.628c-1.356.594-2.712.678-3.644.34-.17-.085-.254-.34-.17-.594l.594-2.457c.085-.17.17-.424.254-.509.085-.085.085-.085.17-.085q.127-.126.254 0c.932.34 2.203.17 3.305-.423 1.526-.763 2.458-2.289 2.458-3.814 0-1.356-.763-1.95-2.543-1.95-2.288 0-4.406-.423-4.491-3.813 0-2.796 1.44-5.678 3.729-7.457V56.78c0-.34.17-.678.423-.848z\"/></svg>",
  azure: "<svg xmlns=\"http://www.w3.org/2000/svg\" fill=\"none\" viewBox=\"0 0 100 100\"><path fill=\"url(#a)\" d=\"M33.338 3.003h29.59L32.212 94.07a4.72 4.72 0 0 1-4.47 3.211H4.71a4.706 4.706 0 0 1-4.66-4.017 4.7 4.7 0 0 1 .196-2.204l28.62-84.85A4.72 4.72 0 0 1 33.337 3v.001z\"/><path fill=\"#0078D4\" d=\"M87.887 97.283h-26.57a4.72 4.72 0 0 1-3.235-1.274l-30.153-28.16a2.18 2.18 0 0 1-.54-2.386 2.17 2.17 0 0 1 2.023-1.376h46.923l11.55 33.197z\"/><path fill=\"url(#b)\" d=\"m63.035 3.003-20.714 61.09 33.845-.008 11.61 33.198H61.304a4.9 4.9 0 0 1-1.61-.292 4.8 4.8 0 0 1-1.42-.814L37.92 77.181l-5.698 16.804a5.06 5.06 0 0 1-3.875 3.298H4.725a4.708 4.708 0 0 1-4.44-6.298l28.573-84.71a4.69 4.69 0 0 1 3.02-3.045c.471-.153.965-.23 1.461-.228h29.697z\"/><path fill=\"url(#c)\" d=\"M99.752 91.06a4.73 4.73 0 0 1-.635 4.257 4.71 4.71 0 0 1-3.826 1.966H62.31a4.72 4.72 0 0 0 3.828-1.966 4.72 4.72 0 0 0 .638-4.256L38.156 6.209a4.71 4.71 0 0 0-4.463-3.206h32.979a4.72 4.72 0 0 1 2.748.883 4.74 4.74 0 0 1 1.717 2.323l28.62 84.852z\"/><defs><linearGradient id=\"a\" x1=\"44.138\" x2=\"13.386\" y1=\"9.99\" y2=\"100.834\" gradientUnits=\"userSpaceOnUse\"><stop stop-color=\"#114A8B\"/><stop offset=\"1\" stop-color=\"#0669BC\"/></linearGradient><linearGradient id=\"b\" x1=\"53.726\" x2=\"46.613\" y1=\"52.318\" y2=\"54.731\" gradientUnits=\"userSpaceOnUse\"><stop stop-opacity=\".3\"/><stop offset=\".1\" stop-opacity=\".2\"/><stop offset=\".3\" stop-opacity=\".1\"/><stop offset=\".6\" stop-opacity=\".1\"/><stop offset=\"1\" stop-opacity=\"0\"/></linearGradient><linearGradient id=\"c\" x1=\"49.8\" x2=\"83.553\" y1=\"7.34\" y2=\"97.259\" gradientUnits=\"userSpaceOnUse\"><stop stop-color=\"#3CCBF4\"/><stop offset=\"1\" stop-color=\"#2892DF\"/></linearGradient></defs></svg>",
  markdown: "<svg xmlns=\"http://www.w3.org/2000/svg\" fill=\"none\" viewBox=\"0 0 106 100\"><path stroke=\"#000\" stroke-width=\"5.051\" d=\"M8.05 20h89.9a5.05 5.05 0 0 1 5.05 5.051v49.495a5.05 5.05 0 0 1-5.05 5.05H8.05A5.05 5.05 0 0 1 3 74.546V25.05A5.05 5.05 0 0 1 8.05 20Z\"/><path fill=\"#000\" d=\"M15.626 66.97V32.627h10.101l10.101 12.626L45.93 32.627h10.1V66.97h-10.1V47.273L35.827 59.9l-10.1-12.627V66.97zm63.131 0-15.15-16.666h10.101V32.627h10.101v17.676H93.91z\"/></svg>",
  kubernetes: "<svg xmlns=\"http://www.w3.org/2000/svg\" fill=\"none\" viewBox=\"0 0 106 103\"><path fill=\"#fff\" stroke=\"#fff\" stroke-miterlimit=\"10\" stroke-width=\"5\" d=\"M94.252 24.498c-.572-1.83-1.944-3.316-3.66-4.23L55.716 3.57C54.8 3.114 53.77 3 52.855 3s-1.943 0-2.858.228l-34.878 16.81c-1.716.8-2.974 2.288-3.431 4.232L3.111 61.892a7.22 7.22 0 0 0 1.258 5.49L28.5 97.227c1.371 1.372 3.315 2.287 5.26 2.401h38.423c2.058.229 4.002-.686 5.26-2.401L101.57 67.38c1.143-1.6 1.601-3.545 1.372-5.489z\"/><path fill=\"#326DE6\" d=\"M94.252 24.498c-.572-1.83-1.944-3.316-3.66-4.23L55.716 3.57C54.8 3.114 53.77 3 52.855 3s-1.943 0-2.858.228l-34.878 16.81c-1.716.8-2.974 2.288-3.431 4.232L3.111 61.892a7.22 7.22 0 0 0 1.258 5.49L28.5 97.227c1.371 1.372 3.315 2.287 5.26 2.401h38.423c2.058.229 4.002-.686 5.26-2.401L101.57 67.38c1.143-1.6 1.601-3.545 1.372-5.489z\"/><path fill=\"#fff\" d=\"M88.877 60.406c-.114 0-.228 0-.228-.115 0-.114-.23-.114-.458-.114-.457-.114-.915-.114-1.372-.114-.229 0-.458 0-.686-.115h-.115a23 23 0 0 1-3.888-.686 1.38 1.38 0 0 1-.8-.8l-.915-.23c.457-3.315.229-6.746-.457-10.062a31 31 0 0 0-4.003-9.377l.686-.687v-.114c0-.343.115-.8.343-1.03 1.03-.914 2.059-1.6 3.202-2.286l.686-.343c.458-.229.8-.458 1.258-.686.115-.115.229-.115.343-.23.115-.113 0-.113 0-.228 1.03-.8 1.258-2.172.458-3.202-.343-.457-1.03-.8-1.601-.8-.572 0-1.144.229-1.601.572l-.114.114c-.115.114-.23.229-.344.229-.343.343-.686.686-.914 1.029-.115.229-.343.343-.458.457-.8.915-1.83 1.83-2.859 2.516-.228.114-.457.229-.686.229-.114 0-.343 0-.457-.115h-.115l-.914.572c-.915-.915-1.944-1.83-2.86-2.744-4.23-3.317-9.49-5.375-14.865-5.947l-.115-.915v.115c-.343-.229-.457-.572-.572-.915 0-1.258 0-2.516.23-3.888v-.114c0-.23.114-.458.114-.687.114-.457.114-.914.228-1.372v-.686c.115-1.143-.8-2.287-1.944-2.401-.686-.115-1.372.228-1.944.8a2.26 2.26 0 0 0-.686 1.601v.572c0 .457.114.915.229 1.372.114.229.114.457.114.686v.114c.229 1.258.229 2.516.229 3.889-.114.343-.229.686-.572.914v.23l-.114.914c-1.258.114-2.516.343-3.888.572-5.375 1.143-10.292 4.002-14.066 8.005l-.686-.458h-.114c-.115 0-.229.114-.458.114s-.457-.114-.686-.228c-1.03-.8-2.058-1.715-2.859-2.63-.114-.23-.343-.343-.457-.458-.343-.343-.572-.686-.915-1.029-.114-.114-.229-.114-.343-.229l-.114-.114c-.458-.343-1.03-.572-1.601-.572-.686 0-1.258.229-1.601.8-.686 1.03-.458 2.402.457 3.203.114 0 .114.114.114.114s.23.229.343.229c.344.228.801.457 1.258.686l.686.343c1.144.686 2.288 1.372 3.202 2.287.23.229.458.686.344 1.03v-.115l.686.686c-.115.229-.23.343-.343.572-3.545 5.603-5.032 12.236-4.003 18.754l-.915.228c0 .115-.114.115-.114.115-.114.343-.457.572-.8.8a21 21 0 0 1-3.889.686c-.228 0-.457 0-.686.115-.457 0-.915.114-1.372.114-.114 0-.229.114-.457.114-.115 0-.115 0-.23.115-1.257.229-2.057 1.372-1.829 2.63.229 1.03 1.258 1.715 2.287 1.601.23 0 .343 0 .572-.114.114 0 .114 0 .114-.115 0-.114.344 0 .458 0 .457-.114.915-.343 1.258-.457.229-.114.457-.229.686-.229h.114c1.258-.457 2.402-.8 3.774-1.029h.114c.343 0 .686.114.915.343.114 0 .114.114.114.114l1.03-.114c1.715 5.26 4.917 9.949 9.377 13.38 1.029.8 1.944 1.486 3.087 2.058l-.571.8c0 .115.114.115.114.115.229.343.229.8.114 1.143-.457 1.144-1.143 2.287-1.83 3.317v.114c-.114.229-.228.343-.457.572s-.457.686-.8 1.143c-.115.115-.115.229-.229.343 0 0 0 .115-.114.115-.572 1.143-.115 2.515.914 3.087.23.115.572.229.801.229.915 0 1.715-.572 2.173-1.372 0 0 0-.115.114-.115 0-.114.114-.228.229-.343.114-.457.343-.8.457-1.258l.229-.686c.343-1.258.915-2.401 1.486-3.545.23-.343.572-.572.915-.686.115 0 .115 0 .115-.114l.457-.915c3.202 1.258 6.518 1.83 9.949 1.83 2.058 0 4.117-.23 6.175-.8 1.258-.23 2.516-.687 3.66-1.03l.457.8c.114 0 .114 0 .114.115.343.114.686.343.915.686.572 1.143 1.144 2.287 1.487 3.545v.114l.228.686c.115.458.229.915.458 1.258.114.115.114.229.228.343 0 0 0 .115.115.115.457.8 1.258 1.372 2.173 1.372.343 0 .571-.114.914-.229.458-.228.915-.686 1.03-1.258s.114-1.143-.115-1.715c0-.114-.114-.114-.114-.114 0-.115-.114-.23-.229-.343-.228-.458-.457-.8-.8-1.144-.115-.229-.229-.343-.458-.572v-.228c-.8-1.03-1.372-2.173-1.83-3.317-.114-.343-.114-.8.115-1.143 0-.115.114-.115.114-.115l-.343-.915c5.832-3.544 10.292-9.033 12.35-15.552l.915.115c.115 0 .115-.115.115-.115.228-.228.572-.343.914-.343h.115a19 19 0 0 1 3.66 1.03h.114c.228.114.457.228.686.228.457.229.8.458 1.258.572.114 0 .228.114.457.114.114 0 .114 0 .229.115.228.114.343.114.572.114 1.029 0 1.944-.686 2.287-1.601-.115-1.258-1.03-2.173-2.059-2.401M55.83 56.86l-3.088 1.486-3.087-1.486-.8-3.317 2.172-2.744h3.43l2.173 2.744zm18.64-7.433c.571 2.401.686 4.802.457 7.204l-10.864-3.088c-1.029-.228-1.6-1.258-1.372-2.287.114-.343.229-.572.458-.8l8.576-7.776c1.258 2.058 2.173 4.345 2.745 6.747M68.293 38.45l-9.377 6.632c-.8.457-1.944.343-2.515-.457-.23-.229-.344-.458-.344-.8l-.686-11.55c5.032.571 9.492 2.744 12.922 6.175m-20.698-5.833 2.287-.457-.571 11.436c0 1.029-.915 1.83-1.944 1.83-.343 0-.572-.115-.915-.23L36.96 38.45c2.974-2.86 6.633-4.918 10.635-5.833M33.644 42.682l8.462 7.547c.8.686.915 1.83.229 2.63-.229.343-.457.458-.915.572l-11.092 3.202a24.07 24.07 0 0 1 3.316-13.951M31.7 62.007l11.321-1.944c.915 0 1.83.571 1.944 1.486.114.343.114.8-.114 1.144l-4.346 10.52c-4.002-2.63-7.204-6.632-8.805-11.206m25.958 14.18c-1.6.343-3.202.571-4.917.571-2.401 0-4.917-.457-7.204-1.143l5.603-10.178c.572-.686 1.487-.915 2.287-.457.343.228.572.457.915.8l5.49 9.95c-.687.113-1.373.228-2.174.456m13.952-9.95a21.8 21.8 0 0 1-6.862 6.862l-4.46-10.75c-.228-.914.23-1.83 1.03-2.172.343-.114.686-.229 1.029-.229l11.435 1.944c-.571 1.601-1.257 3.088-2.172 4.346\"/></svg>",
  openai: "<svg xmlns=\"http://www.w3.org/2000/svg\" fill=\"none\" viewBox=\"0 0 100 100\"><path fill=\"#193718\" d=\"M93.06 40.937c1.25-3.437 1.563-6.875 1.25-10.312-.312-3.438-1.562-6.875-3.125-10C88.373 15.937 84.31 12.187 79.623 10c-5-2.188-10.313-2.813-15.625-1.563-2.5-2.5-5.313-4.687-8.438-6.25S48.685 0 45.248 0c-5.313 0-10.625 1.562-15 4.687a24.16 24.16 0 0 0-9.063 12.5c-3.75.938-6.875 2.5-10 4.375-2.812 2.188-5 5-6.875 7.813-2.812 4.687-3.75 10-3.125 15.312a27.2 27.2 0 0 0 6.25 14.375c-1.25 3.438-1.562 6.875-1.25 10.313.313 3.437 1.563 6.875 3.125 10 2.813 4.687 6.875 8.437 11.563 10.625 5 2.187 10.312 2.812 15.625 1.562 2.5 2.5 5.312 4.688 8.437 6.25S51.81 100 55.248 100c5.312 0 10.625-1.563 15-4.688s7.5-7.5 9.062-12.5c3.438-.625 6.875-2.187 9.688-4.375 2.812-2.187 5.312-4.687 6.875-7.812 2.812-4.688 3.75-10 3.125-15.313C98.373 50 96.498 45 93.06 40.937m-37.5 52.5c-5 0-8.75-1.562-12.187-4.375 0 0 .312-.312.625-.312l20-11.563c.625-.312.937-.625 1.25-1.25s.312-.937.312-1.562V46.25l8.438 5v23.125c.312 10.937-8.438 19.062-18.438 19.062M15.248 76.25c-2.188-3.75-3.125-8.125-2.188-12.5 0 0 .313.312.625.312l20 11.563c.625.312.938.312 1.563.312s1.25 0 1.562-.312l24.375-14.063v9.688L40.873 83.125c-4.375 2.5-9.375 3.125-14.063 1.875-5-1.25-9.062-4.375-11.562-8.75M9.935 32.812c2.188-3.75 5.625-6.562 9.688-8.125v23.75c0 .625 0 1.25.312 1.563.313.625.625.937 1.25 1.25L45.56 65.312l-8.437 5-20-11.562c-4.375-2.5-7.5-6.563-8.75-11.25s-.938-10.313 1.562-14.688M78.998 48.75 54.623 34.687l8.437-5 20 11.563c3.125 1.875 5.625 4.375 7.188 7.5s2.5 6.562 2.187 10.312c-.312 3.438-1.562 6.875-3.75 9.688-2.187 2.812-5 5-8.437 6.25V51.25c0-.625 0-1.25-.313-1.563 0 0-.312-.625-.937-.937m8.437-12.5s-.312-.313-.625-.313l-20-11.562c-.625-.313-.937-.313-1.562-.313s-1.25 0-1.563.313L39.31 38.437V28.75l20.313-11.875c3.125-1.875 6.562-2.5 10.312-2.5 3.438 0 6.875 1.25 10 3.437 2.813 2.188 5.313 5 6.563 8.125s1.562 6.875.937 10.313m-52.5 17.5-8.437-5V25.312c0-3.437.937-7.187 2.812-10 1.875-3.125 4.688-5.312 7.813-6.875s6.875-2.187 10.312-1.562c3.438.312 6.875 1.875 9.688 4.062 0 0-.313.313-.625.313l-20 11.562c-.625.313-.938.625-1.25 1.25s-.313.938-.313 1.563zm4.375-10 10.938-6.25 10.937 6.25v12.5L50.248 62.5 39.31 56.25z\"/></svg>",
  gemini: "<svg xmlns=\"http://www.w3.org/2000/svg\" fill=\"none\" viewBox=\"0 0 296 298\"><mask id=\"a\" width=\"296\" height=\"298\" x=\"0\" y=\"0\" maskUnits=\"userSpaceOnUse\" style=\"mask-type:alpha\"><path fill=\"#3186FF\" d=\"M141.201 4.886c2.282-6.17 11.042-6.071 13.184.148l5.985 17.37a184 184 0 0 0 111.257 113.049l19.304 6.997c6.143 2.227 6.156 10.91.02 13.155l-19.35 7.082a184 184 0 0 0-109.495 109.385l-7.573 20.629c-2.241 6.105-10.869 6.121-13.133.025l-7.908-21.296a184 184 0 0 0-109.02-108.658l-19.698-7.239c-6.102-2.243-6.118-10.867-.025-13.132l20.083-7.467A184 184 0 0 0 133.291 26.28z\"/></mask><g mask=\"url(#a)\"><g filter=\"url(#b)\"><ellipse cx=\"163\" cy=\"149\" fill=\"#3689FF\" rx=\"196\" ry=\"159\"/></g><g filter=\"url(#c)\"><ellipse cx=\"33.5\" cy=\"142.5\" fill=\"#F6C013\" rx=\"68.5\" ry=\"72.5\"/></g><g filter=\"url(#d)\"><ellipse cx=\"19.5\" cy=\"148.5\" fill=\"#F6C013\" rx=\"68.5\" ry=\"72.5\"/></g><g filter=\"url(#e)\"><path fill=\"#FA4340\" d=\"M194 10.5C172 82.5 65.5 134.333 22.5 135L144-66z\"/></g><g filter=\"url(#f)\"><path fill=\"#FA4340\" d=\"M190.5-12.5C168.5 59.5 62 111.333 19 112L140.5-89z\"/></g><g filter=\"url(#g)\"><path fill=\"#14BB69\" d=\"M194.5 279.5C172.5 207.5 66 155.667 23 155l121.5 201z\"/></g><g filter=\"url(#h)\"><path fill=\"#14BB69\" d=\"M196.5 320.5C174.5 248.5 68 196.667 25 196l121.5 201z\"/></g></g><defs><filter id=\"b\" width=\"464\" height=\"390\" x=\"-69\" y=\"-46\" color-interpolation-filters=\"sRGB\" filterUnits=\"userSpaceOnUse\"><feFlood flood-opacity=\"0\" result=\"BackgroundImageFix\"/><feBlend in=\"SourceGraphic\" in2=\"BackgroundImageFix\" result=\"shape\"/><feGaussianBlur result=\"effect1_foregroundBlur_69_17998\" stdDeviation=\"18\"/></filter><filter id=\"c\" width=\"265\" height=\"273\" x=\"-99\" y=\"6\" color-interpolation-filters=\"sRGB\" filterUnits=\"userSpaceOnUse\"><feFlood flood-opacity=\"0\" result=\"BackgroundImageFix\"/><feBlend in=\"SourceGraphic\" in2=\"BackgroundImageFix\" result=\"shape\"/><feGaussianBlur result=\"effect1_foregroundBlur_69_17998\" stdDeviation=\"32\"/></filter><filter id=\"d\" width=\"265\" height=\"273\" x=\"-113\" y=\"12\" color-interpolation-filters=\"sRGB\" filterUnits=\"userSpaceOnUse\"><feFlood flood-opacity=\"0\" result=\"BackgroundImageFix\"/><feBlend in=\"SourceGraphic\" in2=\"BackgroundImageFix\" result=\"shape\"/><feGaussianBlur result=\"effect1_foregroundBlur_69_17998\" stdDeviation=\"32\"/></filter><filter id=\"e\" width=\"299.5\" height=\"329\" x=\"-41.5\" y=\"-130\" color-interpolation-filters=\"sRGB\" filterUnits=\"userSpaceOnUse\"><feFlood flood-opacity=\"0\" result=\"BackgroundImageFix\"/><feBlend in=\"SourceGraphic\" in2=\"BackgroundImageFix\" result=\"shape\"/><feGaussianBlur result=\"effect1_foregroundBlur_69_17998\" stdDeviation=\"32\"/></filter><filter id=\"f\" width=\"299.5\" height=\"329\" x=\"-45\" y=\"-153\" color-interpolation-filters=\"sRGB\" filterUnits=\"userSpaceOnUse\"><feFlood flood-opacity=\"0\" result=\"BackgroundImageFix\"/><feBlend in=\"SourceGraphic\" in2=\"BackgroundImageFix\" result=\"shape\"/><feGaussianBlur result=\"effect1_foregroundBlur_69_17998\" stdDeviation=\"32\"/></filter><filter id=\"g\" width=\"299.5\" height=\"329\" x=\"-41\" y=\"91\" color-interpolation-filters=\"sRGB\" filterUnits=\"userSpaceOnUse\"><feFlood flood-opacity=\"0\" result=\"BackgroundImageFix\"/><feBlend in=\"SourceGraphic\" in2=\"BackgroundImageFix\" result=\"shape\"/><feGaussianBlur result=\"effect1_foregroundBlur_69_17998\" stdDeviation=\"32\"/></filter><filter id=\"h\" width=\"299.5\" height=\"329\" x=\"-39\" y=\"132\" color-interpolation-filters=\"sRGB\" filterUnits=\"userSpaceOnUse\"><feFlood flood-opacity=\"0\" result=\"BackgroundImageFix\"/><feBlend in=\"SourceGraphic\" in2=\"BackgroundImageFix\" result=\"shape\"/><feGaussianBlur result=\"effect1_foregroundBlur_69_17998\" stdDeviation=\"32\"/></filter></defs></svg>",
  copilot: "<svg xmlns=\"http://www.w3.org/2000/svg\" fill=\"none\" viewBox=\"0 0 100 100\"><path fill=\"#000\" d=\"M35.417 60.983a4.066 4.066 0 0 1 8.131 0v7.493a4.066 4.066 0 0 1-8.132 0zm24.899-4.066a4.066 4.066 0 0 0-4.066 4.066v7.493a4.066 4.066 0 0 0 8.132 0v-7.493a4.066 4.066 0 0 0-4.066-4.066\"/><path fill=\"#000\" fill-rule=\"evenodd\" d=\"M100 68.332V57.126a6.86 6.86 0 0 0-1.344-4.082l-3.044-4.125c-1.629-2.22-4.042-2.793-6.63-2.793C88.005 35 85.693 27.063 80.187 21.25 69.688 10.125 55.75 9 50 9s-19.687 1.125-30.187 12.25C14.3 27.063 11.993 35 11.019 46.126c-2.583 0-5.008.583-6.638 2.793l-3.043 4.119A6.9 6.9 0 0 0 0 57.126v11.206a4.67 4.67 0 0 0 1.888 3.712C11.645 79.206 30.834 90.251 50 90.251c17.324 0 34.33-8.117 48.113-18.207A4.67 4.67 0 0 0 100 68.332M76.563 47.769c2.156.709 4.228 1.71 4.362 4.263h-.006c.343 7.187.443 14.411.319 21.606a3.66 3.66 0 0 1-2.126 3.275c-10.337 4.713-20.118 7.088-29.106 7.088-9 0-18.781-2.376-29.125-7.088a3.66 3.66 0 0 1-2.125-3.275c.035-2.327.046-4.652.055-6.976l.002-.246c.02-4.795.04-9.587.268-14.384.137-2.538 2.2-3.556 4.35-4.263 2.272 1.433 5.056 1.988 7.713 1.988 2.825 0 8.1-.675 12.475-5.05 1.106-1.1 1.862-2.825 2.375-4.738a42 42 0 0 1 4.012-.212q2.005.01 4 .212c.513 1.913 1.269 3.638 2.375 4.738 4.382 4.375 9.65 5.05 12.475 5.05 2.657 0 5.433-.561 7.707-1.988m-47.25-2.956c-3.438 0-6.625-1.125-8.313-2.812-2.312-2.375-3.187-13.625.875-18.063 1.875-2 5.563-3.437 9.938-3.875 4.125-.375 7.812.25 9.437 1.688 1.5 1.25 2.5 4.187 2.625 7.75.125 4.437-1 8.812-2.812 10.562-4.126 4.188-9.126 4.75-11.75 4.75m17.437-11.5c-.062-.812-.125-1.563-.25-2.25 1.313-.125 2.5-.187 3.5-.187s2.188.062 3.5.187c-.125.688-.187 1.438-.25 2.25 0 .563 0 1.125.063 1.75-1.25-.125-2.313-.125-3.313-.125s-2.062 0-3.312.125c.062-.625.062-1.187.062-1.75m12.188 6.75C57.125 38.313 56 33.938 56.125 29.5c.125-3.562 1.125-6.5 2.625-7.75 1.625-1.437 5.313-2.062 9.438-1.687 4.374.438 8.062 1.875 9.937 3.875 4.063 4.438 3.188 15.688.875 18.063-1.687 1.687-4.875 2.812-8.312 2.812-2.626 0-7.626-.562-11.75-4.75\" clip-rule=\"evenodd\"/></svg>",
});

const DEVELOPER_ICON_ALIASES = Object.freeze([
  ['githubcopilot', 'copilot'],
  ['copilot', 'copilot'],
  ['claudecode', 'claude'],
  ['claudeai', 'claude'],
  ['claude', 'claude'],
  ['geminicli', 'gemini'],
  ['gemini', 'gemini'],
  ['codex', 'openai'],
  ['openai', 'openai'],
  ['argocd', 'kubernetes'],
  ['kubernetes', 'kubernetes'],
  ['k8s', 'kubernetes'],
  ['azure', 'azure'],
  ['markdown', 'markdown'],
  ['md', 'markdown'],
  ['worktree', 'git'],
  ['git', 'git'],
  ['terminal', 'bash'],
  ['shell', 'bash'],
  ['bash', 'bash'],
]);

let developerIconSequence = 0;

function developerIconName(value) {
  const normalized = String(value ?? '').toLowerCase().replace(/[^a-z0-9]/g, '');
  if (DEVELOPER_ICON_SVG[normalized]) return normalized;
  return DEVELOPER_ICON_ALIASES.find(([alias]) => normalized.includes(alias))?.[1] || '';
}

function scopeDeveloperIconIds(svg, name) {
  const ids = [...svg.matchAll(/\bid="([^"]+)"/g)].map(match => match[1]);
  if (!ids.length) return svg;
  const prefix = `dev-${name}-${++developerIconSequence}-`;
  for (const id of ids) {
    svg = svg
      .replaceAll(`id="${id}"`, `id="${prefix}${id}"`)
      .replaceAll(`url(#${id})`, `url(#${prefix}${id})`)
      .replaceAll(`href="#${id}"`, `href="#${prefix}${id}"`);
  }
  return svg;
}

export function developerIcon(name) {
  const resolved = developerIconName(name);
  if (!resolved) return '';
  const svg = scopeDeveloperIconIds(DEVELOPER_ICON_SVG[resolved], resolved);
  return svg.replace(
    '<svg',
    `<svg class="dev-icon" data-dev-icon="${resolved}" aria-hidden="true" focusable="false"`,
  );
}

export function providerIcon(source) {
  return developerIcon(source);
}

const SESSION_TOOL_LABELS = Object.freeze({
  claude: 'Claude Code',
  openai: 'Codex',
  gemini: 'Gemini CLI',
  copilot: 'GitHub Copilot',
  bash: 'Bash-Terminal',
});

export function sessionToolKey(session) {
  for (const identity of sessionToolCandidates(session)) {
    const resolved = developerIconName(identity);
    if (resolved) return resolved;
  }
  return session?.term ? 'bash' : '';
}

export function sessionToolLabel(session) {
  return SESSION_TOOL_LABELS[sessionToolKey(session)] || 'Coding-Agent';
}

export function sessionToolIcon(session) {
  return developerIcon(sessionToolKey(session));
}

export function mountDeveloperIcons(root = document) {
  const replace = (selector, markup) => {
    const current = root.querySelector(selector);
    if (current) current.outerHTML = markup;
  };
  const slot = name => `<span class="dev-icon-slot">${developerIcon(name)}</span>`;

  replace('#nav-graph > .ico', slot('git'));
  replace('#nav-board > .ico', slot('markdown'));
  replace('#nav-dock > .ico', slot('bash'));
  replace('#nav-stats > .ico', slot('claude'));
}
