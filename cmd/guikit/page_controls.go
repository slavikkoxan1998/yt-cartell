package main

import (
	"fmt"
	"image"

	"gioui.org/layout"
	"gioui.org/unit"

	"ytcartell/internal/gui"
)

func (g *gallery) controls(gtx C) D {
	return g.pageFrame(gtx, "Керування", "Кнопки, чіпи, статуси, вкладки, перемикачі й повзунки — у всіх станах.", func(gtx C) D {
		return gui.Columns{}.Layout(gtx,
			gui.Col{Span: 7, W: func(gtx C) D {
				return gui.VStack(gtx, gui.Sp24,
					g.section("Кнопки", "Primary · Secondary · Tinted · Ghost · Danger", g.buttonMatrix),
					g.section("Розміри й іконки", "Sm 36 · Md 44 · Big 52 · круглі", g.buttonSizes),
					g.section("Чіпи, позначки, статуси", "", g.chipsAndPills),
				)
			}},
			gui.Col{Span: 5, W: func(gtx C) D {
				return gui.VStack(gtx, gui.Sp24,
					g.section("Вкладки", "PillTabs · Segmented — індикатор ковзає", g.tabsDemo),
					g.section("Перемикачі й повзунки", "", g.togglesDemo),
					g.section("Навігація", "NavItem · кнопка «+» · Spinner", g.navDemo),
				)
			}},
		)
	})
}

var stateCols = []struct {
	name  string
	force gui.Force
	dis   bool
	busy  bool
}{
	{"Звичайна", 0, false, false},
	{"Наведення", gui.ForceHover, false, false},
	{"Натиснута", gui.ForceHover | gui.ForcePress, false, false},
	{"Фокус", gui.ForceFocus, false, false},
	{"Вимкнена", 0, true, false},
	{"Зайнята", 0, false, true},
}

func (g *gallery) buttonMatrix(gtx C) D {
	th := g.th
	kinds := []struct {
		name string
		kind gui.ButtonKind
		text string
	}{
		{"Primary", gui.Primary, "Старт"},
		{"Secondary", gui.Secondary, "Далі"},
		{"Tinted", gui.Tinted, "Імпорт"},
		{"Ghost", gui.Ghost, "Назад"},
		{"Danger", gui.Danger, "Стоп"},
	}
	labelW := gtx.Dp(84)
	colW := (gtx.Constraints.Max.X - labelW) / len(stateCols)
	var rows []layout.FlexChild
	rows = append(rows, layout.Rigid(func(gtx C) D {
		var ch []layout.FlexChild
		ch = append(ch, layout.Rigid(layout.Spacer{Width: unit.Dp(float32(labelW) / gtx.Metric.PxPerDp)}.Layout))
		for _, s := range stateCols {
			s := s
			ch = append(ch, layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X, gtx.Constraints.Max.X = colW, colW
				return gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Layout(gtx, th, s.name)
			}))
		}
		return layout.Flex{}.Layout(gtx, ch...)
	}))
	for ki, k := range kinds {
		k := k
		ki := ki
		rows = append(rows, layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout), layout.Rigid(func(gtx C) D {
			var ch []layout.FlexChild
			ch = append(ch, layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X, gtx.Constraints.Max.X = labelW, labelW
				return gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: th.Pal.Text2}.Layout(gtx, th, k.name)
			}))
			for si, s := range stateCols {
				s := s
				key := fmt.Sprint("bm.", ki, ".", si)
				ch = append(ch, layout.Rigid(func(gtx C) D {
					// A fixed-width cell per state column; the button keeps its size.
					gtx.Constraints = layout.Exact(image.Pt(colW, gtx.Dp(44)))
					return layout.W.Layout(gtx, func(gtx C) D {
						gtx.Constraints.Max.X = colW - gtx.Dp(8)
						icon := (*gui.Icon)(nil)
						if k.kind == gui.Primary {
							icon = gui.IconRocket
						}
						return gui.Button{Kind: k.kind, Size: gui.Sm, Text: k.text, Icon: icon, Force: s.force, Disabled: s.dis, Busy: s.busy}.Layout(gtx, th, g.click(key))
					})
				}))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

func (g *gallery) buttonSizes(gtx C) D {
	th := g.th
	return gui.VStack(gtx, gui.Sp16,
		func(gtx C) D {
			return gui.Flow{Gap: gui.Sp12}.Layout(gtx,
				g.labeled("Sm", func(gtx C) D {
					return gui.Button{Size: gui.Sm, Text: "Далі", Trail: gui.IconArrowRight}.Layout(gtx, th, g.click("sz.sm"))
				}),
				g.labeled("Md", func(gtx C) D {
					return gui.Button{Text: "Згенерувати", Icon: gui.IconSparkles}.Layout(gtx, th, g.click("sz.md"))
				}),
				g.labeled("Big", func(gtx C) D {
					return gui.Button{Size: gui.Big, Text: "Автопілот", Icon: gui.IconRocket}.Layout(gtx, th, g.click("sz.big"))
				}),
				g.labeled("Round · pill", func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Round: true, Text: "Слухати", Icon: gui.IconPlay}.Layout(gtx, th, g.click("sz.pill"))
				}),
				g.labeled("Full width", func(gtx C) D {
					gtx.Constraints.Max.X = gtx.Dp(200)
					return gui.Button{Kind: gui.Secondary, Full: true, Text: "На всю ширину"}.Layout(gtx, th, g.click("sz.full"))
				}),
			)
		},
		func(gtx C) D {
			return gui.Flow{Gap: gui.Sp12}.Layout(gtx,
				g.labeled("Icon", func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Icon: gui.IconSettings, Tip: "Налаштування"}.Layout(gtx, th, g.click("ic.1"))
				}),
				g.labeled("Round", func(gtx C) D {
					return gui.Button{Icon: gui.IconArrowRight, Round: true, Tip: "Далі"}.Layout(gtx, th, g.click("ic.2"))
				}),
				g.labeled("Secondary", func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Icon: gui.IconBell, Round: true, Tip: "Сповіщення"}.Layout(gtx, th, g.click("ic.3"))
				}),
				g.labeled("Ghost", func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Icon: gui.IconMoreHorizontal, Tip: "Ще"}.Layout(gtx, th, g.click("ic.4"))
				}),
				g.labeled("Big +", func(gtx C) D {
					return gui.Button{Icon: gui.IconPlus, Round: true, Size: gui.Big, Height: 56, Tip: "Новий проєкт"}.Layout(gtx, th, g.click("ic.5"))
				}),
				g.labeled("IconButton", func(gtx C) D {
					return gui.HStack(gtx, gui.Sp4,
						func(gtx C) D {
							return gui.IconButton{Icon: gui.IconPencil, Tip: "Змінити"}.Layout(gtx, th, g.click("ib.1"))
						},
						func(gtx C) D {
							return gui.IconButton{Icon: gui.IconCopy, Tip: "Копіювати", Force: gui.ForceHover}.Layout(gtx, th, g.click("ib.2"))
						},
						func(gtx C) D {
							return gui.IconButton{Icon: gui.IconTrash2, Tip: "Видалити"}.Layout(gtx, th, g.click("ib.3"))
						},
					)
				}),
			)
		},
	)
}

func (g *gallery) chipsAndPills(gtx C) D {
	th := g.th
	return gui.VStack(gtx, gui.Sp16,
		func(gtx C) D {
			return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx,
				func(gtx C) D { return gui.Chip{Text: "Казки"}.Layout(gtx, th, g.click("c.1")) },
				func(gtx C) D {
					return gui.Chip{Text: "Наведення", Force: gui.ForceHover}.Layout(gtx, th, g.click("c.2"))
				},
				func(gtx C) D { return gui.Chip{Text: "Вибрано", Selected: true}.Layout(gtx, th, g.click("c.3")) },
				func(gtx C) D {
					return gui.Chip{Text: "Українська", Icon: gui.IconLanguages}.Layout(gtx, th, g.click("c.4"))
				},
				func(gtx C) D {
					return gui.Chip{Text: "Онлайн", Dot: th.Pal.Success}.Layout(gtx, th, g.click("c.5"))
				},
				func(gtx C) D {
					return gui.Chip{Text: "@лисиця", Remove: g.click("c.6x")}.Layout(gtx, th, g.click("c.6"))
				},
				func(gtx C) D {
					return gui.Chip{Text: "Вимкнено", Disabled: true}.Layout(gtx, th, g.click("c.7"))
				},
				func(gtx C) D { return gui.Chip{Text: "Малий", Small: true}.Layout(gtx, th, g.click("c.8")) },
			)
		},
		func(gtx C) D {
			return gui.Flow{Gap: gui.Sp8}.Layout(gtx,
				gui.Badge{Text: "3"}.Widget(th),
				gui.Badge{Text: "12", Tone: gui.ToneAction}.Widget(th),
				gui.Badge{Text: "Новий", Tone: gui.ToneAction, Solid: true}.Widget(th),
				gui.Badge{Text: "Готово", Tone: gui.ToneSuccess}.Widget(th),
				gui.Badge{Text: "Бета", Tone: gui.ToneRunning}.Widget(th),
				gui.Badge{Text: "Увага", Tone: gui.ToneWarning}.Widget(th),
				gui.Badge{Text: "2 помилки", Tone: gui.ToneError, Solid: true}.Widget(th),
				gui.Badge{Tone: gui.ToneAction}.Widget(th),
			)
		},
		func(gtx C) D {
			return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx,
				gui.StatusPill{State: gui.StateDone}.Widget(th),
				gui.StatusPill{State: gui.StateRunning, Percent: 45}.Widget(th),
				gui.StatusPill{State: gui.StateError}.Widget(th),
				gui.StatusPill{State: gui.StateStale}.Widget(th),
				gui.StatusPill{State: gui.StateEmpty}.Widget(th),
				gui.StatusPill{State: gui.StateQueued}.Widget(th),
				gui.StatusPill{State: gui.StateDone, Small: true}.Widget(th),
			)
		},
		func(gtx C) D {
			return gui.Flow{Gap: gui.Sp24}.Layout(gtx,
				func(gtx C) D {
					return gui.Link{Text: "Переглянути все", Trail: gui.IconChevronRight}.Layout(gtx, th, g.click("l.1"))
				},
				func(gtx C) D {
					return gui.Link{Text: "Наведення", Trail: gui.IconChevronRight, Force: gui.ForceHover}.Layout(gtx, th, g.click("l.2"))
				},
				func(gtx C) D {
					return gui.Link{Text: "Керувати", Style: th.Label}.Layout(gtx, th, g.click("l.3"))
				},
			)
		},
	)
}

func (g *gallery) tabsDemo(gtx C) D {
	th := g.th
	steps := []gui.Tab{
		{Label: "Сценарій", Done: true}, {Label: "План", Done: true}, {Label: "Розкадровка", Number: 3},
		{Label: "Відео", Number: 4}, {Label: "Монтаж", Number: 5},
	}
	return gui.VStack(gtx, gui.Sp16,
		func(gtx C) D {
			t := st[gui.PillTabs](g, "tabs.steps")
			t.Full = true
			return t.Layout(gtx, th, 2, steps)
		},
		func(gtx C) D {
			t := st[gui.PillTabs](g, "tabs.icons")
			return t.Layout(gtx, th, 0, []gui.Tab{
				{Label: "Сітка", Icon: gui.IconLayoutGrid}, {Label: "Список", Icon: gui.IconList}, {Label: "Черга", Icon: gui.IconListVideo, Badge: "3"},
			})
		},
		func(gtx C) D {
			return gui.Flow{Gap: gui.Sp16, RowGap: gui.Sp12}.Layout(gtx,
				func(gtx C) D {
					s := st[gui.Segmented](g, "seg.aspect")
					return s.Layout(gtx, th, "16:9", []gui.SegOption{{Value: "16:9", Label: "16:9"}, {Value: "9:16", Label: "9:16"}, {Value: "1:1", Label: "1:1"}})
				},
				func(gtx C) D {
					s := st[gui.Segmented](g, "seg.mode")
					return s.Layout(gtx, th, "idea", []gui.SegOption{
						{Value: "idea", Label: "Ідея", Icon: gui.IconLightbulb},
						{Value: "script", Label: "Сценарій", Icon: gui.IconFileText},
						{Value: "voice", Label: "Своя озвучка", Icon: gui.IconMic},
					})
				},
			)
		},
	)
}

func (g *gallery) togglesDemo(gtx C) D {
	th := g.th
	pct := func(v float32) string { return fmt.Sprintf("%.0f%%", v) }
	return gui.VStack(gtx, gui.Sp16,
		func(gtx C) D {
			return gui.Flow{Gap: gui.Sp24, RowGap: gui.Sp12}.Layout(gtx,
				func(gtx C) D { return st[gui.Toggle](g, "t.1").Layout(gtx, th, false, "Субтитри") },
				func(gtx C) D { return st[gui.Toggle](g, "t.2").Layout(gtx, th, true, "Озвучка") },
				func(gtx C) D {
					t := st[gui.Toggle](g, "t.3")
					t.Force = gui.ForceFocus
					return t.Layout(gtx, th, true, "Фокус")
				},
				func(gtx C) D {
					t := st[gui.Toggle](g, "t.4")
					t.Disabled = true
					return t.Layout(gtx, th, false, "Вимкнено")
				},
			)
		},
		func(gtx C) D {
			return layout.Flex{}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					return layout.Inset{Top: gui.Sp32}.Layout(gtx, g.labeled("Гучність музики", func(gtx C) D {
						return st[gui.Slider](g, "s.1").Layout(gtx, th, gui.SliderStyle{Value: 35, Max: 100, Format: pct})
					}))
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp24}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return layout.Inset{Top: gui.Sp32}.Layout(gtx, g.labeled("Під час перетягування — бульбашка", func(gtx C) D {
						return st[gui.Slider](g, "s.2").Layout(gtx, th, gui.SliderStyle{Value: 62, Max: 100, Format: pct, Force: gui.ForcePress})
					}))
				}),
			)
		},
		func(gtx C) D {
			return layout.Flex{}.Layout(gtx,
				layout.Flexed(1, g.labeled("Фокус (стрілки ←/→)", func(gtx C) D {
					return st[gui.Slider](g, "s.3").Layout(gtx, th, gui.SliderStyle{Value: 8, Min: 4, Max: 8, Step: 2, Force: gui.ForceFocus})
				})),
				layout.Rigid(layout.Spacer{Width: gui.Sp24}.Layout),
				layout.Flexed(1, g.labeled("Вимкнено", func(gtx C) D {
					return st[gui.Slider](g, "s.4").Layout(gtx, th, gui.SliderStyle{Value: 20, Max: 100, Disabled: true})
				})),
			)
		},
	)
}

func (g *gallery) navDemo(gtx C) D {
	th := g.th
	item := func(label string, ic *gui.Icon, active bool, badge string, f gui.Force, key string) layout.Widget {
		return func(gtx C) D {
			gtx.Constraints.Min.X, gtx.Constraints.Max.X = gtx.Dp(92), gtx.Dp(92)
			return gui.NavItem{Icon: ic, Label: label, Active: active, Badge: badge, Force: f}.Layout(gtx, th, g.click(key))
		}
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(item("Головна", gui.IconHome, true, "", 0, "n.1")),
		layout.Rigid(item("Проєкти", gui.IconFolder, false, "", 0, "n.2")),
		layout.Rigid(item("Наведення", gui.IconSettings, false, "", gui.ForceHover, "n.3")),
		layout.Rigid(item("Черга", gui.IconListVideo, false, "3", 0, "n.4")),
		layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} }),
		layout.Rigid(func(gtx C) D { return gui.Spinner{Size: 24}.Layout(gtx, th) }),
		layout.Rigid(layout.Spacer{Width: gui.Sp24}.Layout),
		layout.Rigid(func(gtx C) D {
			return gui.Button{Icon: gui.IconPlus, Round: true, Size: gui.Big, Height: 56, Tip: "Новий проєкт"}.Layout(gtx, th, g.click("n.plus"))
		}),
	)
}
