package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Synthetic keys in the providers' formats (never real ones).
var (
	elKey1  = "sk_" + strings.Repeat("0a1b", 12)
	elKey2  = "sk_" + strings.Repeat("9f8e", 12)
	carKey1 = "sk_car_" + strings.Repeat("Ab3", 8)
	elVoice = "JBFqnCBsd6RMkjVDRZzb"
	carVoce = "a0e99841-438c-4a64-b679-ae501e7d6091"
)

func keysOf(p TTSProvider) string { return strings.Join(p.AllKeys(), ",") }

// A config.json written before per-provider settings: the ElevenLabs keys and
// voice under the flat fields end up under "elevenlabs" (the active provider)
// — nothing lost, and the file keeps them after a save and a reload.
func TestTTSMigrationLegacyElevenLabs(t *testing.T) {
	doc := fmt.Sprintf(`{"tts":{"provider":"elevenlabs","apiKey":%q,"keys":[%q],"voice":%q,"model":"eleven_v3","baseUrl":""}}`, elKey1, elKey2, elVoice)
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(Path()) })
	if err := os.WriteFile(Path(), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	check := func(c Config) {
		t.Helper()
		el := c.TTS.Providers["elevenlabs"]
		if c.TTS.Provider != "elevenlabs" || c.TTS.APIKey != elKey1 || strings.Join(c.TTS.Keys, ",") != elKey2 || c.TTS.Voice != elVoice || c.TTS.Model != "eleven_v3" {
			t.Fatalf("active view lost something: %+v", c.TTS.Active())
		}
		if keysOf(el) != elKey1+","+elKey2 || el.Voice != elVoice || el.Model != "eleven_v3" {
			t.Fatalf("providers[elevenlabs] = %+v", el)
		}
		if len(c.TTS.Providers) != 1 {
			t.Fatalf("other providers got settings: %v", c.TTS.Providers)
		}
	}
	check(c)
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path())
	if !strings.Contains(string(b), `"providers"`) {
		t.Fatalf("saved file has no providers: %s", b)
	}
	c2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	check(c2)
}

// The owner's broken state: Cartesia chosen while the shared list still held
// the ElevenLabs keys and voice. They move to ElevenLabs; Cartesia keeps only
// what is its own.
func TestTTSMigrationSplitsForeignKeys(t *testing.T) {
	var c Config
	c.TTS = TTS{Provider: "cartesia", APIKey: elKey1, Keys: []string{elKey2, carKey1}, Voice: elVoice}
	c.Normalize()
	if keysOf(c.TTS.Active()) != carKey1 || c.TTS.Voice != "" {
		t.Fatalf("cartesia view = %+v", c.TTS.Active())
	}
	el := c.TTS.Of("elevenlabs")
	if keysOf(el) != elKey1+","+elKey2 || el.Voice != elVoice {
		t.Fatalf("elevenlabs = %+v", el)
	}
	// Switching back shows them; Cartesia keeps its own key.
	c.TTS.Switch("elevenlabs")
	if c.TTS.APIKey != elKey1 || c.TTS.Voice != elVoice {
		t.Fatalf("after switch: %+v", c.TTS.Active())
	}
	if keysOf(c.TTS.Of("cartesia")) != carKey1 {
		t.Fatalf("cartesia lost its key: %+v", c.TTS.Of("cartesia"))
	}

	// An own server takes any key: nothing moves away from it.
	var own Config
	own.TTS = TTS{Provider: "custom", APIKey: elKey1, BaseURL: "http://127.0.0.1:9/tts", Voice: elVoice}
	own.Normalize()
	if own.TTS.APIKey != elKey1 || own.TTS.Voice != elVoice || len(own.TTS.Providers) != 1 {
		t.Fatalf("custom: %+v", own.TTS)
	}
	// MiniMax voice ids are free-form: an ElevenLabs-looking one stays.
	var mm Config
	mm.TTS = TTS{Provider: "minimax", APIKey: "mm-key", Voice: elVoice}
	mm.Normalize()
	if mm.TTS.Voice != elVoice {
		t.Fatalf("minimax voice moved: %+v", mm.TTS)
	}
}

// Switching providers shows each provider's own keys, model, address and
// voice — empty for a provider never set up — and never shares them.
func TestTTSSwitchKeepsEachProvider(t *testing.T) {
	s := NewStoreWith(Config{TTS: TTS{Provider: "elevenlabs", APIKey: elKey1, Voice: elVoice, Model: "eleven_v3"}})
	if err := s.Update(func(c *Config) { c.TTS.Switch("cartesia") }); err != nil {
		t.Fatal(err)
	}
	c := s.Get()
	if c.TTS.Provider != "cartesia" || c.TTS.APIKey != "" || len(c.TTS.Keys) != 0 || c.TTS.Voice != "" || c.TTS.Model != "" {
		t.Fatalf("cartesia shows another provider's settings: %+v", c.TTS.Active())
	}
	_ = s.Update(func(c *Config) {
		c.TTS.SetKeys([]string{carKey1, " ", carKey1})
		c.TTS.Voice = carVoce
	})
	_ = s.Update(func(c *Config) { c.TTS.Switch("elevenlabs") })
	c = s.Get()
	if c.TTS.APIKey != elKey1 || c.TTS.Voice != elVoice || c.TTS.Model != "eleven_v3" {
		t.Fatalf("elevenlabs after the round trip: %+v", c.TTS.Active())
	}
	if p := c.TTS.Of("cartesia"); keysOf(p) != carKey1 || p.Voice != carVoce {
		t.Fatalf("cartesia stored: %+v", p)
	}
	_ = s.Update(func(c *Config) { c.TTS.Switch("azure") })
	_ = s.Update(func(c *Config) { c.TTS.BaseURL = "westeurope/" })
	_ = s.Update(func(c *Config) { c.TTS.Switch("custom") })
	if c = s.Get(); c.TTS.BaseURL != "" {
		t.Fatalf("custom shows the Azure region %q", c.TTS.BaseURL)
	}
	if got := c.TTS.Of("azure").BaseURL; got != "westeurope" {
		t.Fatalf("azure region = %q", got)
	}

	// Get is a deep copy: editing it changes nothing in the store.
	c = s.Get()
	p := c.TTS.Providers["elevenlabs"]
	p.Keys = append(p.Keys, "x")
	c.TTS.Providers["elevenlabs"] = p
	c.TTS.Providers["fish"] = TTSProvider{APIKey: "y"}
	if got := s.Get().TTS; len(got.Providers["elevenlabs"].Keys) != 0 || got.Providers["fish"].APIKey != "" {
		t.Fatalf("store changed through a copy: %+v", got.Providers)
	}
}

// Assigning Provider directly (old code paths, imports) behaves like Switch;
// fields edited in the same change belong to the new provider.
func TestTTSDirectProviderAssignment(t *testing.T) {
	s := NewStoreWith(Config{TTS: TTS{Provider: "elevenlabs", APIKey: elKey1, Keys: []string{elKey2}, Voice: elVoice}})
	_ = s.Update(func(c *Config) { c.TTS.Provider = "openai"; c.TTS.APIKey = "sk-openai-test" })
	c := s.Get()
	if c.TTS.APIKey != "sk-openai-test" || len(c.TTS.Keys) != 0 {
		t.Fatalf("openai view: %+v", c.TTS.Active())
	}
	if c.TTS.Voice != "" {
		t.Fatalf("openai got the ElevenLabs voice %q", c.TTS.Voice)
	}
	if el := c.TTS.Of("elevenlabs"); keysOf(el) != elKey1+","+elKey2 || el.Voice != elVoice {
		t.Fatalf("elevenlabs lost its settings: %+v", el)
	}
	// A plain provider change (nothing else edited) shows the stored settings.
	_ = s.Update(func(c *Config) { c.TTS.Provider = "elevenlabs" })
	if c = s.Get(); c.TTS.APIKey != elKey1 || c.TTS.Voice != elVoice {
		t.Fatalf("back to elevenlabs: %+v", c.TTS.Active())
	}
	// Unknown ids fall back to ElevenLabs.
	_ = s.Update(func(c *Config) { c.TTS.Provider = "nope" })
	if c = s.Get(); c.TTS.Provider != "elevenlabs" || c.TTS.APIKey != elKey1 {
		t.Fatalf("unknown provider: %+v", c.TTS)
	}
}

// The JSON keeps the flat fields (older builds read them) next to providers.
func TestTTSJSONShape(t *testing.T) {
	c := Default()
	c.TTS = TTS{Provider: "cartesia", APIKey: carKey1, Voice: carVoce}
	c.TTS.Providers = map[string]TTSProvider{"elevenlabs": {APIKey: elKey1, Voice: elVoice}}
	c.Normalize()
	b, err := json.Marshal(c.TTS)
	if err != nil {
		t.Fatal(err)
	}
	var back TTS
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	back.normalize()
	if back.APIKey != carKey1 || back.Of("elevenlabs").APIKey != elKey1 || back.Of("cartesia").Voice != carVoce {
		t.Fatalf("round trip: %s → %+v", b, back)
	}
	if strings.Contains(string(b), "view") {
		t.Fatalf("internal field leaked: %s", b)
	}
}

func TestTTSShapes(t *testing.T) {
	for k, want := range map[string]string{
		elKey1: "elevenlabs", carKey1: "cartesia", "AIza" + strings.Repeat("x", 35): "google",
		"sk-proj-" + strings.Repeat("A1", 12): "openai", strings.Repeat("ab", 16): "", "mm-key": "", "": "",
	} {
		if got := TTSKeyOwner(k); got != want {
			t.Errorf("TTSKeyOwner(%.12s…) = %q, want %q", k, got, want)
		}
	}
	for v, want := range map[string]string{
		elVoice: "elevenlabs", carVoce: "cartesia", strings.Repeat("a1", 16): "fish", "uk-UA-OstapNeural": "azure",
		"en-US-AndrewMultilingualNeural": "azure", "Chirp3-HD-Charon": "google", "uk-UA-Wavenet-A": "google",
		"alloy": "openai", "English_expressive_narrator": "", "abcdefghijklmnopqrst": "", "": "",
	} {
		if got := TTSVoiceOwner(v); got != want {
			t.Errorf("TTSVoiceOwner(%q) = %q, want %q", v, got, want)
		}
	}
	if got := ForeignTTSVoice("cartesia", elVoice, false); got != "elevenlabs" {
		t.Errorf("ElevenLabs voice under Cartesia: %q", got)
	}
	if ForeignTTSVoice("elevenlabs", elVoice, false) != "" || ForeignTTSVoice("custom", elVoice, false) != "" ||
		ForeignTTSVoice("openai", elVoice, true) != "" || ForeignTTSVoice("minimax", elVoice, false) != "" {
		t.Error("an own or ambiguous voice is called foreign")
	}
}
