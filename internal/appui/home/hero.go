package home

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// hero is the story card ("Нова історія"): the quickest way from a story to
// a film.
type hero struct {
	story    gui.TextField
	pasteBtn gui.Clickable
	imp      gui.Clickable
	manage   gui.Clickable
	chips  map[string]*gui.Clickable
	// oneProfile is the phone layout's single profile chip.
	oneProfile gui.Clickable
	lang       gui.Clickable
	dur        gui.Clickable
	sound      gui.Clickable // «Звук» chip (modes.go)
	content    gui.Clickable // «Що в кадрі» chip
	auto       gui.Clickable
	steps      gui.Clickable

	profileID string // picked profile ("" = Типові налаштування), valid with picked
	picked    bool
	langCode  string // picked audience language (with langSet)
	langSet   bool
	secs      int // picked length (with secsSet)
	secsSet   bool
	modes     heroModes // picked sound / content modes and what the chips show
	err       string
	focus     bool // move the keyboard into the story box next frame
	importing bool
	creating  bool // a project is being created (the materials copied)

	// «Матеріали»: the pictures picked for the next project
	mats   []*material
	addMat gui.Clickable
	matSeq int
	edit   matEditor

	mic mic // voice typing into the story box (mic.go)
}

const maxImport = 2 << 20 // bytes of text a .txt import may have

// profile is the profile the card uses: the picked one, else the default
// autopilot profile from Settings (like Engine.StartAutopilot). ok is false
// for "Типові налаштування" — the app defaults, no profile.
func (h *hero) profile(c *core.Ctx) (config.Profile, bool) {
	id := h.profileID
	if !h.picked {
		id = c.E.Config().Get().Autopilot.DefaultProfileID
	}
	if id == "" {
		return config.Profile{}, false
	}
	for _, pr := range c.E.Profiles() {
		if pr.ID == id {
			return pr, true
		}
	}
	return config.Profile{}, false // deleted since: the app defaults
}

// values are the language and length the film will get: the picked ones,
// else the profile's.
func (h *hero) values(pr config.Profile) (lang string, secs int) {
	lang, secs = pr.Language, pr.TargetSeconds
	if secs <= 0 {
		secs = studio.DefaultInput().TargetSeconds
	}
	if h.langSet {
		lang = h.langCode
	}
	if h.secsSet && h.secs > 0 {
		secs = h.secs
	}
	return lang, secs
}

// heroCard draws the story card and handles its actions.
func (p *Page) heroCard(gtx C, c *core.Ctx) D {
	h := &p.hero
	th := c.Th
	pr, has := h.profile(c)
	lang, secs := h.values(pr)
	if h.chips == nil {
		h.chips = map[string]*gui.Clickable{}
	}
	p.micUpdate(gtx, c) // dictation: clicks, Esc, the recognised words (mic.go)
	if _, ok := h.story.Changed(gtx); ok {
		h.err = ""
	}
	if h.pasteBtn.Clicked(gtx) {
		if cb := gui.ReadSystemClipboard(); cb != "" {
			h.story.SetText(cb)
			h.err = ""
			c.Toast(gui.ToneSuccess, "Вставлено з буфера обміну")
		}
	}
	if h.imp.Clicked(gtx) {
		h.importTxt(c)
	}
	if h.manage.Clicked(gtx) {
		c.OpenSheet(core.Sheet{Kind: core.SheetProfiles})
	}
	for id, ck := range h.chips {
		selected := (has && id == pr.ID) || (!has && id == "")
		if ck.Clicked(gtx) && !selected {
			h.profileID, h.picked = id, true
			h.langSet, h.secsSet = false, false
			h.modes.reset() // the new profile's modes show
		}
	}
	if h.oneProfile.Clicked(gtx) {
		p.pop.Open("profile", "home.profile", profileItems(c), profileValue(pr, has), false, 300, false)
	}
	if h.lang.Clicked(gtx) {
		v := lang
		if v == "" {
			v = autoLang
		}
		p.pop.Open("lang", "home.lang", languageItems(), v, true, 320, false)
	}
	if h.dur.Clicked(gtx) {
		p.pop.Open("dur", "home.dur", durationItems(c, secs), strconv.Itoa(secs), false, 260, false)
	}
	h.modes.update(pr, has)
	if h.sound.Clicked(gtx) {
		p.pop.Open("sound", "home.sound", modeItems(pickers.SoundModes), h.modes.sound, false, 340, false)
	}
	if h.content.Clicked(gtx) {
		p.pop.Open("content", "home.content", modeItems(pickers.ContentModes), h.modes.content, false, 340, false)
	}
	if h.auto.Clicked(gtx) {
		p.autopilot(c)
	}
	if h.steps.Clicked(gtx) {
		p.stepByStep(c)
	}
	if h.focus {
		h.focus = false
		h.story.Focus(gtx)
	}
	narrow := gtx.Constraints.Max.X < gtx.Dp(640)
	// The story box is the focal point: roomy on desktop windows (8 lines,
	// ≈ 220 dp), compact on small ones.
	minLines, maxLines := 4, 10
	if windowDp(gtx, th) >= 1280 {
		minLines, maxLines = 8, 16
	}
	return gui.Card{Padding: gui.Sp24}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp16,
			func(gtx C) D { return h.head(gtx, th, narrow) },
			func(gtx C) D {
				return p.micAround(gtx, c, func(gtx C) D { // + the dictation strip (mic.go)
					return gui.Anchor(gtx, th, AnchorStory, func(gtx C) D {
						return h.story.Layout(gtx, th, gui.FieldStyle{
							Placeholder: "Встав історію або опиши ідею: про кого вона, що сталося, чим закінчилось…",
							MinLines:    minLines, MaxLines: maxLines, Counter: true, Error: h.err,
							Pending: h.mic.pending,
						})
					})
				})
			},
			func(gtx C) D { return p.materialsRow(gtx, c) },
			func(gtx C) D { return h.settings(gtx, c, pr, has, lang, secs, narrow) },
			func(gtx C) D { return h.actions(gtx, c, pr, has, lang, secs, narrow) },
		)
	})
}

func (h *hero) head(gtx C, th *gui.Theme, narrow bool) D {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, "Нова історія")),
		layout.Flexed(1, func(gtx C) D {
			if narrow {
				return D{Size: gtx.Constraints.Min}
			}
			return layout.Inset{Left: gui.Sp12, Right: gui.Sp12}.Layout(gtx,
				gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, "Встав текст або опиши ідею — решту зробить студія"))
		}),
		layout.Rigid(func(gtx C) D { return h.micButton(gtx, th) }), // mic.go
		layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
		layout.Rigid(func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconClipboardPaste, Text: "Вставити",
				Tip: "Вставити текст із буфера обміну"}.Layout(gtx, th, &h.pasteBtn)
		}),
		layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
		layout.Rigid(func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconUpload, Text: "Імпорт .txt", Busy: h.importing,
				Tip: "Завантажити історію з текстового файлу"}.Layout(gtx, th, &h.imp)
		}),
	)
}

// settings is the profile row: every profile, «Типові налаштування» and
// «Керувати» across the card's whole width. Phones get one chip naming the
// profile (its list opens under it) with the language and length after it.
func (h *hero) settings(gtx C, c *core.Ctx, pr config.Profile, has bool, lang string, secs int, narrow bool) D {
	th := c.Th
	if narrow {
		// A wrapped row of every profile would fill a phone's screen.
		name := "Типові налаштування"
		if has {
			name = pr.Name
		}
		one := func(gtx C) D {
			return gui.Anchor(gtx, th, AnchorProfiles, func(gtx C) D {
				return gui.Anchor(gtx, th, "home.profile", func(gtx C) D {
					return gui.Chip{Text: "Профіль: " + name, Icon: gui.IconSlidersHorizontal, Tip: "Обрати профіль"}.Layout(gtx, th, &h.oneProfile)
				})
			})
		}
		return gui.Flow{Gap: gui.Sp8}.Layout(gtx, one, func(gtx C) D { return h.pickers(gtx, th, lang, secs) })
	}
	return gui.Anchor(gtx, th, AnchorProfiles, func(gtx C) D {
		ws := []layout.Widget{gui.Text{Style: th.Label, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, "Профіль:")}
		for _, x := range c.E.Profiles() {
			x := x
			ws = append(ws, func(gtx C) D {
				return gui.Chip{Text: x.Name, Selected: has && x.ID == pr.ID}.Layout(gtx, th, h.chip(x.ID))
			})
		}
		// No profile: the app defaults (what the autopilot uses without one).
		ws = append(ws, func(gtx C) D {
			return gui.Chip{Text: "Типові налаштування", Icon: gui.IconSparkles, Selected: !has,
				Tip: "Без профілю — типові налаштування студії; усе можна змінити в проєкті"}.Layout(gtx, th, h.chip(""))
		})
		ws = append(ws, func(gtx C) D {
			return layout.Inset{Left: gui.Sp4}.Layout(gtx, func(gtx C) D {
				return gui.Link{Text: "Керувати", Style: th.Label}.Layout(gtx, th, &h.manage)
			})
		})
		return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
	})
}

// pickers are the language and length chips (their lists open under them).
func (h *hero) pickers(gtx C, th *gui.Theme, lang string, secs int) D {
	return gui.Flow{Gap: gui.Sp8}.Layout(gtx,
		func(gtx C) D {
			return gui.Anchor(gtx, th, "home.lang", func(gtx C) D {
				return gui.Chip{Text: languageLabel(lang), Icon: gui.IconLanguages, Tip: "Мова озвучки й субтитрів"}.Layout(gtx, th, &h.lang)
			})
		},
		func(gtx C) D {
			return gui.Anchor(gtx, th, "home.dur", func(gtx C) D {
				return gui.Chip{Text: durationLabel(secs), Icon: gui.IconTimer, Tip: "Тривалість фільму"}.Layout(gtx, th, &h.dur)
			})
		},
		func(gtx C) D { return h.modeChips(gtx, th) },
	)
}

// actions is the row that starts the film: «Автопілот», «Покроково», the
// language and length chips, then the estimate — at the row's end, or under
// it when the card is too narrow.
func (h *hero) actions(gtx C, c *core.Ctx, pr config.Profile, has bool, lang string, secs int, narrow bool) D {
	th := c.Th
	auto := func(gtx C) D {
		return gui.Anchor(gtx, th, AnchorAutopilot, func(gtx C) D {
			return gui.Button{Size: gui.Big, Text: "Автопілот", Icon: gui.IconRocket, Full: narrow, Busy: h.creating,
				Tip: "Студія сама пройде всі п'ять кроків"}.Layout(gtx, th, &h.auto)
		})
	}
	steps := func(gtx C) D {
		return gui.Button{Kind: gui.Secondary, Size: gui.Big, Text: "Покроково", Trail: gui.IconArrowRight, Full: narrow,
			Disabled: h.creating, Tip: "Відкрити проєкт і пройти кроки разом"}.Layout(gtx, th, &h.steps)
	}
	est := gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 2}.Widget(th, estimate(c, pr, has, secs, h.modes.sound))
	if narrow {
		// Phones: the two ways to start stacked at full width, primary first
		// (the language and length chips follow the profile chip).
		return gui.VStack(gtx, gui.Sp12, auto, steps, est)
	}
	row := func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(auto),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Rigid(steps),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Rigid(func(gtx C) D { return h.pickers(gtx, th, lang, secs) }),
		)
	}
	if gtx.Constraints.Max.X < gtx.Dp(900) {
		return gui.VStack(gtx, gui.Sp12, row, est)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(row),
		layout.Flexed(1, func(gtx C) D {
			return layout.Inset{Left: gui.Sp16}.Layout(gtx, func(gtx C) D { return layout.E.Layout(gtx, est) })
		}),
	)
}

// estimate is the line "≈ 8 сцен по 8 с · 16:9 · голос «Олена»" for a film
// made with the profile at secs seconds (e.Budget on a transient project).
func estimate(c *core.Ctx, pr config.Profile, has bool, secs int, sound string) string {
	p := studio.Project{Input: studio.DefaultInput()}
	if has {
		if pr.Aspect != "" {
			p.Input.Aspect = pr.Aspect
		}
		p.Input.Voiceover = pr.Voiceover
		p.Input.NarratorVoiceID = pr.NarratorVoiceID
	}
	p.Input.TargetSeconds = secs
	p.Input.Mode = studio.ModeStory
	if sound != "" { // the sound mode the chip shows
		p.Input.AudioMode, p.Input.Voiceover = sound, sound == studio.AudioVoiceover
	}
	scenes, clip, _ := c.E.Budget(p)
	per := fmt.Sprintf("по %d с", clip)
	if c.E.Config().Get().Video.Dynamic {
		// Dynamic lengths: every clip as long as its line, within what the
		// video model renders.
		if lo, hi := clipRange(c.E.ClipLengths(), c.E.ClipSeconds()); hi > lo {
			per = fmt.Sprintf("по %d–%d с", lo, hi)
		}
	}
	parts := []string{"≈ " + core.Count(scenes, "сцена", "сцени", "сцен") + " " + per, p.Input.Aspect}
	parts = append(parts, voiceText(c, p.Input))
	return strings.Join(parts, " · ")
}

// clipRange is the span of dynamic clip lengths: the model's lengths (s,
// ascending) up to the Settings length (the engine plans with the same set).
func clipRange(lengths []int, settings int) (lo, hi int) {
	if len(lengths) == 0 {
		return settings, settings
	}
	lo, hi = lengths[0], lengths[0]
	for _, l := range lengths {
		if l <= max(settings, lengths[0]) {
			hi = max(hi, l)
		}
	}
	return lo, hi
}

// voiceText names the narrator voice of an input.
func voiceText(c *core.Ctx, in studio.Input) string {
	switch in.SoundMode() {
	case studio.AudioInVideo:
		return "голос у відео"
	case studio.AudioSilent:
		return "без голосу"
	}
	id := in.NarratorVoiceID
	if id == "" {
		id = c.E.Config().Get().TTS.Voice
	}
	if id == "" {
		return "голос підбере студія"
	}
	list, _, _ := c.Voices()
	for _, v := range list.Voices {
		if v.ID == id && strings.TrimSpace(v.Name) != "" {
			return "голос «" + strings.TrimSpace(v.Name) + "»"
		}
	}
	return "голос із налаштувань"
}

// durationItems are the rows of the length chooser; the current length is
// added when it is not a preset (a profile's 48 s).
func durationItems(c *core.Ctx, cur int) []popItem {
	presets := append([]int(nil), pickers.DurationPresets...) // the same lengths as everywhere
	found := false
	for _, s := range presets {
		found = found || s == cur
	}
	if !found && cur > 0 {
		presets = append(presets, cur)
		sort.Ints(presets)
	}
	items := make([]popItem, 0, len(presets))
	for _, s := range presets {
		// The engine's own count (it caps very long films).
		p := studio.Project{Input: studio.DefaultInput()}
		p.Input.TargetSeconds = s
		n, _, _ := c.E.Budget(p)
		items = append(items, popItem{Value: strconv.Itoa(s), Label: durationLabel(s), Hint: "≈ " + core.Count(n, "сцена", "сцени", "сцен")})
	}
	return items
}

// ---- actions -------------------------------------------------------------------------------------

// project reads the card into Engine.CreateProject's options: the picked
// profile — or none at all for «Типові налаштування», not even the default
// autopilot profile — the picked language («Мова: авто» included) and
// length, and the pictures.
func (h *hero) project(c *core.Ctx, text string, auto bool) studio.NewProjectOptions {
	pr, has := h.profile(c)
	lang, secs := h.values(pr)
	opt := studio.NewProjectOptions{Title: TitleFrom(text), Text: text, Mode: studio.ModeStory,
		Materials: h.inputs(), Autopilot: auto, NoProfile: !has}
	if has {
		opt.ProfileID = pr.ID
	}
	if h.langSet {
		opt.Language, opt.LanguageSet = lang, true
	}
	if h.secsSet {
		opt.TargetSeconds = secs
	}
	h.modes.apply(&opt)
	return opt
}

// autopilot creates a project from the story and its materials and runs the
// whole pipeline on it, then opens the project.
func (p *Page) autopilot(c *core.Ctx) {
	h := &p.hero
	text := strings.TrimSpace(h.story.Text())
	if text == "" {
		h.err = "Спершу встав історію або опиши ідею — хоча б кілька речень, і автопілот зробить решту."
		h.focus = true
		p.home.scroll.List.Position = layout.Position{}
		return
	}
	if c.Offline {
		c.Run("", studio.StageAutopilot, false, "") // refuses: no generation in demo renders
		return
	}
	p.start(c, h.project(c, text, true), 0)
}

// stepByStep creates a project with the card's settings and materials and
// opens step 1 (an empty story keeps the default title the plan replaces).
func (p *Page) stepByStep(c *core.Ctx) {
	h := &p.hero
	p.start(c, h.project(c, strings.TrimSpace(h.story.Text()), false), 1)
}

// start creates the project in the background and opens it at step (0 =
// where it is). The card is cleared once the project exists; when nothing
// was created the story and the pictures stay (the error is toasted).
func (p *Page) start(c *core.Ctx, opt studio.NewProjectOptions, step int) {
	h := &p.hero
	if h.creating {
		return
	}
	h.creating = true
	c.Do(func() error {
		proj, err := c.E.CreateProject(opt) // copies the pictures: off the UI goroutine
		c.Post(func() {
			h.creating = false
			if proj.ID == "" {
				return
			}
			h.reset()
			c.Navigate(core.Route{Page: core.PageProject, ProjectID: proj.ID, Step: step})
			if err == nil && opt.Autopilot {
				c.Toast(gui.ToneRunning, "Автопілот запущено — прогрес видно в «Черзі».")
			}
		})
		return err
	}, nil)
}

// reset clears the card after a project was made from it.
func (h *hero) reset() {
	h.story.SetText("")
	h.langSet, h.secsSet, h.err = false, false, ""
	h.modes.reset()
	h.mats = nil
	h.edit.close()
}

// importTxt puts the text of a .txt file into the story box.
func (h *hero) importTxt(c *core.Ctx) {
	c.PickFile("Вибери текстовий файл з історією", []string{"txt"}, func(path string) {
		h.importing = true
		c.Background(func() {
			text, err := readStory(path)
			c.Post(func() {
				h.importing = false
				if err != nil {
					c.Error(err)
					return
				}
				h.story.SetText(text)
				h.err = ""
				n := words(text)
				c.Toast(gui.ToneSuccess, "Імпортовано "+core.Count(n, "слово", "слова", "слів"))
			})
		})
	})
}

func readStory(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("не вдалося прочитати файл: %w", err)
	}
	if st.Size() > maxImport {
		return "", fmt.Errorf("файл завеликий — історія може мати до 2 МБ тексту")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("не вдалося прочитати файл: %w", err)
	}
	text := strings.TrimSpace(decodeText(b))
	if text == "" {
		return "", fmt.Errorf("у файлі немає тексту")
	}
	return text, nil
}

// chip is the state of a profile chip ("" = Типові налаштування).
func (h *hero) chip(id string) *gui.Clickable {
	ck := h.chips[id]
	if ck == nil {
		ck = new(gui.Clickable)
		h.chips[id] = ck
	}
	return ck
}

// Values of the phone profile list besides the profile ids.
const (
	profDefault = "\x00default"
	profManage  = "\x00manage"
)

// profileItems are the rows of the phone profile list.
func profileItems(c *core.Ctx) []popItem {
	var items []popItem
	for _, pr := range c.E.Profiles() {
		items = append(items, popItem{Value: pr.ID, Label: pr.Name})
	}
	items = append(items,
		popItem{Value: profDefault, Label: "Типові налаштування", Icon: gui.IconSparkles},
		popItem{Value: profManage, Label: "Керувати профілями", Icon: gui.IconSettings2, Rule: true},
	)
	return items
}

func profileValue(pr config.Profile, has bool) string {
	if has {
		return pr.ID
	}
	return profDefault
}
