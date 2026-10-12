package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

// cfgHolder is a mutable config source, like config.Store.Get.
type cfgHolder struct {
	mu  sync.Mutex
	cfg config.Config
}

func (h *cfgHolder) get() config.Config {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.cfg
	c.Google.Keys = append([]string(nil), h.cfg.Google.Keys...)
	return c
}

func (h *cfgHolder) setKeys(keys ...string) {
	h.mu.Lock()
	h.cfg.Google.Keys = keys
	h.mu.Unlock()
}

// reqLog records what the mock server saw.
type reqLog struct {
	Method, Path, Query, Key string
	Body                     map[string]any
}

type mock struct {
	mu   sync.Mutex
	reqs []reqLog
}

func (m *mock) log(r *http.Request) reqLog {
	var body map[string]any
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		_ = json.Unmarshal(b, &body)
	}
	l := reqLog{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Key: r.Header.Get("x-goog-api-key"), Body: body}
	m.mu.Lock()
	m.reqs = append(m.reqs, l)
	m.mu.Unlock()
	return l
}

func (m *mock) all() []reqLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]reqLog(nil), m.reqs...)
}

// setup points the package at a mock server and returns a client over keys.
func setup(t *testing.T, h func(m *mock, l reqLog, w http.ResponseWriter, r *http.Request), keys ...string) (*Client, *mock, *cfgHolder) {
	t.Helper()
	m := &mock{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "key=") {
			t.Errorf("API key leaked into the URL: %s", r.URL)
		}
		h(m, m.log(r), w, r)
	}))
	oldBase, oldPI, oldPM, oldClock := base, pollInterval, pollMax, clock
	base = srv.URL + "/v1beta"
	pollInterval, pollMax = 5*time.Millisecond, 5*time.Second
	clock = func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) } // before the Veo shutdown
	t.Cleanup(func() {
		srv.Close()
		base, pollInterval, pollMax, clock = oldBase, oldPI, oldPM, oldClock
	})
	cfg := config.Default()
	cfg.Google.Keys = keys
	cfg.Normalize()
	// The Veo tests run on Veo 3.1; the Omni tests (omni_test.go) switch to
	// the default Gemini Omni model.
	cfg.Google.VeoModel = "veo-3.1-generate-preview"
	holder := &cfgHolder{cfg: cfg}
	return New(holder.get), m, holder
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func textResp(parts ...map[string]any) map[string]any {
	return map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": parts}, "finishReason": "STOP"}}}
}

func dig(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			a, _ := v.([]any)
			if k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func TestGenerateTextRequestShape(t *testing.T) {
	schema := map[string]any{"type": "ARRAY", "items": map[string]any{"type": "STRING"}}
	c, m, _ := setup(t, func(_ *mock, l reqLog, w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, textResp(
			map[string]any{"text": "thinking…", "thought": true},
			map[string]any{"text": `["a",`},
			map[string]any{"text": `"b"]`},
		))
	}, "k1")

	got, err := c.GenerateText(context.Background(), "", "SYS", "PROMPT", 0, true, schema)
	if err != nil {
		t.Fatal(err)
	}
	if got != `["a","b"]` {
		t.Fatalf("text = %q", got)
	}
	r := m.all()[0]
	if r.Method != "POST" || r.Path != "/v1beta/models/gemini-3.8-flash:generateContent" || r.Key != "k1" {
		t.Fatalf("request = %+v", r)
	}
	if dig(r.Body, "systemInstruction", "parts", 0, "text") != "SYS" ||
		dig(r.Body, "contents", 0, "role") != "user" ||
		dig(r.Body, "contents", 0, "parts", 0, "text") != "PROMPT" {
		t.Fatalf("body = %v", r.Body)
	}
	gc := dig(r.Body, "generationConfig").(map[string]any)
	if _, ok := gc["temperature"]; ok {
		t.Fatalf("Gemini 3 wants no temperature: %v", gc)
	}
	if gc["responseMimeType"] != "application/json" || !reflect.DeepEqual(gc["responseSchema"], schema) {
		t.Fatalf("json mode config = %v", gc)
	}

	// Plain text mode: no JSON fields, explicit model, no system instruction.
	if _, err := c.GenerateText(context.Background(), "models/other-model", "", "hi", 0.7, false, schema); err != nil {
		t.Fatal(err)
	}
	r = m.all()[1]
	if r.Path != "/v1beta/models/other-model:generateContent" || r.Body["systemInstruction"] != nil {
		t.Fatalf("plain request = %+v", r)
	}
	gc = dig(r.Body, "generationConfig").(map[string]any)
	if gc["responseMimeType"] != nil || gc["responseSchema"] != nil || gc["temperature"].(float64) != 0.7 {
		t.Fatalf("plain config = %v", gc)
	}

	// Gemini 2.x takes it, 0 included; Gemini 3 plain text sends no generationConfig at all.
	if _, err := c.GenerateText(context.Background(), "gemini-2.5-flash", "", "hi", 0, false, nil); err != nil {
		t.Fatal(err)
	}
	if temp, ok := dig(m.all()[2].Body, "generationConfig").(map[string]any)["temperature"]; !ok || temp.(float64) != 0 {
		t.Fatalf("2.5: temperature 0 must be sent explicitly: %v", m.all()[2].Body)
	}
	if _, err := c.GenerateText(context.Background(), "", "", "hi", 0.9, false, nil); err != nil {
		t.Fatal(err)
	}
	if b := m.all()[3].Body; b["generationConfig"] != nil {
		t.Fatalf("3.8 plain: %v", b)
	}
}

func TestAcceptsTemperature(t *testing.T) {
	for model, want := range map[string]bool{
		"gemini-3.8-flash": false, "models/gemini-3.1-pro-preview": false, "gemini-3-pro-image": false,
		"gemini-3.1-flash-lite-image": false, "gemini-flash-latest": false, "gemini-10-ultra": false,
		"gemini-2.5-flash": true, "gemini-2.5-flash-image": true, "gemma-3-27b-it": true, "other-model": true, "": true,
	} {
		if got := AcceptsTemperature(model); got != want {
			t.Errorf("AcceptsTemperature(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestGenerateTextFailoverOverHTTP(t *testing.T) {
	c, m, _ := setup(t, func(_ *mock, l reqLog, w http.ResponseWriter, _ *http.Request) {
		if l.Key == "k1" {
			writeJSON(w, 429, map[string]any{"error": map[string]any{"code": 429, "status": "RESOURCE_EXHAUSTED", "message": "Quota exceeded"}})
			return
		}
		writeJSON(w, 200, textResp(map[string]any{"text": "OK from " + l.Key}))
	}, "k1", "k2")

	for i := 0; i < 3; i++ {
		got, err := c.GenerateText(context.Background(), "", "", "x", 0, false, nil)
		if err != nil || got != "OK from k2" {
			t.Fatalf("call %d: %q %v", i, got, err)
		}
	}
	k1 := 0
	for _, r := range m.all() {
		if r.Key == "k1" {
			k1++
		}
	}
	if k1 != 1 {
		t.Fatalf("k1 should be tried once then cool down, got %d", k1)
	}
	if h := c.Pool().Healthy(); h != 1 {
		t.Fatalf("healthy = %d", h)
	}
}

func TestPoolSyncFollowsConfig(t *testing.T) {
	c, _, holder := setup(t, func(_ *mock, l reqLog, w http.ResponseWriter, _ *http.Request) {
		if l.Key == "k1" {
			writeJSON(w, 429, map[string]any{"error": map[string]any{"status": "RESOURCE_EXHAUSTED"}})
			return
		}
		writeJSON(w, 200, textResp(map[string]any{"text": "ok"}))
	}, "k1", "k2")
	if _, err := c.GenerateText(context.Background(), "", "", "x", 0, false, nil); err != nil {
		t.Fatal(err)
	}
	if c.Pool().Size() != 2 || c.Pool().Healthy() != 1 {
		t.Fatal("cooldown must survive while the key list is unchanged")
	}
	holder.setKeys("k1", "k2", "k3")
	if c.Pool().Size() != 3 || c.Pool().Healthy() != 3 {
		t.Fatalf("changed keys: size=%d healthy=%d", c.Pool().Size(), c.Pool().Healthy())
	}
}

func TestGenerateTextErrors(t *testing.T) {
	var mode string
	c, m, _ := setup(t, func(_ *mock, l reqLog, w http.ResponseWriter, _ *http.Request) {
		switch mode {
		case "badkey":
			writeJSON(w, 400, map[string]any{"error": map[string]any{"code": 400, "status": "INVALID_ARGUMENT",
				"message": "API key not valid. Please pass a valid API key.", "details": []any{map[string]any{"reason": "API_KEY_INVALID"}}}})
		case "404":
			writeJSON(w, 404, map[string]any{"error": map[string]any{"code": 404, "status": "NOT_FOUND", "message": "models/x is not found"}})
		case "blocked":
			writeJSON(w, 200, map[string]any{"promptFeedback": map[string]any{"blockReason": "PROHIBITED_CONTENT"}})
		case "garbage":
			_, _ = w.Write([]byte("<html>"))
		}
	}, "k1", "k2")

	mode = "badkey"
	_, err := c.GenerateText(context.Background(), "", "", "x", 0, false, nil)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 400 || !strings.Contains(err.Error(), "ключ Google недійсний") {
		t.Fatalf("bad key: %v", err)
	}
	if len(m.all()) != 1 {
		t.Fatal("a bad key is not a quota error: no failover expected")
	}
	mode = "404"
	if _, err = c.GenerateText(context.Background(), "", "", "x", 0, false, nil); err == nil || !strings.Contains(err.Error(), "модель не знайдена") {
		t.Fatalf("404: %v", err)
	}
	mode = "blocked"
	if _, err = c.GenerateText(context.Background(), "", "", "x", 0, false, nil); err == nil || !strings.Contains(err.Error(), "PROHIBITED_CONTENT") {
		t.Fatalf("blocked: %v", err)
	}
	mode = "garbage"
	if _, err = c.GenerateText(context.Background(), "", "", "x", 0, false, nil); err == nil || !strings.Contains(err.Error(), "незрозуміла") {
		t.Fatalf("garbage: %v", err)
	}
}

func TestGenerateTextTimeoutUsesCallerDeadline(t *testing.T) {
	c, _, _ := setup(t, func(_ *mock, _ reqLog, w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}, "k1")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.GenerateText(ctx, "", "", "x", 0, false, nil)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
	if !strings.Contains(err.Error(), "не відповів вчасно") {
		t.Fatalf("timeout message: %v", err)
	}
	if c.Pool().Healthy() != 1 {
		t.Fatal("timeout must not park the key")
	}
}

func TestNoKeys(t *testing.T) {
	c, _, _ := setup(t, func(*mock, reqLog, http.ResponseWriter, *http.Request) {})
	if _, err := c.GenerateText(context.Background(), "", "", "x", 0, false, nil); !errors.Is(err, ErrNoKeys) {
		t.Fatalf("GenerateText: %v", err)
	}
	if err := c.Test(context.Background()); !errors.Is(err, ErrNoKeys) {
		t.Fatalf("Test: %v", err)
	}
}

func TestTest(t *testing.T) {
	c, m, _ := setup(t, func(_ *mock, l reqLog, w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"models": []any{map[string]any{"name": "models/x"}}})
	}, "k1")
	if err := c.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := m.all()[0]
	if r.Method != "GET" || r.Path != "/v1beta/models" || r.Query != "pageSize=1" || r.Key != "k1" {
		t.Fatalf("test request = %+v", r)
	}
}

// ---- images ---------------------------------------------------------------------------

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR-fake-png")

func TestImageRequestAndInlineData(t *testing.T) {
	c, m, _ := setup(t, func(_ *mock, l reqLog, w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, textResp(
			map[string]any{"text": "Here is your image"},
			map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(pngBytes)}},
		))
	}, "k1")

	jpg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3}
	data, mime, err := c.Image(context.Background(), gen.ImageRequest{
		Prompt:  "A frame",
		UIModel: "GEM_PIX_2",
		Aspect:  "3:4",
		Refs: []gen.RefImage{
			{Data: jpg, MIME: "image/jpeg", Name: "taras.jpg"},
			{Data: pngBytes, Name: "old-forest.png"}, // MIME sniffed
			{Data: nil, Name: "skipped.png"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, pngBytes) || mime != "image/png" {
		t.Fatalf("image = %q %s", data, mime)
	}
	r := m.all()[0]
	if r.Path != "/v1beta/models/gemini-3-pro-image:generateContent" {
		t.Fatalf("model path = %s", r.Path)
	}
	gc := dig(r.Body, "generationConfig").(map[string]any)
	if !reflect.DeepEqual(gc["responseModalities"], []any{"TEXT", "IMAGE"}) ||
		dig(gc, "imageConfig", "aspectRatio") != "3:4" || gc["temperature"] != nil {
		t.Fatalf("generationConfig = %v (Gemini 3 image: no temperature)", gc)
	}
	parts := dig(r.Body, "contents", 0, "parts").([]any)
	if len(parts) != 3 {
		t.Fatalf("parts = %d, want text + 2 images", len(parts))
	}
	text := dig(parts, 0, "text").(string)
	wantLegend := "A frame\n\nReference images, in order: image 1 = @taras, image 2 = @old-forest. Keep each referenced character/place looking exactly like its reference image."
	if text != wantLegend {
		t.Fatalf("prompt+legend =\n%q\nwant\n%q", text, wantLegend)
	}
	if dig(parts, 1, "inlineData", "mimeType") != "image/jpeg" ||
		dig(parts, 1, "inlineData", "data") != base64.StdEncoding.EncodeToString(jpg) ||
		dig(parts, 2, "inlineData", "mimeType") != "image/png" {
		t.Fatalf("image parts = %v", parts[1:])
	}
}

func TestImageSnakeCaseAndNoRefs(t *testing.T) {
	c, m, _ := setup(t, func(_ *mock, l reqLog, w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, textResp(map[string]any{"inline_data": map[string]any{"mime_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8, 0xFF})}}))
	}, "k1")
	data, mime, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "p", UIModel: "unknown"})
	if err != nil || len(data) != 3 || mime != "image/jpeg" {
		t.Fatalf("got %v %s %v", data, mime, err)
	}
	r := m.all()[0]
	// Unknown UI model → cfg.Google.ImageModel; no aspect → no imageConfig; no legend.
	if r.Path != "/v1beta/models/gemini-3.1-flash-image:generateContent" || dig(r.Body, "generationConfig", "imageConfig") != nil {
		t.Fatalf("request = %+v", r)
	}
	if dig(r.Body, "contents", 0, "parts", 0, "text") != "p" {
		t.Fatal("prompt without refs must have no legend")
	}
}

func TestImageNoInlineData(t *testing.T) {
	c, _, _ := setup(t, func(_ *mock, l reqLog, w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"candidates": []any{map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"text": "I can't draw that."}}},
			"finishReason": "IMAGE_SAFETY",
		}}})
	}, "k1")
	_, _, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "не повернув зображення") || !strings.Contains(err.Error(), "IMAGE_SAFETY") {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "  "}); err == nil {
		t.Fatal("empty prompt must fail")
	}
}

// Gemini 3 image models think with interim "thought images": the final image
// is the first non-thought one. At most maxImageRefs references go out.
func TestImageSkipsThoughtImagesAndCapsRefs(t *testing.T) {
	final := []byte("\x89PNG\r\n\x1a\nfinal")
	c, m, _ := setup(t, func(_ *mock, l reqLog, w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, textResp(
			map[string]any{"thought": true, "inlineData": map[string]any{"mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(pngBytes)}},
			map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(final)}},
		))
	}, "k1")
	var refs []gen.RefImage
	for i := 0; i < maxImageRefs+3; i++ {
		refs = append(refs, gen.RefImage{Data: []byte{0xFF, 0xD8, 0xFF, byte(i)}, MIME: "image/jpeg", Name: fmt.Sprintf("r%d.jpg", i)})
	}
	data, _, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "p", UIModel: "NARWHAL", Refs: refs})
	if err != nil || !bytes.Equal(data, final) {
		t.Fatalf("image = %q %v (the thought image must be skipped)", data, err)
	}
	parts := dig(m.all()[0].Body, "contents", 0, "parts").([]any)
	if len(parts) != 1+maxImageRefs {
		t.Fatalf("parts = %d, want text + %d images", len(parts), maxImageRefs)
	}
	if text := dig(parts, 0, "text").(string); !strings.Contains(text, "image 14 = @r13") || strings.Contains(text, "@r14") {
		t.Fatalf("legend = %q", text)
	}
}

func TestAPIErrorQuotaHints(t *testing.T) {
	daily := &APIError{Label: "gemini:m", Status: 429, Body: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"You exceeded your current quota","details":[` +
		`{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaMetric":"generativelanguage.googleapis.com/generate_requests_per_model_per_day","quotaId":"GenerateRequestsPerDayPerProjectPerModel"}]},` +
		`{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"17s"}]}}`}
	if !strings.Contains(daily.Error(), "денну квоту") || !daily.quota() {
		t.Fatal(daily.Error())
	}
	if h := daily.quotaHint(); !h.daily || h.retry != 17*time.Second {
		t.Fatalf("hint = %+v", h)
	}
	minute := &APIError{Label: "l", Status: 429, Body: `{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerMinutePerProjectPerModel"}]}]}}`}
	if !strings.Contains(minute.Error(), "за хвилину") {
		t.Fatal(minute.Error())
	}
	billing := &APIError{Label: "l", Status: 402, Body: `{"error":{"code":402,"message":"Prepay credits depleted"}}`}
	if !billing.quota() || !billing.quotaHint().billing || !strings.Contains(billing.Error(), "передплачені кошти") {
		t.Fatal(billing.Error())
	}
}

func TestImageModelFor(t *testing.T) {
	cases := map[[2]string]string{
		{"GEM_PIX", ""}:        "gemini-3.1-flash-lite-image",
		{"GEM_PIX_2", "x"}:     "gemini-3-pro-image",
		{"NARWHAL", "x"}:       "gemini-3.1-flash-image",
		{"", "custom-image"}:   "custom-image",
		{"weird", ""}:          "gemini-3.1-flash-image",
		{" GEM_PIX ", "fallb"}: "gemini-3.1-flash-lite-image",
	}
	for in, want := range cases {
		if got := ImageModelFor(in[0], in[1]); got != want {
			t.Errorf("ImageModelFor(%q,%q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestAPIErrorMessages(t *testing.T) {
	e := &APIError{Label: "gemini:m", Status: 503, Body: "upstream down"}
	if !strings.Contains(e.Error(), "тимчасово недоступний") || !strings.Contains(e.Error(), "503") {
		t.Fatal(e.Error())
	}
	e = &APIError{Label: "gemini:m", Status: 418, Body: ""}
	if e.Error() != "gemini:m 418" {
		t.Fatal(e.Error())
	}
	e = &APIError{Label: "l", Status: 400, Body: `[{"error":{"message":"User location is not supported for the API use.","status":"FAILED_PRECONDITION"}}]`}
	if !strings.Contains(e.Error(), "регіоні") || !strings.Contains(e.Error(), "FAILED_PRECONDITION") {
		t.Fatal(e.Error())
	}
}

func ExampleImageModelFor() {
	fmt.Println(ImageModelFor("GEM_PIX_2", ""))
	// Output: gemini-3-pro-image
}
