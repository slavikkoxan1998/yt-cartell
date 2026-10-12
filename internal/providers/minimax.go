package providers

// MiniMax video API (https://platform.minimax.io/docs/api-reference/):
//
// V2 — MiniMax-H3 / H3-Max (current):
//
//	POST   {base}/v2/video_generation  {model, content:[{type:"text",text}, {type:"image_url",image_url:{url},role}], resolution, duration, ratio} → {task_id}
//	GET    {base}/v2/query/video_generation/{task_id}  {task:{status queued|running|succeeded|failed|cancelled, content:{url}, error:{code,message}}}
//	DELETE {base}/v2/video_generation/{task_id}        cancel (queued tasks)
//
// V1 — Hailuo 2.3 / 02, I2V-01 (legacy, still served):
//
//	POST {base}/v1/video_generation  {model, prompt, first_frame_image, duration, resolution} → {task_id, base_resp}
//	GET  {base}/v1/query/video_generation?task_id=  {status Preparing|Queueing|Processing|Success|Fail, file_id, base_resp}
//	GET  {base}/v1/files/retrieve?file_id=          {file:{download_url}, base_resp}
//
// Auth: "Authorization: Bearer <key>". V1 reports errors as HTTP 200 with
// base_resp.status_code (1004 auth, 1008 balance, 1002 rate limit, 1026/1027
// sensitive content…); V2 uses real HTTP codes. Images go in as data URIs.
// Key check: GET {base}/v1/files/list (nothing is generated).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"ytcartell/internal/gen"
)

const (
	minimaxV1ImageMax = 19 << 20 // V1: under 20 MB
	minimaxV2ImageMax = 28 << 20 // V2: up to 30 MB per image …
	// … but one request is capped at 64 MB: all its data URIs share ~45 MB.
	minimaxV2Budget = 45 << 20
)

// minimaxV2PerImage is the decoded size each of n images may take so their
// data URIs (base64: 4/3) fit the request budget together.
func minimaxV2PerImage(n int) int {
	return min(minimaxV2ImageMax, minimaxV2Budget*3/4/max(1, n))
}

var minimaxRatios = []string{"21:9", "16:9", "4:3", "1:1", "3:4", "9:16"}

// minimaxV1 reports whether model speaks the legacy V1 API.
func minimaxV1(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, p := range []string{"minimax-hailuo", "i2v-", "t2v-", "s2v-", "video-01"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

type minimaxBase struct {
	Code int    `json:"status_code"`
	Msg  string `json:"status_msg"`
}

// baseErr maps a V1 base_resp to a friendly error (nil for success).
func (ep *endpoint) minimaxBaseErr(b *minimaxBase) *Error {
	if b == nil || b.Code == 0 {
		return nil
	}
	msg := clip(b.Msg, 200)
	switch b.Code {
	case 1004, 2049:
		return ep.errf(401, false, "%s: ключ не прийнято (%d) — перевір API-ключ у Налаштуваннях → Власний API", ep.name, b.Code)
	case 1008:
		return ep.errf(402, false, "%s: на рахунку закінчились кошти або ліміт (%d) — поповни баланс у кабінеті MiniMax", ep.name, b.Code)
	case 2056: // the plan's usage window, not the balance
		return ep.errf(429, false, "%s: ліміт тарифу на це 5-годинне вікно вичерпано (2056) — зачекай наступного 5-годинного вікна", ep.name)
	case 1002, 1039, 1041, 2045: // 2045: requests grew too fast
		return ep.errf(429, true, "%s: забагато запитів (%d) — зачекай хвилинку й спробуй ще раз", ep.name, b.Code)
	case 1026, 1027:
		return ep.errf(400, false, "%s: запит заблоковано фільтром безпеки (%d) — зміни промт або кадр", ep.name, b.Code)
	case 2013:
		return ep.errf(400, false, "%s: сервіс відхилив параметри запиту (2013): %s", ep.name, msg)
	case 1000, 1001, 1013, 1024, 1033:
		return ep.errf(500, true, "%s: сервер тимчасово недоступний (%d) — спробуй пізніше", ep.name, b.Code)
	}
	return ep.errf(0, false, "%s: помилка %d: %s", ep.name, b.Code, msg)
}

// minimaxCall is call + the V1 base_resp check. A rate-limited create call
// is repeated (nothing was started).
func (ep *endpoint) minimaxCall(ctx context.Context, rq request) (reply, error) {
	for attempt := 0; ; attempt++ {
		res, err := ep.call(ctx, rq)
		if err != nil {
			return res, err
		}
		var env struct {
			Base *minimaxBase `json:"base_resp"`
		}
		_ = json.Unmarshal(res.body, &env)
		fail := ep.minimaxBaseErr(env.Base)
		if fail == nil {
			return res, nil
		}
		if !fail.Retry || attempt >= maxRetries || (rq.create && fail.Status != 429) {
			return res, fail
		}
		if err := sleep(ctx, backoff(attempt, nil)); err != nil {
			return res, err
		}
	}
}

func minimaxVideo(ctx context.Context, ep *endpoint, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	if minimaxV1(ep.model) {
		return minimaxV1Video(ctx, ep, r, progress)
	}
	return minimaxV2Video(ctx, ep, r, progress)
}

// ---- V2 -----------------------------------------------------------------------------

// minimaxV2Resolution: H3 renders 768P / 2K, H3-Max 480P / 768P.
func minimaxV2Resolution(model, want string) string {
	allowed := []any{"768P", "2K"}
	if strings.Contains(strings.ToLower(model), "max") {
		allowed = []any{"480P", "768P"}
	}
	if v, ok := pickResolution(prop{kind: "string", enum: allowed}, firstNonEmpty(want, "720p")); ok {
		return v.(string)
	}
	return "768P"
}

type minimaxTask struct {
	Task struct {
		Status  string `json:"status"`
		Content struct {
			URL string `json:"url"`
		} `json:"content"`
		Error *struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"task"`
}

func minimaxV2Video(ctx context.Context, ep *endpoint, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	per := minimaxV2PerImage(1)
	image := func(ref gen.RefImage, role string) (map[string]any, error) {
		fitted, err := fitImage(ref, per)
		if err != nil {
			return nil, ep.errf(0, false, "%s: %v", ep.name, err)
		}
		return map[string]any{"type": "image_url", "image_url": map[string]string{"url": dataURI(fitted)}, "role": role}, nil
	}
	prompt := r.Prompt
	var images []any
	if r.Mode == gen.ModeComponents {
		refs := usableRefs(r.Refs, 9)
		per = minimaxV2PerImage(len(refs)) // one budget for the whole request
		for _, ref := range refs {
			part, err := image(ref, "reference_image")
			if err != nil {
				return nil, err
			}
			images = append(images, part)
		}
		prompt += refLegend(refs)
	}
	if len(images) == 0 && r.Start != nil && len(r.Start.Data) > 0 {
		part, err := image(*r.Start, "first_frame")
		if err != nil {
			return nil, err
		}
		images = append(images, part)
	}
	content := append([]any{map[string]string{"type": "text", "text": clipKeep(prompt, 7000)}}, images...)
	body := map[string]any{
		"model":      ep.model,
		"content":    content,
		"resolution": minimaxV2Resolution(ep.model, r.Resolution),
		"duration":   r.Seconds,
	}
	if slices.Contains(minimaxRatios, r.Aspect) {
		body["ratio"] = r.Aspect
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	progress(5, "starting")
	res, err := ep.call(ctx, request{method: http.MethodPost, url: "/v2/video_generation", body: b, timeout: submitTimeout, create: true})
	if err != nil {
		return nil, err
	}
	var created struct {
		TaskID string `json:"task_id"`
	}
	if json.Unmarshal(res.body, &created) != nil || strings.TrimSpace(created.TaskID) == "" {
		return nil, ep.errf(0, false, "%s: сервер не повернув task_id: %s", ep.name, clip(string(res.body), 200))
	}
	taskURL := "/v2/query/video_generation/" + url.PathEscape(created.TaskID)
	var t minimaxTask
	err = ep.poll(ctx, videoPollTimeout, videoExpected, progress, func(ctx context.Context) (pollState, error) {
		res, err := ep.call(ctx, request{method: http.MethodGet, url: taskURL})
		if err != nil {
			return pollState{}, err
		}
		t = minimaxTask{}
		if err := json.Unmarshal(res.body, &t); err != nil {
			return pollState{}, ep.errf(0, true, "%s: незрозуміла відповідь статусу", ep.name)
		}
		switch t.Task.Status {
		case "succeeded":
			return pollState{done: true}, nil
		case "failed", "cancelled", "canceled":
			msg := t.Task.Status
			if t.Task.Error != nil {
				msg = strings.TrimSpace(t.Task.Error.Message + " " + rawCode(t.Task.Error.Code))
			}
			return pollState{}, ep.failed(msg)
		case "queued":
			return pollState{queued: true}, nil
		}
		return pollState{pct: -1}, nil
	})
	if err != nil {
		if gaveUp(ctx, err) {
			ep.cancelRemote(http.MethodDelete, "/v2/video_generation/"+url.PathEscape(created.TaskID))
		}
		return nil, err
	}
	if t.Task.Content.URL == "" {
		return nil, ep.errf(0, false, "%s: задача завершилась без відео", ep.name)
	}
	data, _, err := ep.download(ctx, t.Task.Content.URL, maxVideoBytes)
	if err != nil {
		return nil, err
	}
	progress(100, "done")
	return data, nil
}

func rawCode(c any) string {
	switch v := c.(type) {
	case string:
		if v != "" {
			return "(" + v + ")"
		}
	case float64:
		return "(" + strconv.FormatFloat(v, 'f', -1, 64) + ")"
	}
	return ""
}

// ---- V1 -----------------------------------------------------------------------------

// minimaxV1Resolution: 10 s clips only at 768P; 1080P only for 6 s; I2V-01
// models render 720P; Hailuo-02 also 512P, but only from a first frame
// (its text-to-video has no 512P).
func minimaxV1Resolution(model, want string, seconds int, firstFrame bool) string {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "i2v-") || strings.HasPrefix(m, "t2v-") || strings.HasPrefix(m, "s2v-"):
		return "720P"
	case seconds >= 10:
		return "768P"
	}
	allowed := []any{"768P", "1080P"}
	if strings.Contains(m, "-02") && firstFrame {
		allowed = []any{"512P", "768P", "1080P"}
	}
	if v, ok := pickResolution(prop{kind: "string", enum: allowed}, firstNonEmpty(want, "720p")); ok {
		return v.(string)
	}
	return "768P"
}

func minimaxV1Video(ctx context.Context, ep *endpoint, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	seconds := 6
	if r.Seconds >= 8 && !strings.HasPrefix(strings.ToLower(ep.model), "i2v-") {
		seconds = 10
	}
	body := map[string]any{
		"model":      ep.model,
		"prompt":     clipKeep(r.Prompt, 2000),
		"duration":   seconds,
		"resolution": minimaxV1Resolution(ep.model, r.Resolution, seconds, r.Start != nil && len(r.Start.Data) > 0),
	}
	if r.Start != nil && len(r.Start.Data) > 0 {
		fitted, err := fitImage(*r.Start, minimaxV1ImageMax)
		if err != nil {
			return nil, ep.errf(0, false, "%s: %v", ep.name, err)
		}
		body["first_frame_image"] = dataURI(fitted)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	progress(5, "starting")
	res, err := ep.minimaxCall(ctx, request{method: http.MethodPost, url: "/v1/video_generation", body: b, timeout: submitTimeout, create: true})
	if err != nil {
		return nil, err
	}
	var created struct {
		TaskID string `json:"task_id"`
	}
	if json.Unmarshal(res.body, &created) != nil || strings.TrimSpace(created.TaskID) == "" {
		return nil, ep.errf(0, false, "%s: сервер не повернув task_id: %s", ep.name, clip(string(res.body), 200))
	}
	var fileID string
	err = ep.poll(ctx, videoPollTimeout, videoExpected, progress, func(ctx context.Context) (pollState, error) {
		res, err := ep.minimaxCall(ctx, request{method: http.MethodGet, url: "/v1/query/video_generation?task_id=" + url.QueryEscape(created.TaskID)})
		if err != nil {
			return pollState{}, err
		}
		var st struct {
			Status string          `json:"status"`
			FileID json.RawMessage `json:"file_id"`
		}
		if err := json.Unmarshal(res.body, &st); err != nil {
			return pollState{}, ep.errf(0, true, "%s: незрозуміла відповідь статусу", ep.name)
		}
		switch st.Status {
		case "Success":
			fileID = rawText(st.FileID)
			return pollState{done: true}, nil
		case "Fail":
			return pollState{}, ep.failed("")
		case "Preparing", "Queueing":
			return pollState{queued: true}, nil
		}
		return pollState{pct: -1}, nil
	})
	if err != nil {
		return nil, err
	}
	if fileID == "" {
		return nil, ep.errf(0, false, "%s: задача завершилась без файлу", ep.name)
	}
	res, err = ep.minimaxCall(ctx, request{method: http.MethodGet, url: "/v1/files/retrieve?file_id=" + url.QueryEscape(fileID)})
	if err != nil {
		return nil, err
	}
	var f struct {
		File struct {
			DownloadURL string `json:"download_url"`
		} `json:"file"`
	}
	if json.Unmarshal(res.body, &f) != nil || f.File.DownloadURL == "" {
		return nil, ep.errf(0, false, "%s: немає посилання на відео", ep.name)
	}
	data, _, err := ep.download(ctx, f.File.DownloadURL, maxVideoBytes)
	if err != nil {
		return nil, err
	}
	progress(100, "done")
	return data, nil
}

// minimaxTest lists files (GET /v1/files/list): the key is checked, nothing
// is generated.
func minimaxTest(ctx context.Context, ep *endpoint) (string, error) {
	_, err := ep.minimaxCall(ctx, request{method: http.MethodGet, url: "/v1/files/list?purpose=video_generation_input", timeout: testTimeout})
	return "", err
}
