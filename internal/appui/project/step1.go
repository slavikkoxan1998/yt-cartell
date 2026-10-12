package project

import (
	"image"
	"os"
	"strings"
	"unicode/utf8"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// Placeholders of the story editor.
const (
	placeholderStory  = "Напиши, про що відео. Наприклад: «Хлопчик Тарас загубився в осінньому лісі, і руда лисиця, що вміє говорити, виводить його додому до бабусі»…"
	placeholderScript = "Встав готовий текст для озвучки. Кожне речення прозвучить дослівно, а студія сама розіб'є текст на сцени…"
)

// step1 is Сценарій: the story card, the profile bar, the settings tiles,
// the free-text directions and the plan action.
type step1 struct {
	ps     *projState
	scroll gui.Scroll

	text      bound
	mode      gui.Segmented
	importBtn gui.Clickable
	voice     voicePanel
	mats      materialsCard
	ref       referenceCard // «Розбір прикладу» of a remake (step1_reference.go)

	profBtn  gui.Clickable
	prof     pickers.Menu
	saveProf gui.Clickable

	dur      pickers.Duration
	aspect   pickers.Aspect
	lang     pickers.Language
	narrator pickers.Voice
	tone     pickers.Choice
	style    pickers.Style
	genre    pickers.Choice
	camera   pickers.Choice
	tempo    pickers.Choice
	model    pickers.ImageModel
	sound    pickers.Sound
	content  pickers.Content

	vmode   pickers.VideoMode
	pipe    pickers.Pipeline
	grammar pickers.CodeGrammar // «Граматика анімації» of a «Анімація кодом» film
	voices  voicesCard          // «Голоси озвучки»: «Мультиголос» and every speaker's voice (voices.go)
	notes   bound
	custom  bound
	clearSt gui.Clickable

	plan, replan, next gui.Clickable

	wantStory bool // focus the story editor in the next frame

	budgetVer    int64
	budgetScenes int
	budgetClip   int
}

func newStep1(ps *projState) *step1 { return &step1{ps: ps} }

// Bleed lets the cards' shadows and the scrollbar live beside the column.
func (s *step1) Bleed() unit.Dp { return bleed }

// budget is the planner's scene budget of the snapshot (cached per version:
// script mode splits the whole text).
func (s *step1) budget(c *core.Ctx, pv *core.ProjectView) (scenes, clip int) {
	if s.budgetVer != pv.Ver || s.budgetClip == 0 {
		n, cl, _ := c.E.Budget(pv.P)
		s.budgetVer, s.budgetScenes, s.budgetClip = pv.Ver, max(1, n), cl
	}
	return s.budgetScenes, s.budgetClip
}

// ready reports whether a plan can be made, with the reason when not.
func ready(pv *core.ProjectView) (bool, string) {
	in := pv.P.Input
	if in.Mode == studio.ModeVoice {
		if in.VoiceFile == "" {
			return false, "Спершу завантаж запис свого голосу."
		}
		return true, ""
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Text)) < 20 {
		return false, "Напиши хоча б кілька речень — тоді ШІ складе план."
	}
	return true, ""
}

// startPlan saves pending edits, starts the plan stage and opens step 2.
func startPlan(c *core.Ctx, ps *projState, id string) {
	ps.bind.flushAll()
	if c.Run(id, studio.StagePlan, false, "") {
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: id, Step: 2})
	}
}

// replan asks before re-planning a project that has a plan.
func replan(c *core.Ctx, ps *projState, id string) {
	c.Confirm(core.Confirm{
		Title:  "Оновити план?",
		Text:   "Персонажі, локації й сцени буде написано заново за поточною історією та налаштуваннями. Уже згенеровані кадри, відео й озвучка відв'яжуться від нових сцен.",
		Icon:   gui.IconRefreshCw,
		OK:     "Оновити план",
		Danger: true,
		OnOK:   func() { startPlan(c, ps, id) },
	})
}

// edit changes the project's input.
func editInput(c *core.Ctx, id string, fn func(in *studio.Input)) {
	c.Edit(id, func(p *studio.Project) { fn(&p.Input) })
}

// Layout draws the step.
func (s *step1) Layout(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	s.handle(gtx, c, pv)
	if s.wantStory {
		s.wantStory = false
		s.scroll.List.Position = layout.Position{}
		s.text.Focus(gtx)
	}
	els := []layout.Widget{
		func(gtx C) D { return s.story(gtx, c, pv) },
		func(gtx C) D { return s.mats.layout(gtx, c, pv, s.ps) },
		func(gtx C) D { return s.profileBar(gtx, c, pv) },
		func(gtx C) D { return s.tiles(gtx, c, pv) },
	}
	if multiVoiceShown(pv.P.Input) {
		els = append(els, func(gtx C) D { return s.voices.layout(gtx, c, pv) })
	}
	els = append(els,
		func(gtx C) D { return s.directions(gtx, c, pv) },
		func(gtx C) D { return s.action(gtx, c, pv) },
	)
	if pv.P.Reference.Active() { // a remake: «Розбір прикладу» first (step1_reference.go)
		els = append([]layout.Widget{func(gtx C) D { return s.ref.layout(gtx, c, pv, s.ps) }}, els...)
	}
	b := gtx.Dp(bleed)
	defer topFade(gtx, th, &s.scroll)
	return s.scroll.Layout(gtx, th, len(els)+1, func(gtx C, i int) D {
		W := gtx.Constraints.Max.X
		if i == len(els) {
			return D{Size: image.Pt(W, gtx.Dp(gui.Sp32))}
		}
		cg := gtx
		cg.Constraints = layout.Constraints{Min: image.Pt(W-2*b, 0), Max: image.Pt(W-2*b, gtx.Constraints.Max.Y)}
		m := op.Record(gtx.Ops)
		d := els[i](cg)
		call := m.Stop()
		o := op.Offset(image.Pt(b, gtx.Dp(2))).Push(gtx.Ops)
		call.Add(gtx.Ops)
		o.Pop()
		return D{Size: image.Pt(W, d.Size.Y+gtx.Dp(2)+gtx.Dp(gui.Sp20))}
	})
}

// handle applies the picks and clicks of the last frame.
func (s *step1) handle(gtx C, c *core.Ctx, pv *core.ProjectView) {
	id := pv.ID
	if v, ok := s.dur.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.TargetSeconds = v })
	}
	if v, ok := s.aspect.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.Aspect = v })
	}
	if v, ok := s.lang.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.Language = v })
	}
	if v, ok := s.narrator.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.NarratorVoiceID = v })
	}
	if v, ok := s.tone.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.Tone = v })
	}
	if v, ok := s.style.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.Style = v })
	}
	if v, ok := s.genre.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.Genre = v })
	}
	if v, ok := s.camera.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.Camera = v })
	}
	if v, ok := s.tempo.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.Tempo = v })
	}
	if v, ok := s.model.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.ImageModel = v })
	}
	if v, ok := s.sound.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.AudioMode = v })
	}
	if v, ok := s.content.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.Content, in.Overlays = v.Content, v.Overlays })
	}
	if v, ok := s.mode.Changed(gtx); ok && studio.StoryMode(v) != pv.P.Input.Mode {
		s.ps.bind.flushAll()
		editInput(c, id, func(in *studio.Input) { in.Mode = studio.StoryMode(v) })
	}
	if v, ok := s.vmode.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.StoryboardMode, in.ChainFrames = v.Storyboard, v.Chain })
	}
	if v, ok := s.pipe.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.Pipeline = v })
	}
	s.pipe.Cost = func(v string) string { return core.PipelineCost(c.E.PipelineCostOf(pv.P, v)) }
	if v, ok := s.grammar.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.CodeGrammar = v })
	}
	s.grammar.Aspect = pv.P.Input.Aspect
	if s.clearSt.Clicked(gtx) {
		s.custom.dirty = false
		s.custom.SetText("")
		editInput(c, id, func(in *studio.Input) { in.CustomStyle = "" })
	}
	if s.importBtn.Clicked(gtx) {
		s.importText(c, id)
	}
	if v, ok := s.prof.Picked(); ok {
		profileAction(c, pv, v)
	}
	if s.saveProf.Clicked(gtx) {
		c.OpenSheet(core.Sheet{Kind: core.SheetProfile, FromProject: id})
	}
	if s.plan.Clicked(gtx) && !pv.Running() {
		if ok, _ := ready(pv); ok {
			startPlan(c, s.ps, id)
		}
	}
	if s.replan.Clicked(gtx) && !pv.Running() {
		replan(c, s.ps, id)
	}
	if s.next.Clicked(gtx) {
		s.ps.bind.flushAll()
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: id, Step: 2})
	}
}

// importText puts a .txt file into the story.
func (s *step1) importText(c *core.Ctx, id string) {
	c.PickFile("Вибери текстовий файл", []string{"txt"}, func(path string) {
		c.Background(func() {
			info, err := os.Stat(path)
			if err == nil && info.Size() > 8<<20 {
				c.Toast(gui.ToneError, "Файл завеликий: до 8 МБ тексту.")
				return
			}
			data, err := os.ReadFile(path)
			if err != nil {
				c.Error(err)
				return
			}
			text := decodeText(data)
			c.Post(func() {
				s.text.dirty = false
				s.text.SetText(text)
				s.text.hold, s.text.holding, s.text.holdAt = text, true, timeNow()
				if c.Edit(id, func(p *studio.Project) { p.Input.Text = text }) {
					c.Toast(gui.ToneSuccess, "Імпортовано "+words(countWords(text)))
				}
			})
		})
	})
}

// ---- story ---------------------------------------------------------------------------------

func (s *step1) story(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	in := pv.P.Input
	id := pv.ID
	narrow := gtx.Constraints.Max.X < gtx.Dp(720)
	modes := []gui.SegOption{
		{Value: string(studio.ModeStory), Label: "Ідея", Icon: gui.IconLightbulb},
		{Value: string(studio.ModeScript), Label: "Готовий сценарій", Icon: gui.IconScrollText},
		{Value: string(studio.ModeVoice), Label: "Своя озвучка", Icon: gui.IconMic},
	}
	mode := in.Mode
	switch w := gtx.Constraints.Max.X; {
	case w < gtx.Dp(440):
		modes[1].Label, modes[2].Label = "Текст", "Голос"
	case w < gtx.Dp(600):
		modes[1].Label, modes[2].Label = "Сценарій", "Мій голос"
	}
	if mode == "" {
		mode = studio.ModeStory
	}
	modeW := func(gtx C) D {
		s.mode.Full = narrow
		return s.mode.Layout(gtx, th, string(mode), modes)
	}
	importW := func(gtx C) D {
		if mode == studio.ModeVoice {
			return D{}
		}
		return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconUpload, Text: "Імпорт .txt", Tip: "Вставити текст із файлу .txt"}.Layout(gtx, th, &s.importBtn)
	}
	head := func(gtx C) D {
		title := gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, "Історія")
		if narrow {
			return gui.VStack(gtx, gui.Sp12,
				func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Flexed(1, title), layout.Rigid(importW))
				},
				modeW,
			)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(title),
			layout.Rigid(layout.Spacer{Width: gui.Sp20}.Layout),
			layout.Rigid(modeW),
			layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} }),
			layout.Rigid(importW),
		)
	}
	body := func(gtx C) D {
		if mode == studio.ModeVoice {
			return s.voice.layout(gtx, c, pv)
		}
		s.text.update(gtx, &s.ps.bind, c.Frame(), in.Text, func(v string) {
			editInput(c, id, func(in *studio.Input) { in.Text = v })
		})
		ph := placeholderStory
		if mode == studio.ModeScript {
			ph = placeholderScript
		}
		lines := 8
		if narrow {
			lines = 6
		}
		return s.text.Layout(gtx, th, gui.FieldStyle{Placeholder: ph, MinLines: lines, MaxLines: 24})
	}
	foot := func(gtx C) D {
		if mode == studio.ModeVoice {
			return D{}
		}
		txt := s.text.text()
		n := countWords(txt)
		left := words(n)
		if n > 0 {
			left += " · ≈ " + humanSeconds(studio.SpeechSeconds(txt)) + " вголос"
		}
		hint := storyHint(in, mode)
		if narrow {
			return gui.VStack(gtx, 2,
				gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, left),
				gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, hint))
		}
		return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
			layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, left)),
			layout.Flexed(1, func(gtx C) D {
				return layout.E.Layout(gtx, gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, hint))
			}),
		)
	}
	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp16, head, body, foot)
	})
}

// ---- profile bar ---------------------------------------------------------------------------

func (s *step1) profileBar(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	name, note := "Без профілю", "Готовий набір налаштувань: формат, стиль, голос, монтаж."
	if pr, ok := c.E.Profile(pv.P.ProfileID); ok && pv.P.ProfileID != "" {
		name, note = pr.Name, "Зміни нижче не змінюють сам профіль."
	}
	apply := func(gtx C) D {
		return s.prof.Layout(gtx, th, &s.profBtn, profileItems(c, pv), func(gtx C) D {
			return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Text: "Застосувати профіль", Trail: gui.IconChevronDown}.Layout(gtx, th, &s.profBtn)
		})
	}
	save := func(gtx C) D {
		return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconSave, Text: "Зберегти як профіль"}.Layout(gtx, th, &s.saveProf)
	}
	text := func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				sz := gtx.Dp(36)
				gui.FillRRect(gtx, image.Rect(0, 0, sz, sz), 10, th.Pal.ActionSoft)
				is := gtx.Dp(18)
				o := op.Offset(image.Pt((sz-is)/2, (sz-is)/2)).Push(gtx.Ops)
				gui.IconBookmark.Layout(gtx, 18, th.Pal.ActionText)
				o.Pop()
				return D{Size: image.Pt(sz, sz)}
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
							layout.Rigid(gui.Text{Style: th.Label, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, "Профіль:")),
							layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
							layout.Flexed(1, gui.Text{Style: th.BodyStrong, MaxLines: 1}.Widget(th, name)),
						)
					}),
					layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, note)),
				)
			}),
		)
	}
	return gui.Card{Padding: gui.Sp16}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		if gtx.Constraints.Max.X < gtx.Dp(720) {
			return gui.VStack(gtx, gui.Sp12, text, func(gtx C) D { return gui.Flow{Gap: gui.Sp8}.Layout(gtx, apply, save) })
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, text),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Rigid(save),
			layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Rigid(apply),
		)
	})
}

// ---- settings tiles ------------------------------------------------------------------------

func (s *step1) tiles(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	in := pv.P.Input
	scenes, clip := s.budget(c, pv)
	voiceMode := in.Mode == studio.ModeVoice
	sound := in.SoundMode()
	scriptVO := in.Mode == studio.ModeScript && (sound == studio.AudioVoiceover || sound == studio.AudioInVideo)

	s.dur.Disabled, s.dur.Note = false, sprintf("≈ %s по %d с", scenesN(scenes), clip)
	if s.ps.f.dynamic {
		s.dur.Note = "сцени — під репліки (4–10 с)"
	}
	switch {
	case voiceMode:
		s.dur.Disabled, s.dur.Note = true, "за довжиною запису"
	case scriptVO:
		s.dur.Disabled, s.dur.Note = true, "за довжиною тексту · "+scenesN(scenes)
	}
	s.narrator.Disabled, s.narrator.Note = false, ""
	switch {
	case voiceMode:
		s.narrator.Disabled, s.narrator.Note = true, "звучить твій запис"
	case sound == studio.AudioInVideo:
		s.narrator.Disabled, s.narrator.Note = true, "у героїв свої голоси у відео"
	case !in.Voiceover:
		s.narrator.Disabled, s.narrator.Note = true, "без голосу"
	}
	s.sound.Disabled, s.sound.Note = voiceMode, ""
	if voiceMode {
		s.sound.Note = "звучить твій запис"
	}
	s.lang.Note = ""
	if voiceMode && in.Language == "" {
		s.lang.Note = "мова твого запису"
	}
	s.style.Note = ""
	if strings.TrimSpace(in.CustomStyle) != "" {
		s.style.Note = "діє свій стиль — опис нижче"
	}
	s.tone.None, s.tone.Icon = "Нейтральний оповідач", gui.IconQuote
	s.genre.None, s.genre.Icon = "Авто — за історією", gui.IconDrama
	s.camera.Icon, s.tempo.Icon = gui.IconCamera, gui.IconGauge

	items := []layout.Widget{
		func(gtx C) D { return s.dur.Layout(gtx, c, "Тривалість", in.TargetSeconds) },
		func(gtx C) D { return s.aspect.Layout(gtx, c, "Формат", in.Aspect) },
		func(gtx C) D { return s.lang.Layout(gtx, c, "Мова фільму", in.Language) },
		func(gtx C) D { return s.sound.Layout(gtx, c, "Звук", sound) },
		func(gtx C) D {
			return s.content.Layout(gtx, c, "Що в кадрі", pickers.ContentValue{Content: in.ContentMode(), Overlays: in.Overlays})
		},
		func(gtx C) D { return s.narrator.Layout(gtx, c, "Голос оповідача", in.NarratorVoiceID) },
		func(gtx C) D { return s.tone.Layout(gtx, c, "Тон оповіді", studio.Tones, in.Tone) },
		func(gtx C) D { return s.style.Layout(gtx, c, "Стиль кадрів", in.Style) },
		func(gtx C) D { return s.genre.Layout(gtx, c, "Жанр або ніша", studio.Genres, in.Genre) },
		func(gtx C) D { return s.camera.Layout(gtx, c, "Камера", studio.Cameras, in.Camera) },
		func(gtx C) D { return s.tempo.Layout(gtx, c, "Темп", studio.Tempos, in.Tempo) },
		func(gtx C) D { return s.model.Layout(gtx, c, "Модель кадрів", in.ImageModel) },
		func(gtx C) D {
			return s.vmode.Layout(gtx, c, "Режим відео", pickers.VideoModeValue{Storyboard: in.StoryboardMode, Chain: in.ChainFrames})
		},
		func(gtx C) D { return s.pipe.Layout(gtx, c, "Режим виробництва", in.Pipeline) },
	}
	if in.CodeAnim() {
		items = append(items, func(gtx C) D { return s.grammar.Layout(gtx, c, "Граматика анімації", in.CodeGrammar) })
	}
	return gui.VStack(gtx, gui.Sp12,
		func(gtx C) D {
			return layout.Inset{Left: gui.Sp4}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.H3}.Widget(th, "Налаштування фільму")),
					layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
					layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, "Натисни на картку, щоб змінити")),
				)
			})
		},
		func(gtx C) D {
			return gui.Grid{MinWidth: 280, Gap: gui.Sp16, Stretch: true}.Layout(gtx, len(items), func(gtx C, i int) D { return items[i](gtx) })
		},
	)
}

// ---- directions ----------------------------------------------------------------------------

func (s *step1) directions(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	id := pv.ID
	in := pv.P.Input
	s.notes.update(gtx, &s.ps.bind, c.Frame(), in.Notes, func(v string) {
		editInput(c, id, func(in *studio.Input) { in.Notes = v })
	})
	s.custom.update(gtx, &s.ps.bind, c.Frame(), in.CustomStyle, func(v string) {
		editInput(c, id, func(in *studio.Input) { in.CustomStyle = v })
	})
	cards := []layout.Widget{
		func(gtx C) D {
			return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
				return gui.VStack(gtx, gui.Sp16,
					cardTitle(th, gui.IconMessageSquare, "Побажання режисеру", "Настрій, аудиторія, що обов'язково показати", nil),
					func(gtx C) D {
						return s.notes.Layout(gtx, th, gui.FieldStyle{Placeholder: "Напр.: для дітей 5–8 років, без страшних сцен, багато осіннього світла", MinLines: 4, MaxLines: 10})
					},
				)
			})
		},
		func(gtx C) D {
			var clear layout.Widget
			if strings.TrimSpace(in.CustomStyle) != "" {
				clear = func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Text: "Прибрати", Tip: "Повернути вибраний стиль"}.Layout(gtx, th, &s.clearSt)
				}
			}
			return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
				return gui.VStack(gtx, gui.Sp16,
					cardTitle(th, gui.IconPaintbrush, "Свій стиль", "Опис англійською — замінить вибраний стиль кадрів", clear),
					func(gtx C) D {
						return s.custom.Layout(gtx, th, gui.FieldStyle{Placeholder: "Напр.: moody 90s anime, grainy film, pastel palette…", MinLines: 4, MaxLines: 10})
					},
				)
			})
		},
	}
	return gui.Grid{MinWidth: 360, Gap: gui.Sp16, MaxCols: 2, Stretch: true}.Layout(gtx, len(cards), func(gtx C, i int) D { return cards[i](gtx) })
}

// ---- action ----------------------------------------------------------------------------------

func (s *step1) action(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	nx := s.Next(c, pv)
	hasPlan := len(pv.P.Scenes) > 0
	ok, why := ready(pv)
	planning := s.ps.f.planning
	buttons := func(gtx C) D {
		switch {
		case planning:
			return gui.Button{Size: gui.Big, Icon: gui.IconWand, Text: "Створюємо план…", Busy: true}.Layout(gtx, th, &s.plan)
		case hasPlan:
			return gui.HStack(gtx, gui.Sp8,
				func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Size: gui.Big, Icon: gui.IconRefreshCw, Text: "Оновити план", Disabled: pv.Running() || !ok}.Layout(gtx, th, &s.replan)
				},
				func(gtx C) D {
					return gui.Button{Size: gui.Big, Text: "Далі: план", Trail: gui.IconArrowRight}.Layout(gtx, th, &s.next)
				},
			)
		}
		scenes, _ := s.budget(c, pv)
		return gui.Button{Size: gui.Big, Icon: gui.IconWand, Text: "Створити план · " + scenesN(scenes), Disabled: !ok || pv.Running()}.Layout(gtx, th, &s.plan)
	}
	title, text := nx.Title, nx.Text
	if !ok && !hasPlan {
		text = why + " " + text
	}
	if hasPlan && !planning {
		title = "План готовий · " + scenesN(len(pv.P.Scenes))
		text = "Перевір сцени в плані. Змінив історію чи налаштування — онови план."
	}
	info := func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				sz := gtx.Dp(52)
				gui.FillCircle(gtx, 52, th.Pal.ActionSoft)
				is := gtx.Dp(24)
				o := op.Offset(image.Pt((sz-is)/2, (sz-is)/2)).Push(gtx.Ops)
				nx.Icon.Layout(gtx, 24, th.Pal.Text)
				o.Pop()
				return D{Size: image.Pt(sz, sz)}
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return gui.VStack(gtx, 2,
					gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, title),
					gui.Text{Style: th.Small.Sized(14), Color: th.Pal.Text2, MaxLines: 3}.Widget(th, text),
				)
			}),
		)
	}
	return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		if gtx.Constraints.Max.X < gtx.Dp(760) {
			return gui.VStack(gtx, gui.Sp16, info, buttons)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, info),
			layout.Rigid(layout.Spacer{Width: gui.Sp24}.Layout),
			layout.Rigid(buttons),
		)
	})
}

// Next is the step's call to action (mirrored by the context column).
func (s *step1) Next(c *core.Ctx, pv *core.ProjectView) core.Next {
	id := pv.ID
	hasPlan := len(pv.P.Scenes) > 0
	scenes, clip := s.budget(c, pv)
	f := factsOf(c, pv)
	switch {
	case f.planning:
		return core.Next{Icon: gui.IconWand, Title: "ШІ пише план", Text: "Зазвичай 20–90 секунд. Вікно можна закрити — робота триває.", Busy: true,
			Do: func() { c.Navigate(core.Route{Page: core.PageProject, ProjectID: id, Step: 2}) }}
	case hasPlan:
		return core.Next{Icon: gui.IconListChecks, Title: "Далі: план",
			Text: scenesN(len(pv.P.Scenes)) + " уже розписано — перевір текст і промти.",
			Do: func() {
				s.ps.bind.flushAll()
				c.Navigate(core.Route{Page: core.PageProject, ProjectID: id, Step: 2})
			}}
	}
	text := sprintf("%s по %d с, герої й локації. Це безкоштовно.", scenesN(scenes), clip)
	if f.dynamic {
		text = "Сцени під репліки (4–10 с), герої й локації. Це безкоштовно."
	}
	if pv.P.Input.Mode == studio.ModeVoice {
		text = "Розпізнаємо запис і наріжемо сцени під паузи. Це безкоштовно."
		if pv.P.Input.TranscriptFile != "" {
			text = "Наріжемо сцени під паузи за твоїми таймкодами. Це безкоштовно."
		}
	}
	nx := core.Next{Icon: gui.IconWand, Title: "Створити план", Text: text}
	switch ok, _ := ready(pv); {
	case ok && !pv.Running():
		nx.Do = func() { startPlan(c, s.ps, id) }
	case !ok && pv.P.Input.Mode == studio.ModeVoice:
		nx.Do = func() { s.voice.pickFile(c, id) }
	case !ok:
		nx.Do = func() { s.wantStory = true } // bring the story editor into view and focus it
	}
	return nx
}

// Side is the step's context card: a checklist of what the plan will use.
func (s *step1) Side(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	in := pv.P.Input
	scenes, clip := s.budget(c, pv)
	n := countWords(in.Text)
	type item struct {
		ok          bool
		title, meta string
	}
	story := item{n >= 4, "Історія", words(n)}
	if in.Mode == studio.ModeVoice {
		story = item{in.VoiceFile != "", "Запис голосу", firstText(in.VoiceFileName, "ще не завантажено")}
		if in.VoiceFile != "" && in.TranscriptFile != "" {
			story.meta += " · з таймкодами"
		}
	} else if n == 0 {
		story.meta = "ще порожньо"
	}
	voice := "без голосу — музика й звуки сцен"
	switch {
	case in.Mode == studio.ModeVoice:
		voice = "твій запис"
	case in.SoundMode() == studio.AudioInVideo:
		voice = "голос у відео — репліки героїв"
		if note, _ := pickers.SpeechNote(c); note != "" {
			voice += " · " + note
		}
	case in.Voiceover && in.MultiVoice:
		voice = "мультиголос · диктор і герої — кожен своїм голосом"
		if n := len(in.ExtraNarrators); n > 0 {
			voice = sprintf("мультиголос · %d диктори й герої — кожен своїм голосом", n+1)
		}
	case in.Voiceover:
		voice = "голос: " + firstText(narratorName(c, in.NarratorVoiceID), "як у налаштуваннях")
	}
	look := firstText(customOr(in), pickers.StyleLabel(in.Style)) + " · " + pickers.ImageModelLabel(in.ImageModel)
	if in.ContentMode() != studio.ContentCharacters {
		look = pickers.ModeByValue(pickers.ContentModes, in.ContentMode()).Label + " · " + look
	}
	film := item{true, "Фільм", sprintf("≈ %s · %s по %d с · %s", core.Clock(float64(scenes*clip)), scenesN(scenes), clip, in.Aspect)}
	switch {
	case in.Mode == studio.ModeVoice:
		film.meta = "довжина запису · сцени під паузи · " + in.Aspect
	case s.ps.f.dynamic:
		film.meta = sprintf("≈ %s · %s · %s", core.Duration(in.TargetSeconds), dynamicNote, in.Aspect)
	}
	if n := len(in.Materials); n > 0 {
		story.meta += " · " + core.Count(n, "фото", "фото", "фото")
	}
	items := []item{
		story,
		film,
		{true, "Вигляд", look},
		{true, "Звук", voice},
		{len(pv.P.Scenes) > 0, "План", planMeta(pv)},
	}
	return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		ws := []layout.Widget{gui.Text{Style: th.H3}.Widget(th, "Що піде в план")}
		for _, it := range items {
			it := it
			ws = append(ws, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return checkDot(gtx, th, it.ok) }),
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
		return gui.VStack(gtx, gui.Sp12, ws...)
	})
}

// storyHint is the line under the story editor: what the AI writes.
func storyHint(in studio.Input, mode studio.StoryMode) string {
	sound := in.SoundMode()
	switch {
	case mode == studio.ModeScript && sound == studio.AudioInVideo:
		return "Репліки візьмемо з тексту; тривалість — за довжиною тексту."
	case mode == studio.ModeScript && sound == studio.AudioVoiceover:
		return "Озвучимо дослівно; тривалість — за довжиною тексту."
	case sound == studio.AudioInVideo:
		return "ШІ напише репліки героїв — їх скажуть прямо в кадрі."
	case sound == studio.AudioSilent:
		return "ШІ розпише історію на сцени без голосу — під музику."
	}
	return "ШІ сам напише текст озвучки під потрібну тривалість."
}

func customOr(in studio.Input) string {
	if strings.TrimSpace(in.CustomStyle) != "" {
		return "свій стиль"
	}
	return ""
}

func planMeta(pv *core.ProjectView) string {
	if n := len(pv.P.Scenes); n > 0 {
		return scenesN(n) + " · " + castMeta(&pv.P)
	}
	return "ще не створено"
}

// narratorName is the name of a voice id from the loaded voice list.
func narratorName(c *core.Ctx, id string) string {
	return pickers.VoiceTitle(c, id) // «Голос іншого сервісу» after a provider switch
}

// checkDot is a round check (done) or an empty ring (to do).
func checkDot(gtx C, th *gui.Theme, ok bool) D {
	sz := gtx.Dp(24)
	if ok {
		gui.FillCircle(gtx, 24, th.Pal.SuccessSoft)
		is := gtx.Dp(14)
		o := op.Offset(image.Pt((sz-is)/2, (sz-is)/2)).Push(gtx.Ops)
		gui.IconCheck.LayoutStroke(gtx, 14, 2.5, th.Pal.SuccessInk)
		o.Pop()
		return D{Size: image.Pt(sz, sz)}
	}
	gui.FillCircle(gtx, 24, th.Pal.Surface)
	return D{Size: image.Pt(sz, sz)}
}

// warnDot marks a check that needs attention (a soft warning circle with "!").
func warnDot(gtx C, th *gui.Theme) D {
	sz := gtx.Dp(24)
	gui.FillCircle(gtx, 24, th.Pal.WarningSoft)
	is := gtx.Dp(14)
	o := op.Offset(image.Pt((sz-is)/2, (sz-is)/2)).Push(gtx.Ops)
	gui.IconCircleAlert.LayoutStroke(gtx, 14, 2, th.Pal.WarningInk)
	o.Pop()
	return D{Size: image.Pt(sz, sz)}
}
