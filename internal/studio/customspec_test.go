package studio

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/providers"
)

const (
	specVideoKey = "vk-live-SECRET-0123456789"
	specTextKey  = "txt-test" // the profile mock's own key
)

// A valid answer: async jobs, in a fence with prose around it.
const specReplyOK = "Here you go:\n```json\n" + `{"spec": {"name": "Acme", "auth": "header:X-API-Key",
 "submit": {"path": "/tasks", "body": {"model": "{{model}}", "prompt": "{{prompt}}", "image": "{{start_image}}", "duration": "{{seconds}}"}},
 "id": "task_id", "poll": {"path": "/tasks/{id}", "every": 10}, "state": "status", "done": ["SUCCEEDED"], "failed": ["FAILED"],
 "result": "output.url", "error": "error", "caps": {"lengths": [5, 10], "aspects": ["16:9", "9:16"]}},
 "baseUrl": "https://api.acme.test/v1/", "model": "acme-v2", "notes": ["Статус «RUNNING» вгадано — перевір.", "Ліміти невідомі."]}` + "\n```"

// docsServer serves an HTML page that links its OpenAPI document; the
// document mentions the user's key (it must never reach the model).
func docsServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/docs":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<html><body><redoc spec-url="/openapi.json"></redoc></body></html>`)
		case "/openapi.json":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"openapi":"3.0.0","info":{"title":"Acme","description":"Example key: `+specVideoKey+`"},
			  "servers":[{"url":"https://api.acme.test/v1"}],"paths":{"/tasks":{"post":{"summary":"Create a video task"}},
			  "/tasks/{id}":{"get":{"summary":"Task status: PENDING, RUNNING, SUCCEEDED, FAILED"}}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	allow(srv)
	t.Cleanup(srv.Close)
	return srv
}

func TestSuggestCustomSpecFromDocsURL(t *testing.T) {
	env, pm := newProfileEnv(t, specReplyOK)
	_ = env.store.Update(func(c *config.Config) {
		c.Custom.VideoKey, c.Custom.VideoModel = specVideoKey, "acme-v1"
	})
	docs := docsServer(t)
	var steps []string
	s, err := env.e.SuggestCustomSpec(context.Background(), true, docs.URL+"/docs", func(m string) { steps = append(steps, m) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := providers.ParseSpec(s.Spec); err != nil || len(s.Problems) != 0 {
		t.Fatalf("spec invalid: %v %v\n%s", err, s.Problems, s.Spec)
	}
	if s.BaseURL != "https://api.acme.test/v1" || s.Model != "acme-v2" || s.Source != docs.URL+"/openapi.json" {
		t.Fatalf("suggestion %+v", s)
	}
	if len(s.Notes) != 3 || !strings.Contains(s.Notes[0], "OpenAPI") || !strings.Contains(s.Notes[1], "RUNNING") {
		t.Fatalf("notes %v", s.Notes)
	}
	if !strings.HasPrefix(s.Spec, "{\n  \"name\": \"Acme\"") || !strings.Contains(s.Spec, `"done": ["SUCCEEDED"]`) {
		t.Fatalf("layout:\n%s", s.Spec)
	}
	calls := pm.calls()
	if len(calls) != 1 {
		t.Fatalf("%d model calls", len(calls))
	}
	sent := calls[0]
	if strings.Contains(sent, specVideoKey) || !strings.Contains(sent, "Create a video task") || !strings.Contains(sent, "SPEC REFERENCE") ||
		!strings.Contains(sent, `model field says "acme-v1"`) {
		t.Fatalf("prompt (key masked, docs + reference in): %s", clipRunes(sent, 600))
	}
	if len(steps) < 3 {
		t.Fatalf("progress %v", steps)
	}
	// Nothing was saved.
	if c := env.store.Get().Custom; c.VideoSpec != "" || c.VideoModel != "acme-v1" {
		t.Fatalf("settings changed: %+v", c)
	}
}

func TestSuggestCustomSpecRepairsOnce(t *testing.T) {
	bad := `{"spec": {"submit": {"path": "/img", "body": {"text": "{{prompt}}"}}, "id": "id", "result": "url"}, "baseUrl": "https://x.test", "model": "", "notes": []}`
	good := `{"spec": {"submit": {"path": "/img", "body": {"text": "{{prompt}}"}}, "id": "id", "poll": {"path": "/img/{id}"}, "state": "s", "done": ["ok"], "result": "url"}, "notes": ["Виправлено."]}`
	env, pm := newProfileEnv(t, bad, good)
	s, err := env.e.SuggestCustomSpec(context.Background(), false, strings.Repeat("POST /img with {\"text\": \"…\"} returns {\"id\"}; GET /img/{id} returns {\"s\": \"ok\", \"url\": \"…\"}. ", 3), nil)
	if err != nil || len(s.Problems) != 0 || s.BaseURL != "https://x.test" || len(pm.calls()) != 2 {
		t.Fatalf("repair: %v %+v (%d calls)", err, s, len(pm.calls()))
	}
	if second := pm.calls()[1]; !strings.Contains(second, "failed validation") || !strings.Contains(second, "poll: потрібен") {
		t.Fatalf("the problems were not sent back: %s", clipRunes(second, 400))
	}
	// Still broken after the repair: returned for review with its problems.
	env, _ = newProfileEnv(t, bad, bad)
	s, err = env.e.SuggestCustomSpec(context.Background(), false, strings.Repeat("Docs about POST /img and the status endpoint. ", 4), nil)
	if err != nil || len(s.Problems) == 0 || s.Spec == "" {
		t.Fatalf("unrepaired: %v %+v", err, s)
	}
}

func TestSuggestCustomSpecErrors(t *testing.T) {
	env, _ := newProfileEnv(t, "I cannot help with that.", "Still no JSON.")
	if _, err := env.e.SuggestCustomSpec(context.Background(), true, "  ", nil); err == nil || !strings.Contains(err.Error(), "встав посилання") {
		t.Fatalf("empty: %v", err)
	}
	if _, err := env.e.SuggestCustomSpec(context.Background(), true, "too short", nil); err == nil || !strings.Contains(err.Error(), "замало тексту") {
		t.Fatalf("short: %v", err)
	}
	if _, err := env.e.SuggestCustomSpec(context.Background(), true, strings.Repeat("POST /video creates a clip. ", 10), nil); err == nil || !strings.Contains(err.Error(), "не схожа на JSON") {
		t.Fatalf("prose: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := env.e.SuggestCustomSpec(ctx, true, "https://docs.example.test/api", nil); err == nil {
		t.Fatal("cancelled: want an error")
	}
}

func TestVideoCapsUniversal(t *testing.T) {
	var c config.Config
	c.Video.Provider = providerCustom
	c.Custom.VideoPreset = providers.PresetUniversal
	c.Custom.VideoSpec = `{"submit":{"path":"/v","body":{"p":"{{prompt}}","r":"{{refs}}"}},"result":"u","caps":{"lengths":[4,12],"maxRefs":5}}`
	caps := VideoCapsFor(c)
	if len(caps.Lengths) != 2 || caps.Lengths[1] != 12 || caps.MaxRefs != 5 || !caps.Components {
		t.Fatalf("universal caps %+v", caps)
	}
	if OmniFlash(c) {
		t.Fatal("a universal API is not Omni Flash")
	}
	c.Custom.VideoSpec = ""
	if caps := VideoCapsFor(c); len(caps.Lengths) != 2 || caps.Lengths[0] != 5 {
		t.Fatalf("defaults %+v", caps)
	}
}
