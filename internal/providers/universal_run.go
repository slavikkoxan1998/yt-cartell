package providers

// The universal protocol at run time (see universal.go for the Spec): the
// request is rendered from the submit template, sent under one
// Idempotency-Key, then — for async APIs — polled until the state says done
// (or failed), optionally fetched once more, and the file is read from the
// result path (or the reply bytes).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/gen"
)

// universalEndpoint resolves the universal preset: the spec is parsed and
// checked first, the base URL may come from an absolute submit path.
func (c *Client) universalEndpoint(base, key, model, specText, what string) (*endpoint, *protocol, error) {
	return c.universalEndpointKind(base, key, model, specText, what, "")
}

// universalEndpointKind is universalEndpoint for a kind of API (universal_audio.go).
func (c *Client) universalEndpointKind(base, key, model, specText, what string, kind SpecKind) (*endpoint, *protocol, error) {
	sp, err := parseSpecKind(specText, kind)
	if err != nil {
		if strings.TrimSpace(specText) == "" {
			return nil, nil, &Error{Msg: fmt.Sprintf("Своє API для %s: опис API порожній — обери шаблон або «Налаштувати з документації» (Налаштування → Своє API)", what)}
		}
		return nil, nil, &Error{Msg: fmt.Sprintf("Своє API для %s: %s", what, err.Error())}
	}
	base = strings.TrimRight(firstNonEmpty(base, sp.origin()), "/")
	name := firstNonEmpty(sp.Name, hostOf(base), "Своє API")
	if base == "" {
		return nil, nil, &Error{Msg: fmt.Sprintf("%s: вкажи адресу API (Налаштування → Своє API)", name)}
	}
	key = strings.TrimSpace(key)
	if u, err := url.Parse(base); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, nil, &Error{Msg: fmt.Sprintf("%s: некоректна адреса API «%s» — потрібно http(s)://…", name, clip(redact(base, key), 80))}
	}
	if key == "" && sp.auth.needsKey() {
		return nil, nil, &Error{Msg: fmt.Sprintf(`%s: не задано API-ключ (Налаштування → Своє API) — або постав "auth": "none", якщо ключ не потрібен`, name)}
	}
	ep := &endpoint{name: name, base: base, key: key, model: strings.TrimSpace(model), hc: c.http, auth: sp.auth.header()}
	if ep.hc == nil {
		ep.hc = &http.Client{}
	}
	if key == "" {
		ep.auth = nil
	}
	p := &protocol{
		name:        name,
		keyOptional: true,
		auth:        ep.auth,
		image: func(ctx context.Context, ep *endpoint, r gen.ImageRequest) ([]byte, string, error) {
			return uniImage(ctx, ep, sp, r)
		},
		video: func(ctx context.Context, ep *endpoint, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
			return uniVideo(ctx, ep, sp, r, progress)
		},
		test: func(ctx context.Context, ep *endpoint) (string, error) { return uniTest(ctx, ep, sp) },
	}
	if sp.Test == nil {
		p.untested = name + `: опис API коректний · ключ і адресу перевірить перша генерація (додай "test" в опис, щоб перевіряти без генерації)`
	}
	return ep, p, nil
}

// uniJob is one generation through a spec.
type uniJob struct {
	ep     *endpoint
	sp     *Spec
	video  bool
	vars   map[string]any // request placeholders
	submit any            // the decoded submit reply ({{submit.…}})
	status any            // the decoded last status ({{status.…}})
	id     string
	media  *reply // a status poll that answered with the file itself
	// raw: the result is the reply itself or the value at "result" (a
	// transcript: JSON, SRT or text — universal_audio.go), not a file.
	raw bool
}

func newUniJob(ep *endpoint, sp *Spec, video bool) *uniJob {
	u := &uniJob{ep: ep, sp: sp, video: video, vars: map[string]any{}}
	if ep.model != "" {
		u.vars["model"] = ep.model
	}
	return u
}

// setFrame fills the aspect placeholders for aspect ("16:9").
func (u *uniJob) setFrame(aspect string, long int) {
	w, h := aspectSize(aspect, long)
	if aspect == "" {
		aspect = "16:9"
	}
	u.vars["aspect"] = aspect
	u.vars["width"], u.vars["height"] = w, h
	u.vars["size"] = fmt.Sprintf("%dx%d", w, h)
	switch {
	case w > h:
		u.vars["orientation"] = "landscape"
	case w < h:
		u.vars["orientation"] = "portrait"
	default:
		u.vars["orientation"] = "square"
	}
}

// aspectSize: the long side long, the other one from the ratio (multiples of 16).
func aspectSize(aspect string, long int) (int, int) {
	aw, ah := 16, 9
	if a, b, ok := strings.Cut(strings.TrimSpace(aspect), ":"); ok {
		x, e1 := strconv.Atoi(a)
		y, e2 := strconv.Atoi(b)
		if e1 == nil && e2 == nil && x > 0 && y > 0 {
			aw, ah = x, y
		}
	}
	round16 := func(v float64) int { return max(16, int(math.Round(v/16))*16) }
	if aw >= ah {
		return long, round16(float64(long) * float64(ah) / float64(aw))
	}
	return round16(float64(long) * float64(aw) / float64(ah)), long
}

// setImages encodes the start frame and/or references as data URIs and raw
// base64 within the upload budget (like RoyalTechno: 4.5 MiB each, 24 MiB
// together).
func (u *uniJob) setImages(start *gen.RefImage, refs []gen.RefImage) error {
	if start != nil && len(start.Data) > 0 {
		img, err := fitImage(*start, royalStartMax)
		if err != nil {
			return u.ep.errf(0, false, "%s: %v", u.ep.name, err)
		}
		u.vars["start_image"], u.vars["start_image_b64"] = dataURI(img), rawB64(img)
	}
	if len(refs) == 0 {
		return nil
	}
	per := min(royalStartMax, max(royalRefMin, royalRefBudget/len(refs)))
	uris, raws := make([]any, 0, len(refs)), make([]any, 0, len(refs))
	for _, r := range refs {
		img, err := fitImage(r, per)
		if err != nil {
			return u.ep.errf(0, false, "%s: %v", u.ep.name, err)
		}
		uris, raws = append(uris, dataURI(img)), append(raws, rawB64(img))
	}
	u.vars["refs"], u.vars["refs_b64"] = uris, raws
	return nil
}

func rawB64(r gen.RefImage) string {
	if bytes.HasPrefix(r.Data, []byte("data:")) {
		if i := bytes.IndexByte(r.Data, ','); i > 0 {
			return string(r.Data[i+1:])
		}
	}
	return base64.StdEncoding.EncodeToString(r.Data)
}

func uniImage(ctx context.Context, ep *endpoint, sp *Spec, r gen.ImageRequest) ([]byte, string, error) {
	caps := sp.caps().Image
	u := newUniJob(ep, sp, false)
	u.setFrame(caps.Aspect(r.Aspect), sp.size())
	prompt := r.Prompt
	refs := usableRefs(r.Refs, -1)
	usesRefs := sp.uses["refs"] || sp.uses["refs_b64"]
	usesStart := sp.uses["start_image"] || sp.uses["start_image_b64"]
	var start *gen.RefImage
	var send []gen.RefImage
	if len(refs) > 0 && caps.MaxRefs > 0 {
		if usesRefs {
			send = refs[:min(len(refs), caps.MaxRefs)]
			prompt += refLegend(send)
		}
		if usesStart {
			start = &refs[0] // image-to-image: the first reference is the source
		}
	}
	if err := u.setImages(start, send); err != nil {
		return nil, "", err
	}
	u.vars["prompt"] = prompt
	data, ctype, err := u.run(ctx, maxImageBytes, gen.NoProgress)
	if err != nil {
		return nil, "", err
	}
	return data, resultMIME(ctype, data), nil
}

// uniVideo renders a clip; r is already snapped by Client.Video (seconds,
// aspect, components mode only when the spec takes references).
func uniVideo(ctx context.Context, ep *endpoint, sp *Spec, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	u := newUniJob(ep, sp, true)
	u.setFrame(r.Aspect, sp.size())
	if r.Seconds > 0 {
		u.vars["seconds"] = r.Seconds
	}
	if res := strings.TrimSpace(r.Resolution); res != "" {
		u.vars["resolution"] = res
	}
	prompt := r.Prompt
	usesRefs := sp.uses["refs"] || sp.uses["refs_b64"]
	usesStart := sp.uses["start_image"] || sp.uses["start_image_b64"]
	var err error
	switch {
	case r.Mode == gen.ModeComponents && usesRefs && len(r.Refs) > 0:
		err = u.setImages(nil, r.Refs)
		prompt += refLegend(r.Refs)
	case r.Start != nil && usesStart:
		err = u.setImages(r.Start, nil)
	case r.Start != nil && usesRefs: // a reference-only API gets the frame as its only reference
		err = u.setImages(nil, []gen.RefImage{*r.Start})
	}
	if err != nil {
		return nil, err
	}
	u.vars["prompt"] = prompt
	progress(5, "starting")
	data, _, err := u.run(ctx, maxVideoBytes, progress)
	if err != nil {
		return nil, err
	}
	progress(100, "done")
	return data, nil
}

// uniTest makes the spec's "test" call (nothing is generated).
func uniTest(ctx context.Context, ep *endpoint, sp *Spec) (string, error) {
	if sp.Test == nil {
		return "", nil
	}
	u := newUniJob(ep, sp, false)
	rq, err := u.request("test", sp.Test, http.MethodGet, testTimeout, 0)
	if err != nil {
		return "", err
	}
	_, err = ep.call(ctx, rq)
	return "", err
}

// run submits the job and returns the file (bytes and the Content-Type).
func (u *uniJob) run(ctx context.Context, limit int64, progress gen.Progress) ([]byte, string, error) {
	sp, ep := u.sp, u.ep
	sync := sp.ID == ""
	timeout := submitTimeout
	if sync {
		timeout = imagePollTimeout
		if u.video {
			timeout = videoPollTimeout
		}
	}
	if sp.Submit.Timeout > 0 {
		timeout = time.Duration(sp.Submit.Timeout * float64(specSecond))
	}
	rq, err := u.request("submit", sp.Submit, http.MethodPost, timeout, max(limit, maxReply))
	if err != nil {
		return nil, "", err
	}
	rq.create = true
	if _, set := headerValue(sp.Submit.Headers, "Idempotency-Key"); !set {
		rq.header["Idempotency-Key"] = uuid4() // one per job: a repeated submit never starts a second one
	} // an empty value in the spec turns it off (request drops empty headers)
	var stop func()
	if sync && u.video {
		stop = tick(ctx, progress, videoExpected)
	}
	res, err := u.submitOnce(ctx, rq)
	if stop != nil {
		stop()
	}
	if err != nil {
		return nil, "", err
	}
	if !u.raw && isMedia(res) { // the API answered with the file itself
		return res.body, res.header.Get("Content-Type"), nil
	}
	if u.raw && (!looksJSON(res.body) || (sync && len(sp.Result) == 0 && sp.Fetch == nil)) { // the transcript itself (JSON, SRT, VTT, text)
		return res.body, res.header.Get("Content-Type"), nil
	}
	v, err := u.decode(res, "запуску")
	if err != nil {
		return nil, "", err
	}
	u.submit, u.status = v, v
	if !sync {
		u.id = scalarText(must(lookupPath(v, sp.ID)))
	}
	st := u.judge(v, res.body)
	if st.failed {
		return nil, "", st.err
	}
	if !sync && !st.done { // done at once: e.g. Replicate's "Prefer: wait"
		if u.id == "" {
			return nil, "", ep.errf(0, false, "%s: сервер не повернув номер задачі (поле «%s»): %s", ep.name, sp.ID, clip(redact(string(res.body), ep.key), 200))
		}
		if final, err := u.wait(ctx, progress); err != nil {
			if final { // the job failed: nothing runs any more
				return nil, "", err
			}
			// Failures in a row, a timeout or the caller gave up: the job may
			// still run — cancel it; retrying is invited only if that worked.
			return nil, "", unfinished(ctx, err, u.cancel())
		}
		if u.media != nil {
			return u.media.body, u.media.header.Get("Content-Type"), nil
		}
	}
	if u.raw {
		return u.finishRaw(ctx, limit)
	}
	return u.finish(ctx, limit)
}

func must(v any, _ bool) any { return v }

// headerValue finds a header of the template (any case).
func headerValue(h map[string]string, name string) (string, bool) {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// submitOnce posts the job; repeats (same Idempotency-Key) only after
// 429/503 or a connection lost before any answer — never after a timeout or
// another refusal, so a job is never started (or billed) twice.
func (u *uniJob) submitOnce(ctx context.Context, rq request) (reply, error) {
	ep := u.ep
	var last error
	for attempt := 0; attempt < royalAttempts; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, royalWait(attempt-1, last)); err != nil {
				return reply{}, err
			}
		}
		res, err := ep.send(ctx, rq)
		switch {
		case err != nil:
			var pe *Error
			if !errors.As(err, &pe) || !pe.Retry || pe.timeout {
				return reply{}, err
			}
			last = err
		case res.ok():
			return res, nil
		case res.status == http.StatusTooManyRequests || res.status == http.StatusServiceUnavailable:
			fail := ep.httpError(res)
			if !fail.Retry { // out of credits: final
				return reply{}, fail
			}
			last = &royalRetry{err: fail, header: res.header}
		default:
			return reply{}, ep.httpError(res)
		}
		log.Printf("[providers] %s submit: retry %d", ep.name, attempt+1)
	}
	var rr *royalRetry
	if errors.As(last, &rr) {
		return reply{}, rr.err
	}
	return reply{}, last
}

// verdict is what a reply says about the job.
type verdict struct {
	pollState
	failed bool
	err    error
}

// judge reads the state (or, without one, whether the result or an error is
// there yet) from a decoded reply.
func (u *uniJob) judge(v any, body []byte) verdict {
	sp, ep := u.sp, u.ep
	errText := ""
	for _, p := range sp.Error {
		if x, ok := lookupPath(v, p); ok {
			if errText = scalarText(x); errText == "" {
				if b, err := json.Marshal(x); err == nil && string(b) != "{}" && string(b) != "[]" {
					errText = rawText(b)
				}
			}
			if errText != "" {
				break
			}
		}
	}
	fail := func(state string) verdict {
		detail := firstNonEmpty(errText, errorDetail(body))
		if detail == "" && strings.Contains(strings.ToLower(state), "cancel") {
			return verdict{failed: true, err: ep.errf(0, false, "%s: задачу скасовано на сервері", ep.name)}
		}
		return verdict{failed: true, err: ep.failed(detail)}
	}
	if sp.State == "" {
		switch {
		case errText != "":
			return fail("")
		case u.hasResult(v):
			return verdict{pollState: pollState{done: true}}
		}
		return verdict{pollState: pollState{pct: u.progress(v)}}
	}
	state := strings.ToLower(scalarText(must(lookupPath(v, sp.State))))
	in := func(list []string) bool {
		for _, s := range list {
			if strings.EqualFold(s, state) {
				return true
			}
		}
		return false
	}
	switch {
	case state != "" && in(sp.Failed):
		return fail(state)
	case state != "" && in(sp.Done):
		if errText != "" && !u.hasResult(v) { // fal: a failed request is "COMPLETED" with an error
			return fail(state)
		}
		return verdict{pollState: pollState{done: true}}
	case state == "" || in(sp.Queued):
		return verdict{pollState: pollState{queued: true}}
	}
	return verdict{pollState: pollState{pct: u.progress(v)}}
}

func (u *uniJob) hasResult(v any) bool {
	for _, p := range u.sp.Result {
		if x, ok := lookupPath(v, p); ok && !emptyValue(x) {
			return true
		}
	}
	return false
}

func emptyValue(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return v == nil
}

// progress reads the progress field: 0–1, 0–100 or "45%" (-1 = unknown).
func (u *uniJob) progress(v any) int {
	for _, p := range u.sp.Progress {
		x, ok := lookupPath(v, p)
		if !ok {
			continue
		}
		s := strings.TrimSpace(strings.TrimSuffix(scalarText(x), "%"))
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f < 0 || math.IsNaN(f) {
			continue
		}
		if f <= 1 && !strings.Contains(scalarText(x), "%") {
			f *= 100
		}
		return int(math.Round(min(f, 100)))
	}
	return -1
}

// wait polls the job until it is done (a job that answered with its file
// leaves it in u.media). Like RoyalTechno: If-None-Match, a 429 waits its
// Retry-After without counting as a failure, network trouble / 5xx /
// garbled replies are blips with backoff (≤ maxBlips in a row). final
// reports an error about the job itself (it failed) as opposed to the
// client giving up.
func (u *uniJob) wait(ctx context.Context, progress gen.Progress) (final bool, err error) {
	if progress == nil {
		progress = gen.NoProgress
	}
	sp := u.sp
	every, timeout, expected := pollInterval, imagePollTimeout, imageExpected
	if u.video {
		every, timeout, expected = 2*pollInterval, videoPollTimeout*12/5, videoExpected // ~60 min: queues can be long
	}
	if sp.Poll.Every > 0 {
		every = time.Duration(sp.Poll.Every * float64(specSecond))
	}
	start := time.Now()
	var started time.Time
	etag := ""
	blips := 0
	wait := time.Duration(0)
	var last verdict
	seen := false
	for {
		if err := sleep(ctx, wait); err != nil {
			return false, err
		}
		if time.Since(start) >= timeout {
			return false, u.ep.errf(statusPollTimeout, true, "%s: генерація не завершилась за %s", u.ep.name, humanDuration(timeout))
		}
		wait = every
		rq, err := u.request("poll", sp.Poll, http.MethodGet, statusTimeout, 0)
		if err != nil {
			return false, err
		}
		if etag != "" {
			rq.header["If-None-Match"] = etag
		}
		res, err := u.ep.send(ctx, rq)
		var blip error
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			var pe *Error
			if !errors.As(err, &pe) || !pe.Retry {
				return false, err
			}
			blip = err
		case res.status == http.StatusTooManyRequests: // rate limited: wait as asked
			if d, ok := retryAfter(res.header); ok {
				wait = min(max(d, every), retryAfterMax*13/6)
			} else {
				wait = max(every, royalBackoff(0))
			}
			continue
		case res.status == http.StatusNotModified: // unchanged since the last ETag
			if !seen {
				last = verdict{pollState: pollState{queued: true}}
			}
		case res.ok():
			if isMedia(res) {
				u.media = &res
				progress(92, "rendered")
				return false, nil
			}
			v, derr := u.decode(res, "статусу")
			if derr != nil {
				blip = derr // a garbled status: try the next one
				break
			}
			u.status, etag, seen = v, res.header.Get("ETag"), true
			last = u.judge(v, res.body)
		case res.status >= 500:
			blip = u.ep.httpError(res)
		default:
			return false, u.ep.httpError(res)
		}
		if blip != nil {
			if blips++; blips > maxBlips {
				return false, blip
			}
			wait = max(every, royalBackoff(blips-1))
			continue
		}
		blips = 0
		switch {
		case last.failed:
			return true, last.err
		case last.done:
			progress(92, "rendered")
			return false, nil
		case last.queued:
			progress(10, "queued")
		default:
			if started.IsZero() {
				started = time.Now()
			}
			pct := estimate(started, expected)
			if last.pct >= 0 {
				pct = int(math.Round(float64(min(last.pct, 100)) * 0.7))
			}
			progress(20+pct, "rendering")
		}
	}
}

// finish makes the optional fetch call and reads the file.
func (u *uniJob) finish(ctx context.Context, limit int64) ([]byte, string, error) {
	sp, ep := u.sp, u.ep
	v := u.status
	var body []byte
	if sp.Fetch != nil {
		rq, err := u.request("fetch", sp.Fetch, http.MethodGet, downloadTimeout, max(limit, maxReply))
		if err != nil {
			return nil, "", err
		}
		if _, set := headerValue(sp.Fetch.Headers, "Accept"); !set {
			rq.header["Accept"] = "*/*" // JSON or the file itself
		}
		res, err := ep.call(ctx, rq)
		if err != nil {
			return nil, "", err
		}
		if isMedia(res) {
			return res.body, res.header.Get("Content-Type"), nil
		}
		if v, err = u.decode(res, "результату"); err != nil {
			return nil, "", err
		}
		body = res.body
		if len(sp.Result) > 0 && !u.hasResult(v) {
			if st := u.judge(v, res.body); st.failed {
				return nil, "", st.err
			}
		}
	} else if b, err := json.Marshal(v); err == nil {
		body = b
	}
	if len(sp.Result) == 0 {
		return nil, "", ep.errf(0, false, `%s: відповідь — JSON, а не файл; вкажи в описі "result" — де в ній посилання на файл: %s`, ep.name, clip(redact(string(body), ep.key), 200))
	}
	for _, p := range sp.Result {
		x, ok := lookupPath(v, p)
		if !ok {
			continue
		}
		data, ctype, err := u.resolve(ctx, x, limit, 0)
		if errors.Is(err, errNoFile) {
			continue
		}
		return data, ctype, err
	}
	return nil, "", ep.errf(0, false, "%s: у відповіді немає файлу (result: %s): %s", ep.name, strings.Join(sp.Result, ", "), clip(redact(string(body), ep.key), 240))
}

var errNoFile = errors.New("no file")

// resultKeys are looked into when a result path points at an object.
var resultKeys = []string{"url", "uri", "href", "download_url", "b64_json", "base64", "b64", "data", "image", "video", "audio", "file", "output", "content"}

// resolve turns a result value into the file: a data: URI, a URL (absolute
// or on the API), base64, or the first usable item of an array / object.
func (u *uniJob) resolve(ctx context.Context, v any, limit int64, depth int) ([]byte, string, error) {
	if depth > 4 {
		return nil, "", errNoFile
	}
	ep := u.ep
	switch x := v.(type) {
	case string:
		s := strings.TrimSpace(x)
		low := strings.ToLower(s)
		switch {
		case s == "":
			return nil, "", errNoFile
		case strings.HasPrefix(low, "data:"):
			return ep.decodeDataURI(s, limit)
		case strings.HasPrefix(low, "http://"), strings.HasPrefix(low, "https://"), strings.HasPrefix(s, "/"):
			return u.download(ctx, s, limit)
		}
		if len(s) >= 32 && !strings.ContainsAny(s, " \n") {
			if b, err := decodeB64(s); err == nil && len(b) > 0 {
				return b, http.DetectContentType(b), nil
			}
		}
		return nil, "", ep.errf(0, false, "%s: у полі результату «%s» — не посилання і не файл", ep.name, clip(redact(s, ep.key), 80))
	case []any:
		var first error
		for _, e := range x {
			data, ct, err := u.resolve(ctx, e, limit, depth+1)
			if err == nil {
				return data, ct, nil
			}
			if !errors.Is(err, errNoFile) && first == nil {
				first = err
			}
		}
		if first != nil {
			return nil, "", first
		}
	case map[string]any:
		for _, k := range resultKeys {
			if e, ok := x[k]; ok && e != nil {
				data, ct, err := u.resolve(ctx, e, limit, depth+1)
				if !errors.Is(err, errNoFile) {
					return data, ct, err
				}
			}
		}
	}
	return nil, "", errNoFile
}

// download fetches a result URL (the key only on the API's own origin) and
// refuses pages that are not a file.
func (u *uniJob) download(ctx context.Context, s string, limit int64) ([]byte, string, error) {
	ep := u.ep
	abs := u.withKey(ep.abs(s))
	data, ctype, err := ep.download(ctx, abs, limit)
	if err != nil {
		return nil, "", err
	}
	sniff := http.DetectContentType(data)
	mt, _, _ := mime.ParseMediaType(ctype)
	if strings.HasPrefix(sniff, "text/") && !strings.HasPrefix(mt, "image/") && !strings.HasPrefix(mt, "video/") {
		return nil, "", ep.errf(0, false, "%s: за посиланням результату не файл, а %s — перевір «result» в описі", ep.name, firstNonEmpty(mt, sniff))
	}
	return data, ctype, nil
}

// decode reads a JSON reply (numbers kept exact).
func (u *uniJob) decode(res reply, what string) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(res.body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		if len(bytes.TrimSpace(res.body)) == 0 {
			return nil, u.ep.errf(0, false, "%s: порожня відповідь %s", u.ep.name, what)
		}
		return nil, u.ep.errf(0, false, "%s: незрозуміла відповідь %s (не JSON і не файл): %s", u.ep.name, what, clip(redact(string(res.body), u.ep.key), 160))
	}
	return v, nil
}

// isMedia: the reply is the file itself (an image, a video, raw bytes).
func isMedia(res reply) bool {
	if len(res.body) == 0 {
		return false
	}
	mt, _, _ := mime.ParseMediaType(res.header.Get("Content-Type"))
	switch {
	case strings.HasPrefix(mt, "image/"), strings.HasPrefix(mt, "video/"), strings.HasPrefix(mt, "audio/"):
		return true
	case strings.Contains(mt, "json"), strings.HasPrefix(mt, "text/"):
		return false
	}
	s := http.DetectContentType(res.body)
	return strings.HasPrefix(s, "image/") || strings.HasPrefix(s, "video/") || strings.HasPrefix(s, "audio/") ||
		(s == "application/octet-stream" && !bytes.HasPrefix(bytes.TrimSpace(res.body), []byte("{")))
}

// cancel asks the API to stop the job (best effort, when the studio gives up).
func (u *uniJob) cancel() bool {
	if u.sp.Cancel == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()
	rq, err := u.request("cancel", u.sp.Cancel, http.MethodPost, cancelTimeout, maxErrBody)
	if err != nil {
		return false
	}
	res, err := u.ep.send(ctx, rq)
	if err != nil {
		log.Printf("[providers] %s: cancel failed: %v", u.ep.name, err)
		return false
	}
	if !res.ok() {
		log.Printf("[providers] %s: cancel refused (%d)", u.ep.name, res.status)
		return false
	}
	return true
}

// ---- rendering -----------------------------------------------------------------------------

// value is a placeholder's value (ok false = none).
func (u *uniJob) value(name string) (any, bool) {
	if base, mod, ok := strings.Cut(name, "|"); ok {
		v, has := u.value(base)
		if has && mod == "text" {
			return valueText(v), true
		}
		return v, has
	}
	switch {
	case name == "id":
		return u.id, u.id != ""
	case strings.HasPrefix(name, "submit."):
		return lookupPath(u.submit, name[len("submit."):])
	case strings.HasPrefix(name, "status."):
		return lookupPath(u.status, name[len("status."):])
	}
	v, ok := u.vars[name]
	return v, ok && v != nil
}

// render fills a body template; false = drop this value (a placeholder
// without a value).
func (u *uniJob) render(v any) (any, bool) {
	switch x := v.(type) {
	case string:
		return u.renderString(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if r, ok := u.render(e); ok {
				out[k] = r
			}
		}
		return out, true
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			r, ok := u.render(e)
			if !ok {
				continue
			}
			if s, isStr := e.(string); isStr && wholePlaceholder(s) != "" {
				if arr, isArr := r.([]any); isArr { // "{{refs}}" inside an array: spliced in
					out = append(out, arr...)
					continue
				}
			}
			out = append(out, r)
		}
		return out, true
	}
	return v, true
}

// wholePlaceholder is the name when s is exactly one placeholder.
func wholePlaceholder(s string) string {
	t := strings.TrimSpace(s)
	if t == "{id}" {
		return "id"
	}
	if m := reSpecVar.FindStringSubmatchIndex(t); m != nil && m[0] == 0 && m[1] == len(t) {
		return t[m[2]:m[3]]
	}
	return ""
}

func (u *uniJob) renderString(s string) (any, bool) {
	if name := wholePlaceholder(s); name != "" {
		return u.value(name)
	}
	ok := true
	out := reSpecVar.ReplaceAllStringFunc(s, func(m string) string {
		v, has := u.value(reSpecVar.FindStringSubmatch(m)[1])
		if !has {
			ok = false
			return ""
		}
		return valueText(v)
	})
	if u.id != "" {
		out = reSpecID.ReplaceAllString(out, u.id)
	}
	return out, ok
}

// valueText is a value as text inside a string.
func valueText(v any) string {
	if s := scalarText(v); s != "" {
		return s
	}
	switch x := v.(type) {
	case string:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			parts = append(parts, valueText(e))
		}
		return strings.Join(parts, ",")
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// path renders a call path: placeholders escaped (the model keeps its
// slashes), or — when the whole path is one placeholder with a URL — that URL.
func (u *uniJob) path(call, tpl string) (string, error) {
	if name := wholePlaceholder(tpl); name != "" {
		v, ok := u.value(name)
		if !ok {
			return "", u.ep.errf(0, false, "%s: немає значення для %s (%s.path) — перевір опис API", u.ep.name, placeholderName(name), call)
		}
		if s := scalarText(v); strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "/") {
			return s, nil
		}
	}
	q := strings.IndexByte(tpl, '?')
	var missing string
	render := func(prefix string, inQuery bool) string {
		out := reSpecVar.ReplaceAllStringFunc(prefix, func(m string) string {
			name := reSpecVar.FindStringSubmatch(m)[1]
			v, ok := u.value(name)
			if !ok {
				missing = name
				return ""
			}
			t := valueText(v)
			switch {
			case inQuery:
				return url.QueryEscape(t)
			case name == "model":
				segs := strings.Split(strings.Trim(t, "/"), "/")
				for i, sg := range segs {
					segs[i] = url.PathEscape(sg)
				}
				return strings.Join(segs, "/")
			}
			return url.PathEscape(t)
		})
		return reSpecID.ReplaceAllStringFunc(out, func(string) string {
			if u.id == "" {
				missing = "id"
				return ""
			}
			if inQuery {
				return url.QueryEscape(u.id)
			}
			return url.PathEscape(u.id)
		})
	}
	var out string
	if q >= 0 {
		out = render(tpl[:q], false) + "?" + render(tpl[q+1:], true)
	} else {
		out = render(tpl, false)
	}
	if missing != "" {
		return "", u.ep.errf(0, false, "%s: немає значення для %s (%s.path) — перевір опис API", u.ep.name, placeholderName(missing), call)
	}
	return out, nil
}

// withKey adds a query-parameter key to a URL on the API's own origin.
func (u *uniJob) withKey(abs string) string {
	a := u.sp.auth
	if a.kind != "query" || u.ep.key == "" || !sameOrigin(abs, u.ep.base) {
		return abs
	}
	pu, err := url.Parse(abs)
	if err != nil {
		return abs
	}
	q := pu.Query()
	q.Set(a.name, u.ep.key)
	pu.RawQuery = q.Encode()
	return pu.String()
}

// request builds one call of the spec. The key goes only to the API's own
// origin (header auth via endpoint.auth, query auth via withKey).
func (u *uniJob) request(name string, c *SpecCall, defMethod string, timeout time.Duration, limit int64) (request, error) {
	ep := u.ep
	p, err := u.path(name, c.Path)
	if err != nil {
		return request{}, err
	}
	abs := ep.abs(p)
	if pu, err := url.Parse(abs); err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
		return request{}, ep.errf(0, false, "%s: некоректна адреса запиту %s («%s»)", ep.name, name, clip(redact(abs, ep.key), 100))
	}
	method := c.Method
	if method == "" {
		method = defMethod
	}
	rq := request{method: method, url: u.withKey(abs), timeout: timeout, limit: limit, noAuth: !sameOrigin(abs, ep.base), header: map[string]string{}}
	for k, v := range c.Headers {
		r, ok := u.renderString(v)
		if !ok {
			continue
		}
		if s := valueText(r); s != "" {
			rq.header[k] = s
		}
	}
	if c.Body != nil {
		body, ok := u.render(c.Body)
		if !ok {
			body = map[string]any{}
		}
		switch c.Encoding {
		case "multipart":
			rq.body, rq.ctype, err = multipartBody(body)
		case "form":
			rq.body, rq.ctype = formBody(body)
		default:
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			err = enc.Encode(body)
			rq.body = bytes.TrimRight(buf.Bytes(), "\n")
		}
		if err != nil {
			return request{}, ep.errf(0, false, "%s: не вдалося скласти запит %s: %v", ep.name, name, err)
		}
	}
	return rq, nil
}

func formBody(body any) ([]byte, string) {
	vals := url.Values{}
	m, _ := body.(map[string]any)
	for k, v := range m {
		if arr, ok := v.([]any); ok {
			for _, e := range arr {
				vals.Add(k, valueText(e))
			}
			continue
		}
		vals.Set(k, valueText(v))
	}
	return []byte(vals.Encode()), "application/x-www-form-urlencoded"
}

// multipartBody: data: URIs become file parts, everything else form fields.
func multipartBody(body any) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	m, _ := body.(map[string]any)
	n := 0
	add := func(k string, v any) error {
		s, isStr := v.(string)
		if isStr && strings.HasPrefix(s, "data:") {
			mm := reDataURI.FindStringSubmatch(s)
			if mm != nil && strings.Contains(mm[2], "base64") {
				data, err := decodeB64(s[len(mm[0]):])
				if err != nil {
					return err
				}
				n++
				mt := firstNonEmpty(mm[1], "application/octet-stream")
				h := textproto.MIMEHeader{}
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeQuotes(k), partName(mt, n)))
				h.Set("Content-Type", mt)
				w, err := mw.CreatePart(h)
				if err != nil {
					return err
				}
				_, err = w.Write(data)
				return err
			}
		}
		return mw.WriteField(k, valueText(v))
	}
	for k, v := range m {
		if arr, ok := v.([]any); ok {
			for _, e := range arr {
				if err := add(k, e); err != nil {
					return nil, "", err
				}
			}
			continue
		}
		if err := add(k, v); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

func escapeQuotes(s string) string { return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) }

// tick reports a time-based progress estimate while a synchronous render
// holds the request open; stop waits for it to end.
func tick(ctx context.Context, progress gen.Progress, expected time.Duration) (stop func()) {
	if progress == nil {
		return func() {}
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	start := time.Now()
	go func() {
		defer wg.Done()
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				frac := float64(time.Since(start)) / float64(expected)
				pct := int(math.Round(70 * (1 - math.Exp(-1.2*frac)) / (1 - math.Exp(-1.2))))
				progress(20+min(pct, 69), "rendering")
			}
		}
	}()
	return func() {
		close(done)
		wg.Wait()
	}
}
