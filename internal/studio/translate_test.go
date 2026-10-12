package studio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
)

// ---- mocks ----------------------------------------------------------------------------------

// trMock is the text model of a translation: the header, scene batches and
// the "too long" retry, in English.
type trMock struct {
	mu      sync.Mutex
	gate    chan struct{}   // requests wait until it is closed (nil = no wait)
	long    map[string]bool // scene ids whose first translation is far too long
	failAll bool            // scene batches fail (400)
	header  []string        // prompts, by kind
	batches []string
	retries []string
}

var english = map[string]string{
	"sc-1": "Taras got lost in the autumn forest.",  // 7 words ≈ 3.0 s (source voice 3 s)
	"sc-2": "Then a red fox came out of the trees.", // 9 words ≈ 3.9 s (3.5 s)
	"sc-3": "She led the boy home.",                 // 5 words ≈ 2.2 s (2.5 s)
}

func (m *trMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	gate := m.gate
	m.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
	}
	_, input, _ := strings.Cut(content, "### INPUT\n")
	var reply any
	m.mu.Lock()
	switch {
	case strings.Contains(content, "professional film translator"):
		m.header = append(m.header, content)
		reply = map[string]any{
			"title": "The Way Home", "logline": "A boy looks for the way home.",
			"characters": []any{
				map[string]any{"id": "ch-fox", "name": "Fox", "role": "guide"},
				map[string]any{"id": "ch-taras", "name": "Taras", "role": "lost boy"},
			},
			"cover": map[string]any{"title": "THE WAY HOME", "subtitle": "", "badge": "NEW"},
		}
	case strings.Contains(content, "too long for their scenes"):
		m.retries = append(m.retries, content)
		var lines []struct{ ID string }
		_ = json.Unmarshal([]byte(input), &lines)
		var out []any
		for _, l := range lines {
			out = append(out, map[string]any{"id": l.ID, "narration": english[l.ID]})
		}
		reply = out
	case strings.Contains(content, "professional dubbing translator"):
		m.batches = append(m.batches, content)
		if m.failAll {
			m.mu.Unlock()
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"message":"bad request"}}`))
			return
		}
		var lines []struct {
			ID, Title string
			Parts     []struct{ Speaker, Text string } // multi-voice (multivoice_test.go)
		}
		_ = json.Unmarshal([]byte(input), &lines)
		var out []any
		for i := len(lines) - 1; i >= 0; i-- { // reversed: matched by id
			l := lines[i]
			n := english[l.ID]
			if m.long[l.ID] {
				n = strings.Repeat("and then something else happened in the forest ", 5)
			}
			o := map[string]any{"id": l.ID, "title": "EN " + l.Title, "narration": n}
			if len(l.Parts) > 0 {
				var parts []any
				for _, sp := range l.Parts {
					parts = append(parts, map[string]any{"speaker": sp.Speaker, "text": "EN " + sp.Text})
				}
				o["parts"] = parts
			}
			out = append(out, o)
		}
		reply = out
	default:
		reply = "OK"
	}
	m.mu.Unlock()
	b, _ := json.Marshal(reply)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": string(b)}}}})
}

// ttsRec is a "custom" TTS server that records language, voice and text.
type ttsRec struct {
	mu    sync.Mutex
	calls []map[string]any
	fail  string // texts containing this fail
	audio []byte
}

func (m *ttsRec) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	m.mu.Lock()
	m.calls = append(m.calls, body)
	n := len(m.calls)
	fail := m.fail != "" && strings.Contains(fmt.Sprint(body["text"]), m.fail)
	m.mu.Unlock()
	// Like a real service, and staggered: however many voices run at once,
	// they finish one by one, so the progress messages are observable.
	time.Sleep(time.Duration(40+60*n) * time.Millisecond)
	if fail {
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Write(m.audio)
}

func (m *ttsRec) byText() map[string]map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]map[string]any{}
	for _, c := range m.calls {
		out[fmt.Sprint(c["text"])] = c
	}
	return out
}

func translateEnv(t *testing.T) (*testEnv, *trMock, *ttsRec) {
	t.Helper()
	tm := &trMock{long: map[string]bool{}}
	ts := httptest.NewServer(tm)
	allow(ts)
	t.Cleanup(ts.Close)
	audio := testVoice
	if len(audio) == 0 {
		audio = []byte("ID3 fake mp3")
	}
	tr := &ttsRec{audio: audio}
	vs := httptest.NewServer(tr)
	allow(vs)
	t.Cleanup(vs.Close)
	env := newEnv(t, func(c *config.Config) {
		c.Text.BaseURL = ts.URL + "/v1"
		c.TTS = config.TTS{Provider: "custom", BaseURL: vs.URL + "/tts"}
	})
	return env, tm, tr
}

// sourceProject is a finished Ukrainian film: references, frames, a 3×3
// board, clips, a chained last frame, voices, music, cover, final render.
func sourceProject(t *testing.T, e *Engine) Project {
	t.Helper()
	tmp := t.TempDir()
	img := filepath.Join(tmp, "ref.jpg")
	pic := jpegOf(t, 320, 180, color.RGBA{60, 110, 170, 255})
	must(t, os.WriteFile(img, pic, 0o644))
	song := filepath.Join(tmp, "song.mp3")
	music := testVoice // a real mp3 when ffmpeg made one (the montage mixes it in)
	if len(music) == 0 {
		music = []byte("ID3 song")
	}
	must(t, os.WriteFile(song, music, 0o644))
	p, err := e.Create("Стежка додому")
	must(t, err)
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Input.Text = "Історія про Тараса."
		p.Input.Language, p.Input.Tone, p.Input.NarratorVoiceID = "uk", "storyteller", "v-narr"
		p.Logline = "Хлопчик шукає дорогу додому."
		p.StyleGuide = "Soft watercolor."
		p.Characters = []Character{
			{ID: "ch-taras", Name: "Тарас", Role: "загублений хлопчик", Description: "A 7-year-old boy in a red jacket."},
			{ID: "ch-fox", Name: "Лисиця", Role: "провідниця", Description: "A small orange fox.", VoiceID: "v-fox"},
		}
		p.Locations = []Location{{ID: "loc-forest", Name: "Осінній ліс", Description: "Autumn forest."}}
		p.Scenes = []Scene{
			{ID: "sc-1", Title: "Загубився", Narration: "Тарас загубився в осінньому лісі.", CharacterIDs: []string{"ch-taras"},
				LocationID: "loc-forest", ImagePrompt: "Wide shot of @taras alone in @osinnii_lis", MotionPrompt: "Slow push-in."},
			{ID: "sc-2", Title: "Лисиця", Narration: "Раптом з-за старого дерева вийшла руда лисиця.", Speaker: "ch-fox",
				CharacterIDs: []string{"ch-taras", "ch-fox"}, LocationID: "loc-forest", ImagePrompt: "@lysytsia meets @taras", MotionPrompt: "Pan left."},
			{ID: "sc-3", Title: "Додому", Narration: "Вона вивела хлопчика додому.", CharacterIDs: []string{"ch-taras", "ch-fox"},
				LocationID: "loc-forest", ImagePrompt: "@lysytsia leads @taras home", MotionPrompt: "Tracking shot."},
		}
		p.Montage.CoverText = CoverText{Title: "Стежка додому", Badge: "НОВЕ", Template: "bold"}
	}))
	p, _ = e.Get(p.ID)
	for _, c := range p.Characters {
		must(t, e.ImportImage(p.ID, RegenCharacter, c.ID, img))
	}
	must(t, e.ImportImage(p.ID, RegenLocation, "loc-forest", img))
	for _, s := range p.Scenes {
		must(t, e.ImportImage(p.ID, RegenFrame, s.ID, img))
	}
	must(t, e.SetMusic(p.ID, song))
	st := e.state(p.ID)
	file := func(sub, name string, data []byte) string {
		rel, err := e.writeAsset(st, sub, name, data)
		must(t, err)
		return rel
	}
	clip := testClip
	if len(clip) == 0 {
		clip = []byte("fake mp4")
	}
	voice := testVoice
	if len(voice) == 0 {
		voice = []byte("ID3 fake")
	}
	durations := map[string]float64{"sc-1": 3, "sc-2": 3.5, "sc-3": 2.5}
	done := func(rel string, d float64) Asset {
		return Asset{Path: rel, Status: StatusDone, Duration: d, UpdatedAt: nowMs(), Progress: 100}
	}
	board := file("images", "board-sc-1-1.jpg", []byte("board"))
	last := file("images", "last-sc-1.jpg", []byte("last frame"))
	_ = last
	cover := file("images", "cover-1.jpg", pic)
	final := file("images", "cover-final-1.jpg", []byte("cover with text"))
	out := file("montage", "final-1.mp4", clip)
	srt := file("montage", "final-1.srt", []byte("1\n00:00:00,000 --> 00:00:01,000\nТарас\n"))
	e.update(st, func(p *Project) {
		for i := range p.Scenes {
			s := &p.Scenes[i]
			s.Video = done(file("videos", "clip-"+s.ID+".mp4", clip), 2)
			s.Audio = done(file("audio", "voice-"+s.ID+".mp3", voice), durations[s.ID])
		}
		p.Scenes[0].Board = done(board, 0)
		p.Montage.Cover = done(cover, 0)
		p.Montage.CoverFinal = done(final, 0)
		p.Montage.Output = done(out, 8)
		p.Montage.SRTPath = srt
		p.Step = 5
	})
	p, _ = e.Get(p.ID)
	return p
}

// jpegOf is a plain w×h JPEG.
func jpegOf(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var b bytes.Buffer
	must(t, jpeg.Encode(&b, img, nil))
	return b.Bytes()
}

// ---- Translate --------------------------------------------------------------------------------

func TestTranslate(t *testing.T) {
	t.Parallel()
	env, tm, tr := translateEnv(t)
	e := env.e
	src := sourceProject(t, e)

	// Validation.
	if _, err := e.Translate(src.ID, TranslateOptions{Language: "xx"}); err == nil || !strings.Contains(err.Error(), "Невідома мова") {
		t.Fatalf("unknown language: %v", err)
	}
	if _, err := e.Translate(src.ID, TranslateOptions{Language: "UK"}); err == nil || !strings.Contains(err.Error(), "уже мовою «Українська»") {
		t.Fatalf("same language: %v", err)
	}
	empty, _ := e.Create("Порожній")
	if _, err := e.Translate(empty.ID, TranslateOptions{Language: "en"}); err == nil || !strings.Contains(err.Error(), "Спершу створи план") {
		t.Fatalf("no plan: %v", err)
	}
	if _, err := e.Translate("missing", TranslateOptions{Language: "en"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}

	tm.mu.Lock()
	tm.gate = make(chan struct{}) // hold the text model: look at the copy first
	tm.long["sc-2"] = true
	gate := tm.gate
	tm.mu.Unlock()
	id, err := e.Translate(src.ID, TranslateOptions{Language: "en", Voice: "v-en"})
	must(t, err)
	notes := jobNotes(t, e, id)
	p, ok := e.Get(id)
	if !ok || id == src.ID {
		t.Fatal("no copy")
	}
	if p.SourceID != src.ID || p.Title != "Стежка додому · English" || p.Input.Language != "en" || p.Input.NarratorVoiceID != "v-en" ||
		p.Job == nil || p.Job.Kind != JobTranslate || p.Job.Status != JobRunning || p.Step != 4 {
		t.Fatalf("copy %q src %q lang %q voice %q job %+v step %d", p.Title, p.SourceID, p.Input.Language, p.Input.NarratorVoiceID, p.Job, p.Step)
	}
	srcDir, dir := e.Dir(src.ID), e.Dir(id)
	same := func(rel string) bool {
		a, err1 := os.Stat(filepath.Join(srcDir, filepath.FromSlash(rel)))
		b, err2 := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		return err1 == nil && err2 == nil && os.SameFile(a, b)
	}
	reused := []string{p.Characters[0].Image.Path, p.Characters[1].Image.Path, p.Locations[0].Image.Path, p.Scenes[0].Board.Path,
		p.Montage.MusicPath, p.Montage.Cover.Path}
	for i, s := range p.Scenes {
		sv := src.Scenes[i]
		if s.Frame != sv.Frame || s.Video != sv.Video || s.Board != sv.Board || s.ImagePrompt != sv.ImagePrompt || s.MotionPrompt != sv.MotionPrompt {
			t.Fatalf("scene %d changed: %+v", i, s)
		}
		if s.Audio.Path != "" || s.Audio.Status == StatusDone {
			t.Fatalf("scene %d voice was reused: %+v", i, s.Audio)
		}
		reused = append(reused, s.Frame.Path, s.Video.Path)
	}
	for _, rel := range reused {
		fileExists(t, filepath.Join(dir, filepath.FromSlash(rel)))
		if !same(rel) {
			t.Errorf("%s is not a hard link of the source's file", rel)
		}
	}
	lastRel := LastFrameRel("sc-1")
	fileExists(t, filepath.Join(dir, filepath.FromSlash(lastRel)))
	if same(lastRel) {
		t.Error("the last-frame cache is rewritten in place: it must be a copy, not a link")
	}
	if p.Montage.Output.Path != "" || p.Montage.SRTPath != "" || p.Montage.CoverFinal.Path != "" {
		t.Fatalf("final render / SRT / cover text reused: %+v", p.Montage)
	}
	for _, rel := range []string{src.Montage.Output.Path, src.Montage.SRTPath, src.Montage.CoverFinal.Path, src.Scenes[0].Audio.Path} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s copied", rel)
		}
	}
	for i, c := range p.Characters {
		if c.Tag != src.Characters[i].Tag || c.ID != src.Characters[i].ID || c.VoiceID != src.Characters[i].VoiceID {
			t.Fatalf("character %d: %+v", i, c)
		}
	}
	close(gate)

	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if p.Job.Message != "Переклад готовий" {
		t.Fatalf("message %q", p.Job.Message)
	}
	// The texts are English; tags, prompts, pictures untouched.
	if p.Title != "The Way Home · English" || p.Logline != "A boy looks for the way home." ||
		p.Montage.CoverText.Title != "THE WAY HOME" || p.Montage.CoverText.Badge != "NEW" || p.Montage.CoverText.Subtitle != "" ||
		p.Montage.CoverText.Template != "bold" {
		t.Fatalf("header %q %q %+v", p.Title, p.Logline, p.Montage.CoverText)
	}
	// The source had a cover with text: the translated text is put on the reused picture.
	if f := p.Montage.CoverFinal; f.Status != StatusDone || f.Path == "" || f.Path == src.Montage.CoverFinal.Path {
		t.Fatalf("translated cover %+v", f)
	}
	if c := p.Characters; c[0].Name != "Taras" || c[0].Role != "lost boy" || c[1].Name != "Fox" || c[1].Role != "guide" ||
		c[0].Tag != "taras" || c[1].Tag != "lysytsia" || c[0].Description != src.Characters[0].Description || p.Locations[0].Name != "Осінній ліс" {
		t.Fatalf("characters %+v / %+v", c, p.Locations[0])
	}
	for i, s := range p.Scenes {
		sv := src.Scenes[i]
		// The clip is shared; a translated line that outgrows it only flags
		// it (VoiceOutgrowsClip — these test clips last 2 s).
		clip := s.Video
		if _, _, short := VoiceOutgrowsClip(&p, &p.Scenes[i]); short != clip.Stale {
			t.Fatalf("scene %d: stale %v, outgrown %v", i, clip.Stale, short)
		}
		clip.Stale = sv.Video.Stale
		if s.Narration != english[s.ID] || s.Title != "EN "+sv.Title || s.ImagePrompt != sv.ImagePrompt || s.MotionPrompt != sv.MotionPrompt ||
			s.Frame != sv.Frame || clip != sv.Video || s.Board != sv.Board {
			t.Fatalf("scene %d: %+v", i, s)
		}
		if s.Audio.Status != StatusDone || !strings.HasPrefix(s.Audio.Path, "audio/voice-") {
			t.Fatalf("scene %d voice %+v", i, s.Audio)
		}
	}
	// Voices: target language, the chosen narrator voice, character voices kept.
	calls := tr.byText()
	for _, s := range p.Scenes {
		c, ok := calls[s.Narration]
		want := "v-en"
		if s.ID == "sc-2" {
			want = "v-fox"
		}
		if !ok || c["language"] != "en" || c["voice"] != want {
			t.Fatalf("tts for %s: %v", s.ID, c)
		}
	}
	if _, ok := calls[src.Scenes[0].Narration]; ok {
		t.Fatal("the Ukrainian line was voiced")
	}

	// The model: one header, one batch, one retry for the line that was too long.
	tm.mu.Lock()
	header, batches, retries := tm.header, tm.batches, tm.retries
	tm.mu.Unlock()
	if len(header) != 1 || len(batches) != 1 || len(retries) != 1 {
		t.Fatalf("requests: %d header, %d batches, %d retries", len(header), len(batches), len(retries))
	}
	mustContain(t, "header", header[0], "from Ukrainian into English", "never translate what a personal name means", `"id": "ch-taras"`, `"badge": "НОВЕ"`)
	// Budget: the source voice of scene 1 lasts 3 s → 3 × 2.3 ≈ 7 words.
	mustContain(t, "batch", batches[0], "Тарас → Taras", "Лисиця → Fox", `"seconds": 3,`, `"words": 7`, `"seconds": 3.5,`, `"words": 8`,
		"±10%", hintOf(Tones, "storyteller"), `"narration": "Тарас загубився в осінньому лісі."`)
	mustContain(t, "retry", retries[0], "too long for their scenes", `"id": "sc-2"`, `"previous": "and then something`)
	mustLack(t, "retry", retries[0], `"id": "sc-1"`, `"id": "sc-3"`)
	if !regexp.MustCompile(`Переклад тексту · \d/3`).MatchString(strings.Join(notes(), "\n")) || !strings.Contains(strings.Join(notes(), "\n"), "Озвучка · ") {
		t.Fatalf("progress messages %q", notes())
	}

	// The source is untouched.
	s2, _ := e.Get(src.ID)
	if s2.Title != src.Title || s2.Scenes[0].Narration != src.Scenes[0].Narration || s2.Scenes[0].Audio != src.Scenes[0].Audio || s2.Characters[0].Name != "Тарас" {
		t.Fatalf("source changed: %+v", s2)
	}
	// Deleting the source keeps the translation's files (links, not references).
	must(t, e.Delete(src.ID))
	fileExists(t, e.Abs(id, p.Scenes[1].Video.Path))
	fileExists(t, e.Abs(id, p.Characters[0].Image.Path))
}

// TestTranslateFailuresAndRetry: a voice that fails is reported and blocks
// the montage; a translation whose text requests failed resumes with Retry.
func TestTranslateFailuresAndRetry(t *testing.T) {
	t.Parallel()
	env, tm, tr := translateEnv(t)
	e := env.e
	src := sourceProject(t, e)

	tr.mu.Lock()
	tr.fail = "red fox"
	tr.mu.Unlock()
	id, err := e.Translate(src.ID, TranslateOptions{Language: "en", Montage: true})
	must(t, err)
	p := waitJob(t, e, id)
	mustJobDone(t, p)
	if p.Job.Message != "Переклад готовий, 1 озвучка з помилкою — монтаж пропущено" || p.Scenes[1].Audio.Status != StatusError ||
		p.Montage.Output.Status != StatusEmpty || p.Input.NarratorVoiceID != "v-narr" {
		t.Fatalf("job %+v, voice %+v, output %+v", p.Job, p.Scenes[1].Audio, p.Montage.Output)
	}

	// Scene requests rejected: nothing to voice, the job fails …
	tr.mu.Lock()
	tr.fail = ""
	tr.mu.Unlock()
	tm.mu.Lock()
	tm.failAll = true
	tm.mu.Unlock()
	id2, err := e.Translate(src.ID, TranslateOptions{Language: "de"})
	must(t, err)
	p = waitJob(t, e, id2)
	if p.Job.Status != JobError || !strings.Contains(p.Job.Error, "400") || p.Title != "The Way Home · Deutsch" || p.Scenes[0].Narration != src.Scenes[0].Narration {
		t.Fatalf("failed translation %+v %q %q", p.Job, p.Title, p.Scenes[0].Narration)
	}
	if !(QueueItem{Kind: JobTranslate, Status: JobError}).Retryable() || !(QueueItem{Kind: JobTranscribe, Status: JobCancelled}).Retryable() {
		t.Fatal("translate and transcribe jobs must be retryable from the queue")
	}
	// … and «Повторити» translates what is missing and voices it.
	tm.mu.Lock()
	tm.failAll = false
	tm.mu.Unlock()
	must(t, e.Retry(id2))
	p = waitJob(t, e, id2)
	mustJobDone(t, p)
	for _, s := range p.Scenes {
		if s.Narration != english[s.ID] || s.Audio.Status != StatusDone {
			t.Fatalf("resumed %s: %q %+v", s.ID, s.Narration, s.Audio)
		}
	}
	calls := tr.byText()
	if c := calls[english["sc-1"]]; c["language"] != "de" {
		t.Fatalf("resumed voices in %v", c["language"])
	}
	// A second retry of a finished translation is refused; a plain project
	// cannot "resume" a translation.
	if err := e.Retry(id2); err == nil {
		t.Fatal("retry of a finished job")
	}
	if err := e.resumeTranslation(src.ID); err == nil || !strings.Contains(err.Error(), "Це не переклад") {
		t.Fatalf("resume of an original: %v", err)
	}
}

// TestTranslateMontage: with Montage the translation ends with a new final
// video of its own.
func TestTranslateMontage(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, _, _ := translateEnv(t)
	e := env.e
	src := sourceProject(t, e)
	id, err := e.Translate(src.ID, TranslateOptions{Language: "en", Montage: true})
	must(t, err)
	p := waitJob(t, e, id)
	mustJobDone(t, p)
	if !strings.HasPrefix(p.Job.Message, "Переклад готовий · ") || p.Montage.Output.Status != StatusDone ||
		same(t, e.Abs(src.ID, src.Montage.Output.Path), e.Abs(id, p.Montage.Output.Path)) {
		t.Fatalf("job %+v output %+v", p.Job, p.Montage.Output)
	}
	fileExists(t, e.Abs(id, p.Montage.Output.Path))
}

func same(t *testing.T, a, b string) bool {
	t.Helper()
	x, err1 := os.Stat(a)
	y, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(x, y)
}

func TestSpeakingTime(t *testing.T) {
	if n, unit := lineBudget(3, "en"); n != 7 || unit != "words" {
		t.Fatalf("en %d %s", n, unit)
	}
	if n, unit := lineBudget(2, "ja"); n != 11 || unit != "characters" {
		t.Fatalf("ja %d %s", n, unit)
	}
	if s := spokenSeconds("Раз два три чотири п'ять шість", ""); s < 2.6 || s > 2.61 {
		t.Fatalf("words %v", s)
	}
	if s := spokenSeconds("今天天气很好。", ""); s < 1.3 || s > 1.4 { // 6 characters at 4.5/s
		t.Fatalf("chinese %v", s)
	}
	if unspacedLanguage("こんにちは世界") != "ja" || unspacedLanguage("สวัสดีชาวโลก") != "th" || unspacedLanguage("Hello") != "" {
		t.Fatal("scripts")
	}
	s := &Scene{Narration: "Раз два три.", Audio: Asset{Status: StatusDone, Duration: 4}}
	if sourceSeconds(s, "uk") != 4 {
		t.Fatal("measured voice")
	}
	s.Audio.Stale = true
	s.AudioSource, s.VoiceStart, s.VoiceEnd = AudioUpload, 1, 3.5
	if sourceSeconds(s, "uk") != 2.5 {
		t.Fatal("recording slice")
	}
	s.AudioSource = ""
	if v := sourceSeconds(s, "uk"); v < 1.3 || v > 1.31 {
		t.Fatalf("estimate %v", v)
	}
	job := translateJob{to: Language{Code: "en"}}
	src := &Scene{Narration: "Раз два три чотири п'ять.", Audio: Asset{Status: StatusDone, Duration: 2}}
	if tooLong("One two three four five.", src, job) || !tooLong("One two three four five six seven eight nine.", src, job) ||
		tooLong("", src, job) {
		t.Fatal("too long rule")
	}
	if !translated(&Scene{Narration: "Hi"}, &Scene{Narration: "Привіт"}) || translated(&Scene{Title: "A"}, &Scene{Title: "A"}) {
		t.Fatal("translated rule")
	}
}

// TestCoverTextEditAndStage: the finished cover is engine-owned in Edit, the
// cover texts are clipped (an empty stroke and "none" survive), and the cover
// stage puts the text on every new picture.
func TestCoverTextEditAndStage(t *testing.T) {
	before := &Project{ID: "p", Title: "T", Step: 1, Input: DefaultInput(), Montage: DefaultMontage()}
	before.Montage.CoverFinal = Asset{Path: "images/cover-final-1.jpg", Status: StatusDone}
	p := cloneProject(before)
	p.Montage.CoverFinal = Asset{Path: "../evil.jpg", Status: StatusDone}
	p.Montage.CoverText = CoverText{Title: strings.Repeat("т", 200), Subtitle: strings.Repeat("s", 130), Badge: strings.Repeat("b", 50),
		Font: strings.Repeat("f", 60), Stroke: "none"}
	reconcile(before, &p)
	ct := p.Montage.CoverText
	if p.Montage.CoverFinal != before.Montage.CoverFinal || len([]rune(ct.Title)) != 120 || len(ct.Subtitle) != 120 || len(ct.Badge) != 40 ||
		len(ct.Font) != 40 || ct.Stroke != "none" || ct.Accent != "" {
		t.Fatalf("reconcile %+v %+v", p.Montage.CoverFinal, ct)
	}

	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	q, _ := e.Create("Обкладинка")
	must(t, e.Edit(q.ID, func(p *Project) { p.Montage.CoverText = CoverText{Title: "Стежка додому"} }))
	must(t, e.Run(q.ID, StageCover, false, ""))
	q = waitJob(t, e, q.ID)
	mustJobDone(t, q)
	if c, f := q.Montage.Cover, q.Montage.CoverFinal; c.Status != StatusDone || f.Status != StatusDone || f.Path == "" || f.Path == c.Path {
		t.Fatalf("cover %+v final %+v", c, f)
	}
	fileExists(t, e.Abs(q.ID, q.Montage.CoverFinal.Path))
}

// ---- Duplicate ---------------------------------------------------------------------------------

func TestDuplicate(t *testing.T) {
	t.Parallel()
	env, _, _ := translateEnv(t)
	e := env.e
	src := sourceProject(t, e)
	if _, err := e.Duplicate("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	p, err := e.Duplicate(src.ID)
	must(t, err)
	if p.ID == src.ID || p.Title != "Стежка додому (копія)" || p.SourceID != "" || p.Job != nil || p.CreatedAt < src.CreatedAt {
		t.Fatalf("copy %+v", p)
	}
	// Everything is there: same relative paths, same states.
	if len(p.Scenes) != 3 || p.Montage.Output != src.Montage.Output || p.Montage.CoverFinal != src.Montage.CoverFinal ||
		p.Montage.SRTPath != src.Montage.SRTPath || p.Montage.MusicPath != src.Montage.MusicPath || !reflect.DeepEqual(p.Input, src.Input) {
		t.Fatalf("montage/input differ: %+v", p.Montage)
	}
	for i, s := range p.Scenes {
		sv := src.Scenes[i]
		if s.Frame != sv.Frame || s.Video != sv.Video || s.Audio != sv.Audio || s.Board != sv.Board || s.Narration != sv.Narration {
			t.Fatalf("scene %d differs", i)
		}
	}
	for _, rel := range projectFiles(e.Dir(src.ID), &src) {
		fileExists(t, e.Abs(p.ID, rel))
	}
	fileExists(t, e.Abs(p.ID, LastFrameRel("sc-1")))
	fileExists(t, e.Abs(p.ID, p.Montage.Output.Path))
	fileExists(t, e.Abs(p.ID, p.Scenes[0].Audio.Path))
	// Heavy media are hard links (no extra disk space); the last-frame cache,
	// which ffmpeg rewrites in place, is a real copy.
	for _, rel := range []string{p.Montage.Output.Path, p.Scenes[0].Video.Path, p.Scenes[0].Audio.Path, p.Montage.MusicPath} {
		if !same(t, e.Abs(src.ID, rel), e.Abs(p.ID, rel)) {
			t.Errorf("%s was copied instead of linked", rel)
		}
	}
	if same(t, e.Abs(src.ID, LastFrameRel("sc-1")), e.Abs(p.ID, LastFrameRel("sc-1"))) {
		t.Error("the last-frame cache must not be shared")
	}
	if len(e.List()) != 2 {
		t.Fatalf("%d projects", len(e.List()))
	}
	// Independent: editing or deleting the source leaves the copy as it was.
	must(t, e.Edit(src.ID, func(p *Project) { p.Scenes = p.Scenes[:1] }))
	must(t, e.Delete(src.ID))
	q, _ := e.Get(p.ID)
	if len(q.Scenes) != 3 {
		t.Fatal("the copy changed with its source")
	}
	for _, s := range q.Scenes {
		fileExists(t, e.Abs(p.ID, s.Video.Path))
		fileExists(t, e.Abs(p.ID, s.Audio.Path))
	}
	// The copy is saved: a fresh engine loads it.
	e2, err := NewEngine(config.NewStoreWith(env.store.Get()))
	must(t, err)
	if r, ok := e2.Get(p.ID); !ok || r.Title != p.Title || len(r.Scenes) != 3 {
		t.Fatalf("reloaded %+v", r)
	}
}
