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
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/httpdiag"
)

// Endpoints and timings; package vars so tests can point them at a mock.
var (
	base         = "https://generativelanguage.googleapis.com/v1beta"
	genTimeout   = 120 * time.Second // one generateContent / start call
	pollInterval = 6 * time.Second   // Veo operation / Omni interaction + file polling
	// pollMax is the render budget of one clip. Veo documents 11 s … 6 min
	// "(during peak hours)" (https://ai.google.dev/gemini-api/docs/veo#limitations),
	// so 6 min would cut off exactly the slowest documented renders.
	pollMax   = 10 * time.Minute
	dlTimeout = 5 * time.Minute // mp4 download
)

const (
	maxBody      = 512 << 20 // hard cap on any response body
	maxPollBlips = 3         // tolerated transient poll failures in a row
)

// Client talks to the Gemini API with keys from cfg.Google.Keys. Safe for
// concurrent use.
type Client struct {
	get  func() config.Config
	pool *Pool
	hc   *http.Client

	mu   sync.Mutex
	keys []string // key list last pushed into pool
}

// New creates a client; the pool is re-synced from cfg.Google.Keys on every
// call (a cheap compare — cooldowns survive until the key list changes).
func New(get func() config.Config) *Client {
	return &Client{get: get, pool: NewPool(nil), hc: &http.Client{}}
}

// Pool exposes the key pool (size / health for the settings screen).
func (c *Client) Pool() *Pool {
	c.sync()
	return c.pool
}

func (c *Client) sync() config.Config {
	var cfg config.Config
	if c.get != nil {
		cfg = c.get()
	} else {
		cfg = config.Default()
		cfg.Normalize()
	}
	c.mu.Lock()
	if !slices.Equal(c.keys, cfg.Google.Keys) {
		c.keys = append([]string(nil), cfg.Google.Keys...)
		c.pool.SetKeys(c.keys)
	}
	c.mu.Unlock()
	return cfg
}

// ---- errors ------------------------------------------------------------------------

// APIError is a non-2xx reply from the Google API.
type APIError struct {
	Label  string // e.g. "gemini:gemini-3.8-flash"
	Status int
	Body   string
}

// quota: the key's project hit a rate limit / quota, or its prepaid credits
// ran out (402) — another key (another project) may still work.
func (e *APIError) quota() bool { return e.Status == 402 || isRateLimit(e.Status, e.Body) }

// quotaHint reads the quota window out of the error details.
func (e *APIError) quotaHint() quotaHint {
	h := bodyHint(e.Body)
	h.billing = e.Status == 402
	return h
}

func (e *APIError) Error() string {
	detail := fmt.Sprint(e.Status)
	msg, status := googleMessage(e.Body)
	if status != "" {
		detail += " " + status
	}
	switch {
	case msg != "":
		detail += ": " + clip(msg, 300)
	case strings.TrimSpace(e.Body) != "":
		detail += ": " + clip(e.Body, 300)
	}
	if h := e.hint(); h != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Label, h, detail)
	}
	return e.Label + " " + detail
}

// hint is a plain-words Ukrainian explanation for the usual failures.
func (e *APIError) hint() string {
	b := strings.ToUpper(e.Body)
	switch {
	case e.Status == 402:
		return "у проєкті Google закінчилися передплачені кошти — поповни баланс в AI Studio"
	case e.quota():
		h := e.quotaHint()
		switch {
		case h.daily:
			return "денну квоту проєкту Google вичерпано — вона оновиться опівночі за тихоокеанським часом"
		case h.minute:
			return "забагато запитів за хвилину — ліміт проєкту Google, спробуй трохи згодом"
		}
		return "квоту ключа Google вичерпано"
	case strings.Contains(b, "API_KEY_INVALID") || strings.Contains(b, "API KEY NOT VALID") || strings.Contains(b, "API_KEY_EXPIRED"):
		return "ключ Google недійсний — перевір його в Налаштуваннях"
	case strings.Contains(b, "USER LOCATION IS NOT SUPPORTED"):
		return "Gemini API недоступний у твоєму регіоні"
	case e.Status == 401 || e.Status == 403:
		return "Google відмовив у доступі — ключ не має доступу до цієї моделі або API не ввімкнено"
	case e.Status == 404:
		return "модель не знайдена — перевір назву моделі в Налаштуваннях"
	case e.Status >= 500:
		return "сервер Google тимчасово недоступний, спробуй ще раз"
	}
	return ""
}

// googleMessage pulls error.message / error.status out of a Google error body
// ({"error":{…}} or [{"error":{…}}]).
func googleMessage(body string) (msg, status string) {
	type gErr struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	var one gErr
	if json.Unmarshal([]byte(body), &one) == nil && one.Error.Message != "" {
		return one.Error.Message, one.Error.Status
	}
	var many []gErr
	if json.Unmarshal([]byte(body), &many) == nil && len(many) > 0 {
		return many[0].Error.Message, many[0].Error.Status
	}
	return "", ""
}

// ---- transport ----------------------------------------------------------------------

// call performs one request with the given key (x-goog-api-key header — the
// key never appears in URLs or error messages).
func (c *Client) call(ctx context.Context, method, u, key string, body []byte, label string) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-api-key", key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		httpdiag.Record(ctx, method, u, 0, nil, err) // «Перевірити» shows the exact request
		return nil, netErr(ctx, label, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		httpdiag.Record(ctx, method, u, 0, nil, err)
		return nil, netErr(ctx, label, err)
	}
	httpdiag.Record(ctx, method, u, res.StatusCode, b, nil)
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, &APIError{Label: label, Status: res.StatusCode, Body: string(b)}
	}
	return b, nil
}

func netErr(ctx context.Context, label string, err error) error {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return fmt.Errorf("%s: Google не відповів вчасно (timeout): %w", label, context.DeadlineExceeded)
	case context.Canceled:
		return context.Canceled
	}
	return fmt.Errorf("%s: немає зв'язку з Google API: %w", label, err)
}

// withDefaultTimeout keeps the caller's deadline if it has one.
func withDefaultTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

func modelPath(model string) string {
	return url.PathEscape(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
}

// ---- generateContent ----------------------------------------------------------------

type blob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type part struct {
	Text       string `json:"text,omitempty"`
	InlineData *blob  `json:"inlineData,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type imageConfig struct {
	AspectRatio string `json:"aspectRatio,omitempty"`
}

type genConfig struct {
	Temperature        *float64     `json:"temperature,omitempty"`
	ResponseMimeType   string       `json:"responseMimeType,omitempty"`
	ResponseSchema     any          `json:"responseSchema,omitempty"`
	ResponseModalities []string     `json:"responseModalities,omitempty"`
	ImageConfig        *imageConfig `json:"imageConfig,omitempty"`
}

type genRequest struct {
	Contents          []content  `json:"contents"`
	SystemInstruction *content   `json:"systemInstruction,omitempty"`
	GenerationConfig  *genConfig `json:"generationConfig,omitempty"`
}

type respBlob struct {
	MimeType  string `json:"mimeType"`
	MimeType2 string `json:"mime_type"`
	Data      string `json:"data"`
}

type respPart struct {
	Text        string    `json:"text"`
	Thought     bool      `json:"thought"`
	InlineData  *respBlob `json:"inlineData"`
	InlineData2 *respBlob `json:"inline_data"`
}

type genResponse struct {
	Candidates []struct {
		Content struct {
			Parts []respPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

// text joins the answer's text parts (thought summaries skipped).
func (r *genResponse) text() string {
	if len(r.Candidates) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range r.Candidates[0].Content.Parts {
		if !p.Thought {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// inline is the first inlineData (image bytes) part of the answer, or nil.
// Thought parts are skipped: Gemini 3 image models think with up to two
// interim "thought images" before the final one
// (https://ai.google.dev/gemini-api/docs/image-generation).
func (r *genResponse) inline() *respBlob {
	if len(r.Candidates) == 0 {
		return nil
	}
	for _, p := range r.Candidates[0].Content.Parts {
		if p.Thought {
			continue
		}
		for _, b := range []*respBlob{p.InlineData, p.InlineData2} {
			if b != nil && b.Data != "" {
				return b
			}
		}
	}
	return nil
}

// why explains an answer without the expected content (for error messages).
func (r *genResponse) why() string {
	var bits []string
	if r.PromptFeedback != nil && r.PromptFeedback.BlockReason != "" {
		bits = append(bits, "blockReason "+r.PromptFeedback.BlockReason)
	}
	if len(r.Candidates) > 0 {
		if f := r.Candidates[0].FinishReason; f != "" && f != "STOP" {
			bits = append(bits, "finishReason "+f)
		}
		if t := strings.TrimSpace(r.text()); t != "" {
			bits = append(bits, "«"+clip(t, 160)+"»")
		}
	} else if len(bits) == 0 {
		bits = append(bits, "немає кандидатів")
	}
	if len(bits) == 0 {
		return ""
	}
	return ": " + strings.Join(bits, ", ")
}

// generateContent is one load-balanced generateContent call: quota failures
// move to the next key, other errors propagate.
func (c *Client) generateContent(ctx context.Context, model string, req genRequest) (*genResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	m := modelPath(model)
	u := base + "/models/" + m + ":generateContent"
	label := "gemini:" + m
	var out *genResponse
	err = c.pool.Do(ctx, func(key string) error {
		actx, cancel := withDefaultTimeout(ctx, genTimeout)
		defer cancel()
		b, err := c.call(actx, http.MethodPost, u, key, body, label)
		if err != nil {
			return err
		}
		var r genResponse
		if err := json.Unmarshal(b, &r); err != nil {
			return fmt.Errorf("%s: незрозуміла відповідь Google: %v", label, err)
		}
		out = &r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GenerateText asks model (default cfg.Google.StoryModel) for text. jsonMode
// sets responseMimeType application/json (+ responseSchema when schema != nil).
// temperature goes out only to models that accept it (AcceptsTemperature).
// Uses ctx's deadline when set, else 120 s per attempt.
func (c *Client) GenerateText(ctx context.Context, model, system, prompt string, temperature float64, jsonMode bool, schema any) (string, error) {
	cfg := c.sync()
	if strings.TrimSpace(model) == "" {
		model = cfg.Google.StoryModel
	}
	gc := &genConfig{}
	if AcceptsTemperature(model) {
		gc.Temperature = &temperature
	}
	if jsonMode {
		gc.ResponseMimeType = "application/json"
		if schema != nil {
			gc.ResponseSchema = schema
		}
	}
	req := genRequest{Contents: []content{{Role: "user", Parts: []part{{Text: prompt}}}}}
	if gc.Temperature != nil || jsonMode {
		req.GenerationConfig = gc
	}
	if strings.TrimSpace(system) != "" {
		req.SystemInstruction = &content{Parts: []part{{Text: system}}}
	}
	resp, err := c.generateContent(ctx, model, req)
	if err != nil {
		return "", err
	}
	if t := resp.text(); strings.TrimSpace(t) != "" {
		return t, nil
	}
	return "", fmt.Errorf("Gemini (%s) повернув порожню відповідь%s", modelPath(model), resp.why())
}

var geminiVersionRe = regexp.MustCompile(`^gemini-(\d+)`)

// AcceptsTemperature reports whether a Gemini model takes the sampling
// parameters (temperature, top_p, top_k). Gemini 3 and later want them
// stripped ("Strip temperature, top_p, and top_k from generation configs",
// https://ai.google.dev/gemini-api/docs/generate-content/latest-model); the
// "-latest" aliases point at them too. Older models (2.x, Gemma …) take them.
func AcceptsTemperature(model string) bool {
	m := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
	if strings.HasPrefix(m, "gemini-") && strings.HasSuffix(m, "-latest") {
		return false
	}
	if g := geminiVersionRe.FindStringSubmatch(m); g != nil {
		if v, err := strconv.Atoi(g[1]); err == nil && v >= 3 {
			return false
		}
	}
	return true
}

// Test checks that at least one key works (GET models?pageSize=1).
func (c *Client) Test(ctx context.Context) error {
	c.sync()
	return c.pool.Do(ctx, func(key string) error {
		actx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_, err := c.call(actx, http.MethodGet, base+"/models?pageSize=1", key, nil, "google:test")
		return err
	})
}

// ImageModelFor maps the UI image model id to a Gemini image model:
// GEM_PIX → Nano Banana 2 Lite, NARWHAL → Nano Banana 2, GEM_PIX_2 → Nano
// Banana Pro. Anything else → fallback (cfg.Google.ImageModel), then the default.
func ImageModelFor(uiModel, fallback string) string {
	switch strings.TrimSpace(uiModel) {
	case "GEM_PIX":
		return "gemini-3.1-flash-lite-image"
	case "GEM_PIX_2":
		return "gemini-3-pro-image"
	case "NARWHAL":
		return "gemini-3.1-flash-image"
	}
	if f := strings.TrimSpace(fallback); f != "" {
		return f
	}
	return "gemini-3.1-flash-image"
}

// decodeB64 accepts standard or URL-safe base64, padded or not.
func decodeB64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("некоректні base64-дані")
}
