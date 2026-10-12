package media

// Motion effects (Ken Burns and friends).
//
// zoompan crops at whole input pixels, so slow moves visibly stair-step even
// on a 4× upscaled image (measured: 0.41 px frame-to-frame jerk against a
// 0.19 px/frame motion). The perspective filter samples the source through a
// projective map with 1/256 px precision; with corner expressions evaluated
// per frame it gives a zoom/pan/rotate that is as smooth as an analytically
// drawn move (jerk at the 8-bit measurement floor) and costs ~10 ms/frame at
// 1080p. So every effect is a moving window over the frame: zoom z (the
// window is 1/z of the frame), centre (cx, cy) and rotation θ, all functions
// of the scene progress p = 0…1.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// motion describes an effect at full strength.
type motion struct {
	z0, z1 float64 // zoom at the start / end (1 = whole frame)
	// x/y: window centre at the start / end as a share of the free travel
	// (-1 = left/top edge … 1 = right/bottom edge).
	x0, x1, y0, y1 float64
	r0, r1         float64 // rotation in degrees
	shake          float64 // handheld wobble, share of the frame size
	ease           bool    // smoothstep instead of a constant speed
}

var motions = map[string]motion{
	"zoomin":   {z0: 1, z1: 1.12},
	"zoomout":  {z0: 1.12, z1: 1},
	"panleft":  {z0: 1.12, z1: 1.12, x0: 0.9, x1: -0.9},
	"panright": {z0: 1.12, z1: 1.12, x0: -0.9, x1: 0.9},
	"panup":    {z0: 1.12, z1: 1.12, y0: 0.9, y1: -0.9},
	"pandown":  {z0: 1.12, z1: 1.12, y0: -0.9, y1: 0.9},
	"diagonal": {z0: 1.06, z1: 1.18, x0: -0.8, x1: 0.8, y0: -0.8, y1: 0.8},
	"pushin":   {z0: 1, z1: 1.3, ease: true},
	"drift":    {z0: 1.05, z1: 1.08, x0: -0.5, x1: 0.5, y0: 0.25, y1: -0.25},
	"shake":    {z0: 1.07, z1: 1.07, shake: 0.006},
	"rotate":   {z0: 1.07, z1: 1.1, r0: -1.2, r1: 1.2},
}

// clipStrength: an effect chosen for a clip runs at half strength — the
// footage already moves; the effect only adds a subtle camera drift.
const clipStrength = 0.5

// shakeRotation is the handheld roll amplitude in degrees per unit of shake.
const shakeRotation = 0.25 / 0.006

// scaled returns the motion at strength k (0…1).
func (m motion) scaled(k float64) motion {
	m.z0 = 1 + (m.z0-1)*k
	m.z1 = 1 + (m.z1-1)*k
	m.r0 *= k
	m.r1 *= k
	m.shake *= k
	return m
}

// safe raises the zoom so a rotated / shaken window never leaves the frame.
func (m motion) safe(W, H int) motion {
	rot := math.Max(math.Abs(m.r0), math.Abs(m.r1)) + m.shake*shakeRotation
	th := rot * math.Pi / 180
	ar := math.Max(float64(W)/float64(H), float64(H)/float64(W))
	need := math.Cos(th) + ar*math.Sin(th)
	if m.shake > 0 {
		// Room for the wobble on top: shift ≤ shake × (W+H)/2 px.
		need += 2 * m.shake * (float64(W+H) / 2) / float64(min(W, H))
	}
	if need > 1 {
		need += 0.005 // a hair of margin against rounding at the edge
	}
	m.z0 = math.Max(m.z0, need)
	m.z1 = math.Max(m.z1, need)
	return m
}

// g formats a constant for an ffmpeg expression; negatives are parenthesised.
func g(x float64) string {
	s := strconv.FormatFloat(x, 'g', 8, 64)
	if x < 0 {
		return "(" + s + ")"
	}
	return s
}

// effectFilter returns the perspective filter for effect id over a scene of
// frames frames at W×H, or "" when the effect does not move the picture.
// Stills get the full motion, clips (clip=true) the subtle one.
func effectFilter(id string, W, H, frames int, clip bool) string {
	m, ok := motions[id]
	if !ok {
		return ""
	}
	if clip {
		m = m.scaled(clipStrength)
	}
	m = m.safe(W, H)

	// p: progress 0…1 over the scene (on counts output frames from 1).
	den := max(frames-1, 1)
	p := fmt.Sprintf("st(0,clip((on-1)/%d,0,1))", den)
	e := "ld(0)"
	if m.ease {
		e = "ld(0)*ld(0)*(3-2*ld(0))"
	}
	lerp := func(a, b float64) string {
		if a == b {
			return g(a)
		}
		return fmt.Sprintf("(%s+%s*%s)", g(a), g(b-a), e)
	}
	hw, hh := float64(W)/2, float64(H)/2
	z := fmt.Sprintf("st(1,%s)", lerp(m.z0, m.z1))
	// Free travel of the window centre at the current zoom: (W/2)(1-1/z).
	cx := fmt.Sprintf("%s+%s*%s*(1-1/ld(1))", g(hw), lerp(m.x0, m.x1), g(hw))
	cy := fmt.Sprintf("%s+%s*%s*(1-1/ld(1))", g(hh), lerp(m.y0, m.y1), g(hh))
	th := fmt.Sprintf("%s*%s", lerp(m.r0, m.r1), g(math.Pi/180))
	if m.shake > 0 {
		// Handheld: a few incommensurate slow sines (t in seconds).
		amp := m.shake * float64(W+H) / 2
		t := fmt.Sprintf("(on-1)/%d", fps)
		cx += fmt.Sprintf("+%s*(0.6*sin(5.215*%s)+0.4*sin(12.13*%s+1.7))", g(amp), t, t)
		cy += fmt.Sprintf("+%s*(0.6*sin(4.084*%s+0.9)+0.4*sin(9.425*%s+2.3))", g(amp*0.8), t, t)
		th += fmt.Sprintf("+%s*sin(3.833*%s+0.4)", g(m.shake*shakeRotation*math.Pi/180), t)
	}
	head := strings.Join([]string{p, z, "st(2," + cx + ")", "st(3," + cy + ")", "st(4," + th + ")"}, ";")
	// Corner (sx, sy) of the rotated window, in source pixels.
	corner := func(sx, sy float64) (string, string) {
		hx := fmt.Sprintf("(%s/ld(1))", g(sx*hw))
		hy := fmt.Sprintf("(%s/ld(1))", g(sy*hh))
		x := fmt.Sprintf("%s;ld(2)+%s*cos(ld(4))-%s*sin(ld(4))", head, hx, hy)
		y := fmt.Sprintf("%s;ld(3)+%s*sin(ld(4))+%s*cos(ld(4))", head, hx, hy)
		return x, y
	}
	x0, y0 := corner(-1, -1)
	x1, y1 := corner(1, -1)
	x2, y2 := corner(-1, 1)
	x3, y3 := corner(1, 1)
	return fmt.Sprintf("perspective=x0='%s':y0='%s':x1='%s':y1='%s':x2='%s':y2='%s':x3='%s':y3='%s':interpolation=linear:sense=source:eval=frame",
		x0, y0, x1, y1, x2, y2, x3, y3)
}
