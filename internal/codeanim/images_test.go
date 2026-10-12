package codeanim

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writePNG(t *testing.T, path string, c color.NRGBA) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 40, 80))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
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

// Every image field the map grammar reads is served (relative to AssetsDir).
func TestParseSpecRewritesAllImageFields(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.png", "a_shock.png", "icon.png", "cue.png", "bub.png", "inline.png"} {
		writePNG(t, filepath.Join(dir, n), color.NRGBA{200, 10, 10, 255})
	}
	spec := `{"grammar":"map","duration":2,"data":{
  "sprites":[{"id":"a","place":"paris","image":"a.png","alt":"a_shock.png"}],
  "icons":[{"id":"i","type":"image","image":"icon.png","place":"paris"}],
  "labels":[{"id":"l","text":"Alt text here","alt":"not a picture"}]},
 "cues":[{"at":0.1,"kind":"sprite","sprite":"a","action":"swap","image":"cue.png"},
  {"at":0.2,"kind":"bubble","sprite":"a","image":"bub.png","icon":{"type":"image","image":"inline.png"}},
  {"at":0.3,"kind":"pop","data":{"image":"icon.png"}}]}`
	c, err := parseSpec([]byte(spec), Options{AssetsDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.files) != 6 {
		t.Fatalf("want 6 served files, got %d: %v", len(c.files), c.files)
	}
	b, _ := json.Marshal(c.raw)
	s := string(b)
	for _, n := range []string{`"a.png"`, `"a_shock.png"`, `"icon.png"`, `"cue.png"`, `"bub.png"`, `"inline.png"`} {
		if strings.Contains(s, n) {
			t.Fatalf("%s not rewritten: %s", n, s)
		}
	}
	if !strings.Contains(s, `"not a picture"`) {
		t.Fatalf("alt text must stay: %s", s)
	}
	// A missing sprite frame is an exact error naming the file and field.
	bad := strings.Replace(spec, `"alt":"a_shock.png"`, `"alt":"gone.png"`, 1)
	_, err = parseSpec([]byte(bad), Options{AssetsDir: dir})
	if err == nil || !strings.Contains(err.Error(), "gone.png") || !strings.Contains(err.Error(), "data.sprites[0].alt") {
		t.Fatalf("want exact missing-file error, got %v", err)
	}
}

// The map's data.sprites images reach the page and are drawn before frame 0.
func TestMapDataSpritesRender(t *testing.T) {
	needBrowser(t)
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "red.png"), color.NRGBA{230, 0, 0, 255})
	spec := `{"grammar":"map","duration":1,"width":540,"height":960,"data":{"view":{"lon":2,"lat":47,"zoom":12},"drift":0,
  "sprites":[{"id":"r","place":"paris","scale":0.5,"anim":"none","image":"red.png"}]},"cues":[{"at":0.5,"kind":"label","text":"x","place":"paris"}]}`
	img, err := stillImage(context.Background(), []byte(spec), 0, Options{AssetsDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	red := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 200 && img.Pix[i+1] < 40 && img.Pix[i+2] < 40 {
			red++
		}
	}
	if red < 500 {
		t.Fatalf("sprite image not drawn at frame 0 (red pixels: %d)", red)
	}
}

// window.prepare is awaited before frame 0; a prepare that never ends is an
// exact timeout error.
func TestPrepareAwaited(t *testing.T) {
	needBrowser(t)
	RegisterGrammar("test_prepare", []byte(`CLIPS.test_prepare = { init() { let done = false;
  Object.defineProperty(window, 'prepare', {configurable: true, get: () => async () => { await new Promise(r => setTimeout(r, 300)); done = true; }, set: () => {}});
  window.__done = () => done; },
  draw(c, t) { c.fillStyle = window.__done() ? '#00ff00' : '#ff0000'; c.fillRect(0, 0, 64, 64); } };`), Grammar{Label: "test"})
	img, err := stillImage(context.Background(), []byte(`{"grammar":"test_prepare","duration":1,"width":64,"height":64}`), 0, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if img.Pix[1] < 200 || img.Pix[0] > 50 {
		t.Fatalf("frame 0 drawn before prepare finished: %v", img.Pix[:4])
	}
	RegisterGrammar("test_prepare_hang", []byte(`CLIPS.test_prepare_hang = { init() {
  Object.defineProperty(window, 'prepare', {configurable: true, get: () => () => new Promise(() => {}), set: () => {}}); },
  draw(c) { c.fillRect(0, 0, 4, 4); } };`), Grammar{Label: "test"})
	old := prepareTimeout
	prepareTimeout = 1500 * time.Millisecond
	defer func() { prepareTimeout = old }()
	_, err = stillImage(context.Background(), []byte(`{"grammar":"test_prepare_hang","duration":1,"width":64,"height":64}`), 0, Options{})
	if err == nil || !strings.Contains(err.Error(), "window.prepare() не завершився") {
		t.Fatalf("want prepare timeout, got %v", err)
	}
}
