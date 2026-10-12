package gui

import (
	"image"
	"image/color"
	"strings"
	"unicode"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
)

// NoPadding makes a Card lay its content edge to edge.
const NoPadding unit.Dp = -1

// Card is the kit's main surface: white, 22 dp corners, a warm hairline and
// a soft two-layer shadow. With Click it becomes pressable and lifts on hover.
//
//	gui.Card{}.Layout(gtx, th, body)
//	gui.Card{Click: &s.open}.Layout(gtx, th, body)   // whole card is a button
//
// The card honours gtx.Constraints.Min, so grids and flex rows can stretch it.
type Card struct {
	Padding  unit.Dp // inner padding; 0 = 24 dp; NoPadding = none
	Radius   unit.Dp // 0 = RadiusCard
	Click    *Clickable
	Selected bool // action outline (picked item)
	Tint     bool // warm surface fill instead of white (inner panels)
	Flat     bool // no shadow (cards inside cards)
	Tip      string
	Disabled bool
	Force    Force
}

// Layout draws the card around w.
func (c Card) Layout(gtx C, th *Theme, w layout.Widget) D {
	pad := gtx.Dp(Sp24)
	switch {
	case c.Padding == NoPadding:
		pad = 0
	case c.Padding > 0:
		pad = gtx.Dp(c.Padding)
	}
	rad := c.Radius
	if rad == 0 {
		rad = RadiusCard
	}
	if c.Disabled {
		gtx = gtx.Disabled()
	}
	st := interaction{scale: 1}
	if c.Click != nil {
		st = c.Click.update(gtx, c.Force, 0.985)
		if st.hovered && c.Tip != "" {
			th.Tooltip(gtx, c.Click, st.pressed, c.Tip)
		}
	}
	cg := gtx
	cg.Constraints.Max = pt(max(0, gtx.Constraints.Max.X-2*pad), max(0, gtx.Constraints.Max.Y-2*pad))
	cg.Constraints.Min = pt(max(0, gtx.Constraints.Min.X-2*pad), max(0, gtx.Constraints.Min.Y-2*pad))
	cg.Constraints.Min.X = min(cg.Constraints.Min.X, cg.Constraints.Max.X)
	cg.Constraints.Min.Y = min(cg.Constraints.Min.Y, cg.Constraints.Max.Y)
	cd, call := record(cg, w)
	size := gtx.Constraints.Constrain(cd.Size.Add(pt(2*pad, 2*pad)))
	if c.Click != nil {
		c.Click.hitArea(gtx, size)
	}
	lift := st.hover
	r := rectWH(size.X, size.Y)
	dy := -float32(gtx.Dp(2)) * lift
	defer opacity(gtx, alphaIf(c.Disabled, disabledAlpha))()
	FocusRing(gtx, th, r, rad, st.focus)
	d := Scale(gtx, st.scale, func(gtx C) D {
		defer op.Affine(f32.AffineId().Offset(f32.Pt(0, dy))).Push(gtx.Ops).Pop()
		fill := th.Pal.Card
		if c.Tint {
			fill = th.Pal.Surface
		}
		if !c.Flat && !c.Tint {
			Shadow(gtx, th, r, rad, LerpElevation(ElevCard, ElevHover, lift))
		}
		border := WithAlpha(th.Pal.Border, 0.75)
		if c.Tint {
			border = color.NRGBA{}
		}
		Box(gtx, r, rad, fill, border)
		if c.Selected {
			Border(gtx, r, rad, 2, th.Pal.Action)
		}
		t := op.Offset(pt(pad, pad)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		return D{Size: size}
	})
	d.Baseline = cd.Baseline + pad
	if cd.Baseline == 0 {
		d.Baseline = 0
	}
	return d
}

// Tile is a quick action: an icon in a soft rounded square with a label
// under it (Home "Швидкі дії").
type Tile struct {
	Icon     *Icon
	Label    string
	Tip      string
	Size     unit.Dp // square side; 0 = 64
	Disabled bool
	Force    Force
}

// Layout draws the tile; c is its state.
func (t Tile) Layout(gtx C, th *Theme, c *Clickable) D {
	if t.Size == 0 {
		t.Size = 64
	}
	if t.Disabled {
		gtx = gtx.Disabled()
	}
	st := c.update(gtx, t.Force, 0.95)
	if st.hovered && t.Tip != "" {
		th.Tooltip(gtx, c, st.pressed, t.Tip)
	}
	sq := gtx.Dp(t.Size)
	gap := gtx.Dp(Sp8)
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(max(sq, gtx.Constraints.Max.X), gtx.Dp(40))}
	fg := LerpColor(th.Pal.Text2, th.Pal.Text, st.hover)
	ld, lc := record(lg, func(gtx C) D {
		return Text{Style: th.Small.Weighted(FontMedium), Color: fg, MaxLines: 2, Align: text.Middle}.Layout(gtx, th, t.Label)
	})
	w := max(sq, ld.Size.X)
	h := sq + gap + ld.Size.Y
	size := pt(w, h)
	c.hitArea(gtx, size)
	defer opacity(gtx, alphaIf(t.Disabled, disabledAlpha))()
	x := (w - sq) / 2
	sr := image.Rect(x, 0, x+sq, sq)
	FocusRing(gtx, th, sr, RadiusInner, st.focus)
	func() {
		defer op.Offset(sr.Min).Push(gtx.Ops).Pop()
		Scale(gtx, st.scale, func(gtx C) D {
			r := rectWH(sq, sq)
			fill := LerpColor(th.Pal.Card, th.Pal.ActionSoft, st.hover)
			if st.hover < 0.01 {
				Shadow(gtx, th, r, RadiusInner, Elevation{{Y: 1, Blur: 4, Alpha: 0.05}})
			}
			Box(gtx, r, RadiusInner, fill, LerpColor(th.Pal.Border, WithAlpha(th.Pal.Border, 0), st.hover))
			icc := LerpColor(th.Pal.Text, th.Pal.ActionText, st.hover)
			is := unit.Dp(float32(t.Size) * 0.4)
			ip := gtx.Dp(is)
			defer op.Offset(pt((sq-ip)/2, (sq-ip)/2)).Push(gtx.Ops).Pop()
			t.Icon.Layout(gtx, is, icc)
			return D{Size: pt(sq, sq)}
		})
	}()
	func() {
		defer op.Offset(pt((w-ld.Size.X)/2, sq+gap)).Push(gtx.Ops).Pop()
		lc.Add(gtx.Ops)
	}()
	return D{Size: size}
}

// StatTile shows one number with a label (project header stats, summaries).
type StatTile struct {
	Icon    *Icon
	Tone    Tone // icon colour family (ToneNeutral = action)
	Label   string
	Value   string
	Caption string // small note under the value ("по 8 с")
	// Plain drops the tinted background (on busy cards).
	Plain bool
}

// Layout draws the tile across the available width.
func (s StatTile) Layout(gtx C, th *Theme) D {
	pad := gtx.Dp(Sp16)
	if s.Plain {
		pad = 0
	}
	body := func(gtx C) D {
		var ch []layout.FlexChild
		if s.Icon != nil {
			ch = append(ch, layout.Rigid(func(gtx C) D {
				tone := s.Tone
				if tone == ToneNeutral {
					tone = ToneAction
				}
				_, _, ink := th.Pal.ToneColors(tone) // readable on white and on Surface
				// A white square pops on the tinted tile; a plain tile gets a tint.
				bg := th.Pal.Card
				if s.Plain {
					bg = th.Pal.Surface
				}
				bs := gtx.Dp(40)
				FillRRect(gtx, rectWH(bs, bs), 12, bg)
				ic := gtx.Dp(20)
				defer op.Offset(pt((bs-ic)/2, (bs-ic)/2)).Push(gtx.Ops).Pop()
				s.Icon.Layout(gtx, 20, ink)
				return D{Size: pt(bs, bs)}
			}), layout.Rigid(layout.Spacer{Width: Sp12}.Layout))
		}
		ch = append(ch, layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, s.Label)),
				layout.Rigid(func(gtx C) D {
					return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
						layout.Rigid(Text{Style: th.H2, MaxLines: 1}.Widget(th, s.Value)),
						layout.Rigid(func(gtx C) D {
							if s.Caption == "" {
								return D{}
							}
							return layout.Inset{Left: Sp8}.Layout(gtx, Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, s.Caption))
						}),
					)
				}),
			)
		}))
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
	}
	if s.Plain {
		return body(gtx)
	}
	return Card{Tint: true, Radius: RadiusInner, Padding: unit.Dp(float32(pad) / gtx.Metric.PxPerDp), Flat: true}.Layout(gtx, th, body)
}

// NextStepCard is the guiding "what next" card: an icon in a soft circle, a
// title, a line of text and a round orange arrow button. The whole card is
// the button.
type NextStepCard struct {
	Icon  *Icon
	Title string
	Text  string
	// Busy shows a spinner in the arrow button (the step is running).
	Busy  bool
	Force Force
}

// Layout draws the card; c is its state (read c.Clicked).
func (n NextStepCard) Layout(gtx C, th *Theme, c *Clickable) D {
	if n.Icon == nil {
		n.Icon = IconSparkles
	}
	var arrow interaction
	return Card{Click: c, Force: n.Force, Padding: Sp20}.Layout(gtx, th, func(gtx C) D {
		arrow = interaction{hover: c.hover.Value(gtx)}
		circle := gtx.Dp(64)
		btn := gtx.Dp(44)
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				fillCircle(gtx.Ops, f32.Pt(float32(circle)/2, float32(circle)/2), float32(circle)/2, th.Pal.ActionSoft)
				ic := gtx.Dp(28)
				defer op.Offset(pt((circle-ic)/2, (circle-ic)/2)).Push(gtx.Ops).Pop()
				n.Icon.Layout(gtx, 28, th.Pal.Text)
				return D{Size: pt(circle, circle)}
			}),
			layout.Rigid(layout.Spacer{Width: Sp16}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(Text{Style: th.H3, MaxLines: 2}.Widget(th, n.Title)),
					layout.Rigid(layout.Spacer{Height: Sp4}.Layout),
					layout.Rigid(Text{Style: th.Small.Sized(14), Color: th.Pal.Text2, MaxLines: 4}.Widget(th, n.Text)),
				)
			}),
			layout.Rigid(layout.Spacer{Width: Sp16}.Layout),
			layout.Rigid(func(gtx C) D {
				bg := LerpColor(th.Pal.Action, th.Pal.ActionHover, arrow.hover)
				FillRRect(gtx, rectWH(btn, btn), RadiusControl, bg)
				ic := gtx.Dp(22)
				defer op.Offset(pt((btn-ic)/2, (btn-ic)/2)).Push(gtx.Ops).Pop()
				if n.Busy {
					Spinner{Size: 22, Color: th.Pal.OnAction}.Layout(gtx, th)
				} else {
					dx := unit.Dp(3 * arrow.hover)
					Offset(gtx, dx, 0, IconArrowRight.Widget(22, th.Pal.OnAction))
				}
				return D{Size: pt(btn, btn)}
			}),
		)
	})
}

// TipCard is a friendly hint with an optional link-style action
// ("Порада" — text — "Спробувати ›").
type TipCard struct {
	Title  string
	Text   string
	Action string // link text; "" = none
	Icon   *Icon  // optional icon before the title
	Force  Force
}

// Layout draws the card; c is the action link's state (may be nil without Action).
func (t TipCard) Layout(gtx C, th *Theme, c *Clickable) D {
	return Card{}.Layout(gtx, th, func(gtx C) D {
		var ch []layout.FlexChild
		ch = append(ch, layout.Rigid(func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					if t.Icon == nil {
						return D{}
					}
					return layout.Inset{Right: Sp8}.Layout(gtx, t.Icon.Widget(20, th.Pal.ActionText))
				}),
				layout.Flexed(1, Text{Style: th.H3}.Widget(th, t.Title)),
			)
		}))
		ch = append(ch, layout.Rigid(layout.Spacer{Height: Sp8}.Layout),
			layout.Rigid(Text{Style: th.Body, Color: th.Pal.Text2}.Widget(th, t.Text)))
		if t.Action != "" && c != nil {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: Sp16}.Layout),
				layout.Rigid(func(gtx C) D {
					return Link{Text: t.Action, Trail: IconChevronRight, Force: t.Force}.Layout(gtx, th, c)
				}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
}

// Link is an action-coloured text button ("Переглянути все ›").
type Link struct {
	Text  string
	Trail *Icon
	Style TextStyle // zero = Label (14 medium)
	Force Force
}

// Layout draws the link; c is its state.
func (l Link) Layout(gtx C, th *Theme, c *Clickable) D {
	st := c.update(gtx, l.Force, 1)
	ts := l.Style
	if ts.Size == 0 {
		ts = th.Label.Sized(15)
	}
	// Hover deepens towards the text colour (never towards the lighter fill
	// tones, which would drop below 4.5:1 on white).
	col := LerpColor(th.Pal.ActionText, th.Pal.Text, 0.3*st.hover)
	gtx.Constraints.Min = image.Point{}
	d, call := record(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(Text{Style: ts, Color: col, MaxLines: 1}.Widget(th, l.Text)),
			layout.Rigid(func(gtx C) D {
				if l.Trail == nil {
					return D{}
				}
				return layout.Inset{Left: Sp4}.Layout(gtx, func(gtx C) D {
					return Offset(gtx, unit.Dp(2*st.hover), 0, l.Trail.Widget(18, col))
				})
			}),
		)
	})
	c.hitArea(gtx, d.Size)
	FocusRing(gtx, th, rectWH(d.Size.X, d.Size.Y), 6, st.focus)
	call.Add(gtx.Ops)
	// Underline on hover.
	if st.hover > 0.01 {
		lw := d.Size.X
		if l.Trail != nil {
			lw -= gtx.Dp(22)
		}
		y := d.Size.Y - gtx.Dp(1)
		FillRect(gtx, image.Rect(0, y, lw, y+max(1, gtx.Dp(1))), WithAlpha(col, st.hover*0.6))
	}
	return d
}

// ActivityRow is one line of a feed or queue: a leading icon in a circle, a
// title, a meta line, an optional thin progress bar and a trailing value or
// widget. Rows are clickable when given a Clickable.
type ActivityRow struct {
	Icon  *Icon
	Tone  Tone
	Solid bool // filled tone circle with a white icon (the live item)
	Title string
	Meta  string // "Сьогодні · Сцена 4"
	// Value is trailing text; Trailing (if set) replaces it.
	Value    string
	Trailing layout.Widget
	// Progress in [0,1] draws a thin bar under the meta line; < 0 hides it.
	Progress float32
	Divider  bool // hairline under the row
	Force    Force
}

// Layout draws the row across the available width; c may be nil.
func (a ActivityRow) Layout(gtx C, th *Theme, c *Clickable) D {
	var st interaction
	if c != nil {
		st = c.update(gtx, a.Force, 1)
	}
	padY := gtx.Dp(Sp12)
	w := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: pt(w, gtx.Constraints.Max.Y)}
	cd, call := record(cg, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				sz := gtx.Dp(44)
				strong, soft, ink := th.Pal.ToneColors(a.Tone)
				bg, fg := soft, ink
				if a.Tone == ToneNeutral {
					bg, fg = th.Pal.Surface, th.Pal.Text
				}
				if a.Solid {
					bg, fg = strong, OnColor(strong)
					if a.Tone == ToneAction {
						fg = th.Pal.OnAction
					}
					if a.Tone == ToneNeutral {
						bg, fg = th.Pal.Text, th.Pal.Card
					}
				}
				fillCircle(gtx.Ops, f32.Pt(float32(sz)/2, float32(sz)/2), float32(sz)/2, bg)
				ic := gtx.Dp(20)
				defer op.Offset(pt((sz-ic)/2, (sz-ic)/2)).Push(gtx.Ops).Pop()
				if a.Icon != nil {
					a.Icon.Layout(gtx, 20, fg)
				}
				return D{Size: pt(sz, sz)}
			}),
			layout.Rigid(layout.Spacer{Width: Sp16}.Layout),
			layout.Flexed(1, func(gtx C) D {
				var ch []layout.FlexChild
				ch = append(ch,
					layout.Rigid(Text{Style: th.Body.Weighted(FontMedium), MaxLines: 1}.Widget(th, a.Title)))
				if a.Meta != "" {
					ch = append(ch, layout.Rigid(layout.Spacer{Height: 2}.Layout),
						layout.Rigid(Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, a.Meta)))
				}
				if a.Progress >= 0 {
					ch = append(ch, layout.Rigid(layout.Spacer{Height: Sp8}.Layout),
						layout.Rigid(func(gtx C) D {
							gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(360))
							return ProgressBar{Value: a.Progress, Height: 6, Tone: a.Tone}.Layout(gtx, th)
						}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
			}),
			layout.Rigid(func(gtx C) D {
				if a.Trailing == nil && a.Value == "" {
					return D{}
				}
				return layout.Inset{Left: Sp16}.Layout(gtx, func(gtx C) D {
					if a.Trailing != nil {
						return a.Trailing(gtx)
					}
					return Text{Style: th.H3, MaxLines: 1}.Layout(gtx, th, a.Value)
				})
			}),
		)
	})
	h := cd.Size.Y + 2*padY
	size := pt(w, h)
	if c != nil {
		c.hitArea(gtx, size)
		if st.hover > 0.01 {
			hx := gtx.Dp(8)
			FillRRect(gtx, image.Rect(-hx, gtx.Dp(2), w+hx, h-gtx.Dp(2)), RadiusInner, WithAlpha(th.Pal.Surface, st.hover))
		}
		FocusRing(gtx, th, rectWH(w, h), RadiusInner, st.focus)
	}
	t := op.Offset(pt(0, padY)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	if a.Divider {
		left := gtx.Dp(44 + 16)
		FillRect(gtx, image.Rect(left, h-max(1, gtx.Dp(1)), w, h), th.Pal.Border)
	}
	return D{Size: size}
}

// EmptyState fills an empty area with an illustrative icon, a title, a line
// of text and (optionally) the action that fills it.
type EmptyState struct {
	Icon       *Icon
	Title      string
	Text       string
	Action     string // button text; "" = no button
	ActionIcon *Icon
	Force      Force
}

// Layout centres the empty state in the available width; c is the action's state.
func (e EmptyState) Layout(gtx C, th *Theme, c *Clickable) D {
	if e.Icon == nil {
		e.Icon = IconSparkles
	}
	maxW := min(gtx.Constraints.Max.X, gtx.Dp(420))
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			outer := gtx.Dp(96)
			inner := gtx.Dp(72)
			cx := float32(outer) / 2
			fillCircle(gtx.Ops, f32.Pt(cx, cx), cx, WithAlpha(th.Pal.ActionSoft, 0.5))
			fillCircle(gtx.Ops, f32.Pt(cx, cx), float32(inner)/2, th.Pal.ActionSoft)
			ic := gtx.Dp(32)
			defer op.Offset(pt((outer-ic)/2, (outer-ic)/2)).Push(gtx.Ops).Pop()
			e.Icon.Layout(gtx, 32, th.Pal.ActionText)
			return D{Size: pt(outer, outer)}
		}),
		layout.Rigid(layout.Spacer{Height: Sp16}.Layout),
		layout.Rigid(func(gtx C) D {
			gtx.Constraints.Max.X = maxW
			return Text{Style: th.H3, Align: text.Middle}.Layout(gtx, th, e.Title)
		}),
		layout.Rigid(layout.Spacer{Height: Sp8}.Layout),
		layout.Rigid(func(gtx C) D {
			gtx.Constraints.Max.X = maxW
			return Text{Style: th.Body, Color: th.Pal.Text2, Align: text.Middle}.Layout(gtx, th, e.Text)
		}),
		layout.Rigid(func(gtx C) D {
			if e.Action == "" || c == nil {
				return D{}
			}
			return layout.Inset{Top: Sp24}.Layout(gtx, func(gtx C) D {
				return Button{Text: e.Action, Icon: e.ActionIcon, Force: e.Force}.Layout(gtx, th, c)
			})
		}),
	)
}

// Divider is a 1 dp warm hairline (horizontal across the width, or vertical
// across the minimum height).
type Divider struct {
	Vertical bool
	Color    color.NRGBA // zero = th.Pal.Border
}

// Layout draws the line.
func (d Divider) Layout(gtx C, th *Theme) D {
	c := d.Color
	if c.A == 0 {
		c = th.Pal.Border
	}
	px := max(1, gtx.Dp(1))
	var sz image.Point
	if d.Vertical {
		sz = pt(px, gtx.Constraints.Min.Y)
		if sz.Y == 0 {
			sz.Y = gtx.Constraints.Max.Y
		}
	} else {
		sz = pt(gtx.Constraints.Max.X, px)
	}
	FillRect(gtx, rectWH(sz.X, sz.Y), c)
	return D{Size: sz}
}

// Avatar is a round picture or, without one, the initials of Name on a soft
// tone fill (cast members, voices, profiles).
type Avatar struct {
	Name  string
	Image *paint.ImageOp // optional
	Size  unit.Dp        // diameter; 0 = 40
	Tone  Tone
}

// Layout draws the avatar.
func (a Avatar) Layout(gtx C, th *Theme) D {
	if a.Size == 0 {
		a.Size = 40
	}
	px := gtx.Dp(a.Size)
	sz := pt(px, px)
	if a.Image != nil && a.Image.Size().X > 0 {
		defer clip.Ellipse(rectWH(px, px)).Push(gtx.Ops).Pop()
		drawImageFit(gtx, *a.Image, sz, FitCover)
		return D{Size: sz}
	}
	tone := a.Tone
	if tone == ToneNeutral {
		tone = ToneAction
	}
	_, soft, ink := th.Pal.ToneColors(tone)
	fillCircle(gtx.Ops, f32.Pt(float32(px)/2, float32(px)/2), float32(px)/2, soft)
	ini := initials(a.Name)
	st := th.Label.Weighted(FontSemiBold).Sized(unit.Sp(float32(a.Size) * 0.38))
	lg := gtx
	lg.Constraints = layout.Constraints{Max: sz}
	d, call := record(lg, Text{Style: st, Color: ink, MaxLines: 1}.Widget(th, ini))
	defer op.Offset(pt((px-d.Size.X)/2, (px-d.Size.Y)/2)).Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
	return D{Size: sz}
}

// initials returns up to two capital initials of name.
func initials(name string) string {
	var out []rune
	for _, f := range strings.Fields(name) {
		for _, r := range f {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				out = append(out, unicode.ToUpper(r))
				break
			}
		}
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}
