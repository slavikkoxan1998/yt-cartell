package settings

// «Автопілот» (the Telegram card lives in telegram.go).

import (
	"strconv"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
)

// ---- Автопілот ------------------------------------------------------------------------------

type autopilotCard struct {
	profile gui.Select
	cover   switchRow
	retries gui.Segmented
	failed  gui.Select // what the montage does with scenes that did not work out
	saved   savedNote
}

func (p *Page) autopilotCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.autopilot
	flash := func() { s.saved.want = true }
	if v, ok := s.profile.Changed(gtx); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.Autopilot.DefaultProfileID = v }, flash)
	}
	if v, ok := s.failed.Changed(gtx); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.Autopilot.OnFailed = v }, flash)
	}
	if v, ok := s.retries.Changed(gtx); ok {
		if n, err := strconv.Atoi(v); err == nil {
			p.cfg.set(c, func(cf *config.Config) { cf.Autopilot.Retries = n }, flash)
		}
	}
	cfg := p.now
	ap := cfg.Autopilot
	opts := []gui.SelectOption{{Value: "", Label: "Типові налаштування студії", Icon: gui.IconSparkles}}
	known := ap.DefaultProfileID == ""
	for _, pr := range c.E.Profiles() {
		opts = append(opts, gui.SelectOption{Value: pr.ID, Label: pr.Name, Hint: pr.Aspect + " · " + core.Duration(pr.TargetSeconds)})
		known = known || pr.ID == ap.DefaultProfileID
	}
	value := ap.DefaultProfileID
	if !known {
		value = ""
	}
	pipeline := func(gtx C) D {
		steps := []struct {
			icon *gui.Icon
			text string
		}{
			{gui.IconScrollText, "План"}, {gui.IconUsers, "Персонажі"}, {gui.IconImage, "Кадри"},
			{gui.IconFilm, "Відео й озвучка"}, {gui.IconScissors, "Монтаж"},
		}
		if ap.MakeCover {
			steps = append(steps, struct {
				icon *gui.Icon
				text string
			}{gui.IconImagePlus, "Обкладинка"})
		}
		var ws []layout.Widget
		for i, st := range steps {
			st := st
			if i > 0 {
				ws = append(ws, gui.IconChevronRight.Widget(16, th.Pal.Text3))
			}
			ws = append(ws, staticChip(th, st.icon, st.text))
		}
		return gui.Flow{Gap: 6, RowGap: gui.Sp8}.Layout(gtx, ws...)
	}
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconRocket, Title: "Автопілот", Big: true,
		Text:  "Вставляєш історію, тиснеш «Автопілот» — студія сама проходить усі кроки й монтує відео. Прогрес видно в черзі; вікно можна згорнути.",
		Trail: s.saved.widget(th),
	},
		pipeline,
		func(gtx C) D {
			return lines(gtx, th,
				func(gtx C) D {
					return line(gtx, th, "Профіль за замовчуванням", "Коли на головній профіль не вибрано.", 300, func(gtx C) D {
						return s.profile.Layout(gtx, th, gui.SelectStyle{Options: opts, Value: value, Leading: gui.IconLayers})
					})
				},
				func(gtx C) D {
					return s.cover.layout(gtx, th, ap.MakeCover, "Робити обкладинку для YouTube", "Після монтажу студія намалює прев'ю з назвою.", func(v bool) {
						p.cfg.set(c, func(cf *config.Config) { cf.Autopilot.MakeCover = v }, flash)
					})
				},
				func(gtx C) D {
					return line(gtx, th, "Повторні спроби", "Скільки разів ще пробувати кадри, кліпи й озвучку, що не вдалися.", 220, func(gtx C) D {
						s.retries.Full = true
						return s.retries.Layout(gtx, th, strconv.Itoa(ap.Retries), []gui.SegOption{
							{Value: "0", Label: "0"}, {Value: "1", Label: "1"}, {Value: "2", Label: "2"}, {Value: "3", Label: "3"},
						})
					})
				},
				func(gtx C) D {
					// Nothing is filled in silently: unless the user picks a rule
					// here, the autopilot stops before the montage and asks.
					return line(gtx, th, "Якщо частина сцен не вдалася", "Що робити з ними на монтажі. «Питати мене» — автопілот зупиниться перед монтажем і покаже, що не вийшло й чому.", 300, func(gtx C) D {
						return s.failed.Layout(gtx, th, gui.SelectStyle{Value: ap.OnFailed, Leading: gui.IconScissors, Options: []gui.SelectOption{
							{Value: config.FailedAsk, Label: "Питати мене"},
							{Value: config.FailedSkip, Label: "Змонтувати без них"},
							{Value: config.FailedStill, Label: "З кадром замість відео"},
							{Value: config.FailedStop, Label: "Зупинитись"},
						}})
					})
				},
			)
		},
	)
}
