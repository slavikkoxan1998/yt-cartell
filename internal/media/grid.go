package media

// Storyboard sheets: an image model draws rows×cols equal panels separated by
// thin gutters. These helpers find the panels and crop them — pure Go, no
// FFmpeg needed.

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png" // decoders for DecodeImage
	"math"
	"os"
	"path/filepath"

	_ "golang.org/x/image/webp"
)

const (
	// sepShare: a row/column whose pixels are at least this share near-white
	// or near-black (together) runs through a gutter (or the outer
	// margin/border).
	sepShare = 0.9
	// marginMax: an outer margin/border is at most this share of the side.
	marginMax = 0.06
	// gutterWin: gutters are looked for within ± this share of the side
	// around the ideal equal split.
	gutterWin = 0.07
	// gutterMax: a gutter band (with its outline lines) is at most this share.
	gutterMax = 0.08
	// insetCut / insetGuess trim each panel edge: a little where the gutter was
	// found (anti-aliasing, JPEG ringing, thin outlines), more where it had to
	// be guessed (a gutter we could not see may still be there).
	insetCut   = 0.01
	insetGuess = 0.025
	jpegQ      = 92
)

// DecodeImage decodes PNG, JPEG or WEBP bytes; format is "png", "jpeg" or "webp".
func DecodeImage(data []byte) (image.Image, string, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("не вдалося прочитати зображення: %w", err)
	}
	return img, format, nil
}

// EncodeJPEG encodes img as a high-quality JPEG.
func EncodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQ}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GridRects returns the rows×cols panel rectangles of a storyboard sheet in
// reading order (left→right, top→bottom), in img's coordinates.
//
// Gutters — rows/columns crossing the whole sheet that are nearly all white
// or black (a white gutter with the panels' thin black outlines is one band)
// — are looked for near the ideal equal split, and a thin outer margin or
// border is dropped, so uneven AI-drawn grids still crop cleanly. Each panel
// is then trimmed by a small inset; where no gutter could be seen the equal
// split is used with a slightly larger inset.
func GridRects(img image.Image, rows, cols int) ([]image.Rectangle, error) {
	if rows < 1 || cols < 1 || rows > 16 || cols > 16 {
		return nil, fmt.Errorf("неправильна сітка %d×%d", rows, cols)
	}
	b := img.Bounds()
	W, H := b.Dx(), b.Dy()
	if W < cols*8 || H < rows*8 {
		return nil, fmt.Errorf("зображення %d×%d замале для сітки %d×%d", W, H, rows, cols)
	}
	px := toRGBA(img)
	colLight, colDark := make([]float64, W), make([]float64, W)
	rowLight, rowDark := make([]float64, H), make([]float64, H)
	for y := 0; y < H; y++ {
		line := px.Pix[y*px.Stride : y*px.Stride+W*4]
		for x := 0; x < W; x++ {
			switch pixelClass(line[x*4], line[x*4+1], line[x*4+2]) {
			case 1:
				colLight[x]++
				rowLight[y]++
			case 2:
				colDark[x]++
				rowDark[y]++
			}
		}
	}
	for x := range colLight {
		colLight[x] /= float64(H)
		colDark[x] /= float64(H)
	}
	for y := range rowLight {
		rowLight[y] /= float64(W)
		rowDark[y] /= float64(W)
	}
	xs := splitAxis(colLight, colDark, cols)
	ys := splitAxis(rowLight, rowDark, rows)
	rects := make([]image.Rectangle, 0, rows*cols)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			rects = append(rects, image.Rect(xs[c].lo, ys[r].lo, xs[c].hi, ys[r].hi).Add(b.Min))
		}
	}
	return rects, nil
}

// GridPanel returns panel index (0-based, reading order) of a rows×cols sheet.
func GridPanel(img image.Image, rows, cols, index int) (image.Image, error) {
	if index < 0 || index >= rows*cols {
		return nil, fmt.Errorf("панелі %d немає в сітці %d×%d", index+1, rows, cols)
	}
	rects, err := GridRects(img, rows, cols)
	if err != nil {
		return nil, err
	}
	return subImage(img, rects[index]), nil
}

// CropGridPanel crops panel index (0-based, reading order) of the rows×cols
// storyboard sheet srcPath (PNG, JPEG or WEBP) and writes it to outJPG as JPEG.
func CropGridPanel(srcPath string, rows, cols, index int, outJPG string) error {
	img, err := readImageFile(srcPath)
	if err != nil {
		return err
	}
	panel, err := GridPanel(img, rows, cols, index)
	if err != nil {
		return err
	}
	return writeJPEG(outJPG, panel)
}

// SplitGrid writes every panel of the rows×cols sheet srcPath to
// outDir/<prefix>-1.jpg … <prefix>-<rows*cols>.jpg and returns the paths in
// reading order.
func SplitGrid(srcPath string, rows, cols int, outDir, prefix string) ([]string, error) {
	img, err := readImageFile(srcPath)
	if err != nil {
		return nil, err
	}
	rects, err := GridRects(img, rows, cols)
	if err != nil {
		return nil, err
	}
	if prefix == "" {
		prefix = "panel"
	}
	out := make([]string, 0, len(rects))
	for i, r := range rects {
		p := filepath.Join(outDir, fmt.Sprintf("%s-%d.jpg", prefix, i+1))
		if err := writeJPEG(p, subImage(img, r)); err != nil {
			return out, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ---- detection -------------------------------------------------------------------------------

// pixelClass: 1 near-white (light grey included), 2 near-black, 0 content.
func pixelClass(r, g, b uint8) int {
	mx, mn := max(r, g, b), min(r, g, b)
	switch {
	case mn >= 215 && mx-mn <= 40:
		return 1
	case mx <= 45:
		return 2
	}
	return 0
}

// span is one panel along an axis: [lo, hi) after the inset.
type span struct{ lo, hi int }

// splitAxis finds n panels along one axis from the per-line shares of light
// and dark pixels.
func splitAxis(light, dark []float64, n int) []span {
	size := len(light)
	// A line through a gutter is white where it crosses other gutters and
	// black along panel outlines: count both. A white gutter next to dark
	// picture content is still found by lightOnly.
	sep := func(i int) bool { return light[i]+dark[i] >= sepShare }
	lightOnly := func(i int) bool { return light[i] >= sepShare }

	// Outer margin / border: a thin separator run touching the edge.
	lo, hi := 0, size
	for lo < size && sep(lo) {
		lo++
	}
	if float64(lo) > float64(size)*marginMax {
		lo = 0 // too wide for a margin: light or dark picture content
	}
	for hi > lo && sep(hi-1) {
		hi--
	}
	if float64(size-hi) > float64(size)*marginMax {
		hi = size
	}
	length := hi - lo
	if length < n*4 {
		lo, hi, length = 0, size, size
	}

	// Inner gutters near the ideal split.
	type cut struct {
		a, b  int // gutter band [a, b)
		found bool
	}
	win := max(1, int(float64(length)*gutterWin))
	widest := max(1, int(float64(length)*gutterMax))
	cuts := make([]cut, n-1)
	for k := 1; k < n; k++ {
		e := lo + length*k/n
		c := cut{a: e, b: e}
		if a, b, ok := nearestRun(sep, lo, hi, e, win, widest); ok {
			c = cut{a, b, true}
		} else if a, b, ok := nearestRun(lightOnly, lo, hi, e, win, widest); ok {
			c = cut{a, b, true}
		}
		cuts[k-1] = c
	}

	ideal := float64(length) / float64(n)
	out := make([]span, n)
	for k := 0; k < n; k++ {
		a, b := lo, hi
		aCut, bCut := true, true // outer edges: the sheet edge or its margin
		if k > 0 {
			a, aCut = cuts[k-1].b, cuts[k-1].found
		}
		if k < n-1 {
			b, bCut = cuts[k].a, cuts[k].found
		}
		if l := float64(b - a); l < ideal*0.6 || l > ideal*1.4 {
			return insetAll(equalSplit(lo, hi, n))
		}
		out[k] = inset(a, b, aCut, bCut)
	}
	return out
}

// nearestRun is the run of pred lines (at most widest long) whose centre is
// closest to e among the runs that reach into [e-win, e+win).
func nearestRun(pred func(int) bool, lo, hi, e, win, widest int) (a, b int, ok bool) {
	best := -1
	for i := max(lo, e-win); i < min(hi, e+win); {
		if !pred(i) {
			i++
			continue
		}
		ra, rb := i, i
		for ra > lo && pred(ra-1) {
			ra--
		}
		for rb < hi && pred(rb) {
			rb++
		}
		i = rb
		if rb-ra > widest {
			continue
		}
		d := (ra+rb)/2 - e
		if d < 0 {
			d = -d
		}
		if best < 0 || d < best {
			a, b, ok, best = ra, rb, true, d
		}
	}
	return a, b, ok
}

type edgeSpan struct {
	a, b       int
	aCut, bCut bool
}

func equalSplit(lo, hi, n int) []edgeSpan {
	out := make([]edgeSpan, n)
	for k := range out {
		out[k] = edgeSpan{lo + (hi-lo)*k/n, lo + (hi-lo)*(k+1)/n, k == 0, k == n-1}
	}
	return out
}

func insetAll(es []edgeSpan) []span {
	out := make([]span, len(es))
	for i, e := range es {
		out[i] = inset(e.a, e.b, e.aCut, e.bCut)
	}
	return out
}

// inset trims both edges of [a, b): insetCut where the edge is a detected cut
// (or the sheet edge), insetGuess where it is a guessed split.
func inset(a, b int, aCut, bCut bool) span {
	l := float64(b - a)
	trim := func(cut bool) int {
		f := insetGuess
		if cut {
			f = insetCut
		}
		return max(1, int(math.Round(l*f)))
	}
	s := span{a + trim(aCut), b - trim(bCut)}
	if s.hi <= s.lo { // tiny panels: keep at least one line
		s = span{a, max(a+1, b)}
	}
	return s
}

// ---- files -----------------------------------------------------------------------------------

func toRGBA(img image.Image) *image.RGBA {
	b := img.Bounds()
	if r, ok := img.(*image.RGBA); ok && b.Min == (image.Point{}) {
		return r
	}
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

func subImage(img image.Image, r image.Rectangle) image.Image {
	if s, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return s.SubImage(r)
	}
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), img, r.Min, draw.Src)
	return dst
}

func readImageFile(path string) (image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, _, err := DecodeImage(data)
	return img, err
}

// writeJPEG writes img as JPEG via a temp file (never a half-written image).
func writeJPEG(path string, img image.Image) error {
	if path == "" {
		return errors.New("не вказано файл для кадру")
	}
	data, err := EncodeJPEG(img)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := replaceFile(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
