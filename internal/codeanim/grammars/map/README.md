# Grammar `map` — antique parchment map for history shorts

A clip grammar for the huashu-art-motion engine (clip contract: `references/09-视频动画语法.md` «片段契约»):
`CLIPS.map = { fonts, safe: true, init(ctx), draw(c, t, ctx) }`, driven by `window.renderFrame(t)`, a pure function of `t`
(no clock, seeded textures — the same `t` always gives the same pixels).

The look of a witty history Short: beige-tan parchment land with paper grain and mottling, muted teal ocean,
thin dark coastlines, EB Garamond atlas labels; regions as flat fills (France royal blue, Britain red, USA teal,
Spain orange) that spread / recolour with an ink wipe; glow outlines; a wavy glowing river that draws on; dashed
routes with arrows; ships and crates travelling along routes; icons that pop with a bounce; PNG character sprites
standing at geographic points (slide in, bounce, wobble, flip, shake, swap to a shocked frame); thought bubbles with
a live mini-map; a ◀◀◀ rewind beat. The camera flies between geographic views and keeps drifting; the bottom
~30 % (portrait) stays calm for subtitles.

## Files

| file | what |
|---|---|
| `map.js` | the grammar (`CLIPS.map`) |
| `world.js` | `window.MAPGRAM_WORLD`: Natural Earth 1:50m TopoJSON — 242 countries (ISO-A3 ids, names in en/ru/uk/de/fr/es/pl/it/pt/tr), US + Canada admin-1 (ISO 3166-2 ids), big lakes, 312 rivers. ~1.1 MB |
| `presets.js` | `window.MAPGRAM_PRESETS`: historical regions, the hand-drawn Mississippi (shared by river and borders), gazetteer, palette |
| `meta.json` | Ukrainian metadata (`codeanim.Grammar`: fields, cue kinds, minimal example) |
| `examples/*.json` | `louisiana_purchase` (1080×1920, ru), `napoleon_europe` (1920×1080, en), `triangular_trade` (1080×1920, en); placeholder sprites in `examples/sprites/` |
| `tools/build_world.js` | rebuilds `world.js` from Natural Earth GeoJSON |
| `../../grammars_map.go` | `init()` → `RegisterGrammar("map", world.js + presets.js + map.js, meta)` |

The clip file the browser loads as `clips/map.js` is the concatenation **world.js + presets.js + map.js** (that order).
For the upstream engine: `cat world.js presets.js map.js > scripts/engine/clips/map.js`.

Engine libs used: `U` (util), `MO` (easings), `CAM.zlerp`. Fonts: `EBGaramond-400`, `EBGaramond-400i` (OFL 1.1, Latin + Cyrillic,
already in the engine's `lib/fonts`). Italic text with Cyrillic in it (water and river labels, `italic: true`) is drawn
**oblique** — the upright face slanted — not in the true italic: Cyrillic italic letterforms (п т и д → n m u g shapes)
make «Міссісіпі» read as «Miccicini» at map sizes. Latin italic keeps the true italic face.

## Spec

```json
{ "grammar": "map", "duration": 16, "fps": 30, "width": 1080, "height": 1920,
  "safe": { "bottom": 560 },            // optional; otherwise data.safeBottom
  "data": { ... }, "cues": [ ... ] }
```

Coordinates are always `[lon, lat]` (or `"lon"`/`"lat"` keys). Anywhere a point is accepted you may also write a place
name from the gazetteer (`"paris"`, `"new_orleans"`, `"moscow"`, `"liverpool"`, `"elmina"`, `"kingston"`, `"cadiz"`,
`"borodino"`, … see `presets.js`), your own `data.places`, a region id (its label point) or a sprite id.

### `data`

| key | type | meaning |
|---|---|---|
| `view` | object | initial camera: `{lon, lat, zoom}` — **zoom: the short side of the frame spans 360/zoom degrees of longitude** (Europe ≈ 9–13, a country ≈ 12–20, North America ≈ 5–7, the Atlantic ≈ 2.5–3.5); or `{bbox:[w,s,e,n], pad?}` (fit into the safe box); or `{place}` |
| `language` | string | label language: `en` (default) `uk` `ru` `de` `fr` `es` `pl` `it` `pt`. Country names come from Natural Earth, preset labels from `presets.js`; your own text is used verbatim |
| `projection` | string | `mercator` (default, like the reference) · `equirect` · `robinson` |
| `regions` | array | flat fills, drawn in this order (later on top) — see below |
| `rivers` | array | glowing lines — see below |
| `routes` | array | dashed arrows — see below |
| `icons` | array | icons placed on the map/screen (`show:false` + a `pop` cue to animate) |
| `sprites` | array | characters `{id, lon,lat|place, scale (height of the figure itself — transparent margins of the PNG are trimmed — as a fraction of the short side; default .37 portrait ≈ 20 % of the frame height, .32 landscape), flip, anim:"idle"|"none", image?, alt?}` |
| `labels` | array | `{id, text, lon,lat|place|pos, style, size, color, rotate, italic, show}` |
| `places` | object | `{"name": [lon, lat]}` |
| `year` | string | small cartouche with the year in the top-left for the whole clip |
| `safeBottom` | number | fraction of the height kept calm for subtitles (default 0.30 portrait / 0.16 landscape). `spec.safe` wins when given. The map still fills the whole frame; the camera centres views in the safe box and screen-space items (`pos`, bubbles, rewind) stay inside it |
| `drift` | number | strength of the always-on camera drift + slow push-in after each move (0 = off, default 1) |
| `borders` | bool | faint modern country borders on the base map (default off — the reference has none) |
| `style` | object | palette overrides `{land, ocean, coast, outline, label, river, route, glow, vignette}` |
| `colors` | object | extra named colours |

**Element visibility rule:** every element in `data` is visible from t = 0 unless a reveal cue targets it (`fill`,
`drawRoute`, `pop`, `sprite enter`, `label`, `show`) — then it appears at that cue. `show: true|false` overrides.

#### Regions
`{ id, preset | country | countries[] | states[] | polygon | polygons, color, label, labelAt, labelSize, labelColor, bbox, opacity, outline, show }`

* `country`/`countries`: ISO-A3 / Natural Earth ADM0_A3 (`FRA`, `GBR`, `USA`, `HTI`…) or English name. `bbox:[w,s,e,n]` keeps only
  the parts of the country whose centre is inside (e.g. France without Guiana).
* `states`: ISO 3166-2 for the US and Canada (`US-LA`, `CA-QC`).
* `polygon`: `[[lon,lat], …]` (or `polygons: [[…], […]]`). **Polygons are clipped to land**, so sea-side edges can be loose;
  coastlines come from the real data. Mix freely with countries (union).
* `preset`: `louisiana_1803`, `usa_1803`, `british_north_america_1803`, `new_spain_1803`, `spanish_florida_1803`,
  `thirteen_colonies`, `france_metropolitan`, `french_empire_1812`, `uk_1801` (GB + Ireland), `spain_europe`, `portugal_europe`,
  `dutch_republic`, `saint_domingue`, `haiti`, `hispaniola`, `russian_empire_1812`, `ottoman_empire_1800`. The 1803 North America
  presets share border lines (incl. the Mississippi), so neighbours meet without gaps. Approximations for storytelling.
* `color`: hex or a name: `blue`/`france`/`royal` `#3352a0`, `red`/`britain` `#c22e24`, `teal`/`usa` `#31878f`,
  `orange`/`spain` `#c37224`, `green`/`haiti`, `purple`, `brown`, `olive`, `yellow`/`gold`, `gray`, `pink`, `cyan`, `navy`, `lightblue`, `darkred`, `black`, `white`.
* `label`: string (`"\n"` for two lines), `{en:…, ru:…}` or `false`. Default: the preset's / country's name in `language`.
  Size auto-fits the region on screen (22–70 px at 1080). A label fades out when a later region spreads over its point.

#### Rivers
`{ id, river | line, color, width, style: "glow"|"electric"|"solid", wave, label, labelAt, labelSize, start, reverse, bbox, show }`
— `river`: `"mississippi"` (hand preset, matches the 1803 borders) or a Natural Earth id: `missouri`, `ohio`, `arkansas`,
`rhine`, `danube`, `seine`, `volga`, `dnieper`, `nile`, `niger`, `congo`, `amazon`, `st_lawrence`, `rio_grande`, `yangtze`, `ganges`, `indus`, …
(segments are chained; the longest chain is used — `chains: n` for more). `start:[lon,lat]` orients the draw-on direction.

#### Routes
`{ id, from, to, via:[…], points:[…], bend (0.18; negative bends the other way), curve:false, color, width, style: "dashed"|"solid"|"glow", dash:[16,11], arrow, label, show }`
Two points → a quadratic arc; more → a Catmull-Rom curve. Dashes march slowly so the route is never static.

### Cues

Common: `at` (seconds; the element is first visible on the frame of `at`), `kind`, `dur`. Fields may also be put in `cue.data`.
Targets accept ids from `data` **or inline definitions** (e.g. `"region": {"preset": "haiti"}`).

| kind (aliases) | fields | effect |
|---|---|---|
| `camera` (`view`, `cam`) | `view` `{lon,lat,zoom}`/`{bbox}`/`{place}`/`{zoomBy}` (relative to the current zoom, e.g. `1.1` = a gentle push-in on the spot), `dur` 1.8, `ease` `inOut`/`smooth`/`sine`/`out`/`linear`, `arc` | eased flight; zoom is log-interpolated; long flights pull out mid-way (`arc:false` to disable) |
| `fill` (`spread`) | `region`, `color`, `from` (point → ink circle; `west`/`east`/`north`/`south` → wipe), `mode:"wipe"`, `dur` 1.1 | region appears, spreading from the point |
| `recolor` | `region`, `color`, `from`, `dur` 0.9 | new colour spreads over the old one |
| `glow` (`highlight`) | `target`/`region`/`route`/`river`/`sprite`/`icon`, `color`, `dur` (default: to the end), `pulse` | glowing **outer** edge of the region silhouette (inner borders never glow) / brighter line / halo around sprite or icon |
| `drawRoute` (`route`, `river`, `draw`) | `route`/`river`, `dur` 1.6, `ease` | the line draws on (river: wavy glowing line with a spark at the head; route: dashes + arrowhead) |
| `moveAlong` (`move`, `travel`) | `route`, `object` (`ship` `crate` `barrel` `flag` `coin` `money` …, or `image`), `count`, `gap` (fraction of dur), `dur` 3, `size`, `color`, `flag`, `stay`, `reverse`, `ease` | convoy travels along the route (ships face the travel direction and rock); fades out on arrival unless `stay` |
| `pop` (`icon`, `year`, `badge`) | `icon` (type, data id or object), `text`, `sub`, `place`/`lon,lat`/`pos:[fx,fy]`, `size`, `color`, `flag`, `symbol`, `rotate`, `dur` (auto-hide), `id` | icon pops with a back-out bounce + settle wobble, then floats gently |
| `sprite` (`character`) | `sprite`, `action`: `enter` (`from`: `pop` `left` `right` `top` `bottom` `fade`), `exit` (`to`), `bounce` (`count`, `height`), `wobble` (`angle`), `shake`, `flip`, `swap` (`image` or `frame:"alt"`; `dur` = revert after), `move` (`to`, `hop`), `talk`; `image`, `dur` | character animation; feet stay on the geographic point and move with the map |
| `bubble` (`think`) | `sprite` (or `place`/`pos`), `view` `{lon,lat,zoom}` (zoom relative to the bubble diameter), `icon`, `text`, `size` 0.3, `side`, `dur` 2.6 | cloud thought bubble with a live mini-map (current fills, rivers, routes) |
| `rewind` | `text`, `dur` 1.4 | desaturate + sepia, heavy vignette, VHS tracking lines, horizontal jitter, blinking ◀◀◀ and text; pair with a `camera` cue |
| `label` (`text`) | `label` (id or object) or `text` + position + `style`, `dur` | label fades/rises in |
| `hide` / `show` | `target` or `targets: []`, `dur` 0.4 | any element (region, line, icon, label, sprite; hiding a route also hides its movers) |

Icon types: `money`, `tag` (price tag, `text` e.g. `$10M`), `flag` (`flag`: FR GB US ES NL RU DE IT PT BE MX IE AT HT PL UA JP OT/TR or `colors`),
`briefcase`, `sale` (FOR SALE sign: `text`, `sub`), `coin` (alone, or `text` like `4¢/acre` — the `symbol` (default `¢`) becomes a coin,
digits gold), `pin`, `year` (big serif number), `ship`, `crate`, `barrel`, `scroll`, `arrow` (`angle`), `swords`, `crown`, `burst` (`text`),
`star`, `text` (outlined note), `image` (cue `image`). Label styles: `country`, `land`, `water` (italic, spaced), `river` (yellow italic),
`city` (dot + label), `title`, `note`.

### Images (sprites, image icons)

YT Cartell's Go renderer serves every image field of the spec: `cues[].image`, `cues[].data.image`, `data.image`,
`data.sprites[].image` / `alt`, `data.icons[].image`, inline icon / sprite objects in cues and bubble images — paths relative to
the assets folder (`Options.AssetsDir`); a missing file fails the render with the exact path and field. (Upstream `render.py`
only serves `cues[].image` and `data.image`; there put sprite frames on the cues.) The grammar loads the images it holds itself
and keeps `window.__ready` false / `window.prepare()` pending until they arrive; the Go renderer awaits `prepare()` and `__ready`
before frame 0. A failed load is a `console.error` (the render fails). A sprite without a loaded image draws a small placeholder doll.

Sprites are drawn at constant screen size (`scale` × short side = the height of the opaque figure, feet at the point — the
picture's transparent margins are trimmed), sorted by screen y, with a contact shadow, gentle idle breathing/sway (`anim:"none"`
to stop). Labels a standing sprite would cover glide aside (the smallest move that clears it; city names stay by their dot);
the sprite never leaves its geographic point. Recommended art: transparent PNG, character standing.

## Rendering

* Portrait and landscape: everything is scaled by `u = min(W,H)/1080`; vector all the way down (Path2D in world units under a camera
  transform), crisp at 1080×1920 and 1920×1080, any fps.
* Textures: four seeded tileable noise tiles (mottle, relief, fibre grain, sea streaks), applied world-anchored with two crossfaded
  octaves so they never swim or blur when zooming.
* Typical cost in headless Edge: 0.3–0.7 s per 1080×1920 frame (glows and bubbles are the expensive parts).

## Data sources and licences

* Geography: Natural Earth 1:50m cultural/physical vectors — **public domain** (naturalearthdata.com), simplified with
  topojson-simplify (Visvalingam, 0.001 deg²) and quantized 1e5 by `tools/build_world.js`.
* Historical regions, the Mississippi line and the gazetteer in `presets.js`: hand-made approximations (MIT like the code).
* Font: EB Garamond — SIL OFL 1.1 (ships with the engine).
* Placeholder sprites in `examples/sprites/` were drawn procedurally for tests (no AI); replace with real art.

## Testing

```sh
# upstream engine (Python): cat world.js presets.js map.js > <engine>/clips/map.js
uv run --with playwright python <engine>/render.py --spec examples/louisiana_purchase.json --stills 1,4,11 --out stills/
# YT Cartell: codeanim.Still / codeanim.Render with Options{AssetsDir: "<dir of the spec>"}
```
