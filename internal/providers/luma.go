package providers

// Luma Agents API — Ray video (https://docs.agents.lumalabs.ai/guides/videos/generation/):
//
//	POST {base}/generations       {model:"ray-3.2", type:"video", prompt, aspect_ratio,
//	                               video:{resolution, duration:"5s"|"10s", start_frame | keyframes}} → {id, state}
//	GET  {base}/generations/{id}  {state queued|processing|completed|failed, output[{type,url}], failure_reason, failure_code}
//	GET  {base}/files?limit=1     key check (nothing is generated)
//
// Auth: "Authorization: Bearer luma-api-…". Images go in inline as
// {data: <base64>, media_type}. A 10 s clip cannot take start_frame, so the
// start frame then becomes keyframe 0. Generations cannot be cancelled (they
// time out after an hour). The legacy Dream Machine API (ray-2) is
// deprecated and needed public image URLs — not supported.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"ytcartell/internal/gen"
)

const lumaImageMax = 45 << 20 // the API takes up to 50 MB

var lumaAspects = []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"}

// lumaResolution maps the Settings resolution: 720p (default), 1080p.
func lumaResolution(r string) string {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "1080p", "4k":
		return "1080p"
	case "540p", "360p":
		return strings.ToLower(r)
	}
	return "720p"
}

type lumaGeneration struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	FailureReason string `json:"failure_reason"`
	FailureCode   string `json:"failure_code"`
	Output        []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"output"`
}

func lumaVideo(ctx context.Context, ep *endpoint, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	dur := "5s"
	if r.Seconds > 7 {
		dur = "10s"
	}
	video := map[string]any{"resolution": lumaResolution(r.Resolution), "duration": dur}
	if r.Start != nil && len(r.Start.Data) > 0 {
		img, err := fitImage(*r.Start, lumaImageMax)
		if err != nil {
			return nil, ep.errf(0, false, "%s: %v", ep.name, err)
		}
		frame := map[string]any{"data": base64.StdEncoding.EncodeToString(img.Data), "media_type": imageMIMEOf(img)}
		if dur == "10s" {
			video["keyframes"] = []any{frame}
			video["keyframe_indexes"] = []int{0}
		} else {
			video["start_frame"] = frame
		}
	}
	body := map[string]any{"model": ep.model, "type": "video", "prompt": clipKeep(r.Prompt, 6000), "video": video}
	if slices.Contains(lumaAspects, r.Aspect) {
		body["aspect_ratio"] = r.Aspect
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	progress(5, "starting")
	res, err := ep.call(ctx, request{method: http.MethodPost, url: "/generations", body: b, timeout: submitTimeout, create: true})
	if err != nil {
		return nil, err
	}
	var g lumaGeneration
	if json.Unmarshal(res.body, &g) != nil || strings.TrimSpace(g.ID) == "" {
		return nil, ep.errf(0, false, "%s: сервер не повернув id генерації: %s", ep.name, clip(string(res.body), 200))
	}
	genURL := "/generations/" + url.PathEscape(g.ID)
	err = ep.poll(ctx, videoPollTimeout, videoExpected, progress, func(ctx context.Context) (pollState, error) {
		res, err := ep.call(ctx, request{method: http.MethodGet, url: genURL})
		if err != nil {
			return pollState{}, err
		}
		g = lumaGeneration{}
		if err := json.Unmarshal(res.body, &g); err != nil {
			return pollState{}, ep.errf(0, true, "%s: незрозуміла відповідь статусу", ep.name)
		}
		switch g.State {
		case "completed":
			return pollState{done: true}, nil
		case "failed":
			return pollState{}, ep.failed(strings.TrimSpace(g.FailureReason + " " + g.FailureCode))
		case "queued":
			return pollState{queued: true}, nil
		}
		return pollState{pct: -1}, nil
	})
	if err != nil {
		return nil, err
	}
	video0 := ""
	for _, o := range g.Output {
		if o.URL != "" && (o.Type == "video" || video0 == "") {
			video0 = o.URL
		}
	}
	if video0 == "" {
		return nil, ep.errf(0, false, "%s: генерація завершилась без відео", ep.name)
	}
	data, _, err := ep.download(ctx, video0, maxVideoBytes)
	if err != nil {
		return nil, err
	}
	progress(100, "done")
	return data, nil
}

// lumaTest lists one file (GET /files?limit=1): the key is checked, nothing
// is generated.
func lumaTest(ctx context.Context, ep *endpoint) (string, error) {
	_, err := ep.call(ctx, request{method: http.MethodGet, url: "/files?limit=1", timeout: testTimeout})
	return "", err
}
