package produce

// Local building blocks shared by steps 3 and 4: layout helpers, notices,
// media overlays (number badge, busy veil, empty caption), a clickable
// picture area, an animated expander and the engine action helpers (with
// the offline guard).

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

type (
	C = layout.Context
	D = layout.Dimensions
)

func pt(x, y int) image.Point { return image.Point{X: x, Y: y} }

func rectWH(w, h int) image.Rectangle { return image.Rectangle{Max: pt(w, h)} }

// record lays w out into a macro.
func record(gtx C, w layout.Widget) (D, op.CallOp) {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	return d, m.Stop()
}

// pxDp converts pixels to dp (kit fields take unit.Dp).
func pxDp(gtx C, px int) unit.Dp { return unit.Dp(float32(px) / gtx.Metric.PxPerDp) }

// drawAt replays a recorded widget at (x, y).
func drawAt(gtx C, x, y int, call op.CallOp) {
	o := op.Offset(pt(x, y)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
}

// loose gives w the width only (Min = 0).
func loose(gtx C) C {
	gtx.Constraints.Min = image.Point{}
	return gtx
}

// ---- stacks -----------------------------------------------------------------------------

// pinned stacks top (gap apart, empty widgets skipped) and puts bottom under
// it. When the constraints ask for more height (a stretched grid row) the
// extra space goes above bottom, so card actions line up across a row.
func pinned(gtx C, gap unit.Dp, top []layout.Widget, bottom layout.Widget) D {
	W := gtx.Constraints.Max.X
	g := gtx.Dp(gap)
	cg := gtx
	cg.Constraints = layout.Constraints{Max: pt(W, gtx.Constraints.Max.Y)}
	type rec struct {
		d D
		c op.CallOp
	}
	recs := make([]rec, 0, len(top))
	for _, w := range top {
		if w == nil {
			continue
		}
		d, c := record(cg, w)
		if d.Size.Y == 0 {
			continue
		}
		recs = append(recs, rec{d, c})
	}
	var bd D
	var bc op.CallOp
	if bottom != nil {
		bd, bc = record(cg, bottom)
	}
	total := 0
	for i, r := range recs {
		if i > 0 {
			total += g
		}
		total += r.d.Size.Y
	}
	if bd.Size.Y > 0 {
		if len(recs) > 0 {
			total += g
		}
		total += bd.Size.Y
	}
	extra := max(0, gtx.Constraints.Min.Y-total)
	y := 0
	for i, r := range recs {
		if i > 0 {
			y += g
		}
		drawAt(gtx, 0, y, r.c)
		y += r.d.Size.Y
	}
	if bd.Size.Y > 0 {
		if len(recs) > 0 {
			y += g
		}
		y += extra
		drawAt(gtx, 0, y, bc)
		y += bd.Size.Y
	}
	return D{Size: pt(W, max(y, gtx.Constraints.Min.Y))}
}

// vstack stacks the non-empty widgets gap apart at full width.
func vstack(gtx C, gap unit.Dp, ws ...layout.Widget) D { return pinned(gtx, gap, ws, nil) }

// spread puts left and right on one row (left takes the rest, both centred
// vertically) or stacks right under left when left would get less than
// minLeft.
func spread(gtx C, gap, minLeft unit.Dp, left, right layout.Widget) D {
	W := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: pt(W, gtx.Constraints.Max.Y)}
	if right == nil {
		d, c := record(cg, left)
		c.Add(gtx.Ops)
		return D{Size: pt(W, d.Size.Y)}
	}
	g := gtx.Dp(gap)
	rd, rc := record(cg, right)
	if lw := W - rd.Size.X - g; rd.Size.X > 0 && lw >= gtx.Dp(minLeft) {
		lg := gtx
		lg.Constraints = layout.Constraints{Max: pt(lw, gtx.Constraints.Max.Y)}
		ld, lc := record(lg, left)
		h := max(ld.Size.Y, rd.Size.Y)
		drawAt(gtx, 0, (h-ld.Size.Y)/2, lc)
		drawAt(gtx, W-rd.Size.X, (h-rd.Size.Y)/2, rc)
		return D{Size: pt(W, h)}
	}
	ld, lc := record(cg, left)
	lc.Add(gtx.Ops)
	if rd.Size.Y == 0 {
		return D{Size: pt(W, ld.Size.Y)}
	}
	drawAt(gtx, 0, ld.Size.Y+g, rc)
	return D{Size: pt(W, ld.Size.Y+g+rd.Size.Y)}
}

// hrow lays widgets left to right, vertically centred, gap apart.
func hrow(gtx C, gap unit.Dp, ws ...layout.Widget) D {
	ch := make([]layout.FlexChild, 0, 2*len(ws))
	for _, w := range ws {
		if w == nil {
			continue
		}
		if len(ch) > 0 {
			ch = append(ch, layout.Rigid(layout.Spacer{Width: gap}.Layout))
		}
		ch = append(ch, layout.Rigid(w))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
}

// ---- text -------------------------------------------------------------------------------

func label(th *gui.Theme, st gui.TextStyle, c color.NRGBA, lines int, s string) layout.Widget {
	return gui.Text{Style: st, Color: c, MaxLines: lines}.Widget(th, s)
}

// secs formats seconds with one decimal and a comma ("6,8 с").
func secs(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	s = strings.TrimSuffix(s, ".0")
	return strings.ReplaceAll(s, ".", ",") + " с"
}

// firstText returns the first non-blank string.
func firstText(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// clipText shortens s to n runes with an ellipsis.
func clipText(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// ---- section head ---------------------------------------------------------------------

// sectionHead is a titled block on the ground: a title with a count badge,
// a line of explanation under it and optional controls at the right.
func sectionHead(gtx C, th *gui.Theme, title, badge, desc string, right layout.Widget) D {
	left := func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return hrow(gtx, gui.Sp8,
					label(th, th.H3.Sized(20), color.NRGBA{}, 1, title),
					func(gtx C) D {
						if badge == "" {
							return D{}
						}
						return gui.Badge{Text: badge}.Layout(gtx, th)
					})
			}),
			layout.Rigid(func(gtx C) D {
				if desc == "" {
					return D{}
				}
				gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(780))
				return layout.Inset{Top: gui.Sp4}.Layout(gtx, label(th, th.Small.Sized(14), th.Pal.Text2, 3, desc))
			}),
		)
	}
	return spread(gtx, gui.Sp16, 280, left, right)
}

// ---- notices ----------------------------------------------------------------------------

// notice is a soft tinted box: an icon, a title in the tone's ink, a line
// of text and an optional action row (retry, dismiss).
type notice struct {
	Tone   gui.Tone
	Icon   *gui.Icon
	Title  string
	Text   string
	Lines  int // text lines (0 = 4)
	Action layout.Widget
	Small  bool // denser padding (inside cards)
}

func (n notice) Layout(gtx C, th *gui.Theme) D {
	_, soft, ink := th.Pal.ToneColors(n.Tone)
	pad := gtx.Dp(14)
	if n.Small {
		pad = gtx.Dp(10)
	}
	lines := n.Lines
	if lines == 0 {
		lines = 4
	}
	W := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: pt(max(0, W-2*pad), gtx.Constraints.Max.Y)}
	is := unit.Dp(18)
	if n.Small {
		is = 16
	}
	d, call := record(cg, func(gtx C) D {
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				if n.Icon == nil {
					return D{}
				}
				return layout.Inset{Top: 1, Right: gui.Sp8}.Layout(gtx, n.Icon.Widget(is, ink))
			}),
			layout.Flexed(1, func(gtx C) D {
				var ch []layout.FlexChild
				tst := th.Label.Weighted(gui.FontSemiBold)
				bst := th.Small
				if n.Small {
					tst = th.Small.Weighted(gui.FontSemiBold)
					bst = th.Caption.Lined(1.45)
				}
				if n.Title != "" {
					ch = append(ch, layout.Rigid(label(th, tst, ink, 2, n.Title)))
				}
				if n.Text != "" {
					if len(ch) > 0 {
						ch = append(ch, layout.Rigid(layout.Spacer{Height: 2}.Layout))
					}
					ch = append(ch, layout.Rigid(label(th, bst, th.Pal.Text2, lines, n.Text)))
				}
				if n.Action != nil {
					ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout), layout.Rigid(n.Action))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
			}),
		)
	})
	size := pt(W, d.Size.Y+2*pad)
	gui.FillRRect(gtx, rectWH(size.X, size.Y), gui.RadiusControl, soft)
	drawAt(gtx, pad, pad, call)
	return D{Size: size}
}

// ---- media overlays --------------------------------------------------------------------

// numBadge is the scene number on a white pill (image overlays).
func numBadge(gtx C, th *gui.Theme, n int) D {
	s := fmt.Sprint(n)
	h := gtx.Dp(26)
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(gtx.Dp(80), h)}
	d, call := record(lg, label(th, th.Small.Weighted(gui.FontSemiBold), th.Pal.Text, 1, s))
	w := max(h, d.Size.X+gtx.Dp(16))
	r := rectWH(w, h)
	gui.Shadow(gtx, th, r, gui.RadiusPill, gui.ElevControl)
	gui.FillRRect(gtx, r, gui.RadiusPill, th.Pal.Card)
	drawAt(gtx, (w-d.Size.X)/2, (h-d.Size.Y)/2, call)
	return D{Size: pt(w, h)}
}

// corner lays w out in a corner of size, m px from the edges.
func corner(gtx C, size image.Point, m int, right, bottom bool, w layout.Widget) D {
	cg := gtx
	cg.Constraints = layout.Constraints{Max: pt(max(0, size.X-2*m), max(0, size.Y-2*m))}
	d, call := record(cg, w)
	x, y := m, m
	if right {
		x = size.X - m - d.Size.X
	}
	if bottom {
		y = size.Y - m - d.Size.Y
	}
	drawAt(gtx, x, y, call)
	return d
}

// veil fades the busy veil in and out (the picture never pops between
// "working" and "done").
type veil struct {
	a    gui.Anim
	ring gui.Anim
	pct  int
}

// Layout draws the veil while on (and while it fades out).
func (v *veil) Layout(gtx C, th *gui.Theme, size image.Point, on bool, pct int) {
	v.a.Duration, v.a.Ease = 220*time.Millisecond, gui.EaseOutCubic
	a := v.a.Animate(gtx, b2f(on))
	if on {
		v.pct = pct
	}
	if a <= 0.01 {
		return
	}
	gui.Fade(gtx, a, func(gtx C) D {
		busyVeil(gtx, th, size, v.pct, &v.ring)
		return D{Size: size}
	})
}

// busyVeil dims a picture and shows the progress in a ring on a white disc
// (pct < 0 spins: queued or no progress reported yet).
func busyVeil(gtx C, th *gui.Theme, size image.Point, pct int, ring *gui.Anim) {
	gui.FillRect(gtx, rectWH(size.X, size.Y), gui.WithAlpha(th.Pal.Scrim, 0.7))
	disc := gtx.Dp(68)
	if min(size.X, size.Y) < gtx.Dp(130) {
		disc = gtx.Dp(48)
	}
	x, y := (size.X-disc)/2, (size.Y-disc)/2
	o := op.Offset(pt(x, y)).Push(gtx.Ops)
	gui.Shadow(gtx, th, rectWH(disc, disc), gui.RadiusPill, gui.ElevPop)
	gui.FillCircle(gtx, pxDp(gtx, disc), th.Pal.Card)
	rs := disc - gtx.Dp(10)
	r := gui.ProgressRing{Value: -1, Size: pxDp(gtx, rs), Tone: gui.ToneRunning, Label: "-", Anim: ring}
	if pct > 0 {
		r.Value, r.Label = float32(min(100, pct))/100, ""
		if disc < gtx.Dp(60) {
			r.Label = "-"
		}
	}
	o2 := op.Offset(pt((disc-rs)/2, (disc-rs)/2)).Push(gtx.Ops)
	r.Layout(gtx, th)
	o2.Pop()
	o.Pop()
}

// emptyCaption writes a short line under the placeholder icon of an empty
// Thumb (the icon sits in the middle).
func emptyCaption(gtx C, th *gui.Theme, size image.Point, s string) {
	if size.Y < gtx.Dp(90) || s == "" {
		return
	}
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(max(0, size.X-gtx.Dp(24)), gtx.Dp(40))}
	d, call := record(lg, gui.Text{Style: th.Caption.Weighted(gui.FontMedium), Color: th.Pal.Text3, MaxLines: 2, Align: text.Middle}.Widget(th, s))
	icon := min(40, max(16, float32(min(size.X, size.Y))/gtx.Metric.PxPerDp*0.28))
	y := size.Y/2 + gtx.Dp(unit.Dp(icon))/2 + gtx.Dp(8)
	drawAt(gtx, (size.X-d.Size.X)/2, y, call)
}

// pressArea is a clickable picture: hand cursor, a hover wash and a small
// "expand" hint in the corner. Read Clicked before Layout.
type pressArea struct {
	widget.Clickable
	hover gui.Anim
}

// Layout registers the area over size and returns the hover amount (0…1).
func (p *pressArea) Layout(gtx C, th *gui.Theme, size image.Point, hint bool) float32 {
	p.Clickable.Layout(gtx, func(gtx C) D {
		defer clip.Rect(rectWH(size.X, size.Y)).Push(gtx.Ops).Pop()
		pointer.CursorPointer.Add(gtx.Ops)
		return D{Size: size}
	})
	p.hover.Duration = gui.DurHover
	h := p.hover.Animate(gtx, b2f(p.Clickable.Hovered()))
	if h > 0.01 && hint {
		gui.FillRect(gtx, rectWH(size.X, size.Y), gui.WithAlpha(th.Pal.Text, 0.08*h))
		b := gtx.Dp(32)
		m := gtx.Dp(10)
		o := op.Offset(pt(size.X-m-b, size.Y-m-b)).Push(gtx.Ops)
		gui.Fade(gtx, h, func(gtx C) D {
			gui.FillCircle(gtx, pxDp(gtx, b), th.Pal.Overlay)
			is := gtx.Dp(16)
			o := op.Offset(pt((b-is)/2, (b-is)/2)).Push(gtx.Ops)
			gui.IconMaximize2.Layout(gtx, 16, th.Pal.OverlayInk)
			o.Pop()
			return D{Size: pt(b, b)}
		})
		o.Pop()
	}
	return h
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// expander animates height changes of its content (an editor opening, a
// prompt collapsing) and clips while it moves.
type expander struct {
	a gui.Anim
}

func (e *expander) Layout(gtx C, w layout.Widget) D {
	d, call := record(gtx, w)
	e.a.Duration, e.a.Ease = 200*time.Millisecond, gui.EaseInOutCubic
	h := int(e.a.Animate(gtx, float32(d.Size.Y)) + 0.5)
	if h == d.Size.Y {
		call.Add(gtx.Ops)
		return d
	}
	cl := clip.Rect(rectWH(d.Size.X, h)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	cl.Pop()
	return D{Size: pt(d.Size.X, h)}
}

// highlight flashes an action-coloured ring around the card the page just
// scrolled to (fade in, hold, fade out).
type highlight struct {
	id    string
	start time.Time
	armed bool
}

const (
	hlIn   = 160 * time.Millisecond
	hlHold = 1300 * time.Millisecond
	hlOut  = 450 * time.Millisecond
)

// Flash starts the highlight of id with the next frame.
func (h *highlight) Flash(id string) { h.id, h.armed, h.start = id, true, time.Time{} }

// Alpha is the ring strength of id at gtx.Now.
func (h *highlight) Alpha(gtx C, id string) float32 {
	if !h.armed || id != h.id {
		return 0
	}
	if h.start.IsZero() {
		h.start = gtx.Now
	}
	el := gtx.Now.Sub(h.start)
	if el >= hlIn+hlHold+hlOut {
		h.armed = false
		return 0
	}
	gtx.Execute(op.InvalidateCmd{})
	switch {
	case gui.ReducedMotion():
		return 1
	case el < hlIn:
		return gui.EaseOutCubic(float32(el) / float32(hlIn))
	case el < hlIn+hlHold:
		return 1
	}
	return 1 - gui.EaseInCubic(float32(el-hlIn-hlHold)/float32(hlOut))
}

// ring draws the highlight around a card of size (drawn after the card).
func hlRing(gtx C, th *gui.Theme, size image.Point, a float32) {
	if a <= 0.01 {
		return
	}
	r := rectWH(size.X, size.Y)
	gui.Border(gtx, r, gui.RadiusCard, 2.5, gui.WithAlpha(th.Pal.Action, a))
	gui.FocusRing(gtx, th, r, gui.RadiusCard, a)
}

// ---- engine actions ----------------------------------------------------------------------

// pickImage lets the user put their own picture in place of a reference
// or a frame (the copy runs off the UI goroutine).
func pickImage(c *core.Ctx, id string, kind studio.RegenKind, item string) {
	c.PickFile("Вибери зображення", []string{"png", "jpg", "jpeg", "webp"}, func(path string) {
		c.Do(func() error { return c.E.ImportImage(id, kind, item, path) },
			func() { c.Toast(gui.ToneSuccess, "Зображення завантажено") })
	})
}

func goStep(c *core.Ctx, id string, step int) {
	c.Navigate(core.Route{Page: core.PageProject, ProjectID: id, Step: step})
}

// busy reports an asset that is queued or being generated.
func busy(a studio.Asset) bool {
	return a.Status == studio.StatusRunning || a.Status == studio.StatusQueued
}

// assetPill is the status pill of an asset (no "0 %" while an image model
// reports no progress).
func assetPill(a studio.Asset) gui.StatusPill {
	st, pct := core.AssetState(a)
	if st == gui.StateRunning && pct <= 0 {
		pct = -1
	}
	return gui.StatusPill{State: st, Percent: pct}
}

// busyPct is the progress to show on a busy asset (-1 = spin).
func busyPct(a studio.Asset) int {
	if a.Status == studio.StatusRunning && a.Progress > 0 {
		return a.Progress
	}
	return -1
}

// assetErr is the user-facing text of a failed asset: the service's exact
// error as the engine stored it (its name, code and own words) and a short
// hint (studio.FailureHint) — never a stand-in text.
func assetErr(a studio.Asset) string {
	s := strings.TrimSpace(a.Error)
	if s == "" {
		return "Сервіс завершив задачу з помилкою, але не повернув її тексту."
	}
	s = clipText(s, 1200)
	if h := studio.FailureHint(a.Error); h != "" {
		s += "\nПідказка: " + h
	}
	return s
}

// errLines is how many lines a failure notice shows: the whole exact
// error, within reason.
const errLines = 10

// ---- job box ------------------------------------------------------------------------------

// jobBox shows a running job (or the stale batch) inside a step header: a
// spinner, what runs, the percentage, "Зупинити" and a progress bar.
type jobBox struct {
	stop gui.Clickable
	bar  gui.Anim
}

// Layout draws the box; stopped reports a click on "Зупинити".
func (j *jobBox) Layout(gtx C, th *gui.Theme, title, msg string, pct int) (d D, stopped bool) {
	stopped = j.stop.Clicked(gtx)
	_, soft, ink := th.Pal.ToneColors(gui.ToneRunning)
	pad := gtx.Dp(16)
	W := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: pt(max(0, W-2*pad), gtx.Constraints.Max.Y)}
	cd, call := record(cg, func(gtx C) D {
		return vstack(gtx, gui.Sp12,
			func(gtx C) D {
				left := func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(gui.Spinner{Size: 20, Color: ink}.Widget(th)),
						layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
						layout.Flexed(1, func(gtx C) D {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(label(th, th.Label.Weighted(gui.FontSemiBold), ink, 1, title)),
								layout.Rigid(func(gtx C) D {
									if msg == "" {
										return D{}
									}
									return label(th, th.Small, th.Pal.Text2, 1, msg)(gtx)
								}),
							)
						}),
					)
				}
				right := func(gtx C) D {
					return hrow(gtx, gui.Sp12,
						func(gtx C) D {
							if pct < 0 {
								return D{}
							}
							return label(th, th.BodyStrong, th.Pal.Text, 1, fmt.Sprintf("%d%%", pct))(gtx)
						},
						func(gtx C) D {
							return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Text: "Зупинити", Icon: gui.IconSquare,
								Tip: "Нові задачі не стартують; ті, що вже йдуть, доробляться"}.Layout(gtx, th, &j.stop)
						})
				}
				return spread(gtx, gui.Sp16, 200, left, right)
			},
			func(gtx C) D {
				v := float32(-1)
				if pct >= 0 {
					v = float32(max(2, pct)) / 100
				}
				return gui.ProgressBar{Value: v, Height: 6, Tone: gui.ToneRunning, Anim: &j.bar, Track: gui.WithAlpha(th.Pal.Card, 0.7)}.Layout(gtx, th)
			},
		)
	})
	size := pt(W, cd.Size.Y+2*pad)
	gui.FillRRect(gtx, rectWH(size.X, size.Y), gui.RadiusInner, soft)
	drawAt(gtx, pad, pad, call)
	return D{Size: size}, stopped
}

// ---- small drawing ----------------------------------------------------------------------

// dot is a small filled circle (legend rows).
func dot(gtx C, size unit.Dp, c color.NRGBA) D {
	px := gtx.Dp(size)
	gui.FillCircle(gtx, size, c)
	return D{Size: pt(px, px)}
}

// sceneTitle is a scene's title or "Сцена n".
func sceneTitle(s *studio.Scene, n int) string {
	if t := strings.TrimSpace(s.Title); t != "" {
		return t
	}
	return fmt.Sprintf("Сцена %d", n)
}

// castLine names who and where a scene is ("Тарас, Лисиця · Осінній ліс").
func castLine(pv *core.ProjectView, s *studio.Scene) string {
	var names []string
	for _, id := range s.CharacterIDs {
		if ch := pv.Character(id); ch != nil && strings.TrimSpace(ch.Name) != "" {
			names = append(names, strings.TrimSpace(ch.Name))
		}
	}
	out := strings.Join(names, ", ")
	if l := pv.Location(s.LocationID); l != nil && strings.TrimSpace(l.Name) != "" {
		if out != "" {
			out += " · "
		}
		out += strings.TrimSpace(l.Name)
	}
	return out
}

// ratioOf is the width/height of the project's frames.
func ratioOf(aspect string) float32 {
	if aspect == "9:16" {
		return 9.0 / 16
	}
	return 16.0 / 9
}
