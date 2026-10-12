package gui

import (
	"image"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// TourStep is one stop of a Tour.
type TourStep struct {
	// Anchor is the key of a widget laid out with gui.Anchor; "" (or an
	// anchor not on screen) shows the bubble centred without a spotlight.
	Anchor string
	Title  string
	Text   string
}

// Tour is the first-run guide: the window dims except for a rounded
// spotlight around the step's widget, and a bubble card explains it with
// "Крок x з N", Назад / Далі / Пропустити. The spotlight glides between
// targets. Enter/→ go on, ← goes back, Escape skips.
//
//	s.tour.Steps = []gui.TourStep{{Anchor: "home.story", Title: "Встав історію", Text: "…"}, …}
//	s.tour.Start()
//	…in the layout: gui.Anchor(gtx, th, "home.story", storyBox)
//	s.tour.Layout(gtx, th)
//	if s.tour.Finished() { cfg.TourSeen = true }
type Tour struct {
	Steps []TourStep
	// Force pins a step for screenshots (no animation).
	Force bool

	active   bool
	step     int
	finished bool
	x, y     Anim
	w, h     Anim
	fade     Anim
	bubble   Anim
	next     Clickable
	back     Clickable
	skip     Clickable
	keys     int
	block    int
	focused  bool
	lastStep int
}

// Start begins the tour at the first step.
func (t *Tour) Start() {
	t.active, t.step, t.finished, t.focused = true, 0, false, false
	t.lastStep = -1
}

// Stop ends the tour without reporting Finished.
func (t *Tour) Stop() { t.active = false }

// Active reports whether the tour is showing.
func (t *Tour) Active() bool { return t.active }

// Step returns the current step index.
func (t *Tour) Step() int { return t.step }

// SetStep jumps to step i (screenshots, "show hints again").
func (t *Tour) SetStep(i int) {
	if i >= 0 && i < len(t.Steps) {
		t.step = i
	}
}

// Finished reports (once) that the user completed or skipped the tour.
func (t *Tour) Finished() bool {
	v := t.finished
	t.finished = false
	return v
}

func (t *Tour) end() {
	t.active, t.finished = false, true
}

// Layout registers the tour for this frame; Root draws it above everything
// once the anchors are resolved.
func (t *Tour) Layout(gtx C, th *Theme) {
	t.fade.Duration = DurDialog
	f := t.fade.Animate(gtx, b2f(t.active))
	if !t.active && f <= 0.001 {
		return
	}
	th.svc.tour = t
}

func (t *Tour) events(gtx C) {
	if t.next.Clicked(gtx) {
		t.forward()
	}
	if t.back.Clicked(gtx) && t.step > 0 {
		t.step--
	}
	if t.skip.Clicked(gtx) {
		t.end()
	}
	for {
		e, ok := gtx.Event(
			key.FocusFilter{Target: &t.keys},
			key.Filter{Focus: &t.keys, Name: key.NameReturn},
			key.Filter{Focus: &t.keys, Name: key.NameEnter},
			key.Filter{Focus: &t.keys, Name: key.NameRightArrow},
			key.Filter{Focus: &t.keys, Name: key.NameLeftArrow},
			key.Filter{Focus: &t.keys, Name: key.NameEscape},
			pointer.Filter{Target: &t.block, Kinds: pointer.Press},
		)
		if !ok {
			break
		}
		ke, ok := e.(key.Event)
		if !ok || ke.State != key.Press || !t.active {
			continue
		}
		switch ke.Name {
		case key.NameReturn, key.NameEnter, key.NameRightArrow:
			t.forward()
		case key.NameLeftArrow:
			if t.step > 0 {
				t.step--
			}
		case key.NameEscape:
			t.end()
		}
		gtx.Execute(op.InvalidateCmd{})
	}
}

func (t *Tour) forward() {
	if t.step+1 < len(t.Steps) {
		t.step++
	} else {
		t.end()
	}
}

// layoutOverlay draws the scrim with the spotlight and the bubble (window
// coordinates, called by Root after anchors are resolved).
func (t *Tour) layoutOverlay(gtx C, th *Theme) {
	t.events(gtx)
	a := t.fade.Animate(gtx, b2f(t.active))
	if t.Force {
		a = 1
	}
	if len(t.Steps) == 0 || a <= 0.001 {
		return
	}
	step := t.Steps[min(t.step, len(t.Steps)-1)]
	win := gtx.Constraints.Max
	target, ok := image.Rectangle{}, false
	if step.Anchor != "" {
		target, ok = th.AnchorRect(step.Anchor)
		ok = ok && !target.Empty() && target.Overlaps(image.Rectangle{Max: win})
	}
	pad := gtx.Dp(8)
	var hole image.Rectangle
	if ok {
		hole = target.Inset(-pad)
	} else {
		c := image.Pt(win.X/2, win.Y/2)
		hole = image.Rectangle{Min: c, Max: c}
	}
	// Glide the spotlight between targets (instant on the first frame).
	first := t.lastStep < 0 || t.Force
	for _, an := range []*Anim{&t.x, &t.y, &t.w, &t.h} {
		an.Duration, an.Ease = DurValue, EaseInOutCubic
		if first {
			an.init = false
		}
	}
	hx := t.x.Animate(gtx, float32(hole.Min.X))
	hy := t.y.Animate(gtx, float32(hole.Min.Y))
	hw := t.w.Animate(gtx, float32(hole.Dx()))
	hh := t.h.Animate(gtx, float32(hole.Dy()))
	if t.step != t.lastStep {
		t.bubble.Set(0)
		t.lastStep = t.step
	}
	t.bubble.Duration = DurDialog
	ba := t.bubble.Animate(gtx, 1)
	if t.Force {
		ba = 1
	}

	defer opacity(gtx, a)()
	// Scrim with a rounded hole: the outer contour runs counter-clockwise and
	// the spotlight clockwise, so the non-zero rule leaves the hole empty.
	rad := float32(gtx.Dp(RadiusInner))
	hr := fRect{Min: f32.Pt(hx, hy), Max: f32.Pt(hx+hw, hy+hh)}
	var p clip.Path
	p.Begin(gtx.Ops)
	W, H := float32(win.X), float32(win.Y)
	p.MoveTo(f32.Pt(0, 0))
	p.LineTo(f32.Pt(0, H))
	p.LineTo(f32.Pt(W, H))
	p.LineTo(f32.Pt(W, 0))
	p.Close()
	if hw > 1 && hh > 1 {
		appendRRect(&p, hr, min(rad, min(hw, hh)/2))
	}
	paint.FillShape(gtx.Ops, th.Pal.Scrim, clip.Outline{Path: p.End()}.Op())
	if hw > 1 && hh > 1 {
		ringf(gtx.Ops, hr, rad, 0, float32(gtx.Dp(2)), WithAlpha(rgb(0xFFFFFF), 0.85))
	}
	// Input: the key tag spans the window pass-through (focus only); presses
	// outside the spotlight are swallowed, so the target stays usable.
	pass := pointer.PassOp{}.Push(gtx.Ops)
	st := clip.Rect(image.Rectangle{Max: win}).Push(gtx.Ops)
	event.Op(gtx.Ops, &t.keys)
	st.Pop()
	pass.Pop()
	blockOutside(gtx, win, hr, &t.block)
	if !t.focused {
		t.focused = true
		gtx.Execute(key.FocusCmd{Tag: &t.keys})
	}
	t.drawBubble(gtx, th, step, hr, ok, ba)
}

// blockOutside registers tag over the window minus the rectangle r, so
// presses outside the spotlight do nothing.
func blockOutside(gtx C, win image.Point, r fRect, tag event.Tag) {
	x0, y0 := int(r.Min.X), int(r.Min.Y)
	x1, y1 := int(r.Max.X+0.5), int(r.Max.Y+0.5)
	for _, b := range []image.Rectangle{
		image.Rect(0, 0, win.X, y0),
		image.Rect(0, y1, win.X, win.Y),
		image.Rect(0, y0, x0, y1),
		image.Rect(x1, y0, win.X, y1),
	} {
		if b.Empty() {
			continue
		}
		st := clip.Rect(b).Push(gtx.Ops)
		event.Op(gtx.Ops, tag)
		st.Pop()
	}
}

// appendRRect appends a clockwise rounded rectangle contour to p.
func appendRRect(p *clip.Path, r fRect, rad float32) {
	const k = 0.5522847
	x0, y0, x1, y1 := r.Min.X, r.Min.Y, r.Max.X, r.Max.Y
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
}

// drawBubble places the step card next to the spotlight: below it when
// there is room, else above, else beside it; centred when there is no target.
func (t *Tour) drawBubble(gtx C, th *Theme, step TourStep, hole fRect, anchored bool, a float32) {
	win := gtx.Constraints.Max
	margin := gtx.Dp(16)
	w := min(gtx.Dp(360), win.X-2*margin)
	pad := gtx.Dp(20)
	cg := gtx
	cg.Constraints = layout.Constraints{Min: pt(w-2*pad, 0), Max: pt(w-2*pad, win.Y)}
	cd, call := record(cg, func(gtx C) D { return t.bubbleContent(gtx, th, step) })
	h := cd.Size.Y + 2*pad
	gap := gtx.Dp(16)
	var x, y int
	if !anchored {
		x, y = (win.X-w)/2, (win.Y-h)/2
	} else {
		cx := int((hole.Min.X + hole.Max.X) / 2)
		below := win.Y - int(hole.Max.Y) - gap - margin
		above := int(hole.Min.Y) - gap - margin
		switch {
		case below >= h:
			x, y = cx-w/2, int(hole.Max.Y)+gap
		case above >= h:
			x, y = cx-w/2, int(hole.Min.Y)-gap-h
		default:
			// Beside the target, on the wider side.
			y = int((hole.Min.Y+hole.Max.Y)/2) - h/2
			if int(hole.Min.X) > win.X-int(hole.Max.X) {
				x = int(hole.Min.X) - gap - w
			} else {
				x = int(hole.Max.X) + gap
			}
		}
		x = max(margin, min(x, win.X-margin-w))
		y = max(margin, min(y, win.Y-margin-h))
	}
	rise := int(float32(gtx.Dp(8)) * (1 - a))
	o := op.Offset(pt(x, y+rise)).Push(gtx.Ops)
	defer o.Pop()
	defer opacity(gtx, a)()
	r := rectWH(w, h)
	Shadow(gtx, th, r, RadiusInner, ElevSheet)
	Box(gtx, r, RadiusInner, th.Pal.Card, WithAlpha(th.Pal.Border, 0.6))
	bl := clip.Rect(r).Push(gtx.Ops)
	event.Op(gtx.Ops, &t.keys)
	bl.Pop()
	o2 := op.Offset(pt(pad, pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o2.Pop()
}

func (t *Tour) bubbleContent(gtx C, th *Theme, step TourStep) D {
	n := len(t.Steps)
	last := t.step >= n-1
	nextTxt := "Далі"
	var nextIcon *Icon = IconArrowRight
	if last {
		nextTxt, nextIcon = "Готово", IconCheck
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Title with the step dots beside it (the dots count the steps; no
		// eyebrow above the heading).
		layout.Rigid(func(gtx C) D {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Flexed(1, Text{Style: th.H3}.Widget(th, step.Title)),
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Left: Sp12, Top: Sp8}.Layout(gtx, func(gtx C) D { return t.dots(gtx, th) })
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: Sp4}.Layout),
		layout.Rigid(Text{Style: th.Body, Color: th.Pal.Text2}.Widget(th, step.Text)),
		layout.Rigid(layout.Spacer{Height: Sp20}.Layout),
		layout.Rigid(func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					if last {
						return D{}
					}
					return Button{Kind: Ghost, Size: Sm, Text: "Пропустити"}.Layout(gtx, th, &t.skip)
				}),
				layout.Flexed(1, func(gtx C) D { return D{Size: pt(gtx.Constraints.Min.X, 0)} }),
				layout.Rigid(func(gtx C) D {
					if t.step == 0 {
						return D{}
					}
					return layout.Inset{Right: Sp8}.Layout(gtx, func(gtx C) D {
						return Button{Kind: Tinted, Size: Sm, Text: "Назад"}.Layout(gtx, th, &t.back)
					})
				}),
				layout.Rigid(func(gtx C) D {
					return Button{Size: Sm, Text: nextTxt, Trail: nextIcon}.Layout(gtx, th, &t.next)
				}),
			)
		}),
	)
}

// dots draws the step progress as small dots (current one wide).
func (t *Tour) dots(gtx C, th *Theme) D {
	n := len(t.Steps)
	d := gtx.Dp(6)
	gap := gtx.Dp(4)
	x := 0
	for i := 0; i < n; i++ {
		w := d
		c := th.Pal.BorderStrong
		if i == t.step {
			w, c = gtx.Dp(16), th.Pal.Action
		} else if i < t.step {
			c = WithAlpha(th.Pal.Action, 0.45)
		}
		fillRRf(gtx.Ops, frect(image.Rect(x, 0, x+w, d)), float32(d)/2, c)
		x += w + gap
	}
	return D{Size: pt(x-gap, d)}
}
