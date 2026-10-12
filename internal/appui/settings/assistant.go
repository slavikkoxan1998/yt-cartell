package settings

// «Помічник підключення» — the first card of Підключення (route section
// "assistant"; Home's set-up call-out leads here). The user pastes anything
// (addresses, keys, docs links, notes); Engine.AnalyzeConnections finds out
// what each item is, checks a proposed plan — Текст → X, Кадри → Y, Відео → Z,
// Озвучка → W, Розпізнавання → V — and «Застосувати» writes the steps left
// on after a backup of the settings (Engine.ApplyAssist). Keys are shown
// masked; the pasted text is never saved.

import (
	"context"
	"errors"
	"image"
	"image/color"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

type assistCard struct {
	input field // multi-line; never committed (no commit func)

	run, stop, apply, discard, clear, undo gui.Clickable

	busy    bool
	step    string
	cancel  context.CancelFunc
	seq     int
	err     string
	plan    *studio.AssistPlan
	toggles []gui.Toggle

	applying bool
	applied  string         // after «Застосувати»: what changed
	backup   *config.Backup // the copy made before it
	show     gui.Anim
}

// assistRoleIcon is the icon of a plan step.
func assistRoleIcon(role string) *gui.Icon {
	switch role {
	case studio.RoleText:
		return gui.IconScrollText
	case studio.RoleImage:
		return gui.IconImage
	case studio.RoleVideo:
		return gui.IconFilm
	case studio.RoleTTS:
		return gui.IconMic
	case studio.RoleSTT:
		return gui.IconAudioLines
	}
	return gui.IconPlug
}

func (p *Page) assistCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.assist
	if _, ok := s.input.Changed(gtx); ok {
		s.err = ""
	}
	if s.run.Clicked(gtx) {
		p.assistRun(c)
	}
	if s.stop.Clicked(gtx) && s.cancel != nil {
		s.cancel()
		s.step = "Скасовуємо…"
	}
	if s.clear.Clicked(gtx) {
		s.input.SetText("")
		s.plan, s.err, s.applied, s.backup = nil, "", "", nil
	}
	if s.discard.Clicked(gtx) {
		s.plan, s.applied = nil, ""
	}
	if s.apply.Clicked(gtx) {
		p.assistApply(c)
	}
	if s.undo.Clicked(gtx) && s.backup != nil {
		p.askRestore(c, *s.backup)
	}
	if s.plan != nil {
		if len(s.toggles) < len(s.plan.Steps) {
			s.toggles = make([]gui.Toggle, len(s.plan.Steps))
		}
		for i, st := range s.plan.Steps {
			if v, ok := s.toggles[i].Changed(gtx); ok {
				st.On = v
			}
		}
	}

	input := func(gtx C) D {
		return s.input.Layout(gtx, th, gui.FieldStyle{MinLines: 3, MaxLines: 10, Leading: gui.IconClipboardPaste, Disabled: s.busy,
			Placeholder: "Напр.: ось мій ендпоінт https://… ключ sk-… модель gemini-3.8-flash; озвучка ElevenLabs sk_…; документація відео https://…/docs"})
	}
	act := func(gtx C) D {
		if s.busy {
			step := func(gtx C) D {
				return layout.Flex{Alignment: layout.Start}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return layout.Inset{Top: 1}.Layout(gtx, gui.Spinner{Size: 18, Color: th.Pal.RunningInk}.Widget(th))
					}),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text, MaxLines: 3}.Widget(th, firstNonEmptyStr(s.step, "Працюємо…"))),
				)
			}
			stop := func(gtx C) D {
				gtx.Constraints.Min.X = 0
				return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconX, Text: "Скасувати"}.Layout(gtx, th, &s.stop)
			}
			if gtx.Constraints.Max.X < gtx.Dp(420) {
				return gui.VStack(gtx, gui.Sp8, step, stop)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, step),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Rigid(stop),
			)
		}
		var clear layout.Widget
		if strings.TrimSpace(s.input.Editor.Text()) != "" || s.plan != nil {
			clear = func(gtx C) D {
				return gui.Button{Kind: gui.Ghost, Icon: gui.IconTrash2, Text: "Очистити"}.Layout(gtx, th, &s.clear)
			}
		}
		return actions(func(gtx C) D {
			kind := gui.Primary
			if s.plan != nil {
				kind = gui.Secondary // «Застосувати» below is the next step
			}
			return gui.Button{Kind: kind, Icon: gui.IconWandSparkles, Text: "Розібрати й перевірити"}.Layout(gtx, th, &s.run)
		}, clear)(gtx)
	}
	body := []layout.Widget{input, act}
	if s.err != "" {
		body = append(body, func(gtx C) D { return resultPanel(gtx, th, false, false, s.err, "", "") })
	}
	if s.applied != "" {
		body = append(body, func(gtx C) D {
			return resultPanel(gtx, th, false, true, s.applied, "", "Застосовано")
		})
		if s.backup != nil {
			body = append(body, func(gtx C) D {
				return note(gtx, th, gui.IconHistory, "Перед змінами студія зберегла копію налаштувань ("+fullDate(s.backup.Time)+").", func(gtx C) D {
					gtx.Constraints.Min.X = 0
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconRotateCcw, Text: "Повернути як було"}.Layout(gtx, th, &s.undo)
				})
			})
		}
	}
	if s.plan != nil && !s.busy {
		s.show.Duration, s.show.Ease = gui.DurDialog, gui.EaseOutCubic
		a := s.show.Animate(gtx, 1)
		body = append(body, func(gtx C) D { return gui.FadeUp(gtx, a, 6, func(gtx C) D { return p.assistPlan(gtx, c) }) })
	}
	var status layout.Widget
	switch {
	case s.busy:
		status = gui.StatusPill{State: gui.StateRunning, Percent: -1, Text: "Перевіряємо", Small: true}.Widget(th)
	case s.plan != nil && len(s.plan.Steps) > 0:
		ok := 0
		for _, st := range s.plan.Steps {
			if st.Status == "ok" {
				ok++
			}
		}
		state, text := gui.StateDone, "Усе працює"
		if ok < len(s.plan.Steps) {
			state, text = gui.StateStale, core.Count(ok, "працює", "працюють", "працюють")+" з "+itoa(len(s.plan.Steps))
		}
		status = gui.StatusPill{State: state, Text: text, Small: true}.Widget(th)
	}
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconWandSparkles, Title: "Помічник підключення",
		Text: "Встав усе, що маєш: адреси, ключі, посилання на документацію чи просто нотатки. Помічник розбереться, що це, " +
			"перевірить і запропонує, що куди підключити. Ключі бачать лише самі сервіси — ШІ отримує їх замаскованими.",
		Trail: status,
	}, body...)
}

// assistPlan draws what was found, the checked steps and the notes.
func (p *Page) assistPlan(gtx C, c *core.Ctx) D {
	th := c.Th
	s := &p.assist
	plan := s.plan
	var ws []layout.Widget
	// What was found.
	if len(plan.Items) > 0 {
		var chips []layout.Widget
		for _, it := range plan.Items {
			icon, text := gui.IconLink, it.Display
			switch it.Kind {
			case "key":
				icon = gui.IconKeyRound
			case "docs":
				icon = gui.IconFileText
			case "model":
				icon = gui.IconSparkles
			case "voice":
				icon = gui.IconMic
			}
			if it.Service != "" {
				text += " · " + it.Service
			}
			chips = append(chips, staticChip(th, icon, clipStr(text, 60)))
		}
		ws = append(ws, func(gtx C) D {
			return gui.VStack(gtx, gui.Sp8,
				gui.Text{Style: th.Label}.Widget(th, "Знайдено"),
				func(gtx C) D { return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, chips...) },
			)
		})
	}
	if plan.AI != "" {
		ws = append(ws, func(gtx C) D { return hintLine(gtx, th, gui.IconSparkles, plan.AI) })
	}
	if len(plan.Steps) > 0 {
		ws = append(ws, gui.Text{Style: th.Label}.Widget(th, "План"))
		for i, st := range plan.Steps {
			i, st := i, st
			ws = append(ws, func(gtx C) D { return p.assistStep(gtx, c, i, st) })
		}
	}
	if len(plan.Notes) > 0 {
		text := ""
		for i, n := range plan.Notes {
			if i > 0 {
				text += "\n"
			}
			text += "• " + n
		}
		ws = append(ws, func(gtx C) D { return note(gtx, th, gui.IconInfo, text, nil) })
	}
	if len(plan.Steps) > 0 {
		on := len(plan.AssistOn())
		ws = append(ws, func(gtx C) D {
			return gui.VStack(gtx, gui.Sp8,
				actions(
					func(gtx C) D {
						t := "Застосувати"
						if on > 0 {
							t += " (" + itoa(on) + ")"
						}
						return gui.Button{Kind: gui.Primary, Icon: gui.IconCheck, Text: t, Busy: s.applying, Disabled: on == 0}.Layout(gtx, th, &s.apply)
					},
					func(gtx C) D {
						return gui.Button{Kind: gui.Ghost, Icon: gui.IconX, Text: "Відкинути"}.Layout(gtx, th, &s.discard)
					},
				),
				gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, "Перед змінами студія збереже копію налаштувань — повернути можна одним натиском."),
			)
		})
	}
	return gui.VStack(gtx, gui.Sp12, ws...)
}

// assistStep is one plan row: the role, where it goes, the check, the switch.
func (p *Page) assistStep(gtx C, c *core.Ctx, i int, st *studio.AssistStep) D {
	th := c.Th
	s := &p.assist
	var pill layout.Widget
	switch st.Status {
	case "ok":
		pill = gui.StatusPill{State: gui.StateDone, Text: "Працює", Small: true}.Widget(th)
	case "fail":
		pill = gui.StatusPill{State: gui.StateError, Text: "Не працює", Small: true}.Widget(th)
	default:
		pill = gui.StatusPill{State: gui.StateEmpty, Text: "Не перевірено", Small: true}.Widget(th)
	}
	head := func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				sz := gtx.Dp(32)
				gui.FillRRect(gtx, image.Rect(0, 0, sz, sz), gui.RadiusSmall+2, th.Pal.Card)
				is := gtx.Dp(16)
				o := op.Offset(image.Pt((sz-is)/2, (sz-is)/2)).Push(gtx.Ops)
				assistRoleIcon(st.Role).Layout(gtx, 16, th.Pal.Text)
				o.Pop()
				return D{Size: image.Pt(sz, sz)}
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return gui.VStack(gtx, 2,
					gui.Text{Style: th.Body.Weighted(gui.FontSemiBold), MaxLines: 1}.Widget(th, studio.AssistRoleLabel(st.Role)+" → "+st.Target),
					func(gtx C) D { gtx.Constraints.Min.X = 0; return pill(gtx) },
				)
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Rigid(func(gtx C) D { return s.toggles[i].Layout(gtx, th, st.On, "") }),
		)
	}
	ws := []layout.Widget{head}
	if len(st.Lines) > 0 {
		ws = append(ws, gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, strings.Join(st.Lines, " · ")))
	}
	if st.Msg != "" {
		col := th.Pal.SuccessInk
		if st.Status == "fail" {
			col = th.Pal.ErrorInk
		}
		ws = append(ws, msgBlock(th, st.Msg, col))
	}
	return inset(gtx, th, func(gtx C) D { return gui.VStack(gtx, gui.Sp8, ws...) })
}

// msgBlock is a check's message: the first line in col, the request lines quieter.
func msgBlock(th *gui.Theme, msg string, col color.NRGBA) layout.Widget {
	first, rest, _ := strings.Cut(msg, "\n")
	return func(gtx C) D {
		ws := []layout.Widget{gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: col}.Widget(th, first)}
		if rest = strings.TrimSpace(rest); rest != "" {
			ws = append(ws, gui.Text{Style: th.Small.Sized(12), Color: th.Pal.Text2}.Widget(th, rest))
		}
		return gui.VStack(gtx, 2, ws...)
	}
}

func clipStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// assistRun analyses the pasted text in the background (progress, cancel).
func (p *Page) assistRun(c *core.Ctx) {
	s := &p.assist
	if s.busy {
		return
	}
	text := strings.TrimSpace(s.input.Editor.Text())
	switch {
	case text == "":
		s.err = "Встав сюди адреси, ключі, посилання на документацію або просто нотатки — помічник розбереться."
		return
	case c.Offline:
		s.err = "Помічник недоступний у демо-режимі."
		return
	}
	p.flushFields()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	s.busy, s.err, s.step, s.cancel, s.applied, s.backup = true, "", "Починаємо…", cancel, "", nil
	s.seq++
	seq := s.seq
	p.cfg.barrier(c, func() {
		c.Background(func() {
			defer cancel()
			plan, err := c.E.AnalyzeConnections(ctx, text, func(msg string) {
				c.Post(func() {
					if seq == s.seq && s.busy {
						s.step = msg
					}
				})
			})
			c.Post(func() {
				if seq != s.seq {
					return
				}
				s.busy, s.cancel, s.step = false, nil, ""
				switch {
				case errors.Is(err, context.Canceled):
					c.Toast(gui.ToneNeutral, "Помічника зупинено")
				case errors.Is(err, context.DeadlineExceeded):
					s.err = "Помічник не встиг за 10 хвилин — спробуй вставити менше за раз."
				case err != nil:
					s.err = core.ErrorText(err)
				}
				if plan != nil && (err == nil || len(plan.Items) > 0) {
					s.plan = plan
					s.toggles = make([]gui.Toggle, len(plan.Steps))
					s.show.Set(0)
				}
			})
		})
	})
}

// assistApply writes the steps left on (after a backup).
func (p *Page) assistApply(c *core.Ctx) {
	s := &p.assist
	if s.applying || s.plan == nil {
		return
	}
	plan := s.plan
	s.applying = true
	p.cfg.barrier(c, func() {
		c.Background(func() {
			res, err := c.E.ApplyAssist(plan)
			c.Post(func() {
				s.applying = false
				if err != nil {
					s.err = core.ErrorText(err)
					return
				}
				s.plan = nil
				s.applied = "Підключено: " + strings.Join(res.Applied, ", ") + ". Натисни «Перевірити» в картках, щоб переконатися ще раз."
				if !res.Backup.Time.IsZero() {
					b := res.Backup
					s.backup = &b
				}
				p.data.io.backupsOK = false // the Data card shows the new copy
				for _, k := range []*check{&p.text.check, &p.tts.check, &p.stt.check, &p.frames.check, &p.video.check} {
					k.reset()
				}
				c.ReloadVoices()
				c.Toast(gui.ToneSuccess, "Застосовано: "+strings.Join(res.Applied, ", "))
			})
		})
	})
}
