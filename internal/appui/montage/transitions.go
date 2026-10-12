package montage

// Transitions: the gallery card (the project's default transition and its
// duration), the preview drawing shared with the monitor and the per-scene
// popover, and the gallery tile used by every catalog of the step.

import (
	"image"
	"image/color"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

// transitionIcon is a small symbol for a transition (timeline chips).
func transitionIcon(t media.Transition) *gui.Icon {
	switch t.ID {
	case "none":
		return gui.IconScissors
	case "fade":
		return gui.IconMoon
	case "fadewhite":
		return gui.IconSun
	case "slideleft":
		return gui.IconArrowLeft
	case "slideright":
		return gui.IconArrowRight
	case "slideup":
		return gui.IconArrowUp
	case "slidedown":
		return gui.IconArrowDown
	case "wipeleft", "wiperight", "smoothleft", "smoothright":
		return gui.IconColumns2
	case "wipeup", "wipedown":
		return gui.IconRows2
	case "circleopen", "circleclose":
		return gui.IconCircleDot
	case "radial":
		return gui.IconClock
	case "pixelize":
		return gui.IconGrid3x3
	case "zoomin":
		return gui.IconZoomIn
	case "squeezeh", "squeezev":
		return gui.IconMinimize2
	}
	return gui.IconBlend
}

// ---- preview drawing -----------------------------------------------------------------------------

// transPics are the pictures a transition is drawn between.
type transPics struct {
	a, b         paint.ImageOp
	tinyA, tinyB paint.ImageOp // a few pixels wide (pixelize)
	drawA, drawB func(gtx C, size image.Point)
}

func (tp transPics) paintA(gtx C, size image.Point) {
	if tp.drawA != nil {
		tp.drawA(gtx, size)
		return
	}
	drawImage(gtx, tp.a, size, true)
}

func (tp transPics) paintB(gtx C, size image.Point) {
	if tp.drawB != nil {
		tp.drawB(gtx, size)
		return
	}
	drawImage(gtx, tp.b, size, true)
}

// drawTransition paints the cut from a to b at progress p (0 = all a, 1 =
// all b) in a box of size, approximating how the montage renders it.
func drawTransition(gtx C, tr media.Transition, pics transPics, p float32, size image.Point) {
	split := tr.Kind == media.KindCut && p < 0 // a cut at rest: both scenes side by side
	p = clamp32(p, 0, 1)
	W, H := float32(size.X), float32(size.Y)
	defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
	full := func(fn func(gtx C, size image.Point)) { fn(gtx, size) }
	alpha := func(a float32, fn func(gtx C, size image.Point)) {
		if a <= 0.001 {
			return
		}
		st := paint.PushOpacity(gtx.Ops, min(1, a))
		fn(gtx, size)
		st.Pop()
	}
	clipR := func(r image.Rectangle, fn func(gtx C, size image.Point)) {
		if r.Empty() {
			return
		}
		st := clip.Rect(r).Push(gtx.Ops)
		fn(gtx, size)
		st.Pop()
	}
	shift := func(dx, dy float32, fn func(gtx C, size image.Point)) {
		st := op.Affine(f32.AffineId().Offset(f32.Pt(dx, dy))).Push(gtx.Ops)
		fn(gtx, size)
		st.Pop()
	}
	scaled := func(sx, sy float32, fn func(gtx C, size image.Point)) {
		c := f32.Pt(W/2, H/2)
		st := op.Affine(f32.AffineId().Scale(c, f32.Pt(max(sx, 0.001), max(sy, 0.001)))).Push(gtx.Ops)
		fn(gtx, size)
		st.Pop()
	}
	a, b := pics.paintA, pics.paintB
	dip := func(col color.NRGBA) {
		if p < 0.5 {
			full(a)
			paint.FillShape(gtx.Ops, gui.WithAlpha(col, p*2), clip.Rect(image.Rectangle{Max: size}).Op())
		} else {
			full(b)
			paint.FillShape(gtx.Ops, gui.WithAlpha(col, (1-p)*2), clip.Rect(image.Rectangle{Max: size}).Op())
		}
	}
	black := color.NRGBA{R: 8, G: 6, B: 5, A: 255}
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	switch tr.ID {
	case "none":
		switch {
		case split:
			clipR(image.Rect(0, 0, size.X/2, size.Y), a)
			clipR(image.Rect(size.X/2, 0, size.X, size.Y), b)
			lw := max(1, gtx.Dp(2))
			gui.FillRect(gtx, image.Rect(size.X/2-lw/2, 0, size.X/2-lw/2+lw, size.Y), white)
		case p < 0.5:
			full(a)
		default:
			full(b)
		}
	case "fade":
		dip(black)
	case "fadewhite":
		dip(white)
	case "wipeleft":
		full(a)
		clipR(image.Rect(int(W*(1-p)), 0, size.X, size.Y), b)
	case "wiperight":
		full(a)
		clipR(image.Rect(0, 0, int(W*p), size.Y), b)
	case "wipeup":
		full(a)
		clipR(image.Rect(0, int(H*(1-p)), size.X, size.Y), b)
	case "wipedown":
		full(a)
		clipR(image.Rect(0, 0, size.X, int(H*p)), b)
	case "slideleft":
		shift(-W*p, 0, a)
		shift(W*(1-p), 0, b)
	case "slideright":
		shift(W*p, 0, a)
		shift(-W*(1-p), 0, b)
	case "slideup":
		shift(0, -H*p, a)
		shift(0, H*(1-p), b)
	case "slidedown":
		shift(0, H*p, a)
		shift(0, -H*(1-p), b)
	case "smoothleft", "smoothright":
		full(a)
		band := W * 0.3
		front := W + band - p*(W+2*band) // left edge of the soft band
		const n = 10
		for i := 0; i < n; i++ {
			x0 := front + band*float32(i)/n
			x1 := front + band*float32(i+1)/n
			r := image.Rect(int(x0), 0, int(math.Ceil(float64(x1))), size.Y)
			if tr.ID == "smoothright" {
				r = image.Rect(int(W-x1), 0, int(math.Ceil(float64(W-x0))), size.Y)
			}
			st := clip.Rect(r).Push(gtx.Ops)
			alpha((float32(i)+0.5)/n, b)
			st.Pop()
		}
		if tr.ID == "smoothright" {
			clipR(image.Rect(0, 0, int(W-front-band), size.Y), b)
		} else {
			clipR(image.Rect(int(front+band), 0, size.X, size.Y), b)
		}
	case "circleopen":
		full(a)
		r := p * float32(math.Hypot(float64(W), float64(H))) / 2
		st := clip.Ellipse(image.Rect(int(W/2-r), int(H/2-r), int(W/2+r), int(H/2+r))).Push(gtx.Ops)
		full(b)
		st.Pop()
	case "circleclose":
		full(b)
		r := (1 - p) * float32(math.Hypot(float64(W), float64(H))) / 2
		st := clip.Ellipse(image.Rect(int(W/2-r), int(H/2-r), int(W/2+r), int(H/2+r))).Push(gtx.Ops)
		full(a)
		st.Pop()
	case "radial":
		full(a)
		if p > 0.001 {
			st := clip.Outline{Path: piePath(gtx, W, H, p)}.Op().Push(gtx.Ops)
			full(b)
			st.Pop()
		}
	case "pixelize":
		lvl := 1 - abs32(2*p-1) // 0 → 1 → 0
		base, tiny := a, pics.tinyA
		if p >= 0.5 {
			base, tiny = b, pics.tinyB
		}
		full(base)
		if tiny.Size().X > 0 {
			t := tiny
			t.Filter = paint.FilterNearest
			alpha(min(1, lvl*1.6), func(gtx C, size image.Point) { drawImage(gtx, t, size, true) })
		}
	case "zoomin":
		full(b)
		alpha(1-p, func(gtx C, size image.Point) { scaled(1+p*0.9, 1+p*0.9, a) })
	case "hblur":
		smear := float32(math.Sin(float64(p)*math.Pi)) * W * 0.08
		for i := -2; i <= 2; i++ {
			dx := smear * float32(i) / 2
			alpha(0.34*(1-p), func(gtx C, size image.Point) { shift(dx, 0, a) })
			alpha(0.34*p, func(gtx C, size image.Point) { shift(dx, 0, b) })
		}
	case "squeezeh":
		full(b)
		scaled(1-p, 1, a)
	case "squeezev":
		full(b)
		scaled(1, 1-p, a)
	default:
		switch tr.Kind {
		case media.KindCut:
			if p < 0.5 {
				full(a)
			} else {
				full(b)
			}
		case media.KindDip:
			dip(black)
		default:
			full(a)
			alpha(p, b)
		}
	}
}

// piePath is a clockwise sector from 12 o'clock covering fraction p of a turn.
func piePath(gtx C, W, H, p float32) clip.PathSpec {
	cx, cy := W/2, H/2
	r := float32(math.Hypot(float64(W), float64(H)))
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(cx, cy))
	steps := max(2, int(p*48))
	for i := 0; i <= steps; i++ {
		a := -math.Pi/2 + float64(p)*2*math.Pi*float64(i)/float64(steps)
		path.LineTo(f32.Pt(cx+r*float32(math.Cos(a)), cy+r*float32(math.Sin(a))))
	}
	path.Close()
	return path.End()
}

// restProgress is the frozen progress a preview shows at rest: mid-way, so
// the pattern is recognisable (dips less, or the tile would be plain black).
func restProgress(t media.Transition) float32 {
	switch t.Kind {
	case media.KindDip:
		return 0.32
	case media.KindCut:
		return -1 // split view
	}
	if t.ID == "zoomin" {
		return 0.35
	}
	return 0.5
}

// loopProgress maps looping preview time to a transition progress: hold A,
// transition, hold B.
func loopProgress(sec float64) float32 {
	const hold, dur = 0.35, 1.2
	period := 2*hold + dur
	u := math.Mod(sec, period)
	switch {
	case u < hold:
		return 0
	case u < hold+dur:
		return gui.EaseInOutCubic(float32((u - hold) / dur))
	}
	return 1
}

// ---- gallery tile ------------------------------------------------------------------------------------

// tile is the state of one gallery item.
type tile struct {
	cl    gui.Clickable
	hover gui.Anim
	since time.Time
	on    bool
}

// tileStyle describes a gallery item for one frame.
type tileStyle struct {
	Label    string
	Tip      string
	Selected bool
	Ratio    float32 // preview width/height (0 = 16/9)
	Disabled bool
	// Preview draws the picture into size; sec ≥ 0 is the time since the
	// hover started (animate), −1 at rest.
	Preview func(gtx C, size image.Point, sec float64)
}

// layout draws the tile across the available width; onPick runs on a click.
func (t *tile) layout(gtx C, th *gui.Theme, st tileStyle, onPick func()) D {
	if st.Disabled {
		gtx = gtx.Disabled()
	}
	if t.cl.Clicked(gtx) && onPick != nil && !st.Disabled {
		onPick()
	}
	hovered := gtx.Enabled() && t.cl.Hovered()
	if hovered && !t.on {
		t.since = gtx.Now
	}
	t.on = hovered
	t.hover.Duration = gui.DurHover
	hv := t.hover.Animate(gtx, b2f(hovered))
	ratio := st.Ratio
	if ratio <= 0 {
		ratio = 16.0 / 9
	}
	w := gtx.Constraints.Max.X
	ph := int(float32(w) / ratio)
	rad := gtx.Dp(12)
	sec := -1.0
	if hovered && !gui.ReducedMotion() {
		sec = gtx.Now.Sub(t.since).Seconds()
		gtx.Execute(op.InvalidateCmd{})
	}
	// Preview.
	pr := image.Rectangle{Max: pt(w, ph)}
	if st.Tip != "" && hovered {
		th.Tooltip(gtx, &t.cl, t.cl.Pressed(), st.Tip)
	}
	opacity := float32(1)
	if st.Disabled {
		opacity = 0.45
	}
	alphaStack := paint.PushOpacity(gtx.Ops, opacity)
	func() {
		defer clip.UniformRRect(pr, rad).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, th.Pal.Surface)
		if st.Preview != nil {
			st.Preview(gtx, pr.Size(), sec)
		}
		if hv > 0.01 {
			paint.FillShape(gtx.Ops, gui.WithAlpha(color.NRGBA{R: 255, G: 255, B: 255, A: 255}, 0.06*hv), clip.Rect(pr).Op())
		}
	}()
	switch {
	case st.Selected:
		strokeRR(gtx, pr, rad, float32(gtx.Dp(2.5)), th.Pal.Action)
		d := gtx.Dp(22)
		x, y := w-d-gtx.Dp(6), gtx.Dp(6)
		fillCircle(gtx, float32(x)+float32(d)/2, float32(y)+float32(d)/2, float32(d)/2, th.Pal.Action)
		is := gtx.Dp(13)
		o := op.Offset(pt(x+(d-is)/2, y+(d-is)/2)).Push(gtx.Ops)
		gui.IconCheck.LayoutStroke(gtx, 13, 2.5, th.Pal.OnAction)
		o.Pop()
	case hv > 0.01:
		strokeRR(gtx, pr, rad, float32(gtx.Dp(1.5)), gui.WithAlpha(th.Pal.Text, 0.3*hv))
	default:
		strokeRR(gtx, pr, rad, float32(max(1, gtx.Dp(1))), gui.WithAlpha(th.Pal.Text, 0.1))
	}
	if gtx.Focused(&t.cl.Clickable) {
		gui.FocusRing(gtx, th, pr, pxToDp(gtx, rad), 1)
	}
	// Label.
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(w, gtx.Dp(40))}
	col := gui.LerpColor(th.Pal.Text2, th.Pal.Text, max(hv, b2f(st.Selected)))
	ts := th.Small.Weighted(gui.FontMedium)
	if st.Selected {
		ts = th.Small.Weighted(gui.FontSemiBold)
	}
	lines := 1
	if w < gtx.Dp(112) {
		lines = 2 // narrow tiles (phones): let long names wrap instead of "…"
	}
	ld, lc := measure(lg, gui.Text{Style: ts, Color: col, MaxLines: lines}.Widget(th, st.Label))
	lo := op.Offset(pt(0, ph+gtx.Dp(6))).Push(gtx.Ops)
	lc.Add(gtx.Ops)
	lo.Pop()
	alphaStack.Pop()
	size := pt(w, ph+gtx.Dp(6)+ld.Size.Y)
	pressArea(gtx, &t.cl, size)
	return D{Size: size}
}

// galleryMin is the smallest tile width of a gallery: three per row on phones.
func galleryMin(gtx C) unit.Dp {
	if gtx.Constraints.Max.X < gtx.Dp(420) {
		return 84
	}
	return 104
}

// tiles returns n tile states (grown on demand).
func tiles(list *[]tile, n int) []tile {
	if len(*list) < n {
		*list = append(*list, make([]tile, n-len(*list))...)
	}
	return *list
}

// ---- the card --------------------------------------------------------------------------------------

type transCard struct {
	tiles []tile
	dur   valSlider
	reset gui.Clickable
}

func (s *proj) transCard(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.trans
	m := pv.P.Montage
	def := projectTransition(m)
	id := pv.ID
	if v, ok := st.dur.Commit(gtx); ok {
		d := math.Round(float64(v)*10) / 10
		c.Edit(id, func(p *studio.Project) { p.Montage.TransitionDuration = d })
	}
	own := 0
	for _, sc := range pv.P.Scenes {
		if sc.Transition != "" && sc.Transition != def {
			own++
		}
	}
	if st.reset.Clicked(gtx) && own > 0 {
		c.Edit(id, func(p *studio.Project) {
			for i := range p.Scenes {
				p.Scenes[i].Transition = ""
			}
		})
	}
	pics := s.transPics(c, pv, 240)
	ts := tiles(&st.tiles, len(media.Transitions))
	tr, _ := media.TransitionByID(def)
	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		sub := "Як одна сцена змінюється на іншу — для всього фільму. Свій перехід для окремої сцени — на таймлайні."
		return gui.VStack(gtx, gui.Sp16,
			func(gtx C) D { return cardHeader(gtx, th, gui.IconBlend, "Переходи", sub, nil) },
			func(gtx C) D {
				return gui.Grid{MinWidth: galleryMin(gtx), Gap: gui.Sp12}.Layout(gtx, len(media.Transitions), func(gtx C, i int) D {
					t := media.Transitions[i]
					return ts[i].layout(gtx, th, tileStyle{Label: t.Label, Tip: t.Hint, Selected: t.ID == def,
						Preview: func(gtx C, size image.Point, sec float64) {
							p := restProgress(t)
							if sec >= 0 {
								p = loopProgress(sec)
							}
							drawTransition(gtx, t, pics, p, size)
						}}, func() {
						tid := t.ID
						c.Edit(id, func(p *studio.Project) { p.Montage.Transition = tid })
					})
				})
			},
			func(gtx C) D {
				d := transDuration(m)
				label := "Тривалість переходу"
				val := secText(float64(st.dur.Value(float32(d))))
				disabled := tr.Kind == media.KindCut
				return fieldLabel(gtx, th, label, val, func(gtx C) D {
					return st.dur.Layout(gtx, th, gui.SliderStyle{Value: float32(d), Min: media.MinTransitionDuration, Max: media.MaxTransitionDuration,
						Step: 0.1, Disabled: disabled, Format: func(v float32) string { return secText(float64(v)) }})
				})
			},
			func(gtx C) D {
				switch {
				case own > 0:
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, count(own, "сцена має", "сцени мають", "сцен мають")+" свій перехід")),
						layout.Rigid(func(gtx C) D {
							return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconRotateCcw, Text: "Скинути до спільного"}.Layout(gtx, th, &st.reset)
						}),
					)
				case tr.Overlaps():
					return hint(th, "Сцени накладаються на час переходу — фільм стане трохи коротшим.")(gtx)
				case tr.Kind == media.KindDip:
					return hint(th, "Фільм також починається й закінчується плавним затемненням.")(gtx)
				}
				return hint(th, "Різкі склейки без ефектів — як у динамічних роликах.")(gtx)
			},
		)
	})
}

// transPics is the preview pair for transition tiles.
func (s *proj) transPics(c *core.Ctx, pv *core.ProjectView, px int) transPics {
	pr := s.pair(c, pv, px)
	return transPics{a: pr.a, b: pr.b, tinyA: pr.tinyA, tinyB: pr.tinyB}
}
