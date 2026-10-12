package tts

// OpenAI(-compatible): POST {base}/audio/speech (Bearer) — models
// gpt-4o-mini-tts (default on api.openai.com; takes "instructions", used here
// to name the film language; snapshot gpt-4o-mini-tts-2025-12-15), tts-1-hd,
// tts-1 (the default of compatible gateways); input ≤ 4096 characters. 13
// built-in voices, no list endpoint; tts-1 / tts-1-hd take only 9 of them
// (not ballad, verse, marin, cedar). Errors: 401 invalid key; 429 with
// error.code credit_balance_exhausted / organization_spend_limit_exceeded /
// project_spend_limit_exceeded / organization_usage_limit_exceeded (type may
// still be insufficient_quota) = spent balance vs any other 429 = rate limit;
// 503 server_is_overloaded. Docs (2026-10):
// https://developers.openai.com/api/reference/resources/audio/subresources/speech/methods/create
//
// Own server ("custom"): POST {url} {text, voice, model, language?, speed?}
// with an optional Bearer key; raw audio or JSON {audio|audioBase64|data|audioUrl|url}.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// officialOpenAI: no base set, or the OpenAI API itself.
func officialOpenAI(s settings) bool {
	return s.base == "" || strings.EqualFold(strings.TrimRight(s.base, "/"), strings.TrimRight(openaiBase, "/"))
}

// openAIModel is the default model: gpt-4o-mini-tts on OpenAI, tts-1 on a
// compatible gateway (the id every gateway knows).
func openAIModel(s settings) string {
	if officialOpenAI(s) {
		return "gpt-4o-mini-tts"
	}
	return "tts-1"
}

// openAIVoices are the built-in voices of the Speech API (no list endpoint);
// tts-1 and tts-1-hd cannot use the four newest.
func openAIVoices(model string) []Voice {
	list := []struct {
		id, gender string
		mini       bool // gpt-4o-mini-tts only
	}{
		{"alloy", "neutral", false}, {"ash", "male", false}, {"ballad", "male", true}, {"cedar", "male", true}, {"coral", "female", false},
		{"echo", "male", false}, {"fable", "male", false}, {"marin", "female", true}, {"nova", "female", false}, {"onyx", "male", false},
		{"sage", "female", false}, {"shimmer", "female", false}, {"verse", "male", true},
	}
	legacy := strings.HasPrefix(model, "tts-1")
	out := make([]Voice, 0, len(list))
	for _, v := range list {
		vo := Voice{ID: v.id, Name: strings.ToUpper(v.id[:1]) + v.id[1:], Category: "premade", Gender: v.gender, Usable: true}
		if legacy && v.mini {
			vo.Usable, vo.Locked = false, "лише з моделлю gpt-4o-mini-tts"
		}
		out = append(out, vo)
	}
	sortVoices(out, "")
	return out
}

func (c *Client) openAI(ctx context.Context, s settings, text, voice string, opt SynthOptions) ([]byte, error) {
	if len(s.keys) == 0 {
		return nil, errNoKey(s)
	}
	base := firstNonEmpty(s.base, openaiBase)
	model := firstNonEmpty(opt.Model, s.model, openAIModel(s))
	payload := map[string]any{
		"model":           model,
		"input":           text,
		"voice":           firstNonEmpty(voice, s.info.DefaultVoice, "alloy"),
		"response_format": "mp3",
		"speed":           orDefault(opt.Speed, 1, 0.25, 4),
	}
	if strings.HasPrefix(model, "gpt-4o-mini-tts") {
		if name := languageName(langCode(opt.LanguageCode)); name != "" {
			payload["instructions"] = "Speak " + strings.ToUpper(name[:1]) + name[1:] + " with a natural native pronunciation."
		}
	}
	body, _ := json.Marshal(payload)
	var out []byte
	err := c.run(ctx, s, voice, runes(text), false, func(key string) (int, error) {
		res, err := c.do(ctx, http.MethodPost, base+"/audio/speech", bearer(key), body, synthTimeout, maxAudio)
		if err != nil {
			return 0, netErr(ctx, "OpenAI", err)
		}
		if !res.ok() {
			return 0, openAIErr(res)
		}
		out, err = audio(res, "OpenAI")
		return 0, err
	})
	return out, err
}

// openAIErr classifies an OpenAI(-compatible) error {"error":{"message","type","code"}}.
func openAIErr(r reply) *apiErr {
	var j struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.body, &j)
	code := strings.ToLower(str(j.Error.Code) + " " + j.Error.Type)
	detail := firstNonEmpty(j.Error.Message, clip(string(r.body), 200))
	switch {
	case strings.Contains(code, "insufficient_quota") || strings.Contains(code, "credit_balance_exhausted") ||
		strings.Contains(code, "usage_limit_exceeded") || strings.Contains(strings.ToLower(detail), "exceeded your current quota"):
		return &apiErr{kind: failQuota, msg: "OpenAI: закінчились кошти на балансі ключа"}
	case strings.Contains(code, "spend_limit_exceeded"):
		return &apiErr{kind: failQuota, msg: "OpenAI: досягнуто ліміт витрат організації або проєкту ключа"}
	case r.status == http.StatusUnauthorized:
		return &apiErr{kind: failAuth, msg: "OpenAI: невірний API-ключ (401)"}
	case r.status == http.StatusForbidden:
		return &apiErr{kind: failSkip, msg: "OpenAI: ключ не має доступу до цієї моделі або регіону (403): " + clip(detail, 160)}
	case r.status == http.StatusNotFound:
		return fatal("OpenAI: модель або адресу не знайдено (404) — перевір модель і адресу в Налаштуваннях")
	}
	return httpErr("OpenAI", r, detail)
}

// openAIProbe: GET {base}/models.
func (c *Client) openAIProbe(ctx context.Context, s settings, key string) probeResult {
	out := probeResult{used: -1}
	res, err := c.do(ctx, http.MethodGet, firstNonEmpty(s.base, openaiBase)+"/models", bearer(key), nil, listTimeout, 4<<20)
	if err != nil {
		out.err = kindOf(netErr(ctx, "OpenAI", err))
		return out
	}
	if res.status == http.StatusNotFound && !officialOpenAI(s) {
		out.info = "сервер відповідає (список моделей недоступний)"
		return out
	}
	if !res.ok() {
		out.err = openAIErr(res)
		return out
	}
	out.info = "модель " + firstNonEmpty(s.model, openAIModel(s))
	return out
}

// ---- own server ---------------------------------------------------------------------------

func (c *Client) custom(ctx context.Context, s settings, text, voice string, opt SynthOptions) ([]byte, error) {
	if spec := c.ttsSpec(); spec != "" { // universal.go: any API described in JSON
		return c.customSpec(ctx, s, spec, text, voice, opt)
	}
	if s.base == "" {
		return nil, errors.New("Для custom TTS вкажи URL у Налаштуваннях")
	}
	payload := map[string]any{"text": text, "voice": voice, "model": firstNonEmpty(opt.Model, s.model)}
	if lang := langCode(opt.LanguageCode); lang != "" {
		payload["language"] = lang
	}
	if opt.Speed > 0 {
		payload["speed"] = opt.Speed
	}
	body, _ := json.Marshal(payload)
	var out []byte
	err := c.run(ctx, s, voice, runes(text), false, func(key string) (int, error) {
		hdr := map[string]string{}
		if key != "" {
			hdr = bearer(key)
		}
		res, err := c.do(ctx, http.MethodPost, s.base, hdr, body, synthTimeout, maxAudio)
		if err != nil {
			return 0, netErr(ctx, "TTS(custom)", err)
		}
		if !res.ok() {
			return 0, httpErr("TTS(custom)", res, "")
		}
		out, err = c.customAudio(ctx, s, res)
		return 0, err
	})
	return out, err
}

// customAudio takes the audio out of an own server's reply.
func (c *Client) customAudio(ctx context.Context, s settings, res reply) ([]byte, error) {
	if !strings.Contains(strings.ToLower(res.ctype), "application/json") {
		return audio(res, "TTS(custom)")
	}
	var j map[string]any
	if err := json.Unmarshal(res.body, &j); err != nil {
		return nil, fmt.Errorf("custom TTS: незрозуміла відповідь: %s", clip(string(res.body), 200))
	}
	if b64 := firstNonEmpty(str(j["audio"]), str(j["audioBase64"]), str(j["data"])); b64 != "" {
		b, err := decodeB64(reDataPrefix.ReplaceAllString(b64, ""))
		if err != nil {
			return nil, errors.New("custom TTS: аудіо у відповіді пошкоджене (base64)")
		}
		return audio(reply{body: b}, "TTS(custom)")
	}
	if link := firstNonEmpty(str(j["audioUrl"]), str(j["url"])); link != "" {
		if ref, err := url.Parse(link); err == nil {
			if base, err := url.Parse(s.base); err == nil {
				link = base.ResolveReference(ref).String()
			}
		}
		res, err := c.do(ctx, http.MethodGet, link, nil, nil, synthTimeout, maxAudio)
		if err != nil {
			return nil, netErr(ctx, "TTS(custom)", err)
		}
		if !res.ok() {
			return nil, fmt.Errorf("TTS(custom): не вдалося завантажити аудіо (%d)", res.status)
		}
		return audio(res, "TTS(custom)")
	}
	return nil, errors.New("custom TTS: аудіо не знайдено у відповіді")
}

// customProbe: GET {url} — any answer but 401/403/5xx means the server is up
// (an own server has no standard check; nothing is synthesized).
func (c *Client) customProbe(ctx context.Context, s settings, key string) probeResult {
	if spec := c.ttsSpec(); spec != "" {
		return c.customSpecProbe(ctx, s, spec, key)
	}
	out := probeResult{used: -1}
	hdr := map[string]string{}
	if key != "" {
		hdr = bearer(key)
	}
	res, err := c.do(ctx, http.MethodGet, s.base, hdr, nil, listTimeout, 1<<20)
	if err != nil {
		out.err = kindOf(netErr(ctx, "TTS(custom)", err))
		return out
	}
	switch {
	case res.status == http.StatusUnauthorized || res.status == http.StatusForbidden:
		out.err = &apiErr{kind: failAuth, msg: fmt.Sprintf("TTS(custom): сервер не прийняв ключ (%d)", res.status)}
	case res.status >= 500:
		out.err = &apiErr{kind: failTransient, msg: fmt.Sprintf("TTS(custom): сервер відповів помилкою %d", res.status)}
	default:
		out.info = "сервер відповідає"
	}
	return out
}
