package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/reference"
)

// ---- fakes --------------------------------------------------------------------------------------

const remakeBreakdown = `{"seen":true,"title":"Як зробити хук","durationSeconds":95,"language":"en","aspect":"9:16",
"summary":"Поради про хуки для шортсів.","hook":{"end":6,"what":"Ведучий кричить «Stop scrolling»","why":"Пряме звертання"},
"structure":[{"start":0,"end":6,"name":"Хук","purpose":"зупинити"},{"start":6,"end":95,"name":"Поради","purpose":"цінність"}],
"scenes":[{"start":0,"end":6,"shot":"крупний план","visual":"Чоловік у червоній худі дивиться в камеру","onScreenText":"STOP","speaker":"on-camera","speech":"Stop scrolling!"},
{"start":6,"end":40,"shot":"середній план","visual":"Той самий чоловік за столом","speaker":"on-camera","speech":"Here is the trick"},
{"start":40,"end":95,"shot":"швидкі склейки","visual":"Приклади на телефоні","speaker":"voice-over","speech":"Try it today"}],
"cutsPerMinute":24,"avgShotSeconds":2.5,"people":"characters","audioMode":"invideo","narrationStyle":"Енергійно, на ти",
"music":"Лоу-фай","visualStyle":"Живе відео, яскраве світло","palette":"червоний","pacing":"Швидко","whyItWorks":["Пряме звертання"]}`

type fakeWatcher struct {
	label string
	mu    sync.Mutex
	calls int
	err   error
	reply string
}

func (w *fakeWatcher) Name() string  { return "fake" }
func (w *fakeWatcher) Label() string { return w.label }
func (w *fakeWatcher) Watch(ctx context.Context, r reference.Request) (string, error) {
	w.mu.Lock()
	w.calls++
	w.mu.Unlock()
	if w.err != nil {
		return "", w.err
	}
	if !strings.Contains(r.System, "WATCH and LISTEN") || r.URL != "https://www.youtube.com/watch?v=abcdefghijk" {
		return "", fmt.Errorf("unexpected request %q", r.URL)
	}
	return w.reply, nil
}

func (w *fakeWatcher) n() int { w.mu.Lock(); defer w.mu.Unlock(); return w.calls }

// scriptMock is the text model: the screenplay for the remake prompt, the
// planner's replies (harness) for the rest.
type scriptMock struct {
	mu      sync.Mutex
	prompts []string
}

var reExactlyScenes = regexp.MustCompile(`EXACTLY (\d+) scenes\. Each scene is ONE`)

func (m *scriptMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer txt-test" {
		w.WriteHeader(401)
		return
	}
	var req struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) == 0 {
		w.WriteHeader(400)
		return
	}
	content := req.Messages[0].Content
	var reply string
	switch {
	case strings.Contains(content, remakeMarker):
		m.mu.Lock()
		m.prompts = append(m.prompts, content)
		m.mu.Unlock()
		n := 3
		if mm := reExactlyScenes.FindStringSubmatch(content); mm != nil {
			n, _ = strconv.Atoi(mm[1])
		}
		scenes := make([]any, n)
		for i := range scenes {
			scenes[i] = map[string]any{"title": fmt.Sprintf("Момент %d", i+1), "picture": fmt.Sprintf("Жінка в зеленому светрі, кадр %d", i+1),
				"onScreenText": "", "speaker": "on-camera", "narration": fmt.Sprintf("Рядок номер %d.", i+1)}
		}
		scenes[0].(map[string]any)["onScreenText"] = "СТОП"
		b, _ := json.Marshal(map[string]any{"title": "Хук для кав'ярні", "language": "uk", "scenes": scenes,
			"notes": "Bright handheld vertical video, red accents.", "style": "cinematic", "genre": "", "tempo": "fast",
			"camera": "dynamic", "tone": "", "audioMode": "invideo", "content": "characters", "textOverlays": true, "aspect": "9:16"})
		reply = "```json\n" + string(b) + "\n```"
	case strings.Contains(content, "award-winning film director"):
		reply = bibleReply(content)
	case strings.Contains(content, "storyboard artist"):
		reply = shotsReply(content)
	default:
		reply = "OK"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": reply}}}})
}

func (m *scriptMock) last() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.prompts) == 0 {
		return ""
	}
	return m.prompts[len(m.prompts)-1]
}

type remakeEnv struct {
	*testEnv
	script  *scriptMock
	watcher *fakeWatcher
	probes  int
	meta    reference.Meta
	probErr error
}

func newRemakeEnv(t *testing.T, mut func(c *config.Config)) *remakeEnv {
	t.Helper()
	sm := &scriptMock{}
	srv := httptest.NewServer(sm)
	t.Cleanup(srv.Close)
	allow(srv)
	env := newEnv(t, func(c *config.Config) {
		c.Text.BaseURL = srv.URL + "/v1"
		c.Google.Keys = []string{"g-test"}
		if mut != nil {
			mut(c)
		}
	})
	re := &remakeEnv{testEnv: env, script: sm, watcher: &fakeWatcher{label: "Gemini · fake", reply: remakeBreakdown},
		meta: reference.Meta{ID: "abcdefghijk", Title: "Як зробити хук", Channel: "Канал", Duration: 95}}
	op, ot, ow := remakeProbe, remakeThumb, remakeWatchers
	t.Cleanup(func() { remakeProbe, remakeThumb, remakeWatchers = op, ot, ow })
	var mu sync.Mutex
	remakeProbe = func(ctx context.Context, _ *http.Client, id string) (reference.Meta, error) {
		mu.Lock()
		defer mu.Unlock()
		re.probes++
		return re.meta, re.probErr
	}
	remakeThumb = func(ctx context.Context, _ *http.Client, id, dst string) error {
		return os.WriteFile(dst, testImage, 0o644)
	}
	remakeWatchers = func(e *Engine, meta reference.Meta) []reference.Watcher { return []reference.Watcher{re.watcher} }
	return re
}

// ---- tests --------------------------------------------------------------------------------------

func TestRemakeStepByStep(t *testing.T) {
	env := newRemakeEnv(t, nil)
	e := env.e
	p, err := e.StartRemake(RemakeOptions{URL: "https://youtube.com/shorts/abcdefghijk", Wish: "Те саме, але про мою кав'ярню, героїня — жінка 30 років",
		Fidelity: FidelityExact, KeepLength: true, LanguageSet: true, NoProfile: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Reference.URL != "https://www.youtube.com/watch?v=abcdefghijk" || p.Reference.Fidelity != FidelityExact || p.Job == nil || p.Job.Kind != JobRemake {
		t.Fatalf("started: ref %+v job %+v", p.Reference, p.Job)
	}
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if p.Job.Kind != JobRemake || !strings.Contains(p.Job.Message, "Розбір і сценарій готові") {
		t.Fatalf("job = %+v", p.Job)
	}
	ref := p.Reference
	if ref.Stage != remakeScripted || ref.Scenes != 3 || ref.Duration != 95 || ref.Aspect != "9:16" || ref.Via != "Gemini · fake" ||
		ref.Thumb != "reference/thumb.jpg" || ref.Title != "Як зробити хук" || ref.Channel != "Канал" {
		t.Fatalf("reference = %+v", ref)
	}
	if !strings.Contains(ref.Breakdown, "Сцени (3):") || !strings.Contains(ref.Breakdown, "мова: англійська") {
		t.Fatalf("breakdown text:\n%s", ref.Breakdown)
	}
	if _, err := os.Stat(filepath.Join(e.Dir(p.ID), "reference", "breakdown.json")); err != nil {
		t.Fatal("breakdown.json missing")
	}
	// 95 s at 8 s clips: 12 scenes, the plan will have exactly that many.
	in := p.Input
	if in.Mode != ModeStory || in.TargetSeconds != 96 || in.Aspect != "9:16" || in.Language != "uk" || in.Tempo != "fast" ||
		in.Camera != "dynamic" || in.AudioMode != AudioInVideo || in.Voiceover || in.Content != ContentCharacters || in.Overlays != "on" {
		t.Fatalf("input = %+v", in)
	}
	if n, _, _ := e.Budget(p); n != 12 {
		t.Fatalf("plan budget = %d scenes", n)
	}
	if !strings.HasPrefix(in.Text, "Хук для кав'ярні\n\nСцена 1 · Момент 1\nКадр: Жінка в зеленому светрі, кадр 1\nТекст на екрані: «СТОП»\nГоворить у кадрі: «Рядок номер 1.»") ||
		strings.Count(in.Text, "Сцена ") != 12 {
		t.Fatalf("screenplay:\n%s", in.Text)
	}
	if !strings.HasPrefix(in.Notes, "Exact remake") || !strings.Contains(in.Notes, "red accents") || p.Title != "Хук для кав'ярні" {
		t.Fatalf("notes %q title %q", in.Notes, p.Title)
	}
	prompt := env.script.last()
	for _, want := range []string{"FIDELITY: EXACT COPY", "EXACTLY 12 scenes", "Сцени (3):", "про мою кав'ярню", "Never recreate a real, identifiable person",
		"the language of the author's wish", `"cinematic" — `} {
		if !strings.Contains(prompt, want) {
			t.Errorf("script prompt lacks %q", want)
		}
	}
	if env.watcher.n() != 1 || env.probes != 1 {
		t.Fatalf("watched %d, probed %d", env.watcher.n(), env.probes)
	}
	if got := queueJobLabel(JobRemake, false); got != "Перезйомка" {
		t.Fatalf("label = %q", got)
	}
}

func TestRemakeRewriteFromEditedBreakdown(t *testing.T) {
	env := newRemakeEnv(t, nil)
	e := env.e
	p, err := e.StartRemake(RemakeOptions{URL: "abcdefghijk", Fidelity: FidelityStructure, TargetSeconds: 40, LanguageSet: true, Language: "en", NoProfile: true})
	if err != nil {
		t.Fatal(err)
	}
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if p.Input.Language != "en" || p.Input.TargetSeconds != 40 {
		t.Fatalf("explicit language and length: %+v", p.Input)
	}
	if !strings.Contains(env.script.last(), "FIDELITY: STRUCTURE AND PACING ONLY") || !strings.Contains(env.script.last(), "EXACTLY 5 scenes") ||
		!strings.Contains(env.script.last(), "Language of the narration, dialogue, title and on-screen text: English.") {
		t.Fatal("structure-only prompt with 5 scenes in English expected")
	}
	if err := e.Edit(p.ID, func(q *Project) { q.Reference.Breakdown += "\nМоя правка: героїня — бариста." }); err != nil {
		t.Fatal(err)
	}
	if err := e.RewriteRemake(p.ID); err != nil {
		t.Fatal(err)
	}
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if !strings.Contains(env.script.last(), "Моя правка: героїня — бариста.") || env.watcher.n() != 1 {
		t.Fatalf("rewrite must use the edited text without watching again (watched %d)", env.watcher.n())
	}
	if p.Reference.Stage != remakeScripted || !strings.Contains(p.Reference.Breakdown, "бариста") {
		t.Fatalf("after rewrite: %+v", p.Reference)
	}
	// Watching again replaces the edited breakdown.
	if err := e.ReanalyzeRemake(p.ID); err != nil {
		t.Fatal(err)
	}
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if env.watcher.n() != 2 || strings.Contains(p.Reference.Breakdown, "бариста") {
		t.Fatalf("reanalyse: watched %d", env.watcher.n())
	}
}

func TestRemakeErrorsAndResume(t *testing.T) {
	env := newRemakeEnv(t, nil)
	e := env.e
	env.probErr = &reference.Error{Kind: reference.KindPrivate, Msg: "Це приватне відео — його бачить лише автор."}
	p, err := e.StartRemake(RemakeOptions{URL: "https://youtu.be/abcdefghijk", KeepLength: true, NoProfile: true})
	if err != nil {
		t.Fatal(err)
	}
	p = waitJob(t, e, p.ID)
	if p.Job.Status != JobError || !strings.Contains(p.Job.Error, "приватне") {
		t.Fatalf("job = %+v", p.Job)
	}
	// The watcher fails once with a quota error: «Повторити» continues.
	env.probErr = nil
	env.watcher.err = &reference.Error{Kind: reference.KindQuota, Msg: "Ліміт"}
	if err := e.Retry(p.ID); err != nil {
		t.Fatal(err)
	}
	p = waitJob(t, e, p.ID)
	if p.Job.Status != JobError || p.Reference.Stage != "" {
		t.Fatalf("quota: %+v %+v", p.Job, p.Reference)
	}
	env.watcher.err = nil
	if err := e.Retry(p.ID); err != nil {
		t.Fatal(err)
	}
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if p.Reference.Stage != remakeScripted {
		t.Fatalf("resumed: %+v", p.Reference)
	}

	// Bad links and missing services are refused before any project exists.
	before := len(e.List())
	if _, err := e.StartRemake(RemakeOptions{URL: "https://vimeo.com/1"}); reference.KindOf(err) != reference.KindBadURL {
		t.Fatalf("bad url: %v", err)
	}
	if err := env.store.Update(func(c *config.Config) { c.Google.Keys = nil; c.Remake.Analysis = config.RemakeGoogle }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.StartRemake(RemakeOptions{URL: "abcdefghijk"}); err == nil || !strings.Contains(err.Error(), "Google AI Studio") {
		t.Fatalf("no service: %v", err)
	}
	if len(e.List()) != before {
		t.Fatal("a refused remake must not leave a project behind")
	}
}

func TestRemakeFallsBackToTheNextWatcher(t *testing.T) {
	env := newRemakeEnv(t, nil)
	first := &fakeWatcher{label: "Gemini · fake", err: &reference.Error{Kind: reference.KindQuota, Msg: "Ліміт"}}
	second := &fakeWatcher{label: "текстовий сервіс · fake", reply: remakeBreakdown}
	remakeWatchers = func(e *Engine, meta reference.Meta) []reference.Watcher { return []reference.Watcher{first, second} }
	p, err := env.e.StartRemake(RemakeOptions{URL: "abcdefghijk", KeepLength: true, NoProfile: true})
	if err != nil {
		t.Fatal(err)
	}
	p = waitJob(t, env.e, p.ID)
	mustJobDone(t, p)
	if first.n() != 1 || second.n() != 1 || p.Reference.Via != "текстовий сервіс · fake" {
		t.Fatalf("fallback: %d/%d via %q", first.n(), second.n(), p.Reference.Via)
	}
	// Both fail for service reasons: the first one's error is shown.
	first.err = &reference.Error{Kind: reference.KindQuota, Msg: "Ліміт Google"}
	second.err = &reference.Error{Kind: reference.KindNetwork, Msg: "Немає з'єднання"}
	p, _ = env.e.StartRemake(RemakeOptions{URL: "abcdefghijk", KeepLength: true, NoProfile: true})
	p = waitJob(t, env.e, p.ID)
	if p.Job.Status != JobError || p.Job.Error != "Ліміт Google" {
		t.Fatalf("both failed: %+v", p.Job)
	}
	second.err = nil
	// A problem with the video itself is not retried elsewhere.
	first.err = &reference.Error{Kind: reference.KindUnlisted, Msg: "Лише за посиланням"}
	p, _ = env.e.StartRemake(RemakeOptions{URL: "abcdefghijk", KeepLength: true, NoProfile: true})
	p = waitJob(t, env.e, p.ID)
	if p.Job.Status != JobError || second.n() != 2 {
		t.Fatalf("video error must stop: %+v, second %d", p.Job, second.n())
	}
}

func TestRemakeAutopilotTakesOver(t *testing.T) {
	env := newRemakeEnv(t, nil)
	e := env.e
	p, err := e.StartRemake(RemakeOptions{URL: "abcdefghijk", KeepLength: true, NoProfile: true, Autopilot: true})
	if err != nil {
		t.Fatal(err)
	}
	p = waitFor(t, e, p.ID, "the autopilot", func(p Project) bool {
		return p.Job != nil && (p.Job.Kind == JobAutopilot || p.Job.Status != JobRunning)
	})
	if p.Job.Kind != JobAutopilot {
		t.Fatalf("job = %+v", p.Job)
	}
	e.Cancel(p.ID)
	p = waitJob(t, e, p.ID)
	if p.Job.Kind != JobAutopilot || p.Reference.Stage != remakeScripted {
		t.Fatalf("after cancel: %+v", p.Job)
	}
}

func TestRemakeService(t *testing.T) {
	env := newRemakeEnv(t, nil)
	e := env.e
	if s := e.RemakeService(); !s.Ready || s.Via != "google" || s.Model != "gemini-3.8-flash" || !strings.Contains(s.Note, "Gemini") {
		t.Fatalf("keys: %+v", s)
	}
	set := func(fn func(c *config.Config)) {
		if err := env.store.Update(fn); err != nil {
			t.Fatal(err)
		}
	}
	set(func(c *config.Config) { c.Remake.Model = "gemini-3.9-pro" })
	if s := e.RemakeService(); s.Model != "gemini-3.9-pro" {
		t.Fatalf("own model: %+v", s)
	}
	set(func(c *config.Config) { c.Google.Keys = nil })
	if s := e.RemakeService(); !s.Ready || s.Via != "text" {
		t.Fatalf("auto without keys → text: %+v", s)
	}
	set(func(c *config.Config) { c.Remake.Analysis = config.RemakeGoogle })
	if s := e.RemakeService(); s.Ready || !strings.Contains(s.Note, "Google AI Studio") {
		t.Fatalf("google without keys: %+v", s)
	}
	set(func(c *config.Config) { c.Remake.Analysis = config.RemakeText; c.Text.Provider = "google" })
	if s := e.RemakeService(); s.Ready {
		t.Fatalf("text not configured: %+v", s)
	}
	// The real watchers follow the choice.
	set(func(c *config.Config) {
		c.Remake.Analysis = ""
		c.Google.Keys = []string{"k"}
		c.Text.Provider = "openai"
	})
	ws := e.watchers(reference.Meta{})
	if len(ws) != 2 || ws[0].Name() != "google" || ws[1].Name() != "text" {
		t.Fatalf("auto watchers = %v", ws)
	}
	set(func(c *config.Config) { c.Remake.Analysis = config.RemakeText })
	if ws := e.watchers(reference.Meta{}); len(ws) != 1 || ws[0].Name() != "text" {
		t.Fatalf("text watchers = %v", ws)
	}
}

func TestParseRemakeReply(t *testing.T) {
	r, ok := parseRemakeReply("Ось:\n```json\n{\"title\":\"T\",\"scenes\":[{\"visual\":\"A\",\"narration\":\"Б\"},{\"title\":\"порожня\"}],\"textOverlays\":\"так\",}\n```")
	if !ok || len(r.Scenes) != 1 || r.Scenes[0].Picture != "A" || !r.TextOverlays || !r.OverlaysSet {
		t.Fatalf("reply = %+v %v", r, ok)
	}
	if _, ok := parseRemakeReply("no json"); ok {
		t.Fatal("prose must fail")
	}
	if errors.Is(errNoRemake, ErrBusy) {
		t.Fatal("sanity")
	}
}
