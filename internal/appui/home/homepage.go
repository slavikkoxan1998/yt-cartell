package home

import (
	"fmt"
	"image"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// Home draws Головна across the whole content area (it scrolls itself).
//
// Wide windows (≥ 1100 dp of content): 8 + 4 columns — the story card and
// «Мої проєкти» on the left, the next step, the queue, the tip and the
// quick actions on the right, so both columns run down the page together.
// Narrower windows stack: the story, next step + queue, the projects, then
// the tip and the quick actions.
func (p *Page) Home(gtx C, c *core.Ctx) D {
	th := c.Th
	entered := p.entered(c)
	p.svc.refresh(gtx, c, entered)
	p.dispatch(gtx, c)
	h := &p.home
	for i := range h.tiles {
		if h.tiles[i].Clicked(gtx) {
			p.tileAction(c, i)
		}
	}
	query := strings.TrimSpace(h.search.Query())
	if h.all.Clicked(gtx) {
		// A search carries over to the full list.
		p.proj.search.SetText(query)
		c.Navigate(core.Route{Page: core.PageProjects})
	}
	if h.setup.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageSettings, Section: "connections"})
	}
	if h.assist.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageSettings, Section: "assistant"})
	}
	if h.tour.Clicked(gtx) {
		c.StartTour()
	}
	minuteTick(gtx)

	pad := PagePad(gtx, th)
	inner := gtx.Constraints.Max.X - 2*gtx.Dp(pad)
	narrow := narrowWindow(gtx, th)
	list := c.List()

	heroW := func(gtx C) D { return p.heroCard(gtx, c) }
	nextW := func(gtx C) D { return p.nextCard(gtx, c, list) }
	queueW := func(gtx C) D { return p.queueCard(gtx, c) }
	quickW := func(gtx C) D { return p.quickCard(gtx, c) }
	var tipW layout.Widget
	if !c.E.Config().Get().UI.TipsHidden {
		tipW = func(gtx C) D { return p.tipCard(gtx, c) }
	}
	setupW, setup := p.setupCard(c)

	rows := []layout.Widget{func(gtx C) D { return p.greeting(gtx, c, narrow) }}
	if inner >= gtx.Dp(1100) {
		var side []layout.Widget
		if setup {
			side = append(side, setupW)
		}
		side = append(side, nextW, queueW)
		if tipW != nil {
			side = append(side, tipW)
		}
		side = append(side, quickW)
		rows = append(rows, func(gtx C) D {
			return gui.Columns{Gap: gui.Sp24}.Layout(gtx,
				gui.Col{Span: 8, W: func(gtx C) D {
					ws := append([]layout.Widget{heroW}, p.homeProjects(gtx, c, list, query, gtx.Constraints.Max.X, false)...)
					return gui.VStack(gtx, gui.Sp24, ws...)
				}},
				gui.Col{Span: 4, W: func(gtx C) D { return gui.VStack(gtx, gui.Sp24, side...) }},
			)
		})
	} else {
		pair := func(ws ...layout.Widget) layout.Widget {
			return func(gtx C) D {
				return gui.Grid{MinWidth: 300, Gap: gui.Sp24, Stretch: true}.Layout(gtx, len(ws), func(gtx C, i int) D { return ws[i](gtx) })
			}
		}
		rows = append(rows, heroW)
		if setup {
			rows = append(rows, setupW)
		}
		rows = append(rows, pair(nextW, queueW))
		rows = append(rows, p.homeProjects(gtx, c, list, query, inner, true)...)
		if tipW != nil {
			rows = append(rows, pair(tipW, quickW))
		} else {
			rows = append(rows, quickW)
		}
	}
	d := pageList(gtx, th, &h.scroll, pad, rows)
	p.pop.Layout(gtx, th)
	p.hero.edit.Layout(gtx, c, &p.hero)
	return d
}

// homeProjects is «Мої проєкти»: the heading and up to two rows of the
// newest projects (three rows of matches while searching), in fluid
// columns that fill the width w. With rows every grid row is a widget of
// its own (the stacked layout virtualises them); otherwise the grid is one.
func (p *Page) homeProjects(gtx C, c *core.Ctx, list []studio.Summary, query string, w int, rows bool) []layout.Widget {
	th := c.Th
	h := &p.home
	const minW, maxW = unit.Dp(240), cardMaxWidth
	hi := fluidCols(gtx, w, 0, minW, maxW)
	shown, limit := list, 2*hi
	if query != "" {
		shown, limit = search(list, query), 3*hi
	}
	found := len(shown)
	if len(shown) > limit {
		shown = shown[:limit]
	}
	head := func(gtx C) D {
		title, count := "Мої проєкти", ""
		switch {
		case query != "":
			title, count = "Знайдено", itoa(found)
		case len(list) > 0:
			count = itoa(len(list))
		}
		return sectionHead(gtx, th, title, count, "Усі проєкти", &h.all)
	}
	var body []layout.Widget
	switch {
	case len(list) == 0:
		body = append(body, func(gtx C) D {
			return emptyCard(gtx, th, gui.EmptyState{Icon: gui.IconClapperboard, Title: "Тут з'являться твої проєкти",
				Text: "Встав історію в картку «Нова історія» й натисни «Автопілот» — або пройди п'ять кроків разом зі студією."},
				gui.Button{Kind: gui.Secondary, Text: "Як це працює", Icon: gui.IconCompass}, &h.tour)
		})
	case len(shown) == 0:
		body = append(body, func(gtx C) D {
			return emptyCard(gtx, th, gui.EmptyState{Icon: gui.IconSearch, Title: "Нічого не знайдено",
				Text: "Немає проєкту з такою назвою. Спробуй інше слово."}, gui.Button{}, nil)
		})
	default:
		cols := fluidCols(gtx, w, len(shown), minW, maxW)
		body = gridRows(len(shown), cols, &h.stagger, func(gtx C, i int) D { return p.projectCard(gtx, c, shown[i]) })
	}
	if rows {
		return append([]layout.Widget{head}, body...)
	}
	return []layout.Widget{head, func(gtx C) D { return gui.VStack(gtx, gui.Sp24, body...) }}
}

// greeting is the page head: the display title greeting by the time of day
// with the studio's short orange dash under it, and the project search.
func (p *Page) greeting(gtx C, c *core.Ctx, narrow bool) D {
	th := c.Th
	h := &p.home
	title := gui.Text{Style: th.Display, MaxLines: 3}
	dashW, dashH := unit.Dp(56), unit.Dp(6)
	if narrow {
		title.Style = th.H2 // two lines on a phone
		dashW, dashH = 44, 5
	}
	left := func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(title.Widget(th, greetingFor(gtx.Now.Local())+"! Що знімемо сьогодні?")),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: gui.Sp12}.Layout(gtx, func(gtx C) D {
					r := image.Rect(0, 0, gtx.Dp(dashW), gtx.Dp(dashH))
					gui.FillRRect(gtx, r, dashH/2, th.Pal.Action)
					return D{Size: r.Max}
				})
			}),
		)
	}
	search := func(gtx C) D { return h.search.Layout(gtx, th, "Пошук проєктів") }
	if narrow {
		// Phones: the title and the search only (the theme is in Settings ›
		// Вигляд, the community link in Settings and on the desktop rail).
		return gui.VStack(gtx, gui.Sp16, left, search)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, left),
		layout.Rigid(layout.Spacer{Width: gui.Sp24}.Layout),
		layout.Rigid(func(gtx C) D {
			w := min(gtx.Constraints.Max.X, gtx.Dp(340))
			gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
			return search(gtx)
		}),
	)
}

// greetingFor greets by the time of day.
func greetingFor(t time.Time) string {
	switch h := t.Hour(); {
	case h >= 5 && h < 12:
		return "Доброго ранку"
	case h >= 12 && h < 18:
		return "Доброго дня"
	case h >= 18 && h < 23:
		return "Доброго вечора"
	}
	return "Доброї ночі"
}

// emptyCard centres an empty state in a card, with an optional action button
// under it (btn.Text "" = none). The kit's own action is always primary, so
// the button is drawn here in the kind the screen needs.
func emptyCard(gtx C, th *gui.Theme, e gui.EmptyState, btn gui.Button, click *gui.Clickable) D {
	return gui.Card{Padding: gui.Sp40}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D { return e.Layout(gtx, th, nil) }),
			layout.Rigid(func(gtx C) D {
				if btn.Text == "" || click == nil {
					return D{}
				}
				return layout.Inset{Top: gui.Sp24}.Layout(gtx, func(gtx C) D { return btn.Layout(gtx, th, click) })
			}),
		)
	})
}

// search keeps the projects whose title contains q (case-insensitive).
func search(list []studio.Summary, q string) []studio.Summary {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return list
	}
	var out []studio.Summary
	for _, s := range list {
		if strings.Contains(strings.ToLower(s.Title), q) {
			out = append(out, s)
		}
	}
	return out
}

// ToggleTheme switches between the light and the dark theme at once and
// saves the choice (Settings → Вигляд shows it).
func ToggleTheme(c *core.Ctx) {
	dark := !c.Th.Dark()
	c.Th.SetDark(dark)
	v := "light"
	if dark {
		v = "dark"
	}
	c.Background(func() {
		if err := c.E.Config().Update(func(cfg *config.Config) { cfg.UI.Theme = v }); err != nil {
			c.Error(fmt.Errorf("не вдалося зберегти тему: %w", err))
		}
	})
}
