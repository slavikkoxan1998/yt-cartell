package gui

import (
	"fmt"
	"image"
	"math"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
)

// Toggle is a switch: a 44×26 track whose white knob glides across (on =
// action orange), with an optional label to its right.
//
//	if v, ok := s.voice.Changed(gtx); ok { p.Voiceover = v }
//	s.voice.Layout(gtx, th, p.Voiceover, "Озвучка")
type Toggle struct {
	Disabled bool
	Force    Force

	click Clickable
	knob  Anim
	pend  pending[bool]
	value bool
}

// Changed reports the new value after a click (or Space/Enter).
func (t *Toggle) Changed(gtx C) (bool, bool) {
	t.update(gtx)
	return t.pend.take()
}

func (t *Toggle) update(gtx C) {
	if t.click.Clicked(gtx) && !t.Disabled {
		cur := t.value
		if t.pend.ok {
			cur = t.pend.v
		}
		t.pend.set(gtx, !cur)
	}
}

// Layout draws the switch for value with label ("" = none).
func (t *Toggle) Layout(gtx C, th *Theme, value bool, label string) D {
	if t.Disabled {
		gtx = gtx.Disabled()
	}
	t.value = value
	t.update(gtx)
	if t.pend.ok {
		value = t.pend.v
	}
	st := t.click.update(gtx, t.Force, 1)
	tw, thh := gtx.Dp(44), gtx.Dp(26)
	gap := gtx.Dp(12)
	var ld D
	var lc op.CallOp
	if label != "" {
		lg := gtx
		lg.Constraints = layout.Constraints{Max: pt(max(0, gtx.Constraints.Max.X-tw-gap), 1<<16)}
		ld, lc = record(lg, Text{Style: th.Body, MaxLines: 2}.Widget(th, label))
	}
	h := max(thh, ld.Size.Y)
	w := tw
	if label != "" {
		w += gap + ld.Size.X
	}
	size := pt(w, h)
	t.click.hitArea(gtx, size)
	t.knob.Duration, t.knob.Ease = DurTabs, EaseInOutCubic
	k := t.knob.Animate(gtx, b2f(value))
	defer opacity(gtx, alphaIf(t.Disabled, disabledAlpha))()
	ty := (h - thh) / 2
	track := image.Rect(0, ty, tw, ty+thh)
	off := LerpColor(th.Pal.BorderStrong, LerpColor(th.Pal.BorderStrong, th.Pal.Text3, 0.35), st.hover)
	on := LerpColor(th.Pal.Action, th.Pal.ActionHover, st.hover)
	FocusRing(gtx, th, track, unit.Dp(float32(thh)/2/gtx.Metric.PxPerDp), st.focus)
	fillRRf(gtx.Ops, frect(track), float32(thh)/2, LerpColor(off, on, k))
	kr := float32(gtx.Dp(10))
	kx := float32(thh)/2 + (float32(tw)-float32(thh))*k
	// Knob squashes a little while pressed.
	stretch := float32(gtx.Dp(4)) * st.press
	kc := f32.Pt(kx, float32(ty)+float32(thh)/2)
	knob := fRect{Min: f32.Pt(kc.X-kr-stretch*(1-k), kc.Y-kr), Max: f32.Pt(kc.X+kr+stretch*k, kc.Y+kr)}
	kb := image.Rect(int(knob.Min.X), int(knob.Min.Y), int(knob.Max.X), int(knob.Max.Y))
	Shadow(gtx, th, kb, RadiusPill, ElevControl)
	fillRRf(gtx.Ops, knob, kr, rgb(0xFFFFFF))
	if label != "" {
		o := op.Offset(pt(tw+gap, (h-ld.Size.Y)/2)).Push(gtx.Ops)
		lc.Add(gtx.Ops)
		o.Pop()
	}
	return D{Size: size}
}

// SliderStyle configures a Slider for one frame.
type SliderStyle struct {
	Value, Min, Max float32 // Min/Max default to 0…1
	Step            float32 // > 0 snaps
	// Format renders the value for the bubble ("62%", "0:45"); nil = integer.
	Format   func(v float32) string
	Disabled bool
	Force    Force // ForcePress shows the bubble as if dragging
}

func (st SliderStyle) bounds() (float32, float32) {
	if st.Max <= st.Min {
		return 0, 1
	}
	return st.Min, st.Max
}

func (st SliderStyle) snap(v float32) float32 {
	lo, hi := st.bounds()
	if st.Step > 0 {
		v = lo + float32(math.Round(float64((v-lo)/st.Step)))*st.Step
	}
	return min(hi, max(lo, v))
}

// Slider is a range input: warm rail, orange fill, white knob; while dragged
// (or focused) a value bubble floats above the knob. Arrow keys step, Home/End
// jump to the ends.
type Slider struct {
	f      widget.Float
	hover  bool
	pend   pending[float32]
	bubble Anim
	grow   Anim
	cur    float32
	st     SliderStyle // the style of the last Layout (bounds, step)
}

// Changed reports a new value (snapped to Step) while dragging or on key presses.
func (s *Slider) Changed(gtx C) (float32, bool) {
	s.update(gtx, s.st)
	return s.pend.take()
}

func (s *Slider) update(gtx C, st SliderStyle) {
	if st.Disabled {
		gtx = gtx.Disabled() // a disabled slider ignores input, even from the last frame
	}
	lo, hi := st.bounds()
	if s.f.Update(gtx) {
		s.pend.set(gtx, st.snap(lo+s.f.Value*(hi-lo)))
	}
	step := st.Step
	if step <= 0 {
		step = (hi - lo) / 20
	}
	for {
		e, ok := gtx.Event(
			key.FocusFilter{Target: s},
			key.Filter{Focus: s, Name: key.NameLeftArrow},
			key.Filter{Focus: s, Name: key.NameRightArrow},
			key.Filter{Focus: s, Name: key.NameHome},
			key.Filter{Focus: s, Name: key.NameEnd},
			pointer.Filter{Target: s, Kinds: pointer.Enter | pointer.Leave | pointer.Press},
		)
		if !ok {
			break
		}
		switch e := e.(type) {
		case key.Event:
			if e.State != key.Press {
				continue
			}
			cur := s.cur
			if s.pend.ok {
				cur = s.pend.v
			}
			switch e.Name {
			case key.NameLeftArrow:
				cur -= step
			case key.NameRightArrow:
				cur += step
			case key.NameHome:
				cur = lo
			case key.NameEnd:
				cur = hi
			}
			s.pend.set(gtx, st.snap(cur))
		case pointer.Event:
			switch e.Kind {
			case pointer.Enter:
				s.hover = true
			case pointer.Leave:
				s.hover = false
			case pointer.Press:
				gtx.Execute(key.FocusCmd{Tag: s})
			}
		}
	}
}

// Layout draws the slider across the available width.
func (s *Slider) Layout(gtx C, th *Theme, st SliderStyle) D {
	if st.Disabled {
		gtx = gtx.Disabled()
	}
	s.cur, s.st = st.Value, st
	s.update(gtx, st)
	value := st.Value
	if s.pend.ok {
		value = s.pend.v
	}
	lo, hi := st.bounds()
	frac := clamp01((value - lo) / (hi - lo))
	if !s.f.Dragging() {
		s.f.Value = frac
	}
	w := gtx.Constraints.Max.X
	h := gtx.Dp(28)
	kr := gtx.Dp(11)
	trackH := gtx.Dp(6)
	inner := max(1, w-2*kr)
	dragging := s.f.Dragging() || st.Force&ForcePress != 0
	focused := gtx.Focused(s) || st.Force&ForceFocus != 0
	hover := gtx.Enabled() && (s.hover || dragging) || st.Force&ForceHover != 0
	s.grow.Duration = DurHover
	g := s.grow.Animate(gtx, b2f(hover))
	s.bubble.Duration = DurHover
	bub := s.bubble.Animate(gtx, b2f(dragging || (focused && s.pend.ok) || st.Force&ForcePress != 0))

	defer opacity(gtx, alphaIf(st.Disabled, disabledAlpha))()
	ty := (h - trackH) / 2
	cx := kr + int(frac*float32(inner)+0.5)
	fillRRf(gtx.Ops, frect(image.Rect(kr/2, ty, w-kr/2, ty+trackH)), float32(trackH)/2, th.Pal.Track)
	fillRRf(gtx.Ops, frect(image.Rect(kr/2, ty, cx, ty+trackH)), float32(trackH)/2, th.Pal.Action)
	rad := float32(kr) * (1 + 0.1*g)
	kc := f32.Pt(float32(cx), float32(h)/2)
	if focused {
		ringf(gtx.Ops, fRect{Min: f32.Pt(kc.X-rad, kc.Y-rad), Max: f32.Pt(kc.X+rad, kc.Y+rad)}, rad, float32(gtx.Dp(2)), float32(gtx.Dp(2)), th.Pal.Focus)
	}
	kb := image.Rect(int(kc.X-rad), int(kc.Y-rad), int(kc.X+rad), int(kc.Y+rad))
	Shadow(gtx, th, kb, RadiusPill, ElevControl)
	fillCircle(gtx.Ops, kc, rad, rgb(0xFFFFFF))
	fillCircle(gtx.Ops, kc, rad*0.36, th.Pal.Action)
	if bub > 0.01 {
		txt := fmt.Sprintf("%.0f", value)
		if st.Format != nil {
			txt = st.Format(value)
		}
		m := op.Record(gtx.Ops)
		s.drawBubble(gtx, th, kc, rad, txt, bub)
		op.Defer(gtx.Ops, m.Stop())
	}
	// Input: our focus/hover area around the Float's drag area.
	if gtx.Enabled() {
		area := clip.Rect(rectWH(w, h)).Push(gtx.Ops)
		event.Op(gtx.Ops, s)
		pointer.CursorPointer.Add(gtx.Ops)
		t := op.Offset(pt(kr, 0)).Push(gtx.Ops)
		fg := gtx
		fg.Constraints = layout.Exact(pt(inner, h))
		s.f.Layout(fg, layout.Horizontal, 12)
		t.Pop()
		area.Pop()
	}
	return D{Size: pt(w, h)}
}

func (s *Slider) drawBubble(gtx C, th *Theme, kc f32.Point, rad float32, txt string, a float32) {
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(gtx.Dp(160), gtx.Dp(40))}
	ld, lc := record(lg, Text{Style: th.Label.Weighted(FontSemiBold), Color: th.Pal.TooltipFg, MaxLines: 1}.Widget(th, txt))
	padX, padY := gtx.Dp(10), gtx.Dp(5)
	bw, bh := ld.Size.X+2*padX, ld.Size.Y+2*padY
	rise := float32(gtx.Dp(6)) * (1 - a)
	x := int(kc.X) - bw/2
	y := int(kc.Y-rad) - gtx.Dp(10) - bh + int(rise)
	defer op.Offset(pt(x, y)).Push(gtx.Ops).Pop()
	defer opacity(gtx, a)()
	r := rectWH(bw, bh)
	Shadow(gtx, th, r, RadiusSmall, ElevPop)
	FillRRect(gtx, r, RadiusSmall, th.Pal.TooltipBg)
	// Little pointer under the bubble.
	var p clip.Path
	p.Begin(gtx.Ops)
	cx := float32(bw) / 2
	tip := float32(gtx.Dp(5))
	p.MoveTo(f32.Pt(cx-tip, float32(bh)-0.5))
	p.LineTo(f32.Pt(cx+tip, float32(bh)-0.5))
	p.LineTo(f32.Pt(cx, float32(bh)+tip))
	p.Close()
	fillPath(gtx, p.End(), th.Pal.TooltipBg)
	o := op.Offset(pt(padX, padY)).Push(gtx.Ops)
	lc.Add(gtx.Ops)
	o.Pop()
}
