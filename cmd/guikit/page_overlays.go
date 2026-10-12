package main

import (
	"fmt"
	"image"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"ytcartell/internal/gui"
)

// ---- Dialog + toasts + tooltip -----------------------------------------------------

func (g *gallery) dialogSetup() {
	g.dialog.Open()
	g.th.Toast(gui.ToneSuccess, "Проєкт «Космічна одіссея» збережено")
	g.th.Toast(gui.ToneError, "Не вдалося згенерувати кадр 4: G-Labs не відповідає. Спробуй ще раз за хвилину.")
	g.th.Toast(gui.ToneInfo, "Автопілот почав роботу — можна закрити вікно, прогрес буде в черзі.")
}

func (g *gallery) dialogPage(gtx C) D {
	th := g.th
	d := g.home(gtx)
	th.WantAnchors()
	g.dialog.Layout(gtx, th, gui.DialogStyle{
		Title: "Видалити проєкт «Ранкова кава»?",
		Text:  "Разом із ним зникнуть кадри, відео й озвучка. Цю дію не можна скасувати.",
		Icon:  gui.IconTrash2, Tone: gui.ToneError,
		Body: func(gtx C) D {
			return st[gui.Toggle](g, "dlg.keep").Layout(gtx, th, true, "Залишити готове відео в папці «Експорт»")
		},
		Actions: []layout.Widget{
			func(gtx C) D {
				return gui.Button{Kind: gui.Tinted, Text: "Скасувати"}.Layout(gtx, th, g.click("dlg.cancel"))
			},
			func(gtx C) D {
				return gui.Anchor(gtx, th, "dlg.delete", func(gtx C) D {
					return gui.Button{Kind: gui.Danger, Text: "Видалити", Icon: gui.IconTrash2, Tip: "Назавжди видаляє проєкт і його файли"}.Layout(gtx, th, g.click("dlg.delete"))
				})
			},
		},
	})
	return d
}

// tooltipInput moves a synthetic mouse over the dialog's delete button once
// its position is known, so the tooltip shows after its 400 ms delay.
func (g *gallery) tooltipInput(frame int, r *input.Router) {
	if frame != 4 {
		return
	}
	rect, ok := g.th.AnchorRect("dlg.delete")
	if !ok {
		return
	}
	c := f32.Pt(float32(rect.Min.X+rect.Max.X)/2, float32(rect.Min.Y+rect.Max.Y)/2)
	r.Queue(pointer.Event{Kind: pointer.Move, Position: c, Source: pointer.Mouse})
}

// ---- Drawer ----------------------------------------------------------------------------

func (g *gallery) drawerSetup() { g.drawer.Open() }

func (g *gallery) drawerPage(gtx C) D {
	th := g.th
	d := g.home(gtx)
	g.drawer.Layout(gtx, th, gui.DrawerStyle{
		Title:    "Черга",
		Subtitle: "3 завдання · усі проєкти",
		Body: func(gtx C) D {
			return gui.VStack(gtx, gui.Sp4,
				gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), Color: th.Pal.Text2}.Widget(th, "Зараз"),
				func(gtx C) D {
					return gui.ActivityRow{Icon: gui.IconFilm, Tone: gui.ToneRunning, Solid: true, Title: "Кадри · Лисиця і місяць",
						Meta: "Сцена 4 з 8 · ще ~3 хв", Progress: 0.52, Value: "52%", Divider: true}.Layout(gtx, th, g.click("dq.1"))
				},
				func(gtx C) D {
					return gui.ActivityRow{Icon: gui.IconMic, Tone: gui.ToneRunning, Title: "Озвучка · Лисиця і місяць",
						Meta: "Репліка 2 з 12", Progress: 0.16, Value: "16%", Divider: true}.Layout(gtx, th, g.click("dq.2"))
				},
				layout.Spacer{Height: gui.Sp12}.Layout,
				gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), Color: th.Pal.Text2}.Widget(th, "У черзі"),
				func(gtx C) D {
					return gui.ActivityRow{Icon: gui.IconClapperboard, Title: "Монтаж · Космічна одіссея",
						Meta: "Почнеться після озвучки", Progress: -1, Divider: true,
						Trailing: gui.StatusPill{State: gui.StateQueued, Small: true}.Widget(th)}.Layout(gtx, th, g.click("dq.3"))
				},
				layout.Spacer{Height: gui.Sp12}.Layout,
				gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), Color: th.Pal.Text2}.Widget(th, "Нещодавно"),
				func(gtx C) D {
					return gui.ActivityRow{Icon: gui.IconCheck, Tone: gui.ToneSuccess, Title: "Відео · Ранкова кава",
						Meta: "Сьогодні, 14:20", Progress: -1, Value: "0:48", Divider: true}.Layout(gtx, th, g.click("dq.4"))
				},
				func(gtx C) D {
					return gui.ActivityRow{Icon: gui.IconAlertTriangle, Tone: gui.ToneError, Title: "Кадр 4 · Легенда про Довбуша",
						Meta: "G-Labs не відповідає", Progress: -1,
						Trailing: func(gtx C) D {
							return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Text: "Повторити", Icon: gui.IconRotateCcw}.Layout(gtx, th, g.click("dq.retry"))
						}}.Layout(gtx, th, g.click("dq.5"))
				},
			)
		},
		Footer: func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Text: "Скасувати все", Icon: gui.IconX}.Layout(gtx, th, g.click("dq.cancel"))
				}),
				layout.Flexed(1, func(gtx C) D { return D{Size: pt(gtx.Constraints.Min.X, 0)} }),
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Text: "Відкрити сторінку черги"}.Layout(gtx, th, g.click("dq.page"))
				}),
			)
		},
	})
	return d
}

// ---- BigPicker -------------------------------------------------------------------------

func (g *gallery) pickerSetup() { g.picker.Open() }

type styleDef struct {
	label, caption, cat string
	sw                  []uint32
	img                 int
}

var styleDefs = []styleDef{
	{"Кіно 35 мм", "підходить для драм і трилерів", "Кіно", nil, 2},
	{"Теплий нуар", "таємниці, детективи", "Кіно", []uint32{0x1B1B1F, 0x3A2E2A, 0x8C5A3C, 0xD9A066, 0xF2E3C6}, 0},
	{"Документальний", "науково-поп, історія", "Кіно", nil, 4},
	{"Акварель", "казки для малечі", "Ілюстрація", []uint32{0xF6E7D8, 0xE8B4A0, 0xA7C4BC, 0x6E9CA8, 0x3D5A6C}, 0},
	{"Олівець", "спокійні історії", "Ілюстрація", []uint32{0xFAFAF7, 0xD9D6CF, 0xA39E93, 0x6B665D, 0x2E2B27}, 0},
	{"Книжкова графіка", "легенди й міфи", "Ілюстрація", nil, 3},
	{"Ghibli-подібний", "пригоди, природа", "Анімація", nil, 1},
	{"Пластилін", "смішні історії", "Анімація", []uint32{0xF2C14E, 0xF78154, 0x4D9078, 0xB4436C, 0x5FAD56}, 0},
	{"3D-мультфільм", "сімейне кіно", "Анімація", []uint32{0x9AD1D4, 0x80CED7, 0x007EA7, 0x003249, 0xF7B267}, 0},
	{"Ретро-постер", "мотивація, ретро", "Арт", []uint32{0xE63946, 0xF1FAEE, 0xA8DADC, 0x457B9D, 0x1D3557}, 0},
	{"Неон", "кіберпанк, ніч", "Арт", nil, 2},
	{"Мінімалізм", "пояснювальні відео", "Арт", []uint32{0xFFFFFF, 0xF0EDE8, 0xFF6A3D, 0x2B241F, 0x8E8177}, 0},
}

func (g *gallery) pickerPage(gtx C) D {
	th := g.th
	d := g.home(gtx)
	items := make([]gui.PickerItem, len(styleDefs))
	for i, s := range styleDefs {
		it := gui.PickerItem{Value: fmt.Sprint("style-", i), Label: s.label, Caption: s.caption, Category: s.cat}
		if s.sw != nil {
			for _, c := range s.sw {
				it.Swatches = append(it.Swatches, hexc(c))
			}
		} else {
			it.Image = g.image(s.img)
		}
		if i == 6 {
			it.Badge = "Новий"
		}
		items[i] = it
	}
	g.picker.Layout(gtx, th, gui.PickerStyle{
		Title: "Стиль кадрів", Subtitle: "Обери, як виглядатимуть сцени. Стиль можна змінити й перегенерувати кадри пізніше.",
		Items: items, Value: "style-3", SearchPlaceholder: "Пошук стилю",
	})
	return d
}

// ---- Tour --------------------------------------------------------------------------------

func (g *gallery) tourSetup() {
	g.tour.Steps = []gui.TourStep{
		{Title: "Вітаємо в студії!", Text: "Покажемо, як за хвилину зробити перше відео."},
		{Anchor: "home.hero", Title: "Встав історію", Text: "Встав готовий текст або коротко опиши ідею — решту студія зробить сама."},
		{Anchor: "home.autopilot", Title: "Натисни «Автопілот»", Text: "Студія сама зробить план, кадри, озвучку й змонтує відео. Можна піти пити чай."},
		{Anchor: "home.queue", Title: "Стеж за чергою", Text: "Тут видно, що генерується зараз і скільки лишилось."},
		{Anchor: "home.new", Title: "Новий проєкт", Text: "Велика кнопка «+» завжди під рукою."},
	}
	g.tour.Start()
	g.tour.SetStep(2)
}

func (g *gallery) tourPage(gtx C) D {
	d := g.home(gtx)
	g.tour.Layout(gtx, g.th)
	return d
}

func pt(x, y int) image.Point { return image.Point{X: x, Y: y} }
