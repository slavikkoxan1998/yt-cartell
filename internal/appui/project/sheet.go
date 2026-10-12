package project

import (
	"image"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"ytcartell/internal/gui"
)

// sideSheet is the context column as an overlay for narrower windows: a
// panel on the warm ground (not a white sheet, so the context cards inside
// never nest in a card) that slides in from the right over a scrim in 240 ms.
// Escape, the scrim or × close it.
type sideSheet struct {
	open    bool
	focused bool
	anim    gui.Anim
	scroll  gui.Scroll
	close   gui.Clickable
	scrim   int // tags
	keys    int
}

func (s *sideSheet) Open() {
	if !s.open {
		s.open, s.focused = true, false
	}
}
func (s *sideSheet) Close()       { s.open = false }
func (s *sideSheet) Toggle()      { s.open = !s.open; s.focused = false }
func (s *sideSheet) IsOpen() bool { return s.open }

func (s *sideSheet) events(gtx C) {
	if s.close.Clicked(gtx) {
		s.open = false
	}
	for {
		e, ok := gtx.Event(
			key.FocusFilter{Target: &s.keys},
			key.Filter{Focus: &s.keys, Name: key.NameEscape},
			pointer.Filter{Target: &s.scrim, Kinds: pointer.Press},
		)
		if !ok {
			break
		}
		switch e := e.(type) {
		case key.Event:
			if e.State == key.Press && s.open {
				s.open = false
				gtx.Execute(op.InvalidateCmd{})
			}
		case pointer.Event:
			if e.Kind == pointer.Press && s.open {
				s.open = false
				gtx.Execute(op.InvalidateCmd{})
			}
		}
	}
}

// Layout queues the sheet above the window (th.Layer) with title and body.
func (s *sideSheet) Layout(gtx C, th *gui.Theme, title string, width unit.Dp, body layout.Widget) {
	s.events(gtx)
	s.anim.Duration = gui.DurSheet
	if s.open {
		s.anim.Ease = gui.EaseOutCubic
	} else {
		s.anim.Ease = gui.EaseInCubic
	}
	a := s.anim.Animate(gtx, b2f(s.open))
	if a <= 0.001 && !s.open {
		return
	}
	th.Layer(gtx, func(gtx C) {
		s.events(gtx)
		win := gtx.Constraints.Max
		// Scrim: dims the page and takes the press that closes the sheet.
		sc := clip.Rect(image.Rectangle{Max: win}).Push(gtx.Ops)
		paint.ColorOp{Color: gui.WithAlpha(th.Pal.Scrim, a)}.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		if s.open {
			event.Op(gtx.Ops, &s.scrim)
		}
		event.Op(gtx.Ops, &s.keys)
		sc.Pop()
		if s.open && !s.focused {
			s.focused = true
			gtx.Execute(key.FocusCmd{Tag: &s.keys})
		}
		w := min(gtx.Dp(width), win.X-gtx.Dp(40))
		x := win.X - w + int(float32(w+gtx.Dp(24))*(1-a))
		o := op.Offset(image.Pt(x, 0)).Push(gtx.Ops)
		defer o.Pop()
		// The panel: warm ground with a soft shadow on its open edge; the
		// right corners sit off-screen.
		rr := image.Rect(0, 0, w+gtx.Dp(gui.RadiusCard)+2, win.Y)
		gui.Shadow(gtx, th, rr, gui.RadiusCard, gui.ElevSheet)
		gui.Box(gtx, rr, gui.RadiusCard, th.Pal.Ground, gui.WithAlpha(th.Pal.Border, 0.8))
		blk := clip.Rect(image.Rect(0, 0, w, win.Y)).Push(gtx.Ops)
		event.Op(gtx.Ops, &s.keys) // presses inside are not scrim presses
		blk.Pop()
		pad := gtx.Dp(gui.Sp20)
		// Header: title and ×.
		hg := gtx
		hg.Constraints = layout.Constraints{Max: image.Pt(w-2*pad-gtx.Dp(44), win.Y)}
		m := op.Record(gtx.Ops)
		hd := gui.Text{Style: th.H3.Sized(20), MaxLines: 1}.Layout(hg, th, title)
		hc := m.Stop()
		top := pad + gtx.Dp(4)
		t := op.Offset(image.Pt(pad, top)).Push(gtx.Ops)
		hc.Add(gtx.Ops)
		t.Pop()
		b := gtx.Dp(36)
		t = op.Offset(image.Pt(w-pad-b+gtx.Dp(6), top+(hd.Size.Y-b)/2)).Push(gtx.Ops)
		gui.IconButton{Icon: gui.IconX, Size: 36, IconSize: 20, Tip: "Закрити"}.Layout(gtx, th, &s.close)
		t.Pop()
		bodyTop := top + max(hd.Size.Y, b) + gtx.Dp(gui.Sp12)
		bg := gtx
		bg.Constraints = layout.Exact(image.Pt(w, max(0, win.Y-bodyTop)))
		t = op.Offset(image.Pt(0, bodyTop)).Push(gtx.Ops)
		s.scroll.LayoutWidget(bg, th, func(gtx C) D {
			in := layout.Inset{Left: dp(gtx, pad), Right: dp(gtx, pad), Bottom: gui.Sp24}
			return in.Layout(gtx, func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return body(gtx)
			})
		})
		t.Pop()
	})
}
