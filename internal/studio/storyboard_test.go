package studio

// Storyboard mode (3×3 sheets) and continuity chaining against a G-Labs mock
// that keeps whole request bodies and serves real media: a 3×3 sheet for
// storyboard prompts and ffmpeg-made clips whose LAST frame has a distinct
// colour, so the "previous_shot" reference can be checked pixel-wise.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // image.Decode of the frames and last frames
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
)

type sbRef struct {
	name string
	data []byte
}

type sbReq struct {
	kind, prompt, model, aspect, mode string
	length                            int
	refs                              []sbRef
	at                                time.Time // submitted
	file                              string
}

func (r sbReq) names() string {
	n := make([]string, len(r.refs))
	for i, ref := range r.refs {
		n[i] = ref.name
	}
	return strings.Join(n, ",")
}

func (r sbReq) ref(name string) []byte {
	for _, ref := range r.refs {
		if ref.name == name {
			return ref.data
		}
	}
	return nil
}

// sbMock is a G-Labs server that answers every task at the first poll.
type sbMock struct {
	mu      sync.Mutex
	next    int
	files   map[string][]byte    // served file name → bytes
	fetched map[string]time.Time // file name → first download
	reqs    []sbReq
	sheet   []byte   // served for storyboard prompts
	sheets  [][]byte // when set: served (in turn, the last one repeating) instead of sheet
	boards  int
	clips   [][]byte // served round-robin for video requests
	videos  int
}

func dataURIBytes(u string) []byte {
	_, b64, _ := strings.Cut(u, ",")
	b, _ := base64.StdEncoding.DecodeString(b64)
	return b
}

func (m *sbMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/files/") {
		name := strings.TrimPrefix(path, "/api/files/")
		m.mu.Lock()
		data, ok := m.files[name]
		if _, seen := m.fetched[name]; ok && !seen {
			m.fetched[name] = time.Now()
		}
		m.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
		return
	}
	if r.Header.Get("X-API-Key") != "gl-test" {
		w.WriteHeader(401)
		return
	}
	switch {
	case r.Method == http.MethodPost && (path == "/api/image/generate" || path == "/api/video/generate"):
		var body struct {
			Prompt string            `json:"prompt"`
			Model  string            `json:"model"`
			Aspect string            `json:"aspect_ratio"`
			Mode   string            `json:"mode"`
			Length int               `json:"video_length"`
			Refs   []json.RawMessage `json:"reference_images"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		req := sbReq{kind: "image", prompt: body.Prompt, model: body.Model, aspect: body.Aspect, mode: body.Mode, length: body.Length, at: time.Now()}
		for _, raw := range body.Refs {
			var s string
			if json.Unmarshal(raw, &s) == nil {
				req.refs = append(req.refs, sbRef{data: dataURIBytes(s)})
				continue
			}
			var o struct{ Data, Name string }
			_ = json.Unmarshal(raw, &o)
			req.refs = append(req.refs, sbRef{name: o.Name, data: dataURIBytes(o.Data)})
		}
		m.mu.Lock()
		m.next++
		id := fmt.Sprintf("t%d", m.next)
		switch {
		case path == "/api/video/generate":
			req.kind, req.file = "video", id+".mp4"
			m.files[req.file] = m.clips[m.videos%len(m.clips)]
			m.videos++
		case strings.Contains(body.Prompt, "3×3 storyboard sheet"):
			req.file = id + ".png"
			m.files[req.file] = m.sheet
			if len(m.sheets) > 0 {
				m.files[req.file] = m.sheets[min(m.boards, len(m.sheets)-1)]
			}
			m.boards++
		default:
			req.file = id + ".jpg"
			m.files[req.file] = testImage
		}
		m.reqs = append(m.reqs, req)
		m.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"task_id":%q}`, id)
	case strings.HasPrefix(path, "/api/status/"):
		id := strings.TrimPrefix(path, "/api/status/")
		m.mu.Lock()
		file := ""
		for _, q := range m.reqs {
			if strings.TrimSuffix(q.file, filepath.Ext(q.file)) == id {
				file = q.file
			}
		}
		m.mu.Unlock()
		if file == "" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"status":"completed","results":["/api/files/%s"]}`, file)
	default:
		http.NotFound(w, r)
	}
}

func (m *sbMock) requests(kind string) []sbReq {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []sbReq
	for _, r := range m.reqs {
		if r.kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func (m *sbMock) fetchedAt(file string) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fetched[file]
}

// ---- media -------------------------------------------------------------------------------

var sheetColors = []color.RGBA{
	{220, 40, 40, 255}, {40, 160, 60, 255}, {50, 80, 200, 255},
	{200, 170, 30, 255}, {150, 50, 170, 255}, {30, 160, 170, 255},
	{230, 120, 30, 255}, {110, 110, 110, 255}, {160, 90, 60, 255},
}

// sheetPNG draws a 480×270 3×3 storyboard: coloured panels, white gutters.
func sheetPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 480, 270))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	pw, ph := (480-2*6)/3, (270-2*6)/3
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			x0, y0 := c*(pw+6), r*(ph+6)
			for y := y0; y < y0+ph; y++ {
				for x := x0; x < x0+pw; x++ {
					img.SetRGBA(x, y, sheetColors[r*3+c])
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// endClip is a 1 s clip of blue frames whose last frame is `last`.
func endClip(t *testing.T, last string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "clip-"+last+".mp4")
	err := testFF.Run(t.Context(), "", "-f", "lavfi", "-i", "color=c=blue:s=160x90:r=24:d=1",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:d=1",
		"-vf", "drawbox=x=0:y=0:w=iw:h=ih:color="+last+":t=fill:enable='eq(n,23)'",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", out)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func avgRGB(t *testing.T, data []byte) [3]int {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not an image: %v", err)
	}
	b := img.Bounds()
	var s [3]int64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			s[0], s[1], s[2] = s[0]+int64(r>>8), s[1]+int64(g>>8), s[2]+int64(bl>>8)
		}
	}
	n := int64(b.Dx() * b.Dy())
	return [3]int{int(s[0] / n), int(s[1] / n), int(s[2] / n)}
}

func nearRGB(a [3]int, r, g, b, tol int) bool {
	w := [3]int{r, g, b}
	for i := range a {
		if d := a[i] - w[i]; d > tol || d < -tol {
			return false
		}
	}
	return true
}

// ---- fixtures -----------------------------------------------------------------------------

func newSBEnv(t *testing.T, mut func(c *config.Config)) (*testEnv, *sbMock) {
	t.Helper()
	m := &sbMock{files: map[string][]byte{}, fetched: map[string]time.Time{}, sheet: sheetPNG(t),
		clips: [][]byte{endClip(t, "red"), endClip(t, "green"), endClip(t, "yellow")}}
	srv := httptest.NewServer(m)
	allow(srv)
	t.Cleanup(srv.Close)
	env := newEnv(t, func(c *config.Config) {
		c.Glabs.BaseURL = srv.URL
		c.Video.GlabsModel = "omni_flash"
		c.Video.Length = 10
		c.Concurrency = 3
		if mut != nil {
			mut(c)
		}
	})
	return env, m
}

// castProject makes a project with six characters and a location (all with
// reference images) and the given scenes (cast by character index).
func castProject(t *testing.T, e *Engine, in func(in *Input), scenes ...[]int) Project {
	t.Helper()
	img := filepath.Join(t.TempDir(), "ref.jpg")
	must(t, os.WriteFile(img, testImage, 0o644))
	names := []string{"Тарас", "Лисиця", "Олег", "Марта", "Ірина", "Богдан"}
	p, err := e.Create("Ланцюжок")
	must(t, err)
	must(t, e.Edit(p.ID, func(p *Project) {
		p.StyleGuide = "Soft watercolor."
		in(&p.Input)
		for i, n := range names {
			p.Characters = append(p.Characters, Character{ID: fmt.Sprintf("ch-%d-x", i), Name: n, Description: "person " + n})
		}
		p.Locations = []Location{{ID: "loc-forest", Name: "Ліс", Description: "autumn forest"}}
		for i, cast := range scenes {
			var ids []string
			for _, c := range cast {
				ids = append(ids, fmt.Sprintf("ch-%d-x", c))
			}
			p.Scenes = append(p.Scenes, Scene{ID: fmt.Sprintf("scene-%d", i+1), Title: fmt.Sprintf("S%d", i+1),
				Narration: "Рядок.", CharacterIDs: ids, LocationID: "loc-forest",
				ImagePrompt: fmt.Sprintf("Wide shot %d of @taras in @lis", i+1), MotionPrompt: fmt.Sprintf("Motion S%d: slow push-in.", i+1)})
		}
	}))
	p, _ = e.Get(p.ID)
	for _, c := range p.Characters {
		must(t, e.ImportImage(p.ID, RegenCharacter, c.ID, img))
	}
	must(t, e.ImportImage(p.ID, RegenLocation, "loc-forest", img))
	p, _ = e.Get(p.ID)
	if got := p.Characters[0].Tag + "," + p.Characters[1].Tag + "," + p.Locations[0].Tag; got != "taras,lysytsia,lis" {
		t.Fatalf("tags %s", got)
	}
	return p
}

// jobNotes records every distinct job message while the test runs.
func jobNotes(t *testing.T, e *Engine, id string) func() []string {
	var mu sync.Mutex
	var seen []string
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		last := ""
		for {
			if p, ok := e.Get(id); ok && p.Job != nil && p.Job.Message != last {
				last = p.Job.Message
				mu.Lock()
				seen = append(seen, last)
				mu.Unlock()
			}
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(stop); <-done }) })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// ---- tests ----------------------------------------------------------------------------------

// TestStoryboardGridOmniChain: grid mode draws 3×3 sheets (panel 1 → frame);
// Omni Flash renders them in components mode, chained scene by scene with the
// previous clip's last frame; other models fall back to the frame.
func TestStoryboardGridOmniChain(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, gl := newSBEnv(t, nil)
	e := env.e
	p := castProject(t, e, func(in *Input) {
		in.StoryboardMode = StoryboardGrid
		in.ChainFrames = true
	}, []int{0, 1}, []int{0, 1}, []int{0, 1, 2, 3, 4, 5})
	id := p.ID
	notes := jobNotes(t, e, id)

	// ---- storyboard sheets
	must(t, e.Run(id, StageFrames, false, ""))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if p.Job.Message != "Розкадровка 3×3 готова (3 сцен)" {
		t.Fatalf("frames job: %q", p.Job.Message)
	}
	boards := gl.requests("image")
	if len(boards) != 3 {
		t.Fatalf("image requests %d (references are imported, only sheets expected)", len(boards))
	}
	for _, r := range boards {
		// Same named references as a frame, shortest tag first.
		names := r.names()
		if names != "lis.jpg,taras.jpg,lysytsia.jpg" && names != "lis.jpg,oleh.jpg,taras.jpg,marta.jpg,iryna.jpg,bohdan.jpg,lysytsia.jpg" {
			t.Fatalf("storyboard refs %s", names)
		}
		if !strings.Contains(r.prompt, "3×3 storyboard sheet") || r.aspect != "16:9" || !strings.Contains(r.prompt, "over its 10 seconds") ||
			!strings.Contains(r.prompt, "@taras is Тарас — identical face") || !strings.Contains(r.prompt, "Location: @lis (Ліс)") ||
			!strings.Contains(r.prompt, "thin, straight, pure white gutters") || !strings.Contains(r.prompt, "Art style: Soft watercolor.") ||
			!strings.HasSuffix(r.prompt, "No text, no numbers, no panel labels, no captions, no speech bubbles, no watermark.") {
			t.Fatalf("storyboard prompt:\n%s", r.prompt)
		}
	}
	for _, s := range p.Scenes {
		b, f := s.Board, s.Frame
		if b.Status != StatusDone || !strings.HasPrefix(b.Path, "images/board-"+s.ID+"-") || !strings.HasSuffix(b.Path, ".jpg") || b.UpdatedAt == 0 {
			t.Fatalf("board %+v", b)
		}
		if f.Status != StatusDone || !strings.HasPrefix(f.Path, "images/frame-"+s.ID+"-") || AssetState(f) != "done" || SceneState(s) != "framed" {
			t.Fatalf("frame %+v", f)
		}
		sheet, err := os.ReadFile(e.Abs(id, b.Path))
		must(t, err)
		if !bytes.HasPrefix(sheet, []byte{0xff, 0xd8}) { // PNG from the model → stored as JPEG
			t.Fatal("board is not a JPEG")
		}
		frame, err := os.ReadFile(e.Abs(id, f.Path))
		must(t, err)
		cfg, _, err := image.DecodeConfig(bytes.NewReader(frame))
		must(t, err)
		if cfg.Width < 140 || cfg.Width > 156 || cfg.Height < 76 || cfg.Height > 86 {
			t.Fatalf("panel 1 size %dx%d (sheet 480x270, panel 156x86)", cfg.Width, cfg.Height)
		}
		if c := avgRGB(t, frame); !nearRGB(c, 220, 40, 40, 10) {
			t.Fatalf("frame is not panel 1: colour %v", c)
		}
	}

	// ---- chained Omni Flash clips
	must(t, e.Run(id, StageVideos, false, ""))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if p.Job.Message != "Відео й озвучка готові (3)" {
		t.Fatalf("videos job: %q", p.Job.Message)
	}
	vids := gl.requests("video")
	if len(vids) != 3 {
		t.Fatalf("video requests %d", len(vids))
	}
	for i, r := range vids {
		if !strings.HasPrefix(r.prompt, fmt.Sprintf("Motion S%d:", i+1)) {
			t.Fatalf("clip %d rendered out of order: %q", i+1, r.prompt)
		}
		if r.mode != "components" || r.model != "omni_flash" || r.length != 10 || r.aspect != "16:9" {
			t.Fatalf("clip %d request %+v", i+1, r)
		}
		board, err := os.ReadFile(e.Abs(id, p.Scenes[i].Board.Path))
		must(t, err)
		if !bytes.Equal(r.ref("storyboard.jpg"), board) {
			t.Fatalf("clip %d: storyboard ref is not the scene's board", i+1)
		}
		for _, want := range []string{"@storyboard is a 3×3 storyboard of THIS shot", "never show the grid", "@taras is Тарас — identical face",
			fmt.Sprintf("Shot: Wide shot %d of @taras in @lis", i+1), "Art style: Soft watercolor.", "No dialogue, no speech"} {
			if !strings.Contains(r.prompt, want) {
				t.Fatalf("clip %d prompt lacks %q:\n%s", i+1, want, r.prompt)
			}
		}
		if hasPrev := strings.Contains(r.prompt, "@previous_shot is the final frame of the previous shot"); hasPrev != (i > 0) {
			t.Fatalf("clip %d previous-shot line = %v:\n%s", i+1, hasPrev, r.prompt)
		}
		// The location reference is capped out of the crowded third clip.
		if hasLoc := strings.Contains(r.prompt, "Location: @lis (Ліс)"); hasLoc != (i < 2) {
			t.Fatalf("clip %d location line = %v:\n%s", i+1, hasLoc, r.prompt)
		}
	}
	// Priority: storyboard, previous shot, cast, location — capped at 7.
	for i, want := range []string{
		"storyboard.jpg,taras.jpg,lysytsia.jpg,lis.jpg",
		"storyboard.jpg,previous_shot.jpg,taras.jpg,lysytsia.jpg,lis.jpg",
		"storyboard.jpg,previous_shot.jpg,taras.jpg,lysytsia.jpg,oleh.jpg,marta.jpg,iryna.jpg",
	} {
		if got := vids[i].names(); got != want {
			t.Fatalf("clip %d refs %s, want %s", i+1, got, want)
		}
	}
	if strings.Contains(vids[2].prompt, "@bohdan") || !strings.Contains(vids[2].prompt, "@iryna is Ірина") {
		t.Fatalf("clip 3 prompt must name only the references sent:\n%s", vids[2].prompt)
	}
	// Sequential: each clip is submitted only after the previous one was
	// downloaded, and gets that clip's LAST frame (red, then green).
	for i := 1; i < 3; i++ {
		done := gl.fetchedAt(vids[i-1].file)
		if done.IsZero() || !vids[i].at.After(done) {
			t.Fatalf("clip %d submitted at %v, before clip %d finished (%v)", i+1, vids[i].at, i, done)
		}
	}
	if c := avgRGB(t, vids[1].ref("previous_shot.jpg")); !nearRGB(c, 255, 0, 0, 30) {
		t.Fatalf("clip 2 previous_shot colour %v, want the red last frame of clip 1", c)
	}
	if c := avgRGB(t, vids[2].ref("previous_shot.jpg")); !nearRGB(c, 0, 128, 0, 30) {
		t.Fatalf("clip 3 previous_shot colour %v, want the green last frame of clip 2", c)
	}
	for _, s := range p.Scenes {
		fileExists(t, e.Abs(id, LastFrameRel(s.ID)))
		if s.Video.Status != StatusDone || SceneState(s) != "shot" {
			t.Fatalf("scene %s video %+v", s.ID, s.Video)
		}
	}
	sawChain := false
	for _, n := range notes() {
		sawChain = sawChain || n == "Знімаємо сцену 2 (ланцюжок)…"
	}
	if !sawChain {
		t.Fatalf("job messages %q", notes())
	}

	// ---- non-Omni model: the scene frame (panel 1) is animated as before;
	// meanwhile a storyboard regeneration replaces board + frame and drops the clip.
	_ = env.store.Update(func(c *config.Config) { c.Video.GlabsModel = "veo_31_fast" })
	s1, s2, s3 := p.Scenes[0], p.Scenes[1], p.Scenes[2]
	must(t, e.Regen(id, RegenVideo, s2.ID, ""))
	must(t, e.Regen(id, RegenFrame, s1.ID, ""))
	p = waitFor(t, e, id, "regens", func(p Project) bool {
		a, b := p.Scenes[0], p.Scenes[1]
		return b.Video.Status == StatusDone && b.Video.Path != s2.Video.Path && a.Board.Status == StatusDone && a.Board.Path != s1.Board.Path
	})
	vids = gl.requests("video")
	r := vids[len(vids)-1]
	frame, err := os.ReadFile(e.Abs(id, s2.Frame.Path))
	must(t, err)
	if r.mode != "start_image" || r.model != "veo_31_fast" || r.length != 8 || len(r.refs) != 1 || r.refs[0].name != "" ||
		!bytes.Equal(r.refs[0].data, frame) || !strings.Contains(r.prompt, "Keep every character exactly as in the first frame") {
		t.Fatalf("veo fallback request: mode %s model %s length %d refs %q", r.mode, r.model, r.length, r.names())
	}
	a := p.Scenes[0]
	if a.Frame.Path == s1.Frame.Path || a.Frame.Status != StatusDone || a.Video.Status != StatusEmpty || a.Video.Path != "" {
		t.Fatalf("storyboard regen: frame %+v video %+v", a.Frame, a.Video)
	}
	// Scene 3's clip continued the old clip of scene 2 → flagged as outdated.
	if c := p.Scenes[2].Video; c.Status != StatusDone || !c.Stale {
		t.Fatalf("scene 3 clip after re-shooting scene 2: %+v", c)
	}

	// ---- the user's own frame wins over the storyboard
	img := filepath.Join(t.TempDir(), "mine.jpg")
	must(t, os.WriteFile(img, testImage, 0o644))
	must(t, e.ImportImage(id, RegenFrame, s3.ID, img))
	p, _ = e.Get(id)
	if b := p.Scenes[2].Board; b.Path != "" || AssetState(b) != "empty" {
		t.Fatalf("board after a frame upload: %+v", b)
	}

	// ---- single-frame mode: a new frame drops the stale storyboard
	must(t, e.Edit(id, func(p *Project) { p.Input.StoryboardMode = "" }))
	must(t, e.Regen(id, RegenFrame, s2.ID, ""))
	p = waitFor(t, e, id, "single frame", func(p Project) bool {
		return p.Scenes[1].Frame.Status == StatusDone && p.Scenes[1].Frame.Path != s2.Frame.Path
	})
	if b := p.Scenes[1].Board; b.Path != "" || b.Status != StatusEmpty {
		t.Fatalf("board after a single frame: %+v", b)
	}
	imgs := gl.requests("image")
	if last := imgs[len(imgs)-1]; strings.Contains(last.prompt, "storyboard") {
		t.Fatalf("single mode drew a storyboard: %s", last.prompt)
	}
}

// TestChainWithoutBoard: Omni Flash without storyboards renders start_image;
// with ChainFrames the frame leads as "first_frame" next to the previous shot.
func TestChainWithoutBoard(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, gl := newSBEnv(t, func(c *config.Config) { c.Video.Length = 6 })
	e := env.e
	p := castProject(t, e, func(in *Input) {}, []int{0}, []int{0, 1})
	id := p.ID
	frame := filepath.Join(t.TempDir(), "frame.jpg")
	must(t, os.WriteFile(frame, testImage, 0o644))
	for _, s := range p.Scenes {
		must(t, e.ImportImage(id, RegenFrame, s.ID, frame))
	}

	must(t, e.Run(id, StageVideos, false, ""))
	mustJobDone(t, waitJob(t, e, id))
	vids := gl.requests("video")
	if len(vids) != 2 {
		t.Fatalf("video requests %d", len(vids))
	}
	for _, r := range vids {
		if r.mode != "start_image" || r.model != "omni_flash" || r.length != 6 || len(r.refs) != 1 || r.refs[0].name != "" {
			t.Fatalf("unchained omni request: %+v", r)
		}
	}
	if _, err := os.Stat(e.Abs(id, LastFrameRel(p.Scenes[0].ID))); !os.IsNotExist(err) {
		t.Fatal("last frames are only kept when chaining")
	}

	must(t, e.Edit(id, func(p *Project) { p.Input.ChainFrames = true }))
	must(t, e.Run(id, StageVideos, true, ""))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	vids = gl.requests("video")[2:]
	if len(vids) != 2 {
		t.Fatalf("chained requests %d", len(vids))
	}
	if r := vids[0]; r.mode != "start_image" || len(r.refs) != 1 { // first scene: nothing to continue from
		t.Fatalf("chain head: %+v", r)
	}
	r := vids[1]
	if r.mode != "components" || r.names() != "first_frame.jpg,previous_shot.jpg,taras.jpg,lysytsia.jpg,lis.jpg" {
		t.Fatalf("chained request mode %s refs %s", r.mode, r.names())
	}
	if !bytes.Equal(r.ref("first_frame.jpg"), testImage) || !vids[1].at.After(gl.fetchedAt(vids[0].file)) {
		t.Fatal("first_frame must be the scene frame and the clip must wait for the previous one")
	}
	// Clip 3 of the mock (yellow end) was the first chained clip.
	if c := avgRGB(t, r.ref("previous_shot.jpg")); !nearRGB(c, 255, 255, 0, 30) {
		t.Fatalf("previous_shot colour %v", c)
	}
	for _, want := range []string{"@first_frame is the opening frame of this shot", "@previous_shot is the final frame of the previous shot",
		"@lysytsia is Лисиця"} {
		if !strings.Contains(r.prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, r.prompt)
		}
	}
	if strings.Contains(r.prompt, "@storyboard") {
		t.Fatalf("no storyboard was sent:\n%s", r.prompt)
	}
}

// TestChainIgnoredForVeo: models that cannot take the previous shot keep
// rendering in parallel from the frame, even with ChainFrames on.
func TestChainIgnoredForVeo(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, gl := newSBEnv(t, func(c *config.Config) { c.Video.GlabsModel = "veo_31_fast"; c.Concurrency = 2 })
	e := env.e
	p := castProject(t, e, func(in *Input) { in.ChainFrames = true }, []int{0}, []int{1})
	frame := filepath.Join(t.TempDir(), "frame.jpg")
	must(t, os.WriteFile(frame, testImage, 0o644))
	for _, s := range p.Scenes {
		must(t, e.ImportImage(p.ID, RegenFrame, s.ID, frame))
	}
	must(t, e.Run(p.ID, StageVideos, false, ""))
	mustJobDone(t, waitJob(t, e, p.ID))
	vids := gl.requests("video")
	if len(vids) != 2 {
		t.Fatalf("video requests %d", len(vids))
	}
	for _, r := range vids {
		if r.mode != "start_image" || r.length != 8 || len(r.refs) != 1 {
			t.Fatalf("veo request %+v", r)
		}
	}
	if first := gl.fetchedAt(vids[0].file); first.IsZero() || !vids[1].at.Before(first) {
		t.Fatal("veo clips must not wait for each other")
	}
	if _, err := os.Stat(e.Abs(p.ID, LastFrameRel(p.Scenes[0].ID))); !os.IsNotExist(err) {
		t.Fatal("no last frames without a model that uses them")
	}
}

// bindsTo simulates G-Labs: "@kw" hits the first ref whose name (without
// extension) contains kw, case-insensitively.
func bindsTo(refs []videoRef, kw string) string {
	for _, r := range refs {
		if strings.Contains(refStem(r.img.Name), strings.ToLower(kw)) {
			return r.img.Name
		}
	}
	return ""
}

func TestBindSafeOrder(t *testing.T) {
	mk := func(names ...string) []videoRef {
		out := make([]videoRef, len(names))
		for i, n := range names {
			out[i].img.Name = n
		}
		return out
	}
	names := func(refs []videoRef) string {
		var s []string
		for _, r := range refs {
			s = append(s, r.img.Name)
		}
		return strings.Join(s, ",")
	}
	// No clash: priority order is kept.
	in := mk("storyboard.jpg", "previous_shot.jpg", "taras.png", "lysytsia.jpg", "lis.jpg")
	if got := names(bindSafe(in)); got != "storyboard.jpg,previous_shot.jpg,taras.png,lysytsia.jpg,lis.jpg" {
		t.Fatalf("order changed: %s", got)
	}
	// @tor ⊂ storyboard, @evi ⊂ previous_shot, @Board ⊂ storyboard (case-insensitive).
	out := bindSafe(mk("storyboard.jpg", "previous_shot.jpg", "tor.png", "evi.jpg", "Board.jpg", "taras.jpg"))
	if got := names(out); got != "tor.png,Board.jpg,storyboard.jpg,evi.jpg,previous_shot.jpg,taras.jpg" {
		t.Fatalf("clash order: %s", got)
	}
	for kw, want := range map[string]string{"storyboard": "storyboard.jpg", "previous_shot": "previous_shot.jpg", "tor": "tor.png",
		"evi": "evi.jpg", "board": "Board.jpg", "taras": "taras.jpg"} {
		if got := bindsTo(out, kw); got != want {
			t.Errorf("@%s binds to %s, want %s", kw, got, want)
		}
	}
}
