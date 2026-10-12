package settings

// A rotation key list (a TTS provider's keys, the Google AI Studio keys): every
// key masked, with its state, its usage and a remove button. Up to keyFold
// keys are listed as they are; a longer list starts folded into one summary
// («52 ключі · 50 працюють · 2 з помилкою» + the usage) with «Показати всі»;
// unfolded it gets a search by mask or state, filter chips (усі / працюють /
// помилка / вичерпані) and a list of fixed height that scrolls on its own — fifty
// keys never stretch the page.

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
)

// spentExhausted and spentResting name the spent keys of a summary.
func spentExhausted(int) string { return "вичерпано" }
func spentResting(n int) string {
	return gui.PluralUK(n, "відпочиває", "відпочивають", "відпочивають")
}

// keyFold: longer lists start folded.
const keyFold = 3

// keyListMaxH is the height of an unfolded list (it scrolls inside).
const keyListMaxH unit.Dp = 360

// Key states of a list row.
const (
	kOK    = "ok"    // works
	kErr   = "error" // rejected
	kSpent = "spent" // exhausted or resting
	kNew   = "new"   // not checked or used yet
)

// keyItem is one key of a list (never the key itself).
type keyItem struct {
	name    string // the mask (+ « · основний»)
	state   string // kOK | kErr | kSpent | kNew
	pill    gui.StatusPill
	meta    string // the error, or the plan and usage
	metaErr bool
	remove  *gui.Clickable // nil: no remove button (borrowed keys)
}

// keyList is the state of one list.
type keyList struct {
	open   bool
	toggle gui.Clickable
	search gui.SearchField
	filter string // "" = all, else a k* state
	chips  [4]gui.Clickable
	scroll gui.Scroll
}

var keyFilters = []struct{ state, label string }{
	{"", "Усі"}, {kOK, "Працюють"}, {kErr, "Помилка"}, {kSpent, "Вичерпані"},
}

// keyCounts counts the items per state.
func keyCounts(items []keyItem) map[string]int {
	n := map[string]int{"": len(items)}
	for _, it := range items {
		n[it.state]++
	}
	return n
}

// keySummary is the folded line: «52 ключі · 50 працюють · 2 з помилкою · 1
// вичерпано · 3 ще не перевірено».
func keySummary(items []keyItem, spent func(n int) string) string {
	n := keyCounts(items)
	parts := []string{core.Count(len(items), "ключ", "ключі", "ключів"),
		itoa(n[kOK]) + " " + gui.PluralUK(n[kOK], "працює", "працюють", "працюють"),
		itoa(n[kErr]) + " з помилкою"}
	if n[kSpent] > 0 {
		parts = append(parts, itoa(n[kSpent])+" "+spent(n[kSpent]))
	}
	if n[kNew] > 0 {
		parts = append(parts, itoa(n[kNew])+" ще не "+gui.PluralUK(n[kNew], "перевірений", "перевірені", "перевірених"))
	}
	return strings.Join(parts, " · ")
}

// matches: the item passes the filter and the search query.
func (l *keyList) matches(it keyItem) bool {
	if l.filter != "" && it.state != l.filter {
		return false
	}
	q := strings.ToLower(strings.TrimSpace(l.search.Query()))
	if q == "" {
		return true
	}
	words := map[string]string{kOK: "працює", kErr: "помилка", kSpent: "вичерпано відпочиває", kNew: "не перевірений новий"}
	hay := strings.ToLower(it.name + " " + it.meta + " " + it.pill.Text + " " + words[it.state])
	return strings.Contains(hay, q)
}

// layout draws the list: the rows, or (longer than keyFold) the summary with
// «Показати всі» and, unfolded, the search, the filters and the scrolled rows.
// usage is the second summary line ("" = none); spent names n spent keys
// («вичерпано», «відпочиває»).
func (l *keyList) layout(gtx C, th *gui.Theme, items []keyItem, usage string, spent func(n int) string) D {
	if len(items) <= keyFold {
		l.open = false
		rows := make([]layout.Widget, len(items))
		for i := range items {
			it := items[i]
			rows[i] = func(gtx C) D { return keyRow(gtx, th, it) }
		}
		return inset(gtx, th, func(gtx C) D { return lines(gtx, th, rows...) })
	}
	if l.toggle.Clicked(gtx) {
		l.open = !l.open
		if !l.open {
			l.search.SetText("")
			l.filter = ""
		}
	}
	for i, f := range keyFilters {
		if l.chips[i].Clicked(gtx) {
			l.filter = f.state
			l.scroll.List.Position = layout.Position{}
		}
	}
	if _, ok := l.search.Changed(gtx); ok {
		l.scroll.List.Position = layout.Position{}
	}
	summary := func(gtx C) D {
		text := func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(gui.IconKeyRound.Widget(18, th.Pal.Text2)),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium)}.Widget(th, keySummary(items, spent))),
						layout.Rigid(func(gtx C) D {
							if usage == "" {
								return D{}
							}
							return layout.Inset{Top: 2}.Layout(gtx, gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, usage))
						}),
					)
				}),
			)
		}
		btn := func(gtx C) D {
			b := gui.Button{Kind: gui.Secondary, Size: gui.Sm, Text: "Показати всі", Trail: gui.IconChevronDown}
			if l.open {
				b.Text, b.Trail = "Згорнути", gui.IconChevronUp
			}
			return b.Layout(gtx, th, &l.toggle)
		}
		if gtx.Constraints.Max.X < gtx.Dp(440) {
			return gui.VStack(gtx, gui.Sp12, text, func(gtx C) D { gtx.Constraints.Min.X = 0; return btn(gtx) })
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, text),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Rigid(btn),
		)
	}
	if !l.open {
		return inset(gtx, th, summary)
	}
	// Unfolded: the summary, then the search and the filters on the card (the
	// tinted search field reads on white), then the rows in their own panel.
	counts := keyCounts(items)
	return gui.VStack(gtx, gui.Sp12,
		func(gtx C) D { return inset(gtx, th, summary) },
		func(gtx C) D { return l.search.Layout(gtx, th, "Пошук за маскою чи станом") },
		func(gtx C) D {
			chips := make([]layout.Widget, len(keyFilters))
			for i, f := range keyFilters {
				i, f := i, f
				chips[i] = func(gtx C) D {
					return gui.Chip{Text: f.label + " · " + itoa(counts[f.state]), Selected: l.filter == f.state, Small: true,
						Dot: keyDot(th, f.state)}.Layout(gtx, th, &l.chips[i])
				}
			}
			return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, chips...)
		},
		func(gtx C) D { return inset(gtx, th, func(gtx C) D { return l.rows(gtx, th, items) }) },
	)
}

// keyDot is the colour dot of a filter chip.
func keyDot(th *gui.Theme, state string) (c color.NRGBA) {
	switch state {
	case kOK:
		return th.Pal.Success
	case kErr:
		return th.Pal.Error
	case kSpent:
		return th.Pal.Warning
	}
	return c
}

// rows is the unfolded, filtered list (at most keyListMaxH tall; it scrolls).
func (l *keyList) rows(gtx C, th *gui.Theme, items []keyItem) D {
	var shown []keyItem
	for _, it := range items {
		if l.matches(it) {
			shown = append(shown, it)
		}
	}
	if len(shown) == 0 {
		return layout.Inset{Top: gui.Sp4, Bottom: gui.Sp4}.Layout(gtx,
			gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, "Нічого не знайдено — зміни пошук чи фільтр."))
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	gtx.Constraints.Min.Y = 0
	gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(keyListMaxH))
	return l.scroll.Layout(gtx, th, len(shown), func(gtx C, k int) D {
		it := shown[k]
		top := unit.Dp(10)
		if k == 0 {
			top = 0
		}
		return layout.Inset{Top: top, Bottom: 10, Right: gui.Sp12}.Layout(gtx, func(gtx C) D {
			if k == 0 {
				return keyRow(gtx, th, it)
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(gui.Divider{}.Widget(th)),
				layout.Rigid(layout.Spacer{Height: 10}.Layout),
				layout.Rigid(func(gtx C) D { return keyRow(gtx, th, it) }),
			)
		})
	})
}

// keyRow is one key: the mask, its state and usage, and a remove button.
func keyRow(gtx C, th *gui.Theme, it keyItem) D {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(gui.IconKeyRound.Widget(18, th.Pal.Text2)),
		layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					nameW := gui.Text{Style: th.Body.Weighted(gui.FontMedium), MaxLines: 1}.Widget(th, it.name)
					if gtx.Constraints.Max.X < gtx.Dp(300) {
						// Phones: the state under the name, never cut.
						return gui.VStack(gtx, gui.Sp4, nameW, func(gtx C) D { gtx.Constraints.Min.X = 0; return it.pill.Layout(gtx, th) })
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(nameW),
						layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
						layout.Rigid(it.pill.Widget(th)),
					)
				}),
				layout.Rigid(func(gtx C) D {
					if it.meta == "" {
						return D{}
					}
					col := th.Pal.Text2
					if it.metaErr {
						col = th.Pal.ErrorInk
					}
					return gui.Text{Style: th.Small, Color: col, MaxLines: 2}.Layout(gtx, th, it.meta)
				}),
			)
		}),
		layout.Rigid(func(gtx C) D {
			if it.remove == nil {
				return D{Size: image.Pt(0, 0)}
			}
			return gui.IconButton{Icon: gui.IconX, Size: 32, Tip: "Прибрати ключ"}.Layout(gtx, th, it.remove)
		}),
	)
}
