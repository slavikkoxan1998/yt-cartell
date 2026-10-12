package providers

// Fixes from the provider audit of 2026-10-02 (hard prompt limits, retired
// OpenAI models, Runway text-to-video, MiniMax budgets and codes, schema
// const / reference limits, Replicate file downloads).

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unicode/utf16"

	"ytcartell/internal/gen"
)

func TestHardPromptLimits(t *testing.T) {
	if got := clipUTF16(strings.Repeat("😀", 600), 1000); len(utf16.Encode([]rune(got))) != 1000 || !strings.HasSuffix(got, "😀") {
		t.Fatalf("emoji: %d units", len(utf16.Encode([]rune(got))))
	}
	if got := clipUTF16("a"+strings.Repeat("😀", 500), 1000); len(utf16.Encode([]rune(got))) != 999 {
		t.Fatalf("an emoji is never split: %d units", len(utf16.Encode([]rune(got))))
	}
	if clipUTF16("short", 1000) != "short" || clipKeep("щоб", 2) != "що" {
		t.Fatal("short texts")
	}
	fast(t)
	// Runway counts UTF-16 units; Luma and MiniMax runes — never one more.
	long := strings.Repeat("ї😀", 4000)
	m := newMockAPI(t)
	m.on("POST /v1/image_to_video", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 400, `{"error":"stop"}`) })
	m.on("POST /v1/generations", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 400, `{"detail":"stop"}`) })
	m.on("POST /v2/video_generation", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 400, `{"base_resp":{"status_code":2013}}`) })
	m.on("POST /v1/video_generation", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 400, `{"base_resp":{"status_code":2013}}`) })
	req := gen.VideoRequest{Prompt: long, Start: startFrame(), Seconds: 5}
	_, _ = directClient(PresetRunway, m.srv.URL+"/v1", runwayKey, "gen4_turbo").Video(context.Background(), req, nil)
	_, _ = directClient(PresetLuma, m.srv.URL+"/v1", lumaKey, "ray-3.2").Video(context.Background(), req, nil)
	_, _ = directClient(PresetMiniMax, m.srv.URL, minimaxKey, "MiniMax-H3").Video(context.Background(), req, nil)
	_, _ = directClient(PresetMiniMax, m.srv.URL, minimaxKey, "MiniMax-Hailuo-2.3").Video(context.Background(), req, nil)
	if p := m.requests("POST /v1/image_to_video")[0].json(t)["promptText"].(string); len(utf16.Encode([]rune(p))) > 1000 {
		t.Errorf("runway: %d UTF-16 units", len(utf16.Encode([]rune(p))))
	}
	if p := m.requests("POST /v1/generations")[0].json(t)["prompt"].(string); len([]rune(p)) != 6000 {
		t.Errorf("luma: %d runes", len([]rune(p)))
	}
	content := m.requests("POST /v2/video_generation")[0].json(t)["content"].([]any)
	if p := content[0].(map[string]any)["text"].(string); len([]rune(p)) != 7000 {
		t.Errorf("minimax v2: %d runes", len([]rune(p)))
	}
	if p := m.requests("POST /v1/video_generation")[0].json(t)["prompt"].(string); len([]rune(p)) != 2000 {
		t.Errorf("minimax v1: %d runes", len([]rune(p)))
	}
}

func TestOpenAIRetiredModelsNotSuggested(t *testing.T) {
	for _, id := range []string{PresetOpenAI, PresetOpenAICompat} {
		p, _ := FindImagePreset(id)
		for _, m := range p.Models {
			if strings.HasPrefix(m, "gpt-image-1") || strings.Contains(m, "mini") || m == "chatgpt-image-latest" {
				t.Errorf("%s suggests the retired %s", id, m)
			}
		}
		if !strings.HasPrefix(p.DefaultModel, "gpt-image-2.5-") {
			t.Errorf("%s default %s", id, p.DefaultModel)
		}
	}
	if !openaiFlexible("gpt-image-2.5-sunburst") || openaiFlexible("gpt-image-1.5") {
		t.Fatal("typed older ids keep their sizes")
	}
}

func TestRunwayTextToVideo(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	m.on("POST /v1/text_to_video", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 400, `{"error":"stop"}`) })
	// gen4_turbo has no text-to-video: refused before any request.
	_, err := directClient(PresetRunway, m.srv.URL+"/v1", runwayKey, "gen4_turbo").Video(context.Background(), gen.VideoRequest{Prompt: "p", Seconds: 5}, nil)
	if err == nil || !strings.Contains(err.Error(), "gen4.5") || len(m.all()) != 0 {
		t.Fatalf("gen4_turbo without a frame: %v (%d requests)", err, len(m.all()))
	}
	// gen4.5 text-to-video: 1280:720 or 720:1280 only.
	for aspect, want := range map[string]string{"1:1": "1280:720", "21:9": "1280:720", "3:4": "720:1280"} {
		_, _ = directClient(PresetRunway, m.srv.URL+"/v1", runwayKey, "gen4.5").Video(context.Background(), gen.VideoRequest{Prompt: "p", Aspect: aspect, Seconds: 5}, nil)
		reqs := m.requests("POST /v1/text_to_video")
		if got := reqs[len(reqs)-1].json(t)["ratio"]; got != want {
			t.Errorf("gen4.5 %s → %v, want %s", aspect, got, want)
		}
	}
	if runwayRatio("gen4.5", "1:1", "720p", false) != "960:960" {
		t.Error("image-to-video keeps the square ratio")
	}
	if p, _ := FindVideoPreset(PresetRunway); p.DocsURL != "https://docs.dev.runwayml.com/" {
		t.Errorf("docs %s", p.DocsURL)
	}
}

func TestMiniMaxAudit(t *testing.T) {
	fast(t)
	// One budget for all the images of a V2 request (64 MB cap).
	if minimaxV2PerImage(1) != minimaxV2ImageMax || minimaxV2PerImage(9)*9*4/3 > minimaxV2Budget || minimaxV2PerImage(0) != minimaxV2ImageMax {
		t.Fatalf("per image: %d / %d", minimaxV2PerImage(1), minimaxV2PerImage(9))
	}
	// Hailuo-02: 512P only from a first frame.
	if r := minimaxV1Resolution("MiniMax-Hailuo-02", "480p", 6, false); r == "512P" {
		t.Errorf("t2v 512P offered")
	}
	if r := minimaxV1Resolution("MiniMax-Hailuo-02", "480p", 6, true); r != "512P" {
		t.Errorf("i2v %s", r)
	}
	m := newMockAPI(t)
	var creates int
	m.on("POST /v1/video_generation", func(w http.ResponseWriter, r *http.Request) {
		creates++
		switch creates {
		case 1:
			writeJSON(w, 200, `{"base_resp":{"status_code":2045,"status_msg":"rate grows too fast"}}`)
		default:
			writeJSON(w, 200, `{"base_resp":{"status_code":2056,"status_msg":"usage limit exceeded"}}`)
		}
	})
	purpose := ""
	m.on("GET /v1/files/list", func(w http.ResponseWriter, r *http.Request) {
		purpose = r.URL.Query().Get("purpose")
		writeJSON(w, 200, `{"files":[],"base_resp":{"status_code":0}}`)
	})
	c := directClient(PresetMiniMax, m.srv.URL, minimaxKey, "MiniMax-Hailuo-2.3")
	_, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p", Start: startFrame(), Seconds: 6}, nil)
	var pe *Error
	if err == nil || creates != 2 || !strings.Contains(err.Error(), "5-годинного вікна") || strings.Contains(err.Error(), "поповни") || !errorsAs(err, &pe) || pe.Retry {
		t.Fatalf("2045 → retry, 2056 → wait: %v (creates %d)", err, creates)
	}
	if ok, msg := c.TestVideo(context.Background()); !ok || purpose != "video_generation_input" {
		t.Fatalf("key check: %v %q purpose %q", ok, msg, purpose)
	}
}

func TestSchemaConstAndLiveRefLimit(t *testing.T) {
	doc := map[string]any{"type": "object", "properties": map[string]any{
		"duration":   map[string]any{"oneOf": []any{map[string]any{"const": "5"}, map[string]any{"const": "10"}}},
		"mode":       map[string]any{"const": "pro"},
		"image_urls": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"prompt":     map[string]any{"type": "string"},
	}}
	s := parseSchema(doc, nil)
	if d := s.props["duration"]; d.kind != "string" || fmt.Sprint(d.enum) != "[5 10]" {
		t.Fatalf("const branches: %+v", d)
	}
	if fmt.Sprint(s.videoCaps().Lengths) != "[5 10]" || fmt.Sprint(s.props["mode"].enum) != "[pro]" {
		t.Fatalf("caps from consts: %+v", s.videoCaps())
	}
	// No maxItems in the live schema: the model's known limit, not 3.
	if c := s.videoCaps(); c.MaxRefs != 3 {
		t.Fatalf("no model known: %d", c.MaxRefs)
	}
	ko := s.withBuiltin(falVideoSchema("fal-ai/kling-video/o1/reference-to-video"))
	if c := ko.videoCaps(); c.MaxRefs != 7 {
		t.Fatalf("Kling O1: %d refs", c.MaxRefs)
	}
	if s.builtin != nil {
		t.Fatal("withBuiltin must not touch the cached schema")
	}
	if b := falVideoSchema("x"); b.withBuiltin(nil) != b {
		t.Fatal("built-in schemas stay as they are")
	}
}

// A Replicate file URL that wants the token (401/403) is asked again with it
// — but only on Replicate's own hosts.
func TestReplicateDownloadRetriesWithToken(t *testing.T) {
	fast(t)
	api, files := newMockAPI(t), newMockAPI(t) // the API and Replicate's file host
	files.on("GET /files/f1", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+replicateToken {
			writeJSON(w, 401, `{"detail":"Unauthenticated"}`)
			return
		}
		serveBytes("image/png", pngBytes)(w, r)
	})
	own := replicateOwnHost
	replicateOwnHost = func(u string) bool { return strings.HasPrefix(u, files.srv.URL) }
	t.Cleanup(func() { replicateOwnHost = own })
	ep := &endpoint{name: "Replicate", base: api.srv.URL + "/v1", key: replicateToken, hc: &http.Client{}, auth: bearer}
	data, _, err := replicateDownload(context.Background(), ep, files.srv.URL+"/files/f1", maxImageBytes)
	if err != nil || string(data) != string(pngBytes) {
		t.Fatalf("download: %v", err)
	}
	if reqs := files.requests("GET /files/f1"); len(reqs) != 2 || reqs[0].header.Get("Authorization") != "" {
		t.Fatalf("%d requests: first without the token, then with it", len(reqs))
	}
	other := newMockAPI(t)
	other.on("GET /f.png", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("token sent to another host")
		}
		writeJSON(w, 403, `{"detail":"no"}`)
	})
	if _, _, err := replicateDownload(context.Background(), ep, other.srv.URL+"/f.png", maxImageBytes); err == nil || len(other.all()) != 1 {
		t.Fatalf("foreign host: %v (%d requests)", err, len(other.all()))
	}
	if !own("https://pbxt.replicate.delivery/x.png") || own("https://evil.example/x") || own("http://replicate.delivery/x") {
		t.Fatal("replicateHost")
	}
}
