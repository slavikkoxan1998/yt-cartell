package providers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

func TestClientConfigErrors(t *testing.T) {
	cases := []struct {
		name   string
		custom config.CustomAPI
		video  bool
		want   string
	}{
		{"no preset", config.CustomAPI{}, false, "обери сервіс для кадрів"},
		{"no video preset", config.CustomAPI{}, true, "обери сервіс для відео"},
		{"unknown", config.CustomAPI{ImagePreset: "midjourney"}, false, "невідомий сервіс «midjourney»"},
		{"video-only preset for images", config.CustomAPI{ImagePreset: PresetRunway, ImageKey: "k-123456"}, false, "невідомий сервіс «runway»"},
		{"no key", config.CustomAPI{VideoPreset: PresetFal}, true, "не задано API-ключ"},
		{"compat without base", config.CustomAPI{ImagePreset: PresetOpenAICompat}, false, "вкажи адресу API"},
		{"bad scheme", config.CustomAPI{ImagePreset: PresetOpenAI, ImageBaseURL: "ftp://x/v1", ImageKey: "sk-secret-0123456789"}, false, "некоректна адреса API"},
		{"no host", config.CustomAPI{VideoPreset: PresetRunway, VideoBaseURL: "https://", VideoKey: "k-123456"}, true, "некоректна адреса API"},
	}
	for _, c := range cases {
		cl := customClient(func(cfg *config.Config) { cfg.Custom = c.custom })
		var err error
		var msg string
		if c.video {
			_, err = cl.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
			_, msg = cl.TestVideo(context.Background())
		} else {
			_, _, err = cl.Image(context.Background(), gen.ImageRequest{Prompt: "p"})
			_, msg = cl.TestImage(context.Background())
		}
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(msg, c.want) {
			t.Errorf("%s: err %v / test %q, want %q", c.name, err, msg, c.want)
		}
		if strings.Contains(fmt.Sprint(err), "sk-secret") {
			t.Errorf("%s: key leaked: %v", c.name, err)
		}
	}
	// the key hint tells where to get a key
	cl := customClient(func(cfg *config.Config) { cfg.Custom = config.CustomAPI{VideoPreset: PresetReplicate} })
	if _, msg := cl.TestVideo(context.Background()); !strings.Contains(msg, "replicate.com") {
		t.Errorf("hint: %q", msg)
	}
}

func TestClientEmptyPrompt(t *testing.T) {
	cl := customClient(func(cfg *config.Config) {
		cfg.Custom = config.CustomAPI{ImagePreset: PresetOpenAI, ImageKey: "sk-x-0123456789", VideoPreset: PresetRunway, VideoKey: "key_x"}
	})
	if _, _, err := cl.Image(context.Background(), gen.ImageRequest{Prompt: "  "}); err == nil || !strings.Contains(err.Error(), "порожній промт") {
		t.Errorf("image: %v", err)
	}
	if _, err := cl.Video(context.Background(), gen.VideoRequest{}, nil); err == nil || !strings.Contains(err.Error(), "опис руху") {
		t.Errorf("video: %v", err)
	}
}

// Seconds are snapped to the model (Kling 5/10 s), the Settings length is
// used when the request has none, and components mode falls back to the
// start frame on a model without references.
func TestClientVideoNormalizesRequest(t *testing.T) {
	fast(t)
	falHosts(t, "", "")
	m := newMockAPI(t)
	model := "fal-ai/kling-video/v2.5-turbo/pro/image-to-video"
	m.on("POST /"+model, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"request_id":"n1"}`) })
	m.on("GET /fal-ai/kling-video/requests/n1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"status":"COMPLETED"}`)
	})
	m.on("GET /fal-ai/kling-video/requests/n1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, fmt.Sprintf(`{"video":{"url":"%s/c.mp4"}}`, m.srv.URL))
	})
	m.on("GET /c.mp4", serveBytes("video/mp4", mp4Bytes))
	cl := customClient(func(cfg *config.Config) {
		cfg.Video.Provider, cfg.Video.Length = "custom", 7
		cfg.Custom = config.CustomAPI{VideoPreset: PresetFal, VideoBaseURL: m.srv.URL, VideoKey: falKey, VideoModel: model}
	})
	refs := []gen.RefImage{{Data: pngBytes, Name: "storyboard.png"}}
	for _, req := range []gen.VideoRequest{
		{Prompt: "a", Start: startFrame(), Seconds: 8},
		{Prompt: "b", Start: startFrame()}, // Settings: 7 → 5
		{Prompt: "c", Start: startFrame(), Seconds: 6, Mode: gen.ModeComponents, Refs: refs},
	} {
		if _, err := cl.Video(context.Background(), req, nil); err != nil {
			t.Fatal(err)
		}
	}
	got := []string{}
	for _, s := range m.requests("POST /" + model) {
		b := s.json(t)
		got = append(got, fmt.Sprintf("%v/%v/%v", b["prompt"], b["duration"], strings.HasPrefix(fmt.Sprint(b["image_url"]), "data:image/jpeg")))
	}
	if want := "a/10/true b/5/true c/5/true"; strings.Join(got, " ") != want {
		t.Errorf("requests = %v, want %s", got, want)
	}
}
