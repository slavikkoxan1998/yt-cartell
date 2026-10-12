package studio

// «Налаштувати з документації (ШІ)»: the user gives the docs of an image or
// video API (a link or pasted text) and the text model drafts the universal
// preset's spec (providers.Spec). The docs are read by providers.FetchDocs
// (an OpenAPI document or llms.txt when the page links one), the model gets
// providers.SpecPrompt, the reply is checked with providers.ParseSpec and,
// when it fails, sent back once with the problems. Nothing is saved: the
// Settings screen shows the draft in the spec editor for review. No key of
// the user's settings ever reaches the model — every configured secret is
// masked out of the docs text, and the prompt names only the model and the
// base URL fields.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/llm"
	"ytcartell/internal/providers"
)

// CustomSpecSuggestion is the text model's draft of an own API's spec.
type CustomSpecSuggestion struct {
	Spec    string // laid out JSON (providers.FormatSpec)
	BaseURL string // the API root named by the docs ("" = not found)
	Model   string // a model id from the docs ("" = none needed / not found)
	// Notes: what the model could not determine (Ukrainian, ≤ 6), plus what
	// was read; Problems: validation problems left after one repair round.
	Notes    []string
	Problems []string
	Source   string // where the docs came from ("" = pasted text)
}

const specSuggestTimeout = 240 * time.Second

var reDocsURL = regexp.MustCompile(`^https?://\S+$`)

// SuggestCustomSpec drafts the universal spec for images (video false) or
// video from docs — a URL or the documentation text itself. progress
// (optional, any goroutine) gets short Ukrainian steps. The result is not
// saved.
func (e *Engine) SuggestCustomSpec(ctx context.Context, video bool, docs string, progress func(string)) (CustomSpecSuggestion, error) {
	kind := providers.KindImage
	if video {
		kind = providers.KindVideo
	}
	return e.SuggestSpec(ctx, kind, docs, progress)
}

// SuggestSpec is SuggestCustomSpec for any kind of API: images, video, a
// voice-over (TTS, «Власний сервер» of Озвучка) or speech recognition (STT).
func (e *Engine) SuggestSpec(ctx context.Context, kind providers.SpecKind, docs string, progress func(string)) (CustomSpecSuggestion, error) {
	video := kind == providers.KindVideo
	if progress == nil {
		progress = func(string) {}
	}
	docs = strings.TrimSpace(docs)
	if docs == "" {
		return CustomSpecSuggestion{}, errors.New("встав посилання на документацію API або сам текст документації")
	}
	var d providers.DocsText
	var err error
	if reDocsURL.MatchString(docs) {
		d, err = providers.FetchDocs(ctx, docs, progress)
		if err != nil {
			if ctx.Err() != nil {
				return CustomSpecSuggestion{}, ctx.Err()
			}
			return CustomSpecSuggestion{}, fmt.Errorf("Не вдалося прочитати документацію: %w", err)
		}
	} else {
		d = providers.PrepareDocs(docs)
	}
	if len([]rune(strings.TrimSpace(d.Text))) < 80 {
		return CustomSpecSuggestion{}, errors.New("у документації замало тексту — встав більше: як запустити генерацію, що повертає сервер, як дізнатися статус")
	}
	if d.Note != "" {
		progress(d.Note)
	}
	cfg := e.cfg.Get()
	text := maskSecrets(d.Text, configSecrets(cfg))
	model, base := cfg.Custom.ImageModel, cfg.Custom.ImageBaseURL
	switch {
	case video:
		model, base = cfg.Custom.VideoModel, cfg.Custom.VideoBaseURL
	case kind == providers.KindTTS:
		model, base = cfg.TTS.Of("custom").Model, cfg.TTS.Of("custom").BaseURL
	case kind == providers.KindSTT:
		model, base = cfg.STT.Model, cfg.STT.BaseURL
	}
	var p strings.Builder
	if d.Source != "" {
		fmt.Fprintf(&p, "API documentation (from %s, %s):\n\n", d.Source, d.Kind)
	} else {
		p.WriteString("API documentation (pasted by the user):\n\n")
	}
	p.WriteString(text)
	if model = strings.TrimSpace(model); model != "" {
		fmt.Fprintf(&p, "\n\nThe user's model field says %q — keep it when the docs list it.", model)
	}
	if base = strings.TrimSpace(base); base != "" {
		fmt.Fprintf(&p, "\nThe user's base URL field says %q.", maskSecrets(base, configSecrets(cfg)))
	}
	prompt := p.String()
	system := providers.SpecPromptFor(kind)

	progress("ШІ складає опис API…")
	raw, err := e.text.Text(ctx, llm.Request{System: system, Prompt: prompt, Temperature: 0.2, Timeout: specSuggestTimeout})
	if err != nil {
		return CustomSpecSuggestion{}, err
	}
	s, err := parseSpecReply(raw)
	if err != nil {
		progress("Відповідь не схожа на JSON — прошу ще раз…")
		raw, err = e.text.Text(ctx, llm.Request{System: system + "\n\nIMPORTANT: reply with ONE valid JSON object only — no prose, no code fences.",
			Prompt: prompt, Temperature: 0.1, Timeout: specSuggestTimeout})
		if err != nil {
			return CustomSpecSuggestion{}, err
		}
		if s, err = parseSpecReply(raw); err != nil {
			return CustomSpecSuggestion{}, err
		}
	}
	progress("Перевіряю опис…")
	if probs := specProblemsFor(s.Spec, kind); len(probs) > 0 {
		progress("Виправляю помилки в описі…")
		fix := prompt + "\n\nYour previous spec:\n" + s.Spec + "\n\nIt failed validation (messages in Ukrainian): " + strings.Join(probs, "; ") +
			"\nReply again with the whole corrected JSON object ({\"spec\": …, \"baseUrl\": …, \"model\": …, \"notes\": […]}) only."
		raw, err = e.text.Text(ctx, llm.Request{System: system, Prompt: fix, Temperature: 0.1, Timeout: specSuggestTimeout})
		if err != nil {
			return CustomSpecSuggestion{}, err
		}
		if s2, err := parseSpecReply(raw); err == nil {
			if s2.BaseURL == "" {
				s2.BaseURL = s.BaseURL
			}
			if s2.Model == "" {
				s2.Model = s.Model
			}
			s = s2
		}
		s.Problems = specProblemsFor(s.Spec, kind)
	}
	s.Source = d.Source
	if d.Note != "" {
		s.Notes = append([]string{d.Note}, s.Notes...)
	}
	return s, nil
}

// specProblemsFor are the validation problems of a spec of a kind.
func specProblemsFor(spec string, kind providers.SpecKind) []string {
	_, err := providers.ParseSpecFor(spec, kind)
	if err == nil {
		return nil
	}
	var se *providers.SpecError
	if errors.As(err, &se) {
		return se.Problems
	}
	return []string{err.Error()}
}

// parseSpecReply reads {"spec": {…}, "baseUrl", "model", "notes"} (or a bare
// spec object), tolerating fences, prose and the usual JSON slips.
func parseSpecReply(raw string) (CustomSpecSuggestion, error) {
	for _, cand := range []string{raw, repairJSON(raw)} {
		b, err := llm.ExtractJSON(cand)
		if err != nil {
			continue
		}
		var top map[string]json.RawMessage
		if json.Unmarshal(b, &top) != nil {
			continue
		}
		specRaw, ok := top["spec"]
		if !ok {
			if _, bare := top["submit"]; !bare {
				continue
			}
			specRaw = b
		}
		if len(specRaw) == 0 || specRaw[0] != '{' {
			var s string // a spec sent as a JSON string
			if json.Unmarshal(specRaw, &s) != nil || !strings.HasPrefix(strings.TrimSpace(s), "{") {
				continue
			}
			specRaw = json.RawMessage(s)
		}
		out := CustomSpecSuggestion{Spec: providers.FormatSpec(specRaw)}
		var meta struct {
			BaseURL string `json:"baseUrl"`
			Model   string `json:"model"`
			Notes   any    `json:"notes"`
		}
		_ = json.Unmarshal(b, &meta)
		out.BaseURL = strings.TrimRight(strings.TrimSpace(meta.BaseURL), "/")
		if !strings.HasPrefix(out.BaseURL, "http://") && !strings.HasPrefix(out.BaseURL, "https://") {
			out.BaseURL = ""
		}
		out.Model = clipRunes(strings.TrimSpace(meta.Model), 200)
		out.Notes = parseReasons(meta.Notes)
		if len(out.Notes) > 6 {
			out.Notes = out.Notes[:6]
		}
		return out, nil
	}
	return CustomSpecSuggestion{}, errors.New("модель не змогла скласти опис (відповідь не схожа на JSON) — спробуй ще раз або встав іншу частину документації")
}

// configSecrets are every key and token of the settings (masked out of
// whatever goes to the text model).
func configSecrets(c config.Config) []string {
	list := []string{c.Custom.ImageKey, c.Custom.VideoKey, c.Text.APIKey, c.Glabs.APIKey, c.TTS.APIKey, c.STT.APIKey, c.STT.AssemblyKey, c.Telegram.BotToken}
	list = append(list, c.Google.Keys...)
	list = append(list, c.TTS.Keys...)
	for _, p := range c.TTS.Providers {
		list = append(list, p.APIKey)
		list = append(list, p.Keys...)
	}
	return list
}

// maskSecrets replaces each secret (6+ characters) in s.
func maskSecrets(s string, secrets []string) string {
	for _, k := range secrets {
		if k = strings.TrimSpace(k); len(k) >= 6 {
			s = strings.ReplaceAll(s, k, "•••")
		}
	}
	return s
}
