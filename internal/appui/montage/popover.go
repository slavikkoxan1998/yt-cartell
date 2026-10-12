package montage

// The popover of a transition chip: the catalog as animated previews on the
// scene's own frames, anchored under (or over) the chip, above everything.
// An outside press or Escape closes it.

import (
	"image"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

type transPopover struct {
	sceneID string
	anim    gui.Anim
	scroll  gui.Scroll
	tiles   []tile
	useDef  gui.Clickable
	close   gui.Clickable
	scrim   int
	keys    int
	focused bool
	opened  time.Time
}

// open shows the popover for the cut after scene id (toggles when open).
func (p *transPopover) open(id string) {
	if p.sceneID == id {
		p.sceneID = ""
		return
	}
	p.sceneID, p.focused = id, false
	p.anim.Set(0)
	p.scroll.List.Position = layout.Position{}
}

func (p *transPopover) layout(gtx C, c *core.Ctx, pv *core.ProjectView, s *proj) {
	if p.sceneID == "" {
		return
	}
	th := c.Th
	th.WantAnchors()
	id := p.sceneID
	sc := sceneOf(pv, id)
	it := s.film.item(id)
	k, ok := s.film.pos[id]
	if sc == nil || it == nil || !ok || k >= len(s.film.items)-1 {
		p.sceneID = ""
		return
	}
	// Close: the × button, Escape, a press outside.
	if p.close.Clicked(gtx) {
		p.sceneID = ""
		return
	}
	for {
		ev, ok := gtx.Event(
			pointer.Filter{Target: &p.scrim, Kinds: pointer.Press},
			key.FocusFilter{Target: &p.keys},
			key.Filter{Focus: &p.keys, Name: key.NameEscape},
		)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case pointer.Event:
			if e.Kind == pointer.Press {
				p.sceneID = ""
			}
		case key.Event:
			if e.State == key.Press {
				p.sceneID = ""
			}
		}
	}
	if p.sceneID == "" {
		return
	}
	anchor, found := th.AnchorRect("mont.chip:" + id)
	win := th.WindowSize()
	if !found || win.X == 0 {
		gtx.Execute(op.InvalidateCmd{})
		return
	}
	m := pv.P.Montage
	def := projectTransition(m)
	pid := pv.ID
	pick := func(v string) {
		c.Edit(pid, func(p *studio.Project) {
			for i := range p.Scenes {
				if p.Scenes[i].ID == id {
					p.Scenes[i].Transition = v
				}
			}
		})
		p.sceneID = ""
	}
	if p.useDef.Clicked(gtx) {
		pick("")
	}
	// Preview pictures: this scene → the next one.
	next := sceneOf(pv, s.film.items[k+1].SceneID)
	pics := transPics{}
	syn := synthetic()
	pics.a, pics.b, pics.tinyA, pics.tinyB = syn.a, syn.b, syn.tinyA, syn.tinyB
	if img, ok := sceneImage(c, pv, sc, 240); ok {
		pics.a = img
	}
	if img, ok := sceneImage(c, pv, next, 240); ok {
		pics.b = img
	}
	if t, ok := tinyImage(c, pv, sc); ok {
		pics.tinyA = t
	}
	if t, ok := tinyImage(c, pv, next); ok {
		pics.tinyB = t
	}
	ts := tiles(&p.tiles, len(media.Transitions))
	num := sceneNumber(&pv.P, id)

	th.Layer(gtx, func(gtx C) {
		p.anim.Duration, p.anim.Ease = 160*time.Millisecond, gui.EaseOutCubic
		a := p.anim.Animate(gtx, 1)
		margin := gtx.Dp(12)
		w := min(gtx.Dp(420), win.X-2*margin)
		pad := gtx.Dp(16)
		inner := w - 2*pad
		// Content, measured.
		head := func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), MaxLines: 1}.Widget(th, "Перехід після сцени "+itoa(num))),
						layout.Rigid(gui.Text{Style: th.Caption, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, "Перегляд — на твоїх кадрах, наведи, щоб побачити рух")),
					)
				}),
				layout.Rigid(func(gtx C) D {
					return gui.IconButton{Icon: gui.IconX, Size: 32, Tip: "Закрити"}.Layout(gtx, th, &p.close)
				}),
			)
		}
		defRow := func(gtx C) D {
			return gui.Chip{Text: "Як у проєкті · " + transitionLabel(def), Selected: sc.Transition == "" || sc.Transition == def,
				Icon: gui.IconRotateCcw}.Layout(gtx, th, &p.useDef)
		}
		hg := gtx
		hg.Constraints = layout.Constraints{Min: pt(inner, 0), Max: pt(inner, gtx.Dp(200))}
		hd, hc := measure(hg, func(gtx C) D { return gui.VStack(gtx, gui.Sp12, head, defRow) })
		gridH := min(gtx.Dp(360), win.Y/2)
		h := pad + hd.Size.Y + gtx.Dp(12) + gridH + pad/2
		// Place under the chip, or over it when there is more room there.
		x := (anchor.Min.X+anchor.Max.X)/2 - w/2
		x = max(margin, min(x, win.X-margin-w))
		y := anchor.Max.Y + gtx.Dp(8)
		up := false
		if y+h > win.Y-margin && anchor.Min.Y-gtx.Dp(8)-h > margin {
			y, up = anchor.Min.Y-gtx.Dp(8)-h, true
		}
		y = max(margin, min(y, win.Y-margin-h))
		// Scrim: an outside press closes.
		sc2 := clip.Rect(image.Rectangle{Max: win}).Push(gtx.Ops)
		event.Op(gtx.Ops, &p.scrim)
		sc2.Pop()
		rise := float32(gtx.Dp(6)) * (1 - a)
		if up {
			rise = -rise
		}
		off := op.Affine(f32.AffineId().Offset(f32.Pt(float32(x), float32(y)+rise))).Push(gtx.Ops)
		defer off.Pop()
		op1 := paint.PushOpacity(gtx.Ops, a)
		defer op1.Pop()
		r := image.Rectangle{Max: pt(w, h)}
		gui.Shadow(gtx, th, r, gui.RadiusInner, gui.ElevPop)
		gui.Box(gtx, r, gui.RadiusInner, th.Pal.Card, gui.WithAlpha(th.Pal.Border, 0.8))
		cl := clip.UniformRRect(r, gtx.Dp(gui.RadiusInner)).Push(gtx.Ops)
		defer cl.Pop()
		event.Op(gtx.Ops, &p.keys) // presses inside are not "outside"
		if !p.focused {
			p.focused = true
			gtx.Execute(key.FocusCmd{Tag: &p.keys})
		}
		o := op.Offset(pt(pad, pad)).Push(gtx.Ops)
		hc.Add(gtx.Ops)
		o.Pop()
		o = op.Offset(pt(pad/2, pad+hd.Size.Y+gtx.Dp(12))).Push(gtx.Ops)
		lg := gtx
		lg.Constraints = layout.Exact(pt(w-pad, gridH))
		cols := 3
		gap := gtx.Dp(10)
		cw := (w - pad - pad/2 - gap*(cols-1)) / cols
		rows := (len(media.Transitions) + cols - 1) / cols
		p.scroll.Layout(lg, th, rows, func(gtx C, row int) D {
			hmax := 0
			var calls []op.CallOp
			for col := 0; col < cols; col++ {
				i := row*cols + col
				if i >= len(media.Transitions) {
					break
				}
				t := media.Transitions[i]
				cg := gtx
				cg.Constraints = layout.Constraints{Min: pt(cw, 0), Max: pt(cw, 1<<16)}
				selected := sc.Transition != "" && sc.Transition != def && sc.Transition == t.ID
				d, call := measure(cg, func(gtx C) D {
					return ts[i].layout(gtx, th, tileStyle{Label: t.Label, Tip: t.Hint, Selected: selected,
						Preview: func(gtx C, size image.Point, sec float64) {
							pr := restProgress(t)
							if sec >= 0 {
								pr = loopProgress(sec)
							}
							drawTransition(gtx, t, pics, pr, size)
						}}, func() { pick(t.ID) })
				})
				hmax = max(hmax, d.Size.Y)
				calls = append(calls, call)
			}
			for col, call := range calls {
				o := op.Offset(pt(pad/2+col*(cw+gap), 0)).Push(gtx.Ops)
				call.Add(gtx.Ops)
				o.Pop()
			}
			return D{Size: pt(w-pad, hmax+gtx.Dp(12))}
		})
		o.Pop()
	})
}
