package llm

// The text endpoints the user connects with an address and a key
// (config.Text, every provider but "google"). Wire formats, checked against
// the providers' docs on 2026-10-05:
//
//	openai     POST {base}/chat/completions   Authorization: Bearer <key>
//	           (any OpenAI-compatible server: CarteLink, OpenRouter
//	           https://openrouter.ai/api/v1, Groq https://api.groq.com/openai/v1,
//	           Gemini …/v1beta/openai, Ollama …/v1, DeepSeek, Mistral, xAI …)
//	anthropic  POST {base}/messages            x-api-key: <key>, anthropic-version: 2023-06-01
//	           {model, max_tokens, system, messages, temperature ≤ 1}; reply
//	           content[] text blocks, stop_reason end_turn | max_tokens | refusal …
//	           (https://platform.claude.com/docs/en/api/messages)
//	ollama     POST {base}/api/chat            Bearer <key> only for Ollama Cloud
//	           {model, messages, stream:false, format:"json", options.temperature};
//	           reply message.content, done_reason (https://docs.ollama.com/api/chat)
//	azure      v1 API: POST https://{res}.openai.azure.com/openai/v1/chat/completions
//	           (model = the deployment name, no api-version); a deployment URL
//	           …/openai/deployments/{dep}/chat/completions?api-version=2024-10-21;
//	           api-key: <key> (learn.microsoft.com … api-version-lifecycle)
//
// Config.Text.Auth overrides how the key travels (bearer | x-api-key |
// api-key | none). Addresses are normalised (a pasted …/chat/completions or
// …/models loses its endpoint) and, when the server answers 404/405, the
// usual forms are tried (+/v1, /api/v1, /openai/v1 — resolve.go); the form
// that works is remembered for the session and reported through OnResolved
// so the engine can store it. Every failure names the request (method + URL
// without the key), the status and what the server said.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/httpdiag"
)

// Protocols of a Conn.
const (
	ProtoOpenAI    = "openai"
	ProtoAnthropic = "anthropic"
	ProtoOllama    = "ollama"
	ProtoAzure     = "azure"
)

// AnthropicVersion is the anthropic-version header (still the current value).
const AnthropicVersion = "2023-06-01"

// AzureAPIVersion is the api-version of Azure deployment URLs (the latest
// dated GA; the v1 API needs none).
const AzureAPIVersion = "2024-10-21"

// anthropicMaxTokens is max_tokens for the Messages API (required there);
// a model with a smaller output limit gets anthropicMaxTokensSmall.
const (
	anthropicMaxTokens      = 16000
	anthropicMaxTokensSmall = 4096
)

// Conn is one text endpoint.
type Conn struct {
	Protocol   string // openai | anthropic | ollama | azure
	BaseURL    string
	APIKey     string
	Model      string
	Auth       string // "" = the protocol's own | bearer | x-api-key | api-key | none
	APIVersion string // Azure deployment URLs ("" = AzureAPIVersion)
}

// ConnOf is the connection of the Text settings.
func ConnOf(t config.Text) Conn {
	p := t.Provider
	switch p {
	case ProtoAnthropic, ProtoOllama, ProtoAzure:
	default:
		p = ProtoOpenAI
	}
	return Conn{Protocol: p, BaseURL: strings.TrimSpace(t.BaseURL), APIKey: strings.TrimSpace(t.APIKey),
		Model: strings.TrimSpace(t.Model), Auth: strings.TrimSpace(t.Auth), APIVersion: strings.TrimSpace(t.APIVersion)}
}

// ProtocolLabel is the name of a protocol for messages.
func ProtocolLabel(p string) string {
	switch p {
	case ProtoAnthropic:
		return "Anthropic"
	case ProtoOllama:
		return "Ollama"
	case ProtoAzure:
		return "Azure OpenAI"
	case "google":
		return "Google Gemini"
	}
	return "OpenAI-сумісний"
}

// AuthMode is how c's key travels.
func (c Conn) AuthMode() string {
	if c.Auth != "" {
		return c.Auth
	}
	switch c.Protocol {
	case ProtoAnthropic:
		return "x-api-key"
	case ProtoAzure:
		return "api-key"
	}
	return "bearer"
}

func (c Conn) setHeaders(h http.Header, body bool) {
	if body {
		h.Set("Content-Type", "application/json")
	}
	h.Set("Accept", "application/json")
	if c.Protocol == ProtoAnthropic {
		h.Set("anthropic-version", AnthropicVersion)
	}
	key := strings.TrimSpace(c.APIKey)
	if key == "" {
		return
	}
	switch c.AuthMode() {
	case "none":
	case "x-api-key":
		h.Set("x-api-key", key)
	case "api-key":
		h.Set("api-key", key)
	default:
		h.Set("Authorization", "Bearer "+key)
	}
}

// azureDeployment: base is a classic deployment URL (…/openai/deployments/{dep}).
func azureDeployment(base string) bool {
	return strings.Contains(strings.ToLower(base), "/openai/deployments/")
}

// chatURL is the generation endpoint of base.
func (c Conn) chatURL(base string) string {
	switch c.Protocol {
	case ProtoAnthropic:
		return base + "/messages"
	case ProtoOllama:
		return base + "/api/chat"
	case ProtoAzure:
		if azureDeployment(base) {
			return base + "/chat/completions?api-version=" + firstNonEmpty(c.APIVersion, AzureAPIVersion)
		}
	}
	return base + "/chat/completions"
}

// modelsURL is the model list of base ("" = none: an Azure deployment).
func (c Conn) modelsURL(base string) string {
	switch c.Protocol {
	case ProtoAnthropic:
		return base + "/models?limit=1000"
	case ProtoOllama:
		return base + "/api/tags"
	case ProtoAzure:
		if azureDeployment(base) {
			return ""
		}
	}
	return base + "/models"
}

// secrets are what never shows in a message.
func (c Conn) secrets() []string { return []string{c.APIKey} }

// ---- errors -------------------------------------------------------------------------------

// HTTPError is a failed call to the text endpoint: a friendly first line,
// then the request, the status, the reply and a hint (httpdiag.Detail).
type HTTPError struct {
	Msg    string // «Текстовий ендпоінт відповів помилкою 404»
	Method string
	URL    string
	Status int // 0 = no answer
	Body   []byte
	Hint   string
	Err    string // transport error (Status 0)
	key    string
}

func (e *HTTPError) Error() string {
	var d string
	if e.Status == 0 {
		d = "Запит: " + httpdiag.Line(e.Method, e.URL, 0, e.key) + " → немає відповіді"
		if e.Err != "" {
			d += " (" + httpdiag.Redact(clip(e.Err, 160), e.key) + ")"
		}
		if e.Hint != "" {
			d += "\nПідказка: " + e.Hint
		}
	} else {
		d = httpdiag.Detail(e.Method, e.URL, e.Status, e.Body, firstNonEmpty(e.Hint, ""), e.key)
	}
	return httpdiag.Redact(e.Msg, e.key) + "\n" + d
}

// pathMissing: the server has no such endpoint here (404/405 that is not
// about the model) — another form of the address may work.
func (e *HTTPError) pathMissing() bool {
	if e == nil {
		return false
	}
	if e.Status == http.StatusMethodNotAllowed {
		return true
	}
	return e.Status == http.StatusNotFound && !aboutModel(e.Body)
}

// aboutModel: a 404 body that names the model or deployment (the address is
// right, the model is not).
func aboutModel(body []byte) bool {
	low := strings.ToLower(string(body))
	if strings.Contains(low, "model_not_found") || strings.Contains(low, "deploymentnotfound") || strings.Contains(low, "not_found_error") {
		return strings.Contains(low, "model") || strings.Contains(low, "deployment")
	}
	return (strings.Contains(low, "model") || strings.Contains(low, "deployment")) &&
		(strings.Contains(low, "not found") || strings.Contains(low, "does not exist") || strings.Contains(low, "not exist") ||
			strings.Contains(low, "unknown") || strings.Contains(low, "no such") || strings.Contains(low, "not available") ||
			strings.Contains(low, "invalid"))
}

// ---- one generation -----------------------------------------------------------------------

// remoteText generates with an address-and-key endpoint: retries on rate
// limits and server trouble, drops a rejected temperature (or Anthropic's
// max_tokens), and on a missing endpoint tries the other address forms once.
func (c *Client) remoteText(ctx context.Context, conn Conn, r Request, jsonMode bool) (string, error) {
	if strings.TrimSpace(conn.BaseURL) == "" {
		return "", errors.New("Текстовий ендпоінт не задано (Налаштування → Текст і сценарій)")
	}
	if _, ver := NormalizeBase(conn.Protocol, conn.BaseURL); ver != "" && conn.APIVersion == "" {
		conn.APIVersion = ver // a pasted Azure URL's api-version
	}
	base := c.baseFor(conn)
	temp := temperatureFor(conn.Model, r)
	if conn.Protocol == ProtoAnthropic && temp != nil && *temp > 1 {
		t := 1.0 // Anthropic: 0–1
		temp = &t
	}
	if conn.Protocol == ProtoOllama && temp == nil {
		t := r.Temperature // Ollama's models all take it
		temp = &t
	}
	maxTok := anthropicMaxTokens
	build := func() ([]byte, error) { return requestBody(conn, r, jsonMode, temp, maxTok) }
	body, err := build()
	if err != nil {
		return "", err
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	resolved := false
	var lastErr error
	var wait time.Duration
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, max(backoff(attempt-1), wait)); err != nil {
				return "", err
			}
		}
		text, retry, err := c.remoteOnce(ctx, conn, base, body, timeout, jsonMode)
		if err == nil {
			return text, nil
		}
		var he *HTTPError
		if errors.As(err, &he) && he.pathMissing() && !resolved {
			// The address may lack /v1 (or have one too many): find the form
			// the server answers on, then the same request again.
			resolved = true
			if nb, ok := c.rediscover(ctx, conn, base); ok && nb != base {
				base = nb
				attempt--
				continue
			}
			return "", err
		}
		var tr *tempRejected
		if errors.As(err, &tr) && temp != nil && conn.Protocol != ProtoOllama {
			temp = nil
			if body, err = build(); err != nil {
				return "", err
			}
			attempt--
			continue
		}
		var mr *maxTokensRejected
		if errors.As(err, &mr) && maxTok != anthropicMaxTokensSmall {
			maxTok = anthropicMaxTokensSmall
			if body, err = build(); err != nil {
				return "", err
			}
			attempt--
			continue
		}
		if !retry {
			return "", err
		}
		lastErr, wait = err, 0
		var rw *retryWait
		if errors.As(err, &rw) {
			if rw.wait > maxRetryWait {
				return "", fmt.Errorf("%w — спробуй через %s", err, humanWait(rw.wait))
			}
			wait = rw.wait
		}
	}
	return "", lastErr
}

// maxTokensRejected: Anthropic refused max_tokens (a model with a smaller
// output limit).
type maxTokensRejected struct{ error }

func (e *maxTokensRejected) Unwrap() error { return e.error }

// requestBody is the generation request of conn's protocol.
func requestBody(conn Conn, r Request, jsonMode bool, temp *float64, maxTok int) ([]byte, error) {
	switch conn.Protocol {
	case ProtoAnthropic:
		system := strings.TrimSpace(r.System)
		if jsonMode {
			system = strings.TrimSpace(system + "\n\n" + jsonInstruction)
		}
		var content any = r.Prompt
		if len(r.Images) > 0 {
			parts := make([]map[string]any, 0, len(r.Images)+1)
			for _, im := range r.Images {
				parts = append(parts, map[string]any{"type": "image", "source": map[string]any{
					"type": "base64", "media_type": imageMIME(im), "data": base64.StdEncoding.EncodeToString(im.Data)}})
			}
			parts = append(parts, map[string]any{"type": "text", "text": r.Prompt})
			content = parts
		}
		m := map[string]any{
			"model":      conn.Model,
			"max_tokens": maxTok,
			"messages":   []any{map[string]any{"role": "user", "content": content}},
		}
		if system != "" {
			m["system"] = system
		}
		if temp != nil {
			m["temperature"] = *temp
		}
		return json.Marshal(m)
	case ProtoOllama:
		var msgs []any
		system := strings.TrimSpace(r.System)
		if system != "" {
			msgs = append(msgs, map[string]any{"role": "system", "content": system})
		}
		prompt := r.Prompt
		if jsonMode {
			prompt = jsonInstruction + "\n\n" + prompt
		}
		user := map[string]any{"role": "user", "content": prompt}
		if len(r.Images) > 0 {
			imgs := make([]string, 0, len(r.Images))
			for _, im := range r.Images {
				imgs = append(imgs, base64.StdEncoding.EncodeToString(im.Data))
			}
			user["images"] = imgs
		}
		msgs = append(msgs, user)
		m := map[string]any{"model": conn.Model, "messages": msgs, "stream": false}
		if jsonMode {
			m["format"] = "json"
		}
		if temp != nil {
			m["options"] = map[string]any{"temperature": *temp}
		}
		return json.Marshal(m)
	}
	// OpenAI wire format (openai, azure).
	if len(r.Images) > 0 {
		vr := visionRequest(conn.Model, r, jsonMode)
		vr.Temperature = temp
		return json.Marshal(vr)
	}
	return json.Marshal(chatRequest{
		Model:       conn.Model,
		Temperature: temp,
		Messages:    []chatMessage{{Role: "user", Content: userMessage(r.System, r.Prompt, jsonMode)}},
	})
}

func imageMIME(im Image) string {
	if m := strings.TrimSpace(im.MIME); m != "" {
		return m
	}
	return http.DetectContentType(im.Data)
}

// remoteOnce is one attempt; retry reports a transient failure (rate limit
// 429 — not billing —, 5xx, timeout, dropped connection); a retryWait error
// carries the server's Retry-After.
func (c *Client) remoteOnce(ctx context.Context, conn Conn, base string, body []byte, timeout time.Duration, jsonMode bool) (text string, retry bool, err error) {
	u := conn.chatURL(base)
	res, raw, err := c.send(ctx, conn, http.MethodPost, u, body, timeout)
	if err != nil {
		return "", isRetryNet(err), err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return statusError(conn, u, res, raw)
	}
	text, err = parseReply(conn, raw, jsonMode)
	if err != nil {
		return "", false, &HTTPError{Msg: err.Error(), Method: http.MethodPost, URL: u, Status: res.StatusCode, Body: raw, Hint: "-", key: conn.APIKey}
	}
	return text, false, nil
}

// netError is a transport failure (no answer).
type netError struct {
	*HTTPError
	retry bool
}

func (e *netError) Unwrap() error { return e.HTTPError }

func isRetryNet(err error) bool {
	var ne *netError
	return errors.As(err, &ne) && ne.retry
}

// send makes one call and reads the reply (recorded for checks).
func (c *Client) send(ctx context.Context, conn Conn, method, u string, body []byte, timeout time.Duration) (*http.Response, []byte, error) {
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(actx, method, u, rd)
	if err != nil {
		return nil, nil, &HTTPError{Msg: fmt.Sprintf("Текстовий ендпоінт: некоректна адреса «%s» (%v)", httpdiag.URL(u, conn.APIKey), err),
			Method: method, URL: u, Hint: "потрібна адреса на кшталт https://сервер/v1", key: conn.APIKey}
	}
	conn.setHeaders(req.Header, body != nil)
	res, err := c.hc.Do(req)
	if err != nil {
		httpdiag.Record(ctx, method, u, 0, nil, err)
		return nil, nil, netFailure(ctx, actx, conn, method, u, err)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	res.Body.Close()
	if err != nil {
		httpdiag.Record(ctx, method, u, 0, nil, err)
		return nil, nil, netFailure(ctx, actx, conn, method, u, err)
	}
	httpdiag.Record(ctx, method, u, res.StatusCode, raw, nil)
	return res, raw, nil
}

// statusError classifies a non-2xx reply.
func statusError(conn Conn, u string, res *http.Response, raw []byte) (string, bool, error) {
	code, typ, msg := apiError(raw)
	snippet := clip(firstNonEmpty(msg, string(raw)), 300)
	wait := retryAfter(res.Header)
	st := res.StatusCode
	he := &HTTPError{Method: http.MethodPost, URL: u, Status: st, Body: raw, key: conn.APIKey}
	switch {
	case st == 429 && (billingCodes[code] || billingCodes[typ]):
		he.Msg = fmt.Sprintf("Текстовий ендпоінт: вичерпано кредити або ліміт витрат акаунта (%s) — поповни баланс чи підніми ліміт у кабінеті провайдера", firstNonEmpty(code, typ))
		return "", false, he
	case st == 402 || typ == "billing_error":
		he.Msg = "Текстовий ендпоінт: на рахунку бракує коштів (402) — поповни баланс у кабінеті провайдера"
		return "", false, he
	case st == 429:
		he.Msg = "Текстовий ендпоінт: забагато запитів (429)"
		return "", true, &retryWait{he, wait}
	case st >= 500:
		he.Msg = fmt.Sprintf("Текстовий ендпоінт відповів помилкою %d", st)
		return "", true, &retryWait{he, wait}
	case st == 401 || st == 403:
		he.Msg = fmt.Sprintf("Текстовий ендпоінт відхилив ключ (%d). Перевір API-ключ у Налаштуваннях.", st)
		he.Hint = authHint(conn)
		return "", false, he
	case st == 400 && conn.Protocol == ProtoAnthropic && strings.Contains(strings.ToLower(string(raw)), "max_tokens"):
		he.Msg = fmt.Sprintf("Текстовий ендпоінт відповів помилкою %d", st)
		return "", false, &maxTokensRejected{he}
	case st == 400 && strings.Contains(strings.ToLower(string(raw)), "temperature"):
		he.Msg = fmt.Sprintf("Текстовий ендпоінт відповів помилкою %d", st)
		return "", false, &tempRejected{he}
	case st == 404 && aboutModel(raw):
		he.Msg = fmt.Sprintf("Текстовий ендпоінт: модель «%s» не знайдено (404)", conn.Model)
		he.Hint = "адреса правильна, а такої моделі немає — вибери модель зі списку (кнопка ↻ біля поля «Модель»)"
		return "", false, he
	}
	he.Msg = fmt.Sprintf("Текстовий ендпоінт відповів помилкою %d: %s", st, snippet)
	if st == 404 || st == 405 {
		he.Hint = pathHint(conn, u)
	}
	return "", false, he
}

// authHint suggests the other ways a key can travel.
func authHint(conn Conn) string {
	switch conn.AuthMode() {
	case "bearer":
		return "ключ надіслано як «Authorization: Bearer». Якщо сервіс чекає інший заголовок (x-api-key, api-key) — зміни «Авторизацію» в картці"
	case "x-api-key":
		return "ключ надіслано в заголовку x-api-key. Якщо сервіс чекає «Authorization: Bearer» — зміни «Авторизацію» в картці"
	case "api-key":
		return "ключ надіслано в заголовку api-key (Azure). Перевір ключ ресурсу або зміни «Авторизацію»"
	}
	return "ключ не надсилається («Без ключа») — якщо сервіс його вимагає, зміни «Авторизацію»"
}

// pathHint is the advice for an address the server has no endpoint at.
func pathHint(conn Conn, u string) string {
	switch conn.Protocol {
	case ProtoAnthropic:
		return "адреса Anthropic має закінчуватися на /v1 (https://api.anthropic.com/v1)"
	case ProtoOllama:
		return "адреса Ollama — корінь сервера, напр. http://127.0.0.1:11434"
	case ProtoAzure:
		return "для Azure вкажи https://<ресурс>.openai.azure.com (v1 API) або адресу розгортання …/openai/deployments/<назва>"
	}
	return "сервер не знає цього методу за цією адресою — найчастіше бракує /v1 (напр. https://сервер/v1) або /api/v1"
}

// parseReply reads the text out of a 2xx reply.
func parseReply(conn Conn, raw []byte, jsonMode bool) (string, error) {
	switch conn.Protocol {
	case ProtoAnthropic:
		var resp struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			StopReason  string          `json:"stop_reason"`
			StopDetails json.RawMessage `json:"stop_details"`
			Error       json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return "", fmt.Errorf("Текстовий ендпоінт повернув не JSON: %s", clip(string(raw), 200))
		}
		var b strings.Builder
		for _, p := range resp.Content {
			if p.Type == "text" {
				b.WriteString(p.Text)
			}
		}
		text := b.String()
		switch resp.StopReason {
		case "refusal":
			var d struct {
				Explanation string `json:"explanation"`
			}
			_ = json.Unmarshal(resp.StopDetails, &d)
			return "", fmt.Errorf("Модель відмовилася відповідати%s", prefixedQuote(d.Explanation))
		case "max_tokens", "model_context_window_exceeded":
			if strings.TrimSpace(text) == "" || jsonMode {
				return "", errors.New("Відповідь моделі обрізано лімітом токенів (stop_reason max_tokens) — спробуй коротший запит або іншу модель")
			}
		}
		if strings.TrimSpace(text) == "" {
			if msg := errorText(resp.Error); msg != "" {
				return "", fmt.Errorf("Текстовий ендпоінт повернув помилку: %s", clip(msg, 300))
			}
			return "", fmt.Errorf("Текстовий ендпоінт повернув порожню відповідь: %s", clip(string(raw), 200))
		}
		return text, nil
	case ProtoOllama:
		var resp struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Response   string `json:"response"` // /api/generate shape
			DoneReason string `json:"done_reason"`
			Error      string `json:"error"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return "", fmt.Errorf("Текстовий ендпоінт повернув не JSON: %s", clip(string(raw), 200))
		}
		if resp.Error != "" {
			return "", fmt.Errorf("Ollama повернув помилку: %s", clip(resp.Error, 300))
		}
		text := firstNonEmpty(resp.Message.Content, resp.Response)
		if resp.DoneReason == "length" && (text == "" || jsonMode) {
			return "", errors.New("Відповідь моделі обрізано лімітом токенів (done_reason length) — спробуй коротший запит або іншу модель")
		}
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("Текстовий ендпоінт повернув порожню відповідь: %s", clip(string(raw), 200))
		}
		return text, nil
	}
	return parseChat(raw, jsonMode)
}

func prefixedQuote(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return ""
	}
	return ": «" + clip(s, 300) + "»"
}

// parseChat reads an OpenAI chat completion.
func parseChat(raw []byte, jsonMode bool) (string, error) {
	var resp struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
				Refusal *string         `json:"refusal"`
			} `json:"message"`
			Text         string `json:"text"` // legacy completions shape
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("Текстовий ендпоінт повернув не JSON: %s", clip(string(raw), 200))
	}
	if len(resp.Choices) == 0 {
		if msg := errorText(resp.Error); msg != "" {
			return "", fmt.Errorf("Текстовий ендпоінт повернув помилку: %s", clip(msg, 300))
		}
		return "", fmt.Errorf("Текстовий ендпоінт повернув порожню відповідь: %s", clip(string(raw), 200))
	}
	ch := resp.Choices[0]
	text := contentToText(ch.Message.Content)
	if text == "" {
		text = ch.Text
	}
	if ch.Message.Refusal != nil && strings.TrimSpace(*ch.Message.Refusal) != "" {
		return "", fmt.Errorf("Модель відмовилася відповідати: «%s»", clip(*ch.Message.Refusal, 300))
	}
	empty := strings.TrimSpace(text) == ""
	switch ch.FinishReason {
	case "content_filter":
		return "", errors.New("Відповідь моделі заблоковано фільтром вмісту (finish_reason content_filter) — переформулюй запит")
	case "length":
		if empty || jsonMode {
			return "", errors.New("Відповідь моделі обрізано лімітом токенів (finish_reason length) — модель витратила ліміт, не дописавши відповідь; спробуй коротший запит або іншу модель")
		}
	}
	if empty {
		return "", fmt.Errorf("Текстовий ендпоінт повернув порожню відповідь: %s", clip(string(raw), 200))
	}
	return text, nil
}

// netFailure classifies a transport error: caller cancel → stop; our
// per-attempt deadline → retry as timeout; refused/unreachable → stop with a
// hint; anything else (reset, EOF) → retry.
func netFailure(ctx, actx context.Context, conn Conn, method, u string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	inner := err
	var ue *url.Error
	if errors.As(err, &ue) {
		inner = ue.Err
	}
	he := &HTTPError{Method: method, URL: u, Err: inner.Error(), key: conn.APIKey}
	host := hostOf(u)
	var ne net.Error
	if actx.Err() != nil || (errors.As(err, &ne) && ne.Timeout()) {
		he.Msg, he.Err = "Текстовий ендпоінт не відповів вчасно (timeout)", ""
		return &netError{he, true}
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		he.Msg = fmt.Sprintf("Текстовий ендпоінт %s недоступний", host)
		he.Hint = "перевір адресу й інтернет; якщо це програма на цьому комп'ютері (CarteLink, Ollama) — чи вона запущена"
		var dns *net.DNSError
		if errors.As(err, &dns) {
			he.Hint = "такого сервера не знайдено — перевір адресу (домен)"
		}
		return &netError{he, false}
	}
	he.Msg = fmt.Sprintf("Текстовий ендпоінт %s: з'єднання обірвалося", host)
	return &netError{he, true}
}

func hostOf(u string) string {
	s := u
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	return s
}
