package gui

import (
	"fmt"
	"hash/fnv"
	"image"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// Fit decides how a picture fills its box.
type Fit uint8

const (
	FitCover   Fit = iota // fill the box, cropping the overflow
	FitContain            // show all of it, letterboxed
)

// Thumb draws a picture in a rounded box, or a calm placeholder while there
// is none. The box takes the full width; its height comes from Ratio
// (width/height), else from Min.Y, else it is square.
//
//	gui.Thumb{Image: op, Ratio: 16.0 / 9}.Layout(gtx, th)
type Thumb struct {
	Image       paint.ImageOp // zero = placeholder
	Fit         Fit
	Radius      unit.Dp // 0 = RadiusInner; use a negative value for square corners
	Ratio       float32
	Placeholder *Icon // 0 = IconImage
	Loading     bool  // shimmer instead of the placeholder icon
	// Fade (optional state) fades the picture in when it first appears.
	Fade *Anim
	// Overlay is drawn over the picture (badges, play button).
	Overlay layout.Widget
}

// Layout draws the thumb.
func (t Thumb) Layout(gtx C, th *Theme) D {
	w := gtx.Constraints.Max.X
	h := w
	switch {
	case t.Ratio > 0:
		h = int(float32(w)/t.Ratio + 0.5)
	case gtx.Constraints.Min.Y > 0:
		h = gtx.Constraints.Min.Y
	}
	h = min(h, gtx.Constraints.Max.Y)
	size := pt(w, h)
	rad := gtx.Dp(t.Radius)
	if t.Radius == 0 {
		rad = gtx.Dp(RadiusInner)
	} else if t.Radius < 0 {
		rad = 0
	}
	defer clip.UniformRRect(rectWH(w, h), rad).Push(gtx.Ops).Pop()
	present := t.Image.Size().X > 0
	alpha := float32(1)
	if t.Fade != nil {
		if !present {
			t.Fade.Set(0)
		} else {
			if !t.Fade.init {
				t.Fade.Set(1) // already there on the first frame: no fade
			}
			t.Fade.Duration, t.Fade.Ease = 220*time.Millisecond, EaseOutCubic
			alpha = t.Fade.Animate(gtx, 1)
		}
	}
	if !present || alpha < 1 {
		if t.Loading {
			gtx.Constraints = layout.Exact(size)
			Skeleton{Radius: -1}.Layout(gtx, th)
		} else {
			paint.Fill(gtx.Ops, th.Pal.Surface)
			ic := t.Placeholder
			if ic == nil {
				ic = IconImage
			}
			is := unit.Dp(min(40, max(16, float32(min(w, h))/gtx.Metric.PxPerDp*0.28)))
			ip := gtx.Dp(is)
			o := op.Offset(pt((w-ip)/2, (h-ip)/2)).Push(gtx.Ops)
			ic.Layout(gtx, is, th.Pal.Text3)
			o.Pop()
		}
	}
	if present {
		func() {
			defer opacity(gtx, alpha)()
			if t.Fit == FitContain {
				paint.Fill(gtx.Ops, th.Pal.Surface)
			}
			drawImageFit(gtx, t.Image, size, t.Fit)
		}()
	}
	if t.Overlay != nil {
		og := gtx
		og.Constraints = layout.Exact(size)
		t.Overlay(og)
	}
	return D{Size: size}
}

// drawImageFit paints img scaled into a box of size (cover or contain),
// centred. The caller clips.
func drawImageFit(gtx C, img paint.ImageOp, size image.Point, fit Fit) {
	is := img.Size()
	if is.X == 0 || is.Y == 0 || size.X == 0 || size.Y == 0 {
		return
	}
	sx := float32(size.X) / float32(is.X)
	sy := float32(size.Y) / float32(is.Y)
	s := max(sx, sy)
	if fit == FitContain {
		s = min(sx, sy)
	}
	dw, dh := float32(is.X)*s, float32(is.Y)*s
	off := f32.Pt((float32(size.X)-dw)/2, (float32(size.Y)-dh)/2)
	defer clip.Rect(rectWH(size.X, size.Y)).Push(gtx.Ops).Pop()
	tr := f32.AffineId().Scale(f32.Point{}, f32.Pt(s, s)).Offset(off)
	defer op.Affine(tr).Push(gtx.Ops).Pop()
	img.Filter = paint.FilterLinear
	img.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// ImageView is Thumb fed from an ImageCache by file path: it decodes off the
// UI goroutine at the size it is drawn, shows a shimmer while loading and
// fades the picture in.
//
//	s.frame.Layout(gtx, th, cache, e.Abs(id, scene.Frame.Path), scene.Frame.UpdatedAt, gui.Thumb{Ratio: 16.0/9})
type ImageView struct {
	fade Anim
}

// Layout draws path (version bumps refresh it) with the look of st.
func (v *ImageView) Layout(gtx C, th *Theme, cache *ImageCache, path string, version int64, st Thumb) D {
	if cache != nil && path != "" {
		longest := gtx.Constraints.Max.X
		if st.Ratio > 0 && st.Ratio < 1 {
			longest = int(float32(longest) / st.Ratio)
		}
		img, _, ok := cache.Get(path, version, ImgBucket(longest))
		if ok {
			st.Image = img
		} else {
			st.Loading = true
		}
	}
	st.Fade = &v.fade
	return st.Layout(gtx, th)
}

// ---- AudioPill -------------------------------------------------------------------

// AudioBackend plays one sound at a time. The app adapts player.Audio():
//
//	type audio struct{ e *studio.Engine }
//	func (a audio) Play(src string, done func()) error { return player.Audio().Play(a.e.FF(), src, done) }
//	func (a audio) Stop()                            { player.Audio().Stop() }
//	func (a audio) Playing(src string) bool          { return player.Audio().Playing(src) }
type AudioBackend interface {
	Play(src string, onDone func()) error
	Stop()
	Playing(src string) bool
}

// AudioStyle configures an AudioPill for one frame.
type AudioStyle struct {
	Src      string
	Label    string        // "Оповідач"
	Duration time.Duration // clip length for the progress (0 = unknown)
	Backend  AudioBackend  // nil disables the pill
	// Playing/Elapsed pin the look for screenshots (Backend nil).
	Playing bool
	Elapsed time.Duration
	Force   Force
}

// AudioPill is a compact voice player: a round play/stop button, a label, a
// mini waveform that fills as the clip plays, and the elapsed time.
type AudioPill struct {
	btn     Clickable
	started time.Time
	src     string
	err     string
}

// Err is the last playback error ("" when fine).
func (a *AudioPill) Err() string { return a.err }

// Layout draws the pill at its natural width (at most the constraint).
func (a *AudioPill) Layout(gtx C, th *Theme, st AudioStyle) D {
	playing := st.Playing
	if st.Backend != nil {
		playing = st.Backend.Playing(st.Src)
	}
	if a.btn.Clicked(gtx) && st.Backend != nil {
		if playing {
			st.Backend.Stop()
			playing = false
		} else {
			a.err = ""
			if err := st.Backend.Play(st.Src, th.invalidate); err != nil {
				a.err = err.Error()
				th.Toast(ToneError, err.Error())
			} else {
				playing = true
				a.started, a.src = gtx.Now, st.Src
			}
		}
	}
	if st.Src != a.src {
		a.started = time.Time{}
	}
	elapsed := st.Elapsed
	if st.Backend != nil {
		elapsed = 0
		if playing && !a.started.IsZero() {
			elapsed = gtx.Now.Sub(a.started)
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 30)})
		}
	}
	frac := float32(0)
	if st.Duration > 0 {
		frac = clamp01(float32(elapsed) / float32(st.Duration))
	}
	disabled := st.Backend == nil && !st.Playing && st.Force == 0
	bgtx := gtx
	if disabled {
		bgtx = gtx.Disabled()
	}
	bst := a.btn.update(bgtx, st.Force, 0.92)
	h := gtx.Dp(48)
	pad := gtx.Dp(6)
	bs := h - 2*pad
	gap := gtx.Dp(12)
	waveW := gtx.Dp(96)
	timeTxt := fmtClock(elapsed)
	if st.Duration > 0 {
		timeTxt += " / " + fmtClock(st.Duration)
	}
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(max(0, gtx.Constraints.Max.X-bs-waveW-3*gap-2*pad), h)}
	ld, lc := record(lg, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(Text{Style: th.Label, MaxLines: 1}.Widget(th, st.Label)),
			layout.Rigid(Text{Style: th.Caption, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, timeTxt)),
		)
	})
	w := pad + bs + gap + ld.Size.X + gap + waveW + gap + pad
	size := pt(w, h)
	// Whole pill surface, then the button's hit area on top.
	FillRRect(gtx, rectWH(w, h), RadiusPill, th.Pal.Surface)
	func() {
		defer op.Offset(pt(pad, pad)).Push(bgtx.Ops).Pop()
		a.btn.hitArea(bgtx, pt(bs, bs))
		FocusRing(gtx, th, rectWH(bs, bs), unit.Dp(float32(bs)/2/gtx.Metric.PxPerDp), bst.focus)
		Scale(gtx, bst.scale, func(gtx C) D {
			bg := LerpColor(th.Pal.Action, th.Pal.ActionHover, bst.hover)
			fg := th.Pal.OnAction
			if disabled {
				bg, fg = th.Pal.SurfaceHover, th.Pal.Text3
			}
			fillCircle(gtx.Ops, f32.Pt(float32(bs)/2, float32(bs)/2), float32(bs)/2, bg)
			ic := IconPlay
			if playing {
				ic = IconSquare
			}
			is := gtx.Dp(16)
			dx := 0
			if !playing {
				dx = gtx.Dp(1) // optical centring of the triangle
			}
			defer op.Offset(pt((bs-is)/2+dx, (bs-is)/2)).Push(gtx.Ops).Pop()
			ic.LayoutStroke(gtx, 16, 2, fg)
			return D{Size: pt(bs, bs)}
		})
	}()
	func() {
		defer op.Offset(pt(pad+bs+gap, (h-ld.Size.Y)/2)).Push(gtx.Ops).Pop()
		lc.Add(gtx.Ops)
	}()
	func() {
		defer op.Offset(pt(pad+bs+gap+ld.Size.X+gap, 0)).Push(gtx.Ops).Pop()
		drawWave(gtx, th, st.Src, waveW, h, frac, playing)
	}()
	return D{Size: size}
}

// drawWave draws a deterministic mini waveform (bar heights hashed from src);
// bars left of frac are action-coloured.
func drawWave(gtx C, th *Theme, src string, w, h int, frac float32, playing bool) {
	const bars = 18
	hs := fnv.New32a()
	hs.Write([]byte(src))
	seed := hs.Sum32()
	bw := float32(w) / bars
	barW := max(2, bw*0.5)
	maxH := float32(h) * 0.5
	for i := 0; i < bars; i++ {
		seed = seed*1664525 + 1013904223
		v := 0.3 + 0.7*float32(seed>>24)/255
		// Soft envelope so the wave looks like speech, not noise.
		env := 0.55 + 0.45*sin32(float32(i)/bars*3.1416)
		bh := max(barW, maxH*v*env)
		x := float32(i)*bw + (bw-barW)/2
		y := (float32(h) - bh) / 2
		c := th.Pal.BorderStrong
		if (float32(i)+0.5)/bars <= frac && (playing || frac > 0) {
			c = th.Pal.Action
		}
		fillRRf(gtx.Ops, fRect{Min: f32.Pt(x, y), Max: f32.Pt(x+barW, y+bh)}, barW/2, c)
	}
}

// fmtClock formats d as m:ss.
func fmtClock(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
