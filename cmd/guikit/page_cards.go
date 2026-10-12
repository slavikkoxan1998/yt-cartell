package main

import (
	"time"

	"gioui.org/layout"

	"ytcartell/internal/gui"
)

func (g *gallery) cards(gtx C) D {
	return g.pageFrame(gtx, "Картки, прогрес і медіа", "Поверхні, плитки, наступний крок, черга, прогрес, скелетони, порожні стани, зображення й аудіо.", func(gtx C) D {
		cols := 3
		if gtx.Constraints.Max.X > gtx.Dp(1700) {
			cols = 4
		}
		if cols == 4 {
			return gui.Columns{}.Layout(gtx,
				gui.Col{Span: 1, W: func(gtx C) D { return gui.VStack(gtx, gui.Sp24, g.cardSurfaces, g.cardTiles) }},
				gui.Col{Span: 1, W: func(gtx C) D { return gui.VStack(gtx, gui.Sp24, g.cardNext, g.cardQueue) }},
				gui.Col{Span: 1, W: func(gtx C) D { return gui.VStack(gtx, gui.Sp24, g.cardProgress, g.cardEmpty) }},
				gui.Col{Span: 1, W: func(gtx C) D { return gui.VStack(gtx, gui.Sp24, g.cardMedia, g.cardSkeleton) }},
			)
		}
		return gui.Columns{}.Layout(gtx,
			gui.Col{Span: 1, W: func(gtx C) D { return gui.VStack(gtx, gui.Sp24, g.cardSurfaces, g.cardTiles, g.cardSkeleton) }},
			gui.Col{Span: 1, W: func(gtx C) D { return gui.VStack(gtx, gui.Sp24, g.cardNext, g.cardQueue) }},
			gui.Col{Span: 1, W: func(gtx C) D { return gui.VStack(gtx, gui.Sp24, g.cardProgress, g.cardMedia, g.cardEmpty) }},
		)
	})
}

func (g *gallery) cardSurfaces(gtx C) D {
	th := g.th
	mini := func(title, note string, c gui.Card, key string) layout.Widget {
		return func(gtx C) D {
			if c.Click != nil || key != "" {
				c.Click = g.click(key)
			}
			return c.Layout(gtx, th, func(gtx C) D {
				return gui.VStack(gtx, 2,
					gui.Text{Style: th.BodyStrong, MaxLines: 1}.Widget(th, title),
					gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, note),
				)
			})
		}
	}
	return gui.Grid{MinWidth: 150, Gap: gui.Sp16, Stretch: true}.Layout(gtx, 4, func(gtx C, i int) D {
		switch i {
		case 0:
			return mini("Картка", "спокій", gui.Card{Padding: gui.Sp16}, "")(gtx)
		case 1:
			return mini("Наведення", "піднімається", gui.Card{Padding: gui.Sp16, Force: gui.ForceHover}, "cs.hover")(gtx)
		case 2:
			return mini("Вибрана", "контур дії", gui.Card{Padding: gui.Sp16, Selected: true}, "cs.sel")(gtx)
		default:
			return mini("Вкладена", "Tint · без тіні", gui.Card{Padding: gui.Sp16, Tint: true, Radius: gui.RadiusInner}, "")(gtx)
		}
	})
}

func (g *gallery) cardTiles(gtx C) D {
	th := g.th
	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp16,
			gui.Text{Style: th.H3}.Widget(th, "Плитки й статистика"),
			func(gtx C) D {
				return layout.Flex{Spacing: layout.SpaceAround}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return gui.Tile{Icon: gui.IconMic, Label: "Своя озвучка"}.Layout(gtx, th, g.click("tl.1"))
					}),
					layout.Rigid(func(gtx C) D {
						return gui.Tile{Icon: gui.IconLanguages, Label: "Наведення", Force: gui.ForceHover}.Layout(gtx, th, g.click("tl.2"))
					}),
					layout.Rigid(func(gtx C) D {
						return gui.Tile{Icon: gui.IconImage, Label: "Вимкнено", Disabled: true}.Layout(gtx, th, g.click("tl.3"))
					}),
				)
			},
			gui.StatTile{Icon: gui.IconClapperboard, Label: "Сцен", Value: "8", Caption: "по 8 с"}.Widget(th),
			func(gtx C) D {
				return gui.Columns{Gap: gui.Sp12}.Layout(gtx,
					gui.Col{Span: 1, W: gui.StatTile{Icon: gui.IconTimer, Tone: gui.ToneRunning, Label: "Хронометраж", Value: "1:04"}.Widget(th)},
					gui.Col{Span: 1, W: gui.StatTile{Icon: gui.IconCheckCircle, Tone: gui.ToneSuccess, Label: "Готово", Value: "6/8"}.Widget(th)},
				)
			},
		)
	})
}

func (g *gallery) cardNext(gtx C) D {
	th := g.th
	return gui.VStack(gtx, gui.Sp24,
		func(gtx C) D {
			return gui.NextStepCard{Icon: gui.IconSprout, Title: "Наступний крок: план",
				Text: "Студія розкладе історію на 8 сцен і запропонує акторів."}.Layout(gtx, th, g.click("ns.1"))
		},
		func(gtx C) D {
			return gui.NextStepCard{Icon: gui.IconWandSparkles, Title: "Генеруються кадри", Text: "Наведення: стрілка зсувається, картка піднімається.",
				Busy: true, Force: gui.ForceHover}.Layout(gtx, th, g.click("ns.2"))
		},
		func(gtx C) D {
			return gui.TipCard{Icon: gui.IconLightbulb, Title: "Порада", Text: "Короткі речення звучать природніше: до 20 слів на сцену.",
				Action: "Показати приклад"}.Layout(gtx, th, g.click("tip.1"))
		},
	)
}

func (g *gallery) cardQueue(gtx C) D {
	th := g.th
	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp4,
			gui.Text{Style: th.H3}.Widget(th, "Останні дії"),
			func(gtx C) D {
				return gui.ActivityRow{Icon: gui.IconDownload, Tone: gui.ToneAction, Solid: true, Title: "Відео готове",
					Meta: "Сьогодні · Лисиця і місяць", Progress: -1, Value: "1:04", Divider: true}.Layout(gtx, th, g.click("ar.1"))
			},
			func(gtx C) D {
				return gui.ActivityRow{Icon: gui.IconWandSparkles, Tone: gui.ToneRunning, Title: "Кадри сцен 5–8",
					Meta: "Генерується · ~2 хв", Progress: 0.35, Value: "35%", Divider: true, Force: gui.ForceHover}.Layout(gtx, th, g.click("ar.2"))
			},
			func(gtx C) D {
				return gui.ActivityRow{Icon: gui.IconAlertTriangle, Tone: gui.ToneError, Title: "Озвучка сцени 3",
					Meta: "Помилка: ліміт ElevenLabs", Progress: -1, Divider: true,
					Trailing: func(gtx C) D {
						return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Text: "Повторити", Icon: gui.IconRotateCcw}.Layout(gtx, th, g.click("ar.retry"))
					}}.Layout(gtx, th, g.click("ar.3"))
			},
			func(gtx C) D {
				return gui.ActivityRow{Icon: gui.IconCalendar, Title: "Заплановано", Meta: "Завтра · Космічна одіссея", Progress: -1,
					Trailing: gui.StatusPill{State: gui.StateQueued, Small: true}.Widget(th)}.Layout(gtx, th, nil)
			},
		)
	})
}

func (g *gallery) cardProgress(gtx C) D {
	th := g.th
	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp16,
			gui.Text{Style: th.H3}.Widget(th, "Прогрес"),
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(gui.ProgressRing{Value: 0.72, Caption: "готово", Size: 120}.Widget(th)),
					layout.Rigid(layout.Spacer{Width: gui.Sp20}.Layout),
					layout.Flexed(1, func(gtx C) D {
						return gui.VStack(gtx, gui.Sp4,
							gui.Text{Style: th.H3}.Widget(th, "6 з 8 сцен"),
							gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, "Ще трохи — і можна монтувати."),
						)
					}),
				)
			},
			func(gtx C) D {
				return layout.Flex{Spacing: layout.SpaceBetween, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(gui.ProgressRing{Value: 0.3, Size: 56, Stroke: 6}.Widget(th)),
					layout.Rigid(gui.ProgressRing{Value: 1, Size: 56, Stroke: 6, Tone: gui.ToneSuccess}.Widget(th)),
					layout.Rigid(gui.ProgressRing{Value: 0.55, Size: 56, Stroke: 6, Tone: gui.ToneRunning}.Widget(th)),
					layout.Rigid(gui.ProgressRing{Value: -1, Size: 40, Stroke: 4, Label: "-"}.Widget(th)),
				)
			},
			gui.ProgressBar{Value: 0.64}.Widget(th),
			gui.ProgressBar{Value: 0.4, Tone: gui.ToneRunning}.Widget(th),
			gui.ProgressBar{Value: 1, Tone: gui.ToneSuccess}.Widget(th),
			gui.ProgressBar{Value: 0.25, Tone: gui.ToneError, Height: 6}.Widget(th),
			gui.ProgressBar{Value: -1, Height: 6}.Widget(th),
		)
	})
}

func (g *gallery) cardEmpty(gtx C) D {
	th := g.th
	return gui.Card{Padding: gui.Sp32}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.N.Layout(gtx, func(gtx C) D {
			return gui.EmptyState{Icon: gui.IconClapperboard, Title: "Тут з'являться твої проєкти",
				Text: "Встав історію на головній — студія зробить із неї відео.", Action: "Нова історія", ActionIcon: gui.IconPlus}.Layout(gtx, th, g.click("empty.new"))
		})
	})
}

func (g *gallery) cardMedia(gtx C) D {
	th := g.th
	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp16,
			gui.Text{Style: th.H3}.Widget(th, "Медіа"),
			func(gtx C) D {
				return gui.Grid{MinWidth: 90, Gap: gui.Sp12}.Layout(gtx, 4, func(gtx C, i int) D {
					var w layout.Widget
					switch i {
					case 0:
						w = gui.Thumb{Image: g.image(2), Ratio: 1}.Widget(th)
					case 1:
						w = gui.Thumb{Image: g.image(1), Ratio: 1, Fit: gui.FitContain}.Widget(th)
					case 2:
						w = gui.Thumb{Ratio: 1}.Widget(th)
					default:
						w = gui.Thumb{Ratio: 1, Loading: true}.Widget(th)
					}
					label := []string{"Cover", "Contain", "Порожньо", "Завантаження"}[i]
					return g.labeled(label, w)(gtx)
				})
			},
			func(gtx C) D {
				return st[gui.AudioPill](g, "ap.1").Layout(gtx, th, gui.AudioStyle{Src: "voice-1.mp3", Label: "Оповідач · сцена 1", Duration: 8 * time.Second, Backend: silentAudio{}})
			},
			func(gtx C) D {
				return st[gui.AudioPill](g, "ap.2").Layout(gtx, th, gui.AudioStyle{Src: "voice-2.mp3", Label: "Лисиця", Duration: 6 * time.Second,
					Playing: true, Elapsed: 2500 * time.Millisecond})
			},
			func(gtx C) D {
				return gui.HStack(gtx, gui.Sp8,
					gui.Avatar{Name: "Олена Коваль"}.Widget(th),
					gui.Avatar{Name: "Тарас", Tone: gui.ToneRunning}.Widget(th),
					gui.Avatar{Name: "Міра Лис", Tone: gui.ToneSuccess}.Widget(th),
					func(gtx C) D { img := g.image(3); return gui.Avatar{Name: "Кадр", Image: &img}.Layout(gtx, th) },
				)
			},
		)
	})
}

func (g *gallery) cardSkeleton(gtx C) D {
	th := g.th
	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp16,
			gui.Text{Style: th.H3}.Widget(th, "Завантаження"),
			func(gtx C) D { return gui.Skeleton{Height: 120, Radius: gui.RadiusInner}.Layout(gtx, th) },
			func(gtx C) D { return gui.SkeletonLines(gtx, th, 3) },
		)
	})
}

// silentAudio is a do-nothing AudioBackend so the idle pill shows enabled.
type silentAudio struct{}

func (silentAudio) Play(string, func()) error { return nil }
func (silentAudio) Stop()                     {}
func (silentAudio) Playing(string) bool       { return false }
