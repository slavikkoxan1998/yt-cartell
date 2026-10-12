package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const tinyOpenAPI = `{"openapi":"3.1.0","info":{"title":"Acme","version":"1"},"servers":[{"url":"https://api.acme.test/v1"}],
 "components":{"securitySchemes":{"key":{"type":"apiKey","in":"header","name":"X-API-Key"}}},
 "paths":{"/tasks":{"post":{"summary":"Create a video task"}}}}`

func TestFetchDocsFollowsSpecLink(t *testing.T) {
	m := newMockAPI(t)
	m.on("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<!doctype html><html><body><div id="app"></div><script id="api-reference" data-url="/static/openapi.json"></script></body></html>`)
	})
	m.on("GET /static/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("docs requests carry no credentials")
		}
		writeJSON(w, 200, tinyOpenAPI)
	})
	var steps []string
	d, err := FetchDocs(context.Background(), m.srv.URL+"/docs", func(s string) { steps = append(steps, s) })
	if err != nil || d.Kind != "openapi" || !strings.Contains(d.Text, `"X-API-Key"`) || d.Source != m.srv.URL+"/static/openapi.json" {
		t.Fatalf("docs: %v %+v", err, d)
	}
	if len(steps) < 2 || !strings.Contains(strings.Join(steps, "|"), "Знайшов опис API") {
		t.Fatalf("progress %v", steps)
	}
}

func TestFetchDocsWellKnownAndLLMS(t *testing.T) {
	m := newMockAPI(t)
	page := `<html><body><h1>Acme API</h1><p>Welcome.</p></body></html>`
	m.on("GET /reference", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, page)
	})
	m.on("GET /llms-full.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "# Acme\n\nPOST /v1/videos creates a video. "+strings.Repeat("Details. ", 40))
	})
	d, err := FetchDocs(context.Background(), m.srv.URL+"/reference", nil)
	if err != nil || d.Kind != "llms" || !strings.Contains(d.Text, "POST /v1/videos") {
		t.Fatalf("llms: %v %+v", err, d)
	}
	// Nothing machine-readable and almost no text: ask to paste the docs.
	m2 := newMockAPI(t)
	m2.on("GET /app", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<html><head><script>render()</script></head><body><div id=root></div></body></html>`)
	})
	if _, err := FetchDocs(context.Background(), m2.srv.URL+"/app", nil); err == nil || !strings.Contains(err.Error(), "встав його в поле") {
		t.Fatalf("js page: %v", err)
	}
	// A readable HTML page becomes text (code kept, scripts dropped).
	m3 := newMockAPI(t)
	body := `<html><body><h2>Create image</h2><p>Send <code>POST /v1/images</code> with JSON:</p><pre>{"prompt": "a cat"}</pre>` +
		`<script>var secret = 1</script><ul><li>prompt — text</li><li>size — 1024x1024</li></ul>` + strings.Repeat("<p>More words here.</p>", 30) + `</body></html>`
	m3.on("GET /guide", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, body)
	})
	d, err = FetchDocs(context.Background(), m3.srv.URL+"/guide", nil)
	if err != nil || d.Kind != "html" || !strings.Contains(d.Text, "## Create image") || !strings.Contains(d.Text, `{"prompt": "a cat"}`) ||
		!strings.Contains(d.Text, "- size — 1024x1024") || strings.Contains(d.Text, "secret") {
		t.Fatalf("html: %v %q", err, clip(d.Text, 400))
	}
	if _, err := FetchDocs(context.Background(), "ftp://x", nil); err == nil {
		t.Fatal("ftp accepted")
	}
	m4 := newMockAPI(t)
	if _, err := FetchDocs(context.Background(), m4.srv.URL+"/missing", nil); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("404: %v", err)
	}
}

func TestPrepareDocsShortens(t *testing.T) {
	// A big OpenAPI document is summarised, generation paths first.
	paths := map[string]any{}
	for i := 0; i < 400; i++ {
		paths[fmt.Sprintf("/admin/thing%03d", i)] = map[string]any{"get": map[string]any{"summary": strings.Repeat("Admin listing. ", 30)}}
	}
	paths["/v1/videos/generate"] = map[string]any{"post": map[string]any{
		"summary": "Generate a video",
		"requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{
			"schema": map[string]any{"$ref": "#/components/schemas/VideoIn"}}}},
		"responses": map[string]any{"201": map[string]any{"description": "Created", "content": map[string]any{"application/json": map[string]any{
			"schema": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}}}}}},
	}}
	doc := map[string]any{"openapi": "3.0.0", "info": map[string]any{"title": "Big"}, "paths": paths,
		"components": map[string]any{"schemas": map[string]any{"VideoIn": map[string]any{"type": "object", "required": []any{"prompt"},
			"properties": map[string]any{"prompt": map[string]any{"type": "string"}, "ratio": map[string]any{"type": "string", "enum": []any{"16:9", "9:16"}}}}}}}
	raw, _ := json.Marshal(doc)
	if len(raw) <= MaxDocsChars {
		t.Fatalf("fixture too small: %d", len(raw))
	}
	d := PrepareDocs(string(raw))
	gen := strings.Index(d.Text, "POST /v1/videos/generate")
	if d.Kind != "openapi" || len(d.Text) > MaxDocsChars || gen < 0 || gen > 2000 ||
		!strings.Contains(d.Text, "prompt: string (required)") || !strings.Contains(d.Text, "enum[16:9,9:16]") || !strings.Contains(d.Text, "Reply 201") {
		t.Fatalf("summary (%d chars, gen at %d): %s", len(d.Text), gen, clip(d.Text, 600))
	}
	// Long prose keeps the generation parts.
	var b strings.Builder
	b.WriteString("Intro: base URL https://api.x.test, auth with Bearer.\n\n")
	for i := 0; i < 3000; i++ {
		b.WriteString("Company history and unrelated marketing words go here.\n\n")
	}
	b.WriteString("POST /v1/generate — send {\"prompt\": \"…\", \"aspect_ratio\": \"16:9\"}; the response has a job id and status.\n\n")
	text := PrepareDocs(b.String())
	if len(text.Text) > MaxDocsChars || !strings.Contains(text.Text, "Intro: base URL") || !strings.Contains(text.Text, "POST /v1/generate") || text.Note == "" {
		t.Fatalf("prose: %d %q", len(text.Text), text.Note)
	}
	if d := PrepareDocs("short docs"); d.Text != "short docs" || d.Note != "" {
		t.Fatalf("short: %+v", d)
	}
}

func TestFormatSpecKeepsOrder(t *testing.T) {
	in := `{"submit":{"method":"POST","path":"/x","body":{"prompt":"{{prompt}}","input":{"a":1}}},"id":"id","poll":{"path":"/x/{id}"},"state":"s","done":["ok"],"result":"u"}`
	out := FormatSpec([]byte(in))
	if !strings.HasPrefix(out, "{\n  \"submit\": {\n") || !strings.Contains(out, `"done": ["ok"]`) || !strings.Contains(out, `"poll": {"path": "/x/{id}"}`) ||
		strings.Index(out, `"submit"`) > strings.Index(out, `"id"`) {
		t.Fatalf("format:\n%s", out)
	}
	if _, err := ParseSpec(out); err != nil {
		t.Fatalf("formatted spec invalid: %v", err)
	}
	if FormatSpec([]byte("{broken")) != "{broken" {
		t.Fatal("broken JSON must come back unchanged")
	}
	if p := SpecPrompt(true); !strings.Contains(p, "{{start_image}}") || !strings.Contains(p, "NEVER put the API key") || !strings.Contains(p, "EXAMPLE 2") {
		t.Fatal("video prompt")
	}
}
