// Clip grammar "map": an antique parchment world map for history shorts.
// Beige-tan land with paper grain, muted teal ocean, thin dark coastlines, EB Garamond atlas labels; regions are flat fills
// (France royal blue, Britain red, USA teal, Spain orange) that spread / recolour with a wipe; glowing outlines, a wavy glowing
// river that draws on, dashed routes with arrows, ships and crates travelling along routes, icons that pop with a bounce,
// a ◀◀ rewind beat, thought bubbles with a mini-map, and PNG character sprites standing at geographic points.
// The camera flies between geographic views (lon/lat + zoom) and never stops drifting.
//
// Needs window.MAPGRAM_WORLD (world.js, Natural Earth TopoJSON) and window.MAPGRAM_PRESETS (presets.js) evaluated first:
// the host concatenates world.js + presets.js + map.js into clips/map.js.
// Full spec reference: README.md next to this file. Clip contract: references/09 (window.renderFrame(t), deterministic).
//
// spec.data: { view:{lon,lat,zoom}|{bbox:[w,s,e,n]}|{place}, projection:"mercator"|"equirect"|"robinson", language:"en"|"ru"|…,
//              regions:[…], rivers:[…], routes:[…], icons:[…], sprites:[…], labels:[…], places:{name:[lon,lat]}, year,
//              safeBottom (fraction, default .30 portrait / .16 landscape), drift (0..2, default 1), style:{land,ocean,coast,outline,…} }
// cues: camera | fill | recolor | glow | drawRoute | moveAlong | pop | sprite | bubble | rewind | label | hide | show
CLIPS.map = (() => {
const { clamp, lerp } = U;
const D2R = Math.PI / 180, R2D = 180 / Math.PI, TAU = Math.PI * 2;
const FONT = 'EBGaramond-400', FONTI = 'EBGaramond-400i';
const fnt = (px, it) => `${Math.max(1, px).toFixed(1)}px "${it ? FONTI : FONT}", "EB Garamond", Georgia, serif`;
// Cyrillic italic: a true italic (EB Garamond's included) turns п т и д into n m u g shapes — on a map at a glance
// «Міссісіпі» reads as «Miccicini». Labels with Cyrillic are drawn upright-glyph oblique instead (EB Garamond 400 skewed).
const CYR = /[Ѐ-ԯᲀ-᲏Ꙁ-ꚟ]/, OBLIQUE = 0.2;
const EASE = { linear: p => p, inOut: MO.cubicInOut, smooth: MO.smooth, sine: MO.sineInOut, out: MO.quintOut, in: MO.cubicIn, expo: MO.expoOut, back: p => MO.backOut(p, 1.7) };
const easeOf = (n, d) => (typeof n === 'string' && EASE[n]) || d;
const hex = h => { h = String(h).replace('#', ''); if (h.length === 3) h = h.split('').map(x => x + x).join(''); const n = parseInt(h.slice(0, 6), 16); return [n >> 16 & 255, n >> 8 & 255, n & 255]; };
const rgba = (h, a = 1) => { if (!/^#/.test(h)) return h; const c = hex(h); return `rgba(${c[0]},${c[1]},${c[2]},${a})`; };
const mixHex = (a, b, t) => { const x = hex(a), y = hex(b); return '#' + [0, 1, 2].map(i => Math.round(lerp(x[i], y[i], t)).toString(16).padStart(2, '0')).join(''); };
const hsh = (i, j = 0) => U.hash(i | 0, j | 0);
const strHash = s => { let h = 2166136261; for (const ch of String(s)) { h ^= ch.codePointAt(0); h = Math.imul(h, 16777619); } return h >>> 0; };

let S = null;   // everything built in init

// ======================================================================= projection
const ROB_X = [1, .9986, .9954, .99, .9822, .973, .96, .9427, .9216, .8962, .8679, .835, .7986, .7597, .7186, .6732, .6213, .5722, .5322];
const ROB_Y = [0, .062, .124, .186, .248, .31, .372, .434, .4958, .5571, .6176, .6769, .7346, .7903, .8435, .8936, .9394, .9761, 1];
function makeProj(name) {
  name = String(name || 'mercator').toLowerCase();
  if (name.startsWith('equi') || name === 'plate_carree') return (lo, la) => [lo, -la];
  if (name === 'robinson') return (lo, la) => { const a = Math.min(Math.abs(la), 90) / 5, i = Math.min(17, Math.floor(a)), f = a - i;
    const X = lerp(ROB_X[i], ROB_X[i + 1], f), Y = lerp(ROB_Y[i], ROB_Y[i + 1], f); return [X * lo, -Math.sign(la) * Y * 91.29]; };
  return (lo, la) => { const p = clamp(la, -85, 85) * D2R; return [lo, -Math.log(Math.tan(Math.PI / 4 + p / 2)) * R2D]; };
}

// ======================================================================= textures (tileable, seeded, built once)
function noiseTile(N, seed, octs, fn) {
  const cv = document.createElement('canvas'); cv.width = cv.height = N; const g = cv.getContext('2d'), img = g.createImageData(N, N), d = img.data, r = U.rng(seed);
  const grids = octs.map(([cells]) => { const a = new Float32Array(cells * cells); for (let i = 0; i < a.length; i++) a[i] = r() * 2 - 1; return a; });
  const sm = x => x * x * (3 - 2 * x);
  const val = (k, x, y) => { const n = octs[k][0], gx = x / N * n, gy = y / N * n, x0 = Math.floor(gx), y0 = Math.floor(gy), fx = sm(gx - x0), fy = sm(gy - y0), G = grids[k];
    const at = (i, j) => G[((j % n + n) % n) * n + ((i % n + n) % n)];
    return lerp(lerp(at(x0, y0), at(x0 + 1, y0), fx), lerp(at(x0, y0 + 1), at(x0 + 1, y0 + 1), fx), fy); };
  for (let y = 0; y < N; y++) for (let x = 0; x < N; x++) {
    const o = octs.map((q, k) => val(k, x, y) * q[1]);
    const v = fn(o, r), i = (y * N + x) * 4; d[i] = d[i + 1] = d[i + 2] = clamp(v, 0, 255); d[i + 3] = 255;
  }
  g.putImageData(img, 0, 0); return cv;
}
function buildTextures() {
  // mottle: low-frequency parchment blotches (gray around 128 → soft-light)
  const mottle = noiseTile(512, 11, [[3, 1], [6, .55], [12, .3], [24, .16], [48, .08]], o => 128 + (o[0] + o[1] + o[2] + o[3] + o[4]) * 70);
  // relief: ridged noise, reads as worn hills / creases on the land
  const relief = noiseTile(512, 23, [[5, 1], [10, .5], [20, .25], [40, .14], [80, .07]], o => { let s = 0; o.forEach(v => { s += (0.5 - Math.abs(v)) ; }); return 128 + s * 60; });
  // grain: paper fibre + speckle
  const grain = noiseTile(256, 37, [[32, .5], [64, .35], [128, .25]], (o, r) => 128 + (o[0] + o[1] + o[2]) * 50 + (r() - .5) * 46);
  const g = grain.getContext('2d'), r = U.rng(41); g.lineCap = 'round';
  for (let k = 0; k < 150; k++) { const x = r() * 256, y = r() * 256, a = r() * Math.PI, l = 4 + r() * 12, dark = r() < 0.5;
    g.strokeStyle = dark ? `rgba(60,45,25,${0.12 + r() * .12})` : `rgba(255,250,235,${0.12 + r() * .14})`; g.lineWidth = 0.5 + r() * 0.8;
    for (const ox of [0, -256, 256]) for (const oy of [0, -256, 256]) { g.beginPath(); g.moveTo(x + ox, y + oy); g.quadraticCurveTo(x + ox + Math.cos(a + 1) * l * .5, y + oy + Math.sin(a + 1) * l * .5, x + ox + Math.cos(a) * l, y + oy + Math.sin(a) * l); g.stroke(); } }
  // ocean: soft horizontal streaks (old engraved-sea feel)
  const sea = noiseTile(512, 53, [[4, 1], [8, .5], [16, .3], [64, .12]], (o, r) => 128 + (o[0] * .6 + o[1] + o[2] + o[3]) * 55 + (r() - .5) * 14);
  return { mottle, relief, grain, sea };
}
const PATS = new Map();
function pat(c, tile, slot) { const key = tile; let a = PATS.get(key); if (!a) { a = [c.createPattern(tile, 'repeat'), c.createPattern(tile, 'repeat')]; PATS.set(key, a); } return a[slot]; }
// world-anchored texture with two crossfaded octaves: never magnified more than 2×, never swims
function texture(c, V, tile, T, alpha, op, wr) {
  if (alpha <= 0) return;
  const f = Math.log2(T / V.k), fl = Math.floor(f), fr = f - fl;
  const lv = [[Math.pow(2, fl), 1 - fr], [Math.pow(2, fl + 1), fr]];
  c.save(); c.globalCompositeOperation = op;
  lv.forEach(([m, w], i) => { if (w * alpha < 0.01) return; const p = pat(c, tile, i); p.setTransform(new DOMMatrix([m, 0, 0, m, 0, 0])); c.globalAlpha = alpha * w; c.fillStyle = p; c.fillRect(wr.x, wr.y, wr.w, wr.h); });
  c.restore();
}

// ======================================================================= geometry
function decodeWorld(proj) {
  const W0 = window.MAPGRAM_WORLD; if (!W0) throw new Error('map grammar: window.MAPGRAM_WORLD missing (world.js must be bundled before map.js)');
  const tr = W0.transform;
  const ll = W0.arcs.map(a => { let x = 0, y = 0; return a.map(p => { x += p[0]; y += p[1]; return [x * tr.scale[0] + tr.translate[0], y * tr.scale[1] + tr.translate[1]]; }); });
  const wa = ll.map(a => a.map(p => proj(p[0], p[1])));
  return { W0, ll, wa };
}
const arcPts = (A, i) => i >= 0 ? A[i] : A[~i].slice().reverse();
function ringPts(A, ring) { const o = []; ring.forEach((i, k) => { const a = arcPts(A, i); for (let j = k ? 1 : 0; j < a.length; j++) o.push(a[j]); }); return o; }
const polysOf = g => !g || !g.arcs ? [] : g.type === 'Polygon' ? [g.arcs] : g.type === 'MultiPolygon' ? g.arcs : [];
function addRing(path, pts, close = true) { if (!pts.length) return; path.moveTo(pts[0][0], pts[0][1]); for (let i = 1; i < pts.length; i++) path.lineTo(pts[i][0], pts[i][1]); if (close) path.closePath(); }
function bboxAdd(b, p) { if (p[0] < b[0]) b[0] = p[0]; if (p[1] < b[1]) b[1] = p[1]; if (p[0] > b[2]) b[2] = p[0]; if (p[1] > b[3]) b[3] = p[1]; }
const ringCenterLL = (ll, ring) => { const p = ringPts(ll, ring); let x = 0, y = 0; p.forEach(q => { x += q[0]; y += q[1]; }); return [x / p.length, y / p.length]; };

// ======================================================================= lookup helpers
const PLACE = v => {
  if (v == null) return null;
  if (Array.isArray(v) && typeof v[0] === 'number') return [+v[0], +v[1]];
  if (typeof v === 'object' && v.lon != null) return [+v.lon, +v.lat];
  if (typeof v === 'object' && v.place != null) return PLACE(v.place);
  if (typeof v === 'string') { const k = v.toLowerCase().replace(/[\s-]+/g, '_'); const p = S.places[k] || S.places[v];
    if (p) return p; const R = S.regionById[v]; if (R) return R.labelLL; const sp = S.spriteById[v]; if (sp) return sp.ll;
    console.warn(`map: unknown place "${v}"`); return null; }
  return null;
};
const llOf = d => d.lon != null ? [+d.lon, +d.lat] : d.lonlat ? PLACE(d.lonlat) : d.place != null ? PLACE(d.place) : d.at != null && typeof d.at !== 'number' ? PLACE(d.at) : null;
const col = (c, d) => c == null ? d : (S.colors[String(c).toLowerCase()] || c);
const tr = (v, lang) => v == null ? null : typeof v === 'object' && !Array.isArray(v) ? (v[lang] ?? v.en ?? Object.values(v)[0]) : String(v);

// ======================================================================= init
function init(ctx) {
  const { W, H, data } = ctx, PR = window.MAPGRAM_PRESETS || { regions: {}, rivers: {}, places: {}, colors: {} };
  const u = Math.min(W, H) / 1080, lang = String(data.language || data.lang || 'en').toLowerCase().slice(0, 2);
  const proj = makeProj(data.projection);
  const { W0, ll, wa } = decodeWorld(proj);
  const st = data.style || {};
  S = { W, H, u, lang, proj, ll, wa, fps: ctx.FPS, dur: ctx.dur,
    colors: Object.assign({}, PR.colors), places: Object.assign({}, PR.places),
    pal: { ocean: st.ocean || '#5f8589', land: st.land || '#9b8a62', coast: st.coast || '#2b2820', outline: st.outline || '#1c1915', label: st.label || '#f2ead8', ink: st.ink || '#3a2d1d',
      river: st.river || '#a8dcff', route: st.route || '#fbf6ea', glow: st.glow || '#bfe8ff', vignette: st.vignette ?? 0.32 },
    drift: data.drift ?? 1, borders: !!data.borders, regionById: {}, spriteById: {}, imgs: {}, warned: new Set() };
  for (const [k, v] of Object.entries(data.places || {})) S.places[k.toLowerCase()] = PLACE(v) || v;
  if (data.colors) for (const [k, v] of Object.entries(data.colors)) S.colors[k.toLowerCase()] = v;
  S.tex = buildTextures();

  // ---- safe box / focus
  const ss = ctx.spec.safe || {}; const hasSafe = ss.top || ss.bottom || ss.left || ss.right;
  const fb = data.safeBottom != null ? +data.safeBottom : (ctx.portrait ? 0.30 : 0.16);
  S.box = hasSafe ? { ...ctx.box } : { x: 0, y: 0, w: W, h: H * (1 - fb) };
  S.anchor = [S.box.x + S.box.w / 2, S.box.y + S.box.h / 2];
  S.minSide = Math.min(W, H);
  // default sprite height (fraction of the short side, measured on the figure itself — transparent margins are trimmed):
  // portrait 0.37 → ≈ 20 % of the frame height, like the reference; landscape 0.32 (≈ 32 % of the height)
  S.spriteScale = H > W ? 0.37 : 0.32;

  // ---- base geometry: land, coast, borders, lakes
  const C = W0.objects.countries.geometries, use = new Int32Array(wa.length);
  S.land = new Path2D(); S.countries = {}; S.byName = {};
  for (const g of C) {
    const polys = polysOf(g); polys.forEach(p => p.forEach(r => { addRing(S.land, ringPts(wa, r)); r.forEach(i => use[i < 0 ? ~i : i]++); }));
    const rec = { id: g.id, g, polys, props: g.properties || {} }; S.countries[g.id] = rec; if (rec.props.iso) S.countries[rec.props.iso] = S.countries[rec.props.iso] || rec;
    S.byName[String(rec.props.n || '').toLowerCase()] = rec;
  }
  S.coast = new Path2D(); S.bord = new Path2D();
  for (let i = 0; i < wa.length; i++) if (use[i] === 1) addRing(S.coast, wa[i], false); else if (use[i] > 1) addRing(S.bord, wa[i], false);
  S.lakes = new Path2D(); for (const g of W0.objects.lakes.geometries) polysOf(g).forEach(p => p.forEach(r => addRing(S.lakes, ringPts(wa, r))));
  S.states = {}; for (const g of W0.objects.states.geometries) S.states[g.id] = { id: g.id, g, polys: polysOf(g), props: g.properties || {} };
  S.neRivers = {}; for (const g of W0.objects.rivers.geometries) S.neRivers[g.id] = g;

  // ---- elements
  const D = data;
  S.regions = []; S.lines = []; S.icons = []; S.sprites = []; S.labels = []; S.bubbles = []; S.rewinds = []; S.movers = [];
  S.lineById = {}; S.iconById = {}; S.labelById = {};
  (D.regions || []).forEach((r, i) => addRegion(r, 'r' + i));
  (D.rivers || []).forEach((r, i) => addLine(r, 'river', 'river' + i));
  (D.routes || []).forEach((r, i) => addLine(r, 'route', 'route' + i));
  (D.icons || []).forEach((r, i) => addIcon(r, 'icon' + i));
  (D.sprites || []).forEach((r, i) => addSprite(r, 'sprite' + i));
  (D.labels || []).forEach((r, i) => addLabel(r, 'label' + i));

  // ---- cues → element timelines
  const fps = ctx.FPS;
  const camCues = [];
  ctx.cues.forEach((q, qi) => {
    const k = String(q.kind || '').toLowerCase(), d = q.data || {}, Q = Object.assign({}, d, q);   // cue fields win over cue.data
    const at = +q.at || 0;
    const tgt = (...names) => { for (const n of names) if (Q[n] != null) return Q[n]; return null; };
    if (k === 'camera' || k === 'cam' || k === 'view') return camCues.push({ at, Q });
    if (k === 'fill' || k === 'recolor' || k === 'recolour' || k === 'spread') {
      let R = tgt('region', 'target', 'id'); if (R && typeof R === 'object') R = addRegion(R, 'cueR' + qi).id; else if (!S.regionById[R] && (Q.country || Q.preset || Q.polygon || Q.countries)) R = addRegion(Q, 'cueR' + qi).id;
      const reg = S.regionById[R]; if (!reg) return warn(`map: ${k} cue at ${at}: unknown region "${R}"`);
      const isFill = k !== 'recolor' && k !== 'recolour';
      reg.cev.push({ at, dur: +(Q.dur ?? (isFill ? 1.1 : 0.9)), color: col(Q.color, reg.color), from: Q.from, mode: Q.mode || (isFill ? 'radial' : 'radial'), ease: easeOf(Q.ease, MO.cubicInOut), fill: isFill });
      if (isFill) reg.revealed = true;
      return;
    }
    if (k === 'glow' || k === 'highlight') {
      const id = tgt('target', 'region', 'route', 'river', 'line', 'sprite', 'icon', 'id'); const el = find(id); if (!el) return warn(`map: glow cue at ${at}: unknown target "${id}"`);
      (el.glows = el.glows || []).push({ at, dur: Q.dur != null ? +Q.dur : 1e9, color: col(Q.color, S.pal.glow), pulse: Q.pulse !== false, width: Q.width });
      return;
    }
    if (k === 'drawroute' || k === 'route' || k === 'river' || k === 'draw' || k === 'drawriver') {
      let id = tgt('route', 'river', 'line', 'target', 'id'); if (id && typeof id === 'object') id = addLine(id, k === 'river' || k === 'drawriver' ? 'river' : 'route', 'cueL' + qi).id;
      else if (!S.lineById[id] && (Q.from || Q.points || Q.line)) id = addLine(Q, k === 'river' || k === 'drawriver' ? 'river' : 'route', 'cueL' + qi).id;
      const L = S.lineById[id]; if (!L) return warn(`map: ${k} cue at ${at}: unknown route/river "${id}"`);
      L.ev.push({ at, type: 'show', dur: +(Q.dur ?? 1.6), ease: easeOf(Q.ease, MO.cubicInOut) }); L.revealed = true; return;
    }
    if (k === 'movealong' || k === 'move' || k === 'travel') {
      let id = tgt('route', 'river', 'line', 'target'); if (id && typeof id === 'object') id = addLine(Object.assign({ show: false }, id), 'route', 'cueL' + qi).id;
      const L = S.lineById[id]; if (!L) return warn(`map: moveAlong cue at ${at}: unknown route "${id}"`);
      S.movers.push({ at, L, dur: +(Q.dur ?? 3), object: Q.object || Q.icon || 'ship', image: q.image || Q.image, count: Math.max(1, Q.count | 0 || 1), gap: +(Q.gap ?? 0.13), size: +(Q.size ?? 0), ease: easeOf(Q.ease, MO.sineInOut),
        stay: !!Q.stay, reverse: !!Q.reverse, text: Q.text, color: Q.color, flag: Q.flag });
      if (!L.revealed && L.show0 === undefined) L.hiddenLine = true;
      return;
    }
    if (k === 'pop' || k === 'icon' || k === 'year' || k === 'badge') {
      let id = tgt('icon', 'target', 'id'); let I, ty = Q.type;
      if (typeof id === 'string' && !S.iconById[id] && DEF_SIZE[id.toLowerCase()]) { ty = ty || id; id = Q.id ?? null; }
      if (id && typeof id === 'object') I = addIcon(id, 'cueI' + qi);
      else if (S.iconById[id]) I = S.iconById[id];
      else I = addIcon(Object.assign({}, Q, { icon: undefined, type: ty || (k === 'year' ? 'year' : (q.image || Q.image) ? 'image' : 'text'), image: q.image || Q.image, id: id || undefined }), 'cueI' + qi);
      if (k === 'year' && !I.type) I.type = 'year';
      I.ev.push({ at, type: 'show', dur: +(Q.in ?? 0.42) }); I.revealed = true;
      if (Q.dur != null) I.ev.push({ at: at + +Q.dur, type: 'hide', dur: 0.25 });
      return;
    }
    if (k === 'sprite' || k === 'character' || k === 'char') {
      const id = tgt('sprite', 'target', 'id'); let sp = S.spriteById[id];
      if (!sp) { if (!(q.image || Q.image)) return warn(`map: sprite cue at ${at}: unknown sprite "${id}"`); sp = addSprite(Object.assign({}, Q, { id, show: false, image: q.image || Q.image }), 'cueS' + qi); }
      const act = String(Q.action || Q.anim || 'enter').toLowerCase();
      sp.acts.push({ at, act, Q, image: q.image || Q.image });
      if (act === 'enter' || act === 'in' || act === 'show') sp.revealed = true;
      return;
    }
    if (k === 'bubble' || k === 'think' || k === 'thought') { S.bubbles.push({ at, Q, dur: +(Q.dur ?? 2.6), image: q.image }); return; }
    if (k === 'rewind') { S.rewinds.push({ at, dur: +(Q.dur ?? 1.4), text: q.text ?? Q.text, sub: Q.sub }); return; }
    if (k === 'label' || k === 'text') {
      let id = tgt('label', 'target', 'id'); let Lb = typeof id === 'string' ? S.labelById[id] : null;
      if (!Lb) Lb = addLabel(typeof id === 'object' && id ? id : Object.assign({}, Q, { id: undefined, label: undefined, text: Q.text ?? id }), 'cueT' + qi);
      Lb.ev.push({ at, type: 'show', dur: +(Q.in ?? 0.5) }); Lb.revealed = true;
      if (Q.dur != null) Lb.ev.push({ at: at + +Q.dur, type: 'hide', dur: 0.3 });
      return;
    }
    if (k === 'hide' || k === 'show') {
      const ids = [].concat(tgt('targets', 'target', 'region', 'route', 'river', 'icon', 'sprite', 'label', 'id') || []);
      for (const id of ids) { const el = find(id); if (!el) { warn(`map: ${k} cue at ${at}: unknown target "${id}"`); continue; }
        if (el.kind === 'sprite') el.acts.push({ at, act: k === 'hide' ? 'exit' : 'enter', Q });
        else (el.ev = el.ev || []).push({ at, type: k, dur: +(Q.dur ?? (k === 'hide' ? 0.4 : 0.5)) });
        if (k === 'show') el.revealed = true; }
      return;
    }
    warn(`map: unknown cue kind "${q.kind}" at ${at} (ignored)`);
  });
  const byAt = (a, b) => a.at - b.at;
  S.regions.forEach(r => { r.cev.sort(byAt); r.ev.sort(byAt); }); S.lines.forEach(l => l.ev.sort(byAt)); S.icons.forEach(l => l.ev.sort(byAt)); S.labels.forEach(l => l.ev.sort(byAt));
  S.sprites.forEach(s => s.acts.sort(byAt));
  // a line that only carries movers and was never drawn stays invisible
  S.lines.forEach(l => { if (l.hiddenLine && !l.revealed && l.def.show !== true) l.show0 = false; });

  // ---- camera
  const v0 = D.view || { lon: 0, lat: 25, zoom: 1.6 };
  S.cam0 = viewToCam(v0);
  S.z0 = S.cam0.z;
  S.camSegs = [];
  camCues.sort(byAt).forEach(({ at, Q }) => {
    const v = Q.view || Q.to || (Q.bbox ? { bbox: Q.bbox } : Q.place ? { place: Q.place } : { lon: Q.lon, lat: Q.lat, zoom: Q.zoom, zoomBy: Q.zoomBy });
    const start = camAt(at, true), end = viewToCam(v, start);
    S.camSegs.push({ at, dur: Math.max(0.05, +(Q.dur ?? 1.8)), start, end, ease: easeOf(Q.ease, MO.cubicInOut), arc: Q.arc });
  });
  // ---- year cartouche
  S.year = D.year != null ? String(D.year) : null;

  // ---- images: cue.image already preloaded by clip.js (ctx.IMG); data.sprites/icons images are loaded here
  S.IMG = ctx.IMG; const need = new Set();
  const want = u => { if (u && !ctx.IMG[u]) need.add(u); };
  S.sprites.forEach(s => { want(s.def.image); want(s.def.alt); s.acts.forEach(a => want(a.image)); });
  S.icons.forEach(i => want(i.def.image)); S.movers.forEach(m => want(m.image)); S.bubbles.forEach(b => { want(b.image); if (b.Q.icon && b.Q.icon.image) want(b.Q.icon.image); });
  if (need.size) {
    const base = (() => { const sp = new URLSearchParams(location.search).get('spec'); return sp ? sp.replace(/[^/]*$/, '') : ''; })();
    const fix = p => p && !/^(data:|https?:|\/|blob:)/.test(p) ? base + p : p;
    const loads = [...need].map(src => new Promise(res => { const im = new Image(); im.onload = () => { ctx.IMG[src] = im; res(); }; im.onerror = () => { console.error(`map: image failed to load: ${src}`); res(); }; im.src = fix(src); }));
    S.pending = true; const all = Promise.all(loads).then(() => { S.pending = false; });
    const prep = async () => { await all; };
    try { Object.defineProperty(window, 'prepare', { configurable: true, get: () => prep, set: () => {} }); } catch (e) { /* host owns prepare */ }
    // hosts that don't call prepare (YT Cartell's Go renderer) wait for window.__ready: hold it false until the images are in
    let clipReady = window.__ready === true;
    try { Object.defineProperty(window, '__ready', { configurable: true, get: () => clipReady && !S.pending, set: v => { clipReady = v; } }); } catch (e) { }
    window.__mapImages = all;
  }
}
function warn(m) { if (!S.warned.has(m)) { S.warned.add(m); console.warn(m); } }
function find(id) { if (id == null) return null; return S.regionById[id] || S.lineById[id] || S.iconById[id] || S.spriteById[id] || S.labelById[id] || null; }

// ---------------------------------------------------------------- regions
function addRegion(def, fallbackId) {
  if (typeof def === 'string') def = { preset: def };
  const pre = def.preset ? S_pre().regions[def.preset] : null;
  if (def.preset && !pre) warn(`map: unknown region preset "${def.preset}"`);
  const R = { kind: 'region', id: def.id || def.preset || (typeof def.country === 'string' ? def.country : null) || fallbackId, def, cev: [], ev: [] };
  if (S.regionById[R.id]) R.id = R.id + '_' + fallbackId;
  const countries = [].concat(def.countries || def.country || (pre && pre.countries) || []);
  const states = [].concat(def.states || def.state || (pre && pre.states) || []);
  let polys = def.polygons || (def.polygon ? (Array.isArray(def.polygon[0][0]) ? def.polygon : [def.polygon]) : null) || (pre && pre.polygons) || [];
  const bbox = def.bbox || (pre && pre.bbox) || null;
  const fpath = new Path2D(), ppath = new Path2D(), bb = [1e9, 1e9, -1e9, -1e9]; let nf = 0, np = 0;
  const addFeat = (rec) => {
    rec.polys.forEach(p => { if (bbox) { const cc = ringCenterLL(S.ll, p[0]); if (cc[0] < bbox[0] || cc[0] > bbox[2] || cc[1] < bbox[1] || cc[1] > bbox[3]) return; }
      p.forEach(r => { const pts = ringPts(S.wa, r); addRing(fpath, pts); pts.forEach(q => bboxAdd(bb, q)); }); nf++; });
  };
  for (const c0 of countries) { const c = String(c0); const rec = S.countries[c] || S.countries[c.toUpperCase()] || S.byName[c.toLowerCase()]; if (!rec) { warn(`map: unknown country "${c}" (use ISO-A3 / Natural Earth ADM0_A3, e.g. FRA, GBR, USA)`); continue; } addFeat(rec); }
  for (const s0 of states) { const rec = S.states[s0] || S.states[String(s0).toUpperCase()]; if (!rec) { warn(`map: unknown state "${s0}" (use ISO 3166-2, e.g. US-LA)`); continue; } addFeat(rec); }
  for (const poly of polys) { const pts = poly.map(p => S.proj(+p[0], +p[1])); addRing(ppath, pts); pts.forEach(q => bboxAdd(bb, q)); np++; }
  R.fpath = nf ? fpath : null; R.ppath = np ? ppath : null; R.bb = bb; R.multi = countries.length + states.length > 1;
  R.color = col(def.color ?? (pre && pre.color), '#3352a0');
  R.label = def.label === false ? null : tr(def.label ?? (pre && pre.label) ?? (countries.length === 1 ? countryName(countries[0]) : null), S.lang);
  const la = def.labelAt ? PLACE(def.labelAt) : pre && pre.labelAt ? pre.labelAt : null;
  let lw;
  if (la) lw = S.proj(la[0], la[1]);
  else if (countries.length === 1 && S.countries[countries[0]] && S.countries[countries[0]].props.lx != null) { const p = S.countries[countries[0]].props; lw = S.proj(p.lx, p.ly); }
  else lw = [(bb[0] + bb[2]) / 2, (bb[1] + bb[3]) / 2];
  R.labelW = lw; R.labelLL = la || null;
  R.labelSize = def.labelSize; R.labelStyle = def.labelStyle || 'country'; R.labelColor = def.labelColor;
  R.outline = def.outline !== false; R.opacity = def.opacity ?? 1;
  S.regions.push(R); S.regionById[R.id] = R; return R;
}
function countryName(c) { const rec = S.countries[c] || S.countries[String(c).toUpperCase()]; if (!rec) return c; return (rec.props.nm && rec.props.nm[S.lang]) || rec.props.n; }
const S_pre = () => window.MAPGRAM_PRESETS || { regions: {}, rivers: {} };

// ---------------------------------------------------------------- lines (rivers + routes)
function chainLines(lines) {   // greedy join of segments that touch, longest chains first
  const segs = lines.map(l => l.slice()); const out = []; const eq = (a, b) => Math.abs(a[0] - b[0]) < 1e-3 && Math.abs(a[1] - b[1]) < 1e-3;
  while (segs.length) { let ch = segs.shift(), grown = true;
    while (grown) { grown = false;
      for (let i = 0; i < segs.length; i++) { const s = segs[i];
        if (eq(ch[ch.length - 1], s[0])) ch = ch.concat(s.slice(1)); else if (eq(ch[ch.length - 1], s[s.length - 1])) ch = ch.concat(s.slice().reverse().slice(1));
        else if (eq(ch[0], s[s.length - 1])) ch = s.concat(ch.slice(1)); else if (eq(ch[0], s[0])) ch = s.slice().reverse().concat(ch.slice(1)); else continue;
        segs.splice(i, 1); grown = true; break; } }
    out.push(ch); }
  return out.sort((a, b) => b.length - a.length);
}
function curvePts(P, bend, curve) {
  if (P.length === 2) { const [a, b] = P, mx = (a[0] + b[0]) / 2, my = (a[1] + b[1]) / 2, dx = b[0] - a[0], dy = b[1] - a[1], cx = mx - dy * bend, cy = my + dx * bend, o = [];
    for (let i = 0; i <= 64; i++) { const t = i / 64, v = 1 - t; o.push([v * v * a[0] + 2 * v * t * cx + t * t * b[0], v * v * a[1] + 2 * v * t * cy + t * t * b[1]]); } return o; }
  if (curve === false) return P;
  const o = []; for (let i = 0; i < P.length - 1; i++) { const p0 = P[i - 1] || P[i], p1 = P[i], p2 = P[i + 1], p3 = P[i + 2] || P[i + 1];
    for (let j = 0; j < 20; j++) { const t = j / 20, t2 = t * t, t3 = t2 * t;
      o.push([0.5 * (2 * p1[0] + (-p0[0] + p2[0]) * t + (2 * p0[0] - 5 * p1[0] + 4 * p2[0] - p3[0]) * t2 + (-p0[0] + 3 * p1[0] - 3 * p2[0] + p3[0]) * t3),
              0.5 * (2 * p1[1] + (-p0[1] + p2[1]) * t + (2 * p0[1] - 5 * p1[1] + 4 * p2[1] - p3[1]) * t2 + (-p0[1] + 3 * p1[1] - 3 * p2[1] + p3[1]) * t3)]); } }
  o.push(P[P.length - 1]); return o;
}
function addLine(def, kind, fallbackId) {
  if (typeof def === 'string') def = kind === 'river' ? { river: def } : { id: def };
  const L = { kind: 'line', type: kind, id: def.id || (kind === 'river' && typeof def.river === 'string' ? def.river : null) || fallbackId, def, ev: [] };
  if (S.lineById[L.id]) L.id = L.id + '_' + fallbackId;
  let chainsLL = [];
  const preR = kind === 'river' && def.river ? S_pre().rivers[def.river] : null;
  if (def.line || def.points && kind === 'river') chainsLL = [def.line || def.points];
  else if (kind === 'river') {
    if (preR) chainsLL = [preR.line];
    else { const g = S.neRivers[String(def.river || '').toLowerCase().replace(/[^a-z0-9]+/g, '_')];
      if (!g) warn(`map: unknown river "${def.river}" (presets: ${Object.keys(S_pre().rivers).join(', ')}; or a Natural Earth river id like missouri, ohio, rhine, danube, nile, volga, seine)`);
      else { const raw = g.type === 'LineString' ? [g.arcs] : g.arcs; chainsLL = chainLines(raw.map(arcsL => ringPts(S.ll, arcsL))); if (def.bbox) chainsLL = chainsLL.map(ch => ch.filter(p => p[0] >= def.bbox[0] && p[0] <= def.bbox[2] && p[1] >= def.bbox[1] && p[1] <= def.bbox[3])).filter(ch => ch.length > 1);
        if (def.main !== false) chainsLL = chainsLL.slice(0, def.chains || 1); }
    }
    if (def.start) { const s = PLACE(def.start); chainsLL = chainsLL.map(ch => { const a = ch[0], b = ch[ch.length - 1]; return Math.hypot(a[0] - s[0], a[1] - s[1]) <= Math.hypot(b[0] - s[0], b[1] - s[1]) ? ch : ch.slice().reverse(); }); }
    if (def.reverse) chainsLL = chainsLL.map(ch => ch.slice().reverse());
  } else {
    const P = (def.points ? def.points : [def.from, ...(def.via || []), def.to]).map(PLACE).filter(Boolean);
    if (P.length < 2) warn(`map: route "${L.id}" needs from/to or points`);
    const W = P.map(p => S.proj(p[0], p[1]));
    L.chains = P.length >= 2 ? [curvePts(W, def.bend ?? def.curve ?? 0.18, def.curve)] : [];
  }
  if (!L.chains) L.chains = chainsLL.map(ch => ch.map(p => S.proj(+p[0], +p[1])));
  L.chains.forEach(ch => { const cum = [0]; for (let i = 1; i < ch.length; i++) cum.push(cum[i - 1] + Math.hypot(ch[i][0] - ch[i - 1][0], ch[i][1] - ch[i - 1][1])); ch.cum = cum; ch.len = cum[cum.length - 1]; });
  const isR = kind === 'river';
  L.style = def.style || (isR ? 'glow' : 'dashed');
  L.color = col(def.color, isR ? S.pal.river : S.pal.route);
  L.width = +(def.width ?? (isR ? 4 : 5));
  L.arrow = def.arrow ?? !isR;
  L.wave = def.wave ?? (isR ? 2.4 : 0);
  L.dash = def.dash || [16, 11];
  L.label = tr(def.label ?? (preR && preR.label) ?? null, S.lang);
  const la = def.labelAt ? PLACE(def.labelAt) : preR && preR.labelAt ? preR.labelAt : null;
  L.labelW = la ? S.proj(la[0], la[1]) : (L.chains[0] ? L.chains[0][Math.floor(L.chains[0].length / 2)] : null);
  L.show0 = def.show;
  S.lines.push(L); S.lineById[L.id] = L; return L;
}

// ---------------------------------------------------------------- icons, sprites, labels
function addIcon(def, fallbackId) {
  const I = { kind: 'icon', id: def.id || fallbackId, def, type: String(def.type || def.icon || 'text').toLowerCase(), ev: [] };
  if (typeof def.icon === 'object') Object.assign(I, { def: Object.assign({}, def, def.icon), type: String(def.icon.type || 'text').toLowerCase() });
  S.icons.push(I); S.iconById[I.id] = I; return I;
}
function addSprite(def, fallbackId) {
  const sp = { kind: 'sprite', id: def.id || fallbackId, def, acts: [], seed: strHash(def.id || fallbackId) % 1000 };
  sp.ll = llOf(def) || [0, 0];
  S.sprites.push(sp); S.spriteById[sp.id] = sp; return sp;
}
function addLabel(def, fallbackId) {
  const L = { kind: 'label', id: def.id || fallbackId, def, ev: [] };
  S.labels.push(L); S.labelById[L.id] = L; return L;
}
// generic show/hide envelope. returns { a: 0..1, lt (since last show), showAt }
function vis(el, t, defaultShown) {
  let shown = el.def && el.def.show != null ? !!el.def.show : (el.revealed ? false : defaultShown);
  let a = shown ? 1 : 0, lt = 1e9, showAt = shown ? -1e9 : null;
  for (const e of el.ev) { const l = t - e.at + 1 / S.fps; if (l <= 0) break;
    const p = clamp(l / Math.max(1e-3, e.dur));
    if (e.type === 'show') { a = Math.max(a, p); lt = l; showAt = e.at; shown = true; }
    else { a = Math.min(a, 1 - p); if (p >= 1) shown = false; }
  }
  return { a, lt, showAt };
}

// ======================================================================= camera
function viewToCam(v, from) {
  if (typeof v === 'string') v = { place: v };
  v = v || {};
  const box = S.box;
  if (v.bbox) { const [w, s, e, n] = v.bbox; const pts = [[w, s], [e, s], [w, n], [e, n], [(w + e) / 2, s], [(w + e) / 2, n], [w, (s + n) / 2], [e, (s + n) / 2]].map(p => S.proj(p[0], p[1]));
    const b = [1e9, 1e9, -1e9, -1e9]; pts.forEach(p => bboxAdd(b, p)); const k = Math.min(box.w / (b[2] - b[0]), box.h / (b[3] - b[1])) * (v.pad ?? 0.9);
    return { x: (b[0] + b[2]) / 2, y: (b[1] + b[3]) / 2, z: k * 360 / S.minSide }; }
  const p = v.place ? PLACE(v.place) : null;
  const lon = v.lon ?? (p ? p[0] : null), lat = v.lat ?? (p ? p[1] : null);
  const w = lon != null && lat != null ? S.proj(+lon, +lat) : from ? [from.x, from.y] : [0, 0];
  const z0 = from ? from.z : (S.cam0 ? S.cam0.z : 1.6);   // zoomBy: relative to the current zoom (a gentle push-in / pull-out)
  return { x: w[0], y: w[1], z: v.zoomBy != null && v.zoom == null && v.z == null ? z0 * clamp(+v.zoomBy || 1, 0.2, 5) : +(v.zoom ?? v.z ?? z0) };
}
// base camera (no drift); creep = gentle push-in after each move settles
function camAt(t, noCreep) {
  let cam = { ...S.cam0 }, settled = 0;
  for (const s of S.camSegs) {
    const l = t - s.at; if (l < 0) break;
    const e = s.ease(clamp(l / s.dur));
    const zl = Math.min(s.start.z, s.end.z), dist = Math.hypot(s.end.x - s.start.x, s.end.y - s.start.y) * zl * S.minSide / 360 / S.minSide;   // in screens at the wider view
    const dip = s.arc === false ? 0 : clamp((dist - 0.9) / 3, 0, 0.55) * (s.arc === true ? 1.3 : 1);
    cam = { x: lerp(s.start.x, s.end.x, e), y: lerp(s.start.y, s.end.y, e), z: CAM.zlerp(s.start.z, s.end.z, e) * (1 - dip * Math.sin(Math.PI * e)) };
    settled = s.at + s.dur;
  }
  if (!noCreep && S.drift) { const dt = Math.max(0, t - settled); cam.z *= 1 + 0.07 * S.drift * (1 - Math.exp(-dt / 7)); }
  return cam;
}
function camera(t) {
  const c0 = camAt(t), k = c0.z * S.minSide / 360, d = S.drift, u = S.u;
  const dx = d * u * (7 * Math.sin(t * 0.31 + 1.3) + 3 * Math.sin(t * 0.73 + 0.4)), dy = d * u * (5 * Math.sin(t * 0.27 + 2.1) + 2 * Math.sin(t * 0.61));
  let jx = 0;
  for (const r of S.rewinds) { const l = t - r.at; if (l > 0 && l < r.dur) jx += (hsh(Math.floor(t * 24), 7) - 0.5) * 10 * u * Math.sin(Math.PI * l / r.dur); }
  return { x: c0.x - (dx + jx) / k, y: c0.y - dy / k, k: k * (1 + 0.006 * d * Math.sin(t * 0.43)), ax: S.anchor[0], ay: S.anchor[1], z: c0.z };
}
const toS = (V, w) => [V.ax + (w[0] - V.x) * V.k, V.ay + (w[1] - V.y) * V.k];
const llS = (V, ll) => toS(V, S.proj(ll[0], ll[1]));

// ======================================================================= world layers
function worldRect(V, r) { return { x: V.x + (r[0] - V.ax) / V.k, y: V.y + (r[1] - V.ay) / V.k, w: r[2] / V.k, h: r[3] / V.k }; }
function drawBase(c, V, rect) {
  const P = S.pal, u = S.u, wr = worldRect(V, rect), lw = px => px / V.k;
  c.save(); c.transform(V.k, 0, 0, V.k, V.ax - V.x * V.k, V.ay - V.y * V.k);
  // ocean
  c.fillStyle = P.ocean; c.fillRect(wr.x, wr.y, wr.w, wr.h);
  texture(c, V, S.tex.sea, 2.2 * u, 0.55, 'soft-light', wr);
  texture(c, V, S.tex.mottle, 3.2 * u, 0.45, 'soft-light', wr);
  // coastal halo on the water
  c.lineJoin = 'round'; c.lineCap = 'round';
  c.strokeStyle = 'rgba(214,232,222,0.10)'; c.lineWidth = lw(16 * u); c.stroke(S.coast);
  c.strokeStyle = 'rgba(220,236,226,0.10)'; c.lineWidth = lw(6 * u); c.stroke(S.coast);
  // land
  c.fillStyle = P.land; c.fill(S.land, 'evenodd');
  c.save(); c.clip(S.land, 'evenodd');
  texture(c, V, S.tex.mottle, 2.6 * u, 0.85, 'soft-light', wr);
  texture(c, V, S.tex.relief, 1.6 * u, 0.8, 'soft-light', wr);
  texture(c, V, S.tex.grain, 1.1 * u, 0.5, 'soft-light', wr);
  c.strokeStyle = 'rgba(70,52,30,0.22)'; c.lineWidth = lw(9 * u); c.stroke(S.coast);   // inner shade along the shore
  c.restore();
  if (S.borders) { c.strokeStyle = 'rgba(60,45,28,0.22)'; c.lineWidth = lw(1 * u); c.stroke(S.bord); }
  c.strokeStyle = rgba(P.coast, 0.78); c.lineWidth = lw(1.5 * u); c.stroke(S.coast);
  c.restore();
}
function drawLakes(c, V, rect) {
  const u = S.u, wr = worldRect(V, rect);
  c.save(); c.transform(V.k, 0, 0, V.k, V.ax - V.x * V.k, V.ay - V.y * V.k);
  c.fillStyle = rgba(S.pal.ocean, 0.92); c.fill(S.lakes, 'evenodd');
  c.save(); c.clip(S.lakes, 'evenodd'); texture(c, V, S.tex.sea, 2.2 * u, 0.4, 'soft-light', wr); c.restore();
  c.strokeStyle = rgba(S.pal.coast, 0.6); c.lineWidth = 1.2 * u / V.k; c.stroke(S.lakes);
  c.restore();
}
// region state at t: { layers:[{color, mask}], alpha, p (progress of the last colour change), ev }
// timeline = fill/recolor events (cev) + show/hide events (ev), merged in time order. A region that has a fill cue (and no explicit show)
// is invisible until its first fill; every fill makes it visible again (after a hide too).
function regionState(R, t) {
  const fps = S.fps, fillReveal = R.def.show == null && R.cev.some(e => e.fill), hasShow = R.ev.some(e => e.type === 'show');
  let layers = fillReveal ? [] : [{ color: R.color }], a = R.def.show === false || (!fillReveal && hasShow && R.def.show == null) ? 0 : 1, lastP = 1, ev = null;
  const all = R.cev.map(e => ({ e, c: 1 })).concat(R.ev.map(e => ({ e, c: 0 }))).sort((x, y) => x.e.at - y.e.at);
  for (const { e, c } of all) { const l = t - e.at + 1 / fps; if (l <= 0) break;
    if (c) { const p = e.ease(clamp(l / Math.max(1e-3, e.dur)));
      if (e.fill && a < 1) { a = 1; layers = []; }
      layers = p >= 1 ? [{ color: e.color }] : layers.filter(x => !x.mask).concat([{ color: e.color, mask: { e, p } }]);
      lastP = p; ev = e; }
    else { const p = clamp(l / Math.max(1e-3, e.dur)); if (e.type === 'show') { if (!layers.length) layers = [{ color: R.color }]; a = Math.max(a, p); } else a = Math.min(a, 1 - p); }
  }
  return { layers, alpha: R.opacity * a, p: lastP, ev };
}
function regionOrigin(R, e) {
  const f = e.from;
  if (typeof f === 'string' && /^(west|east|north|south|left|right|top|bottom)$/.test(f)) return { dir: f };
  const ll = f != null ? PLACE(f) : null; return { pt: ll ? S.proj(ll[0], ll[1]) : R.labelW };
}
function maskClip(c, R, m) {
  const b = R.bb, o = regionOrigin(R, m.e), p = m.p;
  const path = new Path2D();
  if (m.e.mode === 'wipe' || o.dir) {
    const dir = o.dir || 'west', pad = 0.02 * (b[2] - b[0] + b[3] - b[1]);
    if (dir === 'west' || dir === 'left') path.rect(b[0] - pad, b[1] - pad, (b[2] - b[0] + 2 * pad) * p, b[3] - b[1] + 2 * pad);
    else if (dir === 'east' || dir === 'right') { const w = (b[2] - b[0] + 2 * pad) * p; path.rect(b[2] + pad - w, b[1] - pad, w, b[3] - b[1] + 2 * pad); }
    else if (dir === 'north' || dir === 'top') path.rect(b[0] - pad, b[1] - pad, b[2] - b[0] + 2 * pad, (b[3] - b[1] + 2 * pad) * p);
    else { const h = (b[3] - b[1] + 2 * pad) * p; path.rect(b[0] - pad, b[3] + pad - h, b[2] - b[0] + 2 * pad, h); }
    return { path };
  }
  const pt = o.pt, rmax = Math.max(Math.hypot(b[0] - pt[0], b[1] - pt[1]), Math.hypot(b[2] - pt[0], b[1] - pt[1]), Math.hypot(b[0] - pt[0], b[3] - pt[1]), Math.hypot(b[2] - pt[0], b[3] - pt[1])) * 1.02;
  // slightly wobbly circle so the spread reads as ink, not as a compass
  const r = rmax * p, n = 72;
  for (let i = 0; i <= n; i++) { const a = i / n * TAU, rr = r * (1 + 0.05 * Math.sin(a * 5 + 1.3) + 0.03 * Math.sin(a * 11)); const x = pt[0] + Math.cos(a) * rr, y = pt[1] + Math.sin(a) * rr; i ? path.lineTo(x, y) : path.moveTo(x, y); }
  path.closePath(); return { path, edge: true };
}
function regionBody(c, V, R, color, withOutline) {
  const u = S.u, lw = px => px / V.k;
  c.lineJoin = 'round';
  if (withOutline && R.outline) { c.strokeStyle = S.pal.outline; c.lineWidth = lw(((R.fpath && R.ppath) || R.multi ? 8.8 : 7) * u);
    if (R.fpath) c.stroke(R.fpath);
    if (R.ppath) { c.save(); c.clip(S.land, 'evenodd'); c.stroke(R.ppath); c.restore(); c.save(); c.clip(R.ppath, 'evenodd'); c.stroke(S.coast); c.restore(); } }
  c.fillStyle = color;
  if (R.fpath) c.fill(R.fpath, 'evenodd');
  if (R.ppath) { c.save(); c.clip(S.land, 'evenodd'); c.fill(R.ppath, 'evenodd'); c.restore(); }
  // paint over antialiasing seams between the parts (eats the inner 0.9px of the outline, which is drawn wider to compensate)
  if ((R.fpath && R.ppath) || R.multi) { c.strokeStyle = color; c.lineWidth = lw(1.8 * u);
    if (R.fpath) c.stroke(R.fpath);
    if (R.ppath) { c.save(); c.clip(S.land, 'evenodd'); c.stroke(R.ppath); c.restore(); } }
}
function regionTexture(c, V, R, wr) {
  const u = S.u;
  const doTex = () => { texture(c, V, S.tex.mottle, 2.6 * u, 0.42, 'soft-light', wr); texture(c, V, S.tex.relief, 1.6 * u, 0.32, 'soft-light', wr); texture(c, V, S.tex.grain, 1.1 * u, 0.3, 'soft-light', wr); };
  if (R.fpath) { c.save(); c.clip(R.fpath, 'evenodd'); doTex(); c.restore(); }
  if (R.ppath) { c.save(); c.clip(S.land, 'evenodd'); c.clip(R.ppath, 'evenodd'); doTex(); c.restore(); }
}
function drawRegions(c, V, t, rect) {
  const wr = worldRect(V, rect);
  c.save(); c.transform(V.k, 0, 0, V.k, V.ax - V.x * V.k, V.ay - V.y * V.k);
  for (const R of S.regions) {
    const st = regionState(R, t); if (st.alpha <= 0.001 || !st.layers.length) continue;
    c.save(); c.globalAlpha = st.alpha;
    for (const L of st.layers) {
      c.save();
      let mk = null;
      if (L.mask) { mk = maskClip(c, R, L.mask); c.clip(mk.path); }
      regionBody(c, V, R, L.color, true);
      regionTexture(c, V, R, wr);
      if (mk && mk.edge && L.mask.p < 1) {   // bright leading edge of the spreading ink
        c.save(); if (R.fpath) c.clip(R.fpath, 'evenodd'); else { c.clip(S.land, 'evenodd'); c.clip(R.ppath, 'evenodd'); }
        c.strokeStyle = rgba(mixHex(L.color, '#ffffff', 0.55), 0.55 * (1 - L.mask.p)); c.lineWidth = 10 * S.u / V.k; c.stroke(mk.path); c.restore();
      }
      c.restore();
    }
    c.restore();
  }
  c.restore();
}
function strokeRegionEdge(c, V, R) {
  if (R.fpath) c.stroke(R.fpath);
  if (R.ppath) { c.save(); c.clip(S.land, 'evenodd'); c.stroke(R.ppath); c.restore(); c.save(); c.clip(R.ppath, 'evenodd'); c.stroke(S.coast); c.restore(); }
}
function glowAmt(el, t) {
  let a = 0, g0 = null;
  for (const g of el.glows || []) { const l = t - g.at + 1 / S.fps; if (l <= 0) continue; const fin = clamp(l / 0.35), fout = 1 - clamp((l - g.dur) / 0.4); const v = fin * fout; if (v > a) { a = v; g0 = g; } }
  if (!g0) return null; const pulse = g0.pulse ? 0.72 + 0.28 * Math.sin((t - g0.at) * 5.5) : 1; return { a: a * pulse, g: g0 };
}
// Glow = outer halo + inner rim of the region's SILHOUETTE (rendered to a scratch canvas), so borders between the
// countries/polygons that make up one region never glow — only its outside edge does.
function scratch(i) { S.scr = S.scr || []; if (!S.scr[i]) { const cv = document.createElement('canvas'); cv.width = S.W; cv.height = S.H; S.scr[i] = cv; } const g = S.scr[i].getContext('2d'); g.setTransform(1, 0, 0, 1, 0, 0); g.globalCompositeOperation = 'source-over'; g.globalAlpha = 1; g.shadowColor = 'transparent'; g.clearRect(0, 0, S.W, S.H); return [S.scr[i], g]; }
function shadowOnly(g, img, color, blur) { const off = S.W * 2; g.save(); g.shadowColor = color; g.shadowBlur = blur; g.shadowOffsetX = off; g.drawImage(img, -off, 0); g.restore(); }
function drawRegionGlows(c, V, t) {
  for (const R of S.regions) { const G = glowAmt(R, t); if (!G) continue; const st = regionState(R, t); if (st.alpha <= 0.01) continue;
    const u = S.u, col0 = G.g.color, a = G.a * st.alpha;
    const [sil, g1] = scratch(0);
    g1.setTransform(V.k, 0, 0, V.k, V.ax - V.x * V.k, V.ay - V.y * V.k); g1.fillStyle = '#fff';
    if (R.fpath) g1.fill(R.fpath, 'evenodd');
    if (R.ppath) { g1.save(); g1.clip(S.land, 'evenodd'); g1.fill(R.ppath, 'evenodd'); g1.restore(); }
    // close antialiasing hairlines between the parts (countries, polygons) so they don't light up
    g1.strokeStyle = '#fff'; g1.lineWidth = 2.5 / V.k; g1.lineJoin = 'round';
    if (R.fpath) g1.stroke(R.fpath);
    if (R.ppath) { g1.save(); g1.clip(S.land, 'evenodd'); g1.stroke(R.ppath); g1.restore(); }
    g1.setTransform(1, 0, 0, 1, 0, 0);
    const [out, g2] = scratch(1); g2.fillStyle = '#fff'; g2.fillRect(0, 0, S.W, S.H); g2.globalCompositeOperation = 'destination-out'; g2.drawImage(sil, 0, 0);
    const [fx, g3] = scratch(2);
    // outer halo
    shadowOnly(g3, sil, col0, (G.g.width || 1) * 30 * u); shadowOnly(g3, sil, col0, 10 * u);
    g3.globalCompositeOperation = 'destination-out'; g3.drawImage(sil, 0, 0); g3.globalCompositeOperation = 'source-over';
    c.save(); c.globalAlpha = a; c.drawImage(fx, 0, 0); c.restore();
    // inner rim: blurred outside, kept only inside the silhouette
    const [rim, g4] = scratch(2);
    shadowOnly(g4, out, col0, 16 * u); shadowOnly(g4, out, '#ffffff', 3.5 * u); shadowOnly(g4, out, '#ffffff', 2 * u);
    g4.globalCompositeOperation = 'destination-in'; g4.drawImage(sil, 0, 0);
    c.save(); c.globalAlpha = a; c.globalCompositeOperation = 'lighter'; c.drawImage(rim, 0, 0); c.globalCompositeOperation = 'source-over'; c.globalAlpha = a * 0.6; c.drawImage(rim, 0, 0); c.restore(); }
}

// ---------------------------------------------------------------- lines
function partialScreen(V, ch, p, t, L) {
  // screen points of the chain up to fraction p, densified so waves look smooth
  const u = S.u, target = ch.len * p, out = [];
  let i = 1; out.push(toS(V, ch[0]));
  for (; i < ch.length && ch.cum[i] <= target; i++) out.push(toS(V, ch[i]));
  if (i < ch.length && p < 1) { const a = ch[i - 1], b = ch[i], f = (target - ch.cum[i - 1]) / Math.max(1e-9, ch.cum[i] - ch.cum[i - 1]); out.push(toS(V, [lerp(a[0], b[0], f), lerp(a[1], b[1], f)])); }
  // densify
  const d = []; for (let j = 0; j < out.length; j++) { if (j) { const a = out[j - 1], b = out[j], n = Math.min(40, Math.floor(Math.hypot(b[0] - a[0], b[1] - a[1]) / (5 * u))); for (let k = 1; k < n; k++) d.push([lerp(a[0], b[0], k / n), lerp(a[1], b[1], k / n)]); } d.push(out[j]); }
  if (L.wave > 0 || L.style === 'electric') {   // perpendicular meander, alive in time; fades at both ends
    let s = 0; const res = [d[0]], tot = d.reduce((acc, q, j) => j ? acc + Math.hypot(q[0] - d[j - 1][0], q[1] - d[j - 1][1]) : 0, 0);
    const fr = Math.floor(t * 12);
    for (let j = 1; j < d.length; j++) { const a = d[j - 1], b = d[j], sl = Math.hypot(b[0] - a[0], b[1] - a[1]); s += sl; if (sl < 1e-6) continue;
      const nx = -(b[1] - a[1]) / sl, ny = (b[0] - a[0]) / sl, env = clamp(s / (30 * u)) * clamp((tot - s) / (30 * u));
      let off = L.wave * u * (Math.sin(s / (23 * u) + t * 3.1) * 0.7 + Math.sin(s / (9 * u) - t * 4.3) * 0.3);
      if (L.style === 'electric') off += (hsh(j, fr) - 0.5) * 7 * u;
      res.push([b[0] + nx * off * env, b[1] + ny * off * env]); }
    return res;
  }
  return d;
}
function polyStroke(c, pts) { c.beginPath(); pts.forEach((p, i) => i ? c.lineTo(p[0], p[1]) : c.moveTo(p[0], p[1])); c.stroke(); }
function lineProgress(L, t) {
  let p = L.show0 === true ? 1 : L.show0 === false ? 0 : (L.revealed ? 0 : 1), alpha = 1, drawing = false;
  for (const e of L.ev) { const l = t - e.at + 1 / S.fps; if (l <= 0) break;
    if (e.type === 'show') { p = e.ease ? e.ease(clamp(l / e.dur)) : 1; drawing = p < 1; alpha = 1; if (!e.ease) alpha = clamp(l / e.dur); }
    else alpha = Math.min(alpha, 1 - clamp(l / e.dur)); }
  return { p, alpha, drawing };
}
function drawLines(c, V, t, mini) {
  const u = S.u;
  for (const L of S.lines) {
    const st = lineProgress(L, t); if (st.p <= 0 || st.alpha <= 0) continue;
    const G = glowAmt(L, t);
    c.save(); c.globalAlpha = st.alpha; c.lineJoin = 'round'; c.lineCap = 'round';
    for (const ch of L.chains) { if (ch.length < 2) continue;
      const pts = partialScreen(V, ch, st.p, t, L); if (pts.length < 2) continue;
      const w = L.width * u * (mini ? 0.6 : 1);
      if (L.style === 'glow' || L.style === 'electric') {
        const gc = G ? G.g.color : L.color, boost = G ? 1 + 0.6 * G.a : 1;
        c.save(); c.globalCompositeOperation = 'source-over'; c.strokeStyle = rgba(gc, 0.38 * boost); c.shadowColor = gc; c.shadowBlur = 22 * u * boost; c.lineWidth = w * 2.6; polyStroke(c, pts); c.restore();
        c.strokeStyle = gc; c.shadowColor = gc; c.shadowBlur = 9 * u; c.lineWidth = w * 1.2; polyStroke(c, pts);
        c.shadowBlur = 0; c.strokeStyle = '#ffffff'; c.lineWidth = w * 0.5; polyStroke(c, pts);
      } else {
        if (G) { c.save(); c.strokeStyle = rgba(G.g.color, 0.6 * G.a); c.shadowColor = G.g.color; c.shadowBlur = 20 * u; c.lineWidth = w * 2.6; polyStroke(c, pts); c.restore(); }
        c.strokeStyle = 'rgba(20,16,10,0.45)'; c.lineWidth = w + 3.5 * u; if (L.style === 'dashed') { c.setLineDash(L.dash.map(x => x * u)); c.lineDashOffset = -t * 26 * u; }
        c.save(); c.translate(1.5 * u, 2 * u); polyStroke(c, pts); c.restore();
        c.strokeStyle = L.color; c.lineWidth = w; polyStroke(c, pts); c.setLineDash([]);
      }
      // head: arrow for routes, spark for rivers while drawing
      const a = pts[pts.length - 1], b = pts[Math.max(0, pts.length - 4)], ang = Math.atan2(a[1] - b[1], a[0] - b[0]);
      if (L.arrow && L.style !== 'glow') { const s = (L.width * 3.4 + 6) * u * (mini ? 0.6 : 1);
        c.save(); c.translate(a[0], a[1]); c.rotate(ang); c.beginPath(); c.moveTo(s * 0.55, 0); c.lineTo(-s * 0.6, -s * 0.55); c.lineTo(-s * 0.3, 0); c.lineTo(-s * 0.6, s * 0.55); c.closePath();
        c.fillStyle = 'rgba(20,16,10,0.45)'; c.save(); c.translate(1.5 * u, 2 * u); c.fill(); c.restore(); c.fillStyle = L.color; c.fill(); c.restore(); }
      else if (st.drawing && (L.style === 'glow' || L.style === 'electric')) { const r = 7 * u; c.save(); c.globalCompositeOperation = 'lighter'; const g = c.createRadialGradient(a[0], a[1], 0, a[0], a[1], r * 3);
        g.addColorStop(0, 'rgba(255,255,255,0.95)'); g.addColorStop(0.3, rgba(L.color, 0.7)); g.addColorStop(1, rgba(L.color, 0)); c.fillStyle = g; c.beginPath(); c.arc(a[0], a[1], r * 3, 0, TAU); c.fill(); c.restore(); }
    }
    c.restore();
  }
}

// ---------------------------------------------------------------- labels
function textBlock(c, lines, x, y, size, o) {
  const oblique = !!o.italic && lines.some(l => CYR.test(l));
  c.save(); c.font = fnt(size, o.italic && !oblique); c.textAlign = o.align || 'center'; c.textBaseline = 'middle';
  try { c.letterSpacing = `${(o.spacing ?? 0.02) * size}px`; } catch (e) { }
  const lh = size * (o.lh || 0.98), y0 = y - (lines.length - 1) * lh / 2;
  if (o.rot) { c.translate(x, y); c.rotate(o.rot); c.translate(-x, -y); }
  lines.forEach((ln, i) => {
    const yy = y0 + i * lh;
    c.save(); c.translate(x, yy); if (oblique) c.transform(1, 0, -OBLIQUE, 1, 0, 0);
    if (o.outline) { c.lineJoin = 'round'; c.strokeStyle = o.outline; c.lineWidth = o.outlineW || size * 0.14; c.strokeText(ln, 0, 0); }
    if (o.shadow) { c.shadowColor = o.shadow; c.shadowBlur = o.shadowBlur ?? size * 0.12; c.shadowOffsetY = o.shadowY ?? size * 0.03; }
    c.fillStyle = o.color; c.fillText(ln, 0, 0);
    if (o.bold) { c.shadowColor = 'transparent'; c.strokeStyle = o.color; c.lineWidth = size * o.bold; c.strokeText(ln, 0, 0); }
    c.restore();
  });
  c.restore();
}
// the screen box of a text block (unrotated): [x0, y0, x1, y1]
function textBox(c, lines, x, y, size, o) {
  c.save(); c.font = fnt(size, o.italic); try { c.letterSpacing = `${(o.spacing ?? 0.02) * size}px`; } catch (e) { }
  const w = Math.max(...lines.map(l => c.measureText(l).width)); c.restore();
  const h = size * ((o.lh || 0.98) * (lines.length - 1) + 1), al = o.align || 'center', x0 = al === 'left' ? x : al === 'right' ? x - w : x - w / 2;
  return [x0, y - h / 2, x0 + w, y + h / 2];
}
const STYLE = {
  country: { size: 46, color: '#f2ead8', shadow: 'rgba(15,10,5,0.55)' },
  land: { size: 40, color: '#3a2d1d', alpha: 0.82, shadow: 'rgba(255,245,220,0.25)', shadowBlur: 2 },
  water: { size: 40, color: '#e2ecea', italic: true, alpha: 0.82, spacing: 0.12, shadow: 'rgba(10,30,35,0.35)' },
  river: { size: 40, color: '#f4d84f', italic: true, shadow: 'rgba(0,0,0,0.6)' },
  city: { size: 32, color: '#2a2016', shadow: 'rgba(255,248,230,0.55)', shadowBlur: 3, dot: true },
  title: { size: 84, color: '#fbf5e6', shadow: 'rgba(0,0,0,0.6)', shadowBlur: 18 },
  note: { size: 46, color: '#ffffff', outline: 'rgba(25,18,10,0.9)', bold: 0.035 },
};
function zoomFactor(V, ref) { return clamp(Math.pow(V.z / (ref || S.z0), 0.5), 0.5, 2.4); }
// how much a later (higher) region covers this region's label point (0..1): e.g. "France" fades when "French Empire" spreads over it
function coveredBy(R, t) {
  S.hit = S.hit || document.createElement('canvas').getContext('2d'); let cov = 0; const i = S.regions.indexOf(R), [x, y] = R.labelW;
  for (let j = i + 1; j < S.regions.length; j++) { const O = S.regions[j]; if (!(O.fpath && S.hit.isPointInPath(O.fpath, x, y, 'evenodd')) && !(O.ppath && S.hit.isPointInPath(O.ppath, x, y, 'evenodd'))) continue;
    const st = regionState(O, t); if (!st.layers.length) continue; const last = st.layers[st.layers.length - 1];
    cov = Math.max(cov, st.alpha * (last.mask ? clamp((st.p - 0.3) / 0.4) : 1)); }
  return cov;
}
function drawRegionLabels(c, V, t) {
  for (const R of S.regions) { if (!R.label) continue; const st = regionState(R, t); if (st.alpha <= 0.01 || !st.layers.length) continue;
    const last = st.layers[st.layers.length - 1]; let a = st.alpha * (last.mask && st.ev && st.ev.fill && st.layers.length === 1 ? clamp((st.p - 0.45) / 0.4) : 1);
    a *= 1 - coveredBy(R, t);
    if (a <= 0.01) continue;
    const s = toS(V, R.labelW), lines = R.label.split('\n'), sw = (R.bb[2] - R.bb[0]) * V.k;
    c.save(); c.font = fnt(100); const wmax = Math.max(...lines.map(l => c.measureText(l).width)) / 100; c.restore();
    const ST = STYLE[R.labelStyle] || STYLE.country;
    let size = clamp(sw * 0.42 / Math.max(1, wmax), 22 * S.u, (R.labelSize ? R.labelSize * zoomFactor(V) : 70) * S.u);
    const o = Object.assign({}, ST, { color: R.labelColor || ST.color }), sh = labelShift(textBox(c, lines, s[0], s[1], size, o));
    c.save(); c.globalAlpha = a * (ST.alpha || 1) * (R.def.labelOpacity ?? 0.92); textBlock(c, lines, s[0] + sh[0], s[1] + sh[1], size, o); c.restore(); }
  for (const L of S.lines) { if (!L.label || !L.labelW) continue; const st = lineProgress(L, t); const a = st.alpha * clamp((st.p - 0.55) / 0.3); if (a <= 0.01) continue;
    const s = toS(V, L.labelW), ST = STYLE.river; const size = (L.def.labelSize || ST.size) * S.u * zoomFactor(V);
    const o = Object.assign({}, ST, { color: L.def.labelColor || ST.color, rot: (L.def.labelRotate || 0) * D2R }), lines = L.label.split('\n'), sh = labelShift(textBox(c, lines, s[0], s[1], size, o));
    c.save(); c.globalAlpha = a; textBlock(c, lines, s[0] + sh[0], s[1] + sh[1], size, o); c.restore(); }
}
function screenPos(def, V) {
  if (def.pos) return [S.box.x + def.pos[0] * S.box.w, S.box.y + def.pos[1] * S.box.h];
  const ll = llOf(def); if (ll) return llS(V, ll);
  if (def.sprite && S.spriteById[def.sprite]) return llS(V, S.spriteById[def.sprite].ll);
  return [S.anchor[0], S.box.y + S.box.h * 0.32];
}
function drawLabels(c, V, t) {
  for (const L of S.labels) { const v = vis(L, t, true); if (v.a <= 0.01) continue;
    const d = L.def, ST = STYLE[d.style] || STYLE.land, txt = tr(d.text, S.lang) || ''; if (!txt) continue;
    const p = screenPos(d, V), scaled = d.pos ? 1 : (d.scale === false ? 1 : zoomFactor(V, d.zoomRef));
    const size = (d.size || ST.size) * S.u * scaled, rise = (1 - MO.quintOut(clamp(v.lt / 0.5))) * 14 * S.u;
    c.save(); c.globalAlpha = v.a * (ST.alpha || 1);
    if (ST.dot && !d.pos) { c.fillStyle = '#1c1610'; c.beginPath(); c.arc(p[0], p[1], 6 * S.u, 0, TAU); c.fill(); c.strokeStyle = '#f6eedb'; c.lineWidth = 2.5 * S.u; c.stroke(); }
    const ox = ST.dot && !d.pos ? 14 * S.u : 0, lines = txt.split('\n'), lx = p[0] + ox, ly = p[1] + rise - (ST.dot ? size * 0.05 : 0);
    const o = Object.assign({}, ST, { align: ST.dot ? 'left' : 'center', color: col(d.color, ST.color), rot: (d.rotate || 0) * D2R, italic: d.italic ?? ST.italic });
    const sh = ST.dot || d.sprite ? [0, 0] : labelShift(textBox(c, lines, lx, ly, size, o));   // a city's name stays by its dot
    textBlock(c, lines, lx + sh[0], ly + sh[1], size, o);
    c.restore(); }
}

// ---------------------------------------------------------------- icons (vector, drawn at origin, size s = icon height in px)
const FLAGS = { FR: ['v', '#2b4aa0', '#f4f1ea', '#d8343a'], IT: ['v', '#2f8f4e', '#f4f1ea', '#d8343a'], BE: ['v', '#222', '#f2c400', '#d8343a'], MX: ['v', '#2f7d4a', '#f4f1ea', '#c8303a'],
  IE: ['v', '#2f8f4e', '#f4f1ea', '#e8892f'], NL: ['h', '#c0313a', '#f4f1ea', '#2a4d8f'], RU: ['h', '#f4f1ea', '#2a52a8', '#d8343a'], DE: ['h', '#222', '#d8343a', '#f2c400'],
  AT: ['h', '#d8343a', '#f4f1ea', '#d8343a'], HT: ['h', '#203f9a', '#d0263a'], PL: ['h', '#f4f1ea', '#d8343a'], UA: ['h', '#2a62b8', '#f2cf2a'], ES: ['es'], GB: ['gb'], UK: ['gb'], US: ['us'], USA: ['us'], PT: ['pt'], JP: ['jp'], OT: ['ot'], TR: ['ot'] };
const FLAGC = {};
function flagCanvas(code, colors) {
  const key = code + (colors ? colors.join() : ''); if (FLAGC[key]) return FLAGC[key];
  const W = 150, H = 100, cv = document.createElement('canvas'); cv.width = W; cv.height = H; const g = cv.getContext('2d');
  const f = colors ? ['h', ...colors] : FLAGS[String(code).toUpperCase()] || ['h', '#888', '#ddd'];
  const k = f[0];
  if (k === 'v' || k === 'h') { const n = f.length - 1; for (let i = 0; i < n; i++) { g.fillStyle = f[i + 1]; if (k === 'v') g.fillRect(i * W / n, 0, W / n + 1, H); else g.fillRect(0, i * H / n, W, H / n + 1); } }
  else if (k === 'es') { g.fillStyle = '#c8202c'; g.fillRect(0, 0, W, H); g.fillStyle = '#f2c400'; g.fillRect(0, H / 4, W, H / 2); }
  else if (k === 'pt') { g.fillStyle = '#2f7d3a'; g.fillRect(0, 0, W * 0.4, H); g.fillStyle = '#d0262f'; g.fillRect(W * 0.4, 0, W * 0.6, H); g.fillStyle = '#f2c400'; g.beginPath(); g.arc(W * 0.4, H / 2, H * 0.2, 0, TAU); g.fill(); }
  else if (k === 'jp') { g.fillStyle = '#f4f1ea'; g.fillRect(0, 0, W, H); g.fillStyle = '#c8202c'; g.beginPath(); g.arc(W / 2, H / 2, H * 0.3, 0, TAU); g.fill(); }
  else if (k === 'ot') { g.fillStyle = '#c8202c'; g.fillRect(0, 0, W, H); g.fillStyle = '#f4f1ea'; g.beginPath(); g.arc(W * 0.42, H / 2, H * 0.27, 0, TAU); g.fill(); g.fillStyle = '#c8202c'; g.beginPath(); g.arc(W * 0.47, H / 2, H * 0.22, 0, TAU); g.fill(); star(g, W * 0.64, H / 2, H * 0.1, '#f4f1ea'); }
  else if (k === 'gb') { g.fillStyle = '#1f3a7a'; g.fillRect(0, 0, W, H); g.lineCap = 'butt';
    g.strokeStyle = '#f4f1ea'; g.lineWidth = 20; g.beginPath(); g.moveTo(0, 0); g.lineTo(W, H); g.moveTo(W, 0); g.lineTo(0, H); g.stroke();
    g.strokeStyle = '#cf2a35'; g.lineWidth = 7; g.stroke(); g.strokeStyle = '#f4f1ea'; g.lineWidth = 32; g.beginPath(); g.moveTo(W / 2, 0); g.lineTo(W / 2, H); g.moveTo(0, H / 2); g.lineTo(W, H / 2); g.stroke();
    g.strokeStyle = '#cf2a35'; g.lineWidth = 18; g.stroke(); }
  else if (k === 'us') { for (let i = 0; i < 13; i++) { g.fillStyle = i % 2 ? '#f4f1ea' : '#c8202c'; g.fillRect(0, i * H / 13, W, H / 13 + 1); }
    g.fillStyle = '#23356f'; g.fillRect(0, 0, W * 0.42, H * 7 / 13); for (let r = 0; r < 4; r++) for (let q = 0; q < 5; q++) star(g, 7 + q * 12.5 + (r % 2) * 6, 7 + r * 12.5, 3.2, '#f4f1ea'); }
  FLAGC[key] = cv; return cv;
}
function star(g, x, y, r, fill, n = 5, inner = 0.45) { g.beginPath(); for (let i = 0; i < n * 2; i++) { const a = -Math.PI / 2 + i * Math.PI / n, rr = i % 2 ? r * inner : r; g.lineTo(x + Math.cos(a) * rr, y + Math.sin(a) * rr); } g.closePath(); g.fillStyle = fill; g.fill(); }
function rr(c, x, y, w, h, r) { c.beginPath(); c.roundRect(x, y, w, h, r); }
const INK = '#2a2118';
function drawIcon(c, type, s, d, t, seed = 0) {
  const u = S.u, txt = d.text != null ? tr(d.text, S.lang) : null;
  c.lineJoin = 'round'; c.lineCap = 'round';
  const shadow = (blur = 0.12, oy = 0.05) => { c.shadowColor = 'rgba(0,0,0,0.35)'; c.shadowBlur = s * blur; c.shadowOffsetY = s * oy; };
  const noShadow = () => { c.shadowColor = 'transparent'; c.shadowBlur = 0; c.shadowOffsetY = 0; };
  switch (type) {
    case 'money': case 'cash': {
      const stack = (x, y, a) => { c.save(); c.translate(x, y); c.rotate(a); const w = s * 0.9, h = s * 0.4;
        for (let i = 4; i >= 0; i--) { c.fillStyle = i ? '#5d8a4a' : '#86b56c'; rr(c, -w / 2, -h / 2 - i * s * 0.045, w, h, s * 0.04); c.fill(); c.strokeStyle = '#2f4d26'; c.lineWidth = s * 0.018; c.stroke(); }
        c.fillStyle = '#f2ecd2'; c.fillRect(-w * 0.09, -h / 2 + 1, w * 0.18, h - 2); c.fillStyle = '#2f4d26'; c.font = fnt(h * 0.62); c.textAlign = 'center'; c.textBaseline = 'middle'; c.fillText('$', w * 0.3, 0); c.fillText('$', -w * 0.3, 0); c.restore(); };
      shadow(); stack(-s * 0.32, s * 0.14, -0.18); stack(s * 0.3, s * 0.18, 0.12); noShadow(); stack(0, -s * 0.12, -0.04); break; }
    case 'tag': case 'price': {
      c.save(); c.rotate(-0.22); const w = s * 1.25, h = s * 0.62; shadow();
      c.beginPath(); c.moveTo(-w / 2, 0); c.lineTo(-w / 2 + h * 0.42, -h / 2); c.lineTo(w / 2, -h / 2); c.lineTo(w / 2, h / 2); c.lineTo(-w / 2 + h * 0.42, h / 2); c.closePath();
      c.fillStyle = col(d.color, '#d63a40'); c.fill(); noShadow(); c.strokeStyle = 'rgba(80,10,10,0.6)'; c.lineWidth = s * 0.03; c.stroke();
      c.fillStyle = '#f6efe0'; c.beginPath(); c.arc(-w / 2 + h * 0.4, 0, h * 0.1, 0, TAU); c.fill();
      if (txt) { let fs = h * 0.6; c.font = fnt(fs); const mw = c.measureText(txt).width, lim = w * 0.66; if (mw > lim) fs *= lim / mw;
        textBlock(c, [txt], w * 0.1, h * 0.02, fs, { color: '#ffffff', bold: 0.06, spacing: 0 }); }
      c.restore(); break; }
    case 'flag': {
      const fc = flagCanvas(d.flag || d.country || 'FR', d.colors), fw = s * 0.85, fh = fw * 2 / 3, n = 24;
      c.fillStyle = '#5a4630'; c.fillRect(-s * 0.03, -s, s * 0.06, s); c.fillStyle = '#c9a14a'; c.beginPath(); c.arc(0, -s, s * 0.05, 0, TAU); c.fill();
      shadow(0.08, 0.03);
      for (let i = 0; i < n; i++) { const x = i / n, yo = Math.sin(x * 2.6 * Math.PI - t * 6 - seed) * s * 0.045 * x, sh = 1 - 0.12 * Math.cos(x * 2.6 * Math.PI - t * 6 - seed) * x;
        c.drawImage(fc, x * 150, 0, 150 / n + 0.6, 100, s * 0.03 + x * fw, -s * 0.97 + yo, fw / n + 0.8, fh); if (i === 0) noShadow();
        c.fillStyle = `rgba(0,0,0,${Math.max(0, 0.18 * (1 - sh) * 4)})`; c.fillRect(s * 0.03 + x * fw, -s * 0.97 + yo, fw / n + 0.8, fh); }
      break; }
    case 'briefcase': case 'case': {
      const w = s * 1.15, h = s * 0.78; shadow();
      const g = c.createLinearGradient(0, -h / 2, 0, h / 2); g.addColorStop(0, '#e3e6ea'); g.addColorStop(0.5, '#b9bec5'); g.addColorStop(1, '#8a9099');
      c.fillStyle = g; rr(c, -w / 2, -h / 2, w, h, s * 0.07); c.fill(); noShadow(); c.strokeStyle = '#5e646c'; c.lineWidth = s * 0.03; c.stroke();
      c.beginPath(); c.moveTo(-w / 2, -h * 0.12); c.lineTo(w / 2, -h * 0.12); c.stroke();
      c.lineWidth = s * 0.07; c.strokeStyle = '#7d838b'; c.beginPath(); c.roundRect(-w * 0.16, -h / 2 - s * 0.16, w * 0.32, s * 0.2, s * 0.06); c.stroke();
      c.fillStyle = '#d9b44a'; c.fillRect(-w * 0.3, -h * 0.2, w * 0.1, h * 0.16); c.fillRect(w * 0.2, -h * 0.2, w * 0.1, h * 0.16);
      for (let i = 0; i < 6; i++) { c.strokeStyle = 'rgba(255,255,255,0.35)'; c.lineWidth = s * 0.012; c.beginPath(); c.moveTo(-w / 2 + s * 0.06, -h * 0.05 + i * h * 0.09); c.lineTo(w / 2 - s * 0.06, -h * 0.05 + i * h * 0.09); c.stroke(); }
      break; }
    case 'sale': case 'forsale': case 'sign': {
      const w = s * 1.25, h = s * 0.7;
      c.fillStyle = '#6b4a2b'; c.strokeStyle = '#3b2716'; c.lineWidth = s * 0.02; shadow(0.08, 0.03);
      c.fillRect(-w * 0.55, -s * 1.55, s * 0.09, s * 1.55); c.fillRect(-w * 0.55, -s * 1.55, w * 1.15, s * 0.08); noShadow();
      c.strokeStyle = '#3b3027'; c.lineWidth = s * 0.02; c.beginPath(); c.moveTo(-w * 0.32, -s * 1.47); c.lineTo(-w * 0.32, -s * 1.32); c.moveTo(w * 0.42, -s * 1.47); c.lineTo(w * 0.42, -s * 1.32); c.stroke();
      c.save(); c.translate(w * 0.05, -s * 1.32 + h / 2); c.rotate(0.04 * Math.sin(t * 2.2 + seed));
      shadow(); c.fillStyle = '#ffffff'; rr(c, -w / 2, -h / 2, w, h, s * 0.04); c.fill(); noShadow();
      c.fillStyle = col(d.color, '#d2232a'); rr(c, -w / 2 + s * 0.05, -h / 2 + s * 0.05, w - s * 0.1, h - s * 0.1, s * 0.03); c.fill();
      const lines = (txt || (S.lang === 'ru' ? 'ПРОДАЁТСЯ' : 'FOR SALE')).split('\n'), sub = d.sub != null ? tr(d.sub, S.lang) : null;
      let fs = h * (sub ? 0.36 : 0.46) / Math.max(1, lines.length * 0.8); c.font = fnt(fs); const mw = Math.max(...lines.map(l => c.measureText(l).width)), lim = w * 0.84; if (mw > lim) fs *= lim / mw;
      textBlock(c, lines, 0, sub ? -h * 0.12 : 0, fs, { color: '#ffffff', bold: 0.07, spacing: 0.04, lh: 0.9 });
      if (sub) { c.fillStyle = '#ffffff'; c.fillRect(-w * 0.36, h * 0.12, w * 0.72, h * 0.22); let fs2 = h * 0.17; c.font = fnt(fs2, true); const m2 = c.measureText(sub).width; if (m2 > w * 0.66) fs2 *= w * 0.66 / m2;
        textBlock(c, [sub], 0, h * 0.23, fs2, { color: '#b01d24', italic: true, spacing: 0 }); }
      c.restore(); break; }
    case 'coin': {
      const sym = d.symbol || '¢';
      if (txt) { // "4¢/acre": digits gold, symbol becomes a coin, rest white — all on one baseline
        const fs = s * 0.9, parts = []; let buf = ''; for (const ch of txt) { if (ch === sym) { if (buf) parts.push(buf); parts.push(null); buf = ''; } else buf += ch; } if (buf) parts.push(buf);
        c.font = fnt(fs); const ws = parts.map(p => p == null ? fs * 0.82 : c.measureText(p).width); const tot = ws.reduce((a, b) => a + b, 0) + (parts.length - 1) * fs * 0.06; let x = -tot / 2;
        parts.forEach((p, i) => { if (p == null) { c.save(); c.translate(x + ws[i] / 2, 0); coin(c, fs * 0.78, sym); c.restore(); }
          else textBlock(c, [p], x + ws[i] / 2, fs * 0.02, fs, { color: /^\d/.test(p) ? '#ffd63a' : '#ffffff', outline: 'rgba(30,20,8,0.85)', outlineW: fs * 0.12, bold: 0.045, spacing: 0, shadow: 'rgba(0,0,0,0.4)' });
          x += ws[i] + fs * 0.06; });
      } else coin(c, s, sym);
      break; }
    case 'pin': {
      c.save(); shadow(0.1, 0.04); c.fillStyle = col(d.color, '#d2302f'); c.beginPath(); c.moveTo(0, 0); c.bezierCurveTo(-s * 0.1, -s * 0.3, -s * 0.38, -s * 0.48, -s * 0.38, -s * 0.72);
      c.arc(0, -s * 0.72, s * 0.38, Math.PI, 0); c.bezierCurveTo(s * 0.38, -s * 0.48, s * 0.1, -s * 0.3, 0, 0); c.fill(); noShadow(); c.strokeStyle = 'rgba(60,10,10,0.6)'; c.lineWidth = s * 0.03; c.stroke();
      c.fillStyle = '#fbf3e4'; c.beginPath(); c.arc(0, -s * 0.72, s * 0.15, 0, TAU); c.fill(); c.restore();
      if (txt) textBlock(c, txt.split('\n'), 0, -s * 1.32, s * 0.42, { color: '#fbf5e6', outline: 'rgba(25,18,10,0.85)', outlineW: s * 0.07, bold: 0.03 });
      break; }
    case 'year': case 'date': case 'title': {
      const lines = (txt || (S.year || '')).split('\n');
      textBlock(c, lines, 0, 0, s, { color: col(d.color, '#fbf7ee'), shadow: 'rgba(20,14,6,0.75)', shadowBlur: s * 0.16, shadowY: s * 0.04, spacing: 0.01 });
      textBlock(c, lines, 0, 0, s, { color: 'rgba(255,255,255,0.0)', outline: 'rgba(40,30,15,0.18)', outlineW: s * 0.02 });
      break; }
    case 'ship': case 'boat': {
      const w = s * 1.25; c.save(); shadow(0.08, 0.03);
      c.fillStyle = '#6a3f22'; c.beginPath(); c.moveTo(-w / 2, -s * 0.22); c.lineTo(w / 2, -s * 0.28); c.quadraticCurveTo(w * 0.42, s * 0.05, w * 0.28, s * 0.1); c.lineTo(-w * 0.34, s * 0.1); c.quadraticCurveTo(-w * 0.46, 0, -w / 2, -s * 0.22); c.fill(); noShadow();
      c.fillStyle = '#8b5530'; c.fillRect(-w * 0.42, -s * 0.2, w * 0.86, s * 0.07); c.fillStyle = '#c4a061'; for (let i = 0; i < 4; i++) c.fillRect(-w * 0.3 + i * w * 0.17, -s * 0.11, w * 0.06, s * 0.05);
      c.strokeStyle = '#3d2614'; c.lineWidth = s * 0.035; c.beginPath(); c.moveTo(-w * 0.18, -s * 0.22); c.lineTo(-w * 0.18, -s * 0.95); c.moveTo(w * 0.15, -s * 0.24); c.lineTo(w * 0.15, -s * 1.02); c.stroke();
      const sail = (x, y, ww, hh) => { c.fillStyle = '#f3ecdc'; c.beginPath(); c.moveTo(x - ww / 2, y); c.quadraticCurveTo(x, y + hh * 0.12, x + ww / 2, y); c.lineTo(x + ww / 2 + ww * 0.06, y + hh); c.quadraticCurveTo(x, y + hh * 1.18, x - ww / 2 - ww * 0.06, y + hh); c.closePath(); c.fill(); c.strokeStyle = 'rgba(90,70,40,0.5)'; c.lineWidth = s * 0.015; c.stroke(); };
      sail(-w * 0.18, -s * 0.9, w * 0.34, s * 0.28); sail(-w * 0.18, -s * 0.57, w * 0.4, s * 0.28); sail(w * 0.15, -s * 0.97, w * 0.36, s * 0.3); sail(w * 0.15, -s * 0.62, w * 0.42, s * 0.31);
      const fl = col(d.color, '#c8202c'); c.fillStyle = fl; c.beginPath(); c.moveTo(w * 0.15, -s * 1.02); c.lineTo(w * 0.15 + s * 0.2, -s * 0.98 + Math.sin(t * 7 + seed) * s * 0.02); c.lineTo(w * 0.15, -s * 0.92); c.fill();
      c.restore(); break; }
    case 'crate': case 'box': case 'cargo': {
      const w = s * 0.8; shadow(); c.fillStyle = '#9a6a3c'; c.fillRect(-w / 2, -w / 2, w, w); noShadow(); c.strokeStyle = '#5b3a1d'; c.lineWidth = s * 0.05; c.strokeRect(-w / 2, -w / 2, w, w);
      c.lineWidth = s * 0.035; c.beginPath(); c.moveTo(-w / 2, -w / 2); c.lineTo(w / 2, w / 2); c.stroke(); c.strokeStyle = 'rgba(60,35,15,0.4)'; c.lineWidth = s * 0.015; for (let i = 1; i < 4; i++) { c.beginPath(); c.moveTo(-w / 2, -w / 2 + i * w / 4); c.lineTo(w / 2, -w / 2 + i * w / 4); c.stroke(); }
      break; }
    case 'barrel': {
      const w = s * 0.62, h = s * 0.8; shadow(); c.fillStyle = '#8f5a2e'; c.beginPath(); c.ellipse(0, 0, w / 2, h / 2, 0, 0, TAU); c.fill(); c.fillRect(-w * 0.45, -h / 2, w * 0.9, h); noShadow();
      c.fillStyle = '#4a4440'; c.fillRect(-w * 0.47, -h * 0.32, w * 0.94, h * 0.07); c.fillRect(-w * 0.47, h * 0.25, w * 0.94, h * 0.07);
      c.strokeStyle = 'rgba(50,25,10,0.45)'; c.lineWidth = s * 0.015; for (let i = -1; i <= 1; i++) { c.beginPath(); c.moveTo(i * w * 0.2, -h / 2); c.lineTo(i * w * 0.2, h / 2); c.stroke(); }
      break; }
    case 'scroll': case 'treaty': {
      const w = s * 0.95, h = s * 0.75; shadow(); c.fillStyle = '#efe2c0'; c.fillRect(-w / 2, -h / 2, w, h); noShadow();
      c.fillStyle = '#d9c79b'; c.beginPath(); c.ellipse(-w / 2, 0, s * 0.07, h / 2 + s * 0.03, 0, 0, TAU); c.fill(); c.beginPath(); c.ellipse(w / 2, 0, s * 0.07, h / 2 + s * 0.03, 0, 0, TAU); c.fill();
      c.strokeStyle = 'rgba(60,45,25,0.6)'; c.lineWidth = s * 0.018; for (let i = 0; i < 5; i++) { c.beginPath(); c.moveTo(-w * 0.34, -h * 0.3 + i * h * 0.13); c.lineTo(w * (i === 4 ? 0.05 : 0.34), -h * 0.3 + i * h * 0.13); c.stroke(); }
      c.fillStyle = '#b2232a'; c.beginPath(); c.arc(w * 0.24, h * 0.26, s * 0.1, 0, TAU); c.fill();
      if (txt) textBlock(c, [txt], 0, -h * 0.72, s * 0.32, { color: '#fbf5e6', outline: 'rgba(25,18,10,0.85)', outlineW: s * 0.05 });
      break; }
    case 'arrow': {
      c.save(); c.rotate((d.angle || 0) * D2R); const w = s * 1.3, h = s * 0.32; shadow(0.1, 0.04);
      c.beginPath(); c.moveTo(-w / 2, -h / 2); c.lineTo(w * 0.15, -h / 2); c.lineTo(w * 0.15, -h); c.lineTo(w / 2, 0); c.lineTo(w * 0.15, h); c.lineTo(w * 0.15, h / 2); c.lineTo(-w / 2, h / 2); c.closePath();
      c.fillStyle = col(d.color, '#fbfbf7'); c.fill(); noShadow(); c.strokeStyle = 'rgba(40,30,20,0.35)'; c.lineWidth = s * 0.02; c.stroke(); c.restore(); break; }
    case 'swords': case 'battle': {
      const blade = a => { c.save(); c.rotate(a); c.fillStyle = '#d8dde3'; c.beginPath(); c.moveTo(-s * 0.035, s * 0.25); c.lineTo(-s * 0.035, -s * 0.5); c.lineTo(0, -s * 0.6); c.lineTo(s * 0.035, -s * 0.5); c.lineTo(s * 0.035, s * 0.25); c.fill(); c.strokeStyle = '#6b737c'; c.lineWidth = s * 0.015; c.stroke();
        c.fillStyle = '#c99a2e'; c.fillRect(-s * 0.14, s * 0.24, s * 0.28, s * 0.05); c.fillStyle = '#5a3a1e'; c.fillRect(-s * 0.03, s * 0.29, s * 0.06, s * 0.17); c.restore(); };
      shadow(0.08, 0.03); blade(-0.7); blade(0.7); noShadow(); break; }
    case 'crown': {
      const w = s * 0.95, h = s * 0.62; shadow(); c.fillStyle = '#e1b12e'; c.beginPath(); c.moveTo(-w / 2, h / 2); c.lineTo(-w / 2, -h * 0.35); c.lineTo(-w * 0.25, 0); c.lineTo(0, -h / 2); c.lineTo(w * 0.25, 0); c.lineTo(w / 2, -h * 0.35); c.lineTo(w / 2, h / 2); c.closePath(); c.fill(); noShadow();
      c.strokeStyle = '#8a6512'; c.lineWidth = s * 0.03; c.stroke(); ['#c8202c', '#2b4aa0', '#2f8f4e'].forEach((cc, i) => { c.fillStyle = cc; c.beginPath(); c.arc((i - 1) * w * 0.28, h * 0.22, s * 0.06, 0, TAU); c.fill(); }); break; }
    case 'burst': case 'boom': {
      shadow(); c.beginPath(); for (let i = 0; i < 24; i++) { const a = i / 24 * TAU, r = (i % 2 ? 0.32 : 0.55) * s * (1 + 0.12 * Math.sin(i * 3.7)); c.lineTo(Math.cos(a) * r, Math.sin(a) * r); } c.closePath();
      c.fillStyle = '#ffcf3a'; c.fill(); noShadow(); c.strokeStyle = '#d4371f'; c.lineWidth = s * 0.05; c.stroke(); if (txt) textBlock(c, [txt], 0, 0, s * 0.3, { color: '#c22e1a', bold: 0.06 }); break; }
    case 'star': { shadow(); star(c, 0, 0, s * 0.5, col(d.color, '#f2c230')); noShadow(); c.strokeStyle = '#9b7414'; c.lineWidth = s * 0.03; c.stroke(); break; }
    case 'image': {
      const im = S.IMG[d.image]; if (im) { const h = s, w = h * im.width / im.height; shadow(0.08, 0.03); c.drawImage(im, -w / 2, -h / 2, w, h); noShadow(); }
      break; }
    default: { // text badge
      if (txt) { const ST = STYLE[d.style] || STYLE.note; textBlock(c, txt.split('\n'), 0, 0, s * 0.8, Object.assign({}, ST, { color: col(d.color, ST.color) })); }
    }
  }
}
function coin(c, s, sym) {
  const r = s / 2; c.save(); c.shadowColor = 'rgba(0,0,0,0.35)'; c.shadowBlur = s * 0.1; c.shadowOffsetY = s * 0.04;
  const g = c.createRadialGradient(-r * 0.3, -r * 0.3, r * 0.1, 0, 0, r); g.addColorStop(0, '#fff1a8'); g.addColorStop(0.55, '#e9b938'); g.addColorStop(1, '#b07b14');
  c.fillStyle = g; c.beginPath(); c.arc(0, 0, r, 0, TAU); c.fill(); c.shadowColor = 'transparent';
  c.strokeStyle = '#8c5f0e'; c.lineWidth = s * 0.06; c.stroke(); c.strokeStyle = 'rgba(255,240,180,0.7)'; c.lineWidth = s * 0.03; c.beginPath(); c.arc(0, 0, r * 0.78, 0, TAU); c.stroke();
  textBlock(c, [sym], 0, s * 0.02, s * 0.62, { color: '#8a5c0c', bold: 0.05, spacing: 0 }); c.restore();
}
const ANCHOR_BOTTOM = new Set(['pin', 'flag', 'sale', 'forsale', 'sign', 'ship', 'boat']);
const DEF_SIZE = { money: 140, cash: 140, tag: 130, price: 130, flag: 130, briefcase: 110, case: 110, sale: 170, forsale: 170, sign: 170, coin: 110, pin: 80, year: 190, date: 190, title: 120, ship: 84, boat: 84,
  crate: 56, box: 56, cargo: 56, barrel: 56, scroll: 110, treaty: 110, arrow: 110, swords: 120, battle: 120, crown: 100, burst: 140, boom: 140, star: 90, image: 160, text: 70 };
function drawIcons(c, V, t) {
  for (const I of S.icons) {
    const v = vis(I, t, true); if (v.a <= 0.001) continue;
    const d = I.def, s = (d.size || DEF_SIZE[I.type] || 100) * S.u * (d.pos ? 1 : Math.pow(zoomFactor(V, d.zoomRef), 0.5));
    const p = screenPos(d, V), seed = strHash(I.id) % 97;
    const popIn = v.lt < 1e8 ? clamp(v.lt / 0.42) : 1;
    const sc = (popIn < 1 ? MO.backOut(popIn, 3.2) : 1) * (v.a < 1 && popIn >= 1 ? MO.cubicIn(v.a) : 1);
    const rot = (d.rotate || 0) * D2R + (v.lt < 1e8 ? MO.settle(v.lt, 0.16, 2.6, 5) : 0);
    const bob = Math.sin(t * 2.1 + seed) * 3 * S.u;
    if (sc <= 0.001) continue;
    const G = glowAmt(I, t);
    c.save(); c.translate(p[0], p[1] + bob); c.rotate(rot); c.scale(sc, sc);
    if (!ANCHOR_BOTTOM.has(I.type) && d.anchor === 'bottom') c.translate(0, -s / 2);
    if (G) { c.save(); c.globalAlpha = G.a * 0.8; const g = c.createRadialGradient(0, ANCHOR_BOTTOM.has(I.type) ? -s / 2 : 0, 0, 0, ANCHOR_BOTTOM.has(I.type) ? -s / 2 : 0, s); g.addColorStop(0, rgba(G.g.color, 0.8)); g.addColorStop(1, rgba(G.g.color, 0)); c.fillStyle = g; c.fillRect(-s, -s * 1.6, s * 2, s * 2.6); c.restore(); }
    c.globalAlpha = Math.min(1, v.a * 1.5);
    drawIcon(c, I.type, s, d, t, seed);
    c.restore();
  }
}
function drawMovers(c, V, t) {
  for (const m of S.movers) {
    const l0 = t - m.at + 1 / S.fps; if (l0 <= 0) continue;
    const ch = m.L.chains[0]; if (!ch || ch.len <= 0) continue;
    for (let i = 0; i < m.count; i++) {
      const l = l0 - i * m.gap * m.dur; if (l <= 0) continue;
      let f = m.ease(clamp(l / m.dur)); if (m.reverse) f = 1 - f;
      const done = l >= m.dur;
      let a = clamp(l / 0.25) * lineProgress(m.L, t).alpha; if (done && !m.stay) a *= 1 - clamp((l - m.dur) / 0.45); if (a <= 0.001) continue;
      const pt = (ff) => { const tg = ch.len * clamp(ff); let j = 1; while (j < ch.length - 1 && ch.cum[j] < tg) j++; const A = ch[j - 1], B = ch[j], q = (tg - ch.cum[j - 1]) / Math.max(1e-9, ch.cum[j] - ch.cum[j - 1]); return [lerp(A[0], B[0], q), lerp(A[1], B[1], q)]; };
      const w = pt(f), w2 = pt(f + (m.reverse ? -0.01 : 0.01)), s0 = toS(V, w), s1 = toS(V, w2), dir = s1[0] - s0[0] >= 0 ? 1 : -1;
      const type = m.image ? 'image' : String(m.object).toLowerCase(), size = (m.size || DEF_SIZE[type] || 70) * S.u;
      const bob = Math.sin(t * 3.2 + i * 1.7) * size * 0.04, rock = Math.sin(t * 2.4 + i) * 0.06;
      c.save(); c.globalAlpha = a; c.translate(s0[0], s0[1] + bob); c.rotate(type === 'ship' || type === 'boat' ? rock : 0); c.scale(dir * (type === 'arrow' ? 1 : 1), 1);
      if (type === 'ship' || type === 'boat') { c.fillStyle = 'rgba(240,250,250,0.35)'; c.beginPath(); c.ellipse(-size * 0.2, size * 0.08, size * 0.7, size * 0.07, 0, 0, TAU); c.fill(); }
      drawIcon(c, type, size, { image: m.image, text: m.text, color: m.color, flag: m.flag, angle: 0 }, t, i * 13);
      c.restore();
    }
  }
}

// ---------------------------------------------------------------- sprites
function spriteState(sp, t) {
  const d = sp.def, fps = S.fps;
  let shown = d.show != null ? !!d.show : !sp.revealed;
  const st = { ll: sp.ll.slice(), face: d.flip ? -1 : 1, img: d.image, alpha: shown ? 1 : 0, dx: 0, dy: 0, rot: 0, sx: 1, sy: 1, glow: null, visible: shown, room: shown ? 1 : 0 };
  for (const a of sp.acts) {
    const l = t - a.at + 1 / fps; if (l <= 0) break;
    const Q = a.Q, act = a.act, dur = +(Q.dur ?? ({ enter: 0.55, in: 0.55, show: 0.55, exit: 0.4, out: 0.4, hide: 0.4, bounce: 0.7, jump: 0.7, wobble: 0.9, shake: 0.6, flip: 0.32, turn: 0.32, move: 1.2, walk: 1.2, talk: 1.5, swap: 0 })[act] ?? 0.6);
    const p = clamp(l / Math.max(1e-3, dur)), W = S.W, u = S.u;
    if (act === 'enter' || act === 'in' || act === 'show') {
      if (a.image) st.img = a.image; if (Q.lon != null || Q.place) st.ll = llOf(Q) || st.ll; st.visible = true; st.alpha = 1; st.room = MO.cubicInOut(clamp(l / 0.5));
      const from = Q.from || 'pop';
      if (p < 1) {
        if (from === 'left' || from === 'right') { const e = MO.backOut(p, 1.4); st.dx += (from === 'left' ? -1 : 1) * (1 - e) * W * 0.75; st.rot += (from === 'left' ? -1 : 1) * Math.sin(p * Math.PI * 3) * 0.12 * (1 - p); }
        else if (from === 'top') { const e = p < 0.7 ? MO.cubicIn(p / 0.7) : 1; st.dy -= (1 - e) * S.H * 0.7; if (p >= 0.7) { const q = (p - 0.7) / 0.3; st.sy *= 1 - 0.18 * Math.sin(q * Math.PI); st.sx *= 1 + 0.12 * Math.sin(q * Math.PI); } }
        else if (from === 'bottom') { const e = MO.backOut(p, 1.6); st.dy += (1 - e) * S.H * 0.5; }
        else if (from === 'fade') st.alpha = p;
        else { const e = MO.backOut(p, 2.6); st.sx *= e; st.sy *= e; }
      }
      // landing squash right after any entrance
      const q = l - dur; if (q > 0 && q < 0.35) { const k = Math.sin(q / 0.35 * Math.PI) * 0.07; st.sy *= 1 - k; st.sx *= 1 + k; }
    } else if (act === 'exit' || act === 'out' || act === 'hide') {
      const to = Q.to || 'pop';
      if (to === 'left' || to === 'right') st.dx += (to === 'left' ? -1 : 1) * MO.cubicIn(p) * W * 0.8;
      else if (to === 'fade') st.alpha *= 1 - p;
      else { const e = 1 - MO.cubicIn(p); st.sx *= e; st.sy *= e; }
      st.room = Math.min(st.room, 1 - MO.cubicInOut(p));
      if (p >= 1) { st.visible = false; st.alpha = 0; }
    } else if (act === 'bounce' || act === 'jump') {
      if (p < 1) { const n = Math.max(1, Q.count | 0 || 1), ph = (p * n) % 1, h = (Q.height ?? 0.16) * S.minSide * (1 - p * 0.35);
        st.dy -= Math.sin(ph * Math.PI) * h; const sq = ph < 0.12 ? (0.12 - ph) / 0.12 : ph > 0.88 ? (ph - 0.88) / 0.12 : 0; st.sy *= 1 - 0.14 * sq + 0.06 * Math.sin(ph * Math.PI); st.sx *= 1 + 0.1 * sq - 0.04 * Math.sin(ph * Math.PI); }
    } else if (act === 'wobble') { if (p < 1) st.rot += Math.sin(p * Math.PI * 5) * (Q.angle ?? 9) * D2R * (1 - p); }
    else if (act === 'shake') { if (p < 1) st.dx += Math.sin(l * 70) * 9 * u * (1 - p); }
    else if (act === 'flip' || act === 'turn') { if (p < 1) st.sx *= Math.max(0.04, Math.abs(Math.cos(p * Math.PI))); if (p >= 0.5) st.face = -st.face;
      if (p < 1) st.dy -= Math.sin(p * Math.PI) * 0.04 * S.minSide; }
    else if (act === 'swap' || act === 'frame') {
      const img = a.image || (Q.frame === 'main' ? d.image : d.alt) || d.alt;
      const back = Q.dur != null && l > +Q.dur;
      if (!back) st.img = img;
      const q = back ? l - +Q.dur : l; if (q < 0.3) { const k = MO.settle(q, 0.14, 3, 7); st.sx *= 1 + k; st.sy *= 1 - k; }
    }
    else if (act === 'move' || act === 'walk') {
      const to = llOf(Q.to && typeof Q.to === 'object' && !Array.isArray(Q.to) ? Q.to : { lonlat: Q.to }) || llOf(Q) || st.ll;
      const e = MO.cubicInOut(p); const from = st.ll.slice(); st.ll = [lerp(from[0], to[0], e), lerp(from[1], to[1], e)];
      if (p < 1 && Q.hop !== false) { const hops = Math.max(1, Math.round(dur * 3)); st.dy -= Math.abs(Math.sin(p * hops * Math.PI)) * 0.03 * S.minSide; }
      if (p >= 1) st.ll = to;
    }
    else if (act === 'talk') { if (p < 1) { const k = 0.035 * Math.abs(Math.sin(l * 9)); st.sy *= 1 + k; st.sx *= 1 - k * 0.5; } }
  }
  return st;
}
function pegDoll(c, h, seed) {   // fallback when an image is missing: a tiny peg doll
  const cols = ['#2b3d6e', '#7a2b2b', '#2f5d43', '#5a4a7a'], body = cols[seed % cols.length];
  c.fillStyle = 'rgba(0,0,0,0.0)';
  c.fillStyle = body; rr(c, -h * 0.2, -h * 0.62, h * 0.4, h * 0.62, h * 0.18); c.fill();
  c.fillStyle = '#f4dcc6'; c.beginPath(); c.arc(0, -h * 0.72, h * 0.2, 0, TAU); c.fill();
  c.fillStyle = '#222'; c.beginPath(); c.arc(-h * 0.07, -h * 0.72, h * 0.022, 0, TAU); c.arc(h * 0.07, -h * 0.72, h * 0.022, 0, TAU); c.fill();
}
// the opaque part of a sprite picture (alpha bbox, found once per image on a ≤256 px copy): the figure's height is the
// sprite's scale and its feet stand on the point, whatever transparent margins the picture has
function trimOf(im) {
  if (!im || !im.width || !im.height) return null; if (im.__trim !== undefined) return im.__trim;
  let r = { sx: 0, sy: 0, sw: im.width, sh: im.height };
  try {
    const k = Math.min(1, 256 / Math.max(im.width, im.height)), w = Math.max(1, Math.round(im.width * k)), h = Math.max(1, Math.round(im.height * k));
    const cv = document.createElement('canvas'); cv.width = w; cv.height = h; const g = cv.getContext('2d', { willReadFrequently: true }); g.drawImage(im, 0, 0, w, h);
    const px = g.getImageData(0, 0, w, h).data; let x0 = w, y0 = h, x1 = -1, y1 = -1;
    for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) if (px[(y * w + x) * 4 + 3] > 12) { if (x < x0) x0 = x; if (x > x1) x1 = x; if (y < y0) y0 = y; if (y > y1) y1 = y; }
    if (x1 >= x0 && y1 >= y0) { const sx = Math.max(0, (x0 - 0.5) / k), sy = Math.max(0, (y0 - 0.5) / k);
      r = { sx, sy, sw: Math.min(im.width, (x1 + 1.5) / k) - sx, sh: Math.min(im.height, (y1 + 1.5) / k) - sy }; }
  } catch (e) { /* a tainted canvas: draw the whole picture */ }
  im.__trim = r; return r;
}
function spriteH(sp, V) { const d = sp.def; return (d.scale ?? d.size ?? S.spriteScale) * S.minSide * (d.mapScale ? Math.pow(zoomFactor(V), d.mapScale) : 1); }
function spriteW(st, h) { const im = st.img ? S.IMG[st.img] : null, tr = trimOf(im); return tr ? h * tr.sw / tr.sh : h * 0.45; }
// where the sprites stand this frame (their resting box, without bounce/shake), for the labels to step aside
function spriteBoxes(V, t) {
  const out = [];
  for (const sp of S.sprites) { const st = spriteState(sp, t); if (st.room <= 0.01 || !(st.visible || st.alpha > 0)) continue;
    const p = llS(V, st.ll), h = spriteH(sp, V), w = spriteW(st, h) * 0.9;
    out.push({ box: [p[0] - w / 2, p[1] - h * 0.97, p[0] + w / 2, p[1] + h * 0.04], k: st.room }); }
  return out;
}
const overlap = (a, b) => a[0] < b[2] && b[0] < a[2] && a[1] < b[3] && b[1] < a[3];
// a label's shift away from the sprites that would cover it (smallest move that clears them and stays in the frame);
// eased by the sprites' entrance so labels glide aside instead of jumping
function labelShift(box) {
  if (!S.spriteBox || !S.spriteBox.length) return [0, 0];
  const hits = S.spriteBox.filter(s => overlap(box, s.box)); if (!hits.length) return [0, 0];
  const pad = 8 * S.u, w = box[2] - box[0], h = box[3] - box[1], cands = [];
  for (const s of hits) { const b = s.box;
    cands.push([0, b[3] + pad - box[1]], [0, b[1] - pad - box[3]], [b[0] - pad - box[2], 0], [b[2] + pad - box[0], 0]); }
  const inFrame = d => box[0] + d[0] >= S.box.x && box[2] + d[0] <= S.box.x + S.box.w && box[1] + d[1] >= S.box.y && box[3] + d[1] <= S.box.y + S.box.h;
  const clear = d => { const m = [box[0] + d[0], box[1] + d[1], box[2] + d[0], box[3] + d[1]]; return !S.spriteBox.some(s => overlap(m, s.box)); };
  const cost = d => Math.hypot(d[0] * 0.8, d[1]);
  cands.sort((a, b) => cost(a) - cost(b));
  const best = cands.find(d => inFrame(d) && clear(d)) || cands.find(inFrame); if (!best || cost(best) > Math.max(w, h) * 3 + 240 * S.u) return [0, 0];
  const k = Math.max(...hits.map(s => s.k));
  return [best[0] * k, best[1] * k];
}
function drawSprites(c, V, t) {
  const list = S.sprites.map(sp => ({ sp, st: spriteState(sp, t) })).filter(o => o.st.visible || o.st.alpha > 0);
  list.forEach(o => { o.scr = llS(V, o.st.ll); o.h = spriteH(o.sp, V); }); list.sort((a, b) => a.scr[1] - b.scr[1]);
  for (const { sp, st, scr } of list) {
    if (st.alpha <= 0.001) continue;
    const d = sp.def, h = spriteH(sp, V);
    const idle = d.anim === 'none' ? 0 : 1, br = idle * 0.012 * Math.sin(t * 2.6 + sp.seed), sw = idle * 0.018 * Math.sin(t * 1.3 + sp.seed * 0.7);
    const x = scr[0] + st.dx, y = scr[1] + st.dy;
    c.save(); c.globalAlpha = st.alpha;
    // contact shadow (stays on the ground)
    c.fillStyle = 'rgba(25,18,8,0.28)'; c.beginPath(); c.ellipse(scr[0] + st.dx, scr[1], h * 0.2 * Math.abs(st.sx), h * 0.045, 0, 0, TAU); c.fill();
    c.translate(x, y); c.rotate(st.rot + sw); c.scale(st.face * st.sx * (1 - br * 0.5), st.sy * (1 + br));
    const G = glowAmt(sp, t);
    const im = st.img ? S.IMG[st.img] : null;
    if (G) { c.shadowColor = rgba(G.g.color, G.a); c.shadowBlur = 30 * S.u; }
    if (im) { const r = trimOf(im), w = h * r.sw / r.sh; c.drawImage(im, r.sx, r.sy, r.sw, r.sh, -w / 2, -h, w, h); if (G) c.drawImage(im, r.sx, r.sy, r.sw, r.sh, -w / 2, -h, w, h); }
    else { if (st.img && !S.pending) warn(`map: sprite "${sp.id}" image not loaded: ${st.img}`); pegDoll(c, h, sp.seed); }
    c.restore();
  }
  return list;
}

// ---------------------------------------------------------------- bubbles, rewind, overlays
function cloudPath(c, cx, cy, R, seed) {
  c.beginPath(); const n = 11;
  for (let i = 0; i < n; i++) { const a0 = i / n * TAU, a1 = (i + 1) / n * TAU, am = (a0 + a1) / 2, rb = R * (0.2 + 0.05 * hsh(i, seed));
    const p0 = [cx + Math.cos(a0) * R, cy + Math.sin(a0) * R * 0.92], p1 = [cx + Math.cos(a1) * R, cy + Math.sin(a1) * R * 0.92];
    if (!i) c.moveTo(p0[0], p0[1]); c.quadraticCurveTo(cx + Math.cos(am) * (R + rb * 2.1), cy + Math.sin(am) * (R + rb * 2.1) * 0.92, p1[0], p1[1]); }
  c.closePath();
}
function drawBubbles(c, V, t, sprites) {
  for (const b of S.bubbles) {
    const l = t - b.at + 1 / S.fps; if (l <= 0 || l > b.dur + 0.3) continue;
    const Q = b.Q, u = S.u;
    const pin = Math.min(1, l / 0.45), pout = clamp((l - b.dur) / 0.3), sc = MO.backOut(pin, 2.2) * (1 - MO.cubicIn(pout));
    if (sc <= 0.001) continue;
    // anchor: sprite head, a place, or a screen position
    let ax, ay; const so = Q.sprite && sprites.find(o => o.sp.id === Q.sprite);
    if (so) { const h = so.h; ax = so.scr[0] + so.st.dx; ay = so.scr[1] + so.st.dy - h * 0.95; }
    else { const p = screenPos(Q, V); ax = p[0]; ay = p[1]; }
    const R = (Q.size ?? 0.3) * S.minSide, side = Q.side === 'left' ? -1 : Q.side === 'right' ? 1 : (ax > S.W / 2 ? -1 : 1);
    let cx = ax + side * R * 1.05, cy = ay - R * 0.95;
    cx = clamp(cx, S.box.x + R * 1.12, S.box.x + S.box.w - R * 1.12); cy = clamp(cy, S.box.y + R * 1.1, S.box.y + S.box.h - R * 1.1);
    c.save(); c.translate(ax, ay); c.scale(sc, sc); c.translate(-ax, -ay);
    // trailing puffs
    for (let i = 0; i < 3; i++) { const f = 0.25 + i * 0.22, x = lerp(ax, cx, f), y = lerp(ay, cy + R * 0.7, f), r = (8 + i * 6) * u;
      c.fillStyle = '#fbf8f0'; c.strokeStyle = '#2a2622'; c.lineWidth = 3 * u; c.beginPath(); c.arc(x, y, r, 0, TAU); c.fill(); c.stroke(); }
    c.save(); c.shadowColor = 'rgba(255,255,255,0.9)'; c.shadowBlur = 24 * u; cloudPath(c, cx, cy, R, 3); c.fillStyle = '#fbf8f0'; c.fill(); c.restore();
    c.save(); cloudPath(c, cx, cy, R * 0.93, 3); c.clip();
    const v = viewToCam(Q.view || { lon: Q.lon, lat: Q.lat, zoom: Q.zoom ?? 2 });   // bubble zoom: the bubble's diameter spans 360/zoom degrees
    const MV = { x: v.x + Math.sin(t * 0.4) * 0.3, y: v.y, k: 2 * R * v.z / 360, ax: cx, ay: cy, z: v.z };
    const rect = [cx - R * 1.4, cy - R * 1.4, R * 2.8, R * 2.8];
    drawBase(c, MV, rect); drawRegions(c, MV, t, rect); drawLakes(c, MV, rect); drawLines(c, MV, t, true);
    if (Q.icon) { const ic = typeof Q.icon === 'string' ? { type: Q.icon } : Q.icon, ty = String(ic.type || 'text').toLowerCase(); const s = (ic.size || DEF_SIZE[ty] || 100) * u * 0.8;
      c.save(); c.translate(cx, cy + (ANCHOR_BOTTOM.has(ty) ? s / 2 : 0)); const pp = clamp((l - 0.3) / 0.4); c.scale(MO.backOut(pp, 2.4), MO.backOut(pp, 2.4)); drawIcon(c, ty, s, ic, t, 5); c.restore(); }
    if (Q.text) textBlock(c, tr(Q.text, S.lang).split('\n'), cx, cy + R * 0.55, R * 0.16, STYLE.note);
    c.restore();
    c.lineWidth = 4.5 * u; c.strokeStyle = '#2a2622'; cloudPath(c, cx, cy, R, 3); c.stroke();
    c.restore();
  }
}
function drawRewind(c, t) {
  for (const r of S.rewinds) {
    const l = t - r.at + 1 / S.fps; if (l <= 0 || l > r.dur) continue;
    const env = clamp(l / 0.18) * clamp((r.dur - l) / 0.22), u = S.u, W = S.W, H = S.H;
    c.save();
    c.globalAlpha = 0.75 * env; c.globalCompositeOperation = 'saturation'; c.fillStyle = '#808080'; c.fillRect(0, 0, W, H);
    c.globalCompositeOperation = 'color'; c.globalAlpha = 0.28 * env; c.fillStyle = '#7a5a34'; c.fillRect(0, 0, W, H);
    c.globalCompositeOperation = 'source-over';
    const g = c.createRadialGradient(W / 2, H * 0.42, Math.min(W, H) * 0.2, W / 2, H * 0.42, Math.hypot(W, H) * 0.62); g.addColorStop(0, 'rgba(0,0,0,0)'); g.addColorStop(1, `rgba(8,5,2,${0.85 * env})`);
    c.globalAlpha = 1; c.fillStyle = g; c.fillRect(0, 0, W, H);
    // tracking lines
    const fr = Math.floor(t * 24); c.fillStyle = `rgba(255,255,255,${0.07 * env})`;
    for (let i = 0; i < 6; i++) { const y = ((hsh(i, fr) * H + l * 900 * u) % H); c.fillRect(0, y, W, (2 + hsh(i + 9, fr) * 5) * u); }
    const by = (H * 0.33 + l * 1300 * u) % H; c.fillStyle = `rgba(255,255,255,${0.08 * env})`; c.fillRect(0, by, W, 26 * u);
    // ◀◀◀
    const cx = S.anchor[0], cy = S.box.y + S.box.h * 0.2, s = 64 * u, blink = 0.55 + 0.45 * (Math.floor(l * 6) % 2);
    c.globalAlpha = env * blink; c.shadowColor = 'rgba(0,0,0,0.6)'; c.shadowBlur = 16 * u; c.fillStyle = '#ffffff';
    for (let i = 0; i < 3; i++) { const x = cx + (1 - i) * s * 0.9 - s * 0.1; c.beginPath(); c.moveTo(x - s * 0.5, cy); c.lineTo(x + s * 0.4, cy - s * 0.52); c.lineTo(x + s * 0.4, cy + s * 0.52); c.closePath(); c.fill(); }
    if (r.text) { c.globalAlpha = env; textBlock(c, tr(r.text, S.lang).split('\n'), cx, cy + s * 1.45, 74 * u, { color: '#ffffff', shadow: 'rgba(0,0,0,0.7)', shadowBlur: 18 * u }); }
    c.restore();
  }
}
function drawYear(c) {
  if (!S.year) return; const u = S.u, x = S.box.x + 40 * u, y = S.box.y + (S.H > S.W ? 150 : 60) * u, s = 58 * u;
  c.save(); c.font = fnt(s); const w = c.measureText(S.year).width + 56 * u, h = s * 1.25;
  c.shadowColor = 'rgba(0,0,0,0.35)'; c.shadowBlur = 12 * u; c.fillStyle = '#efe3c4'; rr(c, x, y, w, h, 8 * u); c.fill(); c.shadowColor = 'transparent';
  c.strokeStyle = '#5a4428'; c.lineWidth = 3 * u; c.stroke(); c.lineWidth = 1.2 * u; rr(c, x + 6 * u, y + 6 * u, w - 12 * u, h - 12 * u, 5 * u); c.stroke();
  textBlock(c, [S.year], x + w / 2, y + h / 2 + s * 0.04, s, { color: '#3a2a16', spacing: 0.04 }); c.restore();
}
function vignette(c) {
  const W = S.W, H = S.H, a = S.pal.vignette; if (!a) return;
  const g = c.createRadialGradient(W / 2, H * 0.45, Math.min(W, H) * 0.35, W / 2, H * 0.45, Math.hypot(W, H) * 0.6);
  g.addColorStop(0, 'rgba(20,14,6,0)'); g.addColorStop(1, `rgba(20,14,6,${a})`); c.fillStyle = g; c.fillRect(0, 0, W, H);
}

// ======================================================================= draw
function draw(c, t, ctx) {
  const V = camera(t), rect = [0, 0, S.W, S.H];
  if (!ctx.alpha) drawBase(c, V, rect);
  drawRegions(c, V, t, rect);
  drawLakes(c, V, rect);
  drawRegionGlows(c, V, t);
  drawLines(c, V, t, false);
  S.spriteBox = spriteBoxes(V, t);
  drawRegionLabels(c, V, t);
  drawLabels(c, V, t);
  drawMovers(c, V, t);
  drawIcons(c, V, t);
  const sprites = drawSprites(c, V, t);
  drawBubbles(c, V, t, sprites);
  if (!ctx.alpha) vignette(c);
  drawYear(c);
  drawRewind(c, t);
}

return { fonts: [FONT, FONTI], safe: true, init, draw };
})();
