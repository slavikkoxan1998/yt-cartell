package cover

// Text layout in em units (independent of the final size): tokens → glued
// units → shaped glyph runs → balanced lines. Sizes are applied later by the
// renderer, which searches for the largest size that fits.

import (
	"math"
	"strings"
	"unicode"
)

// token is one word of the input with per-rune highlight flags.
type token struct {
	runes []rune
	hl    []bool
	brk   bool // a forced line break follows
}

// tokenize splits text into words. "*…*" toggles the highlight (an unpaired
// last "*" is kept as a character); "\n" forces a line break.
func tokenize(text string) []token {
	stars := strings.Count(text, "*")
	literalFrom := -1
	if stars%2 == 1 {
		literalFrom = strings.LastIndex(text, "*")
	}
	var out []token
	var cur token
	flush := func(brk bool) {
		if len(cur.runes) > 0 {
			out = append(out, cur)
			cur = token{}
		}
		if brk && len(out) > 0 {
			out[len(out)-1].brk = true
		}
	}
	hl := false
	for i, r := range text {
		switch {
		case r == '*' && i != literalFrom:
			hl = !hl
		case r == '\n':
			flush(true)
		case unicode.IsSpace(r) && r != 0xa0 && r != 0x202f: // no-break spaces keep words together
			flush(false)
		default:
			cur.runes = append(cur.runes, r)
			cur.hl = append(cur.hl, hl)
		}
	}
	flush(false)
	return out
}

// glueWords are short words that should not end a line (Ukrainian
// typography: prepositions and conjunctions stay with the next word).
var glueWords = map[string]bool{
	"для": true, "про": true, "без": true, "під": true, "над": true, "між": true, "при": true,
	"від": true, "біля": true, "через": true, "the": true, "что": true, "как": true,
}

func isShortWord(t token) bool {
	letters := 0
	for _, r := range t.runes {
		if !unicode.IsLetter(r) && r != '\'' && r != 'ʼ' && r != '’' {
			return false
		}
		letters++
	}
	if letters == 0 {
		return false
	}
	return letters <= 2 || glueWords[strings.ToLower(string(t.runes))]
}

func isDash(t token) bool {
	for _, r := range t.runes {
		if r != '—' && r != '–' && r != '-' {
			return false
		}
	}
	return len(t.runes) > 0
}

func isNumber(t token) bool {
	if len(t.runes) == 0 || len(t.runes) > 5 {
		return false
	}
	for _, r := range t.runes {
		if !unicode.IsDigit(r) && r != '№' {
			return false
		}
	}
	return true
}

// glue groups tokens into unbreakable units: a short word or a number goes
// with the next word, a dash with the previous one.
func glue(tokens []token) [][]token {
	var units [][]token
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if isDash(t) && len(units) > 0 && !units[len(units)-1][len(units[len(units)-1])-1].brk {
			last := len(units) - 1
			units[last] = append(units[last], t)
			continue
		}
		u := []token{t}
		for (isShortWord(u[len(u)-1]) || isNumber(u[len(u)-1])) && !u[len(u)-1].brk && i+1 < len(tokens) {
			i++
			u = append(u, tokens[i])
		}
		units = append(units, u)
	}
	return units
}

// span is a placed glyph of a unit (em, pen x relative to the unit start).
type span struct {
	g  *glyph
	x  float32
	hl bool
}

// unit is an unbreakable run (em).
type unit struct {
	spans    []span
	adv      float32 // pen advance, without trailing tracking
	inkL     float32 // ink left relative to the unit start
	inkR     float32 // ink right relative to the unit start
	top, bot float32 // ink above / below the baseline (≥ 0)
	brk      bool
	hasInk   bool
}

// shaper turns runes into glyph runs of one face.
type shaper struct {
	fc    *face
	track float32
	space float32
	upper bool
}

func newShaper(fc *face, track float32, upper bool) *shaper {
	sp := float32(0.26)
	if g := fc.lookup(' '); g != nil && g.adv > 0 {
		sp = g.adv
	}
	return &shaper{fc: fc, track: track, space: sp, upper: upper}
}

// joiner is the pen distance added between two units on one line.
func (sh *shaper) joiner() float32 { return sh.space + 2*sh.track }

// shapeUnit shapes glued tokens (joined by spaces) into one unit.
func (sh *shaper) shapeUnit(toks []token) unit {
	var u unit
	var prev *glyph
	x := float32(0)
	first := true
	for ti, t := range toks {
		if ti > 0 {
			x += sh.joiner()
			prev = nil
		}
		runes := t.runes
		if sh.upper {
			runes = []rune(strings.ToUpper(string(runes)))
			if len(runes) != len(t.hl) { // case mapping changed the length: keep flags aligned by best effort
				hl := make([]bool, len(runes))
				for i := range hl {
					hl[i] = t.hl[min(i, len(t.hl)-1)]
				}
				t.hl = hl
			}
		}
		for ri, r := range runes {
			for _, g := range glyphFor(sh.fc, r) {
				if prev != nil {
					x += sh.track
					if prev.face == g.face {
						x += g.face.kern(prev, g)
					}
				}
				u.spans = append(u.spans, span{g: g, x: x, hl: t.hl[ri]})
				if !g.empty() {
					l, rr := x+g.minX, x+g.maxX
					if first {
						u.inkL, u.inkR = l, rr
						first = false
					} else {
						u.inkL, u.inkR = min(u.inkL, l), max(u.inkR, rr)
					}
					u.top = max(u.top, -g.minY)
					u.bot = max(u.bot, g.maxY)
					u.hasInk = true
				}
				x += g.adv
				prev = g
			}
		}
	}
	u.adv = x
	if !u.hasInk {
		u.inkL, u.inkR = 0, x
	}
	u.brk = toks[len(toks)-1].brk
	return u
}

// shapeText tokenizes, glues and shapes text into units.
func (sh *shaper) shapeText(text string) []unit {
	groups := glue(tokenize(text))
	units := make([]unit, 0, len(groups))
	for _, g := range groups {
		if u := sh.shapeUnit(g); len(u.spans) > 0 {
			units = append(units, u)
		} else if len(units) > 0 && u.brk {
			units[len(units)-1].brk = true
		}
	}
	return units
}

// ---- lines --------------------------------------------------------------------------------

// lineEm is one line of a block: units[a:b].
type lineEm struct {
	a, b     int
	w        float32 // ink width
	inkL     float32 // ink left relative to the pen start of the line
	top, bot float32
}

// block is a set of lines of one face (em units).
type block struct {
	sh    *shaper
	units []unit
	lines []lineEm
	lh    float32   // nominal baseline distance
	gap   float32   // minimum ink gap between lines
	dist  []float32 // baseline distances between consecutive lines
	w     float32   // widest line (ink)
	h     float32   // ink height: top of line 1 … bottom of the last line
}

func (sh *shaper) line(units []unit, a, b int) lineEm {
	l := lineEm{a: a, b: b}
	x := float32(0)
	first := true
	for i := a; i < b; i++ {
		u := &units[i]
		if i > a {
			x += sh.joiner()
		}
		if u.hasInk {
			if first {
				l.inkL, l.w = x+u.inkL, x+u.inkR
				first = false
			} else {
				l.w = max(l.w, x+u.inkR)
			}
			l.top = max(l.top, u.top)
			l.bot = max(l.bot, u.bot)
		}
		x += u.adv
	}
	if first {
		l.inkL, l.w = 0, x
	}
	l.w -= l.inkL
	return l
}

// newBlock lays units out on the given line ranges.
func newBlock(sh *shaper, units []unit, ranges [][2]int, lh, gap float32) *block {
	b := &block{sh: sh, units: units, lh: lh, gap: gap}
	for _, r := range ranges {
		b.lines = append(b.lines, sh.line(units, r[0], r[1]))
	}
	for i, l := range b.lines {
		b.w = max(b.w, l.w)
		if i > 0 {
			p := b.lines[i-1]
			b.dist = append(b.dist, max(lh, p.bot+l.top+gap))
		}
	}
	if n := len(b.lines); n > 0 {
		b.h = b.lines[0].top + b.lines[n-1].bot
		for _, d := range b.dist {
			b.h += d
		}
	}
	return b
}

// forcedRanges are the lines given by "\n" breaks (nil when there are none).
func forcedRanges(units []unit) [][2]int {
	has := false
	for _, u := range units[:max(0, len(units)-1)] {
		has = has || u.brk
	}
	if !has {
		return nil
	}
	var out [][2]int
	a := 0
	for i, u := range units {
		if u.brk || i == len(units)-1 {
			out = append(out, [2]int{a, i + 1})
			a = i + 1
		}
	}
	return out
}

// balanced splits units into k lines (k ≤ maxSplit): the smallest widest
// line (that is the largest font), and among splits within 1.5% of it the
// most even one (least squared slack). Exhaustive — titles are short.
func balanced(sh *shaper, units []unit, k int) [][2]int {
	n := len(units)
	k = min(k, n, maxSplit)
	if k <= 1 {
		return [][2]int{{0, n}}
	}
	w := make([][]float64, n+1)
	for a := 0; a < n; a++ {
		w[a] = make([]float64, n+1)
		for b := a + 1; b <= n; b++ {
			w[a][b] = float64(sh.line(units, a, b).w)
		}
	}
	type cand struct {
		cuts         []int
		worst, slack float64
	}
	var cands []cand
	cuts := make([]int, 0, k-1)
	var walk func(from int)
	walk = func(from int) {
		if len(cuts) == k-1 {
			bounds := append(append([]int{0}, cuts...), n)
			c := cand{cuts: append([]int(nil), cuts...)}
			for i := 0; i+1 < len(bounds); i++ {
				c.worst = math.Max(c.worst, w[bounds[i]][bounds[i+1]])
			}
			for i := 0; i+1 < len(bounds); i++ {
				d := c.worst - w[bounds[i]][bounds[i+1]]
				c.slack += d * d
			}
			cands = append(cands, c)
			return
		}
		// Leave at least one unit for every remaining line.
		for c := from; c <= n-(k-1-len(cuts)); c++ {
			cuts = append(cuts, c)
			walk(c + 1)
			cuts = cuts[:len(cuts)-1]
		}
	}
	walk(1)
	best := cands[0]
	for _, c := range cands[1:] {
		if c.worst < best.worst {
			best = c
		}
	}
	limit := best.worst * 1.015
	for _, c := range cands {
		if c.worst <= limit && c.slack < best.slack {
			best = c
		}
	}
	out := make([][2]int, 0, k)
	prev := 0
	for _, c := range append(best.cuts, n) {
		out = append(out, [2]int{prev, c})
		prev = c
	}
	return out
}

// maxSplit is the most lines balanced() splits into.
const maxSplit = 4
