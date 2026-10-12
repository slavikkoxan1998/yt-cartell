// Package pickers holds the shared choosers for project settings, used by
// Step 1 Сценарій (package project), the profile editor and the new-project /
// translate sheets (package dialogs), so every place looks and behaves the
// same:
//
//	Style     gallery (studio.Styles by studio.StyleCategories, preview image
//	          or palette tile, "підходить для …", search)
//	Language  "Авто" + studio.Languages (native + Ukrainian names, search)
//	Voice     narrator voices (c.Voices()) with a preview play button
//	Choice    a studio.Choice list (studio.Genres, Cameras, Tempos, Tones)
//	Duration  presets 15 с … 30 хв + custom seconds/minutes
//	Aspect    16:9 / 9:16 (the only formats the video models render)
//	ImageModel Nano Banana 2 (NARWHAL) / Pro (GEM_PIX_2) / 2 Lite (GEM_PIX)
//
// Every picker is a state struct you keep per field. Layout draws the field
// (a labelled, card-like button showing the current value; the gallery opens
// on press as a gui.BigPicker, the lists as a popover anchored to the field)
// and Changed reports — once — a value the user picked; apply it to your
// model:
//
//	if v, ok := s.style.Changed(); ok { c.Edit(id, func(p *studio.Project) { p.Input.Style = v }) }
//	s.style.Layout(gtx, c, "Стиль", p.Input.Style)
//
// The field is a white tile (gui.Card, 16 dp corners): a visual on the left
// (style preview, language code, voice play button, frame shape, icon), the
// label, the value in semibold and a caption, and a chevron that turns while
// the chooser is open. It fills the width it is given (grids, columns) and
// honours a minimum height (gui.Grid{Stretch: true}). Tune it per frame with
// the embedded Look (Disabled, Note, Tip) before calling Layout. label ""
// draws no label line (when a card header already names the setting).
//
// Until the model catches up with a pick (c.Edit → next snapshot) the field
// keeps showing the picked value, so it never flickers back for a frame.
package pickers

import (
	"image"
	"image/color"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"ytcartell/internal/gui"
)

type (
	C = layout.Context
	D = layout.Dimensions
)

// holdFor is how long a fresh pick is shown while the model has not caught
// up with it (the engine answers in the next frame; a refused value falls
// back after this).
const holdFor = 800 * time.Millisecond

type pick[T comparable] struct {
	val     T
	changed bool

	hold    T
	holding bool
	holdAt  time.Time
}

// Changed reports (and consumes) the value the user picked.
func (p *pick[T]) Changed() (T, bool) {
	v, ok := p.val, p.changed
	p.changed = false
	return v, ok
}

// set records a pick (UI goroutine, inside Layout).
func (p *pick[T]) set(gtx C, v T) {
	p.val, p.changed = v, true
	p.hold, p.holding, p.holdAt = v, true, gtx.Now
	gtx.Execute(op.InvalidateCmd{})
}

// shown is the value to draw: a fresh pick until the model reports it.
func (p *pick[T]) shown(gtx C, model T) T {
	if !p.holding {
		return model
	}
	if model == p.hold || gtx.Now.Sub(p.holdAt) >= holdFor {
		p.holding = false
		return model
	}
	gtx.Execute(op.InvalidateCmd{At: p.holdAt.Add(holdFor)})
	return p.hold
}

// Look tunes a picker field for one frame (set it before Layout).
type Look struct {
	// Disabled dims the field; it does not open.
	Disabled bool
	// Note replaces the caption under the value (why it is disabled, a
	// derived fact like "≈ 8 сцен по 8 с").
	Note string
	// Tip is the tooltip of the field.
	Tip string
	// Flat draws the field as a flat inset control (Surface fill, 12 dp
	// radius, hairline, no shadow) for use INSIDE a card or sheet — cards
	// never nest. The default shadowed tile is for grids on the ground.
	Flat bool
}

// ---- the field tile ---------------------------------------------------------------------

// tile is the look of every picker field.
type tile struct {
	Label    string
	Value    string
	Caption  string
	Error    bool          // caption in the error ink
	Warn     bool          // caption in the warning ink
	Visual   layout.Widget // 44×44 on the left (nil = none)
	Trail    layout.Widget // extra widget before the chevron
	Open     bool
	Loading  bool // skeleton instead of the value
	Disabled bool
	Tip      string
	Anchor   string // anchor key of the popover ("" = none)
	// Flat: inside a card or sheet — a flat inset control instead of a
	// shadowed tile (cards never nest).
	Flat bool
}

// visualSize is the side of the tile's leading visual.
const visualSize unit.Dp = 44

func (t tile) layout(gtx C, th *gui.Theme, btn *gui.Clickable, chev *gui.Anim, fx *flatFX) D {
	body := func(gtx C) D {
		content := func(gtx C) D { return t.content(gtx, th, chev) }
		if t.Flat {
			return flatSurface(gtx, th, btn, fx, flatStyle{Open: t.Open, Disabled: t.Disabled, Tip: t.Tip}, content)
		}
		return gui.Card{Click: btn, Padding: gui.Sp16, Radius: gui.RadiusInner, Selected: t.Open, Disabled: t.Disabled, Tip: t.Tip}.Layout(gtx, th, content)
	}
	if t.Anchor == "" {
		return body(gtx)
	}
	return gui.Anchor(gtx, th, t.Anchor, body)
}

// content is the row inside the field: visual, texts, trail, chevron.
func (t tile) content(gtx C, th *gui.Theme, chev *gui.Anim) D {
	var ch []layout.FlexChild
	if t.Visual != nil {
		ch = append(ch, layout.Rigid(t.Visual), layout.Rigid(layout.Spacer{Width: gui.Sp12 + 2}.Layout))
	}
	ch = append(ch, layout.Flexed(1, func(gtx C) D { return t.texts(gtx, th) }))
	if t.Trail != nil {
		ch = append(ch, layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout), layout.Rigid(t.Trail))
	}
	if chev != nil {
		ch = append(ch, layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout), layout.Rigid(func(gtx C) D {
			return chevron(gtx, th, chev, t.Open)
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
}

func (t tile) texts(gtx C, th *gui.Theme) D {
	var ch []layout.FlexChild
	if t.Label != "" {
		ch = append(ch,
			layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, t.Label)),
			layout.Rigid(layout.Spacer{Height: 1}.Layout))
	}
	value := th.BodyStrong.Sized(16).Lined(1.35)
	if t.Loading {
		ch = append(ch, layout.Rigid(func(gtx C) D {
			return layout.Inset{Top: 4, Bottom: 4}.Layout(gtx, gui.Skeleton{Width: 140, Height: 14, Radius: 7}.Widget(th))
		}))
	} else {
		ch = append(ch, layout.Rigid(gui.Text{Style: value, MaxLines: 1}.Widget(th, t.Value)))
	}
	if t.Caption != "" {
		c := th.Pal.Text3
		switch {
		case t.Error:
			c = th.Pal.ErrorInk
		case t.Warn:
			c = th.Pal.WarningInk
		}
		ch = append(ch, layout.Rigid(gui.Text{Style: th.Small, Color: c, MaxLines: 1}.Widget(th, t.Caption)))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

// chevron draws the field's chevron, turned over while open.
func chevron(gtx C, th *gui.Theme, a *gui.Anim, open bool) D {
	a.Duration, a.Ease = gui.DurTabs, gui.EaseInOutCubic
	rot := a.Animate(gtx, b2f(open))
	sz := gtx.Dp(18)
	cc := f32.Pt(float32(sz)/2, float32(sz)/2)
	defer op.Affine(f32.AffineId().Rotate(cc, rot*3.14159265)).Push(gtx.Ops).Pop()
	gui.IconChevronDown.Layout(gtx, 18, th.Pal.Text2)
	return D{Size: image.Pt(sz, sz)}
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// ---- the flat inset control ---------------------------------------------------------------

// flatFX is the interaction state of a flat field (hover, focus, press).
type flatFX struct {
	hov, foc gui.Anim
	press    gui.Spring
}

// flatStyle configures flatSurface.
type flatStyle struct {
	Open     bool // its chooser is open: an action outline
	Disabled bool
	Tip      string
}

// flatSurface draws w on a flat inset control — Surface fill, 12 dp corners,
// a hairline, no shadow — for fields inside cards and sheets (cards never
// nest). Hover warms the fill and the hairline, a press scales it a touch,
// the focus ring and the open outline match the kit's fields. btn may be nil
// (a static field).
func flatSurface(gtx C, th *gui.Theme, btn *gui.Clickable, fx *flatFX, st flatStyle, w layout.Widget) D {
	if st.Disabled {
		gtx = gtx.Disabled()
	}
	var hovered, pressed, focused bool
	requeue := 0
	if btn != nil {
		// widget.Clickable's Layout eats the clicks: drain them here and hand
		// them back afterwards, so Clicked works before or after Layout.
		for {
			if _, ok := btn.Clickable.Update(gtx); !ok {
				break
			}
			requeue++
		}
		en := gtx.Enabled()
		hovered, pressed = en && btn.Hovered(), en && btn.Pressed()
		focused = en && gtx.Focused(&btn.Clickable)
	}
	fx.hov.Duration, fx.foc.Duration = gui.DurHover, gui.DurHover
	h := fx.hov.Animate(gtx, b2f(hovered || pressed))
	f := fx.foc.Animate(gtx, b2f(focused))
	target := float32(1)
	if pressed {
		target = 0.985
	}
	scale := fx.press.Animate(gtx, target)
	if hovered && st.Tip != "" {
		th.Tooltip(gtx, btn, pressed, st.Tip)
	}
	pad := gtx.Dp(gui.Sp16)
	cg := gtx
	cg.Constraints.Max = image.Pt(max(0, gtx.Constraints.Max.X-2*pad), max(0, gtx.Constraints.Max.Y-2*pad))
	cg.Constraints.Min = image.Pt(
		max(0, min(gtx.Constraints.Min.X-2*pad, cg.Constraints.Max.X)),
		max(0, min(gtx.Constraints.Min.Y-2*pad, cg.Constraints.Max.Y)))
	m := op.Record(gtx.Ops)
	cd := w(cg)
	call := m.Stop()
	size := gtx.Constraints.Constrain(cd.Size.Add(image.Pt(2*pad, 2*pad)))
	if btn != nil {
		btn.Clickable.Layout(gtx, func(gtx C) D {
			defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
			if gtx.Enabled() {
				pointer.CursorPointer.Add(gtx.Ops)
			}
			return D{Size: size}
		})
		for i := 0; i < requeue; i++ {
			btn.Clickable.Click()
		}
	}
	r := image.Rectangle{Max: size}
	gui.FocusRing(gtx, th, r, gui.RadiusControl, f)
	if st.Disabled {
		defer paint.PushOpacity(gtx.Ops, 0.45).Pop()
	}
	return gui.Scale(gtx, scale, func(gtx C) D {
		p := &th.Pal
		gui.Box(gtx, r, gui.RadiusControl, gui.LerpColor(p.Surface, p.SurfaceHover, h), gui.LerpColor(p.Border, p.BorderStrong, h))
		if st.Open {
			gui.Border(gtx, r, gui.RadiusControl, 1.5, p.Action)
		}
		o := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		o.Pop()
		return D{Size: size}
	})
}

// ---- visuals ----------------------------------------------------------------------------

// visBg is the square behind a visual: warm on a white tile, white on a flat
// (warm) field, so it always reads.
func visBg(th *gui.Theme, flat bool) color.NRGBA {
	if flat {
		return th.Pal.Card
	}
	return th.Pal.Surface
}

// visIcon is an icon on a small square.
func visIcon(th *gui.Theme, ic *gui.Icon, flat bool) layout.Widget {
	return func(gtx C) D {
		s := gtx.Dp(visualSize)
		gui.FillRRect(gtx, image.Rect(0, 0, s, s), 12, visBg(th, flat))
		is := gtx.Dp(22)
		defer op.Offset(image.Pt((s-is)/2, (s-is)/2)).Push(gtx.Ops).Pop()
		ic.Layout(gtx, 22, th.Pal.Text)
		return D{Size: image.Pt(s, s)}
	}
}

// visText is a short word ("EN", "1 хв") on a small square.
func visText(th *gui.Theme, s string, size unit.Sp, flat bool) layout.Widget {
	return func(gtx C) D {
		px := gtx.Dp(visualSize)
		gui.FillRRect(gtx, image.Rect(0, 0, px, px), 12, visBg(th, flat))
		lg := gtx
		lg.Constraints = layout.Constraints{Max: image.Pt(px-gtx.Dp(4), px)}
		m := op.Record(gtx.Ops)
		d := gui.Text{Style: th.Label.Weighted(gui.FontSemiBold).Sized(size), MaxLines: 1}.Layout(lg, th, s)
		call := m.Stop()
		defer op.Offset(image.Pt((px-d.Size.X)/2, (px-d.Size.Y)/2)).Push(gtx.Ops).Pop()
		call.Add(gtx.Ops)
		return D{Size: image.Pt(px, px)}
	}
}

// visThumb is a picture cropped into the square.
func visThumb(th *gui.Theme, t gui.Thumb) layout.Widget {
	return func(gtx C) D {
		px := gtx.Dp(visualSize)
		gtx.Constraints = layout.Exact(image.Pt(px, px))
		t.Radius = 12
		t.Ratio = 1
		return t.Layout(gtx, th)
	}
}

// visFrame draws a frame of the given width/height ratio on a small square
// (formats).
func visFrame(th *gui.Theme, ratio float32, flat bool) layout.Widget {
	return func(gtx C) D {
		px := gtx.Dp(visualSize)
		gui.FillRRect(gtx, image.Rect(0, 0, px, px), 12, visBg(th, flat))
		frameIcon(gtx, th, image.Rect(0, 0, px, px), ratio, 26, th.Pal.Text)
		return D{Size: image.Pt(px, px)}
	}
}

// frameIcon strokes a rounded frame of ratio w/h, its longest side box dp,
// centred in r.
func frameIcon(gtx C, th *gui.Theme, r image.Rectangle, ratio float32, box unit.Dp, c color.NRGBA) {
	b := float32(gtx.Dp(box))
	w, h := b, b
	if ratio >= 1 {
		h = b / ratio
	} else {
		w = b * ratio
	}
	x0 := float32(r.Min.X) + (float32(r.Dx())-w)/2
	y0 := float32(r.Min.Y) + (float32(r.Dy())-h)/2
	fr := image.Rect(int(x0+0.5), int(y0+0.5), int(x0+w+0.5), int(y0+h+0.5))
	gui.Border(gtx, fr, 4, 1.75, c)
}
