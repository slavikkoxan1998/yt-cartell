package gui

import (
	"image"
	"image/color"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/op"
	"gioui.org/unit"
)

// ButtonKind is the role of a button.
type ButtonKind uint8

const (
	// Primary is the one bright action of a view (action orange).
	Primary ButtonKind = iota
	// Secondary is outlined: card fill with a dark hairline.
	Secondary
	// Tinted sits on the warm surface fill — quiet but clearly a button.
	Tinted
	// Ghost is text only until hovered.
	Ghost
	// Danger confirms destructive actions (delete, cancel all).
	Danger
)

// Size of a control.
type Size uint8

const (
	Md  Size = iota // 44 dp
	Sm              // 36 dp
	Big             // 52 dp
)

// Height returns the control height of s.
func (s Size) Height() unit.Dp {
	switch s {
	case Sm:
		return HeightSm
	case Big:
		return HeightBig
	default:
		return HeightMd
	}
}

// Button is a push button. With Text == "" and an Icon it is an icon button
// (square, or a circle with Round) — give it a Tip.
//
//	gui.Button{Text: "Автопілот", Icon: gui.IconRocket}.Layout(gtx, th, &s.auto)
//	gui.Button{Kind: gui.Tinted, Icon: gui.IconBell, Round: true, Tip: "Сповіщення"}.Layout(gtx, th, &s.bell)
type Button struct {
	Kind  ButtonKind
	Size  Size
	Text  string
	Icon  *Icon // leading icon (or the only content)
	Trail *Icon // trailing icon (arrow, chevron)
	Round bool  // pill-shaped; icon-only buttons become circles
	// Height overrides the size's height (the rail's 56 dp round "+").
	Height   unit.Dp
	Busy     bool // a spinner replaces the leading icon; clicks are ignored
	Disabled bool
	Full     bool   // stretch to the available width
	Tip      string // tooltip after 400 ms hover (required for icon-only buttons)
	Force    Force
}

type btnMetrics struct {
	h, padX, gap, icon unit.Dp
	text               TextStyle
}

func (b Button) metrics(th *Theme) btnMetrics {
	switch b.Size {
	case Sm:
		return btnMetrics{HeightSm, 14, 6, 16, th.Label}
	case Big:
		return btnMetrics{HeightBig, 26, 10, 20, th.Body.Weighted(FontMedium).Sized(16).Lined(1.3)}
	default:
		return btnMetrics{HeightMd, 20, 8, 18, th.Body.Weighted(FontMedium).Lined(1.3)}
	}
}

// colors returns fill, content and border colours at rest, hover and press.
func (b Button) colors(th *Theme, st interaction) (bg, fg, bd color.NRGBA) {
	p := &th.Pal
	switch b.Kind {
	case Primary:
		bg = LerpColor(p.Action, p.ActionHover, st.hover)
		bg = LerpColor(bg, p.ActionPressed, st.press)
		fg = p.OnAction
	case Secondary:
		bg = LerpColor(p.Card, p.Surface, st.hover)
		bg = LerpColor(bg, p.SurfaceHover, st.press)
		fg, bd = p.Text, WithAlpha(p.Text, 0.85)
	case Tinted:
		bg = LerpColor(p.Surface, p.SurfaceHover, st.hover)
		bg = LerpColor(bg, LerpColor(p.SurfaceHover, p.BorderStrong, 0.35), st.press)
		fg = p.Text
	case Ghost:
		bg = LerpColor(WithAlpha(p.Surface, 0), p.Surface, st.hover)
		bg = LerpColor(bg, p.SurfaceHover, st.press)
		fg = LerpColor(p.Text2, p.Text, st.hover)
	case Danger:
		deep := LerpColor(p.ErrorFill, rgb(0x000000), 0.12)
		bg = LerpColor(p.ErrorFill, deep, st.hover)
		bg = LerpColor(bg, LerpColor(p.ErrorFill, rgb(0x000000), 0.22), st.press)
		fg = rgb(0xFFFFFF)
	}
	return bg, fg, bd
}

// Layout draws the button. Read c.Clicked(gtx) before or after it.
func (b Button) Layout(gtx C, th *Theme, c *Clickable) D {
	if c == nil {
		c = new(Clickable)
	}
	inactive := b.Disabled || b.Busy
	if inactive {
		gtx = gtx.Disabled()
	}
	st := c.update(gtx, b.Force, 0.97)
	if st.hovered && b.Tip != "" && !b.Disabled {
		th.Tooltip(gtx, c, st.pressed, b.Tip)
	}
	m := b.metrics(th)
	if b.Height > 0 {
		m.h = b.Height
	}
	bg, fg, bd := b.colors(th, st)

	h, padX, gap, ic := gtx.Dp(m.h), gtx.Dp(m.padX), gtx.Dp(m.gap), gtx.Dp(m.icon)
	lead := b.Icon != nil || b.Busy
	iconOnly := b.Text == "" && lead
	avail := gtx.Constraints.Max.X - 2*padX
	if lead && !iconOnly {
		avail -= ic + gap
	}
	if b.Trail != nil {
		avail -= ic + gap
	}
	var ld D
	var lc op.CallOp
	if b.Text != "" {
		lg := gtx
		lg.Constraints.Min = image.Point{}
		lg.Constraints.Max = pt(max(0, avail), h)
		ld, lc = record(lg, func(gtx C) D {
			return Text{Style: m.text, Color: fg, MaxLines: 1}.Layout(gtx, th, b.Text)
		})
	}
	content := ld.Size.X
	if lead {
		content += ic
		if b.Text != "" {
			content += gap
		}
	}
	if b.Trail != nil {
		content += gap + ic
	}
	w := content + 2*padX
	if iconOnly {
		w = h
	}
	// Buttons keep their natural width (layout.Flex hands its cross-axis
	// minimum to children); Full stretches them on purpose.
	if b.Full {
		w = max(w, gtx.Constraints.Max.X)
	}
	size := pt(w, h)
	c.hitArea(gtx, size)

	r := rectWH(w, h)
	rad := RadiusControl
	if b.Round {
		rad = unit.Dp(float32(h) / 2 / gtx.Metric.PxPerDp)
	}
	FocusRing(gtx, th, r, rad, st.focus)
	defer opacity(gtx, alphaIf(b.Disabled, disabledAlpha))()
	scaled := Scale(gtx, st.scale, func(gtx C) D {
		Box(gtx, r, rad, bg, color.NRGBA{})
		if bd.A > 0 {
			Border(gtx, r, rad, 1.25, bd)
		}
		x := (w - content) / 2
		cy := h / 2
		if lead {
			t := op.Offset(pt(x, cy-ic/2)).Push(gtx.Ops)
			if b.Busy {
				Spinner{Size: m.icon, Color: fg}.Layout(gtx, th)
			} else {
				b.Icon.Layout(gtx, m.icon, fg)
			}
			t.Pop()
			x += ic
			if b.Text != "" {
				x += gap
			}
		}
		if b.Text != "" {
			t := op.Offset(pt(x, cy-ld.Size.Y/2)).Push(gtx.Ops)
			lc.Add(gtx.Ops)
			t.Pop()
			x += ld.Size.X
		}
		if b.Trail != nil {
			x += gap
			t := op.Offset(pt(x, cy-ic/2)).Push(gtx.Ops)
			b.Trail.Layout(gtx, m.icon, fg)
			t.Pop()
		}
		return D{Size: size}
	})
	base := 0
	if b.Text != "" {
		base = h - (h/2 - ld.Size.Y/2 + ld.Size.Y - ld.Baseline)
	}
	return D{Size: scaled.Size, Baseline: base}
}

func alphaIf(cond bool, a float32) float32 {
	if cond {
		return a
	}
	return 1
}

// Spinner is a short arc circling over a faint track (busy states). Size defaults to 18 dp and
// Color to th.Pal.Action.
type Spinner struct {
	Size  unit.Dp
	Color color.NRGBA
	// Stroke width in dp; 0 scales with Size.
	Stroke unit.Dp
}

// Layout draws the spinner and keeps animating while it is visible.
func (s Spinner) Layout(gtx C, th *Theme) D {
	if s.Size == 0 {
		s.Size = 18
	}
	if s.Color.A == 0 {
		s.Color = th.Pal.Action
	}
	px := gtx.Dp(s.Size)
	sw := s.Stroke
	if sw == 0 {
		sw = max(1.75, s.Size/9)
	}
	w := float32(gtx.Dp(sw))
	rad := float32(px)/2 - w/2
	c := f32.Pt(float32(px)/2, float32(px)/2)
	phase := float32(0)
	if !ReducedMotion() {
		const period = 900 * time.Millisecond
		phase = float32(gtx.Now.UnixNano()%int64(period)) / float32(period)
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 60)})
	}
	a0 := phase*2*math.Pi - math.Pi/2
	// Faint full track, then the moving arc.
	strokeArc(gtx.Ops, c, rad, 0, 2*math.Pi, w, WithAlpha(s.Color, 0.2))
	strokeArc(gtx.Ops, c, rad, a0, 0.3*2*math.Pi, w, s.Color)
	return D{Size: pt(px, px)}
}

// IconButton is a compact round ghost button for inline actions (close,
// remove, more). Size is the diameter (default 32 dp).
type IconButton struct {
	Icon     *Icon
	Size     unit.Dp
	IconSize unit.Dp     // default 55 % of Size
	Color    color.NRGBA // icon colour; zero = th.Pal.Text2 (Text on hover)
	Tip      string
	Disabled bool
	Force    Force
}

// Layout draws the button.
func (b IconButton) Layout(gtx C, th *Theme, c *Clickable) D {
	if b.Size == 0 {
		b.Size = 32
	}
	if b.IconSize == 0 {
		b.IconSize = max(14, b.Size*0.55)
	}
	if b.Disabled {
		gtx = gtx.Disabled()
	}
	st := c.update(gtx, b.Force, 0.94)
	if st.hovered && b.Tip != "" {
		th.Tooltip(gtx, c, st.pressed, b.Tip)
	}
	px := gtx.Dp(b.Size)
	size := pt(px, px)
	c.hitArea(gtx, size)
	fg := b.Color
	if fg.A == 0 {
		fg = LerpColor(th.Pal.Text2, th.Pal.Text, st.hover)
	}
	bg := LerpColor(WithAlpha(th.Pal.Surface, 0), th.Pal.Surface, st.hover)
	bg = LerpColor(bg, th.Pal.SurfaceHover, st.press)
	r := rectWH(px, px)
	rad := unit.Dp(float32(px) / 2 / gtx.Metric.PxPerDp)
	FocusRing(gtx, th, r, rad, st.focus)
	defer opacity(gtx, alphaIf(b.Disabled, disabledAlpha))()
	return Scale(gtx, st.scale, func(gtx C) D {
		fillCircle(gtx.Ops, f32.Pt(float32(px)/2, float32(px)/2), float32(px)/2, bg)
		ic := gtx.Dp(b.IconSize)
		defer op.Offset(pt((px-ic)/2, (px-ic)/2)).Push(gtx.Ops).Pop()
		b.Icon.Layout(gtx, b.IconSize, fg)
		return D{Size: size}
	})
}
