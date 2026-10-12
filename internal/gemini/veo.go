package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

// Veo 3.1 image-to-video via the Gemini API (port of veo.ts):
//
//	POST {BASE}/models/{VEO_MODEL}:predictLongRunning   start the operation
//	GET  {BASE}/{operationName}                          poll
//	GET  {BASE}/{fileRef}:download?alt=media             fetch the mp4
//
// The whole operation (start → poll → download) is pinned to ONE pooled key:
// an operation belongs to the key/project that created it. A quota error in
// any step parks that key and the whole generation restarts on the next one.
//
// The Veo 3.1 previews shut down on 2026-10-22 (config.VeoShutdown); the
// default Google video model is Gemini Omni Flash (omni.go), and Video sends
// a shut-down Veo model there too.

// clock is the time source of the Veo shutdown check (tests pin it).
var clock = time.Now

var fileRefRe = regexp.MustCompile(`files/[^:?]+`)

// veoAspect: Veo renders only 16:9 or 9:16.
func veoAspect(a string) string {
	if strings.TrimSpace(a) == "9:16" {
		return "9:16"
	}
	return "16:9"
}

// opStatus is an operation's error: a google.rpc.Status — code (a
// google.rpc.Code number, NOT an HTTP status), message and details. There is
// no "status" string in it (that exists only in HTTP error bodies).
type opStatus struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Details []json.RawMessage `json:"details"`
}

// rpcResourceExhausted is google.rpc.Code RESOURCE_EXHAUSTED (HTTP 429):
// https://github.com/googleapis/googleapis/blob/master/google/rpc/code.proto
const rpcResourceExhausted = 8

// rpcCodeNames are the google.rpc.Code names an operation may end with.
var rpcCodeNames = map[int]string{
	1: "CANCELLED", 2: "UNKNOWN", 3: "INVALID_ARGUMENT", 4: "DEADLINE_EXCEEDED", 5: "NOT_FOUND",
	7: "PERMISSION_DENIED", 8: "RESOURCE_EXHAUSTED", 9: "FAILED_PRECONDITION", 10: "ABORTED",
	13: "INTERNAL", 14: "UNAVAILABLE", 16: "UNAUTHENTICATED",
}

type operation struct {
	Name     string          `json:"name"`
	Done     bool            `json:"done"`
	Error    *opStatus       `json:"error"`
	Response json.RawMessage `json:"response"`
}

// opError is an operation that finished with an error.
type opError struct{ st opStatus }

func (e *opError) Error() string {
	code := rpcCodeNames[e.st.Code]
	if code == "" && e.st.Code != 0 {
		code = fmt.Sprintf("код %d", e.st.Code)
	}
	return strings.TrimSpace(fmt.Sprintf("Veo: генерація не вдалася — %s %s", code, e.st.Message))
}

func (e *opError) quota() bool {
	return e.st.Code == rpcResourceExhausted || isRateLimit(0, e.st.Message)
}

func (e *opError) quotaHint() quotaHint { return detailsHint(e.st.Details) }

// veoVideo is a generated video: a file uri and/or the bytes ("encodedVideo"
// in the Gemini API REST shape — the SDKs' video_bytes —, "bytesBase64Encoded"
// in the Vertex shape).
type veoVideo struct {
	URI                string `json:"uri"`
	EncodedVideo       string `json:"encodedVideo"`
	BytesBase64Encoded string `json:"bytesBase64Encoded"`
}

func (v *veoVideo) b64() string {
	if v.EncodedVideo != "" {
		return v.EncodedVideo
	}
	return v.BytesBase64Encoded
}

type veoSample struct {
	Video *veoVideo `json:"video"`
	veoVideo
}

// maxVeoRefs: Veo 3.1 takes up to three "asset" reference images.
const maxVeoRefs = 3

// veoSupportsRefs: reference images ("ingredients") exist on Veo 3.1 and
// 3.1 Fast (and later models), not on Veo 3.1 Lite, Veo 3.0 or Veo 2.
func veoSupportsRefs(model string) bool {
	m := strings.ToLower(model)
	return !strings.Contains(m, "lite") && !strings.Contains(m, "veo-2") && !strings.Contains(m, "veo-3.0")
}

// veoRefs are the components references Veo gets: the first maxVeoRefs refs
// with data, only in gen.ModeComponents on a model that supports them (else
// nil and the request falls back to r.Start).
func veoRefs(r gen.VideoRequest, model string) []gen.RefImage {
	if r.Mode != gen.ModeComponents || !veoSupportsRefs(model) {
		return nil
	}
	var out []gen.RefImage
	for _, ref := range r.Refs {
		if len(ref.Data) == 0 {
			continue
		}
		out = append(out, ref)
		if len(out) == maxVeoRefs {
			break
		}
	}
	return out
}

// Video renders one clip into an mp4 with the configured Google video model:
// Gemini Omni Flash (the default, and for every shut-down Veo model) through
// the Interactions API — see omniVideo — or a Veo 3.1 preview through
// predictLongRunning until its shutdown.
//
// Veo: image-to-video from r.Start, or
// text-to-video when it is nil. In gen.ModeComponents the refs go out as
// Veo 3.1 reference images (instances[0].referenceImages = [{image:
// {inlineData}, referenceType:"asset"}], at most 3, in the caller's order)
// with durationSeconds 8 — the documented requirement — and WITHOUT the start
// frame: the Gemini API documents reference images on their own, not combined
// with image/lastFrame. Names do not bind on Google (no @tag), so the prompt
// should describe the references. Models without reference images (Lite,
// Veo 2/3.0) fall back to r.Start. progress gets 5 starting → 40 queued →
// 40…90 rendering → 95 rendered → 100 done.
func (c *Client) Video(ctx context.Context, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	if progress == nil {
		progress = gen.NoProgress
	}
	cfg := c.sync()
	prompt := strings.TrimSpace(r.Prompt)
	if prompt == "" {
		return nil, errors.New("Google відео: потрібен опис руху (motion prompt)")
	}
	model := modelPath(cfg.Google.VeoModel)
	if model == "" || config.VeoRetired(model, clock()) {
		model = config.GoogleOmniModel
	}
	if config.GoogleOmni(model) {
		return c.omniVideo(ctx, cfg, model, prompt, r, progress)
	}
	fallback := cfg.Video.Length
	if fallback != 4 && fallback != 6 && fallback != 8 {
		fallback = 8
	}
	start, refs := r.Start, veoRefs(r, model)
	duration := config.SnapVeoLength(float64(r.Seconds), fallback)
	if len(refs) > 0 {
		start, duration = nil, 8
	}
	params := map[string]any{
		"aspectRatio":     veoAspect(r.Aspect),
		"durationSeconds": duration,
	}
	bodies, err := veoBodies(prompt, start, refs, params)
	if err != nil {
		return nil, err
	}

	var mp4 []byte
	err = c.pool.Do(ctx, func(key string) error {
		progress(5, "starting")
		name, err := c.startVeo(ctx, key, model, bodies)
		if err != nil {
			return err
		}
		progress(40, "queued")
		op, err := c.pollVeo(ctx, key, name, progress)
		if err != nil {
			return err
		}
		b, err := c.fetchVideo(ctx, key, op)
		if err != nil {
			return err
		}
		mp4 = b
		progress(100, "done")
		return nil
	})
	if err != nil {
		return nil, err
	}
	return mp4, nil
}

// veoBodies builds the request with the start frame as image.inlineData (and
// the reference images as referenceImages[].image.inlineData), plus an
// alternative with bytesBase64Encoded for API revisions that reject
// inlineData (tried only after a 400 naming the field — nothing is rendered).
func veoBodies(prompt string, start *gen.RefImage, refs []gen.RefImage, params map[string]any) ([][]byte, error) {
	type pic struct{ mime, data string }
	encode := func(r gen.RefImage) pic {
		mime := strings.TrimSpace(r.MIME)
		if mime == "" {
			mime = http.DetectContentType(r.Data)
		}
		return pic{mime, base64.StdEncoding.EncodeToString(r.Data)}
	}
	var first *pic
	if start != nil && len(start.Data) > 0 {
		p := encode(*start)
		first = &p
	}
	assets := make([]pic, 0, len(refs))
	for _, r := range refs {
		if len(r.Data) > 0 {
			assets = append(assets, encode(r))
		}
	}
	build := func(image func(pic) any) ([]byte, error) {
		inst := map[string]any{"prompt": prompt}
		if first != nil {
			inst["image"] = image(*first)
		}
		if len(assets) > 0 {
			list := make([]any, len(assets))
			for i, a := range assets {
				list[i] = map[string]any{"image": image(a), "referenceType": "asset"}
			}
			inst["referenceImages"] = list
		}
		return json.Marshal(map[string]any{"instances": []any{inst}, "parameters": params})
	}
	primary, err := build(func(p pic) any {
		return map[string]any{"inlineData": map[string]string{"mimeType": p.mime, "data": p.data}}
	})
	if err != nil || (first == nil && len(assets) == 0) {
		return [][]byte{primary}, err
	}
	alt, err := build(func(p pic) any {
		return map[string]any{"bytesBase64Encoded": p.data, "mimeType": p.mime}
	})
	if err != nil {
		return nil, err
	}
	return [][]byte{primary, alt}, nil
}

func (c *Client) startVeo(ctx context.Context, key, model string, bodies [][]byte) (string, error) {
	u := base + "/models/" + model + ":predictLongRunning"
	var (
		b   []byte
		err error
	)
	for i, body := range bodies {
		actx, cancel := context.WithTimeout(ctx, genTimeout)
		b, err = c.call(actx, http.MethodPost, u, key, body, "veo:"+model)
		cancel()
		var ae *APIError
		if err == nil || i == len(bodies)-1 || !errors.As(err, &ae) || ae.Status != 400 || !mentionsImageField(ae.Body) {
			break
		}
	}
	if err != nil {
		return "", err
	}
	var op operation
	if json.Unmarshal(b, &op) != nil || op.Name == "" {
		return "", fmt.Errorf("Veo: у відповіді немає назви операції: %s", clip(string(b), 300))
	}
	return op.Name, nil
}

func mentionsImageField(body string) bool {
	l := strings.ToLower(body)
	return strings.Contains(l, "inlinedata") || strings.Contains(l, "inline_data") ||
		strings.Contains(l, "bytesbase64encoded") || strings.Contains(l, "bytes_base64_encoded")
}

// pollVeo waits for the operation, easing progress 40→90 across the budget.
// Up to maxPollBlips transient failures in a row (network, 5xx) are tolerated
// so a hiccup does not throw away a render in progress.
func (c *Client) pollVeo(ctx context.Context, key, name string, progress gen.Progress) (*operation, error) {
	u := base + "/" + strings.TrimPrefix(name, "/")
	start := time.Now()
	blips := 0
	for time.Since(start) < pollMax {
		t := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}

		actx, cancel := context.WithTimeout(ctx, 30*time.Second)
		b, err := c.call(actx, http.MethodGet, u, key, nil, "veo:poll")
		cancel()
		var op operation
		if err == nil {
			if jerr := json.Unmarshal(b, &op); jerr != nil {
				err = fmt.Errorf("Veo: незрозуміла відповідь опитування: %s", clip(string(b), 200))
			}
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

		if op.Done {
			if op.Error != nil && (op.Error.Code != 0 || op.Error.Message != "") {
				return nil, &opError{st: *op.Error}
			}
			progress(95, "rendered")
			return &op, nil
		}
		frac := float64(time.Since(start)) / float64(pollMax)
		progress(40+min(50, int(math.Round(frac*50))), "rendering")
	}
	return nil, fmt.Errorf("Veo не встиг згенерувати відео за %s (timeout)", humanDur(pollMax))
}

// transient: worth another poll (network trouble, 5xx) — not quota, not 4xx.
func transient(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status >= 500 && !ae.quota()
	}
	return !isQuotaError(err)
}

// fetchVideo pulls the mp4 out of a completed operation.
func (c *Client) fetchVideo(ctx context.Context, key string, op *operation) ([]byte, error) {
	var resp struct {
		GenerateVideoResponse *struct {
			GeneratedSamples        []veoSample `json:"generatedSamples"`
			RaiMediaFilteredReasons []string    `json:"raiMediaFilteredReasons"`
		} `json:"generateVideoResponse"`
		PredictResponse *struct {
			Predictions []veoSample `json:"predictions"`
		} `json:"predictResponse"`
		Videos []veoSample `json:"videos"`
	}
	if len(op.Response) > 0 {
		_ = json.Unmarshal(op.Response, &resp)
	}
	var samples []veoSample
	var filtered []string
	switch {
	case resp.GenerateVideoResponse != nil && len(resp.GenerateVideoResponse.GeneratedSamples) > 0:
		samples = resp.GenerateVideoResponse.GeneratedSamples
	case resp.PredictResponse != nil && len(resp.PredictResponse.Predictions) > 0:
		samples = resp.PredictResponse.Predictions
	case len(resp.Videos) > 0:
		samples = resp.Videos
	}
	if resp.GenerateVideoResponse != nil {
		filtered = resp.GenerateVideoResponse.RaiMediaFilteredReasons
	}
	if len(samples) == 0 {
		if len(filtered) > 0 {
			return nil, fmt.Errorf("Veo відхилив відео фільтром безпеки: %s", clip(strings.Join(filtered, "; "), 300))
		}
		return nil, fmt.Errorf("Veo: у відповіді немає відео: %s", clip(string(op.Response), 300))
	}

	s := samples[0]
	uri := s.URI
	if s.Video != nil && s.Video.URI != "" {
		uri = s.Video.URI
	}
	if uri != "" {
		actx, cancel := context.WithTimeout(ctx, dlTimeout)
		defer cancel()
		b, err := c.call(actx, http.MethodGet, downloadURL(uri), key, nil, "veo:download")
		if err != nil {
			return nil, err
		}
		if len(b) == 0 {
			return nil, errors.New("Veo: завантажене відео порожнє")
		}
		return b, nil
	}
	b64 := s.b64()
	if s.Video != nil && s.Video.b64() != "" {
		b64 = s.Video.b64()
	}
	if b64 != "" {
		return decodeB64(b64)
	}
	return nil, fmt.Errorf("Veo: невідомий формат відповіді: %s", clip(string(op.Response), 200))
}

// downloadURL: ".../files/abc:download?alt=media" or "files/abc" →
// {BASE}/files/abc:download?alt=media; other absolute URLs are used as is.
func downloadURL(uri string) string {
	ref := uri
	if m := fileRefRe.FindString(uri); m != "" {
		ref = m
	}
	if strings.HasPrefix(ref, "http") {
		return ref
	}
	return base + "/" + ref + ":download?alt=media"
}

func humanDur(d time.Duration) string {
	if d >= time.Minute {
		return fmt.Sprintf("%d хв", int(math.Round(d.Minutes())))
	}
	return fmt.Sprintf("%d с", int(math.Round(d.Seconds())))
}
