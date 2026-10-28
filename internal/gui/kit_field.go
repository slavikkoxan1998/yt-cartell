package gui

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"strings"
	"unicode/utf8"

	"gioui.org/gesture"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
)

// FieldStyle configures a TextField for one frame.
type FieldStyle struct {
	Label       string // above the field
	Placeholder string
	Hint        string // helper text under the field
	Error       string // replaces Hint in red; the border turns red
	// MaxLen > 0 limits the length and shows a "12/200" counter.
	MaxLen int
	// Counter shows the character count even without MaxLen.
	Counter bool
	// Password masks the text and adds a show/hide button.
	Password bool
	// PasteButton shows a clipboard paste button on the right side of the box.
	PasteButton bool
	// MinLines > 0 makes a multi-line TextArea that grows with its text up to
	// MaxLines (0 = no limit) and then scrolls.
	MinLines, MaxLines int
	Leading            *Icon // icon inside the field, before the text
	// Submit makes Enter a submit (Submitted) instead of a newline.
	Submit   bool
	Style    TextStyle // text style; zero = th.Body
	Disabled bool
	Force    Force
	// Pending marks the runes [Pending[0], Pending[1]) as text that may still
	// change (live dictation): faded, with a dotted underline. Zero = none.
	Pending [2]int
}

// TextField is a text input bound to a model string. Every frame:
//
//	if v, ok := s.title.Changed(gtx); ok { p.Title = v }   // user edits
//	s.title.Sync(gtx, p.Title)                              // model → field (never while typing)
//	s.title.Layout(gtx, th, gui.FieldStyle{Label: "Назва", Placeholder: "Моя історія"})
//
// The embedded widget.Editor exposes Filter, SetCaret, Insert etc.
type TextField struct {
	widget.Editor

	last      string
	pend      pending[string]
	submitted bool
	hover     gesture.Hover
	click     gesture.Click
	focusA    Anim
	hoverA    Anim
	reveal    Clickable
	pasteBtn  Clickable
	shown     bool
}

// Sync puts the model value into the field unless the user is typing in it
// or it already shows that value.
func (f *TextField) Sync(gtx C, value string) {
	if value == f.last || gtx.Focused(&f.Editor) || f.pend.ok {
		return
	}
	f.SetText(value)
}

// SetText replaces the text (not reported by Changed).
func (f *TextField) SetText(s string) {
	f.last = s
	if f.Editor.Text() != s {
		f.Editor.SetText(s)
	}
}

// Changed reports the text after a user edit.
func (f *TextField) Changed(gtx C) (string, bool) {
	f.update(gtx)
	return f.pend.take()
}

// Submitted reports (once) that Enter was pressed in a Submit field.
func (f *TextField) Submitted(gtx C) bool {
	f.update(gtx)
	s := f.submitted
	f.submitted = false
	return s
}

// Focused reports whether the field has keyboard focus.
func (f *TextField) Focused(gtx C) bool { return gtx.Focused(&f.Editor) }

// Focus moves keyboard focus into the field.
func (f *TextField) Focus(gtx C) { gtx.Execute(key.FocusCmd{Tag: &f.Editor}) }

func (f *TextField) update(gtx C) {
	for {
		ev, ok := f.Editor.Update(gtx)
		if !ok {
			break
		}
		switch ev.(type) {
		case widget.ChangeEvent:
			// SetText reports a change too; only user edits differ from the
			// last synced or reported text.
			t := f.Editor.Text()
			if t == f.last {
				continue
			}
			f.last = t
			f.pend.set(gtx, t)
		case widget.SubmitEvent:
			f.submitted = true
			gtx.Execute(op.InvalidateCmd{})
		}
	}
	for {
		e, ok := f.click.Update(gtx.Source)
		if !ok {
			break
		}
		if e.Kind == gesture.KindPress && !gtx.Focused(&f.Editor) {
			gtx.Execute(key.FocusCmd{Tag: &f.Editor})
		}
	}
	if f.reveal.Clicked(gtx) {
		f.shown = !f.shown
	}
	if f.pasteBtn.Clicked(gtx) {
		gtx.Execute(key.FocusCmd{Tag: &f.Editor})
		if cb := ReadSystemClipboard(); cb != "" {
			f.Editor.Insert(cb)
			t := f.Editor.Text()
			f.last = t
			f.pend.set(gtx, t)
			f.submitted = true
			gtx.Execute(op.InvalidateCmd{})
		} else {
			gtx.Execute(clipboard.ReadCmd{Tag: &f.Editor})
		}
	}
	// Cyrillic & Latin shortcuts for layout-independent copy, paste, cut & select all
	filters := []event.Filter{
		// Paste (Ctrl+V / Ctrl+М)
		key.Filter{Focus: &f.Editor, Name: "М", Required: key.ModShortcut},
		key.Filter{Focus: &f.Editor, Name: "м", Required: key.ModShortcut},
		key.Filter{Focus: &f.Editor, Name: "V", Required: key.ModShortcut},
		key.Filter{Focus: &f.Editor, Name: "v", Required: key.ModShortcut},
		// Copy (Ctrl+C / Ctrl+С)
		key.Filter{Focus: &f.Editor, Name: "С", Required: key.ModShortcut},
		key.Filter{Focus: &f.Editor, Name: "с", Required: key.ModShortcut},
		key.Filter{Focus: &f.Editor, Name: "C", Required: key.ModShortcut},
		key.Filter{Focus: &f.Editor, Name: "c", Required: key.ModShortcut},
		// Cut (Ctrl+X / Ctrl+Ч)
		key.Filter{Focus: &f.Editor, Name: "Ч", Required: key.ModShortcut},
		key.Filter{Focus: &f.Editor, Name: "ч", Required: key.ModShortcut},
		key.Filter{Focus: &f.Editor, Name: "X", Required: key.ModShortcut},
		key.Filter{Focus: &f.Editor, Name: "x", Required: key.ModShortcut},
		// Select All (Ctrl+A / Ctrl+Ф)
		key.Filter{Focus: &f.Editor, Name: "Ф", Required: key.ModShortcut},
		key.Filter{Focus: &f.Editor, Name: "ф", Required: key.ModShortcut},
		key.Filter{Focus: &f.Editor, Name: "A", Required: key.ModShortcut},
		key.Filter{Focus: &f.Editor, Name: "a", Required: key.ModShortcut},
	}
	for {
		ke, ok := gtx.Event(filters...)
		if !ok {
			break
		}
		if k, ok := ke.(key.Event); ok && k.State == key.Press {
			switch strings.ToUpper(string(k.Name)) {
			case "М", "V":
				if !f.Editor.ReadOnly {
					if cb := ReadSystemClipboard(); cb != "" {
						f.Editor.Insert(cb)
						t := f.Editor.Text()
						f.last = t
						f.pend.set(gtx, t)
						gtx.Execute(op.InvalidateCmd{})
					} else {
						gtx.Execute(clipboard.ReadCmd{Tag: &f.Editor})
					}
				}
			case "С", "C":
				sel := f.Editor.SelectedText()
				if sel != "" {
					WriteSystemClipboard(sel)
					gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(sel))})
				}
			case "Ч", "X":
				if !f.Editor.ReadOnly {
					sel := f.Editor.SelectedText()
					if sel != "" {
						WriteSystemClipboard(sel)
						gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(sel))})
						f.Editor.Delete(1)
						t := f.Editor.Text()
						f.last = t
						f.pend.set(gtx, t)
						gtx.Execute(op.InvalidateCmd{})
					}
				}
			case "Ф", "A":
				f.Editor.SetCaret(0, f.Editor.Len())
				gtx.Execute(op.InvalidateCmd{})
			}
		}
	}
}

// Layout draws the label, the field, and the hint/error/counter row.
func (f *TextField) Layout(gtx C, th *Theme, st FieldStyle) D {
	return fieldFrame(gtx, th, st, utf8.RuneCountInString(f.Editor.Text()), func(gtx C) D {
		return f.layoutBox(gtx, th, st, RadiusControl, false, 0)
	})
}

// fieldFrame stacks label, box and the hint/counter row.
func fieldFrame(gtx C, th *Theme, st FieldStyle, count int, box layout.Widget) D {
	var ch []layout.FlexChild
	if st.Label != "" {
		ch = append(ch,
			layout.Rigid(Text{Style: th.Label, Color: th.Pal.Text}.Widget(th, st.Label)),
			layout.Rigid(layout.Spacer{Height: Sp8}.Layout))
	}
	ch = append(ch, layout.Rigid(box))
	counter := st.MaxLen > 0 || st.Counter
	if st.Error != "" || st.Hint != "" || counter {
		ch = append(ch, layout.Rigid(layout.Spacer{Height: 6}.Layout), layout.Rigid(func(gtx C) D {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					switch {
					case st.Error != "":
						return layout.Flex{Alignment: layout.Start}.Layout(gtx,
							layout.Rigid(func(gtx C) D {
								return layout.Inset{Top: 1, Right: 6}.Layout(gtx, IconCircleAlert.Widget(16, th.Pal.ErrorInk))
							}),
							layout.Flexed(1, Text{Style: th.Small, Color: th.Pal.ErrorInk}.Widget(th, st.Error)),
						)
					case st.Hint != "":
						return Text{Style: th.Small, Color: th.Pal.Text3}.Layout(gtx, th, st.Hint)
					}
					return D{Size: pt(gtx.Constraints.Max.X, 0)}
				}),
				layout.Rigid(func(gtx C) D {
					if !counter {
						return D{}
					}
					txt := fmt.Sprintf("%d %s", count, PluralUK(count, "символ", "символи", "символів"))
					c := th.Pal.Text3
					if st.MaxLen > 0 {
						txt = fmt.Sprintf("%d/%d", count, st.MaxLen)
						if count >= st.MaxLen {
							c = th.Pal.WarningInk
						}
					}
					return layout.Inset{Left: Sp12}.Layout(gtx, Text{Style: th.Caption, Color: c, MaxLines: 1}.Widget(th, txt))
				}),
			)
		}))
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

// layoutBox draws the input box. pill makes it fully rounded (search).
func (f *TextField) layoutBox(gtx C, th *Theme, st FieldStyle, radius unit.Dp, tinted bool, trailW int) D {
	if st.Disabled {
		gtx = gtx.Disabled()
	}
	f.update(gtx)
	ts := st.Style
	if ts.Size == 0 {
		ts = th.Body
	}
	multi := st.MinLines > 0
	f.Editor.SingleLine = !multi
	f.Editor.Submit = st.Submit || !multi
	f.Editor.LineHeightScale = ts.lineHeight()
	if st.MaxLen > 0 {
		f.Editor.MaxLen = st.MaxLen
	}
	f.Editor.Mask = 0
	if st.Password && !f.shown {
		f.Editor.Mask = '•'
	}
	hovered := f.hover.Update(gtx.Source) || st.Force&ForceHover != 0
	focused := gtx.Focused(&f.Editor) || st.Force&ForceFocus != 0
	f.focusA.Duration, f.hoverA.Duration = DurHover*3/2, DurHover
	fa := f.focusA.Animate(gtx, b2f(focused))
	ha := f.hoverA.Animate(gtx, b2f(hovered && gtx.Enabled()))

	p := &th.Pal
	bg := p.Card
	bd := LerpColor(p.Border, p.BorderStrong, ha)
	if tinted {
		bg = LerpColor(LerpColor(p.Surface, p.SurfaceHover, ha), p.Card, fa)
		bd = LerpColor(WithAlpha(p.Border, 0), p.Border, ha*(1-fa))
	}
	accent := p.Action
	if st.Error != "" {
		accent = p.Error
		bd = p.Error
	}
	bd = LerpColor(bd, accent, fa)
	if st.Disabled {
		bg, bd = p.Surface, p.Border
	}

	padX := gtx.Dp(14)
	padY := gtx.Dp(11)
	w := gtx.Constraints.Max.X
	px := gtx.Sp(ts.Size)
	lineH := int(float32(px)*ts.lineHeight() + 0.5)
	firstH := int(float32(px)*1.2 + 0.5)
	minH, maxH := firstH, firstH
	if multi {
		minH = firstH + (st.MinLines-1)*lineH
		maxH = 1 << 20
		if st.MaxLines > 0 {
			maxH = firstH + (max(st.MaxLines, st.MinLines)-1)*lineH
		}
		maxH = min(maxH, max(minH, gtx.Constraints.Max.Y-2*padY))
	}
	lead := 0
	if st.Leading != nil {
		lead = gtx.Dp(18) + gtx.Dp(10)
	}
	trail := trailW
	if st.Password {
		trail += gtx.Dp(32) + gtx.Dp(4)
	}
	if st.PasteButton {
		trail += gtx.Dp(32) + gtx.Dp(4)
	}
	innerW := max(0, w-2*padX-lead-trail)
	eg := gtx
	eg.Constraints = layout.Constraints{Min: pt(innerW, minH), Max: pt(innerW, maxH)}
	fg := p.Text
	if st.Disabled {
		fg = p.Text2
	}
	boost := th.inkBoost(fg, ts.Size)
	sel := p.Selection
	if boost {
		sel = WithAlpha(sel, 0.55) // painted twice
	}
	ed, ec := record(eg, func(gtx C) D {
		return f.Editor.Layout(gtx, th.Shaper, ts.Font, ts.Size, colorMacro(gtx.Ops, fg), colorMacro(gtx.Ops, sel))
	})
	var hc op.CallOp
	if f.Editor.Len() == 0 && st.Placeholder != "" {
		maxLines := 0
		if !multi {
			maxLines = 1
		}
		_, hc = record(eg, Text{Style: ts, Color: p.Text3, MaxLines: maxLines}.Widget(th, st.Placeholder))
	}
	eh := max(ed.Size.Y, minH)
	h := eh + 2*padY
	if !multi {
		h = max(h, gtx.Dp(HeightMd))
	}
	size := pt(w, h)
	r := rectWH(w, h)
	// Focus glow: a soft ring that grows in with focus.
	if fa > 0.01 {
		ringf(gtx.Ops, frect(r), float32(gtx.Dp(radius)), 0, float32(gtx.Dp(3))*fa, WithAlpha(p.Focus, 0.9*fa))
	}
	fillRRf(gtx.Ops, frect(r), float32(gtx.Dp(radius)), bg)
	bw := float32(max(1, gtx.Dp(1))) + float32(gtx.Dp(1))*0.5*fa
	strokeRRf(gtx.Ops, frect(r), float32(gtx.Dp(radius)), bw, bd)

	// Whole-box pointer area: hover, text cursor, click-to-focus on padding.
	area := clip.Rect(r).Push(gtx.Ops)
	if st.Disabled {
		pointer.CursorDefault.Add(gtx.Ops)
	} else {
		f.hover.Add(gtx.Ops)
		f.click.Add(gtx.Ops)
		pointer.CursorText.Add(gtx.Ops)
	}
	if st.Leading != nil {
		ic := gtx.Dp(18)
		iy := (h - ic) / 2
		if multi {
			iy = padY + (firstH-ic)/2
		}
		o := op.Offset(pt(padX, iy)).Push(gtx.Ops)
		st.Leading.Layout(gtx, 18, LerpColor(p.Text3, p.Text2, fa))
		o.Pop()
	}
	yoff := padY
	if !multi {
		yoff = (h - ed.Size.Y) / 2
	}
	t := op.Offset(pt(padX+lead, yoff)).Push(gtx.Ops)
	hc.Add(gtx.Ops)
	ec.Add(gtx.Ops)
	if boost && !st.Disabled {
		ec.Add(gtx.Ops) // ink boost; handlers register twice over one area, harmlessly
	}
	if a, b := st.Pending[0], st.Pending[1]; b > a {
		f.pending(gtx, ed.Size, a, b, bg, p.Text3)
	}
	t.Pop()
	area.Pop()
	if st.Password {
		b := gtx.Dp(32)
		o := op.Offset(pt(w-padX/2-b, (h-b)/2)).Push(gtx.Ops)
		ic, tip := IconEye, "Показати"
		if f.shown {
			ic, tip = IconEyeOff, "Сховати"
		}
		IconButton{Icon: ic, Size: 32, IconSize: 18, Tip: tip, Disabled: st.Disabled}.Layout(gtx, th, &f.reveal)
		o.Pop()
	}
	if st.PasteButton {
		b := gtx.Dp(32)
		xPos := w - padX/2 - b
		if st.Password {
			xPos -= gtx.Dp(34)
		}
		o := op.Offset(pt(xPos, (h-b)/2)).Push(gtx.Ops)
		IconButton{Icon: IconClipboardPaste, Size: 32, IconSize: 18, Tip: "Вставити з буфера обміну", Disabled: st.Disabled}.Layout(gtx, th, &f.pasteBtn)
		o.Pop()
	}
	return D{Size: size, Baseline: h - yoff - (ed.Size.Y - ed.Baseline)}
}

// SearchField is a pill-shaped search input on the warm surface with a
// search icon and a clear button; Escape clears it.
//
//	if q, ok := s.search.Changed(gtx); ok { s.filter(q) }
//	s.search.Layout(gtx, th, "Пошук стилю")
type SearchField struct {
	TextField
	clear Clickable
	Force Force
}

// Query returns the current text.
func (s *SearchField) Query() string { return s.Editor.Text() }

// Layout draws the field across the available width.
func (s *SearchField) Layout(gtx C, th *Theme, placeholder string) D {
	if s.clear.Clicked(gtx) {
		s.Editor.SetText("")
		s.last = ""
		s.pend.set(gtx, "")
		s.Focus(gtx)
	}
	for {
		e, ok := gtx.Event(key.Filter{Focus: &s.Editor, Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, ok := e.(key.Event); ok && ke.State == key.Press && s.Editor.Len() > 0 {
			s.Editor.SetText("")
			s.last = ""
			s.pend.set(gtx, "")
		}
	}
	trail := 0
	if s.Editor.Len() > 0 {
		trail = gtx.Dp(28)
	}
	d := s.layoutBox(gtx, th, FieldStyle{Placeholder: placeholder, Leading: IconSearch, Force: s.Force}, RadiusPill, true, trail)
	if s.Editor.Len() > 0 {
		b := gtx.Dp(28)
		o := op.Offset(pt(d.Size.X-gtx.Dp(8)-b, (d.Size.Y-b)/2)).Push(gtx.Ops)
		IconButton{Icon: IconX, Size: 28, IconSize: 16, Tip: "Очистити"}.Layout(gtx, th, &s.clear)
		o.Pop()
	}
	return d
}

// pending fades the runes [a, b) of the laid-out editor (size: its visible
// area) to read as interim text and underlines them with dots.
func (f *TextField) pending(gtx C, size image.Point, a, b int, bg, ink color.NRGBA) {
	defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
	dot, gap, w := gtx.Dp(2), gtx.Dp(3), max(1, gtx.Dp(1))
	for _, r := range f.Editor.Regions(a, b, nil) {
		FillRect(gtx, r.Bounds, WithAlpha(bg, 0.55))
		y := r.Bounds.Max.Y - r.Baseline + gtx.Dp(2)
		for x := r.Bounds.Min.X; x < r.Bounds.Max.X; x += dot + gap {
			FillRect(gtx, image.Rect(x, y, min(x+dot, r.Bounds.Max.X), y+w), WithAlpha(ink, 0.8))
		}
	}
}
