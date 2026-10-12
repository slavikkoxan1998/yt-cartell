package providers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

const replicateToken = "r8_TestToken0123456789abcdef"

func replicateClient(base, model string, video bool) *Client {
	return customClient(func(c *config.Config) {
		if video {
			c.Custom.VideoPreset, c.Custom.VideoBaseURL, c.Custom.VideoKey, c.Custom.VideoModel = PresetReplicate, base, replicateToken, model
		} else {
			c.Custom.ImagePreset, c.Custom.ImageBaseURL, c.Custom.ImageKey, c.Custom.ImageModel = PresetReplicate, base, replicateToken, model
		}
	})
}

const nanoReplicateSchema = `{"latest_version":{"id":"abc","openapi_schema":{"components":{"schemas":{
 "Input":{"type":"object","required":["prompt"],"properties":{
  "prompt":{"type":"string"},
  "image_input":{"type":"array","items":{"type":"string","format":"uri"},"default":[]},
  "aspect_ratio":{"allOf":[{"$ref":"#/components/schemas/aspect_ratio"}],"default":"match_input_image"},
  "output_format":{"allOf":[{"$ref":"#/components/schemas/output_format"}],"default":"jpg"}}},
 "aspect_ratio":{"type":"string","enum":["match_input_image","1:1","16:9","9:16","3:4","4:3"]},
 "output_format":{"type":"string","enum":["jpg","png"]}}}}}}`

func TestReplicateImagePreferWaitUploadsAndSchema(t *testing.T) {
	fast(t)
	resetSchemas()
	t.Cleanup(resetSchemas)
	api, delivery := newMockAPI(t), newMockAPI(t)
	api.on("GET /v1/models/google/nano-banana-2", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, nanoReplicateSchema)
	})
	var uploaded []string
	api.on("POST /v1/files", func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			if p.FormName() == "content" {
				uploaded = append(uploaded, fmt.Sprintf("%s:%s:%d", p.FileName(), p.Header.Get("Content-Type"), len(b)))
			}
		}
		writeJSON(w, 201, `{"id":"f1","urls":{"get":"https://api.replicate.com/v1/files/f1"}}`)
	})
	api.on("POST /v1/models/google/nano-banana-2/predictions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Prefer") != "wait=60" {
			t.Errorf("Prefer = %q", r.Header.Get("Prefer"))
		}
		writeJSON(w, 201, fmt.Sprintf(`{"id":"p1","status":"processing","output":"%s/out.jpg","urls":{"get":"https://evil.example/steal"}}`, delivery.srv.URL))
	})
	delivery.on("GET /out.jpg", serveBytes("image/jpeg", jpegBytes))

	big := append(append([]byte(nil), pngBytes...), bytes.Repeat([]byte{1}, replicateInlineMax)...)
	refs := []gen.RefImage{{Data: jpegBytes, MIME: "image/jpeg", Name: "taras.jpg"}, {Data: big, MIME: "image/png", Name: "kyiv.png"}}
	c := replicateClient(api.srv.URL+"/v1", "google/nano-banana-2", false)
	data, mt, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "@taras in @kyiv", Aspect: "16:9", Refs: refs})
	if err != nil || mt != "image/jpeg" || string(data) != string(jpegBytes) {
		t.Fatalf("image: %q %v", mt, err)
	}
	body := api.requests("POST /v1/models/google/nano-banana-2/predictions")[0].json(t)
	input, _ := body["input"].(map[string]any)
	imgs, _ := input["image_input"].([]any)
	if len(imgs) != 2 || !strings.HasPrefix(fmt.Sprint(imgs[0]), "data:image/jpeg;base64,") || imgs[1] != "https://api.replicate.com/v1/files/f1" {
		t.Errorf("image_input = %v", imgs)
	}
	if input["aspect_ratio"] != "16:9" || !strings.Contains(fmt.Sprint(input["prompt"]), "image 2 = @kyiv") {
		t.Errorf("input = %v", input)
	}
	if len(uploaded) != 1 || !strings.HasPrefix(uploaded[0], "ref-2.png:image/png:") {
		t.Errorf("uploads = %v", uploaded)
	}
	if len(delivery.requests("GET /out.jpg")) != 1 || delivery.requests("GET /out.jpg")[0].header.Get("Authorization") != "" {
		t.Error("the output is downloaded without the token")
	}
	if len(api.requests("GET /v1/predictions")) != 0 {
		t.Error("output already there: no polling")
	}
	noKeyLeak(t, api, replicateToken, "Authorization")
}

func TestReplicateVideoVersionPollProgressCancel(t *testing.T) {
	fast(t)
	resetSchemas()
	t.Cleanup(resetSchemas)
	m := newMockAPI(t)
	model := "kwaivgi/kling-v2.1:0123456789abcdef"
	m.on("GET /v1/models/kwaivgi/kling-v2.1/versions/0123456789abcdef", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, `{"detail":"Not found"}`) // falls back to the built-in schema
	})
	m.on("POST /v1/predictions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 201, `{"id":"pv","status":"starting"}`)
	})
	var polls atomic.Int32
	var hang atomic.Bool
	m.on("GET /v1/predictions/pv", func(w http.ResponseWriter, r *http.Request) {
		n := polls.Add(1)
		switch {
		case hang.Load() || n == 1:
			writeJSON(w, 200, `{"id":"pv","status":"processing","logs":"step 1\n 40%|####      | 4/10\n"}`)
		default:
			writeJSON(w, 200, fmt.Sprintf(`{"id":"pv","status":"succeeded","output":["%s/v.mp4"]}`, m.srv.URL))
		}
	})
	m.on("GET /v.mp4", serveBytes("video/mp4", mp4Bytes))
	m.on("POST /v1/predictions/pv/cancel", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"id":"pv","status":"canceled"}`) })

	c := replicateClient(m.srv.URL+"/v1", model, true)
	var seen []int
	clip, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "move", Start: startFrame(), Seconds: 8, Aspect: "16:9"}, func(p int, _ string) { seen = append(seen, p) })
	if err != nil || string(clip) != string(mp4Bytes) {
		t.Fatalf("video: %v", err)
	}
	body := m.requests("POST /v1/predictions")[0].json(t)
	input, _ := body["input"].(map[string]any)
	if body["version"] != "0123456789abcdef" || input["duration"] != float64(10) || !strings.HasPrefix(fmt.Sprint(input["start_image"]), "data:image/jpeg") {
		t.Errorf("body = %v", body)
	}
	if !containsInt(seen, 48) { // 40% from the logs → 20 + 0.7·40
		t.Errorf("progress = %v", seen)
	}
	hang.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	_, err = c.Video(ctx, gen.VideoRequest{Prompt: "move", Start: startFrame(), Seconds: 5}, func(p int, phase string) {
		if phase == "rendering" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || len(m.requests("POST /v1/predictions/pv/cancel")) != 1 {
		t.Errorf("cancel: %v", err)
	}
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestReplicateErrors(t *testing.T) {
	fast(t)
	resetSchemas()
	t.Cleanup(resetSchemas)
	m := newMockAPI(t)
	var mode atomic.Int32
	var creates atomic.Int32
	m.on("POST /v1/models/google/veo-3.1-fast/predictions", func(w http.ResponseWriter, r *http.Request) {
		creates.Add(1)
		switch mode.Load() {
		case 0:
			writeJSON(w, 201, `{"id":"e1","status":"starting"}`)
		case 1:
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(402)
			io.WriteString(w, `{"title":"Payment Required","detail":"Insufficient credit","status":402}`)
		case 2:
			if creates.Load()%2 == 1 {
				w.Header().Set("Retry-After", "0")
				writeJSON(w, 429, `{"detail":"Request was throttled. Your rate limit resets in ~1s."}`)
				return
			}
			writeJSON(w, 201, fmt.Sprintf(`{"id":"e2","status":"succeeded","output":"%s/v.mp4"}`, m.srv.URL))
		}
	})
	m.on("GET /v1/predictions/e1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"id":"e1","status":"failed","error":"The input or output was flagged as sensitive. Please try again with different inputs. (E005)"}`)
	})
	m.on("GET /v.mp4", serveBytes("video/mp4", mp4Bytes))
	c := replicateClient(m.srv.URL+"/v1", "google/veo-3.1-fast", true)
	req := gen.VideoRequest{Prompt: "p", Start: startFrame(), Seconds: 6}
	if _, err := c.Video(context.Background(), req, nil); err == nil || !strings.Contains(err.Error(), "фільтром безпеки") {
		t.Errorf("E005: %v", err)
	}
	mode.Store(1)
	if _, err := c.Video(context.Background(), req, nil); err == nil || !strings.Contains(err.Error(), "закінчились кошти") {
		t.Errorf("402: %v", err)
	}
	mode.Store(2)
	creates.Store(0)
	if _, err := c.Video(context.Background(), req, nil); err != nil || creates.Load() != 2 {
		t.Errorf("429 then success: %v (creates %d)", err, creates.Load())
	}
}

func TestReplicateTest(t *testing.T) {
	m := newMockAPI(t)
	m.on("GET /v1/account", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+replicateToken {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(401)
			io.WriteString(w, `{"title":"Unauthenticated","detail":"You did not pass a valid authentication token","status":401}`)
			return
		}
		writeJSON(w, 200, `{"type":"user","username":"ivan","name":"Ivan"}`)
	})
	m.on("GET /v1/models/google/veo-3.1-fast", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"name":"veo-3.1-fast"}`) })
	m.on("GET /v1/models/google/nope", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 404, `{"detail":"Not found."}`) })
	ok, msg := replicateClient(m.srv.URL+"/v1", "google/veo-3.1-fast", true).TestVideo(context.Background())
	if !ok || !strings.Contains(msg, "акаунт ivan") {
		t.Errorf("good: %v %q", ok, msg)
	}
	ok, msg = replicateClient(m.srv.URL+"/v1", "google/nope", true).TestVideo(context.Background())
	if ok || !strings.Contains(msg, "не знайдена") {
		t.Errorf("unknown model: %v %q", ok, msg)
	}
	bad := customClient(func(c *config.Config) {
		c.Custom = config.CustomAPI{VideoPreset: PresetReplicate, VideoBaseURL: m.srv.URL + "/v1", VideoKey: "r8_wrongwrongwrongwrong"}
	})
	ok, msg = bad.TestVideo(context.Background())
	if ok || !strings.Contains(msg, "401") || strings.Contains(msg, "r8_wrong") {
		t.Errorf("bad token: %v %q", ok, msg)
	}
	if len(m.requests("POST")) != 0 {
		t.Error("a key check must never create a prediction")
	}
}

func TestReplicateHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"owner/name":           "/models/owner/name/predictions",
		"owner/name:abc":       "/predictions",
		"5c7d5dc6dd8bf75c1acb": "/predictions",
	} {
		if p, _, _ := replicateCreate(in, nil); p != want {
			t.Errorf("replicateCreate(%q) = %q, want %q", in, p, want)
		}
	}
	if got := logPercent("a\n 12%|#\r 57%|####   | 4/7\n"); got != 57 {
		t.Errorf("logPercent = %d", got)
	}
	if got := logPercent("no progress"); got != -1 {
		t.Errorf("logPercent = %d", got)
	}
	if got := outputURLs([]byte(`[{"url":"a"},"b"]`)); len(got) != 2 || got[0] != "a" {
		t.Errorf("outputURLs = %v", got)
	}
}
