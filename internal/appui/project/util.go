package project

import (
	"bytes"
	"fmt"
	"image"
	"math"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"golang.org/x/text/encoding/charmap"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

type (
	C = layout.Context
	D = layout.Dimensions
)

// ---- derived facts -------------------------------------------------------------------------

// facts are the derived states of a project snapshot (tabs, header, docks).
type facts struct {
	ver      int64
	clip     int
	hasPlan  bool
	anyFrame bool
	planning bool // a plan job runs
	anyVideo bool
	// done/enabled are indexed by step 1–5 (0 unused).
	done    [6]bool
	enabled [6]bool
	total   float64 // estimated running time (s)
	ndone   int     // completed steps
	// dynamic: Settings fit each scene's clip to its voice line (4–10 s).
	dynamic bool
}

// factsOf computes the facts of pv (cheap: one pass over the scenes).
func factsOf(c *core.Ctx, pv *core.ProjectView) facts {
	p := &pv.P
	f := facts{ver: pv.Ver, clip: c.E.ClipSeconds(), dynamic: c.E.Config().Get().Video.Dynamic}
	clip := float64(f.clip)
	f.hasPlan = len(p.Scenes) > 0
	allFrames, allVideos, allPrompts := f.hasPlan, f.hasPlan, f.hasPlan
	cfg := c.E.Config().Get()
	stockMode := cfg.Stock.Enabled || cfg.Video.Provider == "stock" || p.Input.Pipeline == "stock"
	for _, s := range p.Scenes {
		if s.Frame.Path != "" {
			f.anyFrame = true
		}
		if s.Video.Path != "" {
			f.anyVideo = true
		}
		allFrames = allFrames && (s.Frame.Status == studio.StatusDone || stockMode)
		allVideos = allVideos && s.Video.Status == studio.StatusDone
		allPrompts = allPrompts && strings.TrimSpace(s.ImagePrompt) != ""
		f.total += sceneSeconds(s, clip)
	}
	f.planning = pv.RunningKind() == studio.JobPlan || (pv.RunningKind() == studio.JobAutopilot && !f.hasPlan)
	f.done = [6]bool{false, f.hasPlan, allPrompts, allFrames, allVideos, p.Montage.Output.Status == studio.StatusDone}
	canVideo := f.anyFrame || f.hasPlan
	canMontage := f.anyFrame || f.anyVideo
	f.enabled = [6]bool{false, true, true, f.hasPlan, canVideo, canMontage}
	for n := 1; n <= 5; n++ {
		if f.done[n] {
			f.ndone++
		}
	}
	return f
}

// sceneSeconds is a scene's length: its clip (the clip chosen for it, else
// the clip setting) or its voice line plus a short tail, whichever is longer.
func sceneSeconds(s studio.Scene, clip float64) float64 {
	v := s.Video.Duration
	if v <= 0 && s.ClipLen > 0 {
		v = float64(s.ClipLen)
	}
	if v <= 0 {
		v = clip
	}
	if s.Audio.Duration > 0 {
		v = max(v, s.Audio.Duration+0.6)
	}
	return v
}

// ---- words ---------------------------------------------------------------------------------

// countWords counts runs of non-space characters (no allocation).
func countWords(t string) int {
	n, in := 0, false
	for _, r := range t {
		sp := unicode.IsSpace(r)
		if !sp && !in {
			n++
		}
		in = !sp
	}
	return n
}

// words is "<n> слово/слова/слів".
func words(n int) string { return core.Count(n, "слово", "слова", "слів") }

// scenesN is "<n> сцена/сцени/сцен".
func scenesN(n int) string { return core.Count(n, "сцена", "сцени", "сцен") }

// humanSeconds is a friendly length: "45 с", "2 хв 15 с" (rounded).
func humanSeconds(sec float64) string {
	if sec <= 0 || math.IsNaN(sec) {
		return "0 с"
	}
	return core.Duration(int(math.Round(sec)))
}

// firstText returns the first non-blank value.
func firstText(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// clipText shortens s to n runes with an ellipsis.
func clipText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}

// decodeText turns a text file into a string: UTF-8 (with or without a
// BOM), UTF-16 with a BOM (Notepad's "Unicode"), else Windows-1251 — the
// usual encoding of older Ukrainian .txt files. Line ends become "\n".
func decodeText(b []byte) string {
	var s string
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		s = string(b[3:])
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		le := b[0] == 0xFF
		b = b[2:]
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			if le {
				u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
			} else {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			}
		}
		s = string(utf16.Decode(u))
	case utf8.Valid(b):
		s = string(b)
	default:
		if d, err := charmap.Windows1251.NewDecoder().Bytes(b); err == nil {
			s = string(d)
		} else {
			s = strings.ToValidUTF8(string(b), "�")
		}
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// ---- small widgets ---------------------------------------------------------------------------

// statPill is a quiet fact in the header: an icon and a short text on the
// warm surface.
func statPill(th *gui.Theme, ic *gui.Icon, txt string) layout.Widget {
	return func(gtx C) D {
		h := gtx.Dp(30)
		padL, padR, gap := gtx.Dp(10), gtx.Dp(12), gtx.Dp(6)
		is := gtx.Dp(15)
		lg := gtx
		lg.Constraints = layout.Constraints{Max: image.Pt(max(0, gtx.Constraints.Max.X-padL-padR-is-gap), h)}
		m := op.Record(gtx.Ops)
		ld := gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: th.Pal.Text2, MaxLines: 1}.Layout(lg, th, txt)
		call := m.Stop()
		w := padL + is + gap + ld.Size.X + padR
		gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusPill, th.Pal.Surface)
		o := op.Offset(image.Pt(padL, (h-is)/2)).Push(gtx.Ops)
		ic.Layout(gtx, 15, th.Pal.Text2)
		o.Pop()
		o = op.Offset(image.Pt(padL+is+gap, (h-ld.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		o.Pop()
		return D{Size: image.Pt(w, h)}
	}
}

// sectionHead is a section title with a count and actions on the right
// (wrapping under the title when narrow).
func sectionHead(gtx C, th *gui.Theme, title, meta string, actions ...layout.Widget) D {
	text := func(gtx C) D {
		return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
			layout.Rigid(gui.Text{Style: th.H2.Sized(22), MaxLines: 1}.Widget(th, title)),
			layout.Rigid(func(gtx C) D {
				if meta == "" {
					return D{}
				}
				return layout.Inset{Left: gui.Sp12}.Layout(gtx, gui.Text{Style: th.Body, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, meta))
			}),
		)
	}
	if len(actions) == 0 {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return text(gtx)
	}
	acts := func(gtx C) D { return gui.HStack(gtx, gui.Sp8, actions...) }
	W := gtx.Constraints.Max.X
	ag := gtx
	ag.Constraints = layout.Constraints{Max: image.Pt(W, gtx.Constraints.Max.Y)}
	m := op.Record(gtx.Ops)
	ad := acts(ag)
	m.Stop()
	tg := gtx
	tg.Constraints = layout.Constraints{Max: image.Pt(W, gtx.Constraints.Max.Y)}
	m = op.Record(gtx.Ops)
	td := text(tg)
	m.Stop()
	if td.Size.X+ad.Size.X+gtx.Dp(16) > W {
		return gui.VStack(gtx, gui.Sp12, text, acts)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, text),
		layout.Rigid(acts),
	)
}

// collapse draws w clipped to a fraction a (0…1) of its height: banners
// and disclosures grow and shrink instead of jumping.
func collapse(gtx C, a float32, w layout.Widget) D {
	if a >= 0.999 {
		return w(gtx)
	}
	if a <= 0.001 {
		return D{Size: image.Pt(gtx.Constraints.Min.X, 0)}
	}
	m := op.Record(gtx.Ops)
	d := w(gtx)
	call := m.Stop()
	h := int(float32(d.Size.Y)*a + 0.5)
	defer clip.Rect(image.Rect(-gtx.Dp(40), -gtx.Dp(40), d.Size.X+gtx.Dp(40), h)).Push(gtx.Ops).Pop()
	gui.Fade(gtx, a, func(gtx C) D { call.Add(gtx.Ops); return d })
	return D{Size: image.Pt(d.Size.X, h)}
}

// field is a labelled block (label above w, hint under it).
func field(th *gui.Theme, label, hint string, w layout.Widget) layout.Widget {
	return func(gtx C) D {
		var ch []layout.FlexChild
		if label != "" {
			ch = append(ch, layout.Rigid(gui.Text{Style: th.Label, MaxLines: 1}.Widget(th, label)),
				layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout))
		}
		ch = append(ch, layout.Rigid(w))
		if hint != "" {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: 6}.Layout),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, hint)))
		}
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	}
}

// cardTitle is a card header: an icon in a soft square, a title and an
// optional note, and an optional trailing widget.
func cardTitle(th *gui.Theme, ic *gui.Icon, title, note string, trail layout.Widget) layout.Widget {
	return func(gtx C) D {
		var ch []layout.FlexChild
		if ic != nil {
			ch = append(ch, layout.Rigid(func(gtx C) D {
				s := gtx.Dp(36)
				gui.FillRRect(gtx, image.Rect(0, 0, s, s), 10, th.Pal.Surface)
				is := gtx.Dp(18)
				o := op.Offset(image.Pt((s-is)/2, (s-is)/2)).Push(gtx.Ops)
				ic.Layout(gtx, 18, th.Pal.Text)
				o.Pop()
				return D{Size: image.Pt(s, s)}
			}), layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout))
		}
		ch = append(ch, layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, title)),
				layout.Rigid(func(gtx C) D {
					if note == "" {
						return D{}
					}
					return gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Layout(gtx, th, note)
				}),
			)
		}))
		if trail != nil {
			ch = append(ch, layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout), layout.Rigid(trail))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
	}
}

// dp converts pixels back to dp (widths measured in px).
func dp(gtx C, px int) unit.Dp { return unit.Dp(float32(px) / gtx.Metric.PxPerDp) }

// plural helpers for other counts.
func charactersN(n int) string {
	return core.Count(n, "персонаж", "персонажі", "персонажів")
}
func locationsN(n int) string {
	return core.Count(n, "локація", "локації", "локацій")
}

// sprintf is fmt.Sprintf (short).
func sprintf(f string, a ...any) string { return fmt.Sprintf(f, a...) }

// pointerHand shows the hand cursor over the current clip area.
func pointerHand(gtx C) { pointer.CursorPointer.Add(gtx.Ops) }

// fillStack stacks ws with gap; when the parent asks for a minimum height
// (rows of equal-height cards) the last widget sits at the bottom, so
// controls line up across the row.
func fillStack(gtx C, gap unit.Dp, ws ...layout.Widget) D {
	if gtx.Constraints.Min.Y <= 0 || len(ws) < 2 {
		return gui.VStack(gtx, gap, ws...)
	}
	gtx.Constraints.Max.Y = gtx.Constraints.Min.Y
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	ch := make([]layout.FlexChild, 0, 2*len(ws)+1)
	for i, w := range ws {
		w := w
		if i == len(ws)-1 {
			ch = append(ch, layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, gtx.Constraints.Min.Y)} }))
		}
		if i > 0 {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: gap}.Layout))
		}
		ch = append(ch, layout.Rigid(func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			gtx.Constraints.Min.Y = 0
			return w(gtx)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

// topFade softens the edge where a scrolled page slides under the tabs.
func topFade(gtx C, th *gui.Theme, s *gui.Scroll) {
	pos := s.List.Position
	if pos.First == 0 && pos.Offset <= 0 {
		return
	}
	h := gtx.Dp(16)
	W := gtx.Constraints.Max.X
	defer clip.Rect(image.Rect(0, 0, W, h)).Push(gtx.Ops).Pop()
	paint.LinearGradientOp{
		Stop1: f32.Pt(0, 0), Color1: th.Pal.Ground,
		Stop2: f32.Pt(0, float32(h)), Color2: gui.WithAlpha(th.Pal.Ground, 0),
	}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// offlineText refuses generation in demo contexts (headless renders, tests),
// worded like core's refusal.
const offlineText = "Демо-режим: генерація вимкнена"

// maxDynamicClip is the longest clip dynamic lengths choose (s).
const maxDynamicClip = 10

// dynamicNote explains dynamic clip lengths in one line.
const dynamicNote = "довжина кожної сцени — під її репліку (4–10 с)"

// clipOf is the clip a scene's voice line has to fit: the length chosen for
// it, else the longest dynamic clip, else the clip setting.
func clipOf(s studio.Scene, f facts) float64 {
	switch {
	case s.ClipLen > 0:
		return float64(s.ClipLen)
	case f.dynamic:
		return maxDynamicClip
	}
	return float64(f.clip)
}
