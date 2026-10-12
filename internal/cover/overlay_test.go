package cover

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"testing"
)

func TestParseCount(t *testing.T) {
	for in, want := range map[string]string{
		"73%":         "73%",
		"1 200 км":    "1 200 км",
		"$4.5 млрд":   "$4.5 млрд",
		"1,200,000":   "1,200,000",
		"1914":        "1914",
		"−12°":        "−12°",
		"-3,75 °C":    "−3,75 °C",
		"х2":          "х2",
		"12 000 000+": "12 000 000+",
	} {
		n, ok := parseCount(in)
		if !ok {
			t.Errorf("%q: not a number", in)
			continue
		}
		if got := n.format(n.value); got != want {
			t.Errorf("%q: format %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "сто", "2019–2024", "1.2.3"} {
		if _, ok := parseCount(bad); ok {
			t.Errorf("%q counted", bad)
		}
	}
	n, _ := parseCount("1 200 км")
	if got := n.format(600); got != "600 км" {
		t.Errorf("half way: %q", got)
	}
	if got := n.format(1000); got != "1 000 км" {
		t.Errorf("grouping: %q", got)
	}
}

// Every design draws inside the frame, is empty before and after its time,
// at rest in the middle and changes while it enters.
func TestOverlayFrames(t *testing.T) {
	designs := []Overlay{
		{Title: "Населення Землі", Value: "8,1 млрд", Caption: "станом на 2025 рік", Anim: AnimCount},
		{Title: "Як працює фотосинтез", Caption: "світло + вода + CO₂ → цукор", Anim: AnimType},
		{Title: "Карпати", Caption: "найвища точка — Говерла", Position: "lower", Anim: AnimSlide},
		{Value: "73%", Caption: "людей не знають цього", Anim: AnimPop, Position: "top"},
		{Caption: "лише підпис"},
	}
	for _, size := range []image.Point{{1280, 720}, {1080, 1920}} {
		for i, o := range designs {
			c, err := NewOverlayClip(o, size, 0.4, 7.5)
			if err != nil {
				t.Fatal(err)
			}
			b := c.Bounds()
			if b.Empty() || !b.In(image.Rectangle{Max: size}) {
				t.Fatalf("%d %v: bounds %v", i, size, b)
			}
			if st, id := c.Still(0.2); !st || id != 0 {
				t.Errorf("%d: before show still=%v id=%d", i, st, id)
			}
			if st, id := c.Still(5); !st || id != 1 {
				t.Errorf("%d: at rest still=%v id=%d", i, st, id)
			}
			if st, _ := c.Still(0.6); st {
				t.Errorf("%d: entering frame reported still", i)
			}
			if ink(c.Frame(0.2)) != 0 || ink(c.Frame(7.6)) != 0 {
				t.Errorf("%d: ink outside the time on screen", i)
			}
			rest := ink(c.Frame(5))
			if rest < 500 {
				t.Errorf("%d: almost nothing drawn at rest (%d px)", i, rest)
			}
			if mid := ink(c.Frame(0.55)); mid >= rest {
				t.Errorf("%d: entering frame has as much ink (%d) as the rest (%d)", i, mid, rest)
			}
		}
	}
	if _, err := NewOverlayClip(Overlay{}, image.Pt(1280, 720), 0, 5); err == nil {
		t.Error("empty overlay accepted")
	}
}

// A short scene squeezes the entrance but keeps the text at rest a while.
func TestOverlayShortScene(t *testing.T) {
	c, err := NewOverlayClip(Overlay{Title: "Дуже коротко", Value: "100%", Caption: "і все"}, image.Pt(1280, 720), 0.2, 2.2)
	if err != nil {
		t.Fatal(err)
	}
	if st, id := c.Still(1.6); !st || id != 1 {
		t.Errorf("no rest in a short scene (inEnd %.2f)", c.inEnd)
	}
}

func ink(img *image.RGBA) int {
	n := 0
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 32 {
			n++
		}
	}
	return n
}

// With COVER_SAMPLES=<dir>: contact sheets of the designs at rest and while
// they enter, over a synthetic picture.
func TestSampleOverlays(t *testing.T) {
	dir := os.Getenv("COVER_SAMPLES")
	if dir == "" {
		t.Skip()
	}
	designs := map[string]Overlay{
		"count":  {Title: "Населення Землі", Value: "8,1 млрд", Caption: "станом на 2025 рік", Anim: AnimCount},
		"type":   {Title: "Як працює фотосинтез", Caption: "світло, вода й вуглекислий газ", Anim: AnimType},
		"lower":  {Title: "Карпати", Caption: "найвища точка — Говерла, 2061 м", Position: "lower", Anim: AnimSlide},
		"lowerv": {Title: "Говерла", Value: "2061 м", Caption: "найвища вершина України", Position: "lower", Anim: AnimCount},
		"pop":    {Value: "73%", Title: "Опитування", Caption: "людей не знають цього факту", Anim: AnimPop, Position: "top"},
	}
	for name, o := range designs {
		for _, vertical := range []bool{false, true} {
			size, tag := Landscape, "169"
			if vertical {
				size, tag = image.Pt(1080, 1920), "916"
			}
			c, err := NewOverlayClip(o, size, 0.3, 6)
			if err != nil {
				t.Fatal(err)
			}
			base := CoverFit(baseFor(t, vertical), size)
			for _, at := range []float64{0.55, 0.8, 3} {
				canvas := image.NewRGBA(base.Rect)
				copy(canvas.Pix, base.Pix)
				f := c.Frame(at)
				draw.Draw(canvas, c.Bounds(), f, image.Point{}, draw.Over)
				writeJPEG(t, filepath.Join(dir, fmt.Sprintf("overlay-%s-%s-%03.0f.jpg", name, tag, at*100)), canvas)
			}
		}
	}
}
