package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/config"
)

func textCfg(t config.Text) func() config.Config {
	return func() config.Config {
		c := config.Default()
		c.Text = t
		return c
	}
}

// TestNormalizeBase: pasted request URLs lose their endpoint, schemes are
// added, Azure keeps its api-version apart.
func TestNormalizeBase(t *testing.T) {
	for _, tc := range []struct{ proto, in, want, ver string }{
		{"openai", "https://gateway.example.com", "https://gateway.example.com", ""},
		{"openai", "https://gateway.example.com/", "https://gateway.example.com", ""},
		{"openai", "https://gateway.example.com/v1/chat/completions", "https://gateway.example.com/v1", ""},
		{"openai", "https://openrouter.ai/api/v1/models", "https://openrouter.ai/api/v1", ""},
		{"openai", "gateway.example.com/v1", "https://gateway.example.com/v1", ""},
		{"openai", "127.0.0.1:8317/v1", "http://127.0.0.1:8317/v1", ""},
		{"openai", " «https://x.test/v1/» ", "https://x.test/v1", ""},
		{"anthropic", "https://api.anthropic.com/v1/messages", "https://api.anthropic.com/v1", ""},
		{"ollama", "http://localhost:11434/api/chat", "http://localhost:11434", ""},
		{"ollama", "http://localhost:11434/v1", "http://localhost:11434", ""},
		{"ollama", "http://localhost:11434/api", "http://localhost:11434", ""},
		{"azure", "https://res.openai.azure.com", "https://res.openai.azure.com/openai/v1", ""},
		{"azure", "https://res.openai.azure.com/openai/deployments/gpt4o/chat/completions?api-version=2024-10-21",
			"https://res.openai.azure.com/openai/deployments/gpt4o", "2024-10-21"},
	} {
		got, ver := NormalizeBase(tc.proto, tc.in)
		if got != tc.want || ver != tc.ver {
			t.Errorf("NormalizeBase(%s, %q) = %q, %q; want %q, %q", tc.proto, tc.in, got, ver, tc.want, tc.ver)
		}
	}
	if got := Candidates("openai", "https://gateway.example.com"); !reflect.DeepEqual(got, []string{
		"https://gateway.example.com", "https://gateway.example.com/v1", "https://gateway.example.com/api/v1", "https://gateway.example.com/openai/v1"}) {
		t.Errorf("candidates = %v", got)
	}
}

// v1Server answers only under /v1 (like gateway.example.com), 404 {"error":"not_found"} elsewhere.
func v1Server(t *testing.T, prefix string) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case prefix + "/models":
			reply(w, 200, map[string]any{"object": "list", "data": []any{map[string]any{"id": "gemini-3.8-flash"}, map[string]any{"id": "gpt-5"}}})
		case prefix + "/chat/completions":
			if r.Header.Get("Authorization") != "Bearer sk-test" {
				reply(w, 401, map[string]any{"error": map[string]any{"message": "bad key"}})
				return
			}
			reply(w, 200, completion("OK"))
		default:
			reply(w, 404, map[string]any{"error": "not_found"})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

// TestBaseWithoutV1: a user's report — the address without /v1 answers
// 404; generation finds /v1, reports it once and keeps using it.
func TestBaseWithoutV1(t *testing.T) {
	srv, paths := v1Server(t, "/v1")
	c := New(textCfg(config.Text{Provider: "openai", BaseURL: srv.URL, APIKey: "sk-test", Model: "gemini-3.8-flash"}), nil)
	var from, to string
	c.OnResolved(func(f, tt string) { from, to = f, tt })
	out, err := c.Text(context.Background(), Request{Prompt: "x"})
	if err != nil || out != "OK" {
		t.Fatalf("text = %q, %v", out, err)
	}
	if from != srv.URL || to != srv.URL+"/v1" {
		t.Fatalf("resolved %q → %q", from, to)
	}
	n := len(*paths)
	if _, err := c.Text(context.Background(), Request{Prompt: "y"}); err != nil {
		t.Fatal(err)
	}
	if got := (*paths)[n:]; !reflect.DeepEqual(got, []string{"POST /v1/chat/completions"}) {
		t.Fatalf("second call went to %v", got)
	}

	// Check (Settings → «Перевірити»): the working form, the models, Fixed.
	c2 := New(nil, nil)
	p, err := c2.Check(context.Background(), Conn{Protocol: "openai", BaseURL: srv.URL + "/chat/completions", APIKey: "sk-test", Model: "gemini-3.8-flash"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Conn.BaseURL != srv.URL+"/v1" || !p.Fixed || p.Reply != "OK" || !reflect.DeepEqual(p.Models, []string{"gemini-3.8-flash", "gpt-5"}) {
		t.Fatalf("probe = %+v", p)
	}
	if !strings.Contains(p.Summary(), "моделей: 2") {
		t.Fatalf("summary %q", p.Summary())
	}

	// Models of a /v1-less address: found too.
	ms, err := c.ModelsOf(context.Background(), Conn{Protocol: "openai", BaseURL: srv.URL, APIKey: "sk-test"})
	if err != nil || len(ms) != 2 {
		t.Fatalf("models %v %v", ms, err)
	}
}

// TestBaseAPIV1: OpenRouter-like /api/v1 is found too.
func TestBaseAPIV1(t *testing.T) {
	srv, _ := v1Server(t, "/api/v1")
	p, err := New(nil, nil).Check(context.Background(), Conn{Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "sk-test", Model: "m"}, true)
	if err != nil || p.Conn.BaseURL != srv.URL+"/api/v1" {
		t.Fatalf("probe %+v %v", p, err)
	}
}

// TestCheckNothingAnswers: every form is 404 — the error lists each request
// (method + URL, no key) and the server's reply.
func TestCheckNothingAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply(w, 404, map[string]any{"error": "not_found"})
	}))
	defer srv.Close()
	_, err := New(nil, nil).Check(context.Background(), Conn{Protocol: "openai", BaseURL: srv.URL + "?key=sk-secret-123456", APIKey: "sk-secret-123456", Model: "m"}, true)
	if err == nil {
		t.Fatal("no error")
	}
	msg := err.Error()
	for _, want := range []string{"404", "POST " + srv.URL + "/chat/completions", "POST " + srv.URL + "/v1/chat/completions",
		"GET " + srv.URL + "/api/v1/models", `{"error":"not_found"}`, "Підказка:"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "sk-secret-123456") {
		t.Fatalf("key leaked:\n%s", msg)
	}

	// A plain generation shows the exact request too.
	c := New(textCfg(config.Text{Provider: "openai", BaseURL: srv.URL, APIKey: "sk-secret-123456", Model: "m"}), nil)
	_, err = c.Text(context.Background(), Request{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "Запит: POST "+srv.URL+"/chat/completions → 404 Not Found") ||
		!strings.Contains(err.Error(), "/v1") || strings.Contains(err.Error(), "sk-secret") {
		t.Fatalf("text err = %v", err)
	}
}

// TestModelNotFound: a 404 about the model is not an address problem.
func TestModelNotFound(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		reply(w, 404, map[string]any{"error": map[string]any{"message": "The model `nope` does not exist", "code": "model_not_found"}})
	}))
	defer srv.Close()
	c := New(textCfg(config.Text{Provider: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "nope"}), nil)
	_, err := c.Text(context.Background(), Request{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "модель «nope» не знайдено") || hits != 1 {
		t.Fatalf("err %v, hits %d", err, hits)
	}
}

// TestAnthropic: the Messages API — headers, body, text blocks, refusal,
// the models list with pages, and the max_tokens fallback.
func TestAnthropic(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-ant-test" || r.Header.Get("anthropic-version") != AnthropicVersion || r.Header.Get("Authorization") != "" {
			reply(w, 401, map[string]any{"type": "error", "error": map[string]any{"type": "authentication_error", "message": "invalid x-api-key"}})
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			if r.URL.Query().Get("after_id") == "" {
				reply(w, 200, map[string]any{"data": []any{map[string]any{"id": "claude-a"}}, "has_more": true, "last_id": "claude-a"})
			} else {
				reply(w, 200, map[string]any{"data": []any{map[string]any{"id": "claude-b"}}, "has_more": false})
			}
		case "/v1/messages":
			b, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			mu.Lock()
			bodies = append(bodies, m)
			mu.Unlock()
			if m["max_tokens"].(float64) > anthropicMaxTokensSmall && m["model"] == "claude-small" {
				reply(w, 400, map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": "max_tokens: 16000 > 4096"}})
				return
			}
			if m["model"] == "claude-refuse" {
				reply(w, 200, map[string]any{"content": []any{}, "stop_reason": "refusal", "stop_details": map[string]any{"explanation": "policy"}})
				return
			}
			reply(w, 200, map[string]any{"content": []any{map[string]any{"type": "thinking", "thinking": "hm"}, map[string]any{"type": "text", "text": `{"a":1}`}}, "stop_reason": "end_turn"})
		default:
			reply(w, 404, map[string]any{"type": "error", "error": map[string]any{"type": "not_found_error", "message": "Not found"}})
		}
	}))
	defer srv.Close()
	cfg := config.Text{Provider: "anthropic", BaseURL: srv.URL, APIKey: "sk-ant-test", Model: "claude-a"} // no /v1: found
	c := New(textCfg(cfg), nil)
	var out struct{ A int }
	if err := c.JSON(context.Background(), Request{System: "SYS", Prompt: "P", Temperature: 1.5}, &out); err != nil || out.A != 1 {
		t.Fatalf("json %v %v", out, err)
	}
	b := bodies[len(bodies)-1]
	if b["system"] == nil || !strings.HasPrefix(b["system"].(string), "SYS") || b["temperature"].(float64) != 1 || b["max_tokens"].(float64) != anthropicMaxTokens {
		t.Fatalf("body %v", b)
	}
	ms, err := c.Models(context.Background())
	if err != nil || !reflect.DeepEqual(ms, []string{"claude-a", "claude-b"}) {
		t.Fatalf("models %v %v", ms, err)
	}
	cfg.Model = "claude-small"
	if _, err := New(textCfg(cfg), nil).Text(context.Background(), Request{Prompt: "x"}); err != nil {
		t.Fatalf("small: %v", err)
	}
	if b := bodies[len(bodies)-1]; b["max_tokens"].(float64) != anthropicMaxTokensSmall {
		t.Fatalf("fallback body %v", b)
	}
	cfg.Model = "claude-refuse"
	if _, err := New(textCfg(cfg), nil).Text(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "відмовилася") {
		t.Fatalf("refusal: %v", err)
	}
	// Bearer instead of x-api-key: refused, and the hint says how to switch.
	cfg.Model, cfg.Auth = "claude-a", "bearer"
	if _, err := New(textCfg(cfg), nil).Text(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "Авторизацію") {
		t.Fatalf("bearer: %v", err)
	}
}

// TestOllama: native /api/chat and /api/tags.
func TestOllama(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			reply(w, 200, map[string]any{"models": []any{map[string]any{"name": "qwen3:8b", "model": "qwen3:8b"}, map[string]any{"name": "llama3.3"}}})
		case "/api/chat":
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &got)
			if got["model"] == "missing" {
				reply(w, 404, map[string]any{"error": "model 'missing' not found"})
				return
			}
			reply(w, 200, map[string]any{"message": map[string]any{"role": "assistant", "content": `{"ok":true}`}, "done": true, "done_reason": "stop"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := config.Text{Provider: "ollama", BaseURL: srv.URL + "/v1", Model: "qwen3:8b"}
	c := New(textCfg(cfg), nil)
	var out struct{ OK bool }
	if err := c.JSON(context.Background(), Request{System: "S", Prompt: "P", Temperature: 0.8, Images: []Image{{MIME: "image/png", Data: []byte("png")}}}, &out); err != nil || !out.OK {
		t.Fatalf("json %v %v", out, err)
	}
	msgs := got["messages"].([]any)
	if got["stream"] != false || got["format"] != "json" || len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" ||
		len(msgs[1].(map[string]any)["images"].([]any)) != 1 || got["options"].(map[string]any)["temperature"].(float64) != 0.8 {
		t.Fatalf("body %v", got)
	}
	ms, err := c.Models(context.Background())
	if err != nil || !reflect.DeepEqual(ms, []string{"llama3.3", "qwen3:8b"}) {
		t.Fatalf("models %v %v", ms, err)
	}
	cfg.Model = "missing"
	if _, err := New(textCfg(cfg), nil).Text(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "не знайдено") {
		t.Fatalf("missing: %v", err)
	}
}

// TestAzure: the v1 API (api-key header, model = deployment) and a classic
// deployment URL with its api-version.
func TestAzure(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		if r.Header.Get("api-key") != "az-key" || r.Header.Get("Authorization") != "" {
			reply(w, 401, map[string]any{"error": map[string]any{"code": "401", "message": "Access denied due to invalid subscription key"}})
			return
		}
		switch r.URL.Path {
		case "/openai/v1/chat/completions", "/openai/deployments/dep1/chat/completions":
			reply(w, 200, completion("OK"))
		case "/openai/v1/models":
			reply(w, 200, map[string]any{"data": []any{map[string]any{"id": "gpt-5-mini"}}})
		default:
			reply(w, 404, map[string]any{"error": map[string]any{"code": "404", "message": "Resource not found"}})
		}
	}))
	defer srv.Close()
	c := New(textCfg(config.Text{Provider: "azure", BaseURL: srv.URL + "/openai/v1", APIKey: "az-key", Model: "my-deployment"}), nil)
	if out, err := c.Text(context.Background(), Request{Prompt: "x"}); err != nil || out != "OK" {
		t.Fatalf("v1: %q %v", out, err)
	}
	c = New(textCfg(config.Text{Provider: "azure", BaseURL: srv.URL + "/openai/deployments/dep1/chat/completions?api-version=2025-04-01-preview", APIKey: "az-key"}), nil)
	if out, err := c.Text(context.Background(), Request{Prompt: "x"}); err != nil || out != "OK" {
		t.Fatalf("deployment: %q %v", out, err)
	}
	if last := paths[len(paths)-1]; last != "/openai/deployments/dep1/chat/completions?api-version=2025-04-01-preview" {
		t.Fatalf("deployment call %s", last)
	}
	if ms, err := c.Models(context.Background()); err != nil || !reflect.DeepEqual(ms, []string{"dep1"}) {
		t.Fatalf("deployment models %v %v", ms, err)
	}
	p, err := New(nil, nil).Check(context.Background(), Conn{Protocol: "azure", BaseURL: srv.URL + "/openai/deployments/dep1/chat/completions?api-version=2025-04-01-preview", APIKey: "az-key", Model: "dep1"}, true)
	if err != nil || p.Conn.APIVersion != "2025-04-01-preview" || paths[len(paths)-1] != "/openai/deployments/dep1/chat/completions?api-version=2025-04-01-preview" {
		t.Fatalf("check %+v %v %v", p, err, paths)
	}
}

// TestCheckFindsAuthHeader: a server that wants x-api-key while the card is
// on auto — Check finds the header and says so.
func TestCheckFindsAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "kk-123456" {
			reply(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			reply(w, 200, []any{map[string]any{"id": "m1"}}) // a bare array (Together)
		case "/v1/chat/completions":
			reply(w, 200, completion("OK"))
		default:
			reply(w, 404, map[string]any{"error": "not_found"})
		}
	}))
	defer srv.Close()
	p, err := New(nil, nil).Check(context.Background(), Conn{Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "kk-123456", Model: "m1"}, true)
	if err != nil || p.Conn.Auth != "x-api-key" || !p.AuthSet || !reflect.DeepEqual(p.Models, []string{"m1"}) {
		t.Fatalf("probe %+v %v", p, err)
	}
	// A wrong key: the exact request and the auth hint, no key in the text.
	_, err = New(nil, nil).Check(context.Background(), Conn{Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "wrong-key-999", Model: "m1"}, true)
	if err == nil || !strings.Contains(err.Error(), "GET "+srv.URL+"/v1/models → 401 Unauthorized") || strings.Contains(err.Error(), "wrong-key-999") {
		t.Fatalf("bad key: %v", err)
	}
}
