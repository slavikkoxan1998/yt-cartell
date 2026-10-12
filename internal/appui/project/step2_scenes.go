package project

import (
	"context"
	"errors"
	"image"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// sceneUI is the widget state of one scene card.
type sceneUI struct {
	title             inline
	narr, img, motion bound
	speaker, loc      gui.Select
	cast              map[string]*gui.Clickable

	up, down, moreBtn gui.Clickable
	more              pickers.Menu

	prompts gui.Clickable
	open    bool
	openA   gui.Anim

	rwOpen, rwRun, rwCancel gui.Clickable
	rw                      bool
	wish                    gui.TextField
	busy                    bool
	focusWish               bool

	flash gui.Anim

	// «Голос у відео» lines and the on-screen text (step2_modes.go).
	lines   []*lineUI
	addLine gui.Clickable
	screen  onScreenUI

	// «Мультиголос» parts of the voice-over (step2_parts.go).
	parts   []*lineUI
	addPart gui.Clickable
}

func (s *step2) sceneState(id string) *sceneUI {
	u := s.scenes[id]
	if u == nil {
		u = &sceneUI{cast: map[string]*gui.Clickable{}}
		s.scenes[id] = u
	}
	return u
}

func (s *step2) scenesHead(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	n := len(pv.P.Scenes)
	meta := scenesN(n) + " · ≈ " + core.Clock(s.ps.f.total) + " · кліп " + strconv.Itoa(s.ps.f.clip) + " с"
	if s.ps.f.dynamic {
		meta = scenesN(n) + " · ≈ " + core.Clock(s.ps.f.total) + " · кліпи під репліки"
	}
	view := "grid"
	if c.UI(pv.ID).List {
		view = "list"
	}
	return sectionHead(gtx, th, "Сцени", meta,
		func(gtx C) D {
			return s.view.Layout(gtx, th, view, []gui.SegOption{
				{Value: "grid", Label: "Сітка", Icon: gui.IconLayoutGrid},
				{Value: "list", Label: "Список", Icon: gui.IconRows2},
			})
		},
		func(gtx C) D {
			return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconRefreshCw, Text: "Перепланувати",
				Disabled: pv.Running(), Tip: "Написати план заново за поточною історією"}.Layout(gtx, th, &s.replanBtn)
		})
}

// sceneRow draws one row of scene cards (cols per row, equal heights).
func (s *step2) sceneRow(gtx C, c *core.Ctx, pv *core.ProjectView, row, cols int) D {
	n := len(pv.P.Scenes)
	start := row * cols
	end := min(n, start+cols)
	if start >= end {
		return D{}
	}
	W := gtx.Constraints.Max.X
	gap := gtx.Dp(gui.Sp16)
	cw := (W - gap*(cols-1)) / cols
	list := cols == 1 && c.UI(pv.ID).List
	type cell struct {
		d    D
		call op.CallOp
	}
	cells := make([]cell, end-start)
	h := 0
	for i := start; i < end; i++ {
		cg := gtx
		cg.Constraints = layout.Constraints{Min: image.Pt(cw, 0), Max: image.Pt(cw, gtx.Constraints.Max.Y)}
		m := op.Record(gtx.Ops)
		d := s.sceneCard(cg, c, pv, i, list)
		cells[i-start] = cell{d, m.Stop()}
		h = max(h, d.Size.Y)
	}
	// Equal heights keep the rows tidy — unless a card is expanded (open
	// prompts, a rewrite wish): then its neighbours keep their own height
	// instead of a tall empty card.
	expanded := false
	for i := start; i < end; i++ {
		if u := s.scenes[pv.P.Scenes[i].ID]; u != nil && (u.open || u.rw) && !list {
			expanded = true
		}
	}
	for i := start; i < end && !expanded; i++ {
		if cells[i-start].d.Size.Y == h {
			continue
		}
		cg := gtx
		cg.Constraints = layout.Constraints{Min: image.Pt(cw, h), Max: image.Pt(cw, h)}
		m := op.Record(gtx.Ops)
		d := s.sceneCard(cg, c, pv, i, list)
		cells[i-start] = cell{d, m.Stop()}
	}
	for k, cl := range cells {
		o := op.Offset(image.Pt(k*(cw+gap), 0)).Push(gtx.Ops)
		cl.call.Add(gtx.Ops)
		o.Pop()
	}
	return D{Size: image.Pt(W, h)}
}

// sceneCard draws scene k (grid: prompts behind a disclosure; list: prompts
// in a second column).
func (s *step2) sceneCard(gtx C, c *core.Ctx, pv *core.ProjectView, k int, list bool) D {
	th := c.Th
	id := pv.ID
	p := &pv.P
	sc := p.Scenes[k]
	sid := sc.ID
	u := s.sceneState(sid)
	fr := c.Frame()
	edit := func(fn func(s *studio.Scene)) {
		c.Edit(id, func(p *studio.Project) {
			for i := range p.Scenes {
				if p.Scenes[i].ID == sid {
					fn(&p.Scenes[i])
					return
				}
			}
		})
	}
	u.title.update(gtx, &s.ps.bind, fr, sc.Title, func(v string) { edit(func(s *studio.Scene) { s.Title = v }) })
	u.narr.update(gtx, &s.ps.bind, fr, sc.Narration, func(v string) { edit(func(s *studio.Scene) { s.Narration = v }) })
	u.img.update(gtx, &s.ps.bind, fr, sc.ImagePrompt, func(v string) { edit(func(s *studio.Scene) { s.ImagePrompt = v }) })
	u.motion.update(gtx, &s.ps.bind, fr, sc.MotionPrompt, func(v string) { edit(func(s *studio.Scene) { s.MotionPrompt = v }) })

	// Actions.
	if u.up.Clicked(gtx) {
		s.move(c, id, sid, -1)
	}
	if u.down.Clicked(gtx) {
		s.move(c, id, sid, 1)
	}
	if v, ok := u.more.Picked(); ok {
		switch v {
		case "insert":
			s.insertAfter(c, id, sid)
		case "delete":
			n := k + 1
			c.Confirm(core.Confirm{
				Title: "Видалити сцену " + strconv.Itoa(n) + "?", Text: "Разом із нею зникнуть її кадр, відео й озвучка.",
				Icon: gui.IconTrash2, OK: "Видалити", Danger: true,
				OnOK: func() {
					c.Edit(id, func(p *studio.Project) {
						for i := range p.Scenes {
							if p.Scenes[i].ID == sid {
								p.Scenes = append(p.Scenes[:i:i], p.Scenes[i+1:]...)
								return
							}
						}
					})
				},
			})
		}
	}
	if v, ok := u.speaker.Changed(gtx); ok && v != sc.Speaker {
		edit(func(s *studio.Scene) { s.Speaker = v })
	}
	if v, ok := u.loc.Changed(gtx); ok && v != sc.LocationID {
		edit(func(s *studio.Scene) { s.LocationID = v })
	}
	for _, ch := range p.Characters {
		b := u.cast[ch.ID]
		if b == nil {
			b = new(gui.Clickable)
			u.cast[ch.ID] = b
		}
		if b.Clicked(gtx) {
			cid := ch.ID
			edit(func(s *studio.Scene) {
				ids := make([]string, 0, len(s.CharacterIDs)+1)
				had := false
				for _, x := range s.CharacterIDs {
					if x == cid {
						had = true
						continue
					}
					ids = append(ids, x)
				}
				if !had {
					ids = append(ids, cid)
				}
				s.CharacterIDs = ids
			})
		}
	}
	if u.prompts.Clicked(gtx) {
		u.open = !u.open
	}
	if u.rwOpen.Clicked(gtx) {
		u.rw, u.focusWish = true, true
		u.wish.SetText("")
	}
	if u.rwCancel.Clicked(gtx) {
		u.rw = false
	}
	u.wish.Changed(gtx)
	if (u.rwRun.Clicked(gtx) || u.wish.Submitted(gtx)) && u.rw {
		s.rewrite(c, id, sid, k+1, strings.TrimSpace(u.wish.Text()), u)
	}

	header := func(gtx C) D { return s.sceneHeader(gtx, c, pv, k, u) }
	meta := func(gtx C) D { return s.sceneMeta(gtx, c, pv, k, u) }
	narration := func(gtx C) D {
		var ws []layout.Widget
		if p.Input.SoundMode() == studio.AudioInVideo {
			ws = append(ws, func(gtx C) D { return s.linesBlock(gtx, c, pv, k, u) })
		} else if p.Input.MultiVoiceOn() && sc.AudioSource != studio.AudioUpload {
			ws = append(ws, func(gtx C) D { return s.partsBlock(gtx, c, pv, k, u) })
		} else {
			ws = append(ws, func(gtx C) D { return s.narration(gtx, c, pv, k, u) })
		}
		if p.Input.TextOverlays() {
			ws = append(ws, func(gtx C) D { return s.onScreenBlock(gtx, c, pv, k, u) })
		}
		return gui.VStack(gtx, gui.Sp16, ws...)
	}
	rewrite := func(gtx C) D { return s.rewriteRow(gtx, c, u) }
	prompts := func(gtx C) D { return s.promptFields(gtx, th, u) }

	// Brief highlight after a jump from the navigator.
	u.flash.Duration, u.flash.Ease = 1400*time.Millisecond, gui.EaseInCubic
	if s.flashID == sid {
		u.flash.Set(1)
		s.flashID = ""
	}
	fl := u.flash.Animate(gtx, 0)

	m := op.Record(gtx.Ops)
	d := gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		if list && gtx.Constraints.Max.X >= gtx.Dp(900) {
			return gui.VStack(gtx, gui.Sp16, header,
				func(gtx C) D {
					return gui.Columns{Gap: gui.Sp24}.Layout(gtx,
						gui.Col{Span: 5, W: func(gtx C) D { return gui.VStack(gtx, gui.Sp16, meta, narration, rewrite) }},
						gui.Col{Span: 7, W: prompts},
					)
				})
		}
		return fillStack(gtx, gui.Sp16, header, meta, narration,
			func(gtx C) D { return s.promptsDisclosure(gtx, th, u, prompts) },
			rewrite)
	})
	call := m.Stop()
	call.Add(gtx.Ops)
	if fl > 0.01 {
		gui.Border(gtx, image.Rect(0, 0, d.Size.X, d.Size.Y), gui.RadiusCard, 2, gui.WithAlpha(th.Pal.Action, fl))
	}
	return d
}

// sceneHeader: number, inline title, status, ↑ ↓ ⋯.
func (s *step2) sceneHeader(gtx C, c *core.Ctx, pv *core.ProjectView, k int, u *sceneUI) D {
	th := c.Th
	sc := pv.P.Scenes[k]
	n := len(pv.P.Scenes)
	num := func(gtx C) D {
		sz := gtx.Dp(32)
		gui.FillCircle(gtx, 32, th.Pal.Surface)
		m := op.Record(gtx.Ops)
		d := gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), MaxLines: 1}.Layout(gtx, th, strconv.Itoa(k+1))
		call := m.Stop()
		o := op.Offset(image.Pt((sz-d.Size.X)/2, (sz-d.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		o.Pop()
		return D{Size: image.Pt(sz, sz)}
	}
	roomy := gtx.Constraints.Max.X >= gtx.Dp(380)
	status := func(gtx C) D {
		st := studio.SceneState(sc)
		if st == "planned" || !roomy {
			return D{}
		}
		as := gui.ParseState(st)
		return layout.Inset{Left: gui.Sp8}.Layout(gtx, gui.StatusPill{State: as, Small: true, Text: shortState(as)}.Widget(th))
	}
	tools := func(gtx C) D {
		return gui.HStack(gtx, 0,
			func(gtx C) D {
				return gui.IconButton{Icon: gui.IconArrowUp, Size: 32, Tip: "Вище", Disabled: k == 0}.Layout(gtx, th, &u.up)
			},
			func(gtx C) D {
				return gui.IconButton{Icon: gui.IconArrowDown, Size: 32, Tip: "Нижче", Disabled: k == n-1}.Layout(gtx, th, &u.down)
			},
			func(gtx C) D {
				return u.more.Layout(gtx, th, &u.moreBtn, []pickers.MenuItem{
					{Value: "insert", Label: "Вставити сцену після", Icon: gui.IconPlus},
					{Value: "delete", Label: "Видалити сцену", Icon: gui.IconTrash2, Danger: true, Divider: true},
				}, func(gtx C) D {
					return gui.IconButton{Icon: gui.IconMoreHorizontal, Size: 32, Tip: "Ще"}.Layout(gtx, th, &u.moreBtn)
				})
			},
		)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(num),
		layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
		layout.Flexed(1, func(gtx C) D {
			return u.title.layout(gtx, th, th.BodyStrong.Sized(16).Lined(1.35), "Назва сцени", 2)
		}),
		layout.Rigid(status),
		layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
		layout.Rigid(tools),
	)
}

// shortState is a compact status word for scene cards.
func shortState(s gui.AssetState) string {
	switch s {
	case gui.StateStale:
		return "Змінено"
	case gui.StateDone:
		return "Готово"
	}
	return ""
}

// sceneMeta: who speaks, where, who is in the shot.
func (s *step2) sceneMeta(gtx C, c *core.Ctx, pv *core.ProjectView, k int, u *sceneUI) D {
	th := c.Th
	p := &pv.P
	sc := p.Scenes[k]
	speakers := []gui.SelectOption{{Value: "narrator", Label: "Оповідач", Icon: gui.IconMic}}
	for _, ch := range p.Characters {
		if !ch.IsObject() {
			speakers = append(speakers, gui.SelectOption{Value: ch.ID, Label: firstText(ch.Name, "Персонаж"), Icon: gui.IconUserRound})
		}
	}
	locs := []gui.SelectOption{{Value: "", Label: "Без локації", Icon: gui.IconMapPin}}
	for _, l := range p.Locations {
		locs = append(locs, gui.SelectOption{Value: l.ID, Label: firstText(l.Name, "Локація"), Icon: gui.IconMapPin})
	}
	// Lines / multi-voice parts carry their speakers; nobody narrates in frame.
	noSpeaker := p.Input.SoundMode() != studio.AudioVoiceover || p.Input.NoPeople() || p.Input.MultiVoiceOn()
	selects := func(gtx C) D {
		if noSpeaker {
			return u.loc.Layout(gtx, th, gui.SelectStyle{Options: locs, Value: sc.LocationID, Leading: gui.IconMapPin})
		}
		sp := func(gtx C) D {
			return u.speaker.Layout(gtx, th, gui.SelectStyle{Options: speakers, Value: sc.Speaker, Leading: gui.IconMessageCircle})
		}
		lc := func(gtx C) D {
			return u.loc.Layout(gtx, th, gui.SelectStyle{Options: locs, Value: sc.LocationID, Leading: gui.IconMapPin})
		}
		if gtx.Constraints.Max.X < gtx.Dp(340) {
			return gui.VStack(gtx, gui.Sp8, sp, lc)
		}
		return gui.Columns{Gap: gui.Sp8}.Layout(gtx, gui.Col{Span: 1, W: sp}, gui.Col{Span: 1, W: lc})
	}
	chips := func(gtx C) D {
		if len(p.Characters) == 0 {
			return D{}
		}
		ws := []layout.Widget{gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, "У кадрі:")}
		for _, ch := range p.Characters {
			ch := ch
			in := false
			for _, x := range sc.CharacterIDs {
				in = in || x == ch.ID
			}
			ws = append(ws, func(gtx C) D {
				return gui.Chip{Text: firstText(ch.Name, "Персонаж"), Selected: in, Small: true}.Layout(gtx, th, u.cast[ch.ID])
			})
		}
		return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, ws...)
	}
	return gui.VStack(gtx, gui.Sp12, selects, chips)
}

// narration: the voice line and its speech meter.
func (s *step2) narration(gtx C, c *core.Ctx, pv *core.ProjectView, k int, u *sceneUI) D {
	th := c.Th
	sc := pv.P.Scenes[k]
	vo := pv.P.Input.Voiceover
	ph := "Що звучить у цій сцені…"
	label := "Текст озвучки"
	if !vo {
		ph, label = "Підпис до сцени (без голосу — для субтитрів)", "Підпис"
	}
	field := func(gtx C) D {
		return u.narr.Layout(gtx, th, gui.FieldStyle{Label: label, Placeholder: ph, MinLines: 2, MaxLines: 8})
	}
	if sc.AudioSource == studio.AudioUpload {
		note := func(gtx C) D {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return layout.Inset{Top: 1}.Layout(gtx, gui.IconAudioLines.Widget(16, th.Pal.Text2)) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th,
					"Голос — уривок твого запису "+core.Clock(sc.VoiceStart)+"–"+core.Clock(sc.VoiceEnd)+
						". Зміна тексту не перезапише голос: текст піде в субтитри.")),
			)
		}
		return gui.VStack(gtx, gui.Sp8, field, note)
	}
	if !vo {
		return field(gtx)
	}
	return gui.VStack(gtx, gui.Sp8, field, func(gtx C) D {
		return speechMeter(gtx, th, u.narr.text(), clipOf(sc, s.ps.f), sc.ClipLen > 0)
	})
}

// speechMeter shows how long the line speaks against the clip: a short bar
// and "≈ 5,2 с із 8 с" ("≈ 5,2 с · кліп 6 с" when the clip was chosen for
// this line); too long turns it red with a hint.
func speechMeter(gtx C, th *gui.Theme, narration string, clip float64, chosen bool) D {
	return meter(gtx, th, studio.SpeechSeconds(narration), clip, chosen)
}

// promptFields: the frame and motion prompts.
func (s *step2) promptFields(gtx C, th *gui.Theme, u *sceneUI) D {
	return gui.VStack(gtx, gui.Sp12,
		func(gtx C) D {
			return u.img.Layout(gtx, th, gui.FieldStyle{Label: "Кадр — що на картинці (англ.)", Style: th.Small.Sized(14).Lined(1.5), MinLines: 3, MaxLines: 12})
		},
		func(gtx C) D {
			return u.motion.Layout(gtx, th, gui.FieldStyle{Label: "Рух — що відбувається у відео (англ.)", Style: th.Small.Sized(14).Lined(1.5), MinLines: 2, MaxLines: 10})
		},
	)
}

// promptsDisclosure hides the prompts behind a line that opens smoothly.
func (s *step2) promptsDisclosure(gtx C, th *gui.Theme, u *sceneUI, prompts layout.Widget) D {
	u.openA.Duration, u.openA.Ease = gui.DurSheet, gui.EaseInOutCubic
	a := u.openA.Animate(gtx, b2f(u.open))
	summary := firstText(clipText(u.img.text(), 90), "промт кадру порожній")
	line := func(gtx C) D {
		return gui.Card{Click: &u.prompts, Tint: true, Radius: gui.RadiusControl, Padding: gui.Sp12}.Layout(gtx, th, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(gui.IconImage.Widget(16, th.Pal.Text2)),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Rigid(gui.Text{Style: th.Label, MaxLines: 1}.Widget(th, "Промти кадру й руху")),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Flexed(1, func(gtx C) D {
					if u.open {
						return D{Size: image.Pt(gtx.Constraints.Min.X, 0)}
					}
					return gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 1}.Layout(gtx, th, summary)
				}),
				layout.Rigid(func(gtx C) D {
					ic := gui.IconChevronDown
					if u.open {
						ic = gui.IconChevronUp
					}
					return ic.Layout(gtx, 18, th.Pal.Text2)
				}),
			)
		})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(line),
		layout.Rigid(func(gtx C) D {
			if a <= 0.001 {
				return D{}
			}
			return collapse(gtx, a, func(gtx C) D { return layout.Inset{Top: gui.Sp12}.Layout(gtx, prompts) })
		}),
	)
}

// rewriteRow is "Переписати промти ШІ" with an optional wish.
func (s *step2) rewriteRow(gtx C, c *core.Ctx, u *sceneUI) D {
	th := c.Th
	if !u.rw {
		txt := "Переписати промти ШІ"
		if u.busy {
			txt = "Переписуємо…"
		}
		return layout.W.Layout(gtx, func(gtx C) D {
			return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconWand, Text: txt, Busy: u.busy,
				Tip: "Нові промти кадру й руху для цієї сцени"}.Layout(gtx, th, &u.rwOpen)
		})
	}
	if u.focusWish {
		u.focusWish = false
		u.wish.Focus(gtx)
	}
	field := func(gtx C) D {
		return u.wish.Layout(gtx, th, gui.FieldStyle{Placeholder: "Побажання (необов'язково): крупний план, герой усміхається", Submit: true})
	}
	btns := func(gtx C) D {
		return gui.HStack(gtx, gui.Sp8,
			func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconWand, Text: "Переписати", Busy: u.busy}.Layout(gtx, th, &u.rwRun)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Text: "Скасувати"}.Layout(gtx, th, &u.rwCancel)
			},
		)
	}
	if gtx.Constraints.Max.X < gtx.Dp(520) {
		return gui.VStack(gtx, gui.Sp8, field, btns)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, field),
		layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
		layout.Rigid(btns),
	)
}

// rewrite asks the text model for new prompts of one scene (off the UI goroutine).
func (s *step2) rewrite(c *core.Ctx, id, sid string, n int, wish string, u *sceneUI) {
	if u.busy {
		return
	}
	if c.Offline {
		c.Toast(gui.ToneError, offlineText)
		return
	}
	u.busy = true
	s.ps.bind.flushAll() // the model reads the latest text
	c.Background(func() {
		// A safety net only: the text client already bounds each attempt.
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
		defer cancel()
		err := c.E.Rewrite(ctx, id, sid, wish)
		c.Post(func() {
			u.busy = false
			if err != nil {
				c.Toast(gui.ToneError, rewriteErr(err))
				return
			}
			u.rw = false
			u.open = true
			c.Toast(gui.ToneSuccess, "Промти сцени "+strconv.Itoa(n)+" переписано")
		})
	})
}

// rewriteErr words a rewrite failure: timeouts and cancellation come back
// from the text client as bare context errors.
func rewriteErr(err error) string {
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, context.DeadlineExceeded.Error()):
		return "Текстова модель не відповіла вчасно — спробуй переписати ще раз трохи згодом."
	case errors.Is(err, context.Canceled) || strings.Contains(msg, context.Canceled.Error()):
		return "Переписування промтів скасовано."
	}
	return core.ErrorText(err)
}

// move shifts a scene one place up (-1) or down (+1).
func (s *step2) move(c *core.Ctx, id, sid string, dir int) {
	c.Edit(id, func(p *studio.Project) {
		i := -1
		for k := range p.Scenes {
			if p.Scenes[k].ID == sid {
				i = k
			}
		}
		j := i + dir
		if i < 0 || j < 0 || j >= len(p.Scenes) {
			return
		}
		p.Scenes[i], p.Scenes[j] = p.Scenes[j], p.Scenes[i]
	})
	c.UI(id).Scene = sid
}

// insertAfter adds a blank scene after sid, in the same location.
func (s *step2) insertAfter(c *core.Ctx, id, sid string) {
	nid := newUID()
	c.Edit(id, func(p *studio.Project) {
		for i := range p.Scenes {
			if p.Scenes[i].ID != sid {
				continue
			}
			sc := studio.Scene{ID: nid, Title: "Нова сцена", Speaker: "narrator", CharacterIDs: []string{},
				LocationID: p.Scenes[i].LocationID,
				Frame:      studio.EmptyAsset(), Board: studio.EmptyAsset(), Video: studio.EmptyAsset(), Audio: studio.EmptyAsset()}
			p.Scenes = append(p.Scenes[:i+1:i+1], append([]studio.Scene{sc}, p.Scenes[i+1:]...)...)
			return
		}
	})
	u := s.sceneState(nid)
	u.open = true
	c.UI(id).Scene = nid
}

// ---- next + side ---------------------------------------------------------------------------

// Next is the step's call to action.
func (s *step2) Next(c *core.Ctx, pv *core.ProjectView) core.Next {
	id := pv.ID
	f := factsOf(c, pv)
	switch {
	case f.planning:
		return core.Next{Icon: gui.IconWand, Title: "ШІ пише план", Text: "Зазвичай 20–90 секунд. Вікно можна закрити — робота триває.", Busy: true}
	case len(pv.P.Scenes) == 0:
		return core.Next{Icon: gui.IconScrollText, Title: "Спершу — сценарій", Text: "Напиши історію й створи план на кроці 1.",
			Do: func() { c.Navigate(core.Route{Page: core.PageProject, ProjectID: id, Step: 1}) }}
	}
	text := "Перевір текст і промти — далі герої, місця й кадри."
	if n := noPrompt(pv); n > 0 {
		text = scenesN(n) + " без промту кадру — допиши або перепиши ШІ."
	}
	return core.Next{Icon: gui.IconImages, Title: "Далі: розкадровка", Text: text,
		Do: func() {
			s.ps.bind.flushAll()
			c.Navigate(core.Route{Page: core.PageProject, ProjectID: id, Step: 3})
		}}
}

func noPrompt(pv *core.ProjectView) int {
	n := 0
	for _, sc := range pv.P.Scenes {
		if strings.TrimSpace(sc.ImagePrompt) == "" {
			n++
		}
	}
	return n
}

// Side is the plan check: prompts, long lines, cast — with a jump to the
// first scene that needs attention.
func (s *step2) Side(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	p := &pv.P
	if len(p.Scenes) == 0 {
		return D{}
	}
	clip := float64(s.ps.f.clip)
	long, firstLong := 0, ""
	talk := p.Input.SoundMode() == studio.AudioInVideo
	for _, sc := range p.Scenes {
		tooLong := sc.AudioSource != studio.AudioUpload && p.Input.Voiceover && studio.SpeechSeconds(sc.Narration) > clipOf(sc, s.ps.f)-0.3
		if talk {
			tooLong = studio.DialogueClipSeconds(sc.Lines) > clipOf(sc, s.ps.f)-0.3
		}
		if tooLong {
			long++
			if firstLong == "" {
				firstLong = sc.ID
			}
		}
	}
	if s.jumpLong.Clicked(gtx) && firstLong != "" {
		c.UI(pv.ID).ScrollTo = firstLong
	}
	np := noPrompt(pv)
	type item struct {
		ok          bool
		title, meta string
	}
	items := []item{
		{np == 0, "Промти кадрів", strconv.Itoa(len(p.Scenes)-np) + " з " + strconv.Itoa(len(p.Scenes))},
		{long == 0, "Довжина реплік", func() string {
			if long == 0 && s.ps.f.dynamic {
				return "кліп кожної сцени — під її репліку (4–10 с)"
			}
			if long == 0 {
				return "усі вміщаються в кліп " + strconv.Itoa(int(clip)) + " с"
			}
			return core.Count(long, "сцена довша", "сцени довші", "сцен довші") + " за кліп"
		}()},
		{len(p.Characters) > 0 || p.Input.NoPeople(), castWordsOf(p.Input).title, castMeta(p) + " · " + locationsN(len(p.Locations))},
	}
	return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		ws := []layout.Widget{gui.Text{Style: th.H3}.Widget(th, "Перевірка плану")}
		for _, it := range items {
			it := it
			ws = append(ws, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return mark(gtx, th, it.ok) }),
					layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
					layout.Flexed(1, func(gtx C) D {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(gui.Text{Style: th.Label, MaxLines: 1}.Widget(th, it.title)),
							layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, it.meta)),
						)
					}),
				)
			})
		}
		if firstLong != "" {
			ws = append(ws, func(gtx C) D {
				return gui.Link{Text: "Показати першу довгу", Trail: gui.IconChevronRight, Style: th.Label}.Layout(gtx, th, &s.jumpLong)
			})
		}
		return gui.VStack(gtx, gui.Sp12, ws...)
	})
}

// mark is a plan check's mark: a check when fine, a warning when not.
func mark(gtx C, th *gui.Theme, ok bool) D {
	if ok {
		return checkDot(gtx, th, true)
	}
	return warnDot(gtx, th)
}
