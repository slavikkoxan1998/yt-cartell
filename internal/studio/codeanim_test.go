package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
)

// ---- a fake renderer -------------------------------------------------------------------------

// fakeCoder renders a real mp4 of the spec's duration with FFmpeg (no
// browser) and records every spec; block makes Render wait for its ctx.
type fakeCoder struct {
	mu      sync.Mutex
	renders [][]byte
	stills  []float64
	block   bool
	started chan struct{}
	fail    string // Render fails for specs containing this
}

func (f *fakeCoder) Grammars() []CodeGrammar {
	return []CodeGrammar{
		{ID: "t3_finance_chart", Label: "Фінансовий графік", Description: "Economist-style chart.", Schema: `data.title, data.series`,
			Kinds: []string{"title", "bar", "line", "highlight", "number"}, Example: json.RawMessage(`{"grammar":"t3_finance_chart","duration":6,"cues":[{"at":0,"kind":"title"},{"at":1,"kind":"bar"}]}`)},
		{ID: "y5_kinetic_type", Label: "Кінетичний текст", Description: "Kinetic type.", Kinds: []string{"title", "point", "number", "highlight"},
			Example: json.RawMessage(`{"grammar":"y5_kinetic_type","duration":5,"cues":[{"at":0,"kind":"title","text":"Hi"}]}`)},
		{ID: "map", Label: "Мапа", Description: "Vintage map with sprites.", Kinds: []string{"title", "region", "route", "sprite"}, Sprites: true,
			Example:   json.RawMessage(`{"grammar":"map","duration":5,"cues":[{"at":0,"kind":"title","text":"1803"}]}`),
			Reference: "## Spec REF-MAP-FULL", Examples: []json.RawMessage{json.RawMessage(`{"grammar":"map","duration":16,"cues":[{"at":1,"kind":"sprite","sprite":"napoleon"}]}`), json.RawMessage(`{"grammar":"map","duration":9,"cues":[]}`)}},
		{ID: "y4_storytime", Label: "Історія", Description: "Storytime card.", Kinds: []string{"title", "point"},
			Example: json.RawMessage(`{"grammar":"y4_storytime","duration":5,"cues":[{"at":0,"kind":"title","text":"Hi"}]}`)},
	}
}

func (f *fakeCoder) Available(context.Context) (string, error) { return "fake-browser", nil }

func (f *fakeCoder) Render(ctx context.Context, spec []byte, out string, opt CodeRenderOptions) error {
	f.mu.Lock()
	f.renders = append(f.renders, append([]byte(nil), spec...))
	block, started, fail := f.block, f.started, f.fail
	f.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	if fail != "" && bytes.Contains(spec, []byte(fail)) {
		return errors.New("page error: clip.js: boom")
	}
	var s map[string]any
	if err := json.Unmarshal(spec, &s); err != nil {
		return err
	}
	// Pictures must resolve inside the assets folder.
	refs := map[string]bool{}
	imageRefs(s["cues"], refs)
	for r := range refs {
		if !isFile(filepath.Join(opt.AssetsDir, filepath.FromSlash(r))) {
			return fmt.Errorf("image %s not found", r)
		}
	}
	dur, _ := s["duration"].(float64)
	if opt.Progress != nil {
		opt.Progress(1, 2)
		opt.Progress(2, 2)
	}
	return testFF.Run(ctx, "", "-y", "-f", "lavfi", "-i", "color=c=0x224466:s=160x90:rate=30", "-t", strconv.FormatFloat(dur, 'f', 3, 64),
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-f", "mp4", out)
}

func (f *fakeCoder) Still(ctx context.Context, spec []byte, at float64, outPNG string, opt CodeRenderOptions) error {
	f.mu.Lock()
	f.stills = append(f.stills, at)
	f.mu.Unlock()
	img := image.NewRGBA(image.Rect(0, 0, 32, 18))
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return os.WriteFile(outPNG, buf.Bytes(), 0o644)
}

func (f *fakeCoder) lastSpec(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.renders) == 0 {
		t.Fatal("nothing rendered")
	}
	var s map[string]any
	must(t, json.Unmarshal(f.renders[len(f.renders)-1], &s))
	return s
}

func (f *fakeCoder) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.renders)
}

// ---- a text model that writes specs ----------------------------------------------------------

// codeMock answers the plan like the default mock and the spec writer per
// scene title: "ok" (valid), "repair" (invalid first, valid after the
// repair round), "bad" (always invalid).
type codeMock struct {
	mu      sync.Mutex
	mode    map[string]string
	prompts []string
	specs   int
	// delay holds every spec answer; inflight / maxInflight count the spec
	// requests answered at once; events is "start:<scene>" / "end:<scene>".
	delay       time.Duration
	inflight    int
	maxInflight int
	events      []string
}

var (
	reCodeDur   = regexp.MustCompile(`"duration" is EXACTLY ([0-9.]+)`)
	reCodeScene = regexp.MustCompile(`(?m)^Scene: (.+)$`)
)

func (m *codeMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if r.Header.Get("Authorization") != "Bearer txt-test" || json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Messages) != 1 {
		w.WriteHeader(400)
		return
	}
	content := req.Messages[0].Content
	var reply string
	switch {
	case strings.Contains(content, "award-winning film director"):
		reply = bibleReply(content)
	case strings.Contains(content, "storyboard artist"):
		reply = shotsReply(content)
	case strings.Contains(content, "motion designer"):
		if mm := reCodeScene.FindStringSubmatch(content); mm != nil {
			m.mu.Lock()
			down := m.mode[strings.TrimSpace(mm[1])] == "down"
			m.mu.Unlock()
			if down { // the text service fails for this scene
				w.WriteHeader(503)
				return
			}
		}
		scene := ""
		if mm := reCodeScene.FindStringSubmatch(content); mm != nil {
			scene = strings.TrimSpace(mm[1])
		}
		m.mu.Lock()
		m.prompts = append(m.prompts, content)
		m.specs++
		m.inflight++
		m.maxInflight = max(m.maxInflight, m.inflight)
		m.events = append(m.events, "start:"+scene)
		delay := m.delay
		m.mu.Unlock()
		time.Sleep(delay)
		reply = m.spec(content)
		m.mu.Lock()
		m.inflight--
		m.events = append(m.events, "end:"+scene)
		m.mu.Unlock()
	default:
		reply = "OK"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": reply}}}})
}

func (m *codeMock) spec(content string) string {
	dur := 6.0
	if mm := reCodeDur.FindStringSubmatch(content); mm != nil {
		dur, _ = strconv.ParseFloat(mm[1], 64)
	}
	title := ""
	if mm := reCodeScene.FindStringSubmatch(content); mm != nil {
		title = strings.TrimSpace(mm[1])
	}
	m.mu.Lock()
	mode := m.mode[title]
	m.mu.Unlock()
	repairing := strings.Contains(content, "YOUR PREVIOUS ANSWER")
	cues := []any{
		map[string]any{"at": 0.3, "kind": "title", "text": "Заголовок " + title},
		map[string]any{"at": math.Round(dur*50) / 100, "kind": "bar"},
	}
	var images []any
	if title == "Сцена 1" {
		cues = append(cues, map[string]any{"at": 1.0, "kind": "highlight", "image": "taras.png", "text": "Тарас"})
		images = append(images, map[string]any{"name": "taras.png", "kind": "sprite", "prompt": "Taras waving", "character": "@taras"})
	}
	if mode == "map" { // the cast's sprites placed by geography
		b, _ := json.Marshal(map[string]any{"grammar": "map", "spec": map[string]any{
			"data": map[string]any{"language": "en", "sprites": []any{map[string]any{"id": "t", "lon": 30.5, "lat": 50.4, "image": "taras.png", "alt": "taras_shock.png"}}},
			"cues": []any{
				map[string]any{"at": 0.3, "kind": "region", "region": "ukr"},
				map[string]any{"at": math.Round(dur*50) / 100, "kind": "sprite", "sprite": "t", "action": "swap", "frame": "alt"},
			}}})
		return string(b)
	}
	if mode == "nogeo" { // no geography: an alternate grammar
		b, _ := json.Marshal(map[string]any{"grammar": "y5_kinetic_type", "spec": map[string]any{"cues": []any{
			map[string]any{"at": 0.3, "kind": "title", "text": "Без мапи"}}}})
		return string(b)
	}
	if mode == "y5" { // a spec the real renderer draws
		b, _ := json.Marshal(map[string]any{"grammar": "y5_kinetic_type", "spec": map[string]any{"cues": []any{
			map[string]any{"at": 0.3, "kind": "title", "text": "Привіт"},
			map[string]any{"at": math.Round(dur*50) / 100, "kind": "point", "text": "Код", "sub": "анімація"},
		}}})
		return string(b)
	}
	switch {
	case mode == "bad", mode == "repair" && !repairing:
		cues = append(cues, map[string]any{"at": dur + 5, "kind": "explosion"})
	}
	b, _ := json.Marshal(map[string]any{
		"grammar": "t3_finance_chart",
		"spec":    map[string]any{"grammar": "t3_finance_chart", "duration": 99, "data": map[string]any{"title": "T"}, "cues": cues},
		"images":  images,
	})
	return "```json\n" + string(b) + "\n```"
}

func (m *codeMock) prompt(contains string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.prompts {
		if strings.Contains(p, contains) {
			return p
		}
	}
	return ""
}

func codeEnv(t *testing.T, mode map[string]string) (*testEnv, *codeMock, *fakeCoder) {
	t.Helper()
	cm := &codeMock{mode: mode}
	srv := httptest.NewServer(cm)
	allow(srv)
	t.Cleanup(srv.Close)
	env := newEnv(t, func(c *config.Config) { c.Text.BaseURL = srv.URL + "/v1" })
	fc := &fakeCoder{}
	env.e.SetCodeRenderer(fc)
	return env, cm, fc
}

func codeProject(t *testing.T, e *Engine, pipeline, grammar string, autopilot bool) Project {
	t.Helper()
	pr := quickProfile(t, e)
	pr.ID, pr.Name, pr.Pipeline, pr.CodeGrammar = "", "Тест · код", pipeline, grammar
	pr, err := e.SaveProfile(pr)
	must(t, err)
	if pr.Pipeline != pipeline || pr.CodeGrammar != grammar {
		t.Fatalf("profile %q %q", pr.Pipeline, pr.CodeGrammar)
	}
	p, err := e.CreateProject(NewProjectOptions{Text: apStory, ProfileID: pr.ID, Autopilot: autopilot})
	must(t, err)
	if p.Input.Pipeline != pipeline || p.Input.CodeGrammar != grammar {
		t.Fatalf("project %q %q", p.Input.Pipeline, p.Input.CodeGrammar)
	}
	return p
}

// ---- tests -----------------------------------------------------------------------------------

// TestCodePipeline: «Анімація кодом» from story to film — no clip from the
// video model, the voices first, a spec per scene timed to its voice, the
// sprite drawn on chroma green and keyed, the clips rendered by the
// renderer into videos/code-*, the montage plays them without a camera move.
func TestCodePipeline(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, cm, fc := codeEnv(t, nil)
	e := env.e
	p := codeProject(t, e, PipelineCode, "", true)
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if n := len(env.gl.requests("video")); n != 0 {
		t.Fatalf("%d clips from the video model", n)
	}
	var sprites, frames int
	for _, r := range env.gl.requests("image") {
		switch {
		case strings.Contains(r.prompt, "chroma-key"):
			sprites++
			if !strings.Contains(r.prompt, "@taras reference image") || r.aspect != "3:4" {
				t.Fatalf("sprite request without the cast reference: %+v", r)
			}
		case strings.Contains(r.prompt, "Medium shot of"):
			frames++
		}
	}
	if sprites != 1 || frames != 0 {
		t.Fatalf("sprites %d, frames %d", sprites, frames)
	}
	if fc.count() != len(p.Scenes) {
		t.Fatalf("%d renders for %d scenes", fc.count(), len(p.Scenes))
	}
	for i, s := range p.Scenes {
		if !strings.HasPrefix(s.Video.Path, "videos/code-") || s.Video.Status != StatusDone || s.Code.Fallback {
			t.Fatalf("scene %d: video %+v code %+v", i+1, s.Video, s.Code)
		}
		dur, off, voice, est := codeTiming(&p, &s)
		if est || voice != s.Audio.Duration || math.Abs(s.Code.Duration-dur) > 1e-9 || math.Abs(s.Video.Duration-dur) > 1e-9 {
			t.Fatalf("scene %d: timing %.3f/%.3f/%.3f est %v, spec %.3f clip %.3f, voice %.3f", i+1, dur, off, voice, est, s.Code.Duration, s.Video.Duration, s.Audio.Duration)
		}
		// The clip covers the line exactly as the montage places it (fade film: dips on both sides).
		if dur < voice+0.6-1e-9 || dur > voice+0.6+0.5 {
			t.Fatalf("scene %d: clip %.3f for a %.3f s line", i+1, dur, voice)
		}
		spec, err := parseCodeSpec(s.Code.Spec)
		must(t, err)
		if spec["duration"].(float64) != dur || spec["fps"].(float64) != 30 || spec["width"].(float64) != 1920 || spec["height"].(float64) != 1080 {
			t.Fatalf("scene %d spec frame: %v", i+1, spec)
		}
		if s.Code.Words != spokenText(&s) || s.Code.Estimated || s.Code.Edited || s.Code.Rendered == "" {
			t.Fatalf("scene %d bookkeeping %+v", i+1, s.Code)
		}
	}
	// The sprite: drawn once, a PNG with alpha, referenced by its file in the render.
	im := p.Scenes[0].Code.Images
	if len(im) != 1 || im[0].Image.Status != StatusDone || !strings.HasPrefix(im[0].Image.Path, "images/code/taras-") {
		t.Fatalf("images %+v", im)
	}
	data, err := os.ReadFile(e.Abs(p.ID, im[0].Image.Path))
	must(t, err)
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("sprite is not a PNG: %v", err)
	}
	fc.mu.Lock()
	first := string(fc.renders[0]) + string(fc.renders[1]) + string(fc.renders[2])
	fc.mu.Unlock()
	if !strings.Contains(first, `"image":"images/code/taras-`) {
		t.Fatalf("render did not get the picture's file: %s", first)
	}
	// The writer saw the words on the clip's clock and the frame rules.
	pr := cm.prompt("Scene: Сцена 1")
	for _, want := range []string{"Narration timeline (seconds from the clip start, measured from the voice", "Модельний", `"fps" 30, "width" 1920, "height" 1080`, "### y5_kinetic_type"} {
		if !strings.Contains(pr, want) {
			t.Fatalf("spec prompt lacks %q:\n%s", want, pr)
		}
	}
	// The montage: the clips as they are — no camera move, no gaps.
	if p.Montage.Output.Status != StatusDone || len(p.Montage.Gaps) != 0 {
		t.Fatalf("montage %+v gaps %+v", p.Montage.Output, p.Montage.Gaps)
	}
	for i, it := range e.Timeline(p) {
		if !it.HasClip || (it.Effect != "" && it.Effect != "none") {
			t.Fatalf("scene %d: clip %v effect %q", i+1, it.HasClip, it.Effect)
		}
	}
	if c := e.PipelineCostOf(p, PipelineCode); c.Clips != 0 || c.Frames != 0 || c.Code != len(p.Scenes) || c.Refs == 0 {
		t.Fatalf("cost %+v", c)
	}
	if c := e.PipelineCostOf(p, PipelineNoRefsCode); c.Refs != 0 || c.Code != len(p.Scenes) {
		t.Fatalf("norefs cost %+v", c)
	}
	// Rendering again renders nothing (every clip shows its spec).
	before := fc.count()
	must(t, e.Run(p.ID, StageRender, false, ""))
	mustJobDone(t, waitJob(t, e, p.ID))
	if fc.count() != before {
		t.Fatalf("an unchanged film was rendered again (%d → %d)", before, fc.count())
	}
}

// TestCodeRepairFallback: an invalid spec gets ONE repair round; a spec
// that stays invalid makes the scene its frame with the exact problems
// kept — listed in the montage's gaps, never silent.
func TestCodeRepairFallback(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, cm, fc := codeEnv(t, map[string]string{"Сцена 2": "repair", "Сцена 3": "bad"})
	e := env.e
	p := codeProject(t, e, PipelineNoRefsCode, "t3_finance_chart", true)
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	s1, s2, s3 := p.Scenes[0], p.Scenes[1], p.Scenes[2]
	if s1.Code.Fallback || s2.Code.Fallback || s2.Video.Status != StatusDone {
		t.Fatalf("scenes 1–2: %+v / %+v", s1.Code, s2.Code)
	}
	if !s3.Code.Fallback || !strings.Contains(s3.Code.Error, `kind "explosion" is not drawn by t3_finance_chart`) ||
		!strings.Contains(s3.Code.Error, "is outside the clip") || s3.Frame.Status != StatusDone || s3.Video.Path != "" {
		t.Fatalf("scene 3: code %+v frame %+v video %+v", s3.Code, s3.Frame, s3.Video)
	}
	if fc.count() != 2 {
		t.Fatalf("%d renders", fc.count())
	}
	cm.mu.Lock()
	specs := cm.specs
	cm.mu.Unlock()
	if specs != 1+2+2 { // scene 1 once, scene 2 repaired, scene 3 repaired and given up
		t.Fatalf("%d spec requests", specs)
	}
	// Norefs: no reference images; the sprite is described in words.
	for _, r := range env.gl.requests("image") {
		if strings.Contains(r.prompt, "reference image of") {
			t.Fatalf("a reference was drawn: %q", r.prompt)
		}
		if strings.Contains(r.prompt, "chroma-key") && !strings.Contains(r.prompt, "keep exactly this look") {
			t.Fatalf("sprite without the written look: %q", r.prompt)
		}
	}
	if len(p.Montage.Gaps) != 1 || p.Montage.Gaps[0].Number != 3 || p.Montage.Gaps[0].Action != GapStill ||
		!strings.Contains(p.Montage.Gaps[0].Reason, "анімація кодом: анімацію не вдалося написати") {
		t.Fatalf("gaps %+v", p.Montage.Gaps)
	}
	tl := e.Timeline(p)
	if tl[2].HasClip || tl[2].Effect == "" || tl[2].Effect == "none" || !tl[0].HasClip {
		t.Fatalf("timeline %+v", tl)
	}
	// A new try of the scene that fell back (the model writes it the same way).
	cm.mu.Lock()
	cm.mode["Сцена 3"] = "ok"
	cm.mu.Unlock()
	must(t, e.Regen(p.ID, RegenVideo, s3.ID, ""))
	p = waitFor(t, e, p.ID, "scene 3 animated", func(p Project) bool { return p.Scenes[2].Video.Status == StatusDone })
	if p.Scenes[2].Code.Fallback || p.Scenes[2].Code.Error != "" || fc.count() != 3 {
		t.Fatalf("after the new try: %+v (%d renders)", p.Scenes[2].Code, fc.count())
	}
}

// TestCodeEdit: an edited spec is kept (Edited), makes the clip stale and is
// rendered by the clip's regeneration; text that is not JSON is refused; a
// new grammar makes the next render write a new spec.
func TestCodeEdit(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, cm, fc := codeEnv(t, nil)
	e := env.e
	p := codeProject(t, e, PipelineCode, "", true)
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	sid := p.Scenes[1].ID
	edited, ok := SetCodeCue(p.Scenes[1].Code.Spec, 0, 0.5, "Мій заголовок", "")
	if !ok {
		t.Fatal("SetCodeCue")
	}
	if msg := e.ValidateCodeSpec(&p, sid, edited); msg != "" {
		t.Fatalf("valid spec refused: %s", msg)
	}
	if msg := e.ValidateCodeSpec(&p, sid, []byte(`{"grammar":"t3_finance_chart","duration":3,"cues":[{"at":9,"kind":"nope"}]}`)); !strings.Contains(msg, "outside the clip") || !strings.Contains(msg, `"nope"`) {
		t.Fatalf("invalid spec: %q", msg)
	}
	must(t, e.Edit(p.ID, func(p *Project) { findScene(p, sid).Code.Spec = edited }))
	q, _ := e.Get(p.ID)
	s := findScene(&q, sid)
	if !s.Code.Edited || !s.Video.Stale || !strings.Contains(string(s.Code.Spec), "Мій заголовок") {
		t.Fatalf("after the edit: %+v stale %v", s.Code, s.Video.Stale)
	}
	must(t, e.Edit(p.ID, func(p *Project) { findScene(p, sid).Code.Spec = json.RawMessage(`{not json`) }))
	q, _ = e.Get(p.ID)
	if !strings.Contains(string(findScene(&q, sid).Code.Spec), "Мій заголовок") {
		t.Fatal("broken JSON replaced the spec")
	}
	cm.mu.Lock()
	before := cm.specs
	cm.mu.Unlock()
	must(t, e.Regen(p.ID, RegenVideo, sid, ""))
	q = waitFor(t, e, p.ID, "re-render", func(p Project) bool {
		s := findScene(&p, sid)
		return s.Video.Status == StatusDone && !s.Video.Stale
	})
	if !strings.Contains(fmt.Sprint(fc.lastSpec(t)["cues"]), "Мій заголовок") {
		t.Fatalf("the edited spec was not rendered: %v", fc.lastSpec(t))
	}
	cm.mu.Lock()
	after := cm.specs
	cm.mu.Unlock()
	if after != before {
		t.Fatal("an edited spec was written again")
	}
	// Another grammar for this scene: stale, and the next render writes it anew.
	must(t, e.Edit(p.ID, func(p *Project) { findScene(p, sid).Code.Grammar = "y5_kinetic_type" }))
	q, _ = e.Get(p.ID)
	if s := findScene(&q, sid); s.Code.Edited || !s.Video.Stale || codeNeedsSpec(&q, s) == "" {
		t.Fatalf("grammar change: %+v", s.Code)
	}
	must(t, e.Edit(p.ID, func(p *Project) { findScene(p, sid).Code.Grammar = "Bad Id!" }))
	q, _ = e.Get(p.ID)
	if g := findScene(&q, sid).Code.Grammar; g != "" {
		t.Fatalf("bad grammar id kept: %q", g)
	}
	// The preview still: drawn by the renderer at a moment after the last cue.
	must(t, e.CodePreview(p.ID, sid, -1))
	q = waitFor(t, e, p.ID, "preview", func(p Project) bool { return findScene(&p, sid).Code.Preview.Status == StatusDone })
	if pv := findScene(&q, sid).Code.Preview; !strings.HasPrefix(pv.Path, "images/code/code-preview-") || !isFile(e.Abs(p.ID, pv.Path)) {
		t.Fatalf("preview %+v", pv)
	}
}

// TestCodeCancel: stopping the render stage stops the renderer at once; the
// clip is not marked failed.
func TestCodeCancel(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, _, fc := codeEnv(t, nil)
	e := env.e
	p := codeProject(t, e, PipelineCode, "", false)
	must(t, e.Run(p.ID, StagePlan, false, ""))
	mustJobDone(t, waitJob(t, e, p.ID))
	fc.mu.Lock()
	fc.block, fc.started = true, make(chan struct{}, 4)
	fc.mu.Unlock()
	must(t, e.Run(p.ID, StageRender, false, ""))
	select {
	case <-fc.started:
	case <-time.After(60 * time.Second):
		t.Fatal("render did not start")
	}
	e.Cancel(p.ID)
	p = waitJob(t, e, p.ID)
	if p.Job.Status != JobCancelled {
		t.Fatalf("job %+v", p.Job)
	}
	for _, s := range p.Scenes {
		if s.Video.Status == StatusError || s.Video.Status == StatusRunning {
			t.Fatalf("clip after cancel: %+v", s.Video)
		}
	}
}

// TestCodeUnavailable: without a browser the render stage says exactly why
// on every clip — no frames are drawn instead.
func TestCodeUnavailable(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, _, _ := codeEnv(t, nil)
	e := env.e
	e.SetCodeRenderer(unavailableCoder{&fakeCoder{}})
	p := codeProject(t, e, PipelineCode, "", false)
	must(t, e.Run(p.ID, StagePlan, false, ""))
	mustJobDone(t, waitJob(t, e, p.ID))
	must(t, e.Run(p.ID, StageRender, false, ""))
	p = waitJob(t, e, p.ID)
	if !strings.Contains(p.Job.Message, "Анімація кодом недоступна: no browser here") {
		t.Fatalf("job %+v", p.Job)
	}
	for _, s := range p.Scenes {
		if s.Video.Status != StatusError || !strings.Contains(s.Video.Error, "no browser here") || s.Frame.Path != "" {
			t.Fatalf("scene %+v", s.Video)
		}
	}
	gaps := MontageGaps(&p, e.Dir(p.ID))
	if len(gaps) != 3 || !strings.Contains(gaps[0].Reason, "анімація: Анімація кодом недоступна") {
		t.Fatalf("gaps %+v", gaps)
	}
}

type unavailableCoder struct{ *fakeCoder }

func (unavailableCoder) Available(context.Context) (string, error) {
	return "", errors.New("no browser here")
}

// ---- pure helpers ------------------------------------------------------------------------------

func TestCodeTimingAndWords(t *testing.T) {
	p := &Project{Input: Input{Voiceover: true, AudioMode: AudioVoiceover, Aspect: "9:16"}, Montage: Montage{Transition: "none"},
		Scenes: []Scene{{ID: "a", Narration: "Раз два. Три чотири п'ять", Audio: Asset{Status: StatusDone, Path: "x.mp3", Duration: 3}}, {ID: "b"}}}
	dur, off, voice, est := codeTiming(p, &p.Scenes[0])
	if est || voice != 3 || off != 0.25 || math.Abs(dur-3.6) > 1.0/30 {
		t.Fatalf("timing %v %v %v %v", dur, off, voice, est)
	}
	p.Input.Tempo = TempoRapid // tight lines: one frame of breath
	if dur, off, _, _ := codeTiming(p, &p.Scenes[0]); math.Abs(off-1.0/24) > 1e-9 || dur > 3.2 {
		t.Fatalf("tight timing %v %v", dur, off)
	}
	p.Input.Tempo = ""
	ws := codeWords(p, &p.Scenes[0], nil, 3, 0.25)
	if len(ws) != 5 || ws[0].At != 0.25 || ws[2].At <= ws[1].At || ws[4].At >= 3.25 {
		t.Fatalf("words %+v", ws)
	}
	// Multi-voice parts keep their places.
	s := p.Scenes[0]
	s.Parts = []Line{{Speaker: "narrator", Text: "Раз два.", Start: 0, End: 1}, {Speaker: "narrator2", Text: "Три", Start: 2, End: 2.5}}
	ws = codeWords(p, &s, nil, 3, 0.25)
	if len(ws) != 3 || ws[2].At != 2.25 {
		t.Fatalf("part words %+v", ws)
	}
	// The own recording: the transcript's words, relative to the slice.
	s = Scene{ID: "c", AudioSource: AudioUpload, VoiceStart: 10, VoiceEnd: 12}
	if d, o, v, _ := codeTiming(p, &s); v != 2 || o != 0 || d < 2 {
		t.Fatalf("upload timing %v %v %v", d, o, v)
	}
	// Nothing said: a still's length.
	if d, _, v, _ := codeTiming(p, &p.Scenes[1]); v != 0 || d < 3 {
		t.Fatalf("silent timing %v %v", d, v)
	}
}

func TestCodeSpecHelpers(t *testing.T) {
	s, err := parseCodeSpec([]byte(`{"grammar":"t3_finance_chart","duration":4,"alpha":true,"cues":[{"at":1,"kind":"bar","dur":2},{"at":2,"kind":"number","data":{"hit":3}},{"at":3,"kind":"highlight","image":"a.png"}]}`))
	must(t, err)
	retimeSpec(s, 8)
	c := cuesOf(s)
	if c[0]["at"] != 2.0 || c[0]["dur"] != 4.0 || c[1]["data"].(map[string]any)["hit"] != 6.0 || s["duration"] != 8.0 {
		t.Fatalf("retime %v", s)
	}
	fixSpec(s, codeFrame{grammar: "t3_finance_chart", duration: 8, width: 1080, height: 1920, safeBottom: 520})
	if s["alpha"] != nil || s["width"] != 1080 || s["safe"].(map[string]any)["bottom"] != 520 {
		t.Fatalf("fix %v", s)
	}
	gs := (&fakeCoder{}).Grammars()
	probs := checkSpec(s, gs, nil, "")
	if len(probs) != 1 || !strings.Contains(probs[0], `"a.png" is used but not declared`) {
		t.Fatalf("problems %v", probs)
	}
	if p := checkSpec(s, gs, []CodeImage{{Name: "a.png"}}, ""); len(p) != 0 {
		t.Fatalf("declared picture refused: %v", p)
	}
	s["grammar"] = "nope"
	if p := checkSpec(s, gs, []CodeImage{{Name: "a.png"}}, ""); len(p) != 1 || !strings.Contains(p[0], "unknown") {
		t.Fatalf("grammar check %v", p)
	}
	code := CodeClip{Spec: marshalSpec(s), Images: []CodeImage{{Name: "a.png", Image: Asset{Path: "images/code/a-1.png"}}}}
	raw, err := resolvedSpec(&code)
	must(t, err)
	if !strings.Contains(string(raw), `"image":"images/code/a-1.png"`) {
		t.Fatalf("resolved %s", raw)
	}
	if at, ok := ParseCueTime("0:01,5"); !ok || at != 1.5 {
		t.Fatalf("cue time %v", at)
	}
	if cleanGrammarID(" Map ") != "map" || cleanGrammarID("../x") != "" {
		t.Fatal("grammar ids")
	}
}

func TestKeyGreen(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 1))
	src.Set(0, 0, color.RGBA{0, 255, 0, 255})     // the chroma background
	src.Set(1, 0, color.RGBA{200, 40, 30, 255})   // the subject
	src.Set(2, 0, color.RGBA{120, 190, 100, 255}) // a green-spilled edge
	out := keyGreen(src)
	if a := out.NRGBAAt(0, 0).A; a != 0 {
		t.Fatalf("background alpha %d", a)
	}
	if c := out.NRGBAAt(1, 0); c.A != 255 || c.R != 200 {
		t.Fatalf("subject %v", c)
	}
	if c := out.NRGBAAt(2, 0); c.A == 0 || c.A == 255 || c.G > 120 {
		t.Fatalf("edge %v", c)
	}
}

// TestCodeRealRender is the whole scene with the real renderer — the voice
// (mock TTS), the spec (mock text model, a kinetic-type grammar), the render
// in a real browser, the preview still and the montage. Skipped where no
// browser or FFmpeg exists.
func TestCodeRealRender(t *testing.T) {
	needMedia(t)
	r := newCodeRenderer()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := r.Available(ctx); err != nil {
		t.Skip("no browser for code animations:", err)
	}
	if !slices.ContainsFunc(r.Grammars(), func(g CodeGrammar) bool { return g.ID == "y5_kinetic_type" }) {
		t.Skip("no y5_kinetic_type grammar")
	}
	env, cm, _ := codeEnv(t, map[string]string{"Привіт": "y5"})
	e := env.e
	e.SetCodeRenderer(r)
	p, err := e.Create("Код")
	must(t, err)
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Input.Pipeline, p.Input.CodeGrammar, p.Input.NarratorVoiceID = PipelineCode, "y5_kinetic_type", "v-adam"
		p.Scenes = []Scene{{Title: "Привіт", Narration: "Привіт, це анімація кодом."}}
	}))
	must(t, e.Run(p.ID, StageRender, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	s := p.Scenes[0]
	if s.Video.Status != StatusDone || s.Code.Fallback || cm.prompt("### y5_kinetic_type") == "" {
		t.Fatalf("scene: video %+v code %+v (%s)", s.Video, s.Code, p.Job.Message)
	}
	info, err := testFF.Probe(ctx, e.Abs(p.ID, s.Video.Path))
	must(t, err)
	if math.Abs(info.Duration-s.Code.Duration) > 0.1 || math.Abs(s.Code.Duration-(s.Audio.Duration+0.6)) > 0.6 {
		t.Fatalf("clip %.3f s, spec %.3f s, voice %.3f s", info.Duration, s.Code.Duration, s.Audio.Duration)
	}
	must(t, e.CodePreview(p.ID, s.ID, -1))
	p = waitFor(t, e, p.ID, "real preview", func(p Project) bool { return !busyAsset(p.Scenes[0].Code.Preview) })
	if p.Scenes[0].Code.Preview.Status != StatusDone {
		t.Fatalf("preview %+v", p.Scenes[0].Code.Preview)
	}
	must(t, e.Run(p.ID, StageMontage, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if tl := e.Timeline(p); len(tl) != 1 || !tl[0].HasClip {
		t.Fatalf("timeline %+v", tl)
	}
}

// TestCodeMapCast: a "map" film — the cast's chibi sprites (main + shocked
// frame drawn from the main one, keyed, drawn ONCE for all scenes), the
// full reference and the examples in the writer's prompt, the labels in the
// film's language, and an alternate grammar only where the writer chose it.
func TestCodeMapCast(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, cm, fc := codeEnv(t, map[string]string{"Сцена 1": "map", "Сцена 2": "nogeo", "Сцена 3": "map"})
	e := env.e
	pr := quickProfile(t, e)
	pr.ID, pr.Name, pr.Pipeline, pr.CodeGrammar, pr.Language = "", "Тест · мапа", PipelineCode, "map", "uk"
	pr, err := e.SaveProfile(pr)
	must(t, err)
	p, err := e.CreateProject(NewProjectOptions{Text: apStory, ProfileID: pr.ID, Autopilot: true})
	must(t, err)
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	var sprites []glabsReq
	for _, r := range env.gl.requests("image") {
		if strings.Contains(r.prompt, "chroma-key") {
			sprites = append(sprites, r)
		}
	}
	if len(sprites) != 2 {
		t.Fatalf("%d sprite requests (want main + shocked, once for the film)", len(sprites))
	}
	var shock glabsReq
	for _, r := range sprites {
		if strings.Contains(r.prompt, "SHOCKED") {
			shock = r
		}
		if !strings.Contains(r.prompt, "Draw ONLY the figure") {
			t.Fatalf("sprite prompt: %s", r.prompt)
		}
	}
	if shock.prompt == "" || !slices.ContainsFunc(shock.refNames, func(n string) bool { return strings.Contains(n, "base") }) {
		t.Fatalf("the shocked frame is not drawn from the main figure: %+v", shock)
	}
	for _, i := range []int{0, 2} {
		s := p.Scenes[i]
		g, _ := specGrammar(s.Code.Spec)
		if g != "map" || len(s.Code.Images) != 2 || s.Code.Images[1].Base != "taras.png" || s.Code.Images[0].Image.Path == s.Code.Images[1].Image.Path {
			t.Fatalf("scene %d: %s %+v", i+1, g, s.Code.Images)
		}
		spec, _ := parseCodeSpec(s.Code.Spec)
		if lang := spec["data"].(map[string]any)["language"]; lang != "uk" {
			t.Fatalf("map language %v", lang)
		}
	}
	if p.Scenes[0].Code.Images[0].Image.Path != p.Scenes[2].Code.Images[0].Image.Path {
		t.Fatal("the cast sprite must be one file for the film")
	}
	if g, _ := specGrammar(p.Scenes[1].Code.Spec); g != "y5_kinetic_type" {
		t.Fatalf("scene without geography: %s", g)
	}
	fc.mu.Lock()
	all := ""
	for _, r := range fc.renders {
		all += string(r)
	}
	fc.mu.Unlock()
	if !strings.Contains(all, `"image":"images/code/taras-`) || !strings.Contains(all, `"alt":"images/code/taras_shock-`) {
		t.Fatalf("render did not get the sprite files: %s", all)
	}
	pr1 := cm.prompt("Scene: Сцена 1")
	for _, want := range []string{"REF-MAP-FULL", "Example film 2 of 2", "Cast figures for map scenes", `image "taras.png", alt (shocked) "taras_shock.png"`,
		`"data.language" is set to "uk"`, `use "map"`, "### y5_kinetic_type", "### y4_storytime"} {
		if !strings.Contains(pr1, want) {
			t.Fatalf("spec prompt lacks %q:\n%s", want, pr1)
		}
	}
	if strings.Contains(pr1, "### t3_finance_chart") {
		t.Fatal("only the map's alternates are offered")
	}
}

func TestMapLanguageAndAlternatesRule(t *testing.T) {
	for in, want := range map[string]string{"uk": "uk", "uk-UA": "uk", "pt-BR": "pt", "ja": "en", "": "en", "EN": "en"} {
		if got := mapLanguage(in); got != want {
			t.Fatalf("mapLanguage(%q) = %q", in, got)
		}
	}
	p := &Project{Input: Input{Pipeline: PipelineCode, CodeGrammar: "map"}}
	rules := strings.Join(codeShotRules(p), "\n")
	if !strings.Contains(rules, `"map" (the film's grammar) whenever the scene involves ANY place`) || !strings.Contains(rules, "y5_kinetic_type") {
		t.Fatalf("shot rules: %s", rules)
	}
	st := prevMapState(json.RawMessage(`{"grammar":"map","data":{"view":{"lon":1,"lat":2,"zoom":3},"regions":[{"id":"fr","preset":"france_metropolitan","color":"blue","label":"x"}]},
"cues":[{"at":1,"kind":"camera","view":{"lon":-90,"lat":30,"zoom":8}},{"at":2,"kind":"recolor","region":"fr","color":"usa"}]}`))
	if !strings.Contains(st, `"lon":-90`) || !strings.Contains(st, `"preset":"france_metropolitan"`) || !strings.Contains(st, `"recolor":"fr"`) || strings.Contains(st, `"label"`) {
		t.Fatalf("prev map state %s", st)
	}
}

// A text service that fails is an error of the scene (exact, retryable),
// never a silent fallback to its frame.
func TestCodeServiceErrorIsNoFallback(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, _, _ := codeEnv(t, map[string]string{"Сцена 2": "down"})
	e := env.e
	p := codeProject(t, e, PipelineNoRefsCode, "t3_finance_chart", false)
	must(t, e.Run(p.ID, StagePlan, false, ""))
	mustJobDone(t, waitJob(t, e, p.ID))
	must(t, e.Run(p.ID, StageFrames, false, ""))
	p = waitJob(t, e, p.ID)
	s2 := p.Scenes[1]
	if s2.Code.Fallback || s2.Video.Status != StatusError || !strings.Contains(s2.Video.Error, "анімацію не вдалося написати") ||
		!strings.Contains(p.Job.Message, "помилок: 1") || p.Scenes[0].Code.Fallback || len(p.Scenes[0].Code.Spec) == 0 {
		t.Fatalf("scene 2 %+v video %+v job %+v", s2.Code, s2.Video, p.Job)
	}
}

func TestLiftFirstCue(t *testing.T) {
	s, _ := parseCodeSpec([]byte(`{"cues":[{"at":0.62,"kind":"title"},{"at":2,"kind":"point"},{"at":0.62,"kind":"x"}]}`))
	liftFirstCue(s, 0.25)
	c := cuesOf(s)
	if c[0]["at"] != 0.25 || c[2]["at"] != 0.25 || c[1]["at"] != 2.0 {
		t.Fatalf("%v", c)
	}
	s, _ = parseCodeSpec([]byte(`{"cues":[{"at":0.3,"kind":"title"}]}`))
	liftFirstCue(s, 0.25)
	if cuesOf(s)[0]["at"] != 0.3 {
		t.Fatal("a cue on the voice start stays")
	}
}
