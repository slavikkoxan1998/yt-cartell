package main

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/gui"
)

var languages = []gui.SelectOption{
	{Value: "auto", Label: "Авто — як у тексті", Icon: gui.IconSparkles},
	{Value: "uk", Label: "Українська", Hint: "uk"},
	{Value: "en", Label: "Англійська", Hint: "en"},
	{Value: "pl", Label: "Польська", Hint: "pl"},
	{Value: "de", Label: "Німецька", Hint: "de"},
	{Value: "fr", Label: "Французька", Hint: "fr"},
	{Value: "es", Label: "Іспанська", Hint: "es"},
	{Value: "it", Label: "Італійська", Hint: "it"},
	{Value: "pt", Label: "Португальська", Hint: "pt"},
	{Value: "ja", Label: "Японська", Hint: "ja"},
	{Value: "ko", Label: "Корейська", Hint: "ko"},
	{Value: "tr", Label: "Турецька", Hint: "tr"},
}

var voices = []gui.SelectOption{
	{Value: "olena", Label: "Олена", Hint: "тепла, спокійна"},
	{Value: "taras", Label: "Тарас", Hint: "глибокий, казковий"},
	{Value: "mira", Label: "Міра", Hint: "дзвінка, швидка"},
	{Value: "bohdan", Label: "Богдан", Hint: "преміум", Disabled: true},
}

var durations = []gui.SelectOption{
	{Value: "15", Label: "15 секунд"}, {Value: "30", Label: "30 секунд"}, {Value: "60", Label: "1 хвилина"},
	{Value: "180", Label: "3 хвилини"}, {Value: "600", Label: "10 хвилин"},
}

func (g *gallery) inputs(gtx C) D {
	th := g.th
	d := g.pageFrame(gtx, "Поля введення", "Текстові поля, область тексту, пошук і випадні списки з пошуком та розгортанням угору.", func(gtx C) D {
		return gui.Columns{}.Layout(gtx,
			gui.Col{Span: 6, W: g.section("Текстові поля", "стани: порожнє · заповнене · фокус · помилка · вимкнене", g.fields)},
			gui.Col{Span: 6, W: func(gtx C) D {
				return gui.VStack(gtx, gui.Sp24,
					g.section("Текст і пошук", "TextArea росте з текстом · SearchField", g.textAreas),
					g.section("Випадні списки", "Select: клавіші ↑ ↓ Enter Esc · пошук, коли пунктів > 8", g.selects),
				)
			}},
		)
	})
	// A select pinned near the bottom edge (bottom-left, under the fields):
	// its list opens upwards.
	win := gtx.Constraints.Max
	w := gtx.Dp(300)
	x := gtx.Dp(32)
	y := win.Y - gtx.Dp(32) - gtx.Dp(HeightMdPx)
	o := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
	sg := gtx
	sg.Constraints = layout.Exact(image.Pt(w, gtx.Dp(HeightMdPx)))
	s := st[gui.Select](g, "sel.up")
	s.Layout(sg, th, gui.SelectStyle{Options: durations, Value: "60", Leading: gui.IconTimer, Force: gui.ForcePress})
	o.Pop()
	o = op.Offset(image.Pt(x+w+gtx.Dp(16), y+gtx.Dp(4))).Push(gtx.Ops)
	lg := gtx
	lg.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(220), gtx.Dp(40))}
	gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 2}.Layout(lg, th, "← біля нижнього краю вікна список відкривається вгору")
	o.Pop()
	return d
}

// HeightMdPx is the Md control height in dp (for gallery placement maths).
const HeightMdPx = 44

func (g *gallery) fields(gtx C) D {
	th := g.th
	f := func(key string) *gui.TextField { return st[gui.TextField](g, key) }
	if g.frame <= 1 {
		f("f.filled").SetText("Лисиця і місяць")
		f("f.focus").SetText("Нічна казка")
		f("f.err").SetText("sk-12")
		f("f.dis").SetText("Сервер CarteLink")
		f("f.pw").SetText("секретний-ключ")
		f("f.count").SetText("Руда лисиця шукає місяць")
	}
	return gui.VStack(gtx, gui.Sp20,
		func(gtx C) D {
			return f("f.empty").Layout(gtx, th, gui.FieldStyle{Label: "Назва проєкту", Placeholder: "Наприклад, «Лисиця і місяць»", Hint: "Назву можна змінити будь-коли."})
		},
		func(gtx C) D {
			return gui.Columns{Gap: gui.Sp16}.Layout(gtx,
				gui.Col{Span: 1, W: func(gtx C) D {
					return f("f.filled").Layout(gtx, th, gui.FieldStyle{Label: "Заповнене", Leading: gui.IconFolder})
				}},
				gui.Col{Span: 1, W: func(gtx C) D {
					return f("f.focus").Layout(gtx, th, gui.FieldStyle{Label: "У фокусі", Force: gui.ForceFocus})
				}},
			)
		},
		func(gtx C) D {
			return gui.Columns{Gap: gui.Sp16}.Layout(gtx,
				gui.Col{Span: 1, W: func(gtx C) D {
					return f("f.err").Layout(gtx, th, gui.FieldStyle{Label: "Ключ API", Error: "Ключ закороткий — перевір, чи скопійовано повністю."})
				}},
				gui.Col{Span: 1, W: func(gtx C) D {
					return f("f.dis").Layout(gtx, th, gui.FieldStyle{Label: "Вимкнене", Disabled: true, Hint: "Налаштовується автоматично."})
				}},
			)
		},
		func(gtx C) D {
			return gui.Columns{Gap: gui.Sp16}.Layout(gtx,
				gui.Col{Span: 1, W: func(gtx C) D {
					return f("f.pw").Layout(gtx, th, gui.FieldStyle{Label: "Пароль / ключ", Password: true})
				}},
				gui.Col{Span: 1, W: func(gtx C) D {
					return f("f.count").Layout(gtx, th, gui.FieldStyle{Label: "З лічильником", MaxLen: 40, Force: gui.ForceHover})
				}},
			)
		},
	)
}

func (g *gallery) textAreas(gtx C) D {
	th := g.th
	ta := st[gui.TextField](g, "ta.1")
	sf := st[gui.SearchField](g, "sf.2")
	if g.frame <= 1 {
		ta.SetText("Руда лисиця щоночі виходила на пагорб і розмовляла з місяцем.\nОдного разу місяць не з'явився — і лисиця вирушила його шукати.")
		sf.SetText("кіно")
	}
	return gui.VStack(gtx, gui.Sp16,
		func(gtx C) D {
			return ta.Layout(gtx, th, gui.FieldStyle{Label: "Побажання режисеру", MinLines: 3, MaxLines: 6, Counter: true,
				Placeholder: "Що важливо в цьому відео?"})
		},
		func(gtx C) D {
			return gui.Columns{Gap: gui.Sp16}.Layout(gtx,
				gui.Col{Span: 1, W: func(gtx C) D { return st[gui.SearchField](g, "sf.1").Layout(gtx, th, "Пошук стилю") }},
				gui.Col{Span: 1, W: func(gtx C) D { return sf.Layout(gtx, th, "Пошук стилю") }},
			)
		},
	)
}

func (g *gallery) selects(gtx C) D {
	th := g.th
	return layout.Inset{Bottom: 300}.Layout(gtx, func(gtx C) D {
		return gui.Columns{Gap: gui.Sp16}.Layout(gtx,
			gui.Col{Span: 1, W: func(gtx C) D {
				return st[gui.Select](g, "sel.closed").Layout(gtx, th, gui.SelectStyle{Label: "Закритий", Options: voices, Value: "olena", Leading: gui.IconMic})
			}},
			gui.Col{Span: 1, W: func(gtx C) D {
				return st[gui.Select](g, "sel.open").Layout(gtx, th, gui.SelectStyle{Label: "Відкритий", Options: voices, Value: "taras", Force: gui.ForcePress})
			}},
			gui.Col{Span: 1, W: func(gtx C) D {
				return st[gui.Select](g, "sel.search").Layout(gtx, th, gui.SelectStyle{Label: "Мова (з пошуком)", Options: languages, Value: "uk", Leading: gui.IconLanguages, Force: gui.ForcePress})
			}},
		)
	})
}
