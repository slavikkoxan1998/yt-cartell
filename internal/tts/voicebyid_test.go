package tts

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"ytcartell/internal/config"
)

const (
	carLibVoice = "694f9389-aac1-45b6-b726-9d9369183238"
	elLibVoice  = "JBFqnCBsd6RMkjVDRZzb"
)

// Cartesia: GET /voices/{id} with the documented headers; 404 → not found
// (without trying every key); another provider's id or a broken one is refused
// before any request.
func TestLookupVoiceCartesia(t *testing.T) {
	var paths []string
	mockAll(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		if r.Header.Get("Authorization") != "Bearer sk_car_test_0001" || r.Header.Get("Cartesia-Version") != cartesiaVersion || r.Method != http.MethodGet {
			t.Errorf("request %s %v", r.Method, r.Header)
		}
		switch r.URL.Path {
		case "/voices/" + carLibVoice:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"` + carLibVoice + `","is_owner":false,"status":"active","access":"public","name":"Olena","description":"Warm Ukrainian narrator",
				"gender":"feminine","language":"uk","accents":[{"accent":"ukrainian","locale":"uk-UA","is_native":true}],"preview_file_url":"https://example.test/p.mp3"}`))
		case "/voices/b7d50908-b17c-442d-ad8d-810c63997ed9":
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error_code":"voice_not_found","message":"Voice not found"}`))
		default:
			w.WriteHeader(500)
		}
	})
	c, _ := cfgClient(config.TTS{Provider: "cartesia", APIKey: "sk_car_test_0001", Keys: []string{"sk_car_test_0002"}})
	v, err := c.LookupVoice(context.Background(), "  https://play.cartesia.ai/voices/"+carLibVoice+" ")
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != carLibVoice || v.Name != "Olena" || v.Gender != "female" || v.Language != "uk-UA" || !v.Usable || v.PreviewURL == "" {
		t.Fatalf("voice %+v", v)
	}
	if paths[0] != "/voices/"+carLibVoice+"?expand%5B%5D=preview_file_url" {
		t.Fatalf("path %s", paths[0])
	}
	n := len(paths)
	_, err = c.LookupVoice(context.Background(), "b7d50908-b17c-442d-ad8d-810c63997ed9")
	if !errors.Is(err, ErrVoiceNotFound) || len(paths) != n+1 {
		t.Fatalf("not found: %v after %d requests", err, len(paths)-n)
	}
	n = len(paths)
	for _, bad := range []string{elLibVoice, "olena", ""} {
		if _, err := c.LookupVoice(context.Background(), bad); err == nil || errors.Is(err, ErrVoiceNotFound) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if !strings.Contains(VoiceIDProblem("cartesia", elLibVoice), "ID голосу ElevenLabs") {
		t.Fatalf("foreign id: %q", VoiceIDProblem("cartesia", elLibVoice))
	}
	if len(paths) != n {
		t.Fatal("a malformed id reached the network")
	}
	// No key: a plain error, no request.
	c2, _ := cfgClient(config.TTS{Provider: "cartesia"})
	if _, err := c2.LookupVoice(context.Background(), carLibVoice); err == nil || !strings.Contains(err.Error(), "Ключ Cartesia не задано") {
		t.Fatalf("no key: %v", err)
	}
}

// ElevenLabs: GET /v1/voices/{id}; not in the account → the Voice Library
// (GET /v1/shared-voices?search=id), matched on voice_id.
func TestLookupVoiceElevenLabs(t *testing.T) {
	mockAll(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") != "el-key" {
			t.Errorf("key %q", r.Header.Get("xi-api-key"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/voices/"+elLibVoice:
			_, _ = w.Write([]byte(`{"voice_id":"` + elLibVoice + `","name":"George","category":"premade","labels":{"gender":"male","accent":"british","language":"en"},"preview_url":"https://example.test/g.mp3"}`))
		case strings.HasPrefix(r.URL.Path, "/v1/voices/"):
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"detail":{"type":"not_found","code":"voice_not_found","message":"Voice not found"}}`))
		case r.URL.Path == "/v1/shared-voices":
			if r.URL.Query().Get("search") == "AbCdEfGhIjKlMnOpQrSt" {
				_, _ = w.Write([]byte(`{"voices":[{"voice_id":"zzzzzzzzzzzzzzzzzzzZ","name":"Other"},{"voice_id":"AbCdEfGhIjKlMnOpQrSt","name":"Мирослава - narrator","gender":"female","accent":"ukrainian","language":"uk","locale":"uk-UA","preview_url":"https://example.test/m.mp3"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"voices":[]}`))
		default:
			w.WriteHeader(500)
		}
	})
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "el-key"})
	v, err := c.LookupVoice(context.Background(), elLibVoice)
	if err != nil || v.Name != "George" || v.Gender != "male" || v.Language != "en" {
		t.Fatalf("account voice %+v %v", v, err)
	}
	v, err = c.LookupVoice(context.Background(), "AbCdEfGhIjKlMnOpQrSt")
	if err != nil || v.Name != "Мирослава" || v.Category != "library" || v.Language != "uk-UA" || !v.Usable {
		t.Fatalf("library voice %+v %v", v, err)
	}
	if _, err = c.LookupVoice(context.Background(), "QqQqQqQqQqQqQqQqQqQq"); !errors.Is(err, ErrVoiceNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

// Google AI Studio keys: AIza + 35, from a list with labels, quotes, spaces and
// newlines; other services' keys refused, broken ones counted.
func TestParseGoogleKeys(t *testing.T) {
	k := func(c string) string { return "AIza" + strings.Repeat(c, 35) }
	text := "Ключі Google:\r\n" +
		"key1: " + k("a") + "\r\n" +
		`  "` + k("b") + `"  ` + "\n" +
		k("c") + ", " + k("d") + "; " + k("e") + "\n" +
		"GEMINI_API_KEY=" + k("f") + "\n" +
		"AIzaSyShort123\n" + // broken
		"AIza" + strings.Repeat("g", 34) + "\n" + // one short
		"sk_" + strings.Repeat("0a1b", 12) + "\n" + // ElevenLabs
		k("a") // repeated
	got := ParseGoogleKeys(text, []string{k("z")})
	want := []string{k("a"), k("b"), k("c"), k("d"), k("e"), k("f")}
	if strings.Join(got.Keys, ",") != strings.Join(want, ",") {
		t.Fatalf("keys %v", got.Keys)
	}
	if got.Dup != 1 || got.Junk != 2 || got.Foreign != 1 {
		t.Fatalf("counts %+v", got)
	}
	if s := got.SummaryFor(GoogleKeysLabel); s != "Додано 6 ключів · 1 вже був · 2 не схожі на ключ · 1 — ключ ElevenLabs, не Google AI Studio" {
		t.Fatalf("summary %q", s)
	}
	if p := ParseGoogleKeys(" "+k("q")+" ", nil); len(p.Keys) != 1 || p.Junk != 0 {
		t.Fatalf("one key with spaces: %+v", p)
	}
	// Under a TTS provider a Google key is foreign and named plainly.
	if s := ParseKeys(k("q"), "cartesia", false, nil).Summary("cartesia"); !strings.Contains(s, "ключ Google AI Studio, не Cartesia") {
		t.Fatalf("tts summary %q", s)
	}
}
