package studio

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"ytcartell/internal/assets"
	"ytcartell/internal/media"
)

// overlayFilm renders a two-scene motion-graphics film (stills with
// on-screen text) into dir and returns the montage result.
func overlayFilm(t *testing.T, dir string) media.MontageResult {
	t.Helper()
	needMedia(t)
	p := Project{Input: DefaultInput(), Montage: DefaultMontage()}
	p.Input.Content, p.Input.Style = ContentMotion, "motion-graphics"
	p.Montage.Transition, p.Montage.Subtitles = "fade", "none"
	img, err := jpeg.Decode(bytes.NewReader(assets.StylePreview("infographic")))
	if err != nil {
		t.Fatal(err)
	}
	must(t, os.MkdirAll(filepath.Join(dir, "images"), 0o755))
	var buf bytes.Buffer
	must(t, jpeg.Encode(&buf, img, nil))
	must(t, os.WriteFile(filepath.Join(dir, "images", "f.jpg"), buf.Bytes(), 0o644))
	p.Scenes = []Scene{
		{ID: "s1", Frame: Asset{Status: StatusDone, Path: "images/f.jpg"}, OnScreen: OnScreen{Title: "Населення Землі", Value: "8,1 млрд", Caption: "станом на 2025 рік", Anim: OverlayCount}},
		{ID: "s2", Frame: Asset{Status: StatusDone, Path: "images/f.jpg"}, OnScreen: OnScreen{Title: "Говерла", Value: "2061 м", Caption: "найвища вершина України", Position: "lower", Anim: OverlaySlide}},
	}
	opts := montageOptions(&p, dir)
	if opts.Overlay == nil {
		t.Fatal("no overlay renderer")
	}
	res, err := testFF.Montage(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The montage draws the on-screen text over the scenes: the frame with the
// text differs from the plain picture where the text is.
func TestMontageOverlayText(t *testing.T) {
	dir := t.TempDir()
	res := overlayFilm(t, dir)
	if res.Duration < 15 || res.Duration > 17 {
		t.Fatalf("duration %.2f", res.Duration)
	}
	at := func(sec float64) image.Image {
		out := filepath.Join(dir, "probe.png")
		if err := testFF.Run(context.Background(), "", "-ss", "4", "-i", res.Output, "-frames:v", "1", out); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(out)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		img, _, err := image.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		_ = sec
		return img
	}
	img := at(4)
	// The hero number sits in the middle of the frame: bright white pixels there.
	b := img.Bounds()
	white := 0
	for y := b.Dy() * 2 / 5; y < b.Dy()*3/5; y++ {
		for x := b.Dx() / 4; x < b.Dx()*3/4; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r > 0xF000 && g > 0xF000 && bl > 0xF000 {
				white++
			}
		}
	}
	if white < 500 {
		t.Fatalf("no text in the middle of the frame (%d white px)", white)
	}
}

// With OVERLAY_SAMPLE=<dir>: frames of the sample film for review.
func TestOverlaySampleFrames(t *testing.T) {
	out := os.Getenv("OVERLAY_SAMPLE")
	if out == "" {
		t.Skip("set OVERLAY_SAMPLE=<dir> to write montage overlay frames")
	}
	dir := t.TempDir()
	res := overlayFilm(t, dir)
	for _, sec := range []string{"0.7", "1.2", "4", "8.9", "12"} {
		if err := testFF.Run(context.Background(), "", "-ss", sec, "-i", res.Output, "-frames:v", "1", filepath.Join(out, "montage-overlay-"+sec+"s.png")); err != nil {
			t.Fatal(err)
		}
	}
}
