package main

import (
	"fmt"
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"

	"ytcartell/internal/gui"
)

func (g *gallery) foundations(gtx C) D {
	return g.pageFrame(gtx, "Основи", "Кольори, шрифт Fira Sans, відступи, радіуси й тіні — токени з GUI.md.", func(gtx C) D {
		return gui.Columns{}.Layout(gtx,
			gui.Col{Span: 6, W: func(gtx C) D {
				return gui.VStack(gtx, gui.Sp24, g.section("Кольори", "th.Pal", g.palette), g.section("Відступи й радіуси", "Sp4…Sp56 · Radius*", g.spacing))
			}},
			gui.Col{Span: 6, W: func(gtx C) D {
				return gui.VStack(gtx, gui.Sp24, g.section("Типографіка", "Fira Sans · th.Display … th.Caption", g.typeScale), g.section("Тіні", "ElevControl · Card · Hover · Pop · Sheet", g.elevations))
			}},
		)
	})
}

func (g *gallery) palette(gtx C) D {
	th := g.th
	p := th.Pal
	type tok struct {
		name string
		c    color.NRGBA
	}
	toks := []tok{
		{"Ground", p.Ground}, {"Surface", p.Surface}, {"SurfaceHover", p.SurfaceHover}, {"Card", p.Card},
		{"Border", p.Border}, {"BorderStrong", p.BorderStrong}, {"Text", p.Text}, {"Text2", p.Text2},
		{"Text3", p.Text3}, {"Action", p.Action}, {"ActionHover", p.ActionHover}, {"ActionPressed", p.ActionPressed},
		{"ActionSoft", p.ActionSoft}, {"ActionText", p.ActionText}, {"Success", p.Success}, {"SuccessSoft", p.SuccessSoft},
		{"Running", p.Running}, {"RunningSoft", p.RunningSoft}, {"Warning", p.Warning}, {"WarningSoft", p.WarningSoft},
		{"Error", p.Error}, {"ErrorSoft", p.ErrorSoft}, {"Track", p.Track}, {"TooltipBg", p.TooltipBg},
	}
	return gui.Grid{MinWidth: 150, Gap: gui.Sp12}.Layout(gtx, len(toks), func(gtx C, i int) D {
		t := toks[i]
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				sz := gtx.Dp(36)
				gui.Box(gtx, image.Rect(0, 0, sz, sz), 10, t.c, th.Pal.Border)
				return D{Size: image.Pt(sz, sz)}
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return gui.VStack(gtx, 0,
					gui.Text{Style: th.Small.Weighted(gui.FontMedium), MaxLines: 1}.Widget(th, t.name),
					gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, fmt.Sprintf("#%02X%02X%02X", t.c.R, t.c.G, t.c.B)),
				)
			}),
		)
	})
}

func (g *gallery) typeScale(gtx C) D {
	th := g.th
	rows := []struct {
		name string
		st   gui.TextStyle
		txt  string
	}{
		{"Display · 40 SemiBold", th.Display, "Що знімемо сьогодні?"},
		{"H1 · 32 SemiBold", th.H1, "Лисиця і місяць"},
		{"H2 · 24 SemiBold", th.H2, "Мої проєкти"},
		{"H3 · 18 SemiBold", th.H3, "Швидкі дії"},
		{"Body · 15", th.Body, "Встав історію — студія зробить план, кадри, озвучку й монтаж."},
		{"BodyStrong · 15 SemiBold", th.BodyStrong, "Готово до монтажу"},
		{"Label · 14 Medium", th.Label, "Голос оповідача"},
		{"Small · 13", th.Small, "8 сцен по 8 с · 16:9"},
		{"Caption · 12", th.Caption, "Оновлено щойно"},
	}
	var ch []layout.FlexChild
	for i, r := range rows {
		r := r
		if i > 0 {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout))
		}
		ch = append(ch, layout.Rigid(func(gtx C) D {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					gtx.Constraints.Min.X, gtx.Constraints.Max.X = gtx.Dp(190), gtx.Dp(190)
					return gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Layout(gtx, th, r.name)
				}),
				layout.Flexed(1, gui.Text{Style: r.st, MaxLines: 1}.Widget(th, r.txt)),
			)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

func (g *gallery) elevations(gtx C) D {
	th := g.th
	els := []struct {
		name string
		e    gui.Elevation
	}{
		{"Control", gui.ElevControl}, {"Card", gui.ElevCard}, {"Hover", gui.ElevHover}, {"Pop", gui.ElevPop}, {"Sheet", gui.ElevSheet},
	}
	return layout.Inset{Top: gui.Sp8, Bottom: gui.Sp24}.Layout(gtx, func(gtx C) D {
		return gui.Grid{MinWidth: 96, Gap: gui.Sp24}.Layout(gtx, len(els), func(gtx C, i int) D {
			w := gtx.Constraints.Max.X
			h := gtx.Dp(76)
			r := image.Rect(0, 0, w, h)
			gui.Shadow(gtx, th, r, gui.RadiusInner, els[i].e)
			gui.Box(gtx, r, gui.RadiusInner, th.Pal.Card, gui.WithAlpha(th.Pal.Border, 0.6))
			lg := gtx
			lg.Constraints = layout.Constraints{Max: image.Pt(w, h)}
			d, call := recordW(lg, gui.Text{Style: th.Label, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, els[i].name))
			o := op.Offset(image.Pt((w-d.Size.X)/2, (h-d.Size.Y)/2)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			o.Pop()
			return D{Size: image.Pt(w, h)}
		})
	})
}

func (g *gallery) spacing(gtx C) D {
	th := g.th
	sps := []unit.Dp{gui.Sp4, gui.Sp8, gui.Sp12, gui.Sp16, gui.Sp20, gui.Sp24, gui.Sp32, gui.Sp40, gui.Sp56}
	radii := []struct {
		name string
		r    unit.Dp
	}{{"Small 8", gui.RadiusSmall}, {"Control 12", gui.RadiusControl}, {"Inner 16", gui.RadiusInner}, {"Card 22", gui.RadiusCard}, {"Pill", gui.RadiusPill}}
	return gui.VStack(gtx, gui.Sp20,
		func(gtx C) D {
			var ch []layout.FlexChild
			for _, s := range sps {
				s := s
				ch = append(ch, layout.Rigid(func(gtx C) D {
					return layout.Inset{Right: gui.Sp16}.Layout(gtx, func(gtx C) D {
						return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx C) D {
								sz := gtx.Dp(s)
								box := gtx.Dp(56)
								gui.FillRRect(gtx, image.Rect((box-sz)/2, box-sz, (box+sz)/2, box), 4, th.Pal.ActionSoft)
								return D{Size: image.Pt(box, box)}
							}),
							layout.Rigid(gui.Text{Style: th.Caption, Color: th.Pal.Text3}.Widget(th, fmt.Sprint(int(s)))),
						)
					})
				}))
			}
			return layout.Flex{Alignment: layout.End}.Layout(gtx, ch...)
		},
		func(gtx C) D {
			return gui.Grid{MinWidth: 100, Gap: gui.Sp16}.Layout(gtx, len(radii), func(gtx C, i int) D {
				w := gtx.Constraints.Max.X
				h := gtx.Dp(64)
				gui.Box(gtx, image.Rect(0, 0, w, h), radii[i].r, th.Pal.Surface, th.Pal.BorderStrong)
				lg := gtx
				lg.Constraints = layout.Constraints{Max: image.Pt(w, h)}
				d, call := recordW(lg, gui.Text{Style: th.Caption, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, radii[i].name))
				o := op.Offset(image.Pt((w-d.Size.X)/2, (h-d.Size.Y)/2)).Push(gtx.Ops)
				call.Add(gtx.Ops)
				o.Pop()
				return D{Size: image.Pt(w, h)}
			})
		},
	)
}

func (g *gallery) icons(gtx C) D {
	th := g.th
	return g.pageFrame(gtx, "Іконки", fmt.Sprintf("Lucide (ISC) — %d іконок, штрих 1.75 при 24 dp, круглі кінці. gui.Icon<Name>.Layout(gtx, size, color).", len(gui.AllIcons)), func(gtx C) D {
		return gui.VStack(gtx, gui.Sp24,
			func(gtx C) D {
				return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
					sizes := []unit.Dp{16, 20, 24, 32, 48}
					var ws []layout.Widget
					for _, s := range sizes {
						s := s
						ws = append(ws, g.labeled(fmt.Sprintf("%v dp", int(s)), func(gtx C) D {
							return gui.HStack(gtx, gui.Sp8, gui.IconSparkles.Widget(s, th.Pal.Text), gui.IconRocket.Widget(s, th.Pal.ActionText), gui.IconHeart.Widget(s, th.Pal.Error))
						}))
					}
					return gui.Flow{Gap: gui.Sp40}.Layout(gtx, ws...)
				})
			},
			func(gtx C) D {
				return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
					return gui.Grid{MinWidth: 96, Gap: gui.Sp4}.Layout(gtx, len(gui.AllIcons), func(gtx C, i int) D {
						ic := gui.AllIcons[i]
						return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
							layout.Rigid(ic.Widget(24, th.Pal.Text)),
							layout.Rigid(layout.Spacer{Height: 6}.Layout),
							layout.Rigid(gui.Text{Style: th.Caption.Sized(11), Color: th.Pal.Text3, MaxLines: 1, Align: text.Middle}.Widget(th, ic.Name)),
						)
					})
				})
			},
		)
	})
}

// recordW records a widget (gallery-side twin of the kit's helper).
func recordW(gtx C, w layout.Widget) (D, op.CallOp) {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	return d, m.Stop()
}
