package main

import (
	"fmt"
	"image"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/gui"
)

// home composes a Home screen from kit parts (GUI.md "FIRST VIEWPORT"): the
// rail, the greeting hero with the story box, quick actions, the projects
// grid with progress rings and the right column with the queue and a tip.
func (g *gallery) home(gtx C) D {
	th := g.th
	return layout.Flex{}.Layout(gtx,
		layout.Rigid(func(gtx C) D { return g.rail(gtx, "home") }),
		layout.Flexed(1, func(gtx C) D {
			sc := st[gui.Scroll](g, "home.scroll")
			return sc.LayoutWidget(gtx, th, func(gtx C) D {
				return layout.UniformInset(gui.Sp32).Layout(gtx, func(gtx C) D {
					return gui.VStack(gtx, gui.Sp24,
						g.greeting,
						func(gtx C) D {
							right := gtx.Dp(400)
							if gtx.Constraints.Max.X < gtx.Dp(1100) {
								right = 0
							}
							if right == 0 {
								return gui.VStack(gtx, gui.Sp24, g.homeMain, g.homeSide)
							}
							return layout.Flex{Alignment: layout.Start}.Layout(gtx,
								layout.Flexed(1, g.homeMain),
								layout.Rigid(layout.Spacer{Width: gui.Sp24}.Layout),
								layout.Rigid(func(gtx C) D {
									gtx.Constraints.Min.X, gtx.Constraints.Max.X = right, right
									return g.homeSide(gtx)
								}),
							)
						},
					)
				})
			})
		}),
	)
}

// rail is the left navigation rail.
func (g *gallery) rail(gtx C, active string) D {
	th := g.th
	w := gtx.Dp(92)
	h := gtx.Constraints.Max.Y
	gui.FillRect(gtx, image.Rect(0, 0, w, h), th.Pal.Card)
	gui.FillRect(gtx, image.Rect(w-1, 0, w, h), th.Pal.Border)
	gtx.Constraints = layout.Exact(image.Pt(w-1, h))
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(layout.Spacer{Height: gui.Sp24}.Layout),
		layout.Rigid(func(gtx C) D {
			sz := gtx.Dp(48)
			gui.FillRRect(gtx, image.Rect(0, 0, sz, sz), 14, th.Pal.Action)
			ic := gtx.Dp(26)
			o := op.Offset(image.Pt((sz-ic)/2, (sz-ic)/2)).Push(gtx.Ops)
			gui.IconClapperboard.Layout(gtx, 26, th.Pal.OnAction)
			o.Pop()
			return D{Size: image.Pt(sz, sz)}
		}),
		layout.Rigid(layout.Spacer{Height: gui.Sp32}.Layout),
		layout.Rigid(func(gtx C) D {
			items := []struct {
				key, label string
				icon       *gui.Icon
				badge      string
			}{
				{"home", "Головна", gui.IconHome, ""},
				{"projects", "Проєкти", gui.IconFolder, ""},
				{"queue", "Черга", gui.IconListVideo, "3"},
				{"settings", "Налаштування", gui.IconSettings, ""},
			}
			var ch []layout.FlexChild
			for _, it := range items {
				it := it
				ch = append(ch, layout.Rigid(func(gtx C) D {
					return gui.NavItem{Icon: it.icon, Label: it.label, Active: it.key == active, Badge: it.badge}.Layout(gtx, th, g.click("nav."+it.key))
				}), layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
		}),
		layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(0, gtx.Constraints.Min.Y)} }),
		layout.Rigid(func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Icon: themeIcon(th), Round: true, Tip: "Тема"}.Layout(gtx, th, g.click("nav.theme"))
		}),
		layout.Rigid(layout.Spacer{Height: gui.Sp16}.Layout),
		layout.Rigid(func(gtx C) D {
			return gui.Anchor(gtx, th, "home.new", func(gtx C) D {
				return gui.Button{Icon: gui.IconPlus, Round: true, Size: gui.Big, Height: 56, Tip: "Новий проєкт"}.Layout(gtx, th, g.click("nav.new"))
			})
		}),
		layout.Rigid(layout.Spacer{Height: gui.Sp24}.Layout),
	)
}

func (g *gallery) greeting(gtx C) D {
	th := g.th
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D {
			return gui.VStack(gtx, gui.Sp4,
				gui.Text{Style: th.Display}.Widget(th, "Добрий вечір, Олено"),
				gui.Text{Style: th.Body.Sized(17), Color: th.Pal.Text2}.Widget(th, "Що знімемо сьогодні? Встав історію — решту зробить студія."),
			)
		}),
		layout.Rigid(func(gtx C) D {
			return gui.HStack(gtx, gui.Sp12,
				width(300, func(gtx C) D {
					return st[gui.SearchField](g, "home.search").Layout(gtx, th, "Пошук проєктів")
				}),
				func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Icon: gui.IconBell, Round: true, Tip: "Сповіщення"}.Layout(gtx, th, g.click("home.bell"))
				},
			)
		}),
	)
}

func (g *gallery) homeMain(gtx C) D {
	th := g.th
	return gui.VStack(gtx, gui.Sp24,
		func(gtx C) D { return gui.Anchor(gtx, th, "home.hero", g.hero) },
		g.quickActions,
		func(gtx C) D {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Flexed(1, gui.Text{Style: th.H2}.Widget(th, "Мої проєкти")),
				layout.Rigid(func(gtx C) D {
					return gui.Link{Text: "Усі проєкти", Trail: gui.IconChevronRight}.Layout(gtx, th, g.click("home.all"))
				}),
			)
		},
		func(gtx C) D {
			projects := []struct {
				title, meta string
				state       gui.AssetState
				pct         int
				progress    float32
				step        string
			}{
				{"Лисиця і місяць", "8 сцен · 1:04 · 16:9", gui.StateRunning, 52, 0.52, "Крок 3 з 5"},
				{"Космічна одіссея", "12 сцен · 1:36 · 16:9", gui.StateDone, -1, 1, "Готово"},
				{"Ранкова кава", "6 сцен · 0:48 · 9:16", gui.StateStale, -1, 0.8, "Крок 4 з 5"},
				{"Легенда про Довбуша", "15 сцен · 2:00 · 16:9", gui.StateEmpty, -1, 0.2, "Крок 1 з 5"},
			}
			return gui.Grid{MinWidth: 280, Gap: gui.Sp24, Stretch: true}.Layout(gtx, len(projects), func(gtx C, i int) D {
				p := projects[i]
				return g.projectCard(gtx, i, p.title, p.meta, p.state, p.pct, p.progress, p.step)
			})
		},
	)
}

func (g *gallery) hero(gtx C) D {
	th := g.th
	story := st[gui.TextField](g, "home.story")
	if g.frame <= 1 {
		story.SetText("Жила-була руда лисиця, яка щоночі виходила на пагорб і розмовляла з місяцем. " +
			"Одного разу місяць не з'явився — і лисиця вирушила його шукати через ліс, річку й старе місто…")
	}
	return gui.Card{Padding: gui.Sp24}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp16,
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.H3}.Widget(th, "Нова історія")),
					layout.Flexed(1, func(gtx C) D {
						return layout.Inset{Left: gui.Sp12}.Layout(gtx,
							gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, "Встав текст або опиши ідею — решту зробить студія"))
					}),
					layout.Rigid(func(gtx C) D {
						return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconUpload, Text: "Імпорт .txt"}.Layout(gtx, th, g.click("home.import"))
					}),
				)
			},
			func(gtx C) D {
				return story.Layout(gtx, th, gui.FieldStyle{
					Placeholder: "Жила-була лисиця, яка щоночі розмовляла з місяцем…",
					MinLines:    4, MaxLines: 8, Counter: true,
				})
			},
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D {
						return gui.Flow{Gap: gui.Sp8}.Layout(gtx,
							gui.Text{Style: th.Label, Color: th.Pal.Text2}.Widget(th, "Профіль:"),
							func(gtx C) D {
								return gui.Chip{Text: "Казки", Selected: true}.Layout(gtx, th, g.click("chip.tales"))
							},
							func(gtx C) D { return gui.Chip{Text: "Жахи"}.Layout(gtx, th, g.click("chip.horror")) },
							func(gtx C) D { return gui.Chip{Text: "Науково-поп"}.Layout(gtx, th, g.click("chip.sci")) },
							func(gtx C) D {
								return layout.Inset{Left: gui.Sp4}.Layout(gtx, func(gtx C) D {
									return gui.Link{Text: "Керувати", Style: th.Label}.Layout(gtx, th, g.click("chip.manage"))
								})
							},
						)
					}),
					layout.Rigid(func(gtx C) D {
						return gui.HStack(gtx, gui.Sp8,
							func(gtx C) D {
								return gui.Chip{Text: "Українська", Icon: gui.IconLanguages}.Layout(gtx, th, g.click("chip.lang"))
							},
							func(gtx C) D {
								return gui.Chip{Text: "1 хв", Icon: gui.IconTimer}.Layout(gtx, th, g.click("chip.dur"))
							},
						)
					}),
				)
			},
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return gui.Anchor(gtx, th, "home.autopilot", func(gtx C) D {
							return gui.Button{Size: gui.Big, Text: "Автопілот", Icon: gui.IconRocket}.Layout(gtx, th, g.click("home.auto"))
						})
					}),
					layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
					layout.Rigid(func(gtx C) D {
						return gui.Button{Kind: gui.Secondary, Size: gui.Big, Text: "Покроково", Trail: gui.IconArrowRight}.Layout(gtx, th, g.click("home.steps"))
					}),
					layout.Flexed(1, func(gtx C) D {
						return layout.E.Layout(gtx, gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, "≈ 8 сцен по 8 с · 16:9 · голос «Олена»"))
					}),
				)
			},
		)
	})
}

func (g *gallery) quickActions(gtx C) D {
	th := g.th
	tiles := []struct {
		icon  *gui.Icon
		label string
	}{
		{gui.IconPlus, "Нова історія"},
		{gui.IconRocket, "Автопілот"},
		{gui.IconLanguages, "Перекласти проєкт"},
		{gui.IconMic, "Своя озвучка"},
		{gui.IconImage, "Обкладинка"},
	}
	return gui.Card{Padding: gui.Sp24}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp16,
			gui.Text{Style: th.H3}.Widget(th, "Швидкі дії"),
			func(gtx C) D {
				var ch []layout.FlexChild
				for i, t := range tiles {
					t := t
					key := fmt.Sprint("tile.", i)
					ch = append(ch, layout.Flexed(1, func(gtx C) D {
						return layout.N.Layout(gtx, func(gtx C) D {
							return gui.Tile{Icon: t.icon, Label: t.label}.Layout(gtx, th, g.click(key))
						})
					}))
				}
				return layout.Flex{}.Layout(gtx, ch...)
			},
		)
	})
}

func (g *gallery) projectCard(gtx C, i int, title, meta string, state gui.AssetState, pct int, progress float32, step string) D {
	th := g.th
	return gui.Card{Click: g.click(fmt.Sprint("proj.", i)), Padding: gui.Sp12, Radius: gui.RadiusCard}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp12,
			func(gtx C) D {
				img := g.image(i)
				if state == gui.StateEmpty {
					img = paintZero()
				}
				return gui.Thumb{Image: img, Ratio: 16.0 / 10, Radius: gui.RadiusInner, Placeholder: gui.IconClapperboard,
					Overlay: func(gtx C) D {
						return layout.NE.Layout(gtx, func(gtx C) D {
							return layout.UniformInset(gui.Sp8).Layout(gtx, func(gtx C) D {
								return gui.StatusPill{State: state, Percent: pct, Small: true}.Layout(gtx, th)
							})
						})
					}}.Layout(gtx, th)
			},
			func(gtx C) D {
				return layout.Inset{Left: gui.Sp4, Right: gui.Sp4, Bottom: gui.Sp4}.Layout(gtx, func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx C) D {
							return gui.VStack(gtx, 2,
								gui.Text{Style: th.BodyStrong.Sized(16), MaxLines: 1}.Widget(th, title),
								gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, meta),
								gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, step+" · оновлено сьогодні"),
							)
						}),
						layout.Rigid(func(gtx C) D {
							tone := gui.ToneNeutral
							if state == gui.StateDone {
								tone = gui.ToneSuccess
							}
							return gui.ProgressRing{Value: progress, Size: 48, Stroke: 5, Tone: tone}.Layout(gtx, th)
						}),
					)
				})
			},
		)
	})
}

func (g *gallery) homeSide(gtx C) D {
	th := g.th
	return gui.VStack(gtx, gui.Sp24,
		func(gtx C) D {
			return gui.NextStepCard{Icon: gui.IconSprout, Title: "Продовж «Лисиця і місяць»",
				Text: "Крок 3 з 5 — розкадровка чекає на тебе."}.Layout(gtx, th, g.click("home.next"))
		},
		func(gtx C) D {
			return gui.Anchor(gtx, th, "home.queue", func(gtx C) D {
				return gui.Card{Padding: gui.Sp24}.Layout(gtx, th, func(gtx C) D {
					return gui.VStack(gtx, gui.Sp8,
						func(gtx C) D {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(gui.Text{Style: th.H3}.Widget(th, "Черга")),
								layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
								layout.Rigid(gui.Badge{Text: "3", Tone: gui.ToneAction}.Widget(th)),
								layout.Flexed(1, func(gtx C) D {
									return layout.E.Layout(gtx, func(gtx C) D {
										return gui.Link{Text: "Відкрити", Trail: gui.IconChevronRight, Style: th.Label}.Layout(gtx, th, g.click("queue.open"))
									})
								}),
							)
						},
						func(gtx C) D {
							return gui.ActivityRow{Icon: gui.IconFilm, Tone: gui.ToneRunning, Solid: true, Title: "Кадри · Лисиця і місяць",
								Meta: "Сцена 4 з 8 · ще ~3 хв", Progress: 0.52, Value: "52%", Divider: true}.Layout(gtx, th, g.click("q.1"))
						},
						func(gtx C) D {
							return gui.ActivityRow{Icon: gui.IconMic, Title: "Озвучка · Космічна одіссея",
								Meta: "У черзі · 12 реплік", Progress: -1, Divider: true,
								Trailing: gui.StatusPill{State: gui.StateQueued, Small: true}.Widget(th)}.Layout(gtx, th, g.click("q.2"))
						},
						func(gtx C) D {
							return gui.ActivityRow{Icon: gui.IconCheck, Tone: gui.ToneSuccess, Title: "Монтаж · Ранкова кава",
								Meta: "Сьогодні, 14:20", Progress: -1, Value: "0:48"}.Layout(gtx, th, g.click("q.3"))
						},
					)
				})
			})
		},
		func(gtx C) D {
			return gui.TipCard{Icon: gui.IconLightbulb, Title: "Порада", Text: "Додай 2–3 профілі — і автопілот сам підбере голос, стиль і темп для кожної історії.",
				Action: "Налаштувати профілі"}.Layout(gtx, th, g.click("tip.profiles"))
		},
	)
}
