package dialogs

// «Новий проєкт»: story box (pre-filled from Sheet.Text, .txt import),
// profile chips, language / duration / format, then «Автопілот» (the whole
// film unattended) or «Покроково» (open step 1).

import (
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

type newProjectSheet struct {
	drawer gui.Drawer
	sh     core.Sheet

	story     gui.TextField
	importTxt gui.Clickable
	remake    gui.Clickable // «Перезняти за прикладом» (remake.go)
	mat       materialsEditor
	chips     []gui.Clickable
	profileID string

	lang    pickers.Language
	dur     pickers.Duration
	aspect  pickers.Aspect
	sound   pickers.Sound
	content pickers.Content
	// Values the user picked here (else the profile's).
	language, aspectV          string
	seconds                    int
	langSet, durSet, aspectSet bool
	soundV                     string
	contentV                   pickers.ContentValue
	soundSet, contentSet       bool
	pipe                       pickers.Pipeline
	pipeV                      string
	pipeSet                    bool

	auto, steps gui.Clickable
	busy        string // "auto" | "steps" while creating
	err         string // engine error (footer)
	errA        gui.Anim
	storyErr    string // the story is missing
}

func (s *newProjectSheet) kind() core.SheetKind { return core.SheetNewProject }
func (s *newProjectSheet) isOpen() bool         { return s.drawer.IsOpen() }
func (s *newProjectSheet) shut()                { s.drawer.Close() }

func (s *newProjectSheet) open(c *core.Ctx, sh core.Sheet) {
	s.sh = sh
	s.story.SetText(sh.Text)
	s.err, s.busy, s.storyErr = "", "", ""
	s.mat.reset()
	s.langSet, s.durSet, s.aspectSet = false, false, false
	s.soundSet, s.contentSet = false, false
	def := strings.TrimSpace(c.E.Config().Get().Autopilot.DefaultProfileID)
	if _, ok := c.E.Profile(def); !ok {
		def = ""
	}
	s.profileID = def
	s.drawer.Open()
}

// values are the language, length and format the project will get: the
// user's picks here, else the chosen profile's, else the app defaults.
func (s *newProjectSheet) values(c *core.Ctx) (lang string, secs int, aspect string) {
	in := studio.DefaultInput()
	lang, secs, aspect = in.Language, in.TargetSeconds, in.Aspect
	if pr, ok := c.E.Profile(s.profileID); ok && s.profileID != "" {
		lang = pr.Language
		if pr.TargetSeconds > 0 {
			secs = pr.TargetSeconds
		}
		if pr.Aspect != "" {
			aspect = pr.Aspect
		}
	}
	if s.langSet {
		lang = s.language
	}
	if s.durSet {
		secs = s.seconds
	}
	if s.aspectSet {
		aspect = s.aspectV
	}
	return lang, secs, aspect
}

func (s *newProjectSheet) layout(gtx C, c *core.Ctx, host *Sheets) {
	th := c.Th
	if s.drawer.Dismissed() {
		s.err, s.storyErr = "", ""
	}
	profiles := c.E.Profiles()
	if len(s.chips) < len(profiles)+1 {
		s.chips = append(s.chips, make([]gui.Clickable, len(profiles)+1-len(s.chips))...)
	}
	for i := range profiles {
		if s.chips[i].Clicked(gtx) {
			s.profileID = profiles[i].ID
		}
	}
	if s.chips[len(profiles)].Clicked(gtx) {
		s.profileID = ""
	}
	if v, ok := s.lang.Changed(); ok {
		s.language, s.langSet = v, true
	}
	if v, ok := s.dur.Changed(); ok {
		s.seconds, s.durSet = v, true
	}
	if v, ok := s.aspect.Changed(); ok {
		s.aspectV, s.aspectSet = v, true
	}
	if v, ok := s.sound.Changed(); ok {
		s.soundV, s.soundSet = v, true
	}
	if v, ok := s.content.Changed(); ok {
		s.contentV, s.contentSet = v, true
	}
	if v, ok := s.pipe.Changed(); ok {
		s.pipeV, s.pipeSet = v, true
	}
	if s.importTxt.Clicked(gtx) {
		s.importText(c)
	}
	if s.remake.Clicked(gtx) { // the story typed so far becomes the wish
		c.OpenSheet(core.Sheet{Kind: core.SheetRemake, Text: s.story.Editor.Text(), Done: s.sh.Done})
	}
	s.mat.update(gtx, c)
	if s.auto.Clicked(gtx) {
		s.start(c, true)
	}
	if s.steps.Clicked(gtx) {
		s.start(c, false)
	}
	if _, ok := s.story.Changed(gtx); ok {
		s.storyErr = ""
	}
	lang, secs, aspect := s.values(c)
	s.lang.Flat, s.dur.Flat, s.aspect.Flat = true, true, true // inside the sheet
	s.sound.Flat, s.content.Flat = true, true
	s.pipe.Flat = true
	pipeline := s.pipeline(c)
	s.pipe.Cost = func(v string) string {
		p := studio.Project{Input: studio.DefaultInput()}
		snd, _ := s.modes(c)
		p.Input.TargetSeconds, p.Input.Aspect, p.Input.AudioMode = secs, aspect, snd
		return core.PipelineCost(c.E.PipelineCostOf(p, v))
	}
	sound, content := s.modes(c)

	body := func(gtx C) D {
		story := func(gtx C) D {
			return s.story.Layout(gtx, th, gui.FieldStyle{
				Label:       "Історія або ідея",
				Placeholder: "Встав готовий текст або коротко опиши ідею: хто герой, що з ним стається, чим усе закінчиться.",
				MinLines:    7, MaxLines: 14, Error: s.storyErr,
			})
		}
		storyMeta := func(gtx C) D {
			text := s.story.Editor.Text()
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return gui.Link{Text: "Імпорт .txt", Trail: gui.IconFileUp}.Layout(gtx, th, &s.importTxt)
				}),
				layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
				layout.Rigid(gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, wordsLine(text))),
			)
		}
		chips := func(gtx C) D {
			var ws []layout.Widget
			for i, pr := range profiles {
				i, pr := i, pr
				ws = append(ws, func(gtx C) D {
					return gui.Chip{Text: pr.Name, Selected: pr.ID == s.profileID, Tip: summary(pr)}.Layout(gtx, th, &s.chips[i])
				})
			}
			n := len(profiles)
			ws = append(ws, func(gtx C) D {
				return gui.Chip{Text: "Типові налаштування", Selected: s.profileID == "", Icon: gui.IconSparkles}.Layout(gtx, th, &s.chips[n])
			})
			return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
		}
		profileNote := func(gtx C) D {
			pr, ok := c.E.Profile(s.profileID)
			txt := "Кіно-стиль, 16:9, 1 хв — зміниш будь-що на кроці «Сценарій»."
			if ok && s.profileID != "" {
				txt = styleName(pr) + " · " + summary(pr)
			}
			return gui.Text{Style: th.Small, Color: th.Pal.Text2}.Layout(gtx, th, txt)
		}
		fields := func(gtx C) D {
			scenes := (secs + c.E.ClipSeconds() - 1) / max(1, c.E.ClipSeconds())
			s.dur.Note = fmt.Sprintf("≈ %s по %d с", core.Count(scenes, "сцена", "сцени", "сцен"), c.E.ClipSeconds())
			return tileGrid(gtx, 240,
				func(gtx C) D { return s.dur.Layout(gtx, c, "Тривалість", secs) },
				func(gtx C) D { return s.aspect.Layout(gtx, c, "Формат", aspect) },
				func(gtx C) D { return s.lang.Layout(gtx, c, "Мова фільму", lang) },
				func(gtx C) D { return s.sound.Layout(gtx, c, "Звук", sound) },
				func(gtx C) D { return s.content.Layout(gtx, c, "Що в кадрі", content) },
				func(gtx C) D { return s.pipe.Layout(gtx, c, "Режим виробництва", pipeline) },
			)
		}
		remake := func(gtx C) D {
			return flatTile(gtx, th, gui.Sp16, false, func(gtx C) D {
				return tileRowWrap(gtx, flatVisual(th, gui.IconRepeat),
					tileTexts(th, "", "Є приклад на YouTube?", "Студія подивиться відео й зробить своє за його зразком."),
					func(gtx C) D {
						return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Text: "Перезняти за прикладом"}.Layout(gtx, th, &s.remake)
					})
			})
		}
		materials := func(gtx C) D {
			return s.mat.layout(gtx, c, "Фото героїв, місць, предметів чи зразок стилю — студія візьме саме їх. Особливо в автопілоті.")
		}
		return gui.VStack(gtx, gui.Sp16,
			story,
			storyMeta,
			remake,
			func(gtx C) D { return layout.Spacer{Height: gui.Sp4}.Layout(gtx) },
			materials,
			func(gtx C) D { return layout.Spacer{Height: gui.Sp4}.Layout(gtx) },
			heading(th, "Профіль"),
			chips,
			profileNote,
			func(gtx C) D { return layout.Spacer{Height: gui.Sp4}.Layout(gtx) },
			fields,
		)
	}
	foot := func(gtx C) D {
		return sheetFoot(gtx, th, &s.err, &s.errA, nil,
			func(gtx C, full bool) D {
				b := gui.Button{Kind: gui.Secondary, Text: "Покроково", Trail: gui.IconArrowRight, Busy: s.busy == "steps",
					Disabled: s.busy == "auto", Full: full}
				if full {
					b.Trail = nil
				}
				return b.Layout(gtx, th, &s.steps)
			},
			func(gtx C, full bool) D {
				return gui.Button{Kind: gui.Primary, Text: "Автопілот", Icon: gui.IconRocket, Busy: s.busy == "auto",
					Disabled: s.busy == "steps", Full: full, Tip: "Студія сама зробить план, кадри, відео, озвучку й монтаж"}.Layout(gtx, th, &s.auto)
			},
		)
	}
	s.drawer.Layout(gtx, th, gui.DrawerStyle{
		Title:    "Новий проєкт",
		Subtitle: "Встав історію, вибери профіль — і вперед: автопілотом або крок за кроком.",
		Width:    620,
		Body:     body,
		Footer:   foot,
	})
}

// modes are the sound and content modes the project will get: the user's
// picks here, else the chosen profile's, else the app defaults.
func (s *newProjectSheet) modes(c *core.Ctx) (string, pickers.ContentValue) {
	in := studio.DefaultInput()
	if pr, ok := c.E.Profile(s.profileID); ok && s.profileID != "" {
		in.AudioMode, in.Voiceover, in.Content, in.Overlays = pr.AudioMode, pr.Voiceover, pr.Content, pr.Overlays
	}
	sound, content := in.SoundMode(), pickers.ContentValue{Content: in.ContentMode(), Overlays: in.Overlays}
	if s.soundSet {
		sound = s.soundV
	}
	if s.contentSet {
		content = s.contentV
	}
	return sound, content
}

// wordsLine is "123 слова · ≈ 1 хв читання" (empty text → a hint).
func wordsLine(text string) string {
	words := 0
	in := false
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '’' {
			if !in {
				words++
				in = true
			}
		} else {
			in = false
		}
	}
	if words == 0 {
		return "Порожньо"
	}
	mins := (words + 149) / 150 // ~150 words a minute of narration
	return fmt.Sprintf("%s · ≈ %d хв озвучки", core.Count(words, "слово", "слова", "слів"), mins)
}

// importText reads a .txt file into the story box.
func (s *newProjectSheet) importText(c *core.Ctx) {
	c.PickFile("Текст історії", []string{"txt", "md"}, func(path string) {
		c.Background(func() {
			b, err := os.ReadFile(path)
			if err == nil && len(b) > 2<<20 {
				err = fmt.Errorf("файл завеликий — до 2 МБ тексту")
			}
			if err == nil && !utf8.Valid(b) {
				err = fmt.Errorf("це не текст у UTF-8 — збережи файл як «Текст (UTF-8)»")
			}
			c.Post(func() {
				if err != nil {
					c.Error(err)
					return
				}
				s.story.SetText(strings.TrimSpace(strings.TrimPrefix(string(b), "\ufeff")))
				s.err = ""
			})
		})
	})
}

// options are what the sheet shows, as the project gets it: «Типові
// налаштування» means no profile at all (not even the autopilot's default
// one) and the shown language applies even when it is «Авто» ("").
func (s *newProjectSheet) options(c *core.Ctx, text string, auto bool) studio.NewProjectOptions {
	lang, secs, aspect := s.values(c)
	opt := studio.NewProjectOptions{
		Text: text, ProfileID: s.profileID, NoProfile: s.profileID == "",
		Language: lang, LanguageSet: true, Aspect: aspect, TargetSeconds: secs,
		Materials: s.mat.inputs(), Autopilot: auto,
	}
	if s.soundSet {
		opt.AudioMode = s.soundV
	}
	if s.contentSet {
		opt.Content, opt.Overlays = s.contentV.Content, s.contentV.Overlays
	}
	if s.pipeSet {
		opt.Pipeline, opt.PipelineSet = s.pipeV, true
	}
	if text != "" {
		opt.Mode = studio.ModeStory
	}
	return opt
}

// start creates the project in the background (Engine.CreateProject: the
// profile, the choices, the materials and — for Автопілот — the job, in one
// call) and opens it.
func (s *newProjectSheet) start(c *core.Ctx, auto bool) {
	if s.busy != "" {
		return
	}
	text := strings.TrimSpace(s.story.Editor.Text())
	if auto && text == "" {
		s.storyErr = "Спершу встав історію або опиши ідею — автопілоту потрібен текст."
		return
	}
	opt := s.options(c, text, auto)
	s.err = ""
	s.busy = "steps"
	if auto {
		s.busy = "auto"
	}
	done := s.sh.Done
	c.Background(func() {
		p, err := c.E.CreateProject(opt)
		c.Post(func() {
			s.busy = ""
			if p.ID == "" {
				s.err = core.ErrorText(err)
				return
			}
			s.drawer.Close()
			switch {
			case err != nil:
				c.Error(err) // created, but e.g. the job did not start
			case auto:
				c.Toast(gui.ToneSuccess, "Автопілот почав роботу — прогрес видно в черзі")
			}
			if done != nil {
				done(p.ID)
			}
			r := core.Route{Page: core.PageProject, ProjectID: p.ID}
			if !auto {
				r.Step = 1
			}
			c.Navigate(r)
		})
	})
}

// pipeline is the production mode the project will get: the user's pick
// here, else the chosen profile's, else the full pipeline.
func (s *newProjectSheet) pipeline(c *core.Ctx) string {
	if s.pipeSet {
		return s.pipeV
	}
	if pr, ok := c.E.Profile(s.profileID); ok && s.profileID != "" && studio.ValidPipeline(pr.Pipeline) {
		return pr.Pipeline
	}
	return studio.PipelineFull
}
