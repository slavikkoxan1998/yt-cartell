package tts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
)

// setup points every endpoint at srv and shrinks the retry backoff.
func setup(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	eb, ob, rb := elevenBase, openaiBase, retryBase
	elevenBase, openaiBase, retryBase = srv.URL, srv.URL+"/v1", time.Millisecond
	t.Cleanup(func() {
		srv.Close()
		elevenBase, openaiBase, retryBase = eb, ob, rb
	})
	return srv
}

// cfgClient returns a client whose settings the test can change between calls.
func cfgClient(t config.TTS) (*Client, *config.Config) {
	cfg := &config.Config{TTS: t}
	return New(func() config.Config { return *cfg }), cfg
}

func TestElevenRetryThenSuccess(t *testing.T) {
	var calls atomic.Int32
	var body map[string]any
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.URL.Path != "/v1/text-to-speech/voice-1" || r.URL.Query().Get("output_format") != "mp3_44100_128" {
			t.Errorf("url = %s", r.URL)
		}
		if r.Header.Get("xi-api-key") != "el-key" || r.Method != http.MethodPost {
			t.Errorf("method/key = %s %q", r.Method, r.Header.Get("xi-api-key"))
		}
		b, _ := io.ReadAll(r.Body)
		body = nil
		_ = json.Unmarshal(b, &body)
		switch n {
		case 1, 2:
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"detail":{"status":"too_many_concurrent_requests"}}`))
		case 3:
			w.WriteHeader(503)
		default:
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("ID3mp3"))
		}
	})
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "el-key", Voice: "voice-1"})
	out, err := c.Synthesize(context.Background(), "  Привіт  ", "")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "ID3mp3" || calls.Load() != 4 {
		t.Fatalf("out %q after %d calls", out, calls.Load())
	}
	if body["text"] != "Привіт" || body["model_id"] != "eleven_multilingual_v2" {
		t.Errorf("body = %v", body)
	}
	vs, _ := body["voice_settings"].(map[string]any)
	if vs["stability"] != 0.5 || vs["similarity_boost"] != 0.75 || vs["use_speaker_boost"] != true {
		t.Errorf("voice_settings = %v", vs)
	}
}

func TestElevenRetryExhausted(t *testing.T) {
	var calls atomic.Int32
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
		_, _ = w.Write([]byte("upstream down"))
	})
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k"})
	_, err := c.Synthesize(context.Background(), "hi", "v")
	if err == nil || err.Error() != "ElevenLabs 500: upstream down" {
		t.Fatalf("err = %v", err)
	}
	if n := calls.Load(); n != int32(maxRetries+1) {
		t.Errorf("calls = %d, want %d", n, maxRetries+1)
	}
}

func TestElevenErrorsNotRetried(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{429, `{"detail":{"status":"quota_exceeded"}}`, "Ключ ElevenLabs вичерпав ліміт до "}, // the only key rests until the 1st
		{402, `{"detail":{"type":"payment_required","code":"insufficient_credits","message":"Not enough credits"}}`, "Ключ ElevenLabs вичерпав ліміт до "},
		{402, `{"detail":{"message":"Free users cannot use library voices via the API."}}`, "ElevenLabs: це голос із бібліотеки"},
		{402, `{"detail":{"type":"payment_required","code":"paid_plan_required","message":"Upgrade to use this voice."}}`, "ElevenLabs: це голос із бібліотеки"},
		{404, `{"detail":{"type":"not_found","code":"model_not_found","message":"no such model"}}`, "ElevenLabs: модель не знайдено"},
		{401, `{"detail":"invalid key"}`, "ElevenLabs: невірний API-ключ"},
		{400, `bad text`, "ElevenLabs 400: bad text"},
	}
	for _, tc := range cases {
		var calls atomic.Int32
		setup(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k"})
		_, err := c.Synthesize(context.Background(), "hi", "v")
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%d → %v, want %q", tc.status, err, tc.want)
		}
		if calls.Load() != 1 {
			t.Errorf("%d retried %d times", tc.status, calls.Load())
		}
	}
}

func TestElevenValidation(t *testing.T) {
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs"})
	if _, err := c.Synthesize(context.Background(), "hi", "v"); err == nil || !strings.Contains(err.Error(), "Ключ ElevenLabs не задано") {
		t.Errorf("no key → %v", err)
	}
	c, _ = cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k"})
	if _, err := c.Synthesize(context.Background(), "hi", ""); err == nil || err.Error() != "Не вибрано голос для озвучки" {
		t.Errorf("no voice → %v", err)
	}
	if _, err := c.Synthesize(context.Background(), "  ", "v"); err == nil {
		t.Error("empty text accepted")
	}
}

func TestElevenCancelDuringBackoff(t *testing.T) {
	setup(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) })
	retryBase = time.Hour
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k"})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Synthesize(ctx, "hi", "v"); err != context.DeadlineExceeded {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAICompatible(t *testing.T) {
	var body map[string]any
	var auth, path string
	srv := setup(t, func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("mp3"))
	})
	// default base (api.openai.com → mock via openaiBase)
	c, cfg := cfgClient(config.TTS{Provider: "openai", APIKey: "sk"})
	out, err := c.Synthesize(context.Background(), "hello", "")
	if err != nil || string(out) != "mp3" {
		t.Fatalf("out %q err %v", out, err)
	}
	if path != "/v1/audio/speech" || auth != "Bearer sk" {
		t.Errorf("path %q auth %q", path, auth)
	}
	if body["model"] != "gpt-4o-mini-tts" || body["voice"] != "alloy" || body["input"] != "hello" || body["response_format"] != "mp3" {
		t.Errorf("body = %v", body)
	}
	// custom base + model + voice
	cfg.TTS = config.TTS{Provider: "openai", APIKey: "sk", BaseURL: srv.URL + "/gw/", Model: "gpt-4o-mini-tts", Voice: "nova"}
	if _, err := c.Synthesize(context.Background(), "x", ""); err != nil {
		t.Fatal(err)
	}
	if path != "/gw/audio/speech" || body["model"] != "gpt-4o-mini-tts" || body["voice"] != "nova" {
		t.Errorf("path %q body %v", path, body)
	}
	cfg.TTS.APIKey = ""
	if _, err := c.Synthesize(context.Background(), "x", ""); err == nil || !strings.Contains(err.Error(), "Ключ OpenAI (або сумісний) не задано") {
		t.Errorf("no key → %v", err)
	}
}

func TestOpenAIJSONBodyRejected(t *testing.T) {
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	})
	c, _ := cfgClient(config.TTS{Provider: "openai", APIKey: "sk"})
	if _, err := c.Synthesize(context.Background(), "x", ""); err == nil || !strings.Contains(err.Error(), "замість аудіо") {
		t.Errorf("err = %v", err)
	}
}

func TestCustomProvider(t *testing.T) {
	mode := ""
	var body map[string]any
	var auth string
	srv := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/files/a.mp3" {
			_, _ = w.Write([]byte("linked-audio"))
			return
		}
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		switch mode {
		case "raw":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("raw-audio"))
		case "b64":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(`{"audioBase64":"data:audio/mpeg;base64,` + base64.StdEncoding.EncodeToString([]byte("b64-audio")) + `"}`))
		case "url":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"audioUrl":"/files/a.mp3"}`))
		case "none":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	})
	c, cfg := cfgClient(config.TTS{Provider: "custom", BaseURL: srv.URL + "/tts", Voice: "v9", Model: "m1"})
	for m, want := range map[string]string{"raw": "raw-audio", "b64": "b64-audio", "url": "linked-audio"} {
		mode = m
		out, err := c.Synthesize(context.Background(), "text", "")
		if err != nil || string(out) != want {
			t.Errorf("%s: out %q err %v", m, out, err)
		}
	}
	if body["text"] != "text" || body["voice"] != "v9" || body["model"] != "m1" || auth != "" {
		t.Errorf("body %v auth %q", body, auth)
	}
	mode = "none"
	if _, err := c.Synthesize(context.Background(), "t", ""); err == nil || !strings.Contains(err.Error(), "аудіо не знайдено") {
		t.Errorf("none → %v", err)
	}
	cfg.TTS.APIKey = "ck"
	mode = "raw"
	if _, err := c.Synthesize(context.Background(), "t", ""); err != nil || auth != "Bearer ck" {
		t.Errorf("auth %q err %v", auth, err)
	}
	cfg.TTS.BaseURL = ""
	if _, err := c.Synthesize(context.Background(), "t", ""); err == nil || !strings.Contains(err.Error(), "custom TTS") {
		t.Errorf("no url → %v", err)
	}
}

const voicesJSON = `{"voices":[
 {"voice_id":"a","name":"Zed - deep narrator","category":"premade","labels":{"gender":"male","age":"old","accent":"british"},"preview_url":"https://x/a.mp3"},
 {"voice_id":"b","name":"Anna","category":"professional","labels":{"gender":"female"}},
 {"voice_id":"c","name":"bob ","category":"cloned","labels":null,"preview_url":null},
 {"voice_id":"d","name":"","category":"premade"}
],"has_more":false}`

func TestVoicesParseSortCache(t *testing.T) {
	var vHits, sHits atomic.Int32
	subStatus := 200
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") == "" {
			t.Errorf("%s without key", r.URL.Path)
		}
		switch r.URL.Path {
		case "/v2/voices":
			vHits.Add(1)
			if r.URL.Query().Get("page_size") != "100" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(voicesJSON))
		case "/v1/user/subscription":
			sHits.Add(1)
			w.WriteHeader(subStatus)
			_, _ = w.Write([]byte(`{"tier":"free","character_count":2500,"character_limit":10000}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	c, cfg := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k1"})
	l, err := c.Voices(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if l.Provider != "elevenlabs" || l.Tier != "free" || l.CharsLeft != 7500 {
		t.Errorf("list = %+v", l)
	}
	var order []string
	for _, v := range l.Voices {
		order = append(order, v.ID+":"+v.Name)
	}
	if got := strings.Join(order, ","); got != "c:bob,d:d,a:Zed,b:Anna" {
		t.Errorf("order = %s", got)
	}
	zed := l.Voices[2]
	if zed.Gender != "male" || zed.Age != "old" || zed.Accent != "british" || zed.PreviewURL != "https://x/a.mp3" || !zed.Usable || zed.Category != "premade" {
		t.Errorf("zed = %+v", zed)
	}
	if l.Voices[3].Usable {
		t.Error("professional voice must be unusable on the free tier")
	}

	// cached: no new hits; mutating the result does not leak into the cache
	l.Voices[0].Name = "mutated"
	l2, _ := c.Voices(context.Background(), false)
	if vHits.Load() != 1 || l2.Voices[0].Name != "bob" {
		t.Errorf("cache miss: hits %d, name %q", vHits.Load(), l2.Voices[0].Name)
	}
	// force bypasses the cache
	if _, err := c.Voices(context.Background(), true); err != nil || vHits.Load() != 2 {
		t.Errorf("force: hits %d err %v", vHits.Load(), err)
	}
	// a different key is a different cache entry
	cfg.TTS.APIKey = "k2"
	if _, err := c.Voices(context.Background(), false); err != nil || vHits.Load() != 3 {
		t.Errorf("new key: hits %d err %v", vHits.Load(), err)
	}
	// subscription unavailable → the plan remembered from the last read stays
	subStatus = 500
	l3, err := c.Voices(context.Background(), true)
	if err != nil || l3.Tier != "free" || l3.CharsLeft != -1 || l3.Voices[len(l3.Voices)-1].Usable {
		t.Errorf("no subscription, remembered plan: %+v err %v", l3, err)
	}
	if sHits.Load() != 4 {
		t.Errorf("subscription hits = %d", sHits.Load())
	}
	// a key never read: unknown plan, everything usable
	c2, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k3"})
	l4, err := c2.Voices(context.Background(), true)
	if err != nil || l4.Tier != "" || !l4.Voices[len(l4.Voices)-1].Usable {
		t.Errorf("no subscription, unknown plan: %+v err %v", l4, err)
	}
}

func TestVoicesPagination(t *testing.T) {
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/voices" {
			_, _ = w.Write([]byte(`{"tier":"creator"}`))
			return
		}
		if r.URL.Query().Get("next_page_token") == "p2" {
			_, _ = w.Write([]byte(`{"voices":[{"voice_id":"y","name":"Yan","category":"professional"}],"has_more":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"voices":[{"voice_id":"x","name":"Xena","category":"premade"}],"has_more":true,"next_page_token":"p2"}`))
	})
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k"})
	l, err := c.Voices(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Voices) != 2 || l.Voices[0].ID != "x" || l.Voices[1].ID != "y" || !l.Voices[1].Usable || l.Tier != "creator" || l.CharsLeft != -1 {
		t.Errorf("list = %+v", l)
	}
}

func TestVoicesErrorsAndOtherProviders(t *testing.T) {
	var hits atomic.Int32
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"detail":"invalid_api_key"}`))
	})
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "bad"})
	if _, err := c.Voices(context.Background(), false); err == nil || err.Error() != "ElevenLabs: невірний API-ключ" {
		t.Errorf("401 → %v", err)
	}
	c, _ = cfgClient(config.TTS{Provider: "elevenlabs"})
	if _, err := c.Voices(context.Background(), false); err == nil || !strings.HasPrefix(err.Error(), "Ключ ElevenLabs не задано") {
		t.Errorf("no key → %v", err)
	}
	before := hits.Load()
	// An own server and an OpenAI-compatible gateway have no list; OpenAI's
	// built-in voices need no request.
	for _, tc := range []struct {
		cfg config.TTS
		n   int
	}{
		{config.TTS{Provider: "custom", APIKey: "k", BaseURL: "http://127.0.0.1:9/tts"}, 0},
		{config.TTS{Provider: "openai", APIKey: "k", BaseURL: "http://127.0.0.1:9/gw"}, 0},
		{config.TTS{Provider: "openai", APIKey: "k"}, 13},
	} {
		c, _ = cfgClient(tc.cfg)
		l, err := c.Voices(context.Background(), false)
		if err != nil || l.Provider != tc.cfg.Provider || l.Voices == nil || len(l.Voices) != tc.n || l.CharsLeft != -1 {
			t.Errorf("%+v → %+v %v", tc.cfg, l, err)
		}
	}
	if hits.Load() != before {
		t.Error("non-elevenlabs providers must not call ElevenLabs")
	}
}

func TestUnreachable(t *testing.T) {
	srv := setup(t, func(w http.ResponseWriter, r *http.Request) {})
	srv.Close()
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k"})
	if _, err := c.Synthesize(context.Background(), "hi", "v"); err == nil || !strings.Contains(err.Error(), "немає з'єднання") {
		t.Errorf("err = %v", err)
	}
}
