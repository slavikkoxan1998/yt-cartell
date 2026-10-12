package providers

// OpenAI Images API (and any OpenAI-compatible images endpoint):
//
//	POST {base}/images/generations   JSON {model, prompt, n, size}        → data[].b64_json | data[].url
//	POST {base}/images/edits         multipart image[]… + prompt (refs)   → data[].b64_json | data[].url
//	GET  {base}/models               key check (nothing is generated)
//
// Auth: "Authorization: Bearer <key>". GPT Image models always answer with
// base64 and reject response_format (never sent); compatible servers may
// answer with a URL (or a data: URL) instead — all are handled.
// Docs: https://developers.openai.com/api/reference/resources/images ·
// https://developers.openai.com/api/docs/guides/image-generation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"

	"ytcartell/internal/gen"
)

// openaiFlexible: GPT Image 2 and later take any WIDTHxHEIGHT (multiples of
// 16, ratio 1:3–3:1, 0.65–8.3 MP); GPT Image 1.x and unknown models of
// compatible servers get the classic sizes.
func openaiFlexible(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "gpt-image-") && !strings.HasPrefix(m, "gpt-image-1")
}

// openaiSize maps an aspect ratio to a size: exact ratios on GPT Image 2+
// (16:9 → 1536x864), else the classic 1536x1024 / 1024x1536 / 1024x1024
// (16:9 frames then come out 3:2). Unknown aspect → "auto".
func openaiSize(model, aspect string) string {
	if openaiFlexible(model) {
		switch aspect {
		case "16:9":
			return "1536x864"
		case "9:16":
			return "864x1536"
		case "4:3":
			return "1024x768"
		case "3:4":
			return "768x1024"
		case "3:2":
			return "1536x1024"
		case "2:3":
			return "1024x1536"
		case "1:1":
			return "1024x1024"
		}
		return "auto"
	}
	switch aspect {
	case "16:9", "4:3", "3:2":
		return "1536x1024"
	case "9:16", "3:4", "2:3":
		return "1024x1536"
	case "1:1":
		return "1024x1024"
	}
	return "auto"
}

// openaiFidelity: models that take input_fidelity ("high" keeps faces and
// details of the references): GPT Image 1 / 1.5 (not mini; the guide says
// to omit it from GPT Image 2).
func openaiFidelity(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gpt-image-1") && !strings.Contains(m, "mini")
}

type openaiImages struct {
	Data []struct {
		B64 string `json:"b64_json"`
		URL string `json:"url"`
	} `json:"data"`
	OutputFormat string `json:"output_format"`
}

func openaiImage(ctx context.Context, ep *endpoint, r gen.ImageRequest) ([]byte, string, error) {
	caps := ImageCapsFor(PresetOpenAI, ep.model)
	refs := usableRefs(r.Refs, caps.MaxRefs)
	size := openaiSize(ep.model, r.Aspect)
	prompt := r.Prompt + refLegend(refs)
	var rq request
	if len(refs) == 0 {
		body := map[string]any{"model": ep.model, "prompt": prompt, "n": 1, "size": size}
		b, err := json.Marshal(body)
		if err != nil {
			return nil, "", err
		}
		rq = request{method: http.MethodPost, url: "/images/generations", body: b, timeout: submitTimeout, create: true}
	} else {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fields := [][2]string{{"model", ep.model}, {"prompt", prompt}, {"n", "1"}, {"size", size}}
		if openaiFidelity(ep.model) {
			fields = append(fields, [2]string{"input_fidelity", "high"})
		}
		for _, f := range fields {
			if err := mw.WriteField(f[0], f[1]); err != nil {
				return nil, "", err
			}
		}
		field := "image[]"
		if len(refs) == 1 {
			field = "image" // the classic single-image form every compatible server knows
		}
		for i, ref := range refs {
			mt := imageMIMEOf(ref)
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="ref-%d.%s"`, field, i+1, imageExt(mt)))
			h.Set("Content-Type", mt)
			w, err := mw.CreatePart(h)
			if err != nil {
				return nil, "", err
			}
			if _, err := w.Write(ref.Data); err != nil {
				return nil, "", err
			}
		}
		if err := mw.Close(); err != nil {
			return nil, "", err
		}
		rq = request{method: http.MethodPost, url: "/images/edits", body: buf.Bytes(), ctype: mw.FormDataContentType(), timeout: submitTimeout, create: true}
	}
	res, err := ep.call(ctx, rq)
	if err != nil {
		return nil, "", err
	}
	var out openaiImages
	if err := json.Unmarshal(res.body, &out); err != nil {
		return nil, "", ep.errf(0, false, "%s: незрозуміла відповідь сервера: %s", ep.name, clip(string(res.body), 200))
	}
	for _, d := range out.Data {
		switch {
		case d.B64 != "":
			data, err := decodeB64(d.B64)
			if err != nil || len(data) == 0 {
				return nil, "", ep.errf(0, false, "%s: некоректні дані зображення", ep.name)
			}
			return data, resultMIME("image/"+strings.TrimSpace(out.OutputFormat), data), nil
		case d.URL != "":
			data, ctype, err := ep.download(ctx, d.URL, maxImageBytes)
			if err != nil {
				return nil, "", err
			}
			return data, resultMIME(ctype, data), nil
		}
	}
	return nil, "", ep.errf(0, false, "%s: сервер не повернув зображення: %s", ep.name, clip(string(res.body), 200))
}

// openaiTest lists the models (GET /models): the key is checked, nothing is
// generated. The note says whether the configured model is on the list.
func openaiTest(ctx context.Context, ep *endpoint) (string, error) {
	res, err := ep.call(ctx, request{method: http.MethodGet, url: "/models", timeout: testTimeout})
	if err != nil {
		if statusOf(err) == http.StatusNotFound {
			return "", ep.errf(404, false, "%s: за адресою %s немає /models — перевір адресу API (часто бракує /v1)", ep.name, ep.base)
		}
		return "", err
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(res.body, &list) != nil || len(list.Data) == 0 {
		return "", nil
	}
	for _, m := range list.Data {
		if m.ID == ep.model {
			return "", nil
		}
	}
	return fmt.Sprintf("модель «%s» не знайдена серед %d доступних — перевір назву", ep.model, len(list.Data)), nil
}
