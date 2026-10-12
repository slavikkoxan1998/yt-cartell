package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

// omniCompleted is a create/GET response in the documented REST shape: the
// input, a thought step, then the model_output with the given blocks.
func omniCompleted(content ...map[string]any) map[string]any {
	return map[string]any{
		"id": "v1_abc", "status": "completed", "model": "gemini-omni-1.1-flash", "object": "interaction",
		"steps": []any{
			map[string]any{"type": "user_input", "content": []any{map[string]any{"type": "text", "text": "..."}}},
			map[string]any{"type": "thought", "content": []any{map[string]any{"type": "thought", "text": "planning"}}},
			map[string]any{"type": "model_output", "content": content},
		},
	}
}

// omniSetup is setup() on the default Google video model (Gemini Omni Flash).
func omniSetup(t *testing.T, h func(m *mock, l reqLog, w http.ResponseWriter, r *http.Request), keys ...string) (*Client, *mock, *cfgHolder) {
	c, m, holder := setup(t, h, keys...)
	holder.mu.Lock()
	holder.cfg.Google.VeoModel = config.GoogleOmniModel
	holder.mu.Unlock()
	return c, m, holder
}

// Text-to-video with URI delivery: create → file PROCESSING → ACTIVE → download.
func TestOmniTextToVideoURIDelivery(t *testing.T) {
	var mu sync.Mutex
	fileGets := 0
	c, m, holder := omniSetup(t, func(_ *mock, l reqLog, w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "POST" && l.Path == "/v1beta/interactions":
			writeJSON(w, 200, omniCompleted(map[string]any{"type": "video", "mime_type": "video/mp4",
				"uri": "https://generativelanguage.googleapis.com/v1beta/files/vid123:download?alt=media"}))
		case r.Method == "GET" && l.Path == "/v1beta/files/vid123":
			fileGets++
			state := "PROCESSING"
			if fileGets > 2 {
				state = "ACTIVE"
			}
			writeJSON(w, 200, map[string]any{"name": "files/vid123", "state": state, "mimeType": "video/mp4"})
		case r.Method == "GET" && l.Path == "/v1beta/files/vid123:download":
			if r.URL.Query().Get("alt") != "media" {
				t.Errorf("download without alt=media")
			}
			_, _ = w.Write(mp4Bytes)
		default:
			t.Errorf("unexpected %s %s", r.Method, l.Path)
			w.WriteHeader(500)
		}
	}, "k1")
	holder.mu.Lock()
	holder.cfg.Video.Resolution = "4K"
	holder.mu.Unlock()
	rec := &progressRec{}
	got, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "  A marble rolling fast  ", Aspect: "9:16", Seconds: 9}, rec.fn)
	if err != nil || !bytes.Equal(got, mp4Bytes) {
		t.Fatalf("got %q err %v", got, err)
	}
	reqs := m.all()
	body := reqs[0].Body
	if body["model"] != "gemini-omni-1.1-flash" || body["input"] != "A marble rolling fast" {
		t.Fatalf("create body = %v", body)
	}
	rf := dig(body, "response_format").(map[string]any)
	if rf["type"] != "video" || rf["aspect_ratio"] != "9:16" || rf["resolution"] != "4k" || rf["delivery"] != "uri" || rf["duration"] != "10s" {
		t.Fatalf("response_format = %v", rf)
	}
	for k := range body {
		if k != "model" && k != "input" && k != "response_format" {
			t.Fatalf("unexpected field %q (no temperature / system instruction for Omni)", k)
		}
	}
	for _, r := range reqs {
		if r.Key != "k1" {
			t.Fatalf("request with wrong key: %+v", r)
		}
	}
	if fileGets != 3 || reqs[len(reqs)-1].Path != "/v1beta/files/vid123:download" {
		t.Fatalf("file polls %d, last %s", fileGets, reqs[len(reqs)-1].Path)
	}
	// Progress: 5 → rendering 10…85 → 90 → 100, never backwards.
	if rec.pcts[0] != 5 || rec.pcts[1] != 10 || rec.pcts[len(rec.pcts)-2] != 90 || rec.pcts[len(rec.pcts)-1] != 100 {
		t.Fatalf("progress = %v %v", rec.pcts, rec.phases)
	}
	for i, p := range rec.pcts {
		if i > 0 && p < rec.pcts[i-1] {
			t.Fatalf("progress went backwards: %v", rec.pcts)
		}
		if rec.phases[i] == "rendering" && (p < 10 || p > 85) {
			t.Fatalf("rendering %d out of 10..85", p)
		}
	}
}

// Image-to-video: the start frame as an image block, bound as <FIRST_FRAME>;
// inline base64 delivery; thought steps are skipped.
func TestOmniStartFrameInlineVideo(t *testing.T) {
	c, m, _ := omniSetup(t, func(_ *mock, l reqLog, w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, omniCompleted(
			map[string]any{"type": "text", "text": "Here is your video"},
			map[string]any{"type": "video", "mime_type": "video/mp4", "data": base64.StdEncoding.EncodeToString(mp4Bytes)}))
	}, "k1")
	got, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "slow push-in", Start: &gen.RefImage{Data: pngBytes}, Seconds: 6}, nil)
	if err != nil || !bytes.Equal(got, mp4Bytes) {
		t.Fatalf("got %q err %v", got, err)
	}
	if len(m.all()) != 1 {
		t.Fatalf("inline data needs no download: %d requests", len(m.all()))
	}
	body := m.all()[0].Body
	input, _ := body["input"].([]any)
	if len(input) != 2 || dig(input, 0, "type") != "image" || dig(input, 0, "mime_type") != "image/png" ||
		dig(input, 0, "data") != base64.StdEncoding.EncodeToString(pngBytes) ||
		dig(input, 1, "type") != "text" || dig(input, 1, "text") != "<FIRST_FRAME> slow push-in" {
		t.Fatalf("input = %v", input)
	}
	if dig(body, "response_format", "duration") != "6s" || dig(body, "response_format", "aspect_ratio") != "16:9" ||
		dig(body, "response_format", "resolution") != "720p" {
		t.Fatalf("response_format = %v", dig(body, "response_format"))
	}
}

// Components: up to MaxOmniRefs images in priority order, @tags bound to
// <IMAGE_REF_N>, a legend; the start frame is not sent alongside.
func TestOmniComponentsReferences(t *testing.T) {
	c, m, _ := omniSetup(t, func(_ *mock, l reqLog, w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, omniCompleted(map[string]any{"type": "video", "data": base64.StdEncoding.EncodeToString(mp4Bytes)}))
	}, "k1")
	refs := []gen.RefImage{
		{Data: []byte("board"), MIME: "image/jpeg", Name: "storyboard.jpg"},
		{Data: nil, Name: "empty.png"}, // skipped
		{Data: []byte("prev"), MIME: "image/jpeg", Name: "previous_shot.jpg"},
		{Data: []byte("tor"), MIME: "image/jpeg", Name: "tor.jpg"},
		{Data: []byte("torvald"), MIME: "image/jpeg", Name: "torvald.jpg"},
		{Data: []byte("taras"), MIME: "image/jpeg", Name: "тарас.jpg"},
	}
	for i := 0; len(refs) < MaxOmniRefs+3; i++ {
		refs = append(refs, gen.RefImage{Data: []byte{byte(i)}, MIME: "image/png", Name: fmt.Sprintf("extra%d.png", i)})
	}
	prompt := "Push in.\n@tor is Tor; @Torvald is his father; @тарас waves; @тарасик is not a ref.\n" +
		"The reference image @storyboard is a 3×3 storyboard. @previous_shot ends the last shot. Mail me@example.com"
	_, err := c.Video(context.Background(), gen.VideoRequest{Prompt: prompt, Mode: gen.ModeComponents, Refs: refs,
		Start: &gen.RefImage{Data: pngBytes}, Seconds: 8}, nil)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := m.all()[0].Body["input"].([]any)
	if len(input) != MaxOmniRefs+1 {
		t.Fatalf("input blocks = %d, want %d images + text", len(input), MaxOmniRefs)
	}
	if dig(input, 0, "data") != base64.StdEncoding.EncodeToString([]byte("board")) || dig(input, 1, "data") != base64.StdEncoding.EncodeToString([]byte("prev")) {
		t.Fatalf("order = %v / %v", dig(input, 0), dig(input, 1))
	}
	text := dig(input, MaxOmniRefs, "text").(string)
	want := "Push in.\n<IMAGE_REF_2> is Tor; <IMAGE_REF_3> is his father; <IMAGE_REF_4> waves; @тарасик is not a ref.\n" +
		"The reference image <IMAGE_REF_0> is a 3×3 storyboard. <IMAGE_REF_1> ends the last shot. Mail me@example.com"
	if !strings.HasPrefix(text, want) {
		t.Fatalf("prompt =\n%s\nwant prefix\n%s", text, want)
	}
	if !strings.Contains(text, "Reference images, in order: <IMAGE_REF_0> (storyboard), <IMAGE_REF_1> (previous_shot), <IMAGE_REF_2> (tor)") ||
		strings.Contains(text, "<FIRST_FRAME>") || strings.Contains(text, "<IMAGE_REF_10>") {
		t.Fatalf("legend = %s", text)
	}
}

// A 400 naming response_format.duration retries without it (the length goes
// into the prompt) — nothing was rendered.
func TestOmniDurationFallback(t *testing.T) {
	c, m, _ := omniSetup(t, func(_ *mock, l reqLog, w http.ResponseWriter, r *http.Request) {
		if dig(l.Body, "response_format", "duration") != nil {
			writeJSON(w, 400, map[string]any{"error": map[string]any{"code": 400, "status": "INVALID_ARGUMENT",
				"message": `Invalid JSON payload received. Unknown name "duration" at 'response_format': Cannot find field.`}})
			return
		}
		writeJSON(w, 200, omniCompleted(map[string]any{"type": "video", "data": base64.StdEncoding.EncodeToString(mp4Bytes)}))
	}, "k1")
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p", Seconds: 4}, nil); err != nil {
		t.Fatal(err)
	}
	reqs := m.all()
	if len(reqs) != 2 || reqs[1].Body["input"] != "p\n\nThe video is 4 seconds long." {
		t.Fatalf("fallback = %v", reqs[len(reqs)-1].Body)
	}
}

// A 429 with a per-day QuotaFailure parks the key until midnight Pacific and
// the clip renders on the next key.
func TestOmniQuotaFailover(t *testing.T) {
	c, _, _ := omniSetup(t, func(_ *mock, l reqLog, w http.ResponseWriter, r *http.Request) {
		if l.Key == "k1" {
			writeJSON(w, 429, map[string]any{"error": map[string]any{"code": 429, "status": "RESOURCE_EXHAUSTED", "message": "You exceeded your current quota",
				"details": []any{map[string]any{"@type": "type.googleapis.com/google.rpc.QuotaFailure",
					"violations": []any{map[string]any{"quotaId": "GenerateRequestsPerDayPerProjectPerModel"}}}}}})
			return
		}
		writeJSON(w, 200, omniCompleted(map[string]any{"type": "video", "data": base64.StdEncoding.EncodeToString(mp4Bytes)}))
	}, "k1", "k2")
	before := time.Now()
	got, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	if err != nil || !bytes.Equal(got, mp4Bytes) {
		t.Fatalf("got %q err %v", got, err)
	}
	p := c.Pool()
	if p.Healthy() != 1 {
		t.Fatal("k1 must cool down")
	}
	p.mu.Lock()
	until := p.keys[0].cooldownUntil
	p.mu.Unlock()
	if mid := nextPacificMidnight(before); until.Before(mid) || until.After(mid.Add(time.Minute)) {
		t.Fatalf("k1 parked until %v, want the Pacific midnight %v", until, mid)
	}
}

// Failed / blocked / running interactions.
func TestOmniStatuses(t *testing.T) {
	var mode string
	polls, rateLimited := 0, 0
	c, m, _ := omniSetup(t, func(_ *mock, l reqLog, w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && l.Path == "/v1beta/interactions/v1_run":
			polls++
			if polls < 3 {
				writeJSON(w, 200, map[string]any{"id": "v1_run", "status": "in_progress"})
				return
			}
			writeJSON(w, 200, omniCompleted(map[string]any{"type": "video", "data": base64.StdEncoding.EncodeToString(mp4Bytes)}))
			return
		case r.Method == "GET" && l.Path == "/v1beta/files/bad":
			writeJSON(w, 200, map[string]any{"name": "files/bad", "state": "FAILED", "error": map[string]any{"code": 13, "message": "transcoding failed"}})
			return
		}
		switch mode {
		case "failed":
			writeJSON(w, 200, map[string]any{"id": "v1_x", "status": "failed", "errors": []any{map[string]any{"code": "safety", "message": "The prompt was blocked."}}})
		case "ratelimit":
			if rateLimited++; rateLimited == 1 { // the first key tried
				writeJSON(w, 200, map[string]any{"id": "v1_x", "status": "failed", "errors": []any{map[string]any{"message": "RESOURCE_EXHAUSTED: rate limit"}}})
				return
			}
			writeJSON(w, 200, omniCompleted(map[string]any{"type": "video", "data": base64.StdEncoding.EncodeToString(mp4Bytes)}))
		case "text":
			writeJSON(w, 200, omniCompleted(map[string]any{"type": "text", "text": "I can't create that video."}))
		case "running":
			writeJSON(w, 200, map[string]any{"id": "v1_run", "status": "in_progress"})
		case "filefailed":
			writeJSON(w, 200, omniCompleted(map[string]any{"type": "video", "uri": "files/bad"}))
		}
	}, "k1", "k2")

	mode = "failed"
	_, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	if err == nil || !strings.Contains(err.Error(), "The prompt was blocked") || len(m.all()) != 1 {
		t.Fatalf("failed: %v (%d requests — a non-quota failure is not retried)", err, len(m.all()))
	}
	mode = "ratelimit"
	if got, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil); err != nil || !bytes.Equal(got, mp4Bytes) || c.Pool().Healthy() != 1 {
		t.Fatalf("ratelimit failover: %v healthy %d", err, c.Pool().Healthy())
	}
	mode = "text"
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil); err == nil || !strings.Contains(err.Error(), "фільтром безпеки") ||
		!strings.Contains(err.Error(), "can't create") {
		t.Fatalf("text: %v", err)
	}
	mode = "running"
	if got, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil); err != nil || !bytes.Equal(got, mp4Bytes) || polls != 3 {
		t.Fatalf("running: %v polls %d", err, polls)
	}
	mode = "filefailed"
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil); err == nil || !strings.Contains(err.Error(), "transcoding failed") {
		t.Fatalf("file failed: %v", err)
	}
}

func TestOmniTimeoutAndCancel(t *testing.T) {
	c, _, _ := omniSetup(t, func(_ *mock, _ reqLog, w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}, "k1")
	pollMax = 80 * time.Millisecond
	_, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	if err == nil || !strings.Contains(err.Error(), "timeout") || c.Pool().Healthy() != 1 {
		t.Fatalf("timeout: %v", err)
	}
	pollMax = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	if _, err := c.Video(ctx, gen.VideoRequest{Prompt: "p"}, nil); err == nil || ctx.Err() == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("cancel: %v after %v", err, time.Since(start))
	}
}

func TestOmniResolutionAndRefStem(t *testing.T) {
	for in, want := range map[string]string{"720p": "720p", "1080p": "1080p", "4K": "4k", "360p": "360p", "": "720p", "8K": "720p"} {
		if got := omniResolution(in); got != want {
			t.Errorf("omniResolution(%q) = %q, want %q", in, got, want)
		}
	}
	if omniRefStem("@taras.jpg") != "taras" || omniRefStem("old-forest.png") != "old-forest" || omniRefStem("") != "" {
		t.Fatal("refStem")
	}
}
