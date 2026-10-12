package cover

// Render: the design is laid out as a vertical stack — badge, title,
// subtitle — inside a text box chosen by the position, within the safe area
// of the canvas (YouTube draws the duration over the bottom-right corner of
// horizontal thumbnails; Shorts shelves cover the top and bottom of vertical
// ones). The title size is the largest that fits (optionally capped by
// Spec.Size), trying 1–3 balanced lines (1–4 on vertical Shorts covers).

import (
	"errors"
	"image"
	"image/color"
	"math"
	"strings"
)

// Reference canvases: Spec.Size is in pixels of these.
var (
	Landscape = image.Pt(1280, 720)  // YouTube thumbnail
	Portrait  = image.Pt(1080, 1920) // Shorts cover
)

// SizeFor is the output size of a project aspect ("9:16" → Shorts cover).
func SizeFor(aspect string) image.Point {
	if strings.TrimSpace(aspect) == "9:16" {
		return Portrait
	}
	return Landscape
}

// frame is the canvas geometry.
type frame struct {
	w, h     float32
	vertical bool
	unit     float32 // min(w, h): the layout's size unit
	scale    float32 // canvas px per reference px
	safe     rectF
	avoid    []rectF
}

func frameFor(size image.Point) frame {
	w, h := float32(size.X), float32(size.Y)
	f := frame{w: w, h: h, vertical: size.Y > size.X, unit: min(w, h)}
	if f.vertical {
		f.scale = w / float32(Portrait.X)
		f.safe = rectF{0.05 * w, 0.08 * h, 0.95 * w, 0.80 * h}
	} else {
		f.scale = w / float32(Landscape.X)
		f.safe = rectF{0.045 * w, 0.065 * h, 0.955 * w, 0.935 * h}
		// The duration badge (and the progress bar) of the YouTube player.
		f.avoid = []rectF{{0.80 * w, 0.835 * h, w, h}}
	}
	return f
}

// SafeArea is where text may go on a canvas of size, and the corner(s) it
// must keep clear of.
func SafeArea(size image.Point) (safe image.Rectangle, avoid []image.Rectangle) {
	f := frameFor(size)
	safe = f.safe.rect()
	for _, a := range f.avoid {
		avoid = append(avoid, a.rect())
	}
	return safe, avoid
}

// box is the text area of a position.
func (f frame) box(pos string) rectF {
	s := f.safe
	sw, sh := s.w(), s.h()
	frac := float32(0.54)
	if f.vertical {
		frac = 0.44
	}
	mid := (s.y0 + s.y1) / 2
	switch pos {
	case "top":
		return rectF{s.x0, s.y0, s.x1, s.y0 + sh*frac}
	case "center":
		return rectF{s.x0, mid - sh*0.37, s.x1, mid + sh*0.37}
	case "left", "right":
		if f.vertical {
			return rectF{s.x0, mid - sh*0.32, s.x1, mid + sh*0.32}
		}
		if pos == "left" {
			return rectF{s.x0, s.y0, s.x0 + sw*0.6, s.y1}
		}
		y1 := s.y1
		for _, a := range f.avoid {
			y1 = min(y1, a.y0-0.01*f.h)
		}
		return rectF{s.x1 - sw*0.6, s.y0, s.x1, y1}
	}
	return rectF{s.x0, s.y1 - sh*frac, s.x1, s.y1}
}

// ---- parts ----------------------------------------------------------------------------

// part is a shaped text with its candidate line splits.
type part struct {
	sh    *shaper
	units []unit
	cands []*block
}

func (p *part) empty() bool { return p == nil || len(p.units) == 0 }

func newPart(fc *face, text string, track float32, upper bool, maxLines int, lh, gap float32) *part {
	p := &part{sh: newShaper(fc, track, upper)}
	p.units = p.sh.shapeText(text)
	if len(p.units) == 0 {
		return p
	}
	if fr := forcedRanges(p.units); fr != nil {
		if len(fr) > maxLines { // join the extra lines into the last one
			fr = append(fr[:maxLines-1], [2]int{fr[maxLines-1][0], fr[len(fr)-1][1]})
		}
		p.cands = []*block{newBlock(p.sh, p.units, fr, lh, gap)}
		return p
	}
	for k := 1; k <= min(maxLines, len(p.units)); k++ {
		p.cands = append(p.cands, newBlock(p.sh, p.units, balanced(p.sh, p.units, k), lh, gap))
	}
	return p
}

// ---- geometry ---------------------------------------------------------------------------

// lineGeom is a placed line (px).
type lineGeom struct {
	a, b  int
	penX  float32
	base  float32
	ink   rectF
	outer rectF
}

// textGeom is a placed block (px).
type textGeom struct {
	blk   *block
	size  float32
	lines []lineGeom
	outer rectF // everything that belongs to the text (outline, plates, box)
	box   rectF // box behind the whole block ("box" scrim, subtitle box)
}

func (t *textGeom) translate(dx, dy float32) {
	for i := range t.lines {
		l := &t.lines[i]
		l.penX += dx
		l.base += dy
		l.ink = l.ink.translate(dx, dy)
		l.outer = l.outer.translate(dx, dy)
	}
	t.outer = t.outer.translate(dx, dy)
	t.box = t.box.translate(dx, dy)
}

// badgeGeom is the placed badge.
type badgeGeom struct {
	text   textGeom
	pill   rectF // unrotated
	tilt   float32
	cx, cy float32 // rotation centre
	bbox   rectF   // rotated bounds
}

func (b *badgeGeom) translate(dx, dy float32) {
	b.text.translate(dx, dy)
	b.pill = b.pill.translate(dx, dy)
	b.bbox = b.bbox.translate(dx, dy)
	b.cx += dx
	b.cy += dy
}

// geom is a full layout at one title size.
type geom struct {
	s     float32
	title *textGeom
	sub   *textGeom
	badge *badgeGeom
	stack rectF
	fits  bool
}

// protected are the rectangles that must stay inside the safe area and out
// of the avoided corners.
func (g *geom) protected() []rectF {
	var out []rectF
	for _, t := range []*textGeom{g.title, g.sub} {
		if t == nil {
			continue
		}
		for _, l := range t.lines {
			out = append(out, l.outer)
		}
		if !t.box.empty() {
			out = append(out, t.box)
		}
	}
	if g.badge != nil {
		out = append(out, g.badge.bbox)
	}
	return out
}

func (g *geom) translate(dx, dy float32) {
	for _, t := range []*textGeom{g.title, g.sub} {
		if t != nil {
			t.translate(dx, dy)
		}
	}
	if g.badge != nil {
		g.badge.translate(dx, dy)
	}
	g.stack = g.stack.translate(dx, dy)
}

// ---- renderer ---------------------------------------------------------------------------

type renderer struct {
	spec  Spec
	look  Look
	fr    frame
	pos   string
	box   rectF
	align string

	titleFace, subFace, badgeFace *face
	title, sub, badge             *part

	textCol, strokeCol, outerCol, hlCol color.NRGBA
	hasStroke, hasOuter                 bool
}

// Plate / box / band paddings (em of the text they belong to).
const (
	platePadX, platePadY = 0.26, 0.11
	boxPadX, boxPadY     = 0.3, 0.17
	bandPadY             = 0.2
	subBoxPadX           = 0.42
	subBoxPadY           = 0.24
	badgePadX, badgePadY = 0.55, 0.32
)

func newRenderer(spec Spec, size image.Point) (*renderer, error) {
	r := &renderer{spec: spec, look: spec.Look, fr: frameFor(size)}
	lk := &r.look
	if lk.LineHeight <= 0 {
		lk.LineHeight = 1.05
	}
	if lk.MaxSize <= 0 {
		lk.MaxSize = 0.2
	}
	if lk.SubtitleScale <= 0 {
		lk.SubtitleScale = 0.42
	}
	pos := spec.Position
	if !knownPosition(pos) {
		pos = "bottom"
	}
	r.pos = pos
	r.box = r.fr.box(pos)
	switch {
	case pos == "left":
		r.align = "left"
	case pos == "right":
		r.align = "right"
	case lk.Align == "left" || lk.Align == "right" || lk.Align == "center":
		r.align = lk.Align
	default:
		r.align = "center"
	}

	var err error
	if r.titleFace, err = loadFace(spec.Font); err != nil {
		return nil, err
	}
	subID := lk.SubtitleFont
	if subID == "" {
		subID = spec.Font
		if r.titleFace.def.Script {
			subID = "rubik"
		}
	}
	if r.subFace, err = loadFace(subID); err != nil {
		return nil, err
	}
	badgeID := lk.BadgeFont
	if badgeID == "" {
		badgeID = spec.Font
		if r.titleFace.def.Script {
			badgeID = fallbackFont
		}
	}
	if r.badgeFace, err = loadFace(badgeID); err != nil {
		return nil, err
	}

	r.textCol = parseHex(spec.Color)
	r.hasStroke = spec.Stroke != "" && lk.StrokeWidth > 0
	if r.hasStroke {
		r.strokeCol = parseHex(spec.Stroke)
	}
	r.hasOuter = r.hasStroke && lk.Outer != "" && lk.OuterWidth > 0
	if r.hasOuter {
		r.outerCol = spec.pick(lk.Outer, color.NRGBA{255, 255, 255, 255})
	}
	r.hlCol = spec.pick(lk.Highlight, parseHex(spec.Accent))

	upper := lk.Uppercase && !r.titleFace.def.Script
	lh := lk.LineHeight
	if r.titleFace.def.Script {
		lh = max(lh, 1.12)
	}
	gap := float32(0.05)
	if lk.Scrim == "plates" {
		gap = 2*platePadY - 0.06
	}
	r.title = newPart(r.titleFace, spec.Title, lk.Tracking, upper, r.maxLines(), lh, gap)
	subUpper := lk.SubtitleUpper && !r.subFace.def.Script
	subGap := float32(0.08)
	if lk.SubtitleBox != "" {
		subGap = 0.12
	}
	r.sub = newPart(r.subFace, spec.Subtitle, lk.SubtitleTrack, subUpper, 2, 1.12, subGap)
	r.badge = newPart(r.badgeFace, strings.ReplaceAll(spec.Badge, "*", ""), 0.02, !r.badgeFace.def.Script, 1, 1, 0)
	return r, nil
}

// maxLines is the most title lines: 3, or 4 on a vertical (Shorts) cover —
// its canvas is less than half as wide, three lines would leave the title
// unreadably small.
func (r *renderer) maxLines() int {
	if r.fr.vertical {
		return MaxLinesShorts
	}
	return MaxLines
}

// Title line limits.
const (
	MaxLines       = 3
	MaxLinesShorts = 4
)

// outline is the title's outline width in em (stroke + outer).
func (r *renderer) outline() float32 {
	w := float32(0)
	if r.hasStroke {
		w += r.look.StrokeWidth
		if r.hasOuter {
			w += r.look.OuterWidth
		}
	}
	return w
}

// layoutTitle places blk at size s with the pen of line 0 at x=0 and its
// baseline at y=0; lines are aligned inside the block's width.
func (r *renderer) layoutTitle(blk *block, s float32) *textGeom {
	t := &textGeom{blk: blk, size: s}
	e := r.outline() * s
	var maxTop, maxBot float32
	for _, l := range blk.lines {
		maxTop, maxBot = max(maxTop, l.top), max(maxBot, l.bot)
	}
	base := float32(0)
	for i, l := range blk.lines {
		if i > 0 {
			base += blk.dist[i-1] * s
		}
		ink := rectF{l.inkL * s, base - l.top*s, (l.inkL + l.w) * s, base + l.bot*s}
		outer := ink.inset(-e, -e)
		switch r.look.Scrim {
		case "plates":
			outer = rectF{ink.x0 - platePadX*s, base - (maxTop+platePadY)*s, ink.x1 + platePadX*s, base + (maxBot+platePadY)*s}
		case "band":
			outer = rectF{ink.x0 - e, base - (maxTop+bandPadY)*s, ink.x1 + e, base + (maxBot+bandPadY)*s}
		}
		t.lines = append(t.lines, lineGeom{a: l.a, b: l.b, base: base, ink: ink, outer: outer})
	}
	alignLines(t, r.align)
	if r.look.Scrim == "box" {
		var u rectF
		for _, l := range t.lines {
			u = u.union(l.ink)
		}
		t.box = rectF{u.x0 - boxPadX*s, u.y0 - boxPadY*s, u.x1 + boxPadX*s, u.y1 + boxPadY*s}
		t.outer = t.box
	}
	return t
}

// layoutSub places a subtitle block like layoutTitle.
func (r *renderer) layoutSub(blk *block, s float32) *textGeom {
	t := &textGeom{blk: blk, size: s}
	e := float32(0)
	if r.look.SubtitleStroke && r.hasStroke && r.look.SubtitleBox == "" {
		e = r.look.StrokeWidth * s
	}
	base := float32(0)
	for i, l := range blk.lines {
		if i > 0 {
			base += blk.dist[i-1] * s
		}
		ink := rectF{l.inkL * s, base - l.top*s, (l.inkL + l.w) * s, base + l.bot*s}
		t.lines = append(t.lines, lineGeom{a: l.a, b: l.b, base: base, ink: ink, outer: ink.inset(-e, -e)})
	}
	alignLines(t, r.align)
	if r.look.SubtitleBox != "" {
		var u rectF
		capTop := r.subFace.capH
		for _, l := range t.lines {
			// Box height from the cap height: lines without descenders or
			// accents get the same box.
			u = u.union(rectF{l.ink.x0, l.base - capTop*s, l.ink.x1, l.base})
			u = u.union(l.ink)
		}
		t.box = rectF{u.x0 - subBoxPadX*s, u.y0 - subBoxPadY*s, u.x1 + subBoxPadX*s, u.y1 + subBoxPadY*s}
		t.outer = t.box
	}
	return t
}

// alignLines shifts every line so the outers line up (left/center/right)
// inside the block and computes the block's outer rectangle.
func alignLines(t *textGeom, align string) {
	var w float32
	for _, l := range t.lines {
		w = max(w, l.outer.w())
	}
	var u rectF
	for i := range t.lines {
		l := &t.lines[i]
		var dx float32
		switch align {
		case "left":
			dx = -l.outer.x0
		case "right":
			dx = w - l.outer.x1
		default:
			dx = (w-l.outer.w())/2 - l.outer.x0
		}
		l.penX += dx
		l.ink = l.ink.translate(dx, 0)
		l.outer = l.outer.translate(dx, 0)
		u = u.union(l.outer)
	}
	t.outer = u
}

// layoutBadge builds the pill at text size s (top-left of its bounds at 0,0).
func (r *renderer) layoutBadge(blk *block, s float32) *badgeGeom {
	t := &textGeom{blk: blk, size: s}
	l := blk.lines[0]
	capH := r.badgeFace.capH
	ink := rectF{l.inkL * s, -l.top * s, (l.inkL + l.w) * s, l.bot * s}
	t.lines = []lineGeom{{a: l.a, b: l.b, ink: ink, outer: ink}}
	h := (capH + 2*badgePadY) * s
	w := l.w*s + 2*badgePadX*s
	pill := rectF{0, 0, w, h}
	// Text centred on the cap height.
	dx := badgePadX*s - ink.x0
	dy := badgePadY*s + capH*s
	t.translate(dx, dy)
	b := &badgeGeom{text: *t, pill: pill, tilt: r.look.BadgeTilt, cx: w / 2, cy: h / 2}
	b.bbox = rotatedBounds(pill, b.tilt, b.cx, b.cy)
	b.translate(-b.bbox.x0, -b.bbox.y0)
	return b
}

func rotatedBounds(rc rectF, deg, cx, cy float32) rectF {
	if deg == 0 {
		return rc
	}
	m := identity.rotateAbout(deg, cx, cy)
	var out rectF
	for i, p := range []pt{{rc.x0, rc.y0}, {rc.x1, rc.y0}, {rc.x1, rc.y1}, {rc.x0, rc.y1}} {
		x, y := m.apply(p)
		if i == 0 {
			out = rectF{x, y, x, y}
		} else {
			out = rectF{min(out.x0, x), min(out.y0, y), max(out.x1, x), max(out.y1, y)}
		}
	}
	return out
}

// place lays the whole stack out with title block cand at title size s.
func (r *renderer) place(cand *block, s float32) *geom {
	g := &geom{s: s}
	u := r.fr.unit
	lk := &r.look
	var elems []struct {
		w, h float32
		set  func(x, y float32)
	}
	add := func(w, h float32, set func(x, y float32)) {
		elems = append(elems, struct {
			w, h float32
			set  func(x, y float32)
		}{w, h, set})
	}

	boxW := r.box.w()
	if !r.badge.empty() {
		sb := max(0.036*u, min(0.07*u, s*0.38))
		blk := r.badge.cands[0]
		if need := (blk.w + 2*badgePadX) * sb; need > boxW {
			sb *= boxW / need
		}
		b := r.layoutBadge(blk, sb)
		g.badge = b
		add(b.bbox.w(), b.bbox.h(), func(x, y float32) { b.translate(x-b.bbox.x0, y-b.bbox.y0) })
	}
	if cand != nil {
		t := r.layoutTitle(cand, s)
		g.title = t
		add(t.outer.w(), t.outer.h(), func(x, y float32) { t.translate(x-t.outer.x0, y-t.outer.y0) })
	}
	if !r.sub.empty() {
		target := max(0.032*u, s*lk.SubtitleScale)
		target = min(target, 0.08*u)
		var best *textGeom
		for _, blk := range r.sub.cands {
			ext := float32(0)
			if lk.SubtitleBox != "" {
				ext = 2 * subBoxPadX
			} else if lk.SubtitleStroke && r.hasStroke {
				ext = 2 * lk.StrokeWidth
			}
			ss := min(target, boxW/(blk.w+ext))
			if best == nil || ss > best.size*1.04 {
				best = r.layoutSub(blk, ss)
			}
		}
		g.sub = best
		add(best.outer.w(), best.outer.h(), func(x, y float32) { best.translate(x-best.outer.x0, y-best.outer.y0) })
	}
	if len(elems) == 0 {
		return g
	}

	// Gaps between the stacked elements.
	gapBadge := max(0.16*s, 0.012*u)
	gapSub := max(0.14*s, 0.012*u)
	if lk.Glow != "" && lk.GlowStrength > 0 {
		gapSub += 0.1 * s // keep the subtitle out of the title's halo
	}
	if lk.SubtitleBox != "" && (lk.Scrim == "band" || lk.Scrim == "box") {
		gapSub = 0 // two-tier lower third
	}
	var stackW, stackH float32
	for i, e := range elems {
		stackW = max(stackW, e.w)
		stackH += e.h
		if i > 0 {
			if g.badge != nil && i == 1 {
				stackH += gapBadge
			} else {
				stackH += gapSub
			}
		}
	}
	y := r.box.y0
	switch r.pos {
	case "top":
	case "bottom":
		y = r.box.y1 - stackH
	default:
		y = (r.box.y0+r.box.y1)/2 - stackH/2
	}
	g.stack = rectF{0, y, 0, y + stackH}
	for i, e := range elems {
		if i > 0 {
			if g.badge != nil && i == 1 {
				y += gapBadge
			} else {
				y += gapSub
			}
		}
		var x float32
		switch r.align {
		case "left":
			x = r.box.x0
		case "right":
			x = r.box.x1 - e.w
		default:
			x = (r.box.x0+r.box.x1)/2 - e.w/2
		}
		e.set(x, y)
		y += e.h
	}
	var sx rectF
	for _, pr := range g.protected() {
		sx = sx.union(pr)
	}
	g.stack = rectF{sx.x0, g.stack.y0, sx.x1, g.stack.y1}

	// Keep clear of the duration badge: move the stack up just enough.
	var lift float32
	for _, a := range r.fr.avoid {
		for _, pr := range g.protected() {
			if pr.overlaps(a) {
				lift = max(lift, pr.y1-a.y0+0.012*u)
			}
		}
	}
	if lift > 0 {
		g.translate(0, -lift)
	}
	g.fits = stackW <= boxW+0.5 && stackH <= r.box.h()+0.5
	for _, pr := range g.protected() {
		if !r.fr.safe.contains(pr) {
			g.fits = false
		}
		for _, a := range r.fr.avoid {
			if pr.overlaps(a) {
				g.fits = false
			}
		}
	}
	return g
}

// layout finds the largest title that fits (1…maxLines lines).
func (r *renderer) layout() *geom {
	u := r.fr.unit
	hi := r.look.MaxSize * u
	if r.spec.Size > 0 {
		hi = float32(r.spec.Size) * r.fr.scale
	}
	lo := 0.018 * u
	if r.title.empty() {
		return r.place(nil, min(hi, 0.16*u))
	}
	var best *geom
	for _, cand := range r.title.cands {
		s := hi
		g := r.place(cand, s)
		if !g.fits {
			a, b := lo, hi
			for i := 0; i < 22; i++ {
				m := (a + b) / 2
				if r.place(cand, m).fits {
					a = m
				} else {
					b = m
				}
			}
			s = a
			g = r.place(cand, s)
		}
		if !g.fits {
			continue
		}
		if best == nil || g.s > best.s*1.025 {
			best = g
		}
	}
	if best == nil {
		// Nothing fits even tiny (a very long word on a narrow canvas): use
		// the fullest split at the minimum size.
		best = r.place(r.title.cands[len(r.title.cands)-1], lo)
	}
	return best
}

// Metrics describe how a design fits a canvas.
type Metrics struct {
	TitleSize float64 // title font size, px of the measured canvas (0 = no title)
	Lines     int     // title lines (1–3; up to 4 on a vertical cover)
	// Small: the title ends up too small to read in a phone feed — suggest a
	// shorter title, another layout or a narrower font.
	Small bool
}

// Measure lays spec out on a canvas of size without drawing anything.
func Measure(spec Spec, size image.Point) (Metrics, error) {
	if size.X < 16 || size.Y < 16 {
		return Metrics{}, errors.New("cover: canvas too small")
	}
	r, err := newRenderer(spec, size)
	if err != nil {
		return Metrics{}, err
	}
	if r.title.empty() {
		return Metrics{}, nil
	}
	g := r.layout()
	m := Metrics{TitleSize: float64(g.s)}
	if g.title != nil {
		m.Lines = len(g.title.lines)
	}
	m.Small = g.s < smallTitle*r.fr.unit
	return m, nil
}

// smallTitle: below this fraction of the short canvas side a title is hard
// to read on a phone (about 12 px on a 360 px wide feed thumbnail).
const smallTitle = 0.06

// ---- painting -----------------------------------------------------------------------------

// testHook, when set (tests), receives the final ink masks of the text.
var testHook func(kind string, m *mask)

// Draw paints the text design of spec onto canvas, which already holds the
// picture (see CoverFit).
func Draw(canvas *image.RGBA, spec Spec) error {
	_, err := drawSpec(canvas, spec)
	return err
}

func drawSpec(canvas *image.RGBA, spec Spec) (*geom, error) {
	if canvas == nil || canvas.Rect.Dx() < 16 || canvas.Rect.Dy() < 16 {
		return nil, errors.New("cover: canvas too small")
	}
	if canvas.Rect.Min != (image.Point{}) {
		return nil, errors.New("cover: canvas must start at 0,0")
	}
	r, err := newRenderer(spec, canvas.Rect.Size())
	if err != nil {
		return nil, err
	}
	if r.title.empty() && r.sub.empty() && r.badge.empty() {
		return &geom{}, nil
	}
	g := r.layout()
	r.paintScrim(canvas, g)
	if g.title != nil {
		r.paintTitle(canvas, g.title)
	}
	if g.sub != nil {
		r.paintSub(canvas, g.sub)
	}
	if g.badge != nil {
		r.paintBadge(canvas, g.badge)
	}
	return g, nil
}

// shapes lists the glyph outlines of t (hl: only highlighted, or only plain
// glyphs; all when both are true).
func textShapes(p *part, t *textGeom, plain, hl bool, rot affine, rotate bool) []shape {
	var out []shape
	s := t.size
	j := p.sh.joiner()
	for _, l := range t.lines {
		x := l.penX
		for ui := l.a; ui < l.b; ui++ {
			u := &p.units[ui]
			if ui > l.a {
				x += j * s
			}
			for _, sp := range u.spans {
				if sp.g.empty() || (sp.hl && !hl) || (!sp.hl && !plain) {
					continue
				}
				m := scaleAt(s, x+sp.x*s, l.base)
				if rotate {
					m = compose(rot, m)
				}
				out = append(out, shape{path: sp.g.path, m: m})
			}
			x += u.adv * s
		}
	}
	return out
}

// compose returns outer∘inner.
func compose(outer, inner affine) affine {
	return affine{
		a: outer.a*inner.a + outer.c*inner.b,
		b: outer.b*inner.a + outer.d*inner.b,
		c: outer.a*inner.c + outer.c*inner.d,
		d: outer.b*inner.c + outer.d*inner.d,
		e: outer.a*inner.e + outer.c*inner.f + outer.e,
		f: outer.b*inner.e + outer.d*inner.f + outer.f,
	}
}

// region is r grown by margin and clipped to the canvas.
func region(canvas *image.RGBA, r rectF, margin float32) image.Rectangle {
	return r.inset(-margin, -margin).rect().Intersect(canvas.Rect)
}

func (r *renderer) shadowOn() bool { return r.look.ShadowOpacity > 0 }

func (r *renderer) shadowColor() color.NRGBA {
	return r.spec.pick(r.look.ShadowColor, color.NRGBA{A: 255})
}

// paintText draws one block: glow, shadow, outer outline, outline, fill.
func (r *renderer) paintText(canvas *image.RGBA, p *part, t *textGeom, fill, hl color.NRGBA,
	strokeW, outerW float32, strokeC, outerC color.NRGBA, shadow, glow bool, kind string) {
	s := t.size
	lk := &r.look
	outline := strokeW + outerW
	shadowReach := float32(0)
	if shadow {
		shadowReach = float32(math.Hypot(float64(lk.ShadowX), float64(lk.ShadowY)))*s + 1.6*lk.ShadowBlur*s
	}
	glowReach := float32(0)
	if glow {
		glowReach = 1.6 * lk.GlowRadius * s
	}
	var ink rectF
	for _, l := range t.lines {
		ink = ink.union(l.ink)
	}
	reg := region(canvas, ink, outline+max(shadowReach, glowReach)+3)
	if reg.Empty() {
		return
	}
	all := textShapes(p, t, true, true, affine{}, false)
	fillM := fillMask(reg, all)
	top := fillM
	var strokeM, outerM *mask
	if strokeW > 0 {
		strokeM = dilate(reg, all, strokeW)
		top = strokeM
		if outerW > 0 {
			outerM = dilate(reg, all, strokeW+outerW)
			top = outerM
		}
	}
	if glow {
		gc := r.spec.pick(lk.Glow, parseHex(r.spec.Accent))
		src := top
		if strokeM != nil && lk.StrokeWidth < 0.05 {
			src = strokeM
		}
		strength := lk.GlowStrength
		gm := blur(src, lk.GlowRadius*s/2)
		paint(canvas, gm, gc, min(1, strength), 0, 0)
		if strength > 1 {
			paint(canvas, blur(src, lk.GlowRadius*s/5), gc, strength-1, 0, 0)
		}
	}
	if shadow {
		sm := blur(top, lk.ShadowBlur*s/2)
		paint(canvas, sm, r.shadowColor(), lk.ShadowOpacity, int(math.Round(float64(lk.ShadowX*s))), int(math.Round(float64(lk.ShadowY*s))))
	}
	if outerM != nil {
		paint(canvas, outerM, outerC, 1, 0, 0)
	}
	if strokeM != nil {
		paint(canvas, strokeM, strokeC, 1, 0, 0)
	}
	hasHL := false
	for _, l := range t.lines {
		for ui := l.a; ui < l.b && !hasHL; ui++ {
			for _, sp := range p.units[ui].spans {
				if sp.hl {
					hasHL = true
					break
				}
			}
		}
	}
	if hasHL {
		paint(canvas, fillMask(reg, textShapes(p, t, true, false, affine{}, false)), fill, 1, 0, 0)
		paint(canvas, fillMask(reg, textShapes(p, t, false, true, affine{}, false)), hl, 1, 0, 0)
	} else {
		paint(canvas, fillM, fill, 1, 0, 0)
	}
	if testHook != nil {
		testHook(kind, top)
	}
}

func (r *renderer) paintTitle(canvas *image.RGBA, t *textGeom) {
	lk := &r.look
	s := t.size
	onShape := lk.Scrim == "plates" || lk.Scrim == "box" || lk.Scrim == "band"
	if onShape {
		var shapes []shape
		var bounds rectF
		scrimCol := r.spec.pick(lk.ScrimColor, color.NRGBA{A: 255})
		switch lk.Scrim {
		case "plates":
			for _, l := range t.lines {
				shapes = append(shapes, shape{path: roundRect(l.outer.x0, l.outer.y0, l.outer.x1, l.outer.y1, 0.16*s), m: identity})
				bounds = bounds.union(l.outer)
			}
		case "box":
			shapes = append(shapes, shape{path: roundRect(t.box.x0, t.box.y0, t.box.x1, t.box.y1, 0.05*s), m: identity})
			bounds = t.box
		case "band":
			var u rectF
			for _, l := range t.lines {
				u = u.union(l.outer)
			}
			band := rectF{0, u.y0, r.fr.w, u.y1}
			shapes = append(shapes, shape{path: roundRect(band.x0-2, band.y0, band.x1+2, band.y1, 0), m: identity})
			bounds = band
		}
		reach := float32(0)
		if r.shadowOn() {
			reach = float32(math.Abs(float64(lk.ShadowY)))*s + 1.6*lk.ShadowBlur*s
		}
		reg := region(canvas, bounds, reach+3)
		m := fillMask(reg, shapes)
		if r.shadowOn() {
			paint(canvas, blur(m, lk.ShadowBlur*s/2), r.shadowColor(), lk.ShadowOpacity,
				int(math.Round(float64(lk.ShadowX*s))), int(math.Round(float64(lk.ShadowY*s))))
		}
		paint(canvas, m, scrimCol, max(lk.ScrimOpacity, 0.05), 0, 0)
	}
	var sw, ow float32
	if r.hasStroke {
		sw = lk.StrokeWidth * s
		if r.hasOuter {
			ow = lk.OuterWidth * s
		}
	}
	r.paintText(canvas, r.title, t, r.textCol, r.hlCol, sw, ow, r.strokeCol, r.outerCol,
		r.shadowOn() && !onShape, lk.Glow != "" && lk.GlowStrength > 0, "title")
}

func (r *renderer) paintSub(canvas *image.RGBA, t *textGeom) {
	lk := &r.look
	s := t.size
	col := r.spec.pick(lk.SubtitleColor, r.textCol)
	if lk.SubtitleBox != "" {
		bc := r.spec.pick(lk.SubtitleBox, color.NRGBA{A: 255})
		reg := region(canvas, t.box, 0.3*s+3)
		m := fillMask(reg, []shape{{path: roundRect(t.box.x0, t.box.y0, t.box.x1, t.box.y1, 0.1*s), m: identity}})
		if r.shadowOn() {
			paint(canvas, blur(m, 0.2*s), color.NRGBA{A: 255}, 0.3, 0, int(math.Round(float64(0.06*s))))
		}
		paint(canvas, m, bc, 1, 0, 0)
		r.paintText(canvas, r.sub, t, col, col, 0, 0, color.NRGBA{}, color.NRGBA{}, false, false, "sub")
		return
	}
	var sw float32
	if lk.SubtitleStroke && r.hasStroke {
		sw = lk.StrokeWidth * s
	}
	shadow := r.shadowOn() || sw == 0 // an unboxed subtitle always gets a soft shadow
	saved := r.look
	if !r.shadowOn() {
		r.look.ShadowY, r.look.ShadowBlur, r.look.ShadowOpacity = 0.05, 0.18, 0.7
	}
	r.paintText(canvas, r.sub, t, col, col, sw, 0, r.strokeCol, color.NRGBA{}, shadow, false, "sub")
	r.look = saved
}

func (r *renderer) paintBadge(canvas *image.RGBA, b *badgeGeom) {
	lk := &r.look
	s := b.text.size
	fillC := r.spec.pick(lk.BadgeFill, parseHex(r.spec.Accent))
	textC := r.spec.pick(lk.BadgeText, contrastText(fillC))
	rot := identity.rotateAbout(b.tilt, b.cx, b.cy)
	pill := shape{path: roundRect(b.pill.x0, b.pill.y0, b.pill.x1, b.pill.y1, b.pill.h()/2), m: rot}
	reg := region(canvas, b.bbox, 0.5*s+3)
	m := fillMask(reg, []shape{pill})
	paint(canvas, blur(m, 0.18*s), color.NRGBA{A: 255}, 0.35, 0, int(math.Round(float64(0.08*s))))
	paint(canvas, m, fillC, 1, 0, 0)
	tm := fillMask(reg, textShapes(r.badge, &b.text, true, true, rot, true))
	paint(canvas, tm, textC, 1, 0, 0)
	if testHook != nil {
		testHook("badge", m)
	}
}

// paintScrim darkens the picture behind the text (gradient from the nearest
// edge or a soft band), then the vignette.
func (r *renderer) paintScrim(canvas *image.RGBA, g *geom) {
	lk := &r.look
	col := r.spec.pick(lk.ScrimColor, color.NRGBA{A: 255})
	if lk.Scrim == "gradient" && lk.ScrimOpacity > 0 && !g.stack.empty() {
		st := g.stack
		w, h := r.fr.w, r.fr.h
		switch r.pos {
		case "top":
			gradient(canvas, col, lk.ScrimOpacity, false, 0, st.y1+0.2*h, st.y1-st.y0)
		case "left":
			if r.fr.vertical {
				band(canvas, col, lk.ScrimOpacity, st, 0.22*h)
			} else {
				gradient(canvas, col, lk.ScrimOpacity, true, 0, st.x1+0.2*w, st.x1-st.x0)
			}
		case "right":
			if r.fr.vertical {
				band(canvas, col, lk.ScrimOpacity, st, 0.22*h)
			} else {
				gradient(canvas, col, lk.ScrimOpacity, true, w, st.x0-0.2*w, st.x1-st.x0)
			}
		case "center":
			band(canvas, col, lk.ScrimOpacity, st, 0.22*h)
		default:
			gradient(canvas, col, lk.ScrimOpacity, false, h, st.y0-0.2*h, st.y1-st.y0)
		}
	}
	if lk.Vignette > 0 {
		vignette(canvas, lk.Vignette)
	}
}

// gradient darkens from edge (x or y, horizontal when along x) towards end:
// full strength over the first 40% of the text extent, then eased to zero.
func gradient(dst *image.RGBA, c color.NRGBA, maxA float32, horizontal bool, edge, end, textExt float32) {
	dist := float32(math.Abs(float64(end - edge)))
	if dist < 1 {
		return
	}
	solid := min(0.35*dist, 0.4*textExt)
	alpha := func(t float32) float32 { // t = distance from the edge
		if t <= solid {
			return maxA
		}
		u := (t - solid) / (dist - solid)
		if u >= 1 {
			return 0
		}
		v := 1 - u
		return maxA * v * v * (3 - 2*v)
	}
	b := dst.Rect
	n := b.Dy()
	if horizontal {
		n = b.Dx()
	}
	line := make([]uint8, n)
	for i := range line {
		pos := float32(i) + 0.5
		t := float32(math.Abs(float64(pos - edge)))
		line[i] = uint8(alpha(t)*255 + 0.5)
	}
	tint(dst, c, horizontal, line)
}

// band darkens a soft horizontal band around st (fading over fade px).
func band(dst *image.RGBA, c color.NRGBA, maxA float32, st rectF, fade float32) {
	n := dst.Rect.Dy()
	line := make([]uint8, n)
	for y := range line {
		p := float32(y) + 0.5
		var d float32
		switch {
		case p < st.y0:
			d = st.y0 - p
		case p > st.y1:
			d = p - st.y1
		}
		v := 1 - min(1, d/fade)
		line[y] = uint8(maxA*v*v*(3-2*v)*255 + 0.5)
	}
	tint(dst, c, false, line)
}

// tint blends c with a per-row (or per-column) alpha profile.
func tint(dst *image.RGBA, c color.NRGBA, horizontal bool, line []uint8) {
	b := dst.Rect
	cr, cg, cb := uint32(c.R), uint32(c.G), uint32(c.B)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := dst.Pix[dst.PixOffset(b.Min.X, y):]
		ay := uint32(0)
		if !horizontal {
			ay = uint32(line[y-b.Min.Y])
			if ay == 0 {
				continue
			}
		}
		for x := 0; x < b.Dx(); x++ {
			a := ay
			if horizontal {
				a = uint32(line[x])
				if a == 0 {
					continue
				}
			}
			p := row[x*4 : x*4+3 : x*4+3]
			ia := 255 - a
			p[0] = uint8((uint32(p[0])*ia + cr*a + 127) / 255)
			p[1] = uint8((uint32(p[1])*ia + cg*a + 127) / 255)
			p[2] = uint8((uint32(p[2])*ia + cb*a + 127) / 255)
		}
	}
}

// vignette darkens the corners (strength 0–1).
func vignette(dst *image.RGBA, strength float32) {
	b := dst.Rect
	w, h := float32(b.Dx()), float32(b.Dy())
	cx, cy := w/2, h/2
	vy := float32(0.9)
	if h > w { // tall canvas: keep the top and bottom lighter
		vy = 0.55
	}
	for y := 0; y < b.Dy(); y++ {
		dy := (float32(y) + 0.5 - cy) / cy
		row := dst.Pix[dst.PixOffset(b.Min.X, b.Min.Y+y):]
		for x := 0; x < b.Dx(); x++ {
			dx := (float32(x) + 0.5 - cx) / cx
			d := dx*dx*0.75 + dy*dy*vy
			if d < 0.36 {
				continue
			}
			t := min(1, (d-0.36)/1.1)
			a := uint32(strength*t*t*(3-2*t)*255 + 0.5)
			if a == 0 {
				continue
			}
			ia := 255 - a
			p := row[x*4 : x*4+3 : x*4+3]
			p[0] = uint8((uint32(p[0])*ia + 127) / 255)
			p[1] = uint8((uint32(p[1])*ia + 127) / 255)
			p[2] = uint8((uint32(p[2])*ia + 127) / 255)
		}
	}
}
