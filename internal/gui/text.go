package gui

import (
	"image/color"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"golang.org/x/image/math/fixed"
)

// TextStyle is one step of the type scale.
type TextStyle struct {
	Font font.Font
	Size unit.Sp
	// LineHeight is a multiple of Size (CSS line-height); 0 means 1.25.
	LineHeight float32
	// Upper renders the text in capitals.
	Upper bool
	// Tracking is extra letter spacing in em (CSS letter-spacing). It applies
	// to single-line text only; wrapped text ignores it.
	Tracking float32
}

// Typography is the type scale (GUI.md "Tokens"). Theme embeds it, so the
// styles read as th.H1, th.Body, th.Caption …
type Typography struct {
	Display    TextStyle // 40 SemiBold — the Home greeting only
	H1         TextStyle // 32 SemiBold
	H2         TextStyle // 24 SemiBold
	H3         TextStyle // 18 SemiBold — card titles
	Body       TextStyle // 15 Regular
	BodyStrong TextStyle // 15 SemiBold
	Label      TextStyle // 14 Medium — buttons, field labels, tabs
	Small      TextStyle // 13 Regular — meta, hints
	Caption    TextStyle // 12 Regular
	Stat       TextStyle // 28 SemiBold — numbers in stat tiles and rings
	// There is deliberately no overline/eyebrow style: labels above headings
	// are banned by the design floor (use sentence-case Label or Small).
}

// DefaultTypography returns the kit's type scale in Fira Sans.
func DefaultTypography() Typography {
	return Typography{
		Display:    TextStyle{Font: FontSemiBold, Size: 40, LineHeight: 1.12},
		H1:         TextStyle{Font: FontSemiBold, Size: 32, LineHeight: 1.15},
		H2:         TextStyle{Font: FontSemiBold, Size: 24, LineHeight: 1.2},
		H3:         TextStyle{Font: FontSemiBold, Size: 18, LineHeight: 1.3},
		Body:       TextStyle{Font: FontRegular, Size: 15, LineHeight: 1.5},
		BodyStrong: TextStyle{Font: FontSemiBold, Size: 15, LineHeight: 1.5},
		Label:      TextStyle{Font: FontMedium, Size: 14, LineHeight: 1.3},
		Small:      TextStyle{Font: FontRegular, Size: 13, LineHeight: 1.45},
		Caption:    TextStyle{Font: FontRegular, Size: 12, LineHeight: 1.4},
		Stat:       TextStyle{Font: FontSemiBold, Size: 28, LineHeight: 1.1},
	}
}

// Sized returns a copy with another size.
func (s TextStyle) Sized(sp unit.Sp) TextStyle { s.Size = sp; return s }

// Weighted returns a copy in another face (FontMedium, FontSemiBold …).
func (s TextStyle) Weighted(f font.Font) TextStyle { s.Font = f; return s }

// Lined returns a copy with another line height (multiple of the size).
func (s TextStyle) Lined(lh float32) TextStyle { s.LineHeight = lh; return s }

func (s TextStyle) lineHeight() float32 {
	if s.LineHeight <= 0 {
		return 1.25
	}
	return s.LineHeight
}

// LinePx is the height of one line of s in pixels.
func (s TextStyle) LinePx(gtx layout.Context) int {
	return int(float32(gtx.Sp(s.Size))*s.lineHeight() + 0.5)
}

// Text draws a string in a TextStyle:
//
//	gui.Text{Style: th.H3}.Layout(gtx, th, "Черга")
//	gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Layout(gtx, th, hint)
//
// The zero Color means th.Pal.Text.
type Text struct {
	Style    TextStyle
	Color    color.NRGBA
	MaxLines int // 0 = unlimited; truncated lines end with "…"
	Align    text.Alignment
}

// Layout draws s within the constraints (wrapping at Max.X).
func (t Text) Layout(gtx layout.Context, th *Theme, s string) layout.Dimensions {
	if t.Style.Size == 0 {
		t.Style = th.Body
	}
	c := t.Color
	if c == (color.NRGBA{}) {
		c = th.Pal.Text
	}
	if t.Style.Upper {
		s = strings.ToUpper(s)
	}
	cs := &gtx.Constraints
	cs.Min.X, cs.Min.Y = min(cs.Min.X, cs.Max.X), min(cs.Min.Y, cs.Max.Y)
	d, call := record(gtx, func(gtx layout.Context) layout.Dimensions {
		if t.Style.Tracking != 0 && t.Align == text.Start && !strings.Contains(s, "\n") {
			if d, ok := trackedLine(gtx, th.Shaper, t.Style, c, s); ok {
				return d
			}
		}
		lbl := widget.Label{MaxLines: t.MaxLines, Alignment: t.Align, LineHeightScale: t.Style.lineHeight()}
		return lbl.Layout(gtx, th.Shaper, t.Style.Font, t.Style.Size, s, colorMacro(gtx.Ops, c))
	})
	call.Add(gtx.Ops)
	if th.inkBoost(c, t.Style.Size) {
		call.Add(gtx.Ops)
	}
	return d
}

// Widget returns a layout.Widget drawing s (for layout.Flex children).
func (t Text) Widget(th *Theme, s string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return t.Layout(gtx, th, s) }
}

// TextWidth measures the single-line width of s in style st, in pixels.
func (th *Theme) TextWidth(gtx layout.Context, st TextStyle, s string) int {
	if st.Upper {
		s = strings.ToUpper(s)
	}
	th.Shaper.LayoutString(text.Parameters{
		Font:     st.Font,
		PxPerEm:  fixed.I(gtx.Sp(st.Size)),
		MaxWidth: 1 << 24,
		MaxLines: 1,
		Locale:   gtx.Locale,
	}, s)
	var w fixed.Int26_6
	clusters := 0
	for g, ok := th.Shaper.NextGlyph(); ok; g, ok = th.Shaper.NextGlyph() {
		if e := g.X + g.Advance; e > w {
			w = e
		}
		if g.Flags&text.FlagClusterBreak != 0 {
			clusters++
		}
	}
	w += fixed.Int26_6(float32(max(0, clusters-1)) * st.Tracking * float32(gtx.Sp(st.Size)) * 64)
	return w.Ceil()
}

// inkBoost reports whether text in colour c should be painted twice. Gio
// blends in linear light, which renders dark glyphs on light grounds visibly
// thinner than browsers and design tools do; a second pass at the same spot
// raises edge coverage (a → 2a−a²) and restores the intended weight. Large
// type (≥ 20 sp) has stems wide enough not to need it, and light text on dark
// grounds already looks right; both are left alone.
func (th *Theme) inkBoost(c color.NRGBA, size unit.Sp) bool {
	if !th.InkBoost || c.A < 160 || size >= 20 {
		return false
	}
	return luma(c) < 0.42
}

// trackedLine lays out one line with letter spacing between clusters (Gio has
// no letter-spacing). ok is false when it does not fit, so the caller can fall
// back to a plain truncating label.
func trackedLine(gtx layout.Context, sh *text.Shaper, st TextStyle, c color.NRGBA, s string) (layout.Dimensions, bool) {
	px := gtx.Sp(st.Size)
	track := fixed.Int26_6(st.Tracking * float32(px) * 64)
	sh.LayoutString(text.Parameters{
		Font:     st.Font,
		PxPerEm:  fixed.I(px),
		MaxWidth: 1 << 24,
		MaxLines: 1,
		Locale:   gtx.Locale,
	}, s)
	var glyphs []text.Glyph
	var shift, asc, desc, right fixed.Int26_6
	for g, ok := sh.NextGlyph(); ok; g, ok = sh.NextGlyph() {
		g.X += shift
		if g.Flags&text.FlagClusterBreak != 0 {
			shift += track
		}
		asc, desc = max(asc, g.Ascent), max(desc, g.Descent)
		right = max(right, g.X+g.Advance)
		glyphs = append(glyphs, g)
	}
	if len(glyphs) == 0 {
		return layout.Dimensions{}, true
	}
	if right.Ceil() > gtx.Constraints.Max.X {
		return layout.Dimensions{}, false
	}
	// Same line box as widget.Label: the baseline sits at the ceiled ascent
	// and a single line is exactly ascent + descent tall.
	base := asc.Ceil()
	size := gtx.Constraints.Constrain(pt(right.Ceil(), base+desc.Ceil()))
	t := op.Offset(pt(glyphs[0].X.Round(), base)).Push(gtx.Ops)
	// Shape positions glyphs relative to the first; move the origin to the dot.
	outline := clip.Outline{Path: sh.Shape(glyphs)}.Op().Push(gtx.Ops)
	paint.ColorOp{Color: c}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	outline.Pop()
	t.Pop()
	return layout.Dimensions{Size: size, Baseline: size.Y - base}, true
}

// colorMacro records a ColorOp for widget.Label/Editor materials.
func colorMacro(ops *op.Ops, c color.NRGBA) op.CallOp {
	m := op.Record(ops)
	paint.ColorOp{Color: c}.Add(ops)
	return m.Stop()
}

// PluralUK picks the Ukrainian plural form for n: one (1, 21, 31 …),
// few (2–4, 22–24 …) or many (0, 5–20, 25–30 …) — "1 сцена", "3 сцени", "8 сцен".
func PluralUK(n int, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	switch m10, m100 := n%10, n%100; {
	case m10 == 1 && m100 != 11:
		return one
	case m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14):
		return few
	default:
		return many
	}
}
