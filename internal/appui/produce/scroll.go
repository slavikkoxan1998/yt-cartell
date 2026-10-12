package produce

import (
	"image"
	"math"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/gui"
)

// pageList is a step's virtualised page: a gui.Scroll over elements (the
// header, section heads, one element per grid row — only the visible ones
// are built) that remembers where the visible elements are and can glide to
// an element (jumps from the scene navigator).
type pageList struct {
	sc gui.Scroll

	heights map[int]int // element → height, laid out this frame
	ys      map[int]int // element → y from the viewport top (visible ones)
	viewH   int

	// Smooth scroll: total px to travel, applied of them done.
	anim    gui.Anim
	moving  bool
	total   float32
	applied int
}

const scrollDur = 460 * time.Millisecond

// Layout lays out n elements, each inset by inset px on both sides (the
// list itself spans the bleed, so shadows and the scrollbar have room).
func (l *pageList) Layout(gtx C, th *gui.Theme, n, inset int, el layout.ListElement) D {
	if l.heights == nil {
		l.heights, l.ys = map[int]int{}, map[int]int{}
	}
	l.viewH = gtx.Constraints.Max.Y
	pos := &l.sc.List.Position
	if l.moving {
		if l.sc.List.List.Dragging() || l.sc.List.Scrollbar.Dragging() {
			l.moving = false // the user took over
		} else {
			v := l.anim.Value(gtx)
			want := int(math.Round(float64(l.total * v)))
			pos.Offset += want - l.applied
			pos.BeforeEnd = true
			l.applied = want
			if !l.anim.Running() {
				l.moving = false
			}
		}
	}
	clear(l.heights)
	d := l.sc.Layout(gtx, th, n, func(gtx C, i int) D {
		W := gtx.Constraints.Max.X
		cg := gtx
		cg.Constraints.Max.X = max(0, W-2*inset)
		cg.Constraints.Min.X = min(cg.Constraints.Min.X, cg.Constraints.Max.X)
		m := op.Record(gtx.Ops)
		dd := el(cg, i)
		call := m.Stop()
		o := op.Offset(image.Pt(inset, 0)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		o.Pop()
		l.heights[i] = dd.Size.Y
		return D{Size: image.Pt(W, dd.Size.Y)}
	})
	clear(l.ys)
	y := -pos.Offset
	for i := pos.First; ; i++ {
		h, ok := l.heights[i]
		if !ok {
			break
		}
		l.ys[i] = y
		y += h
	}
	return d
}

// ScrollTo glides so element i sits margin px below the top. A far target
// is first brought near (just outside the view) and the last stretch is
// animated, so every jump reads as one smooth move.
func (l *pageList) ScrollTo(gtx C, i, margin int) {
	pos := &l.sc.List.Position
	if y, ok := l.ys[i]; ok && y > -l.viewH && y < 2*l.viewH {
		l.start(gtx, float32(y-margin))
		return
	}
	travel := int(float32(max(l.viewH, 400)) * 0.55)
	below := i >= pos.First
	pos.First, pos.BeforeEnd = i, true
	if below {
		pos.Offset = -(margin + travel)
		l.start(gtx, float32(travel))
	} else {
		pos.Offset = -(margin - travel)
		l.start(gtx, float32(-travel))
	}
}

// Top jumps to the first element (another project).
func (l *pageList) Top() {
	l.moving = false
	l.sc.List.Position = layout.Position{}
}

func (l *pageList) start(gtx C, total float32) {
	if gui.ReducedMotion() || total == 0 {
		l.sc.List.Position.Offset += int(total)
		l.moving = false
		return
	}
	l.total, l.applied, l.moving = total, 0, true
	l.anim.Duration, l.anim.Ease = scrollDur, gui.EaseInOutCubic
	l.anim.Set(0)
	l.anim.To(gtx, 1)
}
