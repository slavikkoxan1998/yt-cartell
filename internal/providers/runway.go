package providers

// Runway API (https://docs.dev.runwayml.com/):
//
//	POST   {base}/image_to_video   {model, promptImage, promptText, ratio, duration} → {id}
//	POST   {base}/text_to_video    {model, promptText, ratio, duration}              → {id}
//	GET    {base}/tasks/{id}       {status PENDING|THROTTLED|RUNNING|SUCCEEDED|FAILED|CANCELLED, progress, output[], failure, failureCode}
//	DELETE {base}/tasks/{id}       cancel (best effort when the caller gives up)
//	GET    {base}/organization     key check: credit balance (nothing is generated)
//
// Auth: "Authorization: Bearer <key>" + "X-Runway-Version: 2024-11-06".
// promptImage is a data URI (the start frame, at most 5 MB encoded); ratio
// is "W:H" in pixels; promptText is at most 1000 UTF-16 code units. Models:
// gen4_turbo (image-to-video only) / gen4.5 (2–10 s; text-to-video only at
// 1280:720 or 720:1280), veo3.1 / veo3.1_fast (4/6/8 s). Poll no more often
// than every 5 s.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"ytcartell/internal/gen"
)

// RunwayVersion is the API version header the client speaks.
const RunwayVersion = "2024-11-06"

func runwayAuth(h http.Header, key string) {
	h.Set("Authorization", "Bearer "+key)
	h.Set("X-Runway-Version", RunwayVersion)
}

// runwayRatio maps an aspect ratio to the model's pixel ratio: Veo 3.1
// 1280:720 / 720:1280 (1920:1080 / 1080:1920 at 1080p and up), Gen-4
// 1280:720, 720:1280, 1104:832, 832:1104, 960:960, 1584:672.
func runwayRatio(model, aspect, resolution string, textOnly bool) string {
	portrait := aspect == "9:16" || aspect == "3:4" || aspect == "2:3"
	if textOnly && !strings.HasPrefix(strings.ToLower(model), "veo") { // gen4.5 text_to_video: two ratios only
		if portrait {
			return "720:1280"
		}
		return "1280:720"
	}
	if strings.HasPrefix(strings.ToLower(model), "veo") {
		hd := resolution == "1080p" || strings.EqualFold(resolution, "4K")
		switch {
		case portrait && hd:
			return "1080:1920"
		case portrait:
			return "720:1280"
		case hd:
			return "1920:1080"
		}
		return "1280:720"
	}
	switch aspect {
	case "9:16", "2:3":
		return "720:1280"
	case "4:3":
		return "1104:832"
	case "3:4":
		return "832:1104"
	case "1:1":
		return "960:960"
	case "21:9":
		return "1584:672"
	}
	return "1280:720"
}

// runwayImageMax keeps a data URI under the API's 5 MB (base64 grows 4/3).
const runwayImageMax = 3_600_000

type runwayTask struct {
	ID          string          `json:"id"`
	Status      string          `json:"status"`
	Progress    *float64        `json:"progress"`
	Output      []string        `json:"output"`
	Failure     string          `json:"failure"`
	FailureCode string          `json:"failureCode"`
	Error       json.RawMessage `json:"error"`
}

// clipUTF16 cuts s to at most n UTF-16 code units (what JavaScript-based
// APIs count; an emoji takes two), never splitting a character.
func clipUTF16(s string, n int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			return s[:i]
		}
		units += w
	}
	return s
}

func runwayVideo(ctx context.Context, ep *endpoint, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	hasStart := r.Start != nil && len(r.Start.Data) > 0
	if !hasStart && strings.HasPrefix(strings.ToLower(strings.TrimSpace(ep.model)), "gen4_turbo") {
		return nil, ep.errf(0, false, "%s: gen4_turbo оживляє лише кадр (image-to-video), а кадру сцени немає — намалюй кадр або обери модель gen4.5 чи veo3.1, що знімають і з тексту", ep.name)
	}
	body := map[string]any{
		"model":      ep.model,
		"promptText": clipUTF16(r.Prompt, 1000), // the API counts UTF-16 units
		"ratio":      runwayRatio(ep.model, r.Aspect, r.Resolution, !hasStart),
		"duration":   r.Seconds,
	}
	path := "/text_to_video"
	if hasStart {
		img, err := fitImage(*r.Start, runwayImageMax)
		if err != nil {
			return nil, ep.errf(0, false, "%s: %v", ep.name, err)
		}
		path = "/image_to_video"
		body["promptImage"] = dataURI(img)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	progress(5, "starting")
	res, err := ep.call(ctx, request{method: http.MethodPost, url: path, body: b, timeout: submitTimeout, create: true})
	if err != nil {
		return nil, err
	}
	var created runwayTask
	if json.Unmarshal(res.body, &created) != nil || strings.TrimSpace(created.ID) == "" {
		return nil, ep.errf(0, false, "%s: сервер не повернув id задачі: %s", ep.name, clip(string(res.body), 200))
	}
	taskURL := "/tasks/" + url.PathEscape(created.ID)
	progress(10, "queued")
	var task runwayTask
	err = ep.poll(ctx, videoPollTimeout, videoExpected, progress, func(ctx context.Context) (pollState, error) {
		res, err := ep.call(ctx, request{method: http.MethodGet, url: taskURL})
		if err != nil {
			return pollState{}, err
		}
		task = runwayTask{}
		if err := json.Unmarshal(res.body, &task); err != nil {
			return pollState{}, ep.errf(0, true, "%s: незрозуміла відповідь статусу", ep.name)
		}
		switch strings.ToUpper(task.Status) {
		case "SUCCEEDED":
			return pollState{done: true}, nil
		case "FAILED", "CANCELLED", "CANCELED":
			return pollState{}, ep.failed(strings.TrimSpace(task.Failure + " " + task.FailureCode))
		case "PENDING", "THROTTLED":
			return pollState{queued: true}, nil
		}
		pct := -1
		if task.Progress != nil {
			pct = int(*task.Progress * 100)
		}
		return pollState{pct: pct}, nil
	})
	if err != nil {
		if gaveUp(ctx, err) {
			ep.cancelRemote(http.MethodDelete, taskURL)
		}
		return nil, err
	}
	if len(task.Output) == 0 {
		return nil, ep.errf(0, false, "%s: задача завершилась без відео", ep.name)
	}
	data, _, err := ep.download(ctx, task.Output[0], maxVideoBytes)
	if err != nil {
		return nil, err
	}
	progress(100, "done")
	return data, nil
}

// runwayTest reads the organization (credit balance): the key is checked,
// nothing is generated.
func runwayTest(ctx context.Context, ep *endpoint) (string, error) {
	res, err := ep.call(ctx, request{method: http.MethodGet, url: "/organization", timeout: testTimeout})
	if err != nil {
		return "", err
	}
	var org struct {
		CreditBalance *float64 `json:"creditBalance"`
	}
	if json.Unmarshal(res.body, &org) == nil && org.CreditBalance != nil {
		return fmt.Sprintf("кредитів: %g", *org.CreditBalance), nil
	}
	return "", nil
}
