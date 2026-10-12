package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
	"ytcartell/internal/providers"
)

func TestVideoCapsTable(t *testing.T) {
	cases := []struct {
		name    string
		video   config.Video
		google  string
		custom  config.CustomAPI
		lengths []int
		refs    int
		comp    bool
	}{
		{name: "glabs veo", video: config.Video{Provider: "glabs", GlabsModel: "veo_31_fast"}, lengths: []int{8}, refs: 3, comp: true},
		{name: "glabs veo ultra", video: config.Video{Provider: "glabs", GlabsModel: "veo_31_quality", Ultra: true}, lengths: []int{4, 6, 8}, refs: 3, comp: true},
		{name: "glabs omni", video: config.Video{Provider: "glabs", GlabsModel: "omni_flash"}, lengths: []int{4, 6, 8, 10}, refs: 7, comp: true},
		{name: "glabs omni ultra", video: config.Video{Provider: "glabs", GlabsModel: "omni_flash", Ultra: true}, lengths: []int{4, 6, 8, 10}, refs: 7, comp: true},
		// Veo ids without a shutdown date (the 3.1 previews retire on 2026-10-22 and then render with Omni)
		{name: "google veo 3.1", video: config.Video{Provider: "google"}, google: "veo-3.1-generate-001", lengths: []int{4, 6, 8}, refs: 3, comp: true},
		{name: "google veo lite", video: config.Video{Provider: "google"}, google: "veo-3.1-lite-generate-001", lengths: []int{4, 6, 8}, refs: 0, comp: false},
		{name: "google omni", video: config.Video{Provider: "google"}, google: "gemini-omni-1.1-flash", lengths: []int{4, 6, 8, 10}, refs: 10, comp: true},
		{name: "google default", video: config.Video{Provider: "google"}, lengths: []int{4, 6, 8, 10}, refs: 10, comp: true},
		{name: "google veo 3.0 (shut down → omni)", video: config.Video{Provider: "google"}, google: "veo-3.0-generate-001", lengths: []int{4, 6, 8, 10}, refs: 10, comp: true},
		{
			name: "custom fal kling", video: config.Video{Provider: "custom"},
			custom:  config.CustomAPI{VideoPreset: providers.PresetFal, VideoModel: "fal-ai/kling-video/v2.1/standard/image-to-video"},
			lengths: []int{5, 10},
		},
	}
	for _, c := range cases {
		cfg := config.Config{Video: c.video, Google: config.Google{VeoModel: c.google}, Custom: c.custom}
		caps := VideoCapsFor(cfg) // exported wrapper of videoCaps (Settings UI)
		if !slices.Equal(caps.Lengths, c.lengths) || caps.MaxRefs != c.refs || caps.Components != c.comp || !caps.StartFrame {
			t.Errorf("%s: caps = %+v, want lengths %v refs %d components %v", c.name, caps, c.lengths, c.refs, c.comp)
		}
		if !slices.Contains(caps.Aspects, "16:9") || !slices.Contains(caps.Aspects, "9:16") {
			t.Errorf("%s: aspects %v", c.name, caps.Aspects)
		}
	}
}

func TestVideoSecondsAndOmniFlashCustom(t *testing.T) {
	custom := config.Config{
		Video:  config.Video{Provider: "custom", GlabsModel: "omni_flash", Length: 8},
		Custom: config.CustomAPI{VideoPreset: providers.PresetFal, VideoModel: "fal-ai/kling-video/v2.1/standard/image-to-video"},
	}
	if omniFlash(custom) || OmniFlash(custom) {
		t.Error("a custom provider is never G-Labs Omni Flash")
	}
	for in, want := range map[int]int{8: 10, 5: 5, 7: 5, 10: 10, 0: 10, 30: 10} {
		custom.Video.Length = in
		if got := videoSeconds(custom); got != want {
			t.Errorf("custom kling Length %d → %d, want %d", in, got, want)
		}
	}
	// G-Labs / Google keep their snapping.
	gl := config.Config{Video: config.Video{Provider: "glabs", GlabsModel: "omni_flash", Length: 10}}
	if !omniFlash(gl) || videoSeconds(gl) != 10 {
		t.Errorf("omni flash: %v %d", omniFlash(gl), videoSeconds(gl))
	}
	g := config.Config{Video: config.Video{Provider: "google", GlabsModel: "omni_flash", Length: 10}, Google: config.Google{VeoModel: "veo-3.1-generate-001"}}
	if omniFlash(g) || videoSeconds(g) != 8 {
		t.Errorf("google veo: %v %d", omniFlash(g), videoSeconds(g))
	}
	// Google Gemini Omni: storyboards/chaining and 10 s clips like G-Labs Omni Flash.
	g.Video.GlabsModel, g.Google.VeoModel = "veo_31_fast", "gemini-omni-1.1-flash"
	if !omniFlash(g) || !OmniFlash(g) || videoSeconds(g) != 10 {
		t.Errorf("google omni: %v %d", omniFlash(g), videoSeconds(g))
	}
}

// customMock is an OpenAI-compatible images server plus a fal.ai-style queue.
type customMock struct {
	mu    sync.Mutex
	paths []string
	auth  []string
	srv   *httptest.Server
}

func newCustomMock(t *testing.T) *customMock {
	m := &customMock{}
	m.srv = httptest.NewServer(m)
	allow(m.srv)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *customMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.paths = append(m.paths, r.Method+" "+r.URL.Path)
	m.auth = append(m.auth, r.Header.Get("Authorization"))
	m.mu.Unlock()
	switch {
	case r.URL.Path == "/v1/models":
		fmt.Fprint(w, `{"data":[{"id":"img-model"}]}`)
	case r.URL.Path == "/v1/images/generations":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "img-model" {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"bad model"}}`)
			return
		}
		fmt.Fprintf(w, `{"data":[{"url":%q}]}`, m.srv.URL+"/files/frame.jpg")
	case r.URL.Path == "/files/frame.jpg":
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(testImage)
	case r.URL.Path == "/files/clip.mp4":
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(testClip)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/fal-ai/"):
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		if json.Unmarshal(b, &body) != nil || body["image_url"] == nil {
			w.WriteHeader(422)
			fmt.Fprint(w, `{"detail":[{"loc":["body","image_url"],"msg":"field required"}]}`)
			return
		}
		base := m.srv.URL + "/fal-ai/kling-video/requests/r1"
		fmt.Fprintf(w, `{"request_id":"r1","status_url":%q,"response_url":%q,"cancel_url":%q}`, base+"/status", base, base+"/cancel")
	case strings.HasSuffix(r.URL.Path, "/requests/r1/status"):
		fmt.Fprint(w, `{"status":"COMPLETED"}`)
	case strings.HasSuffix(r.URL.Path, "/requests/r1"):
		fmt.Fprintf(w, `{"video":{"url":%q}}`, m.srv.URL+"/files/clip.mp4")
	default:
		http.NotFound(w, r)
	}
}

func TestEngineDispatchCustom(t *testing.T) {
	needMedia(t)
	m := newCustomMock(t)
	env := newEnv(t, func(c *config.Config) {
		c.Image.Provider = "custom"
		c.Video.Provider = "custom"
		c.Custom = config.CustomAPI{
			ImagePreset: providers.PresetOpenAICompat, ImageBaseURL: m.srv.URL + "/v1/", ImageKey: "img-key-123456", ImageModel: "img-model",
			VideoPreset: providers.PresetFal, VideoBaseURL: m.srv.URL, VideoKey: "fal-key-123456", VideoModel: "fal-ai/kling-video/v2.1/standard/image-to-video",
		}
	})
	ctx := context.Background()
	data, mime, err := env.e.image(ctx, gen.ImageRequest{Prompt: "a frame", Aspect: "16:9"})
	if err != nil || len(data) == 0 || mime != "image/jpeg" {
		t.Fatalf("custom image: %d bytes %q %v", len(data), mime, err)
	}
	start := gen.RefImage{Data: testImage, MIME: "image/jpeg"}
	var last int
	clip, err := env.e.video(ctx, gen.VideoRequest{Prompt: "move", Start: &start, Aspect: "16:9", Seconds: 8}, func(p int, _ string) { last = p })
	if err != nil || len(clip) == 0 {
		t.Fatalf("custom video: %d bytes %v", len(clip), err)
	}
	if last < 90 {
		t.Errorf("progress ended at %d", last)
	}
	if len(env.gl.requests("image"))+len(env.gl.requests("video")) != 0 {
		t.Error("custom provider must not call G-Labs")
	}
	m.mu.Lock()
	paths, auths := append([]string(nil), m.paths...), append([]string(nil), m.auth...)
	m.mu.Unlock()
	for i, p := range paths {
		a := auths[i]
		switch {
		case strings.HasPrefix(p, "POST /v1/"):
			if a != "Bearer img-key-123456" {
				t.Errorf("%s auth = %q", p, a)
			}
		case strings.HasPrefix(p, "POST /fal-ai/") || strings.Contains(p, "/requests/"):
			if a != "Key fal-key-123456" {
				t.Errorf("%s auth = %q", p, a)
			}
		}
	}

	ok, msg := env.e.TestCustomImage(ctx)
	if !ok || !strings.Contains(msg, "img-model") || strings.Contains(msg, "img-key-123456") {
		t.Errorf("TestCustomImage = %v %q", ok, msg)
	}
	env.store.Update(func(c *config.Config) { c.Custom.VideoKey = "" })
	ok, msg = env.e.TestCustomVideo(ctx)
	if ok || !strings.Contains(msg, "API-ключ") {
		t.Errorf("TestCustomVideo without a key = %v %q", ok, msg)
	}
}
