package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/gemini"
)

func TestVisionOpenAI(t *testing.T) {
	var mu sync.Mutex
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = nil
		_ = json.Unmarshal(b, &body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(completion("A boy in a red jacket."))
	}))
	defer srv.Close()
	c := New(openaiCfg(srv.URL+"/v1", "k"), nil)
	png := []byte("\x89PNG\r\n\x1a\nfake")
	out, err := c.Text(context.Background(), Request{System: "Describe.", Prompt: "Who is this?", Images: []Image{{MIME: "image/png", Data: png}}})
	if err != nil || out != "A boy in a red jacket." {
		t.Fatalf("%q %v", out, err)
	}
	mu.Lock()
	first := body
	mu.Unlock()
	msgs, _ := first["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages %v", body["messages"])
	}
	parts, _ := msgs[0].(map[string]any)["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("content parts %v", msgs[0])
	}
	text, _ := parts[0].(map[string]any)
	img, _ := parts[1].(map[string]any)
	url, _ := img["image_url"].(map[string]any)
	if text["type"] != "text" || text["text"] != "### INSTRUCTIONS\nDescribe.\n\n### INPUT\nWho is this?" ||
		img["type"] != "image_url" || url["url"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png) {
		t.Fatalf("parts %v", parts)
	}

	// Without pictures the request stays a plain string message.
	if _, err := c.Text(context.Background(), Request{Prompt: "Hi"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	msgs, _ = body["messages"].([]any)
	mu.Unlock()
	if s, ok := msgs[0].(map[string]any)["content"].(string); !ok || s != "Hi" {
		t.Fatalf("plain request %v", msgs[0])
	}
}

func TestVisionGoogle(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		keys = append(keys, r.Header.Get("x-goog-api-key"))
		_ = json.Unmarshal(b, &body)
		mu.Unlock()
		if r.Header.Get("x-goog-api-key") == "spent" { // quota: the pool moves on
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/models/gemini-vision:generateContent") {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"thinking","thought":true},{"text":"An old oak."}]}}]}`)
	}))
	defer srv.Close()
	old := googleBase
	googleBase = srv.URL
	defer func() { googleBase = old }()
	get := func() config.Config {
		c := config.Default()
		c.Text.Provider = "google"
		c.Google.StoryModel = "gemini-vision"
		c.Google.Keys = []string{"spent", "good"}
		return c
	}
	c := New(get, gemini.New(get))
	jpg := []byte("\xff\xd8\xff fake jpeg")
	out, err := c.Text(context.Background(), Request{System: "Describe the place.", Prompt: "What is it?", Images: []Image{{MIME: "image/jpeg", Data: jpg}}})
	if err != nil || out != "An old oak." {
		t.Fatalf("%q %v", out, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(keys, ",") != "spent,good" {
		t.Fatalf("keys %v", keys)
	}
	contents, _ := body["contents"].([]any)
	parts, _ := contents[0].(map[string]any)["parts"].([]any)
	inline, _ := parts[1].(map[string]any)["inlineData"].(map[string]any)
	sys, _ := body["systemInstruction"].(map[string]any)
	if parts[0].(map[string]any)["text"] != "What is it?" || inline["mimeType"] != "image/jpeg" ||
		inline["data"] != base64.StdEncoding.EncodeToString(jpg) || sys == nil {
		t.Fatalf("google body %v", body)
	}
	if dig := body["generationConfig"].(map[string]any); dig["temperature"] == nil {
		t.Fatalf("a model without a Gemini 3 id keeps its temperature: %v", body["generationConfig"])
	}
	// No Google client: a clear error, nothing sent.
	if _, err := New(get, nil).Text(context.Background(), Request{Prompt: "x", Images: []Image{{Data: jpg}}}); err == nil {
		t.Fatal("vision without a Google client")
	}
}

// Gemini 3: "Strip temperature, top_p, and top_k" — vision requests (Google
// and OpenAI-compatible) carry none.
func TestVisionGemini3NoTemperature(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		if strings.Contains(r.URL.Path, ":generateContent") {
			io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`)
			return
		}
		_ = json.NewEncoder(w).Encode(completion("ok"))
	}))
	defer srv.Close()
	old := googleBase
	googleBase = srv.URL
	defer func() { googleBase = old }()
	get := func() config.Config {
		c := config.Default()
		c.Text.Provider = "google"
		c.Google.StoryModel = "gemini-3.8-flash"
		c.Google.Keys = []string{"k"}
		return c
	}
	img := []Image{{MIME: "image/png", Data: []byte("\x89PNG\r\n\x1a\nfake")}}
	if _, err := New(get, gemini.New(get)).Text(context.Background(), Request{Prompt: "x", Temperature: 0.3, Images: img}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(openaiCfg(srv.URL+"/v1", "k"), nil).Text(context.Background(), Request{Prompt: "x", Temperature: 0.3, Images: img}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if bodies[0]["generationConfig"] != nil {
		t.Fatalf("google: %v", bodies[0]["generationConfig"])
	}
	if _, ok := bodies[1]["temperature"]; ok {
		t.Fatalf("openai-compatible gemini-3.8-flash: %v", bodies[1])
	}
}
