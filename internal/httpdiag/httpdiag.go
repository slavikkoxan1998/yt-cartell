// Package httpdiag describes a failed HTTP call for the «Перевірити» buttons
// of Settings: the method and the URL (without a key), the status, what the
// provider answered (trimmed, secrets redacted) and a hint. The services
// record their last request into a Trace carried by the context, so a check
// can add the exact request to its friendly message without each provider
// threading it through its error types.
//
// No internal imports (used by llm, tts, stt, providers, studio).
package httpdiag

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

// secretParams are query parameters that carry credentials.
var secretParams = map[string]bool{
	"key": true, "api_key": true, "apikey": true, "api-key": true, "token": true, "access_token": true,
	"auth": true, "signature": true, "sig": true, "x-amz-signature": true, "x-goog-signature": true,
	"code": true, "secret": true, "password": true, "client_secret": true,
}

// reSecretish matches key-looking tokens in a body or a URL (sk-…, AIza…,
// long hex/base64 runs after "key"/"token"): they are masked even when the
// caller did not name them.
var reSecretish = regexp.MustCompile(`\b(sk-ant-[A-Za-z0-9_\-]{8,}|sk-(?:proj-|or-v1-)?[A-Za-z0-9_\-]{16,}|sk_(?:car_)?[A-Za-z0-9]{16,}|AIza[0-9A-Za-z_\-]{30,}|gsk_[A-Za-z0-9]{16,}|xai-[A-Za-z0-9]{16,}|r8_[A-Za-z0-9]{16,}|hf_[A-Za-z0-9]{16,})`)

// Mask shortens a secret for display: "sk-…a1b2".
func Mask(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return ""
	}
	r := []rune(k)
	if len(r) <= 8 {
		return "•••"
	}
	head := 3
	if i := strings.IndexAny(k, "-_."); i > 0 && i < 8 {
		head = i + 1
	}
	return string(r[:min(head, len(r)-4)]) + "…" + string(r[len(r)-4:])
}

// Redact replaces every secret (6+ characters) and anything that looks like a
// known key in s.
func Redact(s string, secrets ...string) string {
	for _, k := range secrets {
		if k = strings.TrimSpace(k); len(k) >= 6 {
			s = strings.ReplaceAll(s, k, Mask(k))
		}
	}
	return reSecretish.ReplaceAllStringFunc(s, Mask)
}

// URL is u for display: no user info, credential query parameters masked,
// other secrets redacted.
func URL(raw string, secrets ...string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return Redact(raw, secrets...)
	}
	u.User = nil
	if u.RawQuery != "" {
		q := u.Query()
		for k, vs := range q {
			if secretParams[strings.ToLower(k)] {
				for i := range vs {
					vs[i] = "•••"
				}
			}
		}
		u.RawQuery = q.Encode()
	}
	s, _ := url.PathUnescape(u.String())
	if s == "" {
		s = u.String()
	}
	return Redact(s, secrets...)
}

var (
	reTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reTags  = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace = regexp.MustCompile(`\s+`)
)

// Body is a provider's reply for display: HTML pages become their title (or
// «HTML-сторінка»), whitespace is collapsed, the text clipped to n runes and
// secrets redacted. "" when the body is empty.
func Body(b []byte, n int, secrets ...string) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	if !utf8.ValidString(s) {
		return fmt.Sprintf("(двійкові дані, %d Б)", len(b))
	}
	low := strings.ToLower(s[:min(len(s), 200)])
	if strings.HasPrefix(low, "<!doctype html") || strings.HasPrefix(low, "<html") || strings.Contains(low, "<head") {
		t := ""
		if m := reTitle.FindStringSubmatch(s); m != nil {
			t = strings.TrimSpace(html.UnescapeString(reTags.ReplaceAllString(m[1], "")))
		}
		if t == "" {
			return "HTML-сторінка (не API)"
		}
		return "HTML-сторінка «" + Clip(reSpace.ReplaceAllString(t, " "), 120) + "» (не API)"
	}
	s = reSpace.ReplaceAllString(s, " ")
	if n <= 0 {
		n = 300
	}
	return Redact(Clip(s, n), secrets...)
}

// Clip cuts s to n runes with an ellipsis.
func Clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// Status is "404 Not Found" (the code alone for unknown codes).
func Status(code int) string {
	if t := http.StatusText(code); t != "" {
		return fmt.Sprintf("%d %s", code, t)
	}
	return fmt.Sprint(code)
}

// Line is "POST https://host/path → 404 Not Found" (status 0 = no answer).
func Line(method, rawURL string, status int, secrets ...string) string {
	if method == "" {
		method = http.MethodGet
	}
	s := strings.ToUpper(method) + " " + URL(rawURL, secrets...)
	if status > 0 {
		s += " → " + Status(status)
	}
	return s
}

// Hint is a short Ukrainian hint for an HTTP status ("" = none).
func Hint(status int, body string) string {
	low := strings.ToLower(body)
	switch {
	case status == http.StatusNotFound && (strings.Contains(low, "model") || strings.Contains(low, "deployment")):
		return "сервер є, але такої моделі (або розгортання) немає — перевір назву моделі"
	case status == http.StatusNotFound:
		return "за цією адресою немає такого методу — перевір адресу (часто бракує /v1 чи /api/v1)"
	case status == http.StatusMethodNotAllowed:
		return "метод не підтримується за цією адресою — схоже, адреса веде не на API"
	case status == http.StatusUnauthorized:
		return "ключ не прийнято — перевір ключ і спосіб авторизації (Bearer, x-api-key, api-key)"
	case status == http.StatusForbidden:
		return "доступ заборонено — ключ не має прав на цю модель чи регіон"
	case status == http.StatusPaymentRequired:
		return "на рахунку бракує коштів — поповни баланс у кабінеті сервісу"
	case status == http.StatusTooManyRequests:
		return "забагато запитів або вичерпано ліміт — зачекай або перевір тариф"
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return "сервер відхилив запит — перевір модель і параметри"
	case status >= 500:
		return "сервер тимчасово не працює — спробуй пізніше"
	}
	return ""
}

// Detail is the block a check shows under its message:
//
//	Запит: POST https://host/v1/chat/completions → 404 Not Found
//	Відповідь: {"error":"not_found"}
//	Підказка: …
//
// hint "" = the status's own hint; "-" = none.
func Detail(method, rawURL string, status int, body []byte, hint string, secrets ...string) string {
	lines := []string{"Запит: " + Line(method, rawURL, status, secrets...)}
	b := Body(body, 300, secrets...)
	if b != "" {
		lines = append(lines, "Відповідь: "+b)
	}
	if hint == "" {
		hint = Hint(status, b)
	}
	if hint != "" && hint != "-" {
		lines = append(lines, "Підказка: "+hint)
	}
	return strings.Join(lines, "\n")
}

// ---- trace ----------------------------------------------------------------------------

// Call is one recorded HTTP call.
type Call struct {
	Method, URL string
	Status      int    // 0 = no answer (network error)
	Body        []byte // the reply (clipped to 4 KB)
	Err         string // transport error
}

// Trace collects the calls of one check (safe for concurrent use).
type Trace struct {
	mu    sync.Mutex
	calls []Call
}

type traceKey struct{}

// WithTrace returns ctx carrying a fresh Trace.
func WithTrace(ctx context.Context) (context.Context, *Trace) {
	t := &Trace{}
	return context.WithValue(ctx, traceKey{}, t), t
}

// Record adds a call to the context's Trace (no-op without one).
func Record(ctx context.Context, method, rawURL string, status int, body []byte, err error) {
	if ctx == nil {
		return
	}
	t, _ := ctx.Value(traceKey{}).(*Trace)
	if t == nil {
		return
	}
	c := Call{Method: method, URL: rawURL, Status: status}
	if len(body) > 4<<10 {
		body = body[:4<<10]
	}
	c.Body = append([]byte(nil), body...)
	if err != nil {
		c.Err = err.Error()
	}
	t.mu.Lock()
	t.calls = append(t.calls, c)
	t.mu.Unlock()
}

// Calls is a copy of the recorded calls.
func (t *Trace) Calls() []Call {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Call(nil), t.calls...)
}

// Last is the last call that failed (non-2xx or no answer), else the last
// call; ok false when nothing was recorded.
func (t *Trace) Last() (Call, bool) {
	calls := t.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if c := calls[i]; c.Status == 0 || c.Status >= 300 {
			return c, true
		}
	}
	if len(calls) == 0 {
		return Call{}, false
	}
	return calls[len(calls)-1], true
}

// Detail describes the last failed call (Detail), "" when none failed.
func (t *Trace) Detail(secrets ...string) string {
	c, ok := t.Last()
	if !ok || (c.Status >= 200 && c.Status < 300) {
		return ""
	}
	if c.Status == 0 {
		line := "Запит: " + Line(c.Method, c.URL, 0, secrets...) + " → немає відповіді"
		if c.Err != "" {
			line += " (" + Redact(Clip(c.Err, 160), secrets...) + ")"
		}
		return line
	}
	return Detail(c.Method, c.URL, c.Status, c.Body, "", secrets...)
}

// Append adds the trace's detail to msg unless msg already names the request.
func (t *Trace) Append(msg string, secrets ...string) string {
	d := t.Detail(secrets...)
	if d == "" || strings.Contains(msg, "Запит: ") {
		return msg
	}
	return strings.TrimRight(msg, " \n") + "\n" + d
}
