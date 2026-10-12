package studio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
	"ytcartell/internal/tts"
)

// multiVoiceParts is what the bible mock says in a multi-voice film: scene 1
// narrator + «Диктор 2», scene 2 narrator + the fox, the rest the narrator.
func multiVoiceParts(i int) []any {
	first := map[string]any{"speaker": "narrator", "text": fmt.Sprintf("Модельний рядок номер %d.", i+1)}
	switch i {
	case 0:
		return []any{first, map[string]any{"speaker": "narrator2", "text": "Другий диктор додає факт."}}
	case 1:
		return []any{first, map[string]any{"speaker": "Лисиця", "text": "Я знаю дорогу додому!", "delivery": "warm, calm"}}
	}
	return []any{first}
}

// dialogue answers ElevenLabs text-to-dialogue/with-timestamps: the test
// voice, every input placed one second apart.
func (m *elevenMock) dialogue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Inputs []struct {
			Text    string `json:"text"`
			VoiceID string `json:"voice_id"`
		} `json:"inputs"`
		Model string `json:"model_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var calls []ttsCall
	var segs []any
	for i, in := range body.Inputs {
		calls = append(calls, ttsCall{in.VoiceID, in.Text})
		segs = append(segs, map[string]any{"voice_id": in.VoiceID, "start_time_seconds": float64(i), "end_time_seconds": float64(i) + 0.8,
			"character_start_index": 0, "character_end_index": len(in.Text), "dialogue_input_index": i})
	}
	m.mu.Lock()
	m.dialogues = append(m.dialogues, calls)
	m.mu.Unlock()
	if body.Model != "eleven_v3" {
		w.WriteHeader(400)
		return
	}
	audio := testVoice
	if len(audio) == 0 {
		audio = []byte("ID3 fake")
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"audio_base64": base64.StdEncoding.EncodeToString(audio), "voice_segments": segs})
}

func (m *elevenMock) dialogueWith(text string) []ttsCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.dialogues {
		for _, c := range d {
			if strings.Contains(c.text, text) {
				return d
			}
		}
	}
	return nil
}

// multiVoiceProject plans a multi-voice film with one extra narrator.
func multiVoiceProject(t *testing.T, env *testEnv, text string) Project {
	t.Helper()
	e := env.e
	p, err := e.Create("Мультиголос")
	must(t, err)
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Input.Text = text
		p.Input.AudioMode = AudioVoiceover
		p.Input.MultiVoice = true
		p.Input.ExtraNarrators = []string{""}
	}))
	must(t, e.Run(p.ID, StagePlan, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	return p
}

// The plan asks for speaker parts, keeps them (Narration = their words), and
// gives every speaker a voice of its own matching the gender.
func TestMultiVoicePlan(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	p := multiVoiceProject(t, env, "Історія про Тараса й лисицю, що знає дорогу додому через осінній ліс.")
	env.text.mu.Lock()
	bible := env.text.biblePrompts[len(env.text.biblePrompts)-1]
	env.text.mu.Unlock()
	for _, want := range []string{`"parts": [ { "speaker", "text", "delivery" } ]`, `"narrator2"`, `"age": "child|young|adult|old"`} {
		if !strings.Contains(bible, want) {
			t.Fatalf("bible prompt lacks %s", want)
		}
	}
	fox := ""
	for _, c := range p.Characters {
		if c.Name == "Лисиця" {
			fox = c.ID
		}
	}
	s0, s1 := p.Scenes[0], p.Scenes[1]
	if len(s0.Parts) != 2 || s0.Parts[1].Speaker != narrator2ID || s0.Narration != "Модельний рядок номер 1. Другий диктор додає факт." {
		t.Fatalf("scene 1 parts: %+v / %q", s0.Parts, s0.Narration)
	}
	if len(s1.Parts) != 2 || s1.Parts[1].Speaker != fox || s1.Parts[1].Delivery != "warm, calm" || s1.Speaker != narratorID {
		t.Fatalf("scene 2 parts: %+v speaker %q", s1.Parts, s1.Speaker)
	}
	// Voices: the narrator (first listed: male Adam), the fox (female) the
	// usable female voice, «Диктор 2» a contrasting (female) voice — on this
	// free account only Adam and Bella are usable, so it shares Bella.
	if p.Input.NarratorVoiceID != "v-adam" || len(p.Input.ExtraNarrators) != 1 || p.Input.ExtraNarrators[0] != "v-bella" {
		t.Fatalf("narrators: %q %q", p.Input.NarratorVoiceID, p.Input.ExtraNarrators)
	}
	if c := findCharacter(&p, fox); c == nil || c.VoiceID != "v-bella" {
		t.Fatalf("fox voice: %+v", c)
	}
	if got := VoiceSpeakers(&p); len(got) != 3 || got[2] != fox {
		t.Fatalf("speakers: %v", got)
	}
}

// Each part is voiced with its speaker's voice and joined into one line;
// the parts are timed in it and the subtitles follow them in colour.
func TestMultiVoiceVoicesJoined(t *testing.T) {
	t.Parallel()
	needMedia(t)
	env := newEnv(t, nil)
	e := env.e
	p := multiVoiceProject(t, env, "Тарас загубився, а лисиця, що знає ліс, вивела його додому до бабусі.")
	img := filepath.Join(t.TempDir(), "frame.jpg")
	must(t, os.WriteFile(img, testImage, 0o644))
	for _, s := range p.Scenes { // the montage lays out scenes with a picture
		must(t, e.ImportImage(p.ID, RegenFrame, s.ID, img))
	}
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Scenes[1].Parts[0].Text = "Диктор розповідає про стежку." // unique for this test (the mocks are shared)
		p.Scenes[1].Parts[1].Text = "Я знаю дорогу, ходімо разом!"
		p.Montage.SubtitleStyle.SpeakerColors = true
	}))
	must(t, e.Run(p.ID, StageVoices, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	s := p.Scenes[1]
	fox := s.Parts[1].Speaker
	if v := eleven.voiceFor("Я знаю дорогу, ходімо разом!"); v != findCharacter(&p, fox).VoiceID {
		t.Fatalf("the fox's part was voiced with %q", v)
	}
	if v := eleven.voiceFor("Диктор розповідає про стежку."); v != p.Input.NarratorVoiceID {
		t.Fatalf("the narrator's part was voiced with %q", v)
	}
	if s.Audio.Status != StatusDone || s.Audio.Duration < 3 {
		t.Fatalf("joined line: %+v", s.Audio)
	}
	a, b := s.Parts[0], s.Parts[1]
	if a.Start != 0 || math.Abs(a.End-1.5) > 0.1 || math.Abs(b.Start-(a.End+partGapTurn)) > 0.01 || b.End <= b.Start || b.End > s.Audio.Duration+0.1 {
		t.Fatalf("part timings: %+v %+v (line %.2f s)", a, b, s.Audio.Duration)
	}
	// A narrator-only scene: one request, one part over the whole line.
	if s2 := p.Scenes[2]; len(s2.Parts) != 1 || s2.Parts[0].Start != 0 || math.Abs(s2.Parts[0].End-s2.Audio.Duration) > 0.01 {
		t.Fatalf("single part: %+v (%.2f s)", s2.Parts, s2.Audio.Duration)
	}
	// Subtitles: every part at its own time; the fox in its colour.
	opts := montageOptions(&p, e.Dir(p.ID))
	if vp := opts.Segments[1].VoiceParts; len(vp) != 2 || vp[0].Color != "" || vp[1].Color != SpeakerColor(&p, fox) || vp[1].Color == "" {
		t.Fatalf("voice parts: %+v", vp)
	}
	var texts []string
	for _, c := range media.SubtitleCues(opts) {
		texts = append(texts, c.Text)
	}
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "Диктор розповідає про стежку.|Я знаю дорогу, ходімо разом!") {
		t.Fatalf("cues: %s", joined)
	}
	// Editing a part makes the line stale and drops that part's timing only.
	must(t, e.Edit(p.ID, func(p *Project) { p.Scenes[1].Parts[1].Text = "Ходімо!" }))
	p, _ = e.Get(p.ID)
	if s := p.Scenes[1]; !s.Audio.Stale || s.Parts[1].End != 0 || s.Parts[0].End == 0 || s.Narration != "Диктор розповідає про стежку. Ходімо!" {
		t.Fatalf("after the edit: %+v %+v", s.Audio, s.Parts)
	}
}

// With Eleven v3 the scene is voiced in one text-to-dialogue request (audio
// tags from the delivery) and the reply's segments time the parts.
func TestMultiVoiceDialogueRequest(t *testing.T) {
	t.Parallel()
	env := newEnv(t, func(c *config.Config) { c.TTS.Model = "eleven_v3" })
	e := env.e
	p := multiVoiceProject(t, env, "Лисиця заговорила з Тарасом і повела його додому стежкою.")
	must(t, e.Edit(p.ID, func(p *Project) { p.Scenes[1].Parts[1].Text = "Не бійся, я поруч." }))
	p, _ = e.Get(p.ID)
	must(t, e.Regen(p.ID, RegenVoice, p.Scenes[1].ID, ""))
	p = waitFor(t, e, p.ID, "the dialogue voice", func(p Project) bool {
		return p.Scenes[1].Audio.Status == StatusDone || p.Scenes[1].Audio.Status == StatusError
	})
	s := p.Scenes[1]
	if s.Audio.Status != StatusDone {
		t.Fatalf("voice: %+v", s.Audio)
	}
	d := eleven.dialogueWith("Не бійся, я поруч.")
	if len(d) != 2 || d[1].text != "[warm, calm] Не бійся, я поруч." || d[0].voice != p.Input.NarratorVoiceID {
		t.Fatalf("dialogue request: %+v", d)
	}
	if s.Parts[0].Start != 0 || s.Parts[0].End != 0.8 || s.Parts[1].Start != 1 || s.Parts[1].End != 1.8 {
		t.Fatalf("timings from the segments: %+v", s.Parts)
	}
}

// Speakers and voices: matching, fallbacks and edits.
func TestMultiVoiceSpeakers(t *testing.T) {
	voices := []tts.Voice{
		{ID: "en-m", Gender: "male", Language: "en", Usable: true},
		{ID: "uk-narr", Gender: "male", Age: "middle_aged", Language: "uk-UA", Usable: true},
		{ID: "uk-m-old", Gender: "male", Age: "old", Language: "uk-UA", Usable: true},
		{ID: "uk-f-young", Gender: "female", Age: "young", Language: "uk-UA", Usable: true},
		{ID: "any-f", Gender: "female", Usable: true},
		{ID: "uk-m-young", Gender: "male", Age: "young", Language: "uk", Usable: true},
		{ID: "uk-f-mid", Gender: "female", Age: "middle_aged", Language: "uk", Usable: true},
		{ID: "locked-f", Gender: "female", Language: "uk", Usable: false},
	}
	p := Project{
		Input: Input{AudioMode: AudioVoiceover, Voiceover: true, MultiVoice: true, Language: "uk", ExtraNarrators: []string{"", ""}},
		Characters: []Character{
			{ID: "grandpa", Name: "Дід", Gender: "male", Age: AgeOld},
			{ID: "girl", Name: "Оля", Gender: "female", Age: AgeChild},
			{ID: "silent", Name: "Мовчун", Gender: "male"},
			{ID: "box", Name: "Скриня", Kind: KindObject},
		},
		Scenes: []Scene{{Parts: []Line{{Speaker: "grandpa", Text: "Ох."}, {Speaker: "girl", Text: "Діду!"}, {Speaker: "box", Text: "?"}}}},
	}
	assignSpeakerVoices(&p, voices, "", "uk", false)
	if p.Input.NarratorVoiceID != "uk-narr" {
		t.Fatalf("narrator: %q (the film is Ukrainian, an adult voice)", p.Input.NarratorVoiceID)
	}
	if got := p.Input.ExtraNarrators; got[0] != "uk-f-mid" || got[1] != "uk-m-young" {
		t.Fatalf("extra narrators: %q (a contrasting voice, then the narrator's gender)", got)
	}
	byID := map[string]string{}
	for _, c := range p.Characters {
		byID[c.ID] = c.VoiceID
	}
	if byID["grandpa"] != "uk-m-old" || byID["girl"] != "uk-f-young" || byID["silent"] != "" || byID["box"] != "" {
		t.Fatalf("cast voices: %v", byID)
	}
	seen := map[string]bool{}
	for _, id := range append([]string{p.Input.NarratorVoiceID, byID["grandpa"], byID["girl"]}, p.Input.ExtraNarrators...) {
		if id == "" || seen[id] {
			t.Fatalf("voices must be distinct and set: %q %v %v", p.Input.NarratorVoiceID, p.Input.ExtraNarrators, byID)
		}
		seen[id] = true
	}
	// Set voices stay; «all» picks again.
	p.Characters[0].VoiceID = "any-f"
	assignSpeakerVoices(&p, voices, "", "uk", false)
	if p.Characters[0].VoiceID != "any-f" {
		t.Fatal("a chosen voice was replaced")
	}
	assignSpeakerVoices(&p, voices, "", "uk", true)
	if p.Characters[0].VoiceID != "uk-m-old" {
		t.Fatalf("all: %q", p.Characters[0].VoiceID)
	}

	// Speaker names written by the model.
	byName := func(n any) *Character {
		for i := range p.Characters {
			if strings.EqualFold(p.Characters[i].Name, jsString(n)) {
				return &p.Characters[i]
			}
		}
		return nil
	}
	in := Input{ExtraNarrators: []string{"v"}}
	for raw, want := range map[string]string{"Narrator": narratorID, "narrator 2": narrator2ID, "Диктор 3": narratorID, "дід": "grandpa", "Скриня": narratorID, "Хтось": narratorID} {
		if got := partSpeaker(in, raw, byName); got != want {
			t.Errorf("%q → %q, want %q", raw, got, want)
		}
	}
	// A script part said with other words falls back to one narrator part.
	scene := map[string]any{"parts": []any{map[string]any{"speaker": "Дід", "text": "Зовсім інші слова"}}}
	obj, _ := parseJSON(mustJSON(t, scene))
	if got := planParts(in, obj, "Ох, — сказав дід.", byName); len(got) != 1 || got[0].Speaker != narratorID || got[0].Text != "Ох, — сказав дід." {
		t.Fatalf("verbatim fallback: %+v", got)
	}
	scene = map[string]any{"parts": []any{map[string]any{"speaker": "Дід", "text": "«Ох»,"}, map[string]any{"speaker": "narrator", "text": "сказав дід."}}}
	obj, _ = parseJSON(mustJSON(t, scene))
	if got := planParts(in, obj, "«Ох», — сказав дід.", byName); len(got) != 2 || got[0].Speaker != "grandpa" {
		t.Fatalf("verbatim split: %+v", got)
	}
	if SpeakerLabel(&p, narrator2ID) != "Диктор 2" || SpeakerLabel(&p, "girl") != "Оля" || SpeakerLabel(&p, "gone") != "Диктор" {
		t.Fatal("labels")
	}
	if SpeakerColor(&p, narratorID) != "" || SpeakerColor(&p, "girl") == SpeakerColor(&p, "grandpa") {
		t.Fatal("colours")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	must(t, err)
	return b
}

// Edits: parts of removed characters go to the narrator, the whole line
// edited at once collapses the parts, old projects stay untouched.
func TestMultiVoiceEdit(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, err := e.Create("Правки")
	must(t, err)
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Input.AudioMode, p.Input.MultiVoice = AudioVoiceover, true
		p.Input.ExtraNarrators = []string{" v-2 ", "v-3", "v-4"}
		p.Characters = []Character{{ID: "fox", Name: "Лисиця"}}
		p.Scenes = []Scene{
			{ID: "a", Parts: []Line{{Speaker: "narrator", Text: "Раз."}, {Speaker: "fox", Text: "Два!"}, {Speaker: "", Text: " "}}},
			{ID: "b", Narration: "Старий рядок."},
		}
	}))
	p, _ = e.Get(p.ID)
	if got := p.Input.ExtraNarrators; len(got) != 2 || got[0] != "v-2" {
		t.Fatalf("extra narrators: %q", got)
	}
	if s := p.Scenes[0]; len(s.Parts) != 2 || s.Narration != "Раз. Два!" {
		t.Fatalf("parts: %+v %q", s.Parts, s.Narration)
	}
	if p.Scenes[1].Parts != nil {
		t.Fatal("a scene without parts got some")
	}
	must(t, e.Edit(p.ID, func(p *Project) { p.Characters = nil }))
	p, _ = e.Get(p.ID)
	if s := p.Scenes[0]; s.Parts[1].Speaker != narratorID {
		t.Fatalf("a removed character's part: %+v", s.Parts)
	}
	must(t, e.Edit(p.ID, func(p *Project) { p.Scenes[0].Narration = "Усе разом." }))
	p, _ = e.Get(p.ID)
	if s := p.Scenes[0]; len(s.Parts) != 1 || s.Parts[0].Text != "Усе разом." || s.Parts[0].Speaker != narratorID {
		t.Fatalf("narration edited as a whole: %+v", s.Parts)
	}
	// AssignVoices without a voice list: the provider's error.
	if err := e.AssignVoices(context.Background(), "missing", false); err != ErrNotFound {
		t.Fatalf("missing project: %v", err)
	}
}

// A translation keeps every part's speaker and voices the parts again.
func TestMultiVoiceTranslate(t *testing.T) {
	t.Parallel()
	env, tm, tr := translateEnv(t)
	e := env.e
	src := sourceProject(t, e)
	must(t, e.Edit(src.ID, func(p *Project) {
		p.Input.AudioMode, p.Input.MultiVoice = AudioVoiceover, true
		p.Scenes[1].Parts = []Line{{Speaker: "narrator", Text: "Раптом вийшла лисиця."}, {Speaker: "ch-fox", Text: "Привіт, Тарасе!"}}
	}))
	id, err := e.Translate(src.ID, TranslateOptions{Language: "en"})
	must(t, err)
	p := waitJob(t, e, id)
	mustJobDone(t, p)
	s := findScene(&p, "sc-2")
	if len(s.Parts) != 2 || s.Parts[1].Speaker != "ch-fox" || s.Parts[0].Text != "EN Раптом вийшла лисиця." || s.Parts[1].Text != "EN Привіт, Тарасе!" ||
		s.Narration != "EN Раптом вийшла лисиця. EN Привіт, Тарасе!" {
		t.Fatalf("translated parts: %+v %q", s.Parts, s.Narration)
	}
	tm.mu.Lock()
	batch := strings.Join(tm.batches, "\n")
	tm.mu.Unlock()
	if !strings.Contains(batch, `"speaker": "Лисиця"`) || !strings.Contains(batch, `"parts"`) {
		t.Fatalf("the batch lacks the parts: %s", batch)
	}
	if testFF.FFmpeg != "" {
		calls := tr.byText()
		if c := calls["EN Привіт, Тарасе!"]; c == nil || c["voice"] != "v-fox" {
			t.Fatalf("the fox's translated part: %v", c)
		}
	}
}
