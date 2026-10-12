package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ytcartell/internal/gen"
	"ytcartell/internal/httpdiag"
)

// Tunables (package vars so tests can shrink them).
var (
	pollInterval     = 5 * time.Second  // Runway asks for ≥ 5 s between polls
	imagePollTimeout = 8 * time.Minute  // one image render
	videoPollTimeout = 25 * time.Minute // one clip render (queues can be long)
	maxBlips         = 5                // consecutive failed status polls tolerated

	submitTimeout   = 3 * time.Minute // one create call (Replicate "Prefer: wait" holds up to 60 s)
	statusTimeout   = 30 * time.Second
	downloadTimeout = 10 * time.Minute
	testTimeout     = 20 * time.Second
	cancelTimeout   = 10 * time.Second

	// Retries of a request answered 429/5xx (Retry-After wins when present).
	maxRetries    = 3
	backoffBase   = 2 * time.Second
	backoffMax    = 30 * time.Second
	retryAfterMax = 60 * time.Second

	// Expected render time — only drives the progress estimate.
	imageExpected = 45 * time.Second
	videoExpected = 3 * time.Minute
)

const (
	maxReply      = 96 << 20  // JSON replies (OpenAI returns base64 images inline)
	maxImageBytes = 64 << 20  // a downloaded image
	maxVideoBytes = 768 << 20 // a downloaded clip
	maxErrBody    = 256 << 10 // error replies
)

// Error is a failure of the own API with a user-facing (Ukrainian) message.
// Messages never contain the key.
type Error struct {
	Msg    string
	Status int  // HTTP status (or a provider code); 0 when unknown
	Retry  bool // transient: worth trying again later (rate limit, server trouble, network)
	// timeout: the request was sent but no answer came in time — the server
	// may have started (and billed) the work, so a create call is not repeated.
	timeout bool
}

func (e *Error) Error() string { return e.Msg }

// endpoint is one configured API: where, which key, which model.
type endpoint struct {
	name  string // display name ("fal.ai", "Replicate", …)
	base  string // API base URL, no trailing slash
	key   string
	model string
	hc    *http.Client
	// auth sets the authentication (and any fixed) headers; nil = none.
	auth func(h http.Header, key string)
	// every is the status poll interval (0 = pollInterval).
	every time.Duration
}

// errf builds an *Error whose message is redacted of the key.
func (ep *endpoint) errf(status int, retry bool, format string, a ...any) *Error {
	return &Error{Msg: redact(fmt.Sprintf(format, a...), ep.key), Status: status, Retry: retry}
}

// request is one HTTP call.
type request struct {
	method  string
	url     string // absolute, or a path relative to base ("/v1/…")
	body    []byte
	ctype   string // body Content-Type ("" + body → application/json)
	timeout time.Duration
	limit   int64 // reply size cap (0 = maxReply)
	noAuth  bool
	// create: the call starts a (paid) generation, so it is retried only on
	// answers that guarantee nothing was started (429, 503) — never on other
	// 5xx or a lost connection.
	create bool
	header map[string]string
}

type reply struct {
	status int
	header http.Header
	body   []byte
}

func (r reply) ok() bool { return r.status >= 200 && r.status < 300 }

func (ep *endpoint) abs(u string) string {
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	if !strings.HasPrefix(u, "/") {
		u = "/" + u
	}
	return ep.base + u
}

// send performs one request (no retries). Transport failures become *Error
// (Retry) — or ctx.Err() when the caller cancelled.
func (ep *endpoint) send(ctx context.Context, rq request) (reply, error) {
	timeout := rq.timeout
	if timeout <= 0 {
		timeout = statusTimeout
	}
	limit := rq.limit
	if limit <= 0 {
		limit = maxReply
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if rq.body != nil {
		rd = bytes.NewReader(rq.body)
	}
	u := ep.abs(rq.url)
	req, err := http.NewRequestWithContext(rctx, rq.method, u, rd)
	if err != nil {
		return reply{}, ep.errf(0, false, "%s: некоректна адреса API (%s)", ep.name, clip(err.Error(), 160))
	}
	if rq.body != nil {
		ct := rq.ctype
		if ct == "" {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("Accept", "application/json")
	if !rq.noAuth && ep.auth != nil && ep.key != "" {
		ep.auth(req.Header, ep.key)
	}
	for k, v := range rq.header {
		req.Header.Set(k, v)
	}
	res, err := ep.hc.Do(req)
	if err != nil {
		httpdiag.Record(ctx, rq.method, u, 0, nil, unwrapURLErr(err)) // «Перевірити» shows the exact request
		return reply{}, ep.netErr(ctx, req.URL, err)
	}
	defer res.Body.Close()
	if !(res.StatusCode >= 200 && res.StatusCode < 300) {
		limit = min(limit, maxErrBody)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		httpdiag.Record(ctx, rq.method, u, 0, nil, unwrapURLErr(err))
		return reply{}, ep.netErr(ctx, req.URL, err)
	}
	httpdiag.Record(ctx, rq.method, u, res.StatusCode, b, nil)
	if int64(len(b)) > limit {
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			return reply{}, ep.errf(0, false, "%s: відповідь завелика (понад %d МБ)", ep.name, limit>>20)
		}
		b = b[:limit]
	}
	return reply{status: res.StatusCode, header: res.Header, body: b}, nil
}

// unwrapURLErr drops the "Get \"url\": " wrapper of a transport error.
func unwrapURLErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func (ep *endpoint) netErr(ctx context.Context, u *url.URL, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	host := ""
	if u != nil {
		host = u.Host
	}
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		e := ep.errf(0, true, "%s: сервер %s не відповів вчасно — спробуй ще раз", ep.name, host)
		e.timeout = true
		return e
	}
	return ep.errf(0, true, "%s: немає зв'язку з %s — перевір інтернет і адресу API (%s)", ep.name, host, shortErr(err))
}

func isTimeout(err error) bool {
	var te interface{ Timeout() bool }
	return errors.As(err, &te) && te.Timeout()
}

// call is send with retries (429 and server trouble, see request.create) and
// non-2xx replies mapped to friendly *Error.
func (ep *endpoint) call(ctx context.Context, rq request) (reply, error) {
	for attempt := 0; ; attempt++ {
		res, err := ep.send(ctx, rq)
		var wait time.Duration
		switch {
		case err != nil:
			var pe *Error
			if !errors.As(err, &pe) || !pe.Retry || rq.create || attempt >= maxRetries {
				return res, err
			}
			wait = backoff(attempt, nil)
		case res.ok():
			return res, nil
		default:
			fail := ep.httpError(res)
			if !retryable(res.status, rq.create) || !fail.Retry || attempt >= maxRetries {
				return res, fail
			}
			wait = backoff(attempt, res.header)
		}
		log.Printf("[providers] %s %s: retry %d in %s", ep.name, rq.method, attempt+1, wait.Round(time.Millisecond))
		if err := sleep(ctx, wait); err != nil {
			return reply{}, err
		}
	}
}

// retryable: worth repeating the same request. A create call only after
// answers that guarantee nothing was started.
func retryable(status int, create bool) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return true
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout, 520, 522, 524, 529:
		return !create
	}
	return false
}

// backoff is the wait before retry attempt+1: Retry-After when the server
// sent one (capped), else exponential with jitter-free doubling.
func backoff(attempt int, h http.Header) time.Duration {
	if h != nil {
		if d, ok := retryAfter(h); ok {
			return min(d, retryAfterMax)
		}
	}
	d := backoffBase << attempt
	if d <= 0 || d > backoffMax {
		d = backoffMax
	}
	return d
}

func retryAfter(h http.Header) (time.Duration, bool) {
	for _, k := range []string{"Retry-After", "X-RateLimit-Reset-After"} {
		v := strings.TrimSpace(h.Get(k))
		if v == "" {
			continue
		}
		if n, err := strconv.ParseFloat(v, 64); err == nil && n >= 0 {
			return time.Duration(n * float64(time.Second)), true
		}
		if t, err := http.ParseTime(v); err == nil {
			return max(0, time.Until(t)), true
		}
	}
	return 0, false
}

// ---- error mapping ------------------------------------------------------------------

var (
	reQuota  = regexp.MustCompile(`(?i)insufficient[_ ]quota|insufficient[_ ](credit|balance|funds)|exhausted[_ ]balance|balance[_ ]exhausted|out of credits|not enough credits|billing|payment required|credit[_ ]balance|quota[_ ]exceeded|exceeded your current quota|spend[_ ]limit|usage[_ ]limit[_ ]exceeded|budget[_ ]exhausted|top.?up|balance`)
	reVerify = regexp.MustCompile(`(?i)must be verified|verify (your )?organi[sz]ation`)
	rePolicy = regexp.MustCompile(`(?i)safety|moderat|content[_ ]policy|policy violation|nsfw|sensitive|flagged|inappropriate|prohibited|\bE005\b|blocked by|not allowed content`)
)

// httpError maps a non-2xx reply to a friendly error.
func (ep *endpoint) httpError(res reply) *Error {
	detail := errorDetail(res.body)
	low := strings.ToLower(detail + " " + string(res.body))
	quota := reQuota.MatchString(low)
	s := res.status
	switch {
	case s == http.StatusUnauthorized:
		return ep.errf(s, false, "%s: ключ не прийнято (401) — перевір API-ключ у Налаштуваннях → Власний API", ep.name)
	case s == http.StatusPaymentRequired || (quota && (s == 403 || s == 429 || s == 400)):
		return ep.errf(s, false, "%s: на рахунку закінчились кошти або квота (%d) — поповни баланс у кабінеті сервісу%s", ep.name, s, suffix(detail))
	case s == http.StatusTooManyRequests:
		return ep.errf(s, true, "%s: забагато запитів (429) — зачекай хвилинку й спробуй ще раз", ep.name)
	case rePolicy.MatchString(low) && s >= 400 && s < 500:
		return ep.errf(s, false, "%s: запит заблоковано фільтром безпеки — зміни промт або референси%s", ep.name, suffix(detail))
	case s == http.StatusForbidden && reVerify.MatchString(low):
		return ep.errf(s, false, "%s: модель доступна лише верифікованим організаціям — пройди верифікацію в кабінеті сервісу або обери іншу модель%s", ep.name, suffix(detail))
	case s == http.StatusForbidden:
		return ep.errf(s, false, "%s: доступ заборонено (403) — ключ не має доступу до цієї моделі%s", ep.name, suffix(detail))
	case s == http.StatusNotFound:
		return ep.errf(s, false, "%s: не знайдено (404) — перевір назву моделі «%s» і адресу API%s", ep.name, ep.model, suffix(detail))
	case s == http.StatusRequestEntityTooLarge:
		return ep.errf(s, false, "%s: запит завеликий (413) — зменш кількість або розмір референсів", ep.name)
	case s == http.StatusBadRequest || s == http.StatusUnprocessableEntity:
		return ep.errf(s, false, "%s: сервіс відхилив запит (%d)%s", ep.name, s, suffix(detail))
	case s >= 500:
		return ep.errf(s, true, "%s: сервер тимчасово недоступний (%d) — спробуй пізніше%s", ep.name, s, suffix(detail))
	}
	return ep.errf(s, false, "%s: помилка %d%s", ep.name, s, suffix(detail))
}

// failed maps a task that finished with an error (reported in the status
// body, not by HTTP) to a friendly error.
func (ep *endpoint) failed(detail string) *Error {
	detail = strings.TrimSpace(detail)
	switch {
	case rePolicy.MatchString(detail):
		return ep.errf(0, false, "%s: запит заблоковано фільтром безпеки — зміни промт або референси%s", ep.name, suffix(detail))
	case reQuota.MatchString(detail):
		return ep.errf(0, false, "%s: на рахунку закінчились кошти або квота — поповни баланс у кабінеті сервісу%s", ep.name, suffix(detail))
	case detail == "":
		return ep.errf(0, false, "%s: генерація не вдалася (сервіс не пояснив причину)", ep.name)
	}
	return ep.errf(0, false, "%s: генерація не вдалася: %s", ep.name, clip(detail, 300))
}

func suffix(detail string) string {
	if detail = strings.TrimSpace(detail); detail == "" {
		return ""
	}
	return ": " + clip(detail, 300)
}

// errorDetail pulls a human message out of the usual error bodies:
// {"error":{"message"}}, {"error":"…"}, {"detail":"…"|[{"msg","loc"}]},
// {"title","detail"}, {"message"}, {"failure"}, {"base_resp":{"status_msg"}}.
func errorDetail(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return ""
	}
	var j map[string]json.RawMessage
	if json.Unmarshal(body, &j) != nil {
		if bytes.HasPrefix(body, []byte("<")) { // an HTML error page
			return ""
		}
		return clip(string(body), 300)
	}
	if raw, ok := j["error"]; ok {
		var o struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
			Type    string `json:"type"`
		}
		if json.Unmarshal(raw, &o) == nil && o.Message != "" {
			return o.Message
		}
		if s := rawText(raw); s != "" && s != "{}" {
			return s
		}
	}
	if raw, ok := j["detail"]; ok {
		var list []struct {
			Msg  string `json:"msg"`
			Loc  []any  `json:"loc"`
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
			parts := make([]string, 0, len(list))
			for _, d := range list {
				loc := make([]string, 0, len(d.Loc))
				for _, l := range d.Loc {
					if s := fmt.Sprint(l); s != "body" {
						loc = append(loc, s)
					}
				}
				if len(loc) > 0 {
					parts = append(parts, strings.Join(loc, ".")+": "+d.Msg)
				} else {
					parts = append(parts, d.Msg)
				}
			}
			return strings.Join(parts, "; ")
		}
		if s := rawText(raw); s != "" {
			return s
		}
	}
	for _, k := range []string{"message", "failure", "error_message", "msg", "title"} {
		if s := rawText(j[k]); s != "" {
			return s
		}
	}
	if raw, ok := j["base_resp"]; ok {
		var br struct {
			Msg string `json:"status_msg"`
		}
		if json.Unmarshal(raw, &br) == nil && br.Msg != "" {
			return br.Msg
		}
	}
	return ""
}

// ---- polling ------------------------------------------------------------------------

// pollState is one status check of a running task.
type pollState struct {
	done   bool
	queued bool // waiting in the provider's queue
	pct    int  // provider-reported progress 0–100; < 0 = unknown
}

// poll runs check every pollInterval until the task is done, reporting
// progress 10 (queued) → 20…90 (rendering, provider value or a time-based
// estimate against expected). Up to maxBlips transient failures in a row are
// tolerated; a cancelled ctx stops at once.
func (ep *endpoint) poll(ctx context.Context, timeout, expected time.Duration, progress gen.Progress, check func(ctx context.Context) (pollState, error)) error {
	if progress == nil {
		progress = gen.NoProgress
	}
	start := time.Now()
	blips := 0
	var started time.Time
	for first := true; time.Since(start) < timeout; first = false {
		if !first {
			iv := pollInterval
			if ep.every > 0 {
				iv = ep.every
			}
			if err := sleep(ctx, iv); err != nil {
				return err
			}
		}
		st, err := check(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var pe *Error
			if errors.As(err, &pe) && pe.Retry && blips < maxBlips {
				blips++
				continue
			}
			return err
		}
		blips = 0
		switch {
		case st.done:
			progress(92, "rendered")
			return nil
		case st.queued:
			progress(10, "queued")
		default:
			if started.IsZero() {
				started = time.Now()
			}
			pct := st.pct
			if pct < 0 {
				frac := float64(time.Since(started)) / float64(expected)
				pct = int(math.Round(70 * (1 - math.Exp(-1.2*frac)) / (1 - math.Exp(-1.2))))
				pct = min(pct, 69)
			} else {
				pct = int(math.Round(float64(min(pct, 100)) * 0.7))
			}
			progress(20+pct, "rendering")
		}
	}
	return ep.errf(statusPollTimeout, true, "%s: генерація не завершилась за %s — спробуй ще раз", ep.name, humanDuration(timeout))
}

// statusPollTimeout marks the error of a task the client stopped waiting for.
const statusPollTimeout = http.StatusRequestTimeout

// gaveUp: the caller cancelled or the poll budget ran out — the remote task
// should be cancelled so nobody pays for a clip nobody waits for.
func gaveUp(ctx context.Context, err error) bool {
	return ctx.Err() != nil || statusOf(err) == statusPollTimeout
}

// cancelRemote asks the provider to stop a task after the caller cancelled
// (best effort, so the user is not billed for a clip nobody waits for).
func (ep *endpoint) cancelRemote(method, u string) {
	if u == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()
	if _, err := ep.send(ctx, request{method: method, url: u, timeout: cancelTimeout}); err != nil {
		log.Printf("[providers] %s: cancel failed: %v", ep.name, err)
	}
}

// ---- downloads ----------------------------------------------------------------------

var (
	reDataURI = regexp.MustCompile(`^data:([^;,]*)(;[^,]*)?,`)
	reScheme  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)
)

// download fetches a result: a data: URI, an absolute http(s) URL or a path
// relative to the API base. The key goes only to the API's own origin. Size
// is capped by limit.
func (ep *endpoint) download(ctx context.Context, u string, limit int64) ([]byte, string, error) {
	u = strings.TrimSpace(u)
	if u == "" {
		return nil, "", ep.errf(0, false, "%s: відповідь без посилання на файл", ep.name)
	}
	if strings.HasPrefix(u, "data:") {
		return ep.decodeDataURI(u, limit)
	}
	if reScheme.MatchString(u) && !strings.HasPrefix(strings.ToLower(u), "http://") && !strings.HasPrefix(strings.ToLower(u), "https://") {
		return nil, "", ep.errf(0, false, "%s: некоректне посилання на файл результату", ep.name)
	}
	abs := ep.abs(u)
	pu, err := url.Parse(abs)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
		return nil, "", ep.errf(0, false, "%s: некоректне посилання на файл результату", ep.name)
	}
	rq := request{method: http.MethodGet, url: abs, timeout: downloadTimeout, limit: limit, noAuth: !sameOrigin(abs, ep.base),
		header: map[string]string{"Accept": "*/*"}}
	res, err := ep.call(ctx, rq)
	if err != nil {
		var pe *Error
		if errors.As(err, &pe) && pe.Status == 0 && !pe.Retry {
			return nil, "", err
		}
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", ep.errf(statusOf(err), false, "%s: не вдалося завантажити результат: %s", ep.name, err.Error())
	}
	if len(res.body) == 0 {
		return nil, "", ep.errf(0, false, "%s: завантажений файл порожній", ep.name)
	}
	return res.body, res.header.Get("Content-Type"), nil
}

func (ep *endpoint) decodeDataURI(u string, limit int64) ([]byte, string, error) {
	m := reDataURI.FindStringSubmatch(u)
	if m == nil {
		return nil, "", ep.errf(0, false, "%s: некоректні дані результату", ep.name)
	}
	payload := u[len(m[0]):]
	if int64(len(payload))*3/4 > limit {
		return nil, "", ep.errf(0, false, "%s: файл результату завеликий (понад %d МБ)", ep.name, limit>>20)
	}
	var data []byte
	if strings.Contains(m[2], "base64") {
		b, err := decodeB64(payload)
		if err != nil {
			return nil, "", ep.errf(0, false, "%s: некоректні base64-дані результату", ep.name)
		}
		data = b
	} else {
		s, err := url.PathUnescape(payload)
		if err != nil {
			return nil, "", ep.errf(0, false, "%s: некоректні дані результату", ep.name)
		}
		data = []byte(s)
	}
	if len(data) == 0 {
		return nil, "", ep.errf(0, false, "%s: порожній файл результату", ep.name)
	}
	return data, m[1], nil
}

func statusOf(err error) int {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Status
	}
	return 0
}

// ---- helpers ------------------------------------------------------------------------

// dataURI encodes an input image as a data: URI.
func dataURI(r gen.RefImage) string {
	if bytes.HasPrefix(r.Data, []byte("data:")) {
		return string(r.Data)
	}
	return "data:" + imageMIMEOf(r) + ";base64," + base64.StdEncoding.EncodeToString(r.Data)
}

// imageMIMEOf is the ref's MIME type (sniffed when missing).
func imageMIMEOf(r gen.RefImage) string {
	if m := strings.TrimSpace(r.MIME); strings.HasPrefix(m, "image/") {
		return m
	}
	if s := http.DetectContentType(r.Data); strings.HasPrefix(s, "image/") {
		return s
	}
	return "image/png"
}

// imageExt is the file extension for a MIME type.
func imageExt(mimeType string) string {
	switch strings.ToLower(mimeType) {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	}
	return "png"
}

// resultMIME: the Content-Type when it names an image, else sniffed bytes.
func resultMIME(ctype string, data []byte) string {
	if mt, _, err := mime.ParseMediaType(ctype); err == nil && strings.HasPrefix(mt, "image/") {
		return mt
	}
	if s := http.DetectContentType(data); strings.HasPrefix(s, "image/") {
		return s
	}
	return "image/png"
}

// usableRefs drops refs without data and keeps at most n (n < 0 = all).
func usableRefs(refs []gen.RefImage, n int) []gen.RefImage {
	out := make([]gen.RefImage, 0, len(refs))
	for _, r := range refs {
		if n >= 0 && len(out) == n {
			break
		}
		if len(r.Data) > 0 {
			out = append(out, r)
		}
	}
	return out
}

// refLegend names the references by position for APIs without "@tag"
// binding: "Reference images, in order: image 1 = @taras, …".
func refLegend(refs []gen.RefImage) string {
	var named []string
	for i, r := range refs {
		n := strings.TrimPrefix(strings.TrimSpace(r.Name), "@")
		if n == "" {
			continue
		}
		if dot := strings.LastIndexByte(n, '.'); dot > 0 {
			n = n[:dot]
		}
		named = append(named, fmt.Sprintf("image %d = @%s", i+1, n))
	}
	if len(named) == 0 {
		return ""
	}
	return "\n\nReference images, in order: " + strings.Join(named, ", ") +
		". Keep each referenced character/place looking exactly like its reference image."
}

// rawText renders a JSON scalar as text; objects/arrays as compact JSON.
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

func sameOrigin(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	return err1 == nil && err2 == nil && ua.Host != "" && strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Host, ub.Host)
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
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

// unfinished is the error of a remote job the client stopped waiting for
// (failures in a row, timeout): worth retrying only when the job was
// cancelled — otherwise it may still render (and bill), and a resubmit
// would pay twice. A cancelled ctx stays ctx.Err().
func unfinished(ctx context.Context, err error, cancelled bool) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var pe *Error
	if !errors.As(err, &pe) {
		return err
	}
	e := *pe
	e.Retry = cancelled
	if !cancelled {
		e.Msg += " — задача на сервері, можливо, ще працює, тож студія не запускає її вдруге сама"
	}
	return &e
}

// estimate is the time-based render progress (0–69) of a job running since
// started, against its expected duration.
func estimate(started time.Time, expected time.Duration) int {
	frac := float64(time.Since(started)) / float64(expected)
	return min(69, int(math.Round(70*(1-math.Exp(-1.2*frac))/(1-math.Exp(-1.2)))))
}
