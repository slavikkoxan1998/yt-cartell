package gui

import (
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// Low-level drawing helpers (pixel coordinates unless a parameter is unit.Dp).

// C and D shorten widget signatures in the kit and in screens.
type (
	C = layout.Context
	D = layout.Dimensions
)

func pt(x, y int) image.Point { return image.Point{X: x, Y: y} }

func rectWH(w, h int) image.Rectangle { return image.Rectangle{Max: pt(w, h)} }

// fRect is a float rectangle.
type fRect struct{ Min, Max f32.Point }

func (r fRect) Dx() float32 { return r.Max.X - r.Min.X }
func (r fRect) Dy() float32 { return r.Max.Y - r.Min.Y }

func frect(r image.Rectangle) fRect { return fRect{Min: layout.FPt(r.Min), Max: layout.FPt(r.Max)} }

func (r fRect) inset(d float32) fRect {
	return fRect{Min: f32.Pt(r.Min.X+d, r.Min.Y+d), Max: f32.Pt(r.Max.X-d, r.Max.Y-d)}
}

// rrPath builds a rounded rectangle path with per-corner radii (clamped to
// half the shorter side).
func rrPath(ops *op.Ops, r fRect, nw, ne, se, sw float32) clip.PathSpec {
	w, h := r.Dx(), r.Dy()
	lim := max(0, min(w, h)/2)
	nw, ne, se, sw = min(max(nw, 0), lim), min(max(ne, 0), lim), min(max(se, 0), lim), min(max(sw, 0), lim)
	const k = 0.5522847 // cubic circle constant
	var p clip.Path
	p.Begin(ops)
	x0, y0, x1, y1 := r.Min.X, r.Min.Y, r.Max.X, r.Max.Y
	p.MoveTo(f32.Pt(x0+nw, y0))
	p.LineTo(f32.Pt(x1-ne, y0))
	p.CubeTo(f32.Pt(x1-ne+ne*k, y0), f32.Pt(x1, y0+ne-ne*k), f32.Pt(x1, y0+ne))
	p.LineTo(f32.Pt(x1, y1-se))
	p.CubeTo(f32.Pt(x1, y1-se+se*k), f32.Pt(x1-se+se*k, y1), f32.Pt(x1-se, y1))
	p.LineTo(f32.Pt(x0+sw, y1))
	p.CubeTo(f32.Pt(x0+sw-sw*k, y1), f32.Pt(x0, y1-sw+sw*k), f32.Pt(x0, y1-sw))
	p.LineTo(f32.Pt(x0, y0+nw))
	p.CubeTo(f32.Pt(x0, y0+nw-nw*k), f32.Pt(x0+nw-nw*k, y0), f32.Pt(x0+nw, y0))
	p.Close()
	return p.End()
}

func fillRRf(ops *op.Ops, r fRect, rad float32, c color.NRGBA) {
	if c.A == 0 || r.Dx() <= 0 || r.Dy() <= 0 {
		return
	}
	paint.FillShape(ops, c, clip.Outline{Path: rrPath(ops, r, rad, rad, rad, rad)}.Op())
}

// strokeRRf strokes a border of width w lying fully inside r.
func strokeRRf(ops *op.Ops, r fRect, rad, w float32, c color.NRGBA) {
	if c.A == 0 || w <= 0 || r.Dx() <= w || r.Dy() <= w {
		return
	}
	h := w / 2
	ir := r.inset(h)
	rr := max(0, rad-h)
	paint.FillShape(ops, c, clip.Stroke{Path: rrPath(ops, ir, rr, rr, rr, rr), Width: w}.Op())
}

// FillRect paints r with c.
func FillRect(gtx C, r image.Rectangle, c color.NRGBA) {
	if c.A == 0 || r.Empty() {
		return
	}
	paint.FillShape(gtx.Ops, c, clip.Rect(r).Op())
}

// FillRRect paints a rounded rectangle.
func FillRRect(gtx C, r image.Rectangle, radius unit.Dp, c color.NRGBA) {
	fillRRf(gtx.Ops, frect(r), float32(gtx.Dp(radius)), c)
}

// Box paints a rounded rectangle with a fill and an optional 1 dp inner
// border (zero-alpha colours are skipped).
func Box(gtx C, r image.Rectangle, radius unit.Dp, fill, border color.NRGBA) {
	rad := float32(gtx.Dp(radius))
	fillRRf(gtx.Ops, frect(r), rad, fill)
	if border.A > 0 {
		strokeRRf(gtx.Ops, frect(r), rad, float32(max(1, gtx.Dp(1))), border)
	}
}

// Border strokes a width-dp border inside r.
func Border(gtx C, r image.Rectangle, radius, width unit.Dp, c color.NRGBA) {
	strokeRRf(gtx.Ops, frect(r), float32(gtx.Dp(radius)), max(1, float32(gtx.Metric.PxPerDp)*float32(width)), c)
}

// ringf strokes an outline of width w at gap outside r.
func ringf(ops *op.Ops, r fRect, rad, gap, w float32, c color.NRGBA) {
	if c.A == 0 || w <= 0 {
		return
	}
	o := gap + w/2
	fr := r.inset(-o)
	rr := rad + o
	paint.FillShape(ops, c, clip.Stroke{Path: rrPath(ops, fr, rr, rr, rr, rr), Width: w}.Op())
}

// FocusRing draws the kit's focus ring (2 dp at 2 dp outside r) in th.Pal.Focus
// scaled by alpha (the animated focus amount, 0…1).
func FocusRing(gtx C, th *Theme, r image.Rectangle, radius unit.Dp, alpha float32) {
	if alpha <= 0.01 {
		return
	}
	ringf(gtx.Ops, frect(r), float32(gtx.Dp(radius)), float32(gtx.Dp(2)), float32(gtx.Dp(2)), WithAlpha(th.Pal.Focus, alpha))
}

// fillCircle fills a circle of radius rad centred at c.
func fillCircle(ops *op.Ops, c f32.Point, rad float32, col color.NRGBA) {
	if col.A == 0 || rad <= 0 {
		return
	}
	var p clip.Path
	p.Begin(ops)
	circlePath(&p, c, rad)
	paint.FillShape(ops, col, clip.Outline{Path: p.End()}.Op())
}

// FillCircle fills a circle of diameter size (dp) at the origin.
func FillCircle(gtx C, size unit.Dp, c color.NRGBA) D {
	px := gtx.Dp(size)
	fillCircle(gtx.Ops, f32.Pt(float32(px)/2, float32(px)/2), float32(px)/2, c)
	return D{Size: pt(px, px)}
}

// circlePath appends a closed circle made of four cubic arcs.
func circlePath(p *clip.Path, c f32.Point, r float32) {
	const k = 0.5522847
	p.MoveTo(f32.Pt(c.X+r, c.Y))
	p.CubeTo(f32.Pt(c.X+r, c.Y+r*k), f32.Pt(c.X+r*k, c.Y+r), f32.Pt(c.X, c.Y+r))
	p.CubeTo(f32.Pt(c.X-r*k, c.Y+r), f32.Pt(c.X-r, c.Y+r*k), f32.Pt(c.X-r, c.Y))
	p.CubeTo(f32.Pt(c.X-r, c.Y-r*k), f32.Pt(c.X-r*k, c.Y-r), f32.Pt(c.X, c.Y-r))
	p.CubeTo(f32.Pt(c.X+r*k, c.Y-r), f32.Pt(c.X+r, c.Y-r*k), f32.Pt(c.X+r, c.Y))
	p.Close()
}

// strokeArc strokes a circular arc (round caps) of radius r around c,
// starting at angle a0 (radians, 0 = 3 o'clock, clockwise positive on
// screen) and sweeping sweep radians clockwise.
func strokeArc(ops *op.Ops, c f32.Point, r, a0, sweep, width float32, col color.NRGBA) {
	if col.A == 0 || sweep <= 0 || r <= 0 {
		return
	}
	sweep = min(sweep, 2*math.Pi-1e-4)
	var p clip.Path
	p.Begin(ops)
	p.MoveTo(f32.Pt(c.X+r*cos32(a0), c.Y+r*sin32(a0)))
	// Split into ≤ 90° cubic segments (accurate to ~0.03 % of r).
	n := int(math.Ceil(float64(sweep / (math.Pi / 2))))
	step := sweep / float32(n)
	k := 4.0 / 3.0 * float32(math.Tan(float64(step)/4))
	a := a0
	for i := 0; i < n; i++ {
		b := a + step
		p1 := f32.Pt(c.X+r*cos32(a), c.Y+r*sin32(a))
		p4 := f32.Pt(c.X+r*cos32(b), c.Y+r*sin32(b))
		p2 := f32.Pt(p1.X-k*r*sin32(a), p1.Y+k*r*cos32(a))
		p3 := f32.Pt(p4.X+k*r*sin32(b), p4.Y-k*r*cos32(b))
		p.CubeTo(p2, p3, p4)
		a = b
	}
	paint.FillShape(ops, col, clip.Stroke{Path: p.End(), Width: width}.Op())
}

func cos32(a float32) float32 { return float32(math.Cos(float64(a))) }
func sin32(a float32) float32 { return float32(math.Sin(float64(a))) }

// ClipRRect pushes a rounded-rectangle clip of size sz; Pop the result.
func ClipRRect(gtx C, sz image.Point, radius unit.Dp) clip.Stack {
	return clip.UniformRRect(rectWH(sz.X, sz.Y), gtx.Dp(radius)).Push(gtx.Ops)
}

// record lays w out into a macro and returns its dimensions and call.
func record(gtx C, w layout.Widget) (D, op.CallOp) {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	return d, m.Stop()
}

// ---- Elevation -------------------------------------------------------------

// ShadowLayer is one CSS-style box-shadow: offset Y, Blur radius, Spread (dp)
// and Alpha as the design specifies it (sRGB blending, like CSS).
type ShadowLayer struct {
	Y, Blur, Spread unit.Dp
	Alpha           float32
}

// Elevation is a stack of shadow layers drawn under a surface.
type Elevation [2]ShadowLayer

// Elevations (GUI.md): cards rest on two soft layers, lift on hover, and
// sheets/dialogs float high.
var (
	ElevNone  = Elevation{}
	ElevCard  = Elevation{{Y: 2, Blur: 10, Alpha: 0.06}, {Y: 12, Blur: 28, Alpha: 0.05}}
	ElevHover = Elevation{{Y: 3, Blur: 10, Alpha: 0.06}, {Y: 16, Blur: 32, Alpha: 0.09}}
	ElevPop   = Elevation{{Y: 2, Blur: 6, Alpha: 0.06}, {Y: 14, Blur: 36, Alpha: 0.14}}
	ElevSheet = Elevation{{Y: 2, Blur: 8, Alpha: 0.06}, {Y: 24, Blur: 60, Alpha: 0.18}}
	// ElevControl is the tiny lift of knobs, thumbs and tab indicators.
	ElevControl = Elevation{{Y: 1, Blur: 3, Alpha: 0.10}, {Y: 2, Blur: 8, Alpha: 0.06}}
)

// LerpElevation blends two elevations (hover lift animations).
func LerpElevation(a, b Elevation, t float32) Elevation {
	var e Elevation
	l := func(x, y unit.Dp) unit.Dp { return x + (y-x)*unit.Dp(t) }
	for i := range e {
		e[i] = ShadowLayer{
			Y:      l(a[i].Y, b[i].Y),
			Blur:   l(a[i].Blur, b[i].Blur),
			Spread: l(a[i].Spread, b[i].Spread),
			Alpha:  a[i].Alpha + (b[i].Alpha-a[i].Alpha)*t,
		}
	}
	return e
}

// gaussQ is the upper tail of the standard normal distribution, 1 − Φ(x).
func gaussQ(x float32) float32 { return float32(0.5 * math.Erfc(float64(x)/math.Sqrt2)) }

// linearAlpha converts an sRGB-space overlay alpha a (fg over bg) into the
// alpha that gives the same visible result under linear-light blending.
func linearAlpha(bg, fg color.NRGBA, a float32) float32 {
	if a <= 0 {
		return 0
	}
	b, f := luma(bg), luma(fg)
	want := b*(1-a) + f*a
	bl, fl, wl := srgbToLinear(b), srgbToLinear(f), srgbToLinear(want)
	if math.Abs(float64(bl-fl)) < 1e-4 {
		return a
	}
	return min(1, max(0, (bl-wl)/(bl-fl)))
}

func srgbToLinear(v float32) float32 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return float32(math.Pow(float64((v+0.055)/1.055), 2.4))
}

// fillPath fills a path outline with c.
func fillPath(gtx C, spec clip.PathSpec, c color.NRGBA) {
	paint.FillShape(gtx.Ops, c, clip.Outline{Path: spec}.Op())
}
