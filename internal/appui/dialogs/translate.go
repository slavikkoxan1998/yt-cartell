package dialogs

// «Перекласти проєкт»: a copy of the film in another language that reuses
// every frame and clip — only the text, the voice-over and the subtitles are
// made again (Engine.Translate).

import (
	"image"
	"strings"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

type translateSheet struct {
	drawer gui.Drawer
	sh     core.Sheet

	project   gui.Select
	projectID string
	lang      pickers.Language
	language  string
	voiceMode gui.Segmented
	voiceKeep bool
	voice     pickers.Voice
	voiceID   string
	montage   gui.Toggle
	doMontage bool
	thumb     gui.ImageView

	run  gui.Clickable
	busy bool
	err  string
	errA gui.Anim
}

func (s *translateSheet) kind() core.SheetKind { return core.SheetTranslate }
func (s *translateSheet) isOpen() bool         { return s.drawer.IsOpen() }
func (s *translateSheet) shut()                { s.drawer.Close() }

func (s *translateSheet) open(c *core.Ctx, sh core.Sheet) {
	s.sh = sh
	s.err, s.busy = "", false
	s.projectID = sh.ProjectID
	if s.projectID == "" {
		for _, p := range c.List() {
			if p.Scenes > 0 {
				s.projectID = p.ID
				break
			}
		}
	}
	s.language = ""
	s.voiceID = ""
	s.doMontage = false
	s.lang.NoAuto = true
	s.voiceKeep = true
	s.pickDefaults(c)
	s.drawer.Open()
}

// pickDefaults suggests a target language (English for a Ukrainian film,
// Ukrainian otherwise) and the montage when the source has a finished film.
func (s *translateSheet) pickDefaults(c *core.Ctx) {
	pv := c.Project(s.projectID)
	if pv == nil {
		return
	}
	src := strings.ToLower(pv.P.Input.Language)
	if s.language == "" || s.language == src {
		s.language = "uk"
		if src == "uk" || src == "" {
			s.language = "en"
		}
	}
	s.doMontage = pv.P.Montage.Output.Status == studio.StatusDone
}

func (s *translateSheet) layout(gtx C, c *core.Ctx, host *Sheets) {
	th := c.Th
	if s.drawer.Dismissed() {
		s.err = ""
	}
	if v, ok := s.project.Changed(gtx); ok {
		s.projectID = v
		s.err = ""
		s.pickDefaults(c)
	}
	if v, ok := s.lang.Changed(); ok {
		s.language = v
		s.err = ""
	}
	if v, ok := s.voiceMode.Changed(gtx); ok {
		s.voiceKeep = v == "keep"
	}
	if v, ok := s.voice.Changed(); ok {
		s.voiceID = v
	}
	if v, ok := s.montage.Changed(gtx); ok {
		s.doMontage = v
	}
	if s.run.Clicked(gtx) {
		s.start(c)
	}
	pv := c.Project(s.projectID)
	hasFilm := pv != nil && pv.P.Montage.Output.Status == studio.StatusDone
	s.lang.Flat, s.voice.Flat = true, true // inside the sheet
	body := func(gtx C) D {
		var ws []layout.Widget
		ws = append(ws, func(gtx C) D {
			return panel(gtx, th, gui.ToneNeutral, gui.ToneNeutral, gui.IconImages, "Кадри й відео не перегенеровуються",
				"Лише текст, озвучка й субтитри — тому переклад займає хвилини й майже нічого не коштує. Оригінал не зміниться.")
		})
		// The project.
		if s.sh.ProjectID == "" {
			var opts []gui.SelectOption
			for _, p := range c.List() {
				if p.Scenes == 0 {
					continue
				}
				opts = append(opts, gui.SelectOption{Value: p.ID, Label: p.Title, Hint: core.Count(p.Scenes, "сцена", "сцени", "сцен")})
			}
			ws = append(ws, heading(th, "Проєкт"), func(gtx C) D {
				if len(opts) == 0 {
					return panel(gtx, th, gui.ToneWarning, gui.ToneWarning, gui.IconInfo, "Немає що перекладати",
						"Переклад працює для проєктів із готовим планом. Створи план на кроці «План».")
				}
				return s.project.Layout(gtx, th, gui.SelectStyle{Options: opts, Value: s.projectID, Leading: gui.IconFolder,
					Placeholder: "Вибери проєкт", SearchPlaceholder: "Пошук проєкту"})
			})
		}
		if pv != nil {
			ws = append(ws, func(gtx C) D { return s.projectCard(gtx, c, pv) })
			if len(pv.P.Scenes) == 0 {
				ws = append(ws, func(gtx C) D {
					return panel(gtx, th, gui.ToneWarning, gui.ToneWarning, gui.IconInfo, "Спершу потрібен план",
						"У цьому проєкті ще немає сцен — перекладати нічого. Створи план на кроці «План», а тоді повертайся.")
				})
			}
		}
		ws = append(ws,
			func(gtx C) D { return s.lang.Layout(gtx, c, "Мова перекладу", s.language) },
			func(gtx C) D {
				return gui.VStack(gtx, gui.Sp12,
					gui.Text{Style: th.Label}.Widget(th, "Голос оповідача"),
					func(gtx C) D {
						s.voiceMode.Full = true
						mode := "new"
						if s.voiceKeep {
							mode = "keep"
						}
						return s.voiceMode.Layout(gtx, th, mode, []gui.SegOption{
							{Value: "keep", Label: "Як в оригіналі"},
							{Value: "new", Label: "Інший голос"},
						})
					},
					func(gtx C) D {
						if s.voiceKeep {
							return gui.Text{Style: th.Small, Color: th.Pal.Text2}.Layout(gtx, th,
								"Багатомовні голоси ElevenLabs говорять і новою мовою. Голоси персонажів лишаються як є.")
						}
						return s.voice.Layout(gtx, c, "", s.voiceID)
					},
				)
			},
			func(gtx C) D {
				caption := "студія ще й склеїть готове відео новою мовою"
				if !hasFilm {
					caption = "в оригіналі ще немає готового відео — змонтуєш пізніше"
				}
				s.montage.Disabled = !hasFilm
				return toggleTile(gtx, th, &s.montage, gui.IconClapperboard, "Також змонтувати відео", "Так", "Ні", caption, s.doMontage && hasFilm)
			},
		)
		return gui.VStack(gtx, gui.Sp16, ws...)
	}
	foot := func(gtx C) D {
		label := "Перекласти"
		if l, ok := studio.LanguageByCode(s.language); ok {
			label = "Перекласти: " + strings.ToLower(l.Label)
		}
		return sheetFoot(gtx, th, &s.err, &s.errA, nil, func(gtx C, full bool) D {
			return gui.Button{Kind: gui.Primary, Icon: gui.IconLanguages, Text: label, Busy: s.busy, Disabled: pv == nil || len(pv.P.Scenes) == 0,
				Full: full}.Layout(gtx, th, &s.run)
		})
	}
	s.drawer.Layout(gtx, th, gui.DrawerStyle{
		Title:    "Перекласти проєкт",
		Subtitle: "Той самий фільм іншою мовою — окремим проєктом.",
		Width:    560,
		Body:     body,
		Footer:   foot,
	})
}

// projectCard shows the source film: thumbnail, title, facts.
func (s *translateSheet) projectCard(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	p := pv.P
	thumb := ""
	var ver int64
	for _, sc := range p.Scenes {
		if sc.Frame.Path != "" {
			thumb, ver = pv.Abs(sc.Frame.Path), sc.Frame.UpdatedAt
			break
		}
	}
	lang := "мова як у тексті"
	if l, ok := studio.LanguageByCode(p.Input.Language); ok {
		lang = l.Label
	}
	facts := core.Count(len(p.Scenes), "сцена", "сцени", "сцен") + " · " + p.Input.Aspect + " · " + lang
	return flatTile(gtx, th, gui.Sp12, false, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				g := gtx
				w := gtx.Dp(96)
				g.Constraints = layout.Exact(image.Pt(w, w*9/16))
				return s.thumb.Layout(g, th, c.Img, thumb, ver, gui.Thumb{Radius: gui.RadiusSmall, Placeholder: gui.IconFilm})
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontSemiBold), MaxLines: 2}.Widget(th, p.Title)),
					layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, facts)),
				)
			}),
		)
	})
}

func (s *translateSheet) start(c *core.Ctx) {
	if s.busy || s.projectID == "" {
		return
	}
	if _, ok := studio.LanguageByCode(s.language); !ok {
		s.err = "Вибери мову перекладу."
		return
	}
	opt := studio.TranslateOptions{Language: s.language, Montage: s.doMontage}
	if !s.voiceKeep {
		opt.Voice = s.voiceID
		if opt.Voice == "" {
			opt.Voice = c.E.Config().Get().TTS.Voice
		}
	}
	id, done := s.projectID, s.sh.Done
	s.busy, s.err = true, ""
	c.Background(func() {
		newID, err := c.E.Translate(id, opt)
		c.Post(func() {
			s.busy = false
			if newID == "" {
				s.err = core.ErrorText(err)
				return
			}
			s.drawer.Close()
			if err != nil {
				c.Error(err)
			} else {
				c.Toast(gui.ToneSuccess, "Переклад почався — нову версію видно в черзі")
			}
			if done != nil {
				done(newID)
			}
			c.Navigate(core.Route{Page: core.PageProject, ProjectID: newID})
		})
	})
}
