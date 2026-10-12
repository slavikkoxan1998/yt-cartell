package providers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

const falKey = "0f0e0d0c-1111-2222-3333-444455556666:00112233445566778899aabbccddeeff"

// falHosts points the platform and storage APIs at the mock for a test.
func falHosts(t *testing.T, platform, storage string) {
	t.Helper()
	p, s := falPlatformFor, falStorageFor
	falPlatformFor = func(string) string { return platform }
	falStorageFor = func(string) string { return storage }
	resetSchemas()
	t.Cleanup(func() { falPlatformFor, falStorageFor = p, s; resetSchemas() })
}

func falClient(base, model string, video bool) *Client {
	return customClient(func(c *config.Config) {
		if video {
			c.Custom.VideoPreset, c.Custom.VideoBaseURL, c.Custom.VideoKey, c.Custom.VideoModel = PresetFal, base, falKey, model
		} else {
			c.Custom.ImagePreset, c.Custom.ImageBaseURL, c.Custom.ImageKey, c.Custom.ImageModel = PresetFal, base, falKey, model
		}
		c.Video.Resolution = "1080p"
	})
}

const nanoEditDoc = `{"models":[{"endpoint_id":"fal-ai/nano-banana-2/edit","openapi":{"paths":{
 "/fal-ai/nano-banana-2/edit":{"post":{"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/In"}}}}}}},
 "components":{"schemas":{"In":{"type":"object","required":["prompt"],"properties":{
  "prompt":{"type":"string"},
  "image_urls":{"type":"array","items":{"type":"string"}},
  "num_images":{"type":"integer","minimum":1,"maximum":4,"default":1},
  "aspect_ratio":{"anyOf":[{"enum":["auto","16:9","9:16","1:1"],"type":"string"},{"type":"null"}],"default":"auto"},
  "output_format":{"type":"string","enum":["jpeg","png"],"default":"png"}}}}}}}]}`

func TestFalImageWithRefsLiveSchemaAndUploads(t *testing.T) {
	fast(t)
	api, cdn := newMockAPI(t), newMockAPI(t)
	falHosts(t, api.srv.URL+"/platform", api.srv.URL+"/storage")
	api.on("GET /platform/models", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("endpoint_id") != "fal-ai/nano-banana-2/edit" || r.URL.Query().Get("expand") != "openapi-3.0" {
			t.Errorf("schema query = %s", r.URL.RawQuery)
		}
		writeJSON(w, 200, nanoEditDoc)
	})
	var uploads atomic.Int32
	api.on("POST /storage/storage/upload/initiate", func(w http.ResponseWriter, r *http.Request) {
		n := uploads.Add(1)
		writeJSON(w, 200, fmt.Sprintf(`{"upload_url":"%s/put/%d","file_url":"%s/files/ref%d"}`, cdn.srv.URL, n, cdn.srv.URL, n))
	})
	cdn.on("PUT /put/*", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	api.on("POST /fal-ai/nano-banana-2/edit", func(w http.ResponseWriter, r *http.Request) {
		base := api.srv.URL + "/fal-ai/nano-banana-2/requests/q1"
		writeJSON(w, 200, fmt.Sprintf(`{"request_id":"q1","status_url":"%s/status","response_url":"%s","cancel_url":"%s/cancel","queue_position":0}`, base, base, base))
	})
	var polls atomic.Int32
	api.on("GET /fal-ai/nano-banana-2/requests/q1/status", func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) == 1 {
			writeJSON(w, 202, `{"status":"IN_QUEUE","queue_position":2}`)
			return
		}
		writeJSON(w, 200, `{"status":"COMPLETED"}`)
	})
	api.on("GET /fal-ai/nano-banana-2/requests/q1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, fmt.Sprintf(`{"images":[{"url":"%s/out/1.png","content_type":"image/png"}],"description":""}`, cdn.srv.URL))
	})
	cdn.on("GET /out/1.png", serveBytes("image/png", pngBytes))

	c := falClient("https://ignored.example", "fal-ai/nano-banana-2/edit", false)
	c.get = func() config.Config { // the real base is the mock
		cfg := config.Default()
		cfg.Custom = config.CustomAPI{ImagePreset: PresetFal, ImageBaseURL: api.srv.URL, ImageKey: falKey, ImageModel: "fal-ai/nano-banana-2/edit"}
		return cfg
	}
	refs := []gen.RefImage{{Data: jpegBytes, MIME: "image/jpeg", Name: "taras.jpg"}, {Data: pngBytes, Name: "kyiv.png"}}
	data, mt, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "@taras walks in @kyiv", Aspect: "16:9", Refs: refs})
	if err != nil || mt != "image/png" || string(data) != string(pngBytes) {
		t.Fatalf("image: %q %v", mt, err)
	}
	sub := api.requests("POST /fal-ai/nano-banana-2/edit")
	if len(sub) != 1 {
		t.Fatalf("submits: %d", len(sub))
	}
	body := sub[0].json(t)
	wantRefs := []any{cdn.srv.URL + "/files/ref1", cdn.srv.URL + "/files/ref2"}
	if !reflect.DeepEqual(body["image_urls"], wantRefs) || body["aspect_ratio"] != "16:9" || body["num_images"] != float64(1) {
		t.Errorf("body = %v", body)
	}
	if p, _ := body["prompt"].(string); !strings.Contains(p, "image 1 = @taras, image 2 = @kyiv") {
		t.Errorf("prompt legend: %q", p)
	}
	puts := cdn.requests("PUT /put/")
	if len(puts) != 2 || puts[0].header.Get("Content-Type") != "image/jpeg" || string(puts[0].body) != string(jpegBytes) {
		t.Errorf("uploads: %d", len(puts))
	}
	for _, s := range api.all() {
		if s.header.Get("Authorization") != "Key "+falKey {
			t.Errorf("%s %s: auth %q", s.method, s.path, s.header.Get("Authorization"))
		}
	}
	for _, s := range cdn.all() {
		if s.header.Get("Authorization") != "" {
			t.Errorf("CDN %s %s got the key", s.method, s.path)
		}
	}
}

func TestFalImageNoRefsUsesTextToImageSibling(t *testing.T) {
	fast(t)
	falHosts(t, "", "")
	m := newMockAPI(t)
	m.on("POST /fal-ai/nano-banana-2", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"request_id":"a1"}`)
	})
	m.on("GET /fal-ai/nano-banana-2/requests/a1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"status":"COMPLETED"}`)
	})
	m.on("GET /fal-ai/nano-banana-2/requests/a1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"images":[{"url":"data:image/jpeg;base64,/9j/4AAQ"}]}`)
	})
	c := falClient(m.srv.URL, "fal-ai/nano-banana-2/edit", false)
	data, mt, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "a hero, full body", Aspect: "3:4"})
	if err != nil || mt != "image/jpeg" || len(data) == 0 {
		t.Fatalf("t2i: %q %v", mt, err)
	}
	body := m.requests("POST /fal-ai/nano-banana-2")[0].json(t)
	if _, ok := body["image_urls"]; ok || body["aspect_ratio"] != "3:4" || body["prompt"] != "a hero, full body" {
		t.Errorf("body = %v", body)
	}
}

func TestFalVideoBuiltinSchemaAppIDStatusAndProgress(t *testing.T) {
	fast(t)
	falHosts(t, "", "")
	m, cdn := newMockAPI(t), newMockAPI(t)
	model := "fal-ai/kling-video/v3/pro/image-to-video"
	m.on("POST /"+model, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"request_id":"v1"}`) // no URLs: built from the app id
	})
	m.on("GET /"+model+"/requests/*", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(405) // the queue rejects the sub-path
	})
	var polls atomic.Int32
	m.on("GET /fal-ai/kling-video/requests/v1/status", func(w http.ResponseWriter, r *http.Request) {
		switch polls.Add(1) {
		case 1:
			writeJSON(w, 200, `{"status":"IN_QUEUE","queue_position":1}`)
		case 2:
			writeJSON(w, 200, `{"status":"IN_PROGRESS","logs":[]}`)
		default:
			writeJSON(w, 200, `{"status":"COMPLETED","metrics":{"inference_time":42}}`)
		}
	})
	m.on("GET /fal-ai/kling-video/requests/v1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, fmt.Sprintf(`{"video":{"url":"%s/clip.mp4","content_type":"video/mp4"}}`, cdn.srv.URL))
	})
	cdn.on("GET /clip.mp4", serveBytes("video/mp4", mp4Bytes))
	c := falClient(m.srv.URL, model, true)
	var seen []int
	clip, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "slow push-in", Start: startFrame(), Aspect: "16:9", Seconds: 10}, func(p int, _ string) {
		seen = append(seen, p)
	})
	if err != nil || string(clip) != string(mp4Bytes) {
		t.Fatalf("video: %d bytes %v", len(clip), err)
	}
	body := m.requests("POST /" + model)[0].json(t)
	if body["duration"] != "10" || !strings.HasPrefix(fmt.Sprint(body["start_image_url"]), "data:image/jpeg;base64,") || body["image_url"] != nil {
		t.Errorf("body = %v", body)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] < seen[i-1] {
			t.Errorf("progress went back: %v", seen)
		}
	}
	if len(seen) == 0 || seen[0] != 5 || seen[len(seen)-1] != 100 {
		t.Errorf("progress = %v", seen)
	}
	noKeyLeak(t, m, falKey, "Authorization")
}

func TestFalFailuresAndCancel(t *testing.T) {
	fast(t)
	falHosts(t, "", "")
	m := newMockAPI(t)
	model := "fal-ai/veo3.1/fast/image-to-video"
	var mode atomic.Int32
	m.on("POST /"+model, func(w http.ResponseWriter, r *http.Request) {
		if mode.Load() == 3 {
			writeJSON(w, 403, `{"detail":"User is locked. Reason: Exhausted balance. Top up your balance at fal.ai/dashboard/billing."}`)
			return
		}
		writeJSON(w, 200, `{"request_id":"x1"}`)
	})
	m.on("GET /fal-ai/veo3.1/requests/x1/status", func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 0:
			writeJSON(w, 200, `{"status":"COMPLETED","error":"Content policy violation: the prompt was flagged","error_type":"content_policy_violation"}`)
		case 1:
			writeJSON(w, 200, `{"status":"COMPLETED"}`)
		default:
			writeJSON(w, 200, `{"status":"IN_PROGRESS"}`)
		}
	})
	m.on("GET /fal-ai/veo3.1/requests/x1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 422, `{"detail":[{"loc":["body","image_url"],"msg":"Image too small","type":"image_too_small"}]}`)
	})
	m.on("PUT /fal-ai/veo3.1/requests/x1/cancel", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 202, `{"status":"CANCELLATION_REQUESTED"}`)
	})
	c := falClient(m.srv.URL, model, true)
	req := gen.VideoRequest{Prompt: "p", Start: startFrame(), Seconds: 8}

	_, err := c.Video(context.Background(), req, nil)
	if err == nil || !strings.Contains(err.Error(), "фільтром безпеки") {
		t.Errorf("policy: %v", err)
	}
	mode.Store(1)
	_, err = c.Video(context.Background(), req, nil)
	if err == nil || !strings.Contains(err.Error(), "image_url: Image too small") || !strings.Contains(err.Error(), "(422)") {
		t.Errorf("422 on result: %v", err)
	}
	mode.Store(2)
	ctx, cancel := context.WithCancel(context.Background())
	_, err = c.Video(ctx, req, func(p int, phase string) {
		if phase == "rendering" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancel: %v", err)
	}
	if cs := m.requests("PUT /fal-ai/veo3.1/requests/x1/cancel"); len(cs) != 1 || cs[0].header.Get("Authorization") != "Key "+falKey {
		t.Errorf("remote cancel: %d", len(cs))
	}
	mode.Store(3)
	before := len(m.requests("POST /" + model))
	_, err = c.Video(context.Background(), req, nil)
	if err == nil || !strings.Contains(err.Error(), "закінчились кошти") || len(m.requests("POST /"+model)) != before+1 {
		t.Errorf("locked account: %v", err)
	}
	noKeyLeak(t, m, falKey, "Authorization", err.Error())
}

func TestFalTest(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	falHosts(t, m.srv.URL+"/v1", "")
	m.on("GET /v1/models/pricing", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Key "+falKey {
			writeJSON(w, 401, `{"error":{"type":"authorization_error","message":"Invalid API key"}}`)
			return
		}
		if r.URL.Query().Get("endpoint_id") == "fal-ai/none" {
			writeJSON(w, 200, `{"prices":[]}`)
			return
		}
		writeJSON(w, 200, `{"prices":[{"endpoint_id":"fal-ai/veo3.1/fast/image-to-video","unit_price":0.15,"unit":"second","currency":"USD"}]}`)
	})
	ok, msg := falClient(m.srv.URL, "fal-ai/veo3.1/fast/image-to-video", true).TestVideo(context.Background())
	if !ok || !strings.Contains(msg, "ціна $0.15 за секунду відео") {
		t.Errorf("good key: %v %q", ok, msg)
	}
	ok, msg = falClient(m.srv.URL, "fal-ai/none", true).TestVideo(context.Background())
	if !ok || !strings.Contains(msg, "не знайдена") {
		t.Errorf("unknown model: %v %q", ok, msg)
	}
	bad := customClient(func(c *config.Config) {
		c.Custom = config.CustomAPI{ImagePreset: PresetFal, ImageBaseURL: m.srv.URL, ImageKey: "bad-key-123456", ImageModel: "fal-ai/flux-2"}
	})
	ok, msg = bad.TestImage(context.Background())
	if ok || !strings.Contains(msg, "ключ не прийнято (401)") || strings.Contains(msg, "bad-key-123456") {
		t.Errorf("bad key: %v %q", ok, msg)
	}
	// behind an unknown host the queue itself is asked
	falHosts(t, "", "")
	m.on("GET /fal-ai/flux-2/requests/*", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Key "+falKey {
			writeJSON(w, 401, `{"detail":"invalid key credentials"}`)
			return
		}
		writeJSON(w, 404, `{"status":"NOT_FOUND"}`)
	})
	ok, msg = falClient(m.srv.URL, "fal-ai/flux-2", false).TestImage(context.Background())
	if !ok {
		t.Errorf("proxy probe: %q", msg)
	}
	ok, _ = bad.TestImage(context.Background())
	if ok {
		t.Error("proxy probe with a bad key must fail")
	}
}

// When the client stops waiting (its poll budget ran out) the remote task is
// cancelled, so the user does not pay for a clip nobody downloads.
func TestFalPollTimeoutCancelsRemote(t *testing.T) {
	fast(t)
	falHosts(t, "", "")
	old := videoPollTimeout
	videoPollTimeout = 30 * time.Millisecond
	t.Cleanup(func() { videoPollTimeout = old })
	m := newMockAPI(t)
	model := "fal-ai/veo3.1/fast/image-to-video"
	m.on("POST /"+model, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"request_id":"slow"}`) })
	m.on("GET /fal-ai/veo3.1/requests/slow/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"status":"IN_PROGRESS"}`)
	})
	m.on("PUT /fal-ai/veo3.1/requests/slow/cancel", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 202, `{}`) })
	_, err := falClient(m.srv.URL, model, true).Video(context.Background(), gen.VideoRequest{Prompt: "p", Start: startFrame(), Seconds: 8}, nil)
	if err == nil || !strings.Contains(err.Error(), "не завершилась") {
		t.Fatalf("timeout: %v", err)
	}
	if len(m.requests("PUT /fal-ai/veo3.1/requests/slow/cancel")) != 1 {
		t.Error("the remote task must be cancelled")
	}
}

func TestSchemaPromptMaxLength(t *testing.T) {
	s := schemaOf(field{name: "prompt", p: prop{kind: "string", maxLen: 5}}, fStr("image_url"))
	in, _, _ := s.build(inputs{prompt: "абвгдеж", start: "u"})
	if in["prompt"] != "абвгд" {
		t.Errorf("prompt = %q", in["prompt"])
	}
}

// A failed CDN upload falls back to an inline data URI; nothing the client
// logs on the way (retries, the fallback) contains the key.
func TestFalUploadFallbackAndLogsWithoutKey(t *testing.T) {
	fast(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	m := newMockAPI(t)
	falHosts(t, "", m.srv.URL+"/storage")
	m.on("POST /storage/storage/upload/initiate", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 500, `{"detail":"storage down for key `+falKey+`"}`)
	})
	var submits atomic.Int32
	m.on("POST /fal-ai/flux-pro/kontext", func(w http.ResponseWriter, r *http.Request) {
		if submits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			writeJSON(w, 429, `{"detail":"rate limited"}`)
			return
		}
		writeJSON(w, 200, `{"request_id":"k1"}`)
	})
	m.on("GET /fal-ai/flux-pro/requests/k1/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"status":"COMPLETED"}`) })
	m.on("GET /fal-ai/flux-pro/requests/k1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"images":[{"url":"data:image/png;base64,iVBORw0KGgo="}]}`)
	})
	c := falClient(m.srv.URL, "fal-ai/flux-pro/kontext", false)
	_, _, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "p", Aspect: "16:9", Refs: []gen.RefImage{{Data: jpegBytes, MIME: "image/jpeg"}}})
	if err != nil {
		t.Fatal(err)
	}
	body := m.requests("POST /fal-ai/flux-pro/kontext")[1].json(t)
	if !strings.HasPrefix(fmt.Sprint(body["image_url"]), "data:image/jpeg;base64,") {
		t.Errorf("fallback image_url = %.60v", body["image_url"])
	}
	out := logs.String()
	if !strings.Contains(out, "retry") || !strings.Contains(out, "inline") {
		t.Errorf("expected retry + fallback logs, got %q", out)
	}
	if strings.Contains(out, falKey) || strings.Contains(out, "00112233445566778899") {
		t.Errorf("key in logs: %q", out)
	}
}
