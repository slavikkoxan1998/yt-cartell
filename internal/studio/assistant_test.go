package studio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/config"
)

const (
	gatewayKey = "sk-demo000123456789abcdefXYZ"
	elevenKey  = "sk_0123456789abcdef0123456789abcdef01234567"
	googleKey  = "AIzaSyDEMO_not_a_real_key_0000000000001"
)

func TestParseAssist(t *testing.T) {
	in := "ось мій ендпоінт https://gateway.example.com ключ " + gatewayKey + " модель gemini-3.8-flash\n" +
		"озвучка: " + elevenKey + " голос JBFqnCBsd6RMkjVDRZzb\n" +
		googleKey + "\n" +
		"документація для відео https://api.example.com/docs/video, а ще openrouter.ai/api/v1"
	items := ParseAssist(in)
	byKind := map[string][]AssistItem{}
	for _, it := range items {
		byKind[it.Kind] = append(byKind[it.Kind], it)
		if it.Kind == "key" && strings.Contains(it.Display, it.value[4:len(it.value)-4]) {
			t.Errorf("key shown in clear: %q", it.Display)
		}
	}
	if len(byKind["url"]) != 2 || byKind["url"][0].value != "https://gateway.example.com" || byKind["url"][1].value != "https://openrouter.ai/api/v1" ||
		byKind["url"][1].Service != "OpenRouter" {
		t.Fatalf("urls %+v", byKind["url"])
	}
	if len(byKind["docs"]) != 1 || byKind["docs"][0].role != RoleVideo {
		t.Fatalf("docs %+v", byKind["docs"])
	}
	keys := byKind["key"]
	if len(keys) != 3 || keys[0].value != gatewayKey || keys[0].Service != "" || keys[1].Service != "ElevenLabs" || keys[1].role != RoleTTS || keys[2].Service != "Google AI Studio" {
		t.Fatalf("keys %+v", keys)
	}
	if m := byKind["model"]; len(m) != 1 || m[0].value != "gemini-3.8-flash" {
		t.Fatalf("models %+v", m)
	}
	if v := byKind["voice"]; len(v) != 1 || v[0].value != "JBFqnCBsd6RMkjVDRZzb" {
		t.Fatalf("voices %+v", v)
	}
	gs := groupItems(items)
	if gs[0].url.value != "https://gateway.example.com" || gs[0].key.value != gatewayKey || gs[0].model.value != "gemini-3.8-flash" {
		t.Fatalf("first group %+v", gs[0])
	}
}

// gatewayServer is an OpenAI-compatible API that lives under /v1 only.
func gatewayServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+gatewayKey {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid key"}}`))
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gemini-3.8-flash"},{"id":"gpt-5"}]}`))
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"not_found"}`))
		}
	}))
	allow(srv)
	t.Cleanup(srv.Close)
	return srv
}

// TestAssistantGateway: the owner's case — an address without /v1, a key, a
// model: the plan's text step works through /v1 and applying it (after a
// backup) stores the working address.
func TestAssistantGateway(t *testing.T) {
	srv := gatewayServer(t)
	env := newEnv(t, nil)
	e := env.e
	text := "ось мій ендпоінт " + srv.URL + " ключ " + gatewayKey + " модель gemini-3.8-flash"
	var steps []string
	plan, err := e.AnalyzeConnections(context.Background(), text, func(s string) { steps = append(steps, s) })
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("steps %+v notes %v", plan.Steps, plan.Notes)
	}
	st := plan.Steps[0]
	if st.Role != RoleText || st.Status != "ok" || !st.On || !strings.Contains(strings.Join(st.Lines, "\n"), "Адреса: "+srv.URL+"/v1") ||
		!strings.Contains(st.Target, "gemini-3.8-flash") {
		t.Fatalf("step %+v", st)
	}
	for _, l := range st.Lines {
		if strings.Contains(l, gatewayKey) {
			t.Fatalf("key in clear: %q", l)
		}
	}
	before := len(env.store.Backups())
	res, err := e.ApplyAssist(plan)
	if err != nil || len(res.Applied) != 1 || res.Applied[0] != "Текст" {
		t.Fatalf("apply %+v %v", res, err)
	}
	if len(env.store.Backups()) != before+1 {
		t.Fatal("no backup")
	}
	c := e.Config().Get()
	if c.Text.Provider != "openai" || c.Text.BaseURL != srv.URL+"/v1" || c.Text.APIKey != gatewayKey || c.Text.Model != "gemini-3.8-flash" {
		t.Fatalf("text %+v", c.Text)
	}
	if ok, msg := e.TestText(context.Background()); !ok {
		t.Fatalf("after apply: %s", msg)
	}
}

// TestAssistantWrongKey: a refused key gives a failed step with the exact
// request, proposed off.
func TestAssistantWrongKey(t *testing.T) {
	srv := gatewayServer(t)
	env := newEnv(t, nil)
	plan, err := env.e.AnalyzeConnections(context.Background(), srv.URL+"/v1 key sk-wrong0123456789abcdefXYZ", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Status != "fail" || plan.Steps[0].On ||
		!strings.Contains(plan.Steps[0].Msg, "GET "+srv.URL+"/v1/models → 401 Unauthorized") || strings.Contains(plan.Steps[0].Msg, "sk-wrong0123456789") {
		t.Fatalf("plan %+v", plan.Steps[0])
	}
	if _, err := env.e.ApplyAssist(plan); err == nil {
		t.Fatal("applied a plan with nothing on")
	}
}

// TestAssistantLLMNeverSeesKeys: an unclear address goes to the text model —
// with every key replaced by [KEYn] and the stored secrets masked.
func TestAssistantLLMNeverSeesKeys(t *testing.T) {
	var mu sync.Mutex
	var prompts []string
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"mock"}]}`))
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		prompts = append(prompts, string(b))
		mu.Unlock()
		reply := `{"urls":[{"ref":"URL1","role":"tts","protocol":"other","key":"KEY1"}],"notes":[]}`
		out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
		_, _ = w.Write(out)
	}))
	allow(llmSrv)
	defer llmSrv.Close()
	unknown := httptest.NewServer(http.NotFoundHandler()) // answers nothing useful
	allow(unknown)
	defer unknown.Close()
	env := newEnv(t, func(c *config.Config) {
		c.Text = config.Text{Provider: "openai", BaseURL: llmSrv.URL + "/v1", APIKey: "stored-text-key-123456", Model: "mock"}
		c.Telegram.BotToken = "123456:stored-telegram-token"
	})
	secret := "zz9Q8w7E6r5T4y3U2i1O0pXX"
	text := "щось " + unknown.URL + "/thing\n" + secret + "\nтелеграм 123456:stored-telegram-token"
	plan, err := env.e.AnalyzeConnections(context.Background(), text, nil)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) == 0 {
		t.Fatal("the text model was not asked")
	}
	for _, p := range prompts {
		for _, k := range []string{secret, "stored-text-key-123456", "stored-telegram-token"} {
			if strings.Contains(p, k) {
				t.Fatalf("prompt leaks %q:\n%s", k, p)
			}
		}
		if !strings.Contains(p, "[KEY1]") || !strings.Contains(p, "[URL1: ") {
			t.Fatalf("prompt without refs:\n%s", p)
		}
	}
	if plan.AI == "" {
		t.Fatalf("plan.AI empty, notes %v", plan.Notes)
	}
}

// TestAssistantKnownKeys: keys of known services become their steps without
// a single probe of an unknown path (Google key alone = a fallback for Text).
func TestAssistantKnownKeys(t *testing.T) {
	srv := gatewayServer(t)
	env := newEnv(t, nil)
	text := "текст: " + srv.URL + "/v1 " + gatewayKey + " модель gpt-5\n" + googleKey
	plan, err := env.e.AnalyzeConnections(context.Background(), text, nil)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]*AssistStep{}
	for _, s := range plan.Steps {
		roles[s.Role] = s
	}
	if s := roles[RoleText]; s == nil || !strings.Contains(s.Target, "gpt-5") || s.Status != "ok" {
		t.Fatalf("text step %+v", s)
	}
	if roles[RoleImage] == nil || roles[RoleVideo] == nil || !strings.Contains(roles[RoleImage].Target, "Google") {
		t.Fatalf("google steps %+v", plan.Steps)
	}
	if roles[RoleImage].On || roles[RoleVideo].On { // G-Labs works here: not switched away silently
		t.Fatalf("google image/video on: %+v %+v", roles[RoleImage], roles[RoleVideo])
	}
	found := false
	for _, n := range plan.Notes {
		found = found || strings.Contains(n, "Текст: є й інший варіант — Google AI Studio")
	}
	if !found {
		t.Fatalf("notes %v", plan.Notes)
	}
}
