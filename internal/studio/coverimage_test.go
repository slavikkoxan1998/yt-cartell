package studio

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetCoverImage(t *testing.T) {
	env := newEnv(t, nil)
	e := env.e
	p, err := e.Create("Своя обкладинка")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "pic.png")
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := e.SetCoverImage(p.ID, filepath.Join(t.TempDir(), "missing.png")); err == nil {
		t.Fatal("a missing file must be refused")
	}
	notImage := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(notImage, []byte("hello"), 0o644)
	if err := e.SetCoverImage(p.ID, notImage); err == nil {
		t.Fatal("a text file must be refused")
	}
	must(t, e.SetCoverImage(p.ID, src))
	q, _ := e.Get(p.ID)
	c, f2 := q.Montage.Cover, q.Montage.CoverFinal
	if c.Status != StatusDone || !strings.HasPrefix(c.Path, "images/cover-") || c.UpdatedAt == 0 {
		t.Fatalf("cover %+v", c)
	}
	if f2.Status != StatusDone || !strings.HasPrefix(f2.Path, "images/cover-final-") || CoverFinalOutdated(q) {
		t.Fatalf("final cover %+v", f2)
	}
	fileExists(t, e.Abs(p.ID, c.Path))

	// A second picture replaces the first (the old one goes to the trash).
	old := c.Path
	must(t, e.SetCoverImage(p.ID, src))
	q, _ = e.Get(p.ID)
	if q.Montage.Cover.Path == old {
		t.Fatal("cover not replaced")
	}
}
