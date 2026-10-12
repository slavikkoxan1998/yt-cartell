package tts

// MiniMax Speech: POST /v1/t2a_v2, POST /v1/get_voice (Bearer) on
// https://api.minimax.io (the URL field may point at https://api.minimax.cn
// for a mainland-China account; keys work only on the platform that issued
// them) — https://platform.minimax.io/docs/api-reference/speech-t2a-http
// (checked 2026-10: models speech-2.8/2.6/02/01 hd|turbo, text < 10 000
// characters, 60 requests a minute; cloned voices cost $1.5 and designed ones
// $3 on their first synthesis, unused clones are deleted after 7 days).
// The audio comes back hex-encoded in JSON with extra_info.usage_characters
// (the billed characters). The film language goes in as language_boost
// ("Ukrainian", …; "auto" when unknown). Errors come as HTTP 200 with
// base_resp.status_code (docs: api-reference/errorcode): 1008 insufficient
// balance → exhausted; 2056 usage limit of the 5-hour window → rest;
// 1004 / 2049 invalid key → error; 1002 / 1039 / 1041 / 2045 rate limits;
// 1026 / 1027 sensitive text, 2013 bad params → the request; 2042 a voice of
// another account → next key.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var minimaxWindowRest = 30 * time.Minute // after 2056 (the 5-hour usage window)

type minimaxBaseResp struct {
	BaseResp struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp"`
}

func (c *Client) minimax(ctx context.Context, s settings, text, voice string, opt SynthOptions) ([]byte, error) {
	if len(s.keys) == 0 {
		return nil, errNoKey(s)
	}
	if voice == "" {
		return nil, fmt.Errorf("Не вибрано голос для озвучки")
	}
	boost := minimaxLangs[filmLanguage(opt.LanguageCode, text)]
	if boost == "" {
		boost = "auto"
	}
	payload := map[string]any{
		"model":          firstNonEmpty(opt.Model, s.model, s.info.DefaultModel),
		"text":           text,
		"stream":         false,
		"voice_setting":  map[string]any{"voice_id": voice, "speed": orDefault(opt.Speed, 1, 0.5, 2), "vol": 1, "pitch": 0},
		"audio_setting":  map[string]any{"sample_rate": 44100, "bitrate": 128000, "format": "mp3", "channel": 1},
		"language_boost": boost,
		"output_format":  "hex",
	}
	body, _ := json.Marshal(payload)
	base := firstNonEmpty(s.base, minimaxBase)
	var out []byte
	err := c.run(ctx, s, voice, runes(text), false, func(key string) (int, error) {
		res, err := c.do(ctx, http.MethodPost, base+"/v1/t2a_v2", bearer(key), body, synthTimeout, maxAudio*2)
		if err != nil {
			return 0, netErr(ctx, "MiniMax", err)
		}
		if !res.ok() {
			return 0, minimaxErr(res.status, 0, "", res)
		}
		var j struct {
			minimaxBaseResp
			Data struct {
				Audio string `json:"audio"`
			} `json:"data"`
			ExtraInfo struct {
				UsageCharacters int `json:"usage_characters"`
			} `json:"extra_info"`
		}
		if err := json.Unmarshal(res.body, &j); err != nil {
			return 0, fatal("MiniMax: незрозуміла відповідь: " + clip(string(res.body), 160))
		}
		if code := j.BaseResp.StatusCode; code != 0 {
			return 0, minimaxErr(res.status, code, j.BaseResp.StatusMsg, res)
		}
		b, err := hex.DecodeString(strings.TrimSpace(j.Data.Audio))
		if err != nil || len(b) == 0 {
			return 0, fatal("MiniMax: у відповіді немає аудіо")
		}
		out = b
		return j.ExtraInfo.UsageCharacters, nil
	})
	return out, err
}

// minimaxErr classifies an HTTP failure (code 0) or a base_resp error code.
func minimaxErr(status, code int, msg string, r reply) *apiErr {
	detail := firstNonEmpty(msg, clip(string(r.body), 160))
	switch code {
	case 0:
		return httpErr("MiniMax", r, "")
	case 1008:
		return &apiErr{kind: failQuota, msg: "MiniMax: на балансі закінчились кошти"}
	case 2056:
		return &apiErr{kind: failRate, retry: minimaxWindowRest, msg: "MiniMax: вичерпано ліміт тарифу на цей 5-годинний період"}
	case 1004, 2049:
		return &apiErr{kind: failAuth, msg: "MiniMax: невірний API-ключ"}
	case 1002, 1039, 1041, 2045:
		return &apiErr{kind: failRate, retry: r.retry, msg: fmt.Sprintf("MiniMax: перевищено ліміт запитів (%d)", code)}
	case 1000, 1001, 1024, 1033:
		return &apiErr{kind: failTransient, msg: fmt.Sprintf("MiniMax: тимчасова помилка сервісу (%d): %s", code, detail)}
	case 1026, 1027:
		return fatal("MiniMax відхилив текст як чутливий — зміни формулювання сцени")
	case 1042:
		return fatal("MiniMax: у тексті забагато невидимих символів — перевір текст сцени")
	case 2042:
		return &apiErr{kind: failSkip, voice: true, msg: "MiniMax: цей голос недоступний акаунту ключа — вибери інший голос"}
	}
	return fatal(fmt.Sprintf("MiniMax: сервіс не прийняв запит (%d): %s", code, detail))
}

type minimaxVoiceJSON struct {
	VoiceID     string   `json:"voice_id"`
	VoiceName   string   `json:"voice_name"`
	Description []string `json:"description"`
}

func (c *Client) minimaxVoices(ctx context.Context, s settings) (VoiceList, error) {
	var sys, cloned, gen []minimaxVoiceJSON
	base := firstNonEmpty(s.base, minimaxBase)
	err := c.run(ctx, s, "", 0, true, func(key string) (int, error) {
		res, err := c.do(ctx, http.MethodPost, base+"/v1/get_voice", bearer(key), []byte(`{"voice_type":"all"}`), listTimeout, 16<<20)
		if err != nil {
			return 0, netErr(ctx, "MiniMax", err)
		}
		if !res.ok() {
			return 0, minimaxErr(res.status, 0, "", res)
		}
		var j struct {
			minimaxBaseResp
			System     []minimaxVoiceJSON `json:"system_voice"`
			Cloning    []minimaxVoiceJSON `json:"voice_cloning"`
			Generation []minimaxVoiceJSON `json:"voice_generation"`
		}
		if err := json.Unmarshal(res.body, &j); err != nil {
			return 0, fatal("MiniMax: незрозуміла відповідь списку голосів: " + clip(string(res.body), 160))
		}
		if code := j.BaseResp.StatusCode; code != 0 {
			return 0, minimaxErr(res.status, code, j.BaseResp.StatusMsg, res)
		}
		sys, cloned, gen = j.System, j.Cloning, j.Generation
		return 0, nil
	})
	if err != nil {
		return VoiceList{}, err
	}
	var voices []Voice
	add := func(list []minimaxVoiceJSON, cat string) {
		for _, v := range list {
			if v.VoiceID == "" {
				continue
			}
			desc := strings.Join(v.Description, " ")
			voices = append(voices, Voice{
				ID: v.VoiceID, Name: firstNonEmpty(v.VoiceName, strings.ReplaceAll(v.VoiceID, "_", " ")), Category: cat,
				Gender: genderFromWords(v.VoiceID + " " + v.VoiceName + " " + desc), Accent: minimaxAccent(v.VoiceID), Usable: true,
			})
		}
	}
	add(cloned, "cloned")
	add(gen, "generated")
	add(sys, "premade")
	sortVoices(voices, "")
	return VoiceList{CharsLeft: -1, Voices: voices}, nil
}

// minimaxAccent: system voice ids start with their language ("English_…",
// "Russian_…"); other ids have none.
func minimaxAccent(id string) string {
	first, _, ok := strings.Cut(id, "_")
	if !ok {
		return ""
	}
	if code := NormalizeLanguage(first); code != "" {
		return languageName(code)
	}
	return ""
}

// genderFromWords guesses a voice's gender from its name and description.
func genderFromWords(s string) string {
	w := strings.ToLower(s)
	for _, f := range []string{"female", "woman", "girl", "lady", "queen", "mother", "grandma", "sister"} {
		if strings.Contains(w, f) {
			return "female"
		}
	}
	for _, m := range []string{"male", "man", "boy", "gentleman", "guy", "king", "father", "grandpa", "brother", "knight"} {
		if strings.Contains(w, m) {
			return "male"
		}
	}
	return ""
}

// minimaxProbe: POST /v1/get_voice {"voice_type":"voice_cloning"} (authenticated, free).
func (c *Client) minimaxProbe(ctx context.Context, s settings, key string) probeResult {
	out := probeResult{used: -1}
	res, err := c.do(ctx, http.MethodPost, firstNonEmpty(s.base, minimaxBase)+"/v1/get_voice", bearer(key), []byte(`{"voice_type":"voice_cloning"}`), listTimeout, 4<<20)
	if err != nil {
		out.err = kindOf(netErr(ctx, "MiniMax", err))
		return out
	}
	if !res.ok() {
		out.err = minimaxErr(res.status, 0, "", res)
		return out
	}
	var j minimaxBaseResp
	_ = json.Unmarshal(res.body, &j)
	if code := j.BaseResp.StatusCode; code != 0 {
		out.err = minimaxErr(res.status, code, j.BaseResp.StatusMsg, res)
		return out
	}
	out.info = "модель " + firstNonEmpty(s.model, s.info.DefaultModel)
	return out
}
