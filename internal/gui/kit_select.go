package gui

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
)

// SelectOption is one entry of a Select.
type SelectOption struct {
	Value, Label string
	Hint         string // secondary text on the right ("Преміум", "12 голосів")
	Icon         *Icon
	Disabled     bool // shown dimmed; cannot be picked
}

// SelectStyle configures a Select for one frame.
type SelectStyle struct {
	Label       string // above the field
	Options     []SelectOption
	Value       string
	Placeholder string // shown when Value matches no option
	Hint        string
	Error       string
	Leading     *Icon
	Disabled    bool
	// Search shows a filter field in the popover; it turns on by itself when
	// there are more than 8 options.
	Search            bool
	SearchPlaceholder string
	Force             Force // ForcePress pins the popover open (screenshots)
}

// Select is a dropdown: a field showing the current label and a popover list
// anchored to it, drawn above everything. It opens below the field, or above
// it when the window has more room there; it filters with a search field when
// there are more than 8 options; Up/Down/Enter/Escape work while it is open,
// and an outside click closes it.
//
//	if v, ok := s.lang.Changed(gtx); ok { p.Language = v }
//	s.lang.Layout(gtx, th, gui.SelectStyle{Label: "Мова", Options: langs, Value: p.Language})
type Select struct {
	btn      Clickable
	items    []Clickable
	scroll   Scroll
	search   SearchField
	open     bool
	kbd      bool // opened from the keyboard: focus returns to the field
	hi       int  // highlighted option (index into options)
	pend     pending[string]
	anim     Anim
	scrim    int // tags
	keys     int
	opts     []SelectOption
	shown    []int // filtered option indexes
	value    string
	key      string
	placed   bool
	up       bool // the popover opened upwards (last placement)
	disabled bool
}

// Changed reports a newly picked value.
func (s *Select) Changed(gtx C) (string, bool) {
	s.update(gtx)
	return s.pend.take()
}

// IsOpen reports whether the popover is showing.
func (s *Select) IsOpen() bool { return s.open }

// Close closes the popover.
func (s *Select) Close() { s.open = false }

func (s *Select) anchorKey() string {
	if s.key == "" {
		s.key = fmt.Sprintf("select:%p", s)
	}
	return s.key
}

func (s *Select) openList(gtx C, current string) {
	s.open, s.placed = true, false
	s.kbd = gtx.Focused(&s.btn.Clickable)
	s.hi = -1
	for i, o := range s.opts {
		if o.Value == current {
			s.hi = i
		}
	}
	s.search.SetText("")
	s.anim.Set(0)
	if s.hi >= 0 {
		s.scroll.List.Position.First = max(0, s.hi-3)
		s.scroll.List.Position.Offset = 0
	}
	// Focus moves into the popover once it is drawn (layoutPopover).
	gtx.Execute(op.InvalidateCmd{})
}

func (s *Select) closeList(gtx C) {
	s.open = false
	if s.kbd {
		gtx.Execute(key.FocusCmd{Tag: &s.btn.Clickable})
	}
	gtx.Execute(op.InvalidateCmd{})
}

func (s *Select) pick(gtx C, i int) {
	if i < 0 || i >= len(s.opts) || s.opts[i].Disabled {
		return
	}
	s.pend.set(gtx, s.opts[i].Value)
	s.closeList(gtx)
}

// move shifts the highlight by dir over the visible (filtered) options.
func (s *Select) move(dir int) {
	if len(s.shown) == 0 {
		return
	}
	pos := -1
	for k, i := range s.shown {
		if i == s.hi {
			pos = k
		}
	}
	for step := 0; step < len(s.shown); step++ {
		pos += dir
		if pos < 0 || pos >= len(s.shown) {
			return
		}
		if !s.opts[s.shown[pos]].Disabled {
			s.hi = s.shown[pos]
			lp := &s.scroll.List.Position
			if pos < lp.First {
				lp.First, lp.Offset = pos, 0
			} else if lp.Count > 0 && pos >= lp.First+lp.Count-1 {
				lp.First, lp.Offset = max(0, pos-lp.Count+2), 0
			}
			return
		}
	}
}

func (s *Select) useSearch(st SelectStyle) bool { return st.Search || len(st.Options) > 8 }

func (s *Select) update(gtx C) {
	current := s.value
	if s.pend.ok {
		current = s.pend.v
	}
	for s.btn.Clicked(gtx) {
		if s.disabled {
			continue
		}
		if s.open {
			s.closeList(gtx)
		} else {
			s.openList(gtx, current)
		}
	}
	for i := range s.items {
		if i < len(s.opts) && s.items[i].Clicked(gtx) && s.open {
			s.pick(gtx, i)
		}
	}
	// Keys arrive at the list tag, or at the search field while it has focus.
	for _, tag := range []event.Tag{&s.keys, &s.search.Editor} {
		for {
			e, ok := gtx.Event(
				key.Filter{Focus: tag, Name: key.NameEscape},
				key.Filter{Focus: tag, Name: key.NameUpArrow},
				key.Filter{Focus: tag, Name: key.NameDownArrow},
				key.Filter{Focus: tag, Name: key.NameReturn},
				key.Filter{Focus: tag, Name: key.NameEnter},
			)
			if !ok {
				break
			}
			ke, ok := e.(key.Event)
			if !ok || ke.State != key.Press || !s.open {
				continue
			}
			switch ke.Name {
			case key.NameEscape:
				if tag != event.Tag(&s.keys) && s.search.Editor.Len() > 0 {
					s.search.SetText("")
					break
				}
				s.closeList(gtx)
			case key.NameUpArrow:
				s.move(-1)
			case key.NameDownArrow:
				s.move(1)
			case key.NameReturn, key.NameEnter:
				s.pick(gtx, s.hi)
			}
			gtx.Execute(op.InvalidateCmd{})
		}
	}
	for {
		e, ok := gtx.Event(
			pointer.Filter{Target: &s.scrim, Kinds: pointer.Press},
			key.FocusFilter{Target: &s.keys},
		)
		if !ok {
			break
		}
		if pe, ok := e.(pointer.Event); ok && pe.Kind == pointer.Press && s.open {
			s.closeList(gtx)
		}
	}
}

// Layout draws the field across the available width (and the popover).
func (s *Select) Layout(gtx C, th *Theme, st SelectStyle) D {
	s.opts = st.Options
	s.value = st.Value
	s.disabled = st.Disabled
	if len(s.items) < len(st.Options) {
		s.items = append(s.items, make([]Clickable, len(st.Options)-len(s.items))...)
	}
	if st.Disabled {
		s.open = false
		gtx = gtx.Disabled()
	}
	s.update(gtx)
	value := st.Value
	if s.pend.ok {
		value = s.pend.v
	}
	forced := st.Force&ForcePress != 0
	fs := FieldStyle{Label: st.Label, Hint: st.Hint, Error: st.Error}
	return fieldFrame(gtx, th, fs, 0, func(gtx C) D {
		return Anchor(gtx, th, s.anchorKey(), func(gtx C) D {
			d := s.layoutField(gtx, th, st, value)
			if s.open || forced {
				s.layoutPopover(gtx, th, st, value, d.Size, forced)
			}
			return d
		})
	})
}

func (s *Select) layoutField(gtx C, th *Theme, st SelectStyle, value string) D {
	cs := s.btn.update(gtx, st.Force&^ForcePress, 1)
	label, found := st.Placeholder, false
	var icon *Icon
	for _, o := range st.Options {
		if o.Value == value {
			label, found, icon = o.Label, true, o.Icon
			break
		}
	}
	if !found && value != "" && st.Placeholder == "" {
		label, found = value, true
	}
	if st.Leading != nil {
		icon = st.Leading
	}
	p := &th.Pal
	open := s.open || st.Force&ForcePress != 0
	bd := LerpColor(p.Border, p.BorderStrong, cs.hover)
	if open {
		bd = p.Action
	}
	if st.Error != "" {
		bd = p.Error
	}
	bg := p.Card
	if st.Disabled {
		bg, bd = p.Surface, p.Border
	}
	w := gtx.Constraints.Max.X
	h := gtx.Dp(HeightMd)
	size := pt(w, h)
	s.btn.hitArea(gtx, size)
	r := rectWH(w, h)
	FocusRing(gtx, th, r, RadiusControl, cs.focus*(1-b2f(open)))
	fillRRf(gtx.Ops, frect(r), float32(gtx.Dp(RadiusControl)), bg)
	bw := float32(max(1, gtx.Dp(1)))
	if open {
		bw += float32(gtx.Dp(1)) * 0.5
	}
	strokeRRf(gtx.Ops, frect(r), float32(gtx.Dp(RadiusControl)), bw, bd)
	padX := gtx.Dp(14)
	x := padX
	if icon != nil {
		ic := gtx.Dp(18)
		o := op.Offset(pt(x, (h-ic)/2)).Push(gtx.Ops)
		icon.Layout(gtx, 18, p.Text2)
		o.Pop()
		x += ic + gtx.Dp(10)
	}
	fg := p.Text
	if !found {
		fg = p.Text3
	}
	if st.Disabled {
		fg = p.Text2
	}
	chev := gtx.Dp(18)
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(max(0, w-x-padX-chev-gtx.Dp(8)), h)}
	ld, lc := record(lg, Text{Style: th.Body, Color: fg, MaxLines: 1}.Widget(th, label))
	o := op.Offset(pt(x, (h-ld.Size.Y)/2)).Push(gtx.Ops)
	lc.Add(gtx.Ops)
	o.Pop()
	// The chevron turns over while open.
	s.anim.Duration = DurTabs
	rot := float32(0)
	if open {
		rot = 1
	}
	o = op.Offset(pt(w-padX-chev, (h-chev)/2)).Push(gtx.Ops)
	cc := f32.Pt(float32(chev)/2, float32(chev)/2)
	tr := op.Affine(f32.AffineId().Rotate(cc, rot*3.14159265)).Push(gtx.Ops)
	IconChevronDown.Layout(gtx, 18, LerpColor(p.Text2, p.Text, cs.hover))
	tr.Pop()
	o.Pop()
	return D{Size: size, Baseline: h - ((h-ld.Size.Y)/2 + ld.Size.Y - ld.Baseline)}
}

// Popover geometry (dp).
const (
	selRowH    = 40
	selPad     = 6
	selGap     = 6
	selMargin  = 12
	selMaxRows = 8
)

// placePopover decides where a popover of natural height popH goes relative
// to an anchor rectangle in a window winH pixels tall: below the anchor when
// it fits, otherwise on the side with more room, shrunk to fit that room
// (never below minH). y is relative to the anchor's top edge.
func placePopover(anchor image.Rectangle, winH, popH, minH, gap, margin int) (y, h int, up bool) {
	below := winH - margin - (anchor.Max.Y + gap)
	above := anchor.Min.Y - gap - margin
	switch {
	case popH <= below:
		return anchor.Dy() + gap, popH, false
	case above > below:
		h = max(min(popH, above), min(popH, minH))
		return -gap - h, h, true
	default:
		h = max(min(popH, below), min(popH, minH))
		return anchor.Dy() + gap, h, false
	}
}

// filter returns the indexes of options matching q (case-insensitive,
// label or hint).
func filterOptions(opts []SelectOption, q string, out []int) []int {
	out = out[:0]
	q = strings.ToLower(strings.TrimSpace(q))
	for i, o := range opts {
		if q == "" || strings.Contains(strings.ToLower(o.Label), q) || strings.Contains(strings.ToLower(o.Hint), q) {
			out = append(out, i)
		}
	}
	return out
}

// layoutPopover records the option list and defers it above all content.
func (s *Select) layoutPopover(gtx C, th *Theme, st SelectStyle, value string, field image.Point, forced bool) {
	th.WantAnchors()
	search := s.useSearch(st)
	if _, ok := s.search.Changed(gtx); ok {
		s.scroll.List.Position = layout.Position{}
	}
	s.shown = filterOptions(st.Options, s.search.Query(), s.shown)
	if s.hi >= 0 {
		keep := false
		for _, i := range s.shown {
			keep = keep || i == s.hi
		}
		if !keep {
			s.hi = -1
		}
	}
	if s.hi < 0 {
		// Highlight the current value, else the first enabled option.
		for _, i := range s.shown {
			if st.Options[i].Value == value && !st.Options[i].Disabled {
				s.hi = i
			}
		}
		for _, i := range s.shown {
			if s.hi < 0 && !st.Options[i].Disabled {
				s.hi = i
			}
		}
	}
	rowH := gtx.Dp(selRowH)
	pad := gtx.Dp(selPad)
	searchH := 0
	if search {
		searchH = gtx.Dp(HeightMd) + gtx.Dp(6)
	}
	rows := max(1, min(len(s.shown), selMaxRows))
	listH := rows * rowH
	if len(s.shown) > selMaxRows {
		listH += rowH / 2 // a half row hints that the list scrolls
	}
	popH := listH + 2*pad + searchH
	// Width: the field's, or wider for long labels (up to 440 dp).
	w := field.X
	for _, o := range st.Options {
		lw := th.TextWidth(gtx, th.Body, o.Label) + gtx.Dp(14+14+26)
		if o.Hint != "" {
			lw += th.TextWidth(gtx, th.Small, o.Hint) + gtx.Dp(16)
		}
		if o.Icon != nil {
			lw += gtx.Dp(28)
		}
		w = max(w, min(lw, gtx.Dp(440)))
	}
	// Placement against the window: from the anchor rectangle resolved at the
	// end of the previous frame. Until it is known, draw nothing.
	ar, ok := th.AnchorRect(s.anchorKey())
	win := th.WindowSize()
	if !ok || win.Y == 0 {
		if forced && win.Y == 0 {
			ar, ok = image.Rectangle{Max: field}, true
			win = pt(field.X, 1<<20)
		} else {
			gtx.Execute(op.InvalidateCmd{})
			return
		}
	}
	y, h, up := placePopover(ar, win.Y, popH, 3*rowH+2*pad+searchH, gtx.Dp(selGap), gtx.Dp(selMargin))
	s.up = up
	listH = max(rowH, h-2*pad-searchH)
	// Keep the popover inside the window horizontally.
	x := 0
	if win.X > 0 && ar.Min.X+w > win.X-gtx.Dp(selMargin) {
		x = max(-ar.Min.X+gtx.Dp(selMargin), win.X-gtx.Dp(selMargin)-ar.Min.X-w)
	}
	s.anim.Duration, s.anim.Ease = DurHover*3/2, EaseOutCubic
	a := s.anim.Animate(gtx, 1)
	if forced {
		a = 1
	}

	m := op.Record(gtx.Ops)
	// Scrim: swallows the outside press that closes the list.
	sc := clip.Rect(image.Rect(-1<<20, -1<<20, 1<<20, 1<<20)).Push(gtx.Ops)
	event.Op(gtx.Ops, &s.scrim)
	sc.Pop()
	rise := float32(gtx.Dp(6)) * (1 - a)
	if up {
		rise = -rise
	}
	off := op.Affine(f32.AffineId().Offset(f32.Pt(float32(x), float32(y)-rise))).Push(gtx.Ops)
	pop := opacity(gtx, a)
	r := rectWH(w, h)
	Shadow(gtx, th, r, RadiusInner, ElevPop)
	Box(gtx, r, RadiusInner, th.Pal.Card, WithAlpha(th.Pal.Border, 0.8))
	cl := clip.UniformRRect(r, gtx.Dp(RadiusInner)).Push(gtx.Ops)
	event.Op(gtx.Ops, &s.keys) // presses inside are not "outside"
	if search {
		o := op.Offset(pt(pad, pad)).Push(gtx.Ops)
		sg := gtx
		sg.Constraints = layout.Exact(pt(w-2*pad, gtx.Dp(HeightMd)))
		ph := st.SearchPlaceholder
		if ph == "" {
			ph = "Пошук"
		}
		s.search.Layout(sg, th, ph)
		o.Pop()
	}
	lt := op.Offset(pt(0, pad+searchH)).Push(gtx.Ops)
	lg := gtx
	lg.Constraints = layout.Exact(pt(w, listH))
	if len(s.shown) == 0 {
		o := op.Offset(pt(gtx.Dp(16), gtx.Dp(10))).Push(gtx.Ops)
		Text{Style: th.Body, Color: th.Pal.Text3}.Layout(lg, th, "Нічого не знайдено")
		o.Pop()
	}
	s.scroll.Layout(lg, th, len(s.shown), func(gtx C, k int) D {
		i := s.shown[k]
		return s.row(gtx, th, i, st.Options[i], st.Options[i].Value == value, w, rowH, pad)
	})
	lt.Pop()
	cl.Pop()
	pop()
	off.Pop()
	op.Defer(gtx.Ops, m.Stop())
	if s.open && !forced && !s.placed {
		s.placed = true
		if search {
			s.search.Focus(gtx)
		} else {
			gtx.Execute(key.FocusCmd{Tag: &s.keys})
		}
	}
}

func (s *Select) row(gtx C, th *Theme, i int, o SelectOption, selected bool, w, rowH, pad int) D {
	ig := gtx
	if o.Disabled {
		ig = gtx.Disabled()
	}
	c := &s.items[i]
	st := c.update(ig, 0, 1)
	if st.hovered && !o.Disabled {
		s.hi = i
	}
	size := pt(w, rowH)
	c.hitArea(ig, size)
	hl := i == s.hi && !o.Disabled
	if hl {
		FillRRect(gtx, image.Rect(pad, 1, w-pad, rowH-1), 10, th.Pal.Surface)
	}
	fg := th.Pal.Text
	ts := th.Body
	if selected {
		ts = th.Body.Weighted(FontMedium)
	}
	if o.Disabled {
		fg = th.Pal.Text3
	}
	x := pad + gtx.Dp(10)
	if o.Icon != nil {
		ic := gtx.Dp(18)
		off := op.Offset(pt(x, (rowH-ic)/2)).Push(gtx.Ops)
		o.Icon.Layout(gtx, 18, th.Pal.Text2)
		off.Pop()
		x += ic + gtx.Dp(10)
	}
	right := w - pad - gtx.Dp(10)
	if selected {
		ic := gtx.Dp(18)
		off := op.Offset(pt(right-ic, (rowH-ic)/2)).Push(gtx.Ops)
		IconCheck.LayoutStroke(gtx, 18, 2, th.Pal.Action)
		off.Pop()
		right -= ic + gtx.Dp(8)
	}
	if o.Hint != "" {
		hg := gtx
		hg.Constraints = layout.Constraints{Max: pt(max(0, (right-x)/2), rowH)}
		hd, hc := record(hg, Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, o.Hint))
		off := op.Offset(pt(right-hd.Size.X, (rowH-hd.Size.Y)/2)).Push(gtx.Ops)
		hc.Add(gtx.Ops)
		off.Pop()
		right -= hd.Size.X + gtx.Dp(12)
	}
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(max(0, right-x), rowH)}
	ld, lc := record(lg, Text{Style: ts, Color: fg, MaxLines: 1}.Widget(th, o.Label))
	off := op.Offset(pt(x, (rowH-ld.Size.Y)/2)).Push(gtx.Ops)
	lc.Add(gtx.Ops)
	off.Pop()
	return D{Size: size}
}
