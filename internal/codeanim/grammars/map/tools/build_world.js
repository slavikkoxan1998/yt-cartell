// Build the compact TopoJSON used by the "map" grammar from Natural Earth (public domain) 1:50m GeoJSON.
// usage (in a scratch dir): npm i topojson-server topojson-simplify topojson-client
//   download from github.com/nvkelso/natural-earth-vector/tree/master/geojson into <ne_dir>:
//   ne_50m_admin_0_countries, ne_50m_admin_1_states_provinces_lakes, ne_50m_lakes, ne_50m_rivers_lake_centerlines (.geojson)
//   MINW=0.001 Q=1e5 node build_world.js <ne_dir> ../world.js      (MINW = Visvalingam area threshold in deg², Q = quantization)
const fs = require('fs'), path = require('path');
const { topology } = require('topojson-server');
const { presimplify, simplify } = require('topojson-simplify');
const [, , NE, OUT] = process.argv;
const rd = f => JSON.parse(fs.readFileSync(path.join(NE, f + '.geojson'), 'utf8'));

const r5 = v => Math.round(v * 1e3) / 1e3;
// countries
const C = rd('ne_50m_admin_0_countries');
const countries = { type: 'FeatureCollection', features: C.features.map(f => {
  const p = f.properties;
  let a3 = p.ADM0_A3; if (a3 === 'SDS') a3 = 'SSD'; if (a3 === 'KOS') a3 = 'XKX';
  const nm = {}; for (const L of ['ru', 'uk', 'de', 'fr', 'es', 'pl', 'it', 'pt', 'tr']) { const v = p['NAME_' + L.toUpperCase()]; if (v && v !== p.NAME) nm[L] = v; }
  return { type: 'Feature', id: a3, properties: { n: p.NAME, nm, iso: p.ISO_A3 !== '-99' ? p.ISO_A3 : undefined, lx: r5(p.LABEL_X), ly: r5(p.LABEL_Y) }, geometry: f.geometry };
}) };
// US + Canada states/provinces
const S = rd('ne_50m_admin_1_states_provinces_lakes');
const states = { type: 'FeatureCollection', features: S.features.filter(f => ['USA', 'CAN'].includes(f.properties.adm0_a3)).map(f => ({
  type: 'Feature', id: f.properties.iso_3166_2, properties: { n: f.properties.name }, geometry: f.geometry })) };
// lakes (big ones only)
const L = rd('ne_50m_lakes');
const lakes = { type: 'FeatureCollection', features: L.features.filter(f => f.properties.scalerank <= 3).map(f => ({ type: 'Feature', properties: { n: f.properties.name_en || f.properties.name }, geometry: f.geometry })) };
// rivers, grouped by normalized id
const ALIAS = { rhein: 'rhine', rhin: 'rhine', nederrijn: 'rhine', waal: 'rhine', lek: 'rhine', donau: 'danube', borcea: 'danube', 'bratul chillia': 'danube', 'bratul sulina': 'danube', 'bratul sfintu gheorghe': 'danube',
  amazonas: 'amazon', 'chang jiang': 'yangtze', 'jinsha': 'yangtze', 'tongtian': 'yangtze', 'tuotuo': 'yangtze', 'el bahr el abyad': 'nile', 'bahr el jebel': 'nile', 'rosetta branch': 'nile', 'damietta branch': 'nile', 'albert nile': 'nile', 'victoria nile': 'nile',
  firat: 'euphrates', 'al furat': 'euphrates', dicle: 'tigris', dnepre: 'dnieper', dnipro: 'dnieper', 'heilong jiang': 'amur', huang: 'yellow', 'st. lawrence': 'st_lawrence', 'rio grande': 'rio_grande', 'grande': 'rio_grande', 'ertis': 'irtysh', 'ertix': 'irtysh', 'lualaba': 'congo', 'ayeyarwady': 'irrawaddy', 'lancang': 'mekong', 'sénégal': 'senegal', 'bénoué': 'benue', 'paraná': 'parana' };
const R = rd('ne_50m_rivers_lake_centerlines');
const rivers = { type: 'FeatureCollection', features: [] };
const byId = {};
for (const f of R.features) {
  const p = f.properties; if (!f.geometry || p.scalerank > 6) continue;
  const nm = (p.name_en || p.name || '').trim(); if (!nm) continue;
  let id = nm.toLowerCase(); id = ALIAS[id] || id.replace(/[^a-z0-9]+/g, '_');
  const lines = f.geometry.type === 'LineString' ? [f.geometry.coordinates] : f.geometry.coordinates;
  (byId[id] = byId[id] || { n: nm, sr: p.scalerank, lines: [] }).lines.push(...lines);
  byId[id].sr = Math.min(byId[id].sr, p.scalerank);
}
for (const [id, v] of Object.entries(byId)) rivers.features.push({ type: 'Feature', id, properties: { n: v.n, sr: v.sr }, geometry: { type: 'MultiLineString', coordinates: v.lines } });

let topo = topology({ countries, states, lakes, rivers }, 1e6);
topo = presimplify(topo);
const minW = +(process.env.MINW || 0.0006);
topo = simplify(topo, minW);
// re-quantize by hand: topology() already delta-encoded; easiest is to rebuild with quantization through topojson-client
const { quantize } = require('topojson-client');
// topology(...,1e6) is quantized already; quantize again to coarser grid
topo = quantize(topo, +(process.env.Q||1e5));
// strip empty arcs left by simplify
const js = JSON.stringify(topo);
fs.writeFileSync(OUT, '/* Natural Earth 1:50m (public domain, naturalearthdata.com) — countries, US/CA admin-1, lakes, rivers. Simplified TopoJSON for the "map" grammar. Built by tools/build_world.js */\nwindow.MAPGRAM_WORLD=' + js + ';\n');
console.log('wrote', OUT, (js.length / 1024).toFixed(0) + ' KB', 'arcs', topo.arcs.length, 'rivers', rivers.features.length);
