package providers

// RoyalTechno AI (https://api.royaltechno.cc/docs) — one async job API for
// images and video (Nano Banana, Veo 3.1, Gemini Omni Flash, Seedance,
// Grok Imagine, GPT Image, Seedream):
//
//	POST {base}/v1/jobs              {model, input:{prompt, aspect_ratio landscape|portrait,
//	                                  duration_sec, resolution, start_image_url | reference_image_urls}}
//	                                  + Idempotency-Key → 201 {id, status}
//	GET  {base}/v1/jobs/{id}         If-None-Match → 304 | {status queued|processing|succeeded|failed|canceled,
//	                                  output{url, images[], videos[]}, error{code, message, retryable}}
//	POST {base}/v1/jobs/{id}/cancel  when the caller gives up
//	GET  {base}/v1/account           key check: plans, unlimited kinds, balance
//	GET  {base}/v1/pricing           the models this key can order
//
// Auth: "Authorization: Bearer rt_live_…". Images go in as data: URIs (5 MiB
// decoded each). The base may end in /v1 or not. The client follows the
// API's rules for scripts: one Idempotency-Key per job (a retried submit
// never pays twice), polls every 10 s for video and 5 s for images (≤ 60 /
// 30 min) with If-None-Match, waits a 429's Retry-After (not a failure),
// retries 5xx / network blips with min(60, 2^n) s ±20 % backoff, never
// retries 400/401/402/403 nor daily_limit_reached (402), queue_full and
// model_unavailable, and cancels a job nobody waits for any more — after
// which the error invites a retry only if the cancel was accepted (a job
// that may still run is never submitted again).

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"ytcartell/internal/gen"
)

const (
	royalStartMax  = 4<<20 + 512<<10 // a start frame (the API takes 5 MiB decoded)
	royalRefBudget = 24 << 20        // all references of one request together
	royalRefMin    = 1 << 20
	royalAttempts  = 6 // submits of one job (429 / 503 / lost connection)
)

// Tunables (package vars so tests can shrink them).
var (
	royalImagePollTimeout = 30 * time.Minute // image queues can be long too
	royalBackoffUnit      = time.Second      // retry n waits min(60, 2^n) s ±20 %
	royalBackoffMax       = 60 * time.Second
)

var royalAspects = []string{"16:9", "9:16"}

// royalAspect maps a ratio onto the API's landscape | portrait.
func royalAspect(a string) string {
	if pickAspect(a, royalAspects) == "9:16" {
		return "portrait"
	}
	return "landscape"
}

// royalPath is an API path relative to the configured base, which may or may
// not already end in /v1.
func royalPath(ep *endpoint, p string) string {
	if strings.HasSuffix(strings.ToLower(ep.base), "/v1") {
		return p
	}
	return "/v1" + p
}

// royalVideoCaps: Omni Flash 4/6/8/10 s with up to 7 references, Veo 3.1
// fixed 8 s with up to 3, Seedance 2.0 2–15 s with up to 9, Grok Imagine
// 6/10 s (start frame only). 16:9 or 9:16.
func royalVideoCaps(m string) VideoCaps {
	m = strings.ToLower(strings.TrimSpace(m))
	aspects := slices.Clone(royalAspects)
	switch {
	case strings.HasPrefix(m, "omni-flash"):
		return VideoCaps{Lengths: []int{4, 6, 8, 10}, MaxRefs: 7, StartFrame: true, Components: true, Aspects: aspects}
	case strings.HasPrefix(m, "veo"):
		return VideoCaps{Lengths: []int{8}, MaxRefs: 3, StartFrame: true, Components: true, Aspects: aspects}
	case strings.HasPrefix(m, "seedance"):
		var ls []int
		for i := 2; i <= 15; i++ {
			ls = append(ls, i)
		}
		return VideoCaps{Lengths: ls, MaxRefs: 9, StartFrame: true, Components: true, Aspects: aspects}
	case strings.HasPrefix(m, "grok"):
		return VideoCaps{Lengths: []int{6, 10}, StartFrame: true, Aspects: aspects}
	}
	return VideoCaps{Lengths: []int{4, 6, 8, 10}, StartFrame: true, Aspects: aspects}
}

// royalImageCaps: Nano Banana up to 10 references, Seedream 4.6 up to 6,
// the others one source image (image-to-image). 16:9 or 9:16.
func royalImageCaps(m string) ImageCaps {
	m = strings.ToLower(strings.TrimSpace(m))
	switch {
	case strings.HasPrefix(m, "nano-banana"):
		return ImageCaps{MaxRefs: 10, Aspects: slices.Clone(royalAspects)}
	case strings.HasPrefix(m, "seedream"):
		return ImageCaps{MaxRefs: 6, Aspects: slices.Clone(royalAspects)}
	}
	return ImageCaps{MaxRefs: 1, Aspects: slices.Clone(royalAspects)}
}

// royalPromptMax is the model's prompt cap (longer prompts are refused).
func royalPromptMax(m string) int {
	m = strings.ToLower(m)
	switch {
	case strings.HasPrefix(m, "nano-banana"), strings.HasPrefix(m, "gpt-image"):
		return 32000
	case strings.HasPrefix(m, "seedream"):
		return 1600
	case strings.HasPrefix(m, "grok"), strings.HasPrefix(m, "kling"):
		return 2500
	case m == "seedance-2.0":
		return 5000
	}
	return 4000
}

// OmniFlash reports whether the custom video preset/model is Gemini Omni
// Flash on RoyalTechno — it takes the studio's storyboards and previous shots
// like G-Labs Omni Flash.
func OmniFlash(preset, model string) bool {
	if strings.TrimSpace(preset) != PresetRoyal {
		return false
	}
	if strings.TrimSpace(model) == "" {
		if p, ok := FindVideoPreset(preset); ok {
			model = p.DefaultModel
		}
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "omni-flash")
}

// withLegend puts the reference legend after the prompt, clipping the prompt
// so both fit the model's cap.
func withLegend(prompt, legend string, limit int) string {
	return clipKeep(prompt, max(200, limit-len([]rune(legend)))) + legend
}

// royalRefs encodes references as data URIs within the request budget.
func royalRefs(ep *endpoint, refs []gen.RefImage) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	per := min(royalStartMax, max(royalRefMin, royalRefBudget/len(refs)))
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		img, err := fitImage(r, per)
		if err != nil {
			return nil, ep.errf(0, false, "%s: %v", ep.name, err)
		}
		out = append(out, dataURI(img))
	}
	return out, nil
}

func royalImage(ctx context.Context, ep *endpoint, r gen.ImageRequest) ([]byte, string, error) {
	caps := royalImageCaps(ep.model)
	input := map[string]any{"aspect_ratio": royalAspect(caps.Aspect(r.Aspect))}
	refs := usableRefs(r.Refs, caps.MaxRefs)
	legend := ""
	if caps.MaxRefs > 1 && len(refs) > 0 {
		urls, err := royalRefs(ep, refs)
		if err != nil {
			return nil, "", err
		}
		input["reference_image_urls"] = urls
		legend = refLegend(refs)
	} else if len(refs) > 0 {
		img, err := fitImage(refs[0], royalStartMax)
		if err != nil {
			return nil, "", ep.errf(0, false, "%s: %v", ep.name, err)
		}
		input["start_image_url"] = dataURI(img)
	}
	input["prompt"] = withLegend(r.Prompt, legend, royalPromptMax(ep.model))
	job, err := royalRun(ctx, ep, input, false, gen.NoProgress)
	if err != nil {
		return nil, "", err
	}
	data, ctype, err := ep.download(ctx, job.resultURL(), maxImageBytes)
	if err != nil {
		return nil, "", err
	}
	return data, resultMIME(ctype, data), nil
}

func royalVideo(ctx context.Context, ep *endpoint, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	m := strings.ToLower(strings.TrimSpace(ep.model))
	input := map[string]any{"aspect_ratio": royalAspect(r.Aspect)}
	if !strings.HasPrefix(m, "veo") && r.Seconds > 0 { // Veo renders 8 s clips only
		input["duration_sec"] = r.Seconds
	}
	if strings.EqualFold(strings.TrimSpace(r.Resolution), "1080p") && (strings.HasPrefix(m, "veo") || m == "seedance-2.0") {
		input["resolution"] = "1080p" // priced higher; only when asked for
	}
	legend := ""
	if r.Mode == gen.ModeComponents && len(r.Refs) > 0 {
		urls, err := royalRefs(ep, r.Refs)
		if err != nil {
			return nil, err
		}
		input["reference_image_urls"] = urls
		legend = refLegend(r.Refs)
	} else if r.Start != nil && len(r.Start.Data) > 0 {
		img, err := fitImage(*r.Start, royalStartMax)
		if err != nil {
			return nil, ep.errf(0, false, "%s: %v", ep.name, err)
		}
		input["start_image_url"] = dataURI(img)
	}
	input["prompt"] = withLegend(r.Prompt, legend, royalPromptMax(m))
	progress(5, "starting")
	job, err := royalRun(ctx, ep, input, true, progress)
	if err != nil {
		return nil, err
	}
	data, _, err := ep.download(ctx, job.resultURL(), maxVideoBytes)
	if err != nil {
		return nil, err
	}
	progress(100, "done")
	return data, nil
}

// royalJob is GET /v1/jobs/{id} (and the 201 of a submit).
type royalJob struct {
	ID            string       `json:"id"`
	Status        string       `json:"status"`
	Output        *royalOutput `json:"output"`
	MediaExpired  bool         `json:"media_expired"`
	QueuePosition *int         `json:"queue_position"`
	Error         *struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

type royalOutput struct {
	URL    string `json:"url"`
	Images []struct {
		URL string `json:"url"`
	} `json:"images"`
	Videos []struct {
		URL string `json:"url"`
	} `json:"videos"`
}

// resultURL is the first delivered file ("" = none).
func (j *royalJob) resultURL() string {
	if j == nil || j.Output == nil {
		return ""
	}
	if j.Output.URL != "" {
		return j.Output.URL
	}
	for _, i := range j.Output.Images {
		if i.URL != "" {
			return i.URL
		}
	}
	for _, v := range j.Output.Videos {
		if v.URL != "" {
			return v.URL
		}
	}
	return ""
}

// royalRun submits one job and waits for it: video is polled every 10 s
// (2 × pollInterval), images every 5 s; a job nobody waits for any more is
// cancelled so it is not billed.
func royalRun(ctx context.Context, ep *endpoint, input map[string]any, video bool, progress gen.Progress) (*royalJob, error) {
	b, err := json.Marshal(map[string]any{"model": ep.model, "input": input})
	if err != nil {
		return nil, err
	}
	res, err := royalSubmit(ctx, ep, b)
	if err != nil {
		return nil, err
	}
	var job royalJob
	if json.Unmarshal(res.body, &job) != nil || strings.TrimSpace(job.ID) == "" {
		return nil, ep.errf(0, false, "%s: сервер не повернув id задачі: %s", ep.name, clip(string(res.body), 200))
	}
	final, err := royalWaitJob(ctx, ep, &job, video, progress)
	if err != nil {
		if final { // the job itself failed or was cancelled: nothing runs any more
			return nil, err
		}
		// Blips exhausted, timed out or the caller gave up: the job may still
		// be queued or rendering — cancel it, and never let the error invite a
		// resubmit while it might still run (and bill).
		return nil, unfinished(ctx, err, royalCancel(ep, job.ID))
	}
	if job.MediaExpired {
		return nil, ep.errf(0, false, "%s: файл результату вже недоступний (термін зберігання минув)", ep.name)
	}
	if job.resultURL() == "" {
		return nil, ep.errf(0, false, "%s: задача завершилась без файлу", ep.name)
	}
	return &job, nil
}

// royalWaitJob polls the job until it succeeds (job is updated in place).
// Status checks use If-None-Match; a 429 waits its Retry-After without
// counting as a failure; network trouble and 5xx are blips with backoff (up
// to maxBlips in a row). final reports an error about the job itself
// (failed, cancelled on the server) as opposed to the client giving up.
func royalWaitJob(ctx context.Context, ep *endpoint, job *royalJob, video bool, progress gen.Progress) (final bool, err error) {
	if progress == nil {
		progress = gen.NoProgress
	}
	jobURL := royalPath(ep, "/jobs/"+url.PathEscape(job.ID))
	every, timeout, expected := pollInterval, royalImagePollTimeout, imageExpected
	if video {
		every, timeout, expected = 2*pollInterval, videoPollTimeout*12/5, videoExpected // ~60 min: queues can be long
	}
	start := time.Now()
	var started time.Time
	etag := ""
	blips := 0
	wait := time.Duration(0)
	for {
		if err := sleep(ctx, wait); err != nil {
			return false, err
		}
		if time.Since(start) >= timeout {
			return false, ep.errf(statusPollTimeout, true, "%s: генерація не завершилась за %s", ep.name, humanDuration(timeout))
		}
		wait = every
		hdr := map[string]string{}
		if etag != "" {
			hdr["If-None-Match"] = etag
		}
		res, err := ep.send(ctx, request{method: http.MethodGet, url: jobURL, header: hdr})
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
		case res.status == http.StatusTooManyRequests:
			// Rate limited: wait as asked, it is not a failure of the job.
			if d, ok := retryAfter(res.header); ok {
				wait = min(max(d, every), retryAfterMax*13/6)
			} else {
				wait = max(every, royalBackoff(0))
			}
			continue
		case res.status >= 500:
			blip = ep.httpError(res)
		case res.status == http.StatusNotModified:
			// unchanged: keep the last state
		case res.ok():
			var j royalJob
			if json.Unmarshal(res.body, &j) != nil {
				blip = ep.errf(0, true, "%s: незрозуміла відповідь статусу", ep.name)
				break
			}
			*job, etag = j, res.header.Get("ETag")
		default:
			return false, ep.httpError(res)
		}
		if blip != nil {
			if blips++; blips > maxBlips {
				return false, blip
			}
			wait = max(every, royalBackoff(blips-1))
			continue
		}
		blips = 0
		switch job.Status {
		case "succeeded":
			progress(92, "rendered")
			return false, nil
		case "failed":
			return true, royalFailed(ep, job)
		case "canceled":
			return true, ep.errf(0, false, "%s: задачу скасовано", ep.name)
		case "queued", "":
			progress(10, "queued")
		default:
			if started.IsZero() {
				started = time.Now()
			}
			progress(20+estimate(started, expected), "rendering")
		}
	}
}

// royalCancel asks the API to cancel the job; true when it accepted.
func royalCancel(ep *endpoint, id string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()
	res, err := ep.send(ctx, request{method: http.MethodPost, url: royalPath(ep, "/jobs/"+url.PathEscape(id)+"/cancel"), timeout: cancelTimeout})
	if err != nil {
		log.Printf("[providers] %s: cancel failed: %v", ep.name, err)
		return false
	}
	if !res.ok() {
		log.Printf("[providers] %s: cancel refused (%d)", ep.name, res.status)
		return false
	}
	return true
}

// royalFailed maps a failed job: a content-policy code first, then the
// message (ep.failed), keeping the API's retryable flag.
func royalFailed(ep *endpoint, j *royalJob) *Error {
	detail, retry, code := "", false, ""
	if j.Error != nil {
		detail, retry, code = firstNonEmpty(j.Error.Message, j.Error.Code), j.Error.Retryable, strings.TrimSpace(j.Error.Code)
	}
	if code == "content_policy" {
		return ep.errf(0, false, "%s: запит заблоковано фільтром безпеки — зміни промт або референси%s", ep.name, suffix(firstNonEmpty(j.Error.Message)))
	}
	e := ep.failed(detail)
	e.Retry = retry
	return e
}

// royalCodeError maps the API's error codes that need their own words; nil
// for the others (httpError handles them). Any HTTP status: daily limits
// come as 402, queues as 429, unavailable models as 503.
func royalCodeError(ep *endpoint, res reply) *Error {
	detail := suffix(errorDetail(res.body))
	switch royalCode(res.body) {
	case "daily_limit_reached":
		return ep.errf(res.status, false, "%s: денний ліміт тарифу вичерпано — він оновиться опівночі за Києвом%s", ep.name, detail)
	case "queue_full":
		return ep.errf(res.status, false, "%s: черга RoyalTechno заповнена — дочекайся, поки завершаться інші задачі%s", ep.name, detail)
	case "model_unavailable":
		return ep.errf(res.status, false, "%s: модель тимчасово недоступна — спробуй пізніше%s", ep.name, detail)
	case "content_policy":
		return ep.errf(res.status, false, "%s: запит заблоковано фільтром безпеки — зміни промт або референси%s", ep.name, detail)
	}
	return nil
}

// royalBackoff is the wait before retry n+1: min(60, 2^n) s ±20 %.
func royalBackoff(n int) time.Duration {
	d := royalBackoffUnit << min(n, 16)
	if d <= 0 || d > royalBackoffMax {
		d = royalBackoffMax
	}
	return time.Duration(float64(d) * (0.8 + 0.4*mrand.Float64()))
}

// royalSubmit posts one job under one Idempotency-Key, so repeating the same
// request (429, 503, a lost connection) can never start — or bill — a second
// job. Other refusals (400/401/402/403) are final.
func royalSubmit(ctx context.Context, ep *endpoint, body []byte) (reply, error) {
	rq := request{method: http.MethodPost, url: royalPath(ep, "/jobs"), body: body, timeout: submitTimeout,
		header: map[string]string{"Idempotency-Key": uuid4()}}
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
			if !errors.As(err, &pe) || !pe.Retry {
				return reply{}, err
			}
			last = err
		case res.ok():
			return res, nil
		case royalCodeError(ep, res) != nil: // daily limit, full queue, model unavailable, policy: final
			return reply{}, royalCodeError(ep, res)
		case res.status == http.StatusTooManyRequests || res.status == http.StatusServiceUnavailable:
			fail := ep.httpError(res)
			if !fail.Retry { // out of credits
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

// royalRetry is a refusal worth repeating, with its Retry-After.
type royalRetry struct {
	err    *Error
	header http.Header
}

func (r *royalRetry) Error() string { return r.err.Error() }

// royalWait: Retry-After (up to ~2 min — a paused endpoint answers 120 s),
// else min(60, 2^n) s with ±20 % jitter.
func royalWait(attempt int, last error) time.Duration {
	var rr *royalRetry
	if errors.As(last, &rr) {
		if d, ok := retryAfter(rr.header); ok {
			return min(d, retryAfterMax*13/6)
		}
	}
	return royalBackoff(attempt)
}

// royalCode is error.code of an API error body.
func royalCode(body []byte) string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	return strings.TrimSpace(e.Error.Code)
}

// uuid4 is a random RFC 4122 version-4 UUID (the Idempotency-Key).
func uuid4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// royalTest reads the account (plans, what is unlimited, balance) and checks
// that the model can be ordered — nothing is generated.
func royalTest(ctx context.Context, ep *endpoint) (string, error) {
	res, err := ep.call(ctx, request{method: http.MethodGet, url: royalPath(ep, "/account"), timeout: testTimeout})
	if err != nil {
		return "", err
	}
	var acc struct {
		BalanceUSD string `json:"balance_usd"`
		Plans      []struct {
			Name string `json:"name"`
		} `json:"plans"`
		Unlimited []struct {
			Label string `json:"label"`
			Kind  string `json:"kind"`
		} `json:"unlimited"`
		Daily []struct {
			Label          string `json:"label"`
			Kind           string `json:"kind"`
			Limit          int    `json:"limit"`
			RemainingToday int    `json:"remaining_today"`
		} `json:"daily"`
	}
	_ = json.Unmarshal(res.body, &acc)
	var notes []string
	var plans []string
	for _, p := range acc.Plans {
		if p.Name != "" {
			plans = append(plans, p.Name)
		}
	}
	if len(plans) > 0 {
		notes = append(notes, "тариф "+strings.Join(plans, ", "))
	}
	var unl []string
	for _, u := range acc.Unlimited {
		if l := firstNonEmpty(u.Label, u.Kind); l != "" {
			unl = append(unl, l)
		}
	}
	if len(unl) > 0 {
		notes = append(notes, "без ліміту: "+strings.Join(unl, ", "))
	}
	for _, d := range acc.Daily {
		notes = append(notes, fmt.Sprintf("%s: сьогодні лишилось %d з %d", firstNonEmpty(d.Label, d.Kind), d.RemainingToday, d.Limit))
	}
	if acc.BalanceUSD != "" {
		notes = append(notes, "баланс "+acc.BalanceUSD)
	}

	// The model must be orderable with this key.
	pres, err := ep.call(ctx, request{method: http.MethodGet, url: royalPath(ep, "/pricing"), timeout: testTimeout})
	if err == nil {
		var pr struct {
			Data []struct {
				Model string `json:"model"`
			} `json:"data"`
		}
		if json.Unmarshal(pres.body, &pr) == nil && len(pr.Data) > 0 {
			seen := map[string]bool{}
			var models []string
			for _, d := range pr.Data {
				if d.Model != "" && !seen[d.Model] {
					seen[d.Model] = true
					models = append(models, d.Model)
				}
			}
			if !seen[ep.model] { // maybe only out of supply for a while
				sort.Strings(models)
				return "", ep.errf(0, false, "%s: ключ працює, але моделі «%s» зараз немає в каталозі твого ключа — можливо, тимчасово; доступні: %s",
					ep.name, ep.model, strings.Join(models, ", "))
			}
		}
	}
	return strings.Join(notes, " · "), nil
}
