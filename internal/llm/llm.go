// Package llm is provider-agnostic text generation (port of services/llm.ts),
// used for story direction, scene planning and rewrites:
//
//	openai     → any OpenAI-compatible /chat/completions endpoint (e.g. CarteLink)
//	anthropic  → the Anthropic Messages API
//	ollama     → Ollama's native /api/chat
//	azure      → Azure OpenAI (v1 API or a deployment URL)
//	google     → Gemini generateContent (key pool, native JSON mode)
//
// (wire formats: remote.go; addresses and checks: resolve.go). OpenAI-
// compatible gateways may rewrite or replace the system prompt, so on that
// path the instructions travel inside the single user message instead.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gemini"
)

// Request is one text generation.
type Request struct {
	System, Prompt string
	// Temperature is the caller's sampling temperature (0 = deterministic; the
	// planner uses 0.8–0.85). It goes out only to models that accept it —
	// not to Gemini 3+ ("strip temperature", Google) nor to OpenAI reasoning
	// models (o-series, GPT-5 and later: "remove temperature"), which then use
	// their own defaults; an endpoint that rejects it gets the request again
	// without it.
	Temperature float64
	// Schema is a Gemini responseSchema (google provider, JSON only).
	Schema any
	// Timeout per attempt; 0 = 180 s (openai) / 120 s (google).
	Timeout time.Duration
	// Images go along with the prompt to a model that can see them
	// (OpenAI-compatible image_url data URLs / Gemini inline data). A model
	// or gateway without vision fails the request.
	Images []Image
}

// Image is a picture for a vision request.
type Image struct {
	MIME string // image/png | image/jpeg | image/webp
	Data []byte
}

// textGen is the slice of *gemini.Client used here (a fake in tests).
type textGen interface {
	GenerateText(ctx context.Context, model, system, prompt string, temperature float64, jsonMode bool, schema any) (string, error)
}

// Client generates text with the provider selected in the settings.
type Client struct {
	get func() config.Config
	gem textGen
	gc  *gemini.Client // vision on the "google" provider (its key pool)
	hc  *http.Client
	// bases are the address forms found to work this session (resolve.go).
	bases resolvedBases
}

const (
	jsonInstruction = "Return ONLY valid JSON. No markdown fences, no commentary before or after the JSON."
	attempts        = 3
	maxBody         = 64 << 20
)

// Package vars so tests can shorten them.
var (
	defaultTimeout = 180 * time.Second
	modelsTimeout  = 30 * time.Second
	googleBase     = "https://generativelanguage.googleapis.com/v1beta" // vision requests
	backoff        = func(attempt int) time.Duration { return time.Duration(attempt+1) * 2 * time.Second }
	// maxRetryWait: a Retry-After longer than this is not waited out — the
	// error says when to try again instead.
	maxRetryWait = 90 * time.Second
)

// New creates a client. g serves the "google" provider (may be nil when unused).
func New(get func() config.Config, g *gemini.Client) *Client {
	c := &Client{get: get, hc: &http.Client{}}
	if g != nil {
		c.gem, c.gc = g, g
	}
	return c
}

func (c *Client) cfg() config.Config {
	if c.get != nil {
		return c.get()
	}
	d := config.Default()
	d.Normalize()
	return d
}

// Text generates plain text with the configured provider.
func (c *Client) Text(ctx context.Context, r Request) (string, error) {
	return c.generate(ctx, r, false)
}

// JSON asks for JSON, extracts it from the reply (fences/prose tolerated) and
// unmarshals it into out. When out is a slice but the model wrapped the array
// in an object ({"scenes":[…]}), the first array value is used.
func (c *Client) JSON(ctx context.Context, r Request, out any) error {
	raw, err := c.generate(ctx, r, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(raw) == "" {
		return errors.New("модель повернула порожню відповідь")
	}
	b, err := ExtractJSON(raw)
	if err != nil {
		return err
	}
	return decodeInto(b, out)
}

func (c *Client) generate(ctx context.Context, r Request, jsonMode bool) (string, error) {
	cfg := c.cfg()
	if cfg.Text.Provider == "google" {
		return c.googleText(ctx, cfg, r, jsonMode)
	}
	return c.remoteText(ctx, ConnOf(cfg.Text), r, jsonMode)
}

// ---- google -----------------------------------------------------------------------------

func (c *Client) googleText(ctx context.Context, cfg config.Config, r Request, jsonMode bool) (string, error) {
	if len(r.Images) > 0 {
		return c.googleVision(ctx, cfg, r, jsonMode)
	}
	if c.gem == nil {
		return "", errors.New("Google-клієнт не налаштовано")
	}
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	var schema any
	if jsonMode {
		schema = r.Schema
	}
	return c.gem.GenerateText(ctx, cfg.Google.StoryModel, r.System, r.Prompt, r.Temperature, jsonMode, schema)
}

// ---- openai-compatible ----------------------------------------------------------------

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model string `json:"model"`
	// Temperature is nil (not sent) unless the model accepts it.
	Temperature *float64      `json:"temperature,omitempty"`
	Stream      bool          `json:"stream"`
	Messages    []chatMessage `json:"messages"`
}

var (
	gptVersionRe = regexp.MustCompile(`^gpt-(\d+)`)
	oSeriesRe    = regexp.MustCompile(`^o\d`)
)

// acceptsTemperature reports whether the model behind an OpenAI-compatible
// endpoint takes a temperature. No: OpenAI reasoning models — the o-series
// and GPT-5 and later at their default reasoning effort ("When reasoning
// effort is not none, remove temperature, top_p", OpenAI latest-model guide)
// — and Gemini 3 and later through a gateway such as CarteLink (Google:
// "Strip temperature, top_p, and top_k"). Gateway prefixes ("openai/",
// "google/", "models/") are ignored.
func acceptsTemperature(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	if oSeriesRe.MatchString(m) {
		return false
	}
	if g := gptVersionRe.FindStringSubmatch(m); g != nil {
		if v, err := strconv.Atoi(g[1]); err == nil && v >= 5 {
			return false
		}
	}
	return gemini.AcceptsTemperature(m)
}

// temperatureFor is the request's temperature for model, or nil.
func temperatureFor(model string, r Request) *float64 {
	if !acceptsTemperature(model) {
		return nil
	}
	t := r.Temperature
	return &t
}

// userMessage folds system + JSON instructions into the one user message:
// "### INSTRUCTIONS\n…\n\n### INPUT\n…".
func userMessage(system, prompt string, jsonMode bool) string {
	var parts []string
	if s := strings.TrimSpace(system); s != "" {
		parts = append(parts, system)
	}
	if jsonMode {
		parts = append(parts, jsonInstruction)
	}
	if len(parts) == 0 {
		return prompt
	}
	return "### INSTRUCTIONS\n" + strings.Join(parts, "\n\n") + "\n\n### INPUT\n" + prompt
}

// tempRejected: a 400 that names the temperature parameter (the model takes
// none or only its default).
type tempRejected struct{ error }

func (e *tempRejected) Unwrap() error { return e.error }

// retryWait is a retryable failure with the server's Retry-After.
type retryWait struct {
	error
	wait time.Duration
}

func (e *retryWait) Unwrap() error { return e.error }

// retryAfter reads Retry-After (seconds or an HTTP date) or retry-after-ms.
func retryAfter(h http.Header) time.Duration {
	if ms, err := strconv.ParseFloat(strings.TrimSpace(h.Get("Retry-After-Ms")), 64); err == nil && ms > 0 {
		return time.Duration(ms * float64(time.Millisecond))
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if s, err := strconv.ParseFloat(v, 64); err == nil {
		if s <= 0 {
			return 0
		}
		return time.Duration(s * float64(time.Second))
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(0, time.Until(t))
	}
	return 0
}

// billingCodes are the 429 error codes that retrying never fixes: credits,
// spend or usage limits (OpenAI "Error codes": "Retrying billing, spend, or
// quota errors won't restore API access").
var billingCodes = map[string]bool{
	"insufficient_quota":                true,
	"credit_balance_exhausted":          true,
	"organization_spend_limit_exceeded": true,
	"project_spend_limit_exceeded":      true,
	"organization_usage_limit_exceeded": true,
	"billing_hard_limit_reached":        true,
}

// apiError is error.code / error.type / error.message of an OpenAI-style body.
func apiError(raw []byte) (code, typ, msg string) {
	var b struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &b) != nil {
		return "", "", ""
	}
	var o struct {
		Code    any    `json:"code"`
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b.Error, &o) == nil {
		if o.Code != nil {
			code = strings.TrimSpace(fmt.Sprint(o.Code))
		}
		return code, o.Type, o.Message
	}
	return "", "", errorText(b.Error)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// humanWait: "40 с", "12 хв".
func humanWait(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d с", max(1, int(d.Seconds()+0.999)))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d хв", int(d.Minutes()+0.999))
	}
	return fmt.Sprintf("%d год", int(d.Hours()+0.999))
}

// contentToText accepts message.content as a string or an array of parts
// (strings or {"type":"text","text":…}).
func contentToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		var ps string
		if json.Unmarshal(p, &ps) == nil {
			b.WriteString(ps)
			continue
		}
		var obj struct {
			Type string  `json:"type"`
			Text *string `json:"text"`
		}
		if json.Unmarshal(p, &obj) == nil && obj.Text != nil && (obj.Type == "" || obj.Type == "text" || obj.Type == "output_text") {
			b.WriteString(*obj.Text)
		}
	}
	return b.String()
}

// errorText reads an OpenAI-style error: "msg" or {"message":"msg"}.
func errorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &o) == nil && o.Message != "" {
		return o.Message
	}
	return string(raw)
}

// ---- helpers ----------------------------------------------------------------------------

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + "…"
}
