package tts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ytcartell/internal/config"
)

// modelsJSON mimics GET /v1/models: v2 lists ISO 639-1 ids, v3 ISO 639-3 ids
// and names (both occur in the wild), v4 adds Amharic, an STS model cannot do TTS.
const modelsJSON = `[
 {"model_id":"eleven_multilingual_v2","can_do_text_to_speech":true,
  "languages":[{"language_id":"en","name":"English"},{"language_id":"uk","name":"Ukrainian"},{"language_id":"de","name":"German"}]},
 {"model_id":"eleven_v3","can_do_text_to_speech":true,
  "languages":[{"language_id":"eng","name":"English"},{"language_id":"ukr","name":"Ukrainian"},{"language_id":"kat","name":"Georgian"},
               {"language_id":"x-arm","name":"Armenian"},{"language_id":"hun","name":"Hungarian"}]},
 {"model_id":"eleven_v4","can_do_text_to_speech":true,"languages":[{"language_id":"amh","name":"Amharic"},{"language_id":"en","name":"English"}]},
 {"model_id":"eleven_flash_v2_5","can_do_text_to_speech":true,
  "languages":[{"language_id":"en","name":"English"},{"language_id":"hu","name":"Hungarian"},{"language_id":"vi","name":"Vietnamese"}]},
 {"model_id":"eleven_english_sts_v2","can_do_text_to_speech":false,"languages":[{"language_id":"yo","name":"Yoruba"}]}
]`

// modelsServer serves /v1/models (body chosen per call) and records TTS bodies.
type modelsServer struct {
	mu        sync.Mutex
	models    string // "" → 500
	modelHits atomic.Int32
	bodies    []map[string]any
	reply     func(body map[string]any) (int, string) // TTS reply; nil = 200 audio
}

func (m *modelsServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/models":
			m.modelHits.Add(1)
			if r.Header.Get("xi-api-key") == "" {
				t.Errorf("/v1/models without key")
			}
			m.mu.Lock()
			body := m.models
			m.mu.Unlock()
			if body == "" {
				w.WriteHeader(500)
				return
			}
			_, _ = w.Write([]byte(body))
		case strings.HasPrefix(r.URL.Path, "/v1/text-to-speech/"):
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(b, &body)
			m.mu.Lock()
			m.bodies = append(m.bodies, body)
			reply := m.reply
			m.mu.Unlock()
			if reply != nil {
				if code, msg := reply(body); code != 200 {
					w.WriteHeader(code)
					_, _ = w.Write([]byte(msg))
					return
				}
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("mp3"))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}
}

func (m *modelsServer) last() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.bodies) == 0 {
		return nil
	}
	return m.bodies[len(m.bodies)-1]
}

func TestModelForAutomaticChoice(t *testing.T) {
	ms := &modelsServer{models: modelsJSON}
	setup(t, ms.handler(t))
	c, cfg := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k1"})
	ctx := context.Background()

	// Unknown language: multilingual v2 detects it itself, no request needed.
	if m, lc := c.ModelFor(ctx, ""); m != "eleven_multilingual_v2" || lc != "" || ms.modelHits.Load() != 0 {
		t.Fatalf(`"" → %q %q (hits %d)`, m, lc, ms.modelHits.Load())
	}
	cases := []struct{ lang, model, code string }{
		{"uk", "eleven_multilingual_v2", ""}, // v2 speaks it; v2 does not take language_code
		{"UK", "eleven_multilingual_v2", ""},
		{"ka", "eleven_v3", "ka"},         // only v3 (ISO 639-3 "kat" in the list)
		{"hy", "eleven_v3", "hy"},         // matched by the name "Armenian"
		{"am", "eleven_v4", "am"},         // only the newer v4 lists Amharic
		{"vi", "eleven_flash_v2_5", "vi"}, // only Flash v2.5 here
		{"yo", "eleven_v3", ""},           // nobody (STS cannot do TTS): v3 detects it
		{"pt-BR", "eleven_v3", ""},        // not listed anywhere → v3 without a hint
		{"Ukrainian", "eleven_multilingual_v2", ""},
	}
	for _, tc := range cases {
		if m, lc := c.ModelFor(ctx, tc.lang); m != tc.model || lc != tc.code {
			t.Errorf("%q → %q %q, want %q %q", tc.lang, m, lc, tc.model, tc.code)
		}
	}
	if n := ms.modelHits.Load(); n != 1 {
		t.Fatalf("/v1/models fetched %d times, want 1 (cached)", n)
	}
	// A different key is a different account: fetched again.
	cfg.TTS.APIKey = "k2"
	c.ModelFor(ctx, "ka")
	if n := ms.modelHits.Load(); n != 2 {
		t.Fatalf("new key: %d fetches", n)
	}
	// The cache expires after modelsTTL.
	c.mu.Lock()
	c.models.at = c.models.at.Add(-2 * modelsTTL)
	c.mu.Unlock()
	c.ModelFor(ctx, "ka")
	if n := ms.modelHits.Load(); n != 3 {
		t.Fatalf("expired cache: %d fetches", n)
	}
}

func TestModelForWithoutV3PrefersFlash(t *testing.T) {
	ms := &modelsServer{models: `[
	 {"model_id":"eleven_multilingual_v2","languages":[{"language_id":"en"}]},
	 {"model_id":"eleven_turbo_v2_5","languages":[{"language_id":"hu"}]},
	 {"model_id":"eleven_flash_v2_5","languages":[{"language_id":"hu"}]}]`}
	setup(t, ms.handler(t))
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k"})
	if m, lc := c.ModelFor(context.Background(), "hu"); m != "eleven_flash_v2_5" || lc != "hu" {
		t.Fatalf("hu → %q %q", m, lc)
	}
	// Not listed at all and no v3 on the account: back to the default model.
	if m, lc := c.ModelFor(context.Background(), "ka"); m != "eleven_multilingual_v2" || lc != "" {
		t.Fatalf("ka → %q %q", m, lc)
	}
}

func TestModelForFallsBackToDocumentedTables(t *testing.T) {
	ms := &modelsServer{} // /v1/models → 500
	setup(t, ms.handler(t))
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k"})
	ctx := context.Background()
	for lang, want := range map[string][2]string{
		"uk": {"eleven_multilingual_v2", ""},
		"ka": {"eleven_v3", "ka"},
		"hu": {"eleven_v3", "hu"}, // v3 before Flash v2.5
		"am": {"eleven_v3", ""},   // v4 is used only when the account lists it
		"xx": {"eleven_v3", ""},
	} {
		if m, lc := c.ModelFor(ctx, lang); m != want[0] || lc != want[1] {
			t.Errorf("%s → %q %q, want %v", lang, m, lc, want)
		}
	}
	if n := ms.modelHits.Load(); n != 1 {
		t.Fatalf("a failed listing is remembered for a while: %d fetches", n)
	}
	// No key: nothing is fetched at all.
	c2, _ := cfgClient(config.TTS{Provider: "elevenlabs"})
	if m, lc := c2.ModelFor(ctx, "ka"); m != "eleven_v3" || lc != "ka" || ms.modelHits.Load() != 1 {
		t.Fatalf("no key: %q %q", m, lc)
	}
}

func TestModelForSettingsModelAndOtherProviders(t *testing.T) {
	ms := &modelsServer{models: modelsJSON}
	setup(t, ms.handler(t))
	ctx := context.Background()
	c, cfg := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k", Model: "eleven_v3"})
	if m, lc := c.ModelFor(ctx, "uk"); m != "eleven_v3" || lc != "uk" {
		t.Fatalf("pinned v3 → %q %q", m, lc)
	}
	if m, lc := c.ModelFor(ctx, "de"); m != "eleven_v3" || lc != "" { // this account's v3 does not list German
		t.Fatalf("pinned v3, unlisted language → %q %q", m, lc)
	}
	cfg.TTS.Model = "eleven_multilingual_v2"
	if m, lc := c.ModelFor(ctx, "uk"); m != "eleven_multilingual_v2" || lc != "" {
		t.Fatalf("pinned v2 → %q %q", m, lc)
	}
	cfg.TTS = config.TTS{Provider: "openai", APIKey: "sk"}
	if m, lc := c.ModelFor(ctx, "uk"); m != "gpt-4o-mini-tts" || lc != "" {
		t.Fatalf("openai → %q %q", m, lc)
	}
	cfg.TTS.BaseURL = "http://gateway.local/v1" // a compatible gateway: the id every gateway knows
	if m, _ := c.ModelFor(ctx, "uk"); m != "tts-1" {
		t.Fatalf("openai-compatible → %q", m)
	}
	cfg.TTS = config.TTS{Provider: "custom", BaseURL: "http://x", Model: "m1"}
	if m, lc := c.ModelFor(ctx, "uk"); m != "m1" || lc != "" {
		t.Fatalf("custom → %q %q", m, lc)
	}
}

func TestSynthesizeWithLanguageAndVoiceSettings(t *testing.T) {
	ms := &modelsServer{models: modelsJSON}
	setup(t, ms.handler(t))
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k", Voice: "v1"})
	ctx := context.Background()

	if _, err := c.SynthesizeWith(ctx, "გამარჯობა", "", SynthOptions{LanguageCode: "ka", Stability: 0.62, Similarity: 0.9, Speed: 2}); err != nil {
		t.Fatal(err)
	}
	b := ms.last()
	vs, _ := b["voice_settings"].(map[string]any)
	if b["model_id"] != "eleven_v3" || b["language_code"] != "ka" {
		t.Fatalf("body %v", b)
	}
	if _, sped := vs["speed"]; vs["stability"] != 0.5 || vs["similarity_boost"] != 0.9 || sped {
		t.Fatalf("v3 voice settings %v (stability snaps to 0/0.5/1; v3 takes no speed)", vs)
	}

	if _, err := c.SynthesizeWith(ctx, "Привіт", "", SynthOptions{LanguageCode: "uk", Speed: 0.5}); err != nil {
		t.Fatal(err)
	}
	b = ms.last()
	vs, _ = b["voice_settings"].(map[string]any)
	if _, has := b["language_code"]; has || b["model_id"] != "eleven_multilingual_v2" || vs["speed"] != 0.7 || vs["stability"] != 0.5 {
		t.Fatalf("v2 body %v", b)
	}

	// An explicit model wins; language_code only where that model takes it.
	if _, err := c.SynthesizeWith(ctx, "hello", "", SynthOptions{Model: "eleven_flash_v2_5", LanguageCode: "vi"}); err != nil {
		t.Fatal(err)
	}
	if b = ms.last(); b["model_id"] != "eleven_flash_v2_5" || b["language_code"] != "vi" {
		t.Fatalf("explicit model body %v", b)
	}
	if _, err := c.SynthesizeWith(ctx, "hello", "", SynthOptions{Model: "eleven_multilingual_v2", LanguageCode: "uk"}); err != nil {
		t.Fatal(err)
	}
	if _, has := ms.last()["language_code"]; has {
		t.Fatal("multilingual v2 must not get language_code")
	}
}

func TestSynthesizeRetriesWithoutRefusedLanguage(t *testing.T) {
	ms := &modelsServer{models: modelsJSON}
	ms.reply = func(body map[string]any) (int, string) {
		if _, has := body["language_code"]; has {
			return 400, `{"detail":{"status":"unsupported_language","message":"language_code 'ka' is not supported"}}`
		}
		return 200, ""
	}
	setup(t, ms.handler(t))
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k", Voice: "v1"})
	out, err := c.SynthesizeWith(context.Background(), "გამარჯობა", "", SynthOptions{LanguageCode: "ka"})
	if err != nil || string(out) != "mp3" {
		t.Fatalf("out %q err %v", out, err)
	}
	ms.mu.Lock()
	n := len(ms.bodies)
	ms.mu.Unlock()
	if n != 2 {
		t.Fatalf("%d TTS calls, want 2 (hinted, then plain)", n)
	}
	if b := ms.last(); b["model_id"] != "eleven_v3" {
		t.Fatalf("retry body %v", b)
	}
	// Other 400s are not retried.
	ms.mu.Lock()
	ms.bodies = nil
	ms.reply = func(map[string]any) (int, string) { return 400, "bad text" }
	ms.mu.Unlock()
	if _, err := c.SynthesizeWith(context.Background(), "x", "", SynthOptions{LanguageCode: "ka"}); err == nil || err.Error() != "ElevenLabs 400: bad text" {
		t.Fatalf("err %v", err)
	}
}

func TestSynthesizeWithOtherProviders(t *testing.T) {
	var body map[string]any
	srv := setup(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = nil
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("mp3"))
	})
	c, cfg := cfgClient(config.TTS{Provider: "openai", APIKey: "sk"})
	if _, err := c.SynthesizeWith(context.Background(), "hi", "", SynthOptions{Model: "gpt-4o-mini-tts", Speed: 1.25, LanguageCode: "uk"}); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "gpt-4o-mini-tts" || body["speed"] != 1.25 || body["language"] != nil {
		t.Fatalf("openai body %v", body)
	}
	cfg.TTS = config.TTS{Provider: "custom", BaseURL: srv.URL + "/tts"}
	if _, err := c.SynthesizeWith(context.Background(), "hi", "v", SynthOptions{LanguageCode: "UKR", Speed: 0.9}); err != nil {
		t.Fatal(err)
	}
	if body["language"] != "uk" || body["speed"] != 0.9 || body["voice"] != "v" {
		t.Fatalf("custom body %v", body)
	}
	if _, err := c.Synthesize(context.Background(), "hi", "v"); err != nil {
		t.Fatal(err)
	}
	if _, has := body["language"]; has {
		t.Fatalf("plain Synthesize sends no language: %v", body)
	}
}

func TestNormalizeLanguage(t *testing.T) {
	for in, want := range map[string]string{
		"uk": "uk", "UKR": "uk", "Ukrainian": "uk", " en ": "en", "eng": "en", "pt-BR": "pt", "zh_TW": "zh",
		"cmn": "zh", "iw": "he", "nob": "no", "tl": "fil", "Portuguese, Brazil": "pt", "Fula/Pulaar": "ff",
		"Kurdish (Sorani)": "ckb", "Mandarin Chinese": "zh", "yue": "yue", "fil": "fil", "": "", "xx": "", "Klingon": "",
	} {
		if got := NormalizeLanguage(in); got != want {
			t.Errorf("NormalizeLanguage(%q) = %q, want %q", in, got, want)
		}
	}
	// Every documented model language is a canonical code.
	for model, codes := range documented {
		for _, code := range codes {
			if NormalizeLanguage(code) != code {
				t.Errorf("%s: %q is not canonical", model, code)
			}
		}
	}
	if len(documented["eleven_v3"]) != 74 {
		t.Errorf("Eleven v3 documents 74 languages, table has %d", len(documented["eleven_v3"]))
	}
	if snapV3Stability(0.1) != 0 || snapV3Stability(0.3) != 0.5 || snapV3Stability(0.8) != 1 {
		t.Error("v3 stability snapping")
	}
}
