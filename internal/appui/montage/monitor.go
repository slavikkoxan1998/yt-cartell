package montage

// The preview monitor: the film at the playhead as a live draft — the
// scene's frame with its colour look and motion effect, the transitions
// between scenes, fades, and burnt-in subtitles — plus play/pause (the
// playhead runs in real time and each voice line starts as it is reached)
// and scene skipping. It is a draft of the montage, not the render: clips
// show their first frame.

import (
	"image"
	"image/color"
	"math"
	"sort"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

type monitor struct {
	play, prev, next gui.Clickable
	playing          bool
	start            time.Time
	from             float64
	lastT            float64
}

// stop pauses playback (and the voice it started).
func (m *monitor) stop(c *core.Ctx) {
	if m.playing {
		m.playing = false
		c.Audio.Stop()
	}
}

// monitorSize is the picture size of the monitor for an inner card width.
func monitorSize(gtx C, inner int, ratio float32, wide bool) image.Point {
	if !wide {
		w := inner
		if ratio < 1 {
			w = min(inner, gtx.Dp(240))
		}
		return pt(w, int(float32(w)/ratio))
	}
	if ratio < 1 {
		// A Shorts picture: tall, about as high as the inspector beside it.
		h := int(clampf(float64(inner)*0.36, float64(gtx.Dp(300)), float64(gtx.Dp(420))))
		return pt(int(float32(h)*ratio), h)
	}
	w := int(clampf(float64(inner)*0.4, float64(gtx.Dp(320)), float64(gtx.Dp(620))))
	return pt(w, int(float32(w)/ratio))
}

// monitorBlock is the picture with its transport controls.
func (s *proj) monitorBlock(gtx C, c *core.Ctx, pv *core.ProjectView, wide bool) D {
	th := c.Th
	m := &s.mon
	f := &s.film
	t := &s.tl
	ratio := ratioOf(pv.P.Input.Aspect)
	size := monitorSize(gtx, gtx.Constraints.Max.X, ratio, wide)
	// The block is as wide as the picture (the whole card when narrow), and
	// never narrower than the transport; a Shorts picture stands centred.
	colW := size.X
	if !wide {
		colW = gtx.Constraints.Max.X
	}
	colW = min(gtx.Constraints.Max.X, max(colW, gtx.Dp(212)))
	caption := colW >= gtx.Dp(330)

	// Transport.
	if m.play.Clicked(gtx) {
		if m.playing {
			m.stop(c)
		} else {
			if t.playhead >= f.total-0.05 {
				t.playhead = 0
			}
			m.playing, m.start, m.from, m.lastT = true, gtx.Now, t.playhead, t.playhead-0.001
			// A line already under way starts with the playhead only at its beginning.
		}
	}
	if m.prev.Clicked(gtx) {
		m.stop(c)
		s.jumpScene(c, pv, -1)
	}
	if m.next.Clicked(gtx) {
		m.stop(c)
		s.jumpScene(c, pv, +1)
	}
	if m.playing {
		now := m.from + gtx.Now.Sub(m.start).Seconds()
		if now >= f.total {
			now = f.total
			m.playing = false
		}
		for _, it := range f.items {
			if it.VoiceDuration <= 0 {
				continue
			}
			sc := sceneOf(pv, it.SceneID)
			if sc == nil || sc.Audio.Path == "" || c.Offline || m.lastT >= it.VoiceStart || now < it.VoiceStart {
				continue
			}
			// Exactly the kept part of the line, where the film plays it.
			_ = c.PlayRange(pv.Abs(sc.Audio.Path), it.VoiceTrimIn, it.VoiceDuration, nil)
		}
		m.lastT = now
		t.playhead = now
		if m.playing {
			gtx.Execute(op.InvalidateCmd{})
		} else {
			c.Audio.Stop()
		}
	}

	gtx.Constraints = layout.Constraints{Min: pt(colW, 0), Max: pt(colW, gtx.Constraints.Max.Y)}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			defer op.Offset(pt((colW-size.X)/2, 0)).Push(gtx.Ops).Pop()
			r := image.Rectangle{Max: size}
			defer clip.UniformRRect(r, gtx.Dp(gui.RadiusInner)).Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, color.NRGBA{R: 14, G: 11, B: 9, A: 255})
			s.drawFrameAt(gtx, c, pv, t.playhead, size)
			s.monitorBadges(gtx, c, pv, t.playhead, size)
			return D{Size: pt(colW, size.Y)}
		}),
		layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
		layout.Rigid(func(gtx C) D {
			gtx.Constraints.Min.X = colW
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return gui.IconButton{Icon: gui.IconSkipBack, Size: 36, Tip: "Попередня сцена"}.Layout(gtx, th, &m.prev)
				}),
				layout.Rigid(func(gtx C) D {
					ic, tip := gui.IconPlay, "Переглянути чернетку"
					if m.playing {
						ic, tip = gui.IconPause, "Пауза"
					}
					return gui.Button{Kind: gui.Tinted, Icon: ic, Round: true, Size: gui.Sm, Tip: tip}.Layout(gtx, th, &m.play)
				}),
				layout.Rigid(func(gtx C) D {
					return gui.IconButton{Icon: gui.IconSkipForward, Size: 36, Tip: "Наступна сцена"}.Layout(gtx, th, &m.next)
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Rigid(func(gtx C) D {
					return gui.Text{Style: th.Label, MaxLines: 1}.Layout(gtx, th, core.Clock(t.playhead))
				}),
				layout.Rigid(func(gtx C) D {
					return gui.Text{Style: th.Label, Color: th.Pal.Text3, MaxLines: 1}.Layout(gtx, th, " / "+core.Clock(f.total))
				}),
				layout.Flexed(1, func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					if !caption {
						return D{Size: pt(gtx.Constraints.Max.X, 0)}
					}
					return layout.E.Layout(gtx, gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, "чернетка, без музики"))
				}),
			)
		}),
	)
}

// jumpScene moves the playhead (and the selection) to the previous/next scene.
func (s *proj) jumpScene(c *core.Ctx, pv *core.ProjectView, dir int) {
	f := &s.film
	t := &s.tl
	if len(f.items) == 0 {
		return
	}
	k := s.itemAt(t.playhead)
	slots := f.slots()
	if dir < 0 && k >= 0 && k < len(slots) && t.playhead > slots[k].s0+0.4 {
		// First back to the start of the current scene.
	} else {
		k += dir
	}
	k = max(0, min(len(slots)-1, k))
	t.playhead = slots[k].s0 + 0.001
	t.selectScene(c, pv.ID, slots[k].id, false)
	t.revealScene = slots[k].id
}

// itemAt is the index of the scene on screen at film time sec.
func (s *proj) itemAt(sec float64) int {
	items := s.film.items
	k := sort.Search(len(items), func(i int) bool { return items[i].Start > sec }) - 1
	return max(0, k)
}

// drawFrameAt paints the film at time sec into size.
func (s *proj) drawFrameAt(gtx C, c *core.Ctx, pv *core.ProjectView, sec float64, size image.Point) {
	f := &s.film
	n := len(f.items)
	if n == 0 {
		return
	}
	p := &pv.P
	m := p.Montage
	sec = clampf(sec, 0, math.Max(0, f.total-1e-3))
	k := s.itemAt(sec)
	px := max(size.X, size.Y)
	pic := func(j int, at float64) func(gtx C, size image.Point) {
		it := f.items[j]
		sc := sceneOf(pv, it.SceneID)
		return func(gtx C, size image.Point) {
			img, ok := lookImage(c, pv, sc, m.Filter, px)
			if !ok {
				g := gtx
				g.Constraints = layout.Exact(size)
				layout.Center.Layout(g, gui.Spinner{Size: 22, Color: color.NRGBA{R: 255, G: 255, B: 255, A: 200}}.Widget(c.Th))
				return
			}
			prog := float32(clampf((at-it.Start)/math.Max(0.001, it.Duration), 0, 1))
			drawMotion(gtx, img, size, it.Effect, prog, at-it.Start, it.HasClip)
		}
	}
	// Prefetch the next scene while playing, so cuts never flash.
	if s.mon.playing && k+1 < n {
		if sc := sceneOf(pv, f.items[k+1].SceneID); sc != nil {
			lookImage(c, pv, sc, m.Filter, px)
		}
	}
	def, _ := media.TransitionByID(projectTransition(m))
	dip := 0.6 * transDuration(m)
	if k > 0 && f.items[k-1].Overlap > 0 && sec < f.items[k].Start+f.items[k-1].Overlap {
		// Inside an overlapping transition from k-1 to k.
		prev := f.items[k-1]
		tr, _ := media.TransitionByID(prev.Transition)
		prog := float32((sec - f.items[k].Start) / prev.Overlap)
		drawTransition(gtx, tr, transPics{drawA: pic(k-1, sec), drawB: pic(k, sec)}, prog, size)
	} else {
		it := f.items[k]
		pic(k, sec)(gtx, size)
		u := sec - it.Start
		// Dip fades at the start and end of the scene.
		var alpha float32
		var col color.NRGBA
		black := color.NRGBA{R: 6, G: 5, B: 4, A: 255}
		white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		dipCol := func(id string) color.NRGBA {
			if t, ok := media.TransitionByID(id); ok && t.Color == "white" {
				return white
			}
			return black
		}
		if u < dip {
			switch {
			case k == 0 && def.Kind != media.KindCut:
				alpha, col = float32(1-u/dip), black
			case k > 0:
				if t, ok := media.TransitionByID(f.items[k-1].Transition); ok && t.Kind == media.KindDip {
					alpha, col = float32(1-u/dip), dipCol(t.ID)
				}
			}
		}
		if rem := it.Duration - u; rem < dip {
			switch {
			case k == n-1 && def.Kind != media.KindCut:
				alpha, col = max(alpha, float32(1-rem/dip)), black
			case k < n-1:
				if t, ok := media.TransitionByID(it.Transition); ok && t.Kind == media.KindDip {
					alpha, col = max(alpha, float32(1-rem/dip)), dipCol(t.ID)
				}
			}
		}
		if alpha > 0.001 {
			paint.FillShape(gtx.Ops, gui.WithAlpha(col, clamp32(alpha, 0, 1)), clip.Rect(image.Rectangle{Max: size}).Op())
		}
	}
	if m.Subtitles == "burn" {
		style := mediaStyle(m.SubtitleStyle).Resolve()
		if cue := s.cueAt(pv, sec, style.Uppercase); cue != "" {
			drawSubtitle(gtx, c.Th, style, cue, size, p.Input.Aspect == "9:16")
		}
	}
}

// monitorBadges labels the monitor: the scene number and what it shows.
func (s *proj) monitorBadges(gtx C, c *core.Ctx, pv *core.ProjectView, sec float64, size image.Point) {
	f := &s.film
	if len(f.items) == 0 {
		return
	}
	k := s.itemAt(sec)
	it := f.items[k]
	label := "Сцена " + itoa(sceneNumber(&pv.P, it.SceneID)) + " з " + itoa(len(pv.P.Scenes))
	if !it.HasClip {
		label += " · кадр, " + lower1(effectLabel(it.Effect))
		if it.Effect == "none" {
			label = "Сцена " + itoa(sceneNumber(&pv.P, it.SceneID)) + " · нерухомий кадр"
		}
	}
	if size.X >= c.Th.TextWidth(gtx, c.Th.Caption.Weighted(gui.FontSemiBold), label)+gtx.Dp(28) {
		pill(gtx, c.Th, gtx.Dp(10), gtx.Dp(10), label)
	}
	_ = studio.StatusDone
}
