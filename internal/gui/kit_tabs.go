package gui

import (
	"fmt"
	"image"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// pending keeps a user's pick until the caller reads it with Changed, so
// Changed works before or after Layout.
type pending[T any] struct {
	v  T
	ok bool
}

func (p *pending[T]) set(gtx C, v T) {
	p.v, p.ok = v, true
	gtx.Execute(op.InvalidateCmd{})
}

func (p *pending[T]) take() (T, bool) {
	v, ok := p.v, p.ok
	var zero T
	p.v, p.ok = zero, false
	return v, ok
}

// Tab is one entry of PillTabs.
type Tab struct {
	Label    string
	Icon     *Icon
	Number   int    // step number in a small circle (0 = none)
	Done     bool   // the step is complete: a check replaces the number
	Badge    string // trailing count
	Disabled bool
}

// PillTabs is a row of pill tabs on a warm track with a white indicator that
// slides to the selected tab (200 ms). With numbers/Done it doubles as the
// project's step tabs. Left/Right move between tabs from the keyboard.
//
//	if i, ok := s.steps.Changed(gtx); ok { s.step = i }
//	s.steps.Layout(gtx, th, s.step, tabs)
type PillTabs struct {
	// Full spreads the tabs over the available width.
	Full  bool
	Force Force

	clicks []Clickable
	ix, iw Anim
	pend   pending[int]
	n      int
	dis    []bool // disabled tabs of the last Layout
}

// Changed reports a newly selected tab index.
func (p *PillTabs) Changed(gtx C) (int, bool) {
	p.update(gtx)
	return p.pend.take()
}

func (p *PillTabs) update(gtx C) {
	for i := 0; i < min(p.n, len(p.clicks)); i++ {
		c := &p.clicks[i]
		if c.Clicked(gtx) && !p.disabled(i) {
			p.pend.set(gtx, i)
		}
		for {
			e, ok := gtx.Event(
				key.Filter{Focus: &c.Clickable, Name: key.NameLeftArrow},
				key.Filter{Focus: &c.Clickable, Name: key.NameRightArrow},
			)
			if !ok {
				break
			}
			if ke, ok := e.(key.Event); ok && ke.State == key.Press {
				j := i - 1
				if ke.Name == key.NameRightArrow {
					j = i + 1
				}
				for dir := j - i; j >= 0 && j < p.n && p.disabled(j); {
					j += dir // skip disabled tabs
				}
				if j >= 0 && j < p.n {
					p.pend.set(gtx, j)
					gtx.Execute(key.FocusCmd{Tag: &p.clicks[j].Clickable})
				}
			}
		}
	}
}

// Layout draws the tabs with sel selected.
func (p *PillTabs) Layout(gtx C, th *Theme, sel int, tabs []Tab) D {
	p.n = len(tabs)
	p.dis = p.dis[:0]
	for _, t := range tabs {
		p.dis = append(p.dis, t.Disabled)
	}
	if len(p.clicks) < len(tabs) {
		p.clicks = append(p.clicks, make([]Clickable, len(tabs)-len(p.clicks))...)
	}
	p.update(gtx)
	if p.pend.ok {
		sel = p.pend.v
	}
	pad := gtx.Dp(4)
	h := gtx.Dp(HeightMd)
	ih := h - 2*pad
	maxW := gtx.Constraints.Max.X - 2*pad

	// Measure every tab.
	type item struct {
		d    D
		call op.CallOp
	}
	items := make([]item, len(tabs))
	sts := make([]interaction, len(tabs))
	for i, t := range tabs {
		tg := gtx
		if t.Disabled {
			tg = gtx.Disabled()
		}
		force := Force(0)
		if i == sel {
			force = p.Force &^ ForceHover
		} else {
			force = p.Force &^ (ForceFocus | ForcePress)
		}
		sts[i] = p.clicks[i].update(tg, force, 1)
		tg.Constraints = layout.Constraints{Max: pt(maxW, ih)}
		items[i].d, items[i].call = record(tg, func(gtx C) D {
			return p.tabContent(gtx, th, t, i == sel, false, sts[i])
		})
	}
	// Positions.
	widths := make([]int, len(tabs))
	sum := 0
	for i := range items {
		widths[i] = items[i].d.Size.X
		sum += widths[i]
	}
	if sum > maxW && maxW > 0 {
		// Too wide: first collapse the other tabs to their number/icon
		// (the step tabs of a narrow window), keeping the selected label.
		compactable := true
		for _, t := range tabs {
			compactable = compactable && (t.Number > 0 || t.Done || t.Icon != nil)
		}
		if compactable {
			sum = 0
			for i, t := range tabs {
				if i != sel {
					tg := gtx
					if t.Disabled {
						tg = gtx.Disabled()
					}
					tg.Constraints = layout.Constraints{Max: pt(maxW, ih)}
					items[i].d, items[i].call = record(tg, func(gtx C) D {
						return p.tabContent(gtx, th, t, false, true, sts[i])
					})
				}
				widths[i] = items[i].d.Size.X
				sum += widths[i]
			}
		}
	}
	if len(tabs) > 0 && sum > maxW && maxW > 0 {
		// Too wide: equal slots, labels truncate with "…".
		slot := maxW / len(tabs)
		for i, t := range tabs {
			tg := gtx
			if t.Disabled {
				tg = gtx.Disabled()
			}
			tg.Constraints = layout.Constraints{Max: pt(slot, ih)}
			items[i].d, items[i].call = record(tg, func(gtx C) D {
				return p.tabContent(gtx, th, t, i == sel, false, sts[i])
			})
			widths[i] = slot
		}
		widths[len(widths)-1] += maxW - slot*len(tabs)
		sum = maxW
	} else if p.Full && len(tabs) > 0 && sum < maxW {
		extra := maxW - sum
		for i := range widths {
			add := extra / len(tabs)
			if i == len(tabs)-1 {
				add = extra - add*(len(tabs)-1)
			}
			widths[i] += add
		}
		sum = maxW
	}
	w := sum + 2*pad
	size := pt(w, h)
	FillRRect(gtx, rectWH(w, h), RadiusPill, th.Pal.Surface)
	// Indicator.
	x := pad
	selX, selW := 0, 0
	for i := range tabs {
		if i == sel {
			selX, selW = x, widths[i]
		}
		x += widths[i]
	}
	p.ix.Duration, p.ix.Ease = DurTabs, EaseInOutCubic
	p.iw.Duration, p.iw.Ease = DurTabs, EaseInOutCubic
	ixv := p.ix.Animate(gtx, float32(selX))
	iwv := p.iw.Animate(gtx, float32(selW))
	if sel >= 0 && sel < len(tabs) {
		ir := fRect{Min: f32.Pt(ixv, float32(pad)), Max: f32.Pt(ixv+iwv, float32(pad+ih))}
		irr := image.Rect(int(ir.Min.X), int(ir.Min.Y), int(ir.Max.X+0.5), int(ir.Max.Y))
		Shadow(gtx, th, irr, RadiusPill, ElevControl)
		fillRRf(gtx.Ops, ir, float32(ih)/2, th.Pal.Card)
	}
	x = pad
	for i := range tabs {
		tw := widths[i]
		tg := gtx
		if tabs[i].Disabled {
			tg = gtx.Disabled()
		}
		o := op.Offset(pt(x, pad)).Push(gtx.Ops)
		p.clicks[i].hitArea(tg, pt(tw, ih))
		// Hover wash on unselected tabs.
		if i != sel && sts[i].hover > 0.01 {
			FillRRect(gtx, rectWH(tw, ih), RadiusPill, WithAlpha(th.Pal.SurfaceHover, sts[i].hover))
		}
		FocusRing(gtx, th, rectWH(tw, ih), RadiusPill, sts[i].focus)
		func() {
			defer op.Offset(pt((tw-items[i].d.Size.X)/2, (ih-items[i].d.Size.Y)/2)).Push(gtx.Ops).Pop()
			defer opacity(gtx, alphaIf(tabs[i].Disabled, disabledAlpha))()
			items[i].call.Add(gtx.Ops)
		}()
		o.Pop()
		x += tw
	}
	return D{Size: size}
}

func (p *PillTabs) tabContent(gtx C, th *Theme, t Tab, selected, compact bool, st interaction) D {
	fg := LerpColor(th.Pal.Text2, th.Pal.Text, st.hover)
	ts := th.Label
	if selected {
		fg = th.Pal.Text
		ts = th.Label.Weighted(FontSemiBold)
	}
	padX := gtx.Dp(16)
	if gtx.Constraints.Max.X < gtx.Dp(130) {
		padX = gtx.Dp(10) // squeezed tabs keep more of their label
	}
	var ch []layout.FlexChild
	if t.Number > 0 || t.Done {
		ch = append(ch, layout.Rigid(func(gtx C) D {
			d := gtx.Dp(22)
			c := f32.Pt(float32(d)/2, float32(d)/2)
			switch {
			case t.Done:
				fillCircle(gtx.Ops, c, float32(d)/2, th.Pal.Success)
				o := op.Offset(pt((d-gtx.Dp(14))/2, (d-gtx.Dp(14))/2)).Push(gtx.Ops)
				IconCheck.LayoutStroke(gtx, 14, 2.5, OnColor(th.Pal.Success))
				o.Pop()
			default:
				bg, nf := th.Pal.SurfaceHover, th.Pal.Text2
				if selected {
					bg, nf = th.Pal.Action, th.Pal.OnAction
				}
				fillCircle(gtx.Ops, c, float32(d)/2, bg)
				lg := gtx
				lg.Constraints = layout.Constraints{Max: pt(d, d)}
				nd, nc := record(lg, Text{Style: th.Caption.Weighted(FontSemiBold), Color: nf, MaxLines: 1}.Widget(th, fmt.Sprint(t.Number)))
				o := op.Offset(pt((d-nd.Size.X)/2, (d-nd.Size.Y)/2)).Push(gtx.Ops)
				nc.Add(gtx.Ops)
				o.Pop()
			}
			return D{Size: pt(d, d)}
		}), layout.Rigid(layout.Spacer{Width: Sp8}.Layout))
	} else if t.Icon != nil {
		icC := fg
		if selected {
			icC = th.Pal.Action
		}
		ch = append(ch, layout.Rigid(t.Icon.Widget(18, icC)), layout.Rigid(layout.Spacer{Width: Sp8}.Layout))
	}
	if compact && len(ch) > 0 {
		ch = ch[:len(ch)-1] // drop the spacer after the circle/icon
		padX = gtx.Dp(12)
		return layout.Inset{Left: unit.Dp(float32(padX) / gtx.Metric.PxPerDp), Right: unit.Dp(float32(padX) / gtx.Metric.PxPerDp)}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
		})
	}
	ch = append(ch, layout.Rigid(Text{Style: ts, Color: fg, MaxLines: 1}.Widget(th, t.Label)))
	if t.Badge != "" {
		ch = append(ch, layout.Rigid(layout.Spacer{Width: Sp8}.Layout), layout.Rigid(Badge{Text: t.Badge}.Widget(th)))
	}
	return layout.Inset{Left: unit.Dp(float32(padX) / gtx.Metric.PxPerDp), Right: unit.Dp(float32(padX) / gtx.Metric.PxPerDp)}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
	})
}

// Widget adapts Badge.Layout to a layout.Widget.
func (b Badge) Widget(th *Theme) layout.Widget {
	return func(gtx C) D { return b.Layout(gtx, th) }
}

// SegOption is one option of a Segmented control.
type SegOption struct {
	Value, Label string
	Icon         *Icon
	Disabled     bool
}

// Segmented is a compact control of equal-width options on a warm track
// with a white thumb that slides to the chosen option (formats, modes).
//
//	if v, ok := s.format.Changed(gtx); ok { p.Aspect = v }
//	s.format.Layout(gtx, th, p.Aspect, []gui.SegOption{{Value: "16:9", Label: "16:9"}, …})
type Segmented struct {
	// Full stretches the control over the available width.
	Full  bool
	Force Force

	clicks []Clickable
	vals   []string
	dis    []bool // disabled options of the last Layout
	tx     Anim
	pend   pending[string]
}

// Changed reports a newly picked value.
func (s *Segmented) Changed(gtx C) (string, bool) {
	s.update(gtx)
	return s.pend.take()
}

func (s *Segmented) update(gtx C) {
	for i := range s.vals {
		if i < len(s.clicks) && s.clicks[i].Clicked(gtx) && !(i < len(s.dis) && s.dis[i]) {
			s.pend.set(gtx, s.vals[i])
		}
		if i >= len(s.clicks) {
			break
		}
		c := &s.clicks[i]
		for {
			e, ok := gtx.Event(
				key.Filter{Focus: &c.Clickable, Name: key.NameLeftArrow},
				key.Filter{Focus: &c.Clickable, Name: key.NameRightArrow},
			)
			if !ok {
				break
			}
			if ke, ok := e.(key.Event); ok && ke.State == key.Press {
				j := i - 1
				if ke.Name == key.NameRightArrow {
					j = i + 1
				}
				if j >= 0 && j < len(s.vals) {
					s.pend.set(gtx, s.vals[j])
					gtx.Execute(key.FocusCmd{Tag: &s.clicks[j].Clickable})
				}
			}
		}
	}
}

// Layout draws the options with value selected.
func (s *Segmented) Layout(gtx C, th *Theme, value string, opts []SegOption) D {
	if len(s.clicks) < len(opts) {
		s.clicks = append(s.clicks, make([]Clickable, len(opts)-len(s.clicks))...)
	}
	s.vals, s.dis = s.vals[:0], s.dis[:0]
	for _, o := range opts {
		s.vals = append(s.vals, o.Value)
		s.dis = append(s.dis, o.Disabled)
	}
	s.update(gtx)
	if s.pend.ok {
		value = s.pend.v
	}
	if len(opts) == 0 {
		return D{}
	}
	pad := gtx.Dp(3)
	h := gtx.Dp(40)
	ih := h - 2*pad
	padX := gtx.Dp(14)
	// Measure labels to find the widest option.
	type item struct {
		d    D
		call op.CallOp
	}
	items := make([]item, len(opts))
	sts := make([]interaction, len(opts))
	sel := -1
	optW := 0
	recs := make([]func(maxW int), len(opts))
	for i, o := range opts {
		tg := gtx
		if o.Disabled {
			tg = gtx.Disabled()
		}
		selected := o.Value == value
		if selected {
			sel = i
		}
		force := s.Force &^ (ForceFocus | ForcePress)
		if selected {
			force = s.Force &^ ForceHover
		}
		sts[i] = s.clicks[i].update(tg, force, 1)
		fg := LerpColor(th.Pal.Text2, th.Pal.Text, sts[i].hover)
		ts := th.Label
		if selected {
			fg, ts = th.Pal.Text, th.Label.Weighted(FontSemiBold)
		}
		o := o
		recs[i] = func(maxW int) {
			tg.Constraints = layout.Constraints{Max: pt(maxW, ih)}
			items[i].d, items[i].call = record(tg, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						if o.Icon == nil {
							return D{}
						}
						icC := fg
						if selected {
							icC = th.Pal.Action
						}
						return layout.Inset{Right: Sp8}.Layout(gtx, o.Icon.Widget(16, icC))
					}),
					layout.Rigid(Text{Style: ts, Color: fg, MaxLines: 1}.Widget(th, o.Label)),
				)
			})
		}
		recs[i](gtx.Constraints.Max.X)
		optW = max(optW, items[i].d.Size.X+2*padX)
	}
	n := len(opts)
	w := optW*n + 2*pad
	if s.Full {
		w = max(w, gtx.Constraints.Max.X)
	}
	w = min(w, max(gtx.Constraints.Max.X, 2*pad+n))
	optW = (w - 2*pad) / n
	// Squeezed: labels truncate inside their slots.
	inner := max(0, optW-2*gtx.Dp(8))
	for i := range items {
		if items[i].d.Size.X > inner {
			recs[i](inner)
		}
	}
	size := pt(w, h)
	FillRRect(gtx, rectWH(w, h), RadiusControl, th.Pal.Surface)
	s.tx.Duration, s.tx.Ease = DurTabs, EaseInOutCubic
	if sel >= 0 {
		tx := s.tx.Animate(gtx, float32(pad+sel*optW))
		tr := fRect{Min: f32.Pt(tx, float32(pad)), Max: f32.Pt(tx+float32(optW), float32(pad+ih))}
		Shadow(gtx, th, image.Rect(int(tr.Min.X), int(tr.Min.Y), int(tr.Max.X), int(tr.Max.Y)), 10, ElevControl)
		fillRRf(gtx.Ops, tr, float32(gtx.Dp(10)), th.Pal.Card)
	}
	for i := range opts {
		tg := gtx
		if opts[i].Disabled {
			tg = gtx.Disabled()
		}
		o := op.Offset(pt(pad+i*optW, pad)).Push(gtx.Ops)
		s.clicks[i].hitArea(tg, pt(optW, ih))
		if i != sel && sts[i].hover > 0.01 {
			FillRRect(gtx, rectWH(optW, ih), 10, WithAlpha(th.Pal.SurfaceHover, sts[i].hover))
		}
		FocusRing(gtx, th, rectWH(optW, ih), 10, sts[i].focus)
		func() {
			defer op.Offset(pt((optW-items[i].d.Size.X)/2, (ih-items[i].d.Size.Y)/2)).Push(gtx.Ops).Pop()
			defer opacity(gtx, alphaIf(opts[i].Disabled, disabledAlpha))()
			items[i].call.Add(gtx.Ops)
		}()
		o.Pop()
	}
	return D{Size: size}
}

func (p *PillTabs) disabled(i int) bool { return i >= 0 && i < len(p.dis) && p.dis[i] }
