package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
)

// ---- a text model for the modes ---------------------------------------------------------------

// modesMock answers the bible pass with a mode-specific plan, the shot pass
// like the default mock and the dubbing translator with English lines.
type modesMock struct {
	mu      sync.Mutex
	bible   func(content string) any
	prompts []string
}

func (m *modesMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	m.mu.Lock()
	m.prompts = append(m.prompts, content)
	m.mu.Unlock()
	var reply string
	switch {
	case strings.Contains(content, "award-winning film director"):
		b, _ := json.Marshal(m.bible(content))
		reply = string(b)
	case strings.Contains(content, "storyboard artist"):
		reply = shotsReply(content)
	case strings.Contains(content, "professional dubbing translator"):
		_, input, _ := strings.Cut(content, "### INPUT\n")
		var lines []struct {
			ID    string
			Title string
			Lines []struct{ Speaker, Text string }
		}
		_ = json.Unmarshal([]byte(input), &lines)
		var out []any
		for _, l := range lines {
			var said []any
			for _, x := range l.Lines {
				said = append(said, map[string]any{"speaker": x.Speaker, "text": "EN " + x.Text})
			}
			out = append(out, map[string]any{"id": l.ID, "title": "EN " + l.Title, "lines": said,
				"onScreen": map[string]any{"title": "Population", "value": "8.1 bn", "caption": "in 2025"}})
		}
		b, _ := json.Marshal(out)
		reply = string(b)
	case strings.Contains(content, "professional film translator"):
		reply = `{"title": "The Way Home", "logline": "A boy.", "characters": [], "cover": {}}`
	default:
		reply = "OK"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": reply}}}})
}

func (m *modesMock) last(kind string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.prompts) - 1; i >= 0; i-- {
		if strings.Contains(m.prompts[i], kind) {
			return m.prompts[i]
		}
	}
	return ""
}

func modesEnv(t *testing.T, bible func(content string) any, mut func(c *config.Config)) (*testEnv, *modesMock) {
	t.Helper()
	mm := &modesMock{bible: bible}
	srv := httptest.NewServer(mm)
	allow(srv)
	t.Cleanup(srv.Close)
	env := newEnv(t, func(c *config.Config) {
		c.Text.BaseURL = srv.URL + "/v1"
		if mut != nil {
			mut(c)
		}
	})
	return env, mm
}

// ---- the input --------------------------------------------------------------------------------

func TestModesOfInput(t *testing.T) {
	in := DefaultInput()
	if in.SoundMode() != AudioVoiceover || in.ContentMode() != ContentCharacters || in.TextOverlays() || in.NoPeople() {
		t.Fatalf("defaults: %s %s %v", in.SoundMode(), in.ContentMode(), in.TextOverlays())
	}
	in.Voiceover = false
	if in.SoundMode() != AudioSilent {
		t.Fatal("an older project without voice-over is silent")
	}
	in.AudioMode = AudioInVideo
	if in.SoundMode() != AudioInVideo {
		t.Fatal("audio mode ignored")
	}
	in.Mode = ModeVoice
	in.Voiceover = true
	if in.SoundMode() != AudioVoiceover {
		t.Fatal("the own recording is always the voice-over")
	}
	in = DefaultInput()
	in.Content = ContentMotion
	if !in.TextOverlays() || !in.NoPeople() {
		t.Fatal("motion graphics: text on, nobody in frame")
	}
	in.Overlays = "off"
	if in.TextOverlays() {
		t.Fatal("text switched off")
	}
	in.Content, in.Overlays = ContentNoPeople, "on"
	if !in.TextOverlays() || !in.NoPeople() {
		t.Fatal("no people with text")
	}
}

// Edit keeps Voiceover in step with the sound mode; bad values keep the old;
// lines and on-screen text are cleaned; changed lines make the clip stale.
func TestModesEdit(t *testing.T) {
	env := newEnv(t, nil)
	e := env.e
	p, err := e.Create("Режими")
	must(t, err)
	edit := func(fn func(p *Project)) Project {
		t.Helper()
		must(t, e.Edit(p.ID, fn))
		q, _ := e.Get(p.ID)
		return q
	}
	q := edit(func(p *Project) { p.Input.AudioMode = AudioInVideo })
	if q.Input.Voiceover || q.Input.SoundMode() != AudioInVideo {
		t.Fatalf("in-video: voiceover %v", q.Input.Voiceover)
	}
	q = edit(func(p *Project) { p.Input.AudioMode, p.Input.Content, p.Input.Overlays = "loud", "robots", "maybe" })
	if q.Input.AudioMode != AudioInVideo || q.Input.Content != "" || q.Input.Overlays != "" {
		t.Fatalf("bad values: %+v", q.Input)
	}
	q = edit(func(p *Project) { p.Input.Voiceover = true }) // an older switch turns the voice-over on
	if q.Input.AudioMode != "" || q.Input.SoundMode() != AudioVoiceover {
		t.Fatalf("voiceover switch: %q %v", q.Input.AudioMode, q.Input.Voiceover)
	}
	q = edit(func(p *Project) { p.Input.AudioMode = AudioSilent })
	if q.Input.Voiceover {
		t.Fatal("silent with voice-over")
	}

	// Lines: a deleted speaker goes off screen; on-screen text is checked.
	q = edit(func(p *Project) {
		p.Input.AudioMode = AudioInVideo
		p.Characters = []Character{{ID: "char0001", Name: "Тарас"}, {ID: "obj00001", Name: "Компас", Kind: KindObject}, {ID: "xxxx0001", Name: "X", Kind: "robot"}}
		p.Scenes = []Scene{{ID: "scene001", Lines: []Line{{Speaker: "char0001", Text: "Мамо!"}, {Speaker: "ghost", Text: "Хто тут?"}},
			OnScreen: OnScreen{Title: "Заголовок", Anim: "explode", Position: "left"}}}
	})
	if c := q.Characters; c[1].Kind != KindObject || c[2].Kind != "" {
		t.Fatalf("kinds %+v", c)
	}
	s := q.Scenes[0]
	if s.Lines[0].Speaker != "char0001" || s.Lines[1].Speaker != "narrator" || s.OnScreen.Anim != "" || s.OnScreen.Position != "" {
		t.Fatalf("scene %+v", s)
	}
	// A clip that exists goes stale when the lines change.
	st := e.state(p.ID)
	st.mu.Lock()
	st.p.Scenes[0].Video = Asset{Status: StatusDone, Path: "videos/x.mp4"}
	st.mu.Unlock()
	q = edit(func(p *Project) { p.Scenes[0].OnScreen.Title = "Інший" })
	if q.Scenes[0].Video.Stale {
		t.Fatal("on-screen text made the clip stale")
	}
	q = edit(func(p *Project) { p.Scenes[0].Lines[0].Text = "Тату!" })
	if !q.Scenes[0].Video.Stale {
		t.Fatal("new words, clip not stale")
	}
	// Deleting the speaker sends the line off screen.
	q = edit(func(p *Project) { p.Characters = p.Characters[1:] })
	if q.Scenes[0].Lines[0].Speaker != "narrator" {
		t.Fatalf("orphan line %+v", q.Scenes[0].Lines[0])
	}
}

// ---- the plan ------------------------------------------------------------------------------------

func planWith(mut func(in *Input)) *Project {
	p := &Project{Input: DefaultInput()}
	p.Input.Text = "Хлопчик Тарас загубився в лісі."
	if mut != nil {
		mut(&p.Input)
	}
	return p
}

func TestBiblePromptModes(t *testing.T) {
	has := func(name, s string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(s, w) {
				t.Errorf("%s lacks %q", name, w)
			}
		}
	}
	lacks := func(name, s string, bad ...string) {
		t.Helper()
		for _, b := range bad {
			if strings.Contains(s, b) {
				t.Errorf("%s has %q", name, b)
			}
		}
	}
	// The classic film is unchanged.
	sys, _ := biblePrompt(planWith(nil), 3, 18, nil, 8)
	has("classic", sys, `"narration": the voice-over line`, `"speaker": "narrator" for narration`, `"characters": [ { "name", "role", "gender"`)
	lacks("classic", sys, `"lines"`, `"objects"`, `"onScreen"`, "NOBODY")

	sys, _ = biblePrompt(planWith(func(in *Input) { in.AudioMode, in.Voiceover = AudioInVideo, false }), 3, DialogueWords(8), nil, 8)
	has("in-video", sys, `"lines": [ { "speaker", "text", "delivery" } ]`, `"voice"`, `"narratorVoice"`, "speak ON CAMERA",
		fmt.Sprintf("at most %d words in total", DialogueWords(8)), `"delivery": ENGLISH`)
	lacks("in-video", sys, `"narration": the voice-over line`, `"speaker": "narrator" for narration`)

	sys, _ = biblePrompt(planWith(func(in *Input) { in.Content = ContentNoPeople }), 3, 18, nil, 8)
	has("no people", sys, "NOBODY appears", `"objects": [ { "name", "role", "description"`, `"objects": [names]`, "Nobody appears in any scene")
	lacks("no people", sys, `"characters": [`, `"speaker"`, `"onScreen"`)

	sys, _ = biblePrompt(planWith(func(in *Input) { in.Content, in.Language = ContentMotion, "en" }), 3, 18, nil, 8)
	has("motion", sys, "MOTION-GRAPHICS", "designed backdrops", `"onScreen": { "title", "value", "caption", "anim" }`, "EVERY scene gets it", "in English:")

	sys, _ = biblePrompt(planWith(func(in *Input) { in.Content = ContentMixed }), 3, 18, nil, 8)
	has("mixed", sys, `"characters": [names]`, `"objects": [names]`, "[] for a shot without people")
	lacks("mixed", sys, `"onScreen"`)

	sys, _ = biblePrompt(planWith(func(in *Input) { in.Content, in.Overlays = ContentNoPeople, "on" }), 3, 18, nil, 8)
	has("no people + text", sys, "Only scenes with a key fact")

	sys, _ = biblePrompt(planWith(func(in *Input) { in.AudioMode, in.Voiceover = AudioSilent, false }), 3, 18, nil, 8)
	has("silent", sys, "music and ambient sound only", "used only as a subtitle/caption")

	// No people + in-video: the off-screen voice speaks every line.
	sys, _ = biblePrompt(planWith(func(in *Input) { in.AudioMode, in.Voiceover, in.Content = AudioInVideo, false, ContentNoPeople }), 3, 16, nil, 8)
	has("no people + in-video", sys, `"speaker" is always "narrator"`)

	// A script in-video: the parts become lines.
	sys, prompt := biblePrompt(planWith(func(in *Input) { in.AudioMode, in.Voiceover, in.Mode = AudioInVideo, false, ModeScript }), 2, 16,
		[]string{"«Мамо, дивись!» — гукнув Тарас.", "Лисиця підійшла ближче."}, 8)
	has("script in-video", prompt+sys, "Turn each part into what is said ON CAMERA", "The words of the scene's script part")
}

// An in-video plan: lines with speakers and deliveries, no narration, the
// cast's voices, the off-screen voice, no TTS voices; the shot prompts get
// the dialogue and keep the words out of the motion prompt.
func TestPlanInVideo(t *testing.T) {
	env, mm := modesEnv(t, func(string) any {
		return map[string]any{
			"title": "Стежка", "logline": "Хлопчик і лисиця.", "styleGuide": "Warm autumn light.",
			"narratorVoice": "A calm, warm grandmother's voice.",
			"characters": []any{
				map[string]any{"name": "Тарас", "role": "boy", "gender": "male", "description": "A 7-year-old boy in a red jacket, freckles.", "voice": "a boy of about 7, soft high voice"},
				map[string]any{"name": "Лисиця", "role": "guide", "gender": "female", "description": "A small orange fox.", "voice": "a sly, gentle female voice"},
			},
			"locations": []any{map[string]any{"name": "Ліс", "description": "Autumn forest."}},
			"scenes": []any{
				map[string]any{"title": "Зустріч", "characters": []any{"Тарас"}, "location": "Ліс", "narration": "ignored",
					"lines": []any{
						map[string]any{"speaker": "Тарас", "text": "Хто тут?", "delivery": "whispers, scared"},
						map[string]any{"speaker": "Лисиця", "text": "Не бійся, я покажу дорогу.", "delivery": "warm"},
					}},
				map[string]any{"title": "Дорога", "characters": []any{"Тарас", "Лисиця"}, "location": "Ліс",
					"lines": []any{map[string]any{"speaker": "narrator", "text": "І вони пішли разом."}}},
				map[string]any{"title": "Дім", "characters": []any{"Тарас"}, "location": "Ліс", "lines": []any{}},
			},
		}
	}, nil)
	e := env.e
	p, err := e.CreateProject(NewProjectOptions{Text: "Тарас і лисиця в осінньому лісі.", AudioMode: AudioInVideo, TargetSeconds: 24})
	must(t, err)
	if p.Input.SoundMode() != AudioInVideo || p.Input.Voiceover {
		t.Fatalf("new project: %+v", p.Input)
	}
	must(t, e.Run(p.ID, StagePlan, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	taras, fox := p.Characters[0], p.Characters[1]
	if taras.Voice != "a boy of about 7, soft high voice" || fox.Voice == "" || p.NarratorVoice != "A calm, warm grandmother's voice." {
		t.Fatalf("voices: %q %q %q", taras.Voice, fox.Voice, p.NarratorVoice)
	}
	if p.Input.NarratorVoiceID != "" || taras.VoiceID != "" || fox.VoiceID != "" {
		t.Fatal("TTS voices assigned to an in-video film")
	}
	s0 := p.Scenes[0]
	if s0.Narration != "" || len(s0.Lines) != 2 || s0.Lines[0].Speaker != taras.ID || s0.Lines[1].Speaker != fox.ID ||
		s0.Lines[0].Delivery != "whispers, scared" || len(s0.CharacterIDs) != 2 {
		t.Fatalf("scene 1: %+v", s0)
	}
	if l := p.Scenes[1].Lines; len(l) != 1 || l[0].Speaker != "narrator" {
		t.Fatalf("scene 2 lines %+v", l)
	}
	if len(p.Scenes[2].Lines) != 0 {
		t.Fatalf("scene 3 lines %+v", p.Scenes[2].Lines)
	}
	shots := mm.last("storyboard artist")
	for _, want := range []string{"dialogue: @taras (whispers, scared): «Хто тут?»", "dialogue: (nobody speaks)", "never write them", `"Ambient sound:"`} {
		if !strings.Contains(shots, want) {
			t.Errorf("shots prompt lacks %q", want)
		}
	}

	// Frames, then the clips: no voices, the dialogue in the prompt, Veo's way.
	must(t, e.Run(p.ID, StageFrames, false, ""))
	mustJobDone(t, waitJob(t, e, p.ID))
	calls := len(eleven.calls)
	must(t, e.Run(p.ID, StageRender, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if len(eleven.calls) != calls {
		t.Fatal("TTS called in an in-video film")
	}
	for _, s := range p.Scenes {
		if s.Video.Status != StatusDone || s.Audio.Status == StatusDone {
			t.Fatalf("scene %s: video %+v audio %+v", s.ID, s.Video, s.Audio)
		}
	}
	var prompts []string
	for _, r := range env.gl.requests("video") {
		prompts = append(prompts, r.prompt)
	}
	all := strings.Join(prompts, "\n----\n")
	for _, want := range []string{
		`Тарас (A 7-year-old boy in a red jacket; voice: a boy of about 7, soft high voice) says (whispers, scared): "Хто тут?"`,
		`An off-screen narrator (voice: A calm, warm grandmother's voice.; nobody in frame moves their lips) says: "І вони пішли разом."`,
		"no subtitles, no captions, no on-screen text", "only the lines above are spoken", "Sound: no speech",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("clip prompts lack %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "No dialogue, no speech") {
		t.Error("an in-video clip forbids speech")
	}
	// The montage plays the clips at full level with the lines as subtitles.
	opts := montageOptions(&p, e.Dir(p.ID))
	if s := opts.Segments[0]; s.ClipVolume != 1 || !s.ClipSpeech || len(s.Captions) != 2 || s.Captions[0].Text != "Хто тут?" || s.Voice != "" {
		t.Fatalf("montage segment %+v", s)
	}
	if s := opts.Segments[2]; s.ClipVolume != 1 || s.ClipSpeech || len(s.Captions) != 0 {
		t.Fatalf("silent scene segment %+v", s)
	}
}

// A film without people: the cast are objects with object references, the
// shots are told there are no people.
func TestPlanNoPeople(t *testing.T) {
	env, mm := modesEnv(t, func(string) any {
		return map[string]any{
			"title": "Компас", "logline": "Історія старого компаса.", "styleGuide": "Macro, warm.",
			"objects":   []any{map[string]any{"name": "Компас", "role": "the hero object", "description": "A vintage brass compass."}},
			"locations": []any{map[string]any{"name": "Стіл", "description": "An old oak desk."}},
			"scenes": []any{
				map[string]any{"title": "Компас", "narration": "Він бачив усе.", "objects": []any{"Компас"}, "location": "Стіл",
					"onScreen": map[string]any{"title": "1912 рік", "anim": "explode"}},
				map[string]any{"title": "Мапа", "narration": "І мапу теж.", "objects": []any{}, "location": "Стіл"},
			},
		}
	}, nil)
	e := env.e
	p, err := e.CreateProject(NewProjectOptions{Text: "Історія старого компаса, що бачив пів світу.", Content: ContentNoPeople, TargetSeconds: 16})
	must(t, err)
	must(t, e.Run(p.ID, StagePlan, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if len(p.Characters) != 1 || !p.Characters[0].IsObject() || p.Characters[0].Gender != "" {
		t.Fatalf("objects %+v", p.Characters)
	}
	if !strings.Contains(p.Job.Message, "") || p.Scenes[0].CharacterIDs[0] != p.Characters[0].ID {
		t.Fatalf("scene objects %+v", p.Scenes[0])
	}
	if !p.Scenes[1].OnScreen.Empty() || !p.Scenes[0].OnScreen.Empty() {
		t.Fatal("on-screen text kept although the text is off for this film")
	}
	shots := mm.last("storyboard artist")
	for _, want := range []string{"Recurring objects", "@kompas", "NO people anywhere", "visible objects: @kompas"} {
		if !strings.Contains(shots, want) {
			t.Errorf("shots prompt lacks %q", want)
		}
	}
	if strings.Contains(shots, "visible characters") {
		t.Error("shots prompt asks for characters")
	}
	if ref := characterPrompt(&p, &p.Characters[0]); !strings.Contains(ref, "Object reference image of Компас") || !strings.Contains(ref, "No people") {
		t.Fatalf("object reference prompt %q", ref)
	}
	fp := framePrompt(&p, &p.Scenes[0])
	if !strings.Contains(fp, "Objects: @kompas is Компас — identical shape") || !strings.Contains(fp, "No people anywhere in the frame") {
		t.Fatalf("frame prompt %q", fp)
	}
}

// Motion graphics: the plan keeps the on-screen text; the montage renders
// it as overlay frames.
func TestPlanMotionOverlay(t *testing.T) {
	env, _ := modesEnv(t, func(string) any {
		return map[string]any{
			"title": "Населення", "logline": "Скільки нас.", "styleGuide": "Flat navy design.",
			"objects":   []any{},
			"locations": []any{map[string]any{"name": "Фон", "description": "Navy gradient with a dot grid."}},
			"scenes": []any{
				map[string]any{"title": "Скільки нас", "narration": "Нас понад вісім мільярдів.", "location": "Фон",
					"onScreen": map[string]any{"title": "Населення Землі", "value": "8,1 млрд", "caption": "2025 рік", "anim": "count"}},
			},
		}
	}, nil)
	e := env.e
	p, err := e.CreateProject(NewProjectOptions{Text: "Скільки людей на Землі і як ми рахуємо.", Content: ContentMotion, TargetSeconds: 8})
	must(t, err)
	must(t, e.Run(p.ID, StagePlan, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	o := p.Scenes[0].OnScreen
	if o.Title != "Населення Землі" || o.Value != "8,1 млрд" || o.Anim != OverlayCount {
		t.Fatalf("on-screen %+v", o)
	}
	opts := montageOptions(&p, e.Dir(p.ID))
	if opts.Overlay == nil {
		t.Fatal("no overlay renderer for a motion film")
	}
	dir := t.TempDir()
	ov, ok, err := opts.Overlay(context.Background(), 0, media.OverlaySpan{Duration: 6, In: 0.5}, image.Pt(1280, 720), dir)
	if err != nil || !ok {
		t.Fatalf("overlay: %v %v", ok, err)
	}
	list, err := os.ReadFile(ov.List)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.png"))
	if !strings.HasPrefix(string(list), "ffconcat version 1.0") || len(files) < 10 || len(files) > 80 || ov.X < 0 || ov.Y < 0 {
		t.Fatalf("overlay list (%d frames, at %d,%d):\n%s", len(files), ov.X, ov.Y, list)
	}
	total := 0.0
	for _, l := range strings.Split(string(list), "\n") {
		var d float64
		if _, err := fmt.Sscanf(l, "duration %f", &d); err == nil {
			total += d
		}
	}
	if total < 5.99 || total > 6.01 {
		t.Fatalf("overlay lasts %.3f s, want 6", total)
	}
	// Text off: no renderer.
	p.Input.Overlays = "off"
	if montageOptions(&p, e.Dir(p.ID)).Overlay != nil {
		t.Fatal("overlay renderer with the text off")
	}
}

// ---- production, clip lengths -------------------------------------------------------------------

func TestClipPromptModes(t *testing.T) {
	p := &Project{Input: DefaultInput(), StyleGuide: "Warm."}
	p.Characters = []Character{{ID: "c1", Name: "Тарас", Tag: "taras", Description: "A 7-year-old boy in a red jacket, freckles.", Gender: "male"}}
	p.Scenes = []Scene{{ID: "s1", MotionPrompt: "Slow push-in.", CharacterIDs: []string{"c1"},
		Lines: []Line{{Speaker: "c1", Text: `Він сказав "так"`}}}}
	in, _ := shotInputsOf(p, "s1")
	if got := clipPrompt(&in); got != motionPrompt(&p.Scenes[0]) {
		t.Fatalf("the classic clip prompt changed: %q", got)
	}
	p.Input.AudioMode, p.Input.Voiceover = AudioInVideo, false
	in, _ = shotInputsOf(p, "s1")
	got := clipPrompt(&in)
	if !strings.HasPrefix(got, `Тарас (A 7-year-old boy in a red jacket; voice: a natural male voice) says: "Він сказав 'так'"`) ||
		!strings.Contains(got, "Slow push-in.") || !strings.Contains(got, "only the lines above are spoken") {
		t.Fatalf("in-video prompt:\n%s", got)
	}
	refs := []videoRef{{role: refStoryboard}, {role: roleCast, tag: "taras", name: "Тарас"}}
	if cp := componentsPrompt(&in, refs); !strings.HasPrefix(cp, "Тарас (") || strings.Contains(cp, "No dialogue") {
		t.Fatalf("components prompt:\n%s", cp)
	}
	p.Input.Content = ContentNoPeople
	p.Scenes[0].Lines = nil
	in, _ = shotInputsOf(p, "s1")
	if got := clipPrompt(&in); !strings.Contains(got, "No people appear") || !strings.Contains(got, "Sound: no speech") {
		t.Fatalf("no-people prompt:\n%s", got)
	}
}

func TestClipLengthFollowsLines(t *testing.T) {
	c := config.Default()
	c.Video = config.Video{Provider: "glabs", GlabsModel: "veo_31_fast", Length: 8, Ultra: true, Dynamic: true}
	short := &Scene{Lines: []Line{{Speaker: "c1", Text: "Привіт!"}}}
	long := &Scene{Lines: []Line{{Speaker: "c1", Text: "Сьогодні ми підемо далеко в ліс"}, {Speaker: "c2", Text: "Обережно, там вовки й туман"}}}
	if got := sceneClip(c, short); got != 4 {
		t.Errorf("short line: %d s", got)
	}
	if got := sceneClip(c, long); got != 8 { // 11 words at 2 w/s ≈ 5.5 s + a pause + the beats around = 6.8 s
		t.Errorf("two lines: %d s", got)
	}
	start, end := lineTiming(long.Lines, 8)
	if start[0] < 0.4 || end[1] > 8 || start[1] <= end[0] {
		t.Fatalf("timing %v %v", start, end)
	}
	if n := DialogueWords(8); n < 12 || n > 18 {
		t.Fatalf("dialogue words for 8 s: %d", n)
	}
}

func TestVideoSpeech(t *testing.T) {
	cfg := func(provider, preset, model, veo string) config.Config {
		c := config.Default()
		c.Video.Provider, c.Custom.VideoPreset, c.Custom.VideoModel, c.Google.VeoModel = provider, preset, model, veo
		return c
	}
	for name, tc := range map[string]struct {
		c    config.Config
		want Speech
	}{
		"glabs":          {cfg("glabs", "", "", ""), SpeechYes},
		"google veo 3.1": {cfg("google", "", "", "veo-3.1-generate-preview"), SpeechYes},
		"google veo 2":   {cfg("google", "", "", "veo-2.0-generate-001"), SpeechNo},
		"royal omni":     {cfg("custom", "royaltechno", "omni-flash", ""), SpeechYes},
		"royal veo":      {cfg("custom", "royaltechno", "veo-3.1", ""), SpeechYes},
		"royal seedance": {cfg("custom", "royaltechno", "seedance-2.0", ""), SpeechMaybe},
		"royal grok":     {cfg("custom", "royaltechno", "grok-imagine-1.5", ""), SpeechMaybe},
		"runway gen4":    {cfg("custom", "runway", "gen4_turbo", ""), SpeechNo},
		"luma":           {cfg("custom", "luma", "ray-3.2", ""), SpeechNo},
		"hailuo":         {cfg("custom", "minimax", "MiniMax-Hailuo-2.3", ""), SpeechNo},
		"fal veo":        {cfg("custom", "fal", "fal-ai/veo3.1/fast/image-to-video", ""), SpeechYes},
	} {
		if got := VideoSpeech(tc.c); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}
}

// ---- profiles, translation ---------------------------------------------------------------------

func TestProfileModes(t *testing.T) {
	env := newEnv(t, nil)
	e := env.e
	pr, err := e.SaveProfile(config.Profile{Name: "Моушн", AudioMode: AudioInVideo, Content: ContentMotion, Overlays: "on", Voiceover: true})
	must(t, err)
	if pr.Voiceover || pr.AudioMode != AudioInVideo {
		t.Fatalf("profile %+v", pr)
	}
	p, err := e.CreateWithProfile("", pr.ID)
	must(t, err)
	if p.Input.SoundMode() != AudioInVideo || p.Input.Content != ContentMotion || p.Input.Overlays != "on" || p.Input.Voiceover {
		t.Fatalf("applied %+v", p.Input)
	}
	back, err := e.ProfileFromProject(p.ID, "Назад")
	must(t, err)
	if back.AudioMode != AudioInVideo || back.Content != ContentMotion || back.Overlays != "on" {
		t.Fatalf("from project %+v", back)
	}
	// An older profile (no modes) gives the classic film again.
	old, err := e.SaveProfile(config.Profile{Name: "Старий", Voiceover: true})
	must(t, err)
	must(t, e.ApplyProfile(p.ID, old.ID))
	q, _ := e.Get(p.ID)
	if q.Input.SoundMode() != AudioVoiceover || q.Input.ContentMode() != ContentCharacters {
		t.Fatalf("older profile %+v", q.Input)
	}
}

// Translating an in-video film translates the lines and the on-screen text;
// the clips (they speak the source language) are flagged stale; no voices.
func TestTranslateInVideo(t *testing.T) {
	env, mm := modesEnv(t, func(string) any { return map[string]any{} }, nil)
	e := env.e
	p, err := e.Create("Діалог")
	must(t, err)
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Input.AudioMode, p.Input.Language, p.Input.Content = AudioInVideo, "uk", ContentMixed
		p.Input.Overlays = "on"
		p.Characters = []Character{{ID: "char0001", Name: "Тарас"}}
		p.Scenes = []Scene{{ID: "scene001", Title: "Ліс", Lines: []Line{{Speaker: "char0001", Text: "Хто тут?"}, {Speaker: "narrator", Text: "Тиша."}},
			OnScreen: OnScreen{Title: "Населення", Value: "8,1 млрд", Caption: "у 2025"}}}
	}))
	st := e.state(p.ID)
	must(t, os.MkdirAll(filepath.Join(st.dir, "videos"), 0o755))
	must(t, os.WriteFile(filepath.Join(st.dir, "videos", "x.mp4"), []byte("clip"), 0o644))
	st.mu.Lock()
	st.p.Scenes[0].Video = Asset{Status: StatusDone, Path: "videos/x.mp4", Duration: 8}
	st.mu.Unlock()
	calls := len(eleven.calls)
	id, err := e.Translate(p.ID, TranslateOptions{Language: "en"})
	must(t, err)
	q := waitJob(t, e, id)
	mustJobDone(t, q)
	s := q.Scenes[0]
	if s.Lines[0].Text != "EN Хто тут?" || s.Lines[1].Text != "EN Тиша." || s.Lines[0].Speaker != "char0001" || !s.Video.Stale {
		t.Fatalf("translated scene %+v", s)
	}
	if s.OnScreen.Title != "Population" || s.OnScreen.Value != "8.1 bn" || s.OnScreen.Caption != "in 2025" {
		t.Fatalf("on-screen %+v", s.OnScreen)
	}
	if len(eleven.calls) != calls {
		t.Fatal("voiced an in-video translation")
	}
	if !strings.Contains(q.Job.Message, "мовою оригіналу") {
		t.Fatalf("message %q", q.Job.Message)
	}
	batch := mm.last("professional dubbing translator")
	if !strings.Contains(batch, `"speaker": "Тарас"`) || !strings.Contains(batch, `"onScreen"`) {
		t.Fatalf("batch prompt:\n%s", batch)
	}
}
