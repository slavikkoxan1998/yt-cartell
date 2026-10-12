# Vendored animation engine

The files in this folder come from **huashu-art-motion** by alchaincyf (花叔 · 花生),
https://github.com/alchaincyf/huashu-art-motion, commit `f178bd7754a71d6d399473af1501634548efa6cb`
(2026-10-08), folder `scripts/engine/`. The code is MIT-licensed — see `LICENSE` (the upstream
licence, unchanged). The fonts in `lib/fonts/` are NOT under MIT: each keeps its own licence, all
SIL Open Font License 1.1 — see `lib/fonts/LICENSES.md` and `lib/fonts/OFL.txt`.

Included: `clip.html`, `clip.js` (the parametric clip runtime), `clips/*.js` (the 8 grammars),
`lib/*.js`, `engine.js`, `transitions.js`, and the font files the clip grammars use.

Changes made for YT Cartell Studio:

- `lib/fonts.js`: `FONT_FACES` lists only the shipped font files (the art-history film scenes and
  their fonts are not vendored); `lib/fonts/index.json` dropped.
- `lib/fonts.js`: after the upstream faces, Cyrillic overlay faces (same family names,
  `unicode-range` limited, flagged `overlay: true`) from `lib/fonts/cyr/` — Fira Sans, Montserrat
  Black, Caveat (OFL; see `lib/fonts/LICENSES.md`). Without them Ukrainian text fell back to the
  system serif.
- `lib/util.js`: `U.loadCmaps` skips the overlay faces (they are not families of their own).
- `lib/typo.js`: `TY.wrap` keeps Latin/Cyrillic/Greek words whole (upstream matched ASCII words
  only, so Cyrillic broke mid-word) and does not start a line with `: ; » …`.
- Not included: `render.py` (replaced by the Go renderer in `internal/codeanim`), `index.html`,
  `eras*.js`, `scenes/`, `demos/`, `reference_films/`, `compare.py`, the remaining fonts.
- The upstream example specs live in `../testdata/examples/` (tests only, not embedded).
