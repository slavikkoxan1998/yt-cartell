package tts

// Azure AI Speech (REST): POST {endpoint}/cognitiveservices/v1 with SSML,
// GET {endpoint}/cognitiveservices/voices/list — Ocp-Apim-Subscription-Key,
// X-Microsoft-OutputFormat audio-48khz-192kbitrate-mono-mp3
// (https://learn.microsoft.com/azure/ai-services/speech-service/rest-text-to-speech).
// The URL field holds the resource's region ("westeurope") or an endpoint URL.
// Voices are locale-bound ("uk-UA-PolinaNeural", "uk-UA-OstapNeural");
// Multilingual / DragonHD voices speak other languages through <lang xml:lang>.
// A voice of another language gets replaced by a same-gender voice of the
// film's language. Docs (2026-10): a User-Agent is required; ≤ 10 min of audio
// per request; F0 = 0.5M characters a month and 20 requests a minute. Errors:
// 400, 401 key or region, 415, 429 "exceeded the quota or rate", 502, 503 —
// a 429 is a rate limit unless the body names the monthly quota (it resets at
// the start of the month); 403 "Out of call volume quota" (other F0 services)
// → quota. The voices list is read from the regional host (the docs now show
// the resource host, which this field does not know; the regional one works).

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	azureFormat = "audio-48khz-192kbitrate-mono-mp3"
	azureMulti  = "en-US-AndrewMultilingualNeural"
)

var (
	reAzureLocale = regexp.MustCompile(`^([a-z]{2,3}-[A-Z]{2})-`)
	// A body about the quota is the monthly quota unless it speaks of a rate
	// ("the quota or rate", "20 transactions per 60 seconds").
	reQuotaWord = regexp.MustCompile(`(?i)quota`)
	reRateWord  = regexp.MustCompile(`(?i)\brate\b|per (second|minute|60 seconds)|transactions|concurren`)
)

// azureBase is the endpoint of the configured region / URL ("" = not set).
// A resource host (https://<name>.cognitiveservices.azure.com) serves text
// to speech under /tts: /tts/cognitiveservices/voices/list, /tts/cognitiveservices/v1.
func azureBase(s settings) string {
	b := strings.TrimRight(strings.TrimSpace(s.base), "/")
	if b == "" {
		return ""
	}
	if !strings.Contains(b, "://") {
		return azureEndpoint(strings.ToLower(b))
	}
	if u, err := url.Parse(b); err == nil && strings.HasSuffix(strings.ToLower(u.Hostname()), ".cognitiveservices.azure.com") &&
		!strings.HasSuffix(strings.ToLower(u.Path), "/tts") {
		return b + "/tts"
	}
	return b
}

func azureHeaders(key string) map[string]string {
	return map[string]string{"Ocp-Apim-Subscription-Key": key, "User-Agent": "YT-Cartell-Studio"}
}

func azureLocaleOf(voice string) string {
	if m := reAzureLocale.FindStringSubmatch(voice); m != nil {
		return m[1]
	}
	return ""
}

func azureMultilingual(voice string) bool {
	v := strings.ToLower(voice)
	return strings.Contains(v, "multilingual") || strings.Contains(v, "dragonhd")
}

// azureVoice picks the voice for lang ("" = unknown) and the <lang> locale
// to wrap the text in ("" = none).
func (c *Client) azureVoice(s settings, voice, lang string) (name, langTag string) {
	target := localeFor("azure", lang)
	if voice == "" {
		voice = azureMulti // detects the language of the text itself
		if lang == "uk" {
			voice = firstNonEmpty(s.info.DefaultVoice, voice)
		}
	}
	vloc := azureLocaleOf(voice)
	switch {
	case target == "" || vloc == "" || langOfLocale(vloc) == lang:
		return voice, ""
	case azureMultilingual(voice):
		return voice, target
	}
	if alt := c.azureAlternative(s, voice, lang); alt != "" {
		return alt, ""
	}
	return azureMulti, target
}

// azureAlternative is a voice of lang with the gender of voice (the cached
// voice list; Ukrainian is known without it).
func (c *Client) azureAlternative(s settings, voice, lang string) string {
	list := c.cachedVoices(s)
	g := ""
	for _, v := range list {
		if v.ID == voice {
			g = v.Gender
		}
	}
	var any string
	for _, v := range list {
		if langOfLocale(v.Language) != lang || azureMultilingual(v.ID) {
			continue
		}
		if g != "" && v.Gender == g {
			return v.ID
		}
		any = firstNonEmpty(any, v.ID)
	}
	if lang == "uk" {
		if g == "female" {
			return "uk-UA-PolinaNeural"
		}
		return "uk-UA-OstapNeural"
	}
	return any
}

// azureSSML is the request body: the voice, optional <lang> and speed.
func azureSSML(voice, langTag, text string, speed float64) []byte {
	var t bytes.Buffer
	_ = xml.EscapeText(&t, []byte(text))
	inner := t.String()
	if speed > 0 && speed != 1 {
		pct := int((orDefault(speed, 1, 0.5, 2) - 1) * 100)
		inner = fmt.Sprintf(`<prosody rate="%+d%%">%s</prosody>`, pct, inner)
	}
	if langTag != "" {
		inner = fmt.Sprintf(`<lang xml:lang="%s">%s</lang>`, langTag, inner)
	}
	loc := firstNonEmpty(azureLocaleOf(voice), "en-US")
	var v bytes.Buffer
	_ = xml.EscapeText(&v, []byte(voice))
	return []byte(fmt.Sprintf(`<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xml:lang="%s"><voice name="%s">%s</voice></speak>`, loc, v.String(), inner))
}

func (c *Client) azure(ctx context.Context, s settings, text, voice string, opt SynthOptions) ([]byte, error) {
	if len(s.keys) == 0 {
		return nil, errNoKey(s)
	}
	base := azureBase(s)
	if base == "" {
		return nil, fmt.Errorf("Azure: вкажи регіон ресурсу Speech у полі адреси (напр. westeurope)")
	}
	lang := filmLanguage(opt.LanguageCode, text)
	if lang != "" && !c.voicesCached(s) && voice != "" && !azureMultilingual(voice) && langOfLocale(azureLocaleOf(voice)) != lang {
		_, _ = c.Voices(ctx, false) // the list tells the voice's gender for a same-gender replacement
	}
	name, langTag := c.azureVoice(s, voice, lang)
	body := azureSSML(name, langTag, text, opt.Speed)
	var out []byte
	err := c.run(ctx, s, voice, runes(text), false, func(key string) (int, error) {
		hdr := azureHeaders(key)
		hdr["Content-Type"] = "application/ssml+xml"
		hdr["X-Microsoft-OutputFormat"] = azureFormat
		res, err := c.do(ctx, http.MethodPost, base+"/cognitiveservices/v1", hdr, body, synthTimeout, maxAudio)
		if err != nil {
			return 0, netErr(ctx, "Azure", err)
		}
		if !res.ok() {
			return 0, azureErr(res)
		}
		out, err = audio(res, "Azure")
		return 0, err
	})
	return out, err
}

func (c *Client) voicesCached(s settings) bool { return len(c.cachedVoices(s)) > 0 }

// azureErr classifies a failed Azure Speech reply (bodies are short texts).
func azureErr(r reply) *apiErr {
	body := clip(string(r.body), 200)
	switch {
	case r.status == http.StatusUnauthorized:
		return &apiErr{kind: failAuth, msg: "Azure: ключ або регіон не підходять (401) — перевір ключ і регіон ресурсу Speech"}
	case (r.status == http.StatusForbidden || r.status == http.StatusTooManyRequests) && reQuotaWord.MatchString(body) && !reRateWord.MatchString(body):
		return &apiErr{kind: failQuota, msg: "Azure: вичерпано місячний ліміт ресурсу (безкоштовний тариф F0)"}
	case r.status == http.StatusForbidden:
		return &apiErr{kind: failAuth, msg: "Azure: доступ заборонено (403)" + prefixed(": ", body)}
	case r.status == http.StatusTooManyRequests:
		return &apiErr{kind: failRate, retry: r.retry, msg: "Azure: забагато запитів (429)"}
	case r.status == http.StatusNotFound:
		return fatal("Azure: адресу не знайдено (404) — перевір регіон у Налаштуваннях")
	case r.status == http.StatusBadRequest:
		return fatal("Azure: сервіс не прийняв запит (400) — перевір голос" + prefixed(": ", body))
	case r.status >= 500:
		return &apiErr{kind: failTransient, msg: fmt.Sprintf("Azure %d: %s", r.status, body)}
	}
	return fatal(fmt.Sprintf("Azure %d: %s", r.status, body))
}

type azureVoiceJSON struct {
	ShortName           string   `json:"ShortName"`
	DisplayName         string   `json:"DisplayName"`
	LocalName           string   `json:"LocalName"`
	Gender              string   `json:"Gender"`
	Locale              string   `json:"Locale"`
	SecondaryLocaleList []string `json:"SecondaryLocaleList"`
	Status              string   `json:"Status"`
}

func (c *Client) azureVoices(ctx context.Context, s settings) (VoiceList, error) {
	base := azureBase(s)
	if base == "" {
		return VoiceList{}, fmt.Errorf("Azure: вкажи регіон ресурсу Speech у полі адреси (напр. westeurope)")
	}
	var raw []azureVoiceJSON
	err := c.run(ctx, s, "", 0, true, func(key string) (int, error) {
		res, err := c.do(ctx, http.MethodGet, base+"/cognitiveservices/voices/list", azureHeaders(key), nil, listTimeout, 32<<20)
		if err != nil {
			return 0, netErr(ctx, "Azure", err)
		}
		if !res.ok() {
			return 0, azureErr(res)
		}
		if err := json.Unmarshal(res.body, &raw); err != nil {
			return 0, fatal("Azure: незрозуміла відповідь списку голосів: " + clip(string(res.body), 200))
		}
		return 0, nil
	})
	if err != nil {
		return VoiceList{}, err
	}
	voices := make([]Voice, 0, len(raw))
	for _, v := range raw {
		if v.ShortName == "" {
			continue
		}
		lang := v.Locale
		if azureMultilingual(v.ShortName) {
			lang = "" // speaks the film's language
		}
		voices = append(voices, Voice{
			ID: v.ShortName, Name: firstNonEmpty(v.DisplayName, v.LocalName, v.ShortName), Category: "premade",
			Gender: gender(v.Gender), Accent: accentOf(v.Locale), Language: lang, Usable: true,
		})
	}
	sortVoices(voices, "uk")
	return VoiceList{CharsLeft: -1, Voices: voices}, nil
}

// azureProbe: GET voices/list (authenticated, free).
func (c *Client) azureProbe(ctx context.Context, s settings, key string) probeResult {
	out := probeResult{used: -1}
	base := azureBase(s)
	res, err := c.do(ctx, http.MethodGet, base+"/cognitiveservices/voices/list", azureHeaders(key), nil, listTimeout, 32<<20)
	if err != nil {
		out.err = kindOf(netErr(ctx, "Azure", err))
		return out
	}
	if !res.ok() {
		out.err = azureErr(res)
		return out
	}
	var raw []azureVoiceJSON
	_ = json.Unmarshal(res.body, &raw)
	uk := 0
	for _, v := range raw {
		if strings.HasPrefix(v.Locale, "uk-") {
			uk++
		}
	}
	out.info = fmt.Sprintf("регіон %s · %d %s, українських %d", strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://"), len(raw), plural(len(raw), "голос", "голоси", "голосів"), uk)
	return out
}
