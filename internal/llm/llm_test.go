package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gemini"
)

func init() {
	backoff = func(int) time.Duration { return time.Millisecond }
}

type captured struct {
	Method, Path, Auth string
	Body               chatRequest
}

// openaiServer runs handler for /v1/*, recording every request.
func openaiServer(t *testing.T, h func(n int, w http.ResponseWriter, r *http.Request)) (*httptest.Server, *[]captured, *sync.Mutex) {
	t.Helper()
	var (
		mu   sync.Mutex
		reqs []captured
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body chatRequest
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		reqs = append(reqs, captured{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Body: body})
		n := len(reqs)
		mu.Unlock()
		h(n, w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs, &mu
}

func openaiCfg(baseURL, key string) func() config.Config {
	return func() config.Config {
		c := config.Default()
		c.Text = config.Text{Provider: "openai", BaseURL: baseURL, APIKey: key, Model: "gemini-3.8-flash"}
		return c
	}
}

func completion(content any) map[string]any {
	return map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}}}
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestOpenAIJSONRequestFormatAndFences(t *testing.T) {
	srv, reqs, _ := openaiServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, completion("```json\n{\"title\":\"Пригода\",\"n\":2}\n```"))
	})
	c := New(openaiCfg(srv.URL+"/v1/", "sk-test"), nil)
	var out struct {
		Title string `json:"title"`
		N     int    `json:"n"`
	}
	if err := c.JSON(context.Background(), Request{System: "SYS", Prompt: "PROMPT", Temperature: 0.85}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Title != "Пригода" || out.N != 2 {
		t.Fatalf("out = %+v", out)
	}
	r := (*reqs)[0]
	if r.Method != "POST" || r.Path != "/v1/chat/completions" || r.Auth != "Bearer sk-test" {
		t.Fatalf("request = %+v", r)
	}
	want := "### INSTRUCTIONS\nSYS\n\n" + jsonInstruction + "\n\n### INPUT\nPROMPT"
	if len(r.Body.Messages) != 1 || r.Body.Messages[0].Role != "user" || r.Body.Messages[0].Content != want {
		t.Fatalf("messages = %+v\nwant content %q", r.Body.Messages, want)
	}
	// Gemini 3 through the gateway: no temperature ("Strip temperature").
	if r.Body.Model != "gemini-3.8-flash" || r.Body.Temperature != nil || r.Body.Stream {
		t.Fatalf("body = %+v", r.Body)
	}
}

// The temperature goes only to models that take it; a 400 naming it sends
// the request again without it.
func TestOpenAITemperaturePerModel(t *testing.T) {
	var raw []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		raw = append(raw, string(b))
		mu.Unlock()
		if strings.Contains(string(b), `"temperature"`) && strings.Contains(string(b), "picky-model") {
			reply(w, 400, map[string]any{"error": map[string]any{"message": "Unsupported value: 'temperature' does not support 0.4 with this model. Only the default (1) value is supported.",
				"type": "invalid_request_error", "param": "temperature", "code": "unsupported_value"}})
			return
		}
		reply(w, 200, completion("ok"))
	}))
	defer srv.Close()
	for model, want := range map[string]bool{
		"gpt-4o": true, "claude-sonnet": true, "llama-3.3-70b": true, "gemini-2.5-flash": true,
		"gpt-5.5": false, "openai/gpt-6-astra": false, "o3-mini": false, "o4-mini": false,
		"gemini-3.8-flash": false, "google/gemini-3.1-pro-preview": false,
	} {
		mu.Lock()
		raw = nil
		mu.Unlock()
		cfg := func() config.Config {
			c := config.Default()
			c.Text = config.Text{Provider: "openai", BaseURL: srv.URL, Model: model}
			return c
		}
		if _, err := New(cfg, nil).Text(context.Background(), Request{Prompt: "x", Temperature: 0.4}); err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		mu.Lock()
		got := strings.Contains(raw[0], `"temperature":0.4`)
		mu.Unlock()
		if got != want {
			t.Errorf("%s: temperature sent = %v, want %v (%s)", model, got, want, raw[0])
		}
	}
	mu.Lock()
	raw = nil
	mu.Unlock()
	cfg := func() config.Config {
		c := config.Default()
		c.Text = config.Text{Provider: "openai", BaseURL: srv.URL, Model: "picky-model"}
		return c
	}
	if got, err := New(cfg, nil).Text(context.Background(), Request{Prompt: "x", Temperature: 0.4}); err != nil || got != "ok" {
		t.Fatalf("picky: %q %v", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(raw) != 2 || strings.Contains(raw[1], "temperature") {
		t.Fatalf("retry without temperature: %v", raw)
	}
}

// Billing 429s are never retried; rate-limit 429s wait at least Retry-After;
// a Retry-After too long to wait out is reported instead.
func TestOpenAI429Kinds(t *testing.T) {
	var mode string
	srv, reqs, mu := openaiServer(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		switch mode {
		case "billing":
			reply(w, 429, map[string]any{"error": map[string]any{"message": "Your organization has no prepaid credits remaining.", "type": "insufficient_quota", "code": "credit_balance_exhausted"}})
		case "quota-type":
			reply(w, 429, map[string]any{"error": map[string]any{"message": "You exceeded your current quota", "type": "insufficient_quota", "code": "insufficient_quota"}})
		case "retry-ms":
			if n == 1 {
				w.Header().Set("Retry-After-Ms", "150")
				reply(w, 429, map[string]any{"error": map[string]any{"message": "Rate limit reached for requests", "type": "requests", "code": "rate_limit_exceeded"}})
				return
			}
			reply(w, 200, completion("after wait"))
		case "retry-long":
			w.Header().Set("Retry-After", "3600")
			reply(w, 429, map[string]any{"error": map[string]any{"message": "Slow down", "type": "rate_limit_error", "code": "slow_down"}})
		}
	})
	c := New(openaiCfg(srv.URL+"/v1", "k"), nil)
	reset := func() { mu.Lock(); *reqs = nil; mu.Unlock() }

	for _, m := range []string{"billing", "quota-type"} {
		mode = m
		reset()
		_, err := c.Text(context.Background(), Request{Prompt: "x"})
		if err == nil || !strings.Contains(err.Error(), "кредити") || len(*reqs) != 1 {
			t.Fatalf("%s: %v (%d requests — billing must not be retried)", m, err, len(*reqs))
		}
	}
	mode = "retry-ms"
	reset()
	start := time.Now()
	got, err := c.Text(context.Background(), Request{Prompt: "x"})
	if err != nil || got != "after wait" || len(*reqs) != 2 || time.Since(start) < 150*time.Millisecond {
		t.Fatalf("retry-after-ms: %q %v requests %d after %v", got, err, len(*reqs), time.Since(start))
	}
	mode = "retry-long"
	reset()
	if _, err := c.Text(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "спробуй через 1 год") || len(*reqs) != 1 {
		t.Fatalf("retry-long: %v requests %d", err, len(*reqs))
	}
	if d := retryAfter(http.Header{"Retry-After": []string{"2"}}); d != 2*time.Second {
		t.Fatalf("retryAfter = %v", d)
	}
}

// refusal / content_filter / length are explained instead of "empty response".
func TestOpenAIRefusalAndFinishReasons(t *testing.T) {
	var choice map[string]any
	srv, _, _ := openaiServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, map[string]any{"choices": []any{choice}})
	})
	c := New(openaiCfg(srv.URL+"/v1", "k"), nil)
	msg := func(content any, refusal any) map[string]any {
		return map[string]any{"role": "assistant", "content": content, "refusal": refusal}
	}
	cases := []struct {
		choice map[string]any
		json   bool
		want   string
	}{
		{map[string]any{"message": msg(nil, "I can't help with that."), "finish_reason": "stop"}, false, "відмовилася"},
		{map[string]any{"message": msg(nil, nil), "finish_reason": "content_filter"}, false, "фільтром вмісту"},
		{map[string]any{"message": msg("", nil), "finish_reason": "length"}, false, "обрізано"},
		{map[string]any{"message": msg(`{"scenes":[{"a":`, nil), "finish_reason": "length"}, true, "обрізано"},
	}
	for i, tc := range cases {
		choice = tc.choice
		var err error
		if tc.json {
			var out any
			err = c.JSON(context.Background(), Request{Prompt: "x"}, &out)
		} else {
			_, err = c.Text(context.Background(), Request{Prompt: "x"})
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("#%d: %v, want %q", i, err, tc.want)
		}
	}
	// Plain text cut by the limit is still returned.
	choice = map[string]any{"message": msg("A long story that ends abru", nil), "finish_reason": "length"}
	if got, err := c.Text(context.Background(), Request{Prompt: "x"}); err != nil || got != "A long story that ends abru" {
		t.Fatalf("length text: %q %v", got, err)
	}
}

func TestOpenAIPlainTextNoInstructionsNoKey(t *testing.T) {
	srv, reqs, _ := openaiServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, completion("OK"))
	})
	c := New(openaiCfg(srv.URL+"/v1", ""), nil)
	got, err := c.Text(context.Background(), Request{Prompt: "Reply with exactly: OK"})
	if err != nil || got != "OK" {
		t.Fatalf("got %q %v", got, err)
	}
	r := (*reqs)[0]
	if r.Body.Messages[0].Content != "Reply with exactly: OK" || r.Auth != "" {
		t.Fatalf("request = %+v", r)
	}
	// System only (no JSON): instructions block without the JSON line.
	if _, err := c.Text(context.Background(), Request{System: "Be brief", Prompt: "x"}); err != nil {
		t.Fatal(err)
	}
	if got := (*reqs)[1].Body.Messages[0].Content; got != "### INSTRUCTIONS\nBe brief\n\n### INPUT\nx" {
		t.Fatalf("content = %q", got)
	}
}

func TestOpenAIContentParts(t *testing.T) {
	srv, _, _ := openaiServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, completion([]any{
			map[string]any{"type": "text", "text": "[1,"},
			"2,",
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "x"}},
			map[string]any{"type": "text", "text": "3]"},
		}))
	})
	c := New(openaiCfg(srv.URL+"/v1", "k"), nil)
	got, err := c.Text(context.Background(), Request{Prompt: "x"})
	if err != nil || got != "[1,2,3]" {
		t.Fatalf("got %q %v", got, err)
	}
	var nums []int
	if err := c.JSON(context.Background(), Request{Prompt: "x"}, &nums); err != nil || !reflect.DeepEqual(nums, []int{1, 2, 3}) {
		t.Fatalf("nums %v %v", nums, err)
	}
}

func TestOpenAI401NoRetry(t *testing.T) {
	for _, status := range []int{401, 403} {
		srv, reqs, _ := openaiServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
			reply(w, status, map[string]any{"error": map[string]any{"message": "invalid key"}})
		})
		c := New(openaiCfg(srv.URL+"/v1", "bad"), nil)
		_, err := c.Text(context.Background(), Request{Prompt: "x"})
		want := "Текстовий ендпоінт відхилив ключ (" + map[int]string{401: "401", 403: "403"}[status] + "). Перевір API-ключ у Налаштуваннях."
		if err == nil || !strings.HasPrefix(err.Error(), want+"\nЗапит: POST "+srv.URL+"/v1/chat/completions → "+strconv.Itoa(status)) ||
			!strings.Contains(err.Error(), "invalid key") || strings.Contains(err.Error(), "bad") {
			t.Fatalf("%d: err = %v", status, err)
		}
		if len(*reqs) != 1 {
			t.Fatalf("%d must not retry, requests = %d", status, len(*reqs))
		}
	}
}

func TestOpenAIRetryOn5xxAnd429(t *testing.T) {
	srv, reqs, _ := openaiServer(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		switch n {
		case 1:
			reply(w, 500, map[string]any{"error": "boom"})
		case 2:
			reply(w, 429, map[string]any{"error": "slow down"})
		default:
			reply(w, 200, completion("finally"))
		}
	})
	c := New(openaiCfg(srv.URL+"/v1", "k"), nil)
	got, err := c.Text(context.Background(), Request{Prompt: "x"})
	if err != nil || got != "finally" || len(*reqs) != 3 {
		t.Fatalf("got %q err %v requests %d", got, err, len(*reqs))
	}
}

func TestOpenAIRetryExhausted(t *testing.T) {
	srv, reqs, _ := openaiServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		reply(w, 503, map[string]any{"error": "unavailable"})
	})
	c := New(openaiCfg(srv.URL+"/v1", "k"), nil)
	_, err := c.Text(context.Background(), Request{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "503") || len(*reqs) != attempts {
		t.Fatalf("err %v requests %d", err, len(*reqs))
	}
}

func TestOpenAI400NoRetry(t *testing.T) {
	srv, reqs, _ := openaiServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		reply(w, 400, map[string]any{"error": map[string]any{"message": "unknown model"}})
	})
	c := New(openaiCfg(srv.URL+"/v1", "k"), nil)
	_, err := c.Text(context.Background(), Request{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "unknown model") || len(*reqs) != 1 {
		t.Fatalf("err %v requests %d", err, len(*reqs))
	}
}

func TestOpenAITimeoutRetries(t *testing.T) {
	var hits atomic.Int32
	srv, _, _ := openaiServer(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	c := New(openaiCfg(srv.URL+"/v1", "k"), nil)
	start := time.Now()
	_, err := c.Text(context.Background(), Request{Prompt: "x", Timeout: 60 * time.Millisecond})
	if err == nil || !strings.HasPrefix(err.Error(), "Текстовий ендпоінт не відповів вчасно (timeout)\nЗапит: POST "+srv.URL+"/v1/chat/completions → немає відповіді") {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != attempts || time.Since(start) > 4*time.Second {
		t.Fatalf("hits %d after %v", hits.Load(), time.Since(start))
	}
}

func TestOpenAIConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	dead := srv.URL + "/v1"
	srv.Close()
	c := New(openaiCfg(dead, "k"), nil)
	_, err := c.Text(context.Background(), Request{Prompt: "x"})
	want := "Текстовий ендпоінт " + strings.TrimPrefix(srv.URL, "http://") + " недоступний\nЗапит: POST " + dead + "/chat/completions → немає відповіді"
	if err == nil || !strings.HasPrefix(err.Error(), want) || !strings.Contains(err.Error(), "чи вона запущена") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAIEmptyAndErrorBodies(t *testing.T) {
	var mode string
	srv, _, _ := openaiServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		switch mode {
		case "empty":
			reply(w, 200, completion("   "))
		case "error200":
			reply(w, 200, map[string]any{"error": map[string]any{"message": "model overloaded"}})
		case "html":
			_, _ = w.Write([]byte("<html>gateway</html>"))
		case "legacy":
			reply(w, 200, map[string]any{"choices": []any{map[string]any{"text": "legacy text"}}})
		}
	})
	c := New(openaiCfg(srv.URL+"/v1", "k"), nil)
	mode = "empty"
	if _, err := c.Text(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "порожню відповідь") {
		t.Fatalf("empty: %v", err)
	}
	mode = "error200"
	if _, err := c.Text(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "model overloaded") {
		t.Fatalf("error200: %v", err)
	}
	mode = "html"
	if _, err := c.Text(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "не JSON") {
		t.Fatalf("html: %v", err)
	}
	mode = "legacy"
	if got, err := c.Text(context.Background(), Request{Prompt: "x"}); err != nil || got != "legacy text" {
		t.Fatalf("legacy: %q %v", got, err)
	}
}

func TestOpenAICancelDuringBackoff(t *testing.T) {
	old := backoff
	backoff = func(int) time.Duration { return 10 * time.Second }
	defer func() { backoff = old }()
	srv, _, _ := openaiServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		reply(w, 500, map[string]any{"error": "x"})
	})
	c := New(openaiCfg(srv.URL+"/v1", "k"), nil)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	_, err := c.Text(ctx, Request{Prompt: "x"})
	if !errors.Is(err, context.Canceled) || time.Since(start) > 3*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

func TestOpenAINoBaseURL(t *testing.T) {
	c := New(openaiCfg("  ", "k"), nil)
	if _, err := c.Text(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "не задано") {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Models(context.Background()); err == nil {
		t.Fatal("Models without base URL must fail")
	}
}

func TestJSONUnwrapsAndLenient(t *testing.T) {
	var body string
	srv, _, _ := openaiServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, completion(body))
	})
	c := New(openaiCfg(srv.URL+"/v1", "k"), nil)
	type shot struct {
		Index int    `json:"index"`
		Title string `json:"title"`
	}

	body = "Ось план:\n{\"note\":\"x\",\"scenes\":[{\"index\":1,\"title\":\"a\"},{\"index\":2,\"title\":\"b\"}]}"
	var shots []shot
	if err := c.JSON(context.Background(), Request{Prompt: "x"}, &shots); err != nil || len(shots) != 2 || shots[1].Title != "b" {
		t.Fatalf("unwrap: %+v %v", shots, err)
	}

	body = `[{"index":"3","title":"kept"}]`
	shots = nil
	if err := c.JSON(context.Background(), Request{Prompt: "x"}, &shots); err != nil || len(shots) != 1 || shots[0].Title != "kept" {
		t.Fatalf("lenient: %+v %v", shots, err)
	}

	body = `[1,2]`
	var one shot
	if err := c.JSON(context.Background(), Request{Prompt: "x"}, &one); err == nil {
		t.Fatal("array into struct must fail")
	}

	body = "no json at all"
	if err := c.JSON(context.Background(), Request{Prompt: "x"}, &one); err == nil || !strings.Contains(err.Error(), "не містить JSON") {
		t.Fatalf("no json: %v", err)
	}

	var anyOut any
	body = `{"a":[1]}`
	if err := c.JSON(context.Background(), Request{Prompt: "x"}, &anyOut); err != nil || !reflect.DeepEqual(anyOut, map[string]any{"a": []any{float64(1)}}) {
		t.Fatalf("any: %v %v", anyOut, err)
	}
}

func TestModels(t *testing.T) {
	var mode string
	srv, reqs, _ := openaiServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		switch mode {
		case "data":
			reply(w, 200, map[string]any{"object": "list", "data": []any{
				map[string]any{"id": "gemini-3.8-flash"}, map[string]any{"id": "gpt-5"},
				map[string]any{"id": "gemini-3.8-flash"}, map[string]any{"id": ""}}})
		case "models":
			reply(w, 200, map[string]any{"models": []any{map[string]any{"name": "models/gemini-x"}}})
		case "401":
			reply(w, 401, map[string]any{"error": "no"})
		case "500":
			reply(w, 500, map[string]any{"error": "no"})
		}
	})
	c := New(openaiCfg(srv.URL+"/v1", "sk"), nil)
	mode = "data"
	got, err := c.Models(context.Background())
	if err != nil || !reflect.DeepEqual(got, []string{"gemini-3.8-flash", "gpt-5"}) {
		t.Fatalf("models = %v %v", got, err)
	}
	if r := (*reqs)[0]; r.Method != "GET" || r.Path != "/v1/models" || r.Auth != "Bearer sk" {
		t.Fatalf("request = %+v", r)
	}
	mode = "models"
	if got, err = c.Models(context.Background()); err != nil || !reflect.DeepEqual(got, []string{"gemini-x"}) {
		t.Fatalf("models shape = %v %v", got, err)
	}
	mode = "401"
	if _, err = c.Models(context.Background()); err == nil || !strings.Contains(err.Error(), "відхилив ключ") {
		t.Fatalf("401: %v", err)
	}
	mode = "500"
	if _, err = c.Models(context.Background()); err == nil || !strings.HasPrefix(err.Error(), "Список моделей недоступний (500)\nЗапит: GET ") {
		t.Fatalf("500: %v", err)
	}

	dead := httptest.NewServer(http.NotFoundHandler())
	u := dead.URL + "/v1"
	dead.Close()
	if _, err = New(openaiCfg(u, ""), nil).Models(context.Background()); err == nil || !strings.Contains(err.Error(), "недоступний\nЗапит: GET "+u+"/models") {
		t.Fatalf("dead: %v", err)
	}
}

// ---- google provider ----------------------------------------------------------------------

type fakeGem struct {
	model, system, prompt string
	temp                  float64
	jsonMode              bool
	schema                any
	hasDeadline           bool
	out                   string
	err                   error
}

func (f *fakeGem) GenerateText(ctx context.Context, model, system, prompt string, temperature float64, jsonMode bool, schema any) (string, error) {
	f.model, f.system, f.prompt, f.temp, f.jsonMode, f.schema = model, system, prompt, temperature, jsonMode, schema
	_, f.hasDeadline = ctx.Deadline()
	return f.out, f.err
}

func googleCfg() config.Config {
	c := config.Default()
	c.Text.Provider = "google"
	c.Google.StoryModel = "gemini-story"
	return c
}

func TestGoogleProvider(t *testing.T) {
	f := &fakeGem{out: "```json\n[{\"a\":1}]\n```"}
	c := New(func() config.Config { return googleCfg() }, nil)
	c.gem = f
	schema := map[string]any{"type": "ARRAY"}

	var out []map[string]int
	if err := c.JSON(context.Background(), Request{System: "S", Prompt: "P", Temperature: 0.3, Schema: schema, Timeout: time.Minute}, &out); err != nil {
		t.Fatal(err)
	}
	if out[0]["a"] != 1 {
		t.Fatalf("out = %v", out)
	}
	// Google gets the system prompt natively (no ### INSTRUCTIONS folding).
	if f.model != "gemini-story" || f.system != "S" || f.prompt != "P" || f.temp != 0.3 || !f.jsonMode || !reflect.DeepEqual(f.schema, schema) || !f.hasDeadline {
		t.Fatalf("call = %+v", f)
	}

	f.out = "plain"
	got, err := c.Text(context.Background(), Request{Prompt: "P", Schema: schema})
	if err != nil || got != "plain" || f.jsonMode || f.schema != nil || f.hasDeadline {
		t.Fatalf("text: %q %v %+v", got, err, f)
	}

	f.err = gemini.ErrNoKeys
	if _, err := c.Text(context.Background(), Request{Prompt: "P"}); !errors.Is(err, gemini.ErrNoKeys) {
		t.Fatalf("error passthrough: %v", err)
	}

	noGem := New(func() config.Config { return googleCfg() }, nil)
	if _, err := noGem.Text(context.Background(), Request{Prompt: "P"}); err == nil {
		t.Fatal("google provider without gemini client must fail")
	}
}

// A real *gemini.Client wired through New satisfies textGen (no network: no keys).
func TestGoogleProviderRealClientNoKeys(t *testing.T) {
	get := func() config.Config { return googleCfg() }
	c := New(get, gemini.New(get))
	if _, err := c.Text(context.Background(), Request{Prompt: "P"}); !errors.Is(err, gemini.ErrNoKeys) {
		t.Fatalf("err = %v", err)
	}
}
