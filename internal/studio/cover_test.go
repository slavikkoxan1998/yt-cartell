package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/cover"
)

const coverTestTitle = "Він світився до останнього і врятував хлопчика в лісі"

// setTestCover stores a synthetic picture as the project's cover (the cover
// stage normally draws it) and returns its relative path.
func setTestCover(t *testing.T, e *Engine, id string, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(40 + 150*x/w), uint8(60 + 120*y/h), uint8(90 + 60*(x/97%2) + 40*(y/61%2)), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	st := e.state(id)
	rel, err := e.writeAsset(st, "images", "cover-"+newID(8)+".png", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	e.update(st, func(p *Project) {
		p.Montage.Cover = Asset{Path: rel, Status: StatusDone, UpdatedAt: nowMs()}
	})
	return rel
}

func setCoverText(t *testing.T, e *Engine, id string, ct CoverText) {
	t.Helper()
	if err := e.Edit(id, func(p *Project) { p.Montage.CoverText = ct }); err != nil {
		t.Fatal(err)
	}
}

func decodeJPEGFile(t *testing.T, path string) image.Image {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > cover.MaxBytes {
		t.Fatalf("%s: %d bytes > YouTube limit", path, len(data))
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s is not a JPEG: %v", path, err)
	}
	return img
}

func coverFinals(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(dir, "images"))
	var out []string
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), "cover-final-") {
			out = append(out, en.Name())
		}
	}
	sort.Strings(out)
	return out
}

var reFinal = regexp.MustCompile(`^images/cover-final-\d+\.jpg$`)

func TestApplyCoverText(t *testing.T) {
	env := newEnv(t, nil)
	e := env.e
	p, err := e.Create("Обкладинка")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ApplyCoverText(p.ID); !errors.Is(err, ErrNoCover) {
		t.Fatalf("without a picture: %v", err)
	}
	if err := e.ApplyCoverText("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project: %v", err)
	}
	coverRel := setTestCover(t, e, p.ID, 1376, 768)
	setCoverText(t, e, p.ID, CoverText{Template: "yellow-shock", Title: coverTestTitle, Badge: "Нове", Subtitle: "Правдива історія"})

	var mu sync.Mutex
	notified := 0
	unsub := e.Subscribe(func(pid string) {
		if pid == p.ID {
			mu.Lock()
			notified++
			mu.Unlock()
		}
	})
	defer unsub()

	if err := e.ApplyCoverText(p.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Get(p.ID)
	fin := got.Montage.CoverFinal
	if fin.Status != StatusDone || fin.Error != "" || fin.UpdatedAt == 0 || fin.Stale || !reFinal.MatchString(fin.Path) {
		t.Fatalf("CoverFinal = %+v", fin)
	}
	if got.Montage.Cover.Path != coverRel {
		t.Fatalf("the picture changed: %q", got.Montage.Cover.Path)
	}
	img := decodeJPEGFile(t, e.Abs(p.ID, fin.Path))
	if img.Bounds().Size() != cover.Landscape {
		t.Fatalf("final size %v", img.Bounds().Size())
	}
	if CoverFinalOutdated(got) {
		t.Fatal("fresh final reported outdated")
	}
	time.Sleep(3 * notifyGap)
	mu.Lock()
	if notified == 0 {
		t.Error("subscribers were not notified")
	}
	mu.Unlock()

	// A new design replaces the file (the old one is deleted).
	first := fin.Path
	setCoverText(t, e, p.ID, CoverText{Template: "white-plate", Title: "*Врятував* хлопчика"})
	time.Sleep(2 * time.Millisecond)
	if err := e.ApplyCoverText(p.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = e.Get(p.ID)
	if got.Montage.CoverFinal.Path == first || got.Montage.CoverFinal.Status != StatusDone {
		t.Fatalf("second apply: %+v", got.Montage.CoverFinal)
	}
	if isFile(e.Abs(p.ID, first)) {
		t.Error("the replaced final is still on disk")
	}
	if n := coverFinals(t, e.Dir(p.ID)); len(n) != 1 {
		t.Errorf("final files: %v", n)
	}

	// Without a title the final cover is the plain picture.
	setCoverText(t, e, p.ID, CoverText{Template: "white-plate", Subtitle: "лише підзаголовок"})
	if err := e.ApplyCoverText(p.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = e.Get(p.ID)
	plain := decodeJPEGFile(t, e.Abs(p.ID, got.Montage.CoverFinal.Path))
	src, err := cover.LoadImage(e.Abs(p.ID, coverRel))
	if err != nil {
		t.Fatal(err)
	}
	want := cover.CoverFit(src, cover.Landscape)
	if d := meanDiff(plain, want); d > 3 {
		t.Errorf("plain final differs from the picture by %.2f", d)
	}

	// A newer picture makes the final outdated.
	setTestCover(t, e, p.ID, 1376, 768)
	got, _ = e.Get(p.ID)
	if !CoverFinalOutdated(got) {
		t.Error("new picture not detected")
	}
	if e.Flush() != nil {
		t.Fatal("flush")
	}
	// The saved project keeps the design and the final.
	raw, err := os.ReadFile(filepath.Join(e.Dir(p.ID), projectFile))
	if err != nil || !bytes.Contains(raw, []byte(`"coverFinal"`)) || !bytes.Contains(raw, []byte(`лише підзаголовок`)) {
		t.Fatalf("project.json: %v", err)
	}
}

func meanDiff(a, b image.Image) float64 {
	r := a.Bounds()
	var sum, n float64
	for y := r.Min.Y; y < r.Max.Y; y += 3 {
		for x := r.Min.X; x < r.Max.X; x += 3 {
			ar, ag, ab, _ := a.At(x, y).RGBA()
			br, bg, bb, _ := b.At(x, y).RGBA()
			d := func(p, q uint32) float64 {
				if p > q {
					return float64(p-q) / 257
				}
				return float64(q-p) / 257
			}
			sum += d(ar, br) + d(ag, bg) + d(ab, bb)
			n += 3
		}
	}
	return sum / n
}

func TestApplyCoverTextShortsAndConcurrency(t *testing.T) {
	env := newEnv(t, nil)
	e := env.e
	p, _ := e.Create("Shorts")
	if err := e.Edit(p.ID, func(p *Project) { p.Input.Aspect = "9:16" }); err != nil {
		t.Fatal(err)
	}
	setTestCover(t, e, p.ID, 768, 1376)
	setCoverText(t, e, p.ID, CoverText{Title: coverTestTitle})

	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- e.ApplyCoverText(p.ID)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, _ := e.Get(p.ID)
	fin := got.Montage.CoverFinal
	if fin.Status != StatusDone || !reFinal.MatchString(fin.Path) {
		t.Fatalf("CoverFinal = %+v", fin)
	}
	if img := decodeJPEGFile(t, e.Abs(p.ID, fin.Path)); img.Bounds().Size() != cover.Portrait {
		t.Fatalf("Shorts cover size %v", img.Bounds().Size())
	}
	if n := coverFinals(t, e.Dir(p.ID)); len(n) != 1 || "images/"+n[0] != fin.Path {
		t.Fatalf("final files %v, CoverFinal %s", n, fin.Path)
	}
	if spec := CoverSpec(got.Montage.CoverText, "9:16"); spec.Template != "shorts-top" {
		t.Errorf("Shorts default template = %s", spec.Template)
	}
}

func TestPreviewCover(t *testing.T) {
	env := newEnv(t, nil)
	e := env.e
	if _, err := e.PreviewCover("nope", CoverText{}, 640); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project: %v", err)
	}
	p, _ := e.Create("Прев'ю")
	// No picture yet: a neutral background still shows the design.
	img, err := e.PreviewCover(p.ID, CoverText{Title: "Без картинки"}, 640)
	if err != nil || img.Bounds().Size() != image.Pt(640, 360) {
		t.Fatalf("preview without picture: %v %v", img.Bounds(), err)
	}
	setTestCover(t, e, p.ID, 1376, 768)
	before := listFiles(t, e.Dir(p.ID))

	ct := CoverText{Template: "bottom-bold", Title: coverTestTitle, Subtitle: "Правдива історія з Карпат", Badge: "Нове"}
	start := time.Now()
	img, err = e.PreviewCover(p.ID, ct, 640)
	cold := time.Since(start)
	if err != nil || img.Bounds().Size() != image.Pt(640, 360) {
		t.Fatalf("preview: %v %v", img.Bounds(), err)
	}
	t.Logf("cold preview (decode + fit + text): %v", cold)
	var worst time.Duration
	for i, tpl := range cover.Templates {
		ct.Template = tpl.ID
		ct.Title = coverTestTitle[:len(coverTestTitle)-i%5] // "typing"
		start := time.Now()
		if _, err := e.PreviewCover(p.ID, ct, 640); err != nil {
			t.Fatal(err)
		}
		worst = max(worst, time.Since(start))
	}
	t.Logf("slowest warm preview: %v", worst)
	if worst > 150*time.Millisecond {
		t.Errorf("warm preview took %v (> 150 ms)", worst)
	}
	if cold > 600*time.Millisecond {
		t.Errorf("cold preview took %v", cold)
	}
	// The preview matches the final render (same design, smaller).
	if after := listFiles(t, e.Dir(p.ID)); strings.Join(after, "|") != strings.Join(before, "|") {
		t.Fatalf("preview wrote files: %v → %v", before, after)
	}
	// The previous image is not modified by later previews (fresh canvas).
	a, _ := e.PreviewCover(p.ID, CoverText{Title: "Перший"}, 320)
	snap := append([]uint8(nil), a.(*image.RGBA).Pix...)
	if _, err := e.PreviewCover(p.ID, CoverText{Title: "Другий"}, 320); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snap, a.(*image.RGBA).Pix) {
		t.Fatal("a preview image changed after the next preview")
	}
	// Vertical project.
	if err := e.Edit(p.ID, func(p *Project) { p.Input.Aspect = "9:16" }); err != nil {
		t.Fatal(err)
	}
	img, err = e.PreviewCover(p.ID, CoverText{Title: coverTestTitle}, 360)
	if err != nil || img.Bounds().Size() != image.Pt(360, 640) {
		t.Fatalf("vertical preview: %v %v", img.Bounds(), err)
	}
}

func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() != projectFile {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// coverTextMock is an OpenAI-compatible endpoint answering the cover-title
// prompt.
type coverTextMock struct {
	mu      sync.Mutex
	prompts []string
	reply   string
}

func (m *coverTextMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer txt-test" || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		w.WriteHeader(401)
		return
	}
	var req struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) != 1 {
		w.WriteHeader(400)
		return
	}
	m.mu.Lock()
	m.prompts = append(m.prompts, req.Messages[0].Content)
	reply := m.reply
	m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": reply}}},
	})
}

func (m *coverTextMock) last() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.prompts) == 0 {
		return ""
	}
	return m.prompts[len(m.prompts)-1]
}

func TestSuggestCoverTitle(t *testing.T) {
	mock := &coverTextMock{}
	srv := httptest.NewServer(mock)
	defer srv.Close()
	allow(srv)
	env := newEnv(t, func(c *config.Config) { c.Text.BaseURL = srv.URL + "/v1" })
	e := env.e
	p, _ := e.Create("Світлячок")
	if err := e.Edit(p.ID, func(p *Project) {
		p.Title = "Світлячок, що не згас"
		p.Logline = "Світлячок рятує хлопчика, який заблукав у нічному лісі."
		p.Input.Genre = "fairy tale"
	}); err != nil {
		t.Fatal(err)
	}

	mock.reply = "Ось варіант:\n```json\n{\"title\": \"«*Врятував* хлопчика в лісі!» 🔥 #shorts\"}\n```"
	got, err := e.SuggestCoverTitle(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "*Врятував* хлопчика в лісі!" {
		t.Fatalf("title = %q", got)
	}
	prompt := mock.last()
	for _, want := range []string{"Світлячок, що не згас", "рятує хлопчика", "2 to 6 words", "SAME language", `{"title"`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}

	// An audience language is named; long answers are cut to six words.
	if err := e.Edit(p.ID, func(p *Project) { p.Input.Language = "en" }); err != nil {
		t.Fatal(err)
	}
	mock.reply = `{"title": "He glowed until the very end and saved the boy."}`
	got, err = e.SuggestCoverTitle(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "He glowed until the very end" {
		t.Fatalf("title = %q", got)
	}
	if !strings.Contains(mock.last(), "in English") {
		t.Errorf("language missing from prompt:\n%s", mock.last())
	}

	mock.reply = `{"title": "  🔥🔥 "}`
	if _, err := e.SuggestCoverTitle(context.Background(), p.ID); err == nil {
		t.Fatal("empty suggestion accepted")
	}
	if _, err := e.SuggestCoverTitle(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project: %v", err)
	}
}

func TestCleanCoverTitle(t *testing.T) {
	for in, want := range map[string]string{
		`"Він *врятував* хлопчика!"`:          "Він *врятував* хлопчика!",
		"«Пʼять секунд до вибуху».":           "Пʼять секунд до вибуху",
		"*Раз* *два* три":                     "*Раз* два три",
		"Непарна *зірочка":                    "Непарна зірочка",
		"Хто *це* зробив?!":                   "Хто *це* зробив?!",
		"Один два три чотири пʼять шість сім": "Один два три чотири пʼять шість",
		"— Чекай... ":                         "Чекай...",
		"#мʼята 🌿 історія":                    "історія",
		fmt.Sprintf("%s\n%s", "Два", "рядки"): "Два рядки",
	} {
		if got := cleanCoverTitle(in); got != want {
			t.Errorf("cleanCoverTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCoverCatalogPassthrough(t *testing.T) {
	tpls, fonts := CoverTemplates(), CoverFonts()
	if len(tpls) < 8 || len(fonts) < 5 {
		t.Fatalf("%d templates, %d fonts", len(tpls), len(fonts))
	}
	tpls[0].ID = "changed"
	if cover.Templates[0].ID == "changed" {
		t.Fatal("catalog is not a copy")
	}
	if s := CoverSpec(CoverText{}, "16:9"); s.Template != cover.DefaultTemplate(false) {
		t.Fatalf("default template %s", s.Template)
	}
	if s := CoverSpec(CoverText{Template: "news", Stroke: "none", Color: "#fff"}, "9:16"); s.Template != "news" || s.Stroke != "" || s.Color != "#FFFFFF" {
		t.Fatalf("spec %+v", s)
	}
}

func TestCoverImageCache(t *testing.T) {
	c := &imgCache{srcBudget: 1000, fitBudget: 1000}
	img := func(n int) image.Image { return image.NewRGBA(image.Rect(0, 0, n, 1)) } // 4n bytes
	k := func(path string, mod int64, w int) imgKey { return imgKey{path: path, mod: mod, size: 1, w: w, h: w} }
	c.put(k("a", 1, 10), img(100)) // 400 B
	c.put(k("b", 1, 10), img(100))
	if c.get(k("a", 1, 10)) == nil || c.get(k("b", 1, 10)) == nil {
		t.Fatal("entries lost")
	}
	c.put(k("c", 1, 10), img(100)) // 1200 B > budget: the least recently used ("a") goes
	if c.get(k("a", 1, 10)) != nil || c.get(k("c", 1, 10)) == nil {
		t.Fatal("LRU eviction")
	}
	c.put(k("c", 2, 10), img(10)) // a new version of the file replaces the old one
	if c.get(k("c", 1, 10)) != nil || c.get(k("c", 2, 10)) == nil {
		t.Fatal("stale version kept")
	}
	c.put(k("d", 1, 10), img(1000)) // bigger than the budget: still kept alone
	if c.get(k("d", 1, 10)) == nil || len(c.fit) != 1 {
		t.Fatalf("newest entry dropped (%d entries)", len(c.fit))
	}
	c.put(k("d", 1, 10), img(1000)) // same key: replaced, not duplicated
	if len(c.fit) != 1 {
		t.Fatalf("duplicate entries: %d", len(c.fit))
	}
	c.put(k("s", 1, 0), img(10))
	if c.get(k("s", 1, 0)) == nil || len(c.src) != 1 {
		t.Fatal("sources are kept apart")
	}
}

func TestApplyCoverTextMissingPicture(t *testing.T) {
	env := newEnv(t, nil)
	e := env.e
	p, _ := e.Create("Без файлу")
	rel := setTestCover(t, e, p.ID, 640, 360)
	setCoverText(t, e, p.ID, CoverText{Title: "Заголовок"})
	if err := os.Remove(e.Abs(p.ID, rel)); err != nil {
		t.Fatal(err)
	}
	err := e.ApplyCoverText(p.ID)
	if err == nil || !strings.Contains(err.Error(), "не знайдено") {
		t.Fatalf("err = %v", err)
	}
	got, _ := e.Get(p.ID)
	if f := got.Montage.CoverFinal; f.Status != StatusError || f.Error == "" {
		t.Fatalf("CoverFinal = %+v", f)
	}
}
