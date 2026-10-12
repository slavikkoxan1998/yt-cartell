package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/gen"
)

var mp4Bytes = []byte("\x00\x00\x00\x18ftypmp42-fake-video-bytes")

// progressRec collects progress callbacks.
type progressRec struct {
	mu     sync.Mutex
	pcts   []int
	phases []string
}

func (p *progressRec) fn(pct int, phase string) {
	p.mu.Lock()
	p.pcts = append(p.pcts, pct)
	p.phases = append(p.phases, phase)
	p.mu.Unlock()
}

// veoServer simulates predictLongRunning: each start creates op "<key>-<n>",
// which finishes after `pending` polls with doneBody(key, op).
type veoServer struct {
	mu       sync.Mutex
	starts   int
	polls    map[string]int
	pending  int
	doneBody func(key, op string) map[string]any
	pollHook func(key, op string, n int, w http.ResponseWriter) bool // true = handled
	startErr func(l reqLog, w http.ResponseWriter) bool
}

func (v *veoServer) handle(t *testing.T) func(*mock, reqLog, http.ResponseWriter, *http.Request) {
	return func(_ *mock, l reqLog, w http.ResponseWriter, r *http.Request) {
		v.mu.Lock()
		defer v.mu.Unlock()
		switch {
		case r.Method == "POST" && strings.HasSuffix(l.Path, ":predictLongRunning"):
			if v.startErr != nil && v.startErr(l, w) {
				return
			}
			v.starts++
			writeJSON(w, 200, map[string]any{"name": "models/veo-3.1-generate-preview/operations/" + l.Key + "-" + string(rune('0'+v.starts))})
		case r.Method == "GET" && strings.Contains(l.Path, "/operations/"):
			op := l.Path[strings.LastIndex(l.Path, "/")+1:]
			if !strings.HasPrefix(op, l.Key+"-") {
				t.Errorf("operation %s polled with foreign key %s", op, l.Key)
			}
			v.polls[op]++
			n := v.polls[op]
			if v.pollHook != nil && v.pollHook(l.Key, op, n, w) {
				return
			}
			if n <= v.pending {
				writeJSON(w, 200, map[string]any{"name": op, "done": false})
				return
			}
			writeJSON(w, 200, v.doneBody(l.Key, op))
		case r.Method == "GET" && strings.HasSuffix(l.Path, ":download"):
			if r.URL.Query().Get("alt") != "media" {
				t.Errorf("download without alt=media: %s", r.URL)
			}
			_, _ = w.Write(mp4Bytes)
		default:
			t.Errorf("unexpected request %s %s", r.Method, l.Path)
			w.WriteHeader(500)
		}
	}
}

func doneWithURI(_, op string) map[string]any {
	return map[string]any{"name": op, "done": true, "response": map[string]any{
		"@type": "type.googleapis.com/google.ai.generativelanguage.v1beta.PredictLongRunningResponse",
		"generateVideoResponse": map[string]any{"generatedSamples": []any{map[string]any{
			"video": map[string]any{"uri": "https://generativelanguage.googleapis.com/v1beta/files/" + op + ":download?alt=media"},
		}}},
	}}
}

func TestVideoStartPollDownload(t *testing.T) {
	vs := &veoServer{polls: map[string]int{}, pending: 3, doneBody: doneWithURI}
	c, m, _ := setup(t, vs.handle(t), "k1")
	rec := &progressRec{}
	start := &gen.RefImage{Data: pngBytes, MIME: "image/png"}

	got, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "  slow push-in  ", Start: start, Aspect: "9:16", Seconds: 6}, rec.fn)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, mp4Bytes) {
		t.Fatalf("mp4 = %q", got)
	}

	reqs := m.all()
	body := reqs[0].Body
	if reqs[0].Path != "/v1beta/models/veo-3.1-generate-preview:predictLongRunning" {
		t.Fatalf("start path = %s", reqs[0].Path)
	}
	if dig(body, "instances", 0, "prompt") != "slow push-in" ||
		dig(body, "instances", 0, "image", "inlineData", "mimeType") != "image/png" ||
		dig(body, "instances", 0, "image", "inlineData", "data") != base64.StdEncoding.EncodeToString(pngBytes) ||
		dig(body, "parameters", "aspectRatio") != "9:16" ||
		dig(body, "parameters", "durationSeconds") != float64(6) {
		t.Fatalf("start body = %v", body)
	}
	for _, r := range reqs {
		if r.Key != "k1" {
			t.Fatalf("request with wrong key: %+v", r)
		}
	}
	last := reqs[len(reqs)-1]
	if last.Path != "/v1beta/files/k1-1:download" {
		t.Fatalf("download path = %s", last.Path)
	}

	// Progress: 5 → 40 → rendering (40…90) → 95 → 100, never decreasing.
	if rec.pcts[0] != 5 || rec.pcts[1] != 40 || rec.pcts[len(rec.pcts)-2] != 95 || rec.pcts[len(rec.pcts)-1] != 100 {
		t.Fatalf("progress = %v %v", rec.pcts, rec.phases)
	}
	rendering := 0
	for i, p := range rec.pcts {
		if i > 0 && p < rec.pcts[i-1] {
			t.Fatalf("progress went backwards: %v", rec.pcts)
		}
		if rec.phases[i] == "rendering" {
			rendering++
			if p < 40 || p > 90 {
				t.Fatalf("rendering pct %d out of 40..90", p)
			}
		}
	}
	if rendering != 3 {
		t.Fatalf("rendering updates = %d, want 3 (one per pending poll)", rendering)
	}
}

func TestVideoDefaultsAndTextToVideo(t *testing.T) {
	vs := &veoServer{polls: map[string]int{}, doneBody: doneWithURI}
	c, m, holder := setup(t, vs.handle(t), "k1")
	holder.mu.Lock()
	holder.cfg.Video.Length = 4
	holder.cfg.Google.VeoModel = "veo-custom"
	holder.mu.Unlock()

	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p", Aspect: "1:1"}, nil); err != nil {
		t.Fatal(err)
	}
	body := m.all()[0].Body
	if m.all()[0].Path != "/v1beta/models/veo-custom:predictLongRunning" {
		t.Fatalf("model path = %s", m.all()[0].Path)
	}
	if dig(body, "instances", 0, "image") != nil {
		t.Fatal("text-to-video must not send an image")
	}
	// 1:1 → 16:9; Seconds 0 → cfg.Video.Length.
	if dig(body, "parameters", "aspectRatio") != "16:9" || dig(body, "parameters", "durationSeconds") != float64(4) {
		t.Fatalf("parameters = %v", dig(body, "parameters"))
	}

	// Seconds snapped to 4/6/8.
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p", Seconds: 11}, nil); err != nil {
		t.Fatal(err)
	}
	var startBody map[string]any
	for _, r := range m.all() {
		if strings.HasSuffix(r.Path, ":predictLongRunning") {
			startBody = r.Body
		}
	}
	if dig(startBody, "parameters", "durationSeconds") != float64(8) {
		t.Fatalf("snapped duration = %v", dig(startBody, "parameters", "durationSeconds"))
	}

	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: " "}, nil); err == nil {
		t.Fatal("empty prompt must fail")
	}
}

func TestVideoQuotaDuringPollFailsOverWholeOperation(t *testing.T) {
	vs := &veoServer{polls: map[string]int{}, pending: 1, doneBody: func(key, op string) map[string]any {
		if key == "k1" {
			// A google.rpc.Status: code 8 = RESOURCE_EXHAUSTED, no "status" string.
			return map[string]any{"name": op, "done": true, "error": map[string]any{"code": 8, "message": "Resource has been exhausted."}}
		}
		return doneWithURI(key, op)
	}}
	c, m, _ := setup(t, vs.handle(t), "k1", "k2")
	rec := &progressRec{}
	got, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p", Start: &gen.RefImage{Data: pngBytes}}, rec.fn)
	if err != nil || !bytes.Equal(got, mp4Bytes) {
		t.Fatalf("got %q err %v", got, err)
	}
	if vs.starts != 2 {
		t.Fatalf("operation must restart on the next key, starts = %d", vs.starts)
	}
	if c.Pool().Healthy() != 1 {
		t.Fatal("k1 must be cooling down")
	}
	// Progress restarts at 5 for the second key.
	fives := 0
	for _, p := range rec.pcts {
		if p == 5 {
			fives++
		}
	}
	if fives != 2 {
		t.Fatalf("progress = %v", rec.pcts)
	}
	// MIME sniffed from the bytes when not given.
	if dig(m.all()[0].Body, "instances", 0, "image", "inlineData", "mimeType") != "image/png" {
		t.Fatal("start frame mime not sniffed")
	}
}

func TestVideoOpErrorNonQuota(t *testing.T) {
	vs := &veoServer{polls: map[string]int{}, doneBody: func(_, op string) map[string]any {
		return map[string]any{"name": op, "done": true, "error": map[string]any{"code": 3, "message": "prompt rejected"}}
	}}
	c, _, _ := setup(t, vs.handle(t), "k1", "k2")
	_, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	if err == nil || !strings.Contains(err.Error(), "INVALID_ARGUMENT prompt rejected") || vs.starts != 1 {
		t.Fatalf("err=%v starts=%d", err, vs.starts)
	}
}

// The Gemini API REST shape carries the bytes as video.encodedVideo.
func TestVideoEncodedVideoSample(t *testing.T) {
	vs := &veoServer{polls: map[string]int{}, doneBody: func(_, op string) map[string]any {
		return map[string]any{"name": op, "done": true, "response": map[string]any{"generateVideoResponse": map[string]any{
			"generatedSamples": []any{map[string]any{"video": map[string]any{"encodedVideo": base64.StdEncoding.EncodeToString(mp4Bytes), "encoding": "video/mp4"}}}}}}
	}}
	c, _, _ := setup(t, vs.handle(t), "k1")
	got, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	if err != nil || !bytes.Equal(got, mp4Bytes) {
		t.Fatalf("encodedVideo: %q %v", got, err)
	}
}

// After the shutdown a configured Veo 3.1 preview renders with Gemini Omni.
func TestVideoRetiredVeoGoesToOmni(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	c, _, _ := setup(t, func(_ *mock, l reqLog, w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+l.Path)
		mu.Unlock()
		if r.Method == "POST" && l.Path == "/v1beta/interactions" {
			if l.Body["model"] != "gemini-omni-1.1-flash" {
				t.Errorf("model = %v", l.Body["model"])
			}
			writeJSON(w, 200, omniCompleted(map[string]any{"type": "video", "mime_type": "video/mp4", "data": base64.StdEncoding.EncodeToString(mp4Bytes)}))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, l.Path)
	}, "k1")
	clock = func() time.Time { return time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC) }
	got, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	if err != nil || !bytes.Equal(got, mp4Bytes) || len(paths) != 1 {
		t.Fatalf("got %q err %v paths %v", got, err, paths)
	}
}

func TestVideoPollBlips(t *testing.T) {
	fail := 2
	vs := &veoServer{polls: map[string]int{}, doneBody: doneWithURI}
	vs.pollHook = func(_, _ string, n int, w http.ResponseWriter) bool {
		if n <= fail {
			writeJSON(w, 503, map[string]any{"error": map[string]any{"status": "UNAVAILABLE"}})
			return true
		}
		return false
	}
	c, _, _ := setup(t, vs.handle(t), "k1")
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil); err != nil {
		t.Fatalf("2 blips must be tolerated: %v", err)
	}

	fail = 100
	vs.polls = map[string]int{}
	_, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 503 {
		t.Fatalf("persistent 503 must fail: %v", err)
	}
}

func TestVideoFilteredAndBase64Sample(t *testing.T) {
	var mode string
	vs := &veoServer{polls: map[string]int{}, doneBody: func(_, op string) map[string]any {
		if mode == "filtered" {
			return map[string]any{"name": op, "done": true, "response": map[string]any{"generateVideoResponse": map[string]any{
				"raiMediaFilteredCount": 1, "raiMediaFilteredReasons": []any{"Unsafe content detected"}}}}
		}
		return map[string]any{"name": op, "done": true, "response": map[string]any{"videos": []any{
			map[string]any{"bytesBase64Encoded": base64.StdEncoding.EncodeToString(mp4Bytes)}}}}
	}}
	c, _, _ := setup(t, vs.handle(t), "k1")
	mode = "filtered"
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil); err == nil || !strings.Contains(err.Error(), "фільтром безпеки") {
		t.Fatalf("filtered: %v", err)
	}
	mode = "b64"
	got, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	if err != nil || !bytes.Equal(got, mp4Bytes) {
		t.Fatalf("b64 sample: %q %v", got, err)
	}
}

func TestVideoInlineDataFallback(t *testing.T) {
	vs := &veoServer{polls: map[string]int{}, doneBody: doneWithURI}
	vs.startErr = func(l reqLog, w http.ResponseWriter) bool {
		if dig(l.Body, "instances", 0, "image", "inlineData") != nil {
			writeJSON(w, 400, map[string]any{"error": map[string]any{"code": 400, "status": "INVALID_ARGUMENT",
				"message": "`inlineData` isn't supported by this model. Use bytesBase64Encoded."}})
			return true
		}
		return false
	}
	c, m, _ := setup(t, vs.handle(t), "k1")
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p", Start: &gen.RefImage{Data: pngBytes, MIME: "image/png"}}, nil); err != nil {
		t.Fatal(err)
	}
	second := m.all()[1].Body
	if dig(second, "instances", 0, "image", "bytesBase64Encoded") != base64.StdEncoding.EncodeToString(pngBytes) ||
		dig(second, "instances", 0, "image", "mimeType") != "image/png" {
		t.Fatalf("fallback body = %v", second)
	}
}

func TestVideoTimeoutAndCancel(t *testing.T) {
	vs := &veoServer{polls: map[string]int{}, pending: 1 << 30, doneBody: doneWithURI}
	c, _, _ := setup(t, vs.handle(t), "k1")
	pollMax = 60 * time.Millisecond
	_, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("timeout: %v", err)
	}
	if c.Pool().Healthy() != 1 {
		t.Fatal("timeout must not park the key")
	}

	pollMax = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	_, err = c.Video(ctx, gen.VideoRequest{Prompt: "p"}, nil)
	if !errors.Is(err, context.Canceled) || time.Since(start) > 3*time.Second {
		t.Fatalf("cancel: %v after %v", err, time.Since(start))
	}
}

func TestDownloadURL(t *testing.T) {
	old := base
	base = "https://api.example/v1beta"
	defer func() { base = old }()
	cases := map[string]string{
		"https://generativelanguage.googleapis.com/v1beta/files/abc123:download?alt=media": "https://api.example/v1beta/files/abc123:download?alt=media",
		"files/xyz": "https://api.example/v1beta/files/xyz:download?alt=media",
		"https://storage.example/video.mp4?sig=1": "https://storage.example/video.mp4?sig=1",
	}
	for in, want := range cases {
		if got := downloadURL(in); got != want {
			t.Errorf("downloadURL(%q) = %q, want %q", in, got, want)
		}
	}
	if veoAspect("9:16") != "9:16" || veoAspect("16:9") != "16:9" || veoAspect("1:1") != "16:9" || veoAspect("") != "16:9" {
		t.Fatal("veoAspect")
	}
	if humanDur(6*time.Minute) != "6 хв" || humanDur(30*time.Second) != "30 с" {
		t.Fatal("humanDur")
	}
}
