package providers

// Replicate HTTP API (https://replicate.com/docs/reference/http):
//
//	POST {base}/models/{owner}/{name}/predictions  {"input": {…}}       official models (latest version)
//	POST {base}/predictions  {"version": "<id>", "input": {…}}          "owner/name:version" model ids
//	GET  {base}/predictions/{id}  {status starting|processing|succeeded|failed|canceled|aborted, output, error, logs}
//	POST {base}/predictions/{id}/cancel                                  best effort when the caller gives up
//	GET  {base}/models/{owner}/{name}  latest_version.openapi_schema     the model's input schema (cached)
//	POST {base}/files  multipart "content"                               references larger than 256 KB
//	GET  {base}/account                                                  key check (nothing is generated)
//
// Auth: "Authorization: Bearer <token>". Small images go in as data URIs,
// larger ones through the Files API; outputs are URLs (a string or a list)
// on replicate.delivery — downloaded without the token. Status URLs are
// always built from the configured base, so the token never travels
// anywhere else. Images ask for "Prefer: wait" (the create call returns the
// finished prediction within 60 s, usually without polling).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ytcartell/internal/gen"
)

// Inline (data URI) references up to this size; larger ones are uploaded.
const (
	replicateInlineMax  = 256 << 10
	replicateDataURIMax = 8 << 20 // when the upload fails
)

type replicatePrediction struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Output json.RawMessage `json:"output"`
	Error  json.RawMessage `json:"error"`
	Logs   string          `json:"logs"`
}

// replicateCreate is the create call for a model id: "owner/name" → the
// official-model endpoint, "owner/name:version" or a bare version id → the
// versioned one.
func replicateCreate(model string, input map[string]any) (string, []byte, error) {
	model = strings.TrimSpace(model)
	body := map[string]any{"input": input}
	path := ""
	switch owner, rest, ok := strings.Cut(model, "/"); {
	case strings.Contains(model, ":"):
		_, version, _ := strings.Cut(model, ":")
		body["version"] = version
		path = "/predictions"
	case ok && owner != "" && rest != "" && !strings.Contains(rest, "/"):
		path = "/models/" + url.PathEscape(owner) + "/" + url.PathEscape(rest) + "/predictions"
	default: // a bare version id
		body["version"] = model
		path = "/predictions"
	}
	b, err := json.Marshal(body)
	return path, b, err
}

var rePercent = regexp.MustCompile(`(\d{1,3})%`)

// logPercent is the last "NN%" a model printed on its last log line (tqdm
// progress bars), or -1.
func logPercent(logs string) int {
	logs = strings.TrimRight(logs, "\r\n")
	if i := strings.LastIndexAny(logs, "\r\n"); i >= 0 {
		logs = logs[i+1:]
	}
	m := rePercent.FindAllStringSubmatch(logs, -1)
	if len(m) == 0 {
		return -1
	}
	n, err := strconv.Atoi(m[len(m)-1][1])
	if err != nil || n > 100 {
		return -1
	}
	return n
}

// replicateRun creates a prediction, waits for it and returns its output
// URLs. wait asks the API to hold the create call up to 60 s ("Prefer:
// wait"), which usually returns a finished image without any polling.
func replicateRun(ctx context.Context, ep *endpoint, input map[string]any, wait bool, timeout, expected time.Duration, progress gen.Progress) ([]string, error) {
	path, body, err := replicateCreate(ep.model, input)
	if err != nil {
		return nil, err
	}
	rq := request{method: http.MethodPost, url: path, body: body, timeout: submitTimeout, create: true}
	if wait {
		rq.header = map[string]string{"Prefer": "wait=60"}
	}
	res, err := ep.call(ctx, rq)
	if err != nil {
		return nil, err
	}
	var pred replicatePrediction
	if json.Unmarshal(res.body, &pred) != nil || strings.TrimSpace(pred.ID) == "" {
		return nil, ep.errf(0, false, "%s: сервер не повернув id задачі: %s", ep.name, clip(string(res.body), 200))
	}
	getURL := "/predictions/" + url.PathEscape(pred.ID)
	if !replicateFinished(pred) {
		err = ep.poll(ctx, timeout, expected, progress, func(ctx context.Context) (pollState, error) {
			res, err := ep.call(ctx, request{method: http.MethodGet, url: getURL})
			if err != nil {
				return pollState{}, err
			}
			pred = replicatePrediction{}
			if err := json.Unmarshal(res.body, &pred); err != nil {
				return pollState{}, ep.errf(0, true, "%s: незрозуміла відповідь статусу", ep.name)
			}
			switch {
			case replicateFinished(pred):
				return pollState{done: true}, nil
			case pred.Status == "starting":
				return pollState{queued: true}, nil
			}
			return pollState{pct: logPercent(pred.Logs)}, nil
		})
		if err != nil {
			if gaveUp(ctx, err) {
				ep.cancelRemote(http.MethodPost, getURL+"/cancel")
			}
			return nil, err
		}
	}
	switch pred.Status {
	case "succeeded", "processing": // processing with the output already there (Prefer: wait)
	case "canceled", "aborted":
		return nil, ep.errf(0, true, "%s: задачу скасовано на сервері — спробуй ще раз", ep.name)
	default: // failed
		return nil, ep.failed(rawText(pred.Error))
	}
	outs := outputURLs(pred.Output)
	if len(outs) == 0 {
		return nil, ep.errf(0, false, "%s: задача завершилась без результату", ep.name)
	}
	return outs, nil
}

// replicateFinished: a terminal status, or file output already there while
// the status still says processing (documented for "Prefer: wait").
func replicateFinished(p replicatePrediction) bool {
	switch p.Status {
	case "succeeded", "failed", "canceled", "aborted":
		return true
	case "processing":
		return len(outputURLs(p.Output)) > 0
	}
	return false
}

// outputURLs collects result URLs from an output that is a string, a list,
// or an object/list of objects with a url-like field.
func outputURLs(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s = strings.TrimSpace(s); s != "" {
			return []string{s}
		}
		return nil
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		var out []string
		for _, item := range list {
			out = append(out, outputURLs(item)...)
		}
		return out
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		for _, k := range []string{"url", "video", "image", "output", "file"} {
			if v, ok := obj[k]; ok {
				if out := outputURLs(v); len(out) > 0 {
					return out
				}
			}
		}
	}
	return nil
}

// ---- schemas, uploads ---------------------------------------------------------------

// replicateModelPath is the model's API path: /models/{owner}/{name} or
// /models/{owner}/{name}/versions/{version}; "" for a bare version id.
func replicateModelPath(model string) string {
	ref, version, _ := strings.Cut(strings.TrimSpace(model), ":")
	owner, name, ok := strings.Cut(ref, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return ""
	}
	p := "/models/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	if version != "" {
		p += "/versions/" + url.PathEscape(version)
	}
	return p
}

// replicateSchema is the model's input schema: live from the API (cached),
// else the built-in one for its family.
func replicateSchema(ctx context.Context, ep *endpoint, image bool) *inputSchema {
	builtin := replicateVideoSchema(ep.model)
	if image {
		builtin = replicateImageSchema(ep.model)
	}
	if path := replicateModelPath(ep.model); path != "" {
		s := cachedSchema(ctx, "replicate|"+ep.base+"|"+ep.model, func() (*inputSchema, error) {
			return replicateFetchSchema(ctx, ep, path)
		})
		if s != nil {
			return s.withBuiltin(builtin)
		}
	}
	return builtin
}

func replicateFetchSchema(ctx context.Context, ep *endpoint, path string) (*inputSchema, error) {
	res, err := ep.send(ctx, request{method: http.MethodGet, url: path, timeout: statusTimeout})
	if err != nil {
		return nil, err
	}
	if !res.ok() {
		return nil, fmt.Errorf("schema: HTTP %d", res.status)
	}
	var doc struct {
		Latest *struct {
			Schema map[string]any `json:"openapi_schema"`
		} `json:"latest_version"`
		Schema map[string]any `json:"openapi_schema"`
	}
	if err := json.Unmarshal(res.body, &doc); err != nil {
		return nil, err
	}
	schema := doc.Schema
	if doc.Latest != nil && doc.Latest.Schema != nil {
		schema = doc.Latest.Schema
	}
	comps, _ := schema["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	input, _ := schemas["Input"].(map[string]any)
	if s := parseSchema(input, schemas); s != nil {
		return s, nil
	}
	return nil, errors.New("schema: no Input")
}

// replicateInputs turns images into model inputs: data URIs up to 256 KB,
// larger ones uploaded through the Files API (data URIs when that fails).
func replicateInputs(ctx context.Context, ep *endpoint, refs []gen.RefImage) ([]string, error) {
	out := make([]string, 0, len(refs))
	for i, r := range refs {
		if len(r.Data) > replicateInlineMax {
			u, err := replicateUpload(ctx, ep, r, i)
			if err == nil {
				out = append(out, u)
				continue
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			log.Printf("[providers] Replicate: upload failed, sending the image inline: %v", err)
		}
		fitted, err := fitImage(r, replicateDataURIMax)
		if err != nil {
			return nil, ep.errf(0, false, "%s: %v", ep.name, err)
		}
		out = append(out, dataURI(fitted))
	}
	return out, nil
}

// replicateUpload stores one image with the Files API and returns its URL
// (readable by this account's predictions).
func replicateUpload(ctx context.Context, ep *endpoint, r gen.RefImage, i int) (string, error) {
	mt := imageMIMEOf(r)
	name := fmt.Sprintf("ref-%d.%s", i+1, imageExt(mt))
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="content"; filename="%s"`, name))
	h.Set("Content-Type", mt)
	w, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(r.Data); err != nil {
		return "", err
	}
	for _, f := range [][2]string{{"filename", name}, {"type", mt}} {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return "", err
		}
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	res, err := ep.call(ctx, request{method: http.MethodPost, url: "/files", body: buf.Bytes(), ctype: mw.FormDataContentType(), timeout: downloadTimeout})
	if err != nil {
		return "", err
	}
	var f struct {
		URLs struct {
			Get string `json:"get"`
		} `json:"urls"`
	}
	if json.Unmarshal(res.body, &f) != nil || f.URLs.Get == "" {
		return "", errors.New("upload: no urls.get")
	}
	return f.URLs.Get, nil
}

// ---- image / video / test -----------------------------------------------------------

func replicateImage(ctx context.Context, ep *endpoint, r gen.ImageRequest) ([]byte, string, error) {
	schema := replicateSchema(ctx, ep, true)
	refs := usableRefs(r.Refs, schema.imageCaps().MaxRefs)
	urls, err := replicateInputs(ctx, ep, refs)
	if err != nil {
		return nil, "", err
	}
	input, missing := buildImage(schema, r.Prompt, r.Aspect, refs, urls)
	if len(missing) > 0 {
		return nil, "", ep.missingErr(ep.model, missing)
	}
	outs, err := replicateRun(ctx, ep, input, true, imagePollTimeout, imageExpected, nil)
	if err != nil {
		return nil, "", err
	}
	data, ctype, err := replicateDownload(ctx, ep, outs[0], maxImageBytes)
	if err != nil {
		return nil, "", err
	}
	return data, resultMIME(ctype, data), nil
}

func replicateVideo(ctx context.Context, ep *endpoint, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	schema := replicateSchema(ctx, ep, false)
	in, err := videoInputs(ctx, ep, schema, r, replicateInputs)
	if err != nil {
		return nil, err
	}
	input, _, missing := schema.build(in)
	if len(missing) > 0 {
		return nil, ep.missingErr(ep.model, missing)
	}
	progress(5, "starting")
	outs, err := replicateRun(ctx, ep, input, false, videoPollTimeout, videoExpected, progress)
	if err != nil {
		return nil, err
	}
	pick := outs[0]
	for _, u := range outs {
		if strings.Contains(strings.ToLower(u), ".mp4") {
			pick = u
			break
		}
	}
	data, _, err := replicateDownload(ctx, ep, pick, maxVideoBytes)
	if err != nil {
		return nil, err
	}
	progress(100, "done")
	return data, nil
}

// replicateTest reads the account (GET /account) and the model: the token
// and the model id are checked, nothing is generated.
func replicateTest(ctx context.Context, ep *endpoint) (string, error) {
	res, err := ep.call(ctx, request{method: http.MethodGet, url: "/account", timeout: testTimeout})
	if err != nil {
		return "", err
	}
	note := ""
	var acc struct {
		Username string `json:"username"`
	}
	if json.Unmarshal(res.body, &acc) == nil && acc.Username != "" {
		note = "акаунт " + acc.Username
	}
	if path := replicateModelPath(ep.model); path != "" {
		if _, err := ep.call(ctx, request{method: http.MethodGet, url: path, timeout: testTimeout}); err != nil {
			if statusOf(err) == http.StatusNotFound {
				return "", ep.errf(404, false, "%s: ключ працює, але модель «%s» не знайдена — перевір назву (owner/name)", ep.name, ep.model)
			}
			return "", err
		}
	}
	return note, nil
}

// replicateDownload fetches an output file without the token (delivery
// URLs are signed); when Replicate's own file host refuses that (401/403 —
// API file URLs need it), it is asked once more with the token. The token
// never goes to other hosts.
func replicateDownload(ctx context.Context, ep *endpoint, u string, limit int64) ([]byte, string, error) {
	data, ctype, err := ep.download(ctx, u, limit)
	if st := statusOf(err); err == nil || (st != http.StatusUnauthorized && st != http.StatusForbidden) || !replicateHost(ep.abs(u), ep.base) {
		return data, ctype, err
	}
	res, err2 := ep.call(ctx, request{method: http.MethodGet, url: ep.abs(u), timeout: downloadTimeout, limit: limit, header: map[string]string{"Accept": "*/*"}})
	if err2 != nil || len(res.body) == 0 {
		return nil, "", err
	}
	return res.body, res.header.Get("Content-Type"), nil
}

// replicateHost: a URL on the API's origin or on Replicate's own domains.
func replicateHost(u, base string) bool {
	if sameOrigin(u, base) {
		return true
	}
	return replicateOwnHost(u)
}

// replicateOwnHost: an https URL on replicate.com / replicate.delivery (a
// package var so tests can point it at a mock).
var replicateOwnHost = func(u string) bool {
	h := hostOf(u)
	return strings.HasPrefix(u, "https://") && (h == "replicate.com" || strings.HasSuffix(h, ".replicate.com") ||
		h == "replicate.delivery" || strings.HasSuffix(h, ".replicate.delivery"))
}
