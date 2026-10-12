package codeanim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
)

var examples = []string{"t1_3b1b", "t2_keynote_ui", "t3_finance_chart", "y1_kurzgesagt", "y2_vox", "y3_whiteboard", "y4_storytime", "y5_kinetic_type"}

func TestMain(m *testing.M) {
	code := m.Run()
	Shutdown()
	os.Exit(code)
}

func needBrowser(t *testing.T) {
	t.Helper()
	if _, err := Available(context.Background()); err != nil {
		t.Skip("no browser:", err)
	}
}

func needFF(t *testing.T) media.FF {
	t.Helper()
	ff, err := media.Locate(config.Config{})
	if !ff.OK() {
		t.Skip("no ffmpeg/ffprobe:", err)
	}
	return ff
}

func exampleSpec(t *testing.T, name string, mutate func(m map[string]any)) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "examples", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if mutate == nil {
		return raw
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	mutate(m)
	b, _ := json.Marshal(m)
	return b
}

func assetsDir() string { abs, _ := filepath.Abs(filepath.Join("testdata", "examples")); return abs }

type probe struct {
	Streams []struct {
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		PixFmt    string `json:"pix_fmt"`
		NbFrames  string `json:"nb_read_frames"`
		RFrame    string `json:"r_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func ffprobe(t *testing.T, ff media.FF, p string) probe {
	t.Helper()
	out, err := exec.Command(ff.FFprobe, "-v", "error", "-count_frames", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height,pix_fmt,nb_read_frames,r_frame_rate:format=duration", "-of", "json", p).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", p, err)
	}
	var pr probe
	if err := json.Unmarshal(out, &pr); err != nil || len(pr.Streams) == 0 {
		t.Fatalf("ffprobe output: %s", out)
	}
	return pr
}

// shorten keeps the first secs of a spec (cues after it are dropped by the engine).
func shorten(secs float64) func(m map[string]any) {
	return func(m map[string]any) { m["duration"] = secs }
}

func TestExamplesRender(t *testing.T) {
	needBrowser(t)
	ff := needFF(t)
	dir := t.TempDir()
	for _, name := range examples {
		t.Run(name, func(t *testing.T) {
			spec := exampleSpec(t, name, func(m map[string]any) {
				m["duration"] = 1.5
				m["width"], m["height"] = 640, 360
			})
			out := filepath.Join(dir, name+".mp4")
			var last, total int
			start := time.Now()
			if err := Render(context.Background(), spec, out, Options{AssetsDir: assetsDir(), Progress: func(d, n int) { last, total = d, n }}); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: 45 frames 640x360 in %v", name, time.Since(start).Round(time.Millisecond))
			if last != 45 || total != 45 {
				t.Fatalf("progress %d/%d, want 45/45", last, total)
			}
			pr := ffprobe(t, ff, out)
			s := pr.Streams[0]
			if s.CodecName != "h264" || s.PixFmt != "yuv420p" || s.Width != 640 || s.Height != 360 || s.NbFrames != "45" || s.RFrame != "30/1" {
				t.Fatalf("stream %+v", s)
			}
			if d, _ := strconv.ParseFloat(pr.Format.Duration, 64); d < 1.49 || d > 1.51 {
				t.Fatalf("duration %v", pr.Format.Duration)
			}
		})
	}
}

func TestPortraitFrameCount(t *testing.T) {
	needBrowser(t)
	ff := needFF(t)
	// duration×fps not an integer: frames = round(…) (1.75×30 = 52.5 → 52: Python's round-half-even, as upstream).
	spec := exampleSpec(t, "y5_kinetic_type", func(m map[string]any) {
		m["duration"] = 1.75
		m["width"], m["height"] = 360, 640
	})
	out := filepath.Join(t.TempDir(), "p.mp4")
	if err := Render(context.Background(), spec, out, Options{}); err != nil {
		t.Fatal(err)
	}
	s := ffprobe(t, ff, out).Streams[0]
	if s.Width != 360 || s.Height != 640 || s.NbFrames != "52" {
		t.Fatalf("stream %+v", s)
	}
}

func TestDeterministic(t *testing.T) {
	needBrowser(t)
	for _, name := range []string{"t1_3b1b", "y4_storytime", "y2_vox"} {
		spec := exampleSpec(t, name, func(m map[string]any) { m["width"], m["height"] = 480, 270 })
		opt := Options{AssetsDir: assetsDir()}
		a, err := stillImage(context.Background(), spec, 3.4, opt)
		if err != nil {
			t.Fatal(err)
		}
		b, err := stillImage(context.Background(), spec, 3.4, opt)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a.Pix, b.Pix) {
			t.Fatalf("%s: two renders of the same frame differ", name)
		}
		blank := true
		for i := 0; i < len(a.Pix); i += 4 {
			if a.Pix[i] != a.Pix[0] || a.Pix[i+1] != a.Pix[1] || a.Pix[i+2] != a.Pix[2] {
				blank = false
				break
			}
		}
		if blank {
			t.Fatalf("%s: frame is a single colour", name)
		}
	}
}

// Frames rendered in parallel pages (round-robin) equal a single page's.
func TestParallelPagesMatchStill(t *testing.T) {
	needBrowser(t)
	ff := needFF(t)
	old := Concurrency()
	SetConcurrency(3)
	defer SetConcurrency(old)
	spec := exampleSpec(t, "t3_finance_chart", func(m map[string]any) { m["width"], m["height"] = 320, 180; m["duration"] = 3 })
	dir := t.TempDir()
	out := filepath.Join(dir, "a.mov")
	if err := Render(context.Background(), spec, out, Options{Alpha: true}); err != nil {
		t.Fatal(err)
	}
	// frame 50 of the ProRes (10-bit 4:4:4, near-lossless) vs Still at 50/30.
	png1 := filepath.Join(dir, "f50.png")
	if err := exec.Command(ff.FFmpeg, "-v", "error", "-i", out, "-vf", "select=eq(n\\,50)", "-frames:v", "1", "-pix_fmt", "rgba", png1).Run(); err != nil {
		t.Fatal(err)
	}
	png2 := filepath.Join(dir, "s50.png")
	if err := Still(context.Background(), spec, 50.0/30, png2, Options{Alpha: true}); err != nil {
		t.Fatal(err)
	}
	a, b := decodePNG(t, png1), decodePNG(t, png2)
	var maxd int
	for i := range a {
		d := int(a[i]) - int(b[i])
		maxd = max(maxd, d, -d)
	}
	if maxd > 6 {
		t.Fatalf("frame 50 differs from the still by up to %d", maxd)
	}
}

func decodePNG(t *testing.T, p string) []byte {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	r := img.Bounds()
	out := make([]byte, 0, r.Dx()*r.Dy()*4)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := img.At(x, y)
			cr, cg, cb, ca := c.RGBA()
			if ca > 0 { // compare unpremultiplied
				cr, cg, cb = cr*0xffff/ca, cg*0xffff/ca, cb*0xffff/ca
			}
			if ca < 0x101 {
				cr, cg, cb = 0, 0, 0
			}
			out = append(out, byte(cr>>8), byte(cg>>8), byte(cb>>8), byte(ca>>8))
		}
	}
	return out
}

func TestAlpha(t *testing.T) {
	needBrowser(t)
	ff := needFF(t)
	spec := exampleSpec(t, "t2_keynote_ui", func(m map[string]any) { m["width"], m["height"] = 480, 270; m["duration"] = 1 })
	dir := t.TempDir()
	out := filepath.Join(dir, "a.mov")
	if err := Render(context.Background(), spec, out, Options{Alpha: true, AssetsDir: assetsDir()}); err != nil {
		t.Fatal(err)
	}
	s := ffprobe(t, ff, out).Streams[0]
	if s.CodecName != "prores" || !strings.HasPrefix(s.PixFmt, "yuva444") || s.NbFrames != "30" {
		t.Fatalf("stream %+v", s)
	}
	// The still has transparent and opaque pixels.
	p := filepath.Join(dir, "a.png")
	if err := Still(context.Background(), spec, 0.9, p, Options{Alpha: true, AssetsDir: assetsDir()}); err != nil {
		t.Fatal(err)
	}
	px := decodePNG(t, p)
	var clear, solid int
	for i := 3; i < len(px); i += 4 {
		switch px[i] {
		case 0:
			clear++
		case 255:
			solid++
		}
	}
	if clear == 0 || solid == 0 {
		t.Fatalf("alpha still: %d transparent, %d opaque pixels", clear, solid)
	}
	// And the movie's alpha plane too.
	if err := exec.Command(ff.FFmpeg, "-v", "error", "-i", out, "-vf", "select=eq(n\\,27),alphaextract", "-frames:v", "1", filepath.Join(dir, "alpha.png")).Run(); err != nil {
		t.Fatal(err)
	}
	am := decodePNG(t, filepath.Join(dir, "alpha.png"))
	var lo, hi int
	for i := 0; i < len(am); i += 4 {
		if am[i] < 10 {
			lo++
		} else if am[i] > 245 {
			hi++
		}
	}
	if lo == 0 || hi == 0 {
		t.Fatalf("movie alpha plane: %d clear, %d solid", lo, hi)
	}
}

func TestPageErrorFails(t *testing.T) {
	needBrowser(t)
	needFF(t)
	RegisterGrammar("test_broken", []byte(`CLIPS.test_broken = { draw(c, t, ctx) { if (t > 0.1) throw new Error('boom at ' + t.toFixed(2)); c.fillRect(0, 0, 10, 10); } };`), Grammar{Label: "test"})
	RegisterGrammar("test_console", []byte(`CLIPS.test_console = { init() { console.error('bad thing'); }, draw(c, t) { c.fillRect(0, 0, 10, 10); } };`), Grammar{Label: "test"})
	dir := t.TempDir()
	out := filepath.Join(dir, "x.mp4")
	err := Render(context.Background(), []byte(`{"grammar":"test_broken","duration":1,"width":64,"height":64}`), out, Options{})
	var pe *PageError
	if !errors.As(err, &pe) || !strings.Contains(err.Error(), "boom at 0.13") {
		t.Fatalf("want page error with the JS message, got %v", err)
	}
	if _, serr := os.Stat(out); serr == nil {
		t.Fatal("output left behind")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("left files: %v", ents)
	}
	err = Render(context.Background(), []byte(`{"grammar":"test_console","duration":1,"width":64,"height":64}`), out, Options{})
	if !errors.As(err, &pe) || !strings.Contains(err.Error(), "bad thing") {
		t.Fatalf("want console.error failure, got %v", err)
	}
	err = Render(context.Background(), []byte(`{"grammar":"nope","duration":1}`), out, Options{})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("unknown grammar: %v", err)
	}
	err = Render(context.Background(), exampleSpec(t, "y2_vox", nil), out, Options{AssetsDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "не знайдено") {
		t.Fatalf("missing image: %v", err)
	}
}

func TestCancel(t *testing.T) {
	needBrowser(t)
	needFF(t)
	ctx, cancel := context.WithCancel(context.Background())
	spec := exampleSpec(t, "y1_kurzgesagt", func(m map[string]any) { m["width"], m["height"] = 320, 180 })
	out := filepath.Join(t.TempDir(), "c.mp4")
	err := Render(ctx, spec, out, Options{Progress: func(d, n int) {
		if d == 10 {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if _, serr := os.Stat(out); serr == nil {
		t.Fatal("output left behind")
	}
}

func TestConcurrentRenders(t *testing.T) {
	needBrowser(t)
	needFF(t)
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i, name := range []string{"y3_whiteboard", "t1_3b1b", "y5_kinetic_type"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			spec := exampleSpec(t, name, func(m map[string]any) { m["width"], m["height"] = 320, 180; m["duration"] = 1 })
			errs[i] = Render(context.Background(), spec, filepath.Join(dir, name+".mp4"), Options{})
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
}

func TestGrammars(t *testing.T) {
	gs := Grammars()
	if len(gs) < 8 {
		t.Fatalf("%d grammars", len(gs))
	}
	for i, id := range examples {
		g := gs[i]
		if g.ID != id || g.Label == "" || g.Description == "" || len(g.Uses) == 0 || len(g.Cues) == 0 {
			t.Fatalf("grammar %d: %+v", i, g)
		}
		if _, ok := grammarJS(id); !ok {
			t.Fatalf("%s: no js", id)
		}
		var ex map[string]any
		if err := json.Unmarshal(g.Example, &ex); err != nil || ex["grammar"] != id {
			t.Fatalf("%s: bad example %s", id, g.Example)
		}
		var sc map[string]any
		if err := json.Unmarshal(g.Schema, &sc); err != nil {
			t.Fatalf("%s: schema %v", id, err)
		}
		for _, c := range g.Cues {
			if len(c.Example) > 0 && !json.Valid(c.Example) {
				t.Fatalf("%s/%s: bad cue example", id, c.Kind)
			}
		}
	}
}

// The Ukrainian examples of Grammars() render without page errors.
func TestGrammarExamplesRender(t *testing.T) {
	needBrowser(t)
	needFF(t)
	for _, g := range Grammars()[:8] {
		t.Run(g.ID, func(t *testing.T) {
			var m map[string]any
			_ = json.Unmarshal(g.Example, &m)
			m["width"], m["height"] = 540, 960
			spec, _ := json.Marshal(m)
			p := filepath.Join(t.TempDir(), "s.png")
			d, _ := m["duration"].(float64)
			if err := Still(context.Background(), spec, d-0.2, p, Options{}); err != nil {
				t.Fatal(err)
			}
			if dir := os.Getenv("CODEANIM_SHOTS"); dir != "" {
				b, _ := os.ReadFile(p)
				_ = os.WriteFile(filepath.Join(dir, g.ID+".png"), b, 0o644)
			}
		})
	}
}

// Speed: a 1080×1920 30 fps clip (CODEANIM_SPEED=1 go test -run Speed -v).
func TestSpeed(t *testing.T) {
	if os.Getenv("CODEANIM_SPEED") == "" {
		t.Skip("set CODEANIM_SPEED=1")
	}
	needBrowser(t)
	needFF(t)
	for _, name := range []string{"t3_finance_chart", "y1_kurzgesagt", "t3_finance_chart", "y1_kurzgesagt"} {
		spec := exampleSpec(t, name, func(m map[string]any) { m["width"], m["height"] = 1080, 1920; m["duration"] = 4 })
		out := filepath.Join(t.TempDir(), "speed.mp4")
		start := time.Now()
		if err := Render(context.Background(), spec, out, Options{}); err != nil {
			t.Fatal(err)
		}
		el := time.Since(start)
		t.Logf("%s 1080x1920@30, 120 frames, %d pages: %v = %.1f fps", name, Concurrency(), el.Round(time.Millisecond), 120/el.Seconds())
	}
}
