package gui

import (
	"image/color"
	"math"
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
)

func approx(a, b, eps float32) bool { return math.Abs(float64(a-b)) <= float64(eps) }

func TestEasings(t *testing.T) {
	for name, e := range map[string]Easing{
		"linear": Linear, "outCubic": EaseOutCubic, "inCubic": EaseInCubic,
		"inOutCubic": EaseInOutCubic, "outQuart": EaseOutQuart,
	} {
		if !approx(e(0), 0, 1e-6) || !approx(e(1), 1, 1e-6) {
			t.Errorf("%s: e(0)=%v e(1)=%v", name, e(0), e(1))
		}
		prev := float32(-1)
		for i := 0; i <= 100; i++ {
			v := e(float32(i) / 100)
			if v < prev-1e-6 {
				t.Errorf("%s: not monotonic at %d", name, i)
			}
			prev = v
		}
	}
	if !approx(EaseOutCubic(0.5), 0.875, 1e-6) || !approx(EaseInOutCubic(0.5), 0.5, 1e-6) {
		t.Errorf("midpoints: outCubic %v inOutCubic %v", EaseOutCubic(0.5), EaseInOutCubic(0.5))
	}
}

// wakes reports whether the frame asked for another frame.
func wakes(r *input.Router, ops *op.Ops) bool {
	r.Frame(ops)
	_, w := r.WakeupTime()
	return w
}

func TestAnimDeterministic(t *testing.T) {
	var r input.Router
	t0 := SnapshotEpoch
	a := Anim{Duration: 200 * time.Millisecond}
	frame := func(at time.Duration, target float32) (float32, bool) {
		var ops op.Ops
		gtx := layout.Context{Ops: &ops, Now: t0.Add(at), Source: r.Source()}
		v := a.Animate(gtx, target)
		return v, wakes(&r, &ops)
	}
	// The first target is adopted without animating and without redraws.
	if v, w := frame(0, 0); v != 0 || w {
		t.Fatalf("first frame = %v wake=%v, want 0 and no wake", v, w)
	}
	// A new target starts at the current value and asks for frames.
	if v, w := frame(0, 1); v != 0 || !w {
		t.Fatalf("start = %v wake=%v, want 0 and a wake", v, w)
	}
	if v, _ := frame(100*time.Millisecond, 1); !approx(v, EaseOutCubic(0.5), 1e-5) {
		t.Errorf("half-way = %v, want %v", v, EaseOutCubic(0.5))
	}
	if v, w := frame(200*time.Millisecond, 1); v != 1 {
		t.Errorf("end = %v (wake %v), want 1", v, w)
	}
	if v, w := frame(216*time.Millisecond, 1); v != 1 || w || a.Running() {
		t.Errorf("settled = %v wake=%v running=%v, want 1, no wake", v, w, a.Running())
	}
	// Re-targeting mid-flight continues from where it is.
	frame(300*time.Millisecond, 0)
	mid, _ := frame(400*time.Millisecond, 0)
	v, _ := frame(400*time.Millisecond, 0.5)
	if !approx(v, mid, 1e-5) {
		t.Errorf("retarget jumped: %v → %v", mid, v)
	}
	if !approx(a.Target(), 0.5, 0) {
		t.Errorf("target = %v, want 0.5", a.Target())
	}
}

func TestAnimReducedMotion(t *testing.T) {
	SetReducedMotion(true)
	defer SetReducedMotion(false)
	a := Anim{Duration: time.Second}
	gtx := testCtx(SnapshotEpoch, nil)
	a.Animate(gtx, 0)
	if v := a.Animate(gtx, 1); v != 1 {
		t.Errorf("reduced motion value = %v, want 1 at once", v)
	}
	var s Spring
	s.Animate(gtx, 1)
	if v := s.Animate(gtx, 0.97); v != 0.97 {
		t.Errorf("reduced motion spring = %v, want 0.97", v)
	}
	var st Stagger
	if p := st.Progress(gtx, 5); p != 1 {
		t.Errorf("reduced motion stagger = %v, want 1", p)
	}
}

func TestSpringCriticallyDamped(t *testing.T) {
	s := Spring{Settle: 220 * time.Millisecond}
	now := SnapshotEpoch
	s.Animate(testCtx(now, nil), 1)
	prev := float32(1)
	var v float32
	for i := 1; i <= 30; i++ {
		now = now.Add(16 * time.Millisecond)
		v = s.Animate(testCtx(now, nil), 0.97)
		if v > prev+1e-6 {
			t.Fatalf("frame %d: moved away from the target (%v → %v)", i, prev, v)
		}
		if v < 0.97-1e-4 {
			t.Fatalf("frame %d: overshoot %v", i, v)
		}
		prev = v
		// Motion starts on the frame the target changed (i = 1); 220 ms of
		// motion later it is within 1 % of the step.
		if i == 15 {
			if !approx(v, 0.97, 0.03*0.01) {
				t.Errorf("after 224 ms of motion v = %v, want within 1 %% of 0.97", v)
			}
		}
	}
	if s.Moving() || v != 0.97 {
		t.Errorf("not settled: v=%v moving=%v", v, s.Moving())
	}
}

func TestColorAnim(t *testing.T) {
	c := ColorAnim{Duration: 100 * time.Millisecond, Ease: Linear}
	a, b := color.NRGBA{R: 0, A: 255}, color.NRGBA{R: 200, A: 255}
	now := SnapshotEpoch
	c.Animate(testCtx(now, nil), a)
	c.Animate(testCtx(now, nil), b)
	got := c.Animate(testCtx(now.Add(50*time.Millisecond), nil), b)
	if got.R < 95 || got.R > 105 {
		t.Errorf("half-way colour R = %d, want ≈ 100", got.R)
	}
	if got := c.Animate(testCtx(now.Add(100*time.Millisecond), nil), b); got != b {
		t.Errorf("end colour = %v, want %v", got, b)
	}
}

func TestStagger(t *testing.T) {
	s := Stagger{Step: 12 * time.Millisecond, Duration: 180 * time.Millisecond, Max: 12}
	t0 := SnapshotEpoch
	if p := s.Progress(testCtx(t0, nil), 0); p != 0 {
		t.Errorf("item 0 at start = %v, want 0", p)
	}
	at := func(d time.Duration, i int) float32 { return s.Progress(testCtx(t0.Add(d), nil), i) }
	if p := at(12*time.Millisecond, 1); p != 0 {
		t.Errorf("item 1 at its delay = %v, want 0", p)
	}
	if p0, p3 := at(90*time.Millisecond, 0), at(90*time.Millisecond, 3); !(p0 > p3 && p3 > 0) {
		t.Errorf("staggered order broken: item0 %v item3 %v", p0, p3)
	}
	// Items past Max share the last delay.
	if a, b := at(150*time.Millisecond, 12), at(150*time.Millisecond, 40); a != b {
		t.Errorf("capped delays differ: %v vs %v", a, b)
	}
	if p := at(400*time.Millisecond, 11); p != 1 || !s.Done() {
		t.Errorf("after the run: %v done=%v", p, s.Done())
	}
	s.Restart()
	if p := s.Progress(testCtx(t0.Add(time.Second), nil), 0); p != 0 {
		t.Errorf("after Restart item 0 = %v, want 0", p)
	}
}

func TestThemeCrossfade(t *testing.T) {
	h := newHarness(t)
	h.frame(func(C) {})
	if h.th.Pal.Ground != LightPalette.Ground {
		t.Fatal("theme does not start light")
	}
	h.th.SetMode(ModeDark)
	h.frame(func(C) {}) // the fade starts on this frame
	h.frames(7, func(C) {})
	mid := h.th.Pal.Ground
	if mid == LightPalette.Ground || mid == DarkPalette.Ground {
		t.Errorf("no crossfade mid-way: %v", mid)
	}
	h.frames(12, func(C) {})
	if h.th.Pal != DarkPalette || !h.th.Dark() {
		t.Errorf("fade did not land on the dark palette")
	}
}

func TestRouteTransition(t *testing.T) {
	var r Route
	drawn := ""
	page := func(name string) func(C) D {
		return func(gtx C) D { drawn = name; return D{} }
	}
	at := func(d time.Duration, key string) string {
		drawn = ""
		r.Layout(testCtx(SnapshotEpoch.Add(d), nil), key, page(key))
		return drawn
	}
	if got := at(0, "home"); got != "home" || r.Animating() {
		t.Fatalf("first page = %q animating=%v", got, r.Animating())
	}
	if got := at(16*time.Millisecond, "settings"); got != "home" || !r.Animating() {
		t.Errorf("fade-out draws %q (animating %v), want the old page", got, r.Animating())
	}
	if got := at(16*time.Millisecond+DurFade+DurRoute/2, "settings"); got != "settings" {
		t.Errorf("fade-in draws %q, want the new page", got)
	}
	if at(16*time.Millisecond+DurFade+DurRoute+time.Millisecond, "settings"); r.Animating() {
		t.Error("route still animating after 270 ms")
	}
}
