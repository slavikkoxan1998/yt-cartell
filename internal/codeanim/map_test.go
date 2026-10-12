package codeanim

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// a map spec on a fixed view (no drift) with the given data and cues
func mapStillSpec(w, h int, data, cues string) []byte {
	return fmt.Appendf(nil, `{"grammar":"map","duration":1,"width":%d,"height":%d,"data":{"view":{"lon":-90,"lat":35,"zoom":8},"drift":0%s},"cues":[%s]}`,
		w, h, data, cues)
}

func diffPixels(a, b *image.NRGBA) int {
	n := 0
	for i := 0; i+3 < len(a.Pix) && i+3 < len(b.Pix); i += 4 {
		d := math.Abs(float64(a.Pix[i])-float64(b.Pix[i])) + math.Abs(float64(a.Pix[i+1])-float64(b.Pix[i+1])) + math.Abs(float64(a.Pix[i+2])-float64(b.Pix[i+2]))
		if d > 60 {
			n++
		}
	}
	return n
}

// Ukrainian labels read as Cyrillic in every label style. A true Cyrillic
// italic draws п і т as n i m — «Міссісіпі» read «Miccicini» — so italic
// Cyrillic is drawn oblique (the upright glyphs slanted): "піт" must not look
// like Latin "nim" in any style (regular, italic, bold).
func TestMapCyrillicLabelsAllStyles(t *testing.T) {
	needBrowser(t)
	styles := []string{`"style":"river"`, `"style":"water"`, `"style":"land","italic":true`, `"style":"land"`, `"style":"country"`, `"style":"note"`, `"style":"title"`}
	for _, st := range styles {
		still := func(text string) *image.NRGBA {
			spec := mapStillSpec(540, 960, `,"labels":[{"text":"`+text+`","lon":-90,"lat":35,"size":90,`+st+`}]`, "")
			img, err := stillImage(context.Background(), spec, 0, Options{})
			if err != nil {
				t.Fatal(err)
			}
			return img
		}
		cyr, lat := still("піт піт"), still("nim nim")
		if n := diffPixels(cyr, lat); n < 400 {
			t.Errorf("%s: Cyrillic «піт» looks like Latin «nim» (%d differing pixels)", st, n)
		}
	}
}

// writeFigure writes a w×h transparent PNG with an opaque red figure inside
// margins (top, bottom, side px) — like generated sprites with empty space.
func writeFigure(t *testing.T, path string, w, h, top, bottom, side int) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := top; y < h-bottom; y++ {
		for x := side; x < w-side; x++ {
			img.SetNRGBA(x, y, color.NRGBA{230, 0, 0, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func redBox(img *image.NRGBA) (x0, y0, x1, y1, n int) {
	b := img.Bounds()
	x0, y0, x1, y1 = b.Max.X, b.Max.Y, -1, -1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			p := img.Pix[img.PixOffset(x, y):]
			if p[0] > 200 && p[1] < 40 && p[2] < 40 {
				n++
				x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
			}
		}
	}
	return
}

// Sprites are big by default: the figure itself (transparent margins
// trimmed) is 0.37 of the short side in portrait ≈ 20 % of the frame height,
// 0.32 in landscape; its feet stay on the geographic point.
func TestMapSpriteDefaultSize(t *testing.T) {
	needBrowser(t)
	dir := t.TempDir()
	writeFigure(t, filepath.Join(dir, "fig.png"), 200, 400, 80, 60, 50) // the figure is 260 of 400 px
	for _, c := range []struct {
		w, h  int
		scale float64
	}{{540, 960, 0.37}, {960, 540, 0.32}} {
		spec := mapStillSpec(c.w, c.h, `,"sprites":[{"id":"f","lon":-90,"lat":35,"anim":"none","image":"fig.png"}]`, "")
		img, err := stillImage(context.Background(), spec, 0, Options{AssetsDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		_, y0, _, y1, n := redBox(img)
		want := c.scale * float64(min(c.w, c.h))
		if got := float64(y1 - y0 + 1); n == 0 || math.Abs(got-want) > want*0.04 {
			t.Fatalf("%dx%d: figure %v px high, want %.0f (%.0f %% of the frame height)", c.w, c.h, got, want, want/float64(c.h)*100)
		}
		// the feet: the view centre (the point) is in the safe box's middle
		if c.h > c.w {
			mid := float64(c.h) * 0.7 / 2
			if math.Abs(float64(y1)-mid) > 6 {
				t.Fatalf("feet at y=%d, the point is at %.0f", y1, mid)
			}
		}
	}
}

// A label a sprite would cover glides aside; the sprite stays on its point.
func TestMapLabelStepsAsideFromSprite(t *testing.T) {
	needBrowser(t)
	dir := t.TempDir()
	writeFigure(t, filepath.Join(dir, "fig.png"), 200, 400, 0, 0, 0)
	label := `"labels":[{"text":"Луїзіана","lon":-90,"lat":36.5,"style":"land","color":"#00ff00","size":44}]`
	green := func(withSprite bool) int {
		data := "," + label
		if withSprite {
			data += `,"sprites":[{"id":"f","lon":-90,"lat":35,"anim":"none","image":"fig.png"}]`
		}
		img, err := stillImage(context.Background(), mapStillSpec(540, 960, data, ""), 0, Options{AssetsDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for i := 0; i < len(img.Pix); i += 4 {
			if img.Pix[i] < 90 && img.Pix[i+1] > 190 && img.Pix[i+2] < 90 {
				n++
			}
		}
		return n
	}
	alone, beside := green(false), green(true)
	if alone < 150 || float64(beside) < float64(alone)*0.8 {
		t.Fatalf("label pixels: %d alone, %d next to the sprite (covered)", alone, beside)
	}
}

// camera view {zoomBy}: relative to the current zoom (a push-in on the spot).
func TestMapCameraZoomBy(t *testing.T) {
	needBrowser(t)
	a, err := stillImage(context.Background(), []byte(`{"grammar":"map","duration":1,"width":270,"height":480,"data":{"view":{"lon":-90,"lat":35,"zoom":6},"drift":0},
"cues":[{"at":0,"kind":"camera","view":{"zoomBy":2},"dur":0.4,"arc":false}]}`), 0.8, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := stillImage(context.Background(), []byte(`{"grammar":"map","duration":1,"width":270,"height":480,"data":{"view":{"lon":-90,"lat":35,"zoom":12},"drift":0},"cues":[]}`), 0.8, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := diffPixels(a, b); n > len(a.Pix)/4/200 {
		t.Fatalf("zoomBy 2 from zoom 6 differs from zoom 12 in %d pixels", n)
	}
	c, _ := stillImage(context.Background(), []byte(`{"grammar":"map","duration":1,"width":270,"height":480,"data":{"view":{"lon":-90,"lat":35,"zoom":6},"drift":0},"cues":[]}`), 0.8, Options{})
	if n := diffPixels(a, c); n < len(a.Pix)/4/20 {
		t.Fatalf("zoomBy did not move the camera (%d pixels differ)", n)
	}
}
