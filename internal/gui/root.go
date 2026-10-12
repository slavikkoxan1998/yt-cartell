package gui

import (
	"image"
	"strings"
	"sync"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
)

// services are the window-wide helpers Root runs every frame.
type services struct {
	win     image.Point // window size (px) of the current frame
	layers  []func(gtx C)
	tip     tipState
	toasts  toastHost
	anchors anchorState
	tour    *Tour // the tour drawing this frame, if any

	tracker   int // pointer tracker tag
	pointer   f32.Point
	pointerIn bool
}

func (s *services) init() {
	s.anchors.rects = map[string]image.Rectangle{}
}

// Root lays out the whole window: it paints the ground, advances the theme
// crossfade, draws w (your app) and then the overlay layers above it —
// dialogs, drawers and pickers (in the order they were laid out), toasts, the
// tour and the tooltip. Call it once per frame with the window's gtx.
func (th *Theme) Root(gtx C, w layout.Widget) D {
	th.advancePalette(gtx)
	s := &th.svc
	size := gtx.Constraints.Max
	s.win = size
	s.layers = s.layers[:0]
	s.tour = nil
	s.tip.seen = false
	s.anchors.want = false
	th.readPointer(gtx)

	FillRect(gtx, image.Rectangle{Max: size}, th.Pal.Ground)
	cgtx := gtx
	cgtx.Constraints = layout.Exact(size)
	w(cgtx)

	// Modal layers, each deferred so it sits above every popover of the
	// content. Layers may add layers (a picker opened from a dialog).
	for i := 0; i < len(s.layers); i++ {
		fn := s.layers[i]
		m := op.Record(gtx.Ops)
		fn(cgtx)
		op.Defer(gtx.Ops, m.Stop())
	}
	th.layoutToasts(cgtx)
	th.resolveAnchors(gtx)
	if s.tour != nil {
		m := op.Record(gtx.Ops)
		s.tour.layoutOverlay(cgtx, th)
		op.Defer(gtx.Ops, m.Stop())
	}
	th.layoutTooltip(cgtx)
	th.trackPointer(gtx)
	return D{Size: size}
}

// Layer queues fn to draw above the window content this frame, in window
// coordinates (gtx covers the whole window). Dialog, Drawer and BigPicker use
// it; custom overlays can too. Outside Root it draws immediately.
func (th *Theme) Layer(gtx C, fn func(gtx C)) {
	if th.svc.win == (image.Point{}) {
		fn(gtx)
		return
	}
	th.svc.layers = append(th.svc.layers, fn)
}

// WindowSize is the window size in pixels of the current frame (zero
// outside Root).
func (th *Theme) WindowSize() image.Point { return th.svc.win }

// ---- pointer tracking -------------------------------------------------------------

// readPointer consumes the tracker's events: the pointer position in window
// coordinates (tooltips) and whether it is inside the window.
func (th *Theme) readPointer(gtx C) {
	s := &th.svc
	for {
		e, ok := gtx.Event(pointer.Filter{Target: &s.tracker, Kinds: pointer.Move | pointer.Enter | pointer.Leave})
		if !ok {
			break
		}
		if pe, ok := e.(pointer.Event); ok {
			s.pointer = pe.Position
			s.pointerIn = pe.Kind != pointer.Leave
		}
	}
}

// trackPointer registers a pass-through window-sized handler on top of
// everything (deferred last), so it sees the pointer over any layer without
// taking events from the widgets underneath.
func (th *Theme) trackPointer(gtx C) {
	m := op.Record(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect(image.Rectangle{Max: th.svc.win}).Push(gtx.Ops)
	event.Op(gtx.Ops, &th.svc.tracker)
	area.Pop()
	pass.Pop()
	op.Defer(gtx.Ops, m.Stop())
}

// Pointer returns the pointer position in window coordinates and whether it
// is inside the window (as of the latest pointer move).
func (th *Theme) Pointer() (f32.Point, bool) { return th.svc.pointer, th.svc.pointerIn }

// ---- anchors ---------------------------------------------------------------------

const anchorPrefix = "gui.anchor:"

// anchorState resolves anchor rectangles to window coordinates by running the
// frame's operations through a private input router and reading the bounds
// of the semantic descriptions Anchor leaves (Gio has no public way to ask
// where a widget ended up). It only runs on frames that need it.
type anchorState struct {
	want   bool
	rects  map[string]image.Rectangle
	router input.Router
	nodes  []input.SemanticNode
}

// Anchor lays out w and marks its area with key, so the tour (and popovers)
// can find it in window coordinates: th.AnchorRect(key).
func Anchor(gtx C, th *Theme, key string, w layout.Widget) D {
	d := w(gtx)
	area := clip.Rect(image.Rectangle{Max: d.Size}).Push(gtx.Ops)
	semantic.DescriptionOp(anchorPrefix + key).Add(gtx.Ops)
	area.Pop()
	return d
}

// WantAnchors asks Root to resolve anchor rectangles at the end of this
// frame (popovers call it while open; the tour while running).
func (th *Theme) WantAnchors() { th.svc.anchors.want = true }

// AnchorRect returns the window rectangle (px) of the anchor laid out under
// key, as resolved at the end of the last frame that wanted anchors.
func (th *Theme) AnchorRect(key string) (image.Rectangle, bool) {
	r, ok := th.svc.anchors.rects[key]
	return r, ok
}

func (th *Theme) resolveAnchors(gtx C) {
	a := &th.svc.anchors
	if !a.want && th.svc.tour == nil {
		return
	}
	a.router.Frame(gtx.Ops)
	a.nodes = a.router.AppendSemantics(a.nodes[:0])
	clear(a.rects)
	// AppendSemantics returns the whole tree flattened; Children alias it.
	for _, n := range a.nodes {
		if k, ok := strings.CutPrefix(n.Desc.Description, anchorPrefix); ok {
			a.rects[k] = n.Desc.Bounds
		}
	}
}

// ---- tooltips ----------------------------------------------------------------------

// tipState is the tooltip of the control under the pointer.
type tipState struct {
	owner   any // the control's state pointer; nil = none
	text    string
	since   time.Time // hover start
	seen    bool      // requested during this frame
	blocked bool      // pressed: hidden until the pointer leaves
	fade    Anim
}

// Tooltip requests text as the tooltip of owner (the widget's state pointer)
// while it is hovered; Root shows it after TipDelay (400 ms). Call it every
// frame while hovered; Button does this for its Tip.
func (th *Theme) Tooltip(gtx C, owner any, pressed bool, text string) {
	if text == "" || owner == nil {
		return
	}
	ts := &th.svc.tip
	if ts.owner != owner {
		ts.owner, ts.since, ts.blocked = owner, gtx.Now, false
		ts.fade.Set(0)
	}
	if pressed {
		ts.blocked = true
	}
	ts.text, ts.seen = text, true
}

func (th *Theme) layoutTooltip(gtx C) {
	ts := &th.svc.tip
	if !ts.seen {
		ts.owner = nil
		return
	}
	ptr, in := th.Pointer()
	if ts.blocked || !in {
		return
	}
	if wait := TipDelay - gtx.Now.Sub(ts.since); wait > 0 {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(wait)})
		return
	}
	ts.fade.Duration = DurHover
	alpha := ts.fade.Animate(gtx, 1)
	m := op.Record(gtx.Ops)
	drawTipBubble(gtx, th, ptr, ts.text, alpha)
	op.Defer(gtx.Ops, m.Stop())
}

// drawTipBubble draws a small dark bubble below the pointer (above it near the
// bottom edge), kept inside the window.
func drawTipBubble(gtx C, th *Theme, ptr f32.Point, txt string, alpha float32) {
	W, H := th.svc.win.X, th.svc.win.Y
	padX, padY := gtx.Dp(10), gtx.Dp(6)
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(max(0, min(gtx.Dp(280), W-gtx.Dp(16))-2*padX), H)}
	d, call := record(lg, func(gtx C) D {
		return Text{Style: th.Caption.Weighted(FontMedium), Color: th.Pal.TooltipFg, MaxLines: 3}.Layout(gtx, th, txt)
	})
	w, h := d.Size.X+2*padX, d.Size.Y+2*padY
	m := gtx.Dp(8)
	x := int(ptr.X) - w/2
	y := int(ptr.Y) + gtx.Dp(22)
	if y+h > H-m {
		y = int(ptr.Y) - h - gtx.Dp(12)
	}
	x = max(m, min(x, W-m-w))
	y = max(m, y)
	defer op.Offset(pt(x, y)).Push(gtx.Ops).Pop()
	defer opacity(gtx, alpha)()
	r := rectWH(w, h)
	Shadow(gtx, th, r, RadiusSmall, ElevPop)
	FillRRect(gtx, r, RadiusSmall, th.Pal.TooltipBg)
	t := op.Offset(pt(padX, padY)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
}

// ---- invalidation from other goroutines ------------------------------------------

var invalidateMu sync.Mutex

// SetInvalidate installs the window's Invalidate (app.Window.Invalidate) so
// background events (Toast from a goroutine, image decodes) can wake the UI.
func (th *Theme) SetInvalidate(fn func()) {
	invalidateMu.Lock()
	th.svc.toasts.invalidate = fn
	invalidateMu.Unlock()
}

func (th *Theme) invalidate() {
	invalidateMu.Lock()
	fn := th.svc.toasts.invalidate
	invalidateMu.Unlock()
	if fn != nil {
		fn()
	}
}
