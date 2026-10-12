package gui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// ProgressBar is a rounded bar. Give it an *Anim to glide to new values over
// 300 ms (GUI.md); a negative Value shows an indeterminate sweep.
//
//	gui.ProgressBar{Value: job.Pct / 100, Anim: &s.bar}.Layout(gtx, th)
type ProgressBar struct {
	Value  float32 // 0…1; < 0 = indeterminate
	Height unit.Dp // 0 = 8 dp
	Tone   Tone    // bar colour family; ToneNeutral = action
	Anim   *Anim   // optional animation state
	// Track overrides the track colour (zero = th.Pal.Track).
	Track color.NRGBA
}

// Layout draws the bar across the available width.
func (p ProgressBar) Layout(gtx C, th *Theme) D {
	h := gtx.Dp(p.Height)
	if p.Height == 0 {
		h = gtx.Dp(8)
	}
	w := gtx.Constraints.Max.X
	size := pt(w, h)
	track := p.Track
	if track.A == 0 {
		track = th.Pal.Track
	}
	fill := toneStrong(th, p.Tone)
	rad := float32(h) / 2
	fillRRf(gtx.Ops, frect(rectWH(w, h)), rad, track)
	if p.Value < 0 {
		// Indeterminate: a 35 % segment sliding across, eased at both ends.
		const period = 1400 * time.Millisecond
		ph := float32(0.3)
		if !ReducedMotion() {
			ph = float32(gtx.Now.UnixNano()%int64(period)) / float32(period)
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 60)})
		}
		seg := float32(w) * 0.35
		x := EaseInOutCubic(ph)*(float32(w)+seg) - seg
		defer clip.UniformRRect(rectWH(w, h), h/2).Push(gtx.Ops).Pop()
		fillRRf(gtx.Ops, fRect{Min: f32.Pt(x, 0), Max: f32.Pt(x+seg, float32(h))}, rad, fill)
		return D{Size: size}
	}
	v := clamp01(p.Value)
	if p.Anim != nil {
		p.Anim.Duration, p.Anim.Ease = DurValue, EaseOutCubic
		v = clamp01(p.Anim.Animate(gtx, v))
	}
	if v > 0 {
		fw := max(float32(h), float32(w)*v) // never thinner than a dot
		fillRRf(gtx.Ops, fRect{Max: f32.Pt(fw, float32(h))}, rad, fill)
	}
	return D{Size: size}
}

func toneStrong(th *Theme, t Tone) color.NRGBA {
	if t == ToneNeutral {
		return th.Pal.Action
	}
	s, _, _ := th.Pal.ToneColors(t)
	return s
}

// ProgressRing is the circular progress of projects and jobs: a round-capped
// arc over a soft track with the percentage in the middle (the "%" smaller)
// and an optional caption under it.
//
//	gui.ProgressRing{Value: 0.72, Caption: "готово", Anim: &s.ring}.Layout(gtx, th)
type ProgressRing struct {
	Value   float32 // 0…1; < 0 = indeterminate (spinning arc)
	Size    unit.Dp // diameter; 0 = 120
	Stroke  unit.Dp // 0 = Size/11
	Tone    Tone
	Label   string // centre text; "" = the percentage; "-" = none
	Caption string // small text under the label
	Anim    *Anim
}

// Layout draws the ring.
func (r ProgressRing) Layout(gtx C, th *Theme) D {
	if r.Size == 0 {
		r.Size = 120
	}
	if r.Stroke == 0 {
		r.Stroke = max(3, r.Size/11)
	}
	px := gtx.Dp(r.Size)
	sw := float32(gtx.Dp(r.Stroke))
	c := f32.Pt(float32(px)/2, float32(px)/2)
	rad := float32(px)/2 - sw/2
	strong := toneStrong(th, r.Tone)
	track := th.Pal.ActionSoft
	if r.Tone != ToneNeutral && r.Tone != ToneAction {
		_, track, _ = th.Pal.ToneColors(r.Tone)
	}
	strokeArc(gtx.Ops, c, rad, 0, 2*math.Pi, sw, track)
	v := r.Value
	if v < 0 {
		const period = 1100 * time.Millisecond
		ph := float32(0)
		if !ReducedMotion() {
			ph = float32(gtx.Now.UnixNano()%int64(period)) / float32(period)
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 60)})
		}
		strokeArc(gtx.Ops, c, rad, ph*2*math.Pi-math.Pi/2, 0.25*2*math.Pi, sw, strong)
	} else {
		v = clamp01(v)
		if r.Anim != nil {
			r.Anim.Duration, r.Anim.Ease = DurValue, EaseOutCubic
			v = clamp01(r.Anim.Animate(gtx, v))
		}
		if v > 0.001 {
			strokeArc(gtx.Ops, c, rad, -math.Pi/2, v*2*math.Pi, sw, strong)
		}
	}
	if r.Label == "-" {
		return D{Size: pt(px, px)}
	}
	// Centre label.
	big := th.Stat.Sized(unit.Sp(max(12, float32(r.Size)*0.27)))
	small := th.Label.Sized(unit.Sp(max(9, float32(r.Size)*0.13)))
	cg := gtx
	cg.Constraints = layout.Constraints{Max: pt(px, px)}
	ld, lc := record(cg, func(gtx C) D {
		var ch []layout.FlexChild
		ch = append(ch, layout.Rigid(func(gtx C) D {
			if r.Label != "" || v < 0 {
				if r.Label == "" {
					return D{}
				}
				return Text{Style: big, MaxLines: 1}.Layout(gtx, th, r.Label)
			}
			pct := int(math.Round(float64(clamp01(r.Value) * 100)))
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(Text{Style: big, MaxLines: 1}.Widget(th, fmt.Sprint(pct))),
				layout.Rigid(Text{Style: big.Sized(big.Size * 0.55), MaxLines: 1}.Widget(th, "%")),
			)
		}))
		if r.Caption != "" && r.Size >= 72 {
			ch = append(ch, layout.Rigid(Text{Style: small.Weighted(FontRegular), Color: th.Pal.Text2, MaxLines: 1}.Widget(th, r.Caption)))
		}
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx, ch...)
	})
	t := op.Offset(pt((px-ld.Size.X)/2, (px-ld.Size.Y)/2)).Push(gtx.Ops)
	lc.Add(gtx.Ops)
	t.Pop()
	return D{Size: pt(px, px)}
}

// Skeleton is a loading placeholder with a soft shimmer sweeping across
// every 1.4 s. Width/Height 0 take the constraints (Min.Y or 16 dp).
type Skeleton struct {
	Width, Height unit.Dp
	Radius        unit.Dp // 0 = 8 dp
}

// Layout draws the placeholder and keeps the shimmer moving.
func (s Skeleton) Layout(gtx C, th *Theme) D {
	w := gtx.Constraints.Max.X
	if s.Width > 0 {
		w = min(w, gtx.Dp(s.Width))
	}
	h := gtx.Dp(16)
	if s.Height > 0 {
		h = gtx.Dp(s.Height)
	} else if gtx.Constraints.Min.Y > 0 {
		h = gtx.Constraints.Min.Y
	}
	rad := s.Radius
	if rad == 0 {
		rad = 8
	}
	size := pt(w, h)
	r := 0
	if rad > 0 {
		r = min(gtx.Dp(rad), h/2)
	}
	defer clip.UniformRRect(rectWH(w, h), r).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, th.Pal.Surface)
	if ReducedMotion() {
		return D{Size: size}
	}
	// The band position is shared by every skeleton (by absolute time), so
	// placeholders shimmer in unison.
	ph := float32(gtx.Now.UnixNano()%int64(DurShimmer)) / float32(DurShimmer)
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 30)})
	band := float32(max(gtx.Dp(160), w/3))
	x := -band + (float32(w)+2*band)*EaseInOutCubic(ph)
	hi := th.Pal.Shimmer
	lo := WithAlpha(hi, 0)
	half := band / 2
	grad := func(x0, x1 float32, c0, c1 color.NRGBA) {
		r := image.Rect(int(x0), 0, int(math.Ceil(float64(x1))), h)
		st := clip.Rect(r).Push(gtx.Ops)
		paint.LinearGradientOp{Stop1: f32.Pt(x0, 0), Color1: c0, Stop2: f32.Pt(x1, 0), Color2: c1}.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		st.Pop()
	}
	grad(x, x+half, lo, hi)
	grad(x+half, x+band, hi, lo)
	return D{Size: size}
}

// SkeletonLines stacks n text-line placeholders (the last one shorter).
func SkeletonLines(gtx C, th *Theme, n int) D {
	var ch []layout.FlexChild
	for i := 0; i < n; i++ {
		last := i == n-1 && n > 1
		ch = append(ch, layout.Rigid(func(gtx C) D {
			if last {
				gtx.Constraints.Max.X = gtx.Constraints.Max.X * 3 / 5
			}
			return Skeleton{Height: 12, Radius: 6}.Layout(gtx, th)
		}))
		if i < n-1 {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: Sp12}.Layout))
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}
