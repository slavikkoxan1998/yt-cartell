package tts

// Cartesia (Sonic 3.6): POST /tts/bytes, GET /voices, GET /voices/{id}
// (voicebyid.go; checked 2026-10-02 against /api-reference/voices/get) — Authorization: Bearer
// sk_car_…, Cartesia-Version 2026-08-14 (the latest; checked 2026-10 against
// docs.cartesia.ai/api-reference/tts/bytes, /api-reference/voices/list,
// /use-the-api/api-errors). Body: model_id (sonic-3.6 default, sonic-3.5,
// sonic-3, sonic-latest; sonic-2 / sonic-turbo are sunset on 2026-10-20),
// transcript, voice {id} (no "mode" any more), output_format, language,
// generation_config.speed 0.6–1.5. Voices: {data, has_more, next_page};
// gender masculine | feminine | gender_neutral, accents[].locale (language is
// deprecated), is_owner. Since 2026-03-01 errors are JSON {error_code, title,
// message, request_id}: quota_exceeded → exhausted; plan_upgrade_required (a
// feature or voice of a higher plan) → next key; concurrency_limited (429) →
// rest; voice_not_found → next key (a voice of another account);
// language_not_supported / voice_model_mismatch / model_not_found /
// model_sunsetted → the request. Older versions answered "Title: Message"
// text, e.g. 402 "credit limit reached" (Cartelsia's IsQuotaExceeded) — still
// recognised. Plans: Free 20K credits/month (no commercial use, no cloning,
// 2 concurrent), Pro 3, Startup 5, Scale 15; a normal key cannot read its
// credits (/usage needs an admin key), so the check lists one voice.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const cartesiaVersion = "2026-08-14"

func cartesiaHeaders(key string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + key, "Cartesia-Version": cartesiaVersion}
}

func (c *Client) cartesia(ctx context.Context, s settings, text, voice string, opt SynthOptions) ([]byte, error) {
	if len(s.keys) == 0 {
		return nil, errNoKey(s)
	}
	if voice == "" {
		return nil, fmt.Errorf("Не вибрано голос для озвучки")
	}
	payload := map[string]any{
		"model_id":      firstNonEmpty(opt.Model, s.model, s.info.DefaultModel),
		"transcript":    text,
		"voice":         map[string]any{"id": voice},
		"output_format": map[string]any{"container": "mp3", "sample_rate": 44100, "bit_rate": 128000},
	}
	// The film language; on "auto" the writing system decides, and an
	// unsupported guess is left to the model rather than refused.
	if code := langCode(opt.LanguageCode); code != "" {
		payload["language"] = firstNonEmpty(cartesiaLangs[code], code)
	} else if l := cartesiaLangs[guessLanguage(text)]; l != "" {
		payload["language"] = l
	}
	if opt.Speed > 0 && opt.Speed != 1 {
		payload["generation_config"] = map[string]any{"speed": orDefault(opt.Speed, 1, 0.6, 1.5)}
	}
	body, _ := json.Marshal(payload)
	var out []byte
	err := c.run(ctx, s, voice, runes(text), false, func(key string) (int, error) {
		res, err := c.do(ctx, http.MethodPost, cartesiaBase+"/tts/bytes", cartesiaHeaders(key), body, synthTimeout, maxAudio)
		if err != nil {
			return 0, netErr(ctx, "Cartesia", err)
		}
		if !res.ok() {
			return 0, cartesiaErr(res, str(payload["model_id"]))
		}
		out, err = audio(res, "Cartesia")
		return 0, err
	})
	return out, err
}

// cartesiaErr classifies a failed Cartesia reply.
func cartesiaErr(r reply, model string) *apiErr {
	var j struct {
		ErrorCode string `json:"error_code"`
		Title     string `json:"title"`
		Message   string `json:"message"`
		Error     string `json:"error"`
	}
	_ = json.Unmarshal(r.body, &j)
	code := strings.ToLower(j.ErrorCode)
	detail := clip(firstNonEmpty(j.Message, j.Title, j.Error, string(r.body)), 200)
	low := strings.ToLower(string(r.body))
	switch {
	case code == "plan_upgrade_required":
		return &apiErr{kind: failSkip, voice: true, msg: "Cartesia: цей голос або можливість потребує вищого тарифу — вибери інший голос"}
	case code == "quota_exceeded" || r.status == http.StatusPaymentRequired ||
		strings.Contains(low, "credit limit") || strings.Contains(low, "credits limit"):
		return &apiErr{kind: failQuota, msg: "Cartesia: закінчились кредити на тарифі"}
	case r.status == http.StatusUnauthorized || code == "unauthorized":
		return &apiErr{kind: failAuth, msg: "Cartesia: невірний API-ключ"}
	case r.status == http.StatusForbidden || code == "forbidden":
		return &apiErr{kind: failAuth, msg: "Cartesia: ключ не має доступу (403): " + detail}
	case r.status == http.StatusTooManyRequests || code == "concurrency_limited":
		return &apiErr{kind: failRate, retry: r.retry, msg: "Cartesia: забагато запитів одночасно"}
	case code == "voice_not_found" || (r.status == http.StatusNotFound && strings.Contains(low, "voice")):
		return &apiErr{kind: failSkip, voice: true, msg: "Cartesia: голос не знайдено в акаунті цього ключа — вибери інший голос"}
	case code == "language_not_supported":
		return fatal("Cartesia: модель не озвучує мову цього фільму — вибери іншу мову або іншого провайдера")
	case code == "voice_model_mismatch":
		return fatal(fmt.Sprintf("Cartesia: голос не працює з моделлю %s — вибери інший голос або модель", model))
	case code == "model_not_found":
		return fatal(fmt.Sprintf("Cartesia: модель %s не знайдено — перевір модель у Налаштуваннях", model))
	case code == "model_sunsetted":
		return fatal(fmt.Sprintf("Cartesia: модель %s більше не працює — вибери sonic-3.6 у Налаштуваннях → Озвучка", model))
	case r.status >= 500:
		return &apiErr{kind: failTransient, msg: fmt.Sprintf("Cartesia %d: %s", r.status, detail)}
	}
	return fatal(fmt.Sprintf("Cartesia %d: %s", r.status, detail))
}

type cartesiaVoice struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Gender      *string `json:"gender"`
	Language    string  `json:"language"` // deprecated in 2026-08-14: accents[].locale
	Accents     []struct {
		Accent   string `json:"accent"`
		Locale   string `json:"locale"`
		IsNative bool   `json:"is_native"`
	} `json:"accents"`
	PreviewURL *string `json:"preview_file_url"`
	IsOwner    bool    `json:"is_owner"`
}

func (c *Client) cartesiaVoices(ctx context.Context, s settings) (VoiceList, error) {
	var raw []cartesiaVoice
	err := c.run(ctx, s, "", 0, true, func(key string) (int, error) {
		raw = nil
		cursor := ""
		for page := 0; page < 10; page++ {
			q := url.Values{"limit": {"100"}, "expand[]": {"preview_file_url"}}
			if cursor != "" {
				q.Set("starting_after", cursor)
			}
			res, err := c.do(ctx, http.MethodGet, cartesiaBase+"/voices?"+q.Encode(), cartesiaHeaders(key), nil, listTimeout, 16<<20)
			if err != nil {
				return 0, netErr(ctx, "Cartesia", err)
			}
			if !res.ok() {
				return 0, cartesiaErr(res, "")
			}
			var j struct {
				Data     []cartesiaVoice `json:"data"`
				HasMore  bool            `json:"has_more"`
				NextPage *string         `json:"next_page"`
			}
			if err := json.Unmarshal(res.body, &j); err != nil {
				var plain []cartesiaVoice // older versions: a bare array
				if json.Unmarshal(res.body, &plain) != nil {
					return 0, fatal("Cartesia: незрозуміла відповідь списку голосів: " + clip(string(res.body), 200))
				}
				j.Data = plain
			}
			raw = append(raw, j.Data...)
			next := ""
			if j.NextPage != nil {
				next = strings.TrimSpace(*j.NextPage)
			}
			if next == "" && j.HasMore && len(j.Data) > 0 {
				next = j.Data[len(j.Data)-1].ID
			}
			if !j.HasMore || next == "" || next == cursor {
				break
			}
			cursor = next
		}
		return 0, nil
	})
	if err != nil {
		return VoiceList{}, err
	}
	voices := make([]Voice, 0, len(raw))
	for _, v := range raw {
		voices = append(voices, cartesiaVoiceOf(v)) // voicebyid.go
	}
	sortVoices(voices, "uk")
	return VoiceList{Tier: "", CharsLeft: -1, Voices: voices}, nil
}

// cartesiaProbe: GET /voices?limit=1 (authenticated, free).
func (c *Client) cartesiaProbe(ctx context.Context, s settings, key string) probeResult {
	out := probeResult{used: -1}
	res, err := c.do(ctx, http.MethodGet, cartesiaBase+"/voices?limit=1", cartesiaHeaders(key), nil, listTimeout, 1<<20)
	if err != nil {
		out.err = kindOf(netErr(ctx, "Cartesia", err))
		return out
	}
	if !res.ok() {
		out.err = cartesiaErr(res, "")
		return out
	}
	out.info = "модель " + firstNonEmpty(s.model, s.info.DefaultModel)
	return out
}
