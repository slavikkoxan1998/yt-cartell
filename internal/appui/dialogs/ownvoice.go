package dialogs

// «Своя озвучка»: the user's own recording drives the film — the speech is
// recognised (or read from a ready transcript with timecodes), the scenes
// are cut to it and the voice-over is the recording itself, not TTS. A new
// project (Покроково / Автопілот, with the user's materials) or, with
// Sheet.ProjectID, a new recording for an existing project.

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

type ownVoiceSheet struct {
	drawer gui.Drawer
	sh     core.Sheet

	pick, change gui.Clickable
	path         string
	size         int64

	// The optional transcript with timecodes (SRT, VTT, JSON, TXT).
	tpick, tchange, tremove gui.Clickable
	tpath                   string
	tsize                   int64

	mat       materialsEditor
	title     gui.TextField
	chips     []gui.Clickable
	profileID string
	lang      pickers.Language
	language  string

	auto, steps, replace gui.Clickable
	busy                 string
	err                  string
	errA                 gui.Anim
}

func (s *ownVoiceSheet) kind() core.SheetKind { return core.SheetOwnVoice }
func (s *ownVoiceSheet) isOpen() bool         { return s.drawer.IsOpen() }
func (s *ownVoiceSheet) shut()                { s.drawer.Close() }

func (s *ownVoiceSheet) open(c *core.Ctx, sh core.Sheet) {
	s.sh = sh
	s.path, s.size = "", 0
	s.tpath, s.tsize = "", 0
	s.err, s.busy = "", ""
	s.title.SetText("")
	s.language = ""
	s.mat.reset()
	def := strings.TrimSpace(c.E.Config().Get().Autopilot.DefaultProfileID)
	if _, ok := c.E.Profile(def); !ok {
		def = ""
	}
	s.profileID = def
	s.drawer.Open()
}

// setFile takes a picked recording.
func (s *ownVoiceSheet) setFile(path string) {
	s.path, s.size, s.err = path, fileSize(path), ""
}

// setTranscript takes a picked transcript.
func (s *ownVoiceSheet) setTranscript(path string) {
	s.tpath, s.tsize, s.err = path, fileSize(path), ""
}

func fileSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

func (s *ownVoiceSheet) layout(gtx C, c *core.Ctx, host *Sheets) {
	th := c.Th
	if s.drawer.Dismissed() {
		s.err = ""
	}
	existing := s.sh.ProjectID != ""
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
	if s.pick.Clicked(gtx) || s.change.Clicked(gtx) {
		c.PickFile("Твоя озвучка — аудіо або відео з голосом", studio.VoiceExts(), s.setFile)
	}
	if s.tpick.Clicked(gtx) || s.tchange.Clicked(gtx) {
		c.PickFile("Текст озвучки з таймкодами (SRT, VTT, JSON або TXT)", studio.TranscriptExts(), s.setTranscript)
	}
	if s.tremove.Clicked(gtx) {
		s.tpath, s.tsize = "", 0
	}
	if !existing {
		s.mat.update(gtx, c)
	}
	if v, ok := s.lang.Changed(); ok {
		s.language = v
	}
	s.lang.Flat = true // inside the sheet
	if s.auto.Clicked(gtx) {
		s.start(c, "auto")
	}
	if s.steps.Clicked(gtx) {
		s.start(c, "steps")
	}
	if s.replace.Clicked(gtx) {
		s.start(c, "replace")
	}

	body := func(gtx C) D {
		ws := []layout.Widget{
			func(gtx C) D {
				return steps(gtx, th,
					stepItem{gui.IconMic, "Твій запис", "Аудіо або відео з голосом — розповідь від першого до останнього слова."},
					stepItem{gui.IconAudioLines, "Студія розпізнає мову", "Текст і точний час кожного слова. Є текст із таймкодами — розпізнавати не треба."},
					stepItem{gui.IconScissors, "Сцени — під твоє мовлення", "Кадри й кліпи ріжуться по фразах, а у фільмі звучить твій голос замість синтезованого."},
				)
			},
			heading(th, "Запис"),
			func(gtx C) D { return s.fileTile(gtx, th) },
			func(gtx C) D { return s.transcriptTile(gtx, th) },
		}
		if existing {
			if pv := c.Project(s.sh.ProjectID); pv != nil {
				ws = append(ws, func(gtx C) D {
					return panel(gtx, th, gui.ToneNeutral, gui.ToneNeutral, gui.IconInfo, "Проєкт «"+pv.P.Title+"»",
						"Новий запис замінить попередній. Сцени наріжуться під нього, коли ти перебудуєш план на кроці «План».")
				})
			}
		} else {
			ws = append(ws,
				func(gtx C) D { return layout.Spacer{Height: gui.Sp4}.Layout(gtx) },
				func(gtx C) D {
					return s.mat.layout(gtx, c, "Фото героїв, місць, предметів чи зразок стилю — студія візьме саме їх для кадрів.")
				},
				func(gtx C) D { return layout.Spacer{Height: gui.Sp4}.Layout(gtx) },
				func(gtx C) D {
					return s.title.Layout(gtx, th, gui.FieldStyle{Label: "Назва (необов'язково)", Placeholder: "Студія придумає сама за змістом", MaxLen: 120})
				},
				func(gtx C) D {
					var chips []layout.Widget
					for i, pr := range profiles {
						i, pr := i, pr
						chips = append(chips, func(gtx C) D {
							return gui.Chip{Text: pr.Name, Selected: pr.ID == s.profileID, Tip: summary(pr)}.Layout(gtx, th, &s.chips[i])
						})
					}
					n := len(profiles)
					chips = append(chips, func(gtx C) D {
						return gui.Chip{Text: "Типові налаштування", Selected: s.profileID == "", Icon: gui.IconSparkles}.Layout(gtx, th, &s.chips[n])
					})
					return gui.VStack(gtx, gui.Sp8,
						gui.Text{Style: th.Label}.Widget(th, "Профіль"),
						func(gtx C) D { return gui.Flow{Gap: gui.Sp8}.Layout(gtx, chips...) },
					)
				},
				func(gtx C) D {
					s.lang.Note = ""
					if s.language == "" {
						s.lang.Note = "розпізнається із запису"
					}
					return s.lang.Layout(gtx, c, "Мова запису", s.language)
				},
			)
		}
		return gui.VStack(gtx, gui.Sp16, ws...)
	}
	foot := func(gtx C) D {
		noFile := s.path == ""
		if existing {
			return sheetFoot(gtx, th, &s.err, &s.errA, nil, func(gtx C, full bool) D {
				return gui.Button{Kind: gui.Primary, Icon: gui.IconMic, Text: "Замінити озвучку", Busy: s.busy == "replace",
					Disabled: noFile, Full: full}.Layout(gtx, th, &s.replace)
			})
		}
		return sheetFoot(gtx, th, &s.err, &s.errA, nil,
			func(gtx C, full bool) D {
				b := gui.Button{Kind: gui.Secondary, Text: "Покроково", Trail: gui.IconArrowRight, Busy: s.busy == "steps",
					Disabled: noFile || s.busy == "auto", Full: full}
				if full {
					b.Trail = nil
				}
				return b.Layout(gtx, th, &s.steps)
			},
			func(gtx C, full bool) D {
				return gui.Button{Kind: gui.Primary, Text: "Автопілот", Icon: gui.IconRocket, Busy: s.busy == "auto",
					Disabled: noFile || s.busy == "steps", Full: full}.Layout(gtx, th, &s.auto)
			},
		)
	}
	title := "Своя озвучка"
	sub := "Фільм під твій голос: запис → розпізнавання → сцени точно під мовлення."
	if existing {
		title = "Нова озвучка для проєкту"
	}
	s.drawer.Layout(gtx, th, gui.DrawerStyle{Title: title, Subtitle: sub, Width: 600, Body: body, Footer: foot})
}

// fileTile is the big «pick a file» button, or the picked file.
func (s *ownVoiceSheet) fileTile(gtx C, th *gui.Theme) D {
	if s.path == "" {
		return gui.Card{Tint: true, Click: &s.pick, Padding: gui.Sp24, Radius: gui.RadiusInner}.Layout(gtx, th, func(gtx C) D {
			W := gtx.Constraints.Max.X
			free := gtx
			free.Constraints.Min = image.Point{}
			d, call := record(free, func(gtx C) D {
				return s.pickBody(gtx, th)
			})
			o := op.Offset(image.Pt((W-d.Size.X)/2, 0)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			o.Pop()
			return D{Size: image.Pt(W, d.Size.Y)}
		})
	}
	return s.pickedTile(gtx, th)
}

// pickBody is the content of the «pick a file» tile (centred by the caller).
func (s *ownVoiceSheet) pickBody(gtx C, th *gui.Theme) D {
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			sz := gtx.Dp(56)
			circle(gtx, sz, th.Pal.Card)
			is := gtx.Dp(26)
			o := opOffset(gtx, (sz-is)/2, (sz-is)/2)
			gui.IconFileAudio.Layout(gtx, 26, th.Pal.Text)
			o.Pop()
			return D{Size: pointXY(sz, sz)}
		}),
		layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout),
		layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontSemiBold)}.Widget(th, "Вибрати аудіо або відео")),
		layout.Rigid(layout.Spacer{Height: 2}.Layout),
		layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, Align: text.Middle}.Widget(th, "MP3, WAV, M4A, OGG, FLAC або MP4, MOV, WEBM · до 2 ГБ")),
	)
}

// pickedTile shows the chosen recording.
func (s *ownVoiceSheet) pickedTile(gtx C, th *gui.Theme) D {
	name := filepath.Base(s.path)
	meta := "готово до розпізнавання"
	if s.tpath != "" {
		meta = "текст береться з файлу нижче"
	}
	if s.size > 0 {
		meta = sizeText(s.size) + " · " + meta
	}
	return flatTile(gtx, th, gui.Sp16, false, func(gtx C) D {
		return tileRowWrap(gtx, flatVisual(th, gui.IconFileAudio), tileTexts(th, "", name, meta), func(gtx C) D {
			return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Text: "Змінити"}.Layout(gtx, th, &s.change)
		})
	})
}

// transcriptTile offers (or shows) the optional transcript with timecodes.
func (s *ownVoiceSheet) transcriptTile(gtx C, th *gui.Theme) D {
	if s.tpath == "" {
		return flatTile(gtx, th, gui.Sp16, false, func(gtx C) D {
			return tileRowWrap(gtx, flatVisual(th, gui.IconCaptions),
				tileTexts(th, "Необов'язково", "Текст із таймкодами",
					"SRT, VTT, JSON чи TXT з часом фраз — з ним нічого не треба розпізнавати."),
				func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconPlus, Text: "Додати"}.Layout(gtx, th, &s.tpick)
				})
		})
	}
	meta := "розпізнавання не потрібне"
	if s.tsize > 0 {
		meta = sizeText(s.tsize) + " · " + meta
	}
	return flatTile(gtx, th, gui.Sp16, false, func(gtx C) D {
		return tileRowWrap(gtx, flatVisual(th, gui.IconCaptions), tileTexts(th, "Текст із таймкодами", filepath.Base(s.tpath), meta),
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Text: "Змінити"}.Layout(gtx, th, &s.tchange)
					}),
					layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
					layout.Rigid(func(gtx C) D {
						return gui.IconButton{Icon: gui.IconX, Size: 32, Tip: "Прибрати текст"}.Layout(gtx, th, &s.tremove)
					}),
				)
			})
	})
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f ГБ", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f МБ", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d КБ", n>>10)
	}
	return fmt.Sprintf("%d Б", n)
}

// start runs the chosen action in the background (copying the recording can
// take a moment) and opens the project.
func (s *ownVoiceSheet) start(c *core.Ctx, how string) {
	if s.busy != "" {
		return
	}
	if s.path == "" {
		s.err = "Спершу вибери файл із записом."
		return
	}
	path, tpath := s.path, s.tpath
	// «Типові налаштування» = no profile at all; the shown language applies
	// as is («Авто» = recognised from the recording).
	opt := studio.NewProjectOptions{
		Title:     strings.TrimSpace(s.title.Editor.Text()),
		ProfileID: s.profileID, NoProfile: s.profileID == "",
		Language: s.language, LanguageSet: true, Mode: studio.ModeVoice,
		VoiceFile: path, TranscriptFile: tpath,
		Materials: s.mat.inputs(), Autopilot: how == "auto",
	}
	existing, done := s.sh.ProjectID, s.sh.Done
	s.busy, s.err = how, ""
	e := c.E
	c.Background(func() {
		var id string
		var err, soft error
		switch how {
		case "replace":
			id = existing
			if err = e.SetVoiceFile(id, path); err == nil {
				if tpath != "" {
					err = e.SetTranscriptFile(id, tpath)
				} else {
					soft = e.Transcribe(id)
				}
			}
		default:
			var p studio.Project
			p, err = e.CreateProject(opt)
			id = p.ID
			if err == nil && how == "steps" && tpath == "" {
				// Step 1 shows the recognised text: start on it right away.
				if soft = e.Transcribe(id); errors.Is(soft, studio.ErrBusy) {
					soft = nil // the engine already started it
				}
			}
			if id != "" && err != nil {
				soft, err = err, nil // created; tell what failed after
			}
		}
		c.Post(func() {
			s.busy = ""
			if err != nil {
				s.err = core.ErrorText(err)
				return
			}
			s.drawer.Close()
			switch {
			case soft != nil:
				c.Error(soft)
			case how == "auto" && tpath != "":
				c.Toast(gui.ToneSuccess, "Автопілот почав роботу — прогрес у черзі")
			case how == "auto":
				c.Toast(gui.ToneSuccess, "Автопілот почав роботу: спершу розпізнаємо мову — прогрес у черзі")
			case tpath != "":
				c.Toast(gui.ToneSuccess, "Запис і текст із таймкодами додано")
			default:
				c.Toast(gui.ToneSuccess, "Розпізнаємо мову запису — прогрес видно в черзі")
			}
			if done != nil {
				done(id)
			}
			r := core.Route{Page: core.PageProject, ProjectID: id}
			if how != "auto" {
				r.Step = 1
			}
			c.Navigate(r)
		})
	})
}
