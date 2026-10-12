package providers

// fal.ai queue API (https://fal.ai/docs/documentation/model-apis/inference/queue):
//
//	POST {base}/{model}                       input JSON → {request_id, status_url, response_url, cancel_url}
//	GET  {base}/{app}/requests/{id}/status    {status IN_QUEUE|IN_PROGRESS|COMPLETED, queue_position, error?}
//	GET  {base}/{app}/requests/{id}           the model's output (images[].url | video.url) or an HTTP error
//	PUT  {base}/{app}/requests/{id}/cancel    best effort when the caller gives up
//
// {app} is the model id without its sub-path ("fal-ai/kling-video" for
// "fal-ai/kling-video/v3/pro/image-to-video"; the queue answers 405 to the
// full id). A failed request is still COMPLETED, with "error". Auth:
// "Authorization: Key <FAL_KEY>".
//
// Platform API (https://api.fal.ai/v1): the model's input schema
// (GET /models?endpoint_id=…&expand=openapi-3.0, cached) and the key check
// (GET /models/pricing?endpoint_id=… — nothing is generated). Storage
// (https://rest.fal.ai/storage/upload/initiate): reference images are
// uploaded to fal's CDN (data URIs are "not recommended" beyond a few KB);
// data URIs remain the fallback.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ytcartell/internal/gen"
)

func falAuth(h http.Header, key string) { h.Set("Authorization", "Key "+key) }

// Hosts of fal's platform API and storage for a queue base URL ("" =
// unknown, e.g. behind a proxy: built-in schemas and data URIs are used).
// Package vars so tests can point them at a mock.
var (
	falPlatformFor = func(base string) string {
		if hostOf(base) == "queue.fal.run" {
			return "https://api.fal.ai/v1"
		}
		return ""
	}
	falStorageFor = func(base string) string {
		if hostOf(base) == "queue.fal.run" {
			return "https://rest.fal.ai"
		}
		return ""
	}
)

// falDataURIMax caps an inline (data URI) reference.
const falDataURIMax = 6 << 20

func hostOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return strings.ToLower(p.Hostname())
}

// falApp is the app part of a model id: its first two path segments (three
// for "workflows/…" and "comfy/…").
func falApp(model string) string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(model), "/"), "/")
	n := 2
	if len(parts) > 0 && (parts[0] == "workflows" || parts[0] == "comfy") {
		n = 3
	}
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "/")
}

// falModelPath escapes the model id segment by segment.
func falModelPath(model string) string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(model), "/"), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return "/" + strings.Join(parts, "/")
}

type falQueued struct {
	RequestID   string `json:"request_id"`
	StatusURL   string `json:"status_url"`
	ResponseURL string `json:"response_url"`
	CancelURL   string `json:"cancel_url"`
}

type falStatus struct {
	Status    string          `json:"status"`
	Error     json.RawMessage `json:"error"`
	ErrorType string          `json:"error_type"`
}

// falRun submits input to the model's queue, waits and returns the output JSON.
func falRun(ctx context.Context, ep *endpoint, model string, input map[string]any, timeout, expected time.Duration, progress gen.Progress) (json.RawMessage, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	res, err := ep.call(ctx, request{method: http.MethodPost, url: falModelPath(model), body: body, timeout: submitTimeout, create: true})
	if err != nil {
		return nil, err
	}
	var q falQueued
	if json.Unmarshal(res.body, &q) != nil || strings.TrimSpace(q.RequestID) == "" {
		return nil, ep.errf(0, false, "%s: сервер не повернув request_id: %s", ep.name, clip(string(res.body), 200))
	}
	// The queue's own URLs when they point back at the configured server,
	// else built from the documented layout (the key never goes elsewhere).
	reqURL := "/" + falApp(model) + "/requests/" + url.PathEscape(q.RequestID)
	pick := func(got, built string) string {
		if got != "" && sameOrigin(got, ep.base) {
			return got
		}
		return built
	}
	statusURL := pick(q.StatusURL, reqURL+"/status")
	responseURL := pick(q.ResponseURL, reqURL)
	cancelURL := pick(q.CancelURL, reqURL+"/cancel")

	err = ep.poll(ctx, timeout, expected, progress, func(ctx context.Context) (pollState, error) {
		res, err := ep.call(ctx, request{method: http.MethodGet, url: statusURL})
		if err != nil {
			return pollState{}, err
		}
		var st falStatus
		if err := json.Unmarshal(res.body, &st); err != nil {
			return pollState{}, ep.errf(0, true, "%s: незрозуміла відповідь статусу", ep.name)
		}
		switch st.Status {
		case "COMPLETED":
			if msg := rawText(st.Error); msg != "" {
				return pollState{}, ep.failed(strings.TrimSpace(msg + " " + st.ErrorType))
			}
			return pollState{done: true}, nil
		case "IN_QUEUE":
			return pollState{queued: true}, nil
		}
		return pollState{pct: -1}, nil
	})
	if err != nil {
		if gaveUp(ctx, err) {
			ep.cancelRemote(http.MethodPut, cancelURL)
		}
		return nil, err
	}
	// A failed generation may also be reported here, as an HTTP error.
	res, err = ep.call(ctx, request{method: http.MethodGet, url: responseURL})
	if err != nil {
		return nil, err
	}
	return res.body, nil
}

// ---- schemas, uploads ---------------------------------------------------------------

// falSchema is the model's input schema: live from the platform API
// (cached), else the built-in one for its family.
func falSchema(ctx context.Context, ep *endpoint, model string, image bool) *inputSchema {
	builtin := falVideoSchema(model)
	if image {
		builtin = falImageSchema(model)
	}
	if platform := falPlatformFor(ep.base); platform != "" {
		s := cachedSchema(ctx, "fal|"+platform+"|"+model, func() (*inputSchema, error) {
			return falFetchSchema(ctx, ep, platform, model)
		})
		if s != nil {
			return s.withBuiltin(builtin)
		}
	}
	return builtin
}

func falFetchSchema(ctx context.Context, ep *endpoint, platform, model string) (*inputSchema, error) {
	u := platform + "/models?endpoint_id=" + url.QueryEscape(model) + "&expand=openapi-3.0"
	res, err := ep.send(ctx, request{method: http.MethodGet, url: u, timeout: statusTimeout})
	if err != nil {
		return nil, err
	}
	if !res.ok() {
		return nil, fmt.Errorf("schema: HTTP %d", res.status)
	}
	var doc struct {
		Models []struct {
			EndpointID string         `json:"endpoint_id"`
			OpenAPI    map[string]any `json:"openapi"`
		} `json:"models"`
	}
	if err := json.Unmarshal(res.body, &doc); err != nil {
		return nil, err
	}
	for _, m := range doc.Models {
		if m.EndpointID != "" && m.EndpointID != model {
			continue
		}
		if s := openAPIInput(m.OpenAPI, "/"+model); s != nil {
			return s, nil
		}
	}
	return nil, errors.New("schema: no input schema")
}

// openAPIInput finds the request body schema of the POST operation (the
// one at path when present) and flattens it.
func openAPIInput(doc map[string]any, path string) *inputSchema {
	paths, _ := doc["paths"].(map[string]any)
	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	try := func(item any) *inputSchema {
		op, _ := item.(map[string]any)
		post, _ := op["post"].(map[string]any)
		body, _ := post["requestBody"].(map[string]any)
		content, _ := body["content"].(map[string]any)
		js, _ := content["application/json"].(map[string]any)
		node, _ := js["schema"].(map[string]any)
		if node == nil {
			return nil
		}
		return parseSchema(node, schemas)
	}
	if s := try(paths[path]); s != nil {
		return s
	}
	for p, item := range paths {
		if !strings.Contains(p, "/requests/") {
			if s := try(item); s != nil {
				return s
			}
		}
	}
	return nil
}

// falInputs turns images into model inputs: uploaded to fal's CDN when its
// storage API is known, else (or when the upload fails) data URIs.
func falInputs(ctx context.Context, ep *endpoint, refs []gen.RefImage) ([]string, error) {
	storage := falStorageFor(ep.base)
	out := make([]string, 0, len(refs))
	for i, r := range refs {
		if storage != "" {
			u, err := falUpload(ctx, ep, storage, r, i)
			if err == nil {
				out = append(out, u)
				continue
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			log.Printf("[providers] fal.ai: upload failed, sending the image inline: %v", err)
		}
		fitted, err := fitImage(r, falDataURIMax)
		if err != nil {
			return nil, ep.errf(0, false, "%s: %v", ep.name, err)
		}
		out = append(out, dataURI(fitted))
	}
	return out, nil
}

// falUpload puts one image on fal's CDN and returns its URL.
func falUpload(ctx context.Context, ep *endpoint, storage string, r gen.RefImage, i int) (string, error) {
	mt := imageMIMEOf(r)
	body, err := json.Marshal(map[string]string{"content_type": mt, "file_name": fmt.Sprintf("ref-%d.%s", i+1, imageExt(mt))})
	if err != nil {
		return "", err
	}
	res, err := ep.call(ctx, request{method: http.MethodPost, url: storage + "/storage/upload/initiate?storage_type=fal-cdn-v3", body: body, timeout: statusTimeout})
	if err != nil {
		return "", err
	}
	var up struct {
		UploadURL string `json:"upload_url"`
		FileURL   string `json:"file_url"`
	}
	if json.Unmarshal(res.body, &up) != nil || up.UploadURL == "" || up.FileURL == "" {
		return "", errors.New("upload: no upload_url")
	}
	if _, err := ep.call(ctx, request{method: http.MethodPut, url: up.UploadURL, body: r.Data, ctype: mt, noAuth: true, timeout: downloadTimeout}); err != nil {
		return "", err
	}
	return up.FileURL, nil
}

// missingErr: the model needs input the studio does not have.
func (ep *endpoint) missingErr(model string, missing []string) error {
	return ep.errf(0, false, "%s: модель «%s» вимагає поля «%s», яких студія не заповнює — обери іншу модель",
		ep.name, model, strings.Join(missing, "», «"))
}

// ---- image / video / test -----------------------------------------------------------

func falImage(ctx context.Context, ep *endpoint, r gen.ImageRequest) ([]byte, string, error) {
	refs := usableRefs(r.Refs, -1)
	model := falImageModel(ep.model, len(refs) > 0)
	schema := falSchema(ctx, ep, model, true)
	if limit := schema.imageCaps().MaxRefs; len(refs) > limit {
		refs = refs[:limit]
	}
	urls, err := falInputs(ctx, ep, refs)
	if err != nil {
		return nil, "", err
	}
	input, missing := buildImage(schema, r.Prompt, r.Aspect, refs, urls)
	if len(missing) > 0 {
		return nil, "", ep.missingErr(model, missing)
	}
	out, err := falRun(ctx, ep, model, input, imagePollTimeout, imageExpected, nil)
	if err != nil {
		return nil, "", err
	}
	u := falImageURL(out)
	if u == "" {
		return nil, "", ep.errf(0, false, "%s: модель не повернула зображення: %s", ep.name, clip(string(out), 200))
	}
	data, ctype, err := ep.download(ctx, u, maxImageBytes)
	if err != nil {
		return nil, "", err
	}
	return data, resultMIME(ctype, data), nil
}

func falVideo(ctx context.Context, ep *endpoint, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	model := ep.model
	schema := falSchema(ctx, ep, model, false)
	in, err := videoInputs(ctx, ep, schema, r, falInputs)
	if err != nil {
		return nil, err
	}
	input, _, missing := schema.build(in)
	if len(missing) > 0 {
		return nil, ep.missingErr(model, missing)
	}
	progress(5, "starting")
	out, err := falRun(ctx, ep, model, input, videoPollTimeout, videoExpected, progress)
	if err != nil {
		return nil, err
	}
	u := falVideoURL(out)
	if u == "" {
		return nil, ep.errf(0, false, "%s: модель не повернула відео: %s", ep.name, clip(string(out), 200))
	}
	data, _, err := ep.download(ctx, u, maxVideoBytes)
	if err != nil {
		return nil, err
	}
	progress(100, "done")
	return data, nil
}

// buildImage maps an image request with its uploaded refs onto the schema;
// the prompt's legend names only the refs actually sent.
func buildImage(schema *inputSchema, prompt, aspect string, refs []gen.RefImage, urls []string) (map[string]any, []string) {
	in := inputs{image: true, prompt: prompt, refs: urls, aspect: aspect}
	_, used, _ := schema.build(in)
	in.prompt = prompt + refLegend(refs[:min(used, len(refs))])
	out, _, missing := schema.build(in)
	return out, missing
}

// videoInputs prepares a clip request for a schema-driven API: components
// refs (when the model takes them) or the start frame, uploaded by put.
func videoInputs(ctx context.Context, ep *endpoint, schema *inputSchema, r gen.VideoRequest,
	put func(context.Context, *endpoint, []gen.RefImage) ([]string, error)) (inputs, error) {
	in := inputs{prompt: r.Prompt, seconds: r.Seconds, aspect: r.Aspect, resolution: firstNonEmpty(r.Resolution, "720p")}
	caps := schema.videoCaps()
	if r.Mode == gen.ModeComponents && caps.Components {
		if refs := usableRefs(r.Refs, caps.MaxRefs); len(refs) > 0 {
			urls, err := put(ctx, ep, refs)
			if err != nil {
				return in, err
			}
			in.refs, in.components = urls, true
			in.prompt += refLegend(refs)
			return in, nil
		}
	}
	if r.Start != nil && len(r.Start.Data) > 0 {
		urls, err := put(ctx, ep, []gen.RefImage{*r.Start})
		if err != nil {
			return in, err
		}
		in.start = urls[0]
	}
	return in, nil
}

// falTest checks the key with the platform pricing API (and that the model
// exists) — nothing is generated. Behind an unknown host (a proxy) the queue
// status of a made-up request is asked instead.
func falTest(ctx context.Context, ep *endpoint) (string, error) {
	platform := falPlatformFor(ep.base)
	if platform == "" {
		res, err := ep.send(ctx, request{method: http.MethodGet, url: "/" + falApp(ep.model) + "/requests/00000000-0000-0000-0000-000000000000/status", timeout: testTimeout})
		if err != nil {
			return "", err
		}
		if res.status == http.StatusUnauthorized || res.status == http.StatusForbidden {
			return "", ep.httpError(res)
		}
		return "", nil
	}
	res, err := ep.call(ctx, request{method: http.MethodGet, url: platform + "/models/pricing?endpoint_id=" + url.QueryEscape(ep.model), timeout: testTimeout})
	if err != nil {
		return "", err
	}
	var p struct {
		Prices []struct {
			EndpointID string  `json:"endpoint_id"`
			UnitPrice  float64 `json:"unit_price"`
			Unit       string  `json:"unit"`
			Currency   string  `json:"currency"`
		} `json:"prices"`
	}
	if json.Unmarshal(res.body, &p) != nil || len(p.Prices) == 0 {
		return fmt.Sprintf("модель «%s» не знайдена в каталозі fal.ai — перевір назву", ep.model), nil
	}
	pr := p.Prices[0]
	cur := pr.Currency
	if cur == "" || strings.EqualFold(cur, "USD") {
		cur = "$"
	}
	return fmt.Sprintf("ціна %s%.3g за %s", cur, pr.UnitPrice, unitUA(pr.Unit)), nil
}

// unitUA names a billing unit in Ukrainian.
func unitUA(u string) string {
	switch strings.ToLower(strings.TrimSpace(u)) {
	case "second", "seconds", "video_second", "video_seconds":
		return "секунду відео"
	case "image", "images":
		return "зображення"
	case "video", "videos":
		return "відео"
	case "megapixel", "megapixels":
		return "мегапіксель"
	case "request", "requests", "call", "calls":
		return "запит"
	case "":
		return "одиницю"
	}
	return u
}

type falFile struct {
	URL string `json:"url"`
}

// falImageURL / falVideoURL pick the output file of an image / video model.
func falImageURL(out json.RawMessage) string {
	var o struct {
		Images []falFile `json:"images"`
		Image  *falFile  `json:"image"`
	}
	if json.Unmarshal(out, &o) == nil {
		for _, f := range o.Images {
			if f.URL != "" {
				return f.URL
			}
		}
		if o.Image != nil && o.Image.URL != "" {
			return o.Image.URL
		}
	}
	return ""
}

func falVideoURL(out json.RawMessage) string {
	var o struct {
		Video  *falFile  `json:"video"`
		Videos []falFile `json:"videos"`
	}
	if json.Unmarshal(out, &o) == nil {
		if o.Video != nil && o.Video.URL != "" {
			return o.Video.URL
		}
		for _, f := range o.Videos {
			if f.URL != "" {
				return f.URL
			}
		}
	}
	return ""
}
