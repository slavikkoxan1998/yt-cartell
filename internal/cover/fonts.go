package cover

// Embedded display faces (SIL Open Font License 1.1, licences next to the
// files in fonts/). Sources: github.com/google/fonts (ofl/…). The variable
// fonts were pinned to one weight with fontTools' instancer and every face
// except Russo One was subset to Latin (Basic, Latin-1, Extended-A, Ș/Ț),
// Cyrillic, punctuation, №, ₴, € and arrows; GSUB was dropped (no shaping)
// and the kern lookups were made reachable from the "latn" script, which is
// the one golang.org/x/image/font/sfnt reads. Russo One carries a Reserved
// Font Name, so it is embedded unmodified.
//
//	montserrat-black.ttf  Montserrat[wght] @900   OFL-Montserrat.txt
//	rubik-black.ttf       Rubik[wght] @900        OFL-Rubik.txt
//	oswald-bold.ttf       Oswald[wght] @700       OFL-Oswald.txt
//	russo-one.ttf         RussoOne-Regular        OFL-RussoOne.txt
//	unbounded-black.ttf   Unbounded[wght] @900    OFL-Unbounded.txt
//	alegreya-black.ttf    Alegreya[wght] @900     OFL-Alegreya.txt
//	pacifico.ttf          Pacifico-Regular        OFL-Pacifico.txt
//	dela-gothic.ttf       DelaGothicOne-Regular   OFL-DelaGothicOne.txt

import (
	"embed"
	"fmt"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

//go:embed fonts/*.ttf
var fontFS embed.FS

// FontDef is one face of the font catalog.
type FontDef struct {
	ID    string // stable id stored in projects (CoverText.Font)
	Label string // Ukrainian name for the UI
	Name  string // the typeface name, e.g. "Montserrat Black"
	Hint  string // Ukrainian: what the face suits
	// Script faces (handwriting) are never uppercased and get looser lines.
	Script bool

	file string
}

// Fonts is the catalog, in display order. Every face covers the Ukrainian
// alphabet (Ґґ Єє Іі Її) and the apostrophe.
var Fonts = []FontDef{
	{ID: "montserrat", Label: "Класичний жирний", Name: "Montserrat Black", Hint: "Універсальний, читається навіть у маленькій мініатюрі", file: "montserrat-black.ttf"},
	{ID: "rubik", Label: "М'який округлий", Name: "Rubik Black", Hint: "Дружній і щільний: історії, лайфстайл, діти", file: "rubik-black.ttf"},
	{ID: "oswald", Label: "Вузький", Name: "Oswald Bold", Hint: "Вміщує довгі заголовки великим кеглем", file: "oswald-bold.ttf"},
	{ID: "dela", Label: "Ультражирний", Name: "Dela Gothic One", Hint: "Найважчий: гучні заголовки з одного-двох слів", file: "dela-gothic.ttf"},
	{ID: "russo", Label: "Техно", Name: "Russo One", Hint: "Спорт, ігри, техніка, авто", file: "russo-one.ttf"},
	{ID: "unbounded", Label: "Широкий сучасний", Name: "Unbounded Black", Hint: "Фантастика, космос, технології", file: "unbounded-black.ttf"},
	{ID: "alegreya", Label: "Книжковий із засічками", Name: "Alegreya Black", Hint: "Історія, драма, містика, казки для дорослих", file: "alegreya-black.ttf"},
	{ID: "pacifico", Label: "Рукописний", Name: "Pacifico", Hint: "Казки, романтика, подорожі, їжа", Script: true, file: "pacifico.ttf"},
}

// fallbackFont draws characters the chosen face does not have.
const fallbackFont = "montserrat"

// FontByID finds a face of the catalog.
func FontByID(id string) (FontDef, bool) {
	for _, f := range Fonts {
		if f.ID == id {
			return f, true
		}
	}
	return FontDef{}, false
}

// ---- loaded faces --------------------------------------------------------------------

// pt is a point in em units (y grows downward, baseline at 0).
type pt struct{ X, Y float32 }

// pathOp is one outline command in em units.
type pathOp struct {
	op  sfnt.SegmentOp
	pts [3]pt
}

// glyph is a loaded outline with its metrics (all in em).
type glyph struct {
	face  *face
	index sfnt.GlyphIndex
	adv   float32
	path  []pathOp
	// ink bounds (em, y down); empty glyphs (space) have minX == maxX.
	minX, minY, maxX, maxY float32
}

func (g *glyph) empty() bool { return len(g.path) == 0 }

// face is a parsed font with a glyph cache. Safe for concurrent use.
type face struct {
	def  FontDef
	f    *sfnt.Font
	upem float32
	// capH / xH in em (from OS/2 or measured).
	capH, xH float32

	mu     sync.Mutex
	buf    sfnt.Buffer
	glyphs map[rune]*glyph // nil value = the face lacks the rune
	kerns  map[[2]sfnt.GlyphIndex]float32
}

var (
	facesMu sync.Mutex
	faces   = map[string]*face{}
)

// loadFace returns the parsed face of a catalog id (unknown → fallback).
func loadFace(id string) (*face, error) {
	def, ok := FontByID(id)
	if !ok {
		def, _ = FontByID(fallbackFont)
	}
	facesMu.Lock()
	defer facesMu.Unlock()
	if fc := faces[def.ID]; fc != nil {
		return fc, nil
	}
	data, err := fontFS.ReadFile("fonts/" + def.file)
	if err != nil {
		return nil, fmt.Errorf("cover: font %s: %w", def.ID, err)
	}
	f, err := sfnt.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("cover: font %s: %w", def.ID, err)
	}
	fc := &face{
		def:    def,
		f:      f,
		upem:   float32(f.UnitsPerEm()),
		glyphs: map[rune]*glyph{},
		kerns:  map[[2]sfnt.GlyphIndex]float32{},
	}
	ppem := fixed.I(int(f.UnitsPerEm()))
	if m, err := f.Metrics(&fc.buf, ppem, font.HintingNone); err == nil {
		fc.capH = float32(m.CapHeight) / 64 / fc.upem
		fc.xH = float32(m.XHeight) / 64 / fc.upem
	}
	faces[def.ID] = fc
	fc.mu.Lock()
	if g := fc.lookupLocked('H'); g != nil && !g.empty() && (fc.capH <= 0.2 || fc.capH > 1.2) {
		fc.capH = -g.minY
	}
	if g := fc.lookupLocked('x'); g != nil && !g.empty() && (fc.xH <= 0.1 || fc.xH > 1) {
		fc.xH = -g.minY
	}
	if fc.capH <= 0.2 || fc.capH > 1.2 {
		fc.capH = 0.7
	}
	fc.mu.Unlock()
	return fc, nil
}

// lookup returns the glyph of r, nil when the face does not have it.
func (fc *face) lookup(r rune) *glyph {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.lookupLocked(r)
}

func (fc *face) lookupLocked(r rune) *glyph {
	if g, ok := fc.glyphs[r]; ok {
		return g
	}
	g := fc.loadLocked(r)
	fc.glyphs[r] = g
	return g
}

func (fc *face) loadLocked(r rune) *glyph {
	idx, err := fc.f.GlyphIndex(&fc.buf, r)
	if err != nil || idx == 0 {
		return nil
	}
	ppem := fixed.I(int(fc.upem))
	adv, err := fc.f.GlyphAdvance(&fc.buf, idx, ppem, font.HintingNone)
	if err != nil {
		return nil
	}
	s := 1 / (64 * fc.upem) // 26.6 font units → em
	g := &glyph{face: fc, index: idx, adv: float32(adv) * s}
	segs, err := fc.f.LoadGlyph(&fc.buf, idx, ppem, nil)
	if err != nil {
		return nil
	}
	first := true
	for _, sg := range segs {
		op := pathOp{op: sg.Op}
		n := 1
		switch sg.Op {
		case sfnt.SegmentOpQuadTo:
			n = 2
		case sfnt.SegmentOpCubeTo:
			n = 3
		}
		for i := 0; i < n; i++ {
			p := pt{float32(sg.Args[i].X) * s, float32(sg.Args[i].Y) * s}
			op.pts[i] = p
			if first {
				g.minX, g.maxX, g.minY, g.maxY = p.X, p.X, p.Y, p.Y
				first = false
			} else {
				g.minX, g.maxX = min(g.minX, p.X), max(g.maxX, p.X)
				g.minY, g.maxY = min(g.minY, p.Y), max(g.maxY, p.Y)
			}
		}
		g.path = append(g.path, op)
	}
	return g
}

// kern is the kerning between two glyphs of this face (em).
func (fc *face) kern(a, b *glyph) float32 {
	if a == nil || b == nil || a.face != fc || b.face != fc {
		return 0
	}
	key := [2]sfnt.GlyphIndex{a.index, b.index}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if k, ok := fc.kerns[key]; ok {
		return k
	}
	var k float32
	if v, err := fc.f.Kern(&fc.buf, a.index, b.index, fixed.I(int(fc.upem)), font.HintingNone); err == nil {
		k = float32(v) / 64 / fc.upem
	}
	fc.kerns[key] = k
	return k
}

// substitutes are stand-ins for characters a face may lack.
var substitutes = map[rune][]rune{
	'\u02bc': {'\u2019', '\''}, // modifier letter apostrophe
	'\u2019': {'\u02bc', '\''}, // right single quote
	'\u2018': {'\''},           // left single quote
	'`':      {'\''},
	'\u00ab': {'"'}, // guillemets
	'\u00bb': {'"'},
	'\u201c': {'"'}, // curly double quotes
	'\u201d': {'"'},
	'\u201e': {'"'},
	'\u2014': {'\u2013', '-'}, // em dash
	'\u2013': {'-'},           // en dash
	'\u2212': {'\u2013', '-'}, // minus
	'\u2011': {'-'},           // non-breaking hyphen
	'\u00a0': {' '},           // no-break space
	'\u202f': {' '},           // narrow no-break space
	'\u2009': {' '},           // thin space
}

// glyphFor resolves r in fc, then its substitutes, then the fallback face.
// It returns nil when nothing can draw it; "…" may resolve to three dots.
func glyphFor(fc *face, r rune) []*glyph {
	if g := fc.lookup(r); g != nil {
		return []*glyph{g}
	}
	for _, s := range substitutes[r] {
		if g := fc.lookup(s); g != nil {
			return []*glyph{g}
		}
	}
	if r == '…' {
		if d := fc.lookup('.'); d != nil {
			return []*glyph{d, d, d}
		}
	}
	if fc.def.ID != fallbackFont {
		if fb, err := loadFace(fallbackFont); err == nil {
			return glyphFor(fb, r)
		}
	}
	return nil
}

// Missing lists the characters of text that no embedded face can draw (they
// are left out of the cover), e.g. emoji or scripts other than Latin and
// Cyrillic. fontID picks the primary face ("" = default).
func Missing(text, fontID string) []rune {
	fc, err := loadFace(fontID)
	if err != nil {
		return nil
	}
	var out []rune
	seen := map[rune]bool{}
	for _, r := range text {
		if r == '\n' || r == '\r' || r == '\t' || r == '*' || seen[r] {
			continue
		}
		seen[r] = true
		if glyphFor(fc, r) == nil {
			out = append(out, r)
		}
	}
	return out
}
