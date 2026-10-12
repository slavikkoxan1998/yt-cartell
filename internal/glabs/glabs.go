// Package glabs is the client for the G-Labs Studio local Webhook API (Google
// Flow: Veo 3.1 video and Nano Banana images, billed against the Google
// account's subscription credits).
//
//	POST {base}/api/image/generate   → 202 { task_id }
//	POST {base}/api/video/generate   → 202 { task_id }
//	GET  {base}/api/status/{task_id} → { status: pending|running|completed|failed, results[] }
//	GET  {base}/api/files/{name}     → file bytes (no key needed)
//	GET  {base}/api/health           → server status (no key needed)
//	POST {base}/api/stop/{task_id}   → stop ONE task this client gave up on
//	                                   (never without the id: that stops all)
//
// Auth: X-API-Key header. The server must be started inside G-Labs Studio
// (Webhook API → Start Server); default port 8765.
package glabs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
	"ytcartell/internal/httpdiag"
)

// Error is a G-Labs failure with a user-facing (Ukrainian) message.
type Error struct {
	Msg  string
	Code int // HTTP status or task error_code; 0 when unknown
	// Status is the failed task's `error` as G-Labs sent it
	// ("INVALID_ARGUMENT (flag=[3])", "REPUTATIONAL"); "" for other failures.
	Status string
	// Reject is set when Google refused the request for its content
	// (RejectInvalid, RejectSafety): the same request fails again, so it is
	// never resubmitted unchanged. Msg then starts with RejectedPrefix.
	Reject string
	// Retry: a transient failure worth trying again later — quota/rate limit
	// (429), G-Labs/Google server trouble (5xx, UPLOAD_ERROR), timeouts, the
	// server not answering.
	Retry bool
}

func (e *Error) Error() string { return e.Msg }

func errf(code int, format string, a ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, a...), Code: code}
}

// Content rejections (Error.Reject).
const (
	// RejectInvalid: Google refused the request itself — error_code 400 /
	// INVALID_ARGUMENT, usually a policy block it does not name ("flag=[3]"
	// is just gRPC code 3 = INVALID_ARGUMENT).
	RejectInvalid = "invalid"
	// RejectSafety: a Google safety filter refused the prompt, a reference
	// or the result (REPUTATIONAL, PROMINENT_PERSON, DANGEROUS …).
	RejectSafety = "safety"
)

// RejectedPrefix starts the message of every content rejection, so a stored
// message (an asset's error) is recognised by Rejected.
const RejectedPrefix = "G-Labs відхилив запит: змінити промпт або референси"

// Rejected reports whether msg is (or wraps) the message of a content
// rejection — the same request would be refused again.
func Rejected(msg string) bool { return strings.Contains(msg, RejectedPrefix) }

// Rejection is err's content-rejection kind (RejectInvalid, RejectSafety) or
// "" when err is not a G-Labs content rejection.
func Rejection(err error) string {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Reject
	}
	return ""
}

// Tunables (unexported vars so tests can shrink them).
var (
	pollInterval = 5 * time.Second
	// G-Labs' own watchdog fails a task stuck in "running" after 15 min.
	pollTimeout = 20 * time.Minute
	// Consecutive status failures tolerated while G-Labs is busy.
	maxBlips = 5

	submitTimeout   = 2 * time.Minute
	statusTimeout   = 30 * time.Second
	downloadTimeout = 10 * time.Minute
	healthTimeout   = 10 * time.Second

	// A task given up on (cancel, timeout) is stopped with POST /api/stop/{id}
	// under a fresh context bounded by this.
	stopTimeout = 10 * time.Second

	// Expected render time — only drives the progress estimate.
	imageExpected = 40 * time.Second
	videoExpected = 150 * time.Second
)

const (
	maxReply    = 4 << 20 // JSON replies
	maxDownload = 1 << 30 // result files
	maxRefs     = 10
)

// Client talks to the configured G-Labs Studio server. Settings are read on
// every call, so changes in the Settings screen apply immediately.
type Client struct {
	get  func() config.Config
	http *http.Client
}

// New creates a client reading its settings from get.
func New(get func() config.Config) *Client {
	return &Client{get: get, http: &http.Client{}}
}

// ImageModel maps the UI image model id to the G-Labs Flow image model.
func ImageModel(uiModel string) string {
	switch uiModel {
	case "GEM_PIX":
		return "nano_banana_2_lite"
	case "GEM_PIX_2":
		return "nano_banana_pro"
	default:
		return "nano_banana_2"
	}
}

var imageRatios = map[string]bool{"1:1": true, "3:4": true, "4:3": true, "9:16": true, "16:9": true}

// Image renders one still and returns its bytes and MIME type.
func (c *Client) Image(ctx context.Context, r gen.ImageRequest) ([]byte, string, error) {
	cn, err := c.conn(true)
	if err != nil {
		return nil, "", err
	}
	aspect := r.Aspect
	if !imageRatios[aspect] {
		aspect = "16:9"
	}
	refs := make([]any, 0, len(r.Refs))
	for _, ref := range r.Refs {
		if len(refs) == maxRefs {
			break
		}
		if e, ok := refEntry(ref); ok {
			refs = append(refs, e)
		}
	}
	id, err := c.submit(ctx, cn, "image", map[string]any{
		"prompt":           r.Prompt,
		"model":            ImageModel(r.UIModel),
		"aspect_ratio":     aspect,
		"reference_images": refs,
	})
	if err != nil {
		return nil, "", err
	}
	results, err := c.waitFor(ctx, cn, id, nil, imageExpected)
	if err != nil {
		return nil, "", err
	}
	data, ctype, err := c.download(ctx, cn, results[0])
	if err != nil {
		return nil, "", err
	}
	return data, imageMIME(ctype, results[0], data), nil
}

var reMP4 = regexp.MustCompile(`(?i)\.mp4($|\?)`)

// OmniFlash is the G-Labs video model that renders 4–10 s clips and takes up
// to 7 components references (the Veo models: 4/6/8 s, 3 references).
const OmniFlash = "omni_flash"

// MaxVideoRefs is how many reference_images a video model accepts in
// components mode: Omni Flash 7, the Veo models 3.
func MaxVideoRefs(model string) int {
	if strings.TrimSpace(model) == OmniFlash {
		return 7
	}
	return 3
}

// Video renders one clip and returns the mp4 bytes. Model/resolution/length
// fall back to Settings; the length is snapped to what the model renders
// (Veo 4/6/8 s, Omni Flash 4/6/8/10 s).
//
// Modes: components (r.Mode == gen.ModeComponents with at least one usable
// ref: reference_images = r.Refs as named {data,name} objects, capped by the
// model with the caller's order as priority), else start_image (r.Start),
// else text_to_video. start_end_image is never sent (Omni Flash rejects it
// and there is no end frame in gen.VideoRequest).
func (c *Client) Video(ctx context.Context, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	if progress == nil {
		progress = gen.NoProgress
	}
	cn, err := c.conn(true)
	if err != nil {
		return nil, err
	}
	v := c.get().Video
	model := firstNonEmpty(r.Model, v.GlabsModel, "veo_31_fast")
	resolution := firstNonEmpty(r.Resolution, v.Resolution, "720p")
	sec := r.Seconds
	if sec <= 0 {
		sec = v.Length
	}
	length := config.SnapVideoLength(float64(sec), model, 8)
	aspect := "16:9"
	if r.Aspect == "9:16" {
		aspect = "9:16"
	}
	mode, refs := videoInputs(r, model)
	id, err := c.submit(ctx, cn, "video", map[string]any{
		"prompt":           r.Prompt,
		"model":            model,
		"aspect_ratio":     aspect,
		"mode":             mode,
		"reference_images": refs,
		"resolution":       []string{resolution},
		"video_length":     length,
	})
	if err != nil {
		return nil, err
	}
	progress(10, "queued")
	results, err := c.waitFor(ctx, cn, id, progress, videoExpected)
	if err != nil {
		return nil, err
	}
	pick := results[0]
	for _, u := range results {
		if reMP4.MatchString(u) {
			pick = u
			break
		}
	}
	data, _, err := c.download(ctx, cn, pick)
	return data, err
}

// Health is the connection check for the Settings screen: the server answers
// (no key needed), then the key is accepted.
func (c *Client) Health(ctx context.Context) (ok bool, msg string) {
	g := c.get().Glabs
	base := strings.TrimRight(strings.TrimSpace(g.BaseURL), "/")
	if base == "" {
		return false, errNoBase.Error()
	}
	h, err := c.do(ctx, http.MethodGet, base+"/api/health", "", nil, healthTimeout, maxReply)
	if err != nil {
		if ctx.Err() != nil {
			return false, "Перевірку скасовано"
		}
		return false, unreachable(base).Error()
	}
	if !h.ok() {
		return false, fmt.Sprintf("G-Labs: сервер відповів %d на /api/health", h.status)
	}
	var info struct {
		Uptime flexFloat `json:"uptime"`
	}
	_ = json.Unmarshal(h.body, &info)

	cn, err := c.conn(true)
	if err != nil {
		return false, err.Error()
	}
	t, err := c.call(ctx, cn, http.MethodGet, "/api/tasks", nil, true, healthTimeout)
	if err != nil {
		if ctx.Err() != nil {
			return false, "Перевірку скасовано"
		}
		return false, err.Error()
	}
	if t.status == http.StatusUnauthorized {
		return false, "Сервер працює, але API-ключ невірний"
	}
	if t.status == http.StatusForbidden {
		return false, errMaxPlan.Error()
	}
	if !t.ok() {
		return false, fmt.Sprintf("G-Labs: сервер відповів %d на /api/tasks", t.status)
	}
	msg = "G-Labs онлайн"
	if up := float64(info.Uptime); up >= 1 {
		msg += " · працює " + humanUptime(up)
	}
	return true, msg
}

// ---------------------------------------------------------------------------

var (
	errNoBase = &Error{Msg: "G-Labs: не задано адресу сервера (Налаштування → G-Labs Studio)"}
	errNoKey  = &Error{Msg: "G-Labs: не задано API-ключ (Налаштування → G-Labs Studio)"}
	// 403 {"error":"Webhook requires MAX plan"}: the server runs, the licence is not MAX.
	errMaxPlan = &Error{Msg: "Webhook потребує тарифу MAX у G-Labs", Code: http.StatusForbidden}
	// 413: the request body is over G-Labs' 50 MB cap (base64 refs ≈ 4/3 of the files).
	errTooLarge = &Error{Msg: "G-Labs: запит більший за 50 МБ — зменш кількість або розмір референсів", Code: http.StatusRequestEntityTooLarge}
)

func unreachable(base string) *Error {
	e := errf(0, "G-Labs Studio не відповідає на %s. Відкрий G-Labs Studio → Webhook API → Start Server.", base)
	e.Retry = true
	return e
}

type conn struct{ base, key string }

func (c *Client) conn(auth bool) (conn, error) {
	g := c.get().Glabs
	cn := conn{
		base: strings.TrimRight(strings.TrimSpace(g.BaseURL), "/"),
		key:  strings.TrimSpace(g.APIKey),
	}
	if cn.base == "" {
		return cn, errNoBase
	}
	if auth && cn.key == "" {
		return cn, errNoKey
	}
	return cn, nil
}

type reply struct {
	status int
	ctype  string
	body   []byte
}

func (r reply) ok() bool { return r.status >= 200 && r.status < 300 }

// do performs one request and reads the whole body under its own timeout.
// Transport errors are returned raw; callers map them to friendly messages.
func (c *Client) do(ctx context.Context, method, u, key string, body []byte, timeout time.Duration, limit int64) (reply, error) {
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(rctx, method, u, rd)
	if err != nil {
		return reply{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	res, err := c.http.Do(req)
	if err != nil {
		httpdiag.Record(ctx, method, u, 0, nil, err) // «Перевірити» shows the exact request
		return reply{}, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		httpdiag.Record(ctx, method, u, 0, nil, err)
		return reply{}, err
	}
	httpdiag.Record(ctx, method, u, res.StatusCode, b, nil)
	if int64(len(b)) > limit {
		return reply{}, fmt.Errorf("reply larger than %d bytes", limit)
	}
	return reply{status: res.StatusCode, ctype: res.Header.Get("Content-Type"), body: b}, nil
}

// call hits {base}{path}; any transport failure becomes "server not responding".
func (c *Client) call(ctx context.Context, cn conn, method, path string, body []byte, auth bool, timeout time.Duration) (reply, error) {
	key := ""
	if auth {
		key = cn.key
	}
	r, err := c.do(ctx, method, cn.base+path, key, body, timeout, maxReply)
	if err != nil {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		return r, unreachable(cn.base)
	}
	return r, nil
}

func (c *Client) submit(ctx context.Context, cn conn, kind string, body map[string]any) (string, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	res, err := c.call(ctx, cn, http.MethodPost, "/api/"+kind+"/generate", b, true, submitTimeout)
	if err != nil {
		return "", err
	}
	text := string(res.body)
	switch {
	case res.status == http.StatusUnauthorized:
		return "", errf(401, "G-Labs: невірний API-ключ (401)")
	case res.status == http.StatusForbidden:
		return "", errMaxPlan
	case res.status == http.StatusRequestEntityTooLarge:
		return "", errTooLarge
	case !res.ok():
		e := errf(res.status, "G-Labs: сервер відхилив запит (%s, %d): %s", kind, res.status, clip(text, 300))
		e.Retry = res.status == http.StatusTooManyRequests || res.status >= 500
		return "", e
	}
	var j struct {
		TaskID json.RawMessage `json:"task_id"`
	}
	if err := json.Unmarshal(res.body, &j); err != nil {
		return "", errf(0, "G-Labs: незрозуміла відповідь сервера: %s", clip(text, 200))
	}
	id := rawText(j.TaskID)
	if id == "" {
		return "", errf(0, "G-Labs: сервер не повернув task_id: %s", clip(text, 200))
	}
	return id, nil
}

type taskState struct {
	Status      string            `json:"status"`
	Results     []json.RawMessage `json:"results"`
	ErrorCode   flexFloat         `json:"error_code"`
	Error       json.RawMessage   `json:"error"`
	ErrorDetail json.RawMessage   `json:"error_detail"`
}

// describeFailure turns a failed task into an Error classified by what a new
// attempt can achieve (see classify).
func describeFailure(t taskState) *Error {
	code := int(t.ErrorCode)
	status := rawText(t.Error)
	msg := firstNonEmpty(status, rawText(t.ErrorDetail), "невідома помилка")
	reject, retry := classify(code, firstNonEmpty(status, rawText(t.ErrorDetail)))
	var e *Error
	switch {
	case reject == RejectSafety:
		e = errf(code, "%s — фільтр безпеки Google (%s)", RejectedPrefix, msg)
	case reject == RejectInvalid && code != 0:
		e = errf(code, "%s · G-Labs [%d]: %s", RejectedPrefix, code, msg)
	case reject == RejectInvalid:
		e = errf(code, "%s · G-Labs: %s", RejectedPrefix, msg)
	case code == 0:
		e = errf(0, "G-Labs: %s", msg)
	default:
		hint := ""
		switch {
		case code == 429:
			hint = " — вичерпано ліміт/кредити акаунта, спробуй пізніше або додай акаунт у G-Labs"
		case code == 403:
			hint = " — проблема з сесією акаунта Google у G-Labs (онови сесію)"
		case code >= 500:
			hint = " — тимчасовий збій G-Labs/Google, спробуй ще раз"
		}
		e = errf(code, "G-Labs [%d]: %s%s", code, msg, hint)
	}
	e.Status, e.Reject, e.Retry = status, reject, retry
	return e
}

var (
	// "400: INVALID_ARGUMENT …" — older G-Labs builds put the code in front.
	reCodePrefix = regexp.MustCompile(`^\d{3}:\s*`)
	// The leading status token of `error` ("REPUTATIONAL", "INVALID_ARGUMENT").
	reStatusToken = regexp.MustCompile(`^[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)*`)
)

// Words of Google's safety-filter categories as G-Labs reports them in
// `error` (error_code 0): REPUTATIONAL, PROMINENT_PERSON, DANGEROUS, …
var safetyWords = []string{
	"REPUTATION", "PROMINENT", "CELEBRIT", "PERSON", "PEOPLE", "DANGEROUS", "UNSAFE", "SAFETY", "POLICY",
	"SEXUAL", "NSFW", "NUDITY", "VIOLEN", "GORE", "HATE", "HARASS", "SELF_HARM", "MINOR", "CHILD", "CSAM",
	"PROHIBITED", "BLOCK", "RESPONSIBLE", "FILTER", "WEAPON", "DRUG", "TERROR", "COPYRIGHT", "RECITATION", "SPII",
}

// classify reads a failed task the way the G-Labs guide advises — the
// error_code, then how `error` STARTS (never the whole text):
//
//	429 / RESOURCE_EXHAUSTED            retry later (quota, rate limit)
//	a Google safety category            RejectSafety (REPUTATIONAL, PROMINENT_PERSON …)
//	400 / INVALID_ARGUMENT              RejectInvalid (Google refused the request)
//	5xx, UPLOAD_ERROR, INTERNAL,        retry later (server trouble, timeouts)
//	UNAVAILABLE, timeouts
//
// Anything else (403 session, "No active accounts available" …) is neither.
func classify(code int, errText string) (reject string, retry bool) {
	s := strings.ToUpper(strings.TrimSpace(errText))
	s = reCodePrefix.ReplaceAllString(s, "")
	token := reStatusToken.FindString(s)
	switch {
	case code == http.StatusTooManyRequests || strings.HasPrefix(s, "RESOURCE_EXHAUSTED") || strings.HasPrefix(s, "RATE_LIMIT"):
		return "", true
	case code == 499 || strings.HasPrefix(s, "STOPPED_BY_CLIENT"):
		return "", false
	case strings.Contains(s, "UPLOAD_ERROR") || code >= 500 ||
		strings.HasPrefix(s, "INTERNAL") || strings.HasPrefix(s, "UNAVAILABLE") || strings.HasPrefix(s, "HTTP_5") ||
		strings.HasPrefix(s, "DEADLINE_EXCEEDED") || strings.Contains(s, "TIMEOUT") || strings.Contains(s, "TIMED OUT"):
		return "", true
	case token != "" && token != "INVALID_ARGUMENT" && slices.ContainsFunc(safetyWords, func(w string) bool { return strings.Contains(token, w) }):
		return RejectSafety, false
	case code == http.StatusBadRequest || strings.HasPrefix(s, "INVALID_ARGUMENT"):
		return RejectInvalid, false
	}
	return "", false
}

// waitFor polls the task until it completes and returns its result URLs. A
// task given up on — cancelled, timed out, the status lost in errors — is
// stopped in G-Labs (stop), so it does not render on for nobody.
func (c *Client) waitFor(ctx context.Context, cn conn, id string, progress gen.Progress, expected time.Duration) (results []string, err error) {
	if progress == nil {
		progress = gen.NoProgress
	}
	finished := false // G-Labs reported the task's end (or does not know it)
	defer func() {
		if err != nil && !finished {
			c.stop(cn, id)
		}
	}()
	path := "/api/status/" + url.PathEscape(id)
	start := time.Now()
	blips := 0
	for time.Since(start) < pollTimeout {
		if err := sleep(ctx, pollInterval); err != nil {
			return nil, err
		}
		t, err := c.status(ctx, cn, path)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var ge *Error
			if errors.As(err, &ge) && (ge.Code == 404 || ge.Code == 401) {
				finished = true // lost task / wrong key: nothing to stop
				return nil, err
			}
			if errors.As(err, &ge) && ge.Code == http.StatusForbidden {
				return nil, err
			}
			// tolerate a few blips while G-Labs is busy
			if blips++; blips > maxBlips {
				return nil, err
			}
			continue
		}
		blips = 0

		switch t.Status {
		case "completed":
			finished = true
			for _, r := range t.Results {
				if u := resultURL(r); u != "" {
					results = append(results, u)
				}
			}
			if len(results) == 0 {
				return nil, errf(0, "G-Labs: задача завершилась без файлів")
			}
			progress(95, "rendered")
			return results, nil
		case "failed":
			finished = true
			return nil, describeFailure(t)
		case "pending":
			progress(10, "queued")
		default:
			elapsed := float64(time.Since(start)) / float64(expected)
			progress(20+int(math.Min(70, math.Round(elapsed*70))), "rendering")
		}
	}
	e := errf(0, "G-Labs: задача не завершилась за %s — спробуй ще раз", humanDuration(pollTimeout))
	e.Retry = true
	return nil, e
}

// stop asks G-Labs to drop a task this client gave up on: best effort, under
// a fresh context (the caller's may be cancelled) bounded by stopTimeout.
// Never without an id — POST /api/stop alone stops EVERY task.
func (c *Client) stop(cn conn, id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"task_id": id}) // the same id; some servers refuse an empty POST body
	_, _ = c.do(ctx, http.MethodPost, cn.base+"/api/stop/"+url.PathEscape(id), cn.key, body, stopTimeout, maxReply)
}

func (c *Client) status(ctx context.Context, cn conn, path string) (taskState, error) {
	var t taskState
	res, err := c.call(ctx, cn, http.MethodGet, path, nil, true, statusTimeout)
	if err != nil {
		return t, err
	}
	switch {
	case res.status == http.StatusNotFound:
		return t, errf(404, "G-Labs: задачу втрачено (програму перезапускали?)")
	case res.status == http.StatusUnauthorized:
		return t, errf(401, "G-Labs: невірний API-ключ (401)")
	case res.status == http.StatusForbidden:
		return t, errMaxPlan
	case !res.ok():
		e := errf(res.status, "G-Labs: сервер відповів %d на запит статусу", res.status)
		e.Retry = res.status >= 500
		return t, e
	}
	if err := json.Unmarshal(res.body, &t); err != nil {
		return t, errf(0, "G-Labs: незрозуміла відповідь статусу: %s", clip(string(res.body), 200))
	}
	return t, nil
}

var reAbs = regexp.MustCompile(`(?i)^https?://`)

// fileURL is where a result is fetched: a path relative to the server joins
// the configured address, and so does an absolute /api/files/ URL — G-Labs
// builds it from its bind address (127.0.0.1, a LAN IP), which a client
// reaching it through a tunnel, a proxy or another machine cannot use.
func fileURL(base, u string) string {
	if !reAbs.MatchString(u) {
		if !strings.HasPrefix(u, "/") {
			return base + "/" + u
		}
		return base + u
	}
	if pu, err := url.Parse(u); err == nil && strings.HasPrefix(pu.EscapedPath(), "/api/files/") {
		out := base + pu.EscapedPath()
		if pu.RawQuery != "" {
			out += "?" + pu.RawQuery
		}
		return out
	}
	return u
}

// download fetches a result file (see fileURL).
func (c *Client) download(ctx context.Context, cn conn, u string) ([]byte, string, error) {
	abs := fileURL(cn.base, u)
	// Files need no key; send it only back to the G-Labs server itself.
	key := ""
	if sameOrigin(abs, cn.base) {
		key = cn.key
	}
	res, err := c.do(ctx, http.MethodGet, abs, key, nil, downloadTimeout, maxDownload)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", errf(0, "G-Labs: не вдалося завантажити файл %s (%s)", abs, shortErr(err))
	}
	if !res.ok() {
		return nil, "", errf(res.status, "G-Labs: не вдалося завантажити файл %s (%d)", abs, res.status)
	}
	if len(res.body) == 0 {
		return nil, "", errf(0, "G-Labs: порожній файл %s", abs)
	}
	ctype := res.ctype
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	return res.body, ctype, nil
}

// ---------------------------------------------------------------------------
// helpers

// videoInputs picks the video mode and its reference_images (see Video).
func videoInputs(r gen.VideoRequest, model string) (string, []any) {
	if r.Mode == gen.ModeComponents {
		limit := MaxVideoRefs(model)
		refs := make([]any, 0, min(limit, len(r.Refs)))
		for _, ref := range r.Refs {
			if len(refs) == limit {
				break
			}
			if e, ok := refEntry(ref); ok {
				refs = append(refs, e)
			}
		}
		if len(refs) > 0 {
			return "components", refs
		}
	}
	if r.Start != nil {
		if e, ok := refEntry(*r.Start); ok {
			return "start_image", []any{e}
		}
	}
	return "text_to_video", []any{}
}

// refEntry is a reference_images entry: a data URI, or {data, name} when the
// image is bound to "@name" in the prompt.
func refEntry(r gen.RefImage) (any, bool) {
	if len(r.Data) == 0 {
		return nil, false
	}
	uri := dataURI(r)
	if name := strings.TrimSpace(r.Name); name != "" {
		return map[string]string{"data": uri, "name": name}, true
	}
	return uri, true
}

func dataURI(r gen.RefImage) string {
	if bytes.HasPrefix(r.Data, []byte("data:")) {
		return string(r.Data)
	}
	m := strings.TrimSpace(r.MIME)
	if m == "" {
		m = http.DetectContentType(r.Data)
		if !strings.HasPrefix(m, "image/") {
			m = "image/png"
		}
	}
	return "data:" + m + ";base64," + base64.StdEncoding.EncodeToString(r.Data)
}

var (
	reJPEG = regexp.MustCompile(`(?i)\.jpe?g($|\?)`)
	reWEBP = regexp.MustCompile(`(?i)\.webp($|\?)`)
)

// imageMIME: Content-Type when it names an image, else sniffed bytes, else extension.
func imageMIME(ctype, u string, data []byte) string {
	if mt, _, err := mime.ParseMediaType(ctype); err == nil && strings.HasPrefix(mt, "image/") {
		return mt
	}
	if s := http.DetectContentType(data); strings.HasPrefix(s, "image/") {
		return s
	}
	switch {
	case reJPEG.MatchString(u):
		return "image/jpeg"
	case reWEBP.MatchString(u):
		return "image/webp"
	}
	return "image/png"
}

// resultURL accepts a plain string or an object carrying the file location.
func resultURL(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var o map[string]any
	if json.Unmarshal(raw, &o) == nil {
		for _, k := range []string{"url", "file_url", "path", "file"} {
			if v, ok := o[k].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

// rawText renders a JSON scalar (string or number) as text; objects as compact JSON.
func rawText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	return string(raw)
}

// flexFloat decodes a number or a numeric string; anything else is 0.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	var n float64
	if json.Unmarshal(b, &n) == nil {
		*f = flexFloat(n)
		return nil
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			*f = flexFloat(n)
		}
	}
	return nil
}

func sameOrigin(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	return err1 == nil && err2 == nil && strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Host, ub.Host)
}

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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func shortErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "тайм-аут"
	}
	return clip(err.Error(), 160)
}

func humanDuration(d time.Duration) string {
	if d >= time.Minute {
		return fmt.Sprintf("%d хв", int(math.Round(d.Minutes())))
	}
	return fmt.Sprintf("%d с", int(math.Ceil(d.Seconds())))
}

func humanUptime(sec float64) string {
	s := int(math.Round(sec))
	switch {
	case s < 60:
		return fmt.Sprintf("%d с", s)
	case s < 3600:
		return fmt.Sprintf("%d хв", s/60)
	default:
		return fmt.Sprintf("%d год %d хв", s/3600, s%3600/60)
	}
}
