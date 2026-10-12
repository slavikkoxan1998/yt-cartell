package pickers

// Catalog is the big preset chooser of Settings (models of «Своє API», TTS
// voices and models, text providers): a drawer with a search field, filter
// chips — one choice per group (service, mode) plus on/off switches (with
// sound, speaks Ukrainian, takes references) — and the entries, popular
// first, each with what it can do, a note and its source. A press on an entry
// picks it (Changed reports its ID once and the drawer closes); an entry with
// a Disabled reason only shows why and keeps its source link.
//
//	if s.cat.Clicked(gtx) { s.models.Open() }
//	if id, ok := s.models.Changed(); ok { apply(id) }
//	s.models.Layout(gtx, c, pickers.CatalogStyle{Title: "Каталог моделей", Items: items, Groups: groups})
//
// The list shows catalogPage entries at a time («Показати ще»): a drawer body
// is laid out whole every frame, and the search narrows a long list faster
// than scrolling.

import (
	"strconv"
	"strings"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
)

// catalogPage is how many entries one «Показати ще» adds.
const catalogPage = 60

// CatalogItem is one entry of a Catalog.
type CatalogItem struct {
	ID       string
	Title    string // "Kling 3.0 Pro · кадр → відео"
	Sub      string // "fal.ai · Kuaishou Kling · fal-ai/kling-video/…"
	Meta     string // "кліпи 3–15 с · 16:9, 9:16 · 1080p"
	Note     string // the catalogue's note
	Badges   []CatalogBadge
	Popular  bool
	Current  bool   // what Settings uses now (outlined)
	Disabled string // why it cannot be picked ("" = it can)
	// Tags are what the filters look for: "<group key>=<value>" for the groups,
	// "<toggle key>" for the switches.
	Tags   []string
	Search string // lower-case text the search looks in (Title and Sub are added)
	Link   string // the source page
}

// CatalogBadge is a small label on an entry ("звук", "українська").
type CatalogBadge struct {
	Text string
	Tone gui.Tone
}

// CatalogGroup is one row of single-choice filter chips («Усі» first).
type CatalogGroup struct {
	Key, Label string
	Options    []CatalogOption
}

// CatalogOption is one chip of a group.
type CatalogOption struct{ Value, Label string }

// CatalogToggle is an on/off filter chip: on shows only entries tagged Key.
type CatalogToggle struct {
	Key, Label string
	Icon       *gui.Icon
}

// CatalogStyle configures a Catalog for one frame.
type CatalogStyle struct {
	Title, Subtitle string
	Placeholder     string // search placeholder
	Items           []CatalogItem
	Groups          []CatalogGroup
	Toggles         []CatalogToggle
	Footer          string // a quiet line under the list (sources, date)
}

// Catalog is the state of one catalogue chooser.
type Catalog struct {
	drawer gui.Drawer
	search gui.SearchField
	sel    map[string]string // group key → value ("" = all)
	on     map[string]bool
	chips  map[string]*gui.Clickable
	rows   map[string]*gui.Clickable
	links  map[string]*gui.Clickable
	more   gui.Clickable
	reset  gui.Clickable
	fold   gui.Clickable // «Фільтри» on a narrow window
	unfold bool
	limit  int
	focus  bool
	picked catalogPick
}

type catalogPick struct {
	id  string
	set bool
}

// Open shows the chooser (filters kept from the last time, the list from the top).
func (s *Catalog) Open() {
	s.drawer.Open()
	s.limit = catalogPage
	s.focus = true
}

// Close hides it.
func (s *Catalog) Close() { s.drawer.Close() }

// IsOpen reports whether it is open.
func (s *Catalog) IsOpen() bool { return s.drawer.IsOpen() }

// Changed reports (once) the ID the user picked.
func (s *Catalog) Changed() (string, bool) {
	p := s.picked
	s.picked = catalogPick{}
	return p.id, p.set
}

// SetFilter selects a group value or switches a toggle (value "on"/"" for a
// toggle) — Settings preselects the current service; tests and screenshots
// use it too.
func (s *Catalog) SetFilter(key, value string) {
	s.init()
	s.sel[key] = value
}

// SetToggle switches a toggle filter.
func (s *Catalog) SetToggle(key string, on bool) {
	s.init()
	s.on[key] = on
}

// SetQuery fills the search field.
func (s *Catalog) SetQuery(q string) { s.search.SetText(q) }

func (s *Catalog) init() {
	if s.sel == nil {
		s.sel, s.on = map[string]string{}, map[string]bool{}
		s.chips, s.rows, s.links = map[string]*gui.Clickable{}, map[string]*gui.Clickable{}, map[string]*gui.Clickable{}
	}
	if s.limit == 0 {
		s.limit = catalogPage
	}
}

func clickable(m map[string]*gui.Clickable, key string) *gui.Clickable {
	c := m[key]
	if c == nil {
		c = new(gui.Clickable)
		m[key] = c
	}
	return c
}

// CatalogMatch reports whether it passes the filters: every selected group
// value, every switched-on toggle and every word of the query.
func CatalogMatch(it CatalogItem, sel map[string]string, on map[string]bool, words []string, skipGroup string) bool {
	for k, v := range sel {
		if v != "" && k != skipGroup && !hasTag(it.Tags, k+"="+v) {
			return false
		}
	}
	for k, v := range on {
		if v && !hasTag(it.Tags, k) {
			return false
		}
	}
	if len(words) > 0 {
		hay := it.Search + " " + strings.ToLower(it.Title+" "+it.Sub)
		for _, w := range words {
			if !strings.Contains(hay, w) {
				return false
			}
		}
	}
	return true
}

func hasTag(tags []string, t string) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}

// Layout queues the chooser above the window (draws nothing while closed).
func (s *Catalog) Layout(gtx C, c *core.Ctx, st CatalogStyle) {
	s.init()
	th := c.Th
	words := strings.Fields(strings.ToLower(s.search.Query()))
	// Presses first: chips, rows, links, «Показати ще».
	for _, g := range st.Groups {
		for _, o := range append([]CatalogOption{{Value: ""}}, g.Options...) {
			if clickable(s.chips, g.Key+"="+o.Value).Clicked(gtx) {
				s.sel[g.Key] = o.Value
				s.limit = catalogPage
			}
		}
	}
	for _, t := range st.Toggles {
		if clickable(s.chips, "~"+t.Key).Clicked(gtx) {
			s.on[t.Key] = !s.on[t.Key]
			s.limit = catalogPage
		}
	}
	if s.reset.Clicked(gtx) {
		clear(s.sel)
		clear(s.on)
		s.search.SetText("")
		words = nil
	}
	if s.fold.Clicked(gtx) {
		s.unfold = !s.unfold
	}
	if s.more.Clicked(gtx) {
		s.limit += catalogPage
	}
	var shown []int
	for i, it := range st.Items {
		if CatalogMatch(it, s.sel, s.on, words, "") {
			shown = append(shown, i)
		}
	}
	for _, i := range shown {
		it := st.Items[i]
		if it.Link != "" && clickable(s.links, it.ID).Clicked(gtx) {
			c.OpenPath(it.Link)
		}
		if it.Disabled == "" && clickable(s.rows, it.ID).Clicked(gtx) {
			s.picked = catalogPick{id: it.ID, set: true}
			s.drawer.Close()
		}
	}
	if s.focus {
		s.focus = false
		s.search.Focus(gtx)
	}
	filtered := len(words) > 0
	for _, v := range s.sel {
		filtered = filtered || v != ""
	}
	for _, v := range s.on {
		filtered = filtered || v
	}

	body := func(gtx C) D {
		ws := []layout.Widget{
			func(gtx C) D {
				ph := st.Placeholder
				if ph == "" {
					ph = "Пошук: назва, сервіс, модель"
				}
				return s.search.Layout(gtx, th, ph)
			},
		}
		// A narrow window (a phone) folds the filters behind «Фільтри».
		narrow := gtx.Constraints.Max.X < gtx.Dp(520)
		if narrow {
			active := 0
			for _, v := range s.sel {
				if v != "" {
					active++
				}
			}
			for _, v := range s.on {
				if v {
					active++
				}
			}
			text := "Фільтри"
			if active > 0 {
				text += " · " + strconv.Itoa(active)
			}
			icon := gui.IconChevronDown
			if s.unfold {
				icon = gui.IconChevronUp
			}
			ws = append(ws, func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconFilter, Trail: icon, Text: text}.Layout(gtx, th, &s.fold)
			})
		}
		showFilters := !narrow || s.unfold
		for _, g := range st.Groups {
			if showFilters {
				ws = append(ws, s.groupRow(th, st, g, words))
			}
		}
		if len(st.Toggles) > 0 && showFilters {
			ws = append(ws, func(gtx C) D {
				chips := make([]layout.Widget, 0, len(st.Toggles))
				for _, t := range st.Toggles {
					n := 0
					for _, it := range st.Items {
						if hasTag(it.Tags, t.Key) && CatalogMatch(it, s.sel, nil, words, "") {
							n++
						}
					}
					chips = append(chips, func(gtx C) D {
						return gui.Chip{Text: t.Label + " · " + strconv.Itoa(n), Icon: t.Icon, Selected: s.on[t.Key], Small: true,
							Disabled: n == 0 && !s.on[t.Key]}.Layout(gtx, th, clickable(s.chips, "~"+t.Key))
					})
				}
				return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, chips...)
			})
		}
		ws = append(ws, func(gtx C) D {
			line := "Знайдено " + strconv.Itoa(len(shown)) + " з " + strconv.Itoa(len(st.Items))
			if !narrow {
				line += " · спершу популярні"
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, line)),
				layout.Rigid(func(gtx C) D {
					if !filtered {
						return D{}
					}
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconRotateCcw, Text: "Скинути фільтри"}.Layout(gtx, th, &s.reset)
				}),
			)
		})
		if len(shown) == 0 {
			ws = append(ws, func(gtx C) D {
				return gui.EmptyState{Icon: gui.IconSearch, Title: "Нічого не знайшлося", Text: "Спробуй інше слово або скинь фільтри."}.Layout(gtx, th, nil)
			})
		}
		n := min(len(shown), s.limit)
		for _, i := range shown[:n] {
			it := st.Items[i]
			ws = append(ws, func(gtx C) D { return s.row(gtx, th, it) })
		}
		if len(shown) > n {
			left := len(shown) - n
			ws = append(ws, func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Icon: gui.IconChevronDown, Full: true,
					Text: "Показати ще " + strconv.Itoa(min(left, catalogPage)) + " з " + strconv.Itoa(left)}.Layout(gtx, th, &s.more)
			})
		}
		if st.Footer != "" {
			ws = append(ws, gui.Text{Style: th.Caption, Color: th.Pal.Text3}.Widget(th, st.Footer))
		}
		return gui.VStack(gtx, gui.Sp12, ws...)
	}
	s.drawer.Layout(gtx, th, gui.DrawerStyle{Title: st.Title, Subtitle: st.Subtitle, Width: 680, Body: body})
}

// groupRow is one group's chips: «Усі» and every option with its count under
// the other filters (an option with nothing left is hidden).
func (s *Catalog) groupRow(th *gui.Theme, st CatalogStyle, g CatalogGroup, words []string) layout.Widget {
	return func(gtx C) D {
		counts := map[string]int{}
		total := 0
		for _, it := range st.Items {
			if !CatalogMatch(it, s.sel, s.on, words, g.Key) {
				continue
			}
			total++
			for _, t := range it.Tags {
				if v, ok := strings.CutPrefix(t, g.Key+"="); ok {
					counts[v]++
				}
			}
		}
		cur := s.sel[g.Key]
		chips := []layout.Widget{func(gtx C) D {
			return gui.Chip{Text: "Усі · " + strconv.Itoa(total), Selected: cur == "", Small: true}.Layout(gtx, th, clickable(s.chips, g.Key+"="))
		}}
		for _, o := range g.Options {
			n := counts[o.Value]
			if n == 0 && cur != o.Value {
				continue // nothing left under the other filters
			}
			chips = append(chips, func(gtx C) D {
				return gui.Chip{Text: o.Label + " · " + strconv.Itoa(n), Selected: cur == o.Value, Small: true,
					Disabled: n == 0 && cur != o.Value}.Layout(gtx, th, clickable(s.chips, g.Key+"="+o.Value))
			})
		}
		return gui.VStack(gtx, gui.Sp4,
			gui.Text{Style: th.Label, Color: th.Pal.Text2}.Widget(th, g.Label),
			func(gtx C) D { return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, chips...) },
		)
	}
}

// row is one entry: title + badges, the service line, what it can do, the
// note, and the source link (or why it cannot be picked).
func (s *Catalog) row(gtx C, th *gui.Theme, it CatalogItem) D {
	card := gui.Card{Padding: gui.Sp16, Radius: gui.RadiusInner, Flat: true, Selected: it.Current}
	if it.Disabled == "" {
		card.Click = clickable(s.rows, it.ID)
	} else {
		card.Tint = true
	}
	return card.Layout(gtx, th, func(gtx C) D {
		ws := []layout.Widget{func(gtx C) D {
			badges := []func(gtx C, th *gui.Theme) D{}
			if it.Popular {
				badges = append(badges, gui.Badge{Text: "популярне", Tone: gui.ToneAction}.Layout)
			}
			if it.Current {
				badges = append(badges, gui.Badge{Text: "зараз", Tone: gui.ToneSuccess}.Layout)
			}
			for _, b := range it.Badges {
				badges = append(badges, gui.Badge{Text: b.Text, Tone: b.Tone}.Layout)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, gui.Text{Style: th.BodyStrong, MaxLines: 2}.Widget(th, it.Title)),
				layout.Rigid(func(gtx C) D {
					if len(badges) == 0 {
						return D{}
					}
					gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(300))
					return layout.Inset{Left: gui.Sp8}.Layout(gtx, func(gtx C) D {
						return gui.Flow{Gap: gui.Sp4, RowGap: gui.Sp4}.Layout(gtx, wrapTheme(th, badges)...)
					})
				}),
			)
		}}
		if it.Sub != "" {
			ws = append(ws, gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, it.Sub))
		}
		if it.Meta != "" {
			ws = append(ws, gui.Text{Style: th.Small, MaxLines: 2}.Widget(th, it.Meta))
		}
		if it.Note != "" {
			ws = append(ws, gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 3}.Widget(th, it.Note))
		}
		if it.Disabled != "" || it.Link != "" {
			ws = append(ws, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D {
						if it.Disabled == "" {
							return D{}
						}
						_, _, ink := th.Pal.ToneColors(gui.ToneWarning)
						return gui.Text{Style: th.Small, Color: ink, MaxLines: 2}.Layout(gtx, th, it.Disabled)
					}),
					layout.Rigid(func(gtx C) D {
						if it.Link == "" {
							return D{}
						}
						return gui.Link{Text: "Джерело", Trail: gui.IconExternalLink, Style: th.Small}.Layout(gtx, th, clickable(s.links, it.ID))
					}),
				)
			})
		}
		return gui.VStack(gtx, gui.Sp4, ws...)
	})
}

// wrapTheme adapts func(gtx, th) layouts to widgets.
func wrapTheme(th *gui.Theme, ls []func(gtx C, th *gui.Theme) D) []layout.Widget {
	out := make([]layout.Widget, len(ls))
	for i, l := range ls {
		out[i] = func(gtx C) D { return l(gtx, th) }
	}
	return out
}
