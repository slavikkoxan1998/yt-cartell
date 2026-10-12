package home

import (
	"image"
	"sort"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// Filters of the Проєкти page.
const (
	filterAll = iota
	filterWork
	filterDone
	filterErr
)

var filterNames = [4]string{"Усі", "У роботі", "Готові", "З помилками"}

type projState struct {
	scroll  gui.Scroll
	search  gui.SearchField
	filter  int
	fclicks [4]gui.Clickable
	sort    gui.Segmented
	sortBy  string // "new" | "name"
	newBtn  gui.Clickable
	newTile gui.Clickable
	newHov  gui.Anim
	fix     gui.Clickable
	stagger gui.Stagger
	coll    *collate.Collator
	cache   projView
}

// matches reports whether a project with status st passes filter f.
func matches(f int, st status) bool {
	switch f {
	case filterWork:
		return st != stDone
	case filterDone:
		return st == stDone
	case filterErr:
		return st == stError
	}
	return true
}

// Projects draws the Проєкти page: every project with search, filters and
// sorting, in a grid wider than Home's.
func (p *Page) Projects(gtx C, c *core.Ctx) D {
	th := c.Th
	s := &p.proj
	entered := p.entered(c)
	p.svc.refresh(gtx, c, entered)
	p.dispatch(gtx, c)
	if s.newBtn.Clicked(gtx) {
		c.OpenSheet(core.Sheet{Kind: core.SheetNewProject})
	}
	if s.fix.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageSettings, Section: "connections"})
	}
	for i := range s.fclicks {
		if s.fclicks[i].Clicked(gtx) {
			s.filter = i
			s.scroll.List.Position = layout.Position{}
		}
	}
	if v, ok := s.sort.Changed(gtx); ok {
		s.sortBy = v
	}
	if s.sortBy == "" {
		s.sortBy = "new"
	}
	minuteTick(gtx)

	list := c.List()
	query := strings.ToLower(strings.TrimSpace(s.search.Query()))
	items, counts := s.view(c, list, query)

	pad := PagePad(gtx, th)
	inner := gtx.Constraints.Max.X - 2*gtx.Dp(pad)
	rows := []layout.Widget{
		func(gtx C) D { return p.projHeader(gtx, c) },
		func(gtx C) D { return p.toolbar(gtx, c, counts) },
	}
	switch {
	case len(list) == 0:
		rows = append(rows, func(gtx C) D {
			return emptyCard(gtx, th, gui.EmptyState{Icon: gui.IconClapperboard, Title: "Тут з'являться твої проєкти",
				Text: "Кожне відео — окремий проєкт. Створи перший: встав історію, і студія проведе тебе крок за кроком."},
				gui.Button{Icon: gui.IconPlus, Text: "Створити проєкт"}, &s.newBtn)
		})
	case len(items) == 0:
		rows = append(rows, func(gtx C) D {
			return emptyCard(gtx, th, gui.EmptyState{Icon: gui.IconSearch, Title: "Нічого не знайдено",
				Text: "Жоден проєкт не підходить під цей фільтр чи запит. Зміни його — або обери «Усі»."}, gui.Button{}, nil)
		})
	default:
		// «Створити проєкт» leads the full list (not a filtered or searched one).
		lead := 0
		if s.filter == filterAll && query == "" {
			lead = 1
		}
		n := len(items) + lead
		cols := projectCols(gtx, inner, n)
		rows = append(rows, gridRows(n, cols, &s.stagger, func(gtx C, i int) D {
			if i < lead {
				return p.newTile(gtx, c, cols == 1)
			}
			return p.projectCard(gtx, c, items[i-lead])
		})...)
	}
	d := pageList(gtx, th, &s.scroll, pad, rows)
	p.pop.Layout(gtx, th)
	return d
}

// projHeader is the title and the service status chips (a new project
// starts from the grid's first tile, the rail's "+" or Ctrl+N).
func (p *Page) projHeader(gtx C, c *core.Ctx) D {
	th := c.Th
	s := &p.proj
	top := func(gtx C) D {
		return gui.VStack(gtx, gui.Sp4,
			gui.Text{Style: th.H1, MaxLines: 1}.Widget(th, "Проєкти"),
			gui.Text{Style: th.Body, Color: th.Pal.Text2}.Widget(th, "Кожне відео — окремий проєкт. Його можна закрити на будь-якому кроці й повернутися пізніше."),
		)
	}
	chips := p.svc.chips(c)
	bad := false
	phone := gtx.Constraints.Max.X < gtx.Dp(640)
	var ws []layout.Widget
	for _, ch := range chips {
		ch := ch
		bad = bad || ch.state == svcBad
		if phone && ch.state != svcBad {
			continue // phones list only what needs attention
		}
		ws = append(ws, func(gtx C) D {
			return gui.Chip{Text: ch.name + " · " + ch.hint, Dot: ch.state.dot(th), Small: true}.Layout(gtx, th, nil)
		})
	}
	if bad {
		ws = append(ws, func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconSettings, Text: "Налаштувати"}.Layout(gtx, th, &s.fix)
		})
	}
	status := func(gtx C) D { return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...) }
	if len(ws) == 0 {
		return top(gtx)
	}
	return gui.VStack(gtx, gui.Sp16, top, status)
}

// toolbar holds the filter chips, the search and the sort switch.
func (p *Page) toolbar(gtx C, c *core.Ctx, counts [4]int) D {
	th := c.Th
	s := &p.proj
	chips := func(gtx C) D {
		var ws []layout.Widget
		for i := range filterNames {
			i := i
			ws = append(ws, func(gtx C) D {
				return gui.Chip{Text: filterNames[i] + " · " + itoa(counts[i]), Selected: s.filter == i}.Layout(gtx, th, &s.fclicks[i])
			})
		}
		return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
	}
	sorter := func(gtx C) D {
		return s.sort.Layout(gtx, th, s.sortBy, []gui.SegOption{
			{Value: "new", Label: "Нові", Icon: gui.IconClock},
			{Value: "name", Label: "Назва", Icon: gui.IconText},
		})
	}
	search := func(gtx C) D { return s.search.Layout(gtx, th, "Пошук за назвою") }
	if gtx.Constraints.Max.X < gtx.Dp(640) {
		// Phones: each control gets the whole width.
		return gui.VStack(gtx, gui.Sp12, search, chips, func(gtx C) D { return layout.W.Layout(gtx, sorter) })
	}
	if gtx.Constraints.Max.X < gtx.Dp(980) {
		return gui.VStack(gtx, gui.Sp12, search, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, chips),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Rigid(sorter),
			)
		})
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, chips),
		layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
		layout.Rigid(func(gtx C) D {
			w := min(gtx.Constraints.Max.X, gtx.Dp(320))
			gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
			return search(gtx)
		}),
		layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
		layout.Rigid(sorter),
	)
}

// projView is the filtered, searched and sorted list, cached per engine
// snapshot: core hands out a new list slice after every engine change, so
// an unchanged slice (and filter, query, order) means nothing to redo.
type projView struct {
	list   []studio.Summary
	filter int
	query  string
	sortBy string
	items  []studio.Summary
	counts [4]int
	ok     bool
}

func sameList(a, b []studio.Summary) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// view returns the projects to show and the count of every filter.
func (s *projState) view(c *core.Ctx, list []studio.Summary, query string) ([]studio.Summary, [4]int) {
	v := &s.cache
	if v.ok && sameList(v.list, list) && v.filter == s.filter && v.query == query && v.sortBy == s.sortBy {
		return v.items, v.counts
	}
	*v = projView{list: list, filter: s.filter, query: query, sortBy: s.sortBy, ok: true}
	for _, x := range list {
		st := statusOf(c, x)
		for f := range v.counts {
			if matches(f, st) {
				v.counts[f]++
			}
		}
		if !matches(s.filter, st) || (query != "" && !strings.Contains(strings.ToLower(x.Title), query)) {
			continue
		}
		v.items = append(v.items, x)
	}
	if s.sortBy == "name" {
		if s.coll == nil {
			s.coll = collate.New(language.Ukrainian, collate.IgnoreCase)
		}
		sort.SliceStable(v.items, func(i, j int) bool {
			return s.coll.CompareString(strings.TrimSpace(v.items[i].Title), strings.TrimSpace(v.items[j].Title)) < 0
		})
	}
	return v.items, v.counts
}

// newTile is the first cell of the grid: «Створити проєкт» as a card of the
// same size as the projects — an action-tinted picture area with a round
// "+", a title and a line of what happens next. In a single column (phones)
// it is a compact row, so the projects are not pushed off the screen.
func (p *Page) newTile(gtx C, c *core.Ctx, compact bool) D {
	th := c.Th
	s := &p.proj
	if s.newTile.Clicked(gtx) {
		c.OpenSheet(core.Sheet{Kind: core.SheetNewProject})
	}
	s.newHov.Duration = gui.DurHover
	hv := s.newHov.Animate(gtx, b2f(s.newTile.Hovered()))
	plus := func(gtx C, size unit.Dp) D {
		d := gtx.Dp(size)
		gui.FillCircle(gtx, size, gui.LerpColor(th.Pal.Action, th.Pal.ActionHover, hv))
		ic := gtx.Dp(size * 0.47)
		o := op.Offset(image.Pt((d-ic)/2, (d-ic)/2)).Push(gtx.Ops)
		gui.IconPlus.LayoutStroke(gtx, size*0.47, 2.5, th.Pal.OnAction)
		o.Pop()
		return D{Size: image.Pt(d, d)}
	}
	texts := func(gtx C) D {
		return gui.VStack(gtx, 2,
			gui.Text{Style: th.BodyStrong.Sized(16), MaxLines: 1}.Widget(th, "Створити проєкт"),
			gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, "Встав історію — студія проведе крок за кроком або зробить усе автопілотом."),
		)
	}
	if compact {
		return gui.Card{Click: &s.newTile, Padding: gui.Sp16}.Layout(gtx, th, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return plus(gtx, 48) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
				layout.Flexed(1, texts),
			)
		})
	}
	return gui.Card{Click: &s.newTile, Padding: gui.Sp12, Tip: "Ctrl+N"}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp12,
			func(gtx C) D {
				w := gtx.Constraints.Max.X
				h := int(float32(w)/1.6 + 0.5)
				gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusInner, gui.LerpColor(th.Pal.ActionSoft, th.Pal.ActionSoftHi, hv))
				d := gtx.Dp(64)
				o := op.Offset(image.Pt((w-d)/2, (h-d)/2)).Push(gtx.Ops)
				plus(gtx, 64)
				o.Pop()
				return D{Size: image.Pt(w, h)}
			},
			func(gtx C) D {
				return layout.Inset{Left: gui.Sp4, Right: gui.Sp4, Bottom: gui.Sp4}.Layout(gtx, texts)
			},
		)
	})
}

// projectCols is the column count of the Проєкти grid. Wide windows get
// big cards (≈ 570 dp, three across at 1920) that wrap into more rows and
// fill the page's height; narrower ones keep the fluid rule.
func projectCols(gtx C, w, n int) int {
	gap := gtx.Dp(gui.Sp24)
	if big := (w + gap + (gtx.Dp(570)+gap)/2) / (gtx.Dp(570) + gap); big >= 3 {
		return big
	}
	return fluidCols(gtx, w, n, cardMinWidth, cardMaxWidth)
}
