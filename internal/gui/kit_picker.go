package gui

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// PickerItem is one choice of a BigPicker (a style, voice, model, language).
type PickerItem struct {
	Value, Label string
	Caption      string // "підходить для казок"
	Category     string
	Image        paint.ImageOp // preview picture (optional)
	Swatches     []color.NRGBA // palette stripes when there is no picture
	Icon         *Icon         // fallback preview
	Badge        string        // "Новий", "Преміум"
}

// PickerStyle configures a BigPicker for one frame.
type PickerStyle struct {
	Title, Subtitle string
	Items           []PickerItem
	// Categories orders the category tabs (after "Усі"); nil derives them
	// from the items in order of appearance.
	Categories        []string
	AllLabel          string // default "Усі"
	Value             string
	SearchPlaceholder string  // default "Пошук"
	MinCardWidth      unit.Dp // 0 = 200
	Ratio             float32 // preview width/height; 0 = 4/3
}

// BigPicker is a large modal gallery for choices that need previews: a
// search field, category tabs and a responsive grid of preview cards. The
// search has focus while it is open; Up/Down/Left/Right move the highlight,
// Enter picks, Escape closes.
//
//	if s.styleBtn.Clicked(gtx) { s.styles.Open() }
//	if v, ok := s.styles.Changed(gtx); ok { p.Style = v }
//	s.styles.Layout(gtx, th, gui.PickerStyle{Title: "Стиль", Items: items, Value: p.Style})
type BigPicker struct {
	m      modal
	search SearchField
	tabs   PillTabs
	cat    int
	scroll Scroll
	items  []Clickable
	hi     int
	pend   pending[string]
	shown  []int
	cols   int
	opened bool
	query  string // the search text the next opening starts with (OpenWith)
}

// Open shows the picker (search cleared, current value highlighted).
func (p *BigPicker) Open() {
	p.m.setOpen(true)
	p.opened = false
}

// OpenWith shows the picker with its search already holding query (a
// screen that points at one item, screenshots).
func (p *BigPicker) OpenWith(query string) {
	p.Open()
	p.query = query
}

// Close hides the picker (animated).
func (p *BigPicker) Close() { p.m.setOpen(false) }

// IsOpen reports whether the picker is open.
func (p *BigPicker) IsOpen() bool { return p.m.open }

// Changed reports a picked value.
func (p *BigPicker) Changed(gtx C) (string, bool) { return p.pend.take() }

// Dismissed reports (once) that the user closed the picker without picking.
func (p *BigPicker) Dismissed() bool {
	v := p.m.dismissed
	p.m.dismissed = false
	return v
}

func pickerCategories(st PickerStyle) []string {
	if st.Categories != nil {
		return st.Categories
	}
	var out []string
	seen := map[string]bool{}
	for _, it := range st.Items {
		if it.Category != "" && !seen[it.Category] {
			seen[it.Category] = true
			out = append(out, it.Category)
		}
	}
	return out
}

// Layout queues the picker above the window content.
func (p *BigPicker) Layout(gtx C, th *Theme, st PickerStyle) {
	p.m.events(gtx, true)
	a := p.m.progress(gtx, DurSheet)
	if a <= 0.001 && !p.m.open {
		return
	}
	if len(p.items) < len(st.Items) {
		p.items = append(p.items, make([]Clickable, len(st.Items)-len(p.items))...)
	}
	th.Layer(gtx, func(gtx C) {
		p.m.events(gtx, true)
		p.draw(gtx, th, st, a)
	})
}

func (p *BigPicker) filter(st PickerStyle, cats []string) {
	q := strings.ToLower(strings.TrimSpace(p.search.Query()))
	cat := ""
	if p.cat > 0 && p.cat <= len(cats) {
		cat = cats[p.cat-1]
	}
	p.shown = p.shown[:0]
	for i, it := range st.Items {
		if cat != "" && it.Category != cat {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(it.Label), q) &&
			!strings.Contains(strings.ToLower(it.Caption), q) &&
			!strings.Contains(strings.ToLower(it.Category), q) {
			continue
		}
		p.shown = append(p.shown, i)
	}
}

func (p *BigPicker) pick(gtx C, st PickerStyle, i int) {
	if i < 0 || i >= len(st.Items) {
		return
	}
	p.pend.set(gtx, st.Items[i].Value)
	p.m.setOpen(false)
}

// moveHi moves the highlight by d positions within the shown items.
func (p *BigPicker) moveHi(d int) {
	if len(p.shown) == 0 {
		return
	}
	pos := 0
	for k, i := range p.shown {
		if i == p.hi {
			pos = k
		}
	}
	pos = min(len(p.shown)-1, max(0, pos+d))
	p.hi = p.shown[pos]
	if p.cols > 0 {
		row := pos / p.cols
		lp := &p.scroll.List.Position
		if row < lp.First {
			lp.First, lp.Offset = row, 0
		} else if lp.Count > 0 && row >= lp.First+lp.Count-1 {
			lp.First, lp.Offset = max(0, row-lp.Count+2), 0
		}
	}
}

func (p *BigPicker) keys(gtx C, st PickerStyle) {
	for _, tag := range []event.Tag{&p.m.keys, &p.search.Editor} {
		filters := []event.Filter{
			key.Filter{Focus: tag, Name: key.NameUpArrow},
			key.Filter{Focus: tag, Name: key.NameDownArrow},
			key.Filter{Focus: tag, Name: key.NameReturn},
			key.Filter{Focus: tag, Name: key.NameEnter},
		}
		if tag == event.Tag(&p.m.keys) {
			filters = append(filters,
				key.Filter{Focus: tag, Name: key.NameLeftArrow},
				key.Filter{Focus: tag, Name: key.NameRightArrow})
		}
		for {
			e, ok := gtx.Event(filters...)
			if !ok {
				break
			}
			ke, ok := e.(key.Event)
			if !ok || ke.State != key.Press || !p.m.open {
				continue
			}
			cols := max(1, p.cols)
			switch ke.Name {
			case key.NameUpArrow:
				p.moveHi(-cols)
			case key.NameDownArrow:
				p.moveHi(cols)
			case key.NameLeftArrow:
				p.moveHi(-1)
			case key.NameRightArrow:
				p.moveHi(1)
			case key.NameReturn, key.NameEnter:
				p.pick(gtx, st, p.hi)
			}
			gtx.Execute(op.InvalidateCmd{})
		}
	}
}

func (p *BigPicker) draw(gtx C, th *Theme, st PickerStyle, a float32) {
	win := gtx.Constraints.Max
	cats := pickerCategories(st)
	if !p.opened && p.m.open {
		p.opened = true
		p.search.SetText(p.query)
		p.query = ""
		p.cat = 0
		p.hi = -1
		for i, it := range st.Items {
			if it.Value == st.Value {
				p.hi = i
			}
		}
		p.scroll.List.Position = layout.Position{}
		p.m.focused = true // the search takes focus instead of the key tag
		p.search.Focus(gtx)
	}
	p.keys(gtx, st)
	if _, ok := p.search.Changed(gtx); ok {
		p.scroll.List.Position = layout.Position{}
	}
	if i, ok := p.tabs.Changed(gtx); ok {
		p.cat = i
		p.scroll.List.Position = layout.Position{}
	}
	for k := range st.Items {
		if p.items[k].Clicked(gtx) {
			p.pick(gtx, st, k)
		}
	}
	p.filter(st, cats)
	if p.hi < 0 && len(p.shown) > 0 {
		p.hi = p.shown[0]
	}

	p.m.scrimLayer(gtx, th, a)
	margin := gtx.Dp(40)
	w := min(gtx.Dp(1160), win.X-2*margin)
	h := win.Y - 2*margin
	x := (win.X - w) / 2
	y := margin + int(float32(gtx.Dp(28))*(1-a))
	o := op.Offset(pt(x, y)).Push(gtx.Ops)
	defer o.Pop()
	defer opacity(gtx, a)()
	r := rectWH(w, h)
	Shadow(gtx, th, r, RadiusCard, ElevSheet)
	Box(gtx, r, RadiusCard, th.Pal.Card, WithAlpha(th.Pal.Border, 0.6))
	p.m.block(gtx, r.Size())
	cl := clip.UniformRRect(r, gtx.Dp(RadiusCard)).Push(gtx.Ops)
	defer cl.Pop()
	pad := gtx.Dp(28)
	inner := w - 2*pad

	// Header: title + subtitle, close button.
	hg := gtx
	hg.Constraints = layout.Constraints{Max: pt(inner-gtx.Dp(48), h)}
	hd, hc := record(hg, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(Text{Style: th.H2}.Widget(th, st.Title)),
			layout.Rigid(func(gtx C) D {
				if st.Subtitle == "" {
					return D{}
				}
				return layout.Inset{Top: Sp4}.Layout(gtx, Text{Style: th.Body, Color: th.Pal.Text2}.Widget(th, st.Subtitle))
			}),
		)
	})
	t := op.Offset(pt(pad, pad)).Push(gtx.Ops)
	hc.Add(gtx.Ops)
	t.Pop()
	b := gtx.Dp(40)
	t = op.Offset(pt(w-gtx.Dp(18)-b, gtx.Dp(18))).Push(gtx.Ops)
	IconButton{Icon: IconX, Size: 40, IconSize: 22, Tip: "Закрити"}.Layout(gtx, th, &p.m.close)
	t.Pop()

	// Toolbar: search + categories.
	yy := pad + hd.Size.Y + gtx.Dp(20)
	sw := min(gtx.Dp(340), inner)
	sg := gtx
	sg.Constraints = layout.Exact(pt(sw, gtx.Dp(HeightMd)))
	t = op.Offset(pt(pad, yy)).Push(gtx.Ops)
	ph := st.SearchPlaceholder
	if ph == "" {
		ph = "Пошук"
	}
	p.search.Layout(sg, th, ph)
	t.Pop()
	if len(cats) > 0 {
		all := st.AllLabel
		if all == "" {
			all = "Усі"
		}
		tabs := []Tab{{Label: all}}
		for _, c := range cats {
			tabs = append(tabs, Tab{Label: c})
		}
		tg := gtx
		tg.Constraints = layout.Constraints{Max: pt(inner-sw-gtx.Dp(16), gtx.Dp(HeightMd))}
		td, tc := record(tg, func(gtx C) D { return p.tabs.Layout(gtx, th, p.cat, tabs) })
		if td.Size.X <= inner-sw-gtx.Dp(16) {
			t = op.Offset(pt(pad+inner-td.Size.X, yy)).Push(gtx.Ops)
			tc.Add(gtx.Ops)
			t.Pop()
		} else {
			// Not enough room beside the search: a row of their own.
			yy += gtx.Dp(HeightMd) + gtx.Dp(12)
			t = op.Offset(pt(pad, yy)).Push(gtx.Ops)
			tc.Add(gtx.Ops)
			t.Pop()
		}
	}
	yy += gtx.Dp(HeightMd) + gtx.Dp(20)

	// Grid.
	minW := st.MinCardWidth
	if minW == 0 {
		minW = 200
	}
	grid := Grid{MinWidth: minW, Gap: Sp16}
	gw := inner + pad/2 // leave the scrollbar in the right padding
	p.cols = grid.Columns(gtx, inner)
	cols := p.cols
	gap := gtx.Dp(Sp16)
	cw := (inner - gap*(cols-1)) / cols
	rows := (len(p.shown) + cols - 1) / cols
	lg := gtx
	lg.Constraints = layout.Exact(pt(gw, max(0, h-yy)))
	t = op.Offset(pt(pad, yy)).Push(gtx.Ops)
	if len(p.shown) == 0 {
		eg := lg
		eg.Constraints = layout.Constraints{Min: pt(inner, 0), Max: pt(inner, lg.Constraints.Max.Y)}
		layout.Inset{Top: Sp40}.Layout(eg, func(gtx C) D {
			return EmptyState{Icon: IconSearch, Title: "Нічого не знайдено", Text: "Спробуй інше слово або іншу категорію."}.Layout(gtx, th, nil)
		})
	}
	p.scroll.Layout(lg, th, rows, func(gtx C, row int) D {
		rh := 0
		var calls []op.CallOp
		for c := 0; c < cols; c++ {
			k := row*cols + c
			if k >= len(p.shown) {
				break
			}
			i := p.shown[k]
			cg := gtx
			cg.Constraints = layout.Constraints{Min: pt(cw, 0), Max: pt(cw, 1<<20)}
			d, call := record(cg, func(gtx C) D { return p.card(gtx, th, st, i) })
			rh = max(rh, d.Size.Y)
			calls = append(calls, call)
		}
		// Equal heights within a row: re-lay shorter cards at the row height.
		for c := range calls {
			i := p.shown[row*cols+c]
			cg := gtx
			cg.Constraints = layout.Constraints{Min: pt(cw, rh), Max: pt(cw, rh)}
			_, calls[c] = record(cg, func(gtx C) D { return p.card(gtx, th, st, i) })
		}
		for c, call := range calls {
			o := op.Offset(pt(c*(cw+gap), 0)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			o.Pop()
		}
		bottom := gap
		if row == rows-1 {
			bottom = pad
		}
		return D{Size: pt(gw, rh+bottom)}
	})
	t.Pop()
}

func (p *BigPicker) card(gtx C, th *Theme, st PickerStyle, i int) D {
	it := st.Items[i]
	selected := it.Value == st.Value
	hl := i == p.hi
	ratio := st.Ratio
	if ratio == 0 {
		ratio = 4.0 / 3
	}
	force := Force(0)
	if hl {
		force = ForceHover
	}
	return Card{Click: &p.items[i], Padding: NoPadding, Radius: RadiusInner, Selected: selected, Force: force}.Layout(gtx, th, func(gtx C) D {
		w := gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				ph := int(float32(w) / ratio)
				sz := pt(w, ph)
				// Top corners follow the card; the preview is clipped to them.
				rad := float32(gtx.Dp(RadiusInner))
				cl := clip.Outline{Path: rrPath(gtx.Ops, frect(rectWH(w, ph)), rad, rad, 0, 0)}.Op().Push(gtx.Ops)
				switch {
				case it.Image.Size().X > 0:
					drawImageFit(gtx, it.Image, sz, FitCover)
				case len(it.Swatches) > 0:
					sw := float32(w) / float32(len(it.Swatches))
					for k, c := range it.Swatches {
						x0 := int(float32(k) * sw)
						x1 := int(float32(k+1)*sw + 0.5)
						FillRect(gtx, image.Rect(x0, 0, x1, ph), c)
					}
				default:
					FillRect(gtx, rectWH(w, ph), th.Pal.Surface)
					ic := it.Icon
					if ic == nil {
						ic = IconImage
					}
					is := gtx.Dp(36)
					o := op.Offset(pt((w-is)/2, (ph-is)/2)).Push(gtx.Ops)
					ic.Layout(gtx, 36, th.Pal.Text3)
					o.Pop()
				}
				cl.Pop()
				if it.Badge != "" {
					o := op.Offset(pt(gtx.Dp(10), gtx.Dp(10))).Push(gtx.Ops)
					Badge{Text: it.Badge, Tone: ToneAction, Solid: true}.Layout(gtx, th)
					o.Pop()
				}
				if selected {
					d := gtx.Dp(28)
					o := op.Offset(pt(w-gtx.Dp(10)-d, gtx.Dp(10))).Push(gtx.Ops)
					fillCircle(gtx.Ops, f32.Pt(float32(d)/2, float32(d)/2), float32(d)/2, th.Pal.Action)
					ic := gtx.Dp(16)
					o2 := op.Offset(pt((d-ic)/2, (d-ic)/2)).Push(gtx.Ops)
					IconCheck.LayoutStroke(gtx, 16, 2.5, th.Pal.OnAction)
					o2.Pop()
					o.Pop()
				}
				return D{Size: sz}
			}),
			layout.Rigid(func(gtx C) D {
				return layout.UniformInset(Sp12).Layout(gtx, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(Text{Style: th.Body.Weighted(FontMedium), MaxLines: 1}.Widget(th, it.Label)),
						layout.Rigid(func(gtx C) D {
							if it.Caption == "" {
								return D{}
							}
							return layout.Inset{Top: 2}.Layout(gtx, Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, it.Caption))
						}),
					)
				})
			}),
		)
	})
}
