package providers

import (
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

const openaiKey = "sk-test-0123456789abcdefghij"

func TestOpenAIGenerationsJSON(t *testing.T) {
	m := newMockAPI(t)
	m.on("POST /v1/images/generations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(pngBytes)}}, "output_format": "png"})
	})
	c := customClient(func(c *config.Config) {
		c.Custom = config.CustomAPI{ImagePreset: PresetOpenAI, ImageBaseURL: m.srv.URL + "/v1", ImageKey: openaiKey, ImageModel: "gpt-image-1.5"}
	})
	data, mt, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "a cat", Aspect: "9:16"})
	if err != nil || mt != "image/png" || string(data) != string(pngBytes) {
		t.Fatalf("image: %d %q %v", len(data), mt, err)
	}
	reqs := m.requests("POST /v1/images/generations")
	if len(reqs) != 1 {
		t.Fatalf("requests: %d", len(reqs))
	}
	body := reqs[0].json(t)
	if body["model"] != "gpt-image-1.5" || body["prompt"] != "a cat" || body["size"] != "1024x1536" || body["n"] != float64(1) {
		t.Errorf("body = %v", body)
	}
	if _, ok := body["response_format"]; ok {
		t.Error("GPT Image rejects response_format")
	}
	if got := reqs[0].header.Get("Authorization"); got != "Bearer "+openaiKey {
		t.Errorf("auth = %q", got)
	}
	noKeyLeak(t, m, openaiKey, "Authorization")
}

func TestOpenAIEditsMultipartRefs(t *testing.T) {
	m := newMockAPI(t)
	var parts []string
	var fields = map[string]string{}
	m.on("POST /v1/images/edits", func(w http.ResponseWriter, r *http.Request) {
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("content type: %v", err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			if p.FileName() != "" {
				parts = append(parts, p.FormName()+"="+p.FileName()+":"+p.Header.Get("Content-Type"))
				continue
			}
			fields[p.FormName()] = string(b)
		}
		writeJSON(w, 200, `{"data":[{"b64_json":"`+base64.StdEncoding.EncodeToString(jpegBytes)+`"}],"output_format":"jpeg"}`)
	})
	c := customClient(func(c *config.Config) {
		c.Custom = config.CustomAPI{ImagePreset: PresetOpenAI, ImageBaseURL: m.srv.URL + "/v1", ImageKey: openaiKey, ImageModel: "gpt-image-1"}
	})
	refs := []gen.RefImage{{Data: pngBytes, MIME: "image/png", Name: "taras.png"}, {Data: jpegBytes, Name: "kyiv.jpg"}, {}}
	data, mt, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "@taras in @kyiv", Aspect: "16:9", Refs: refs})
	if err != nil || mt != "image/jpeg" || len(data) == 0 {
		t.Fatalf("edit: %q %v", mt, err)
	}
	want := []string{"image[]=ref-1.png:image/png", "image[]=ref-2.jpg:image/jpeg"}
	if strings.Join(parts, "|") != strings.Join(want, "|") {
		t.Errorf("files = %v, want %v", parts, want)
	}
	if fields["model"] != "gpt-image-1" || fields["size"] != "1536x1024" || fields["input_fidelity"] != "high" {
		t.Errorf("fields = %v", fields)
	}
	if !strings.Contains(fields["prompt"], "image 1 = @taras, image 2 = @kyiv") {
		t.Errorf("prompt legend missing: %q", fields["prompt"])
	}
}

func TestOpenAICompatURLResultAndNoKey(t *testing.T) {
	m := newMockAPI(t)
	m.on("POST /api/images/generations", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("no key configured → no auth header")
		}
		writeJSON(w, 200, `{"data":[{"url":"/files/out.png"}]}`)
	})
	m.on("GET /api/files/out.png", serveBytes("image/png", pngBytes))
	m.on("GET /files/out.png", serveBytes("image/png", pngBytes))
	m.on("GET /api/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"data":[{"id":"flux-local"}]}`)
	})
	c := customClient(func(c *config.Config) {
		c.Custom = config.CustomAPI{ImagePreset: PresetOpenAICompat, ImageBaseURL: m.srv.URL + "/api/", ImageModel: "flux-local"}
	})
	data, mt, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "x", Aspect: "1:1"})
	if err != nil || mt != "image/png" || len(data) == 0 {
		t.Fatalf("compat: %q %v", mt, err)
	}
	ok, msg := c.TestImage(context.Background())
	if !ok || !strings.Contains(msg, "сервер відповідає") || !strings.Contains(msg, "flux-local") {
		t.Errorf("test = %v %q", ok, msg)
	}
}

func TestOpenAITestAndErrors(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	m.on("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+openaiKey {
			writeJSON(w, 401, `{"error":{"message":"Incorrect API key provided: sk-bad-0123456789abcdefghij","type":"invalid_request_error","code":"invalid_api_key"}}`)
			return
		}
		writeJSON(w, 200, `{"object":"list","data":[{"id":"gpt-image-1"},{"id":"gpt-image-1.5"}]}`)
	})
	m.on("POST /v1/images/generations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 400, `{"error":{"message":"Your request was rejected as a result of our safety system.","code":"moderation_blocked"}}`)
	})
	mk := func(key, model string) *Client {
		return customClient(func(c *config.Config) {
			c.Custom = config.CustomAPI{ImagePreset: PresetOpenAI, ImageBaseURL: m.srv.URL + "/v1", ImageKey: key, ImageModel: model}
		})
	}
	ok, msg := mk(openaiKey, "gpt-image-1.5").TestImage(context.Background())
	if !ok || !strings.Contains(msg, "ключ працює") {
		t.Errorf("good key: %v %q", ok, msg)
	}
	ok, msg = mk(openaiKey, "gpt-image-9").TestImage(context.Background())
	if !ok || !strings.Contains(msg, "не знайдена") {
		t.Errorf("unknown model: %v %q", ok, msg)
	}
	ok, msg = mk("sk-bad-0123456789abcdefghij", "gpt-image-1").TestImage(context.Background())
	if ok || !strings.Contains(msg, "401") || strings.Contains(msg, "sk-bad-0123456789") {
		t.Errorf("bad key: %v %q", ok, msg)
	}
	_, _, err := mk(openaiKey, "gpt-image-1").Image(context.Background(), gen.ImageRequest{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "фільтром безпеки") {
		t.Errorf("moderation: %v", err)
	}
	if len(m.requests("POST /v1/images/generations")) != 1 {
		t.Error("a rejected generation must not be retried")
	}
	_, _, err = mk("", "gpt-image-1").Image(context.Background(), gen.ImageRequest{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "API-ключ") {
		t.Errorf("no key: %v", err)
	}
}

func TestOpenAISize(t *testing.T) {
	cases := map[[2]string]string{
		{"gpt-image-1", "16:9"}: "1536x1024", {"gpt-image-1", "9:16"}: "1024x1536", {"gpt-image-1", "1:1"}: "1024x1024",
		{"gpt-image-1", "3:4"}: "1024x1536", {"gpt-image-1", ""}: "auto", {"flux-local", "16:9"}: "1536x1024",
		{"gpt-image-2.5-flare", "16:9"}: "1536x864", {"gpt-image-2", "9:16"}: "864x1536", {"gpt-image-2.5-sunburst", "4:3"}: "1024x768",
		{"gpt-image-2.5-flare", "3:4"}: "768x1024", {"gpt-image-2.5-flare", "1:1"}: "1024x1024",
	}
	for in, want := range cases {
		if got := openaiSize(in[0], in[1]); got != want {
			t.Errorf("openaiSize(%v) = %q, want %q", in, got, want)
		}
	}
}
