package reference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gemini"
)

// ---- harness: only this binary's mock servers are reachable ------------------------------------

var (
	allowMu sync.Mutex
	allowed = map[string]bool{}
)

func allow(srv *httptest.Server) {
	u, _ := url.Parse(srv.URL)
	allowMu.Lock()
	allowed[u.Host] = true
	allowMu.Unlock()
}

type guard struct{ base http.RoundTripper }

func (g guard) RoundTrip(r *http.Request) (*http.Response, error) {
	allowMu.Lock()
	ok := allowed[r.URL.Host]
	allowMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("test guard: blocked request to %s", r.URL.Host)
	}
	return g.base.RoundTrip(r)
}

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "ytc-reference-test-")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	os.Setenv("AppData", filepath.Join(tmp, "appdata"))
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	if runtime.GOOS == "darwin" {
		os.Setenv("HOME", filepath.Join(tmp, "home"))
	}
	if !strings.HasPrefix(config.Dir(), tmp) {
		fmt.Println("refusing to run: settings folder is not redirected:", config.Dir())
		os.Exit(1)
	}
	http.DefaultTransport = guard{base: http.DefaultTransport}
	retryPause = 5 * time.Millisecond
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

const testURL = "https://www.youtube.com/watch?v=abcdefghijk"

// ---- addresses -------------------------------------------------------------------------------

func TestYouTubeID(t *testing.T) {
	ids := map[string]string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=10s": "dQw4w9WgXcQ",
		" youtu.be/dQw4w9WgXcQ?si=xyz ":                     "dQw4w9WgXcQ",
		"https://youtube.com/shorts/dQw4w9WgXcQ":            "dQw4w9WgXcQ",
		"https://m.youtube.com/watch?v=dQw4w9WgXcQ":         "dQw4w9WgXcQ",
		"https://music.youtube.com/watch?v=dQw4w9WgXcQ":     "dQw4w9WgXcQ",
		"https://www.youtube.com/embed/dQw4w9WgXcQ":         "dQw4w9WgXcQ",
		"https://www.youtube.com/live/dQw4w9WgXcQ":          "dQw4w9WgXcQ",
		"dQw4w9WgXcQ":                           "dQw4w9WgXcQ",
		"https://vimeo.com/123":                 "",
		"https://www.youtube.com/channel/UCxyz": "",
		"https://www.youtube.com/watch?v=short": "",
		"просто текст":                          "",
		"https://evil.com/watch?v=dQw4w9WgXcQ":  "",
	}
	for in, want := range ids {
		if got := YouTubeID(in); got != want {
			t.Errorf("YouTubeID(%q) = %q, want %q", in, got, want)
		}
	}
	if u, err := NormalizeURL("youtube.com/shorts/dQw4w9WgXcQ"); err != nil || u != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("NormalizeURL = %q %v", u, err)
	}
	if _, err := NormalizeURL("https://tiktok.com/@a/video/1"); KindOf(err) != KindBadURL {
		t.Fatalf("a non-YouTube link must be refused: %v", err)
	}
}

func TestSecondsAndClock(t *testing.T) {
	cases := map[any]float64{83.5: 83.5, "75": 75, "75s": 75, "1:15": 75, "01:01:15": 3675, "1m15s": 75, "2m": 120, "x": -1, nil: -1}
	for in, want := range cases {
		if got := seconds(in); got != want {
			t.Errorf("seconds(%v) = %v, want %v", in, got, want)
		}
	}
	if Clock(75) != "1:15" || Clock(3675) != "1:01:15" {
		t.Fatal(Clock(75), Clock(3675))
	}
}

// ---- parsing ---------------------------------------------------------------------------------

const shortReply = "```json\n" + `{
 "seen": true, "title": "Як зробити хук", "durationSeconds": "1:35", "language": "en-US", "aspect": "9x16",
 "firstWords": "Stop scrolling", "summary": "Поради про хуки.",
 "hook": {"end": 6, "what": "Ведучий кричить «Stop scrolling»", "why": "Пряме звертання"},
 "structure": [{"start": 0, "end": 6, "name": "Хук", "purpose": "зупинити"}, {"start": 6, "end": 95, "name": "Поради", "purpose": "цінність"}],
 "scenes": [
  {"start": 0, "end": 6, "shot": "крупний план", "visual": "Чоловік у червоній худі дивиться в камеру", "onScreenText": "STOP", "speaker": "on-camera", "speech": "Stop scrolling!", "sound": "удар"},
  {"start": 6, "end": "0:40", "shot": "середній план", "visual": "Той самий чоловік за столом", "speaker": "on-camera", "speech": "Here is the trick", "transition": "cut"},
  {"start": 40, "end": 95, "shot": "швидкі склейки", "visual": "Приклади відео на телефоні", "speaker": "voice-over", "speech": "Try it today"}
 ],
 "cutsPerMinute": 24, "avgShotSeconds": "2.5", "people": "Characters", "audioMode": "invideo",
 "narrationStyle": "Енергійно, на ти", "music": "Лоу-фай біт", "visualStyle": "Живе відео, яскраве світло", "palette": "червоний, білий",
 "pacing": "Швидко на початку", "whyItWorks": ["Пряме звертання", "Швидкий темп"]
}` + "\n```\nГотово!"

func TestParseBreakdown(t *testing.T) {
	b, err := ParseBreakdown(shortReply)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Seen || b.Duration != 95 || b.Language != "en" || b.Aspect != "9:16" || len(b.Scenes) != 3 || b.Scenes[1].End != 40 ||
		b.People != "characters" || b.AudioMode != "invideo" || b.AvgShot != 2.5 || len(b.Why) != 2 || b.Hook.End != 6 {
		t.Fatalf("breakdown = %+v", b)
	}
	text := b.Text(func(c string) string { return map[string]string{"en": "англійська"}[c] })
	for _, want := range []string{"«Як зробити хук» · 1:35 · 9:16 · мова: англійська", "Хук (0:00–0:06)", "Сцени (3):",
		"1. 0:00–0:06 · крупний план", "   Текст на екрані: «STOP»", "   Говорять у кадрі: «Stop scrolling!»",
		"   Голос за кадром: «Try it today»", "Темп: 24 склейки/хв, план у середньому 2,5 с", "• Швидкий темп"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if b, err := ParseBreakdown(`{"seen": false}`); err != nil || b.Seen {
		t.Fatalf("seen=false: %+v %v", b, err)
	}
	if _, err := ParseBreakdown("Sorry, I can't access YouTube videos."); KindOf(err) != KindNotSeen {
		t.Fatalf("refusal: %v", err)
	}
	if _, err := ParseBreakdown("just words"); KindOf(err) != KindBadReply {
		t.Fatalf("prose: %v", err)
	}
	// An answer cut off at the token limit keeps its whole scenes.
	cut := `{"seen":true,"durationSeconds":95,"scenes":[{"start":0,"end":6,"visual":"Перша"},{"start":6,"end":40,"visual":"Друга"},{"start":40,"end":9`
	b, err = ParseBreakdown(cut)
	if err != nil || len(b.Scenes) < 2 || b.Scenes[1].Visual != "Друга" {
		t.Fatalf("cut-off reply: %+v %v", b, err)
	}
	cut2 := `{"seen":true,"durationSeconds":95,"summary":"Про щось","scenes":[{"start":0,"end":6,"visual":"Перша сце`
	if b, err := ParseBreakdown(cut2); err != nil || b.Summary != "Про щось" {
		t.Fatalf("cut inside a string: %+v %v", b, err)
	}
}

func TestCleanAndShift(t *testing.T) {
	in := []Scene{{Start: 10, End: 5, Visual: "b"}, {Start: 0, End: 12, Visual: "a"}, {Start: 10.2, End: 20, Visual: "dup"}, {Start: 30, End: 0, Visual: "c"}, {Start: 200, End: 210, Visual: "past"}}
	out := cleanScenes(in, 100)
	if len(out) != 3 || out[0].End != 10 || out[1].Start != 10 || out[1].End != 10.2 || out[2].Start != 30 || out[2].End != 100 {
		t.Fatalf("clean = %+v", out)
	}
	rel := []Scene{{Start: 0, End: 30, Visual: "x"}, {Start: 30, End: 600, Visual: "y"}}
	if s := shiftRelative(rel, 600, 1200); s[0].Start != 600 || s[1].End != 1200 {
		t.Fatalf("relative part not shifted: %+v", s)
	}
	abs := []Scene{{Start: 600, End: 630, Visual: "x"}}
	if s := shiftRelative(abs, 600, 1200); s[0].Start != 600 {
		t.Fatalf("absolute part shifted: %+v", s)
	}
}

// ---- Google AI Studio --------------------------------------------------------------------------

type googleMock struct {
	mu      sync.Mutex
	bodies  []map[string]any
	reply   func(n int, body map[string]any, prompt string) (int, string)
	keysHit []string
}

func (m *googleMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, ":generateContent") {
		http.NotFound(w, r)
		return
	}
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	m.mu.Lock()
	m.bodies = append(m.bodies, body)
	m.keysHit = append(m.keysHit, r.Header.Get("x-goog-api-key"))
	n := len(m.bodies)
	m.mu.Unlock()
	prompt := ""
	parts := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	for _, p := range parts {
		if t, ok := p.(map[string]any)["text"].(string); ok {
			prompt += t
		}
	}
	status, text := m.reply(n, body, prompt)
	w.WriteHeader(status)
	if status != 200 {
		io.WriteString(w, text)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{
		"content":      map[string]any{"parts": []any{map[string]any{"text": "thinking…", "thought": true}, map[string]any{"text": text}}},
		"finishReason": "STOP"}}})
}

func newGoogle(t *testing.T, reply func(n int, body map[string]any, prompt string) (int, string), keys ...string) (*Google, *googleMock) {
	t.Helper()
	m := &googleMock{reply: reply}
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	allow(srv)
	old := googleBase
	googleBase = srv.URL + "/v1beta"
	t.Cleanup(func() { googleBase = old })
	if len(keys) == 0 {
		keys = []string{"key-a"}
	}
	return &Google{Pool: gemini.NewPool(keys), Model: "gemini-3.8-flash"}, m
}

func videoPart(body map[string]any) map[string]any {
	parts := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	return parts[0].(map[string]any)
}

func TestGoogleShortVideo(t *testing.T) {
	g, m := newGoogle(t, func(n int, body map[string]any, prompt string) (int, string) { return 200, shortReply })
	var pcts []float64
	var mu sync.Mutex
	b, err := Analyze(context.Background(), g, Options{URL: testURL, Meta: Meta{Duration: 95, Title: "Як зробити хук"},
		Progress: func(p float64, msg string) { mu.Lock(); pcts = append(pcts, p); mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.bodies) != 1 || len(b.Scenes) != 3 || b.Via != "Gemini · gemini-3.8-flash" || b.Looks != 1 {
		t.Fatalf("looks %d, breakdown %+v", len(m.bodies), b)
	}
	vp := videoPart(m.bodies[0])
	fd := vp["fileData"].(map[string]any)
	if fd["fileUri"] != testURL || fd["mimeType"] != "video/*" || vp["videoMetadata"] != nil {
		t.Fatalf("video part = %v", vp)
	}
	gc := m.bodies[0]["generationConfig"].(map[string]any)
	if gc["responseMimeType"] != "application/json" || gc["mediaResolution"] != nil {
		t.Fatalf("generationConfig = %v", gc)
	}
	sys := m.bodies[0]["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(sys, "WATCH and LISTEN") || m.keysHit[0] != "key-a" {
		t.Fatalf("system = %q key %q", sys, m.keysHit[0])
	}
	mu.Lock()
	defer mu.Unlock()
	if len(pcts) < 2 || pcts[len(pcts)-1] != 100 {
		t.Fatalf("progress = %v", pcts)
	}
}

func TestGoogleLongVideoInParts(t *testing.T) {
	scenes := func(from, to float64, n int) string {
		var out []string
		step := (to - from) / float64(n)
		for i := 0; i < n; i++ {
			out = append(out, fmt.Sprintf(`{"start":%g,"end":%g,"visual":"Сцена %g","speech":"line"}`, from+float64(i)*step, from+float64(i+1)*step, from+float64(i)*step))
		}
		return "[" + strings.Join(out, ",") + "]"
	}
	g, m := newGoogle(t, func(n int, body map[string]any, prompt string) (int, string) {
		switch {
		case strings.Contains(prompt, "long video"):
			return 200, `{"seen":true,"title":"Довге","durationSeconds":1500,"summary":"Лекція","scenes":[],"people":"mixed","audioMode":"voiceover"}`
		case strings.Contains(prompt, "from 10:00 to 20:00"):
			// Timed from the clip's start: must be shifted to the whole video.
			return 200, `{"seen":true,"scenes":` + scenes(0, 600, 3) + `}`
		case strings.Contains(prompt, "from 0:00 to 10:00"):
			return 200, `{"seen":true,"scenes":` + scenes(0, 600, 4) + `}`
		case strings.Contains(prompt, "from 20:00 to 25:00"):
			return 200, `{"seen":true,"scenes":` + scenes(1200, 1500, 2) + `}`
		}
		return 400, `{"error":{"message":"unexpected prompt"}}`
	})
	b, err := Analyze(context.Background(), g, Options{URL: testURL, Meta: Meta{Duration: 1500}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.bodies) != 4 || b.Looks != 4 {
		t.Fatalf("looks = %d", len(m.bodies))
	}
	over := m.bodies[0]
	if vm := videoPart(over)["videoMetadata"].(map[string]any); vm["fps"] != 0.2 {
		t.Fatalf("overview metadata = %v", vm)
	}
	if over["generationConfig"].(map[string]any)["mediaResolution"] != "MEDIA_RESOLUTION_LOW" {
		t.Fatal("a long overview must use the low resolution")
	}
	vm1 := videoPart(m.bodies[1])["videoMetadata"].(map[string]any)
	vm2 := videoPart(m.bodies[2])["videoMetadata"].(map[string]any)
	if vm1["startOffset"] != nil || vm1["endOffset"] != "600s" || vm2["startOffset"] != "600s" || vm2["endOffset"] != "1200s" {
		t.Fatalf("part offsets = %v / %v", vm1, vm2)
	}
	if len(b.Scenes) != 9 || b.Scenes[4].Start != 600 || b.Scenes[6].End != 1200 || b.Scenes[8].End != 1500 {
		var ts []string
		for _, s := range b.Scenes {
			ts = append(ts, fmt.Sprintf("%g-%g", s.Start, s.End))
		}
		t.Fatalf("merged scenes = %v", ts)
	}
	if b.Summary != "Лекція" || b.People != "mixed" || b.CutsPerMin == 0 {
		t.Fatalf("overview fields lost: %+v", b)
	}
}

func TestGoogleContinuesACutOffAnswer(t *testing.T) {
	g, m := newGoogle(t, func(n int, body map[string]any, prompt string) (int, string) {
		if n == 1 { // cut off after the second scene
			return 200, `{"seen":true,"durationSeconds":300,"summary":"Огляд","scenes":[{"start":0,"end":100,"visual":"A"},{"start":100,"end":200,"visual":"B"},{"start":200,"end":2`
		}
		if !strings.Contains(prompt, "from 3:20 to 5:00") {
			return 400, `{"error":{"message":"wrong part: ` + prompt[:60] + `"}}`
		}
		return 200, `{"seen":true,"scenes":[{"start":200,"end":300,"visual":"C"}]}`
	})
	b, err := Analyze(context.Background(), g, Options{URL: testURL})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.bodies) != 2 || len(b.Scenes) != 3 || b.Scenes[2].Visual != "C" || b.Duration != 300 {
		t.Fatalf("looks %d, scenes %+v", len(m.bodies), b.Scenes)
	}
}

func TestGoogleErrors(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		status int
		body   string
		want   Kind
	}{
		{"private", 400, `{"error":{"code":400,"message":"The YouTube video is private.","status":"INVALID_ARGUMENT"}}`, KindPrivate},
		{"gone", 400, `{"error":{"code":400,"message":"Cannot fetch content from the provided URL.","status":"INVALID_ARGUMENT"}}`, KindUnavailable},
		{"long", 400, `{"error":{"code":400,"message":"The input token count (1500000) exceeds the maximum number of tokens allowed (1048576).","status":"INVALID_ARGUMENT"}}`, KindTooLong},
		{"badkey", 400, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"reason":"API_KEY_INVALID"}]}}`, KindOther},
		{"quota", 429, `{"error":{"code":429,"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED"}}`, KindQuota},
	}
	for _, c := range cases {
		g, m := newGoogle(t, func(int, map[string]any, string) (int, string) { return c.status, c.body }, "key-a", "key-b")
		_, err := Analyze(ctx, g, Options{URL: testURL, Meta: Meta{Duration: 60}})
		if KindOf(err) != c.want {
			t.Errorf("%s: err = %v (%s)", c.name, err, KindOf(err))
		}
		if c.name == "quota" && len(m.keysHit) != 2 {
			t.Errorf("quota must try every key, tried %v", m.keysHit)
		}
		if c.name == "badkey" && !strings.Contains(err.Error(), "Ключ Google AI Studio не підходить") {
			t.Errorf("bad key message: %v", err)
		}
	}
	// A 503 is retried.
	g, m := newGoogle(t, func(n int, _ map[string]any, _ string) (int, string) {
		if n == 1 {
			return 503, `{"error":{"message":"The model is overloaded."}}`
		}
		return 200, shortReply
	})
	if _, err := Analyze(ctx, g, Options{URL: testURL}); err != nil || len(m.bodies) != 2 {
		t.Fatalf("503 retry: %v (%d calls)", err, len(m.bodies))
	}
	// No keys: say what to add.
	if _, err := (&Google{Pool: gemini.NewPool(nil), Model: "m"}).Watch(ctx, Request{URL: testURL}); KindOf(err) != KindNoService {
		t.Fatalf("no keys: %v", err)
	}
	// The model could not watch.
	g, _ = newGoogle(t, func(int, map[string]any, string) (int, string) { return 200, `{"seen": false}` })
	if _, err := Analyze(ctx, g, Options{URL: testURL}); KindOf(err) != KindNotSeen {
		t.Fatalf("seen=false: %v", err)
	}
	// Too long before any look.
	if _, err := Analyze(ctx, g, Options{URL: testURL, Meta: Meta{Duration: MaxDuration + 60}}); KindOf(err) != KindTooLong {
		t.Fatalf("too long: %v", err)
	}
}

// ---- an OpenAI-compatible gateway ---------------------------------------------------------------

type textMock struct {
	mu     sync.Mutex
	shapes []string
	answer func(shape string) (int, string)
}

func (m *textMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer txt" || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		w.WriteHeader(401)
		return
	}
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	shape := ""
	if len(body.Messages) == 1 && len(body.Messages[0].Content) == 2 {
		shape, _ = body.Messages[0].Content[1]["type"].(string)
		if !strings.Contains(fmt.Sprint(body.Messages[0].Content[1]), testURL) {
			shape = "no-url"
		}
	}
	m.mu.Lock()
	m.shapes = append(m.shapes, shape)
	m.mu.Unlock()
	status, text := m.answer(shape)
	w.WriteHeader(status)
	if status != 200 {
		io.WriteString(w, text)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": text}}}})
}

func newText(t *testing.T, answer func(shape string) (int, string)) (*Text, *textMock) {
	t.Helper()
	m := &textMock{answer: answer}
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	allow(srv)
	return &Text{BaseURL: srv.URL + "/v1", Key: "txt", Model: "gemini-3.8-flash-high", Truth: Meta{Duration: 95, Title: "Як зробити хук"}}, m
}

func TestTextGatewayFindsTheShape(t *testing.T) {
	tw, m := newText(t, func(shape string) (int, string) {
		switch shape {
		case "image_url":
			return 400, `{"error":{"message":"invalid image url"}}`
		case "file":
			return 200, shortReply
		}
		return 500, "?"
	})
	ctx := context.Background()
	b, err := Analyze(ctx, tw, Options{URL: testURL, Meta: tw.Truth})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Scenes) != 3 || strings.Join(m.shapes, ",") != "image_url,file" {
		t.Fatalf("shapes %v, breakdown %+v", m.shapes, b)
	}
	if can, known := TextCan(tw.BaseURL, tw.Model); !can || !known {
		t.Fatal("the working shape must be remembered")
	}
	// The next look goes straight to the shape that works.
	if _, err := tw.Watch(ctx, Request{URL: testURL, Prompt: "x"}); err != nil || m.shapes[len(m.shapes)-1] != "file" || len(m.shapes) != 3 {
		t.Fatalf("cached shape: %v %v", err, m.shapes)
	}
}

func TestTextGatewayThatOnlyPretends(t *testing.T) {
	// It answers every shape, but with a length the video does not have
	// (it did not watch) — or refuses in words.
	tw, m := newText(t, func(shape string) (int, string) {
		if shape == "video_url" {
			return 200, "I'm sorry, but I can't access external links such as YouTube videos."
		}
		return 200, strings.Replace(shortReply, `"1:35"`, `600`, 1)
	})
	tw.Model = "pretender"
	ctx := context.Background()
	if _, err := Analyze(ctx, tw, Options{URL: testURL, Meta: tw.Truth}); err != errCannot {
		t.Fatalf("err = %v", err)
	}
	if len(m.shapes) != 3 {
		t.Fatalf("shapes tried = %v", m.shapes)
	}
	if can, known := TextCan(tw.BaseURL, tw.Model); can || !known {
		t.Fatal("a gateway that cannot watch must be remembered")
	}
	if _, err := tw.Watch(ctx, Request{URL: testURL}); err != errCannot || len(m.shapes) != 3 {
		t.Fatalf("remembered cannot: %v, %d calls", err, len(m.shapes))
	}
}

func TestVerifyReply(t *testing.T) {
	ok := func(reply string, truth Meta) bool { v, _ := verifyReply(reply, truth); return v }
	if !ok(`{"seen":true,"durationSeconds":100}`, Meta{Duration: 95}) || ok(`{"durationSeconds":140}`, Meta{Duration: 95}) {
		t.Fatal("length check")
	}
	if !ok(`{"title":"How to make a HOOK!"}`, Meta{Title: "How to make a hook"}) || ok(`{"title":"Cooking pasta"}`, Meta{Title: "How to make a hook"}) {
		t.Fatal("title check")
	}
	if ok(`{"seen":false,"durationSeconds":95}`, Meta{Duration: 95}) || ok(`{"seen":true}`, Meta{}) || !ok(`{"durationSeconds":"1:35"}`, Meta{}) {
		t.Fatal("seen / unknown truth")
	}
}

// ---- the watch page ----------------------------------------------------------------------------

func TestProbe(t *testing.T) {
	pages := map[string]string{
		"okokokokoko": `<meta name="title" content="Hook &amp; more"><script>var ytInitialPlayerResponse = {"playabilityStatus":{"status":"OK","playableInEmbed":true},"videoDetails":{"videoId":"okokokokoko","title":"Hook","lengthSeconds":"95","author":"Chan","isLive":false}};</script>`,
		"unlistedvid": `{"playabilityStatus":{"status":"OK"},"videoDetails":{"lengthSeconds":"60"},"microformat":{"playerMicroformatRenderer":{"isUnlisted":true}}}`,
		"privatevide": `{"playabilityStatus":{"status":"LOGIN_REQUIRED","messages":["x"],"reason":"This video is private"}}`,
		"agerestrict": `{"playabilityStatus":{"status":"LOGIN_REQUIRED","reason":"Sign in to confirm your age"}}`,
		"removedvide": `{"playabilityStatus":{"status":"ERROR","reason":"Video unavailable"}}`,
		"livestreamx": `{"playabilityStatus":{"status":"OK"},"videoDetails":{"lengthSeconds":"0","isLive":true}}`,
		"waytoolongx": `{"playabilityStatus":{"status":"OK"},"videoDetails":{"lengthSeconds":"9000"}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oembed":
			id := YouTubeID(r.URL.Query().Get("url"))
			switch id {
			case "missingvide":
				w.WriteHeader(404)
			case "okokokokoko":
				io.WriteString(w, `{"title":"Як зробити хук","author_name":"Канал","width":113,"height":200}`)
			default:
				w.WriteHeader(401)
			}
		case "/watch":
			if p, ok := pages[r.URL.Query().Get("v")]; ok {
				io.WriteString(w, p)
				return
			}
			w.WriteHeader(404)
		case "/vi/okokokokoko/maxresdefault.jpg":
			w.WriteHeader(404)
		case "/vi/okokokokoko/hqdefault.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 2000)...))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	allow(srv)
	oo, ow, ot := oembedURL, watchPageURL, thumbBase
	defer func() { oembedURL, watchPageURL, thumbBase = oo, ow, ot }()
	oembedURL, watchPageURL, thumbBase = srv.URL+"/oembed", srv.URL+"/watch", srv.URL+"/vi/"

	ctx := context.Background()
	m, err := Probe(ctx, nil, "okokokokoko")
	if err != nil || m.Title != "Як зробити хук" || m.Channel != "Канал" || m.Duration != 95 || !m.Vertical || m.Status != "OK" {
		t.Fatalf("ok video: %+v %v", m, err)
	}
	want := map[string]Kind{"unlistedvid": KindUnlisted, "privatevide": KindPrivate, "agerestrict": KindAge,
		"removedvide": KindUnavailable, "livestreamx": KindLive, "waytoolongx": KindTooLong, "missingvide": KindUnavailable}
	for id, k := range want {
		if _, err := Probe(ctx, nil, id); KindOf(err) != k {
			t.Errorf("%s: %v (%s), want %s", id, err, KindOf(err), k)
		}
	}
	// Unknown everything (the page cannot be read): no error, the model decides.
	if m, err := Probe(ctx, nil, "nopagenopag"); err != nil || m.Duration != 0 {
		t.Fatalf("unknown video: %+v %v", m, err)
	}
	dst := filepath.Join(t.TempDir(), "ref", "thumb.jpg")
	if err := FetchThumb(ctx, nil, "okokokokoko", dst); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(dst); err != nil || st.Size() < 1000 {
		t.Fatalf("thumbnail: %v", err)
	}
}

// TestLiveProbe looks at a real public video (opt-in, no key involved):
//
//	REFERENCE_LIVE=1 go test ./internal/reference -run TestLiveProbe -v
func TestLiveProbe(t *testing.T) {
	if os.Getenv("REFERENCE_LIVE") == "" {
		t.Skip("REFERENCE_LIVE=1 probes YouTube itself")
	}
	hc := &http.Client{Transport: http.DefaultTransport.(guard).base}
	m, err := Probe(context.Background(), hc, "jNQXAC9IVRw") // «Me at the zoo», 19 s
	t.Logf("meta = %+v, err = %v", m, err)
	if err != nil || m.Duration < 15 || m.Duration > 25 || m.Title == "" || m.Status != "OK" {
		t.Fatalf("live probe: %+v %v", m, err)
	}
	dst := filepath.Join(t.TempDir(), "thumb.jpg")
	if err := FetchThumb(context.Background(), hc, "jNQXAC9IVRw", dst); err != nil {
		t.Fatal(err)
	}
	if _, err := Probe(context.Background(), hc, "zzzzzzzzzz0"); KindOf(err) != KindUnavailable {
		t.Fatalf("a video that does not exist: %v", err)
	}
}

// TestGoogleRetriesWithoutClipOptions: an API that refuses videoMetadata gets
// the request once more without it.
func TestGoogleRetriesWithoutClipOptions(t *testing.T) {
	g, m := newGoogle(t, func(n int, body map[string]any, prompt string) (int, string) {
		if videoPart(body)["videoMetadata"] != nil {
			return 400, `{"error":{"code":400,"message":"Invalid value at 'contents[0].parts[0].video_metadata.fps' (TYPE_INT32), 0.2","status":"INVALID_ARGUMENT"}}`
		}
		return 200, `{"seen":true,"durationSeconds":95,"scenes":[{"start":0,"end":95,"visual":"Усе відео"}]}`
	})
	out, err := g.Watch(context.Background(), Request{URL: testURL, Prompt: "x", FPS: 0.2})
	if err != nil || !strings.Contains(out, "Усе відео") || len(m.bodies) != 2 {
		t.Fatalf("retry without clip: %q %v (%d calls)", out, err, len(m.bodies))
	}
	// A refusal about the video itself is not retried.
	g, m = newGoogle(t, func(int, map[string]any, string) (int, string) {
		return 400, `{"error":{"code":400,"message":"The YouTube video is private.","status":"INVALID_ARGUMENT"}}`
	})
	if _, err := g.Watch(context.Background(), Request{URL: testURL, FPS: 0.2}); KindOf(err) != KindPrivate || len(m.bodies) != 1 {
		t.Fatalf("private: %v (%d calls)", err, len(m.bodies))
	}
}
