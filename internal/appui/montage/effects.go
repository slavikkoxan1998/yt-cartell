package montage

// Motion effects for stills (Ken Burns and friends): the gallery card and
// the preview transform shared with the monitor. The motion table mirrors
// internal/media/effects.go (unexported there): every effect is a moving
// window over the frame — zoom z, centre (x, y) as a share of the free
// travel, rotation r — as a function of the scene progress.

import (
	"image"
	"math"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

type motion struct {
	z0, z1, x0, x1, y0, y1, r0, r1, shake float64
	ease                                  bool
}

// motions mirrors media's table (keep in sync when effects are added).
var motions = map[string]motion{
	"zoomin":   {z0: 1, z1: 1.12},
	"zoomout":  {z0: 1.12, z1: 1},
	"panleft":  {z0: 1.12, z1: 1.12, x0: 0.9, x1: -0.9},
	"panright": {z0: 1.12, z1: 1.12, x0: -0.9, x1: 0.9},
	"panup":    {z0: 1.12, z1: 1.12, y0: 0.9, y1: -0.9},
	"pandown":  {z0: 1.12, z1: 1.12, y0: -0.9, y1: 0.9},
	"diagonal": {z0: 1.06, z1: 1.18, x0: -0.8, x1: 0.8, y0: -0.8, y1: 0.8},
	"pushin":   {z0: 1, z1: 1.3, ease: true},
	"drift":    {z0: 1.05, z1: 1.08, x0: -0.5, x1: 0.5, y0: 0.25, y1: -0.25},
	"shake":    {z0: 1.07, z1: 1.07, shake: 0.006},
	"rotate":   {z0: 1.07, z1: 1.1, r0: -1.2, r1: 1.2},
}

const shakeRotation = 0.25 / 0.006

// motionTransform returns the transform that moves a W×H picture by effect
// id at progress p (0…1) and time sec (handheld wobble). clip runs the
// effect at half strength (it is added to moving footage). ok is false for
// "none" / unknown ids.
func motionTransform(id string, W, H float32, p float32, sec float64, clip bool) (f32.Affine2D, bool) {
	m, ok := motions[id]
	if !ok || W <= 0 || H <= 0 {
		return f32.AffineId(), false
	}
	if clip {
		k := 0.5
		m.z0, m.z1 = 1+(m.z0-1)*k, 1+(m.z1-1)*k
		m.r0, m.r1, m.shake = m.r0*k, m.r1*k, m.shake*k
	}
	// Safe zoom: a rotated or shaken window never leaves the frame.
	rot := math.Max(math.Abs(m.r0), math.Abs(m.r1)) + m.shake*shakeRotation
	th := rot * math.Pi / 180
	ar := math.Max(float64(W/H), float64(H/W))
	need := math.Cos(th) + ar*math.Sin(th)
	if m.shake > 0 {
		need += 2 * m.shake * float64(W+H) / 2 / float64(min(W, H))
	}
	if need > 1 {
		need += 0.005
	}
	m.z0, m.z1 = math.Max(m.z0, need), math.Max(m.z1, need)

	e := float64(clamp32(p, 0, 1))
	if m.ease {
		e = e * e * (3 - 2*e)
	}
	lerp := func(a, b float64) float64 { return a + (b-a)*e }
	z := lerp(m.z0, m.z1)
	hw, hh := float64(W)/2, float64(H)/2
	cx := hw + lerp(m.x0, m.x1)*hw*(1-1/z)
	cy := hh + lerp(m.y0, m.y1)*hh*(1-1/z)
	theta := lerp(m.r0, m.r1) * math.Pi / 180
	if m.shake > 0 {
		amp := m.shake * float64(W+H) / 2
		cx += amp * (0.6*math.Sin(5.215*sec) + 0.4*math.Sin(12.13*sec+1.7))
		cy += amp * 0.8 * (0.6*math.Sin(4.084*sec+0.9) + 0.4*math.Sin(9.425*sec+2.3))
		theta += m.shake * shakeRotation * math.Pi / 180 * math.Sin(3.833*sec+0.4)
	}
	a := f32.AffineId().
		Offset(f32.Pt(-float32(cx), -float32(cy))).
		Rotate(f32.Point{}, -float32(theta)).
		Scale(f32.Point{}, f32.Pt(float32(z), float32(z))).
		Offset(f32.Pt(W/2, H/2))
	return a, true
}

// drawMotion draws img covering size, moved by effect id at progress p.
func drawMotion(gtx C, img paint.ImageOp, size image.Point, id string, p float32, sec float64, clip bool) {
	a, ok := motionTransform(id, float32(size.X), float32(size.Y), p, sec, clip)
	if !ok {
		drawImage(gtx, img, size, true)
		return
	}
	st := op.Affine(a).Push(gtx.Ops)
	drawImage(gtx, img, size, true)
	st.Pop()
}

// effectIcon is a small symbol for a motion effect.
func effectIcon(id string) *gui.Icon {
	switch id {
	case "none":
		return gui.IconSquare
	case "zoomin":
		return gui.IconZoomIn
	case "zoomout":
		return gui.IconMinimize2
	case "panleft":
		return gui.IconArrowLeft
	case "panright":
		return gui.IconArrowRight
	case "panup":
		return gui.IconArrowUp
	case "pandown":
		return gui.IconArrowDown
	case "diagonal":
		return gui.IconArrowUpRight
	case "pushin":
		return gui.IconTarget
	case "drift":
		return gui.IconMove
	case "shake":
		return gui.IconActivity
	case "rotate":
		return gui.IconRotateCw
	}
	return gui.IconMove
}

// effectLoop maps looping preview time to motion progress (a slow move
// with a short rest at both ends).
func effectLoop(sec float64) float32 {
	const hold, dur = 0.25, 2.2
	u := math.Mod(sec, dur+2*hold)
	switch {
	case u < hold:
		return 0
	case u < hold+dur:
		return float32((u - hold) / dur)
	}
	return 1
}

// ---- the card ----------------------------------------------------------------------------------------

type effectsCard struct {
	tiles []tile
	reset gui.Clickable
}

func (s *proj) effectsCard(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.fx
	m := pv.P.Montage
	id := pv.ID
	cur := m.Effect
	if _, ok := media.EffectByID(cur); !ok {
		cur = media.DefaultEffect
	}
	own := 0
	for _, sc := range pv.P.Scenes {
		if sc.Effect != "" {
			own++
		}
	}
	if st.reset.Clicked(gtx) && own > 0 {
		c.Edit(id, func(p *studio.Project) {
			for i := range p.Scenes {
				p.Scenes[i].Effect = ""
			}
		})
	}
	pr := s.pair(c, pv, 240)
	ts := tiles(&st.tiles, len(media.Effects))
	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		sub := "Як оживає кадр, коли в сцени немає відеокліпу. Для кліпу рух можна додати окремо в інспекторі сцени."
		return gui.VStack(gtx, gui.Sp16,
			func(gtx C) D { return cardHeader(gtx, th, gui.IconMove, "Ефекти руху", sub, nil) },
			func(gtx C) D {
				return gui.Grid{MinWidth: galleryMin(gtx), Gap: gui.Sp12}.Layout(gtx, len(media.Effects), func(gtx C, i int) D {
					e := media.Effects[i]
					return ts[i].layout(gtx, th, tileStyle{Label: e.Label, Tip: e.Hint, Selected: e.ID == cur,
						Preview: func(gtx C, size image.Point, sec float64) {
							p := float32(0.55)
							if sec >= 0 {
								p = effectLoop(sec)
							}
							drawMotion(gtx, pr.a, size, e.ID, p, math.Max(sec, 0.8), false)
							// Motion badge.
							d := gtx.Dp(22)
							x, y := gtx.Dp(6), size.Y-d-gtx.Dp(6)
							fillCircle(gtx, float32(x)+float32(d)/2, float32(y)+float32(d)/2, float32(d)/2, th.Pal.Overlay)
							is := gtx.Dp(13)
							o := op.Offset(pt(x+(d-is)/2, y+(d-is)/2)).Push(gtx.Ops)
							effectIcon(e.ID).Layout(gtx, 13, th.Pal.OverlayInk)
							o.Pop()
						}}, func() {
						eid := e.ID
						c.Edit(id, func(p *studio.Project) { p.Montage.Effect = eid })
					})
				})
			},
			func(gtx C) D {
				var note string
				switch {
				case s.film.stills > 0:
					note = count(s.film.stills, "сцена", "сцени", "сцен") + " без кліпу " + gui.PluralUK(s.film.stills, "покаже", "покажуть", "покажуть") + " кадр із цим рухом."
				case len(s.film.items) > 0:
					note = "Зараз усі сцени мають кліпи — рух знадобиться, якщо якийсь кліп прибрати чи він не вдасться."
				default:
					note = "Рух оживить кадри, поки кліпів ще немає."
				}
				if own > 0 {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, count(own, "сцена має", "сцени мають", "сцен мають")+" свій рух")),
						layout.Rigid(func(gtx C) D {
							return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconRotateCcw, Text: "Скинути до спільного"}.Layout(gtx, th, &st.reset)
						}),
					)
				}
				return hint(th, note)(gtx)
			},
		)
	})
}
