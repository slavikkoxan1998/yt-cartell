package pickers

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
	"gioui.org/op/paint"
	"gioui.org/unit"

	"ytcartell/internal/gui"
)

// panel is the popover a list picker opens: a card anchored under its field
// (above it when the window has more room there), drawn above everything.
// It rises in over 180 ms and fades out in 120 ms; a press outside or
// Escape closes it.
type panel struct {
	open    bool
	kbd     bool // opened from the keyboard: focus returns to the field
	focused bool // focus moved into the panel after opening
	outside bool // an outside press arrived
	anim    gui.Anim
	scrim   int // pointer tag of the area outside the panel
	keys    int // key focus tag inside the panel
	anchor  string
	up      bool
	right   bool // align the panel's right edge with the field's
}

// Popover geometry (dp).
const (
	popGap    = 6
	popMargin = 12
	popPad    = 6
	rowH      = 40
	row2H     = 56 // two-line rows
	maxRows   = 8
)

func (p *panel) key() string {
	if p.anchor == "" {
		p.anchor = fmt.Sprintf("pickers:%p", p)
	}
	return p.anchor
}

func (p *panel) show(gtx C, from *gui.Clickable) {
	if p.open {
		return
	}
	p.open, p.focused, p.outside = true, false, false
	p.kbd = gtx.Focused(&from.Clickable)
	gtx.Execute(op.InvalidateCmd{})
}

func (p *panel) hide(gtx C, from *gui.Clickable) {
	if !p.open {
		return
	}
	p.open = false
	if p.kbd {
		gtx.Execute(key.FocusCmd{Tag: &from.Clickable})
	}
	gtx.Execute(op.InvalidateCmd{})
}

// openNow opens the panel outside of a frame (Open, screenshots).
func (p *panel) openNow() {
	p.open, p.focused, p.outside, p.kbd = true, false, false, false
}

// toggle opens a closed panel and closes an open one.
func (p *panel) toggle(gtx C, from *gui.Clickable) {
	if p.open {
		p.hide(gtx, from)
	} else {
		p.show(gtx, from)
	}
}

// keyPresses drains the key presses meant for the panel (from its own tag and
// from the extra tags, e.g. a search field) and the outside presses.
func (p *panel) keyPresses(gtx C, extra ...event.Tag) []key.Name {
	var out []key.Name
	tags := append([]event.Tag{&p.keys}, extra...)
	for _, tag := range tags {
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
			if ke, ok := e.(key.Event); ok && ke.State == key.Press && p.open {
				out = append(out, ke.Name)
			}
		}
	}
	for {
		e, ok := gtx.Event(
			pointer.Filter{Target: &p.scrim, Kinds: pointer.Press},
			key.FocusFilter{Target: &p.keys},
		)
		if !ok {
			break
		}
		if pe, ok := e.(pointer.Event); ok && pe.Kind == pointer.Press && p.open {
			p.outside = true
		}
	}
	return out
}

// takeOutside reports (once) a press outside the open panel.
func (p *panel) takeOutside() bool {
	v := p.outside
	p.outside = false
	return v
}

// progress advances the enter/leave animation.
func (p *panel) progress(gtx C) float32 {
	if p.open {
		p.anim.Duration, p.anim.Ease = gui.DurHover*3/2, gui.EaseOutCubic
	} else {
		p.anim.Duration, p.anim.Ease = gui.DurHover, gui.EaseInCubic
	}
	return p.anim.Animate(gtx, b2f(p.open))
}

// visible reports whether the panel draws this frame.
func (p *panel) visible(gtx C) bool {
	return p.open || p.anim.Value(gtx) > 0.001
}

// place computes the panel rectangle relative to the field's top-left corner:
// under the field when it fits, else on the side with more room (shrunk to
// fit, never below minH), kept inside the window horizontally.
func (p *panel) place(gtx C, th *gui.Theme, w, natH, minH int) (image.Rectangle, bool) {
	th.WantAnchors()
	ar, ok := th.AnchorRect(p.key())
	win := th.WindowSize()
	if !ok || win.X == 0 || win.Y == 0 {
		gtx.Execute(op.InvalidateCmd{})
		return image.Rectangle{}, false
	}
	gap, margin := gtx.Dp(popGap), gtx.Dp(popMargin)
	w = min(w, max(gtx.Dp(200), win.X-2*margin))
	below := win.Y - margin - (ar.Max.Y + gap)
	above := ar.Min.Y - gap - margin
	var y, h int
	switch {
	case natH <= below:
		y, h, p.up = ar.Dy()+gap, natH, false
	case above > below:
		h = max(min(natH, above), min(natH, minH))
		y, p.up = -gap-h, true
	default:
		h = max(min(natH, below), min(natH, minH))
		y, p.up = ar.Dy()+gap, false
	}
	x := 0
	if p.right {
		x = ar.Dx() - w // right edges aligned (menus of buttons at the right)
	}
	if ar.Min.X+x+w > win.X-margin {
		x = win.X - margin - ar.Min.X - w
	}
	if ar.Min.X+x < margin {
		x = margin - ar.Min.X
	}
	return image.Rect(x, y, x+w, y+h), true
}

// draw defers the panel card at r (relative to the field) with body inside
// (body gets the exact card size). It must run inside the field's layout so
// the panel follows the field when the page scrolls.
func (p *panel) draw(gtx C, th *gui.Theme, r image.Rectangle, body func(gtx C) D) {
	a := p.progress(gtx)
	if a <= 0.001 && !p.open {
		return
	}
	m := op.Record(gtx.Ops)
	if p.open {
		// The scrim swallows the outside press that closes the panel.
		sc := clip.Rect(image.Rect(-1<<20, -1<<20, 1<<20, 1<<20)).Push(gtx.Ops)
		event.Op(gtx.Ops, &p.scrim)
		sc.Pop()
	}
	rise := float32(gtx.Dp(6)) * (1 - a)
	if p.up {
		rise = -rise
	}
	off := op.Affine(f32.AffineId().Offset(f32.Pt(float32(r.Min.X), float32(r.Min.Y)+rise))).Push(gtx.Ops)
	fade := paint.PushOpacity(gtx.Ops, a)
	size := r.Size()
	rr := image.Rectangle{Max: size}
	gui.Shadow(gtx, th, rr, gui.RadiusInner, gui.ElevPop)
	gui.Box(gtx, rr, gui.RadiusInner, th.Pal.Card, gui.WithAlpha(th.Pal.Border, 0.8))
	cl := clip.UniformRRect(rr, gtx.Dp(gui.RadiusInner)).Push(gtx.Ops)
	event.Op(gtx.Ops, &p.keys) // presses inside are not "outside"
	bg := gtx
	bg.Constraints = layout.Exact(size)
	if !p.open {
		bg = bg.Disabled()
	}
	body(bg)
	cl.Pop()
	fade.Pop()
	off.Pop()
	op.Defer(gtx.Ops, m.Stop())
	if p.open && !p.focused {
		p.focused = true
		gtx.Execute(key.FocusCmd{Tag: &p.keys})
	}
}

// ---- option lists -----------------------------------------------------------------------

// option is one row of a list picker.
type option struct {
	Value    string
	Label    string
	Caption  string // second line (two-line lists)
	Hint     string // right-aligned secondary text
	Badge    string // short text in the leading square ("EN")
	Icon     *gui.Icon
	Frame    float32 // leading frame shape of this w/h ratio (formats)
	Disabled bool
	Search   string // extra words the search matches
	Danger   bool   // destructive (menus): the label in the error ink
	Divider  bool   // a hairline above the row (menus)
	Check    bool   // a check on the right regardless of the value (menus)
}

// listStyle configures a list popover for one frame.
type listStyle struct {
	Options  []option
	Value    string
	TwoLine  bool          // rows with a caption line (56 dp)
	Search   bool          // a search field on top (also when > 8 options)
	Hint     string        // search placeholder
	MinWidth unit.Dp       // popover width at least this (0 = the field's)
	Lead     bool          // rows have a leading square (badge/icon/frame)
	Header   layout.Widget // optional block above the rows (not scrolled)
	// Empty replaces the rows when there are no options at all (loading,
	// error states); it gets the whole body.
	Empty  layout.Widget
	EmptyH unit.Dp // natural height of Empty
	// Row draws a custom row (voices); nil = the standard row.
	Row func(gtx C, i int, hi, selected bool) D
	// RowH overrides the row height (custom rows).
	RowH unit.Dp
	// Menu rows: icons without the leading square, no check on Value.
	Menu    bool
	NoCheck bool
}

// list is a popover with an optional search field and a scrolling option list.
type list struct {
	panel
	search gui.SearchField
	scroll gui.Scroll
	rows   []gui.Clickable
	hi     int
	shown  []int
	jump   bool // scroll the highlighted (current) option into view once
}

func (l *list) useSearch(st listStyle) bool { return st.Search || len(st.Options) > maxRows }

// openAt shows the list with the current value highlighted.
func (l *list) openAt(gtx C, from *gui.Clickable, st listStyle) {
	l.show(gtx, from)
	l.reset()
}

// openNow opens the list outside of a frame (Open, screenshots).
func (l *list) openNow() {
	if !l.open {
		l.panel.openNow()
		l.reset()
	}
}

func (l *list) reset() {
	l.search.SetText("")
	l.hi = -1
	l.jump = true
}

// update handles clicks and keys; it reports a picked value.
func (l *list) update(gtx C, from *gui.Clickable, st listStyle) (string, bool) {
	if len(l.rows) < len(st.Options) {
		l.rows = append(l.rows, make([]gui.Clickable, len(st.Options)-len(l.rows))...)
	}
	picked, ok := "", false
	for i := range st.Options {
		if l.rows[i].Clicked(gtx) && l.open && !st.Options[i].Disabled {
			picked, ok = st.Options[i].Value, true
		}
	}
	for _, k := range l.keyPresses(gtx, &l.search.Editor) {
		switch k {
		case key.NameEscape:
			if l.search.Editor.Len() > 0 {
				l.search.SetText("")
				l.scroll.List.Position = layout.Position{}
				continue
			}
			l.hide(gtx, from)
		case key.NameUpArrow:
			l.move(st, -1)
		case key.NameDownArrow:
			l.move(st, 1)
		case key.NameReturn, key.NameEnter:
			if l.hi >= 0 && l.hi < len(st.Options) && !st.Options[l.hi].Disabled {
				picked, ok = st.Options[l.hi].Value, true
			}
		}
		gtx.Execute(op.InvalidateCmd{})
	}
	if l.takeOutside() {
		l.hide(gtx, from)
	}
	if ok {
		l.hide(gtx, from)
	}
	return picked, ok
}

// move shifts the highlight over the shown, enabled options.
func (l *list) move(st listStyle, dir int) {
	if len(l.shown) == 0 {
		return
	}
	pos := -1
	for k, i := range l.shown {
		if i == l.hi {
			pos = k
		}
	}
	for step := 0; step < len(l.shown); step++ {
		pos += dir
		if pos < 0 || pos >= len(l.shown) {
			return
		}
		if !st.Options[l.shown[pos]].Disabled {
			l.hi = l.shown[pos]
			lp := &l.scroll.List.Position
			if pos < lp.First {
				lp.First, lp.Offset = pos, 0
			} else if lp.Count > 0 && pos >= lp.First+lp.Count-1 {
				lp.First, lp.Offset = max(0, pos-lp.Count+2), 0
			}
			return
		}
	}
}

// filter keeps the options matching the search query.
func (l *list) filter(st listStyle) {
	q := strings.ToLower(strings.TrimSpace(l.search.Query()))
	l.shown = l.shown[:0]
	for i, o := range st.Options {
		if q == "" || strings.Contains(strings.ToLower(o.Label), q) || strings.Contains(strings.ToLower(o.Hint), q) ||
			strings.Contains(strings.ToLower(o.Caption), q) || strings.Contains(strings.ToLower(o.Search), q) {
			l.shown = append(l.shown, i)
		}
	}
	keep := false
	for _, i := range l.shown {
		keep = keep || i == l.hi
	}
	if !keep && st.Menu && !l.kbd {
		l.hi = -1 // menus opened by the pointer highlight under the pointer only
	} else if !keep {
		// Highlight the current value, else the first enabled option.
		l.hi = -1
		for _, i := range l.shown {
			if st.Options[i].Value == st.Value && !st.Options[i].Disabled {
				l.hi = i
			}
		}
		for _, i := range l.shown {
			if l.hi < 0 && !st.Options[i].Disabled {
				l.hi = i
			}
		}
	}
	if l.jump {
		l.jump = false
		for k, i := range l.shown {
			if i == l.hi {
				l.scroll.List.Position = layout.Position{First: max(0, k-3)}
			}
		}
	}
}

// layout draws the popover of an open list (call it inside the field's
// layout, after the field; field is the field's size).
func (l *list) layout(gtx C, th *gui.Theme, field image.Point, st listStyle) {
	if !l.visible(gtx) {
		return
	}
	search := l.useSearch(st)
	if _, ok := l.search.Changed(gtx); ok {
		l.scroll.List.Position = layout.Position{}
	}
	l.filter(st)
	rh := gtx.Dp(rowH)
	if st.TwoLine {
		rh = gtx.Dp(row2H)
	}
	if st.RowH > 0 {
		rh = gtx.Dp(st.RowH)
	}
	pad := gtx.Dp(popPad)
	searchH := 0
	if search {
		searchH = gtx.Dp(gui.HeightMd) + pad
	}
	headH := 0
	var headCall op.CallOp
	w := max(field.X, gtx.Dp(st.MinWidth))
	if st.Header != nil {
		hg := gtx
		hg.Constraints = layout.Constraints{Min: image.Pt(w-2*pad, 0), Max: image.Pt(w-2*pad, gtx.Dp(400))}
		m := op.Record(gtx.Ops)
		hd := st.Header(hg)
		headCall = m.Stop()
		headH = hd.Size.Y + pad
	}
	n := len(l.shown)
	listH := max(1, min(n, maxRows)) * rh
	if n > maxRows {
		listH += rh / 2 // a half row hints that the list scrolls
	}
	natH := listH + 2*pad + searchH + headH
	if len(st.Options) == 0 && st.Empty != nil {
		natH = gtx.Dp(st.EmptyH) + 2*pad + headH
	}
	r, ok := l.place(gtx, th, w, natH, 3*rh+2*pad+searchH+headH)
	if !ok {
		return
	}
	if search && l.open && !l.focused {
		// Typing goes straight into the search.
		l.focused = true
		l.search.Focus(gtx)
	}
	l.draw(gtx, th, r, func(gtx C) D {
		W, H := gtx.Constraints.Max.X, gtx.Constraints.Max.Y
		y := pad
		if st.Header != nil {
			o := op.Offset(image.Pt(pad, y)).Push(gtx.Ops)
			headCall.Add(gtx.Ops)
			o.Pop()
			y += headH
		}
		if len(st.Options) == 0 && st.Empty != nil {
			eg := gtx
			eg.Constraints = layout.Exact(image.Pt(W-2*pad, max(0, H-y-pad)))
			o := op.Offset(image.Pt(pad, y)).Push(gtx.Ops)
			st.Empty(eg)
			o.Pop()
			return D{Size: image.Pt(W, H)}
		}
		if search {
			o := op.Offset(image.Pt(pad, y)).Push(gtx.Ops)
			sg := gtx
			sg.Constraints = layout.Exact(image.Pt(W-2*pad, gtx.Dp(gui.HeightMd)))
			hint := st.Hint
			if hint == "" {
				hint = "Пошук"
			}
			l.search.Layout(sg, th, hint)
			o.Pop()
			y += searchH
		}
		lg := gtx
		lg.Constraints = layout.Exact(image.Pt(W, max(rh, H-y-pad)))
		o := op.Offset(image.Pt(0, y)).Push(gtx.Ops)
		if len(l.shown) == 0 {
			t := op.Offset(image.Pt(gtx.Dp(16), gtx.Dp(10))).Push(gtx.Ops)
			gui.Text{Style: th.Body, Color: th.Pal.Text3}.Layout(lg, th, "Нічого не знайдено")
			t.Pop()
		}
		l.scroll.Layout(lg, th, len(l.shown), func(gtx C, k int) D {
			i := l.shown[k]
			return l.row(gtx, th, st, i, W, rh, pad)
		})
		o.Pop()
		return D{Size: image.Pt(W, H)}
	})
}

// row draws option i at width w (the hit area covers the whole row).
func (l *list) row(gtx C, th *gui.Theme, st listStyle, i, w, rh, pad int) D {
	o := st.Options[i]
	c := &l.rows[i]
	size := image.Pt(w, rh)
	rg := gtx
	if o.Disabled {
		rg = gtx.Disabled()
	}
	c.Clickable.Layout(rg, func(gtx C) D {
		defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
		if gtx.Enabled() {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		return D{Size: size}
	})
	if c.Hovered() && !o.Disabled && l.open {
		l.hi = i
	}
	selected := (o.Value == st.Value && !st.NoCheck) || o.Check
	hl := i == l.hi && !o.Disabled
	if o.Divider {
		gui.FillRect(gtx, image.Rect(pad+gtx.Dp(4), 0, w-pad-gtx.Dp(4), max(1, gtx.Dp(1))), th.Pal.Border)
	}
	if hl {
		fill := th.Pal.Surface
		if o.Danger {
			fill = th.Pal.ErrorSoft
		}
		gui.FillRRect(gtx, image.Rect(pad, 2, w-pad, rh-2), 10, fill)
	}
	if st.Row != nil {
		return st.Row(gtx, i, hl, selected)
	}
	fg, sub := th.Pal.Text, th.Pal.Text2
	if o.Danger {
		fg = th.Pal.ErrorInk
	}
	if o.Disabled {
		fg, sub = th.Pal.Text3, th.Pal.Text3
	}
	x := pad + gtx.Dp(10)
	switch {
	case st.Menu && o.Icon != nil:
		ip := gtx.Dp(18)
		ic := th.Pal.Text2
		if o.Danger {
			ic = th.Pal.ErrorInk
		}
		if o.Disabled {
			ic = th.Pal.Text3
		}
		t := op.Offset(image.Pt(x, (rh-ip)/2)).Push(gtx.Ops)
		o.Icon.Layout(gtx, 18, ic)
		t.Pop()
		x += ip + gtx.Dp(12)
	case st.Lead:
		ls := gtx.Dp(28)
		if st.TwoLine {
			ls = gtx.Dp(36)
		}
		lr := image.Rect(x, (rh-ls)/2, x+ls, (rh-ls)/2+ls)
		leading(gtx, th, o, lr, hl || selected)
		x += ls + gtx.Dp(12)
	}
	right := w - pad - gtx.Dp(10)
	if selected {
		ic := gtx.Dp(18)
		t := op.Offset(image.Pt(right-ic, (rh-ic)/2)).Push(gtx.Ops)
		gui.IconCheck.LayoutStroke(gtx, 18, 2, th.Pal.ActionText)
		t.Pop()
		right -= ic + gtx.Dp(8)
	}
	if o.Hint != "" {
		hg := gtx
		hg.Constraints = layout.Constraints{Max: image.Pt(max(0, (right-x)*2/5), rh)}
		m := op.Record(gtx.Ops)
		hd := gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 1}.Layout(hg, th, o.Hint)
		call := m.Stop()
		t := op.Offset(image.Pt(right-hd.Size.X, (rh-hd.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		right -= hd.Size.X + gtx.Dp(12)
	}
	lg := gtx
	lg.Constraints = layout.Constraints{Max: image.Pt(max(0, right-x), rh)}
	ts := th.Body
	if selected {
		ts = th.Body.Weighted(gui.FontMedium)
	}
	m := op.Record(gtx.Ops)
	var td D
	if st.TwoLine && o.Caption != "" {
		td = layout.Flex{Axis: layout.Vertical}.Layout(lg,
			layout.Rigid(gui.Text{Style: ts.Lined(1.3), Color: fg, MaxLines: 1}.Widget(th, o.Label)),
			layout.Rigid(gui.Text{Style: th.Small, Color: sub, MaxLines: 1}.Widget(th, o.Caption)),
		)
	} else {
		td = gui.Text{Style: ts, Color: fg, MaxLines: 1}.Layout(lg, th, o.Label)
	}
	call := m.Stop()
	t := op.Offset(image.Pt(x, (rh-td.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return D{Size: size}
}

// leading draws an option's leading square: a frame shape, an icon or a
// short badge text.
func leading(gtx C, th *gui.Theme, o option, r image.Rectangle, strong bool) {
	bg := th.Pal.Surface
	if strong {
		bg = th.Pal.SurfaceHover
	}
	gui.FillRRect(gtx, r, 8, bg)
	switch {
	case o.Frame > 0:
		box := unit.Dp(float32(r.Dx()) / gtx.Metric.PxPerDp * 0.62)
		frameIcon(gtx, th, r, o.Frame, box, th.Pal.Text)
	case o.Icon != nil:
		is := unit.Dp(float32(r.Dx()) / gtx.Metric.PxPerDp * 0.55)
		ip := gtx.Dp(is)
		t := op.Offset(image.Pt(r.Min.X+(r.Dx()-ip)/2, r.Min.Y+(r.Dy()-ip)/2)).Push(gtx.Ops)
		o.Icon.Layout(gtx, is, th.Pal.Text)
		t.Pop()
	case o.Badge != "":
		lg := gtx
		lg.Constraints = layout.Constraints{Max: r.Size()}
		m := op.Record(gtx.Ops)
		d := gui.Text{Style: th.Caption.Weighted(gui.FontSemiBold), MaxLines: 1}.Layout(lg, th, o.Badge)
		call := m.Stop()
		t := op.Offset(image.Pt(r.Min.X+(r.Dx()-d.Size.X)/2, r.Min.Y+(r.Dy()-d.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
	}
}
