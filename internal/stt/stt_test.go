package stt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
)

// guard lets requests reach loopback mock servers only: a mistake in a test
// can never call the real (paid) services.
type guard struct{ base http.RoundTripper }

func (g guard) RoundTrip(r *http.Request) (*http.Response, error) {
	if ip := net.ParseIP(r.URL.Hostname()); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("test guard: blocked request to %s", r.URL.Host)
	}
	return g.base.RoundTrip(r)
}

func TestMain(m *testing.M) {
	http.DefaultTransport = guard{base: http.DefaultTransport}
	elevenBase = "http://127.0.0.1:9" // never the real API, even if a test forgets to set it
	retryBase = 5 * time.Millisecond
	os.Exit(m.Run())
}

// captured is one request as the mock saw it.
type captured struct {
	method, path string
	header       http.Header
	fields       map[string][]string
	fileName     string
	fileType     string
	file         []byte
	length       int64
}

type mock struct {
	mu    sync.Mutex
	reqs  []captured
	reply func(n int, w http.ResponseWriter, r *http.Request) // n = 1-based request number
}

func (m *mock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := captured{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), length: r.ContentLength}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, "bad multipart: "+err.Error(), 400)
			return
		}
		c.fields = r.MultipartForm.Value
		if fh := r.MultipartForm.File["file"]; len(fh) == 1 {
			c.fileName, c.fileType = fh[0].Filename, fh[0].Header.Get("Content-Type")
			f, _ := fh[0].Open()
			c.file, _ = io.ReadAll(f)
			f.Close()
		}
	}
	m.mu.Lock()
	m.reqs = append(m.reqs, c)
	n := len(m.reqs)
	m.mu.Unlock()
	m.reply(n, w, r)
}

func (m *mock) all() []captured {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]captured(nil), m.reqs...)
}

func serve(t *testing.T, reply func(n int, w http.ResponseWriter, r *http.Request)) (*mock, *httptest.Server) {
	t.Helper()
	m := &mock{reply: reply}
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return m, srv
}

func jsonReply(body string) func(int, http.ResponseWriter, *http.Request) {
	return func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}
}

func client(c config.Config) *Client {
	c.Normalize()
	return New(func() config.Config { return c })
}

func audioFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ElevenLabs Scribe reply in the documented shape: words with punctuation,
// spacing entries and an audio event.
const scribeReply = `{
 "language_code": "ukr", "language_probability": 0.98,
 "text": "Жила-була лисиця. Вона мала хатку!",
 "audio_duration_secs": 4.2,
 "words": [
  {"text": "Жила-була", "start": 0.12, "end": 0.61, "type": "word", "speaker_id": "speaker_0", "logprob": -0.1},
  {"text": " ", "start": 0.61, "end": 0.66, "type": "spacing"},
  {"text": "лисиця.", "start": 0.66, "end": 1.2, "type": "word"},
  {"text": " ", "start": 1.2, "end": 1.9, "type": "spacing"},
  {"text": "(sigh)", "start": 1.3, "end": 1.6, "type": "audio_event"},
  {"text": "Вона", "start": 1.9, "end": 2.2, "type": "word"},
  {"text": " ", "start": 2.2, "end": 2.25, "type": "spacing"},
  {"text": "мала", "type": "word"},
  {"text": " ", "start": 2.5, "end": 2.55, "type": "spacing"},
  {"text": "хатку!", "start": 2.55, "end": 3.1, "type": "word"}
 ]}`

func TestElevenLabsRequestAndReply(t *testing.T) {
	m, srv := serve(t, jsonReply(scribeReply))
	old := elevenBase
	elevenBase = srv.URL
	defer func() { elevenBase = old }()

	c := config.Default()
	c.STT = config.STT{Provider: "elevenlabs", Model: "scribe_v1"} // the removed model is upgraded
	c.TTS = config.TTS{Provider: "elevenlabs", APIKey: "el-key"}   // empty STT key → the TTS key
	audio := []byte("ID3 fake mp3 bytes")
	tr, err := client(c).Transcribe(context.Background(), audioFile(t, "Моя озвучка.MP3", audio), "uk")
	if err != nil {
		t.Fatal(err)
	}
	reqs := m.all()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	r := reqs[0]
	if r.method != http.MethodPost || r.path != "/v1/speech-to-text" || r.header.Get("xi-api-key") != "el-key" || r.header.Get("Authorization") != "" {
		t.Fatalf("request %s %s headers %v", r.method, r.path, r.header)
	}
	want := map[string]string{"model_id": "scribe_v2", "timestamps_granularity": "word", "tag_audio_events": "false", "language_code": "uk"}
	for k, v := range want {
		if got := r.fields[k]; len(got) != 1 || got[0] != v {
			t.Errorf("field %s = %v, want %q", k, got, v)
		}
	}
	if len(r.fields) != len(want) {
		t.Errorf("unexpected fields %v", r.fields)
	}
	if string(r.file) != string(audio) || r.fileName != "audio.mp3" || r.fileType != "audio/mpeg" || r.length <= int64(len(audio)) {
		t.Fatalf("file part %q %q %q length %d", r.file, r.fileName, r.fileType, r.length)
	}

	if tr.Text != "Жила-була лисиця. Вона мала хатку!" || tr.Language != "uk" || tr.Duration != 4.2 {
		t.Fatalf("transcript %q %q %v", tr.Text, tr.Language, tr.Duration)
	}
	got := make([]string, len(tr.Words))
	for i, w := range tr.Words {
		got[i] = fmt.Sprintf("%s@%.2f-%.2f", w.Text, w.Start, w.End)
	}
	// Spacing and audio events are dropped; a word without times gets the previous end.
	if strings.Join(got, " ") != "Жила-була@0.12-0.61 лисиця.@0.66-1.20 Вона@1.90-2.20 мала@2.20-2.20 хатку!@2.55-3.10" {
		t.Fatalf("words %v", got)
	}
	// Scribe has no segments: sentences are built from the words.
	if len(tr.Segments) != 2 || tr.Segments[0].Text != "Жила-була лисиця." || tr.Segments[1].Text != "Вона мала хатку!" ||
		tr.Segments[0].Start != 0.12 || tr.Segments[1].End != 3.1 {
		t.Fatalf("segments %+v", tr.Segments)
	}
}

func TestElevenLabsKeyNeverFromOtherTTS(t *testing.T) {
	m, srv := serve(t, jsonReply(scribeReply))
	old := elevenBase
	elevenBase = srv.URL
	defer func() { elevenBase = old }()
	c := config.Default()
	c.STT = config.STT{Provider: "elevenlabs"}
	c.TTS = config.TTS{Provider: "openai", APIKey: "sk-openai-tts"} // not an ElevenLabs key
	_, err := client(c).Transcribe(context.Background(), audioFile(t, "a.wav", []byte("RIFF")), "")
	if err == nil || !strings.Contains(err.Error(), "Не задано ключ ElevenLabs") || len(m.all()) != 0 {
		t.Fatalf("err %v, %d requests", err, len(m.all()))
	}
	if ok, msg := client(c).Test(context.Background()); ok || !strings.Contains(msg, "Не задано ключ") {
		t.Fatalf("test: %v %s", ok, msg)
	}
}

// Whisper verbose_json: words without punctuation, segments with it.
const whisperReply = `{
 "task": "transcribe", "language": "english", "duration": 5.5,
 "text": "Hello, world. How are you?",
 "words": [
  {"word": "Hello", "start": 0.0, "end": 0.4},
  {"word": "world", "start": 0.5, "end": 0.9},
  {"word": "How", "start": 1.6, "end": 1.8},
  {"word": "are", "start": 1.8, "end": 1.95},
  {"word": "you", "start": 1.95, "end": 2.3}
 ],
 "segments": [
  {"id": 0, "seek": 0, "start": 0.0, "end": 0.9, "text": " Hello, world.", "tokens": [1, 2], "avg_logprob": -0.2},
  {"id": 1, "seek": 0, "start": 1.6, "end": 2.3, "text": " How are you?", "tokens": [3]}
 ],
 "usage": {"type": "duration", "seconds": 6}
}`

func TestOpenAIRequestAndReply(t *testing.T) {
	m, srv := serve(t, jsonReply(whisperReply))
	c := config.Default()
	c.Text = config.Text{Provider: "openai", BaseURL: srv.URL + "/v1/", APIKey: "txt-key", Model: "gpt"}
	c.STT = config.STT{Provider: "openai"} // URL, key and model fall back
	cl := client(c)
	tr, err := cl.Transcribe(context.Background(), audioFile(t, "voice.m4a", []byte("m4a-bytes")), "en")
	if err != nil {
		t.Fatal(err)
	}
	r := m.all()[0]
	if r.path != "/v1/audio/transcriptions" || r.header.Get("Authorization") != "Bearer txt-key" || r.header.Get("xi-api-key") != "" {
		t.Fatalf("request %s %v", r.path, r.header)
	}
	if f := r.fields; strings.Join(f["model"], ",") != "whisper-1" || strings.Join(f["response_format"], ",") != "verbose_json" ||
		strings.Join(f["timestamp_granularities[]"], ",") != "word,segment" || strings.Join(f["language"], ",") != "en" || len(f) != 4 {
		t.Fatalf("fields %v", f)
	}
	if r.fileName != "audio.m4a" || string(r.file) != "m4a-bytes" {
		t.Fatalf("file %q %q", r.fileName, r.file)
	}
	if tr.Language != "en" || tr.Duration != 5.5 || tr.Text != "Hello, world. How are you?" || len(tr.Words) != 5 || tr.Words[2].Text != "How" ||
		len(tr.Segments) != 2 || tr.Segments[0].Text != "Hello, world." {
		t.Fatalf("transcript %+v", tr)
	}

	// A catalogue code without an ISO 639-1 form is not sent (the API would reject it).
	if _, err := cl.Transcribe(context.Background(), audioFile(t, "v.mp3", []byte("x")), "fil"); err != nil {
		t.Fatal(err)
	}
	if f := m.all()[1].fields; f["language"] != nil {
		t.Fatalf("language sent: %v", f)
	}

	// Explicit settings win over the Text endpoint.
	c.STT = config.STT{Provider: "openai", BaseURL: srv.URL + "/alt", APIKey: "stt-key", Model: "whisper-large-v3"}
	if _, err := client(c).Transcribe(context.Background(), audioFile(t, "v.wav", []byte("x")), ""); err != nil {
		t.Fatal(err)
	}
	r = m.all()[2]
	if r.path != "/alt/audio/transcriptions" || r.header.Get("Authorization") != "Bearer stt-key" || r.fields["model"][0] != "whisper-large-v3" {
		t.Fatalf("explicit settings %s %v %v", r.path, r.header, r.fields)
	}
}

func TestOpenAICompatibleShapes(t *testing.T) {
	// Words nested in segments (faster-whisper servers).
	_, srv := serve(t, jsonReply(`{"language":"uk","text":"Раз два.","segments":[{"start":1,"end":2,"text":"Раз два.",
		"words":[{"word":" Раз","start":1,"end":1.4},{"word":" два.","start":1.5,"end":2}]}]}`))
	c := config.Default()
	c.STT = config.STT{Provider: "openai", BaseURL: srv.URL, APIKey: "k"}
	tr, err := client(c).Transcribe(context.Background(), audioFile(t, "a.mp3", []byte("x")), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Words) != 2 || tr.Words[1].Text != "два." || tr.Duration != 2 || tr.Language != "uk" {
		t.Fatalf("nested %+v", tr)
	}
	// Segments only: words are spread over each segment by length.
	_, srv2 := serve(t, jsonReply(`{"text":"","segments":[{"start":0,"end":3,"text":"aa bbbb"},{"start":4,"end":5,"text":"c"}]}`))
	c.STT.BaseURL = srv2.URL
	tr, err = client(c).Transcribe(context.Background(), audioFile(t, "a.mp3", []byte("x")), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Words) != 3 || tr.Words[0].End != 1 || tr.Words[1].Start != 1 || tr.Words[1].End != 3 || tr.Words[2].Start != 4 ||
		tr.Text != "aa bbbb c" || tr.Duration != 5 {
		t.Fatalf("segments only %+v", tr)
	}
	// Not JSON: a clear message.
	_, srv3 := serve(t, func(_ int, w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "Hello world") })
	c.STT.BaseURL = srv3.URL
	if _, err := client(c).Transcribe(context.Background(), audioFile(t, "a.mp3", []byte("x")), ""); err == nil || !strings.Contains(err.Error(), "verbose_json") {
		t.Fatalf("text reply: %v", err)
	}
}

func TestErrorsAndRetries(t *testing.T) {
	status := func(code int, body string) func(int, http.ResponseWriter, *http.Request) {
		return func(_ int, w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			io.WriteString(w, body)
		}
	}
	cases := []struct {
		provider string
		code     int
		body     string
		want     string
		attempts int
	}{
		{"elevenlabs", 401, `{"detail":{"status":"invalid_api_key","message":"Invalid API key"}}`, "Ключ не підходить — ElevenLabs відхилив його (401)", 1},
		{"elevenlabs", 401, `{"detail":{"status":"missing_permissions","message":"missing speech_to_text"}}`, "не має дозволу на розпізнавання мови", 1},
		{"elevenlabs", 422, `{"detail":[{"loc":["body","file"],"msg":"Audio file is corrupted","type":"value_error"}]}`, "(422): Audio file is corrupted", 1},
		{"elevenlabs", 400, `{"detail":{"message":"Audio duration exceeds the maximum"}}`, "Запис задовгий", 1},
		{"elevenlabs", 429, `{"detail":{"status":"quota_exceeded"}}`, "закінчились кредити", 1},
		{"elevenlabs", 503, `oops`, "тимчасово недоступний (503)", 3},
		{"openai", 413, `{"error":{"message":"Maximum content size limit exceeded"}}`, "завеликий", 1},
		{"openai", 404, `404 page not found`, "не вміє розпізнавати мову (немає /audio/transcriptions)", 1},
		{"openai", 429, `{"error":{"message":"Rate limit"}}`, "перевантажений (429)", 3},
		{"openai", 401, `{"error":{"message":"Incorrect API key provided"}}`, "Ключ не підходить", 1},
	}
	for _, tc := range cases {
		m, srv := serve(t, status(tc.code, tc.body))
		c := config.Default()
		c.STT = config.STT{Provider: tc.provider, APIKey: "k", BaseURL: srv.URL}
		old := elevenBase
		elevenBase = srv.URL
		_, err := client(c).Transcribe(context.Background(), audioFile(t, "a.mp3", []byte("x")), "")
		elevenBase = old
		if err == nil || !strings.Contains(err.Error(), tc.want) || len(m.all()) != tc.attempts {
			t.Errorf("%s %d: %v after %d attempts (want %q, %d)", tc.provider, tc.code, err, len(m.all()), tc.want, tc.attempts)
		}
	}

	// A 5xx then success: retried transparently, the file is sent again in full.
	m, srv := serve(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			w.WriteHeader(502)
			return
		}
		jsonReply(whisperReply)(n, w, r)
	})
	c := config.Default()
	c.STT = config.STT{Provider: "openai", APIKey: "k", BaseURL: srv.URL}
	if _, err := client(c).Transcribe(context.Background(), audioFile(t, "a.mp3", []byte("abc")), ""); err != nil || len(m.all()) != 2 || string(m.all()[1].file) != "abc" {
		t.Fatalf("retry: %v, %d requests", err, len(m.all()))
	}

	// No server: a network message naming the host.
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	c.STT.BaseURL = url
	if _, err := client(c).Transcribe(context.Background(), audioFile(t, "a.mp3", []byte("x")), ""); err == nil || !strings.Contains(err.Error(), "Немає з'єднання з 127.0.0.1") {
		t.Fatalf("network: %v", err)
	}

	// Over the OpenAI upload limit: refused before uploading anything.
	big := filepath.Join(t.TempDir(), "big.wav")
	f, _ := os.Create(big)
	f.Truncate(OpenAILimit + 1)
	f.Close()
	c.STT.BaseURL = srv.URL
	before := len(m.all())
	if _, err := client(c).Transcribe(context.Background(), big, ""); err == nil || !strings.Contains(err.Error(), "до 25 МБ") || len(m.all()) != before {
		t.Fatalf("too big: %v", err)
	}
	// Missing / empty files.
	if _, err := client(c).Transcribe(context.Background(), filepath.Join(t.TempDir(), "none.mp3"), ""); err == nil || !strings.Contains(err.Error(), "не знайдено") {
		t.Fatalf("missing: %v", err)
	}
	if _, err := client(c).Transcribe(context.Background(), audioFile(t, "e.mp3", nil), ""); err == nil || !strings.Contains(err.Error(), "порожній") {
		t.Fatalf("empty: %v", err)
	}
}

func TestCancel(t *testing.T) {
	var arrived atomic.Bool
	release := make(chan struct{})
	_, srv := serve(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		arrived.Store(true)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	defer close(release)
	c := config.Default()
	c.STT = config.STT{Provider: "openai", APIKey: "k", BaseURL: srv.URL}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for !arrived.Load() {
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	t0 := time.Now()
	_, err := client(c).Transcribe(ctx, audioFile(t, "a.mp3", []byte("x")), "")
	if !errors.Is(err, context.Canceled) || time.Since(t0) > 5*time.Second {
		t.Fatalf("cancel: %v after %s", err, time.Since(t0))
	}
}

func TestServiceCheck(t *testing.T) {
	m, srv := serve(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/user" && r.Header.Get("xi-api-key") == "good":
			io.WriteString(w, `{"user_id":"u1","subscription":{"tier":"creator","character_count":5}}`)
		case r.URL.Path == "/v1/user" && r.Header.Get("xi-api-key") == "restricted":
			w.WriteHeader(401)
			io.WriteString(w, `{"detail":{"status":"missing_permissions","message":"The API key you used is missing the permission user_read"}}`)
		case r.URL.Path == "/v1/user":
			w.WriteHeader(401)
			io.WriteString(w, `{"detail":{"status":"invalid_api_key"}}`)
		case r.URL.Path == "/v1/models" && r.Header.Get("Authorization") == "Bearer good":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "gpt-x"}, map[string]any{"id": "whisper-1"}}})
		case r.URL.Path == "/v1/models" && r.Header.Get("Authorization") == "Bearer textonly":
			io.WriteString(w, `{"data":[{"id":"gemini-3.8-flash"},{"id":"claude"}]}`)
		case r.URL.Path == "/v1/models":
			w.WriteHeader(401)
		default:
			http.NotFound(w, r)
		}
	})
	old := elevenBase
	elevenBase = srv.URL
	defer func() { elevenBase = old }()
	check := func(provider, key, base string) (bool, string) {
		c := config.Default()
		c.STT = config.STT{Provider: provider, APIKey: key, BaseURL: base}
		return client(c).Test(context.Background())
	}
	if ok, msg := check("elevenlabs", "good", ""); !ok || msg != "ElevenLabs Scribe (scribe_v2): ключ працює · тариф creator" {
		t.Errorf("eleven good: %v %s", ok, msg)
	}
	if ok, msg := check("elevenlabs", "restricted", ""); !ok || !strings.Contains(msg, "ключ дійсний") {
		t.Errorf("eleven restricted: %v %s", ok, msg)
	}
	if ok, msg := check("elevenlabs", "bad", ""); ok || !strings.Contains(msg, "Ключ не підходить") {
		t.Errorf("eleven bad: %v %s", ok, msg)
	}
	if ok, msg := check("openai", "good", srv.URL+"/v1"); !ok || !strings.Contains(msg, "модель whisper-1 доступна") {
		t.Errorf("openai good: %v %s", ok, msg)
	}
	if ok, msg := check("openai", "textonly", srv.URL+"/v1"); ok || !strings.Contains(msg, "моделі «whisper-1» немає (є: gemini-3.8-flash, claude)") {
		t.Errorf("openai text-only: %v %s", ok, msg)
	}
	if ok, msg := check("openai", "bad", srv.URL+"/v1"); ok || !strings.Contains(msg, "Ключ не підходить") {
		t.Errorf("openai bad: %v %s", ok, msg)
	}
	if ok, msg := check("openai", "good", srv.URL+"/nothing"); ok || !strings.Contains(msg, "не відповідає на /models") {
		t.Errorf("openai 404: %v %s", ok, msg)
	}
	for _, r := range m.all() {
		if r.method != http.MethodGet || r.fields != nil {
			t.Errorf("a check must not transcribe: %s %s", r.method, r.path)
		}
	}
	if DefaultModel("openai") != "whisper-1" || DefaultModel("") != "scribe_v2" || elevenModel("scribe_v1_experimental") != "scribe_v2" || elevenModel("scribe_v3") != "scribe_v3" {
		t.Error("default models")
	}
}

func TestJoinAndSegments(t *testing.T) {
	if got := joinTexts([]string{"你好，", "世界。", "Hi", "there"}); got != "你好，世界。 Hi there" {
		t.Fatalf("join %q", got)
	}
	words := []Word{{"One", 0, 0.3}, {"two", 0.3, 0.6}, {"three", 2.0, 2.4}, {"four.", 2.4, 2.8}, {"Five", 3, 3.2}}
	segs := segmentsFromWords(words)
	if len(segs) != 3 || segs[0].Text != "One two" || segs[1].Text != "three four." || segs[2].Text != "Five" {
		t.Fatalf("segments %+v", segs)
	}
}

// TestPlainJSONModelsRefused: the gpt-4o / gpt-transcribe models return no
// word timings (plain json only), so an own voice-over cannot be cut with
// them — the upload and the service check say so instead of a 400.
func TestPlainJSONModelsRefused(t *testing.T) {
	m, srv := serve(t, jsonReply(whisperReply))
	for _, model := range []string{"gpt-4o-transcribe", "gpt-4o-mini-transcribe-2025-12-15", "gpt-transcribe", "GPT-Live-Transcribe"} {
		c := config.Default()
		c.STT = config.STT{Provider: "openai", BaseURL: srv.URL, APIKey: "k", Model: model}
		_, err := client(c).Transcribe(context.Background(), audioFile(t, "a.mp3", []byte("x")), "uk")
		if err == nil || !strings.Contains(err.Error(), "таймкоди") {
			t.Fatalf("%s: err = %v", model, err)
		}
		if ok, msg := client(c).Test(context.Background()); ok || !strings.Contains(msg, "whisper-1") {
			t.Fatalf("%s: check %v %q", model, ok, msg)
		}
	}
	if n := len(m.all()); n != 0 {
		t.Fatalf("%d requests were sent", n)
	}
	if plainJSONOnly("whisper-1") || plainJSONOnly("whisper-large-v3-turbo") {
		t.Fatal("Whisper models have word timings")
	}
}

// TestElevenLabsKeyFromStoredVoiceOver: the ElevenLabs key stored for the
// voice-over (per provider) is used even while another TTS provider is active.
func TestElevenLabsKeyFromStoredVoiceOver(t *testing.T) {
	m, srv := serve(t, jsonReply(scribeReply))
	old := elevenBase
	elevenBase = srv.URL
	defer func() { elevenBase = old }()
	c := config.Default()
	c.STT = config.STT{Provider: "elevenlabs"}
	c.TTS = config.TTS{Provider: "openai", APIKey: "sk-openai-tts",
		Providers: map[string]config.TTSProvider{"elevenlabs": {APIKey: "sk_eleven_stored", Keys: []string{"sk_eleven_2"}}}}
	c.Normalize()
	if _, err := client(c).Transcribe(context.Background(), audioFile(t, "a.wav", []byte("RIFF")), ""); err != nil {
		t.Fatal(err)
	}
	if got := m.all()[0].header.Get("xi-api-key"); got != "sk_eleven_stored" {
		t.Fatalf("key %q", got)
	}
}

// TestAssemblyAI: upload → job → polling → word timings in seconds; the
// language picks Universal-2 for Ukrainian; errors are explained.
func TestAssemblyAI(t *testing.T) {
	old := assemblyPoll
	assemblyPoll = 5 * time.Millisecond
	defer func() { assemblyPoll = old }()
	var mu sync.Mutex
	var uploaded []byte
	var job map[string]any
	var auth []string
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		auth = append(auth, r.Header.Get("authorization"))
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/upload":
			if r.Header.Get("Content-Type") != "application/octet-stream" {
				http.Error(w, "bad type", 400)
				return
			}
			uploaded, _ = io.ReadAll(r.Body)
			fmt.Fprintf(w, `{"upload_url":"https://cdn.assemblyai.com/upload/abc"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v2/transcript":
			_ = json.NewDecoder(r.Body).Decode(&job)
			fmt.Fprint(w, `{"id":"t1","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/transcript/t1":
			polls++
			switch polls {
			case 1:
				fmt.Fprint(w, `{"id":"t1","status":"queued"}`)
			case 2:
				w.WriteHeader(http.StatusTooManyRequests) // retried
				fmt.Fprint(w, `{"error":"Too Many Requests"}`)
			case 3:
				fmt.Fprint(w, `{"id":"t1","status":"processing"}`)
			default:
				fmt.Fprint(w, `{"id":"t1","status":"completed","text":"Привіт, світе.","language_code":"uk","audio_duration":2.5,
					"words":[{"text":"Привіт,","start":120,"end":640,"confidence":0.98},{"text":"світе.","start":700,"end":1350,"confidence":0.97}]}`)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v2/transcript":
			if r.Header.Get("authorization") != "aai-key" {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"error":"Authentication error, API token missing/invalid"}`)
				return
			}
			fmt.Fprint(w, `{"transcripts":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := config.Default()
	oldBase := AssemblyBase
	AssemblyBase = srv.URL
	defer func() { AssemblyBase = oldBase }()
	c.STT = config.STT{Provider: "assemblyai", AssemblyKey: "aai-key", APIKey: "sk_eleven_not_for_assembly"}
	c.Normalize()
	if c.STT.Provider != "assemblyai" {
		t.Fatalf("normalized to %q", c.STT.Provider)
	}
	var steps []string
	tr, err := client(c).TranscribeProgress(context.Background(), audioFile(t, "v.mp3", []byte("mp3-bytes")), "uk",
		func(pct int, msg string) { steps = append(steps, msg) })
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if string(uploaded) != "mp3-bytes" || job["audio_url"] != "https://cdn.assemblyai.com/upload/abc" || job["language_code"] != "uk" ||
		fmt.Sprint(job["speech_models"]) != "[universal-2]" || job["punctuate"] != true {
		t.Fatalf("upload %q job %v", uploaded, job)
	}
	for _, a := range auth {
		if a != "aai-key" {
			t.Fatalf("authorization %q (no Bearer)", a)
		}
	}
	if tr.Text != "Привіт, світе." || tr.Language != "uk" || tr.Duration != 2.5 || len(tr.Words) != 2 ||
		tr.Words[0].Start != 0.12 || tr.Words[1].End != 1.35 || len(tr.Segments) != 1 {
		t.Fatalf("transcript %+v", tr)
	}
	if len(steps) == 0 {
		t.Fatal("no progress")
	}
	mu.Unlock()
	// The check and a bad key.
	if ok, msg := client(c).Test(context.Background()); !ok || !strings.Contains(msg, "AssemblyAI") {
		t.Fatalf("check %v %q", ok, msg)
	}
	c.STT.AssemblyKey = "bad"
	if ok, msg := client(c).Test(context.Background()); ok || !strings.Contains(msg, "401") {
		t.Fatalf("bad key %v %q", ok, msg)
	}
	c.STT.AssemblyKey = ""
	if _, err := client(c).Transcribe(context.Background(), audioFile(t, "v.mp3", []byte("x")), ""); err == nil || !strings.Contains(err.Error(), "AssemblyAI") {
		t.Fatalf("no key: %v", err)
	}
	mu.Lock()
	// Friendly errors.
	for body, want := range map[string]string{
		`{"error":"Your current account balance is negative. Please top up to continue using the API."}`: "закінчились кошти",
		`{"error":"Language code xx is not supported. See https://www.assemblyai.com/docs"}`:             "не розпізнає цю мову",
	} {
		if err := assemblyErr(reply{status: 400, body: []byte(body)}); !strings.Contains(err.Error(), want) {
			t.Errorf("%s → %v", body, err)
		}
	}
}
