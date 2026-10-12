package cover

// Rasterization: outlines → 8-bit coverage masks (golang.org/x/image/vector,
// exact area coverage), outline strokes as the union of offset copies (a
// Minkowski sum with a disc: round joins, no spikes), separable Gaussian-like
// blur for shadows and glows, and compositing of masks onto the canvas.

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/vector"
)

// affine maps local points (em for glyphs, px for shapes) to canvas pixels:
// x' = a·x + c·y + e, y' = b·x + d·y + f.
type affine struct{ a, b, c, d, e, f float32 }

func (m affine) apply(p pt) (float32, float32) {
	return m.a*p.X + m.c*p.Y + m.e, m.b*p.X + m.d*p.Y + m.f
}

// scaleAt places em-space outlines of size s with the pen at (x, y).
func scaleAt(s, x, y float32) affine { return affine{a: s, d: s, e: x, f: y} }

// rotateAbout rotates m's output by deg degrees around (cx, cy).
func (m affine) rotateAbout(deg, cx, cy float32) affine {
	if deg == 0 {
		return m
	}
	sin, cos := math.Sincos(float64(deg) * math.Pi / 180)
	sn, cs := float32(sin), float32(cos)
	// r(p) = R·(p − c) + c, applied after m.
	return affine{
		a: cs*m.a - sn*m.b, b: sn*m.a + cs*m.b,
		c: cs*m.c - sn*m.d, d: sn*m.c + cs*m.d,
		e: cs*(m.e-cx) - sn*(m.f-cy) + cx,
		f: sn*(m.e-cx) + cs*(m.f-cy) + cy,
	}
}

// shape is one outline to rasterize.
type shape struct {
	path []pathOp
	m    affine
}

// rectF is a float rectangle in canvas pixels.
type rectF struct{ x0, y0, x1, y1 float32 }

func (r rectF) w() float32 { return r.x1 - r.x0 }
func (r rectF) h() float32 { return r.y1 - r.y0 }
func (r rectF) empty() bool {
	return r.x1 <= r.x0 || r.y1 <= r.y0
}
func (r rectF) union(o rectF) rectF {
	if r.empty() {
		return o
	}
	if o.empty() {
		return r
	}
	return rectF{min(r.x0, o.x0), min(r.y0, o.y0), max(r.x1, o.x1), max(r.y1, o.y1)}
}
func (r rectF) inset(dx, dy float32) rectF {
	return rectF{r.x0 + dx, r.y0 + dy, r.x1 - dx, r.y1 - dy}
}
func (r rectF) overlaps(o rectF) bool {
	return r.x0 < o.x1 && o.x0 < r.x1 && r.y0 < o.y1 && o.y0 < r.y1
}
func (r rectF) contains(o rectF) bool {
	const eps = 0.5
	return o.x0 >= r.x0-eps && o.y0 >= r.y0-eps && o.x1 <= r.x1+eps && o.y1 <= r.y1+eps
}
func (r rectF) translate(dx, dy float32) rectF {
	return rectF{r.x0 + dx, r.y0 + dy, r.x1 + dx, r.y1 + dy}
}
func (r rectF) rect() image.Rectangle {
	return image.Rect(int(math.Floor(float64(r.x0))), int(math.Floor(float64(r.y0))),
		int(math.Ceil(float64(r.x1))), int(math.Ceil(float64(r.y1))))
}

// ---- masks ------------------------------------------------------------------------------

// mask is 8-bit coverage over a canvas region.
type mask struct {
	r   image.Rectangle
	pix []uint8
}

func newMask(r image.Rectangle) *mask {
	if r.Empty() {
		r = image.Rectangle{}
	}
	return &mask{r: r, pix: make([]uint8, r.Dx()*r.Dy())}
}

func (m *mask) alpha() *image.Alpha {
	return &image.Alpha{Pix: m.pix, Stride: m.r.Dx(), Rect: image.Rect(0, 0, m.r.Dx(), m.r.Dy())}
}

func (m *mask) maxWith(o *mask) {
	if o.r != m.r {
		panic("cover: mask geometry mismatch")
	}
	d, s := m.pix, o.pix[:len(m.pix)]
	for i, v := range s {
		if v > d[i] {
			d[i] = v
		}
	}
}

func (m *mask) clone() *mask {
	return &mask{r: m.r, pix: append([]uint8(nil), m.pix...)}
}

var rasterPool = sync.Pool{New: func() any { return &vector.Rasterizer{} }}

// raster draws shapes (shifted by dx, dy) into m, replacing its content.
func (m *mask) raster(shapes []shape, dx, dy float32) {
	w, h := m.r.Dx(), m.r.Dy()
	if w == 0 || h == 0 {
		return
	}
	z := rasterPool.Get().(*vector.Rasterizer)
	defer rasterPool.Put(z)
	z.Reset(w, h)
	z.DrawOp = draw.Src
	ox, oy := dx-float32(m.r.Min.X), dy-float32(m.r.Min.Y)
	for _, s := range shapes {
		addPath(z, s, ox, oy)
	}
	z.Draw(m.alpha(), image.Rect(0, 0, w, h), image.Opaque, image.Point{})
}

func addPath(z *vector.Rasterizer, s shape, ox, oy float32) {
	open := false
	for _, op := range s.path {
		x0, y0 := s.m.apply(op.pts[0])
		x0, y0 = x0+ox, y0+oy
		switch op.op {
		case sfnt.SegmentOpMoveTo:
			if open {
				z.ClosePath()
			}
			z.MoveTo(x0, y0)
			open = true
		case sfnt.SegmentOpLineTo:
			z.LineTo(x0, y0)
		case sfnt.SegmentOpQuadTo:
			x1, y1 := s.m.apply(op.pts[1])
			z.QuadTo(x0, y0, x1+ox, y1+oy)
		case sfnt.SegmentOpCubeTo:
			x1, y1 := s.m.apply(op.pts[1])
			x2, y2 := s.m.apply(op.pts[2])
			z.CubeTo(x0, y0, x1+ox, y1+oy, x2+ox, y2+oy)
		}
	}
	if open {
		z.ClosePath()
	}
}

// fillMask rasterizes shapes into a new mask over region.
func fillMask(region image.Rectangle, shapes []shape) *mask {
	m := newMask(region)
	m.raster(shapes, 0, 0)
	return m
}

// dilate is the outline of shapes grown by radius px: the union (max) of
// copies shifted around rings of radius r, r/2 (and the shapes themselves),
// each rasterized exactly — smooth round joins at any width.
func dilate(region image.Rectangle, shapes []shape, radius float32) *mask {
	m := fillMask(region, shapes)
	if radius <= 0.05 || m.r.Empty() {
		return m
	}
	tmp := newMask(region)
	for _, o := range ringOffsets(radius) {
		tmp.raster(shapes, o[0], o[1])
		m.maxWith(tmp)
	}
	return m
}

// ringOffsets are the copy offsets for dilate: rings dense enough that the
// polygonal envelope deviates from a circle by well under 0.1 px.
func ringOffsets(r float32) [][2]float32 {
	var out [][2]float32
	ring := func(rr, phase float64) {
		n := int(math.Ceil(2 * math.Pi * rr / 2.2))
		n = max(n, 8)
		for k := 0; k < n; k++ {
			a := (float64(k) + phase) * 2 * math.Pi / float64(n)
			out = append(out, [2]float32{float32(rr * math.Cos(a)), float32(rr * math.Sin(a))})
		}
	}
	ring(float64(r), 0)
	if r > 2.5 {
		ring(float64(r)*0.5, 0.5)
	}
	return out
}

// blur returns a blurred copy of m (Gaussian of the given sigma, as three
// box passes). The region stays the same: callers leave room for the spread.
func blur(m *mask, sigma float32) *mask {
	out := m.clone()
	if sigma < 0.3 || m.r.Empty() {
		return out
	}
	w, h := m.r.Dx(), m.r.Dy()
	buf := make([]float32, w*h)
	for i, v := range m.pix {
		buf[i] = float32(v)
	}
	tmp := make([]float32, max(w, h))
	for _, size := range boxSizes(float64(sigma), 3) {
		rad := (size - 1) / 2
		if rad < 1 {
			continue
		}
		boxH(buf, tmp, w, h, rad)
		boxV(buf, tmp, w, h, rad)
	}
	for i, v := range buf {
		switch {
		case v <= 0:
			out.pix[i] = 0
		case v >= 255:
			out.pix[i] = 255
		default:
			out.pix[i] = uint8(v + 0.5)
		}
	}
	return out
}

// boxSizes are the widths of n box filters approximating a Gaussian.
func boxSizes(sigma float64, n int) []int {
	wIdeal := math.Sqrt(12*sigma*sigma/float64(n) + 1)
	wl := int(math.Floor(wIdeal))
	if wl%2 == 0 {
		wl--
	}
	wu := wl + 2
	mIdeal := (12*sigma*sigma - float64(n*wl*wl) - float64(4*n*wl) - float64(3*n)) / float64(-4*wl-4)
	mm := int(math.Round(mIdeal))
	sizes := make([]int, n)
	for i := range sizes {
		if i < mm {
			sizes[i] = wl
		} else {
			sizes[i] = wu
		}
	}
	return sizes
}

// boxH is a horizontal running-sum box blur (outside = 0).
func boxH(buf, tmp []float32, w, h, r int) {
	inv := 1 / float32(2*r+1)
	for y := 0; y < h; y++ {
		row := buf[y*w : y*w+w]
		copy(tmp[:w], row)
		var acc float32
		for x := 0; x <= r && x < w; x++ {
			acc += tmp[x]
		}
		for x := 0; x < w; x++ {
			row[x] = acc * inv
			if x+r+1 < w {
				acc += tmp[x+r+1]
			}
			if x-r >= 0 {
				acc -= tmp[x-r]
			}
		}
	}
}

// boxV is the vertical pass of boxH.
func boxV(buf, tmp []float32, w, h, r int) {
	inv := 1 / float32(2*r+1)
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			tmp[y] = buf[y*w+x]
		}
		var acc float32
		for y := 0; y <= r && y < h; y++ {
			acc += tmp[y]
		}
		for y := 0; y < h; y++ {
			buf[y*w+x] = acc * inv
			if y+r+1 < h {
				acc += tmp[y+r+1]
			}
			if y-r >= 0 {
				acc -= tmp[y-r]
			}
		}
	}
}

// paint composites colour c through m (times opacity) onto dst, shifted by
// (dx, dy) pixels.
func paint(dst *image.RGBA, m *mask, c color.NRGBA, opacity float32, dx, dy int) {
	if m == nil || opacity <= 0 {
		return
	}
	op := uint32(min(1, opacity)*256 + 0.5)
	r := m.r.Add(image.Pt(dx, dy)).Intersect(dst.Rect)
	if r.Empty() {
		return
	}
	cr, cg, cb := uint32(c.R), uint32(c.G), uint32(c.B)
	mw := m.r.Dx()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		mrow := m.pix[(y-dy-m.r.Min.Y)*mw+(r.Min.X-dx-m.r.Min.X):]
		di := dst.PixOffset(r.Min.X, y)
		drow := dst.Pix[di : di+4*r.Dx()]
		for x := 0; x < r.Dx(); x++ {
			a := uint32(mrow[x]) * op >> 8
			if a == 0 {
				continue
			}
			p := drow[x*4 : x*4+4 : x*4+4]
			if a >= 255 {
				p[0], p[1], p[2], p[3] = uint8(cr), uint8(cg), uint8(cb), 255
				continue
			}
			ia := 255 - a
			p[0] = uint8((uint32(p[0])*ia + cr*a + 127) / 255)
			p[1] = uint8((uint32(p[1])*ia + cg*a + 127) / 255)
			p[2] = uint8((uint32(p[2])*ia + cb*a + 127) / 255)
			p[3] = uint8((uint32(p[3])*ia + 255*a + 127) / 255)
		}
	}
}

// ---- simple shapes ----------------------------------------------------------------------

// kappa is the cubic control distance of a quarter circle.
const kappa = 0.5522848

// roundRect is a closed rounded rectangle path in local px (clockwise on
// screen, like the glyph outer contours).
func roundRect(x0, y0, x1, y1, r float32) []pathOp {
	r = max(0, min(r, (x1-x0)/2, (y1-y0)/2))
	k := r * kappa
	p := func(x, y float32) pt { return pt{x, y} }
	if r == 0 {
		return []pathOp{
			{op: sfnt.SegmentOpMoveTo, pts: [3]pt{p(x0, y0)}},
			{op: sfnt.SegmentOpLineTo, pts: [3]pt{p(x1, y0)}},
			{op: sfnt.SegmentOpLineTo, pts: [3]pt{p(x1, y1)}},
			{op: sfnt.SegmentOpLineTo, pts: [3]pt{p(x0, y1)}},
		}
	}
	return []pathOp{
		{op: sfnt.SegmentOpMoveTo, pts: [3]pt{p(x0+r, y0)}},
		{op: sfnt.SegmentOpLineTo, pts: [3]pt{p(x1-r, y0)}},
		{op: sfnt.SegmentOpCubeTo, pts: [3]pt{p(x1-r+k, y0), p(x1, y0+r-k), p(x1, y0+r)}},
		{op: sfnt.SegmentOpLineTo, pts: [3]pt{p(x1, y1-r)}},
		{op: sfnt.SegmentOpCubeTo, pts: [3]pt{p(x1, y1-r+k), p(x1-r+k, y1), p(x1-r, y1)}},
		{op: sfnt.SegmentOpLineTo, pts: [3]pt{p(x0+r, y1)}},
		{op: sfnt.SegmentOpCubeTo, pts: [3]pt{p(x0+r-k, y1), p(x0, y1-r+k), p(x0, y1-r)}},
		{op: sfnt.SegmentOpLineTo, pts: [3]pt{p(x0, y0+r)}},
		{op: sfnt.SegmentOpCubeTo, pts: [3]pt{p(x0, y0+r-k), p(x0+r-k, y0), p(x0+r, y0)}},
	}
}

var identity = affine{a: 1, d: 1}
