package tts

// Fish Audio: POST /v1/tts (Bearer; the model goes in the "model" header:
// s2.1-pro default, s2.1-pro-free, s2-pro, s1, drama-3-preview — an unknown
// one silently becomes s2.1-pro), GET /model (voice library), GET
// /wallet/self/api-credit (balance) — https://docs.fish.audio/api-reference
// (checked 2026-10). S2.1 Pro speaks 83 languages and detects the language
// from the text, so there is no language field; S1 has no Ukrainian. Voices
// are "models": own ones (self=true) plus popular public ones (Ukrainian
// first) — any key may use public library voices. s2.1-pro-free costs
// nothing under fair use, the others $15 per 1M UTF-8 bytes. Errors: 400 bad
// params or an unknown reference_id (→ next key, like 404), 401 invalid key,
// 402 balance spent → exhausted, 404 voice → next key, 429 concurrency (5
// below $100 paid; no Retry-After), 5xx/503 overload → retry.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) fish(ctx context.Context, s settings, text, voice string, opt SynthOptions) ([]byte, error) {
	if len(s.keys) == 0 {
		return nil, errNoKey(s)
	}
	if voice == "" {
		return nil, fmt.Errorf("Не вибрано голос для озвучки")
	}
	model := firstNonEmpty(opt.Model, s.model, s.info.DefaultModel)
	payload := map[string]any{
		"text": text, "reference_id": voice, "format": "mp3", "mp3_bitrate": 128,
		"normalize": true, "latency": "normal",
	}
	if opt.Speed > 0 && opt.Speed != 1 {
		payload["prosody"] = map[string]any{"speed": orDefault(opt.Speed, 1, 0.5, 2)}
	}
	body, _ := json.Marshal(payload)
	var out []byte
	err := c.run(ctx, s, voice, runes(text), false, func(key string) (int, error) {
		hdr := bearer(key)
		hdr["model"] = model
		res, err := c.do(ctx, http.MethodPost, fishBase+"/v1/tts", hdr, body, synthTimeout, maxAudio)
		if err != nil {
			return 0, netErr(ctx, "Fish Audio", err)
		}
		if !res.ok() {
			return 0, fishErr(res)
		}
		out, err = audio(res, "Fish Audio")
		return 0, err
	})
	return out, err
}

func fishErr(r reply) *apiErr {
	var j struct {
		Message string `json:"message"`
		Detail  any    `json:"detail"`
	}
	_ = json.Unmarshal(r.body, &j)
	detail := firstNonEmpty(j.Message, str(j.Detail), clip(string(r.body), 160))
	low := strings.ToLower(detail)
	switch r.status {
	case http.StatusBadRequest:
		if strings.Contains(low, "reference") || strings.Contains(low, "not found") || strings.Contains(low, "not exist") {
			return &apiErr{kind: failSkip, voice: true, msg: "Fish Audio: голос не знайдено (або він приватний в іншому акаунті) — вибери інший голос"}
		}
	case http.StatusUnauthorized:
		return &apiErr{kind: failAuth, msg: "Fish Audio: невірний API-ключ"}
	case http.StatusPaymentRequired:
		return &apiErr{kind: failQuota, msg: "Fish Audio: закінчились кошти на балансі API"}
	case http.StatusForbidden:
		return &apiErr{kind: failAuth, msg: "Fish Audio: ключ не має доступу (403)" + prefixed(": ", clip(detail, 120))}
	case http.StatusNotFound:
		return &apiErr{kind: failSkip, voice: true, msg: "Fish Audio: голос не знайдено (або він приватний в іншому акаунті) — вибери інший голос"}
	case http.StatusTooManyRequests:
		return &apiErr{kind: failRate, retry: r.retry, msg: "Fish Audio: забагато запитів"}
	}
	if r.status >= 500 {
		return &apiErr{kind: failTransient, msg: fmt.Sprintf("Fish Audio %d: сервіс перевантажений", r.status)}
	}
	return fatal(fmt.Sprintf("Fish Audio %d: %s", r.status, clip(detail, 200)))
}

type fishModel struct {
	ID        string   `json:"_id"`
	Title     string   `json:"title"`
	Tags      []string `json:"tags"`
	Languages []string `json:"languages"`
	Samples   []struct {
		Audio string `json:"audio"`
	} `json:"samples"`
	Visibility string `json:"visibility"`
	State      string `json:"state"`
}

func (c *Client) fishVoices(ctx context.Context, s settings) (VoiceList, error) {
	type part struct {
		q   url.Values
		own bool
	}
	parts := []part{
		{url.Values{"self": {"true"}, "page_size": {"100"}}, true},
		{url.Values{"language": {"uk"}, "page_size": {"50"}, "sort_by": {"score"}}, false},
		{url.Values{"page_size": {"50"}, "sort_by": {"score"}}, false},
	}
	var voices []Voice
	err := c.run(ctx, s, "", 0, true, func(key string) (int, error) {
		voices = nil
		seen := map[string]bool{}
		for i, p := range parts {
			res, err := c.do(ctx, http.MethodGet, fishBase+"/model?"+p.q.Encode(), bearer(key), nil, listTimeout, 16<<20)
			if err != nil {
				return 0, netErr(ctx, "Fish Audio", err)
			}
			if !res.ok() {
				if i > 0 && res.status < 500 && res.status != http.StatusUnauthorized {
					continue // the public library is a bonus
				}
				return 0, fishErr(res)
			}
			var j struct {
				Items []fishModel `json:"items"`
			}
			if err := json.Unmarshal(res.body, &j); err != nil {
				return 0, fatal("Fish Audio: незрозуміла відповідь списку голосів: " + clip(string(res.body), 160))
			}
			for _, m := range j.Items {
				if m.ID == "" || seen[m.ID] || (m.State != "" && m.State != "trained") {
					continue
				}
				seen[m.ID] = true
				v := Voice{ID: m.ID, Name: firstNonEmpty(m.Title, m.ID), Category: "library", Gender: genderFromWords(strings.Join(m.Tags, " ")), Usable: true}
				if p.own {
					v.Category = "cloned"
				}
				if len(m.Languages) > 0 {
					v.Language = langCode(m.Languages[0])
					v.Accent = languageName(v.Language)
				}
				if len(m.Samples) > 0 {
					v.PreviewURL = m.Samples[0].Audio
				}
				voices = append(voices, v)
			}
		}
		return 0, nil
	})
	if err != nil {
		return VoiceList{}, err
	}
	sortVoices(voices, "uk")
	return VoiceList{CharsLeft: -1, Voices: voices}, nil
}

// fishProbe: GET /wallet/self/api-credit — authenticates and tells the balance.
func (c *Client) fishProbe(ctx context.Context, s settings, key string) probeResult {
	out := probeResult{used: -1}
	res, err := c.do(ctx, http.MethodGet, fishBase+"/wallet/self/api-credit", bearer(key), nil, listTimeout, 1<<20)
	if err != nil {
		out.err = kindOf(netErr(ctx, "Fish Audio", err))
		return out
	}
	if !res.ok() {
		out.err = fishErr(res)
		return out
	}
	var j struct {
		Credit any `json:"credit"`
	}
	_ = json.Unmarshal(res.body, &j)
	if credit, err := strconv.ParseFloat(str(j.Credit), 64); err == nil {
		out.info = fmt.Sprintf("баланс %.2f $", credit)
		if credit > 0 {
			no := false
			out.quota, out.quotaMsg = &no, ""
		}
	}
	return out
}
