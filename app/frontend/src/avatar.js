import './avatar.css';

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
