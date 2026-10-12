package providers

// Runway, Luma and MiniMax (the direct video APIs).

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

func directClient(preset, base, key, model string) *Client {
	return customClient(func(c *config.Config) {
		c.Custom = config.CustomAPI{VideoPreset: preset, VideoBaseURL: base, VideoKey: key, VideoModel: model}
		c.Video.Resolution = "720p"
	})
}

// ---- Runway ---------------------------------------------------------------------------

const runwayKey = "key_0123456789abcdef0123456789abcdef"

func TestRunwayImageToVideo(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	m.on("POST /v1/image_to_video", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"id":"t-1","estimatedCost":{"credits":25}}`)
	})
	var polls atomic.Int32
	m.on("GET /v1/tasks/t-1", func(w http.ResponseWriter, r *http.Request) {
		switch polls.Add(1) {
		case 1:
			writeJSON(w, 200, `{"id":"t-1","status":"THROTTLED"}`)
		case 2:
			writeJSON(w, 200, `{"id":"t-1","status":"RUNNING","progress":0.5}`)
		default:
			writeJSON(w, 200, fmt.Sprintf(`{"id":"t-1","status":"SUCCEEDED","output":["%s/out.mp4"]}`, m.srv.URL))
		}
	})
	m.on("GET /out.mp4", serveBytes("video/mp4", mp4Bytes))
	c := directClient(PresetRunway, m.srv.URL+"/v1", runwayKey, "gen4_turbo")
	var seen []int
	clip, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "dolly in", Start: startFrame(), Aspect: "9:16", Seconds: 7}, func(p int, _ string) { seen = append(seen, p) })
	if err != nil || string(clip) != string(mp4Bytes) {
		t.Fatalf("video: %v", err)
	}
	req := m.requests("POST /v1/image_to_video")[0]
	body := req.json(t)
	if body["model"] != "gen4_turbo" || body["ratio"] != "720:1280" || body["duration"] != float64(7) || body["promptText"] != "dolly in" ||
		!strings.HasPrefix(fmt.Sprint(body["promptImage"]), "data:image/jpeg;base64,") {
		t.Errorf("body = %v", body)
	}
	if req.header.Get("X-Runway-Version") != RunwayVersion || req.header.Get("Authorization") != "Bearer "+runwayKey {
		t.Errorf("headers = %v", req.header)
	}
	if !containsInt(seen, 10) || !containsInt(seen, 55) || seen[len(seen)-1] != 100 {
		t.Errorf("progress = %v", seen)
	}
	noKeyLeak(t, m, runwayKey, "Authorization")
}

func TestRunwayFailureCancelAndTest(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	var mode atomic.Int32
	m.on("POST /v1/image_to_video", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"id":"t-2"}`) })
	m.on("GET /v1/tasks/t-2", func(w http.ResponseWriter, r *http.Request) {
		if mode.Load() == 0 {
			writeJSON(w, 200, `{"id":"t-2","status":"FAILED","failure":"Input did not pass moderation","failureCode":"SAFETY.INPUT.IMAGE"}`)
			return
		}
		writeJSON(w, 200, `{"id":"t-2","status":"RUNNING","progress":0.1}`)
	})
	m.on("DELETE /v1/tasks/t-2", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	m.on("GET /v1/organization", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"creditBalance":1250,"tier":{"maxMonthlyCreditSpend":10000}}`)
	})
	c := directClient(PresetRunway, m.srv.URL+"/v1", runwayKey, "veo3.1_fast")
	req := gen.VideoRequest{Prompt: "p", Start: startFrame(), Seconds: 5, Resolution: "1080p"}
	_, err := c.Video(context.Background(), req, nil)
	if err == nil || !strings.Contains(err.Error(), "фільтром безпеки") {
		t.Errorf("safety: %v", err)
	}
	if body := m.requests("POST /v1/image_to_video")[0].json(t); body["duration"] != float64(6) || body["ratio"] != "1920:1080" {
		t.Errorf("veo body = %v", body)
	}
	mode.Store(1)
	ctx, cancel := context.WithCancel(context.Background())
	_, err = c.Video(ctx, req, func(p int, phase string) {
		if phase == "rendering" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || len(m.requests("DELETE /v1/tasks/t-2")) != 1 {
		t.Errorf("cancel: %v", err)
	}
	ok, msg := c.TestVideo(context.Background())
	if !ok || !strings.Contains(msg, "кредитів: 1250") {
		t.Errorf("test: %v %q", ok, msg)
	}
}

// noisePNG is an incompressible PNG of about w·h·3 bytes.
func noisePNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rnd := rand.New(rand.NewSource(1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(rnd.Intn(256)), uint8(rnd.Intn(256)), uint8(rnd.Intn(256)), 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestFitImageShrinksBigFrames(t *testing.T) {
	big := noisePNG(1400, 1000)
	if len(big) < 3_000_000 {
		t.Fatalf("fixture too small: %d", len(big))
	}
	out, err := fitImage(gen.RefImage{Data: big, MIME: "image/png", Name: "frame.png"}, 400_000)
	if err != nil || len(out.Data) > 400_000 || out.MIME != "image/jpeg" || out.Name != "frame.jpg" {
		t.Fatalf("fit: %d bytes %q %q %v", len(out.Data), out.MIME, out.Name, err)
	}
	small := gen.RefImage{Data: jpegBytes, MIME: "image/jpeg"}
	if got, _ := fitImage(small, 400_000); &got.Data[0] != &small.Data[0] {
		t.Error("small images must pass untouched")
	}
	if _, err := fitImage(gen.RefImage{Data: bytes.Repeat([]byte{7}, 1000)}, 10); err == nil {
		t.Error("undecodable oversized input must fail")
	}
}

// ---- Luma -----------------------------------------------------------------------------

const lumaKey = "luma-api-0123456789abcdef0123"

func TestLumaVideo(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	var polls atomic.Int32
	m.on("POST /v1/generations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 201, `{"id":"g-1","state":"queued","model":"ray-3.2"}`)
	})
	m.on("GET /v1/generations/g-1", func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) == 1 {
			writeJSON(w, 200, `{"id":"g-1","state":"processing"}`)
			return
		}
		writeJSON(w, 200, fmt.Sprintf(`{"id":"g-1","state":"completed","output":[{"type":"image","url":"x"},{"type":"video","url":"%s/g.mp4"}]}`, m.srv.URL))
	})
	m.on("GET /g.mp4", serveBytes("video/mp4", mp4Bytes))
	m.on("GET /v1/files", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "1" || r.Header.Get("Authorization") != "Bearer "+lumaKey {
			writeJSON(w, 401, `{"detail":"Invalid API key"}`)
			return
		}
		writeJSON(w, 200, `{"data":[]}`)
	})
	c := directClient(PresetLuma, m.srv.URL+"/v1", lumaKey, "ray-3.2")
	clip, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "pan", Start: startFrame(), Aspect: "16:9", Seconds: 5}, nil)
	if err != nil || string(clip) != string(mp4Bytes) {
		t.Fatalf("5 s: %v", err)
	}
	body := m.requests("POST /v1/generations")[0].json(t)
	video, _ := body["video"].(map[string]any)
	frame, _ := video["start_frame"].(map[string]any)
	if body["model"] != "ray-3.2" || body["type"] != "video" || body["aspect_ratio"] != "16:9" || video["duration"] != "5s" || video["resolution"] != "720p" ||
		frame["media_type"] != "image/jpeg" || frame["data"] != base64.StdEncoding.EncodeToString(jpegBytes) {
		t.Errorf("body = %v", body)
	}
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "pan", Start: startFrame(), Seconds: 10}, nil); err != nil {
		t.Fatal(err)
	}
	body = m.requests("POST /v1/generations")[1].json(t)
	video, _ = body["video"].(map[string]any)
	if video["duration"] != "10s" || video["start_frame"] != nil || len(video["keyframes"].([]any)) != 1 {
		t.Errorf("10 s uses keyframe 0: %v", video)
	}
	if ok, msg := c.TestVideo(context.Background()); !ok {
		t.Errorf("test: %q", msg)
	}
}

func TestLumaFailure(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	m.on("POST /v1/generations", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 201, `{"id":"g-2","state":"queued"}`) })
	m.on("GET /v1/generations/g-2", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"id":"g-2","state":"failed","failure_reason":"Prompt rejected","failure_code":"content_moderated"}`)
	})
	_, err := directClient(PresetLuma, m.srv.URL+"/v1", lumaKey, "ray-3.2").Video(context.Background(), gen.VideoRequest{Prompt: "p", Seconds: 5}, nil)
	if err == nil || !strings.Contains(err.Error(), "фільтром безпеки") {
		t.Errorf("moderated: %v", err)
	}
	m2 := newMockAPI(t)
	m2.on("POST /v1/generations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 402, `{"detail":"Insufficient balance. Please add funds to continue."}`)
	})
	_, err = directClient(PresetLuma, m2.srv.URL+"/v1", lumaKey, "ray-3.2").Video(context.Background(), gen.VideoRequest{Prompt: "p", Seconds: 5}, nil)
	if err == nil || !strings.Contains(err.Error(), "закінчились кошти") {
		t.Errorf("402: %v", err)
	}
}

// ---- MiniMax --------------------------------------------------------------------------

const minimaxKey = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJlLXRlc3Q"

func TestMiniMaxV2(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	var polls atomic.Int32
	m.on("POST /v2/video_generation", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"task_id":"mm-1"}`)
	})
	m.on("GET /v2/query/video_generation/mm-1", func(w http.ResponseWriter, r *http.Request) {
		switch polls.Add(1) {
		case 1:
			writeJSON(w, 200, `{"task":{"status":"queued"}}`)
		case 2:
			writeJSON(w, 200, `{"task":{"status":"running"}}`)
		default:
			writeJSON(w, 200, fmt.Sprintf(`{"task":{"status":"succeeded","content":{"url":"%s/mm.mp4"}}}`, m.srv.URL))
		}
	})
	m.on("GET /mm.mp4", serveBytes("video/mp4", mp4Bytes))
	c := directClient(PresetMiniMax, m.srv.URL, minimaxKey, "MiniMax-H3")
	clip, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "walk", Start: startFrame(), Aspect: "9:16", Seconds: 7, Resolution: "1080p"}, nil)
	if err != nil || string(clip) != string(mp4Bytes) {
		t.Fatalf("v2: %v", err)
	}
	body := m.requests("POST /v2/video_generation")[0].json(t)
	content, _ := body["content"].([]any)
	if body["model"] != "MiniMax-H3" || body["duration"] != float64(7) || body["resolution"] != "768P" || body["ratio"] != "9:16" || len(content) != 2 {
		t.Fatalf("body = %v", body)
	}
	img, _ := content[1].(map[string]any)
	if img["role"] != "first_frame" || !strings.HasPrefix(fmt.Sprint(img["image_url"].(map[string]any)["url"]), "data:image/jpeg;base64,") {
		t.Errorf("image part = %v", img)
	}
	// components → reference images (no first frame)
	refs := []gen.RefImage{{Data: jpegBytes, Name: "hero.jpg"}, {Data: pngBytes, Name: "city.png"}}
	polls.Store(2)
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "walk", Start: startFrame(), Mode: gen.ModeComponents, Refs: refs, Seconds: 5}, nil); err != nil {
		t.Fatal(err)
	}
	body = m.requests("POST /v2/video_generation")[1].json(t)
	content, _ = body["content"].([]any)
	if len(content) != 3 || content[1].(map[string]any)["role"] != "reference_image" || !strings.Contains(fmt.Sprint(content[0]), "image 1 = @hero") {
		t.Errorf("components = %v", content)
	}
	noKeyLeak(t, m, minimaxKey, "Authorization")
}

func TestMiniMaxV1BaseRespAndTest(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	var creates atomic.Int32
	var mode atomic.Int32
	m.on("POST /v1/video_generation", func(w http.ResponseWriter, r *http.Request) {
		n := creates.Add(1)
		switch {
		case mode.Load() == 1:
			writeJSON(w, 200, `{"task_id":"","base_resp":{"status_code":1008,"status_msg":"insufficient balance"}}`)
		case n == 1:
			writeJSON(w, 200, `{"task_id":"","base_resp":{"status_code":1002,"status_msg":"rate limit"}}`)
		default:
			writeJSON(w, 200, `{"task_id":"v1-task","base_resp":{"status_code":0,"status_msg":"success"}}`)
		}
	})
	m.on("GET /v1/query/video_generation", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("task_id") != "v1-task" {
			t.Errorf("task_id = %q", r.URL.RawQuery)
		}
		writeJSON(w, 200, `{"task_id":"v1-task","status":"Success","file_id":"205258526306433","base_resp":{"status_code":0}}`)
	})
	m.on("GET /v1/files/retrieve", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, fmt.Sprintf(`{"file":{"file_id":205258526306433,"download_url":"%s/h.mp4"},"base_resp":{"status_code":0}}`, m.srv.URL))
	})
	m.on("GET /h.mp4", serveBytes("video/mp4", mp4Bytes))
	m.on("GET /v1/files/list", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+minimaxKey {
			writeJSON(w, 200, `{"base_resp":{"status_code":1004,"status_msg":"login fail: Please carry the API secret key in the 'Authorization' field"}}`)
			return
		}
		writeJSON(w, 200, `{"files":[],"base_resp":{"status_code":0,"status_msg":"success"}}`)
	})
	c := directClient(PresetMiniMax, m.srv.URL, minimaxKey, "MiniMax-Hailuo-2.3")
	clip, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "walk", Start: startFrame(), Seconds: 10, Resolution: "1080p"}, nil)
	if err != nil || string(clip) != string(mp4Bytes) || creates.Load() != 2 {
		t.Fatalf("v1: %v (creates %d)", err, creates.Load())
	}
	body := m.requests("POST /v1/video_generation")[1].json(t)
	if body["duration"] != float64(10) || body["resolution"] != "768P" || !strings.HasPrefix(fmt.Sprint(body["first_frame_image"]), "data:image/jpeg;base64,") {
		t.Errorf("body = %v", body)
	}
	mode.Store(1)
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "walk", Start: startFrame(), Seconds: 6}, nil); err == nil || !strings.Contains(err.Error(), "закінчились кошти") {
		t.Errorf("1008: %v", err)
	}
	if ok, msg := c.TestVideo(context.Background()); !ok {
		t.Errorf("test: %q", msg)
	}
	bad := directClient(PresetMiniMax, m.srv.URL, "wrong-key-0123456789", "MiniMax-H3")
	if ok, msg := bad.TestVideo(context.Background()); ok || !strings.Contains(msg, "ключ не прийнято") {
		t.Errorf("bad key: %v %q", ok, msg)
	}
}
