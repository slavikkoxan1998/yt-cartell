package project

// Step 1's «Розбір прикладу»: the YouTube example a remake follows
// (Project.Reference, studio/remake.go). While the remake job runs: its
// stages (link → Gemini watches → the screenplay → the autopilot) with the
// progress and «Зупинити»; after a failure: what went wrong and «Спробувати
// ще раз»; when it is done: the thumbnail, the facts, and the breakdown as an
// editable text — the screenplay is written from it («Переписати сценарій»),
// «Подивитися ще раз» lets the model watch again. The card collapses to its
// header.

import (
	"image"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// referenceCard is the widget state of the card (one per project).
type referenceCard struct {
	closed bool // collapsed by the user
	fold   gui.Anim

	toggle, link, stop, retry, rewrite, again, settings gui.Clickable
	text                                                bound
	thumb                                               gui.ImageView
	bar                                                 gui.Anim
}

// remake stages as the job's progress shows them (studio/remake.go).
var remakeStages = []struct {
	icon  *gui.Icon
	title string
	from  int // the job's progress where the stage starts
}{
	{gui.IconLink, "Перевіряю посилання", 0},
	{gui.IconEye, "Gemini дивиться й слухає відео", 5},
	{gui.IconScrollText, "Пишу сценарій за розбором", 72},
}

var fidelityLabels = map[string]string{
	studio.FidelityExact:     "Точна копія",
	studio.FidelityClose:     "Близько до оригіналу",
	studio.FidelityStructure: "Лише структура й темп",
}

func (s *referenceCard) layout(gtx C, c *core.Ctx, pv *core.ProjectView, ps *projState) D {
	ref := pv.P.Reference
	if !ref.Active() {
		return D{}
	}
	th := c.Th
	id := pv.ID
	job := pv.P.Job
	mine := job != nil && job.Kind == studio.JobRemake
	running := mine && job.Status == studio.JobRunning
	failed := mine && (job.Status == studio.JobError || job.Status == studio.JobCancelled)

	if s.toggle.Clicked(gtx) {
		s.closed = !s.closed
	}
	if s.link.Clicked(gtx) {
		c.OpenPath(ref.URL)
	}
	if s.stop.Clicked(gtx) {
		c.E.Cancel(id)
	}
	if s.retry.Clicked(gtx) {
		s.run(c, func() error { return c.E.Retry(id) })
	}
	if s.settings.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageSettings, Section: "connections"})
	}
	if s.rewrite.Clicked(gtx) {
		c.Confirm(core.Confirm{
			Title: "Переписати сценарій?",
			Text:  "Студія напише сценарій наново з цього розбору — з твоїми правками. Текст історії й налаштування кроку «Сценарій» заміняться; план, якщо він уже є, залишиться, доки ти не оновиш його.",
			Icon:  gui.IconRefreshCw, OK: "Переписати",
			OnOK: func() { s.run(c, func() error { return c.E.RewriteRemake(id) }) },
		})
	}
	if s.again.Clicked(gtx) {
		c.Confirm(core.Confirm{
			Title: "Подивитися відео ще раз?",
			Text:  "Gemini перегляне приклад наново, розбір і сценарій буде написано заново — твої правки в розборі зникнуть.",
			Icon:  gui.IconEye, OK: "Подивитися ще раз",
			OnOK: func() { s.run(c, func() error { return c.E.ReanalyzeRemake(id) }) },
		})
	}
	s.text.update(gtx, &ps.bind, c.Frame(), ref.Breakdown, func(v string) {
		c.Edit(id, func(p *studio.Project) { p.Reference.Breakdown = v })
	})

	open := !s.closed || running || failed
	s.fold.Duration = gui.DurDialog
	a := s.fold.Animate(gtx, b2f(open))

	head := cardTitle(th, gui.IconMonitorPlay, "Розбір прикладу", refNote(ref, running), func(gtx C) D {
		var ws []layout.Widget
		ws = append(ws, func(gtx C) D {
			return gui.IconButton{Icon: gui.IconExternalLink, Size: 32, Tip: "Відкрити приклад на YouTube"}.Layout(gtx, th, &s.link)
		})
		if !running && !failed {
			ic, tip := gui.IconChevronUp, "Згорнути розбір"
			if s.closed {
				ic, tip = gui.IconChevronDown, "Розгорнути розбір"
			}
			ws = append(ws, func(gtx C) D { return gui.IconButton{Icon: ic, Size: 32, Tip: tip}.Layout(gtx, th, &s.toggle) })
		}
		return gui.HStack(gtx, gui.Sp4, ws...)
	})
	var body layout.Widget
	switch {
	case running:
		body = func(gtx C) D { return s.progress(gtx, th, job, ref) }
	case failed:
		body = func(gtx C) D { return s.failure(gtx, th, job, ref) }
	case strings.TrimSpace(ref.Breakdown) != "" || ref.Stage != "":
		body = func(gtx C) D { return s.done(gtx, c, pv, ref) }
	default:
		body = func(gtx C) D {
			return gui.Text{Style: th.Small, Color: th.Pal.Text2}.Layout(gtx, th, "Розбору ще немає — натисни «Подивитися ще раз».")
		}
	}
	return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, 0,
			head,
			func(gtx C) D {
				return collapse(gtx, a, func(gtx C) D {
					return layout.Inset{Top: gui.Sp16}.Layout(gtx, body)
				})
			},
		)
	})
}

// run starts a remake action off the UI goroutine (errors become toasts).
func (s *referenceCard) run(c *core.Ctx, fn func() error) {
	if c.Offline {
		c.Toast(gui.ToneError, offlineText)
		return
	}
	s.closed = false
	c.Do(fn, nil)
}

// refNote is the header's line: what the example is.
func refNote(ref studio.Reference, running bool) string {
	var parts []string
	if t := strings.TrimSpace(ref.Title); t != "" {
		parts = append(parts, "«"+t+"»")
	} else {
		parts = append(parts, "youtube.com/watch?v="+ref.VideoID)
	}
	if ref.Channel != "" {
		parts = append(parts, ref.Channel)
	}
	if ref.Duration > 0 {
		parts = append(parts, core.Clock(ref.Duration))
	}
	if !running {
		if l := fidelityLabels[ref.Fidelity]; l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, " · ")
}

// progress shows the stages of the running remake.
func (s *referenceCard) progress(gtx C, th *gui.Theme, job *studio.Job, ref studio.Reference) D {
	pct := max(0, min(100, job.Progress))
	cur := 0
	for i, st := range remakeStages {
		if pct >= st.from {
			cur = i
		}
	}
	// A rewrite starts at the screenplay.
	if ref.Stage == "analysed" && cur < 2 {
		cur = 2
	}
	detail := strings.TrimPrefix(job.Message, "Перезйомка · ")
	rows := []layout.Widget{}
	for i, st := range remakeStages {
		i, st := i, st
		rows = append(rows, func(gtx C) D {
			state := 0 // waiting
			switch {
			case i < cur:
				state = 2
			case i == cur:
				state = 1
			}
			sub := ""
			if state == 1 && detail != "" && detail != st.title+"…" {
				sub = detail
			}
			return stageRow(gtx, th, st.icon, st.title, sub, state)
		})
	}
	if ref.Autopilot {
		rows = append(rows, func(gtx C) D {
			return stageRow(gtx, th, gui.IconRocket, "Далі — автопілот: план, кадри, відео, озвучка, монтаж", "", 0)
		})
	}
	return gui.VStack(gtx, gui.Sp16,
		func(gtx C) D { return gui.VStack(gtx, gui.Sp12, rows...) },
		func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					return gui.ProgressBar{Value: float32(pct) / 100, Tone: gui.ToneRunning, Anim: &s.bar}.Layout(gtx, th)
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Rigid(gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: th.Pal.Text2, MaxLines: 1}.Widget(th, strconv.Itoa(pct)+" %")),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconCircleStop, Text: "Зупинити"}.Layout(gtx, th, &s.stop)
				}),
			)
		},
		gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, "Можна закрити проєкт — робота триває у фоні, прогрес є в черзі."),
	)
}

// stageRow is one stage: done (check), now (spinner) or waiting (dot).
func stageRow(gtx C, th *gui.Theme, ic *gui.Icon, title, sub string, state int) D {
	mark := func(gtx C) D {
		sz := gtx.Dp(28)
		switch state {
		case 2:
			gui.FillCircle(gtx, 28, th.Pal.SuccessSoft)
			o := op.Offset(image.Pt((sz-gtx.Dp(16))/2, (sz-gtx.Dp(16))/2)).Push(gtx.Ops)
			gui.IconCheck.Layout(gtx, 16, th.Pal.SuccessInk)
			o.Pop()
		case 1:
			gui.FillCircle(gtx, 28, th.Pal.RunningSoft)
			o := op.Offset(image.Pt((sz-gtx.Dp(16))/2, (sz-gtx.Dp(16))/2)).Push(gtx.Ops)
			gui.Spinner{Size: 16, Color: th.Pal.Running}.Layout(gtx, th)
			o.Pop()
		default:
			gui.FillCircle(gtx, 28, th.Pal.Surface)
			o := op.Offset(image.Pt((sz-gtx.Dp(16))/2, (sz-gtx.Dp(16))/2)).Push(gtx.Ops)
			ic.Layout(gtx, 16, th.Pal.Text3)
			o.Pop()
		}
		return D{Size: image.Pt(sz, sz)}
	}
	col := th.Pal.Text
	if state == 0 {
		col = th.Pal.Text2
	}
	weight := gui.FontRegular
	if state == 1 {
		weight = gui.FontMedium
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(mark),
		layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
		layout.Flexed(1, func(gtx C) D {
			return gui.VStack(gtx, 0,
				gui.Text{Style: th.Body.Weighted(weight), Color: col, MaxLines: 1}.Widget(th, title),
				func(gtx C) D {
					if sub == "" {
						return D{}
					}
					return gui.Text{Style: th.Small, Color: th.Pal.RunningInk, MaxLines: 1}.Layout(gtx, th, sub)
				},
			)
		}),
	)
}

// failure explains a failed or stopped remake.
func (s *referenceCard) failure(gtx C, th *gui.Theme, job *studio.Job, ref studio.Reference) D {
	stopped := job.Status == studio.JobCancelled
	msg := strings.TrimSpace(job.Error)
	title := "Не вийшло"
	tone := gui.ToneError
	if stopped {
		title, tone = "Зупинено", gui.ToneNeutral
		msg = "Продовжимо з того ж місця."
		if ref.Stage == "analysed" {
			msg = "Розбір уже є — лишилось написати сценарій."
		}
	}
	key := strings.Contains(msg, "Google AI Studio") || strings.Contains(msg, "ключ")
	retry := "Спробувати ще раз"
	if stopped {
		retry = "Продовжити"
	}
	return gui.VStack(gtx, gui.Sp12,
		func(gtx C) D { return notice(gtx, th, tone, title, msg) },
		func(gtx C) D {
			ws := []layout.Widget{func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconRotateCw, Text: retry}.Layout(gtx, th, &s.retry)
			}}
			if key {
				ws = append(ws, func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconKeyRound, Text: "Налаштування"}.Layout(gtx, th, &s.settings)
				})
			}
			return gui.HStack(gtx, gui.Sp8, ws...)
		},
	)
}

// notice is a soft panel with a title and text inside the card.
func notice(gtx C, th *gui.Theme, tone gui.Tone, title, text string) D {
	_, soft, ink := th.Pal.ToneColors(tone)
	icon := gui.IconCircleAlert
	if tone == gui.ToneNeutral {
		soft, ink, icon = th.Pal.Surface, th.Pal.Text2, gui.IconCirclePause
	}
	pad := gtx.Dp(gui.Sp12 + 2)
	W := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(0, W-2*pad-gtx.Dp(28)), gtx.Constraints.Max.Y)}
	m := op.Record(gtx.Ops)
	d := gui.VStack(cg, 2,
		gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), Color: ink}.Widget(th, title),
		gui.Text{Style: th.Small.Sized(14), Color: th.Pal.Text}.Widget(th, text),
	)
	call := m.Stop()
	h := d.Size.Y + 2*pad
	gui.FillRRect(gtx, image.Rect(0, 0, W, h), gui.RadiusControl, soft)
	o := op.Offset(image.Pt(pad, pad+1)).Push(gtx.Ops)
	icon.Layout(gtx, 18, ink)
	o.Pop()
	o = op.Offset(image.Pt(pad+gtx.Dp(28), pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(W, h)}
}

// done shows the breakdown: the thumbnail and facts, the editable text and
// the actions.
func (s *referenceCard) done(gtx C, c *core.Ctx, pv *core.ProjectView, ref studio.Reference) D {
	th := c.Th
	narrow := gtx.Constraints.Max.X < gtx.Dp(560)
	thumb := func(gtx C) D {
		w := gtx.Dp(176)
		if narrow {
			w = min(gtx.Constraints.Max.X, gtx.Dp(260))
		}
		cg := gtx
		cg.Constraints = layout.Constraints{Max: image.Pt(w, gtx.Constraints.Max.Y)}
		path := ""
		if ref.Thumb != "" {
			path = c.E.Abs(pv.ID, ref.Thumb)
		}
		return s.thumb.Layout(cg, th, c.Img, path, ref.AnalyzedAt, gui.Thumb{Ratio: 16.0 / 9, Placeholder: gui.IconMonitorPlay, Radius: gui.RadiusControl})
	}
	facts := func(gtx C) D {
		var ws []layout.Widget
		if ref.Scenes > 0 {
			ws = append(ws, statPill(th, gui.IconClapperboard, core.Count(ref.Scenes, "сцена", "сцени", "сцен")+" в оригіналі"))
		}
		if ref.CutsPerMin > 0 {
			ws = append(ws, statPill(th, gui.IconScissors, strings.Replace(trimFloat(ref.CutsPerMin), ".", ",", 1)+" склейки/хв"))
		}
		if ref.Aspect != "" {
			ws = append(ws, statPill(th, gui.IconRatio, ref.Aspect))
		}
		if l := soundLabel(ref.AudioMode); l != "" {
			ws = append(ws, statPill(th, gui.IconAudioLines, l))
		}
		if l := peopleLabel(ref.People); l != "" {
			ws = append(ws, statPill(th, gui.IconUsers, l))
		}
		if ref.Via != "" {
			ws = append(ws, statPill(th, gui.IconEye, ref.Via))
		}
		return gui.VStack(gtx, gui.Sp8,
			func(gtx C) D { return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...) },
			gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th,
				"Сценарій нижче написано з цього розбору. Виправ розбір — і натисни «Переписати сценарій»: студія врахує твої правки."),
		)
	}
	top := func(gtx C) D {
		if narrow {
			return gui.VStack(gtx, gui.Sp12, thumb, facts)
		}
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(thumb),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Flexed(1, facts),
		)
	}
	text := func(gtx C) D {
		return s.text.Layout(gtx, th, gui.FieldStyle{MinLines: 8, MaxLines: 22, Style: th.Small.Sized(14).Lined(1.5)})
	}
	actions := func(gtx C) D {
		return gui.Flow{Gap: gui.Sp8}.Layout(gtx,
			func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconRefreshCw, Text: "Переписати сценарій"}.Layout(gtx, th, &s.rewrite)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconEye, Text: "Подивитися ще раз"}.Layout(gtx, th, &s.again)
			},
		)
	}
	return gui.VStack(gtx, gui.Sp16, top, text, actions)
}

func soundLabel(mode string) string {
	switch mode {
	case studio.AudioVoiceover:
		return "голос за кадром"
	case studio.AudioInVideo:
		return "говорять у кадрі"
	case studio.AudioSilent:
		return "без голосу"
	}
	return ""
}

func peopleLabel(p string) string {
	switch p {
	case studio.ContentCharacters:
		return "люди в кадрі"
	case studio.ContentNoPeople:
		return "без людей"
	case studio.ContentMotion:
		return "моушн-графіка"
	case studio.ContentMixed:
		return "мішаний кадр"
	}
	return ""
}

func trimFloat(x float64) string {
	s := sprintf("%.1f", x)
	s = strings.TrimSuffix(s, ".0")
	return s
}
