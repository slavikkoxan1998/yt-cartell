package gui

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// Chip is a pill for toggles and filters (profiles, languages, durations).
// Selected chips take the action tint with a check; Remove adds an × button.
//
//	gui.Chip{Text: "Українська", Icon: gui.IconLanguages, Selected: true}.Layout(gtx, th, &s.lang)
type Chip struct {
	Text     string
	Icon     *Icon
	Dot      color.NRGBA // small status dot before the text (zero = none)
	Selected bool
	Remove   *Clickable // non-nil shows a remove button
	Small    bool       // 30 dp instead of 36 dp
	Disabled bool
	Tip      string
	Force    Force
}

// Layout draws the chip; c is its state (may be nil for a static chip).
func (ch Chip) Layout(gtx C, th *Theme, c *Clickable) D {
	if ch.Disabled {
		gtx = gtx.Disabled()
	}
	st := interaction{scale: 1}
	if c != nil {
		st = c.update(gtx, ch.Force, 0.96)
		if st.hovered && ch.Tip != "" {
			th.Tooltip(gtx, c, st.pressed, ch.Tip)
		}
	} else if ch.Force&ForceHover != 0 {
		st.hover = 1
	}
	h := gtx.Dp(HeightSm)
	ts := th.Label
	is := unit.Dp(16)
	if ch.Small {
		h = gtx.Dp(30)
		ts = th.Small.Weighted(FontMedium)
		is = 14
	}
	gap := gtx.Dp(6)
	p := &th.Pal
	bg := LerpColor(p.Card, p.Surface, st.hover)
	bd := p.Border
	fg := LerpColor(p.Text2, p.Text, st.hover)
	icC := fg
	lead := ch.Icon
	if ch.Selected {
		// The fill stays ActionSoft (its ink reads 4.5:1 there); hover shows
		// as a soft action outline and a deeper ink, press as the scale.
		bg = p.ActionSoft
		bd = WithAlpha(p.Action, 0.5*st.hover)
		fg, icC = p.Text, LerpColor(p.ActionText, p.Text, 0.35*st.hover)
		if lead == nil {
			lead = IconCheck
		}
	} else {
		bg = LerpColor(bg, p.SurfaceHover, st.press*0.5)
	}
	ip := gtx.Dp(is)
	padL, padR := gtx.Dp(14), gtx.Dp(14)
	if lead != nil || ch.Dot.A > 0 {
		padL = gtx.Dp(12)
	}
	rmW := 0
	if ch.Remove != nil {
		rmW, padR = gtx.Dp(24), gtx.Dp(6)
	}
	fixed := padL + padR
	if ch.Dot.A > 0 {
		fixed += gtx.Dp(8) + gap
	}
	if lead != nil {
		fixed += ip + gap
	}
	if rmW > 0 {
		fixed += gap + rmW
	}
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(max(0, gtx.Constraints.Max.X-fixed), h)}
	ld, lc := record(lg, Text{Style: ts, Color: fg, MaxLines: 1}.Widget(th, ch.Text))
	w := fixed + ld.Size.X
	size := pt(w, h)
	if c != nil {
		c.hitArea(gtx, size)
	}
	r := rectWH(w, h)
	FocusRing(gtx, th, r, RadiusPill, st.focus)
	defer opacity(gtx, alphaIf(ch.Disabled, disabledAlpha))()
	Scale(gtx, st.scale, func(gtx C) D {
		Box(gtx, r, RadiusPill, bg, bd)
		x := padL
		if ch.Dot.A > 0 {
			d := gtx.Dp(8)
			fillCircle(gtx.Ops, f32.Pt(float32(x)+float32(d)/2, float32(h)/2), float32(d)/2, ch.Dot)
			x += d + gap
		}
		if lead != nil {
			o := op.Offset(pt(x, (h-ip)/2)).Push(gtx.Ops)
			lead.Layout(gtx, is, icC)
			o.Pop()
			x += ip + gap
		}
		o := op.Offset(pt(x, (h-ld.Size.Y)/2)).Push(gtx.Ops)
		lc.Add(gtx.Ops)
		o.Pop()
		return D{Size: size}
	})
	if ch.Remove != nil {
		o := op.Offset(pt(w-padR-rmW, (h-rmW)/2)).Push(gtx.Ops)
		IconButton{Icon: IconX, Size: 24, IconSize: 14, Tip: "Прибрати", Disabled: ch.Disabled}.Layout(gtx, th, ch.Remove)
		o.Pop()
	}
	return D{Size: size, Baseline: h - ((h-ld.Size.Y)/2 + ld.Size.Y - ld.Baseline)}
}

// Badge is a small count or label pill. Text "" draws an 8 dp dot.
type Badge struct {
	Text  string
	Tone  Tone // ToneNeutral = warm grey; ToneAction = orange
	Solid bool // strong fill with white text (counts on icons)
}

// Layout draws the badge.
func (b Badge) Layout(gtx C, th *Theme) D {
	strong, soft, ink := th.Pal.ToneColors(b.Tone)
	if b.Tone == ToneNeutral {
		// Between Surface and BorderStrong: visible on cards and on tracks.
		strong, soft, ink = th.Pal.Text2, LerpColor(th.Pal.Surface, th.Pal.BorderStrong, 0.55), th.Pal.Text
	}
	if b.Text == "" {
		d := gtx.Dp(8)
		fillCircle(gtx.Ops, f32.Pt(float32(d)/2, float32(d)/2), float32(d)/2, strong)
		return D{Size: pt(d, d)}
	}
	bg, fg := soft, ink
	if b.Solid {
		bg, fg = strong, OnColor(strong)
		switch b.Tone {
		case ToneAction:
			fg = th.Pal.OnAction
		case ToneError:
			bg, fg = th.Pal.ErrorFill, rgb(0xFFFFFF)
		}
	}
	h := gtx.Dp(22)
	pad := gtx.Dp(8)
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(max(0, gtx.Constraints.Max.X-2*pad), h)}
	ld, lc := record(lg, Text{Style: th.Caption.Weighted(FontSemiBold), Color: fg, MaxLines: 1}.Widget(th, b.Text))
	w := max(h, ld.Size.X+2*pad)
	FillRRect(gtx, rectWH(w, h), RadiusPill, bg)
	o := op.Offset(pt((w-ld.Size.X)/2, (h-ld.Size.Y)/2)).Push(gtx.Ops)
	lc.Add(gtx.Ops)
	o.Pop()
	return D{Size: pt(w, h), Baseline: h - ((h-ld.Size.Y)/2 + ld.Size.Y - ld.Baseline)}
}

// AssetState is the life stage of a generated asset or scene.
type AssetState uint8

const (
	StateEmpty   AssetState = iota // "Ще немає"
	StateQueued                    // "У черзі"
	StateRunning                   // "Генерується N%"
	StateDone                      // "Готово"
	StateError                     // "Помилка"
	StateStale                     // "Змінено — перегенеруй"
)

// ParseState maps the engine's state words (empty|queued|running|done|error|
// stale and the scene words planned|framed|shot) to an AssetState.
func ParseState(s string) AssetState {
	switch s {
	case "queued":
		return StateQueued
	case "running":
		return StateRunning
	case "done", "shot", "framed":
		return StateDone
	case "error":
		return StateError
	case "stale":
		return StateStale
	default:
		return StateEmpty
	}
}

// Label is the Ukrainian label of the state (percent < 0 = unknown).
func (s AssetState) Label(percent int) string {
	switch s {
	case StateQueued:
		return "У черзі"
	case StateRunning:
		if percent >= 0 {
			return fmt.Sprintf("Генерується %d%%", min(100, percent))
		}
		return "Генерується"
	case StateDone:
		return "Готово"
	case StateError:
		return "Помилка"
	case StateStale:
		return "Змінено — перегенеруй"
	default:
		return "Ще немає"
	}
}

// Tone is the colour family of the state.
func (s AssetState) Tone() Tone {
	switch s {
	case StateRunning:
		return ToneRunning
	case StateDone:
		return ToneSuccess
	case StateError:
		return ToneError
	case StateStale:
		return ToneWarning
	default:
		return ToneNeutral
	}
}

// StatusPill shows an asset/scene state with its colour and icon; running
// shows a live spinner and the percentage.
type StatusPill struct {
	State   AssetState
	Percent int    // running progress; < 0 hides it
	Text    string // overrides the label
	Small   bool   // 24 dp tall
}

// Layout draws the pill.
func (s StatusPill) Layout(gtx C, th *Theme) D {
	_, soft, ink := th.Pal.ToneColors(s.State.Tone())
	if s.State.Tone() == ToneNeutral {
		soft, ink = th.Pal.Surface, th.Pal.Text2
	}
	label := s.Text
	if label == "" {
		label = s.State.Label(s.Percent)
	}
	h := gtx.Dp(28)
	ts := th.Small.Weighted(FontMedium)
	is := unit.Dp(14)
	if s.Small {
		h = gtx.Dp(24)
		ts = th.Caption.Weighted(FontMedium)
		is = 12
	}
	padL, padR, gap := gtx.Dp(10), gtx.Dp(12), gtx.Dp(6)
	ip := gtx.Dp(is)
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(max(0, gtx.Constraints.Max.X-padL-padR-ip-gap), h)}
	ld, lc := record(lg, Text{Style: ts, Color: ink, MaxLines: 1}.Widget(th, label))
	w := padL + ip + gap + ld.Size.X + padR
	FillRRect(gtx, rectWH(w, h), RadiusPill, soft)
	func() {
		defer op.Offset(pt(padL, (h-ip)/2)).Push(gtx.Ops).Pop()
		switch s.State {
		case StateRunning:
			Spinner{Size: is, Color: ink, Stroke: 1.75}.Layout(gtx, th)
		case StateDone:
			IconCheck.LayoutStroke(gtx, is, 2.25, ink)
		case StateError:
			IconAlertTriangle.LayoutStroke(gtx, is, 1.75, ink)
		case StateStale:
			IconRefreshCw.LayoutStroke(gtx, is, 1.75, ink)
		case StateQueued:
			IconClock.LayoutStroke(gtx, is, 1.75, ink)
		default:
			// A dashed ring: "nothing yet".
			c := f32.Pt(float32(ip)/2, float32(ip)/2)
			r := float32(ip)/2 - float32(gtx.Dp(1.5))
			for k := 0; k < 8; k++ {
				a := float32(k) * math.Pi / 4
				strokeArc(gtx.Ops, c, r, a, math.Pi/4*0.5, float32(gtx.Dp(1.5)), ink)
			}
		}
	}()
	o := op.Offset(pt(padL+ip+gap, (h-ld.Size.Y)/2)).Push(gtx.Ops)
	lc.Add(gtx.Ops)
	o.Pop()
	return D{Size: pt(w, h), Baseline: h - ((h-ld.Size.Y)/2 + ld.Size.Y - ld.Baseline)}
}

// NavItem is a rail destination: an icon over a small label; the active item
// sits on an action-tinted pill with an action-coloured icon and label.
type NavItem struct {
	Icon   *Icon
	Label  string
	Active bool
	Badge  string // small count on the icon ("3"); "" = none
	Force  Force
}

// Layout draws the item across the available width (the rail).
func (n NavItem) Layout(gtx C, th *Theme, c *Clickable) D {
	st := c.update(gtx, n.Force, 0.95)
	w := gtx.Constraints.Max.X
	pillW, pillH := gtx.Dp(56), gtx.Dp(32)
	gap := gtx.Dp(4)
	fg := LerpColor(th.Pal.Text2, th.Pal.Text, st.hover)
	icC := fg
	if n.Active {
		fg, icC = th.Pal.ActionText, th.Pal.Action
	}
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(w, gtx.Dp(32))}
	ts := th.Caption.Weighted(FontMedium)
	if n.Active {
		ts = th.Caption.Weighted(FontSemiBold)
	}
	ld, lc := record(lg, Text{Style: ts, Color: fg, MaxLines: 1}.Widget(th, n.Label))
	h := pillH + gap + ld.Size.Y + gtx.Dp(8)
	size := pt(w, h)
	c.hitArea(gtx, size)
	px := (w - pillW) / 2
	pr := image.Rect(px, gtx.Dp(4), px+pillW, gtx.Dp(4)+pillH)
	FocusRing(gtx, th, pr, RadiusPill, st.focus)
	Scale(gtx, st.scale, func(gtx C) D {
		bg := WithAlpha(th.Pal.Surface, st.hover)
		if n.Active {
			bg = th.Pal.ActionSoft
		}
		FillRRect(gtx, pr, RadiusPill, bg)
		ip := gtx.Dp(22)
		o := op.Offset(pt((w-ip)/2, pr.Min.Y+(pillH-ip)/2)).Push(gtx.Ops)
		n.Icon.Layout(gtx, 22, icC)
		o.Pop()
		if n.Badge != "" {
			o := op.Offset(pt((w+ip)/2-gtx.Dp(6), pr.Min.Y-gtx.Dp(2))).Push(gtx.Ops)
			Badge{Text: n.Badge, Tone: ToneAction, Solid: true}.Layout(gtx, th)
			o.Pop()
		}
		o = op.Offset(pt((w-ld.Size.X)/2, pr.Max.Y+gap)).Push(gtx.Ops)
		lc.Add(gtx.Ops)
		o.Pop()
		return D{Size: size}
	})
	return D{Size: size}
}

// Widget adapts the pill to a layout.Widget.
func (s StatusPill) Widget(th *Theme) layout.Widget {
	return func(gtx C) D { return s.Layout(gtx, th) }
}
