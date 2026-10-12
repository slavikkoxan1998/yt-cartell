package gemini

// Gemini Omni Flash video through the Interactions API — the replacement of
// the Veo 3.1 previews (https://ai.google.dev/gemini-api/docs/omni,
// https://ai.google.dev/api/interactions-api):
//
//	POST {BASE}/interactions                   {"model","input","response_format":{"type":"video",…}}
//	GET  {BASE}/interactions/{id}              only while it is still in_progress / queued
//	GET  {BASE}/files/{id}                     until state ACTIVE (delivery "uri")
//	GET  {BASE}/files/{id}:download?alt=media  the mp4
//
// The create call is synchronous (unary): it answers with status "completed"
// and the clip in steps[] → the "model_output" step → content {type "video",
// mime_type, data (base64) | uri (a Files API download URL)}. Videos over
// 4 MB need delivery "uri", so it is always asked for; inline data is
// accepted too. Like Veo, every step runs on ONE pooled key; a quota error
// parks it and the clip starts again on the next key.
//
// Input: text-to-video is a plain prompt string; image-to-video sends the
// start frame as an image content tagged <FIRST_FRAME> in the prompt;
// components send up to MaxOmniRefs reference images bound to the prompt as
// <IMAGE_REF_N> (the @tags of the studio's prompt are rewritten). Clip length
// 4/6/8/10 s (the model renders 3–10 s) goes out as response_format.duration
// ("8s"); a 400 naming that field retries without it.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

const (
	// MaxOmniRefs: "Maximum images per prompt: 10" (Gemini Omni 1.1 Flash
	// model spec); the Gemini API guide shows six <IMAGE_REF_N> references.
	MaxOmniRefs = 10
	// omniInlineBudget caps the base64 images of one request (each image may
	// be up to 20 MB, the whole request must stay well below the API limit).
	omniInlineBudget = 18 << 20
)

// OmniLengths are the clip lengths (s) requested from Gemini Omni Flash (it
// renders 3–10 s; the studio uses the Omni Flash steps).
var OmniLengths = []int{4, 6, 8, 10}

// omniResolution maps the studio resolution to response_format.resolution
// (360p | 720p default | 1080p | 4k — the last two are upscaled).
func omniResolution(res string) string {
	switch strings.ToLower(strings.TrimSpace(res)) {
	case "360p":
		return "360p"
	case "1080p":
		return "1080p"
	case "4k", "2160p":
		return "4k"
	}
	return "720p"
}

// omniContent is one content block of the Interactions API.
type omniContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	URI      string `json:"uri,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type omniStep struct {
	Type    string        `json:"type"`
	Content []omniContent `json:"content"`
}

// omniInteraction is the Interaction resource (the fields used here).
type omniInteraction struct {
	ID      string        `json:"id"`
	Status  string        `json:"status"`
	Steps   []omniStep    `json:"steps"`
	Outputs []omniContent `json:"outputs"` // earlier API revisions
	Errors  []struct {
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
	} `json:"errors"`
}

// running: not finished yet (poll GET interactions/{id}).
func (it *omniInteraction) running() bool {
	return it.Status == "in_progress" || it.Status == "queued"
}

// failure is the error of a finished interaction without a usable result.
func (it *omniInteraction) failure() error {
	switch it.Status {
	case "", "completed":
		return nil
	}
	e := &omniError{status: it.Status}
	for _, x := range it.Errors {
		code := strings.Trim(string(x.Code), `"`)
		if code == "null" {
			code = ""
		}
		e.details = append(e.details, strings.TrimSpace(code+" "+x.Message))
	}
	return e
}

// video is the first generated video block (thought and input steps skipped).
func (it *omniInteraction) video() *omniContent {
	isVideo := func(c omniContent) bool {
		return (c.Type == "video" || strings.HasPrefix(c.MimeType, "video/")) && (c.Data != "" || c.URI != "")
	}
	for _, s := range it.Steps {
		if s.Type == "thought" || s.Type == "user_input" {
			continue
		}
		for _, c := range s.Content {
			if isVideo(c) {
				return &c
			}
		}
	}
	for _, c := range it.Outputs {
		if isVideo(c) {
			return &c
		}
	}
	return nil
}

// text is what the model said instead of a video (often a safety refusal).
func (it *omniInteraction) text() string {
	var parts []string
	for _, s := range it.Steps {
		if s.Type != "model_output" {
			continue
		}
		for _, c := range s.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				parts = append(parts, strings.TrimSpace(c.Text))
			}
		}
	}
	for _, c := range it.Outputs {
		if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
			parts = append(parts, strings.TrimSpace(c.Text))
		}
	}
	return strings.Join(parts, " ")
}

// omniError is an interaction that ended failed / cancelled / incomplete.
type omniError struct {
	status  string
	details []string
}

func (e *omniError) Error() string {
	what := "генерація не вдалася"
	switch e.status {
	case "cancelled":
		what = "генерацію скасовано"
	case "incomplete", "budget_exceeded":
		what = "відео не завершено"
	case "requires_action":
		what = "сервіс чекає дії користувача, якої студія не підтримує"
	}
	msg := fmt.Sprintf("Gemini Omni: %s (%s)", what, e.status)
	if d := strings.TrimSpace(strings.Join(e.details, "; ")); d != "" {
		msg += " — " + clip(d, 300)
	}
	return msg
}

// quota: a failed interaction may carry a rate-limit error.
func (e *omniError) quota() bool {
	return e.status == "failed" && isRateLimit(0, strings.Join(e.details, " "))
}

func (e *omniError) quotaHint() quotaHint {
	l := strings.ToLower(strings.Join(e.details, " "))
	return quotaHint{daily: strings.Contains(l, "perday") || strings.Contains(l, "per day"),
		minute: strings.Contains(l, "perminute") || strings.Contains(l, "per minute")}
}

// ---- request ---------------------------------------------------------------------------

type omniImage struct {
	ref  gen.RefImage
	data string // base64
	mime string
}

func encodeOmniImage(r gen.RefImage) omniImage {
	mime := strings.TrimSpace(r.MIME)
	if mime == "" {
		mime = http.DetectContentType(r.Data)
	}
	return omniImage{ref: r, data: base64.StdEncoding.EncodeToString(r.Data), mime: mime}
}

// omniRefs are the components references: the first MaxOmniRefs with data,
// in the caller's (priority) order, within omniInlineBudget (the first one
// always goes).
func omniRefs(refs []gen.RefImage) []omniImage {
	var out []omniImage
	size := 0
	for _, r := range refs {
		if len(r.Data) == 0 {
			continue
		}
		img := encodeOmniImage(r)
		if len(out) > 0 && size+len(img.data) > omniInlineBudget {
			break
		}
		out = append(out, img)
		size += len(img.data)
		if len(out) == MaxOmniRefs {
			break
		}
	}
	return out
}

// omniInput is the images (in order) and the prompt text of a request.
// Components (with usable refs) replace the start frame, like on G-Labs:
// the studio passes the frame as a named reference when it matters.
func omniInput(prompt string, r gen.VideoRequest) ([]omniImage, string) {
	if r.Mode == gen.ModeComponents {
		if refs := omniRefs(r.Refs); len(refs) > 0 {
			return refs, refPrompt(prompt, refs)
		}
	}
	if r.Start != nil && len(r.Start.Data) > 0 {
		// <FIRST_FRAME>: "use the image as the starting frame of the video".
		return []omniImage{encodeOmniImage(*r.Start)}, "<FIRST_FRAME> " + prompt
	}
	return nil, prompt
}

// refStem is the @tag a reference is named by ("taras.jpg" → "taras").
func omniRefStem(name string) string {
	return extRe.ReplaceAllString(strings.TrimPrefix(strings.TrimSpace(name), "@"), "")
}

// refPrompt binds the references to the prompt: every "@tag" naming a
// reference becomes its <IMAGE_REF_N> tag (Google does not bind @tags), and
// a legend lists the references in order.
func refPrompt(prompt string, refs []omniImage) string {
	type bind struct{ stem, tag string }
	var binds []bind
	var legend []string
	for i, r := range refs {
		tag := fmt.Sprintf("<IMAGE_REF_%d>", i)
		if s := omniRefStem(r.ref.Name); s != "" {
			binds = append(binds, bind{s, tag})
			legend = append(legend, fmt.Sprintf("%s (%s)", tag, s))
		} else {
			legend = append(legend, tag)
		}
	}
	slices.SortStableFunc(binds, func(a, b bind) int { return len(b.stem) - len(a.stem) })

	var out strings.Builder
	for i := 0; i < len(prompt); {
		if prompt[i] == '@' {
			matched := false
			for _, b := range binds {
				end := i + 1 + len(b.stem)
				if end > len(prompt) || !strings.EqualFold(prompt[i+1:end], b.stem) {
					continue
				}
				if next, _ := utf8.DecodeRuneInString(prompt[end:]); end < len(prompt) &&
					(unicode.IsLetter(next) || unicode.IsDigit(next) || next == '_' || next == '-') {
					continue // a longer tag
				}
				out.WriteString(b.tag)
				i, matched = end, true
				break
			}
			if matched {
				continue
			}
		}
		out.WriteByte(prompt[i])
		i++
	}
	return out.String() + "\n\nReference images, in order: " + strings.Join(legend, ", ") + "."
}

// omniBodies is the create request with response_format.duration and the
// same without it (tried only after a 400 naming the field; the length then
// goes into the prompt).
func omniBodies(model, prompt string, r gen.VideoRequest, seconds int, resolution string) ([][]byte, error) {
	images, text := omniInput(prompt, r)
	build := func(duration bool) ([]byte, error) {
		format := map[string]any{
			"type":         "video",
			"aspect_ratio": veoAspect(r.Aspect),
			"resolution":   resolution,
			"delivery":     "uri",
		}
		t := text
		if duration {
			format["duration"] = fmt.Sprintf("%ds", seconds)
		} else {
			t += fmt.Sprintf("\n\nThe video is %d seconds long.", seconds)
		}
		var input any = t
		if len(images) > 0 {
			list := make([]omniContent, 0, len(images)+1)
			for _, im := range images {
				list = append(list, omniContent{Type: "image", Data: im.data, MimeType: im.mime})
			}
			input = append(list, omniContent{Type: "text", Text: t})
		}
		return json.Marshal(map[string]any{"model": model, "input": input, "response_format": format})
	}
	primary, err := build(true)
	if err != nil {
		return nil, err
	}
	alt, err := build(false)
	if err != nil {
		return nil, err
	}
	return [][]byte{primary, alt}, nil
}

// ---- flow --------------------------------------------------------------------------------

// omniVideo renders one clip with Gemini Omni Flash. progress gets 5
// starting → 10…85 rendering (eased while the synchronous call runs) → 90
// rendered → 100 done.
func (c *Client) omniVideo(ctx context.Context, cfg config.Config, model, prompt string, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	seconds := r.Seconds
	if seconds <= 0 {
		seconds = cfg.Video.Length
	}
	seconds = config.SnapVideoLength(float64(seconds), "omni_flash", 8)
	res := r.Resolution
	if strings.TrimSpace(res) == "" {
		res = cfg.Video.Resolution
	}
	bodies, err := omniBodies(model, prompt, r, seconds, omniResolution(res))
	if err != nil {
		return nil, err
	}
	var mp4 []byte
	err = c.pool.Do(ctx, func(key string) error {
		b, err := c.omniOnce(ctx, key, model, bodies, progress)
		if err != nil {
			return err
		}
		mp4 = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	return mp4, nil
}

func (c *Client) omniOnce(ctx context.Context, key, model string, bodies [][]byte, progress gen.Progress) ([]byte, error) {
	progress(5, "starting")
	start := time.Now()
	ease := startOmniEasing(progress, 10, 85)
	it, err := c.createOmni(ctx, key, model, bodies)
	if err == nil && it.running() {
		it, err = c.pollOmni(ctx, key, it, start)
	}
	ease.stop()
	if err != nil {
		return nil, err
	}
	if err := it.failure(); err != nil {
		return nil, err
	}
	v := it.video()
	if v == nil {
		if t := it.text(); t != "" {
			return nil, fmt.Errorf("Gemini Omni не повернув відео — можливо, запит заблоковано фільтром безпеки: «%s»", clip(t, 300))
		}
		return nil, fmt.Errorf("Gemini Omni: у відповіді немає відео (статус %q)", it.Status)
	}
	progress(90, "rendered")
	b, err := c.omniDownload(ctx, key, v, start)
	if err != nil {
		return nil, err
	}
	progress(100, "done")
	return b, nil
}

// createOmni is the synchronous create call (the whole render, within pollMax).
func (c *Client) createOmni(ctx context.Context, key, model string, bodies [][]byte) (*omniInteraction, error) {
	u := base + "/interactions"
	label := "omni:" + model
	var (
		b   []byte
		err error
	)
	for i, body := range bodies {
		actx, cancel := withDefaultTimeout(ctx, pollMax)
		b, err = c.call(actx, http.MethodPost, u, key, body, label)
		cancel()
		var ae *APIError
		if err == nil || i == len(bodies)-1 || !errors.As(err, &ae) || ae.Status != 400 || !strings.Contains(strings.ToLower(ae.Body), "duration") {
			break
		}
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, fmt.Errorf("Gemini Omni не встиг згенерувати відео за %s (timeout)", humanDur(pollMax))
		}
		return nil, err
	}
	var it omniInteraction
	if err := json.Unmarshal(b, &it); err != nil {
		return nil, fmt.Errorf("Gemini Omni: незрозуміла відповідь: %s", clip(string(b), 300))
	}
	return &it, nil
}

// pollOmni waits for an interaction that is still running (GET
// interactions/{id}); transient failures are tolerated like Veo polls.
func (c *Client) pollOmni(ctx context.Context, key string, it *omniInteraction, start time.Time) (*omniInteraction, error) {
	if it.ID == "" {
		return nil, errors.New("Gemini Omni: генерація ще йде, але відповідь без id — нема що опитувати")
	}
	u := base + "/interactions/" + strings.TrimPrefix(it.ID, "interactions/")
	blips := 0
	for time.Since(start) < pollMax {
		if err := waitCtx(ctx, pollInterval); err != nil {
			return nil, err
		}
		actx, cancel := context.WithTimeout(ctx, 30*time.Second)
		b, err := c.call(actx, http.MethodGet, u, key, nil, "omni:poll")
		cancel()
		var next omniInteraction
		if err == nil && json.Unmarshal(b, &next) != nil {
			err = fmt.Errorf("Gemini Omni: незрозуміла відповідь опитування: %s", clip(string(b), 200))
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if transient(err) && blips < maxPollBlips {
				blips++
				continue
			}
			return nil, err
		}
		blips = 0
		if !next.running() {
			return &next, nil
		}
	}
	return nil, fmt.Errorf("Gemini Omni не встиг згенерувати відео за %s (timeout)", humanDur(pollMax))
}

// omniDownload returns the mp4: inline data, or the Files API file once it is
// ACTIVE.
func (c *Client) omniDownload(ctx context.Context, key string, v *omniContent, start time.Time) ([]byte, error) {
	if v.Data != "" {
		b, err := decodeB64(v.Data)
		if err != nil || len(b) == 0 {
			return nil, errors.New("Gemini Omni: некоректні дані відео")
		}
		return b, nil
	}
	if ref := fileRefRe.FindString(v.URI); ref != "" {
		if err := c.waitFile(ctx, key, ref, start); err != nil {
			return nil, err
		}
	}
	actx, cancel := context.WithTimeout(ctx, dlTimeout)
	defer cancel()
	b, err := c.call(actx, http.MethodGet, downloadURL(v.URI), key, nil, "omni:download")
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, errors.New("Gemini Omni: завантажене відео порожнє")
	}
	return b, nil
}

// waitFile polls GET files/{id} until the video file is ACTIVE (states
// PROCESSING → ACTIVE | FAILED). A file the key cannot look up (403/404) is
// downloaded straight away.
func (c *Client) waitFile(ctx context.Context, key, ref string, start time.Time) error {
	u := base + "/" + ref
	blips := 0
	for first := true; time.Since(start) < pollMax || first; first = false {
		actx, cancel := context.WithTimeout(ctx, 30*time.Second)
		b, err := c.call(actx, http.MethodGet, u, key, nil, "omni:file")
		cancel()
		if err != nil {
			var ae *APIError
			switch {
			case ctx.Err() != nil:
				return ctx.Err()
			case errors.As(err, &ae) && (ae.Status == 403 || ae.Status == 404):
				return nil
			case transient(err) && blips < maxPollBlips:
				blips++
			default:
				return err
			}
		} else {
			blips = 0
			var f struct {
				State string    `json:"state"`
				Error *opStatus `json:"error"`
				File  *struct {
					State string    `json:"state"`
					Error *opStatus `json:"error"`
				} `json:"file"`
			}
			_ = json.Unmarshal(b, &f)
			if f.File != nil && f.State == "" {
				f.State, f.Error = f.File.State, f.File.Error
			}
			switch f.State {
			case "ACTIVE", "":
				return nil
			case "FAILED":
				msg := "файл відео не оброблено"
				if f.Error != nil && f.Error.Message != "" {
					msg = f.Error.Message
				}
				return fmt.Errorf("Gemini Omni: %s", clip(msg, 300))
			}
		}
		if err := waitCtx(ctx, pollInterval); err != nil {
			return err
		}
	}
	return fmt.Errorf("Gemini Omni: файл відео не став доступним за %s (timeout)", humanDur(pollMax))
}

func waitCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ---- progress while waiting --------------------------------------------------------------

// omniEasing reports "rendering" progress from lo towards hi while a call with no
// progress of its own runs (half of the way in ~90 s).
type omniEasing struct {
	done chan struct{}
	wg   sync.WaitGroup
}

func startOmniEasing(progress gen.Progress, lo, hi int) *omniEasing {
	e := &omniEasing{done: make(chan struct{})}
	progress(lo, "rendering")
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		begin, last := time.Now(), lo
		for {
			select {
			case <-e.done:
				return
			case <-t.C:
				frac := 1 - math.Exp(-time.Since(begin).Seconds()/130)
				if p := lo + int(math.Round(frac*float64(hi-lo))); p > last {
					last = p
					progress(p, "rendering")
				}
			}
		}
	}()
	return e
}

// stop ends the updates; no progress call happens after it returns.
func (e *omniEasing) stop() {
	close(e.done)
	e.wg.Wait()
}
