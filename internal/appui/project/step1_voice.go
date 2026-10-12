package project

import (
	"image"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/gui"
	"ytcartell/internal/stt"
	"ytcartell/internal/studio"
)

// voicePanel is the story card body in "Своя озвучка": the recording
// (player, replace, recognise, remove) and the recognised text.
type voicePanel struct {
	pick, replace, transcribe, remove gui.Clickable
	pill                              gui.AudioPill
	// A ready transcript with timecodes (SRT, VTT, JSON, TXT).
	trAdd, trReplace, trRemove gui.Clickable
	trAdding                   bool

	trKey     string // what the loaded transcript belongs to
	tr        stt.Transcript
	trOK      bool
	trLoading bool
}

// maxSegments is how many phrases of the transcript the preview lists.
const maxSegments = 10

func timeNow() time.Time { return time.Now() }

func (v *voicePanel) layout(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	id := pv.ID
	in := pv.P.Input
	if v.pick.Clicked(gtx) || v.replace.Clicked(gtx) {
		v.pickFile(c, id)
	}
	if v.transcribe.Clicked(gtx) {
		switch {
		case c.Offline:
			c.Toast(gui.ToneError, offlineText)
		default:
			c.Error(c.E.Transcribe(id))
		}
	}
	if v.remove.Clicked(gtx) {
		c.Confirm(core.Confirm{
			Title:  "Прибрати запис?",
			Text:   "Файл озвучки зникне з проєкту. Сцени, вже нарізані з нього, залишаться, але їхні голоси позначаться як змінені.",
			Icon:   gui.IconTrash2,
			OK:     "Прибрати",
			Danger: true,
			OnOK:   func() { c.Do(func() error { return c.E.SetVoiceFile(id, "") }, nil) },
		})
	}
	if v.trAdd.Clicked(gtx) || v.trReplace.Clicked(gtx) {
		v.pickTranscript(c, id)
	}
	if v.trRemove.Clicked(gtx) {
		c.Do(func() error { return c.E.SetTranscriptFile(id, "") }, nil)
	}
	if in.VoiceFile == "" {
		return v.empty(gtx, th)
	}
	v.loadTranscript(c, pv)
	transcribing := pv.RunningKind() == studio.JobTranscribe
	busy := pv.Running()
	hasTr := in.TranscriptFile != ""
	dur := time.Duration(in.TargetSeconds) * time.Second
	if v.trOK && v.tr.Duration > 0 {
		dur = time.Duration(v.tr.Duration * float64(time.Second))
	}
	player := func(gtx C) D {
		return v.pill.Layout(gtx, th, gui.AudioStyle{Src: pv.Abs(in.VoiceFile), Label: firstText(in.VoiceFileName, "Мій запис"),
			Duration: dur, Backend: c.Audio})
	}
	buttons := func(gtx C) D {
		return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx,
			func(gtx C) D {
				if hasTr {
					return D{} // the attached text has the timecodes: nothing to recognise
				}
				return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconAudioLines, Text: "Розпізнати мову", Busy: transcribing,
					Disabled: busy && !transcribing, Tip: "Перетворити запис на текст із таймінгом"}.Layout(gtx, th, &v.transcribe)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconUpload, Text: "Замінити файл", Disabled: busy}.Layout(gtx, th, &v.replace)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconTrash2, Tip: "Прибрати запис", Disabled: busy}.Layout(gtx, th, &v.remove)
			},
		)
	}
	top := func(gtx C) D {
		if gtx.Constraints.Max.X < gtx.Dp(720) {
			return gui.VStack(gtx, gui.Sp12, player, buttons)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(player),
			layout.Flexed(1, func(gtx C) D { return layout.E.Layout(gtx, buttons) }),
		)
	}
	return gui.VStack(gtx, gui.Sp16, top,
		func(gtx C) D { return v.transcriptRow(gtx, th, in, busy) },
		func(gtx C) D { return v.transcript(gtx, c, pv) })
}

// empty invites to add the recording.
func (v *voicePanel) empty(gtx C, th *gui.Theme) D {
	return gui.Card{Tint: true, Radius: gui.RadiusInner, Padding: gui.Sp32}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Center.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					sz := gtx.Dp(72)
					gui.FillCircle(gtx, 72, th.Pal.Card)
					is := gtx.Dp(30)
					o := op.Offset(image.Pt((sz-is)/2, (sz-is)/2)).Push(gtx.Ops)
					gui.IconFileAudio.Layout(gtx, 30, th.Pal.ActionText)
					o.Pop()
					return D{Size: image.Pt(sz, sz)}
				}),
				layout.Rigid(layout.Spacer{Height: gui.Sp16}.Layout),
				layout.Rigid(gui.Text{Style: th.H3}.Widget(th, "Завантаж запис свого голосу")),
				layout.Rigid(layout.Spacer{Height: gui.Sp4}.Layout),
				layout.Rigid(func(gtx C) D {
					gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(520))
					return gui.Text{Style: th.Body, Color: th.Pal.Text2, Align: text.Middle}.Layout(gtx, th,
						"MP3, WAV, M4A, OGG, FLAC або відео зі звуком. Студія розпізнає мову, наріже сцени під паузи й намалює до них кадри.")
				}),
				layout.Rigid(layout.Spacer{Height: gui.Sp20}.Layout),
				layout.Rigid(func(gtx C) D {
					return gui.Button{Icon: gui.IconUpload, Text: "Вибрати файл"}.Layout(gtx, th, &v.pick)
				}),
			)
		})
	})
}

// loadTranscript reads the transcript sidecar off the UI goroutine whenever
// the recording or its text changed.
func (v *voicePanel) loadTranscript(c *core.Ctx, pv *core.ProjectView) {
	key := transcriptKey(pv)
	if key == v.trKey || v.trLoading {
		return
	}
	v.trLoading = true
	id := pv.ID
	c.Background(func() {
		tr, ok := c.E.VoiceTranscript(id)
		c.Post(func() {
			v.tr, v.trOK, v.trKey, v.trLoading = tr, ok, key, false
			c.Invalidate()
		})
	})
}

// transcriptKey names what a loaded transcript belongs to: the recording,
// the attached text and the last job (a finished recognition).
func transcriptKey(pv *core.ProjectView) string {
	in := pv.P.Input
	fin := int64(0)
	if j := pv.P.Job; j != nil {
		fin = j.FinishedAt
	}
	return in.VoiceFile + "|" + in.TranscriptFile + "|" + strconv.Itoa(len(in.Text)) + "|" + strconv.FormatInt(fin, 10)
}

// transcript previews the recognised speech (phrases with their times).
func (v *voicePanel) transcript(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	in := pv.P.Input
	head := func(title, meta string) layout.Widget {
		return func(gtx C) D {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(gui.Text{Style: th.Label}.Widget(th, title)),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, meta)),
			)
		}
	}
	noteText := "Текст узято із запису. Помилки розпізнавання можна виправити в Плані — голос усе одно звучить твій."
	if in.TranscriptFile != "" {
		noteText = "Текст і таймкоди взято з твого файлу. Помилки можна виправити в Плані — голос усе одно звучить твій."
	}
	note := gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, noteText)
	box := func(w layout.Widget) layout.Widget {
		return func(gtx C) D {
			return gui.Card{Tint: true, Radius: gui.RadiusInner, Padding: gui.Sp16}.Layout(gtx, th, w)
		}
	}
	switch {
	case v.trOK && len(v.tr.Segments) > 0:
		meta := words(len(v.tr.Words)) + " · " + core.Clock(v.tr.Duration)
		if v.tr.Language != "" {
			meta += " · " + pickers.LanguageLabel(v.tr.Language)
		}
		segs := v.tr.Segments
		more := 0
		if len(segs) > maxSegments {
			more = len(segs) - maxSegments
			segs = segs[:maxSegments]
		}
		rows := make([]layout.Widget, 0, len(segs)+1)
		for _, sg := range segs {
			sg := sg
			rows = append(rows, func(gtx C) D {
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						gtx.Constraints.Min.X = gtx.Dp(48)
						return gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: th.Pal.Text3, MaxLines: 1}.Layout(gtx, th, core.Clock(sg.Start))
					}),
					layout.Flexed(1, gui.Text{Style: th.Body}.Widget(th, sg.Text)),
				)
			})
		}
		if more > 0 {
			rows = append(rows, gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, "і ще "+core.Count(more, "фраза", "фрази", "фраз")+" — усі підуть у сцени"))
		}
		return gui.VStack(gtx, gui.Sp12, head(trTitle(in), meta), box(func(gtx C) D { return gui.VStack(gtx, gui.Sp8, rows...) }), note)
	case in.Text != "":
		return gui.VStack(gtx, gui.Sp12, head(trTitle(in), words(countWords(in.Text))),
			box(gui.Text{Style: th.Body, MaxLines: 8}.Widget(th, in.Text)), note)
	}
	return box(func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(gui.IconInfo.Widget(18, th.Pal.Text2)),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Flexed(1, gui.Text{Style: th.Small.Sized(14), Color: th.Pal.Text2}.Widget(th,
				"Текст ще не розпізнано. Натисни «Розпізнати мову», щоб перевірити його, — або одразу «Створити план»: студія розпізнає запис сама.")),
		)
	})(gtx)
}

// pickFile asks for the recording and copies it into the project (off the
// UI goroutine).
func (v *voicePanel) pickFile(c *core.Ctx, id string) {
	c.PickFile("Вибери запис свого голосу", studio.VoiceExts(), func(path string) {
		c.Do(func() error { return c.E.SetVoiceFile(id, path) }, func() {
			c.Toast(gui.ToneSuccess, "Запис додано — можна розпізнати мову або одразу створити план")
		})
	})
}

// pickTranscript attaches a ready transcript with timecodes (off the UI
// goroutine): then the speech needs no recognition.
func (v *voicePanel) pickTranscript(c *core.Ctx, id string) {
	if v.trAdding {
		return
	}
	c.PickFile("Вибери текст із таймкодами (SRT, VTT, JSON або TXT)", studio.TranscriptExts(), func(path string) {
		v.trAdding = true
		c.Do(func() error {
			err := c.E.SetTranscriptFile(id, path)
			if err != nil {
				c.Post(func() { v.trAdding = false }) // c.Do shows the error
			}
			return err
		}, func() {
			v.trAdding = false
			c.Toast(gui.ToneSuccess, "Текст із таймкодами додано — розпізнавати мову не треба")
		})
	})
}

// transcriptRow offers to attach a ready transcript, or shows the attached one.
func (v *voicePanel) transcriptRow(gtx C, th *gui.Theme, in studio.Input, busy bool) D {
	has := in.TranscriptFile != ""
	icon := func(gtx C) D {
		sz := gtx.Dp(40)
		gui.FillRRect(gtx, image.Rect(0, 0, sz, sz), 10, th.Pal.Card)
		is := gtx.Dp(20)
		o := op.Offset(image.Pt((sz-is)/2, (sz-is)/2)).Push(gtx.Ops)
		gui.IconFileText.Layout(gtx, 20, th.Pal.Text)
		o.Pop()
		return D{Size: image.Pt(sz, sz)}
	}
	title, note := "Текст із таймкодами", "Є субтитри чи розшифровка запису? Додай SRT, VTT, JSON або TXT — і розпізнавати мову не доведеться."
	if has {
		title, note = "Текст із таймкодами · "+trFormat(in.TranscriptFile), "Текст і таймкоди вже є — розпізнавати мову не треба."
	}
	actions := func(gtx C) D {
		if !has {
			txt := "Додати файл"
			if v.trAdding {
				txt = "Додаємо…"
			}
			return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconFileUp, Text: txt, Busy: v.trAdding, Disabled: busy}.Layout(gtx, th, &v.trAdd)
		}
		return gui.HStack(gtx, gui.Sp4,
			func(gtx C) D {
				return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Text: "Замінити", Busy: v.trAdding, Disabled: busy}.Layout(gtx, th, &v.trReplace)
			},
			func(gtx C) D {
				return gui.IconButton{Icon: gui.IconX, Size: 36, Tip: "Прибрати текст із таймкодами", Disabled: busy}.Layout(gtx, th, &v.trRemove)
			},
		)
	}
	text := func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(gui.Text{Style: th.Label, MaxLines: 1}.Widget(th, title)),
			layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 3}.Widget(th, note)),
		)
	}
	return inset(gtx, th, gui.Sp12, func(gtx C) D {
		if gtx.Constraints.Max.X < gtx.Dp(520) {
			return gui.VStack(gtx, gui.Sp12,
				func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(icon), layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout), layout.Flexed(1, text))
				},
				func(gtx C) D { return layout.W.Layout(gtx, actions) },
			)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(icon),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Flexed(1, text),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Rigid(actions),
		)
	})
}

// trTitle names the transcript preview by where its text comes from.
func trTitle(in studio.Input) string {
	if in.TranscriptFile != "" {
		return "Текст із файлу"
	}
	return "Розпізнаний текст"
}

// trFormat names the attached transcript by its format ("SRT"), never by
// the stored file name.
func trFormat(rel string) string {
	if ext := strings.ToUpper(strings.TrimPrefix(path.Ext(filepath.ToSlash(rel)), ".")); ext != "" {
		return ext
	}
	return "файл"
}
