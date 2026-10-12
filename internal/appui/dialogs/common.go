package dialogs

// Building blocks shared by the sheets: headings, info and error panels,
// numbered steps, profile chips, style thumbnails, toggle tiles, footers.

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

func record(gtx C, w layout.Widget) (D, op.CallOp) {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	return d, m.Stop()
}

// heading is a small sentence-case section title inside a sheet.
func heading(th *gui.Theme, text string) layout.Widget {
	return gui.Text{Style: th.Label}.Widget(th, text)
}

// flatTile is the inset tile used inside sheets (cards never nest): the
// warm surface fill, 12 dp corners, a hairline and no shadow — the look of
// the flat picker fields. It fills the width and honours Min.Y.
func flatTile(gtx C, th *gui.Theme, pad unit.Dp, disabled bool, w layout.Widget) D {
	p := gtx.Dp(pad)
	W := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{
		Min: image.Pt(max(0, W-2*p), max(0, gtx.Constraints.Min.Y-2*p)),
		Max: image.Pt(max(0, W-2*p), max(0, gtx.Constraints.Max.Y-2*p)),
	}
	if disabled {
		cg = cg.Disabled()
	}
	d, call := record(cg, w)
	size := image.Pt(W, max(gtx.Constraints.Min.Y, d.Size.Y+2*p))
	gui.Box(gtx, image.Rectangle{Max: size}, gui.RadiusControl, th.Pal.Surface, th.Pal.Border)
	o := op.Offset(image.Pt(p, p)).Push(gtx.Ops)
	if disabled {
		defer paint.PushOpacity(gtx.Ops, 0.45).Pop()
	}
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: size}
}

// flatVisual is a tile's leading icon on a card-coloured square (it has to
// stand out from the surface fill of a flat tile).
func flatVisual(th *gui.Theme, ic *gui.Icon) layout.Widget {
	return func(gtx C) D {
		s := gtx.Dp(44)
		gui.FillRRect(gtx, image.Rect(0, 0, s, s), gui.RadiusControl, th.Pal.Card)
		is := gtx.Dp(22)
		o := op.Offset(image.Pt((s-is)/2, (s-is)/2)).Push(gtx.Ops)
		ic.Layout(gtx, 22, th.Pal.Text)
		o.Pop()
		return D{Size: image.Pt(s, s)}
	}
}

// tileTexts is the label / value / caption column of a tile (the picker
// fields' typography).
func tileTexts(th *gui.Theme, label, value, caption string) layout.Widget {
	return func(gtx C) D {
		var ch []layout.FlexChild
		if label != "" {
			ch = append(ch,
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, label)),
				layout.Rigid(layout.Spacer{Height: 1}.Layout))
		}
		if value != "" {
			ch = append(ch, layout.Rigid(gui.Text{Style: th.BodyStrong.Sized(16).Lined(1.35), MaxLines: 1}.Widget(th, value)))
		}
		if caption != "" {
			ch = append(ch, layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 2}.Widget(th, caption)))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	}
}

// tileRowWrap is tileRow whose trailing buttons move under the texts when
// the tile is narrow (phones), so names are not squeezed.
func tileRowWrap(gtx C, visual, texts, trailing layout.Widget) D {
	if trailing == nil || gtx.Constraints.Max.X >= gtx.Dp(420) {
		return tileRow(gtx, visual, texts, trailing)
	}
	return gui.VStack(gtx, gui.Sp12,
		func(gtx C) D { return tileRow(gtx, visual, texts, nil) },
		func(gtx C) D {
			gtx.Constraints.Min.X = 0
			return layout.Inset{Left: 44 + gui.Sp12 + 2}.Layout(gtx, trailing)
		},
	)
}

// tileRow lays a tile's visual, texts and trailing widget in a row.
func tileRow(gtx C, visual, texts, trailing layout.Widget) D {
	var ch []layout.FlexChild
	if visual != nil {
		ch = append(ch, layout.Rigid(visual), layout.Rigid(layout.Spacer{Width: gui.Sp12 + 2}.Layout))
	}
	ch = append(ch, layout.Flexed(1, texts))
	if trailing != nil {
		ch = append(ch, layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout), layout.Rigid(trailing))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
}

// panel is a tinted box with an icon on the left and a text column.
func panel(gtx C, th *gui.Theme, bg, ink gui.Tone, icon *gui.Icon, title, text string) D {
	p := &th.Pal
	fill, iconC, titleC := p.Surface, p.Text2, p.Text
	switch bg {
	case gui.ToneError:
		fill, iconC, titleC = p.ErrorSoft, p.ErrorInk, p.ErrorInk
	case gui.ToneSuccess:
		fill, iconC, titleC = p.SuccessSoft, p.SuccessInk, p.SuccessInk
	case gui.ToneWarning:
		fill, iconC, titleC = p.WarningSoft, p.WarningInk, p.WarningInk
	}
	_ = ink
	pad := gtx.Dp(gui.Sp12 + 2)
	w := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*pad-gtx.Dp(30)), gtx.Constraints.Max.Y)}
	d, call := record(cg, func(gtx C) D {
		var ch []layout.FlexChild
		if title != "" {
			ch = append(ch, layout.Rigid(gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), Color: titleC}.Widget(th, title)))
		}
		if text != "" {
			if title != "" {
				ch = append(ch, layout.Rigid(layout.Spacer{Height: 2}.Layout))
			}
			col := p.Text2
			if bg == gui.ToneError {
				col = p.Text
			}
			ch = append(ch, layout.Rigid(gui.Text{Style: th.Small.Sized(14).Lined(1.45), Color: col}.Widget(th, text)))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
	h := d.Size.Y + 2*pad
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusControl, fill)
	o := op.Offset(image.Pt(pad, pad+1)).Push(gtx.Ops)
	icon.Layout(gtx, 18, iconC)
	o.Pop()
	o = op.Offset(image.Pt(pad+gtx.Dp(30), pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}

// errorLine shows err (plain Ukrainian) in a red panel; nothing when empty.
func errorLine(th *gui.Theme, msg *string, a *gui.Anim) layout.Widget {
	return func(gtx C) D {
		if *msg == "" {
			a.Set(0)
			return D{}
		}
		a.Duration = gui.DurDialog
		v := a.Animate(gtx, 1)
		return gui.FadeUp(gtx, v, 6, func(gtx C) D {
			return panel(gtx, th, gui.ToneError, gui.ToneError, gui.IconCircleAlert, "Не вийшло", *msg)
		})
	}
}

// steps is a short numbered how-to (icons in soft circles).
func steps(gtx C, th *gui.Theme, items ...stepItem) D {
	var ch []layout.FlexChild
	for i, it := range items {
		if i > 0 {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout))
		}
		it := it
		ch = append(ch, layout.Rigid(func(gtx C) D {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					sz := gtx.Dp(36)
					circle(gtx, sz, th.Pal.Surface)
					is := gtx.Dp(18)
					o := op.Offset(image.Pt((sz-is)/2, (sz-is)/2)).Push(gtx.Ops)
					it.icon.Layout(gtx, 18, th.Pal.Text)
					o.Pop()
					return D{Size: image.Pt(sz, sz)}
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium)}.Widget(th, it.title)),
						layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, it.text)),
					)
				}),
			)
		}))
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

type stepItem struct {
	icon        *gui.Icon
	title, text string
}

func circle(gtx C, size int, col color.NRGBA) {
	defer clip.Ellipse{Max: image.Pt(size, size)}.Push(gtx.Ops).Pop()
	paint.ColorOp{Color: col}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// styleThumb is a rounded square preview of a style (a brush for a custom one).
func styleThumb(gtx C, c *core.Ctx, id, custom string, size unit.Dp) D {
	th := c.Th
	px := gtx.Dp(size)
	sz := image.Pt(px, px)
	g := gtx
	g.Constraints = layout.Exact(sz)
	if strings.TrimSpace(custom) == "" {
		if img, ok := pickers.StylePreview(c, id); ok {
			return gui.Thumb{Image: img, Radius: gui.RadiusControl}.Layout(g, th)
		}
	}
	return gui.Thumb{Radius: gui.RadiusControl, Placeholder: gui.IconPaintbrush}.Layout(g, th)
}

// styleName is the label of a profile's look.
func styleName(pr config.Profile) string {
	if strings.TrimSpace(pr.CustomStyle) != "" {
		return "Свій стиль"
	}
	if pr.Style == "" {
		return "Стиль не вибрано"
	}
	return pickers.StyleLabel(pr.Style)
}

// summary is one line about a profile: "16:9 · 5 хв · Українська".
func summary(pr config.Profile) string {
	parts := []string{pr.Aspect}
	if pr.TargetSeconds > 0 {
		parts = append(parts, core.Duration(pr.TargetSeconds))
	}
	if pr.Language != "" {
		parts = append(parts, pickers.LanguageLabel(pr.Language))
	}
	if pr.Genre != "" {
		if ch, ok := studio.ChoiceByID(studio.Genres, pr.Genre); ok {
			parts = append(parts, ch.Label)
		} else {
			parts = append(parts, pr.Genre)
		}
	}
	if pr.Pipeline != "" {
		parts = append(parts, studio.PipelineLabel(pr.Pipeline))
	}
	return strings.Join(parts, " · ")
}

// toggleTile is a flat picker-style tile with a switch (voice-over, 3×3 …).
func toggleTile(gtx C, th *gui.Theme, t *gui.Toggle, icon *gui.Icon, label, onText, offText, caption string, on bool) D {
	value := offText
	if on {
		value = onText
	}
	return flatTile(gtx, th, gui.Sp16, t.Disabled, func(gtx C) D {
		return tileRow(gtx, flatVisual(th, icon), tileTexts(th, label, value, caption),
			func(gtx C) D { return t.Layout(gtx, th, on, "") })
	})
}

// sheetFoot is the pinned footer of a sheet: the error of the last action
// (if any) above the buttons, so it shows right where the user pressed.
// Buttons get full=true on phones, where they share the width.
func sheetFoot(gtx C, th *gui.Theme, msg *string, a *gui.Anim, left layout.Widget, buttons ...func(gtx C, full bool) D) D {
	nar := narrow(gtx)
	row := func(gtx C) D {
		if nar {
			var ch []layout.FlexChild
			for i, b := range buttons {
				b := b
				if i > 0 {
					ch = append(ch, layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout))
				}
				ch = append(ch, layout.Flexed(1, func(gtx C) D { return b(gtx, true) }))
			}
			return layout.Flex{}.Layout(gtx, ch...)
		}
		ws := make([]layout.Widget, len(buttons))
		for i, b := range buttons {
			b := b
			ws[i] = func(gtx C) D { return b(gtx, false) }
		}
		return footer(gtx, left, ws...)
	}
	if msg == nil || *msg == "" {
		if a != nil {
			a.Set(0)
		}
		return row(gtx)
	}
	return gui.VStack(gtx, gui.Sp12, errorLine(th, msg, a), row)
}

// footer lays out the pinned actions: left widget (optional), then the
// buttons right-aligned.
func footer(gtx C, left layout.Widget, buttons ...layout.Widget) D {
	var ch []layout.FlexChild
	if left != nil {
		ch = append(ch, layout.Rigid(left))
	}
	ch = append(ch, layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} }))
	for i, b := range buttons {
		if i > 0 {
			ch = append(ch, layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout))
		}
		ch = append(ch, layout.Rigid(b))
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
}

// narrow reports a phone-sized sheet.
func narrow(gtx C) bool { return gtx.Constraints.Max.X < gtx.Dp(440) }

func opOffset(gtx C, x, y int) op.TransformStack { return op.Offset(image.Pt(x, y)).Push(gtx.Ops) }

func pointXY(x, y int) image.Point { return image.Pt(x, y) }

// tileGrid lays picker tiles in equal columns at least minW wide; every
// tile of a row gets the row's height, and the tiles of a short last row
// share its whole width (no orphan half-rows).
func tileGrid(gtx C, minW unit.Dp, ws ...layout.Widget) D {
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
	gap := gtx.Dp(gui.Sp12)
	cols := max(1, min(n, (W+gap)/(gtx.Dp(minW)+gap)))
	y := 0
	for start := 0; start < n; start += cols {
		end := min(n, start+cols)
		k := end - start
		cw := (W - gap*(k-1)) / k
		h := 0
		for i := start; i < end; i++ {
			cg := gtx
			cg.Constraints = layout.Constraints{Min: image.Pt(cw, 0), Max: image.Pt(cw, gtx.Constraints.Max.Y)}
			d, _ := record(cg, list[i])
			h = max(h, d.Size.Y)
		}
		for i := start; i < end; i++ {
			cg := gtx
			cg.Constraints = layout.Constraints{Min: image.Pt(cw, h), Max: image.Pt(cw, h)}
			_, call := record(cg, list[i])
			o := op.Offset(image.Pt((i-start)*(cw+gap), y)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			o.Pop()
		}
		y += h
		if end < n {
			y += gap
		}
	}
	return D{Size: image.Pt(W, y)}
}
