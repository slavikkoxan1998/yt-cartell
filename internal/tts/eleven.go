package tts

// ElevenLabs: POST /v1/text-to-speech/{voice}?output_format=mp3_44100_128,
// GET /v2/voices, GET /v1/user/subscription (xi-api-key). Checked against the
// docs 2026-10 (elevenlabs.io/docs/api-reference/text-to-speech/convert,
// /voices/search, /user/subscription/get, /eleven-api/resources/errors).
// Errors: {"detail":{"type","code","status" (legacy = code),"message"}} —
//   402 insufficient_credits (legacy quota_exceeded, once sent with 401/429)
//   → exhausted; 402 paid_plan_required "Free users cannot use library voices
//   via the API" → this key's account cannot use the voice (another key may);
//   401 invalid_api_key / missing_api_key / unauthorized (legacy
//   detected_unusual_activity); 403 insufficient_permissions → the key;
//   403 feature_not_available / subscription_required / voice_access_denied /
//   model_access_denied → another key; 404 voice_not_found (legacy 400
//   voice_does_not_exist) → another key; 404 model_not_found, 400 text_too_long /
//   unsupported_model → the request; 429 rate_limit_exceeded /
//   concurrent_limit_exceeded / system_busy → rest.
// Plans: a free account uses only the default (premade) voices and its own
// through the API — Voice Library voices need a paid plan ("Voice Library
// voices are not available via the API to free tier users"); ElevenLabs
// retires the default voices on 2026-12-31. The plan comes from the
// subscription (tier, or status "free") and is remembered per key (keyRec.Plan).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

func (c *Client) eleven(ctx context.Context, s settings, text, voice string, opt SynthOptions) ([]byte, error) {
	if len(s.keys) == 0 {
		return nil, errNoKey(s)
	}
	if voice == "" {
		return nil, errors.New("Не вибрано голос для озвучки")
	}
	model, lang := currentElevenModel(strings.TrimSpace(opt.Model)), ""
	if model == "" {
		model, lang = c.ModelFor(ctx, opt.LanguageCode)
	} else {
		lang = c.enforcedLanguage(ctx, s, model, langCode(opt.LanguageCode))
	}
	vs := map[string]any{
		"similarity_boost":  orDefault(opt.Similarity, 0.75, 0, 1),
		"stability":         orDefault(opt.Stability, 0.5, 0, 1),
		"style":             0,
		"speed":             orDefault(opt.Speed, 1, 0.7, 1.2),
		"use_speaker_boost": true,
	}
	if isV3(model) {
		vs["stability"] = snapV3Stability(orDefault(opt.Stability, 0.5, 0, 1)) // Creative 0, Natural 0.5, Robust 1
		delete(vs, "speed")                                                    // "speed is unavailable for Eleven v3"
	}
	payload := map[string]any{"text": text, "model_id": model, "voice_settings": vs}
	if lang != "" {
		payload["language_code"] = lang
	}
	body, _ := json.Marshal(payload)
	u := elevenBase + "/v1/text-to-speech/" + url.PathEscape(voice) + "?output_format=mp3_44100_128"
	var out []byte
	err := c.run(ctx, s, voice, runes(text), false, func(key string) (int, error) {
		hdr := map[string]string{"xi-api-key": key}
		for {
			res, err := c.do(ctx, http.MethodPost, u, hdr, body, synthTimeout, maxAudio)
			if err != nil {
				return 0, netErr(ctx, "ElevenLabs", err)
			}
			if res.ok() {
				out, err = audio(res, "ElevenLabs")
				return 0, err
			}
			if _, hinted := payload["language_code"]; hinted && (res.status == 400 || res.status == 422) && reLanguage.MatchString(string(res.body)) {
				// The model refused the language hint: the text carries its language anyway.
				delete(payload, "language_code")
				body, _ = json.Marshal(payload)
				continue
			}
			return 0, elevenErr(res)
		}
	})
	return out, err
}

// elevenDetail reads detail.code (or the legacy detail.status) and detail.message.
func elevenDetail(b []byte) (code, msg string) {
	var j struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(b, &j) != nil || len(j.Detail) == 0 {
		return "", ""
	}
	var d struct {
		Code    string `json:"code"`
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if json.Unmarshal(j.Detail, &d) == nil {
		return strings.ToLower(firstNonEmpty(d.Code, d.Status)), d.Message
	}
	var plain string
	if json.Unmarshal(j.Detail, &plain) == nil {
		return strings.ToLower(plain), plain
	}
	return "", ""
}

// msgElevenPaidVoice is the friendly form of the paid-only voice refusal.
const msgElevenPaidVoice = "ElevenLabs: це голос із бібліотеки (Voice Library) — через API він працює лише на платному тарифі. На безкоштовному доступні стандартні й власні голоси: вибери інший голос або перейди на платний тариф."

// elevenErr classifies a failed ElevenLabs reply.
func elevenErr(r reply) *apiErr {
	code, _ := elevenDetail(r.body)
	body := string(r.body)
	switch {
	case reQuota.MatchString(code) || reQuota.MatchString(body):
		return &apiErr{kind: failQuota, msg: "ElevenLabs: закінчились символи на тарифі"}
	case code == "paid_plan_required" || code == "voice_not_available" ||
		(r.status == http.StatusPaymentRequired && reLibrary.MatchString(body)):
		return &apiErr{kind: failSkip, voice: true, msg: msgElevenPaidVoice}
	case r.status == http.StatusPaymentRequired:
		return &apiErr{kind: failQuota, msg: "ElevenLabs: закінчились кредити на тарифі"}
	case code == "detected_unusual_activity":
		return &apiErr{kind: failAuth, msg: "ElevenLabs вимкнув безкоштовний тариф цього ключа (незвична активність)"}
	case code == "missing_permissions" || code == "insufficient_permissions":
		// A valid key without one permission: it cannot do this, others may.
		perm := elevenPermission(body)
		ae := &apiErr{kind: failSkip, msg: "ElevenLabs: ключ працює, але йому бракує дозволу «" + firstNonEmpty(perm, "доступ") + "» — увімкни його в налаштуваннях ключа на elevenlabs.io"}
		if perm == "" || strings.Contains(perm, "text_to_speech") {
			ae.kind = failAuth // it can never voice: rest it like a rejected key
		}
		return ae
	case r.status == http.StatusUnauthorized:
		return &apiErr{kind: failAuth, msg: "ElevenLabs: невірний API-ключ"}
	case code == "voice_access_denied":
		return &apiErr{kind: failSkip, voice: true, msg: "ElevenLabs: акаунт цього ключа не має доступу до голосу (його обмежив власник або потрібен вищий тариф) — вибери інший голос"}
	case code == "model_access_denied":
		return &apiErr{kind: failSkip, msg: "ElevenLabs: модель недоступна на тарифі ключа — вибери іншу модель у Налаштуваннях"}
	case code == "feature_not_available" || code == "subscription_required":
		return &apiErr{kind: failSkip, msg: "ElevenLabs: ця можливість недоступна на тарифі ключа"}
	case r.status == http.StatusForbidden:
		return &apiErr{kind: failAuth, msg: "ElevenLabs: ключ не має дозволу на озвучку (Text to Speech) — увімкни його в налаштуваннях ключа на elevenlabs.io"}
	case code == "voice_not_found" || code == "voice_does_not_exist" ||
		(r.status == http.StatusNotFound && strings.Contains(code+body, "voice")):
		return &apiErr{kind: failSkip, voice: true, msg: "ElevenLabs: голос не знайдено в акаунті цього ключа — вибери інший голос"}
	case code == "model_not_found" || code == "unsupported_model":
		return fatal("ElevenLabs: модель не знайдено або вона не озвучує текст — перевір модель у Налаштуваннях → Озвучка")
	case code == "text_too_long":
		return fatal("ElevenLabs: текст репліки задовгий для цієї моделі — розбий сцену на коротші")
	case r.status == http.StatusTooManyRequests:
		return &apiErr{kind: failRate, retry: r.retry, msg: "ElevenLabs: забагато запитів одночасно"}
	case r.status >= 500:
		return &apiErr{kind: failTransient, msg: fmt.Sprintf("ElevenLabs %d: %s", r.status, clip(body, 240))}
	}
	return fatal(fmt.Sprintf("ElevenLabs %d: %s", r.status, clip(body, 240)))
}

type elVoice struct {
	VoiceID    string         `json:"voice_id"`
	Name       string         `json:"name"`
	Category   string         `json:"category"` // generated | cloned | premade | professional | famous | high_quality
	Labels     map[string]any `json:"labels"`
	PreviewURL string         `json:"preview_url"`
	IsOwner    *bool          `json:"is_owner"` // nullable
}

// elevenLibrary: a voice taken from the Voice Library (shared by someone
// else) — neither a default voice nor the account's own.
func elevenLibrary(v elVoice) bool {
	switch v.Category {
	case "premade", "":
		return false
	}
	if v.IsOwner != nil {
		return !*v.IsOwner
	}
	return v.Category == "professional" || v.Category == "famous" || v.Category == "high_quality"
}

// elSub is GET /v1/user/subscription.
type elSub struct {
	Tier           string `json:"tier"`
	Status         string `json:"status"` // trialing | active | incomplete | past_due | free | free_disabled
	CharacterCount *int   `json:"character_count"`
	CharacterLimit *int   `json:"character_limit"`
	CanExtend      bool   `json:"can_extend_character_limit"`
	// MaxExtension replaces the deprecated allowed_to_extend_character_limit:
	// usage may go past the limit when it is not 0.
	MaxExtension  *int `json:"max_credit_limit_extension"`
	AllowedExtend bool `json:"allowed_to_extend_character_limit"`
}

// extends: usage may run past the limit (usage-based billing).
func (sj elSub) extends() bool {
	if !sj.CanExtend {
		return false
	}
	if sj.MaxExtension != nil {
		return *sj.MaxExtension != 0
	}
	return sj.AllowedExtend
}

var rePermission = regexp.MustCompile(`(?i)permissions?\s+['"«]?([a-z_]+)`)

// elevenPermission is the permission a refusal names ("user_read"; "" = none).
func elevenPermission(body string) string {
	_, msg := elevenDetail([]byte(body))
	if m := rePermission.FindStringSubmatch(firstNonEmpty(msg, body)); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

// currentElevenModel maps a retired model id to its successor
// (eleven_turbo_v2_5 is deprecated: Flash v2.5 is the same model).
func currentElevenModel(model string) string {
	if model == "eleven_turbo_v2_5" {
		return "eleven_flash_v2_5"
	}
	return model
}

// ElevenDefaultVoicesEnd is when ElevenLabs retires its default (premade)
// voices ("All our Default voices will expire on December 31, 2026").
var ElevenDefaultVoicesEnd = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

// plan is the account's plan in lower case ("free" also for status free).
func (sj elSub) plan() string {
	switch strings.ToLower(strings.TrimSpace(sj.Status)) {
	case "free", "free_disabled":
		return "free"
	}
	return strings.ToLower(strings.TrimSpace(sj.Tier))
}

// usage is (used, limit, spent?) of the subscription; used -1 = unknown.
func (sj elSub) usage() (used, limit int, spent *bool) {
	if sj.CharacterLimit == nil {
		return -1, 0, nil
	}
	limit = *sj.CharacterLimit
	used = 0
	if sj.CharacterCount != nil {
		used = *sj.CharacterCount
	}
	if limit <= 0 {
		return used, 0, nil
	}
	v := used >= limit && !sj.extends()
	return used, limit, &v
}

func (c *Client) elevenVoices(ctx context.Context, s settings) (VoiceList, error) {
	var list VoiceList
	var sub elSub
	var subOK bool
	var used poolKey
	err := c.run(ctx, s, "", 0, true, func(key string) (int, error) {
		hdr := map[string]string{"xi-api-key": key}
		type subReply struct {
			res reply
			err error
		}
		subCh := make(chan subReply, 1)
		go func() {
			r, err := c.do(ctx, http.MethodGet, elevenBase+"/v1/user/subscription", hdr, nil, listTimeout, 1<<20)
			subCh <- subReply{r, err}
		}()
		raw, err := c.voicePages(ctx, hdr)
		sr := <-subCh
		if err != nil {
			return 0, err
		}
		sub, subOK = elSub{}, false
		if sr.err == nil && sr.res.ok() && json.Unmarshal(sr.res.body, &sub) == nil {
			subOK = true
		}
		for _, k := range s.keys {
			if k.key == key {
				used = k
			}
		}
		plan := ""
		if subOK {
			plan = sub.plan()
			c.pool.setPlan(s, used, plan)
		}
		list.Tier = c.elevenPlan(s, used, plan)
		list.Voices = elevenVoiceList(raw, list.Tier == "free" && !c.anyPaidKey(s, used))
		return 0, nil
	})
	if err != nil {
		return VoiceList{}, err
	}
	list.CharsLeft = -1
	if subOK {
		if n, limit, spent := sub.usage(); limit > 0 {
			list.CharsLeft = max(0, limit-n)
			c.pool.report(s, used, n, limit, spent, "ElevenLabs: закінчились символи на тарифі")
		}
	}
	return list, nil
}

// elevenPlan is the plan of the key that listed the voices: the fresh
// subscription, else the one remembered from an earlier read or check.
func (c *Client) elevenPlan(s settings, k poolKey, fresh string) string {
	if fresh != "" {
		return fresh
	}
	return c.pool.plans(s)[k.id]
}

// anyPaidKey: another usable key is known to be on a paid plan (it can voice
// library voices the listing key's free account cannot).
func (c *Client) anyPaidKey(s settings, except poolKey) bool {
	for id, plan := range c.pool.plans(s) {
		if id != except.id && plan != "free" {
			return true
		}
	}
	return false
}

// LockedPaidEleven marks a voice a free ElevenLabs account cannot use.
const LockedPaidEleven = "потрібен платний тариф ElevenLabs"

func elevenVoiceList(raw []elVoice, free bool) []Voice {
	voices := make([]Voice, 0, len(raw))
	for _, v := range raw {
		name := strings.TrimSpace(strings.SplitN(v.Name, " - ", 2)[0])
		if name == "" {
			name = v.VoiceID
		}
		vo := Voice{
			ID:         v.VoiceID,
			Name:       name,
			Category:   v.Category,
			Gender:     str(v.Labels["gender"]),
			Age:        str(v.Labels["age"]),
			Accent:     str(v.Labels["accent"]),
			PreviewURL: v.PreviewURL,
			Usable:     true,
		}
		if elevenLibrary(v) {
			vo.Category = "library"
			if free {
				vo.Usable, vo.Locked = false, LockedPaidEleven
			}
		}
		if v.Category == "premade" && time.Now().Before(ElevenDefaultVoicesEnd) {
			vo.Note = "доступний до 31 грудня 2026"
		}
		voices = append(voices, vo)
	}
	sortVoices(voices, "")
	return voices
}

// voicePages reads GET /v2/voices, following next_page_token for big libraries.
func (c *Client) voicePages(ctx context.Context, hdr map[string]string) ([]elVoice, error) {
	var all []elVoice
	token := ""
	for page := 0; page < voicePages; page++ {
		q := url.Values{"page_size": {"100"}}
		if token != "" {
			q.Set("next_page_token", token)
		}
		res, err := c.do(ctx, http.MethodGet, elevenBase+"/v2/voices?"+q.Encode(), hdr, nil, listTimeout, 16<<20)
		if err != nil {
			return nil, netErr(ctx, "ElevenLabs", err)
		}
		if !res.ok() {
			return nil, elevenErr(res)
		}
		var vj struct {
			Voices        []elVoice `json:"voices"`
			HasMore       bool      `json:"has_more"`
			NextPageToken string    `json:"next_page_token"`
		}
		if err := json.Unmarshal(res.body, &vj); err != nil {
			return nil, fatal(fmt.Sprintf("ElevenLabs: незрозуміла відповідь списку голосів: %s", clip(string(res.body), 200)))
		}
		all = append(all, vj.Voices...)
		if !vj.HasMore || vj.NextPageToken == "" || vj.NextPageToken == token {
			break
		}
		token = vj.NextPageToken
	}
	return all, nil
}

// elevenProbe: GET /v1/user/subscription (tier and characters; a key limited
// to text-to-speech cannot read it and is still valid).
func (c *Client) elevenProbe(ctx context.Context, s settings, key string) probeResult {
	out := probeResult{used: -1}
	res, err := c.do(ctx, http.MethodGet, elevenBase+"/v1/user/subscription", map[string]string{"xi-api-key": key}, nil, listTimeout, 1<<20)
	if err != nil {
		out.err = kindOf(netErr(ctx, "ElevenLabs", err))
		return out
	}
	if !res.ok() {
		if code, _ := elevenDetail(res.body); code == "missing_permissions" || code == "insufficient_permissions" ||
			strings.Contains(string(res.body), "missing_permissions") {
			// Valid, only not allowed to read the account: no plan, no usage.
			out.info = "ключ працює, бракує дозволу «" + firstNonEmpty(elevenPermission(string(res.body)), "user_read") + "» — тариф і ліміт не видно"
			return out
		}
		out.err = elevenErr(res)
		return out
	}
	var sub elSub
	_ = json.Unmarshal(res.body, &sub)
	out.plan = sub.plan()
	var parts []string
	if t := out.plan; t != "" {
		parts = append(parts, "тариф "+t)
	}
	if n, limit, spent := sub.usage(); limit > 0 {
		out.used, out.limit, out.quota, out.quotaMsg = n, limit, spent, "ElevenLabs: закінчились символи на тарифі"
		left := max(0, limit-n)
		parts = append(parts, fmt.Sprintf("залишилось %s %s", thousands(left), plural(left, "символ", "символи", "символів")))
	}
	out.info = strings.Join(parts, " · ")
	return out
}

// thousands is 81234 → "81 234".
func thousands(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}
