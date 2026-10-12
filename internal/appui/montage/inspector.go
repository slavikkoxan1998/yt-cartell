package montage

// The inspector of the selected clip: its transition into the next scene
// (with the film-wide transition duration), the motion effect, the trims
// in numbers, and its line — the text (editable; the voice becomes stale),
// the voice with its status, «Переозвучити», where the line sits — plus a
// jump to the scene on step 4.

import (
	"math"
	"slices"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

type inspector struct {
	trans                  gui.Select
	effect                 gui.Select
	dur                    valSlider
	inMinus, inPlus        gui.Clickable
	outMinus, outPlus      gui.Clickable
	resetTrim, show, retry gui.Clickable
	voice                  gui.AudioPill

	// The line's text: edited here, stored on blur, Ctrl+Enter or «Зберегти».
	text       gui.TextField
	textScene  string // the scene the field shows
	textDirty  bool   // typed, not stored yet
	textFocus  bool   // focused last frame (to notice the blur)
	committed  string // the text just stored, until the snapshot shows it
	commitVer  int64
	saveText   gui.Clickable
	revoice    gui.Clickable
	toPlan     gui.Clickable
	resetVoice gui.Clickable

	// Where the inspected line's recording would start untouched (per
	// version and scene): the readout says how far its words were moved.
	rest      float64
	restVer   int64
	restScene string
}

// voiceMoved is how far the words of a line were moved (seconds, + later):
// its recording's start against where it would start without the scene's
// move and trims. A head trim keeps the words in place, so it is not a move.
func (s *proj) voiceMoved(c *core.Ctx, pv *core.ProjectView, sc *studio.Scene) float64 {
	it := s.film.item(sc.ID)
	if it == nil || it.VoiceDuration <= 0 || (sc.VoiceShift == 0 && sc.VoiceTrimIn == 0) {
		return 0
	}
	st := &s.insp
	if st.restVer != pv.Ver || st.restScene != sc.ID {
		cp := pv.P
		cp.Scenes = slices.Clone(pv.P.Scenes)
		for i := range cp.Scenes {
			if cp.Scenes[i].ID == sc.ID {
				cp.Scenes[i].VoiceShift, cp.Scenes[i].VoiceTrimIn = 0, 0
			}
		}
		st.rest = it.VoiceDefaultStart
		for _, r := range c.E.Timeline(cp) {
			if r.SceneID == sc.ID {
				st.rest = r.VoiceStart
			}
		}
		st.restVer, st.restScene = pv.Ver, sc.ID
	}
	return it.VoiceStart - it.VoiceTrimIn - st.rest
}

// commitText stores the edited line of scene sid (one edit).
func (s *proj) commitText(c *core.Ctx, pv *core.ProjectView, sid string) {
	st := &s.insp
	if !st.textDirty {
		return
	}
	st.textDirty = false
	text := st.text.Text()
	if sc := sceneOf(pv, sid); sc == nil || sc.Narration == text {
		return
	}
	if c.Edit(pv.ID, func(p *studio.Project) {
		for i := range p.Scenes {
			if p.Scenes[i].ID == sid {
				p.Scenes[i].Narration = text
			}
		}
	}) {
		st.committed, st.commitVer = text, pv.Ver
	}
}

// syncText keeps the text field on the selected scene's line.
func (s *proj) syncText(gtx C, c *core.Ctx, pv *core.ProjectView, sc *studio.Scene) {
	st := &s.insp
	if st.textScene != sc.ID {
		// Another scene: store what was typed for the previous one first.
		if st.textScene != "" {
			s.commitText(c, pv, st.textScene)
		}
		st.textScene, st.textDirty, st.committed = sc.ID, false, ""
		st.text.SetText(sc.Narration)
		return
	}
	if _, ok := st.text.Changed(gtx); ok {
		st.textDirty = true
	}
	for {
		ev, ok := gtx.Event(
			key.Filter{Focus: &st.text.Editor, Name: key.NameReturn, Required: key.ModShortcut},
			key.Filter{Focus: &st.text.Editor, Name: key.NameEnter, Required: key.ModShortcut},
		)
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			s.commitText(c, pv, sc.ID)
		}
	}
	focused := st.text.Focused(gtx)
	if st.textFocus && !focused {
		s.commitText(c, pv, sc.ID) // the field lost focus
	}
	st.textFocus = focused
	if st.committed != "" && (sc.Narration == st.committed || pv.Ver != st.commitVer) {
		st.committed = ""
	}
	if !st.textDirty && st.committed == "" {
		st.text.Sync(gtx, sc.Narration)
	}
}

// selectedItem is the inspected scene (the first one when nothing is selected).
func (s *proj) selectedItem(c *core.Ctx, pv *core.ProjectView) (*studio.TimelineItem, *studio.Scene) {
	sel := c.UI(pv.ID).Scene
	it := s.film.item(sel)
	if it == nil && len(s.film.items) > 0 {
		it = &s.film.items[0]
	}
	if it == nil {
		return nil, nil
	}
	return it, sceneOf(pv, it.SceneID)
}

func (s *proj) inspectorBlock(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.insp
	p := &pv.P
	m := p.Montage
	id := pv.ID
	it, sc := s.selectedItem(c, pv)
	if it == nil || sc == nil {
		return D{}
	}
	sid := sc.ID
	k := s.film.pos[sid]
	last := k == len(s.film.items)-1
	vDur := clipSource(it, sc)
	editScene := func(fn func(s *studio.Scene)) {
		c.Edit(id, func(p *studio.Project) {
			for i := range p.Scenes {
				if p.Scenes[i].ID == sid {
					fn(&p.Scenes[i])
				}
			}
		})
	}
	if v, ok := st.trans.Changed(gtx); ok {
		editScene(func(s *studio.Scene) { s.Transition = v })
	}
	if v, ok := st.effect.Changed(gtx); ok {
		editScene(func(s *studio.Scene) { s.Effect = v })
	}
	if v, ok := st.dur.Commit(gtx); ok {
		d := snap(float64(v))
		c.Edit(id, func(p *studio.Project) { p.Montage.TransitionDuration = d })
	}
	step := func(tin, tout float64) {
		tin = snap(clampf(tin, 0, trimLimits(vDur, tout)))
		tout = snap(clampf(tout, 0, trimLimits(vDur, tin)))
		editScene(func(s *studio.Scene) { s.TrimIn, s.TrimOut = tin, tout })
	}
	switch {
	case st.inMinus.Clicked(gtx):
		step(it.TrimIn-0.1, it.TrimOut)
	case st.inPlus.Clicked(gtx):
		step(it.TrimIn+0.1, it.TrimOut)
	case st.outMinus.Clicked(gtx):
		step(it.TrimIn, it.TrimOut-0.1)
	case st.outPlus.Clicked(gtx):
		step(it.TrimIn, it.TrimOut+0.1)
	case st.resetTrim.Clicked(gtx):
		step(0, 0)
	}
	if st.show.Clicked(gtx) {
		u := c.UI(id)
		u.Scene, u.ScrollTo = sid, sid
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: id, Step: 4, Scene: sid})
	}
	s.syncText(gtx, c, pv, sc)
	if st.saveText.Clicked(gtx) {
		s.commitText(c, pv, sid)
	}
	if st.revoice.Clicked(gtx) {
		s.commitText(c, pv, sid) // re-voice what is written now
		if c.Regen(id, studio.RegenVoice, sid, "") && sc.AudioSource != studio.AudioUpload && (sc.VoiceTrimIn > 0 || sc.VoiceTrimOut > 0) {
			// A new take has its own pauses: nothing of it is cut. The move
			// stays — the new line starts where the old words did.
			editScene(func(s *studio.Scene) { s.VoiceTrimIn, s.VoiceTrimOut = 0, 0 })
		}
	}
	if st.toPlan.Clicked(gtx) {
		u := c.UI(id)
		u.Scene, u.ScrollTo = sid, sid
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: id, Step: 2, Scene: sid})
	}
	if st.resetVoice.Clicked(gtx) {
		editScene(func(s *studio.Scene) { s.VoiceShift, s.VoiceTrimIn, s.VoiceTrimOut = 0, 0, 0 })
	}

	num := sceneNumber(p, sid)
	title := strings.TrimSpace(sc.Title)
	if title == "" {
		title = "Без назви"
	}
	def := projectTransition(m)
	tOpts := []gui.SelectOption{{Value: "", Label: "Як у проєкті · " + transitionLabel(def)}}
	for _, t := range media.Transitions {
		tOpts = append(tOpts, gui.SelectOption{Value: t.ID, Label: t.Label, Hint: t.Hint})
	}
	eOpts := []gui.SelectOption{}
	if it.HasClip {
		eOpts = append(eOpts, gui.SelectOption{Value: "", Label: "Без руху", Hint: "кліп рухається сам"})
	} else {
		eOpts = append(eOpts, gui.SelectOption{Value: "", Label: "Як у проєкті · " + effectLabel(m.Effect)})
	}
	for _, e := range media.Effects {
		eOpts = append(eOpts, gui.SelectOption{Value: e.ID, Label: e.Label, Hint: e.Hint})
	}

	header := func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, "Сцена "+itoa(num)+" · "+title)),
				)
			}),
			layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
			layout.Rigid(func(gtx C) D {
				var ws []layout.Widget
				if it.HasClip {
					ws = append(ws, chipW(th, gui.Chip{Text: "Кліп " + secText(it.ClipDuration), Icon: gui.IconFilm, Small: true}))
				} else {
					ws = append(ws, chipW(th, gui.Chip{Text: "Кадр із рухом", Icon: gui.IconImage, Small: true}))
				}
				if it.VoiceDuration > 0 {
					ws = append(ws, chipW(th, gui.Chip{Text: "Голос " + secText(it.VoiceDuration), Icon: gui.IconMic, Small: true}))
				}
				ws = append(ws, chipW(th, gui.Chip{Text: "У фільмі " + core.Clock(it.Start) + " – " + core.Clock(it.Start+it.Duration), Icon: gui.IconClock, Small: true}))
				if hold := it.Duration - it.ClipDuration; hold > 0.3 {
					ws = append(ws, chipW(th, gui.Chip{Text: "Пауза на кадрі " + secText(hold), Small: true,
						Tip: "Репліка довша за кліп — останній кадр затримується, поки звучить голос."}))
				}
				return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, ws...)
			}),
		)
	}
	transField := func(gtx C) D {
		if last {
			return fieldLabel(gtx, th, "Перехід після сцени", "", func(gtx C) D {
				msg := "Остання сцена — фільм закінчиться плавним затемненням."
				if tr, _ := media.TransitionByID(def); tr.Kind == media.KindCut {
					msg = "Остання сцена — фільм закінчиться різко, без затемнення."
				}
				return gui.Text{Style: th.Small, Color: th.Pal.Text2}.Layout(gtx, th, msg)
			})
		}
		return st.trans.Layout(gtx, th, gui.SelectStyle{Label: "Перехід після сцени", Options: tOpts, Value: sc.Transition,
			Leading: transitionIcon(mustTransition(it.Transition)), SearchPlaceholder: "Пошук переходу"})
	}
	durField := func(gtx C) D {
		d := transDuration(m)
		v := st.dur.Value(float32(d))
		tr, _ := media.TransitionByID(it.Transition)
		return fieldLabel(gtx, th, "Тривалість переходів", secText(float64(v)), func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return st.dur.Layout(gtx, th, gui.SliderStyle{Value: float32(d), Min: media.MinTransitionDuration, Max: media.MaxTransitionDuration, Step: 0.1,
						Disabled: last || tr.Kind == media.KindCut, Format: func(v float32) string { return secText(float64(v)) }})
				}),
				layout.Rigid(gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, "Одна для всіх переходів фільму")),
			)
		})
	}
	effectField := func(gtx C) D {
		label := "Рух камери"
		if it.HasClip {
			label = "Рух камери поверх кліпу"
		}
		return st.effect.Layout(gtx, th, gui.SelectStyle{Label: label, Options: eOpts, Value: sc.Effect, Leading: effectIcon(it.Effect)})
	}
	trimField := func(gtx C) D {
		rest := vDur - it.TrimIn - it.TrimOut
		val := "лишається " + secText(rest) + " з " + secText(vDur)
		return fieldLabel(gtx, th, "Обрізка", val, func(gtx C) D {
			stepper := func(label string, v float64, minus, plus *gui.Clickable, canMinus, canPlus bool) layout.Widget {
				return func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							gtx.Constraints.Min.X = gtx.Dp(64)
							return gui.Text{Style: th.Small, Color: th.Pal.Text2}.Layout(gtx, th, label)
						}),
						layout.Rigid(func(gtx C) D {
							return gui.IconButton{Icon: gui.IconMinus, Size: 30, Tip: "Менше на 0,1 с", Disabled: !canMinus}.Layout(gtx, th, minus)
						}),
						layout.Rigid(func(gtx C) D {
							gtx.Constraints.Min.X = gtx.Dp(52)
							return layout.Center.Layout(gtx, gui.Text{Style: th.Label, MaxLines: 1}.Widget(th, secText(v)))
						}),
						layout.Rigid(func(gtx C) D {
							return gui.IconButton{Icon: gui.IconPlus, Size: 30, Tip: "Більше на 0,1 с", Disabled: !canPlus}.Layout(gtx, th, plus)
						}),
					)
				}
			}
			maxIn := trimLimits(vDur, it.TrimOut)
			maxOut := trimLimits(vDur, it.TrimIn)
			return gui.Flow{Gap: gui.Sp16, RowGap: gui.Sp8}.Layout(gtx,
				stepper("Початок", it.TrimIn, &st.inMinus, &st.inPlus, it.TrimIn > 0.001, it.TrimIn < maxIn-0.001),
				stepper("Кінець", it.TrimOut, &st.outMinus, &st.outPlus, it.TrimOut > 0.001, it.TrimOut < maxOut-0.001),
				func(gtx C) D {
					if it.TrimIn < 0.001 && it.TrimOut < 0.001 {
						return D{}
					}
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconRotateCcw, Text: "Скинути"}.Layout(gtx, th, &st.resetTrim)
				},
			)
		})
	}
	footer := func(gtx C) D {
		return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Text: "Показати сцену", Trail: gui.IconArrowRight,
			Tip: "Відкрити цю сцену на кроці «Відео й озвучка»"}.Layout(gtx, th, &st.show)
	}
	line := func(gtx C) D { return s.lineSection(gtx, c, pv, sc) }
	if gtx.Constraints.Max.X >= gtx.Dp(500) {
		return gui.VStack(gtx, gui.Sp16, header, func(gtx C) D {
			return gui.Columns{Gap: gui.Sp20}.Layout(gtx,
				gui.Col{Span: 1, W: func(gtx C) D { return gui.VStack(gtx, gui.Sp16, transField, durField, effectField, trimField) }},
				gui.Col{Span: 1, W: line},
			)
		}, footer)
	}
	return gui.VStack(gtx, gui.Sp16, header, transField, durField, effectField, trimField, line, footer)
}

// lineSection is the scene's line: its text (editable), the voice with its
// status, «Переозвучити», who speaks it and where it sits on the timeline.
func (s *proj) lineSection(gtx C, c *core.Ctx, pv *core.ProjectView, sc *studio.Scene) D {
	th := c.Th
	st := &s.insp
	p := &pv.P
	own := sc.AudioSource == studio.AudioUpload
	voiced := p.Input.Voiceover
	busy := sc.Audio.Status == studio.StatusRunning || sc.Audio.Status == studio.StatusQueued
	empty := strings.TrimSpace(st.text.Text()) == ""
	speaker := "Оповідач"
	if ch := pv.Character(sc.Speaker); ch != nil && strings.TrimSpace(ch.Name) != "" {
		speaker = ch.Name
	}
	pill := core.Pill(sc.Audio)
	pill.Small = true
	if sc.Audio.Path == "" && !busy {
		pill.Text = "Голосу ще немає"
	}
	var hintText string
	switch {
	case !voiced:
		hintText = "Озвучку вимкнено на кроці «Сценарій» — текст піде лише в субтитри."
	case own:
		hintText = "Це шматок твоєї озвучки: текст тут лише для субтитрів. «Нарізати знову» заново виріже цей шматок із запису."
	default:
		hintText = "Ctrl+Enter — зберегти. Змінив текст — переозвуч репліку, і голос на таймлайні оновиться."
	}
	label, icon := "Переозвучити", gui.IconMic
	tip := "Озвучити репліку заново цим текстом — це витрачає кредити"
	if own {
		label, icon = "Нарізати знову", gui.IconScissors
		tip = "Заново вирізати цей шматок із твого запису"
	}
	placed := sc.VoiceShift != 0 || sc.VoiceTrimIn != 0 || sc.VoiceTrimOut != 0
	return gui.VStack(gtx, gui.Sp12,
		func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, gui.Text{Style: th.Label, MaxLines: 1}.Widget(th, "Репліка")),
				layout.Rigid(func(gtx C) D {
					if !voiced {
						return D{}
					}
					return pill.Layout(gtx, th)
				}),
			)
		},
		func(gtx C) D {
			return gui.Anchor(gtx, th, "mont.line", func(gtx C) D {
				return st.text.Layout(gtx, th, gui.FieldStyle{MinLines: 3, MaxLines: 6, MaxLen: 4000,
					Placeholder: "Що звучить у цій сцені", Hint: hintText})
			})
		},
		func(gtx C) D {
			ws := []layout.Widget{}
			if voiced {
				ws = append(ws, func(gtx C) D {
					return gui.Anchor(gtx, th, "mont.revoice", func(gtx C) D {
						return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: icon, Text: label, Busy: busy,
							Disabled: !own && empty, Tip: tip}.Layout(gtx, th, &st.revoice)
					})
				})
			}
			if st.textDirty {
				ws = append(ws, func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconCheck, Text: "Зберегти текст"}.Layout(gtx, th, &st.saveText)
				})
			}
			if sc.Audio.Path != "" && voiced {
				src := pv.Abs(sc.Audio.Path)
				ws = append(ws, func(gtx C) D {
					gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(300))
					return st.voice.Layout(gtx, th, gui.AudioStyle{Src: src, Label: "Послухати", Duration: secDur(sc.Audio.Duration), Backend: c.Audio})
				})
			}
			return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, ws...)
		},
		func(gtx C) D {
			if !voiced || own {
				return D{}
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, "Говорить: "+speaker)),
				layout.Rigid(func(gtx C) D {
					return gui.Link{Text: "Голос — у «Плані»", Trail: gui.IconChevronRight, Style: th.Small.Weighted(gui.FontMedium)}.Layout(gtx, th, &st.toPlan)
				}),
			)
		},
		func(gtx C) D {
			if !voiced || sc.Audio.Path == "" {
				return D{}
			}
			var parts []string
			switch moved := math.Round(s.voiceMoved(c, pv, sc)*10) / 10; {
			case moved > 0:
				parts = append(parts, "звучить на "+secText(moved)+" пізніше")
			case moved < 0:
				parts = append(parts, "звучить на "+secText(-moved)+" раніше")
			}
			switch tin, tout := sc.VoiceTrimIn, sc.VoiceTrimOut; {
			case tin > 0 && tout > 0:
				parts = append(parts, "обрізано "+secText(tin)+" на початку й "+secText(tout)+" у кінці")
			case tin > 0:
				parts = append(parts, "обрізано "+secText(tin)+" на початку")
			case tout > 0:
				parts = append(parts, "обрізано "+secText(tout)+" у кінці")
			}
			text := "Перетягни голос на таймлайні або обріж його за краї."
			if len(parts) > 0 {
				text = "Голос " + strings.Join(parts, ", ") + "."
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 3}.Widget(th, text)),
				layout.Rigid(func(gtx C) D {
					if !placed {
						return D{}
					}
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconRotateCcw, Text: "Скинути",
						Tip: "Повернути голос на місце без обрізки"}.Layout(gtx, th, &st.resetVoice)
				}),
			)
		},
	)
}

// mustTransition returns the catalog entry of id (a hard cut when unknown).
func mustTransition(id string) media.Transition {
	if t, ok := media.TransitionByID(id); ok {
		return t
	}
	t, _ := media.TransitionByID("none")
	return t
}
