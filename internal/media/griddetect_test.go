package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func jpegRoundTrip(t *testing.T, img image.Image) image.Image {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	out, _, err := DecodeImage(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestDetectGridClean: strict 3×3 sheets of equal panels — vertical 9:16 and
// horizontal 16:9, white gutters of various widths, margins, outlines, JPEG
// noise and black gutters — are found as clean 3×3 grids whose panels lie
// inside the drawn panels.
func TestDetectGridClean(t *testing.T) {
	cases := []struct {
		name string
		s    sheet
		jpg  bool
	}{
		{"9x16 gutter 8", sheet{w: 540, h: 960, rows: 3, cols: 3, gutter: 8}, false},
		{"9x16 gutter 1", sheet{w: 540, h: 960, rows: 3, cols: 3, gutter: 1}, false},
		{"9x16 gutter 3 jpeg noise", sheet{w: 540, h: 960, rows: 3, cols: 3, gutter: 3, fill: noise(7)}, true},
		{"9x16 wide gutter margin outline", sheet{w: 576, h: 1024, rows: 3, cols: 3, gutter: 30, margin: 20, outline: 2}, false},
		{"16x9 gutter 4 margin 12", sheet{w: 960, h: 540, rows: 3, cols: 3, gutter: 4, margin: 12, outline: 2}, false},
		{"16x9 gutter 12 jpeg", sheet{w: 1280, h: 720, rows: 3, cols: 3, gutter: 12, fill: noise(3)}, true},
		{"16x9 small jitter", sheet{w: 960, h: 540, rows: 3, cols: 3, gutter: 6, jx: []int{8, -6}, jy: []int{4}}, false},
		{"9x16 black gutters", sheet{w: 540, h: 960, rows: 3, cols: 3, gutter: 10, bg: color.RGBA{0, 0, 0, 255}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var img image.Image = tc.s.draw()
			if tc.jpg {
				img = jpegRoundTrip(t, img)
			}
			g := DetectGrid(img)
			if !g.Clean(3, 3) {
				t.Fatalf("got %d×%d even=%v, want a clean 3×3", g.Rows, g.Cols, g.Even)
			}
			_, content := tc.s.panels()
			checkRects(t, tc.name, g.Rects, content, 3, 0.9)
		})
	}
}

// TestDetectGridRejectsOtherLayouts: what the model drew instead of a 3×3
// — two columns of five, unequal panels, rows with their own column splits,
// a plain picture — is never reported as a clean 3×3.
func TestDetectGridRejectsOtherLayouts(t *testing.T) {
	t.Run("2 cols x 5 rows (9:16)", func(t *testing.T) {
		g := DetectGrid(sheet{w: 540, h: 960, rows: 5, cols: 2, gutter: 8}.draw())
		if g.Rows != 5 || g.Cols != 2 || g.Clean(3, 3) {
			t.Fatalf("got %d×%d even=%v, want 5 rows × 2 cols", g.Rows, g.Cols, g.Even)
		}
	})
	t.Run("uneven panels", func(t *testing.T) {
		g := DetectGrid(sheet{w: 540, h: 960, rows: 3, cols: 3, gutter: 8, jy: []int{120}, jx: []int{-50}}.draw())
		if g.Rows != 3 || g.Cols != 3 || g.Even || g.Clean(3, 3) {
			t.Fatalf("got %d×%d even=%v, want an uneven 3×3", g.Rows, g.Cols, g.Even)
		}
	})
	t.Run("rows with their own columns", func(t *testing.T) {
		// Row 1: three panels; rows 2–3: two panels — no column gutter
		// crosses the whole sheet.
		img := image.NewRGBA(image.Rect(0, 0, 540, 960))
		for i := range img.Pix {
			img.Pix[i] = 255
		}
		fill := func(r image.Rectangle, c color.RGBA) {
			for y := r.Min.Y; y < r.Max.Y; y++ {
				for x := r.Min.X; x < r.Max.X; x++ {
					img.SetRGBA(x, y, c)
				}
			}
		}
		fill(image.Rect(0, 0, 176, 316), palette[0])
		fill(image.Rect(182, 0, 358, 316), palette[1])
		fill(image.Rect(364, 0, 540, 316), palette[2])
		fill(image.Rect(0, 322, 267, 638), palette[3])
		fill(image.Rect(273, 322, 540, 638), palette[4])
		fill(image.Rect(0, 644, 267, 960), palette[5])
		fill(image.Rect(273, 644, 540, 960), palette[6])
		g := DetectGrid(img)
		if g.Clean(3, 3) || g.Cols != 1 {
			t.Fatalf("got %d×%d even=%v, want no whole-sheet column gutters", g.Rows, g.Cols, g.Even)
		}
	})
	t.Run("plain picture", func(t *testing.T) {
		img := sheet{w: 540, h: 960, rows: 1, cols: 1, fill: noise(11)}.draw()
		g := DetectGrid(img)
		if g.Rows != 1 || g.Cols != 1 || len(g.Rects) != 1 {
			t.Fatalf("got %d×%d, want 1×1", g.Rows, g.Cols)
		}
	})
}

// TestFitAspect: a panel is cropped (never stretched) to the frame format.
func TestFitAspect(t *testing.T) {
	cases := []struct {
		r      image.Rectangle
		aw, ah int
		want   image.Rectangle
	}{
		{image.Rect(0, 0, 180, 300), 9, 16, image.Rect(5, 0, 174, 300)},     // too wide → sides trimmed
		{image.Rect(10, 10, 190, 400), 9, 16, image.Rect(10, 45, 190, 365)}, // too tall → top/bottom trimmed
		{image.Rect(0, 0, 320, 180), 16, 9, image.Rect(0, 0, 320, 180)},     // exact
	}
	for _, tc := range cases {
		got := FitAspect(tc.r, tc.aw, tc.ah)
		if got != tc.want {
			t.Errorf("FitAspect(%v, %d:%d) = %v, want %v", tc.r, tc.aw, tc.ah, got, tc.want)
		}
		if !got.In(tc.r) {
			t.Errorf("FitAspect(%v) = %v leaves the panel", tc.r, got)
		}
		if a := float64(got.Dx()) / float64(got.Dy()); a < float64(tc.aw)/float64(tc.ah)*0.98 || a > float64(tc.aw)/float64(tc.ah)*1.02 {
			t.Errorf("FitAspect(%v) = %v has aspect %.3f", tc.r, got, a)
		}
	}
}
