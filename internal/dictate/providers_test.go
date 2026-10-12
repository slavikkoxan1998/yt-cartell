package dictate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"ytcartell/internal/config"
)

// wsServer is a WebSocket mock (golang.org/x/net/websocket's server side:
// an independent implementation checks our client's framing).
func wsServer(t *testing.T, handle func(ws *websocket.Conn)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(websocket.Server{Handler: handle})
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + path
}

// textSink records what a recogniser reports.
type textSink struct {
	mu       sync.Mutex
	finals   []string
	interims []string
}

func (s *textSink) final(t string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t = strings.TrimSpace(t); t != "" {
		s.finals = append(s.finals, t)
	}
}

func (s *textSink) interim(t string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interims = append(s.interims, t)
}

func (s *textSink) get() ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.finals...), append([]string(nil), s.interims...)
}

// feed sends pcm as 20 ms frames, then closes the channel.
func feed(pcm []int16) <-chan []int16 {
	ch := make(chan []int16, len(pcm)/FrameSize+1)
	for i := 0; i+FrameSize <= len(pcm); i += FrameSize {
		ch <- pcm[i : i+FrameSize]
	}
	close(ch)
	return ch
}

func TestElevenLabsRealtime(t *testing.T) {
	var got struct {
		sync.Mutex
		query  string
		key    string
		bytes  int
		commit bool
		rates  map[float64]bool
	}
	got.rates = map[float64]bool{}
	srv := wsServer(t, func(ws *websocket.Conn) {
		got.Lock()
		got.query, got.key = ws.Request().URL.RawQuery, ws.Request().Header.Get("xi-api-key")
		got.Unlock()
		websocket.Message.Send(ws, `{"message_type":"session_started","session_id":"s1"}`)
		sent := false
		for {
			var msg string
			if err := websocket.Message.Receive(ws, &msg); err != nil {
				return
			}
			var m struct {
				Type   string  `json:"message_type"`
				Audio  string  `json:"audio_base_64"`
				Commit bool    `json:"commit"`
				Rate   float64 `json:"sample_rate"`
			}
			if json.Unmarshal([]byte(msg), &m) != nil || m.Type != "input_audio_chunk" {
				t.Errorf("bad message %q", msg)
				return
			}
			b, _ := base64.StdEncoding.DecodeString(m.Audio)
			got.Lock()
			got.bytes += len(b)
			got.rates[m.Rate] = true
			got.Unlock()
			if !sent && got.bytes >= 16000 {
				sent = true
				websocket.Message.Send(ws, `{"message_type":"partial_transcript","text":"привіт"}`)
				websocket.Message.Send(ws, `{"message_type":"committed_transcript","text":"Привіт."}`)
				websocket.Message.Send(ws, `{"message_type":"partial_transcript","text":"як справи"}`)
			}
			if m.Commit {
				got.Lock()
				got.commit = true
				got.Unlock()
				websocket.Message.Send(ws, `{"message_type":"committed_transcript","text":"Як справи?"}`)
			}
		}
	})
	r := &elevenRec{p: Plan{Provider: ElevenLabs, Key: "sk_test", BaseURL: wsURL(srv, "/v1/speech-to-text/realtime"), Model: ElevenModel, Language: "uk"}}
	var out textSink
	if err := r.run(bg, slowFeed(tone(1), time.Millisecond), &out); err != nil {
		t.Fatal(err)
	}
	finals, interims := out.get()
	if strings.Join(finals, "|") != "Привіт.|Як справи?" {
		t.Fatalf("finals %q (interims %q)", finals, interims)
	}
	got.Lock()
	defer got.Unlock()
	for _, want := range []string{"model_id=scribe_v2_realtime", "audio_format=pcm_16000", "commit_strategy=vad", "language_code=uk"} {
		if !strings.Contains(got.query, want) {
			t.Errorf("query %q lacks %s", got.query, want)
		}
	}
	if got.key != "sk_test" || got.bytes != 32000 || !got.commit || !got.rates[16000] || len(got.rates) != 1 {
		t.Fatalf("key %q, %d bytes, commit %v, rates %v", got.key, got.bytes, got.commit, got.rates)
	}
}

func TestElevenLabsErrors(t *testing.T) {
	// A refused handshake.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"detail":{"status":"invalid_api_key","message":"Invalid API key"}}`)
	}))
	defer bad.Close()
	r := &elevenRec{p: Plan{Key: "k", BaseURL: wsURL(bad, "/rt")}}
	if err := r.run(bg, feed(tone(0.2)), &textSink{}); KindOf(err) != KindAuth {
		t.Fatalf("401 → %v", err)
	}
	// An error message from the service mid-stream.
	srv := wsServer(t, func(ws *websocket.Conn) {
		var msg string
		_ = websocket.Message.Receive(ws, &msg)
		websocket.Message.Send(ws, `{"message_type":"quota_exceeded","error":"You exceeded your quota"}`)
		time.Sleep(50 * time.Millisecond)
	})
	r = &elevenRec{p: Plan{Key: "k", BaseURL: wsURL(srv, "/rt")}}
	slow := make(chan []int16)
	go func() {
		for i := 0; i < 200; i++ {
			slow <- tone(0.02)
			time.Sleep(2 * time.Millisecond)
		}
		close(slow)
	}()
	if err := r.run(bg, slow, &textSink{}); KindOf(err) != KindQuota {
		t.Fatalf("quota → %v", err)
	}
	// No server at all.
	r = &elevenRec{p: Plan{Key: "k", BaseURL: "ws://127.0.0.1:1/rt"}}
	if err := r.run(bg, feed(tone(0.1)), &textSink{}); KindOf(err) != KindNetwork {
		t.Fatalf("offline → %v", err)
	}
}

func TestOpenAIRealtime(t *testing.T) {
	type rec struct {
		path, auth, beta string
		update           map[string]any
		appended         int
		commits          int
	}
	var mu sync.Mutex
	var got rec
	srv := wsServer(t, func(ws *websocket.Conn) {
		mu.Lock()
		got.path = ws.Request().URL.Path + "?" + ws.Request().URL.RawQuery
		got.auth, got.beta = ws.Request().Header.Get("Authorization"), ws.Request().Header.Get("OpenAI-Beta")
		mu.Unlock()
		websocket.Message.Send(ws, `{"type":"session.created","session":{"type":"transcription"}}`)
		item := 0
		for {
			var msg string
			if err := websocket.Message.Receive(ws, &msg); err != nil {
				return
			}
			var ev map[string]any
			_ = json.Unmarshal([]byte(msg), &ev)
			switch ev["type"] {
			case "session.update":
				mu.Lock()
				got.update = ev
				mu.Unlock()
				websocket.Message.Send(ws, `{"type":"session.updated"}`)
			case "input_audio_buffer.append":
				b, _ := base64.StdEncoding.DecodeString(ev["audio"].(string))
				mu.Lock()
				got.appended += len(b)
				first := got.appended == len(b)
				mu.Unlock()
				if first { // a live model streams deltas before the commit
					websocket.Message.Send(ws, `{"type":"conversation.item.input_audio_transcription.delta","item_id":"it1","delta":"Раз "}`)
				}
			case "input_audio_buffer.commit":
				mu.Lock()
				got.commits++
				mu.Unlock()
				item++
				id := fmt.Sprintf("it%d", item)
				prev := "null"
				if item > 1 {
					prev = fmt.Sprintf(`"it%d"`, item-1)
				}
				websocket.Message.Send(ws, fmt.Sprintf(`{"type":"input_audio_buffer.committed","item_id":%q,"previous_item_id":%s}`, id, prev))
				if item == 1 {
					websocket.Message.Send(ws, `{"type":"conversation.item.input_audio_transcription.delta","item_id":"it1","delta":"два"}`)
					// The first turn finishes only after the second: order must hold.
				} else {
					websocket.Message.Send(ws, `{"type":"conversation.item.input_audio_transcription.completed","item_id":"it2","transcript":"Три."}`)
					websocket.Message.Send(ws, `{"type":"conversation.item.input_audio_transcription.completed","item_id":"it1","transcript":"Раз, два."}`)
				}
			}
		}
	})
	r := &openaiRec{p: Plan{Provider: OpenAI, Key: "sk-test", BaseURL: srv.URL + "/v1", Model: "gpt-live-transcribe", Language: "uk"}}
	var out textSink
	audio := cat(tone(1), hush(1), tone(1), hush(0.3))
	if err := r.run(bg, feed(audio), &out); err != nil {
		t.Fatal(err)
	}
	finals, interims := out.get()
	if strings.Join(finals, "|") != "Раз, два.|Три." {
		t.Fatalf("finals %q", finals)
	}
	if !contains(interims, "Раз два") {
		t.Fatalf("interims %q", interims)
	}
	mu.Lock()
	defer mu.Unlock()
	if got.path != "/v1/realtime?intent=transcription" || got.auth != "Bearer sk-test" || got.beta != "" {
		t.Fatalf("connection %+v", got)
	}
	// 3.3 s of 16 kHz audio → 24 kHz PCM16.
	if want := len(audio)*3/2*2 - 1*2; got.appended < want-4 || got.appended > want+4 {
		t.Fatalf("appended %d bytes, want ≈ %d", got.appended, want)
	}
	if got.commits != 2 {
		t.Fatalf("%d commits, want 2 (one per phrase)", got.commits)
	}
	b, _ := json.Marshal(got.update)
	for _, want := range []string{`"type":"transcription"`, `"rate":24000`, `"type":"audio/pcm"`, `"turn_detection":null`,
		`"model":"gpt-live-transcribe"`, `"languages":["uk"]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("session.update %s lacks %s", b, want)
		}
	}
	// Other models: server VAD and the singular language.
	r.p.Model = "gpt-4o-transcribe"
	u := string(r.sessionUpdate())
	if !strings.Contains(u, `"server_vad"`) || !strings.Contains(u, `"language":"uk"`) || strings.Contains(u, "languages") {
		t.Fatalf("gpt-4o-transcribe update %s", u)
	}
}

func TestOpenAIRealtimeSetupError(t *testing.T) {
	srv := wsServer(t, func(ws *websocket.Conn) {
		var msg string
		_ = websocket.Message.Receive(ws, &msg) // session.update
		websocket.Message.Send(ws, `{"type":"error","error":{"type":"invalid_request_error","code":"invalid_value","message":"Invalid model: gpt-nope"}}`)
		time.Sleep(100 * time.Millisecond)
	})
	r := &openaiRec{p: Plan{Key: "k", BaseURL: srv.URL + "/v1", Model: "gpt-nope"}}
	slow := make(chan []int16)
	go func() {
		defer close(slow)
		for i := 0; i < 100; i++ {
			slow <- tone(0.02)
			time.Sleep(3 * time.Millisecond)
		}
	}()
	err := r.run(bg, slow, &textSink{})
	if KindOf(err) != KindService || !strings.Contains(err.Error(), "модель") {
		t.Fatalf("setup error → %v", err)
	}
}

func TestDeepgram(t *testing.T) {
	var mu sync.Mutex
	var query, auth string
	var bytesIn int
	var control []string
	srv := wsServer(t, func(ws *websocket.Conn) {
		mu.Lock()
		query, auth = ws.Request().URL.RawQuery, ws.Request().Header.Get("Authorization")
		mu.Unlock()
		said := false
		for {
			var m frame
			if err := rawCodec.Receive(ws, &m); err != nil {
				return
			}
			data := m.data
			if m.op == websocket.TextFrame {
				mu.Lock()
				control = append(control, string(data))
				mu.Unlock()
				if strings.Contains(string(data), "CloseStream") {
					websocket.Message.Send(ws, `{"type":"Results","is_final":true,"channel":{"alternatives":[{"transcript":"Кінець."}]}}`)
					websocket.Message.Send(ws, `{"type":"Metadata","duration":1}`)
					ws.Close()
					return
				}
				continue
			}
			mu.Lock()
			bytesIn += len(data)
			mu.Unlock()
			if !said {
				said = true
				websocket.Message.Send(ws, `{"type":"Results","is_final":false,"channel":{"alternatives":[{"transcript":"добрий"}]}}`)
				websocket.Message.Send(ws, `{"type":"Results","is_final":true,"speech_final":true,"channel":{"alternatives":[{"transcript":"Добрий день."}]}}`)
			}
		}
	})
	r := &deepgramRec{p: Plan{Key: "dg-key", BaseURL: wsURL(srv, "/v1/listen"), Language: "uk"}}
	var out textSink
	if err := r.run(bg, feed(tone(0.5)), &out); err != nil {
		t.Fatal(err)
	}
	finals, interims := out.get()
	if strings.Join(finals, "|") != "Добрий день.|Кінець." || !contains(interims, "добрий") {
		t.Fatalf("finals %q interims %q", finals, interims)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{"model=nova-3", "language=uk", "encoding=linear16", "sample_rate=16000", "interim_results=true", "smart_format=true"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q lacks %s", query, want)
		}
	}
	if auth != "Token dg-key" || bytesIn != 16000 || strings.Join(control, ",") != `{"type":"Finalize"},{"type":"CloseStream"}` {
		t.Fatalf("auth %q, %d bytes, control %q", auth, bytesIn, control)
	}
	// Auto language → multi.
	r.p.Language = ""
	if !strings.Contains(r.url(), "language=multi") {
		t.Fatal(r.url())
	}
}

func TestOpenAIChunks(t *testing.T) {
	var mu sync.Mutex
	type req struct {
		fields map[string][]string
		wavLen int
		auth   string
	}
	var reqs []req
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		f, fh, _ := r.FormFile("file")
		b, _ := io.ReadAll(f)
		f.Close()
		if fh.Header.Get("Content-Type") != "audio/wav" || string(b[:4]) != "RIFF" {
			http.Error(w, "not a wav", 400)
			return
		}
		mu.Lock()
		reqs = append(reqs, req{fields: r.MultipartForm.Value, wavLen: len(b), auth: r.Header.Get("Authorization")})
		n := len(reqs)
		mu.Unlock()
		fmt.Fprintf(w, `{"text":"Фраза %d."}`, n)
	}))
	defer srv.Close()
	p := Plan{Provider: OpenAIChunks, BaseURL: srv.URL + "/v1", Key: "ck", Model: "gpt-4o-mini-transcribe", Language: "uk"}
	r := newChunkRec(&openaiHTTP{p: p}, chunkMode{maxPhrase: 750, finish: time.Second}, p.Language)
	var out textSink
	// Two phrases with a pause, silence around: two uploads, no silence sent.
	audio := cat(hush(0.5), tone(1.2), hush(1.2), tone(0.8), hush(0.5))
	if err := r.run(bg, feed(audio), &out); err != nil {
		t.Fatal(err)
	}
	finals, _ := out.get()
	if strings.Join(finals, "|") != "Фраза 1.|Фраза 2." {
		t.Fatalf("finals %q", finals)
	}
	mu.Lock()
	defer mu.Unlock()
	f := reqs[0].fields
	if reqs[0].auth != "Bearer ck" || f["model"][0] != "gpt-4o-mini-transcribe" || f["language"][0] != "uk" || f["response_format"][0] != "json" {
		t.Fatalf("first request %+v", reqs[0])
	}
	// phrase 1: 0.3 s before + 1.2 s + 0.2 s of the pause (+ hangover) ≈ 1.7–2 s.
	if sec := float64(reqs[0].wavLen-44) / bytesPerSec; sec < 1.6 || sec > 2.1 {
		t.Fatalf("phrase 1 is %.2f s", sec)
	}
	if got := reqs[1].fields["prompt"]; len(got) != 1 || got[0] != "Фраза 1." {
		t.Fatalf("second prompt %q", got)
	}
}

func TestChunkerInterimAndForcedCut(t *testing.T) {
	var mu sync.Mutex
	var lens []float64
	tr := &fakeT{fn: func(pcm []int16, lang, prompt string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		lens = append(lens, float64(len(pcm))/SampleRate)
		sec := float64(len(pcm)) / SampleRate
		if sec > 3.5 { // the forced cut's first part
			return "один два три чотири", nil
		}
		if prompt != "" {
			return "три чотири п'ять", nil // repeats the overlap
		}
		return "один два", nil
	}}
	// 6 s of talking without a pause; a cut at 4 s, interim reads every 1 s.
	r := newChunkRec(tr, chunkMode{maxPhrase: 200, interimStep: 50, finish: time.Second}, "uk")
	var out textSink
	slow := make(chan []int16)
	go func() {
		pcm := cat(tone(6), hush(1))
		for i := 0; i+FrameSize <= len(pcm); i += FrameSize {
			slow <- pcm[i : i+FrameSize]
			time.Sleep(200 * time.Microsecond)
		}
		close(slow)
	}()
	if err := r.run(bg, slow, &out); err != nil {
		t.Fatal(err)
	}
	finals, interims := out.get()
	if strings.Join(finals, "|") != "один два три чотири|п'ять" {
		t.Fatalf("finals %q (lens %v)", finals, lens)
	}
	if !contains(interims, "один два") {
		t.Fatalf("no interim text: %q", interims)
	}
	if !tr.started || !tr.closed {
		t.Fatal("start/close not called")
	}
}

type fakeT struct {
	mu              sync.Mutex
	fn              func(pcm []int16, lang, prompt string) (string, error)
	started, closed bool
}

func (f *fakeT) start(context.Context) error { f.started = true; return nil }
func (f *fakeT) close()                      { f.closed = true }
func (f *fakeT) transcribe(_ context.Context, pcm []int16, lang, prompt string) (string, string, error) {
	t, err := f.fn(pcm, lang, prompt)
	return t, "", err
}

func TestGemini(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := r.Header.Get("x-goog-api-key")
		mu.Lock()
		keys = append(keys, k)
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Unlock()
		if r.URL.Path != "/v1beta/models/gemini-3.5-flash-lite:generateContent" {
			http.NotFound(w, r)
			return
		}
		if k == "g1" {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`)
			return
		}
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"Привіт, світе."}]}}]}`)
	}))
	defer srv.Close()
	g := &geminiT{p: Plan{BaseURL: srv.URL + "/v1beta", Keys: []string{"g1", "g2"}, Model: GeminiDef}}
	text, _, err := g.transcribe(bg, tone(1), "uk", "До цього")
	if err != nil || text != "Привіт, світе." {
		t.Fatalf("%q %v", text, err)
	}
	mu.Lock()
	k1 := strings.Join(keys, ",")
	b, _ := json.Marshal(body)
	mu.Unlock()
	if k1 != "g1,g2" {
		t.Fatalf("keys %v", k1)
	}
	if !strings.Contains(string(b), `"mimeType":"audio/wav"`) || !strings.Contains(string(b), `ISO 639 \"uk\"`) || !strings.Contains(string(b), "До цього") {
		t.Fatalf("body %s", b[:min(len(b), 600)])
	}
	// The next call starts with the key that worked.
	if _, _, err := g.transcribe(bg, tone(1), "", ""); err != nil {
		t.Fatalf("rotation: %v", err)
	}
	mu.Lock()
	last := keys[len(keys)-1]
	mu.Unlock()
	if last != "g2" {
		t.Fatalf("rotation started with %s", last)
	}
	// A bad key.
	g = &geminiT{p: Plan{BaseURL: srv.URL + "/nope", Key: "x", Model: GeminiDef}}
	if _, _, err := g.transcribe(bg, tone(1), "", ""); KindOf(err) != KindService {
		t.Fatalf("404 → %v", err)
	}
}

func TestLocalServer(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "whisper-cli")
	srvBin := filepath.Join(dir, "whisper-server")
	if runtime.GOOS == "windows" {
		cli += ".exe"
		srvBin += ".exe"
	}
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if os.Link(os.Args[0], srvBin) != nil {
		if err := os.WriteFile(srvBin, self, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(cli, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	model := filepath.Join(dir, "ggml-tiny.bin")
	_ = os.WriteFile(model, []byte("lmgg"), 0o600)
	t.Setenv(fakeServerEnv, "Локально")
	old := serverIdle
	serverIdle = 300 * time.Millisecond
	defer func() { serverIdle = old }()

	p := Plan{Provider: Local, Bin: cli, ModelPath: model}
	rec := newLocalRec(p)
	cr := rec.(*chunkRec)
	if cr.mode.interimStep == 0 {
		t.Fatal("with whisper-server the interim text is on")
	}
	var out textSink
	if err := rec.run(bg, feed(cat(tone(2), hush(1))), &out); err != nil {
		t.Fatal(err)
	}
	finals, _ := out.get()
	srvMu.Lock()
	s := server
	srvMu.Unlock()
	// The first long phrase is read with language detection (verbose_json).
	if strings.Join(finals, "|") != "Локально auto" {
		t.Fatalf("finals %q", finals)
	}
	if s == nil {
		t.Fatal("no whisper-server was kept warm")
	}
	select { // the idle server exits after serverIdle
	case <-s.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the idle whisper-server still runs")
	}
	// Without whisper-server: the CLI mode, no interim.
	if err := os.Remove(srvBin); err != nil {
		t.Fatal(err)
	}
	if r := newLocalRec(p).(*chunkRec); r.mode.interimStep != 0 {
		t.Fatal("CLI mode has no interim reads")
	}
	if whisperLang("uk") != "uk" || whisperLang("") != "auto" || whisperLang("fil") != "tl" || whisperLang("xx") != "auto" {
		t.Fatal("whisperLang")
	}
	if whisperCode("ukrainian") != "uk" || whisperCode("javanese") != "jv" || whisperCode("") != "" {
		t.Fatal("whisperCode")
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// frame is one received message with its type (x/net/websocket's Message
// codec drops the type).
type frame struct {
	op   byte
	data []byte
}

var rawCodec = websocket.Codec{
	Marshal: func(v any) ([]byte, byte, error) { return []byte(v.(string)), websocket.TextFrame, nil },
	Unmarshal: func(data []byte, op byte, v any) error {
		m := v.(*frame)
		m.op, m.data = op, append([]byte(nil), data...)
		return nil
	},
}

// slowFeed sends pcm frame by frame with a pause (a live microphone, faster).
func slowFeed(pcm []int16, d time.Duration) <-chan []int16 {
	ch := make(chan []int16)
	go func() {
		defer close(ch)
		for i := 0; i+FrameSize <= len(pcm); i += FrameSize {
			ch <- pcm[i : i+FrameSize]
			time.Sleep(d)
		}
	}()
	return ch
}

func TestCheck(t *testing.T) {
	eleven := wsServer(t, func(ws *websocket.Conn) {
		if ws.Request().Header.Get("xi-api-key") != "good" {
			websocket.Message.Send(ws, `{"message_type":"auth_error","error":"Invalid API key"}`)
			return
		}
		websocket.Message.Send(ws, `{"message_type":"session_started","session_id":"x"}`)
		var s string
		_ = websocket.Message.Receive(ws, &s)
	})
	p := Plan{Provider: ElevenLabs, Ready: true, Key: "good", BaseURL: wsURL(eleven, "/rt")}
	if ok, msg := Check(bg, p); !ok || !strings.Contains(msg, "ElevenLabs") {
		t.Fatalf("eleven ok: %v %q", ok, msg)
	}
	p.Key = "bad"
	if ok, msg := Check(bg, p); ok || !strings.Contains(msg, "ключ") {
		t.Fatalf("eleven bad: %v %q", ok, msg)
	}
	oa := wsServer(t, func(ws *websocket.Conn) {
		var s string
		_ = websocket.Message.Receive(ws, &s)
		if strings.Contains(s, "gpt-nope") {
			websocket.Message.Send(ws, `{"type":"error","error":{"code":"invalid_value","message":"Invalid model"}}`)
		} else {
			websocket.Message.Send(ws, `{"type":"session.updated"}`)
		}
		_ = websocket.Message.Receive(ws, &s)
	})
	p = Plan{Provider: OpenAI, Ready: true, Key: "k", BaseURL: oa.URL + "/v1"}
	if ok, msg := Check(bg, p); !ok || !strings.Contains(msg, OpenAIRealtimeDef) {
		t.Fatalf("openai ok: %v %q", ok, msg)
	}
	p.Model = "gpt-nope"
	if ok, msg := Check(bg, p); ok || !strings.Contains(msg, "модель") {
		t.Fatalf("openai bad model: %v %q", ok, msg)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"whisper-1"},{"id":"gpt-4o-mini-transcribe"}]}`)
		case "/v1beta/models/gemini-3.5-flash-lite":
			fmt.Fprint(w, `{"name":"models/gemini-3.5-flash-lite"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	p = Plan{Provider: OpenAIChunks, Ready: true, Key: "k", BaseURL: api.URL + "/v1", Model: "gpt-4o-mini-transcribe"}
	if ok, msg := Check(bg, p); !ok || !strings.Contains(msg, "доступна") {
		t.Fatalf("chunks: %v %q", ok, msg)
	}
	p.Model = "nope"
	if ok, _ := Check(bg, p); ok {
		t.Fatal("chunks: a missing model passed")
	}
	p = Plan{Provider: Gemini, Ready: true, Key: "k", BaseURL: api.URL + "/v1beta"}
	if ok, msg := Check(bg, p); !ok {
		t.Fatalf("gemini: %q", msg)
	}
	if ok, msg := Check(bg, Plan{Provider: Deepgram, Problem: "Немає ключа Deepgram"}); ok || !strings.Contains(msg, "Deepgram") {
		t.Fatalf("not ready: %v %q", ok, msg)
	}
}

func TestAssemblyAIStreaming(t *testing.T) {
	var mu sync.Mutex
	var query, auth string
	var frames []int
	terminated := false
	srv := wsServer(t, func(ws *websocket.Conn) {
		mu.Lock()
		query, auth = ws.Request().URL.RawQuery, ws.Request().Header.Get("Authorization")
		mu.Unlock()
		websocket.Message.Send(ws, `{"type":"Begin","id":"s1","expires_at":1772570132}`)
		said := false
		for {
			var m frame
			if err := rawCodec.Receive(ws, &m); err != nil {
				return
			}
			if m.op == websocket.TextFrame {
				if strings.Contains(string(m.data), "Terminate") {
					mu.Lock()
					terminated = true
					mu.Unlock()
					// The last turn: unformatted end, then the formatted final.
					websocket.Message.Send(ws, `{"type":"Turn","turn_order":1,"end_of_turn":true,"turn_is_formatted":false,"transcript":"and then"}`)
					websocket.Message.Send(ws, `{"type":"Turn","turn_order":1,"end_of_turn":true,"turn_is_formatted":true,"transcript":"And then."}`)
					websocket.Message.Send(ws, `{"type":"Termination","audio_duration_seconds":1,"session_duration_seconds":1}`)
					return
				}
				continue
			}
			mu.Lock()
			frames = append(frames, len(m.data))
			mu.Unlock()
			if !said {
				said = true
				websocket.Message.Send(ws, `{"type":"Turn","turn_order":0,"end_of_turn":false,"turn_is_formatted":false,"transcript":"hello"}`)
				websocket.Message.Send(ws, `{"type":"Turn","turn_order":0,"end_of_turn":true,"turn_is_formatted":true,"transcript":"Hello there."}`)
			}
		}
	})
	r := &assemblyRec{p: Plan{Key: "aai", BaseURL: wsURL(srv, "/v3/ws"), Language: "en"}}
	var out textSink
	if err := r.run(bg, slowFeed(tone(0.5), time.Millisecond), &out); err != nil {
		t.Fatal(err)
	}
	finals, interims := out.get()
	if strings.Join(finals, "|") != "Hello there.|And then." || !contains(interims, "hello") {
		t.Fatalf("finals %q interims %q", finals, interims)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{"sample_rate=16000", "encoding=pcm_s16le", "speech_model=universal-3-6-pro", "language_codes=en"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q lacks %s", query, want)
		}
	}
	if auth != "aai" || !terminated || len(frames) != 5 || frames[0] != 3200 {
		t.Fatalf("auth %q terminated %v frames %v", auth, terminated, frames)
	}
	// Errors before a close.
	if err := assemblyErr(1008, "Unauthorized Connection: Invalid API key"); KindOf(err) != KindAuth {
		t.Fatalf("1008 → %v", err)
	}
	if err := assemblyErr(1008, "Unauthorized Connection: insufficient balance"); KindOf(err) != KindQuota {
		t.Fatalf("balance → %v", err)
	}
	// Ukrainian is refused before connecting; auto detects.
	cfg := testConfig()
	cfg.Dictation.Provider, cfg.Dictation.AssemblyKey, cfg.Dictation.Language = AssemblyAI, "k", "uk"
	if p := Resolve(cfg); p.Ready || !strings.Contains(p.Problem, "українськ") {
		t.Fatalf("uk: %+v", p)
	}
	r.p.Language = ""
	if !strings.Contains(r.url(), "language_detection=true") {
		t.Fatal(r.url())
	}
}

func testConfig() config.Config {
	c := config.Default()
	c.Normalize()
	return c
}

// TestLocalCLI: without whisper-server every phrase runs whisper-cli.
func TestLocalCLI(t *testing.T) {
	t.Setenv(fakeCLIEnv, "Фраза")
	p := Plan{Provider: Local, Bin: os.Args[0], ModelPath: filepath.Join(t.TempDir(), "ggml-tiny.bin"), Language: "uk"}
	rec := newLocalRec(p)
	var out textSink
	if err := rec.run(bg, feed(cat(tone(1), hush(1), tone(1), hush(1))), &out); err != nil {
		t.Fatal(err)
	}
	if finals, _ := out.get(); strings.Join(finals, "|") != "Фраза uk|Фраза uk" {
		t.Fatalf("finals %q", finals)
	}
	// A failing run explains itself.
	lt := &localT{p: p, cli: true}
	if err := lt.start(bg); err != nil {
		t.Fatal(err)
	}
	defer lt.close()
	t.Setenv(fakeCLIEnv, "")
	lt.p.Bin = filepath.Join(t.TempDir(), "missing-whisper-cli")
	if _, _, err := lt.transcribe(bg, tone(1), "uk", ""); err == nil {
		t.Fatal("a missing program passed")
	}
}
