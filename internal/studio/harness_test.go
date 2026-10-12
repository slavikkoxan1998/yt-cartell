package studio

// Test harness: every HTTP request goes through a guard transport that only
// lets requests reach the httptest mock servers of this test binary (and maps
// api.elevenlabs.io onto the ElevenLabs mock), so no real/paid service can be
// hit. The config store's settings file is redirected to a temp folder.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
)

var (
	allowMu   sync.Mutex
	allowed   = map[string]bool{}
	elevenURL *url.URL
	eleven    *elevenMock

	testFF    media.FF
	testImage []byte // small JPEG
	testClip  []byte // 2 s mp4 with sound
	testVoice []byte // 1.5 s mp3
	mediaErr  error
)

func allow(srv *httptest.Server) {
	u, _ := url.Parse(srv.URL)
	allowMu.Lock()
	allowed[u.Host] = true
	allowMu.Unlock()
}

type guard struct{ base http.RoundTripper }

func (g *guard) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() == "api.elevenlabs.io" && elevenURL != nil {
		r2 := r.Clone(r.Context())
		r2.URL.Scheme, r2.URL.Host, r2.Host = elevenURL.Scheme, elevenURL.Host, ""
		return g.base.RoundTrip(r2)
	}
	allowMu.Lock()
	ok := allowed[r.URL.Host]
	allowMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("test guard: blocked request to %s", r.URL.Host)
	}
	return g.base.RoundTrip(r)
}

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "ytc-studio-test-")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	// config.Store.Update persists to config.Path(): keep it out of the real profile.
	os.Setenv("AppData", filepath.Join(tmp, "appdata"))
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	if runtime.GOOS == "darwin" {
		os.Setenv("HOME", filepath.Join(tmp, "home"))
	}
	if !strings.HasPrefix(config.Path(), tmp) {
		fmt.Println("refusing to run: settings path is not redirected:", config.Path())
		os.Exit(1)
	}

	http.DefaultTransport = &guard{base: http.DefaultTransport}
	eleven = &elevenMock{}
	esrv := httptest.NewServer(eleven)
	allow(esrv)
	elevenURL, _ = url.Parse(esrv.URL)

	mediaErr = makeMedia(filepath.Join(tmp, "media"))

	code := m.Run()
	esrv.Close()
	os.RemoveAll(tmp)
	os.Exit(code)
}

func makeMedia(dir string) error {
	ff, err := media.Locate(config.Default())
	if err != nil || !ff.OK() {
		return fmt.Errorf("ffmpeg not available: %v", err)
	}
	testFF = ff
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	jobs := []struct {
		out  string
		args []string
		dst  *[]byte
	}{
		{"img.jpg", []string{"-f", "lavfi", "-i", "color=c=0x3366aa:s=320x180", "-frames:v", "1"}, &testImage},
		{"clip.mp4", []string{"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
			"-t", "2", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest"}, &testClip},
		{"voice.mp3", []string{"-f", "lavfi", "-i", "sine=frequency=300:sample_rate=44100", "-t", "1.5", "-c:a", "libmp3lame", "-b:a", "64k"}, &testVoice},
	}
	for _, j := range jobs {
		out := filepath.Join(dir, j.out)
		if err := ff.Run(ctx, "", append(j.args, out)...); err != nil {
			return err
		}
		b, err := os.ReadFile(out)
		if err != nil {
			return err
		}
		*j.dst = b
	}
	return nil
}

func needMedia(t *testing.T) {
	t.Helper()
	if mediaErr != nil {
		t.Skip(mediaErr)
	}
}

// ---- ElevenLabs mock ---------------------------------------------------------------------

type ttsCall struct{ voice, text string }

type elevenMock struct {
	mu        sync.Mutex
	calls     []ttsCall
	dialogues [][]ttsCall // text-to-dialogue requests (multivoice_test.go)
}

func (m *elevenMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("xi-api-key") != "el-test" {
		w.WriteHeader(401)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v2/voices":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"voices":[
			{"voice_id":"v-adam","name":"Adam","category":"premade","labels":{"gender":"male"}},
			{"voice_id":"v-bella","name":"Bella","category":"premade","labels":{"gender":"female"}},
			{"voice_id":"v-pro","name":"Aaron Pro","category":"professional","labels":{"gender":"female"}}
		],"has_more":false}`)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/user/subscription":
		fmt.Fprint(w, `{"tier":"free","character_limit":10000,"character_count":100}`)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/text-to-dialogue/with-timestamps":
		m.dialogue(w, r) // multivoice_test.go
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/text-to-speech/"):
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.calls = append(m.calls, ttsCall{strings.TrimPrefix(r.URL.Path, "/v1/text-to-speech/"), body.Text})
		m.mu.Unlock()
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write(testVoice)
	default:
		http.NotFound(w, r)
	}
}

func (m *elevenMock) voiceFor(text string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.calls) - 1; i >= 0; i-- {
		if m.calls[i].text == text {
			return m.calls[i].voice
		}
	}
	return ""
}

// ---- OpenAI-compatible text mock -------------------------------------------------------

type textMock struct {
	mu           sync.Mutex
	bibles       int
	shots        int
	block        bool          // bible requests hang until the client gives up
	started      chan struct{} // signalled when a bible request arrives
	shotPrompts  []string
	biblePrompts []string
}

var (
	reExactly = regexp.MustCompile(`EXACTLY (\d+) scenes`)
	reIndex   = regexp.MustCompile(`(?m)^index: (\d+)$`)
	reWish    = regexp.MustCompile(`Extra direction from the user for this rewrite: (.+)`)
)

func (m *textMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer txt-test" {
		w.WriteHeader(401)
		return
	}
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
		fmt.Fprint(w, `{"data":[{"id":"mock-a"},{"id":"mock-b"}]}`)
		return
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
	default:
		http.NotFound(w, r)
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
	content := req.Messages[0].Content
	var reply string
	switch {
	case strings.Contains(content, "award-winning film director"):
		m.mu.Lock()
		m.bibles++
		m.biblePrompts = append(m.biblePrompts, content)
		block, started := m.block, m.started
		m.mu.Unlock()
		if started != nil {
			select {
			case started <- struct{}{}:
			default:
			}
		}
		if block {
			<-r.Context().Done()
			return
		}
		reply = bibleReply(content)
	case strings.Contains(content, "storyboard artist"):
		m.mu.Lock()
		m.shots++
		m.shotPrompts = append(m.shotPrompts, content)
		m.mu.Unlock()
		reply = shotsReply(content)
	default:
		reply = "OK"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": reply}}},
	})
}

func bibleReply(content string) string {
	n := 3
	if mm := reExactly.FindStringSubmatch(content); mm != nil {
		n, _ = strconv.Atoi(mm[1])
	}
	scenes := make([]any, n)
	for i := range scenes {
		sp := "narrator"
		if i == 1 {
			sp = "Лисиця"
		}
		scene := map[string]any{
			"title":      fmt.Sprintf("Сцена %d", i+1),
			"narration":  fmt.Sprintf("Модельний рядок номер %d.", i+1),
			"speaker":    sp,
			"characters": []any{"Тарас"},
			"location":   "Осінній ліс",
		}
		if strings.Contains(content, `"parts": [`) { // multi-voice (multivoice_test.go)
			scene["parts"] = multiVoiceParts(i)
		}
		scenes[i] = scene
	}
	b, _ := json.MarshalIndent(map[string]any{
		"title":      "Стежка додому",
		"logline":    "Хлопчик шукає дорогу додому.",
		"styleGuide": "Soft autumn palette, warm light.",
		"characters": []any{
			map[string]any{"name": "Тарас", "role": "lost boy", "gender": "male", "description": "A 7-year-old boy in a red jacket."},
			map[string]any{"name": "Лисиця", "role": "guide", "gender": "female", "description": "A small orange fox."},
			map[string]any{"role": "nameless extra"},
		},
		"locations": []any{map[string]any{"name": "Осінній ліс", "description": "Autumn forest with golden leaves."}},
		"scenes":    scenes,
	}, "", "  ")
	return "Ось план фільму:\n```json\n" + string(b) + "\n```\nГотово!"
}

func shotsReply(content string) string {
	_, input, _ := strings.Cut(content, "### INPUT\n")
	wish := ""
	if mm := reWish.FindStringSubmatch(input); mm != nil {
		wish = " | wish: " + mm[1]
	}
	var shots []any
	for _, mm := range reIndex.FindAllStringSubmatch(input, -1) {
		n, _ := strconv.Atoi(mm[1])
		shots = append(shots, map[string]any{
			"index":        n,
			"shot":         "medium shot",
			"imagePrompt":  fmt.Sprintf("Medium shot of @taras in @osinnii_lis, scene %d%s", n, wish),
			"motionPrompt": fmt.Sprintf("Slow push-in, leaves fall, scene %d.", n),
		})
	}
	// Wrapped in an object: the engine must pick the first array member.
	b, _ := json.Marshal(map[string]any{"note": "ok", "shots": shots})
	return string(b)
}

// ---- G-Labs mock ---------------------------------------------------------------------------

type glabsReq struct {
	kind, prompt, model, aspect, mode string
	refNames                          []string
	refs                              int
}

type glabsMock struct {
	mu     sync.Mutex
	next   int
	tasks  map[string]string // id → file name
	reqs   []glabsReq
	failOn string // prompts containing this fail
}

func newGlabsMock() *glabsMock { return &glabsMock{tasks: map[string]string{}} }

func (m *glabsMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/files/") {
		name := strings.TrimPrefix(path, "/api/files/")
		if strings.HasSuffix(name, ".mp4") {
			w.Header().Set("Content-Type", "video/mp4")
			w.Write(testClip)
		} else {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(testImage)
		}
		return
	}
	if path == "/api/health" {
		fmt.Fprint(w, `{"status":"ok","uptime":125}`)
		return
	}
	if r.Header.Get("X-API-Key") != "gl-test" {
		w.WriteHeader(401)
		return
	}
	switch {
	case path == "/api/tasks":
		fmt.Fprint(w, `[]`)
	case r.Method == http.MethodPost && (path == "/api/image/generate" || path == "/api/video/generate"):
		var body struct {
			Prompt string            `json:"prompt"`
			Model  string            `json:"model"`
			Aspect string            `json:"aspect_ratio"`
			Mode   string            `json:"mode"`
			Refs   []json.RawMessage `json:"reference_images"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		req := glabsReq{kind: "image", prompt: body.Prompt, model: body.Model, aspect: body.Aspect, mode: body.Mode, refs: len(body.Refs)}
		ext := ".jpg"
		if path == "/api/video/generate" {
			req.kind, ext = "video", ".mp4"
		}
		for _, raw := range body.Refs {
			var named struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(raw, &named) == nil && named.Name != "" {
				req.refNames = append(req.refNames, named.Name)
			}
		}
		m.mu.Lock()
		m.next++
		id := fmt.Sprintf("t%d", m.next)
		m.reqs = append(m.reqs, req)
		fail := m.failOn != "" && strings.Contains(body.Prompt, m.failOn)
		if fail {
			m.tasks[id] = ""
		} else {
			m.tasks[id] = id + ext
		}
		m.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"task_id":%q}`, id)
	case strings.HasPrefix(path, "/api/status/"):
		id := strings.TrimPrefix(path, "/api/status/")
		m.mu.Lock()
		file, ok := m.tasks[id]
		m.mu.Unlock()
		switch {
		case !ok:
			http.NotFound(w, r)
		case file == "":
			fmt.Fprint(w, `{"status":"failed","error_code":400,"error":"prompt rejected"}`)
		default:
			fmt.Fprintf(w, `{"status":"completed","results":["/api/files/%s"]}`, file)
		}
	default:
		http.NotFound(w, r)
	}
}

func (m *glabsMock) requests(kind string) []glabsReq {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []glabsReq
	for _, r := range m.reqs {
		if r.kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// ---- engine fixture ------------------------------------------------------------------------

type testEnv struct {
	e     *Engine
	store *config.Store
	text  *textMock
	gl    *glabsMock
}

func newEnv(t *testing.T, mut func(c *config.Config)) *testEnv {
	t.Helper()
	tm := &textMock{started: make(chan struct{}, 4)}
	ts := httptest.NewServer(tm)
	allow(ts)
	gm := newGlabsMock()
	gs := httptest.NewServer(gm)
	allow(gs)

	c := config.Default()
	c.DataDir = t.TempDir()
	c.Text = config.Text{Provider: "openai", BaseURL: ts.URL + "/v1", APIKey: "txt-test", Model: "mock-model"}
	c.Image.Provider = "glabs"
	c.Video = config.Video{Provider: "glabs", GlabsModel: "veo_31_fast", Resolution: "720p", Length: 8}
	c.Glabs = config.Glabs{BaseURL: gs.URL, APIKey: "gl-test"}
	c.TTS = config.TTS{Provider: "elevenlabs", APIKey: "el-test"}
	c.Concurrency = 2
	if mut != nil {
		mut(&c)
	}
	store := config.NewStoreWith(c)
	e, err := NewEngine(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		waitIdle(t, e)
		_ = e.Flush()
		ts.Close()
		gs.Close()
	})
	return &testEnv{e: e, store: store, text: tm, gl: gm}
}

// waitIdle waits until no project has a running job or a busy asset.
func waitIdle(t *testing.T, e *Engine) {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		busy := false
		for _, s := range e.List() {
			p, ok := e.Get(s.ID)
			if !ok {
				continue
			}
			if p.Job != nil && p.Job.Status == JobRunning {
				busy = true
			}
			for _, sc := range p.Scenes {
				if SceneState(sc) == "running" {
					busy = true
				}
			}
			for _, c := range p.Characters {
				if AssetState(c.Image) == "running" {
					busy = true
				}
			}
		}
		if !busy {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("engine did not become idle")
}

func waitFor(t *testing.T, e *Engine, id string, what string, cond func(p Project) bool) Project {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	for {
		p, ok := e.Get(id)
		if !ok {
			t.Fatalf("project %s vanished while waiting for %s", id, what)
		}
		if cond(p) {
			return p
		}
		if time.Now().After(deadline) {
			b, _ := json.MarshalIndent(p.Job, "", " ")
			t.Fatalf("timeout waiting for %s (job %s)", what, b)
		}
		time.Sleep(40 * time.Millisecond)
	}
}

// waitJob waits for the current job to end and returns the project.
func waitJob(t *testing.T, e *Engine, id string) Project {
	t.Helper()
	return waitFor(t, e, id, "job end", func(p Project) bool { return p.Job != nil && p.Job.Status != JobRunning })
}

func mustJobDone(t *testing.T, p Project) {
	t.Helper()
	if p.Job == nil || p.Job.Status != JobDone {
		b, _ := json.Marshal(p.Job)
		t.Fatalf("job not done: %s", b)
	}
}
