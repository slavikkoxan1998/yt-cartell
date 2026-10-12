package tts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ytcartell/internal/config"
)

// A key without one permission is valid: the check says so, a voice list
// moves on, and only a key that cannot voice at all rests.
func TestElevenPermissions(t *testing.T) {
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		perm := "user_read"
		if r.URL.Path == "/v1/text-to-speech/v1" {
			perm = "text_to_speech"
		}
		_, _ = w.Write([]byte(`{"detail":{"type":"authentication_error","code":"missing_permissions","status":"missing_permissions","message":"The API key you used is missing the permission ` + perm + ` to execute this operation."}}`))
	})
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k-perm-0000000001"})
	ok, msg := c.Test(context.Background())
	if !ok || !strings.Contains(msg, "бракує дозволу «user_read»") {
		t.Fatalf("check: %v %s", ok, msg)
	}
	if st := c.KeyStatus()[0]; st.State != KeyOK {
		t.Fatalf("a valid key was blamed: %+v", st)
	}
	_, err := c.Synthesize(context.Background(), "hi", "v1")
	if err == nil || !strings.Contains(err.Error(), "«text_to_speech»") {
		t.Fatalf("synthesis: %v", err)
	}
	if st := c.KeyStatus()[0]; st.State != KeyError {
		t.Fatalf("a key that cannot voice keeps working: %+v", st)
	}
}

// Usage-based billing: max_credit_limit_extension replaces the deprecated flag.
func TestElevenSubscriptionExtension(t *testing.T) {
	n, lim := 120, 100
	zero, more := 0, 5000
	if _, _, spent := (elSub{CharacterCount: &n, CharacterLimit: &lim, CanExtend: true, MaxExtension: &more}).usage(); *spent {
		t.Fatal("an extendable plan counts as spent")
	}
	if _, _, spent := (elSub{CharacterCount: &n, CharacterLimit: &lim, CanExtend: true, MaxExtension: &zero, AllowedExtend: true}).usage(); !*spent {
		t.Fatal("max_credit_limit_extension 0 must win over the deprecated flag")
	}
	if _, _, spent := (elSub{CharacterCount: &n, CharacterLimit: &lim, CanExtend: true, AllowedExtend: true}).usage(); *spent {
		t.Fatal("older replies: the deprecated flag still counts")
	}
}

// The retired Turbo v2.5 is sent as Flash v2.5; default voices carry the
// retirement date.
func TestElevenTurboAndDefaultVoices(t *testing.T) {
	ms := &modelsServer{models: modelsJSON}
	setup(t, ms.handler(t))
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "k", Voice: "v1", Model: "eleven_turbo_v2_5"})
	if _, err := c.Synthesize(context.Background(), "Привіт", ""); err != nil {
		t.Fatal(err)
	}
	if got := ms.last()["model_id"]; got != "eleven_flash_v2_5" {
		t.Fatalf("model = %v", got)
	}
	voices := elevenVoiceList([]elVoice{{VoiceID: "p", Name: "Alice", Category: "premade"}, {VoiceID: "o", Name: "Me", Category: "cloned"}}, false)
	for _, v := range voices {
		if (v.ID == "p") != (v.Note != "") {
			t.Fatalf("note on %+v", v)
		}
	}
	if p, _ := ProviderByID("openai"); p.ModelNote("tts-1") == "" || p.ModelNote("gpt-4o-mini-tts-2025-12-15") == "" || p.ModelNote("gpt-4o-mini-tts") != "" {
		t.Fatal("OpenAI model notes")
	}
}

// A rejected key that looks like another provider's says so.
func TestWrongProviderKeyHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error_code":null,"title":"Unauthorized","message":"Invalid API key"}`))
	}))
	t.Cleanup(srv.Close)
	cb := cartesiaBase
	cartesiaBase = srv.URL
	t.Cleanup(func() { cartesiaBase = cb })
	elKey := "sk_" + strings.Repeat("ab12", 12)
	c, _ := cfgClient(config.TTS{Provider: "cartesia", APIKey: elKey})
	ok, msg := c.Test(context.Background())
	if ok || !strings.Contains(msg, "схоже, це ключ ElevenLabs — ключі Cartesia починаються з sk_car_") {
		t.Fatalf("check: %s", msg)
	}
	if st := c.KeyStatus()[0]; !strings.Contains(st.LastError, "ключ ElevenLabs") {
		t.Fatalf("state: %+v", st)
	}
	_, err := c.Synthesize(context.Background(), "x", "a0e99841-438c-4a64-b679-ae501e7d6091")
	if err == nil || !strings.Contains(err.Error(), "схоже, це ключ ElevenLabs") {
		t.Fatalf("synthesis: %v", err)
	}
}

// A resource endpoint serves text to speech under /tts.
func TestAzureResourceHost(t *testing.T) {
	ae := azureEndpoint
	azureEndpoint = func(region string) string { return "https://" + region + ".tts.speech.microsoft.com" }
	t.Cleanup(func() { azureEndpoint = ae })
	for base, want := range map[string]string{
		"westeurope": "https://westeurope.tts.speech.microsoft.com",
		"https://my-speech.cognitiveservices.azure.com/":    "https://my-speech.cognitiveservices.azure.com/tts",
		"https://my-speech.cognitiveservices.azure.com/tts": "https://my-speech.cognitiveservices.azure.com/tts",
		"https://westeurope.tts.speech.microsoft.com":       "https://westeurope.tts.speech.microsoft.com",
	} {
		s := settings{base: strings.TrimRight(base, "/")}
		if got := azureBase(s); got != want {
			t.Errorf("%s → %s, want %s", base, got, want)
		}
	}
}
