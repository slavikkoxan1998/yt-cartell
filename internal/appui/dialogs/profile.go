package dialogs

// The profile editor: every field of config.Profile, grouped like step 1 and
// the montage step, with the shared pickers. «Зберегти» → Engine.SaveProfile.

import (
	"fmt"
	"math"
	"strings"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

type profileSheet struct {
	drawer gui.Drawer
	sh     core.Sheet
	draft  config.Profile
	isNew  bool

	name    gui.TextField
	dur     pickers.Duration
	aspect  pickers.Aspect
	lang    pickers.Language
	style   pickers.Style
	custom  gui.TextField
	model   pickers.ImageModel
	camera  pickers.Choice
	genre   pickers.Choice
	tone    pickers.Choice
	tempo   pickers.Choice
	sound   pickers.Sound
	content pickers.Content
	voice   pickers.Voice
	narr    pickers.Choice
	notes   gui.TextField
	board   gui.Toggle
	chain   gui.Toggle
	pipe    pickers.Pipeline
	grammar pickers.CodeGrammar // «Граматика анімації» («Анімація кодом»)
	trans   pickers.Choice
	transD  gui.Slider
	effect  pickers.Choice
	filter  pickers.Choice
	subs    pickers.Choice
	subSt   pickers.Choice
	music   gui.Slider
	amb     gui.Slider
	duck    gui.Toggle

	ai aiSetup

	save, del gui.Clickable
	busy      bool
	err       string // engine error (footer)
	errA      gui.Anim
	nameErr   string
}

func (s *profileSheet) kind() core.SheetKind { return core.SheetProfile }
func (s *profileSheet) isOpen() bool         { return s.drawer.IsOpen() }
func (s *profileSheet) shut()                { s.drawer.Close() }

func (s *profileSheet) open(c *core.Ctx, sh core.Sheet) {
	s.sh = sh
	s.err, s.busy, s.nameErr = "", false, ""
	s.isNew = true
	s.draft = newProfile()
	switch {
	case sh.ProfileID != "":
		if pr, ok := c.E.Profile(sh.ProfileID); ok {
			s.draft, s.isNew = pr, false
		}
	case sh.FromProject != "":
		if p, ok := c.E.Get(sh.FromProject); ok {
			s.draft = profileOf(p)
		}
	}
	s.ai.reset(s.isNew)
	s.name.SetText(s.draft.Name)
	s.custom.SetText(s.draft.CustomStyle)
	s.notes.SetText(s.draft.Notes)
	s.drawer.Open()
}

// newProfile is the draft of a new profile: the app defaults of a project.
func newProfile() config.Profile {
	in, m := studio.DefaultInput(), studio.DefaultMontage()
	return config.Profile{
		Name: "Мій профіль", TargetSeconds: in.TargetSeconds, Aspect: in.Aspect, Style: in.Style,
		Tempo: in.Tempo, Camera: in.Camera, ImageModel: in.ImageModel, Voiceover: in.Voiceover, AudioMode: in.AudioMode,
		Content: in.Content, Overlays: in.Overlays, Transition: m.Transition, Subtitles: m.Subtitles, MusicVolume: m.MusicVolume,
		AmbientVolume: m.AmbientVolume, Ducking: m.Ducking,
	}
}

// profileOf is the profile a project's settings make (what
// Engine.ProfileFromProject saves), named after the project — as a draft the
// user can still edit before anything is saved.
func profileOf(p studio.Project) config.Profile {
	in, m := p.Input, p.Montage
	return config.Profile{
		Name:          strings.TrimSpace(p.Title),
		TargetSeconds: in.TargetSeconds, Aspect: in.Aspect, Style: in.Style, CustomStyle: in.CustomStyle,
		Genre: in.Genre, Tempo: in.Tempo, Camera: in.Camera, ImageModel: in.ImageModel,
		Voiceover: in.Voiceover, NarratorVoiceID: in.NarratorVoiceID, NarratorStyle: in.NarratorStyle, Language: in.Language, Tone: in.Tone,
		Notes: in.Notes, StoryboardMode: in.StoryboardMode, ChainFrames: in.ChainFrames,
		AudioMode: in.AudioMode, Content: in.Content, Overlays: in.Overlays, Pipeline: in.Pipeline, CodeGrammar: in.CodeGrammar,
		Transition: m.Transition, TransitionDuration: m.TransitionDuration, Filter: m.Filter, Effect: m.Effect,
		SubtitlePreset: m.SubtitleStyle.Preset, Subtitles: m.Subtitles,
		MusicVolume: m.MusicVolume, AmbientVolume: m.AmbientVolume, Ducking: m.Ducking,
	}
}

// Catalog lists as picker choices.
var (
	transitionChoices = func() []studio.Choice {
		out := make([]studio.Choice, len(media.Transitions))
		for i, t := range media.Transitions {
			out[i] = studio.Choice{ID: t.ID, Label: t.Label, Hint: t.Hint}
		}
		return out
	}()
	effectChoices = func() []studio.Choice {
		out := make([]studio.Choice, len(media.Effects))
		for i, t := range media.Effects {
			out[i] = studio.Choice{ID: t.ID, Label: t.Label, Hint: t.Hint}
		}
		return out
	}()
	filterChoices = func() []studio.Choice {
		out := make([]studio.Choice, len(media.Filters))
		for i, t := range media.Filters {
			out[i] = studio.Choice{ID: t.ID, Label: t.Label, Hint: t.Hint}
		}
		return out
	}()
	subtitlePresetChoices = func() []studio.Choice {
		out := make([]studio.Choice, len(media.SubtitlePresets))
		for i, t := range media.SubtitlePresets {
			out[i] = studio.Choice{ID: t.ID, Label: t.Label, Hint: t.Hint}
		}
		return out
	}()
	subtitleChoices = []studio.Choice{
		{ID: "none", Label: "Без субтитрів", Hint: "Лише звук"},
		{ID: "srt", Label: "Файлом .srt", Hint: "Окремий файл поруч із відео — YouTube підхопить його"},
		{ID: "burn", Label: "На відео", Hint: "Вшиті в кадр — видно всюди, навіть без звуку"},
	}
)

func hintOf(list []studio.Choice, id string) string {
	if ch, ok := studio.ChoiceByID(list, id); ok {
		return ch.Hint
	}
	return ""
}

func (s *profileSheet) layout(gtx C, c *core.Ctx, host *Sheets) {
	th := c.Th
	d := &s.draft
	if s.drawer.Dismissed() {
		s.err = ""
	}
	// Picks from the last frame.
	s.aiUpdate(gtx, c)
	if v, ok := s.name.Changed(gtx); ok {
		d.Name = v
		s.nameErr = ""
		s.touch("name")
	}
	if v, ok := s.custom.Changed(gtx); ok {
		d.CustomStyle = v
		s.touch("custom")
	}
	if v, ok := s.notes.Changed(gtx); ok {
		d.Notes = v
		s.touch("notes")
	}
	if v, ok := s.dur.Changed(); ok {
		d.TargetSeconds = v
		s.touch("dur")
	}
	if v, ok := s.aspect.Changed(); ok {
		d.Aspect = v
		s.touch("aspect")
	}
	if v, ok := s.lang.Changed(); ok {
		d.Language = v
		s.touch("lang")
	}
	if v, ok := s.style.Changed(); ok {
		d.Style = v
		s.touch("style")
	}
	if v, ok := s.model.Changed(); ok {
		d.ImageModel = v
		s.touch("model")
	}
	if v, ok := s.camera.Changed(); ok {
		d.Camera = v
		s.touch("camera")
	}
	if v, ok := s.genre.Changed(); ok {
		d.Genre = v
		s.touch("genre")
	}
	if v, ok := s.tone.Changed(); ok {
		d.Tone = v
		s.touch("tone")
	}
	if v, ok := s.tempo.Changed(); ok {
		d.Tempo = v
		s.touch("tempo")
	}
	if v, ok := s.sound.Changed(); ok {
		d.AudioMode, d.Voiceover = v, v == studio.AudioVoiceover
		s.touch("voiceover")
	}
	if v, ok := s.content.Changed(); ok {
		d.Content, d.Overlays = v.Content, v.Overlays
		s.touch("content")
	}
	if v, ok := s.voice.Changed(); ok {
		d.NarratorVoiceID = v
		s.touch("voice")
	}
	if v, ok := s.narr.Changed(); ok {
		d.NarratorStyle = v
		s.touch("narr")
	}
	if v, ok := s.board.Changed(gtx); ok {
		d.StoryboardMode = ""
		if v {
			d.StoryboardMode = "grid3x3"
		}
		s.touch("board")
	}
	if v, ok := s.chain.Changed(gtx); ok {
		d.ChainFrames = v
		s.touch("chain")
	}
	if v, ok := s.pipe.Changed(); ok {
		d.Pipeline = v
		s.touch("pipe")
	}
	if v, ok := s.grammar.Changed(); ok {
		d.CodeGrammar = v
		s.touch("grammar")
	}
	if v, ok := s.trans.Changed(); ok {
		d.Transition = v
		s.touch("trans")
	}
	if v, ok := s.transD.Changed(gtx); ok {
		d.TransitionDuration = math.Round(float64(v)*10) / 10
		s.touch("transD")
	}
	if v, ok := s.effect.Changed(); ok {
		d.Effect = v
		s.touch("effect")
	}
	if v, ok := s.filter.Changed(); ok {
		d.Filter = v
		s.touch("filter")
	}
	if v, ok := s.subs.Changed(); ok {
		d.Subtitles = v
		s.touch("subs")
	}
	if v, ok := s.subSt.Changed(); ok {
		d.SubtitlePreset = v
		s.touch("subSt")
	}
	if v, ok := s.music.Changed(gtx); ok {
		d.MusicVolume = float64(v)
		s.touch("music")
	}
	if v, ok := s.amb.Changed(gtx); ok {
		d.AmbientVolume = float64(v)
		s.touch("amb")
	}
	if v, ok := s.duck.Changed(gtx); ok {
		d.Ducking = v
		s.touch("duck")
	}
	if s.save.Clicked(gtx) {
		s.submit(c)
	}
	if s.del.Clicked(gtx) && !s.isNew {
		s.confirmDelete(c)
	}

	grid := func(gtx C, ws ...layout.Widget) D { return tileGrid(gtx, 280, ws...) }
	for _, l := range []*pickers.Look{&s.dur.Look, &s.aspect.Look, &s.lang.Look, &s.style.Look, &s.model.Look,
		&s.camera.Look, &s.genre.Look, &s.tone.Look, &s.tempo.Look, &s.voice.Look, &s.narr.Look, &s.sound.Look, &s.content.Look,
		&s.trans.Look, &s.effect.Look, &s.filter.Look, &s.subs.Look, &s.subSt.Look} {
		l.Flat = true // fields inside a sheet: cards never nest
	}
	s.genre.None, s.genre.Icon = "Авто — за історією", gui.IconDrama
	s.tone.None, s.tone.Icon = "Нейтральний оповідач", gui.IconMic
	s.camera.Icon, s.tempo.Icon = gui.IconCamera, gui.IconGauge
	s.narr.None, s.narr.Icon = "Будь-який", gui.IconMic
	s.narr.Note = "коли голос не вибрано"
	s.narr.Disabled = d.NarratorVoiceID != "" || (d.AudioMode != "" && d.AudioMode != studio.AudioVoiceover)
	s.trans.Icon, s.effect.Icon, s.filter.Icon = gui.IconBlend, gui.IconMove, gui.IconSwatchBook
	s.subs.Icon, s.subSt.Icon = gui.IconCaptions, gui.IconType
	s.effect.None = "Наближення (типово)"
	s.filter.None = "Без фільтра"
	s.subSt.None = "Класичні білі (типово)"
	s.trans.Note = hintOf(transitionChoices, d.Transition)
	s.effect.Note = hintOf(effectChoices, d.Effect)
	if d.Effect == "" {
		s.effect.Note = "для кадрів без відео"
	}
	s.filter.Note = hintOf(filterChoices, d.Filter)
	if d.Filter == "" {
		s.filter.Note = hintOf(filterChoices, "none")
	}
	s.subs.Note = hintOf(subtitleChoices, d.Subtitles)
	s.subSt.Note = hintOf(subtitlePresetChoices, d.SubtitlePreset)
	if d.SubtitlePreset == "" {
		s.subSt.Note = hintOf(subtitlePresetChoices, media.DefaultSubtitlePreset)
	}
	s.subSt.Disabled = d.Subtitles != "burn"
	if s.subSt.Disabled {
		s.subSt.Note = "для субтитрів на відео"
	}
	sound := studio.Input{AudioMode: d.AudioMode, Voiceover: d.Voiceover}.SoundMode()
	s.voice.Disabled = sound != studio.AudioVoiceover
	s.voice.Default = ""
	s.chain.Disabled = false

	body := func(gtx C) D {
		name := func(gtx C) D {
			return s.mark(th, "name", func(gtx C) D {
				return s.name.Layout(gtx, th, gui.FieldStyle{Label: "Назва профілю", Placeholder: "Напр. «Казки на ніч 9:16»", MaxLen: 80, Error: s.nameErr})
			})(gtx)
		}
		basics := func(gtx C) D {
			return grid(gtx,
				s.mark(th, "dur", func(gtx C) D { return s.dur.Layout(gtx, c, "Тривалість", d.TargetSeconds) }),
				s.mark(th, "aspect", func(gtx C) D { return s.aspect.Layout(gtx, c, "Формат", d.Aspect) }),
				s.mark(th, "lang", func(gtx C) D { return s.lang.Layout(gtx, c, "Мова фільму", d.Language) }),
			)
		}
		look := func(gtx C) D {
			return grid(gtx,
				s.mark(th, "style", func(gtx C) D { return s.style.Layout(gtx, c, "Стиль", d.Style) }),
				s.mark(th, "model", func(gtx C) D { return s.model.Layout(gtx, c, "Модель кадрів", d.ImageModel) }),
				s.mark(th, "camera", func(gtx C) D { return s.camera.Layout(gtx, c, "Камера", studio.Cameras, d.Camera) }),
			)
		}
		custom := func(gtx C) D {
			return s.mark(th, "custom", func(gtx C) D {
				return s.custom.Layout(gtx, th, gui.FieldStyle{Label: "Свій стиль (необов'язково)", MinLines: 2, MaxLines: 5, MaxLen: 1000,
					Placeholder: "Опиши вигляд англійською — він замінить вибраний стиль. Напр. «soft pastel watercolor, paper texture»"})
			})(gtx)
		}
		story := func(gtx C) D {
			return grid(gtx,
				s.mark(th, "genre", func(gtx C) D { return s.genre.Layout(gtx, c, "Жанр або ніша", studio.Genres, d.Genre) }),
				s.mark(th, "tone", func(gtx C) D { return s.tone.Layout(gtx, c, "Тон оповіді", studio.Tones, d.Tone) }),
				s.mark(th, "tempo", func(gtx C) D { return s.tempo.Layout(gtx, c, "Темп", studio.Tempos, d.Tempo) }),
			)
		}
		voice := func(gtx C) D {
			return grid(gtx,
				s.mark(th, "voiceover", func(gtx C) D { return s.sound.Layout(gtx, c, "Звук", sound) }),
				s.mark(th, "content", func(gtx C) D {
					return s.content.Layout(gtx, c, "Що в кадрі", pickers.ContentValue{Content: studio.Input{Content: d.Content}.ContentMode(), Overlays: d.Overlays})
				}),
				s.mark(th, "voice", func(gtx C) D { return s.voice.Layout(gtx, c, "Голос оповідача", d.NarratorVoiceID) }),
				s.mark(th, "narr", func(gtx C) D {
					return s.narr.Layout(gtx, c, "Тип голосу", studio.NarratorStyleChoices(), d.NarratorStyle)
				}),
			)
		}
		notes := func(gtx C) D {
			return s.mark(th, "notes", func(gtx C) D {
				return s.notes.Layout(gtx, th, gui.FieldStyle{Label: "Побажання режисеру", MinLines: 3, MaxLines: 6, MaxLen: 2000,
					Placeholder: "Що важливо в кожному фільмі цього профілю: настрій, для кого, чого уникати…"})
			})(gtx)
		}
		video := func(gtx C) D {
			if in := (studio.Input{Pipeline: d.Pipeline}); in.CodeAnim() {
				s.grammar.Aspect = d.Aspect
				return grid(gtx,
					s.mark(th, "pipe", func(gtx C) D { return s.pipe.Layout(gtx, c, "Режим виробництва", d.Pipeline) }),
					s.mark(th, "grammar", func(gtx C) D { return s.grammar.Layout(gtx, c, "Граматика анімації", d.CodeGrammar) }),
				)
			}
			return grid(gtx,
				s.mark(th, "board", func(gtx C) D {
					return toggleTile(gtx, th, &s.board, gui.IconGrid3x3, "Розкадровка 3×3", "Увімкнена", "Вимкнена",
						"аркуш 3×3 на сцену для Omni Flash", d.StoryboardMode == "grid3x3")
				}),
				s.mark(th, "chain", func(gtx C) D {
					return toggleTile(gtx, th, &s.chain, gui.IconLink, "Ланцюжок кадрів", "Увімкнений", "Вимкнений",
						"плавність між сценами; знімає по одній", d.ChainFrames)
				}),
				s.mark(th, "pipe", func(gtx C) D { return s.pipe.Layout(gtx, c, "Режим виробництва", d.Pipeline) }),
			)
		}
		montage := func(gtx C) D {
			dur := media.ClampTransitionDuration(d.TransitionDuration)
			return grid(gtx,
				s.mark(th, "trans", func(gtx C) D {
					return s.trans.Layout(gtx, c, "Перехід між сценами", transitionChoices, d.Transition)
				}),
				s.mark(th, "transD", func(gtx C) D {
					return sliderTile(gtx, th, &s.transD, gui.IconTimer, "Тривалість переходу", fmt.Sprintf("%.1f с", dur),
						gui.SliderStyle{Value: float32(dur), Min: float32(media.MinTransitionDuration), Max: float32(media.MaxTransitionDuration), Step: 0.1,
							Format: func(v float32) string { return fmt.Sprintf("%.1f с", v) }, Disabled: d.Transition == "none"})
				}),
				s.mark(th, "effect", func(gtx C) D { return s.effect.Layout(gtx, c, "Рух кадрів", effectChoices, d.Effect) }),
				s.mark(th, "filter", func(gtx C) D { return s.filter.Layout(gtx, c, "Колір фільму", filterChoices, d.Filter) }),
				s.mark(th, "subs", func(gtx C) D { return s.subs.Layout(gtx, c, "Субтитри", subtitleChoices, d.Subtitles) }),
				s.mark(th, "subSt", func(gtx C) D {
					return s.subSt.Layout(gtx, c, "Вигляд субтитрів", subtitlePresetChoices, d.SubtitlePreset)
				}),
			)
		}
		sound := func(gtx C) D {
			pct := func(v float32) string { return fmt.Sprintf("%d%%", int(v*100+0.5)) }
			return grid(gtx,
				s.mark(th, "music", func(gtx C) D {
					return sliderTile(gtx, th, &s.music, gui.IconMusic, "Гучність музики", pct(float32(d.MusicVolume)),
						gui.SliderStyle{Value: float32(d.MusicVolume), Min: 0, Max: 1, Step: 0.01, Format: pct})
				}),
				s.mark(th, "amb", func(gtx C) D {
					return sliderTile(gtx, th, &s.amb, gui.IconVolume2, "Фонові звуки кліпів", pct(float32(d.AmbientVolume)),
						gui.SliderStyle{Value: float32(d.AmbientVolume), Min: 0, Max: 1, Step: 0.01, Format: pct})
				}),
				s.mark(th, "duck", func(gtx C) D {
					return toggleTile(gtx, th, &s.duck, gui.IconHeadphones, "Приглушення", "Увімкнене", "Вимкнене",
						"музика тихішає під голос", d.Ducking)
				}),
			)
		}
		return gui.VStack(gtx, gui.Sp16,
			func(gtx C) D { return s.aiBlock(gtx, th) },
			name,
			heading(th, "Основне"), basics,
			heading(th, "Вигляд кадрів"), look, custom,
			heading(th, "Історія й голос"), story, voice, notes,
			heading(th, "Відео"), video,
			heading(th, "Монтаж"), montage, sound,
		)
	}
	title := "Новий профіль"
	sub := "Набір налаштувань для швидкого старту — вибирай його, коли створюєш проєкт."
	if !s.isNew {
		title = "Профіль «" + strings.TrimSpace(s.draft.Name) + "»"
		sub = "Зміни діятимуть для нових проєктів. Проєкти, створені раніше, не зміняться."
	} else if s.sh.FromProject != "" {
		sub = "Налаштування взято з проєкту — зміни, що треба, і збережи."
	}
	foot := func(gtx C) D {
		var left layout.Widget
		if !s.isNew {
			left = func(gtx C) D {
				return gui.Button{Kind: gui.Ghost, Icon: gui.IconTrash2, Text: "Видалити", Disabled: s.busy}.Layout(gtx, th, &s.del)
			}
		}
		return sheetFoot(gtx, th, &s.err, &s.errA, left, func(gtx C, full bool) D {
			return gui.Button{Kind: gui.Primary, Icon: gui.IconSave, Text: "Зберегти профіль", Busy: s.busy, Full: full}.Layout(gtx, th, &s.save)
		})
	}
	s.drawer.Layout(gtx, th, gui.DrawerStyle{Title: title, Subtitle: sub, Width: 760, Body: body, Footer: foot})
}

// sliderTile is a flat picker-style tile holding a slider.
func sliderTile(gtx C, th *gui.Theme, sl *gui.Slider, icon *gui.Icon, label, value string, st gui.SliderStyle) D {
	return flatTile(gtx, th, gui.Sp16, st.Disabled, func(gtx C) D {
		free := gtx
		free.Constraints.Min.Y = 0
		return gui.VStack(free, gui.Sp8,
			func(gtx C) D { return tileRow(gtx, flatVisual(th, icon), tileTexts(th, label, value, ""), nil) },
			func(gtx C) D { return sl.Layout(gtx, th, st) },
		)
	})
}

func (s *profileSheet) submit(c *core.Ctx) {
	if s.busy {
		return
	}
	d := s.draft
	d.Name = strings.TrimSpace(s.name.Editor.Text())
	d.CustomStyle = s.custom.Editor.Text()
	d.Notes = s.notes.Editor.Text()
	if d.Name == "" {
		s.nameErr = "Дай профілю назву — так його буде легко знайти."
		return
	}
	s.err, s.busy = "", true
	done, isNew := s.sh.Done, s.isNew
	c.Background(func() {
		out, err := c.E.SaveProfile(d)
		c.Post(func() {
			s.busy = false
			if err != nil {
				s.err = core.ErrorText(err)
				return
			}
			s.drawer.Close()
			msg := "Профіль «" + out.Name + "» збережено"
			if isNew {
				msg = "Профіль «" + out.Name + "» створено"
			}
			c.Toast(gui.ToneSuccess, msg)
			if done != nil {
				done(out.ID)
			}
		})
	})
}

func (s *profileSheet) confirmDelete(c *core.Ctx) {
	id, name := s.draft.ID, strings.TrimSpace(s.draft.Name)
	c.Confirm(core.Confirm{
		Title: "Видалити профіль «" + name + "»?",
		Text:  "Проєкти, створені з нього, збережуть свої налаштування.",
		Icon:  gui.IconTrash2, OK: "Видалити", Danger: true,
		OnOK: func() {
			c.Background(func() {
				err := c.E.DeleteProfile(id)
				if err == nil {
					_ = c.E.Config().Update(func(cf *config.Config) {
						if cf.Autopilot.DefaultProfileID == id {
							cf.Autopilot.DefaultProfileID = ""
						}
					})
				}
				c.Post(func() {
					if err != nil {
						s.err = core.ErrorText(err)
						return
					}
					s.drawer.Close()
					c.Toast(gui.ToneSuccess, "Профіль «"+name+"» видалено")
				})
			})
		},
	})
}
