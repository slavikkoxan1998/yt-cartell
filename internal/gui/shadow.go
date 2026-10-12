package gui

import (
	"image"
	"image/color"
	"math"
	"sync"

	"gioui.org/f32"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// Soft shadows. Gio has no blur, so each shadow layer is a small texture of a
// Gaussian-blurred rounded rectangle (σ = blur/2, like CSS box-shadow),
// computed once on the CPU from the shape's signed distance and cached. Large
// shapes draw it as a 9-slice (corners as-is, edges stretched), so one
// texture serves every card of that radius and blur. Alphas are corrected
// for Gio's linear-light blending, so the shadow is as light and soft as the
// design's CSS.

// Shadow paints elevation e under the rounded rectangle r (px), radius dp.
func Shadow(gtx C, th *Theme, r image.Rectangle, radius unit.Dp, e Elevation) {
	rad := gtx.Dp(radius)
	for _, l := range e {
		if l.Alpha <= 0.001 {
			continue
		}
		drawShadowLayer(gtx, th, r, rad, l)
	}
}

type shadowKey struct {
	w, h   int // exact shape size in px per axis; 0 = canonical (sliced) axis
	rad    int // corner radius px
	sigma  int // σ in quarter pixels
	alpha  int // alpha in 1/1000
	col    color.NRGBA
	ground color.NRGBA
}

type shadowTex struct {
	op     paint.ImageOp
	img    *image.RGBA // the pixels (tests)
	margin int         // blur margin around the shape (px)
	size   image.Point
}

var (
	shadowMu    sync.Mutex
	shadowCache = map[shadowKey]*shadowTex{}
)

const shadowCacheCap = 384

func drawShadowLayer(gtx C, th *Theme, r image.Rectangle, rad int, l ShadowLayer) {
	px := gtx.Metric.PxPerDp
	sigma := max(0.5, float32(l.Blur)*px/2)
	spread := int(math.Round(float64(float32(l.Spread) * px)))
	dy := int(math.Round(float64(float32(l.Y) * px)))
	shape := r.Add(image.Pt(0, dy)).Inset(-spread)
	if shape.Dx() <= 0 || shape.Dy() <= 0 {
		return
	}
	rad = max(0, min(rad+spread, min(shape.Dx(), shape.Dy())/2))
	pal, dark := th.shadowPalette()
	alpha := l.Alpha
	if dark {
		alpha = min(0.6, alpha*2.6) // dark grounds need stronger shadows to read
	}
	key := shadowKey{
		rad:    rad,
		sigma:  int(math.Round(float64(sigma*2))) * 2,   // ½ px steps (quarter-px units)
		alpha:  int(math.Round(float64(alpha*200))) * 5, // 1/200 steps (1/1000 units)
		col:    pal.Shadow,
		ground: pal.Ground,
	}
	if key.alpha <= 0 {
		return
	}
	sig := float32(key.sigma) / 4
	margin := int(math.Ceil(float64(2.5*sig))) + 1
	// Per axis, the texture is either canonical (a flat middle stretched to
	// any length: 3-slice) or the exact size of a small shape. So a pill of any
	// width, or a card of any size, shares one texture, and animated widths
	// (the sliding tab indicator) never rebuild it.
	canon := 2*(rad+margin) + 3
	if shape.Dx() < canon {
		key.w = shape.Dx()
	}
	if shape.Dy() < canon {
		key.h = shape.Dy()
	}
	tex := shadowTexture(key, sig, margin)
	outer := shape.Inset(-tex.margin)
	L := tex.margin + rad + tex.margin
	type span struct{ s0, s1, d0, d1 int }
	axis := func(exact bool, texLen, o0, o1 int) []span {
		if exact {
			return []span{{0, texLen, o0, o1}}
		}
		return []span{{0, L, o0, o0 + L}, {L, L + 1, o0 + L, o1 - L}, {texLen - L, texLen, o1 - L, o1}}
	}
	xs := axis(key.w != 0, tex.size.X, outer.Min.X, outer.Max.X)
	ys := axis(key.h != 0, tex.size.Y, outer.Min.Y, outer.Max.Y)
	for _, y := range ys {
		for _, x := range xs {
			dst := image.Rect(x.d0, y.d0, x.d1, y.d1)
			if dst.Empty() {
				continue
			}
			drawTexRegion(gtx.Ops, tex.op, image.Rect(x.s0, y.s0, x.s1, y.s1), dst)
		}
	}
}

// drawTexRegion paints the src sub-rectangle of img into dst (scaled).
func drawTexRegion(ops *op.Ops, img paint.ImageOp, src, dst image.Rectangle) {
	sx := float32(dst.Dx()) / float32(src.Dx())
	sy := float32(dst.Dy()) / float32(src.Dy())
	cl := clip.Rect(dst).Push(ops)
	tr := f32.AffineId().
		Offset(f32.Pt(-float32(src.Min.X), -float32(src.Min.Y))).
		Scale(f32.Point{}, f32.Pt(sx, sy)).
		Offset(f32.Pt(float32(dst.Min.X), float32(dst.Min.Y)))
	t := op.Affine(tr).Push(ops)
	img.Add(ops)
	paint.PaintOp{}.Add(ops)
	t.Pop()
	cl.Pop()
}

// shadowTexture returns (building once) the blurred shape for key.
func shadowTexture(key shadowKey, sigma float32, margin int) *shadowTex {
	shadowMu.Lock()
	defer shadowMu.Unlock()
	if t, ok := shadowCache[key]; ok {
		return t
	}
	if len(shadowCache) >= shadowCacheCap {
		clear(shadowCache)
	}
	w, h := key.w, key.h
	if w == 0 {
		w = 2*(key.rad+margin) + 3
	}
	if h == 0 {
		h = 2*(key.rad+margin) + 3
	}
	W, H := w+2*margin, h+2*margin
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	alpha := float32(key.alpha) / 1000
	// Centre and half-size of the shape; signed distance to a rounded rect.
	cx, cy := float32(W)/2, float32(H)/2
	hx, hy := float32(w)/2, float32(h)/2
	rad := float32(key.rad)
	sc := key.col
	// Premultiplied colour channels are applied per pixel below.
	for y := 0; y < H; y++ {
		py := abs32(float32(y)+0.5-cy) - (hy - rad)
		for x := 0; x < W; x++ {
			px := abs32(float32(x)+0.5-cx) - (hx - rad)
			ox, oy := max(px, 0), max(py, 0)
			sd := float32(math.Sqrt(float64(ox*ox+oy*oy))) + min(max(px, py), 0) - rad
			cov := alpha * gaussQ(sd/sigma)
			a := linearAlpha(key.ground, sc, cov)
			if a <= 0 {
				continue
			}
			o := img.PixOffset(x, y)
			img.Pix[o+0] = uint8(float32(sc.R)*a + 0.5)
			img.Pix[o+1] = uint8(float32(sc.G)*a + 0.5)
			img.Pix[o+2] = uint8(float32(sc.B)*a + 0.5)
			img.Pix[o+3] = uint8(255*a + 0.5)
		}
	}
	t := &shadowTex{op: paint.NewImageOp(img), img: img, margin: margin, size: image.Pt(W, H)}
	t.op.Filter = paint.FilterLinear
	shadowCache[key] = t
	return t
}
