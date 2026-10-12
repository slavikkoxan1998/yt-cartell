package tts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
)

const lumeanKey = "lm-test-key-0000000001"

// lumeanMock is Lumean's public API: templates, orders, storage links, the
// ElevenLabs voice library and subscriptions.
type lumeanMock struct {
	mu        sync.Mutex
	srv       *httptest.Server
	templates []map[string]any
	orders    []map[string]any
	polls     int
	paths     []string
	status    string // the order's final status ("" = completed)
	sub403    bool   // the key has no billing.read
}

func newLumeanMock(t *testing.T) *lumeanMock {
	t.Helper()
	m := &lumeanMock{}
	m.srv = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.srv.Close)
	lb, lp, rb := lumeanBase, lumeanPoll, retryBase
	lumeanBase, lumeanPoll, retryBase = m.srv.URL+"/api/public", time.Millisecond, time.Millisecond
	lumeanTemplates.Range(func(k, _ any) bool { lumeanTemplates.Delete(k); return true })
	t.Cleanup(func() { lumeanBase, lumeanPoll, retryBase = lb, lp, rb })
	return m
}

func lmReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (m *lumeanMock) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paths = append(m.paths, r.Method+" "+r.URL.Path)
	if r.URL.Path == "/files/out.mp3" {
		if r.Header.Get("X-API-KEY") != "" {
			w.WriteHeader(400) // the storage link is signed: no key there
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3-lumean-mp3"))
		return
	}
	if r.Header.Get("X-API-KEY") != lumeanKey {
		lmReply(w, 401, map[string]any{"success": false, "message": "auth.api_key.invalid", "data": nil})
		return
	}
	var body map[string]any
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		_ = json.Unmarshal(b, &body)
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/public/templates":
		lmReply(w, 200, map[string]any{"success": true, "data": []any{map[string]any{"id": "tpl-other", "name": "Мій шаблон"}}})
	case r.Method == "POST" && r.URL.Path == "/api/public/templates":
		m.templates = append(m.templates, body)
		lmReply(w, 201, map[string]any{"success": true, "data": map[string]any{"id": "tpl-1"}})
	case r.Method == "POST" && r.URL.Path == "/api/public/orders":
		if body["template_id"] != "tpl-1" {
			lmReply(w, 422, map[string]any{"message": "template invalid", "errors": map[string]any{"template_id": []string{"не знайдено"}}})
			return
		}
		if strings.Contains(body["input_text"].(string), "заборонено") {
			lmReply(w, 422, map[string]any{"success": false, "message": "Текст заблоковано", "reason": "content_blocked", "error_code": "content_blocked"})
			return
		}
		m.orders = append(m.orders, body)
		lmReply(w, 201, map[string]any{"success": true, "data": map[string]any{"id": "ord-1", "status": "created"}})
	case r.Method == "GET" && r.URL.Path == "/api/public/orders/ord-1":
		m.polls++
		if m.polls < 2 {
			lmReply(w, 200, map[string]any{"success": true, "data": map[string]any{"id": "ord-1", "status": "in_progress", "progress_percent": 50}})
			return
		}
		st := m.status
		if st == "" {
			st = "completed"
		}
		data := map[string]any{"id": "ord-1", "status": st, "result": nil}
		if st == "completed" {
			data["result"] = map[string]any{"files": []string{"orders/ord-1/out.mp3"}, "service_files": []string{"orders/ord-1/out.srt"}}
		}
		lmReply(w, 200, map[string]any{"success": true, "data": data})
	case r.Method == "POST" && r.URL.Path == "/api/public/storage/url":
		if body["path"] != "orders/ord-1/out.mp3" {
			lmReply(w, 404, map[string]any{"success": false, "message": "file not found"})
			return
		}
		lmReply(w, 200, map[string]any{"success": true, "data": map[string]any{"url": m.srv.URL + "/files/out.mp3"}})
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/cancel"):
		lmReply(w, 200, map[string]any{"success": true})
	case r.URL.Path == "/api/public/voices/elevenlabs/library":
		q := r.URL.Query()
		var voices []any
		switch {
		case q.Get("required_languages") == "uk":
			voices = []any{map[string]any{"voice_id": "UkVoice0000000000001", "name": "Олена", "gender": "female", "age": "middle_aged", "language": "uk", "preview_url": "https://x/p.mp3"}}
		case q.Get("page") == "0":
			voices = []any{map[string]any{"voice_id": "EnVoice0000000000001", "name": "Adam", "gender": "male", "language": "en", "locale": "en-US"},
				map[string]any{"voice_id": "UkVoice0000000000001", "name": "Олена", "gender": "female"}}
		}
		lmReply(w, 200, map[string]any{"success": true, "data": map[string]any{"voices": voices, "total": 2, "page": q.Get("page"), "page_size": 100, "has_more": false}})
	case r.URL.Path == "/api/public/subscriptions":
		if m.sub403 {
			lmReply(w, 403, map[string]any{"success": false, "message": "permission billing.read required", "data": nil})
			return
		}
		lmReply(w, 200, map[string]any{"success": true, "data": map[string]any{"subscriptions": []any{map[string]any{"items": []any{
			map[string]any{"plan": map[string]any{"code": "pro", "name": "Pro"}, "is_usable": true, "token_allowance": 1000000, "tokens_used": 12345, "tokens_remaining": 987655}}}}}})
	case r.URL.Path == "/api/public/user":
		lmReply(w, 200, map[string]any{"success": true, "data": map[string]any{"id": 7}})
	default:
		lmReply(w, 404, map[string]any{"success": false, "message": "not found"})
	}
}

func TestLumeanSynthesize(t *testing.T) {
	m := newLumeanMock(t)
	c, _ := cfgClient(config.TTS{Provider: "lumean", APIKey: lumeanKey, Voice: "UkVoice0000000000001"})
	out, err := c.SynthesizeWith(context.Background(), "Привіт, світе!", "", SynthOptions{LanguageCode: "uk", Speed: 1.5, Stability: 0.6})
	if err != nil || string(out) != "ID3-lumean-mp3" {
		t.Fatalf("synth %q %v", out, err)
	}
	if len(m.templates) != 1 || len(m.orders) != 1 {
		t.Fatalf("templates %d orders %d", len(m.templates), len(m.orders))
	}
	tpl := m.templates[0]
	ts := tpl["config"].(map[string]any)["tts_settings"].(map[string]any)
	if tpl["service_key"] != "elevenlabs" || ts["model_id"] != "eleven_multilingual_v2" || ts["mode"] != "mode_v1" || ts["voice_id"] != "UkVoice0000000000001" {
		t.Fatalf("template %v", tpl)
	}
	o := m.orders[0]
	ov := o["config_override"].(map[string]any)["tts_settings"].(map[string]any)
	vs := ov["voice_settings"].(map[string]any)
	if o["template_id"] != "tpl-1" || o["input_text"] != "Привіт, світе!" || ov["voice_id"] != "UkVoice0000000000001" || ov["language_code"] != "uk" ||
		vs["speed"].(float64) != 1.2 || vs["stability"].(float64) != 0.6 {
		t.Fatalf("order %v", o)
	}
	// The template is remembered: the next line makes only an order.
	if _, err := c.Synthesize(context.Background(), "Ще раз", ""); err != nil || len(m.templates) != 1 {
		t.Fatalf("second: %v, templates %d", err, len(m.templates))
	}
	// Hebrew is not among the 29 languages of multilingual v2: eleven_v3.
	lumeanTemplates.Range(func(k, _ any) bool { lumeanTemplates.Delete(k); return true })
	m.templates = nil
	if _, err := c.SynthesizeWith(context.Background(), "שלום", "", SynthOptions{LanguageCode: "he", Stability: 0.6}); err != nil {
		t.Fatal(err)
	}
	if ts := m.templates[0]["config"].(map[string]any)["tts_settings"].(map[string]any); ts["model_id"] != "eleven_v3" {
		t.Fatalf("model %v", ts["model_id"])
	}
	if vs := m.orders[len(m.orders)-1]["config_override"].(map[string]any)["tts_settings"].(map[string]any)["voice_settings"].(map[string]any); vs["stability"].(float64) != 0.5 {
		t.Fatalf("v3 stability %v", vs)
	}
}

func TestLumeanFailures(t *testing.T) {
	m := newLumeanMock(t)
	c, _ := cfgClient(config.TTS{Provider: "lumean", APIKey: lumeanKey, Voice: "UkVoice0000000000001"})
	if _, err := c.Synthesize(context.Background(), "заборонено тут", ""); err == nil || !strings.Contains(err.Error(), "контентною політикою") {
		t.Fatalf("blocked: %v", err)
	}
	m.status = "failed"
	if _, err := c.Synthesize(context.Background(), "текст", ""); err == nil || !strings.Contains(err.Error(), "не вдалася (failed)") {
		t.Fatalf("failed: %v", err)
	}
	bad, _ := cfgClient(config.TTS{Provider: "lumean", APIKey: "lm-wrong-key-0000", Voice: "UkVoice0000000000001"})
	if _, err := bad.Synthesize(context.Background(), "текст", ""); err == nil || !strings.Contains(err.Error(), "ключ не прийнято (401)") {
		t.Fatalf("bad key: %v", err)
	}
	// An ElevenLabs-shaped voice id is Lumean's own, not «another service».
	if f := ForeignVoiceOf(config.TTS{Provider: "lumean"}, "JBFqnCBsd6RMkjVDRZzb"); f != "" {
		t.Fatalf("foreign %q", f)
	}
}

func TestLumeanVoicesAndTest(t *testing.T) {
	m := newLumeanMock(t)
	c, _ := cfgClient(config.TTS{Provider: "lumean", APIKey: lumeanKey})
	list, err := c.Voices(context.Background(), true)
	if err != nil || len(list.Voices) != 2 {
		t.Fatalf("voices %+v %v", list, err)
	}
	if v := list.Voices[0]; v.ID != "UkVoice0000000000001" || v.Language != "uk" || v.Gender != "female" || !v.Usable || v.Age != "middle aged" {
		t.Fatalf("first voice %+v", v)
	}
	ok, msg := c.Test(context.Background())
	if !ok || !strings.Contains(msg, "тариф Pro · залишилось 987 655 токенів") {
		t.Fatalf("test %v %q", ok, msg)
	}
	m.sub403 = true
	if ok, msg = c.Test(context.Background()); !ok || !strings.Contains(msg, "billing.read") {
		t.Fatalf("no billing: %v %q", ok, msg)
	}
	bad, _ := cfgClient(config.TTS{Provider: "lumean", APIKey: "lm-wrong-key-0000"})
	ok, msg = bad.Test(context.Background())
	if ok || !strings.Contains(msg, "Запит: GET "+m.srv.URL+"/api/public/subscriptions → 401 Unauthorized") || strings.Contains(msg, "lm-wrong-key-0000") {
		t.Fatalf("bad key test %v %q", ok, msg)
	}
	if p, ok := ProviderByID("lumean"); !ok || !p.ListsVoices || p.DefaultModel != "eleven_multilingual_v2" {
		t.Fatalf("preset %+v", p)
	}
}

// TestCustomSpecTTS: «Власний сервер» with a spec goes through the
// universal adapter (key rotation included).
func TestCustomSpecTTS(t *testing.T) {
	var auth, input string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if r.URL.Path == "/v1/models" {
			lmReply(w, 200, map[string]any{"data": []any{}})
			return
		}
		input, _ = b["input"].(string)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3-spec"))
	}))
	defer srv.Close()
	spec := `{"submit":{"path":"/audio/speech","body":{"input":"{{text}}","voice":"{{voice}}","model":"{{model}}"}},"test":{"path":"/models"}}`
	cfg := config.Config{TTS: config.TTS{Provider: "custom", BaseURL: srv.URL + "/v1", APIKey: "spec-key-000001", Model: "m", Voice: "v1"}}
	cfg.Custom.TTSSpec = spec
	c := New(func() config.Config { return cfg })
	out, err := c.Synthesize(context.Background(), "Текст", "")
	if err != nil || string(out) != "ID3-spec" || auth != "Bearer spec-key-000001" || input != "Текст" {
		t.Fatalf("spec synth %q %v %q %q", out, err, auth, input)
	}
	if ok, msg := c.Test(context.Background()); !ok {
		t.Fatalf("spec test %q", msg)
	}
}
