package montage

// «Експортувати в…»: the film as a project for the owner's own editor
// (studio.Engine.ExportTimelineContext) instead of — or besides — the FFmpeg
// montage. A menu next to «Змонтувати відео» picks the editor; a drawer
// shows what is ready, what goes into the package (voice, music,
// subtitles, on-screen text, cover, sound stems, ZIP), runs the export with
// its progress and cancel, then «Відкрити папку» and the import steps for
// that editor. The project's ⋯ menu opens the same drawer
// (core.ProjectUI.Export).

import (
	"context"
	"fmt"
	"image"
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

// exportPhase is where the drawer is.
type exportPhase int

const (
	exportIdle exportPhase = iota
	exportRunning
	exportDone
	exportFailed
)

// exportIcons are the editors' icons (Lucide: the editors' own marks are
// trademarks).
var exportIcons = map[string]*gui.Icon{
	studio.ExportResolve:  gui.IconAperture,
	studio.ExportPremiere: gui.IconFilm,
	studio.ExportFinalCut: gui.IconClapperboard,
	studio.ExportCapCut:   gui.IconScissors,
	studio.ExportKdenlive: gui.IconLayers,
	studio.ExportOTIO:     gui.IconListVideo,
	studio.ExportFiles:    gui.IconFolderOpen,
}

// exportCaptions are the menu's second lines.
var exportCaptions = map[string]string{
	studio.ExportResolve:  "таймлайн XML · FCPXML · EDL",
	studio.ExportPremiere: "Final Cut Pro 7 XML",
	studio.ExportFinalCut: "FCPXML 1.10",
	studio.ExportCapCut:   "нарізані сцени + голос і музика + SRT",
	studio.ExportKdenlive: "OpenTimelineIO · MLT",
	studio.ExportOTIO:     ".otio для будь-якого редактора",
	studio.ExportFiles:    "файли + монтажний лист",
}

type exportSheet struct {
	drawer gui.Drawer
	// menu is the «Експортувати в…» dropdown of the final card.
	menu    pickers.Menu
	menuBtn gui.Clickable

	target                                   string
	pick                                     gui.Select
	voice, music, subs, texts, coverT, stems gui.Toggle
	optVoice, optMusic, optSubs, optTexts    bool
	optCover, optStems, optZip               bool
	zip                                      gui.Segmented
	dir                                      string // "" = <project>/export
	dirBtn, dirReset                         gui.Clickable
	run, cancel, stop, close, open, zipOpen  gui.Clickable
	again, retry                             gui.Clickable
	phase                                    exportPhase
	pct                                      int
	msg                                      string
	res                                      studio.ExportResult
	err                                      string
	stopFn                                   context.CancelFunc
	bar                                      gui.Anim
	ready                                    studio.ExportReadiness
	readyVer                                 int64
	readyErr                                 error
	fakeReady                                *studio.ExportReadiness // screenshots
}

// openExport shows the drawer for target ("" = the last one, else Resolve).
func (s *proj) openExport(c *core.Ctx, pv *core.ProjectView, target string) {
	st := &s.exp
	if st.phase == exportRunning {
		st.drawer.Open()
		return
	}
	if target == "" {
		target = st.target
	}
	if _, ok := studio.ExportTargetByID(target); !ok {
		target = studio.ExportResolve
	}
	st.target = target
	st.phase, st.err, st.pct, st.msg = exportIdle, "", 0, ""
	st.refresh(c, pv, true)
	st.drawer.Open()
}

// refresh re-reads what the project can export (file checks: only when
// the project changed) and resets the switches to what it has.
func (st *exportSheet) refresh(c *core.Ctx, pv *core.ProjectView, reset bool) {
	if st.fakeReady != nil {
		st.ready, st.readyErr = *st.fakeReady, nil
	} else if reset || st.readyVer != pv.Ver {
		st.ready, st.readyErr = c.E.ExportCheck(pv.ID)
	}
	st.readyVer = pv.Ver
	if reset {
		r := st.ready
		st.optVoice, st.optMusic, st.optSubs = r.Voices > 0, r.Music, r.Subtitles
		st.optTexts, st.optCover = r.Overlays, r.Cover
		st.optStems = false
	}
}

// exportMenuItems lists the editors.
func exportMenuItems() []pickers.MenuItem {
	var items []pickers.MenuItem
	for _, t := range studio.ExportTargets() {
		items = append(items, pickers.MenuItem{Value: t.ID, Label: t.Label, Caption: exportCaptions[t.ID], Icon: exportIcons[t.ID],
			Divider: t.ID == studio.ExportFiles})
	}
	return items
}

// exportButton is «Експортувати в…» with its menu (disabled with the reason
// as a tooltip when nothing can be exported).
func (s *proj) exportButton(gtx C, c *core.Ctx, pv *core.ProjectView, size gui.Size, kind gui.ButtonKind, full bool) D {
	th := c.Th
	st := &s.exp
	if v, ok := st.menu.Picked(); ok {
		s.openExport(c, pv, v)
	}
	blocker := ""
	if len(s.film.items) == 0 {
		blocker = "Немає жодного кліпу чи кадру — спершу згенеруй кадри або відео."
	}
	st.menu.MinWidth = 300
	st.menu.Left = true
	return st.menu.Layout(gtx, th, &st.menuBtn, exportMenuItems(), func(gtx C) D {
		tip := "Перенести фільм у DaVinci Resolve, Premiere, Final Cut, CapCut…"
		if blocker != "" {
			tip = blocker
		}
		return gui.Button{Kind: kind, Size: size, Icon: gui.IconExternalLink, Text: "Експортувати в…", Trail: gui.IconChevronDown,
			Disabled: blocker != "", Tip: tip, Full: full}.Layout(gtx, th, &st.menuBtn)
	})
}

// options are the drawer's switches as engine options.
func (st *exportSheet) options() studio.ExportOptions {
	o := studio.DefaultExportOptions(st.target)
	o.Voice, o.Music, o.Subtitles, o.Overlays, o.Cover = st.optVoice, st.optMusic, st.optSubs, st.optTexts, st.optCover
	o.Stems = o.Stems || st.optStems
	o.Zip = st.optZip
	o.Dir = st.dir
	o.Partial = !st.ready.Complete()
	return o
}

// start runs the export in the background.
func (s *proj) startExport(c *core.Ctx, pv *core.ProjectView) {
	st := &s.exp
	if st.phase == exportRunning || st.ready.Blocker != "" {
		return
	}
	if c.Offline {
		c.Toast(gui.ToneInfo, "Демо-режим: експорт вимкнено")
		return
	}
	id := pv.ID
	opt := st.options()
	ctx, cancel := context.WithCancel(context.Background())
	st.stopFn = cancel
	st.phase, st.pct, st.msg, st.err = exportRunning, 0, "Готуємо файли", ""
	st.bar.Set(0)
	opt.Progress = func(pct int, msg string) {
		c.Post(func() {
			if st.phase == exportRunning {
				st.pct, st.msg = pct, msg
			}
		})
	}
	c.Background(func() {
		res, err := c.E.ExportTimelineContext(ctx, id, opt)
		c.Post(func() {
			st.stopFn = nil
			switch {
			case err != nil && ctx.Err() != nil:
				st.phase = exportIdle
				c.Toast(gui.ToneInfo, "Експорт зупинено")
			case err != nil:
				st.phase, st.err = exportFailed, core.ErrorText(err)
			default:
				st.phase, st.res = exportDone, res
				if !st.drawer.IsOpen() {
					c.Toast(gui.ToneSuccess, "Експорт для "+res.Target.Label+" готовий")
				}
			}
		})
	})
}

// exportLayout draws the drawer (called every frame by the step).
func (s *proj) exportLayout(gtx C, c *core.Ctx, pv *core.ProjectView) {
	st := &s.exp
	th := c.Th
	if st.target == "" {
		return // never opened
	}
	if st.drawer.IsOpen() {
		st.refresh(c, pv, false)
	}
	tgt, _ := studio.ExportTargetByID(st.target)

	// Actions.
	if v, ok := st.pick.Changed(gtx); ok && st.phase != exportRunning {
		st.target, st.phase = v, exportIdle
	}
	for _, t := range []struct {
		tg  *gui.Toggle
		val *bool
	}{{&st.voice, &st.optVoice}, {&st.music, &st.optMusic}, {&st.subs, &st.optSubs}, {&st.texts, &st.optTexts}, {&st.coverT, &st.optCover}, {&st.stems, &st.optStems}} {
		if v, ok := t.tg.Changed(gtx); ok {
			*t.val = v
		}
	}
	if v, ok := st.zip.Changed(gtx); ok {
		st.optZip = v == "zip"
	}
	if st.dirBtn.Clicked(gtx) {
		start := st.dir
		if start == "" {
			start = pv.Dir
		}
		c.PickFolder("Куди зберегти експорт", start, func(path string) { st.dir = path })
	}
	if st.dirReset.Clicked(gtx) {
		st.dir = ""
	}
	if st.run.Clicked(gtx) || st.retry.Clicked(gtx) {
		s.startExport(c, pv)
	}
	if st.stop.Clicked(gtx) && st.stopFn != nil {
		st.stopFn()
	}
	if st.again.Clicked(gtx) {
		st.phase = exportIdle
	}
	if st.cancel.Clicked(gtx) || st.close.Clicked(gtx) {
		st.drawer.Close()
	}
	if st.open.Clicked(gtx) && st.res.Dir != "" {
		c.OpenPath(st.res.Dir)
	}
	if st.zipOpen.Clicked(gtx) && st.res.Zip != "" {
		c.Reveal(st.res.Zip)
	}

	body := func(gtx C) D {
		switch st.phase {
		case exportRunning:
			return st.runningBody(gtx, th, tgt)
		case exportDone:
			return st.doneBody(gtx, th)
		}
		return st.idleBody(gtx, c, pv, tgt)
	}
	foot := func(gtx C) D { return st.footer(gtx, th) }
	title := "Експорт у " + tgt.Label
	if st.target == studio.ExportFiles {
		title = "Експорт файлами"
	}
	st.drawer.Layout(gtx, th, gui.DrawerStyle{Title: title, Subtitle: "Фільм для твого редактора — без монтажу FFmpeg: файли, доріжки й склейки як у студії.",
		Width: 500, Body: body, Footer: foot, NoDismiss: st.phase == exportRunning})
}

// ---- bodies -------------------------------------------------------------------------------------

func (st *exportSheet) idleBody(gtx C, c *core.Ctx, pv *core.ProjectView, tgt studio.ExportTarget) D {
	th := c.Th
	r := st.ready
	var opts []gui.SelectOption
	for _, t := range studio.ExportTargets() {
		opts = append(opts, gui.SelectOption{Value: t.ID, Label: t.Label, Hint: t.Formats, Icon: exportIcons[t.ID]})
	}
	ws := []layout.Widget{
		func(gtx C) D {
			return st.pick.Layout(gtx, th, gui.SelectStyle{Label: "Редактор", Options: opts, Value: st.target, Leading: exportIcons[st.target]})
		},
	}
	// What the package holds for this editor.
	ws = append(ws, func(gtx C) D {
		return infoRow(gtx, th, gui.IconInfo, gui.ToneNeutral, packageLine(tgt))
	})
	if tgt.Note != "" {
		ws = append(ws, func(gtx C) D { return infoRow(gtx, th, gui.IconLightbulb, gui.ToneNeutral, tgt.Note) })
	}
	switch {
	case st.readyErr != nil:
		ws = append(ws, func(gtx C) D {
			return infoRow(gtx, th, gui.IconCircleAlert, gui.ToneError, core.ErrorText(st.readyErr))
		})
	case r.Blocker != "":
		ws = append(ws, func(gtx C) D { return infoRow(gtx, th, gui.IconCircleAlert, gui.ToneError, r.Blocker) })
	case !r.Complete():
		ws = append(ws, func(gtx C) D {
			return infoRow(gtx, th, gui.IconAlertTriangle, gui.ToneWarning, "Не все готово: "+r.Missing()+". Можна експортувати те, що є, а решту долити в редакторі.")
		})
	default:
		ws = append(ws, func(gtx C) D {
			return infoRow(gtx, th, gui.IconCheckCircle, gui.ToneSuccess, fmt.Sprintf("Усе готово: %s, %s.",
				count(r.Clips, "кліп", "кліпи", "кліпів"), count(r.Voices, "репліка", "репліки", "реплік")))
		})
	}
	if st.err != "" {
		ws = append(ws, func(gtx C) D { return infoRow(gtx, th, gui.IconCircleAlert, gui.ToneError, st.err) })
	}
	ws = append(ws, func(gtx C) D { return gui.Text{Style: th.Label}.Layout(gtx, th, "Що включити") })
	row := func(tg *gui.Toggle, icon *gui.Icon, label, caption string, on, have bool) layout.Widget {
		return func(gtx C) D {
			tg.Disabled = !have
			if !have {
				on = false
				caption = "немає в проєкті"
			}
			return toggleRow(gtx, th, tg, icon, label, caption, on)
		}
	}
	forced := st.target == studio.ExportCapCut || st.target == studio.ExportFiles
	stemsCap := "увесь голос і музика (з приглушенням) — двома WAV на всю довжину"
	if forced {
		stemsCap = "для " + tgt.Label + " — завжди: так голос і музика стають на місце одним рухом"
	}
	ws = append(ws, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp8,
			row(&st.voice, gui.IconMic, "Озвучка", count(r.Voices, "репліка", "репліки", "реплік")+" на своїх місцях", st.optVoice, r.Voices > 0),
			row(&st.music, gui.IconMusic, "Музика", "з гучністю й приглушенням під голосом", st.optMusic, r.Music),
			row(&st.subs, gui.IconCaptions, "Субтитри", "файл subtitles.srt — ті самі, що в монтажі", st.optSubs, r.Subtitles),
			row(&st.texts, gui.IconType, "Текст у кадрі", "прозорі PNG над сценами", st.optTexts, r.Overlays),
			row(&st.coverT, gui.IconImage, "Обкладинка", "cover.jpg для YouTube", st.optCover, r.Cover),
			func(gtx C) D {
				st.stems.Disabled = forced
				return toggleRow(gtx, th, &st.stems, gui.IconAudioLines, "Звук окремими доріжками", stemsCap, st.optStems || forced)
			},
		)
	})
	ws = append(ws, func(gtx C) D {
		val := "folder"
		if st.optZip {
			val = "zip"
		}
		return gui.VStack(gtx, gui.Sp8,
			gui.Text{Style: th.Label}.Widget(th, "Як зберегти"),
			func(gtx C) D {
				st.zip.Full = true
				return st.zip.Layout(gtx, th, val, []gui.SegOption{{Value: "folder", Label: "Папка", Icon: gui.IconFolder}, {Value: "zip", Label: "Папка + ZIP", Icon: gui.IconHardDrive}})
			},
		)
	})
	ws = append(ws, func(gtx C) D {
		where := "Папка проєкту › export"
		if st.dir != "" {
			where = st.dir
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Right: gui.Sp8}.Layout(gtx, gui.IconFolderOpen.Widget(18, th.Pal.Text2))
			}),
			layout.Flexed(1, func(gtx C) D {
				return gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Layout(gtx, th, "Куди: "+where)
			}),
			layout.Rigid(func(gtx C) D {
				if st.dir != "" {
					return gui.IconButton{Icon: gui.IconRotateCcw, Size: 32, Tip: "У папку проєкту"}.Layout(gtx, th, &st.dirReset)
				}
				return D{}
			}),
			layout.Rigid(func(gtx C) D {
				return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Text: "Змінити…"}.Layout(gtx, th, &st.dirBtn)
			}),
		)
	})
	return gui.VStack(gtx, gui.Sp16, ws...)
}

// packageLine says what the package holds for an editor.
func packageLine(t studio.ExportTarget) string {
	switch t.ID {
	case studio.ExportCapCut:
		return "Сцени, вже нарізані точно під фільм і пронумеровані по порядку, голос і музика цілими доріжками, текст у кадрі, субтитри та інструкція."
	case studio.ExportFiles:
		return "Усі файли фільму, пронумеровані за сценами, монтажний лист (звідки й куди), голос і музика цілими доріжками, субтитри."
	}
	return "Таймлайн (" + t.Formats + "): кліпи з обрізкою й переходами, голос і музика на своїх доріжках, текст у кадрі, субтитри. Файли — у папці media."
}

// toggleRow is a switch with an icon, a label and a caption.
func toggleRow(gtx C, th *gui.Theme, tg *gui.Toggle, icon *gui.Icon, label, caption string, on bool) D {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	col := th.Pal.Text
	if tg.Disabled {
		col = th.Pal.Text3
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			s := gtx.Dp(36)
			fillRR(gtx, image.Rectangle{Max: pt(s, s)}, gtx.Dp(gui.RadiusControl), th.Pal.Surface)
			is := gtx.Dp(18)
			o := op.Offset(pt((s-is)/2, (s-is)/2)).Push(gtx.Ops)
			icon.Layout(gtx, 18, col)
			o.Pop()
			return layout.Inset{Right: gui.Sp12}.Layout(gtx, func(gtx C) D { return D{Size: pt(s, s)} })
		}),
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium), Color: col, MaxLines: 1}.Widget(th, label)),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, caption)),
			)
		}),
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Left: gui.Sp12}.Layout(gtx, func(gtx C) D { return tg.Layout(gtx, th, on, "") })
		}),
	)
}

func (st *exportSheet) runningBody(gtx C, th *gui.Theme, tgt studio.ExportTarget) D {
	return gui.VStack(gtx, gui.Sp16,
		func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return gui.Spinner{Size: 20, Color: th.Pal.Running}.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Flexed(1, gui.Text{Style: th.Body.Weighted(gui.FontMedium), MaxLines: 2}.Widget(th, st.msg)),
				layout.Rigid(gui.Text{Style: th.Label, Color: th.Pal.Text2}.Widget(th, fmt.Sprintf("%d%%", st.pct))),
			)
		},
		func(gtx C) D {
			return gui.ProgressBar{Value: float32(st.pct) / 100, Tone: gui.ToneRunning, Anim: &st.bar}.Layout(gtx, th)
		},
		hint(th, "Кліпи й голос потрапляють у папку посиланнями — це швидко й не займає місця. "+longerFor(tgt)),
	)
}

func longerFor(t studio.ExportTarget) string {
	if t.ID == studio.ExportCapCut {
		return "Для CapCut кожна сцена ще нарізається під фільм — це хвилина-дві."
	}
	return "Зазвичай кілька секунд."
}

func (st *exportSheet) doneBody(gtx C, th *gui.Theme) D {
	res := st.res
	tgt := res.Target
	ws := []layout.Widget{
		func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					d := gtx.Dp(44)
					fillCircle(gtx, float32(d)/2, float32(d)/2, float32(d)/2, th.Pal.SuccessSoft)
					is := gtx.Dp(22)
					o := opOffset(gtx, (d-is)/2, (d-is)/2)
					gui.IconCheck.Layout(gtx, 22, th.Pal.SuccessInk)
					o.Pop()
					return D{Size: pt(d, d)}
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, "Готово — "+filepath.Base(res.Dir))),
						layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, doneMeta(res))),
					)
				}),
			)
		},
		func(gtx C) D {
			btns := []layout.Widget{func(gtx C) D {
				return gui.Button{Icon: gui.IconFolderOpen, Text: "Відкрити папку"}.Layout(gtx, th, &st.open)
			}}
			if res.Zip != "" {
				btns = append(btns, func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Icon: gui.IconHardDrive, Text: "Показати ZIP"}.Layout(gtx, th, &st.zipOpen)
				})
			}
			return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, btns...)
		},
		func(gtx C) D { return gui.Text{Style: th.Label}.Layout(gtx, th, "Як відкрити в "+tgt.Label) },
	}
	for i, step := range tgt.Steps {
		n, step := i+1, step
		ws = append(ws, func(gtx C) D { return numberedStep(gtx, th, n, step) })
	}
	if tgt.Note != "" {
		ws = append(ws, func(gtx C) D { return infoRow(gtx, th, gui.IconLightbulb, gui.ToneNeutral, tgt.Note) })
	}
	if len(res.Notes) > 0 {
		ws = append(ws, func(gtx C) D {
			return gui.Text{Style: th.Label}.Layout(gtx, th, "Що перенесено приблизно")
		})
		for _, n := range res.Notes {
			n := n
			ws = append(ws, func(gtx C) D {
				return layout.Flex{Alignment: layout.Start}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, "•  ")),
					layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, n)),
				)
			})
		}
	}
	ws = append(ws, hint(th, "Усе розписано й у README.txt у папці експорту."))
	return gui.VStack(gtx, gui.Sp12, ws...)
}

func doneMeta(res studio.ExportResult) string {
	parts := []string{count(res.Files, "файл", "файли", "файлів")}
	if res.Timeline != "" {
		parts = append(parts, "таймлайн "+filepath.Base(res.Timeline))
	}
	if res.Partial {
		parts = append(parts, "не все було готово")
	}
	return strings.Join(parts, " · ")
}

// numberedStep is "1" in a small circle and the step's text.
func numberedStep(gtx C, th *gui.Theme, n int, s string) D {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Flex{Alignment: layout.Start}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			d := gtx.Dp(22)
			fillCircle(gtx, float32(d)/2, float32(d)/2, float32(d)/2, th.Pal.ActionSoft)
			g := gtx
			g.Constraints = layout.Exact(pt(d, d))
			layout.Center.Layout(g, gui.Text{Style: th.Caption.Weighted(gui.FontSemiBold), Color: th.Pal.ActionText, Align: text.Middle}.Widget(th, itoa(n)))
			return layout.Inset{Right: gui.Sp12}.Layout(gtx, func(gtx C) D { return D{Size: pt(d, d)} })
		}),
		layout.Flexed(1, gui.Text{Style: th.Body}.Widget(th, s)),
	)
}

func (st *exportSheet) footer(gtx C, th *gui.Theme) D {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	right := func(ws ...layout.Widget) D {
		var ch []layout.FlexChild
		ch = append(ch, layout.Flexed(1, func(gtx C) D { return D{Size: pt(gtx.Constraints.Min.X, 0)} }))
		for i, w := range ws {
			if i > 0 {
				ch = append(ch, layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout))
			}
			ch = append(ch, layout.Rigid(w))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
	}
	switch st.phase {
	case exportRunning:
		return right(func(gtx C) D {
			return gui.Button{Kind: gui.Secondary, Icon: gui.IconCircleStop, Text: "Зупинити"}.Layout(gtx, th, &st.stop)
		})
	case exportDone:
		return right(
			func(gtx C) D {
				return gui.Button{Kind: gui.Tinted, Text: "Експортувати ще"}.Layout(gtx, th, &st.again)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Text: "Закрити"}.Layout(gtx, th, &st.close)
			},
		)
	case exportFailed:
		return right(
			func(gtx C) D { return gui.Button{Kind: gui.Tinted, Text: "Закрити"}.Layout(gtx, th, &st.close) },
			func(gtx C) D {
				return gui.Button{Icon: gui.IconRefreshCw, Text: "Спробувати ще раз"}.Layout(gtx, th, &st.retry)
			},
		)
	}
	label := "Експортувати"
	if !st.ready.Complete() {
		label = "Експортувати те, що є"
	}
	return right(
		func(gtx C) D {
			return gui.Button{Kind: gui.Tinted, Text: "Скасувати"}.Layout(gtx, th, &st.cancel)
		},
		func(gtx C) D {
			return gui.Button{Icon: gui.IconExternalLink, Text: label, Disabled: st.ready.Blocker != "" || st.readyErr != nil}.Layout(gtx, th, &st.run)
		},
	)
}
