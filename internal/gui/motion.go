package gui

import (
	"image/color"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// Motion toolkit. Every animation is a function of gtx.Now (so headless
// renders with a fixed clock are deterministic) and asks for the next frame
// with op.InvalidateCmd only while it is running. With ReducedMotion every
// animation jumps to its target.

// Easing maps linear progress t ∈ [0,1] to eased progress.
type Easing func(t float32) float32

// Easing curves (GUI.md: easeOutCubic for enter, easeInOutCubic for moves).
var (
	Linear         Easing = func(t float32) float32 { return t }
	EaseOutCubic   Easing = func(t float32) float32 { u := 1 - t; return 1 - u*u*u }
	EaseInCubic    Easing = func(t float32) float32 { return t * t * t }
	EaseInOutCubic Easing = func(t float32) float32 {
		if t < 0.5 {
			return 4 * t * t * t
		}
		u := -2*t + 2
		return 1 - u*u*u/2
	}
	EaseOutQuart Easing = func(t float32) float32 { u := 1 - t; return 1 - u*u*u*u }
)

func clamp01(t float32) float32 { return min(1, max(0, t)) }

// Anim is a time-based float animation. The zero value is ready: it jumps to
// the first target it sees, then eases to every new target over Duration.
//
//	v := s.hover.Animate(gtx, target)   // every frame
type Anim struct {
	Duration time.Duration // 0 = DurHover
	Ease     Easing        // nil = EaseOutCubic

	from, to float32
	start    time.Time
	running  bool
	init     bool
}

// Set jumps to v immediately (no animation).
func (a *Anim) Set(v float32) {
	a.from, a.to, a.running, a.init = v, v, false, true
}

// To starts animating from the current value towards target (no-op when
// the target is unchanged). The clock starts at gtx.Now.
func (a *Anim) To(gtx C, target float32) {
	if !a.init {
		a.Set(target)
		return
	}
	if target == a.to {
		return
	}
	if ReducedMotion() {
		a.Set(target)
		return
	}
	cur := a.value(gtx.Now)
	a.from, a.to, a.start, a.running = cur, target, gtx.Now, true
	gtx.Execute(op.InvalidateCmd{})
}

// Value returns the value at gtx.Now and requests a redraw while running.
func (a *Anim) Value(gtx C) float32 {
	v := a.value(gtx.Now)
	if a.running {
		gtx.Execute(op.InvalidateCmd{})
	}
	return v
}

// Animate is To followed by Value — the usual one-liner.
func (a *Anim) Animate(gtx C, target float32) float32 {
	a.To(gtx, target)
	return a.Value(gtx)
}

// Target returns the value the animation is heading to.
func (a *Anim) Target() float32 { return a.to }

// Running reports whether the animation is still moving.
func (a *Anim) Running() bool { return a.running }

func (a *Anim) value(now time.Time) float32 {
	if !a.running {
		return a.to
	}
	d := a.Duration
	if d <= 0 {
		d = DurHover
	}
	t := float32(now.Sub(a.start)) / float32(d)
	if t >= 1 || ReducedMotion() {
		a.running = false
		a.from = a.to
		return a.to
	}
	if t < 0 {
		t = 0
	}
	e := a.Ease
	if e == nil {
		e = EaseOutCubic
	}
	return a.from + (a.to-a.from)*e(t)
}

// Spring is a critically damped spring (no overshoot, no bounce) for press
// feedback: it follows its target with a smooth, quick settle. Zero value is
// ready; Settle is the time to get within 1 % of a step (default 220 ms).
type Spring struct {
	Settle time.Duration

	x, v   float32
	target float32
	last   time.Time
	init   bool
	moving bool
}

// Animate moves the spring towards target and returns its position.
func (s *Spring) Animate(gtx C, target float32) float32 {
	if !s.init || ReducedMotion() {
		s.x, s.v, s.target, s.last, s.init, s.moving = target, 0, target, gtx.Now, true, false
		return target
	}
	if target != s.target {
		s.target = target
		if !s.moving {
			s.last = gtx.Now
		}
		s.moving = true
	}
	if !s.moving {
		return s.x
	}
	dt := float32(gtx.Now.Sub(s.last).Seconds())
	s.last = gtx.Now
	if dt > 0 {
		s.step(min(dt, 0.1))
	}
	if abs32(s.x-s.target) < 1e-3 && abs32(s.v) < 1e-2 {
		s.x, s.v, s.moving = s.target, 0, false
		return s.x
	}
	gtx.Execute(op.InvalidateCmd{})
	return s.x
}

// Value is the current position (no update).
func (s *Spring) Value() float32 { return s.x }

// Moving reports whether the spring has not settled yet.
func (s *Spring) Moving() bool { return s.moving }

// step advances the analytic critically damped solution by dt seconds:
// x(t) = target + (x0' + (v0 + ω·x0')·t)·e^(−ωt), x0' = x0 − target.
func (s *Spring) step(dt float32) {
	settle := s.Settle
	if settle <= 0 {
		settle = DurPress
	}
	// (1 + ωt)e^(−ωt) = 0.01 at ωt ≈ 6.64.
	w := 6.64 / float32(settle.Seconds())
	x0 := s.x - s.target
	e := float32(math.Exp(float64(-w * dt)))
	c := s.v + w*x0
	s.x = s.target + (x0+c*dt)*e
	s.v = (c - w*(x0+c*dt)) * e
}

// ColorAnim eases between colours (hover fills, borders, states).
type ColorAnim struct {
	Duration time.Duration // 0 = DurHover
	Ease     Easing

	from, to color.NRGBA
	t        Anim
	init     bool
}

// Animate moves towards target and returns the colour of this frame.
func (c *ColorAnim) Animate(gtx C, target color.NRGBA) color.NRGBA {
	if !c.init || ReducedMotion() {
		c.from, c.to, c.init = target, target, true
		c.t.Set(1)
		return target
	}
	if target != c.to {
		cur := LerpColor(c.from, c.to, c.progress(gtx))
		c.from, c.to = cur, target
		c.t.Duration, c.t.Ease = c.Duration, c.Ease
		c.t.Set(0)
		c.t.To(gtx, 1)
	}
	return LerpColor(c.from, c.to, c.t.Value(gtx))
}

func (c *ColorAnim) progress(gtx C) float32 { return c.t.value(gtx.Now) }

// Stagger reveals list items one after another on first load (GUI.md: 12 ms
// apart, at most 12 staggered items, no bounce). Call Progress for each item
// every frame; Restart replays it (e.g. after a route change).
type Stagger struct {
	Step     time.Duration // 0 = 12 ms
	Duration time.Duration // per item; 0 = 180 ms
	Max      int           // items that stagger; later ones share the last delay (0 = 12)

	start time.Time
	begun bool
	done  bool
}

// Restart replays the entry animation from the next frame.
func (s *Stagger) Restart() { s.begun, s.done = false, false }

// Progress returns item i's entry progress in [0,1] (eased) at gtx.Now.
func (s *Stagger) Progress(gtx C, i int) float32 {
	if s.done || ReducedMotion() {
		return 1
	}
	if !s.begun {
		s.begun, s.start = true, gtx.Now
	}
	step, dur, maxN := s.Step, s.Duration, s.Max
	if step <= 0 {
		step = 12 * time.Millisecond
	}
	if dur <= 0 {
		dur = DurRoute
	}
	if maxN <= 0 {
		maxN = 12
	}
	el := gtx.Now.Sub(s.start)
	if el >= dur+step*time.Duration(maxN) {
		s.done = true
		return 1
	}
	gtx.Execute(op.InvalidateCmd{})
	d := step * time.Duration(min(i, maxN))
	return EaseOutCubic(clamp01(float32(el-d) / float32(dur)))
}

// Done reports whether every item has fully entered.
func (s *Stagger) Done() bool { return s.done }

// Fade draws w with opacity alpha (0…1); fully opaque skips the layer.
func Fade(gtx C, alpha float32, w layout.Widget) D {
	if alpha >= 0.999 {
		return w(gtx)
	}
	if alpha <= 0.001 {
		// Keep the layout (and input areas) but draw nothing visible.
		m := op.Record(gtx.Ops)
		d := w(gtx)
		m.Stop()
		return d
	}
	defer paint.PushOpacity(gtx.Ops, alpha).Pop()
	return w(gtx)
}

// Offset draws w shifted by (dx, dy) dp without changing its dimensions.
func Offset(gtx C, dx, dy unit.Dp, w layout.Widget) D {
	if dx == 0 && dy == 0 {
		return w(gtx)
	}
	x := float32(dx) * gtx.Metric.PxPerDp
	y := float32(dy) * gtx.Metric.PxPerDp
	defer op.Affine(f32.AffineId().Offset(f32.Pt(x, y))).Push(gtx.Ops).Pop()
	return w(gtx)
}

// FadeUp is the entry effect of routes and list items: at progress p the
// child is p opaque and (1−p)·rise dp below its place.
func FadeUp(gtx C, p float32, rise unit.Dp, w layout.Widget) D {
	if p >= 0.999 {
		return w(gtx)
	}
	return Fade(gtx, p, func(gtx C) D { return Offset(gtx, 0, rise*unit.Dp(1-p), w) })
}

// Scale draws w scaled by s around its centre (press feedback, dialogs). The
// layout size is the unscaled size.
func Scale(gtx C, s float32, w layout.Widget) D {
	if s == 1 {
		return w(gtx)
	}
	d, call := record(gtx, w)
	c := f32.Pt(float32(d.Size.X)/2, float32(d.Size.Y)/2)
	defer op.Affine(f32.AffineId().Scale(c, f32.Pt(s, s))).Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
	return d
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// Route animates page changes (GUI.md): when the key changes, the old page
// fades out in 90 ms, then the new one fades in rising 8 dp over 180 ms. The
// outgoing page draws without input. Keep one Route per navigation area:
//
//	s.route.Layout(gtx, s.page, func(gtx C) D { return s.pages[s.page](gtx) })
type Route struct {
	key     any
	prev    layout.Widget
	last    layout.Widget
	start   time.Time
	started bool
	running bool
}

// Layout draws w for key, animating when key differs from the last frame's.
func (r *Route) Layout(gtx C, key any, w layout.Widget) D {
	if !r.started {
		r.key, r.started = key, true
	}
	if key != r.key {
		r.key, r.prev, r.running = key, r.last, true
		r.start = gtx.Now
	}
	r.last = w
	if !r.running || ReducedMotion() {
		r.running, r.prev = false, nil
		return w(gtx)
	}
	el := gtx.Now.Sub(r.start)
	gtx.Execute(op.InvalidateCmd{})
	if el < DurFade && r.prev != nil {
		a := 1 - EaseInCubic(float32(el)/float32(DurFade))
		return Fade(gtx.Disabled(), a, r.prev)
	}
	t := float32(el-DurFade) / float32(DurRoute)
	if r.prev == nil {
		t = float32(el) / float32(DurRoute)
	}
	if t >= 1 {
		r.running, r.prev = false, nil
		return w(gtx)
	}
	return FadeUp(gtx, EaseOutCubic(clamp01(t)), 8, w)
}

// Animating reports whether a page change is in progress.
func (r *Route) Animating() bool { return r.running }
