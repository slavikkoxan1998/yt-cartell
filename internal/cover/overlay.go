package cover

// Overlay: the animated on-screen text the montage draws over a scene
// (motion graphics, explainers, key facts) — a headline, a key number and a
// caption laid out for the film frame with the cover faces, animated in
// (fade, slide, pop, a counting number, words appearing one by one) and out.
//
// NewOverlayClip lays the design out once; Frame(t) composes the frame at t
// seconds into the scene as a transparent RGBA image of the clip's Bounds
// (the montage composites it over the picture); Still tells which frames do
// not change, so a caller writes each distinct picture only once.
//
//	center  the key number big in the middle, the headline above it as an
//	        accent label, a short accent bar and the caption below (no
//	        number: the headline is the hero) — motion graphics
//	top     the same stack near the top of the frame
//	lower   a lower third: an accent bar on the left, the number / headline
//	        and the caption left-aligned above the subtitles — footage

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
)

// Overlay is the on-screen text of one scene.
type Overlay struct {
	Title   string // headline (a label above the number when there is one)
	Value   string // a key number or fact: "73%", "1 200 км", "1914"
	Caption string // a short supporting line
	// Anim: AnimFade | AnimSlide | AnimPop | AnimCount | AnimType ("" =
	// a counter for a number, else a fade).
	Anim string
	// Position: "center" | "top" | "lower" ("" = center).
	Position string
	Accent   string // #RRGGBB of the label and the bar ("" = warm yellow)
	Font     string // catalog face ("" = montserrat)
}

// Overlay animations.
const (
	AnimFade  = "fade"
	AnimSlide = "slide"
	AnimPop   = "pop"
	AnimCount = "count"
	AnimType  = "type"
)

// DefaultAccent is the overlay accent when none is given.
const DefaultAccent = "#FFD23F"

// Empty reports an overlay without text.
func (o Overlay) Empty() bool {
	return strings.TrimSpace(o.Title) == "" && strings.TrimSpace(o.Value) == "" && strings.TrimSpace(o.Caption) == ""
}

// Timing of the animations (s).
const (
	ovIn       = 0.5  // an element's entrance
	ovPop      = 0.42 // a pop entrance
	ovWord     = 0.3  // one word of the typed headline
	ovWordStep = 0.13 // between two words
	ovCount    = 1.2  // the number counting up
	ovOut      = 0.32 // everything leaves together
)

// OverlayClip is a laid-out overlay for one scene.
type OverlayClip struct {
	size       image.Point
	box        image.Rectangle
	show, hide float64 // the text is entirely gone before show / after hide
	anim       string
	unit       float64 // min(W, H): the layout's size unit
	pace       float64 // time scale (<1 squeezes the entrance into a short scene)
	els        []*ovElement
	bar        ovBar
	scrim      *image.RGBA // a soft dark glow behind the text (frame coordinates)
	inEnd      float64     // seconds after show when every entrance is over
}

// ovElement is one text part with its entrance.
type ovElement struct {
	sprites []ovSprite // the drawn text: one sprite, or one per word (typing)
	delay   float64    // after show (unscaled)
	anim    string
	count   *ovCounter // a counting number (drawn per frame)
}

// ovSprite is a pre-drawn piece of text in frame coordinates.
type ovSprite struct {
	img   *image.RGBA
	delay float64 // extra delay (typing)
}

// ovBar is the accent bar.
type ovBar struct {
	on       bool
	r        rectF // final rectangle (frame px)
	vertical bool  // grows upwards (lower third) instead of from the middle
	delay    float64
	col      color.NRGBA
}

// ovCounter draws a number counting up to its value.
type ovCounter struct {
	prefix, suffix string
	value          float64
	decimals       int
	group          string // thousands separator ("" = none)
	decimal        string // decimal mark
	fc             *face
	size           float32
	x, top         float32
	align          string
	fill           color.NRGBA
}

// NewOverlayClip lays o out for a film frame of size; the text enters after
// show and has left at hide (seconds into the scene).
func NewOverlayClip(o Overlay, size image.Point, show, hide float64) (*OverlayClip, error) {
	if size.X < 32 || size.Y < 32 {
		return nil, errors.New("cover: overlay frame too small")
	}
	if o.Empty() {
		return nil, errors.New("cover: overlay without text")
	}
	if hide <= show {
		return nil, errors.New("cover: overlay has no time on screen")
	}
	fc, err := loadFace(o.Font)
	if err != nil {
		return nil, err
	}
	W, H := float32(size.X), float32(size.Y)
	U := min(W, H)
	c := &OverlayClip{size: size, show: show, hide: hide, unit: float64(U), anim: strings.TrimSpace(o.Anim)}
	value := strings.TrimSpace(o.Value)
	if c.anim == "" {
		c.anim = AnimFade
		if _, ok := parseCount(value); ok {
			c.anim = AnimCount
		}
	}
	accent := DefaultAccent
	if a, ok := normHex(o.Accent); ok {
		accent = a
	}
	acc := parseHex(accent)
	white := color.NRGBA{255, 255, 255, 255}
	soft := color.NRGBA{238, 238, 238, 255}
	vertical := H > W
	title, caption := strings.TrimSpace(o.Title), strings.TrimSpace(o.Caption)
	if title == "" && value == "" { // a lone caption is the headline
		title, caption = caption, ""
	}
	pos := strings.TrimSpace(o.Position)
	if pos == "lower" {
		c.layoutLower(fc, title, value, caption, W, H, U, vertical, white, soft, acc)
	} else {
		c.layoutCenter(fc, title, value, caption, pos == "top", W, H, U, vertical, white, soft, acc)
	}
	c.finish()
	return c, nil
}

// part kinds of a stack
type ovPart struct {
	tb    *textBlock
	fill  color.NRGBA
	hero  bool // the headline / number (typing and counting act on it)
	value bool
	gap   float32 // space above it
	delay float64
}

func (c *OverlayClip) layoutCenter(fc *face, title, value, caption string, top bool, W, H, U float32, vertical bool,
	white, soft, acc color.NRGBA) {
	maxW := 0.84 * W
	if vertical {
		maxW = 0.88 * W
	}
	var parts []ovPart
	if value != "" {
		if title != "" {
			parts = append(parts, ovPart{tb: layoutText(fc, title, 0.05*U, maxW, 2, true, 0.06), fill: acc, delay: 0})
		}
		parts = append(parts, ovPart{tb: layoutText(fc, value, 0.2*U, maxW, 1, false, 0), fill: white, hero: true, value: true, gap: 0.025 * U, delay: 0.1})
	} else if title != "" {
		upper := utf8.RuneCountInString(title) <= 48
		size := float32(0.095)
		if vertical {
			size = 0.1
		}
		lines := 3
		if vertical {
			lines = 4
		}
		parts = append(parts, ovPart{tb: layoutText(fc, title, size*U, maxW, lines, upper, 0.01), fill: white, hero: true, delay: 0})
	}
	barH, barW := 0.011*U, 0.075*U
	barGap := 0.032 * U
	if caption != "" {
		parts = append(parts, ovPart{tb: layoutText(fc, caption, 0.042*U, maxW, 2, false, 0), fill: soft, gap: 2*barGap + barH, delay: 0.35})
	}
	// Stack height.
	h := float32(0)
	for i, p := range parts {
		if i > 0 {
			h += p.gap
		}
		h += p.tb.height()
	}
	if caption == "" {
		h += barGap + barH
	}
	y := (H - h) / 2
	switch {
	case top && vertical:
		y = 0.16 * H
	case top:
		y = 0.11 * H
	case vertical:
		y = (H - h) * 0.45
	}
	cx := W / 2
	heroBottom := y
	for i, p := range parts {
		if i > 0 {
			y += p.gap
		}
		c.addPart(p, cx, y, "center")
		y += p.tb.height()
		if p.hero {
			heroBottom = y
		}
	}
	c.bar = ovBar{on: true, r: rectF{cx - barW/2, heroBottom + barGap, cx + barW/2, heroBottom + barGap + barH}, delay: 0.25, col: acc}
}

func (c *OverlayClip) layoutLower(fc *face, title, value, caption string, W, H, U float32, vertical bool,
	white, soft, acc color.NRGBA) {
	x0 := 0.06 * W
	maxW := 0.62 * W
	if vertical {
		x0, maxW = 0.08*W, 0.8*W
	}
	barW := 0.009 * U
	tx := x0 + barW + 0.028*U
	var parts []ovPart
	if value != "" {
		parts = append(parts, ovPart{tb: layoutText(fc, value, 0.11*U, maxW, 1, false, 0), fill: white, hero: true, value: true, delay: 0.08})
		if title != "" {
			parts = append(parts, ovPart{tb: layoutText(fc, title, 0.05*U, maxW, 2, false, 0), fill: white, gap: 0.02 * U, delay: 0.16})
		}
	} else if title != "" {
		parts = append(parts, ovPart{tb: layoutText(fc, title, 0.065*U, maxW, 2, false, 0), fill: white, hero: true, delay: 0.08})
	}
	if caption != "" {
		parts = append(parts, ovPart{tb: layoutText(fc, caption, 0.036*U, maxW, 2, false, 0), fill: soft, gap: 0.018 * U, delay: 0.3})
	}
	h := float32(0)
	for i, p := range parts {
		if i > 0 {
			h += p.gap
		}
		h += p.tb.height()
	}
	bottom := 0.8 * H
	if vertical {
		bottom = 0.74 * H
	}
	y := bottom - h
	for i, p := range parts {
		if i > 0 {
			y += p.gap
		}
		c.addPart(p, tx, y, "left")
		y += p.tb.height()
	}
	pad := 0.01 * U
	c.bar = ovBar{on: true, vertical: true, r: rectF{x0, bottom - h - pad, x0 + barW, bottom + pad}, delay: 0, col: acc}
}

// addPart draws a stack part into sprites (per word when it is typed, a
// counter when its number counts up).
func (c *OverlayClip) addPart(p ovPart, x, top float32, align string) {
	el := &ovElement{delay: p.delay, anim: c.anim}
	switch {
	case c.anim == AnimCount && p.value:
		if n, ok := parseCount(p.tb.text); ok {
			n.fc, n.size, n.x, n.top, n.align, n.fill = p.tb.sh.fc, p.tb.size, x, top, align, p.fill
			el.count = &n
		}
		// The final value marks the space the counter needs.
		el.sprites = []ovSprite{{img: p.tb.sprite(x, top, align, p.fill, nil)}}
	case c.anim == AnimType && p.hero:
		for i, img := range p.tb.wordSprites(x, top, align, p.fill) {
			el.sprites = append(el.sprites, ovSprite{img: img, delay: float64(i) * ovWordStep})
		}
	default:
		if c.anim == AnimCount || c.anim == AnimType {
			el.anim = AnimFade // the other parts of a counting / typed design fade in
		}
		el.sprites = []ovSprite{{img: p.tb.sprite(x, top, align, p.fill, nil)}}
	}
	c.els = append(c.els, el)
}

// finish computes the box every frame fits in and the entrance length.
func (c *OverlayClip) finish() {
	c.scrim = c.drawScrim()
	var box image.Rectangle
	if c.scrim != nil {
		box = c.scrim.Rect
	}
	grow := int(math.Ceil(0.09 * c.unit)) // slide / pop / rise reach
	for _, el := range c.els {
		for _, sp := range el.sprites {
			r := sp.img.Rect
			g := max(grow, int(0.1*float64(max(r.Dx(), r.Dy()))))
			box = box.Union(r.Inset(-g))
		}
		if el.count != nil {
			// A counting number may be wider than the final value ("1 000" vs "999").
			box = box.Union(el.sprites[0].img.Rect.Inset(-grow * 2))
		}
	}
	if c.bar.on {
		box = box.Union(c.bar.r.inset(-2, -2).rect())
	}
	c.box = box.Intersect(image.Rectangle{Max: c.size})
	end := 0.0
	for _, el := range c.els {
		for _, sp := range el.sprites {
			end = max(end, el.delay+sp.delay+entrance(el.anim))
		}
		if el.count != nil {
			end = max(end, el.delay+ovCount)
		}
	}
	if c.bar.on {
		end = max(end, c.bar.delay+ovIn)
	}
	c.pace = 1
	if room := c.hide - c.show - ovOut - 0.6; room < end {
		c.pace = math.Max(0.35, room/end)
	}
	c.inEnd = end * c.pace
}

func entrance(anim string) float64 {
	switch anim {
	case AnimPop:
		return ovPop
	case AnimType:
		return ovWord
	}
	return ovIn
}

// Bounds is where the frames go on the picture (every frame has its size).
func (c *OverlayClip) Bounds() image.Rectangle { return c.box }

// Still reports a frame that does not change around t: id 0 = nothing on
// screen, 1 = the whole design at rest; animating frames report false.
func (c *OverlayClip) Still(t float64) (still bool, id int) {
	switch {
	case t <= c.show || t >= c.hide:
		return true, 0
	case t >= c.show+c.inEnd && t <= c.hide-ovOut:
		return true, 1
	}
	return false, -1
}

// Frame composes the overlay t seconds into the scene.
func (c *OverlayClip) Frame(t float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, c.box.Dx(), c.box.Dy()))
	if t <= c.show || t >= c.hide || c.box.Empty() {
		return img
	}
	local := (t - c.show) / c.pace // entrance clock
	// The exit: everything fades and lifts a little.
	out := 1.0
	lift := 0.0
	if left := c.hide - t; left < ovOut {
		k := easeInCubic(1 - left/ovOut)
		out, lift = 1-k, -k*0.012*c.unit
	}
	if c.scrim != nil {
		c.blit(img, c.scrim, out*easeOutCubic(local/0.35), 0, 0, 1)
	}
	if c.bar.on {
		c.drawBar(img, clamp01f(easeOutCubic((local-c.bar.delay)/ovIn)), out)
	}
	for _, el := range c.els {
		for i, sp := range el.sprites {
			if el.count != nil && i == 0 {
				continue // drawn below, per frame
			}
			p := (local - el.delay - sp.delay) / entrance(el.anim)
			c.drawSprite(img, sp.img, el.anim, p, out, lift)
		}
		if n := el.count; n != nil {
			p := clamp01f((local - el.delay) / ovCount)
			v := n.value * easeOutQuart(p)
			if p >= 1 {
				v = n.value
			}
			spr := n.sprite(v)
			fade := clamp01f((local - el.delay) / 0.25)
			if spr != nil {
				c.blit(img, spr, out*fade, 0, lift+(1-easeOutCubic(fade))*0.02*c.unit, 1)
			}
		}
	}
	return img
}

// drawSprite places one sprite at entrance progress p (0…1, may be outside).
func (c *OverlayClip) drawSprite(dst, src *image.RGBA, anim string, p, out, lift float64) {
	if p <= 0 {
		return
	}
	p = math.Min(p, 1)
	alpha, dx, dy, scale := 1.0, 0.0, 0.0, 1.0
	switch anim {
	case AnimSlide:
		e := easeOutCubic(p)
		alpha, dx = math.Min(1, p*1.6), -(1-e)*0.08*c.unit
	case AnimPop:
		alpha, scale = math.Min(1, p*2.5), 0.55+0.45*easeOutBack(p)
	case AnimType:
		alpha, scale, dy = math.Min(1, p*2), 0.8+0.2*easeOutBack(p), (1-easeOutCubic(p))*0.025*c.unit
	default: // fade (and the quiet parts of counting / typed designs)
		e := easeOutCubic(p)
		alpha, dy = e, (1-e)*0.03*c.unit
	}
	c.blit(dst, src, alpha*out, dx, dy+lift, scale)
}

// blit composites src (frame coordinates) into dst (the box), moved by
// dx, dy and scaled about its centre, at opacity alpha.
func (c *OverlayClip) blit(dst, src *image.RGBA, alpha, dx, dy, scale float64) {
	if alpha <= 0.004 {
		return
	}
	alpha = math.Min(1, alpha)
	mask := image.NewUniform(color.Alpha16{A: uint16(alpha * 0xffff)})
	ox := float64(src.Rect.Min.X-c.box.Min.X) + dx
	oy := float64(src.Rect.Min.Y-c.box.Min.Y) + dy
	if math.Abs(scale-1) < 0.002 && math.Abs(dx-math.Round(dx)) < 0.01 && math.Abs(dy-math.Round(dy)) < 0.01 {
		at := image.Pt(int(math.Round(ox)), int(math.Round(oy)))
		r := image.Rectangle{Min: at, Max: at.Add(src.Rect.Size())}
		draw.DrawMask(dst, r, src, src.Rect.Min, mask, image.Point{}, draw.Over)
		return
	}
	w, h := float64(src.Rect.Dx()), float64(src.Rect.Dy())
	// Source pixel (sx, sy) relative to src.Rect.Min → destination.
	cx, cy := ox+w/2, oy+h/2
	m := f64.Aff3{
		scale, 0, cx - scale*w/2 - scale*float64(src.Rect.Min.X),
		0, scale, cy - scale*h/2 - scale*float64(src.Rect.Min.Y),
	}
	xdraw.ApproxBiLinear.Transform(dst, m, src, src.Rect, xdraw.Over, &xdraw.Options{SrcMask: mask, SrcMaskP: image.Point{}})
}

// drawScrim is a soft dark glow behind the whole design, so the text reads
// on busy and bright pictures alike (nil without text).
func (c *OverlayClip) drawScrim() *image.RGBA {
	var ink image.Rectangle
	for _, el := range c.els {
		for _, sp := range el.sprites {
			ink = ink.Union(sp.img.Rect)
		}
	}
	if ink.Empty() {
		return nil
	}
	if c.bar.on {
		ink = ink.Union(c.bar.r.rect())
	}
	u := float32(c.unit)
	pad, sigma := 0.03*u, 0.045*u
	r := rectF{float32(ink.Min.X) - pad, float32(ink.Min.Y) - pad, float32(ink.Max.X) + pad, float32(ink.Max.Y) + pad}
	region := r.inset(-3*sigma, -3*sigma).rect().Intersect(image.Rectangle{Max: c.size})
	if region.Empty() {
		return nil
	}
	m := fillMask(region, []shape{{path: roundRect(r.x0, r.y0, r.x1, r.y1, 0.08*u), m: identity}})
	img := image.NewRGBA(region)
	paint(img, blur(m, sigma), color.NRGBA{A: 255}, 0.34, 0, 0)
	return img
}

// drawBar draws the accent bar grown to p (0…1).
func (c *OverlayClip) drawBar(dst *image.RGBA, p, out float64) {
	if p <= 0 {
		return
	}
	r := c.bar.r
	if c.bar.vertical {
		r.y0 = r.y1 - float32(p)*(r.y1-r.y0)
	} else {
		cx := (r.x0 + r.x1) / 2
		half := float32(p) * (r.x1 - r.x0) / 2
		r.x0, r.x1 = cx-half, cx+half
	}
	r = r.translate(-float32(c.box.Min.X), -float32(c.box.Min.Y))
	rad := min(r.w(), r.h()) / 2
	m := fillMask(dst.Rect, []shape{{path: roundRect(r.x0, r.y0, r.x1, r.y1, rad), m: identity}})
	paint(dst, m, c.bar.col, float32(out), 0, 0)
}

// ---- text blocks ------------------------------------------------------------------------

// textBlock is text laid out at a size (px) on balanced lines.
type textBlock struct {
	text string
	sh   *shaper
	blk  *block
	size float32
}

// layoutText lays text out at size px, at most maxLines lines; a block wider
// than maxW is set on more lines, then smaller.
func layoutText(fc *face, text string, size, maxW float32, maxLines int, upper bool, track float32) *textBlock {
	p := newPart(fc, cleanText(text, 200, true), track, upper && !fc.def.Script, maxLines, 1.08, 0.08)
	tb := &textBlock{text: text, sh: p.sh, size: size}
	if p.empty() {
		tb.blk = newBlock(p.sh, nil, nil, 1.08, 0.08)
		return tb
	}
	best := p.cands[0]
	for _, cand := range p.cands {
		if cand.w*size <= maxW {
			best = cand
			break
		}
		if cand.w < best.w {
			best = cand
		}
	}
	if best.w*size > maxW && best.w > 0 {
		tb.size = maxW / best.w
	}
	tb.blk = best
	return tb
}

func (t *textBlock) height() float32 { return t.blk.h * t.size }

// placed is one unit of a block on the frame.
type placed struct {
	shapes []shape
	ink    rectF
}

// place puts the block with its ink top at top, aligned on x (the left edge,
// the centre or the right edge of every line).
func (t *textBlock) place(x, top float32, align string) []placed {
	b, s := t.blk, t.size
	var out []placed
	base := top
	for li, l := range b.lines {
		if li == 0 {
			base += l.top * s
		} else {
			base += b.dist[li-1] * s
		}
		var pen float32
		switch align {
		case "left":
			pen = x - l.inkL*s
		case "right":
			pen = x - (l.inkL+l.w)*s
		default:
			pen = x - (l.inkL+l.w/2)*s
		}
		for ui := l.a; ui < l.b; ui++ {
			u := &b.units[ui]
			if ui > l.a {
				pen += t.sh.joiner() * s
			}
			var sh []shape
			for _, sp := range u.spans {
				if !sp.g.empty() {
					sh = append(sh, shape{path: sp.g.path, m: scaleAt(s, pen+sp.x*s, base)})
				}
			}
			if len(sh) > 0 {
				out = append(out, placed{shapes: sh, ink: rectF{pen + u.inkL*s, base - u.top*s, pen + u.inkR*s, base + u.bot*s}})
			}
			pen += u.adv * s
		}
	}
	return out
}

// sprite draws the whole block (or only the units keep accepts).
func (t *textBlock) sprite(x, top float32, align string, fill color.NRGBA, keep func(i int) bool) *image.RGBA {
	var shapes []shape
	var ink rectF
	for i, p := range t.place(x, top, align) {
		if keep != nil && !keep(i) {
			continue
		}
		shapes = append(shapes, p.shapes...)
		ink = ink.union(p.ink)
	}
	return drawText(shapes, ink, t.size, fill)
}

// wordSprites draws every unit of the block on its own (typing).
func (t *textBlock) wordSprites(x, top float32, align string, fill color.NRGBA) []*image.RGBA {
	var out []*image.RGBA
	for _, p := range t.place(x, top, align) {
		out = append(out, drawText(p.shapes, p.ink, t.size, fill))
	}
	return out
}

// drawText paints shapes with a soft drop shadow into a new image whose
// rectangle is the ink plus the shadow's reach (frame coordinates).
func drawText(shapes []shape, ink rectF, size float32, fill color.NRGBA) *image.RGBA {
	blurR := 0.07 * size
	offY := 0.035 * size
	reach := 2*blurR + offY + 3
	r := ink.inset(-reach, -reach).rect()
	img := image.NewRGBA(r)
	if len(shapes) == 0 || r.Empty() {
		return img
	}
	m := fillMask(r, shapes)
	paint(img, blur(m, blurR/2), color.NRGBA{A: 255}, 0.55, 0, int(math.Round(float64(offY))))
	paint(img, blur(m, blurR*1.6), color.NRGBA{A: 255}, 0.22, 0, 0)
	paint(img, m, fill, 1, 0, 0)
	return img
}

// ---- counting numbers -------------------------------------------------------------------

// parseCount reads a key number with its prefix and suffix ("73%", "$4.5
// млрд", "1 200 км", "−12°"); ok is false when there is no number or more
// than one ("2019–2024").
func parseCount(v string) (ovCounter, bool) {
	rs := []rune(strings.TrimSpace(v))
	start, end := -1, -1
	for i, r := range rs {
		if unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
			end = i + 1
		}
	}
	if start < 0 {
		return ovCounter{}, false
	}
	n := ovCounter{prefix: string(rs[:start]), suffix: string(rs[end:]), decimal: ","}
	var groups []string
	var seps []rune
	var cur []rune
	for _, r := range rs[start:end] {
		if unicode.IsDigit(r) {
			cur = append(cur, r)
			continue
		}
		if !strings.ContainsRune(",.   '", r) {
			return ovCounter{}, false
		}
		groups, seps, cur = append(groups, string(cur)), append(seps, r), nil
	}
	groups = append(groups, string(cur))
	frac := ""
	if k := len(seps); k > 0 {
		last, tail := seps[k-1], groups[len(groups)-1]
		// "4.5", "1 200,5": the last mark is a decimal one; "1,200", "1 200 000" group thousands.
		if (last == ',' || last == '.') && (len(tail) != 3 || (k > 1 && seps[0] != last)) {
			frac, n.decimal = tail, string(last)
			groups, seps = groups[:len(groups)-1], seps[:k-1]
		}
		for i, g := range groups {
			if i > 0 && len(g) != 3 || g == "" {
				return ovCounter{}, false
			}
		}
		for _, sp := range seps {
			if sp != seps[0] {
				return ovCounter{}, false
			}
		}
		if len(seps) > 0 {
			n.group = string(seps[0])
		}
	}
	val, err := strconv.ParseFloat(strings.Join(groups, "")+"."+frac+"0", 64)
	if err != nil {
		return ovCounter{}, false
	}
	n.value, n.decimals = val, len(frac)
	if p := n.prefix; strings.HasSuffix(p, "-") {
		n.prefix = strings.TrimSuffix(p, "-") + "−"
	}
	return n, true
}

// format writes v like the original value (grouping, decimals).
func (n *ovCounter) format(v float64) string {
	s := strconv.FormatFloat(v, 'f', n.decimals, 64)
	whole, frac, _ := strings.Cut(s, ".")
	if n.group != "" && len(whole) > 3 {
		var b strings.Builder
		for i, r := range whole {
			if i > 0 && (len(whole)-i)%3 == 0 {
				b.WriteString(n.group)
			}
			b.WriteRune(r)
		}
		whole = b.String()
	}
	if frac != "" {
		whole += n.decimal + frac
	}
	return n.prefix + whole + n.suffix
}

// sprite draws the counter at v.
func (n *ovCounter) sprite(v float64) *image.RGBA {
	tb := &textBlock{text: n.format(v), sh: newShaper(n.fc, 0, false), size: n.size}
	units := tb.sh.shapeText(tb.text)
	if len(units) == 0 {
		return nil
	}
	tb.blk = newBlock(tb.sh, units, [][2]int{{0, len(units)}}, 1.08, 0.08)
	return tb.sprite(n.x, n.top, n.align, n.fill, nil)
}

// ---- easing -----------------------------------------------------------------------------

func clamp01f(x float64) float64 { return math.Max(0, math.Min(1, x)) }

func easeOutCubic(x float64) float64 {
	x = clamp01f(x)
	return 1 - math.Pow(1-x, 3)
}

func easeInCubic(x float64) float64 {
	x = clamp01f(x)
	return x * x * x
}

func easeOutQuart(x float64) float64 {
	x = clamp01f(x)
	return 1 - math.Pow(1-x, 4)
}

// easeOutBack overshoots a little before it settles (pop).
func easeOutBack(x float64) float64 {
	x = clamp01f(x)
	const c1 = 1.70158
	const c3 = c1 + 1
	return 1 + c3*math.Pow(x-1, 3) + c1*math.Pow(x-1, 2)
}
