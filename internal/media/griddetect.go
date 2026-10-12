package media

// Grid detection: what layout did the image model REALLY draw? GridRects
// assumes rows×cols and looks for gutters near the ideal split (falling back
// to an equal split), which crops through the picture when the model drew
// another layout (two columns of five, panels of different sizes). DetectGrid
// trusts nothing: it finds every gutter — a straight band crossing the whole
// sheet whose lines are nearly all light (white gutters; a dark pass for
// black ones) — from the luminance projections of the rows and columns,
// drops the outer margin and reports the panels between the gutters, how
// many rows and columns there are and whether they are of equal size.

import (
	"image"
	"math"
)

const (
	// detectLight: a gutter line is at least this share light pixels
	// (JPEG ringing and panel outlines may spoil a few).
	detectLight = 0.85
	// detectDark: the same for black gutters (second pass).
	detectDark = 0.92
	// detectBridge: non-gutter lines (outlines, ringing) of at most this
	// share of the side between two gutter runs join them into one gutter.
	detectBridge = 0.006
	// detectMinPanel: a span between gutters smaller than this share of the
	// side is noise inside a gutter, not a panel.
	detectMinPanel = 0.05
	// detectMaxMargin: an outer margin touching the edge is at most this share.
	detectMaxMargin = 0.2
	// evenTolerance: panels count as equal when the largest is at most this
	// much bigger than the smallest along each axis.
	evenTolerance = 1.12
)

// Grid is the layout DetectGrid found on a storyboard sheet.
type Grid struct {
	Rows, Cols int
	// Rects are the panels in reading order (left→right, top→bottom), in
	// the image's coordinates, trimmed by a small inset (anti-aliasing,
	// outlines). Rows×Cols of them.
	Rects []image.Rectangle
	// Even: the panels are of (nearly) equal width and height.
	Even bool
}

// Clean reports a rows×cols grid of equal panels — the only layout whose
// panels are cropped for the studio.
func (g Grid) Clean(rows, cols int) bool { return g.Rows == rows && g.Cols == cols && g.Even }

// DetectGrid finds the panels the sheet really has. A picture without any
// gutter is one 1×1 "grid" (Rects = the whole picture minus its margin).
func DetectGrid(img image.Image) Grid {
	b := img.Bounds()
	W, H := b.Dx(), b.Dy()
	if W < 16 || H < 16 {
		return Grid{Rows: 1, Cols: 1, Rects: []image.Rectangle{b}, Even: true}
	}
	pr := projections(img)
	light := gridOf(detectAxis(pr.colLight, pr.colLight, pr.colDark, pr.colWhite, detectLight), detectAxis(pr.rowLight, pr.rowLight, pr.rowDark, pr.rowWhite, detectLight), b)
	if light.Even && light.Rows*light.Cols > 1 {
		return light // white gutters (what the studio asks for)
	}
	dark := gridOf(detectAxis(pr.colDark, pr.colLight, pr.colDark, pr.colBlack, detectDark), detectAxis(pr.rowDark, pr.rowLight, pr.rowDark, pr.rowBlack, detectDark), b)
	if dark.Even && dark.Rows*dark.Cols > 1 {
		return dark // black gutters
	}
	return light
}

// lineStats are the per-column and per-row projections of a sheet: the
// shares of light and dark pixels, and how white (mean of the darkest
// channel) and how black (255 − mean of the brightest channel) each line is
// — a pure white gutter is whiter than a beige or light-grey band of the
// picture next to it.
type lineStats struct {
	colLight, colDark, colWhite, colBlack []float64
	rowLight, rowDark, rowWhite, rowBlack []float64
}

func projections(img image.Image) lineStats {
	px := toRGBA(img)
	W, H := px.Bounds().Dx(), px.Bounds().Dy()
	mk := func(n int) []float64 { return make([]float64, n) }
	s := lineStats{colLight: mk(W), colDark: mk(W), colWhite: mk(W), colBlack: mk(W),
		rowLight: mk(H), rowDark: mk(H), rowWhite: mk(H), rowBlack: mk(H)}
	for y := 0; y < H; y++ {
		line := px.Pix[y*px.Stride : y*px.Stride+W*4]
		for x := 0; x < W; x++ {
			r, g, b := line[x*4], line[x*4+1], line[x*4+2]
			switch pixelClass(r, g, b) {
			case 1:
				s.colLight[x]++
				s.rowLight[y]++
			case 2:
				s.colDark[x]++
				s.rowDark[y]++
			}
			lo, hi := float64(min(r, g, b)), float64(255-max(r, g, b))
			s.colWhite[x] += lo
			s.rowWhite[y] += lo
			s.colBlack[x] += hi
			s.rowBlack[y] += hi
		}
	}
	for x := 0; x < W; x++ {
		s.colLight[x] /= float64(H)
		s.colDark[x] /= float64(H)
		s.colWhite[x] /= float64(H)
		s.colBlack[x] /= float64(H)
	}
	for y := 0; y < H; y++ {
		s.rowLight[y] /= float64(W)
		s.rowDark[y] /= float64(W)
		s.rowWhite[y] /= float64(W)
		s.rowBlack[y] /= float64(W)
	}
	return s
}

// coreTol: the lines of a gutter are within this many levels (0–255) of
// its whitest (blackest) line; a light band of the picture beside it is not.
const coreTol = 10

// core trims a run of light (dark) lines to its gutter: the contiguous
// lines around the whitest one (from the sheet edge for a margin) whose
// level is within coreTol of it.
func core(r run, level []float64, size int) run {
	peak, at := -1.0, r.a
	for i := r.a; i < r.b; i++ {
		if level[i] > peak {
			peak, at = level[i], i
		}
	}
	ok := func(i int) bool { return level[i] >= peak-coreTol }
	switch {
	case r.a == 0 && ok(0):
		at = 0
	case r.b == size && ok(size-1):
		at = size - 1
	}
	a, b := at, at+1
	for a > r.a && ok(a-1) {
		a--
	}
	for b < r.b && ok(b) {
		b++
	}
	return run{a, b}
}

// run is a band [a, b) of lines.
type run struct{ a, b int }

// detectAxis returns the panel spans along one axis: gutter runs (share ≥
// thr, widened over adjacent outline lines and joined across tiny gaps), the
// margins touching the edges dropped, spans too small for a panel merged into
// the gutters around them.
func detectAxis(share, light, dark, level []float64, thr float64) []span {
	size := len(share)
	gut := func(i int) bool { return share[i] >= thr }
	sep := func(i int) bool { return light[i]+dark[i] >= sepShare }
	// The gutters are the whitest (blackest) lines of the sheet: a run whose
	// best line is clearly less white is a light band of the picture.
	peak := 0.0
	for i := 0; i < size; i++ {
		if gut(i) {
			peak = math.Max(peak, level[i])
		}
	}
	var runs []run
	for i := 0; i < size; {
		if !gut(i) {
			i++
			continue
		}
		a := i
		for i < size && gut(i) {
			i++
		}
		if c := core(run{a, i}, level, size); level[c.a] >= peak-1.5*coreTol {
			runs = append(runs, c)
		}
	}
	// Widen over a few outline lines (dark lines beside a white gutter: sep
	// but not gutter) and join runs split by a few lines of ringing.
	bridge := max(2, int(math.Round(float64(size)*detectBridge)))
	outline := max(2, int(math.Round(float64(size)*0.005)))
	for k := range runs {
		for n := 0; n < outline && runs[k].a > 0 && sep(runs[k].a-1) && !gut(runs[k].a-1); n++ {
			runs[k].a--
		}
		for n := 0; n < outline && runs[k].b < size && sep(runs[k].b) && !gut(runs[k].b); n++ {
			runs[k].b++
		}
	}
	var joined []run
	for _, r := range runs {
		if n := len(joined); n > 0 && r.a-joined[n-1].b <= bridge {
			joined[n-1].b = max(joined[n-1].b, r.b)
			continue
		}
		joined = append(joined, r)
	}
	// Margins: runs touching an edge (not too wide to be one).
	lo, hi := 0, size
	inner := joined[:0:0]
	for _, r := range joined {
		switch {
		case r.a == 0 && float64(r.b) <= float64(size)*detectMaxMargin:
			lo = r.b
		case r.b == size && float64(size-r.a) <= float64(size)*detectMaxMargin:
			hi = r.a
		case r.a == 0 || r.b == size:
			// a huge light area at the edge: picture content
		case float64(r.b-r.a) > float64(size)*gutterMax:
			// too wide for a gutter: a light band of the picture
		default:
			inner = append(inner, r)
		}
	}
	if hi-lo < size/4 {
		lo, hi = 0, size
	}
	// Panels between the gutters; slivers join the gutter next to them.
	minPanel := max(4, int(float64(hi-lo)*detectMinPanel))
	var out []span
	at := lo
	for _, r := range inner {
		if r.a <= at || r.b >= hi {
			at = max(at, r.b)
			continue
		}
		if r.a-at >= minPanel {
			out = append(out, span{at, r.a})
		}
		at = r.b
	}
	if hi-at >= minPanel {
		out = append(out, span{at, hi})
	} else if len(out) == 0 {
		out = append(out, span{lo, hi})
	}
	return out
}

// gridOf turns the spans of both axes into the panels (inset) and judges
// whether they are equal.
func gridOf(xs, ys []span, b image.Rectangle) Grid {
	g := Grid{Rows: len(ys), Cols: len(xs), Even: even(xs) && even(ys)}
	for _, y := range ys {
		for _, x := range xs {
			ix, iy := inset(x.lo, x.hi, true, true), inset(y.lo, y.hi, true, true)
			g.Rects = append(g.Rects, image.Rect(ix.lo, iy.lo, ix.hi, iy.hi).Add(b.Min))
		}
	}
	return g
}

// even: the spans are of (nearly) equal length.
func even(ss []span) bool {
	if len(ss) == 0 {
		return false
	}
	lo, hi := math.MaxInt, 0
	for _, s := range ss {
		l := s.hi - s.lo
		lo, hi = min(lo, l), max(hi, l)
	}
	return lo > 0 && float64(hi) <= float64(lo)*evenTolerance
}

// FitAspect is the largest centred part of r with the aspect aw:ah — the
// panel cropped (never stretched) to the project's frame format.
func FitAspect(r image.Rectangle, aw, ah int) image.Rectangle {
	if aw <= 0 || ah <= 0 || r.Empty() {
		return r
	}
	w, h := r.Dx(), r.Dy()
	if w*ah > h*aw { // too wide: trim the sides
		nw := max(1, int(math.Round(float64(h)*float64(aw)/float64(ah))))
		x := r.Min.X + (w-nw)/2
		return image.Rect(x, r.Min.Y, x+nw, r.Max.Y)
	}
	nh := max(1, int(math.Round(float64(w)*float64(ah)/float64(aw))))
	y := r.Min.Y + (h-nh)/2
	return image.Rect(r.Min.X, y, r.Max.X, y+nh)
}

// AspectOf parses "9:16" / "16:9" into its two numbers (16:9 when unknown).
func AspectOf(aspect string) (w, h int) {
	if aspect == "9:16" {
		return 9, 16
	}
	return 16, 9
}

// SubImage returns the part r of img (shared pixels when img supports it).
func SubImage(img image.Image, r image.Rectangle) image.Image { return subImage(img, r) }
