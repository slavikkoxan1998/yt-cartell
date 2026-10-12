package montage

// Small helpers shared by the cards of the step: formatting, geometry,
// image drawing and a few local widgets the kit does not have (a slider
// that commits after the user stops, colour swatches, a ±-stepper, custom
// pressable areas).

import (
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"

	"ytcartell/internal/gui"
)

type (
	C = layout.Context
	D = layout.Dimensions
)

// ---- formatting --------------------------------------------------------------------------

// decimal formats v with prec decimals and a Ukrainian decimal comma.
func decimal(v float64, prec int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		v = 0
	}
	return strings.Replace(strconv.FormatFloat(v, 'f', prec, 64), ".", ",", 1)
}

// secText is a short duration in seconds: "8 с", "6,5 с".
func secText(s float64) string {
	if s < 0 || math.IsNaN(s) {
		s = 0
	}
	v := math.Round(s*10) / 10
	if v == math.Trunc(v) {
		return strconv.Itoa(int(v)) + " с"
	}
	return decimal(v, 1) + " с"
}

// pctText is a 0…1 level as a percentage: "18%".
func pctText(v float64) string { return strconv.Itoa(int(math.Round(v*100))) + "%" }

// bytesText is a file size: "850 КБ", "12,3 МБ", "1,2 ГБ".
func bytesText(n int64) string {
	switch {
	case n <= 0:
		return ""
	case n < 1<<20:
		return strconv.Itoa(int(math.Max(1, math.Round(float64(n)/1024)))) + " КБ"
	case n < 1<<30:
		return decimal(float64(n)/(1<<20), 1) + " МБ"
	}
	return decimal(float64(n)/(1<<30), 2) + " ГБ"
}

// count is "3 сцени" with the right plural.
func count(n int, one, few, many string) string {
	return fmt.Sprintf("%d %s", n, gui.PluralUK(n, one, few, many))
}

// ---- geometry ------------------------------------------------------------------------------

// ratioOf is the width/height of a project format.
func ratioOf(aspect string) float32 {
	if strings.TrimSpace(aspect) == "9:16" {
		return 9.0 / 16
	}
	return 16.0 / 9
}

func pt(x, y int) image.Point { return image.Point{X: x, Y: y} }

func clampf(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func clamp32(v, lo, hi float32) float32 { return max(lo, min(hi, v)) }

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// pxToDp converts pixels to dp for the kit's unit.Dp parameters.
func pxToDp(gtx C, px int) unit.Dp { return unit.Dp(float32(px) / gtx.Metric.PxPerDp) }

// hashOf is a stable 64-bit hash of the given strings (cache versions).
func hashOf(parts ...string) int64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return int64(h.Sum64() & (1<<62 - 1))
}

// ---- drawing -------------------------------------------------------------------------------

// drawImage paints img into a box of size at the origin — scaled to cover it
// (cropping the overflow) or to fit inside it — centred and clipped to the box.
func drawImage(gtx C, img paint.ImageOp, size image.Point, cover bool) {
	is := img.Size()
	if is.X == 0 || is.Y == 0 || size.X <= 0 || size.Y <= 0 {
		return
	}
	sx := float32(size.X) / float32(is.X)
	sy := float32(size.Y) / float32(is.Y)
	s := max(sx, sy)
	if !cover {
		s = min(sx, sy)
	}
	dw, dh := float32(is.X)*s, float32(is.Y)*s
	off := f32.Pt((float32(size.X)-dw)/2, (float32(size.Y)-dh)/2)
	defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
	defer op.Affine(f32.AffineId().Scale(f32.Point{}, f32.Pt(s, s)).Offset(off)).Push(gtx.Ops).Pop()
	img.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// coverAffine is the transform that scales an image of size is to cover a box of size.
func coverAffine(is, size image.Point) f32.Affine2D {
	if is.X == 0 || is.Y == 0 {
		return f32.AffineId()
	}
	s := max(float32(size.X)/float32(is.X), float32(size.Y)/float32(is.Y))
	dw, dh := float32(is.X)*s, float32(is.Y)*s
	return f32.AffineId().Scale(f32.Point{}, f32.Pt(s, s)).Offset(f32.Pt((float32(size.X)-dw)/2, (float32(size.Y)-dh)/2))
}

// fillRR fills a rounded rectangle (radius in px).
func fillRR(gtx C, r image.Rectangle, rad int, c color.NRGBA) {
	if c.A == 0 || r.Empty() {
		return
	}
	paint.FillShape(gtx.Ops, c, clip.UniformRRect(r, rad).Op(gtx.Ops))
}

// strokeRR strokes a rounded rectangle border of width w px inside r.
func strokeRR(gtx C, r image.Rectangle, rad int, w float32, c color.NRGBA) {
	if c.A == 0 || r.Empty() || w <= 0 {
		return
	}
	paint.FillShape(gtx.Ops, c, clip.Stroke{Path: rrPathF(gtx.Ops, r, float32(rad), w), Width: w}.Op())
}

// rrPathF is a rounded-rectangle path inset by half the stroke width.
func rrPathF(ops *op.Ops, r image.Rectangle, rad, w float32) clip.PathSpec {
	x0, y0 := float32(r.Min.X)+w/2, float32(r.Min.Y)+w/2
	x1, y1 := float32(r.Max.X)-w/2, float32(r.Max.Y)-w/2
	rad = max(0, min(rad-w/2, min(x1-x0, y1-y0)/2))
	const k = 0.5522847
	var p clip.Path
	p.Begin(ops)
	p.MoveTo(f32.Pt(x0+rad, y0))
	p.LineTo(f32.Pt(x1-rad, y0))
	p.CubeTo(f32.Pt(x1-rad+rad*k, y0), f32.Pt(x1, y0+rad-rad*k), f32.Pt(x1, y0+rad))
	p.LineTo(f32.Pt(x1, y1-rad))
	p.CubeTo(f32.Pt(x1, y1-rad+rad*k), f32.Pt(x1-rad+rad*k, y1), f32.Pt(x1-rad, y1))
	p.LineTo(f32.Pt(x0+rad, y1))
	p.CubeTo(f32.Pt(x0+rad-rad*k, y1), f32.Pt(x0, y1-rad+rad*k), f32.Pt(x0, y1-rad))
	p.LineTo(f32.Pt(x0, y0+rad))
	p.CubeTo(f32.Pt(x0, y0+rad-rad*k), f32.Pt(x0+rad-rad*k, y0), f32.Pt(x0+rad, y0))
	p.Close()
	return p.End()
}

// fillCircle fills a circle of radius r centred at (cx, cy).
func fillCircle(gtx C, cx, cy, r float32, c color.NRGBA) {
	if c.A == 0 || r <= 0 {
		return
	}
	b := image.Rect(int(cx-r), int(cy-r), int(math.Ceil(float64(cx+r))), int(math.Ceil(float64(cy+r))))
	paint.FillShape(gtx.Ops, c, clip.Ellipse(b).Op(gtx.Ops))
}

// lineV draws a vertical line of width w px.
func lineV(gtx C, x, y0, y1, w int, c color.NRGBA) {
	gui.FillRect(gtx, image.Rect(x-w/2, y0, x-w/2+max(1, w), y1), c)
}

// textAt draws s at (x, y) in style st, colour col, at most maxW px wide
// (one line), returning its size.
func textAt(gtx C, th *gui.Theme, x, y, maxW int, st gui.TextStyle, col color.NRGBA, s string) image.Point {
	g := gtx
	g.Constraints = layout.Constraints{Max: pt(max(0, maxW), 1<<16)}
	m := op.Record(gtx.Ops)
	d := gui.Text{Style: st, Color: col, MaxLines: 1}.Layout(g, th, s)
	call := m.Stop()
	defer op.Offset(pt(x, y)).Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
	return d.Size
}

// measure lays w out into a macro, returning its size and the call.
func measure(gtx C, w layout.Widget) (D, op.CallOp) {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	return d, m.Stop()
}

// pill draws a small dark translucent label (on pictures): "8 с", "3".
func pill(gtx C, th *gui.Theme, x, y int, s string) image.Point {
	st := th.Caption.Weighted(gui.FontSemiBold)
	g := gtx
	g.Constraints = layout.Constraints{Max: pt(1<<16, 1<<16)}
	d, call := measure(g, gui.Text{Style: st, Color: color.NRGBA{R: 255, G: 255, B: 255, A: 255}, MaxLines: 1}.Widget(th, s))
	padX, padY := gtx.Dp(6), gtx.Dp(1)
	r := image.Rect(x, y, x+d.Size.X+2*padX, y+d.Size.Y+2*padY)
	fillRR(gtx, r, r.Dy()/2, color.NRGBA{R: 20, G: 16, B: 12, A: 178})
	o := op.Offset(pt(x+padX, y+padY)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return r.Size()
}

// opOffset pushes a translation (Pop it).
func opOffset(gtx C, x, y int) op.TransformStack { return op.Offset(pt(x, y)).Push(gtx.Ops) }

// pxToSp converts a pixel size to sp for text styles.
func pxToSp(gtx C, px float32) unit.Sp { return unit.Sp(px / gtx.Metric.PxPerSp) }

// dashRR draws a dashed 1 dp border inside r with rounded corners.
func dashRR(gtx C, r image.Rectangle, rad int, c color.NRGBA) {
	w := max(1, gtx.Dp(1))
	dash, gap := gtx.Dp(6), gtx.Dp(5)
	rad = min(rad, min(r.Dx(), r.Dy())/2)
	for x := r.Min.X + rad; x < r.Max.X-rad; x += dash + gap {
		x1 := min(x+dash, r.Max.X-rad)
		gui.FillRect(gtx, image.Rect(x, r.Min.Y, x1, r.Min.Y+w), c)
		gui.FillRect(gtx, image.Rect(x, r.Max.Y-w, x1, r.Max.Y), c)
	}
	for y := r.Min.Y + rad; y < r.Max.Y-rad; y += dash + gap {
		y1 := min(y+dash, r.Max.Y-rad)
		gui.FillRect(gtx, image.Rect(r.Min.X, y, r.Min.X+w, y1), c)
		gui.FillRect(gtx, image.Rect(r.Max.X-w, y, r.Max.X, y1), c)
	}
	// Solid quarter arcs at the corners.
	const k = 0.5522847
	fw := float32(w)
	R := float32(rad) - fw/2
	x0, y0 := float32(r.Min.X)+fw/2, float32(r.Min.Y)+fw/2
	x1, y1 := float32(r.Max.X)-fw/2, float32(r.Max.Y)-fw/2
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(x0, y0+R))
	p.CubeTo(f32.Pt(x0, y0+R-R*k), f32.Pt(x0+R-R*k, y0), f32.Pt(x0+R, y0))
	p.MoveTo(f32.Pt(x1-R, y0))
	p.CubeTo(f32.Pt(x1-R+R*k, y0), f32.Pt(x1, y0+R-R*k), f32.Pt(x1, y0+R))
	p.MoveTo(f32.Pt(x1, y1-R))
	p.CubeTo(f32.Pt(x1, y1-R+R*k), f32.Pt(x1-R+R*k, y1), f32.Pt(x1-R, y1))
	p.MoveTo(f32.Pt(x0+R, y1))
	p.CubeTo(f32.Pt(x0+R-R*k, y1), f32.Pt(x0, y1-R+R*k), f32.Pt(x0, y1-R))
	paint.FillShape(gtx.Ops, c, clip.Stroke{Path: p.End(), Width: fw}.Op())
}

// ---- colour swatches -------------------------------------------------------------------------

// swatch is one colour choice.
type swatch struct {
	Value   string      // stored value: "#RRGGBB", "" (the default), "none"
	Color   color.NRGBA // zero = parsed from Value
	Tip     string
	Default bool // the template's / style's own colour (a small reset mark)
	None    bool // "no outline" (a crossed circle)
}

// swatchRow is a row of round colour swatches.
type swatchRow struct {
	clicks []gui.Clickable
	pick   string
	picked bool
}

// Changed reports a picked value (once).
func (r *swatchRow) Changed() (string, bool) {
	v, ok := r.pick, r.picked
	r.pick, r.picked = "", false
	return v, ok
}

// Layout draws the swatches (wrapping) with value selected.
func (r *swatchRow) Layout(gtx C, th *gui.Theme, items []swatch, value string) D {
	if len(r.clicks) < len(items) {
		r.clicks = append(r.clicks, make([]gui.Clickable, len(items)-len(r.clicks))...)
	}
	ws := make([]layout.Widget, len(items))
	for i := range items {
		it := items[i]
		cl := &r.clicks[i]
		ws[i] = func(gtx C) D {
			if cl.Clicked(gtx) && gtx.Enabled() {
				r.pick, r.picked = it.Value, true
				gtx.Execute(op.InvalidateCmd{})
			}
			sel := strings.EqualFold(it.Value, value)
			d := gtx.Dp(30)
			ring := gtx.Dp(3)
			size := d + 2*ring
			cx, cy := float32(size)/2, float32(size)/2
			hov := cl.Hovered() && gtx.Enabled()
			if sel {
				fillCircle(gtx, cx, cy, float32(size)/2, th.Pal.Action)
				fillCircle(gtx, cx, cy, float32(size)/2-float32(gtx.Dp(2)), th.Pal.Card)
			} else if hov {
				fillCircle(gtx, cx, cy, float32(size)/2, th.Pal.BorderStrong)
				fillCircle(gtx, cx, cy, float32(size)/2-float32(gtx.Dp(1.5)), th.Pal.Card)
			}
			col := it.Color
			if col.A == 0 && !it.None {
				col = parseHex(it.Value)
			}
			rr := float32(d) / 2
			switch {
			case it.None:
				fillCircle(gtx, cx, cy, rr, th.Pal.Surface)
				var p clip.Path
				p.Begin(gtx.Ops)
				o := rr * 0.62
				p.MoveTo(f32.Pt(cx-o, cy+o))
				p.LineTo(f32.Pt(cx+o, cy-o))
				paint.FillShape(gtx.Ops, th.Pal.ErrorInk, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
			default:
				fillCircle(gtx, cx, cy, rr, col)
			}
			// Hairline so white and near-card colours stay visible.
			var bp clip.Path
			bp.Begin(gtx.Ops)
			bp.MoveTo(f32.Pt(cx+rr-0.5, cy))
			bp.ArcTo(f32.Pt(cx, cy), f32.Pt(cx, cy), 2*math.Pi)
			paint.FillShape(gtx.Ops, gui.WithAlpha(th.Pal.Text, 0.2), clip.Stroke{Path: bp.End(), Width: float32(max(1, gtx.Dp(1)))}.Op())
			if it.Default {
				is := gtx.Dp(14)
				ic := gui.OnColor(col)
				o := op.Offset(pt((size-is)/2, (size-is)/2)).Push(gtx.Ops)
				gui.IconRotateCcw.LayoutStroke(gtx, 14, 2, ic)
				o.Pop()
			}
			if it.Tip != "" && hov {
				th.Tooltip(gtx, cl, cl.Pressed(), it.Tip)
			}
			pressArea(gtx, cl, pt(size, size))
			return D{Size: pt(size, size)}
		}
	}
	return gui.Flow{Gap: gui.Sp4, RowGap: gui.Sp4}.Layout(gtx, ws...)
}

// ---- pressable areas -----------------------------------------------------------------------

// pressArea registers c's input over a size-sized box with the hand cursor.
// Read c.Clicked(gtx) before calling it (the area drains unread clicks).
func pressArea(gtx C, c *gui.Clickable, size image.Point) {
	g := gtx
	g.Constraints = layout.Exact(size)
	c.Clickable.Layout(g, func(gtx C) D {
		defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
		if gtx.Enabled() {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		return D{Size: size}
	})
}

// ---- a slider that commits when the user pauses -----------------------------------------------

// commitDelay is how long a slider must rest before its value is stored:
// dragging never floods the engine with edits.
const commitDelay = 300 * time.Millisecond

// valSlider wraps gui.Slider for a model value: it shows the value being
// dragged and reports it once (Commit) after the user stopped moving.
type valSlider struct {
	s       gui.Slider
	local   float32
	dirty   bool // showing local instead of the model value
	pending bool // local not reported yet
	last    time.Time
	since   time.Time
}

// Commit reports a value to store (once per pause). Call it every frame
// before Layout.
func (v *valSlider) Commit(gtx C) (float32, bool) {
	if val, ok := v.s.Changed(gtx); ok {
		v.local, v.dirty, v.pending, v.last = val, true, true, gtx.Now
	}
	if !v.pending {
		return 0, false
	}
	if wait := commitDelay - gtx.Now.Sub(v.last); wait > 0 {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(wait)})
		return 0, false
	}
	v.pending, v.since = false, gtx.Now
	return v.local, true
}

// Layout draws the slider for the model value st.Value.
func (v *valSlider) Layout(gtx C, th *gui.Theme, st gui.SliderStyle) D {
	if v.dirty {
		// The stored value may come back rounded: "caught up" within ~1 % of the range.
		tol := max(1e-4, (st.Max-st.Min)*0.011)
		caught := abs32(st.Value-v.local) <= tol
		if !v.pending && (caught || gtx.Now.Sub(v.since) > time.Second) {
			v.dirty = false
		} else {
			st.Value = v.local
		}
	}
	return v.s.Layout(gtx, th, st)
}

// Value is what the slider shows for the model value (the dragged value while dirty).
func (v *valSlider) Value(model float32) float32 {
	if v.dirty {
		return v.local
	}
	return model
}

// ---- labelled rows ---------------------------------------------------------------------------

// fieldLabel stacks a label (and a value on the right) over w.
func fieldLabel(gtx C, th *gui.Theme, label, value string, w layout.Widget) D {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Flexed(1, gui.Text{Style: th.Label, MaxLines: 1}.Widget(th, label)),
				layout.Rigid(func(gtx C) D {
					if value == "" {
						return D{}
					}
					return layout.Inset{Left: gui.Sp8}.Layout(gtx, gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: th.Pal.Text2, MaxLines: 1}.Widget(th, value))
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
		layout.Rigid(w),
	)
}

// hint is a small secondary line of text.
func hint(th *gui.Theme, s string) layout.Widget {
	return gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, s)
}

// cardHeader is the title row of a card: an icon in a soft square, the
// title, a one-line description and an optional trailing widget.
func cardHeader(gtx C, th *gui.Theme, icon *gui.Icon, title, sub string, trailing layout.Widget) D {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			if icon == nil {
				return D{}
			}
			s := gtx.Dp(40)
			fillRR(gtx, image.Rectangle{Max: pt(s, s)}, gtx.Dp(12), th.Pal.Surface)
			is := gtx.Dp(20)
			o := op.Offset(pt((s-is)/2, (s-is)/2)).Push(gtx.Ops)
			icon.Layout(gtx, 20, th.Pal.Text)
			o.Pop()
			return layout.Inset{Right: gui.Sp12}.Layout(gtx, func(gtx C) D { return D{Size: pt(s, s)} })
		}),
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, title)),
				layout.Rigid(func(gtx C) D {
					if sub == "" {
						return D{}
					}
					return gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 3}.Layout(gtx, th, sub)
				}),
			)
		}),
		layout.Rigid(func(gtx C) D {
			if trailing == nil {
				return D{}
			}
			return layout.Inset{Left: gui.Sp12}.Layout(gtx, trailing)
		}),
	)
}

// infoRow is a soft tinted note with an icon (hints, warnings) across the width.
func infoRow(gtx C, th *gui.Theme, icon *gui.Icon, tone gui.Tone, s string) D {
	_, soft, ink := th.Pal.ToneColors(tone)
	if tone == gui.ToneNeutral {
		soft, ink = th.Pal.Surface, th.Pal.Text2
	}
	w := gtx.Constraints.Max.X
	pad := gtx.Dp(12)
	g := gtx
	g.Constraints = layout.Constraints{Max: pt(max(0, w-2*pad), 1<<16)}
	d, call := measure(g, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: 1, Right: gui.Sp8}.Layout(gtx, icon.Widget(18, ink))
			}),
			layout.Flexed(1, gui.Text{Style: th.Small, Color: ink}.Widget(th, s)),
		)
	})
	size := pt(w, d.Size.Y+2*pad)
	fillRR(gtx, image.Rectangle{Max: size}, gtx.Dp(gui.RadiusControl), soft)
	o := op.Offset(pt(pad, pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: size}
}

// centerText draws s centred in a box of width w.
func centerText(gtx C, th *gui.Theme, st gui.TextStyle, col color.NRGBA, s string) D {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return gui.Text{Style: st, Color: col, Align: text.Middle}.Layout(gtx, th, s)
}

// chipW adapts a static chip (no state) to a layout.Widget.
func chipW(th *gui.Theme, ch gui.Chip) layout.Widget {
	return func(gtx C) D { return ch.Layout(gtx, th, nil) }
}

// secDur converts seconds to a time.Duration.
func secDur(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }
