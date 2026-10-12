package gui

import (
	"image"
	"math"

	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
)

// Layout helpers for full-width, card-based pages (GUI.md "Layout").

// Gap is a fixed empty space in both directions (a flex spacer).
func Gap(dp unit.Dp) layout.FlexChild {
	return layout.Rigid(layout.Spacer{Width: dp, Height: dp}.Layout)
}

// VStack lays widgets top to bottom with gap between them, each at full width.
func VStack(gtx C, gap unit.Dp, ws ...layout.Widget) D {
	ch := make([]layout.FlexChild, 0, 2*len(ws))
	for i, w := range ws {
		if i > 0 {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: gap}.Layout))
		}
		w := w
		ch = append(ch, layout.Rigid(func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return w(gtx)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

// HStack lays widgets left to right, vertically centred, with gap between.
func HStack(gtx C, gap unit.Dp, ws ...layout.Widget) D {
	ch := make([]layout.FlexChild, 0, 2*len(ws))
	for i, w := range ws {
		if i > 0 {
			ch = append(ch, layout.Rigid(layout.Spacer{Width: gap}.Layout))
		}
		ch = append(ch, layout.Rigid(w))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
}

// Columns splits the width into columns by weight (8+4, 6+6, 4+4+4 of a
// 12-column grid) with gutters; every column gets the row's full height if
// Stretch is set (cards of equal height).
type Columns struct {
	Gap     unit.Dp // 0 = 24 dp
	Stretch bool
}

// Col is one column: a weight (e.g. 8 of 12) and its content.
type Col struct {
	Span float32
	W    layout.Widget
}

// Layout lays the columns out.
func (c Columns) Layout(gtx C, cols ...Col) D {
	gap := gtx.Dp(c.Gap)
	if c.Gap == 0 {
		gap = gtx.Dp(Sp24)
	}
	var total float32
	for _, col := range cols {
		total += col.Span
	}
	if total <= 0 || len(cols) == 0 {
		return D{}
	}
	avail := gtx.Constraints.Max.X - gap*(len(cols)-1)
	widths := make([]int, len(cols))
	used := 0
	for i, col := range cols {
		widths[i] = int(float32(avail) * col.Span / total)
		used += widths[i]
	}
	widths[len(widths)-1] += avail - used
	// First pass: natural heights.
	calls := make([]op.CallOp, len(cols))
	dims := make([]D, len(cols))
	h := 0
	for i, col := range cols {
		cg := gtx
		cg.Constraints = layout.Constraints{Min: pt(widths[i], 0), Max: pt(widths[i], gtx.Constraints.Max.Y)}
		dims[i], calls[i] = record(cg, col.W)
		h = max(h, dims[i].Size.Y)
	}
	if c.Stretch {
		for i, col := range cols {
			if dims[i].Size.Y == h {
				continue
			}
			cg := gtx
			cg.Constraints = layout.Constraints{Min: pt(widths[i], h), Max: pt(widths[i], h)}
			dims[i], calls[i] = record(cg, col.W)
		}
	}
	x := 0
	for i := range cols {
		o := op.Offset(pt(x, 0)).Push(gtx.Ops)
		calls[i].Add(gtx.Ops)
		o.Pop()
		x += widths[i] + gap
	}
	return D{Size: pt(gtx.Constraints.Max.X, h)}
}

// Grid lays n items in a responsive grid: as many columns as fit with at
// least MinWidth each (and at most MaxCols), items stretched to share the row
// width, rows as tall as their tallest item.
type Grid struct {
	MinWidth unit.Dp // 0 = 300 dp (GUI.md scene cards)
	Gap      unit.Dp // 0 = 24 dp
	MaxCols  int     // 0 = no limit
	// Stretch gives every item of a row the row's height.
	Stretch bool
}

// Columns returns the column count for a width in pixels.
func (g Grid) Columns(gtx C, width int) int {
	minW := gtx.Dp(g.MinWidth)
	if g.MinWidth == 0 {
		minW = gtx.Dp(300)
	}
	gap := gtx.Dp(g.Gap)
	if g.Gap == 0 {
		gap = gtx.Dp(Sp24)
	}
	n := max(1, (width+gap)/(minW+gap))
	if g.MaxCols > 0 {
		n = min(n, g.MaxCols)
	}
	return n
}

// Layout draws the items.
func (g Grid) Layout(gtx C, n int, item func(gtx C, i int) D) D {
	if n <= 0 {
		return D{Size: pt(gtx.Constraints.Min.X, 0)}
	}
	W := gtx.Constraints.Max.X
	gap := gtx.Dp(g.Gap)
	if g.Gap == 0 {
		gap = gtx.Dp(Sp24)
	}
	cols := g.Columns(gtx, W)
	cw := (W - gap*(cols-1)) / cols
	y := 0
	for row := 0; row*cols < n; row++ {
		start := row * cols
		end := min(n, start+cols)
		calls := make([]op.CallOp, end-start)
		dims := make([]D, end-start)
		h := 0
		for i := start; i < end; i++ {
			cg := gtx
			cg.Constraints = layout.Constraints{Min: pt(cw, 0), Max: pt(cw, gtx.Constraints.Max.Y)}
			i := i
			dims[i-start], calls[i-start] = record(cg, func(gtx C) D { return item(gtx, i) })
			h = max(h, dims[i-start].Size.Y)
		}
		if g.Stretch {
			for i := start; i < end; i++ {
				if dims[i-start].Size.Y == h {
					continue
				}
				cg := gtx
				cg.Constraints = layout.Constraints{Min: pt(cw, h), Max: pt(cw, h)}
				i := i
				dims[i-start], calls[i-start] = record(cg, func(gtx C) D { return item(gtx, i) })
			}
		}
		for i := start; i < end; i++ {
			o := op.Offset(pt((i-start)*(cw+gap), y)).Push(gtx.Ops)
			calls[i-start].Add(gtx.Ops)
			o.Pop()
		}
		y += h
		if end < n {
			y += gap
		}
	}
	return D{Size: pt(W, y)}
}

// Scroll is a vertical scroll area with a slim bar that fades in while the
// pointer is over it or it moves. Keep one per scrolling region.
//
//	s.page.Layout(gtx, th, len(rows), func(gtx C, i int) D { … })
type Scroll struct {
	List widget.List

	hover gesture.Hover
	bar   Anim
	last  layout.Position
}

// Layout lays out n elements.
func (s *Scroll) Layout(gtx C, th *Theme, n int, el layout.ListElement) D {
	s.List.Axis = layout.Vertical
	hovered := s.hover.Update(gtx.Source)
	d := s.List.List.Layout(gtx, n, el)
	pos := s.List.Position
	moving := pos.First != s.last.First || pos.Offset != s.last.Offset
	s.last = pos
	// Hover area for the bar (pass-through, so the content still gets events).
	func() {
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
		defer clip.Rect(rectWH(d.Size.X, d.Size.Y)).Push(gtx.Ops).Pop()
		s.hover.Add(gtx.Ops)
	}()
	start, end := viewport(pos, n, d.Size.Y)
	if end-start >= 0.999 || n == 0 {
		return d
	}
	s.bar.Duration = 200e6
	vis := s.bar.Animate(gtx, b2f(hovered || moving || s.List.Scrollbar.Dragging()))
	barW := gtx.Dp(10)
	gg := gtx
	gg.Constraints = layout.Exact(pt(barW, d.Size.Y))
	o := op.Offset(pt(d.Size.X-barW, 0)).Push(gtx.Ops)
	s.List.Scrollbar.Update(gg, layout.Vertical, start, end)
	drawBar(gg, th, &s.List.Scrollbar, start, end, vis)
	o.Pop()
	if delta := s.List.Scrollbar.ScrollDistance(); delta != 0 {
		s.List.List.ScrollBy(delta * float32(n))
	}
	return d
}

// LayoutWidget scrolls one (tall) widget.
func (s *Scroll) LayoutWidget(gtx C, th *Theme, w layout.Widget) D {
	return s.Layout(gtx, th, 1, func(gtx C, _ int) D { return w(gtx) })
}

// viewport estimates the visible fraction [start, end] of a list (as
// material.List does).
func viewport(lp layout.Position, n, major int) (float32, float32) {
	if n == 0 || lp.Length <= 0 {
		return 0, 1
	}
	length := float32(lp.Length)
	per := length / float32(n)
	start := clamp01((float32(lp.First)*per + float32(lp.Offset)) / length)
	end := clamp01((float32(lp.First+lp.Count)*per + float32(lp.OffsetLast)) / length)
	vf := end - start
	visible := float32(major) / length
	if vf < 1 {
		err := visible - vf
		ss := start / (1 - vf)
		es := (1 - end) / (1 - vf)
		start -= ss * err
		end += es * err
	}
	return clamp01(start), clamp01(end)
}

// drawBar paints a slim rounded scrollbar thumb (grows a little when
// hovered) and registers its drag/track input.
func drawBar(gtx C, th *Theme, sb *widget.Scrollbar, start, end, vis float32) {
	size := gtx.Constraints.Min
	area := clip.Rect(rectWH(size.X, size.Y)).Push(gtx.Ops)
	sb.AddDrag(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	sb.AddTrack(gtx.Ops)
	pass.Pop()
	area.Pop()
	if vis < 0.01 {
		return
	}
	pad := gtx.Dp(2)
	track := size.Y - 2*pad
	y0 := pad + int(math.Round(float64(start)*float64(track)))
	y1 := pad + int(math.Round(float64(end)*float64(track)))
	if y1-y0 < gtx.Dp(32) {
		y1 = min(pad+track, y0+gtx.Dp(32))
		y0 = y1 - gtx.Dp(32)
	}
	w := gtx.Dp(4)
	if sb.IndicatorHovered() || sb.Dragging() {
		w = gtx.Dp(6)
	}
	x := size.X - pad - w
	c := WithAlpha(th.Pal.Text3, 0.55*vis)
	if sb.Dragging() {
		c = WithAlpha(th.Pal.Text2, 0.8*vis)
	}
	r := image.Rect(x, y0, x+w, y1)
	fillRRf(gtx.Ops, frect(r), float32(w)/2, c)
	ind := clip.Rect(image.Rect(x-gtx.Dp(3), y0, size.X, y1)).Push(gtx.Ops)
	pass = pointer.PassOp{}.Push(gtx.Ops)
	sb.AddIndicator(gtx.Ops)
	pass.Pop()
	ind.Pop()
}

// Flow lays widgets left to right and wraps them onto new lines (chip rows,
// button groups). Items in a line are vertically centred.
type Flow struct {
	Gap    unit.Dp // between items; 0 = 8 dp
	RowGap unit.Dp // between lines; 0 = Gap
}

// Layout draws the widgets.
func (f Flow) Layout(gtx C, ws ...layout.Widget) D {
	gap := gtx.Dp(f.Gap)
	if f.Gap == 0 {
		gap = gtx.Dp(Sp8)
	}
	rowGap := gap
	if f.RowGap != 0 {
		rowGap = gtx.Dp(f.RowGap)
	}
	maxW := gtx.Constraints.Max.X
	type item struct {
		d    D
		call op.CallOp
	}
	items := make([]item, len(ws))
	for i, w := range ws {
		cg := gtx
		cg.Constraints = layout.Constraints{Max: pt(maxW, gtx.Constraints.Max.Y)}
		items[i].d, items[i].call = record(cg, w)
	}
	x, y, lineH, width := 0, 0, 0, 0
	start := 0
	place := func(end int) {
		// Centre the line's items vertically.
		lx := 0
		for k := start; k < end; k++ {
			it := items[k]
			o := op.Offset(pt(lx, y+(lineH-it.d.Size.Y)/2)).Push(gtx.Ops)
			it.call.Add(gtx.Ops)
			o.Pop()
			lx += it.d.Size.X + gap
		}
	}
	for i, it := range items {
		w := it.d.Size.X
		if i > start && x+w > maxW {
			place(i)
			y += lineH + rowGap
			x, lineH, start = 0, 0, i
		}
		x += w
		width = max(width, x)
		x += gap
		lineH = max(lineH, it.d.Size.Y)
	}
	place(len(items))
	return D{Size: pt(max(width, gtx.Constraints.Min.X), y+lineH)}
}
