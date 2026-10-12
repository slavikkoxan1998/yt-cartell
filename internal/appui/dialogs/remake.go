package dialogs

// «Перезняти за прикладом»: a YouTube link + what to change → Gemini watches
// the example and the studio makes a new project after it (Engine.StartRemake).
// How close to stay («Точна копія» / «Близько до оригіналу» / «Лише
// структура й темп»), the length (as the original or own), the language, the
// profile («Як у прикладі» = everything from the original) and who watches
// the video (config.Remake.Analysis: auto / Google AI Studio / the text
// service); then «Автопілот» or «Покроково». The progress shows in step 1
// («Розбір прикладу») and in the queue.

import (
	"fmt"
	"strings"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/reference"
	"ytcartell/internal/studio"
)

const offlineText = "Демо-режим: генерація вимкнена"

type remakeSheet struct {
	drawer gui.Drawer
	sh     core.Sheet

	link gui.TextField
	wish gui.TextField

	fid      [3]gui.Clickable
	fidelity string

	keepLen  gui.Toggle
	keep     bool
	dur      pickers.Duration
	seconds  int
	lang     pickers.Language
	language string

	chips     []gui.Clickable
	profileID string // "" = «Як у прикладі»

	analysis gui.Segmented
	settings gui.Clickable

	auto, steps gui.Clickable
	busy        string
	err         string
	errA        gui.Anim
	linkErr     string
}

func (s *remakeSheet) kind() core.SheetKind { return core.SheetRemake }
func (s *remakeSheet) isOpen() bool         { return s.drawer.IsOpen() }
func (s *remakeSheet) shut()                { s.drawer.Close() }

func (s *remakeSheet) open(c *core.Ctx, sh core.Sheet) {
	s.sh = sh
	s.link.SetText("")
	s.wish.SetText(strings.TrimSpace(sh.Text))
	s.fidelity = studio.FidelityClose
	s.keep, s.seconds = true, studio.DefaultInput().TargetSeconds
	s.language, s.profileID = "", ""
	s.err, s.busy, s.linkErr = "", "", ""
	s.drawer.Open()
}

// fidelities are the three ways to follow the original.
var fidelities = []struct {
	id          string
	icon        *gui.Icon
	title, text string
}{
	{studio.FidelityExact, gui.IconCopy, "Точна копія", "Ті самі сцени, кадри, дії й темп. Текст — тими ж думками, але своїми словами."},
	{studio.FidelityClose, gui.IconRepeat, "Близько до оригіналу", "Та сама побудова, хук і подача — зміст під твій опис."},
	{studio.FidelityStructure, gui.IconListOrdered, "Лише структура й темп", "Хук, ритм і манера подачі. Тема, герої й картинка — нові."},
}

// analysisOptions are config.Remake.Analysis.
var analysisOptions = []gui.SegOption{
	{Value: config.RemakeAuto, Label: "Авто"},
	{Value: config.RemakeGoogle, Label: "Google AI Studio"},
	{Value: config.RemakeText, Label: "Текстовий сервіс"},
}

// videoID is the YouTube id in the link field ("" = none yet or not YouTube).
func (s *remakeSheet) videoID() string { return reference.YouTubeID(s.link.Editor.Text()) }

func (s *remakeSheet) layout(gtx C, c *core.Ctx, host *Sheets) {
	th := c.Th
	if s.drawer.Dismissed() {
		s.err, s.linkErr = "", ""
	}
	profiles := c.E.Profiles()
	if len(s.chips) < len(profiles)+1 {
		s.chips = append(s.chips, make([]gui.Clickable, len(profiles)+1-len(s.chips))...)
	}
	if s.chips[0].Clicked(gtx) {
		s.profileID = ""
	}
	for i := range profiles {
		if s.chips[i+1].Clicked(gtx) {
			s.profileID = profiles[i].ID
		}
	}
	for i := range fidelities {
		if s.fid[i].Clicked(gtx) {
			s.fidelity = fidelities[i].id
		}
	}
	if v, ok := s.keepLen.Changed(gtx); ok {
		s.keep = v
	}
	if v, ok := s.dur.Changed(); ok {
		s.seconds = v
	}
	if v, ok := s.lang.Changed(); ok {
		s.language = v
	}
	if v, ok := s.analysis.Changed(gtx); ok {
		cfg := c.E.Config()
		c.Background(func() { _ = cfg.Update(func(cf *config.Config) { cf.Remake.Analysis = v }) })
	}
	if s.settings.Clicked(gtx) {
		s.drawer.Close()
		c.Navigate(core.Route{Page: core.PageSettings, Section: "connections"})
	}
	if _, ok := s.link.Changed(gtx); ok {
		s.linkErr, s.err = "", ""
	}
	if s.auto.Clicked(gtx) {
		s.start(c, true)
	}
	if s.steps.Clicked(gtx) {
		s.start(c, false)
	}
	svc := c.E.RemakeService()
	s.lang.Flat, s.dur.Flat = true, true

	body := func(gtx C) D {
		ws := []layout.Widget{
			func(gtx C) D {
				return steps(gtx, th,
					stepItem{gui.IconLink, "Посилання на YouTube", "Звичайне відео або Shorts — публічне, до 90 хвилин."},
					stepItem{gui.IconEye, "Gemini дивиться й слухає", "Розбирає кожну сцену: кадр, дії, текст, голос, музику й темп."},
					stepItem{gui.IconClapperboard, "Новий проєкт за розбором", "Сценарій з твоїми змінами; формат, стиль і ритм — як в оригіналі."},
				)
			},
			func(gtx C) D { return s.linkField(gtx, th) },
			heading(th, "Наскільки близько до оригіналу"),
			func(gtx C) D { return s.fidelityTiles(gtx, th) },
			func(gtx C) D {
				return s.wish.Layout(gtx, th, gui.FieldStyle{
					Label:       "Що змінити або що хочу бачити",
					Placeholder: "Напр.: та сама подача, але про мою кав'ярню; героїня — жінка 30 років; українською.",
					Hint:        "Тема, герої, місця, мова, деталі. Порожньо — та сама тема, але все написано наново.",
					MinLines:    4, MaxLines: 10, MaxLen: 4000,
				})
			},
			func(gtx C) D { return s.fields(gtx, c) },
			heading(th, "Профіль"),
			func(gtx C) D { return s.profileChips(gtx, th, profiles) },
			func(gtx C) D {
				txt := "Як у прикладі: формат, стиль кадрів, темп, звук і камера — з оригіналу."
				if pr, ok := c.E.Profile(s.profileID); ok && s.profileID != "" {
					txt = "Вигляд, формат, темп, голос, субтитри й монтаж — з профілю «" + pr.Name + "»; з прикладу — лише зміст, структура й таймінг."
				}
				return gui.Text{Style: th.Small, Color: th.Pal.Text2}.Layout(gtx, th, txt)
			},
			func(gtx C) D { return layout.Spacer{Height: gui.Sp4}.Layout(gtx) },
			heading(th, "Хто дивиться відео"),
			func(gtx C) D {
				s.analysis.Full = true
				return s.analysis.Layout(gtx, th, svc.Analysis, analysisOptions)
			},
			func(gtx C) D { return s.servicePanel(gtx, th, svc) },
		}
		return gui.VStack(gtx, gui.Sp16, ws...)
	}
	foot := func(gtx C) D {
		blocked := !svc.Ready
		return sheetFoot(gtx, th, &s.err, &s.errA, nil,
			func(gtx C, full bool) D {
				b := gui.Button{Kind: gui.Secondary, Text: "Покроково", Trail: gui.IconArrowRight, Busy: s.busy == "steps",
					Disabled: blocked || s.busy == "auto", Full: full, Tip: "Студія розбере приклад і напише сценарій — далі ти сам"}
				if full {
					b.Trail = nil
				}
				return b.Layout(gtx, th, &s.steps)
			},
			func(gtx C, full bool) D {
				return gui.Button{Kind: gui.Primary, Text: "Автопілот", Icon: gui.IconRocket, Busy: s.busy == "auto",
					Disabled: blocked || s.busy == "steps", Full: full,
					Tip: "Розбір, сценарій, план, кадри, відео, озвучка й монтаж — без зупинок"}.Layout(gtx, th, &s.auto)
			},
		)
	}
	s.drawer.Layout(gtx, th, gui.DrawerStyle{
		Title:    "Перезняти за прикладом",
		Subtitle: "Встав посилання на YouTube і скажи, що змінити, — студія подивиться відео й зробить своє за його зразком.",
		Width:    640,
		Body:     body,
		Footer:   foot,
	})
}

// linkField is the YouTube link with what was recognised in it.
func (s *remakeSheet) linkField(gtx C, th *gui.Theme) D {
	text := strings.TrimSpace(s.link.Editor.Text())
	hint := "Підходять youtube.com/watch?v=…, youtu.be/… і Shorts."
	errText := s.linkErr
	if id := s.videoID(); id != "" {
		hint = "YouTube · відео " + id
	} else if text != "" && errText == "" && len(text) > 8 {
		errText = "Це не схоже на посилання на відео YouTube"
	}
	return s.link.Layout(gtx, th, gui.FieldStyle{
		Label:       "Посилання на відео",
		Placeholder: "https://www.youtube.com/watch?v=…",
		Leading:     gui.IconLink,
		Hint:        hint,
		Error:       errText,
		Submit:      true,
	})
}

// fidelityTiles are the three ways to follow the original.
func (s *remakeSheet) fidelityTiles(gtx C, th *gui.Theme) D {
	ws := make([]layout.Widget, len(fidelities))
	for i, f := range fidelities {
		i, f := i, f
		ws[i] = func(gtx C) D {
			sel := s.fidelity == f.id
			return gui.Card{Tint: true, Click: &s.fid[i], Selected: sel, Padding: gui.Sp16, Radius: gui.RadiusInner}.Layout(gtx, th, func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				ink := th.Pal.Text2
				if sel {
					ink = th.Pal.ActionText
				}
				return gui.VStack(gtx, gui.Sp8,
					func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx C) D { return f.icon.Layout(gtx, 18, ink) }),
							layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
							layout.Flexed(1, gui.Text{Style: th.Body.Weighted(gui.FontSemiBold), MaxLines: 2}.Widget(th, f.title)),
						)
					},
					gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, f.text),
				)
			})
		}
	}
	return tileGrid(gtx, 168, ws...)
}

// fields: the length (as the original or own) and the language.
func (s *remakeSheet) fields(gtx C, c *core.Ctx) D {
	th := c.Th
	length := func(gtx C) D {
		if s.keep {
			return toggleTile(gtx, th, &s.keepLen, gui.IconTimer, "Тривалість", "Як в оригіналі", "Своя",
				"Студія сама підбере кількість сцен", s.keep)
		}
		return gui.VStack(gtx, gui.Sp8,
			func(gtx C) D {
				return toggleTile(gtx, th, &s.keepLen, gui.IconTimer, "Тривалість", "Як в оригіналі", "Своя", "", s.keep)
			},
			func(gtx C) D {
				clip := max(1, c.E.ClipSeconds())
				s.dur.Note = fmt.Sprintf("≈ %s по %d с", core.Count((s.seconds+clip-1)/clip, "сцена", "сцени", "сцен"), clip)
				return s.dur.Layout(gtx, c, "Своя тривалість", s.seconds)
			},
		)
	}
	language := func(gtx C) D {
		s.lang.Note = ""
		if s.language == "" {
			s.lang.Note = "з опису або з оригіналу"
		}
		return s.lang.Layout(gtx, c, "Мова фільму", s.language)
	}
	return tileGrid(gtx, 250, length, language)
}

func (s *remakeSheet) profileChips(gtx C, th *gui.Theme, profiles []config.Profile) D {
	ws := []layout.Widget{func(gtx C) D {
		return gui.Chip{Text: "Як у прикладі", Selected: s.profileID == "", Icon: gui.IconRepeat}.Layout(gtx, th, &s.chips[0])
	}}
	for i, pr := range profiles {
		i, pr := i, pr
		ws = append(ws, func(gtx C) D {
			return gui.Chip{Text: pr.Name, Selected: pr.ID == s.profileID, Tip: summary(pr)}.Layout(gtx, th, &s.chips[i+1])
		})
	}
	return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
}

// servicePanel says who will watch the video, or what to set up.
func (s *remakeSheet) servicePanel(gtx C, th *gui.Theme, svc studio.RemakeService) D {
	if svc.Ready {
		title := "Google AI Studio"
		if svc.Via == "text" {
			title = "Текстовий сервіс"
		}
		return panel(gtx, th, gui.ToneSuccess, gui.ToneSuccess, gui.IconCheckCircle, title, svc.Note)
	}
	return gui.VStack(gtx, gui.Sp8,
		func(gtx C) D {
			return panel(gtx, th, gui.ToneWarning, gui.ToneWarning, gui.IconKeyRound, "Потрібен ключ Google AI Studio", svc.Note)
		},
		func(gtx C) D {
			return gui.Link{Text: "Відкрити налаштування", Trail: gui.IconChevronRight}.Layout(gtx, th, &s.settings)
		},
	)
}

// start creates the project and starts the remake (Engine.StartRemake) in
// the background, then opens step 1 where the progress shows.
func (s *remakeSheet) start(c *core.Ctx, auto bool) {
	if s.busy != "" {
		return
	}
	if s.videoID() == "" {
		s.linkErr = "Встав посилання на відео YouTube"
		if strings.TrimSpace(s.link.Editor.Text()) != "" {
			s.linkErr = "Це не схоже на посилання на відео YouTube"
		}
		return
	}
	if c.Offline {
		s.err = offlineText
		return
	}
	opt := studio.RemakeOptions{
		URL: s.link.Editor.Text(), Wish: s.wish.Editor.Text(), Fidelity: s.fidelity,
		KeepLength: s.keep, Language: s.language, LanguageSet: true,
		ProfileID: s.profileID, NoProfile: s.profileID == "", Autopilot: auto,
	}
	if !s.keep {
		opt.TargetSeconds = s.seconds
	}
	s.err = ""
	s.busy = "steps"
	if auto {
		s.busy = "auto"
	}
	done := s.sh.Done
	c.Background(func() {
		p, err := c.E.StartRemake(opt)
		c.Post(func() {
			s.busy = ""
			if err != nil {
				s.err = core.ErrorText(err)
				return
			}
			s.drawer.Close()
			if auto {
				c.Toast(gui.ToneSuccess, "Gemini дивиться приклад — далі автопілот зробить фільм. Прогрес у черзі")
			} else {
				c.Toast(gui.ToneSuccess, "Gemini дивиться приклад — розбір і сценарій з'являться на кроці «Сценарій»")
			}
			if done != nil {
				done(p.ID)
			}
			c.Navigate(core.Route{Page: core.PageProject, ProjectID: p.ID, Step: 1})
		})
	})
}
