package gui

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// harness drives widgets through a real input router (no GPU) with a fixed
// clock that advances 16 ms per frame.
type harness struct {
	t   *testing.T
	th  *Theme
	r   input.Router
	ops op.Ops
	now time.Time
	w   int
	h   int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	th, err := NewTheme()
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, th: th, now: SnapshotEpoch, w: 800, h: 600}
}

// frame lays out draw inside th.Root and completes the frame.
func (h *harness) frame(draw func(gtx C)) {
	h.ops.Reset()
	gtx := layout.Context{
		Ops:         &h.ops,
		Now:         h.now,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(h.w, h.h)),
		Source:      h.r.Source(),
	}
	h.th.Root(gtx, func(gtx C) D {
		draw(gtx)
		return D{Size: gtx.Constraints.Max}
	})
	h.r.Frame(&h.ops)
	h.now = h.now.Add(16 * time.Millisecond)
}

// frames runs n frames.
func (h *harness) frames(n int, draw func(gtx C)) {
	for i := 0; i < n; i++ {
		h.frame(draw)
	}
}

func (h *harness) click(x, y float32) {
	p := f32.Pt(x, y)
	h.r.Queue(
		pointer.Event{Kind: pointer.Move, Position: p, Source: pointer.Mouse},
		pointer.Event{Kind: pointer.Press, Position: p, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary},
		pointer.Event{Kind: pointer.Release, Position: p, Source: pointer.Mouse},
	)
}

func (h *harness) key(name key.Name) {
	h.r.Queue(key.Event{Name: name, State: key.Press}, key.Event{Name: name, State: key.Release})
}

// typeText inserts s at [start, end) of the focused editor.
func (h *harness) typeText(start, end int, s string) {
	h.r.Queue(key.EditEvent{Range: key.Range{Start: start, End: end}, Text: s})
}

// at lays fn out at (x, y) with a maximum size of w×h.
func at(gtx C, x, y, w, h int, fn layout.Widget) D {
	defer op.Offset(image.Pt(x, y)).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Constraints{Max: image.Pt(w, h)}
	return fn(gtx)
}

// testCtx is a bare context (no router) at time now, for pure animation tests.
func testCtx(now time.Time, r *input.Router) C {
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Now: now, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
	if r != nil {
		gtx.Source = r.Source()
	}
	return gtx
}
