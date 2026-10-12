package gemini

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"ytcartell/internal/gen"
)

func startBody(t *testing.T, m *mock) map[string]any {
	t.Helper()
	for _, r := range m.all() {
		if strings.HasSuffix(r.Path, ":predictLongRunning") {
			return r.Body
		}
	}
	t.Fatal("no predictLongRunning request")
	return nil
}

// Components refs → Veo 3.1 "asset" reference images: first three in the
// caller's order, inlineData, 8 s, and no start frame alongside them.
func TestVideoComponentsReferenceImages(t *testing.T) {
	vs := &veoServer{polls: map[string]int{}, doneBody: doneWithURI}
	c, m, _ := setup(t, vs.handle(t), "k1")
	refs := []gen.RefImage{
		{Data: []byte("board"), MIME: "image/jpeg", Name: "storyboard.jpg"},
		{Data: nil, Name: "empty.png"}, // skipped
		{Data: []byte("prev"), MIME: "image/jpeg", Name: "previous_shot.jpg"},
		{Data: pngBytes, Name: "taras.png"},                            // MIME sniffed
		{Data: []byte("fox"), MIME: "image/png", Name: "lysytsia.png"}, // over the limit
	}
	_, err := c.Video(context.Background(), gen.VideoRequest{
		Prompt: "p", Mode: gen.ModeComponents, Refs: refs, Seconds: 4, Aspect: "9:16",
		Start: &gen.RefImage{Data: []byte("frame"), MIME: "image/jpeg"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := startBody(t, m)
	if dig(body, "instances", 0, "image") != nil {
		t.Fatalf("reference images must not be combined with a start frame: %v", dig(body, "instances", 0))
	}
	list, _ := dig(body, "instances", 0, "referenceImages").([]any)
	if len(list) != 3 {
		t.Fatalf("referenceImages = %v", dig(body, "instances", 0, "referenceImages"))
	}
	want := []struct{ mime, data string }{
		{"image/jpeg", "board"}, {"image/jpeg", "prev"}, {"image/png", string(pngBytes)},
	}
	for i, w := range want {
		if dig(list[i], "referenceType") != "asset" ||
			dig(list[i], "image", "inlineData", "mimeType") != w.mime ||
			dig(list[i], "image", "inlineData", "data") != base64.StdEncoding.EncodeToString([]byte(w.data)) {
			t.Fatalf("reference %d = %v", i, list[i])
		}
		if m, _ := list[i].(map[string]any); len(m) != 2 {
			t.Fatalf("reference %d has extra fields: %v", i, m)
		}
	}
	if dig(body, "parameters", "durationSeconds") != float64(8) || dig(body, "parameters", "aspectRatio") != "9:16" {
		t.Fatalf("parameters = %v (reference images need 8 s)", dig(body, "parameters"))
	}
}

// Models without reference images (Lite) and components without usable refs
// fall back to the start frame.
func TestVideoComponentsFallbackToStart(t *testing.T) {
	vs := &veoServer{polls: map[string]int{}, doneBody: doneWithURI}
	c, m, holder := setup(t, vs.handle(t), "k1")
	holder.mu.Lock()
	holder.cfg.Google.VeoModel = "veo-3.1-lite-generate-preview"
	holder.mu.Unlock()
	start := &gen.RefImage{Data: pngBytes, MIME: "image/png"}
	refs := []gen.RefImage{{Data: []byte("board"), MIME: "image/jpeg", Name: "storyboard.jpg"}}
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p", Mode: gen.ModeComponents, Refs: refs, Start: start, Seconds: 6}, nil); err != nil {
		t.Fatal(err)
	}
	body := startBody(t, m)
	if dig(body, "instances", 0, "referenceImages") != nil || dig(body, "instances", 0, "image", "inlineData", "mimeType") != "image/png" ||
		dig(body, "parameters", "durationSeconds") != float64(6) {
		t.Fatalf("lite fallback body = %v", body)
	}

	holder.mu.Lock()
	holder.cfg.Google.VeoModel = "veo-3.1-generate-preview"
	holder.mu.Unlock()
	m.mu.Lock()
	m.reqs = nil
	m.mu.Unlock()
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p", Mode: gen.ModeComponents, Refs: []gen.RefImage{{Name: "x.png"}}, Start: start}, nil); err != nil {
		t.Fatal(err)
	}
	body = startBody(t, m)
	if dig(body, "instances", 0, "referenceImages") != nil || dig(body, "instances", 0, "image") == nil {
		t.Fatalf("no usable refs → start frame, got %v", body)
	}
	for _, tc := range []struct {
		model string
		ok    bool
	}{{"veo-3.1-generate-preview", true}, {"veo-3.1-fast-generate-preview", true}, {"veo-3.1-lite-generate-preview", false},
		{"veo-3.0-generate-001", false}, {"veo-2.0-generate-001", false}} {
		if veoSupportsRefs(tc.model) != tc.ok {
			t.Errorf("veoSupportsRefs(%q) != %v", tc.model, tc.ok)
		}
	}
}

// A 400 naming inlineData retries with bytesBase64Encoded for the reference
// images too.
func TestVideoReferenceImagesBase64Fallback(t *testing.T) {
	vs := &veoServer{polls: map[string]int{}, doneBody: doneWithURI}
	vs.startErr = func(l reqLog, w http.ResponseWriter) bool {
		if dig(l.Body, "instances", 0, "referenceImages", 0, "image", "inlineData") != nil {
			writeJSON(w, 400, map[string]any{"error": map[string]any{"code": 400, "status": "INVALID_ARGUMENT",
				"message": "`inlineData` isn't supported by this model. Use bytesBase64Encoded."}})
			return true
		}
		return false
	}
	c, m, _ := setup(t, vs.handle(t), "k1")
	refs := []gen.RefImage{{Data: []byte("board"), MIME: "image/jpeg", Name: "storyboard.jpg"}}
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p", Mode: gen.ModeComponents, Refs: refs}, nil); err != nil {
		t.Fatal(err)
	}
	second := m.all()[1].Body
	if dig(second, "instances", 0, "referenceImages", 0, "image", "bytesBase64Encoded") != base64.StdEncoding.EncodeToString([]byte("board")) ||
		dig(second, "instances", 0, "referenceImages", 0, "image", "mimeType") != "image/jpeg" ||
		dig(second, "instances", 0, "referenceImages", 0, "referenceType") != "asset" {
		t.Fatalf("fallback body = %v", second)
	}
}
