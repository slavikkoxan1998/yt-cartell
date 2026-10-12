package llm

// Vision: a prompt with pictures (Request.Images).
//
//	openai → the one user message carries content parts: the text, then each
//	         picture as {"type":"image_url","image_url":{"url":"data:…;base64,…"}}
//	google → generateContent with inlineData parts, through the Gemini key pool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gemini"
)

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type partsMessage struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

type partsRequest struct {
	Model string `json:"model"`
	// Temperature is nil (not sent) unless the model accepts it.
	Temperature *float64       `json:"temperature,omitempty"`
	Stream      bool           `json:"stream"`
	Messages    []partsMessage `json:"messages"`
}

func dataURL(im Image) string {
	mime := strings.TrimSpace(im.MIME)
	if mime == "" {
		mime = http.DetectContentType(im.Data)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(im.Data)
}

// visionRequest is the OpenAI-compatible chat request with pictures.
func visionRequest(model string, r Request, jsonMode bool) partsRequest {
	parts := []contentPart{{Type: "text", Text: userMessage(r.System, r.Prompt, jsonMode)}}
	for _, im := range r.Images {
		parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{URL: dataURL(im)}})
	}
	return partsRequest{Model: model, Temperature: temperatureFor(model, r), Messages: []partsMessage{{Role: "user", Content: parts}}}
}

// googleVision is generateContent with inline pictures, load-balanced over
// the Google keys (a quota failure moves to the next key).
func (c *Client) googleVision(ctx context.Context, cfg config.Config, r Request, jsonMode bool) (string, error) {
	if c.gc == nil {
		return "", errors.New("Google-клієнт не налаштовано")
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	model := strings.TrimPrefix(strings.TrimSpace(cfg.Google.StoryModel), "models/")
	parts := []map[string]any{{"text": r.Prompt}}
	for _, im := range r.Images {
		mime := strings.TrimSpace(im.MIME)
		if mime == "" {
			mime = http.DetectContentType(im.Data)
		}
		parts = append(parts, map[string]any{"inlineData": map[string]string{"mimeType": mime, "data": base64.StdEncoding.EncodeToString(im.Data)}})
	}
	gc := map[string]any{}
	if gemini.AcceptsTemperature(model) { // Gemini 3+: "Strip temperature, top_p, and top_k"
		gc["temperature"] = r.Temperature
	}
	if jsonMode {
		gc["responseMimeType"] = "application/json"
	}
	req := map[string]any{"contents": []any{map[string]any{"role": "user", "parts": parts}}}
	if len(gc) > 0 {
		req["generationConfig"] = gc
	}
	if s := strings.TrimSpace(r.System); s != "" {
		req["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": s}}}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	u := googleBase + "/models/" + url.PathEscape(model) + ":generateContent"
	label := "gemini:" + model
	var out string
	err = c.gc.Pool().Do(ctx, func(key string) error {
		actx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		hr, err := http.NewRequestWithContext(actx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return err
		}
		hr.Header.Set("Content-Type", "application/json")
		hr.Header.Set("x-goog-api-key", key)
		res, err := c.hc.Do(hr)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%s: немає з'єднання з Google (%v)", label, err)
		}
		raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
		res.Body.Close()
		if err != nil {
			return err
		}
		if res.StatusCode < 200 || res.StatusCode > 299 {
			return &gemini.APIError{Label: label, Status: res.StatusCode, Body: string(raw)}
		}
		var resp struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return fmt.Errorf("%s: незрозуміла відповідь Google: %v", label, err)
		}
		var b strings.Builder
		if len(resp.Candidates) > 0 {
			for _, p := range resp.Candidates[0].Content.Parts {
				if !p.Thought {
					b.WriteString(p.Text)
				}
			}
		}
		if strings.TrimSpace(b.String()) == "" {
			return fmt.Errorf("%s повернув порожню відповідь: %s", label, clip(string(raw), 200))
		}
		out = b.String()
		return nil
	})
	if err != nil {
		return "", err
	}
	return out, nil
}
