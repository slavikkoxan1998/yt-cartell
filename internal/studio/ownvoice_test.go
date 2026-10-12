package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/stt"
)

// ---- synthetic speech ----------------------------------------------------------------------

// fairyTale: eight sentences (2–4 s each at 2.5 words per second).
var fairyTale = []string{
	"Жила-була в лісі маленька лисичка.",
	"Вона мала теплу нору під старим дубом.",
	"Одного разу прийшла зима, і все навколо замело снігом.",
	"Лисичка вийшла з нори.",
	"Вона шукала їжу цілий день, але нічого не знайшла.",
	"Увечері вона повернулася додому втомлена й голодна.",
	"А вдома на неї чекав сюрприз!",
	"Друзі принесли їй кошик з ягодами.",
}

// speechOf builds a transcript: words of wordDur seconds 0.08 s apart,
// sentences pause seconds apart. whisper = words without punctuation (like
// OpenAI's word list; the text keeps it).
func speechOf(sentences []string, wordDur, pause float64, whisper bool) stt.Transcript {
	tr := stt.Transcript{Language: "uk"}
	at := 0.4
	for _, s := range sentences {
		for _, w := range strings.Fields(s) {
			if whisper {
				w = strings.Trim(w, ".,!?;:«»\"—")
			}
			if w == "" {
				continue
			}
			tr.Words = append(tr.Words, stt.Word{Text: w, Start: round3(at), End: round3(at + wordDur)})
			at += wordDur + 0.08
		}
		at += pause - 0.08
	}
	tr.Text = strings.Join(sentences, " ")
	tr.Duration = round3(at + 0.6)
	return tr
}

var veoLimits = chunkLimits{min: 4, target: 6.8, max: 8}

func noSpace(s string) string { return strings.Join(strings.Fields(s), "") }

// checkChunks verifies the invariants every split must keep and returns the
// words each chunk holds.
func checkChunks(t *testing.T, tr stt.Transcript, chunks []speechChunk, lim chunkLimits, strictLen bool) [][]stt.Word {
	t.Helper()
	var groups [][]stt.Word
	at := 0
	for k, c := range chunks {
		if c.Words <= 0 || at+c.Words > len(tr.Words) {
			t.Fatalf("chunk %d holds %d words (at %d of %d)", k, c.Words, at, len(tr.Words))
		}
		ws := tr.Words[at : at+c.Words]
		at += c.Words
		groups = append(groups, ws)
		// Never inside a word: the slice covers its first and last word whole.
		if c.Start > ws[0].Start+1e-9 || c.End < ws[len(ws)-1].End-1e-9 {
			t.Errorf("chunk %d [%.3f, %.3f] cuts its words [%.3f, %.3f]", k, c.Start, c.End, ws[0].Start, ws[len(ws)-1].End)
		}
		if c.Start < 0 || (tr.Duration > 0 && c.End > tr.Duration+1e-9) {
			t.Errorf("chunk %d outside the recording: [%.3f, %.3f] of %.3f", k, c.Start, c.End, tr.Duration)
		}
		if k > 0 && c.Start < chunks[k-1].End-1e-9 {
			t.Errorf("chunk %d overlaps its neighbour: %.3f < %.3f", k, c.Start, chunks[k-1].End)
		}
		if d := c.End - c.Start; strictLen && (d < lim.min-1e-9 || d > lim.max+1e-9) {
			t.Errorf("chunk %d lasts %.2f s, want %.0f–%.0f s: %q", k, d, lim.min, lim.max, c.Text)
		}
	}
	if at != len(tr.Words) {
		t.Fatalf("chunks hold %d of %d words", at, len(tr.Words))
	}
	// Verbatim: together the chunks are the transcript, word for word.
	// (Spacing aside: scripts without spaces join without them.)
	var texts []string
	for _, c := range chunks {
		texts = append(texts, c.Text)
	}
	if got, want := noSpace(strings.Join(texts, "")), noSpace(tr.Text); got != want {
		t.Errorf("chunks are not the transcript verbatim:\n got %q\nwant %q", got, want)
	}
	return groups
}

func TestChunkSpeechSentences(t *testing.T) {
	for _, whisper := range []bool{false, true} {
		tr := speechOf(fairyTale, 0.32, 0.5, whisper)
		chunks := chunkSpeech(tr, veoLimits)
		checkChunks(t, tr, chunks, veoLimits, true)
		var texts []string
		for _, c := range chunks {
			texts = append(texts, c.Text)
		}
		if strings.Join(texts, " ") != strings.Join(fairyTale, " ") {
			t.Errorf("whisper=%v: not verbatim %q", whisper, texts)
		}
		if len(chunks) < 3 || len(chunks) > 5 {
			t.Fatalf("whisper=%v: %d chunks", whisper, len(chunks))
		}
		for k, c := range chunks {
			if !strings.ContainsAny(c.Text[len(c.Text)-1:], ".!?") {
				t.Errorf("whisper=%v: chunk %d does not end a sentence: %q", whisper, k, c.Text)
			}
		}
		// Air around the words: a lead before the first word, a tail after the
		// last one (pauses are 0.5 s, so neither is cut short).
		first, last := chunks[0], chunks[len(chunks)-1]
		if first.Start != round3(tr.Words[0].Start-speechLead) || last.End != round3(tr.Words[len(tr.Words)-1].End+speechTail) {
			t.Errorf("pads: first %.3f (word %.3f), last %.3f (word %.3f)", first.Start, tr.Words[0].Start, last.End, tr.Words[len(tr.Words)-1].End)
		}
		if whisper && chunks[0].Text != fairyTale[0]+" "+fairyTale[1] {
			t.Errorf("punctuation from the text: %q", chunks[0].Text)
		}
	}
}

func TestChunkSpeechLongPhrases(t *testing.T) {
	// 30 words without punctuation (12 s), one 0.7 s breath after word 14:
	// the only acceptable cut inside is at that breath.
	var words []string
	for i := 1; i <= 30; i++ {
		words = append(words, fmt.Sprintf("слово%d", i))
	}
	tr := speechOf([]string{strings.Join(words, " ")}, 0.32, 0.5, false)
	for i := 14; i < len(tr.Words); i++ {
		tr.Words[i].Start += 0.62
		tr.Words[i].End += 0.62
	}
	tr.Duration += 0.62
	chunks := chunkSpeech(tr, veoLimits)
	groups := checkChunks(t, tr, chunks, veoLimits, true)
	if len(groups) != 2 || groups[0][len(groups[0])-1].Text != "слово14" {
		t.Fatalf("cut not at the breath: %+v", chunks)
	}

	// A single 12 s word (a song, a scream…) stays whole, longer than a clip.
	long := stt.Transcript{Text: "Ааааааа! Так.", Duration: 16, Words: []stt.Word{{Text: "Ааааааа!", Start: 1, End: 13}, {Text: "Так.", Start: 13.5, End: 14}}}
	chunks = chunkSpeech(long, veoLimits)
	checkChunks(t, long, chunks, veoLimits, false)
	if len(chunks) != 2 || chunks[0].Words != 1 {
		t.Fatalf("long word %+v", chunks)
	}

	// A recording shorter than the shortest clip is one scene.
	short := speechOf([]string{"Привіт, світе!"}, 0.3, 0.5, false)
	chunks = chunkSpeech(short, veoLimits)
	checkChunks(t, short, chunks, veoLimits, false)
	if len(chunks) != 1 || chunks[0].Start != round3(short.Words[0].Start-speechLead) {
		t.Fatalf("short %+v", chunks)
	}
	if chunkSpeech(stt.Transcript{Text: "…"}, veoLimits) != nil {
		t.Fatal("no words must give no scenes")
	}
}

func TestChunkSpeechOddTranscripts(t *testing.T) {
	// Whisper left out a word the text has, and has one the text lacks: the
	// text still comes out verbatim and cuts stay at sentence ends.
	tr := speechOf(fairyTale, 0.32, 0.5, true)
	var words []stt.Word
	for _, w := range tr.Words {
		if w.Text == "під" { // missing from the word list
			continue
		}
		words = append(words, w)
		if w.Text == "снігом" { // a word that is not in the text
			words = append(words, stt.Word{Text: "еее", Start: w.End, End: w.End + 0.05})
		}
	}
	tr.Words = words
	chunks := chunkSpeech(tr, veoLimits)
	var texts []string
	for _, c := range chunks {
		texts = append(texts, c.Text)
	}
	if strings.Join(texts, " ") != strings.Join(fairyTale, " ") {
		t.Fatalf("not verbatim: %q", texts)
	}

	// Overlapping word times: the cut lands in the middle, scenes never overlap.
	ov := stt.Transcript{Text: "Раз два три чотири. П'ять шість сім вісім.", Duration: 10}
	for i, w := range strings.Fields(ov.Text) {
		ov.Words = append(ov.Words, stt.Word{Text: w, Start: 0.5 + float64(i)*1.1, End: 0.5 + float64(i)*1.1 + 1.2})
	}
	chunks = chunkSpeech(ov, chunkLimits{min: 3, target: 4, max: 5.5})
	if len(chunks) != 2 || chunks[0].End != chunks[1].Start || chunks[0].Text != "Раз два три чотири." {
		t.Fatalf("overlapping words %+v", chunks)
	}

	// Chinese: no spaces, Scribe-style words with their punctuation.
	zh := stt.Transcript{Text: "你好，世界。今天天气很好。我们去公园吧！", Duration: 8}
	for i, w := range []string{"你好，", "世界。", "今天", "天气", "很好。", "我们", "去", "公园吧！"} {
		zh.Words = append(zh.Words, stt.Word{Text: w, Start: 0.2 + float64(i)*0.9, End: 0.2 + float64(i)*0.9 + 0.7})
	}
	lim := chunkLimits{min: 1.5, target: 2.2, max: 3}
	chunks = chunkSpeech(zh, lim)
	checkChunks(t, zh, chunks, lim, false)
	if len(chunks) != 3 || chunks[0].Text != "你好，世界。" || chunks[1].Text != "今天天气很好。" || chunks[2].Text != "我们去公园吧！" {
		t.Fatalf("chinese %+v", chunks)
	}
}

func TestOpeningPunctuation(t *testing.T) {
	for _, tc := range []struct{ text, word, want string }{
		{"Вона сказала: «Я повернуся!»", "Я", "«Я повернуся!»"},
		{"Кінець розмови. — Привіт, — сказав він.", "Привіт", "— Привіт, — сказав він."},
		{`He said: "Go home."`, "Go", `"Go home."`},
		{"Кінець. Початок.", "Початок", "Початок."},
		{"Раз — два.", "два", "— два."},
	} {
		r := []rune(tc.text)
		i := strings.Index(tc.text, tc.word)
		at := len([]rune(tc.text[:i]))
		if got := string(r[openingStart(r, at):]); got != tc.want {
			t.Errorf("%q: %q, want %q", tc.text, got, tc.want)
		}
	}
	// Hyphenated and apostrophe words are found in the text by their letters.
	w := alignWords([]rune("Well-known don't stop."), []stt.Word{{Text: "well", End: 1}, {Text: "known", Start: 1, End: 2}, {Text: "don't", Start: 2, End: 3}, {Text: "stop", Start: 3, End: 4}})
	if w[0].from != 0 || w[1].from != 5 || w[2].to != 16 || w[3].from != 17 {
		t.Fatalf("alignment %+v", w)
	}
}

// ---- end to end --------------------------------------------------------------------------------

// sttMock is an OpenAI-compatible transcription server.
type sttMock struct {
	mu     sync.Mutex
	reply  []byte
	calls  int
	fields []map[string][]string
	sizes  []int
}

func (m *sttMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer stt-test" {
		w.WriteHeader(401)
		return
	}
	switch r.URL.Path {
	case "/v1/models":
		io.WriteString(w, `{"data":[{"id":"whisper-1"}]}`)
	case "/v1/audio/transcriptions":
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		size := 0
		if fh := r.MultipartForm.File["file"]; len(fh) == 1 {
			size = int(fh[0].Size)
		}
		m.mu.Lock()
		m.calls++
		m.fields = append(m.fields, r.MultipartForm.Value)
		m.sizes = append(m.sizes, size)
		reply := m.reply
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write(reply)
	default:
		http.NotFound(w, r)
	}
}

func (m *sttMock) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// whisperJSON is the verbose_json reply for tr (words without punctuation).
func whisperJSON(tr stt.Transcript) []byte {
	words := make([]map[string]any, len(tr.Words))
	for i, w := range tr.Words {
		words[i] = map[string]any{"word": w.Text, "start": w.Start, "end": w.End}
	}
	b, _ := json.Marshal(map[string]any{"task": "transcribe", "language": "ukrainian", "duration": tr.Duration, "text": tr.Text, "words": words})
	return b
}

// voiceEnv is a harness env whose STT points at an OpenAI-compatible mock
// answering with the fairy tale.
func voiceEnv(t *testing.T, mut func(c *config.Config)) (*testEnv, *sttMock, stt.Transcript) {
	t.Helper()
	tr := speechOf(fairyTale, 0.32, 0.5, true)
	m := &sttMock{reply: whisperJSON(tr)}
	srv := httptest.NewServer(m)
	allow(srv)
	t.Cleanup(srv.Close)
	env := newEnv(t, func(c *config.Config) {
		c.STT = config.STT{Provider: "openai", BaseURL: srv.URL + "/v1", APIKey: "stt-test"}
		if mut != nil {
			mut(c)
		}
	})
	return env, m, tr
}

// voiceFile makes an mp3 of seconds of tone (the mock "hears" the fairy tale).
func voiceFile(t *testing.T, name string, seconds float64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	must(t, testFF.Run(ctx, "", "-f", "lavfi", "-i", "sine=frequency=220:sample_rate=44100", "-t", fmt.Sprint(seconds),
		"-c:a", "libmp3lame", "-b:a", "64k", path))
	return path
}

func TestVoiceModeEndToEnd(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, sm, tr := voiceEnv(t, nil)
	e := env.e
	src := voiceFile(t, "Моя озвучка.mp3", 26)

	// Refused files.
	txt := filepath.Join(t.TempDir(), "notes.txt")
	must(t, os.WriteFile(txt, []byte("hi"), 0o644))
	if _, err := e.CreateFromVoice("", txt, ""); err == nil || !strings.Contains(err.Error(), "Це не аудіофайл") {
		t.Fatalf("text file: %v", err)
	}
	fake := filepath.Join(t.TempDir(), "fake.mp3")
	must(t, os.WriteFile(fake, testImage, 0o644))
	if _, err := e.CreateFromVoice("", fake, ""); err == nil || len(e.List()) != 0 {
		t.Fatalf("a picture named .mp3 must be refused without leaving a project: %v, %d projects", err, len(e.List()))
	}

	p, err := e.CreateFromVoice("", src, "")
	must(t, err)
	id := p.ID
	in := p.Input
	if in.Mode != ModeVoice || !in.Voiceover || in.VoiceFileName != "Моя озвучка.mp3" ||
		!strings.HasPrefix(in.VoiceFile, "audio/voiceover-moia_ozvuchka-") || in.TargetSeconds != 26 || in.Text != "" {
		t.Fatalf("input %+v", in)
	}
	fileExists(t, e.Abs(id, in.VoiceFile))
	if ok, msg := e.TestSTT(context.Background()); !ok || !strings.Contains(msg, "модель whisper-1 доступна") || sm.count() != 0 {
		t.Fatalf("stt check: %v %s", ok, msg)
	}

	// ---- plan: transcription first, then scenes timed to the speech
	must(t, e.Run(id, StagePlan, false, ""))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if sm.count() != 1 {
		t.Fatalf("stt calls %d", sm.count())
	}
	sm.mu.Lock()
	f, size := sm.fields[0], sm.sizes[0]
	sm.mu.Unlock()
	if f["model"][0] != "whisper-1" || f["response_format"][0] != "verbose_json" || strings.Join(f["timestamp_granularities[]"], ",") != "word,segment" ||
		size == 0 || size > 26*48000/8+8192 {
		t.Fatalf("upload fields %v, %d bytes (a compact mono mp3 is expected)", f, size)
	}
	want := chunkSpeech(tr, voiceLimits(env.store.Get()))
	if p.Input.Text != tr.Text || len(p.Scenes) != len(want) || len(want) < 3 {
		t.Fatalf("plan: text %q, %d scenes (want %d)", p.Input.Text, len(p.Scenes), len(want))
	}
	if p.Job.Message != fmt.Sprintf("План готовий: %d сцен ≈ 0:%02d", len(want), int(math.Round(tr.Duration))) {
		t.Fatalf("plan message %q", p.Job.Message)
	}
	for i, s := range p.Scenes {
		c := want[i]
		if s.AudioSource != AudioUpload || s.VoiceStart != c.Start || s.VoiceEnd != c.End || s.Narration != c.Text ||
			s.ImagePrompt == "" || s.MotionPrompt == "" || s.Title == "" {
			t.Fatalf("scene %d: %+v, want %+v", i, s, c)
		}
		if i > 0 && s.VoiceStart < p.Scenes[i-1].VoiceEnd {
			t.Fatalf("scenes %d/%d overlap", i-1, i)
		}
	}
	env.text.mu.Lock()
	bp := env.text.biblePrompts[len(env.text.biblePrompts)-1]
	env.text.mu.Unlock()
	mustContain(t, "voice bible", bp, "the transcript of the user's own recording, already split into",
		fmt.Sprintf("EXACTLY %d scenes, one per numbered part", len(want)), "[1] "+want[0].Text, "Audience language: Ukrainian.",
		`"index", "title", "speaker"`)
	mustLack(t, "voice bible", bp, "TRANSLATED", "sourceLanguage", `"narration": the voice-over line`)
	if tr2, ok := e.VoiceTranscript(id); !ok || len(tr2.Words) != len(tr.Words) || tr2.Language != "uk" {
		t.Fatalf("sidecar transcript %v %+v", ok, tr2)
	}

	// ---- frames, then voices cut from the recording (no TTS)
	must(t, e.Run(id, StageFrames, false, ""))
	mustJobDone(t, waitJob(t, e, id))
	must(t, e.Run(id, StageVoices, false, ""))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	for i, s := range p.Scenes {
		a := s.Audio
		if a.Status != StatusDone || !strings.HasPrefix(a.Path, "audio/voice-") || math.Abs(a.Duration-(s.VoiceEnd-s.VoiceStart)) > 0.1 {
			t.Fatalf("scene %d voice %+v for [%.3f, %.3f]", i, a, s.VoiceStart, s.VoiceEnd)
		}
		if eleven.voiceFor(s.Narration) != "" {
			t.Fatalf("scene %d was sent to TTS", i)
		}
	}

	// ---- edits: the text is only a subtitle, the range is the voice
	must(t, e.Edit(id, func(p *Project) {
		p.Scenes[0].Narration += " (виправлено)"
		p.Scenes[1].VoiceEnd -= 0.2
		p.Scenes[2].VoiceStart, p.Scenes[2].VoiceEnd = 5, 3 // broken range: kept as it was
		p.Input.VoiceFile = "../elsewhere.mp3"              // engine-owned
		p.Scenes = append(p.Scenes, Scene{Title: "Нова", Narration: "Нова сцена.", AudioSource: AudioUpload, VoiceStart: 1, VoiceEnd: 3})
	}))
	q, _ := e.Get(id)
	if q.Scenes[0].Audio.Stale || !q.Scenes[1].Audio.Stale || q.Scenes[2].VoiceStart != p.Scenes[2].VoiceStart || q.Scenes[2].Audio.Stale {
		t.Fatalf("stale rules: %+v / %+v / %+v", q.Scenes[0].Audio, q.Scenes[1].Audio, q.Scenes[2])
	}
	if q.Input.VoiceFile != p.Input.VoiceFile {
		t.Fatalf("recording path edited: %q", q.Input.VoiceFile)
	}
	if n := q.Scenes[len(q.Scenes)-1]; n.AudioSource != "" || n.VoiceStart != 0 || n.VoiceEnd != 0 {
		t.Fatalf("an inserted scene must be voiced by TTS: %+v", n)
	}
	must(t, e.Regen(id, RegenVoice, q.Scenes[1].ID, ""))
	q = waitFor(t, e, id, "re-cut voice", func(p Project) bool { return p.Scenes[1].Audio.Status == StatusDone && !p.Scenes[1].Audio.Stale })
	if s := q.Scenes[1]; math.Abs(s.Audio.Duration-(s.VoiceEnd-s.VoiceStart)) > 0.1 {
		t.Fatalf("re-cut %+v", s.Audio)
	}
	// The inserted scene goes to TTS in the recording's language.
	must(t, e.Regen(id, RegenVoice, q.Scenes[len(q.Scenes)-1].ID, ""))
	waitFor(t, e, id, "tts voice", func(p Project) bool { return p.Scenes[len(p.Scenes)-1].Audio.Status == StatusDone })
	if eleven.voiceFor("Нова сцена.") == "" {
		t.Fatal("the inserted scene was not voiced by TTS")
	}

	// ---- a new plan reuses the transcript; Transcribe runs it again
	must(t, e.Run(id, StagePlan, false, ""))
	mustJobDone(t, waitJob(t, e, id))
	if sm.count() != 1 {
		t.Fatalf("re-plan transcribed again (%d calls)", sm.count())
	}
	must(t, e.Transcribe(id))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if p.Job.Kind != JobTranscribe || !strings.HasPrefix(p.Job.Message, "Мову розпізнано · "+wordCount(len(tr.Words))+" · 0:") || sm.count() != 2 {
		t.Fatalf("transcribe job %+v, %d calls", p.Job, sm.count())
	}
	if jobLabels[JobTranscribe] != "Розпізнавання мови" {
		t.Fatal("label")
	}
	must(t, e.Run(id, StageVoices, false, "")) // the new plan's scenes are cut again
	mustJobDone(t, waitJob(t, e, id))

	// ---- removing the recording
	must(t, e.SetVoiceFile(id, ""))
	q, _ = e.Get(id)
	if q.Input.VoiceFile != "" || q.Scenes[0].Audio.Status != StatusDone || !q.Scenes[0].Audio.Stale {
		t.Fatalf("after removal: %+v %+v", q.Input, q.Scenes[0].Audio)
	}
	if _, err := os.Stat(e.Abs(id, p.Input.VoiceFile)); !os.IsNotExist(err) {
		t.Fatal("the copy of the recording stays")
	}
	if _, ok := e.VoiceTranscript(id); ok {
		t.Fatal("transcript of a removed recording")
	}
	if err := e.Transcribe(id); err == nil || err.Error() != errNoVoice.Error() {
		t.Fatalf("transcribe without a recording: %v", err)
	}
}

// TestVoiceAutopilot: the quick action runs everything unattended — the
// autopilot's plan stage transcribes, the voices are cut, the film is made.
func TestVoiceAutopilot(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, sm, tr := voiceEnv(t, func(c *config.Config) {
		c.Autopilot = config.Autopilot{MakeCover: false, Retries: 0, ContinueOnError: false}
	})
	e := env.e
	id, err := e.StartVoiceAutopilot("Моя казка", voiceFile(t, "казка.wav", 26), "")
	must(t, err)
	p := waitJob(t, e, id)
	mustJobDone(t, p)
	if p.Job.Kind != JobAutopilot || !strings.HasPrefix(p.Job.Message, "Автопілот завершено · Фінальне відео готове") || sm.count() != 1 {
		t.Fatalf("autopilot %+v, stt calls %d", p.Job, sm.count())
	}
	if p.Title != "Моя казка" || p.Input.Text != tr.Text || len(p.Scenes) < 3 {
		t.Fatalf("project %q %q %d scenes", p.Title, p.Input.Text, len(p.Scenes))
	}
	spoken := 0.0
	for i, s := range p.Scenes {
		if s.AudioSource != AudioUpload || s.Audio.Status != StatusDone || s.Video.Status != StatusDone {
			t.Fatalf("scene %d: %s %+v %+v", i, s.AudioSource, s.Audio, s.Video)
		}
		spoken += s.Audio.Duration
	}
	out := p.Montage.Output
	if out.Status != StatusDone || out.Duration < spoken {
		t.Fatalf("final video %+v shorter than the voice-over (%.1f s)", out, spoken)
	}
	fileExists(t, e.Abs(id, out.Path))
	// Once transcribed, the regular autopilot stage accepts the project too.
	must(t, e.Run(id, StageAutopilot, false, ""))
	mustJobDone(t, waitJob(t, e, id))
	if sm.count() != 1 {
		t.Fatalf("transcribed again: %d", sm.count())
	}
}

func TestVoiceNormalizeAndBudget(t *testing.T) {
	raw := `{"id":"v","input":{"text":"x","mode":"voice","voiceover":false,"voiceFile":"audio/v.mp3"},
		"scenes":[{"id":"a","audioSource":"upload","voiceStart":1,"voiceEnd":3},
		          {"id":"b","audioSource":"upload","voiceStart":3,"voiceEnd":2},
		          {"id":"c","audioSource":"magic","voiceStart":-1,"voiceEnd":2}]}`
	p, err := decodeProject([]byte(raw))
	must(t, err)
	normalize(p)
	if !p.Input.Voiceover || p.Scenes[0].AudioSource != AudioUpload || p.Scenes[1].AudioSource != "" || p.Scenes[2].AudioSource != "" || p.Scenes[2].VoiceStart != 0 {
		t.Fatalf("normalized %+v %+v", p.Input, p.Scenes)
	}
	omni := config.Default()
	omni.Video = config.Video{Provider: "glabs", GlabsModel: "omni_flash", Length: 10}
	if l := voiceLimits(omni); l.min != 4 || l.max != 10 || l.target != 8.5 {
		t.Fatalf("omni limits %+v", l)
	}
	veo := config.Default()
	veo.Video.Length = 4
	if l := voiceLimits(veo); l.max != 8 || l.target != 4 || maxVoiceSeconds(veo) != 1200 {
		t.Fatalf("veo limits %+v", l)
	}
	if voiceClock(125.4) != "2:05" || ukPlural(1, "a", "b", "c") != "a" || ukPlural(3, "a", "b", "c") != "b" || ukPlural(12, "a", "b", "c") != "c" || ukPlural(21, "a", "b", "c") != "a" {
		t.Fatal("helpers")
	}
}

// TestVoiceClipLengths: a scene cut to a slice of the recording asks the
// video model for the shortest clip covering it, and its clip is trimmed to
// end with the slice (plus the montage's breath).
func TestVoiceClipLengths(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, gl := newSBEnv(t, func(c *config.Config) { c.Video.Ultra = true }) // Omni Flash with Ultra: 4/6/8/10 s
	e := env.e
	long := filepath.Join(t.TempDir(), "long.mp4")
	must(t, testFF.Run(t.Context(), "", "-f", "lavfi", "-i", "color=c=blue:s=160x90:r=24:d=10",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:d=10",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", long))
	clip, err := os.ReadFile(long)
	must(t, err)
	gl.mu.Lock()
	gl.clips = [][]byte{clip}
	gl.mu.Unlock()

	p := castProject(t, e, func(in *Input) {}, []int{0}, []int{1}, []int{0, 1})
	img := filepath.Join(t.TempDir(), "f.jpg")
	must(t, os.WriteFile(img, testImage, 0o644))
	for _, s := range p.Scenes {
		must(t, e.ImportImage(p.ID, RegenFrame, s.ID, img))
	}
	slices := [][2]float64{{0, 3.2}, {3.3, 9}, {9.1, 18.6}} // 3.2 s → 4, 5.7 s → 8, 9.5 s → 10
	must(t, e.Edit(p.ID, func(p *Project) {
		for i := range p.Scenes {
			p.Scenes[i].AudioSource, p.Scenes[i].VoiceStart, p.Scenes[i].VoiceEnd = AudioUpload, slices[i][0], slices[i][1]
		}
	}))
	must(t, e.Run(p.ID, StageVideos, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	want := []int{4, 8, 10}
	for i, s := range p.Scenes {
		var got []int
		for _, r := range gl.requests("video") {
			if strings.Contains(r.prompt, fmt.Sprintf("Motion S%d:", i+1)) {
				got = append(got, r.length)
			}
		}
		if len(got) != 1 || got[0] != want[i] {
			t.Fatalf("scene %d asked for %v s, want %d", i+1, got, want[i])
		}
		// The clip ends with the slice and the montage's breath around the
		// line (0.25 s before + 0.35 s after) — unless it is shorter than that
		// (scene 3: 9.5 + 0.6 s > 10 s), then it is kept whole.
		const air = 0.6
		slice := s.VoiceEnd - s.VoiceStart
		trim := math.Max(0, math.Round((s.Video.Duration-slice-air)*100)/100)
		used := s.Video.Duration - s.TrimOut
		fits := s.Video.Duration >= slice+air
		if s.Video.Status != StatusDone || s.Video.Duration < 9.5 || math.Abs(s.TrimOut-trim) > 1e-9 ||
			fits && math.Abs(used-(slice+air)) > 0.011 || !fits && s.TrimOut != 0 {
			t.Fatalf("scene %d clip %+v trim %.2f for a %.1f s slice", i+1, s.Video, s.TrimOut, slice)
		}
	}
}
