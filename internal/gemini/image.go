package gemini

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"ytcartell/internal/gen"
)

var extRe = regexp.MustCompile(`\.\w+$`)

// Image generates one still (port of imagegen.ts viaGoogle). Gemini has no
// @tag binding, so named refs are resolved into a labelled legend appended to
// the prompt: "Reference images, in order: image 1 = @tag, …".
func (c *Client) Image(ctx context.Context, r gen.ImageRequest) ([]byte, string, error) {
	cfg := c.sync()
	if strings.TrimSpace(r.Prompt) == "" {
		return nil, "", errors.New("Gemini image: порожній промпт")
	}
	model := ImageModelFor(r.UIModel, cfg.Google.ImageModel)
	images, legend := refParts(r.Refs)
	gc := &genConfig{ResponseModalities: []string{"TEXT", "IMAGE"}}
	if AcceptsTemperature(model) { // older image models (2.5); Gemini 3 wants none
		temp := 1.0
		gc.Temperature = &temp
	}
	if a := strings.TrimSpace(r.Aspect); a != "" {
		gc.ImageConfig = &imageConfig{AspectRatio: a}
	}
	req := genRequest{
		Contents:         []content{{Role: "user", Parts: append([]part{{Text: r.Prompt + legend}}, images...)}},
		GenerationConfig: gc,
	}
	resp, err := c.generateContent(ctx, model, req)
	if err != nil {
		return nil, "", err
	}
	inline := resp.inline()
	if inline == nil {
		return nil, "", fmt.Errorf("Google не повернув зображення (%s) — можливо, запит заблоковано фільтром%s", model, resp.why())
	}
	data, err := decodeB64(inline.Data)
	if err != nil || len(data) == 0 {
		return nil, "", fmt.Errorf("Gemini image (%s): некоректні дані зображення", model)
	}
	mime := inline.MimeType
	if mime == "" {
		mime = inline.MimeType2
	}
	if mime == "" || !strings.HasPrefix(mime, "image/") {
		mime = http.DetectContentType(data)
	}
	return data, mime, nil
}

// maxImageRefs: Gemini 3 image models mix up to 14 reference images
// (https://ai.google.dev/gemini-api/docs/image-generation, "Use up to 14
// reference images"); the refs come in priority order, the rest is dropped.
const maxImageRefs = 14

// refParts turns reference images (at most maxImageRefs) into inlineData parts
// plus the legend that names them by position ("image N = @tag", extension
// stripped).
func refParts(refs []gen.RefImage) (parts []part, legend string) {
	var named []string
	for _, ref := range refs {
		if len(ref.Data) == 0 {
			continue
		}
		if len(parts) == maxImageRefs {
			break
		}
		mime := strings.TrimSpace(ref.MIME)
		if mime == "" {
			mime = http.DetectContentType(ref.Data)
		}
		parts = append(parts, part{InlineData: &blob{MimeType: mime, Data: base64.StdEncoding.EncodeToString(ref.Data)}})
		if n := strings.TrimPrefix(strings.TrimSpace(ref.Name), "@"); n != "" {
			named = append(named, fmt.Sprintf("image %d = @%s", len(parts), extRe.ReplaceAllString(n, "")))
		}
	}
	if len(named) > 0 {
		legend = "\n\nReference images, in order: " + strings.Join(named, ", ") +
			". Keep each referenced character/place looking exactly like its reference image."
	}
	return parts, legend
}
