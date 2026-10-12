package gui

import (
	"image"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// modal is the shared state of window-covering layers: open flag, the
// enter/leave animation, the scrim and Escape handling.
type modal struct {
	open      bool
	anim      Anim
	scrim     int // tags
	keys      int
	dismissed bool
	focused   bool
	close     Clickable
}

func (m *modal) setOpen(v bool) {
	if v && !m.open {
		m.focused = false
	}
	m.open = v
}

// progress advances the enter/leave animation (0 = hidden, 1 = shown).
func (m *modal) progress(gtx C, d time.Duration) float32 {
	m.anim.Duration, m.anim.Ease = d, EaseOutCubic
	if !m.open {
		m.anim.Ease = EaseInCubic
	}
	return m.anim.Animate(gtx, b2f(m.open))
}

// events handles Escape and scrim presses; dismissable controls whether they
// close the layer.
func (m *modal) events(gtx C, dismissable bool) {
	if m.close.Clicked(gtx) {
		m.open, m.dismissed = false, true
	}
	for {
		e, ok := gtx.Event(
			key.FocusFilter{Target: &m.keys},
			key.Filter{Focus: &m.keys, Name: key.NameEscape},
			pointer.Filter{Target: &m.scrim, Kinds: pointer.Press},
		)
		if !ok {
			break
		}
		if !dismissable || !m.open {
			continue
		}
		switch e := e.(type) {
		case key.Event:
			if e.State == key.Press {
				m.open, m.dismissed = false, true
				gtx.Execute(op.InvalidateCmd{})
			}
		case pointer.Event:
			if e.Kind == pointer.Press {
				m.open, m.dismissed = false, true
				gtx.Execute(op.InvalidateCmd{})
			}
		}
	}
}

// scrimLayer paints the dimmed backdrop (alpha a) over the window and
// registers the modal's scrim and key tags, blocking the content below.
func (m *modal) scrimLayer(gtx C, th *Theme, a float32) {
	win := gtx.Constraints.Max
	r := image.Rectangle{Max: win}
	st := clip.Rect(r).Push(gtx.Ops)
	paint.ColorOp{Color: WithAlpha(th.Pal.Scrim, a)}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	event.Op(gtx.Ops, &m.scrim)
	event.Op(gtx.Ops, &m.keys)
	st.Pop()
	if m.open && !m.focused {
		m.focused = true
		gtx.Execute(key.FocusCmd{Tag: &m.keys})
	}
}

// block registers the modal's key tag over a surface so presses inside it do
// not count as scrim presses.
func (m *modal) block(gtx C, size image.Point) {
	st := clip.Rect(rectWH(size.X, size.Y)).Push(gtx.Ops)
	event.Op(gtx.Ops, &m.keys)
	st.Pop()
}

// DialogStyle configures a Dialog for one frame.
type DialogStyle struct {
	Title string
	Text  string
	Icon  *Icon // shown in a tone circle above the title
	Tone  Tone  // icon tone (ToneError for destructive confirmations)
	Width unit.Dp
	// Body is extra content under the text (forms, lists).
	Body layout.Widget
	// Actions are the dialog's buttons, laid out right-aligned (the primary
	// action last). Their Clickables belong to the caller.
	Actions []layout.Widget
	// NoDismiss disables Escape, the scrim click and the close button.
	NoDismiss bool
}

// Dialog is a centred modal card (confirmations, short forms). It scales in
// from 0.96 and fades in 180 ms over a dimmed scrim; Escape, a scrim click or
// the × close it (unless NoDismiss).
//
//	if s.del.Clicked(gtx) { s.confirm.Open() }
//	s.confirm.Layout(gtx, th, gui.DialogStyle{Title: "Видалити проєкт?", Icon: gui.IconTrash2,
//	    Tone: gui.ToneError, Actions: []layout.Widget{cancelBtn, deleteBtn}})
type Dialog struct {
	m modal
}

// Open shows the dialog.
func (d *Dialog) Open() { d.m.setOpen(true) }

// Close hides the dialog (animated).
func (d *Dialog) Close() { d.m.setOpen(false) }

// IsOpen reports whether the dialog is open.
func (d *Dialog) IsOpen() bool { return d.m.open }

// Dismissed reports (once) that the user closed the dialog with Escape, the
// scrim or the × button.
func (d *Dialog) Dismissed() bool {
	v := d.m.dismissed
	d.m.dismissed = false
	return v
}

// Layout queues the dialog above the window content (see Theme.Layer).
func (d *Dialog) Layout(gtx C, th *Theme, st DialogStyle) {
	d.m.events(gtx, !st.NoDismiss)
	a := d.m.progress(gtx, DurDialog)
	if a <= 0.001 && !d.m.open {
		return
	}
	th.Layer(gtx, func(gtx C) {
		d.m.events(gtx, !st.NoDismiss)
		d.draw(gtx, th, st, a)
	})
}

func (d *Dialog) draw(gtx C, th *Theme, st DialogStyle, a float32) {
	win := gtx.Constraints.Max
	d.m.scrimLayer(gtx, th, a)
	width := st.Width
	if width == 0 {
		width = 480
	}
	margin := gtx.Dp(24)
	w := min(gtx.Dp(width), win.X-2*margin)
	pad := gtx.Dp(28)
	cg := gtx
	cg.Constraints = layout.Constraints{Min: pt(w-2*pad, 0), Max: pt(w-2*pad, win.Y-2*margin-2*pad)}
	cd, call := record(cg, func(gtx C) D { return d.content(gtx, th, st) })
	h := cd.Size.Y + 2*pad
	x, y := (win.X-w)/2, max(margin, (win.Y-h)/2-gtx.Dp(16))
	s := 0.96 + 0.04*a
	c := f32.Pt(float32(x)+float32(w)/2, float32(y)+float32(h)/2)
	tr := op.Affine(f32.AffineId().Scale(c, f32.Pt(s, s))).Push(gtx.Ops)
	defer tr.Pop()
	defer opacity(gtx, a)()
	o := op.Offset(pt(x, y)).Push(gtx.Ops)
	defer o.Pop()
	r := rectWH(w, h)
	Shadow(gtx, th, r, RadiusCard, ElevSheet)
	Box(gtx, r, RadiusCard, th.Pal.Card, WithAlpha(th.Pal.Border, 0.6))
	d.m.block(gtx, r.Size())
	t := op.Offset(pt(pad, pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	if !st.NoDismiss {
		b := gtx.Dp(36)
		t := op.Offset(pt(w-gtx.Dp(14)-b, gtx.Dp(14))).Push(gtx.Ops)
		IconButton{Icon: IconX, Size: 36, IconSize: 20, Tip: "Закрити"}.Layout(gtx, th, &d.m.close)
		t.Pop()
	}
}

func (d *Dialog) content(gtx C, th *Theme, st DialogStyle) D {
	var ch []layout.FlexChild
	if st.Icon != nil {
		ch = append(ch, layout.Rigid(func(gtx C) D {
			tone := st.Tone
			if tone == ToneNeutral {
				tone = ToneAction
			}
			// The readable ink tone on its soft fill (≥ 4.5:1).
			_, soft, ink := th.Pal.ToneColors(tone)
			sz := gtx.Dp(52)
			fillCircle(gtx.Ops, f32.Pt(float32(sz)/2, float32(sz)/2), float32(sz)/2, soft)
			ic := gtx.Dp(26)
			o := op.Offset(pt((sz-ic)/2, (sz-ic)/2)).Push(gtx.Ops)
			st.Icon.Layout(gtx, 26, ink)
			o.Pop()
			return D{Size: pt(sz, sz)}
		}), layout.Rigid(layout.Spacer{Height: Sp16}.Layout))
	}
	if st.Title != "" {
		ch = append(ch, layout.Rigid(func(gtx C) D {
			gtx.Constraints.Max.X -= gtx.Dp(40) // room for the close button
			return Text{Style: th.H2.Sized(22)}.Layout(gtx, th, st.Title)
		}))
	}
	if st.Text != "" {
		ch = append(ch, layout.Rigid(layout.Spacer{Height: Sp8}.Layout),
			layout.Rigid(Text{Style: th.Body, Color: th.Pal.Text2}.Widget(th, st.Text)))
	}
	if st.Body != nil {
		ch = append(ch, layout.Rigid(layout.Spacer{Height: Sp20}.Layout), layout.Rigid(st.Body))
	}
	if len(st.Actions) > 0 {
		ch = append(ch, layout.Rigid(layout.Spacer{Height: Sp24}.Layout), layout.Rigid(func(gtx C) D {
			return layout.Flex{Spacing: layout.SpaceStart, Alignment: layout.Middle}.Layout(gtx, actionRow(st.Actions)...)
		}))
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

func actionRow(ws []layout.Widget) []layout.FlexChild {
	var ch []layout.FlexChild
	for i, w := range ws {
		if i > 0 {
			ch = append(ch, layout.Rigid(layout.Spacer{Width: Sp12}.Layout))
		}
		ch = append(ch, layout.Rigid(w))
	}
	return ch
}

// DrawerStyle configures a Drawer for one frame.
type DrawerStyle struct {
	Title    string
	Subtitle string
	Width    unit.Dp       // 0 = 440
	Body     layout.Widget // scrolls when taller than the window
	Footer   layout.Widget // pinned at the bottom (actions)
	// NoDismiss disables Escape, the scrim click and the close button.
	NoDismiss bool
}

// Drawer is a sheet that slides in from the right edge (240 ms) over a
// dimmed scrim — the queue, details, secondary settings.
type Drawer struct {
	m      modal
	scroll Scroll
}

// Open shows the drawer.
func (d *Drawer) Open() { d.m.setOpen(true) }

// Close hides the drawer (animated).
func (d *Drawer) Close() { d.m.setOpen(false) }

// Toggle opens a closed drawer and closes an open one.
func (d *Drawer) Toggle() { d.m.setOpen(!d.m.open) }

// IsOpen reports whether the drawer is open.
func (d *Drawer) IsOpen() bool { return d.m.open }

// Dismissed reports (once) that the user closed the drawer.
func (d *Drawer) Dismissed() bool {
	v := d.m.dismissed
	d.m.dismissed = false
	return v
}

// Layout queues the drawer above the window content.
func (d *Drawer) Layout(gtx C, th *Theme, st DrawerStyle) {
	d.m.events(gtx, !st.NoDismiss)
	a := d.m.progress(gtx, DurSheet)
	if a <= 0.001 && !d.m.open {
		return
	}
	th.Layer(gtx, func(gtx C) {
		d.m.events(gtx, !st.NoDismiss)
		d.draw(gtx, th, st, a)
	})
}

func (d *Drawer) draw(gtx C, th *Theme, st DrawerStyle, a float32) {
	win := gtx.Constraints.Max
	d.m.scrimLayer(gtx, th, a)
	width := st.Width
	if width == 0 {
		width = 440
	}
	w := min(gtx.Dp(width), win.X-gtx.Dp(48))
	gap := gtx.Dp(12)
	h := win.Y - 2*gap
	x := win.X - gap - w + int(float32(w+2*gap)*(1-a))
	o := op.Offset(pt(x, gap)).Push(gtx.Ops)
	defer o.Pop()
	r := rectWH(w, h)
	Shadow(gtx, th, r, RadiusCard, ElevSheet)
	Box(gtx, r, RadiusCard, th.Pal.Card, WithAlpha(th.Pal.Border, 0.6))
	d.m.block(gtx, r.Size())
	cl := clip.UniformRRect(r, gtx.Dp(RadiusCard)).Push(gtx.Ops)
	defer cl.Pop()
	pad := gtx.Dp(24)
	// Header.
	hg := gtx
	hg.Constraints = layout.Constraints{Min: pt(w-2*pad, 0), Max: pt(w-2*pad-gtx.Dp(44), h)}
	hd, hc := record(hg, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(Text{Style: th.H3.Sized(20)}.Widget(th, st.Title)),
			layout.Rigid(func(gtx C) D {
				if st.Subtitle == "" {
					return D{}
				}
				return layout.Inset{Top: Sp4}.Layout(gtx, Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, st.Subtitle))
			}),
		)
	})
	t := op.Offset(pt(pad, pad)).Push(gtx.Ops)
	hc.Add(gtx.Ops)
	t.Pop()
	if !st.NoDismiss {
		b := gtx.Dp(36)
		t := op.Offset(pt(w-gtx.Dp(16)-b, pad-gtx.Dp(6))).Push(gtx.Ops)
		IconButton{Icon: IconX, Size: 36, IconSize: 20, Tip: "Закрити"}.Layout(gtx, th, &d.m.close)
		t.Pop()
	}
	top := pad + hd.Size.Y + gtx.Dp(16)
	bottom := h
	if st.Footer != nil {
		fg := gtx
		fg.Constraints = layout.Constraints{Min: pt(w-2*pad, 0), Max: pt(w-2*pad, h/2)}
		fd, fc := record(fg, st.Footer)
		fh := fd.Size.Y + 2*gtx.Dp(16)
		bottom = h - fh
		FillRect(gtx, image.Rect(0, bottom, w, bottom+max(1, gtx.Dp(1))), th.Pal.Border)
		t := op.Offset(pt(pad, bottom+gtx.Dp(16))).Push(gtx.Ops)
		fc.Add(gtx.Ops)
		t.Pop()
	}
	if st.Body != nil {
		bg := gtx
		bg.Constraints = layout.Exact(pt(w, max(0, bottom-top)))
		t := op.Offset(pt(0, top)).Push(gtx.Ops)
		d.scroll.LayoutWidget(bg, th, func(gtx C) D {
			return layout.Inset{Left: unit.Dp(float32(pad) / gtx.Metric.PxPerDp), Right: unit.Dp(float32(pad) / gtx.Metric.PxPerDp), Bottom: Sp24}.Layout(gtx, st.Body)
		})
		t.Pop()
	}
}
