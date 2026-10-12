package queue

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// nextStep is a project that waits for its next step.
type nextStep struct {
	s    studio.Summary
	step int    // the step to open
	text string // "Крок 3 з 5 — кадри чекають · готово 5 з 8"
	warn string // what went wrong last time ("7 відео з помилкою")
}

// nextSteps lists the projects (newest first) that are not running and not
// finished, with what waits in each.
func nextSteps(c *core.Ctx, list []studio.Summary) []nextStep {
	var out []nextStep
	for _, s := range list {
		if s.Running != "" {
			continue
		}
		pv := c.Project(s.ID)
		if pv == nil {
			continue
		}
		if n, ok := nextOf(&pv.P); ok {
			n.s = s
			out = append(out, n)
		}
	}
	return out
}

// nextOf reads where a project stands: the first step with work left (the
// story, the plan, the frames, the clips and voices, the montage) and what
// failed there; ok is false for a finished film.
func nextOf(p *studio.Project) (n nextStep, ok bool) {
	scenes := len(p.Scenes)
	var frames, videos, voices, talk, errFrames, errVideos, errVoices int
	for _, s := range p.Scenes {
		if frameOf(s).Status == studio.StatusDone {
			frames++
		}
		if s.Video.Status == studio.StatusDone {
			videos++
		}
		if p.Input.Voiceover && spoken(s) {
			talk++
			if s.Audio.Status == studio.StatusDone {
				voices++
			}
		}
		errFrames += errOf(frameOf(s))
		errVideos += errOf(s.Video)
		errVoices += errOf(s.Audio)
	}
	out := p.Montage.Output
	switch {
	case scenes == 0 && strings.TrimSpace(p.Input.Text) == "" && p.Input.VoiceFile == "":
		n = nextStep{step: 1, text: "Крок 1 з 5 — історія чекає на текст"}
	case scenes == 0:
		n = nextStep{step: 2, text: "Крок 2 з 5 — план ще не створено"}
	case frames < scenes:
		n = nextStep{step: 3, text: fmt.Sprintf("Крок 3 з 5 — кадри чекають · готово %d з %d", frames, scenes)}
		if errFrames > 0 {
			n.warn = core.Count(errFrames, "кадр", "кадри", "кадрів") + " з помилкою"
		}
	case videos < scenes:
		n = nextStep{step: 4, text: fmt.Sprintf("Крок 4 з 5 — відео чекають · готово %d з %d", videos, scenes)}
		n.warn = failedClips(errVideos, errVoices)
	case voices < talk:
		n = nextStep{step: 4, text: fmt.Sprintf("Крок 4 з 5 — озвучка чекає · готово %d з %d", voices, talk)}
		n.warn = failedClips(0, errVoices)
	case out.Status != studio.StatusDone || out.Path == "":
		n = nextStep{step: 5, text: "Крок 5 з 5 — лишився монтаж, і відео готове"}
	case out.Stale:
		n = nextStep{step: 5, text: "Крок 5 з 5 — сцени змінено, перезбери фільм"}
	default:
		return nextStep{}, false
	}
	if j := p.Job; j != nil && j.Status == studio.JobError {
		n.warn = "Не вдалося: " + strings.ToLower(core.JobLabel(j.Kind)) + " — спробуй ще раз"
	}
	return n, true
}

func errOf(a studio.Asset) int {
	if a.Status == studio.StatusError {
		return 1
	}
	return 0
}

func failedClips(videos, voices int) string {
	var parts []string
	if videos > 0 {
		parts = append(parts, fmt.Sprintf("%d відео", videos))
	}
	if voices > 0 {
		parts = append(parts, core.Count(voices, "озвучка", "озвучки", "озвучок"))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " й ") + " з помилкою"
}

// ---- «Що можна запустити» ------------------------------------------------------------------------

// nextCard lists the projects that wait for their next step, each with
// «Продовжити». full (the idle column) opens with "nothing generates" and
// ends with the quick start, which takes the card's spare height; else a
// «Нова історія» button closes the list.
func (v *View) nextCard(gtx C, c *core.Ctx, d pageData, full bool) D {
	th := c.Th
	return gui.Card{Padding: cardPad(gtx)}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		minH := gtx.Constraints.Min.Y
		gtx.Constraints.Min.Y = 0
		limit := 5
		if gtx.Constraints.Max.X < gtx.Dp(520) {
			limit = 3
		}
		rows := d.next[:min(len(d.next), limit)]
		var ws []layout.Widget
		if full && !d.running() {
			ws = append(ws, func(gtx C) D { return idleLine(gtx, th, len(d.next) > 0, len(rows) > 0) })
		}
		for i, n := range rows {
			n, last := n, i == len(rows)-1
			ws = append(ws, func(gtx C) D { return v.projRow(gtx, c, n, !last) })
		}
		if len(d.next) > len(rows) {
			ws = append(ws, func(gtx C) D {
				return layout.Inset{Top: gui.Sp8}.Layout(gtx, func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Text: fmt.Sprintf("Усі проєкти · %d", len(d.next)),
						Trail: gui.IconChevronRight}.Layout(gtx, th, &v.allProjects)
				})
			})
		}
		if !full {
			ws = append(ws, func(gtx C) D {
				return layout.Inset{Top: gui.Sp12}.Layout(gtx, func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconPlus, Text: "Нова історія"}.Layout(gtx, th, &v.newStory)
				})
			})
			return gui.VStack(gtx, 0, ws...)
		}
		ld, lc := record(gtx, func(gtx C) D { return gui.VStack(gtx, 0, ws...) })
		lc.Add(gtx.Ops)
		top := ld.Size.Y
		if top > 0 {
			top += gtx.Dp(gui.Sp16)
		}
		qg := gtx
		qg.Constraints.Min.Y = max(0, minH-top)
		defer op.Offset(image.Pt(0, top)).Push(gtx.Ops).Pop()
		qd := v.quickStart(qg, c)
		return D{Size: image.Pt(gtx.Constraints.Max.X, top+qd.Size.Y)}
	})
}

// idleLine opens the idle column: nothing generates, and what to do.
func idleLine(gtx C, th *gui.Theme, waiting, divider bool) D {
	meta := "Усі проєкти завершені — саме час для нової історії."
	if waiting {
		meta = "Ось проєкти, що чекають на наступний крок. Запусти будь-який — прогрес з'явиться тут."
	}
	d := layout.Inset{Top: gui.Sp4, Bottom: gui.Sp16}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				sz := gtx.Dp(44)
				gui.FillCircle(gtx, 44, th.Pal.Surface)
				ip := gtx.Dp(20)
				defer op.Offset(image.Pt((sz-ip)/2, (sz-ip)/2)).Push(gtx.Ops).Pop()
				gui.IconCoffee.Layout(gtx, 20, th.Pal.Text)
				return D{Size: image.Pt(sz, sz)}
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return gui.VStack(gtx, 2,
					gui.Text{Style: th.Body.Weighted(gui.FontMedium), MaxLines: 1}.Widget(th, "Зараз нічого не генерується"),
					gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 3}.Widget(th, meta),
				)
			}),
		)
	})
	if divider {
		px := max(1, gtx.Dp(1))
		gui.FillRect(gtx, image.Rect(0, d.Size.Y-px, d.Size.X, d.Size.Y), th.Pal.Border)
	}
	return d
}

// projRow is one project of «Що можна запустити»: its picture and name,
// the step that waits and how far the film is, «Продовжити» (under the
// text in narrow cards).
func (v *View) projRow(gtx C, c *core.Ctx, n nextStep, divider bool) D {
	th := c.Th
	ps := v.proj(n.s.ID)
	if ps.open.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: n.s.ID, Step: n.step})
	}
	w := gtx.Constraints.Max.X
	small := w < gtx.Dp(440)
	stack := w < gtx.Dp(520)
	tw := gtx.Dp(120)
	if small {
		tw = gtx.Dp(96)
	}
	title := strings.TrimSpace(n.s.Title)
	if title == "" {
		title = "Без назви"
	}
	updated := "Оновлено " + core.Updated(n.s.UpdatedAt)
	btn := func(gtx C) D {
		return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Text: "Продовжити", Trail: gui.IconChevronRight,
			Tip: fmt.Sprintf("Відкрити крок %d", n.step)}.Layout(gtx, th, &ps.open)
	}
	body := func(gtx C) D {
		ch := []layout.FlexChild{
			layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium), MaxLines: 1}.Widget(th, title)),
			layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, n.text)),
		}
		if n.warn != "" {
			ch = append(ch, layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.WarningInk, MaxLines: 2}.Widget(th, n.warn)))
		}
		ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout), layout.Rigid(func(gtx C) D {
			if stack {
				// No room beside the steps: the time goes under them.
				return gui.VStack(gtx, gui.Sp8,
					func(gtx C) D { return stepBar(gtx, th, n.step) },
					gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, updated))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return stepBar(gtx, th, n.step) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Flexed(1, gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, updated)),
			)
		}))
		if stack {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout), layout.Rigid(btn))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	}
	d := layout.Inset{Top: gui.Sp12, Bottom: gui.Sp12}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		ch := []layout.FlexChild{
			layout.Rigid(func(gtx C) D { return thumbBox(gtx, c, &ps.img, n.s.ThumbPath, gui.IconClapperboard, small) }),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Flexed(1, body),
		}
		align := layout.Start
		if !stack {
			ch = append(ch, layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout), layout.Rigid(btn))
			align = layout.Middle
		}
		return layout.Flex{Alignment: align}.Layout(gtx, ch...)
	})
	if divider {
		px := max(1, gtx.Dp(1))
		gui.FillRect(gtx, image.Rect(tw+gtx.Dp(16), d.Size.Y-px, d.Size.X, d.Size.Y), th.Pal.Border)
	}
	return d
}

// stepBar shows the five steps as segments: the ones behind green, the
// waiting one dark, the rest empty.
func stepBar(gtx C, th *gui.Theme, step int) D {
	seg, h, gap := gtx.Dp(20), gtx.Dp(6), gtx.Dp(4)
	for i := 1; i <= 5; i++ {
		col := th.Pal.Track
		switch {
		case i < step:
			col = th.Pal.Success
		case i == step:
			col = th.Pal.Text3
		}
		x := (i - 1) * (seg + gap)
		gui.FillRRect(gtx, image.Rect(x, 0, x+seg, h), 3, col)
	}
	return D{Size: image.Pt(5*seg+4*gap, h)}
}

// ---- quick start -----------------------------------------------------------------------------------

// filmSteps are the five steps of a film, as the quick start tells them.
var filmSteps = [...]struct {
	icon        *gui.Icon
	title, text string
}{
	{gui.IconPenLine, "Історія", "Встав текст або ідею, обери мову й тривалість."},
	{gui.IconListOrdered, "План", "Студія розпише персонажів, локації й сцени."},
	{gui.IconImages, "Кадри", "Для кожної сцени — перший кадр у твоєму стилі."},
	{gui.IconFilm, "Відео й озвучка", "Кадри оживають, а оповідач читає текст."},
	{gui.IconScissors, "Монтаж", "Студія збирає фільм — з переходами й субтитрами."},
}

// quickStart invites a new story on a warm panel and centres itself in the
// height it is given: with room for it, the five steps of a film get a line
// each, else they are small labels.
func (v *View) quickStart(gtx C, c *core.Ctx) D {
	th := c.Th
	pad := gtx.Dp(gui.Sp24)
	list := false
	if avail := gtx.Constraints.Min.Y - 2*pad; avail > 0 {
		cg := gtx
		cg.Constraints = layout.Constraints{Max: image.Pt(max(0, gtx.Constraints.Max.X-2*pad), gtx.Constraints.Max.Y)}
		ld, _ := record(cg, func(gtx C) D { return v.quickContent(gtx, c, false, true) })
		list = ld.Size.Y <= avail
	}
	return gui.Card{Tint: true, Radius: gui.RadiusInner, Padding: gui.Sp24}.Layout(gtx, th, func(gtx C) D {
		return layout.Center.Layout(gtx, func(gtx C) D {
			return v.quickContent(gtx, c, false, list)
		})
	})
}

// quickContent: an icon, a title, the five steps of a film (a line each
// with list, else small labels) and «Нова історія». big is the welcome
// card's version.
func (v *View) quickContent(gtx C, c *core.Ctx, big, list bool) D {
	th := c.Th
	title, body := "Нова історія — від тексту до відео",
		"Встав текст або ідею — автопілот сам пройде всі п'ять кроків, а прогрес кожної сцени з'явиться тут."
	ts, icon := th.H3, unit.Dp(64)
	if big {
		title, body = "Тут з'явиться все, що генерується",
			"Кадри, відео й озвучка з усіх проєктів — з прогресом кожної сцени. Почни першу історію, а студія зробить решту."
		ts, icon = th.H2, 88
	}
	maxW := min(gtx.Constraints.Max.X, gtx.Dp(560))
	fill := th.Pal.Card
	if big {
		fill = th.Pal.Surface
	}
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D { return iconDisc(gtx, th, gui.IconWandSparkles, icon) }),
		layout.Rigid(layout.Spacer{Height: gui.Sp16}.Layout),
		layout.Rigid(func(gtx C) D {
			gtx.Constraints.Max.X = maxW
			return gui.Text{Style: ts, Align: text.Middle}.Layout(gtx, th, title)
		}),
		layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
		layout.Rigid(func(gtx C) D {
			gtx.Constraints.Max.X = maxW
			return gui.Text{Style: th.Body, Color: th.Pal.Text2, Align: text.Middle}.Layout(gtx, th, body)
		}),
		layout.Rigid(layout.Spacer{Height: gui.Sp24}.Layout),
		layout.Rigid(func(gtx C) D {
			if list {
				gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(440))
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return stepList(gtx, th, fill)
			}
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(680))
			return stepLabels(gtx, th, fill)
		}),
		layout.Rigid(layout.Spacer{Height: gui.Sp24}.Layout),
		layout.Rigid(func(gtx C) D {
			return gui.Button{Icon: gui.IconPlus, Text: "Нова історія"}.Layout(gtx, th, &v.newStory)
		}),
	)
}

// stepList tells the five steps a line each, joined like a path.
func stepList(gtx C, th *gui.Theme, fill color.NRGBA) D {
	disc, gap := gtx.Dp(36), gtx.Dp(gui.Sp12)
	ws := make([]layout.Widget, len(filmSteps))
	for i, s := range filmSteps {
		i, s := i, s
		ws[i] = func(gtx C) D {
			d := layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					gui.FillCircle(gtx, 36, fill)
					ip := gtx.Dp(18)
					defer op.Offset(image.Pt((disc-ip)/2, (disc-ip)/2)).Push(gtx.Ops).Pop()
					s.icon.Layout(gtx, 18, th.Pal.Text2)
					return D{Size: image.Pt(disc, disc)}
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return gui.VStack(gtx, 0,
						gui.Text{Style: th.Body.Weighted(gui.FontMedium), MaxLines: 1}.Widget(th, fmt.Sprintf("%d · %s", i+1, s.title)),
						gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, s.text),
					)
				}),
			)
			if i < len(filmSteps)-1 {
				// The path to the next step's disc.
				lw := max(1, gtx.Dp(2))
				top := (d.Size.Y+disc)/2 + gtx.Dp(4)
				gui.FillRect(gtx, image.Rect((disc-lw)/2, top, (disc+lw)/2, d.Size.Y+gap+(d.Size.Y-disc)/2-gtx.Dp(4)), fill)
			}
			return d
		}
	}
	return gui.VStack(gtx, gui.Sp12, ws...)
}

// stepLabels shows the five steps as small labels.
func stepLabels(gtx C, th *gui.Theme, fill color.NRGBA) D {
	ws := make([]layout.Widget, len(filmSteps))
	for i, s := range filmSteps {
		i, s := i, s
		ws[i] = func(gtx C) D {
			lg := gtx
			lg.Constraints.Min = image.Point{}
			ld, lc := record(lg, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.Caption.Weighted(gui.FontSemiBold), Color: th.Pal.Text3, MaxLines: 1}.Widget(th, fmt.Sprint(i+1))),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Rigid(func(gtx C) D {
						sz := gtx.Dp(16)
						s.icon.Layout(gtx, 16, th.Pal.Text2)
						return D{Size: image.Pt(sz, sz)}
					}),
					layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
					layout.Rigid(gui.Text{Style: th.Small.Weighted(gui.FontMedium), MaxLines: 1}.Widget(th, s.title)),
				)
			})
			px, py := gtx.Dp(12), gtx.Dp(6)
			size := image.Pt(ld.Size.X+2*px, ld.Size.Y+2*py)
			gui.FillRRect(gtx, image.Rectangle{Max: size}, gui.RadiusPill, fill)
			defer op.Offset(image.Pt(px, py)).Push(gtx.Ops).Pop()
			lc.Add(gtx.Ops)
			return D{Size: size}
		}
	}
	return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
}

// iconDisc is a soft action-tinted disc with an icon (the kit's empty
// state look).
func iconDisc(gtx C, th *gui.Theme, ic *gui.Icon, size unit.Dp) D {
	outer := gtx.Dp(size)
	inner := outer * 3 / 4
	gui.FillCircle(gtx, size, gui.WithAlpha(th.Pal.ActionSoft, 0.5))
	func() {
		o := (outer - inner) / 2
		defer op.Offset(image.Pt(o, o)).Push(gtx.Ops).Pop()
		gui.FillCircle(gtx, unit.Dp(float32(inner)/gtx.Metric.PxPerDp), th.Pal.ActionSoft)
	}()
	is := size * 3 / 8
	ip := gtx.Dp(is)
	defer op.Offset(image.Pt((outer-ip)/2, (outer-ip)/2)).Push(gtx.Ops).Pop()
	ic.Layout(gtx, is, th.Pal.ActionText)
	return D{Size: image.Pt(outer, outer)}
}

// ---- welcome ------------------------------------------------------------------------------------------

// welcome is the page before anything ever ran: one card down to the
// bottom of the window with the quick start and how the queue works.
func (v *View) welcome(gtx C, c *core.Ctx, minH int) D {
	th := c.Th
	gtx.Constraints.Min.Y = min(max(gtx.Constraints.Min.Y, minH), gtx.Constraints.Max.Y)
	pad := cardPad(gtx)
	content := func(list bool) layout.Widget {
		return func(gtx C) D {
			return layout.Inset{Top: gui.Sp24, Bottom: gui.Sp24}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return v.quickContent(gtx, c, true, list) }),
					layout.Rigid(layout.Spacer{Height: gui.Sp40}.Layout),
					layout.Rigid(func(gtx C) D { return points(gtx, th) }),
				)
			})
		}
	}
	// The steps get a line each when the window is tall enough for them.
	list := false
	if avail := gtx.Constraints.Min.Y - 2*gtx.Dp(pad); avail > 0 {
		cg := gtx
		cg.Constraints = layout.Constraints{Max: image.Pt(max(0, gtx.Constraints.Max.X-2*gtx.Dp(pad)), gtx.Constraints.Max.Y)}
		ld, _ := record(cg, content(true))
		list = ld.Size.Y <= avail
	}
	return gui.Card{Padding: pad}.Layout(gtx, th, func(gtx C) D {
		return layout.Center.Layout(gtx, content(list))
	})
}

// points: how the queue works, in three short facts.
func points(gtx C, th *gui.Theme) D {
	type pt struct {
		icon        *gui.Icon
		title, text string
	}
	all := [...]pt{
		{gui.IconLayers, "Працює у фоні", "Проєкт можна закрити або взятися за інший — генерація триває."},
		{gui.IconImages, "Видно кожну сцену", "Що вже готово, що генерується, а що чекає в черзі."},
		{gui.IconRotateCcw, "Помилки не губляться", "«Повторити» перезапустить лише те, що не вдалося."},
	}
	ws := make([]layout.Widget, len(all))
	for i, p := range all {
		p := p
		ws[i] = func(gtx C) D {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					sz := gtx.Dp(40)
					gui.FillCircle(gtx, 40, th.Pal.Surface)
					ip := gtx.Dp(20)
					defer op.Offset(image.Pt((sz-ip)/2, (sz-ip)/2)).Push(gtx.Ops).Pop()
					p.icon.Layout(gtx, 20, th.Pal.Text)
					return D{Size: image.Pt(sz, sz)}
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return gui.VStack(gtx, 2,
						gui.Text{Style: th.Body.Weighted(gui.FontMedium), MaxLines: 1}.Widget(th, p.title),
						gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 3}.Widget(th, p.text),
					)
				}),
			)
		}
	}
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(960))
	if gtx.Constraints.Max.X < gtx.Dp(720) {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		return gui.VStack(gtx, gui.Sp16, ws...)
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return gui.Columns{Gap: gui.Sp32}.Layout(gtx, gui.Col{Span: 1, W: ws[0]}, gui.Col{Span: 1, W: ws[1]}, gui.Col{Span: 1, W: ws[2]})
}
