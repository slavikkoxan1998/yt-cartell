package media

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var palette = []color.RGBA{
	{220, 40, 40, 255}, {40, 160, 60, 255}, {50, 80, 200, 255},
	{200, 170, 30, 255}, {150, 50, 170, 255}, {30, 160, 170, 255},
	{230, 120, 30, 255}, {110, 110, 110, 255}, {160, 90, 60, 255},
}

// sheet describes a synthetic storyboard: panels on a background, with
// optional outer margin, gutters, black outlines inside each panel and
// per-gutter offsets (AI grids are rarely exact).
type sheet struct {
	w, h, rows, cols int
	margin, gutter   int
	outline          int
	jx, jy           []int // offsets of the inner gutters (px)
	bg               color.RGBA
	fill             func(panel, x, y int) color.RGBA // nil = solid palette colour
}

func axis(size, n, margin, gutter int, jitter []int) [][2]int {
	pw := (size - 2*margin - (n-1)*gutter) / n
	starts := make([]int, n)
	for k := range starts {
		starts[k] = margin + k*(pw+gutter)
		if k > 0 && k-1 < len(jitter) {
			starts[k] += jitter[k-1]
		}
	}
	out := make([][2]int, n)
	for k := range out {
		end := size - margin
		if k < n-1 {
			end = starts[k+1] - gutter
		}
		out[k] = [2]int{starts[k], end}
	}
	return out
}

// panels are the true panel rectangles; content excludes the outline.
func (s sheet) panels() (panels, content []image.Rectangle) {
	xs := axis(s.w, s.cols, s.margin, s.gutter, s.jx)
	ys := axis(s.h, s.rows, s.margin, s.gutter, s.jy)
	for r := 0; r < s.rows; r++ {
		for c := 0; c < s.cols; c++ {
			p := image.Rect(xs[c][0], ys[r][0], xs[c][1], ys[r][1])
			panels = append(panels, p)
			content = append(content, p.Inset(s.outline))
		}
	}
	return panels, content
}

func (s sheet) draw() *image.RGBA {
	bg := s.bg
	if bg == (color.RGBA{}) {
		bg = color.RGBA{255, 255, 255, 255}
	}
	img := image.NewRGBA(image.Rect(0, 0, s.w, s.h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = bg.R, bg.G, bg.B, 255
	}
	panels, content := s.panels()
	for i, p := range panels {
		for y := p.Min.Y; y < p.Max.Y; y++ {
			for x := p.Min.X; x < p.Max.X; x++ {
				c := color.RGBA{0, 0, 0, 255} // outline
				if (image.Point{x, y}).In(content[i]) {
					if s.fill != nil {
						c = s.fill(i, x, y)
					} else {
						c = palette[i%len(palette)]
					}
				}
				img.SetRGBA(x, y, c)
			}
		}
	}
	return img
}

func area(r image.Rectangle) int { return r.Dx() * r.Dy() }

// checkRects: every crop lies inside its panel's content (no gutter, outline
// or neighbour) and covers at least minCover of it.
func checkRects(t *testing.T, name string, got, content []image.Rectangle, slack int, minCover float64) {
	t.Helper()
	if len(got) != len(content) {
		t.Fatalf("%s: %d rects, want %d", name, len(got), len(content))
	}
	for i := range got {
		if !got[i].In(content[i].Inset(-slack)) {
			t.Errorf("%s: panel %d crop %v spills out of %v", name, i+1, got[i], content[i])
			continue
		}
		if cover := float64(area(got[i].Intersect(content[i]))) / float64(area(content[i])); cover < minCover {
			t.Errorf("%s: panel %d crop %v covers %.0f%% of %v", name, i+1, got[i], cover*100, content[i])
		}
	}
}

func noise(seed int64) func(panel, x, y int) color.RGBA {
	return func(panel, x, y int) color.RGBA {
		r := rand.New(rand.NewSource(seed + int64(panel*1_000_003+y*4099+x)))
		base := palette[panel%len(palette)]
		j := func(v uint8) uint8 { return uint8(max(0, min(255, int(v)+r.Intn(81)-40))) }
		return color.RGBA{j(base.R), j(base.G), j(base.B), 255}
	}
}

func TestGridRectsFindsPanels(t *testing.T) {
	cases := []struct {
		name     string
		s        sheet
		minCover float64
	}{
		{"clean 16:9", sheet{w: 480, h: 270, rows: 3, cols: 3, gutter: 6}, 0.93},
		{"margin + thick gutters + outlines + uneven", sheet{w: 600, h: 338, rows: 3, cols: 3, margin: 14, gutter: 10, outline: 3,
			jx: []int{6, -5}, jy: []int{-4, 5}}, 0.93},
		{"vertical 9:16, uneven", sheet{w: 270, h: 480, rows: 3, cols: 3, margin: 8, gutter: 5, jx: []int{-4, 3}, jy: []int{7, -6}}, 0.93},
		{"noisy picture content", sheet{w: 480, h: 270, rows: 3, cols: 3, margin: 6, gutter: 8, jx: []int{5, 0}, jy: []int{0, -4},
			fill: noise(7)}, 0.93},
		{"dark (not black) content", sheet{w: 480, h: 270, rows: 3, cols: 3, gutter: 6, jx: []int{4, -4},
			fill: func(p, x, y int) color.RGBA { return color.RGBA{60, uint8(50 + p), 70, 255} }}, 0.93},
		{"black gutters", sheet{w: 480, h: 270, rows: 3, cols: 3, gutter: 6, jx: []int{-5, 5}, bg: color.RGBA{0, 0, 0, 255}}, 0.93},
		// Nothing to detect: equal split with the larger inset still stays inside.
		{"no gutters", sheet{w: 480, h: 270, rows: 3, cols: 3}, 0.88},
		{"near-black content, centred gutters", sheet{w: 480, h: 270, rows: 3, cols: 3, gutter: 6,
			fill: func(p, x, y int) color.RGBA { return color.RGBA{20, 20, uint8(25 + p), 255} }}, 0.85},
		{"2×4 grid", sheet{w: 640, h: 360, rows: 2, cols: 4, margin: 10, gutter: 8, jx: []int{3, -3, 2}}, 0.93},
	}
	for _, tc := range cases {
		img := tc.s.draw()
		got, err := GridRects(img, tc.s.rows, tc.s.cols)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		_, content := tc.s.panels()
		checkRects(t, tc.name, got, content, 0, tc.minCover)
	}
}

func TestGridRectsOffsetBoundsAndJPEG(t *testing.T) {
	s := sheet{w: 600, h: 338, rows: 3, cols: 3, margin: 12, gutter: 9, outline: 2, jx: []int{5, -6}, jy: []int{3, -3}}
	full := image.NewRGBA(image.Rect(0, 0, 700, 400))
	src := s.draw()
	for y := 0; y < s.h; y++ {
		copy(full.Pix[(y+40)*full.Stride+50*4:], src.Pix[y*src.Stride:(y+1)*src.Stride])
	}
	sub := full.SubImage(image.Rect(50, 40, 50+s.w, 40+s.h)) // bounds do not start at 0,0
	got, err := GridRects(sub, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	_, content := s.panels()
	for i := range content {
		content[i] = content[i].Add(image.Pt(50, 40))
	}
	checkRects(t, "offset bounds", got, content, 0, 0.93)

	// JPEG artefacts around the gutters.
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	img, format, err := DecodeImage(buf.Bytes())
	if err != nil || format != "jpeg" {
		t.Fatalf("decode: %v %s", err, format)
	}
	got, err = GridRects(img, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	_, content = s.panels()
	checkRects(t, "jpeg", got, content, 1, 0.92)
	for i, r := range got {
		if c := avgColor(img, r); !closeRGB(c, [3]int{int(palette[i].R), int(palette[i].G), int(palette[i].B)}, 8) {
			t.Errorf("jpeg panel %d colour %v, want %v", i+1, c, palette[i])
		}
	}
}

func TestGridPanelErrors(t *testing.T) {
	img := sheet{w: 90, h: 60, rows: 3, cols: 3, gutter: 3}.draw()
	if _, err := GridPanel(img, 3, 3, 9); err == nil {
		t.Fatal("index out of range")
	}
	if _, err := GridPanel(img, 3, 3, -1); err == nil {
		t.Fatal("negative index")
	}
	if _, err := GridRects(img, 0, 3); err == nil {
		t.Fatal("zero rows")
	}
	if _, err := GridRects(image.NewRGBA(image.Rect(0, 0, 20, 20)), 3, 3); err == nil {
		t.Fatal("too small")
	}
	p, err := GridPanel(img, 3, 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	if c := avgColor(p, p.Bounds()); !closeRGB(c, [3]int{150, 50, 170}, 1) {
		t.Fatalf("centre panel colour %v", c)
	}
	if _, _, err := DecodeImage([]byte("not an image")); err == nil {
		t.Fatal("garbage decoded")
	}
}

func TestCropGridPanelAndSplitGrid(t *testing.T) {
	dir := t.TempDir()
	s := sheet{w: 480, h: 270, rows: 3, cols: 3, margin: 8, gutter: 8, outline: 2, jx: []int{4, -4}}
	var buf bytes.Buffer
	if err := png.Encode(&buf, s.draw()); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "board sheet.png")
	if err := os.WriteFile(src, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	_, content := s.panels()

	out := filepath.Join(dir, "frames", "first.jpg")
	if err := CropGridPanel(src, 3, 3, 0, out); err != nil {
		t.Fatal(err)
	}
	fh, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(fh)
	fh.Close()
	if err != nil {
		t.Fatalf("panel is not a JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() < content[0].Dx()*9/10 || b.Dx() > content[0].Dx() || b.Dy() < content[0].Dy()*9/10 || b.Dy() > content[0].Dy() {
		t.Fatalf("panel 1 size %v, content %v", b, content[0])
	}
	if c := avgColor(img, img.Bounds()); !closeRGB(c, [3]int{220, 40, 40}, 6) {
		t.Fatalf("panel 1 colour %v", c)
	}
	if _, err := os.Stat(out + ".part"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}

	paths, err := SplitGrid(src, 3, 3, filepath.Join(dir, "panels"), "board")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 9 {
		t.Fatalf("paths %v", paths)
	}
	for i, p := range paths {
		if !strings.HasSuffix(filepath.ToSlash(p), fmt.Sprintf("panels/board-%d.jpg", i+1)) {
			t.Fatalf("path %d = %s", i, p)
		}
		if c := jpegColor(t, p); !closeRGB(c, [3]int{int(palette[i].R), int(palette[i].G), int(palette[i].B)}, 6) {
			t.Fatalf("panel %d colour %v, want %v", i+1, c, palette[i])
		}
	}
	if err := CropGridPanel(filepath.Join(dir, "missing.png"), 3, 3, 0, out); err == nil {
		t.Fatal("missing source")
	}
	if err := CropGridPanel(src, 3, 3, 12, out); err == nil {
		t.Fatal("bad index")
	}
}
