package tts

// Lumean (https://lumean.app): a studio on one balance whose public API
// voices text through ElevenLabs models. Docs (read 2026-10-05):
// https://api.lumean.app/docs/llm.md and the OpenAPI document
// https://api.lumean.app/docs/public-openapi.en.json. Everything is
// asynchronous — there is no "text in, audio out" call:
//
//	base   https://api.lumean.app/api/public, header X-API-KEY: <key>
//	       (the key needs orders.write, orders.read, orders.download,
//	       templates.read/write, voices.read; billing.read for the balance)
//	1. a template (once per key and model): POST /templates
//	   {service_key:"elevenlabs", name, config:{tts_settings:{mode:"mode_v1",
//	   model_id, voice_id, voice_settings:{stability, speed}}, output_settings}};
//	   an existing one with our name is reused (GET /templates)
//	2. POST /orders {template_id, input_text, name, config_override:{tts_settings:
//	   {voice_id, language_code, voice_settings}}} → 201 data.id
//	3. GET /orders/{id} until data.status completed | result_delivered
//	   (failed | compensated | cancelled | partially_completed = failure)
//	4. POST /storage/url {path: data.result.files[0]} → data.url (≈60 min), GET it
//
// Voices: GET /voices/elevenlabs/library?page=0&page_size=100 (ElevenLabs
// Voice Library objects; page from 0; search, gender, required_languages).
// Models: eleven_multilingual_v2 (29 languages incl. uk) … eleven_v3 (73);
// a language outside the 29 on a non-v3 model is refused (422), so such a
// line goes to eleven_v3; stability on v3 is 0, 0.5 or 1; speed 0.7–1.2.
// Balance: GET /subscriptions (tokens_remaining) or GET /user. Errors:
// {success:false, message, reason|error_code}; 401 key, 402
// insufficient_balance, 403 permission / no_subscription, 422 validation or
// content_blocked, 429 rate_limit_exceeded | token_quota_exceeded |
// limit_exceeded | concurrency_limit_exceeded | policy_cooldown_active |
// ip_banned (Retry-After on some). The docs warn that probing undocumented
// paths bans the IP — only the documented ones are called.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	lumeanBase      = "https://api.lumean.app/api/public"
	lumeanPoll      = 2 * time.Second
	lumeanJobLimit  = 8 * time.Minute
	lumeanVoicePage = 100
)

const lumeanLabel = "Lumean"

// lumeanModels are the ElevenLabs models Lumean takes (first = default).
var lumeanModels = []string{"eleven_multilingual_v2", "eleven_v3", "eleven_flash_v2_5", "eleven_turbo_v2_5", "eleven_v3_beta", "eleven_turbo_v2", "eleven_flash_v2"}

// lumeanBaseLangs are the 29 languages of the non-v3 models.
var lumeanBaseLangs = map[string]bool{}

func init() {
	for _, l := range strings.Split("en ja zh de hi fr ko pt it es id nl tr fil pl sv bg ro ar cs el fi hr ms sk da ta uk ru", " ") {
		lumeanBaseLangs[l] = true
	}
}

func lumeanV3(model string) bool { return strings.HasPrefix(model, "eleven_v3") }

func lumeanHdr(key string) map[string]string {
	return map[string]string{"X-API-KEY": key, "Accept": "application/json"}
}

func (s settings) lumeanBase() string { return firstNonEmpty(s.base, lumeanBase) }

// lumeanTemplates remembers the template per key and model (ids are UUIDs;
// the key is kept as a hash).
var lumeanTemplates sync.Map // sha256(base|key)|model → template id

func lumeanTplKey(base, key, model string) string {
	h := sha256.Sum256([]byte(base + "|" + key))
	return hex.EncodeToString(h[:8]) + "|" + model
}

func lumeanTplName(model string) string { return "YT Cartell Studio · " + model }

func (c *Client) lumean(ctx context.Context, s settings, text, voice string, opt SynthOptions) ([]byte, error) {
	if len(s.keys) == 0 {
		return nil, errNoKey(s)
	}
	if voice == "" {
		return nil, errors.New("Не вибрано голос Lumean — вибери його в Налаштуваннях → Озвучка")
	}
	model := firstNonEmpty(opt.Model, s.model, s.info.DefaultModel, lumeanModels[0])
	lang := langCode(opt.LanguageCode)
	if lang != "" && !lumeanBaseLangs[lang] && !lumeanV3(model) {
		model = "eleven_v3" // the other models refuse this language (422)
	}
	var out []byte
	err := c.run(ctx, s, voice, runes(text), false, func(key string) (int, error) {
		data, err := c.lumeanOnce(ctx, s, key, model, text, voice, lang, opt)
		if err == nil {
			out = data
		}
		return 0, err
	})
	return out, err
}

// lumeanOnce is one order with one key.
func (c *Client) lumeanOnce(ctx context.Context, s settings, key, model, text, voice, lang string, opt SynthOptions) ([]byte, error) {
	base := s.lumeanBase()
	tpl, err := c.lumeanTemplate(ctx, base, key, model, voice)
	if err != nil {
		return nil, err
	}
	vs := map[string]any{}
	if opt.Speed > 0 {
		vs["speed"] = orDefault(opt.Speed, 1, 0.7, 1.2)
	}
	if opt.Stability > 0 {
		st := orDefault(opt.Stability, 0.5, 0, 1)
		if lumeanV3(model) { // v3: 0, 0.5 or 1
			switch {
			case st < 0.25:
				st = 0
			case st > 0.75:
				st = 1
			default:
				st = 0.5
			}
		}
		vs["stability"] = st
	}
	tts := map[string]any{"voice_id": voice}
	if lang != "" {
		tts["language_code"] = lang
	}
	if len(vs) > 0 {
		tts["voice_settings"] = vs
	}
	order := map[string]any{"template_id": tpl, "input_text": text, "name": "YT Cartell Studio", "config_override": map[string]any{"tts_settings": tts}}
	body, _ := json.Marshal(order)
	res, err := c.do(ctx, http.MethodPost, base+"/orders", lumeanHdr(key), body, synthTimeout, 4<<20)
	if err != nil {
		return nil, netErr(ctx, lumeanLabel, err)
	}
	if res.status == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(string(res.body)), "template") {
		// The template is gone (deleted in Lumean): a new one, once.
		lumeanTemplates.Delete(lumeanTplKey(base, key, model))
		if tpl, err = c.lumeanTemplate(ctx, base, key, model, voice); err != nil {
			return nil, err
		}
		order["template_id"] = tpl
		body, _ = json.Marshal(order)
		if res, err = c.do(ctx, http.MethodPost, base+"/orders", lumeanHdr(key), body, synthTimeout, 4<<20); err != nil {
			return nil, netErr(ctx, lumeanLabel, err)
		}
	}
	if !res.ok() {
		return nil, lumeanErr(res)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(res.body, &created) != nil || created.Data.ID == "" {
		return nil, fatal("Lumean: сервер не повернув номер замовлення: " + clip(string(res.body), 200))
	}
	id := created.Data.ID
	path, err := c.lumeanWait(ctx, base, key, id)
	if err != nil {
		return nil, err
	}
	// The file: a temporary link for the stored path, then a plain GET.
	lb, _ := json.Marshal(map[string]any{"path": path})
	res, err = c.do(ctx, http.MethodPost, base+"/storage/url", lumeanHdr(key), lb, listTimeout, 1<<20)
	if err != nil {
		return nil, netErr(ctx, lumeanLabel, err)
	}
	if !res.ok() {
		return nil, lumeanErr(res)
	}
	var link struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if json.Unmarshal(res.body, &link) != nil || link.Data.URL == "" {
		return nil, fatal("Lumean: не вдалося отримати посилання на аудіо: " + clip(string(res.body), 200))
	}
	res, err = c.do(ctx, http.MethodGet, link.Data.URL, nil, nil, synthTimeout, maxAudio)
	if err != nil {
		return nil, netErr(ctx, lumeanLabel, err)
	}
	if !res.ok() {
		return nil, &apiErr{kind: failTransient, msg: fmt.Sprintf("Lumean: не вдалося завантажити аудіо (%d)", res.status)}
	}
	return audio(res, lumeanLabel)
}

// lumeanWait polls the order until it is done and returns its audio path.
func (c *Client) lumeanWait(ctx context.Context, base, key, id string) (string, error) {
	deadline := time.Now().Add(lumeanJobLimit)
	blips := 0
	for {
		if err := sleep(ctx, lumeanPoll); err != nil {
			c.lumeanCancel(base, key, id)
			return "", err
		}
		res, err := c.do(ctx, http.MethodGet, base+"/orders/"+url.PathEscape(id), lumeanHdr(key), nil, listTimeout, 8<<20)
		switch {
		case err != nil && ctx.Err() != nil:
			c.lumeanCancel(base, key, id)
			return "", ctx.Err()
		case err != nil || res.status >= 500 || res.status == http.StatusTooManyRequests:
			if blips++; blips > 5 {
				if err != nil {
					return "", netErr(ctx, lumeanLabel, err)
				}
				return "", lumeanErr(res)
			}
		case !res.ok():
			return "", lumeanErr(res)
		default:
			blips = 0
			var o struct {
				Data struct {
					Status string `json:"status"`
					Result *struct {
						Files       []string `json:"files"`
						UserMessage string   `json:"user_message"`
					} `json:"result"`
				} `json:"data"`
			}
			if json.Unmarshal(res.body, &o) != nil {
				return "", fatal("Lumean: незрозуміла відповідь про замовлення: " + clip(string(res.body), 200))
			}
			switch o.Data.Status {
			case "completed", "result_delivered":
				if o.Data.Result == nil || len(o.Data.Result.Files) == 0 {
					return "", fatal("Lumean: замовлення готове, але файлу немає (можливо, його вже видалено)")
				}
				return o.Data.Result.Files[0], nil
			case "failed", "compensated", "cancelled":
				msg := "Lumean: озвучка не вдалася (" + o.Data.Status + ")"
				if o.Data.Result != nil && strings.TrimSpace(o.Data.Result.UserMessage) != "" {
					msg += ": " + clip(o.Data.Result.UserMessage, 200)
				}
				return "", &apiErr{kind: failTransient, msg: msg}
			case "partially_completed":
				return "", &apiErr{kind: failTransient, msg: "Lumean: частину тексту не озвучено (partially_completed) — спробуй ще раз"}
			}
		}
		if time.Now().After(deadline) {
			c.lumeanCancel(base, key, id)
			return "", fatal(fmt.Sprintf("Lumean: озвучка не завершилась за %d хв", int(lumeanJobLimit.Minutes())))
		}
	}
}

// lumeanCancel asks Lumean to stop an order the studio gave up on.
func (c *Client) lumeanCancel(base, key, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = c.do(ctx, http.MethodPost, base+"/orders/"+url.PathEscape(id)+"/cancel", lumeanHdr(key), []byte("{}"), 10*time.Second, 1<<20)
}

// lumeanTemplate is the template of key and model: remembered, found by
// name, or created (with voice as its default voice).
func (c *Client) lumeanTemplate(ctx context.Context, base, key, model, voice string) (string, error) {
	ck := lumeanTplKey(base, key, model)
	if id, ok := lumeanTemplates.Load(ck); ok {
		return id.(string), nil
	}
	name := lumeanTplName(model)
	if res, err := c.do(ctx, http.MethodGet, base+"/templates", lumeanHdr(key), nil, listTimeout, 8<<20); err == nil && res.ok() {
		var j struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		if json.Unmarshal(res.body, &j) == nil {
			for _, t := range j.Data {
				if t.Name == name && t.ID != "" {
					lumeanTemplates.Store(ck, t.ID)
					return t.ID, nil
				}
			}
		}
	} else if err == nil && (res.status == http.StatusUnauthorized) {
		return "", lumeanErr(res)
	}
	cfg := map[string]any{
		"tts_settings": map[string]any{
			"mode": "mode_v1", "model_id": model, "voice_id": voice,
			"voice_settings": map[string]any{"stability": 0.5, "speed": 1.0},
		},
		"output_settings": map[string]any{"audio_format": "mp3", "audio_quality": "high", "sample_rate": 44100},
	}
	body, _ := json.Marshal(map[string]any{"service_key": "elevenlabs", "name": name, "config": cfg})
	res, err := c.do(ctx, http.MethodPost, base+"/templates", lumeanHdr(key), body, listTimeout, 4<<20)
	if err != nil {
		return "", netErr(ctx, lumeanLabel, err)
	}
	if !res.ok() {
		ae := lumeanErr(res)
		if res.status == http.StatusForbidden {
			ae.msg += " — ключу потрібне право templates.write (пресет full)"
		}
		return "", ae
	}
	var j struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(res.body, &j) != nil || j.Data.ID == "" {
		return "", fatal("Lumean: шаблон не створено: " + clip(string(res.body), 200))
	}
	lumeanTemplates.Store(ck, j.Data.ID)
	return j.Data.ID, nil
}

// lumeanErr classifies {success:false, message, reason|error_code}.
func lumeanErr(r reply) *apiErr {
	var j struct {
		Message   string              `json:"message"`
		Reason    string              `json:"reason"`
		ErrorCode string              `json:"error_code"`
		Errors    map[string][]string `json:"errors"`
		Window    string              `json:"window"`
	}
	_ = json.Unmarshal(r.body, &j)
	reason := firstNonEmpty(j.Reason, j.ErrorCode)
	detail := firstNonEmpty(j.Message, clip(string(r.body), 200))
	for f, list := range j.Errors {
		if len(list) > 0 {
			detail += " (" + f + ": " + list[0] + ")"
			break
		}
	}
	with := func(s string) string {
		if detail == "" {
			return s
		}
		return s + ": " + clip(detail, 200)
	}
	switch {
	case r.status == http.StatusUnauthorized:
		return &apiErr{kind: failAuth, msg: with("Lumean: ключ не прийнято (401)")}
	case r.status == http.StatusPaymentRequired:
		return &apiErr{kind: failQuota, msg: with("Lumean: бракує коштів на балансі (" + firstNonEmpty(reason, "402") + ") — поповни баланс на lumean.app")}
	case r.status == http.StatusForbidden && (reason == "no_subscription" || reason == "entitlement_missing"):
		return &apiErr{kind: failQuota, msg: with("Lumean: немає підписки на озвучку (" + reason + ")")}
	case r.status == http.StatusForbidden:
		return &apiErr{kind: failSkip, msg: with("Lumean: ключу бракує прав (403) — потрібні orders.write, orders.download, templates.write, voices.read")}
	case reason == "ip_banned":
		return fatal(with(fmt.Sprintf("Lumean тимчасово заблокував цю IP-адресу (429, %s)", firstNonEmpty(humanSecs(r.retry), "кілька хвилин"))))
	case reason == "token_quota_exceeded" && (j.Window == "quota" || r.retry == 0), reason == "limit_exceeded":
		return &apiErr{kind: failQuota, msg: with("Lumean: вичерпано ліміт підписки (" + reason + ")")}
	case r.status == http.StatusTooManyRequests:
		return &apiErr{kind: failRate, retry: r.retry, msg: with("Lumean: забагато запитів (" + firstNonEmpty(reason, "429") + ") — зачекаємо й повторимо")}
	case reason == "content_blocked":
		return fatal(with("Lumean: текст заблоковано контентною політикою — зміни спірний фрагмент"))
	case r.status == http.StatusUnprocessableEntity:
		return fatal(with("Lumean відхилив запит (422)"))
	}
	return httpErr(lumeanLabel, r, detail)
}

func humanSecs(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + " с"
	}
	return strconv.Itoa(int(d.Minutes()+0.5)) + " хв"
}

// lumeanVoice is an ElevenLabs Voice Library voice as Lumean lists it.
type lumeanVoice struct {
	VoiceID     string `json:"voice_id"`
	Name        string `json:"name"`
	Gender      string `json:"gender"`
	Age         string `json:"age"`
	Accent      string `json:"accent"`
	Language    string `json:"language"`
	Locale      string `json:"locale"`
	Category    string `json:"category"`
	PreviewURL  string `json:"preview_url"`
	Descriptive string `json:"descriptive"`
	UseCase     string `json:"use_case"`
	Verified    []struct {
		Language string `json:"language"`
		Locale   string `json:"locale"`
	} `json:"verified_languages"`
}

// lumeanVoices lists Voice Library voices: Ukrainian ones first, then the
// popular ones (a few pages).
func (c *Client) lumeanVoices(ctx context.Context, s settings) (VoiceList, error) {
	base := s.lumeanBase()
	queries := []url.Values{
		{"required_languages": {"uk"}, "page": {"0"}, "page_size": {strconv.Itoa(lumeanVoicePage)}},
		{"page": {"0"}, "page_size": {strconv.Itoa(lumeanVoicePage)}},
		{"page": {"1"}, "page_size": {strconv.Itoa(lumeanVoicePage)}},
		{"page": {"2"}, "page_size": {strconv.Itoa(lumeanVoicePage)}},
	}
	var voices []Voice
	err := c.run(ctx, s, "", 0, true, func(key string) (int, error) {
		voices = nil
		seen := map[string]bool{}
		got := 0
		for i, q := range queries {
			res, err := c.do(ctx, http.MethodGet, base+"/voices/elevenlabs/library?"+q.Encode(), lumeanHdr(key), nil, listTimeout, 32<<20)
			if err != nil {
				return 0, netErr(ctx, lumeanLabel, err)
			}
			if !res.ok() {
				if (i == 0 || got > 0) && res.status != http.StatusUnauthorized && res.status != http.StatusForbidden {
					continue // a filter Lumean does not take, or a page past the end
				}
				return 0, lumeanErr(res)
			}
			var j struct {
				Data struct {
					Voices  []lumeanVoice `json:"voices"`
					HasMore bool          `json:"has_more"`
				} `json:"data"`
			}
			if err := json.Unmarshal(res.body, &j); err != nil {
				return 0, fatal("Lumean: незрозуміла відповідь списку голосів: " + clip(string(res.body), 160))
			}
			got++
			for _, v := range j.Data.Voices {
				if v.VoiceID == "" || seen[v.VoiceID] {
					continue
				}
				seen[v.VoiceID] = true
				lang := firstNonEmpty(v.Locale, v.Language)
				if i == 0 {
					lang = firstNonEmpty(lang, "uk")
					for _, vl := range v.Verified {
						if langCode(vl.Language) == "uk" {
							lang = firstNonEmpty(vl.Locale, "uk")
						}
					}
				}
				voices = append(voices, Voice{ID: v.VoiceID, Name: firstNonEmpty(v.Name, v.VoiceID), Category: "library",
					Gender: gender(v.Gender), Age: strings.ReplaceAll(v.Age, "_", " "), Accent: v.Accent, Language: lang,
					PreviewURL: v.PreviewURL, Usable: true})
			}
			if i > 0 && !j.Data.HasMore {
				break
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

// lumeanProbe: GET /subscriptions (plan and tokens left), else GET /user
// (a key without billing.read) — nothing is generated.
func (c *Client) lumeanProbe(ctx context.Context, s settings, key string) probeResult {
	out := probeResult{used: -1}
	base := s.lumeanBase()
	res, err := c.do(ctx, http.MethodGet, base+"/subscriptions", lumeanHdr(key), nil, listTimeout, 4<<20)
	if err != nil {
		out.err = kindOf(netErr(ctx, lumeanLabel, err))
		return out
	}
	if res.ok() {
		var j struct {
			Data struct {
				Subscriptions []struct {
					Items []struct {
						Plan struct {
							Name string `json:"name"`
							Code string `json:"code"`
						} `json:"plan"`
						IsUsable        bool  `json:"is_usable"`
						TokenAllowance  int64 `json:"token_allowance"`
						TokensUsed      int64 `json:"tokens_used"`
						TokensRemaining int64 `json:"tokens_remaining"`
					} `json:"items"`
				} `json:"subscriptions"`
			} `json:"data"`
		}
		_ = json.Unmarshal(res.body, &j)
		var parts []string
		for _, sub := range j.Data.Subscriptions {
			for _, it := range sub.Items {
				parts = append(parts, fmt.Sprintf("тариф %s · залишилось %s токенів", firstNonEmpty(it.Plan.Name, it.Plan.Code, "—"), thousandsUK(it.TokensRemaining)))
				out.plan = firstNonEmpty(it.Plan.Code, it.Plan.Name)
			}
		}
		if len(parts) == 0 {
			parts = append(parts, "без активної підписки — оплата з балансу")
		}
		out.info = strings.Join(parts, "; ")
		return out
	}
	if res.status != http.StatusForbidden {
		out.err = lumeanErr(res)
		return out
	}
	// No billing.read: the profile proves the key.
	res, err = c.do(ctx, http.MethodGet, base+"/user", lumeanHdr(key), nil, listTimeout, 4<<20)
	if err != nil {
		out.err = kindOf(netErr(ctx, lumeanLabel, err))
		return out
	}
	if !res.ok() {
		out.err = lumeanErr(res)
		return out
	}
	out.info = "ключ дійсний (баланс не видно: немає права billing.read)"
	return out
}

func thousandsUK(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s
}
