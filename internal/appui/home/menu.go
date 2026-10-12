package home

import (
	"image"
	"strings"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"ytcartell/internal/gui"
)

// popItem is one row of a popList.
type popItem struct {
	Value  string
	Label  string
	Hint   string // secondary text on the right
	Icon   *gui.Icon
	Danger bool // destructive action (error ink)
	Rule   bool // hairline above the row (separates a destructive action)
}

// popList is an anchored popover list: the ⋯ menu of a project card and the
// language / duration choosers of the story card. It opens under its anchor
// (a widget laid out with gui.Anchor), flips up near the bottom edge, filters
// with a search field when asked, closes on Escape or an outside click, and
// reports the pick once through Take. Only one is open at a time,
// so a page keeps a single popList for all of them.
type popList struct {
	open   bool
	id     string // what the list is for ("lang", "dur", "menu:<project>")
	anchor string
	items  []popItem
	value  string // checked row ("" = none)
	search bool
	width  unit.Dp
	right  bool // align the right edges of popover and anchor

	clicks  []gui.Clickable
	hover   []gui.Anim
	field   gui.SearchField
	scroll  gui.Scroll
	shown   []int
	hi      int
	anim    gui.Anim
	scrim   int
	keys    int
	focused bool
	missing int // frames the anchor was not found

	pickID, pickVal string
	picked          bool
}

// Open shows items under the anchor laid out as anchor.
func (m *popList) Open(id, anchor string, items []popItem, value string, search bool, width unit.Dp, right bool) {
	m.open, m.id, m.anchor = true, id, anchor
	m.items, m.value, m.search, m.width, m.right = items, value, search, width, right
	if len(m.clicks) < len(items) {
		m.clicks = append(m.clicks, make([]gui.Clickable, len(items)-len(m.clicks))...)
		m.hover = append(m.hover, make([]gui.Anim, len(items)-len(m.hover))...)
	}
	m.field.SetText("")
	m.scroll.List.Position = layout.Position{}
	m.hi = -1
	for i, it := range items {
		if it.Value == value && value != "" {
			m.hi = i
			m.scroll.List.Position.First = max(0, i-3)
		}
	}
	m.focused, m.missing = false, 0
	m.anim.Set(0)
}

// Close hides the list.
func (m *popList) Close() { m.open = false }

// Take reports (once) the list a value was picked in, and the value.
func (m *popList) Take() (id, value string, ok bool) {
	if !m.picked {
		return "", "", false
	}
	m.picked = false
	return m.pickID, m.pickVal, true
}

func (m *popList) pick(gtx C, i int) {
	if i < 0 || i >= len(m.items) {
		return
	}
	m.pickID, m.pickVal, m.picked = m.id, m.items[i].Value, true
	m.open = false
	gtx.Execute(op.InvalidateCmd{})
}

// Layout queues the popover above the page (call every frame after the
// anchors were laid out).
func (m *popList) Layout(gtx C, th *gui.Theme) {
	if !m.open {
		m.anim.Set(0)
		return
	}
	th.WantAnchors()
	th.Layer(gtx, func(gtx C) { m.draw(gtx, th) })
}

func (m *popList) events(gtx C) {
	for {
		e, ok := gtx.Event(
			pointer.Filter{Target: &m.scrim, Kinds: pointer.Press},
			key.FocusFilter{Target: &m.keys},
			key.Filter{Focus: &m.keys, Name: key.NameEscape},
			key.Filter{Focus: &m.keys, Name: key.NameUpArrow},
			key.Filter{Focus: &m.keys, Name: key.NameDownArrow},
			key.Filter{Focus: &m.keys, Name: key.NameReturn},
			key.Filter{Focus: &m.keys, Name: key.NameEnter},
			key.Filter{Focus: &m.field.Editor, Name: key.NameEscape},
			key.Filter{Focus: &m.field.Editor, Name: key.NameUpArrow},
			key.Filter{Focus: &m.field.Editor, Name: key.NameDownArrow},
			key.Filter{Focus: &m.field.Editor, Name: key.NameReturn},
			key.Filter{Focus: &m.field.Editor, Name: key.NameEnter},
		)
		if !ok {
			break
		}
		switch e := e.(type) {
		case pointer.Event:
			if e.Kind == pointer.Press {
				m.open = false
				gtx.Execute(op.InvalidateCmd{})
			}
		case key.Event:
			if e.State != key.Press {
				continue
			}
			switch e.Name {
			case key.NameEscape:
				m.open = false
			case key.NameUpArrow:
				m.move(-1)
			case key.NameDownArrow:
				m.move(1)
			case key.NameReturn, key.NameEnter:
				m.pick(gtx, m.hi)
			}
			gtx.Execute(op.InvalidateCmd{})
		}
	}
}

func (m *popList) move(dir int) {
	if len(m.shown) == 0 {
		return
	}
	pos := -1
	for k, i := range m.shown {
		if i == m.hi {
			pos = k
		}
	}
	pos = max(0, min(len(m.shown)-1, pos+dir))
	m.hi = m.shown[pos]
	lp := &m.scroll.List.Position
	if pos < lp.First {
		lp.First, lp.Offset = pos, 0
	} else if lp.Count > 0 && pos >= lp.First+lp.Count-1 {
		lp.First, lp.Offset = max(0, pos-lp.Count+2), 0
	}
}

func (m *popList) filter() {
	m.shown = m.shown[:0]
	q := strings.ToLower(strings.TrimSpace(m.field.Query()))
	for i, it := range m.items {
		if q == "" || strings.Contains(strings.ToLower(it.Label), q) || strings.Contains(strings.ToLower(it.Hint), q) {
			m.shown = append(m.shown, i)
		}
	}
}

const (
	popRowH   = 40
	popPad    = 6
	popMaxH   = 360
	popGap    = 6
	popMargin = 12
)

func (m *popList) draw(gtx C, th *gui.Theme) {
	m.events(gtx)
	if !m.open {
		return
	}
	for _, i := range m.shown {
		if i < len(m.clicks) && m.clicks[i].Clicked(gtx) {
			m.pick(gtx, i)
			return
		}
	}
	if _, ok := m.field.Changed(gtx); ok {
		m.scroll.List.Position = layout.Position{}
	}
	m.filter()
	ar, ok := th.AnchorRect(m.anchor)
	win := gtx.Constraints.Max
	if !ok {
		// Placed once the anchor is known (next frame); an anchor that stays
		// away (scrolled out of a virtualised list) closes the list.
		if m.missing++; m.missing > 3 {
			m.open = false
			return
		}
		gtx.Execute(op.InvalidateCmd{})
		return
	}
	m.missing = 0
	rowH, pad := gtx.Dp(popRowH), gtx.Dp(popPad)
	searchH := 0
	if m.search {
		searchH = gtx.Dp(gui.HeightMd) + gtx.Dp(6)
	}
	listH := max(1, len(m.shown)) * rowH
	natural := listH + 2*pad + searchH
	w := min(gtx.Dp(m.width), win.X-2*gtx.Dp(popMargin))
	below := win.Y - gtx.Dp(popMargin) - ar.Max.Y - gtx.Dp(popGap)
	above := ar.Min.Y - gtx.Dp(popGap) - gtx.Dp(popMargin)
	h := min(natural, gtx.Dp(popMaxH))
	up := false
	switch {
	case h <= below:
	case above > below:
		up, h = true, max(min(h, above), min(h, 3*rowH+2*pad+searchH))
	default:
		h = max(min(h, below), min(h, 3*rowH+2*pad+searchH))
	}
	x := ar.Min.X
	if m.right {
		x = ar.Max.X - w
	}
	x = max(gtx.Dp(popMargin), min(x, win.X-gtx.Dp(popMargin)-w))
	y := ar.Max.Y + gtx.Dp(popGap)
	if up {
		y = ar.Min.Y - gtx.Dp(popGap) - h
	}
	m.anim.Duration, m.anim.Ease = gui.DurHover*3/2, gui.EaseOutCubic
	a := m.anim.Animate(gtx, 1)

	// Scrim: a press anywhere else closes the list.
	sc := clip.Rect(image.Rectangle{Max: win}).Push(gtx.Ops)
	event.Op(gtx.Ops, &m.scrim)
	sc.Pop()
	rise := float32(gtx.Dp(6)) * (1 - a)
	if up {
		rise = -rise
	}
	defer op.Affine(f32.AffineId().Offset(f32.Pt(float32(x), float32(y)+rise))).Push(gtx.Ops).Pop()
	gui.Fade(gtx, a, func(gtx C) D {
		r := image.Rectangle{Max: image.Pt(w, h)}
		gui.Shadow(gtx, th, r, gui.RadiusInner, gui.ElevPop)
		gui.Box(gtx, r, gui.RadiusInner, th.Pal.Card, gui.WithAlpha(th.Pal.Border, 0.8))
		cl := gui.ClipRRect(gtx, r.Size(), gui.RadiusInner)
		event.Op(gtx.Ops, &m.keys) // presses inside are not outside presses
		if m.search {
			o := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
			sg := gtx
			sg.Constraints = layout.Exact(image.Pt(w-2*pad, gtx.Dp(gui.HeightMd)))
			m.field.Layout(sg, th, "Пошук")
			o.Pop()
		}
		lo := op.Offset(image.Pt(0, pad+searchH)).Push(gtx.Ops)
		lg := gtx
		lg.Constraints = layout.Exact(image.Pt(w, max(rowH, h-2*pad-searchH)))
		if len(m.shown) == 0 {
			o := op.Offset(image.Pt(gtx.Dp(16), gtx.Dp(10))).Push(gtx.Ops)
			gui.Text{Style: th.Body, Color: th.Pal.Text3, MaxLines: 1}.Layout(lg, th, "Нічого не знайдено")
			o.Pop()
		}
		m.scroll.Layout(lg, th, len(m.shown), func(gtx C, k int) D {
			return m.row(gtx, th, m.shown[k], w, rowH, pad)
		})
		lo.Pop()
		cl.Pop()
		return D{Size: r.Size()}
	})
	if !m.focused {
		m.focused = true
		if m.search {
			m.field.Focus(gtx)
		} else {
			gtx.Execute(key.FocusCmd{Tag: &m.keys})
		}
	}
}

func (m *popList) row(gtx C, th *gui.Theme, i int, w, rowH, pad int) D {
	it := m.items[i]
	c := &m.clicks[i]
	size := image.Pt(w, rowH)
	if c.Hovered() {
		m.hi = i
	}
	m.hover[i].Duration = gui.DurHover
	hv := m.hover[i].Animate(gtx, b2f(i == m.hi))
	return c.Layout(gtx, func(gtx C) D {
		pointer.CursorPointer.Add(gtx.Ops)
		if it.Rule {
			gui.FillRect(gtx, image.Rect(pad+gtx.Dp(10), 0, w-pad-gtx.Dp(10), max(1, gtx.Dp(1))), th.Pal.Border)
		}
		if hv > 0.01 {
			bg := th.Pal.Surface
			if it.Danger {
				bg = th.Pal.ErrorSoft
			}
			gui.FillRRect(gtx, image.Rect(pad, 2, w-pad, rowH-2), 10, gui.WithAlpha(bg, hv))
		}
		fg, ic := th.Pal.Text, th.Pal.Text2
		if it.Danger {
			fg, ic = th.Pal.ErrorInk, th.Pal.ErrorInk
		}
		x := pad + gtx.Dp(12)
		if it.Icon != nil {
			is := gtx.Dp(18)
			o := op.Offset(image.Pt(x, (rowH-is)/2)).Push(gtx.Ops)
			it.Icon.Layout(gtx, 18, ic)
			o.Pop()
			x += is + gtx.Dp(12)
		}
		right := w - pad - gtx.Dp(12)
		if it.Value == m.value && m.value != "" {
			is := gtx.Dp(18)
			o := op.Offset(image.Pt(right-is, (rowH-is)/2)).Push(gtx.Ops)
			gui.IconCheck.LayoutStroke(gtx, 18, 2, th.Pal.ActionText)
			o.Pop()
			right -= is + gtx.Dp(8)
		}
		if it.Hint != "" {
			hg := gtx
			hg.Constraints = layout.Constraints{Max: image.Pt(max(0, (right-x)/2), rowH)}
			hd, hc := record(hg, gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, it.Hint))
			o := op.Offset(image.Pt(right-hd.Size.X, (rowH-hd.Size.Y)/2)).Push(gtx.Ops)
			hc.Add(gtx.Ops)
			o.Pop()
			right -= hd.Size.X + gtx.Dp(12)
		}
		ts := th.Body
		if it.Value == m.value && m.value != "" {
			ts = th.Body.Weighted(gui.FontMedium)
		}
		lg := gtx
		lg.Constraints = layout.Constraints{Max: image.Pt(max(0, right-x), rowH)}
		ld, lc := record(lg, gui.Text{Style: ts, Color: fg, MaxLines: 1}.Widget(th, it.Label))
		o := op.Offset(image.Pt(x, (rowH-ld.Size.Y)/2)).Push(gtx.Ops)
		lc.Add(gtx.Ops)
		o.Pop()
		return D{Size: size}
	})
}

// record lays w out into a macro and returns its size and call.
func record(gtx C, w layout.Widget) (D, op.CallOp) {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	return d, m.Stop()
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
