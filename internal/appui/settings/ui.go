package settings

// Local building blocks of the settings cards: card frames, labelled fields,
// auto-fit field rows, setting lines, switch rows, check results, path wells.
// Everything draws with the kit (internal/gui) and its palette only.

import (
	"image"
	"image/color"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
)

type (
	C = core.C
	D = core.D
)

// record lays w out into a macro.
func record(gtx C, w layout.Widget) (D, op.CallOp) {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	return d, m.Stop()
}

func dp(gtx C, px int) unit.Dp { return unit.Dp(float32(px) / gtx.Metric.PxPerDp) }

// ---- cards -------------------------------------------------------------------------

// head is the title block of a card.
type head struct {
	Icon  *gui.Icon
	Title string
	Text  string
	// Big is the header of a one-card section (larger icon and title).
	Big bool
	// Trail sits on the right of the title (status pill, saved note).
	Trail layout.Widget
}

// card draws a white settings card: the head, then body blocks 20 dp apart
// (nil blocks are skipped).
func card(gtx C, th *gui.Theme, narrow bool, h head, body ...layout.Widget) D {
	pad := gui.Sp24
	if narrow {
		pad = gui.Sp20
	}
	return gui.Card{Padding: pad}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		ws := []layout.Widget{func(gtx C) D { return cardHead(gtx, th, h) }}
		for _, b := range body {
			if b != nil {
				ws = append(ws, b)
			}
		}
		return gui.VStack(gtx, gui.Sp20, ws...)
	})
}

func cardHead(gtx C, th *gui.Theme, h head) D {
	box, ic, title := unit.Dp(40), unit.Dp(20), th.H3
	if h.Big {
		box, ic, title = 48, 24, th.H2.Sized(22)
	}
	return layout.Flex{Alignment: layout.Start}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			if h.Icon == nil {
				return D{}
			}
			return layout.Inset{Right: gui.Sp16}.Layout(gtx, func(gtx C) D {
				s := gtx.Dp(box)
				gui.FillRRect(gtx, image.Rect(0, 0, s, s), gui.RadiusControl, th.Pal.Surface)
				o := op.Offset(image.Pt((s-gtx.Dp(ic))/2, (s-gtx.Dp(ic))/2)).Push(gtx.Ops)
				h.Icon.Layout(gtx, ic, th.Pal.Text)
				o.Pop()
				return D{Size: image.Pt(s, s)}
			})
		}),
		layout.Flexed(1, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			ch := []layout.FlexChild{layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, gui.Text{Style: title, MaxLines: 2}.Widget(th, h.Title)),
					layout.Rigid(func(gtx C) D {
						if h.Trail == nil {
							return D{}
						}
						return layout.Inset{Left: gui.Sp12}.Layout(gtx, h.Trail)
					}),
				)
			})}
			if h.Text != "" {
				ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp4}.Layout),
					layout.Rigid(gui.Text{Style: th.Small.Sized(14).Lined(1.45), Color: th.Pal.Text2}.Widget(th, h.Text)))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
		}),
	)
}

// sectionHead is the heading of a group of cards on the ground (the action
// goes under the text on narrow screens).
func sectionHead(gtx C, th *gui.Theme, title, text string, action layout.Widget) D {
	if action != nil && gtx.Constraints.Max.X < gtx.Dp(560) {
		return gui.VStack(gtx, gui.Sp12,
			func(gtx C) D { return sectionHead(gtx, th, title, text, nil) },
			func(gtx C) D { gtx.Constraints.Min.X = 0; return action(gtx) },
		)
	}
	return layout.Flex{Alignment: layout.End}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			ch := []layout.FlexChild{layout.Rigid(gui.Text{Style: th.H2}.Widget(th, title))}
			if text != "" {
				ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp4}.Layout),
					layout.Rigid(gui.Text{Style: th.Body, Color: th.Pal.Text2}.Widget(th, text)))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
		}),
		layout.Rigid(func(gtx C) D {
			if action == nil {
				return D{}
			}
			return layout.Inset{Left: gui.Sp16}.Layout(gtx, action)
		}),
	)
}

// ---- fields ------------------------------------------------------------------------

// labeled stacks a field label, the control and a hint (like the kit's
// fields) for controls without their own label (segmented, slider…).
func labeled(th *gui.Theme, label, hint string, w layout.Widget) layout.Widget {
	return func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		var ch []layout.FlexChild
		if label != "" {
			ch = append(ch, layout.Rigid(gui.Text{Style: th.Label}.Widget(th, label)),
				layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout))
		}
		ch = append(ch, layout.Rigid(w))
		if hint != "" {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: 6}.Layout),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, hint)))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	}
}

// fieldRow lays the widgets in equal columns at least minW wide (as many as
// fit, never more than there are widgets); rows wrap 20 dp apart. nil
// widgets are skipped.
func fieldRow(gtx C, minW unit.Dp, ws ...layout.Widget) D {
	var list []layout.Widget
	for _, w := range ws {
		if w != nil {
			list = append(list, w)
		}
	}
	W := gtx.Constraints.Max.X
	n := len(list)
	if n == 0 {
		return D{Size: image.Pt(W, 0)}
	}
	g := gtx.Dp(gui.Sp16)
	cols := max(1, min(n, (W+g)/(gtx.Dp(minW)+g)))
	cw := (W - g*(cols-1)) / cols
	y := 0
	for r := 0; r*cols < n; r++ {
		if r > 0 {
			y += gtx.Dp(gui.Sp20)
		}
		h := 0
		for k := 0; k < cols && r*cols+k < n; k++ {
			cg := gtx
			cg.Constraints = layout.Constraints{Min: image.Pt(cw, 0), Max: image.Pt(cw, gtx.Constraints.Max.Y)}
			d, call := record(cg, list[r*cols+k])
			o := op.Offset(image.Pt(k*(cw+g), y)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			o.Pop()
			h = max(h, d.Size.Y)
		}
		y += h
	}
	return D{Size: image.Pt(W, y)}
}

// line is one row of a settings list: title and hint on the left, the
// control on the right (under the text when the card is narrow).
func line(gtx C, th *gui.Theme, title, hint string, controlW unit.Dp, control layout.Widget) D {
	text := func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		ch := []layout.FlexChild{layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium)}.Widget(th, title))}
		if hint != "" {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: 2}.Layout),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, hint)))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	}
	W := gtx.Constraints.Max.X
	if controlW > 0 && W < gtx.Dp(controlW)+gtx.Dp(240) {
		return gui.VStack(gtx, gui.Sp12, text, control)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, text),
		layout.Rigid(layout.Spacer{Width: gui.Sp24}.Layout),
		layout.Rigid(func(gtx C) D {
			if controlW > 0 {
				gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(controlW))
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
			}
			return control(gtx)
		}),
	)
}

// lines stacks setting lines with hairlines between them.
func lines(gtx C, th *gui.Theme, ws ...layout.Widget) D {
	var ch []layout.FlexChild
	first := true
	for _, w := range ws {
		if w == nil {
			continue
		}
		if !first {
			ch = append(ch,
				layout.Rigid(layout.Spacer{Height: gui.Sp16}.Layout),
				layout.Rigid(gui.Divider{}.Widget(th)),
				layout.Rigid(layout.Spacer{Height: gui.Sp16}.Layout))
		}
		first = false
		w := w
		ch = append(ch, layout.Rigid(func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return w(gtx)
		}))
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

// switchRow is a setting with a switch on the right; the text is clickable too.
type switchRow struct {
	t     gui.Toggle
	label gui.Clickable
}

// layout draws the row; set runs with the new value after a click.
func (s *switchRow) layout(gtx C, th *gui.Theme, value bool, title, hint string, set func(bool)) D {
	if v, ok := s.t.Changed(gtx); ok {
		set(v)
		value = v
	}
	if s.label.Clicked(gtx) {
		value = !value
		set(value)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return s.label.Clickable.Layout(gtx, func(gtx C) D {
				d := layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium)}.Widget(th, title)),
					layout.Rigid(func(gtx C) D {
						if hint == "" {
							return D{}
						}
						return layout.Inset{Top: 2}.Layout(gtx, gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, hint))
					}),
				)
				defer clip.Rect(image.Rectangle{Max: d.Size}).Push(gtx.Ops).Pop()
				pointer.CursorPointer.Add(gtx.Ops)
				return d
			})
		}),
		layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
		layout.Rigid(func(gtx C) D { return s.t.Layout(gtx, th, value, "") }),
	)
}

// ---- checks ------------------------------------------------------------------------

// check is the state of one «Перевірити» run.
type check struct {
	busy, done, ok bool
	msg            string
	seq            int
	show           gui.Anim
}

func (k *check) reset() {
	if !k.busy {
		k.done, k.msg = false, ""
	}
}

// statusPill is the small state shown in a card's head after a check.
func (k *check) statusPill(th *gui.Theme) layout.Widget {
	switch {
	case k.busy:
		return gui.StatusPill{State: gui.StateRunning, Percent: -1, Text: "Перевіряємо", Small: true}.Widget(th)
	case k.done && k.ok:
		return gui.StatusPill{State: gui.StateDone, Text: "Працює", Small: true}.Widget(th)
	case k.done:
		return gui.StatusPill{State: gui.StateError, Text: "Не працює", Small: true}.Widget(th)
	}
	return nil
}

// result draws the outcome of a check: a soft green panel with a tick, or a
// red one with the error and what to do; nothing before the first run.
func (k *check) result(gtx C, th *gui.Theme, advice string) D {
	if !k.busy && !k.done {
		k.show.Set(0)
		return D{}
	}
	k.show.Duration, k.show.Ease = gui.DurDialog, gui.EaseOutCubic
	a := k.show.Animate(gtx, 1)
	return gui.FadeUp(gtx, a, 6, func(gtx C) D { return resultPanel(gtx, th, k.busy, k.ok, k.msg, advice, "") })
}

// resultPanel is the tinted outcome box of a check or an action.
func resultPanel(gtx C, th *gui.Theme, busy, ok bool, msg, advice, okTitle string) D {
	p := &th.Pal
	bg, ink := p.Surface, p.Text2
	icon := (*gui.Icon)(nil)
	title := ""
	switch {
	case busy:
		title = "Перевіряємо…"
		if msg != "" {
			title = msg
			msg = ""
		}
	case ok:
		bg, ink, icon, title = p.SuccessSoft, p.SuccessInk, gui.IconCheckCircle, "Усе працює"
		if okTitle != "" {
			title = okTitle
		}
	default:
		bg, ink, icon, title = p.ErrorSoft, p.ErrorInk, gui.IconCircleAlert, "Не вдалося"
	}
	if msg == "" && !busy && ok {
		msg = "Підключення в порядку."
	}
	pad := gtx.Dp(gui.Sp12)
	w := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*pad-gtx.Dp(30)), gtx.Constraints.Max.Y)}
	d, call := record(cg, func(gtx C) D {
		ch := []layout.FlexChild{layout.Rigid(gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), Color: ink}.Widget(th, title))}
		if msg != "" {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: 2}.Layout),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text, MaxLines: 14}.Widget(th, msg))) // checks show the exact request
		}
		if advice != "" && !ok && !busy {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: 6}.Layout),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, advice)))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
	h := d.Size.Y + 2*pad
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusControl, bg)
	o := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
	if busy {
		gui.Spinner{Size: 18, Color: th.Pal.Text2}.Layout(gtx, th)
	} else {
		icon.Layout(gtx, 20, ink)
	}
	o.Pop()
	o = op.Offset(image.Pt(pad+gtx.Dp(30), pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}

// ---- small pieces ------------------------------------------------------------------

// note is a quiet info panel (tinted, with an icon).
func note(gtx C, th *gui.Theme, icon *gui.Icon, text string, action layout.Widget) D {
	pad := gtx.Dp(gui.Sp12)
	w := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*pad-gtx.Dp(30)), gtx.Constraints.Max.Y)}
	d, call := record(cg, func(gtx C) D {
		ch := []layout.FlexChild{layout.Rigid(gui.Text{Style: th.Small.Sized(14), Color: th.Pal.Text2}.Widget(th, text))}
		if action != nil {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout), layout.Rigid(action))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
	h := d.Size.Y + 2*pad
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusControl, th.Pal.Surface)
	o := op.Offset(image.Pt(pad, pad+1)).Push(gtx.Ops)
	icon.Layout(gtx, 18, th.Pal.Text2)
	o.Pop()
	o = op.Offset(image.Pt(pad+gtx.Dp(30), pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}

// warnBox is a warning panel (amber) with a title, a line and actions — a
// setting that will fail as it is, with the ways out.
func warnBox(gtx C, th *gui.Theme, title, text string, actions ...layout.Widget) D {
	pad := gtx.Dp(gui.Sp12)
	w := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*pad-gtx.Dp(30)), gtx.Constraints.Max.Y)}
	d, call := record(cg, func(gtx C) D {
		ch := []layout.FlexChild{layout.Rigid(gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), Color: th.Pal.WarningInk}.Widget(th, title))}
		if text != "" {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: 2}.Layout),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text}.Widget(th, text)))
		}
		var as []layout.Widget
		for _, a := range actions {
			if a != nil {
				as = append(as, a)
			}
		}
		if len(as) > 0 {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
				layout.Rigid(func(gtx C) D { return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, as...) }))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
	h := d.Size.Y + 2*pad
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusControl, th.Pal.WarningSoft)
	o := op.Offset(image.Pt(pad, pad+1)).Push(gtx.Ops)
	gui.IconAlertTriangle.Layout(gtx, 18, th.Pal.WarningInk)
	o.Pop()
	o = op.Offset(image.Pt(pad+gtx.Dp(30), pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}

// pathWell shows a path read-only in a tinted one-line box; long paths lose
// their middle, never the drive or the file name.
func pathWell(gtx C, th *gui.Theme, icon *gui.Icon, path, empty string) D {
	w := gtx.Constraints.Max.X
	h := gtx.Dp(gui.HeightMd)
	padX, is, gap := gtx.Dp(14), gtx.Dp(18), gtx.Dp(10)
	tw := max(0, w-2*padX-is-gap)
	txt, col := middleEllipsis(gtx, th, th.Body, path, tw), th.Pal.Text
	if strings.TrimSpace(path) == "" {
		txt, col = empty, th.Pal.Text3
	}
	lg := gtx
	lg.Constraints = layout.Constraints{Max: image.Pt(tw, h)}
	ld, lc := record(lg, gui.Text{Style: th.Body, Color: col, MaxLines: 1}.Widget(th, txt))
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusControl, th.Pal.Surface)
	o := op.Offset(image.Pt(padX, (h-is)/2)).Push(gtx.Ops)
	icon.Layout(gtx, 18, th.Pal.Text2)
	o.Pop()
	o = op.Offset(image.Pt(padX+is+gap, (h-ld.Size.Y)/2)).Push(gtx.Ops)
	lc.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}

// middleEllipsis shortens s to fit maxW px by cutting its middle.
func middleEllipsis(gtx C, th *gui.Theme, st gui.TextStyle, s string, maxW int) string {
	if s == "" || th.TextWidth(gtx, st, s) <= maxW {
		return s
	}
	r := []rune(s)
	lo, hi := 0, len(r)
	best := "…"
	for lo <= hi {
		keep := (lo + hi) / 2
		head := keep * 2 / 5
		tail := keep - head
		cand := string(r[:head]) + "…" + string(r[len(r)-tail:])
		if th.TextWidth(gtx, st, cand) <= maxW {
			best = cand
			lo = keep + 1
		} else {
			hi = keep - 1
		}
	}
	return best
}

// savedNote is the "✓ Збережено" that fades in a card's head after a save.
type savedNote struct {
	want bool      // a save finished; show from the next frame
	at   time.Time // gtx.Now when it appeared
	a    gui.Anim
}

const savedFor = 1800 * time.Millisecond

func (s *savedNote) widget(th *gui.Theme) layout.Widget {
	return func(gtx C) D {
		if s.want {
			s.want, s.at = false, gtx.Now
		}
		on := !s.at.IsZero() && gtx.Now.Sub(s.at) < savedFor
		if on {
			gtx.Execute(op.InvalidateCmd{At: s.at.Add(savedFor)})
		}
		s.a.Duration = gui.DurDialog
		a := s.a.Animate(gtx, b2f(on))
		if a <= 0.01 {
			return D{}
		}
		return gui.Fade(gtx, a, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(gui.IconCheck.Widget(16, th.Pal.SuccessInk)),
				layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
				layout.Rigid(gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: th.Pal.SuccessInk, MaxLines: 1}.Widget(th, "Збережено")),
			)
		})
	}
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// trails joins several head trails (status pill, saved note) in a row.
func trails(ws ...layout.Widget) layout.Widget {
	return func(gtx C) D {
		var ch []layout.FlexChild
		for _, w := range ws {
			if w == nil {
				continue
			}
			if len(ch) > 0 {
				ch = append(ch, layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout))
			}
			ch = append(ch, layout.Rigid(w))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
	}
}

// staticChip is a small non-interactive info chip.
func staticChip(th *gui.Theme, icon *gui.Icon, text string) layout.Widget {
	return func(gtx C) D { return gui.Chip{Text: text, Icon: icon, Small: true}.Layout(gtx, th, nil) }
}

// pill is a quiet neutral label with an icon (e.g. "Для автопілота").
func pill(gtx C, th *gui.Theme, icon *gui.Icon, text string) D {
	h := gtx.Dp(24)
	padL, padR, gap, is := gtx.Dp(8), gtx.Dp(10), gtx.Dp(5), gtx.Dp(14)
	lg := gtx
	lg.Constraints = layout.Constraints{Max: image.Pt(max(0, gtx.Constraints.Max.X-padL-padR-is-gap), h)}
	ld, lc := record(lg, gui.Text{Style: th.Caption.Weighted(gui.FontMedium), Color: th.Pal.Text2, MaxLines: 1}.Widget(th, text))
	w := padL + is + gap + ld.Size.X + padR
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusPill, th.Pal.Surface)
	o := op.Offset(image.Pt(padL, (h-is)/2)).Push(gtx.Ops)
	icon.Layout(gtx, 14, th.Pal.Text2)
	o.Pop()
	o = op.Offset(image.Pt(padL+is+gap, (h-ld.Size.Y)/2)).Push(gtx.Ops)
	lc.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}

// numberedSteps is a short how-to: numbered circles and text.
func numberedSteps(gtx C, th *gui.Theme, steps ...string) D {
	var ch []layout.FlexChild
	for i, s := range steps {
		if i > 0 {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout))
		}
		n := i + 1
		s := s
		ch = append(ch, layout.Rigid(func(gtx C) D {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					sz := gtx.Dp(24)
					fillCircle(gtx, sz, th.Pal.Card)
					lg := gtx
					lg.Constraints = layout.Constraints{Max: image.Pt(sz, sz)}
					nd, nc := record(lg, gui.Text{Style: th.Caption.Weighted(gui.FontSemiBold), Color: th.Pal.Text, MaxLines: 1}.Widget(th, itoa(n)))
					o := op.Offset(image.Pt((sz-nd.Size.X)/2, (sz-nd.Size.Y)/2)).Push(gtx.Ops)
					nc.Add(gtx.Ops)
					o.Pop()
					return D{Size: image.Pt(sz, sz)}
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return layout.Inset{Top: 2}.Layout(gtx, gui.Text{Style: th.Small.Sized(14).Lined(1.45), Color: th.Pal.Text}.Widget(th, s))
				}),
			)
		}))
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

func fillCircle(gtx C, size int, c color.NRGBA) {
	r := float32(size) / 2
	defer clip.Ellipse{Min: image.Pt(0, 0), Max: image.Pt(size, size)}.Push(gtx.Ops).Pop()
	paint.ColorOp{Color: c}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	_ = r
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// topFade paints the ground fading out at the top of a scrolled area.
func topFade(gtx C, th *gui.Theme, w, h int, alpha float32) {
	if alpha <= 0.01 {
		return
	}
	g := th.Pal.Ground
	defer clip.Rect(image.Rect(0, 0, w, h)).Push(gtx.Ops).Pop()
	paint.LinearGradientOp{
		Stop1:  f32.Pt(0, 0),
		Color1: gui.WithAlpha(g, alpha),
		Stop2:  f32.Pt(0, float32(h)),
		Color2: gui.WithAlpha(g, 0),
	}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// inset is a flat panel inside a card (cards never nest): the warm surface
// fill, 12 dp corners, a hairline and no shadow — the look of the flat
// picker fields.
func inset(gtx C, th *gui.Theme, w layout.Widget) D {
	p := gtx.Dp(gui.Sp16)
	W := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{
		Min: image.Pt(max(0, W-2*p), 0),
		Max: image.Pt(max(0, W-2*p), max(0, gtx.Constraints.Max.Y-2*p)),
	}
	d, call := record(cg, w)
	size := image.Pt(W, d.Size.Y+2*p)
	gui.Box(gtx, image.Rectangle{Max: size}, gui.RadiusControl, th.Pal.Surface, th.Pal.Border)
	o := op.Offset(image.Pt(p, p)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: size}
}

// insetHead is the title row of an inset panel: an icon on a card-coloured
// square, a title and a line, and a trailing widget (status pill).
func insetHead(gtx C, th *gui.Theme, icon *gui.Icon, title, text string, trail layout.Widget) D {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			s := gtx.Dp(36)
			gui.FillRRect(gtx, image.Rect(0, 0, s, s), gui.RadiusSmall+2, th.Pal.Card)
			is := gtx.Dp(18)
			o := op.Offset(image.Pt((s-is)/2, (s-is)/2)).Push(gtx.Ops)
			icon.Layout(gtx, 18, th.Pal.Text)
			o.Pop()
			return D{Size: image.Pt(s, s)}
		}),
		layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontSemiBold), MaxLines: 1}.Widget(th, title)),
				layout.Rigid(func(gtx C) D {
					if text == "" {
						return D{}
					}
					return gui.Text{Style: th.Small, Color: th.Pal.Text2}.Layout(gtx, th, text)
				}),
			)
		}),
		layout.Rigid(func(gtx C) D {
			if trail == nil {
				return D{}
			}
			return layout.Inset{Left: gui.Sp8}.Layout(gtx, trail)
		}),
	)
}
