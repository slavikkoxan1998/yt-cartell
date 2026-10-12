package montage

// Colour looks in pure Go, for previews: small thumbnails graded roughly
// like the ffmpeg chains of media.Filters (the render itself uses ffmpeg).
// Every look is a per-pixel function on linear-ish sRGB floats, plus two
// spatial effects (dreamy glow, vignette) and deterministic grain.

import (
	"image"
	"image/draw"
	"math"

	xdraw "golang.org/x/image/draw"
)

type rgb struct{ r, g, b float64 }

func (c rgb) luma() float64 { return 0.299*c.r + 0.587*c.g + 0.114*c.b }

func (c rgb) sat(s float64) rgb {
	l := c.luma()
	return rgb{l + (c.r-l)*s, l + (c.g-l)*s, l + (c.b-l)*s}
}

func (c rgb) contrast(k float64) rgb {
	f := func(v float64) float64 { return (v-0.5)*k + 0.5 }
	return rgb{f(c.r), f(c.g), f(c.b)}
}

func (c rgb) add(v float64) rgb { return rgb{c.r + v, c.g + v, c.b + v} }

func (c rgb) gamma(g float64) rgb {
	f := func(v float64) float64 { return math.Pow(math.Max(0, v), 1/g) }
	return rgb{f(c.r), f(c.g), f(c.b)}
}

func (c rgb) clamp() rgb {
	f := func(v float64) float64 { return math.Max(0, math.Min(1, v)) }
	return rgb{f(c.r), f(c.g), f(c.b)}
}

// mix is a 3×3 colour matrix (colorchannelmixer).
func (c rgb) mix(m [9]float64) rgb {
	return rgb{
		m[0]*c.r + m[1]*c.g + m[2]*c.b,
		m[3]*c.r + m[4]*c.g + m[5]*c.b,
		m[6]*c.r + m[7]*c.g + m[8]*c.b,
	}
}

// kelvin is the colour of a blackbody light, normalised to 6500 K = white
// (Tanner Helland's fit).
func kelvin(k float64) rgb {
	f := func(t float64) rgb {
		t /= 100
		var r, g, b float64
		if t <= 66 {
			r = 255
			g = 99.4708025861*math.Log(t) - 161.1195681661
		} else {
			r = 329.698727446 * math.Pow(t-60, -0.1332047592)
			g = 288.1221695283 * math.Pow(t-60, -0.0755148492)
		}
		switch {
		case t >= 66:
			b = 255
		case t <= 19:
			b = 0
		default:
			b = 138.5177312231*math.Log(t-10) - 305.0447927307
		}
		cl := func(v float64) float64 { return math.Max(1, math.Min(255, v)) / 255 }
		return rgb{cl(r), cl(g), cl(b)}
	}
	c, w := f(k), f(6500)
	return rgb{c.r / w.r, c.g / w.g, c.b / w.b}
}

// temperature tints by a light of k kelvin (mix 0…1), keeping the lightness.
func (c rgb) temperature(k, mix float64) rgb {
	t := kelvin(k)
	n := rgb{c.r * t.r, c.g * t.g, c.b * t.b}
	n = rgb{c.r + (n.r-c.r)*mix, c.g + (n.g-c.g)*mix, c.b + (n.b-c.b)*mix}
	if l0, l1 := c.luma(), n.luma(); l1 > 1e-6 {
		k := l0 / l1
		n = rgb{n.r * k, n.g * k, n.b * k}
	}
	return n
}

// curve is a piecewise-linear tone curve through (x, y) points.
func curve(pts ...float64) func(float64) float64 {
	return func(v float64) float64 {
		if v <= pts[0] {
			return pts[1]
		}
		for i := 2; i+1 < len(pts); i += 2 {
			x0, y0, x1, y1 := pts[i-2], pts[i-1], pts[i], pts[i+1]
			if v <= x1 {
				return y0 + (y1-y0)*(v-x0)/math.Max(1e-9, x1-x0)
			}
		}
		return pts[len(pts)-1]
	}
}

// balance shifts shadows, midtones and highlights per channel (colorbalance).
func (c rgb) balance(sh, mid, hi rgb) rgb {
	l := c.luma()
	ws := math.Max(0, 1-l*2.5)
	wh := math.Max(0, (l-0.6)*2.5)
	wm := math.Max(0, 1-math.Abs(l-0.5)*2)
	return rgb{
		c.r + sh.r*ws + mid.r*wm + hi.r*wh,
		c.g + sh.g*ws + mid.g*wm + hi.g*wh,
		c.b + sh.b*ws + mid.b*wm + hi.b*wh,
	}
}

// vibrance raises the saturation of dull colours more than of vivid ones.
func (c rgb) vibrance(k float64) rgb {
	mx := math.Max(c.r, math.Max(c.g, c.b))
	mn := math.Min(c.r, math.Min(c.g, c.b))
	return c.sat(1 + k*(1-(mx-mn)))
}

// lookPixel is the per-pixel part of a look.
func lookPixel(id string) func(rgb) rgb {
	switch id {
	case "cinematic":
		return func(c rgb) rgb {
			l := c.luma()
			ws, wh := math.Max(0, 1-l*2), math.Max(0, l*2-1)
			c = rgb{c.r - 0.06*ws + 0.05*wh, c.g + 0.015*ws + 0.01*wh, c.b + 0.07*ws - 0.06*wh}
			return c.contrast(1.08).sat(1.1)
		}
	case "warm":
		return func(c rgb) rgb { return c.temperature(5200, 1).sat(1.05) }
	case "cool":
		return func(c rgb) rgb { return c.temperature(9000, 0.9) }
	case "vintage":
		cr, cg, cb := curve(0, 0.10, 0.5, 0.55, 1, 0.95), curve(0, 0.07, 0.5, 0.50, 1, 0.90), curve(0, 0.14, 0.5, 0.42, 1, 0.76)
		return func(c rgb) rgb { return rgb{cr(c.r), cg(c.g), cb(c.b)}.sat(0.78).contrast(0.98) }
	case "noir":
		m := [9]float64{.3, .45, .25, .3, .45, .25, .3, .45, .25}
		return func(c rgb) rgb { return c.mix(m).contrast(1.35).add(-0.02) }
	case "sepia":
		m := [9]float64{.32, .62, .15, .28, .56, .13, .22, .43, .10}
		return func(c rgb) rgb { return c.mix(m).contrast(1.05) }
	case "faded":
		cv := curve(0, 0.10, 0.5, 0.52, 1, 0.90)
		return func(c rgb) rgb { return rgb{cv(c.r), cv(c.g), cv(c.b)}.sat(0.78) }
	case "contrast":
		cv := curve(0, 0, 0.149, 0.066, 0.831, 0.905, 0.905, 0.98, 1, 1)
		return func(c rgb) rgb { return rgb{cv(c.r), cv(c.g), cv(c.b)}.sat(1.06) }
	case "vivid":
		return func(c rgb) rgb { return c.vibrance(0.35).sat(1.18).contrast(1.04) }
	case "dreamy":
		return func(c rgb) rgb { return c.contrast(0.95).sat(0.95) }
	case "moonlight":
		return func(c rgb) rgb {
			c = c.temperature(11000, 1).add(-0.06).sat(0.55).gamma(0.9)
			return c.balance(rgb{0, 0, 0.08}, rgb{0, 0, 0.05}, rgb{})
		}
	case "golden":
		return func(c rgb) rgb {
			c = c.temperature(3800, 0.7).balance(rgb{}, rgb{}, rgb{0.06, 0.02, -0.06})
			return c.sat(1.12).contrast(1.04)
		}
	case "grain":
		return func(c rgb) rgb { return c.contrast(1.04) }
	}
	return nil
}

// applyLook returns src graded with look id (src is not modified). Unknown
// ids and "none" return src itself.
func applyLook(src *image.RGBA, id string) *image.RGBA {
	fn := lookPixel(id)
	spatial := id == "dreamy" || id == "vignette" || id == "vintage" || id == "noir" || id == "grain"
	if fn == nil && !spatial {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Rect, src, b.Min, draw.Src)
	var glow *image.RGBA
	if id == "dreamy" {
		glow = blur(dst, max(2, w/60))
	}
	vig := 0.0
	switch id {
	case "vignette":
		vig = 0.55
	case "vintage":
		vig = 0.4
	case "noir":
		vig = 0.48
	}
	grain := 0.0
	switch id {
	case "grain":
		grain = 0.07
	case "vintage":
		grain = 0.025
	}
	seed := uint32(2463534242)
	cx, cy := float64(w)/2, float64(h)/2
	dmax := math.Hypot(cx, cy)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := dst.PixOffset(x, y)
			c := rgb{float64(dst.Pix[i]) / 255, float64(dst.Pix[i+1]) / 255, float64(dst.Pix[i+2]) / 255}
			if glow != nil {
				g := rgb{float64(glow.Pix[i]) / 255, float64(glow.Pix[i+1]) / 255, float64(glow.Pix[i+2]) / 255}
				scr := func(a, b float64) float64 { return 1 - (1-a)*(1-b) }
				const op = 0.38
				c = rgb{c.r + (scr(c.r, g.r)-c.r)*op, c.g + (scr(c.g, g.g)-c.g)*op, c.b + (scr(c.b, g.b)-c.b)*op}
			}
			if fn != nil {
				c = fn(c)
			}
			if vig > 0 {
				d := math.Hypot(float64(x)-cx, float64(y)-cy) / dmax
				k := 1 - vig*d*d*d*1.2
				c = rgb{c.r * k, c.g * k, c.b * k}
			}
			if grain > 0 {
				seed ^= seed << 13
				seed ^= seed >> 17
				seed ^= seed << 5
				n := (float64(seed&0xFFFF)/65535 - 0.5) * 2 * grain
				c = c.add(n)
			}
			c = c.clamp()
			dst.Pix[i] = uint8(c.r*255 + 0.5)
			dst.Pix[i+1] = uint8(c.g*255 + 0.5)
			dst.Pix[i+2] = uint8(c.b*255 + 0.5)
		}
	}
	return dst
}

// blur is a separable box blur (3 passes ≈ gaussian) of radius r.
func blur(src *image.RGBA, r int) *image.RGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	a := image.NewRGBA(src.Rect)
	copy(a.Pix, src.Pix)
	tmp := image.NewRGBA(src.Rect)
	for pass := 0; pass < 3; pass++ {
		boxH(a, tmp, w, h, r)
		boxV(tmp, a, w, h, r)
	}
	return a
}

func boxH(src, dst *image.RGBA, w, h, r int) {
	for y := 0; y < h; y++ {
		for ch := 0; ch < 3; ch++ {
			sum, n := 0, 0
			for x := -r; x <= r; x++ {
				if x >= 0 && x < w {
					sum += int(src.Pix[src.PixOffset(x, y)+ch])
					n++
				}
			}
			for x := 0; x < w; x++ {
				dst.Pix[dst.PixOffset(x, y)+ch] = uint8(sum / max(1, n))
				if o := x - r; o >= 0 {
					sum -= int(src.Pix[src.PixOffset(o, y)+ch])
					n--
				}
				if o := x + r + 1; o < w {
					sum += int(src.Pix[src.PixOffset(o, y)+ch])
					n++
				}
			}
		}
		for x := 0; x < w; x++ {
			dst.Pix[dst.PixOffset(x, y)+3] = 255
		}
	}
}

func boxV(src, dst *image.RGBA, w, h, r int) {
	for x := 0; x < w; x++ {
		for ch := 0; ch < 3; ch++ {
			sum, n := 0, 0
			for y := -r; y <= r; y++ {
				if y >= 0 && y < h {
					sum += int(src.Pix[src.PixOffset(x, y)+ch])
					n++
				}
			}
			for y := 0; y < h; y++ {
				dst.Pix[dst.PixOffset(x, y)+ch] = uint8(sum / max(1, n))
				if o := y - r; o >= 0 {
					sum -= int(src.Pix[src.PixOffset(x, o)+ch])
					n--
				}
				if o := y + r + 1; o < h {
					sum += int(src.Pix[src.PixOffset(x, o)+ch])
					n++
				}
			}
		}
		for y := 0; y < h; y++ {
			dst.Pix[dst.PixOffset(x, y)+3] = 255
		}
	}
}

// shrink scales img so its longest side is maxPx (never enlarges). Large
// reductions average whole pixel blocks first, so thumbnails stay smooth.
func shrink(img image.Image, maxPx int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	tw, th := w, h
	if maxPx > 0 && (w > maxPx || h > maxPx) {
		if w >= h {
			th, tw = max(1, h*maxPx/w), maxPx
		} else {
			tw, th = max(1, w*maxPx/h), maxPx
		}
	}
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(src, src.Rect, img, b.Min, draw.Src)
	if f := min(w/tw, h/th); f >= 2 {
		src = boxReduce(src, f)
	}
	if src.Rect.Dx() == tw && src.Rect.Dy() == th {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	xdraw.ApproxBiLinear.Scale(dst, dst.Rect, src, src.Rect, draw.Src, nil)
	return dst
}

// boxReduce averages f×f blocks.
func boxReduce(src *image.RGBA, f int) *image.RGBA {
	w, h := src.Rect.Dx()/f, src.Rect.Dy()/f
	out := image.NewRGBA(image.Rect(0, 0, max(1, w), max(1, h)))
	n := uint32(f * f)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b uint32
			for yy := 0; yy < f; yy++ {
				i := src.PixOffset(x*f, y*f+yy)
				for xx := 0; xx < f; xx++ {
					r += uint32(src.Pix[i])
					g += uint32(src.Pix[i+1])
					b += uint32(src.Pix[i+2])
					i += 4
				}
			}
			o := out.PixOffset(x, y)
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = uint8(r/n), uint8(g/n), uint8(b/n), 255
		}
	}
	return out
}
