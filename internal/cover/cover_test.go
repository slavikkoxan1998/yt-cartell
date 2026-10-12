package cover

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
)

// Set COVER_SAMPLES=<dir> to write every rendered sample there (JPEG, plus
// a 320 px "phone feed" copy); COVER_BASE_169 / COVER_BASE_916 pick real
// pictures as the background instead of the synthetic one.

const longTitle = "Він світився до останнього і врятував хлопчика в лісі"

var titles = []string{
	longTitle,
	"Ґава, їжак і *єнот*: неймовірна історія",
	"ВРЯТУВАВ!",
}

// synthetic is a busy, high-contrast background (worst case for legibility).
func synthetic(size image.Point, seed int64) image.Image {
	img := image.NewRGBA(image.Rectangle{Max: size})
	rnd := rand.New(rand.NewSource(seed))
	for y := 0; y < size.Y; y++ {
		for x := 0; x < size.X; x++ {
			fx, fy := float64(x)/float64(size.X), float64(y)/float64(size.Y)
			r := 120 + 100*math.Sin(fx*9+fy*3)
			g := 110 + 90*math.Sin(fy*7-fx*2)
			b := 140 + 90*math.Cos(fx*5+fy*11)
			n := rnd.Float64()*40 - 20
			img.Set(x, y, color.RGBA{clamp8(r + n), clamp8(g + n), clamp8(b + n), 255})
		}
	}
	return img
}

func clamp8(v float64) uint8 { return uint8(math.Max(0, math.Min(255, v))) }

func baseFor(t *testing.T, vertical bool) image.Image {
	env := "COVER_BASE_169"
	size := Landscape
	if vertical {
		env, size = "COVER_BASE_916", Portrait
	}
	if p := os.Getenv(env); p != "" {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		img, _, err := image.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	return synthetic(size, 7)
}

func samplesDir(t *testing.T) string {
	if d := os.Getenv("COVER_SAMPLES"); d != "" {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		return d
	}
	return ""
}

func writeJPEG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := EncodeJPEG(f, img); err != nil {
		t.Fatal(err)
	}
}

func small(img image.Image, w int) image.Image {
	b := img.Bounds()
	h := w * b.Dy() / b.Dx()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Rect, img, b, xdraw.Src, nil)
	return dst
}

// inkOutside counts ink pixels (alpha > 24) outside the safe area or inside
// an avoided corner (1 px of tolerance for anti-aliasing).
func inkOutside(m *mask, safe image.Rectangle, avoid []image.Rectangle) int {
	safe = safe.Inset(-1)
	bad := 0
	w := m.r.Dx()
	for i, v := range m.pix {
		if v <= 24 {
			continue
		}
		p := image.Pt(m.r.Min.X+i%w, m.r.Min.Y+i/w)
		if !p.In(safe) {
			bad++
			continue
		}
		for _, a := range avoid {
			if p.In(a.Inset(1)) {
				bad++
				break
			}
		}
	}
	return bad
}

func TestTemplatesRenderInsideSafeArea(t *testing.T) {
	dir := samplesDir(t)
	for _, vertical := range []bool{false, true} {
		base := baseFor(t, vertical)
		size := Landscape
		tag := "169"
		if vertical {
			size, tag = Portrait, "916"
		}
		canvasBase := CoverFit(base, size)
		safe, avoid := SafeArea(size)
		for _, tpl := range Templates {
			for ti, title := range titles {
				if ti == 2 && tpl.ID != "bottom-bold" && tpl.ID != "yellow-shock" {
					continue
				}
				spec := ResolveSpec(Text{
					Template: tpl.ID,
					Title:    title,
					Subtitle: "Правдива історія з Карпат",
					Badge:    "Нове",
				})
				var inks []*mask
				var kinds []string
				testHook = func(kind string, m *mask) {
					inks = append(inks, m.clone())
					kinds = append(kinds, kind)
				}
				canvas := image.NewRGBA(canvasBase.Rect)
				copy(canvas.Pix, canvasBase.Pix)
				g, err := drawSpec(canvas, spec)
				testHook = nil
				if err != nil {
					t.Fatalf("%s: %v", tpl.ID, err)
				}
				name := fmt.Sprintf("%s-%s-%d", tag, tpl.ID, ti)
				if len(inks) < 3 {
					t.Errorf("%s: %d text layers painted (%v), want title, subtitle and badge", name, len(inks), kinds)
				}
				for i, m := range inks {
					if n := inkOutside(m, safe, avoid); n > 0 {
						t.Errorf("%s: %d %s pixels outside the safe area", name, n, kinds[i])
					}
				}
				if !g.fits {
					t.Errorf("%s: layout does not fit", name)
				}
				maxLines := MaxLines
				if vertical {
					maxLines = MaxLinesShorts
				}
				if g.title == nil || len(g.title.lines) == 0 || len(g.title.lines) > maxLines {
					t.Errorf("%s: title lines = %v", name, g.title)
				}
				if min := 0.035 * float32(min(size.X, size.Y)); g.s < min && ti == 0 {
					t.Errorf("%s: title size %.1f px is too small (< %.1f)", name, g.s, min)
				}
				if dir != "" {
					writeJPEG(t, filepath.Join(dir, name+".jpg"), canvas)
					w := 320
					if vertical {
						w = 180
					}
					writeJPEG(t, filepath.Join(dir, name+"-small.jpg"), small(canvas, w))
				}
			}
		}
	}
}

func TestResolveSpec(t *testing.T) {
	s := ResolveSpec(Text{})
	if s.Template != Templates[0].ID || s.Font != Templates[0].Font || s.Color != Templates[0].Color {
		t.Fatalf("defaults not applied: %+v", s)
	}
	s = ResolveSpec(Text{Template: "yellow-shock", Font: "oswald", Color: "#abc", Stroke: "none", Accent: "zzz", Position: "LEFT", Size: 9999})
	if s.Font != "oswald" || s.Color != "#AABBCC" || s.Stroke != "" || s.Accent != "#E5162B" || s.Position != "left" || s.Size != maxSize {
		t.Fatalf("overrides: %+v", s)
	}
	s = ResolveSpec(Text{Template: "nope", Font: "nope", Position: "middle", Stroke: "#123456"})
	if s.Template != Templates[0].ID || s.Font != Templates[0].Font || s.Position != Templates[0].Position || s.Stroke != "#123456" {
		t.Fatalf("fallbacks: %+v", s)
	}
	s = ResolveSpec(Text{Template: "minimal", Stroke: "#000000"})
	if s.Look.StrokeWidth <= 0 {
		t.Fatalf("user outline on a template without one needs a width")
	}
	s = ResolveSpec(Text{Title: "  раз \n\n два\nтри\nчотири\n п'ять  шість ", Badge: strings.Repeat("Б", 40)})
	if s.Title != "раз\nдва\nтри\nчотири п'ять шість" {
		t.Fatalf("title cleanup: %q", s.Title)
	}
	if len([]rune(s.Badge)) != MaxBadge {
		t.Fatalf("badge not clipped: %d", len([]rune(s.Badge)))
	}
}

func TestCatalogs(t *testing.T) {
	if len(Templates) < 8 {
		t.Fatalf("%d templates, want ≥ 8", len(Templates))
	}
	seen := map[string]bool{}
	for _, tpl := range Templates {
		if tpl.ID == "" || tpl.Label == "" || tpl.Description == "" || seen[tpl.ID] {
			t.Errorf("bad template %+v", tpl)
		}
		seen[tpl.ID] = true
		if _, ok := FontByID(tpl.Font); !ok {
			t.Errorf("%s: unknown font %s", tpl.ID, tpl.Font)
		}
		for _, c := range []string{tpl.Color, tpl.Accent} {
			if _, ok := normHex(c); !ok {
				t.Errorf("%s: bad colour %q", tpl.ID, c)
			}
		}
		if !knownPosition(tpl.Position) {
			t.Errorf("%s: bad position %q", tpl.ID, tpl.Position)
		}
	}
	if _, ok := TemplateByID(DefaultTemplate(true)); !ok {
		t.Error("no default Shorts template")
	}
	if len(Fonts) < 5 {
		t.Fatalf("%d fonts, want ≥ 5", len(Fonts))
	}
	for _, f := range Fonts {
		fc, err := loadFace(f.ID)
		if err != nil {
			t.Fatalf("%s: %v", f.ID, err)
		}
		if fc.def.ID != f.ID || f.Label == "" || f.Name == "" {
			t.Errorf("font %+v", f)
		}
		// Full Ukrainian alphabet + apostrophes, drawn by the face itself
		// (only Russo One lacks ʼ and borrows ’).
		for _, r := range "АБВГҐДЕЄЖЗИІЇЙКЛМНОПРСТУФХЦЧШЩЬЮЯабвгґдеєжзиіїйклмнопрстуфхцчшщьюя’" {
			if fc.lookup(r) == nil {
				t.Errorf("%s lacks %q", f.ID, r)
			}
		}
		if m := Missing("Слава Україні ʼ«»—…№₴", f.ID); len(m) > 0 {
			t.Errorf("%s: cannot draw %q", f.ID, string(m))
		}
		if fc.capH < 0.5 || fc.capH > 0.9 {
			t.Errorf("%s: cap height %.3f", f.ID, fc.capH)
		}
	}
	if m := Missing("Hi 😀 你", ""); string(m) != "😀你" {
		t.Errorf("Missing = %q", string(m))
	}
}

func TestKerningCyrillic(t *testing.T) {
	fc, err := loadFace("montserrat")
	if err != nil {
		t.Fatal(err)
	}
	pairs := [][2]rune{{'A', 'V'}, {'Т', 'А'}, {'Г', 'А'}}
	for _, p := range pairs {
		if k := fc.kern(fc.lookup(p[0]), fc.lookup(p[1])); k >= 0 {
			t.Errorf("no kerning for %c%c (%.3f em)", p[0], p[1], k)
		}
	}
}

func TestTokenizeAndGlue(t *testing.T) {
	toks := tokenize("Він *світився* до\nостаннього 5*")
	var words []string
	var hl []bool
	for _, tk := range toks {
		words = append(words, string(tk.runes))
		hl = append(hl, tk.hl[0])
	}
	if strings.Join(words, "|") != "Він|світився|до|останнього|5*" {
		t.Fatalf("words %q", words)
	}
	if hl[0] || !hl[1] || hl[2] {
		t.Fatalf("highlight flags %v", hl)
	}
	if !toks[2].brk {
		t.Fatalf("forced break lost")
	}
	units := glue(tokenize("Він і врятував хлопчика в лісі — назавжди 10 років"))
	var got []string
	for _, u := range units {
		var ws []string
		for _, tk := range u {
			ws = append(ws, string(tk.runes))
		}
		got = append(got, strings.Join(ws, " "))
	}
	want := "Він|і врятував|хлопчика|в лісі —|назавжди|10 років"
	if strings.Join(got, "|") != want {
		t.Fatalf("glue = %q, want %q", strings.Join(got, "|"), want)
	}
}

func TestBalancedLines(t *testing.T) {
	fc, _ := loadFace("montserrat")
	sh := newShaper(fc, 0, true)
	for _, text := range []string{longTitle, "Ґава, їжак і єнот: неймовірна історія", "Раз два три чотири пʼять шість сім"} {
		units := sh.shapeText(text)
		n := len(units)
		for k := 1; k <= 3; k++ {
			r := balanced(sh, units, k)
			if len(r) != min(k, n) || r[0][0] != 0 || r[len(r)-1][1] != n {
				t.Fatalf("k=%d ranges %v", k, r)
			}
			worst := func(rs [][2]int) float32 {
				var w float32
				for _, x := range rs {
					w = max(w, sh.line(units, x[0], x[1]).w)
				}
				return w
			}
			got := worst(r)
			// Brute force: no split may have a clearly narrower widest line.
			best := got
			if k == 2 {
				for i := 1; i < n; i++ {
					best = min(best, worst([][2]int{{0, i}, {i, n}}))
				}
			}
			if k == 3 {
				for i := 1; i < n-1; i++ {
					for j := i + 1; j < n; j++ {
						best = min(best, worst([][2]int{{0, i}, {i, j}, {j, n}}))
					}
				}
			}
			if got > best*1.016 {
				t.Errorf("%q k=%d: widest line %.2f em, optimum %.2f", text, k, got, best)
			}
		}
	}
}

func TestEmptyAndEdgeCases(t *testing.T) {
	base := synthetic(image.Pt(640, 360), 1)
	for _, tx := range []Text{
		{},
		{Title: "**"},
		{Title: "Надзвичайнодовгесловобезжоднихпробілівщобперевіритистискання"},
		{Title: "😀😀😀"},
		{Subtitle: "Лише підзаголовок"},
		{Badge: "Лише бейдж"},
		{Title: "Рядок один\nрядок два\nрядок три", Position: "right"},
	} {
		img, err := Render(base, ResolveSpec(tx), image.Pt(640, 360))
		if err != nil || img.Bounds().Dx() != 640 {
			t.Fatalf("%+v: %v", tx, err)
		}
	}
	if _, err := Render(nil, ResolveSpec(Text{Title: "Без картинки"}), image.Pt(320, 180)); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(base, ResolveSpec(Text{}), image.Pt(4, 4)); err == nil {
		t.Fatal("tiny size accepted")
	}
}

func TestPreviewSpeed(t *testing.T) {
	size := image.Pt(640, 360)
	base := CoverFit(synthetic(Landscape, 3), size)
	for _, tpl := range Templates {
		spec := ResolveSpec(Text{Template: tpl.ID, Title: longTitle, Subtitle: "Правдива історія", Badge: "Нове"})
		start := time.Now()
		const n = 3
		for i := 0; i < n; i++ {
			if _, err := Render(base, spec, size); err != nil {
				t.Fatal(err)
			}
		}
		if d := time.Since(start) / n; d > 150*time.Millisecond {
			t.Errorf("%s: preview takes %v (> 150 ms)", tpl.ID, d)
		} else {
			t.Logf("%s: %v", tpl.ID, d)
		}
	}
}

func TestFullSizeJPEG(t *testing.T) {
	img, err := Render(synthetic(image.Pt(1920, 1080), 5), ResolveSpec(Text{Title: longTitle}), Landscape)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "c.jpg")
	writeJPEG(t, path, img)
	st, _ := os.Stat(path)
	if st.Size() > MaxBytes {
		t.Fatalf("jpeg %d bytes", st.Size())
	}
	f, _ := os.Open(path)
	defer f.Close()
	dec, err := jpeg.Decode(f)
	if err != nil || dec.Bounds().Size() != Landscape {
		t.Fatalf("decoded %v %v", dec.Bounds(), err)
	}
}

// The preview must match the final cover: same lines, proportional sizes.
func TestPreviewMatchesFinal(t *testing.T) {
	for _, vertical := range []bool{false, true} {
		full := Landscape
		if vertical {
			full = Portrait
		}
		for _, tpl := range Templates {
			spec := ResolveSpec(Text{Template: tpl.ID, Title: longTitle, Subtitle: "Правдива історія", Badge: "Нове"})
			a, err := Measure(spec, full)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range []int{640, 320} {
				small := image.Pt(w, full.Y*w/full.X)
				b, _ := Measure(spec, small)
				ratio := b.TitleSize / a.TitleSize * float64(full.X) / float64(w)
				if a.Lines != b.Lines || math.Abs(ratio-1) > 0.03 {
					t.Errorf("%s %v@%d: final %d lines %.1f px, preview %d lines %.1f px", tpl.ID, vertical, w, a.Lines, a.TitleSize, b.Lines, b.TitleSize)
				}
			}
		}
	}
}

func TestMeasure(t *testing.T) {
	m, err := Measure(ResolveSpec(Text{Title: "Врятував!"}), Landscape)
	if err != nil || m.Lines != 1 || m.Small || m.TitleSize < 100 {
		t.Fatalf("short title: %+v %v", m, err)
	}
	long := strings.Repeat("Дуже довгий заголовок ", 5)
	m, _ = Measure(ResolveSpec(Text{Template: "left-gradient", Title: long}), Landscape)
	if !m.Small || m.Lines != 3 {
		t.Fatalf("long title in a column: %+v", m)
	}
	m, _ = Measure(ResolveSpec(Text{Title: "Раз два", Size: 60}), Landscape)
	if math.Abs(m.TitleSize-60) > 0.5 {
		t.Fatalf("size cap: %+v", m)
	}
	m, _ = Measure(ResolveSpec(Text{Title: "Раз два", Size: 60}), image.Pt(640, 360))
	if math.Abs(m.TitleSize-30) > 0.5 {
		t.Fatalf("size cap scales with the canvas: %+v", m)
	}
	if m, _ := Measure(ResolveSpec(Text{}), Landscape); m.Lines != 0 || m.TitleSize != 0 {
		t.Fatalf("empty: %+v", m)
	}
}

// Every template in every position keeps its ink inside the safe area.
func TestAllPositionsInsideSafeArea(t *testing.T) {
	for _, size := range []image.Point{{640, 360}, {360, 640}} {
		base := CoverFit(synthetic(size, 11), size)
		safe, avoid := SafeArea(size)
		for _, tpl := range Templates {
			for _, pos := range Positions {
				for _, title := range []string{longTitle, "Ґава, їжак і *єнот*: неймовірна історія", "Так!"} {
					spec := ResolveSpec(Text{Template: tpl.ID, Position: pos.ID, Title: title, Subtitle: "Правдива історія з Карпат", Badge: "Нове"})
					var bad []string
					testHook = func(kind string, m *mask) {
						if n := inkOutside(m, safe, avoid); n > 0 {
							bad = append(bad, fmt.Sprintf("%s:%d", kind, n))
						}
					}
					c := image.NewRGBA(base.Rect)
					copy(c.Pix, base.Pix)
					g, err := drawSpec(c, spec)
					testHook = nil
					if err != nil {
						t.Fatal(err)
					}
					if len(bad) > 0 || !g.fits {
						t.Errorf("%v %s/%s %q: fits=%v outside=%v", size, tpl.ID, pos.ID, title, g.fits, bad)
					}
					for _, pr := range g.protected() {
						if !frameFor(size).safe.contains(pr) {
							t.Errorf("%v %s/%s: %v leaves the safe area", size, tpl.ID, pos.ID, pr)
						}
					}
				}
			}
		}
	}
}
