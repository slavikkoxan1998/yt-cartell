package tts

// Google Cloud Text-to-Speech: POST /v1/text:synthesize, GET /v1/voices with
// an API key (x-goog-api-key) — https://docs.cloud.google.com/text-to-speech/docs/chirp3-hd.
// Model = the voice family (Chirp3-HD default, Chirp-HD, Neural2, Wavenet,
// Studio, Standard). Chirp 3 HD has the same 30 personas in 50+ locales
// (uk-UA among them), so a persona id ("Chirp3-HD-Charon") — or a Chirp 3 HD
// voice of another locale — speaks the film's language: the locale prefix
// follows SynthOptions.LanguageCode. Other families keep their own locale.
// Without TTS keys the Google AI Studio keys are tried (they work when the
// key's project has the Cloud TTS API and billing enabled).
// Errors {"error":{code,message,status,details[{reason}]}}: API_KEY_INVALID,
// SERVICE_DISABLED, BILLING_DISABLED, API_KEY_SERVICE_BLOCKED → the key;
// status 429 RESOURCE_EXHAUSTED (reasons RATE_LIMIT_EXCEEDED /
// RESOURCE_QUOTA_EXCEEDED) is a per-minute (or per-day) rate quota — Google
// bills beyond the free tier, so no monthly exhaustion. Limits (docs 2026-10):
// 5000 bytes of input per request (≈ 2500 Cyrillic letters), Chirp 3 HD 200
// requests a minute; free each month: 1M characters of Chirp 3 HD / Neural2 /
// Studio, 4M of WaveNet / Standard. All 30 Chirp 3 HD personas exist for uk-UA.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var (
	reGoogleLocale  = regexp.MustCompile(`^([a-z]{2,3}-[A-Z]{2})-(.+)$`)
	reGoogleNoVoice = regexp.MustCompile(`(?i)voice.*(does not exist|not found|not available|unsupported)|(does not exist|not found|not available|unsupported).*voice`)
)

func googleHeaders(key string) map[string]string { return map[string]string{"x-goog-api-key": key} }

// personaFamily: families whose voices exist under the same name in every locale.
func personaFamily(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "chirp3-hd") || strings.Contains(n, "chirp-hd")
}

// googleVoice resolves the voice name and languageCode of a request; built
// tells that the name was made for the film's locale (a persona), so a locale
// without that persona may fall back to its default voice.
func googleVoice(voice, family, lang string) (name, languageCode string, built bool) {
	target := localeFor("google", lang)
	if voice == "" && personaFamily(family) {
		voice = "Chirp3-HD-Charon"
	}
	if voice == "" {
		return "", firstNonEmpty(target, "en-US"), false
	}
	if m := reGoogleLocale.FindStringSubmatch(voice); m != nil {
		loc, rest := m[1], m[2]
		if target != "" && personaFamily(rest) && langOfLocale(target) != langOfLocale(loc) {
			return target + "-" + rest, target, true
		}
		return voice, loc, false
	}
	loc := firstNonEmpty(target, "en-US")
	return loc + "-" + voice, loc, personaFamily(voice) // an unknown id (another provider's) fails plainly
}

func (c *Client) google(ctx context.Context, s settings, text, voice string, opt SynthOptions) ([]byte, error) {
	if len(s.keys) == 0 {
		return nil, errNoKey(s)
	}
	family := firstNonEmpty(opt.Model, s.model, s.info.DefaultModel)
	name, lc, built := googleVoice(voice, family, filmLanguage(opt.LanguageCode, text))
	vsel := map[string]any{"languageCode": lc}
	if name != "" {
		vsel["name"] = name
	}
	acfg := map[string]any{"audioEncoding": "MP3"}
	if opt.Speed > 0 && opt.Speed != 1 {
		acfg["speakingRate"] = orDefault(opt.Speed, 1, 0.25, 2)
	}
	payload := map[string]any{"input": map[string]any{"text": text}, "voice": vsel, "audioConfig": acfg}
	body, _ := json.Marshal(payload)
	var out []byte
	err := c.run(ctx, s, voice, runes(text), false, func(key string) (int, error) {
		for {
			res, err := c.do(ctx, http.MethodPost, googleBase+"/v1/text:synthesize", googleHeaders(key), body, synthTimeout, maxAudio*2)
			if err != nil {
				return 0, netErr(ctx, "Google TTS", err)
			}
			if !res.ok() {
				if built && res.status == http.StatusBadRequest && reGoogleNoVoice.Match(res.body) {
					// This persona has no voice in the film's locale: any voice of it.
					built = false
					delete(vsel, "name")
					body, _ = json.Marshal(payload)
					continue
				}
				return 0, googleErr(res)
			}
			var j struct {
				AudioContent string `json:"audioContent"`
			}
			if err := json.Unmarshal(res.body, &j); err != nil || j.AudioContent == "" {
				return 0, fatal("Google TTS: у відповіді немає аудіо: " + clip(string(res.body), 160))
			}
			b, err := decodeB64(j.AudioContent)
			if err != nil {
				return 0, fatal("Google TTS: аудіо у відповіді пошкоджене (base64)")
			}
			out = b
			return 0, nil
		}
	})
	return out, err
}

// googleErr classifies a failed Google API reply.
func googleErr(r reply) *apiErr {
	var j struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.body, &j)
	reason := ""
	for _, d := range j.Error.Details {
		if reason = d.Reason; reason != "" {
			break
		}
	}
	msg := firstNonEmpty(j.Error.Message, clip(string(r.body), 200))
	low := strings.ToLower(msg)
	switch {
	case reason == "API_KEY_INVALID" || strings.Contains(low, "api key not valid") || strings.Contains(low, "api key expired"):
		return &apiErr{kind: failAuth, msg: "Google TTS: невірний або прострочений API-ключ"}
	case reason == "SERVICE_DISABLED" || strings.Contains(low, "has not been used in project") || strings.Contains(low, "it is disabled"):
		return &apiErr{kind: failAuth, msg: "Google TTS: у проєкті цього ключа вимкнено Cloud Text-to-Speech API — увімкни його в Google Cloud Console"}
	case reason == "BILLING_DISABLED" || strings.Contains(low, "billing"):
		return &apiErr{kind: failAuth, msg: "Google TTS: для Cloud Text-to-Speech у проєкті ключа потрібен увімкнений білінг"}
	case strings.HasPrefix(reason, "API_KEY_"):
		return &apiErr{kind: failAuth, msg: "Google TTS: ключ обмежений іншими API — дозволь для нього Cloud Text-to-Speech API або додай окремий ключ"}
	case r.status == http.StatusUnauthorized || r.status == http.StatusForbidden || j.Error.Status == "PERMISSION_DENIED" || j.Error.Status == "UNAUTHENTICATED":
		return &apiErr{kind: failAuth, msg: fmt.Sprintf("Google TTS: ключ не має доступу (%d): %s", r.status, clip(msg, 160))}
	case r.status == http.StatusBadRequest && (strings.Contains(low, "5000 bytes") || strings.Contains(low, "too long")):
		return fatal("Google TTS: текст репліки задовгий — до 5000 байтів за раз (≈ 2500 українських літер); розбий сцену на коротші")
	case r.status == http.StatusTooManyRequests || j.Error.Status == "RESOURCE_EXHAUSTED" ||
		reason == "RATE_LIMIT_EXCEEDED" || reason == "RESOURCE_QUOTA_EXCEEDED":
		ae := &apiErr{kind: failRate, retry: r.retry, msg: "Google TTS: перевищено ліміт запитів"}
		if strings.Contains(low, "per day") {
			ae.daily, ae.msg = true, "Google TTS: вичерпано денний ліміт запитів"
		}
		return ae
	case r.status >= 500:
		return &apiErr{kind: failTransient, msg: fmt.Sprintf("Google TTS %d: %s", r.status, clip(msg, 200))}
	}
	return fatal(fmt.Sprintf("Google TTS %d: %s", r.status, clip(msg, 200)))
}

type googleVoiceJSON struct {
	LanguageCodes []string `json:"languageCodes"`
	Name          string   `json:"name"`
	SSMLGender    string   `json:"ssmlGender"`
}

func (c *Client) googleVoices(ctx context.Context, s settings) (VoiceList, error) {
	var raw []googleVoiceJSON
	err := c.run(ctx, s, "", 0, true, func(key string) (int, error) {
		res, err := c.do(ctx, http.MethodGet, googleBase+"/v1/voices", googleHeaders(key), nil, listTimeout, 16<<20)
		if err != nil {
			return 0, netErr(ctx, "Google TTS", err)
		}
		if !res.ok() {
			return 0, googleErr(res)
		}
		var j struct {
			Voices []googleVoiceJSON `json:"voices"`
		}
		if err := json.Unmarshal(res.body, &j); err != nil {
			return 0, fatal("Google TTS: незрозуміла відповідь списку голосів: " + clip(string(res.body), 200))
		}
		raw = j.Voices
		return 0, nil
	})
	if err != nil {
		return VoiceList{}, err
	}
	return VoiceList{CharsLeft: -1, Voices: googleVoiceList(raw, firstNonEmpty(s.model, s.info.DefaultModel))}, nil
}

// googleVoiceList keeps the voices of the family; Chirp 3 HD / Chirp HD
// collapse into locale-free personas that follow the film language.
func googleVoiceList(raw []googleVoiceJSON, family string) []Voice {
	fam := strings.ToLower(family)
	persona := personaFamily(family)
	seen := map[string]bool{}
	voices := []Voice{}
	for _, v := range raw {
		m := reGoogleLocale.FindStringSubmatch(v.Name)
		if m == nil {
			continue
		}
		loc, rest := m[1], m[2]
		famPart, who, _ := strings.Cut(rest, "-")
		if strings.Contains(rest, "-") && strings.Count(rest, "-") >= 2 { // "Chirp3-HD-Charon"
			i := strings.LastIndex(rest, "-")
			famPart, who = rest[:i], rest[i+1:]
		}
		if fam != "" && !strings.EqualFold(famPart, fam) {
			continue
		}
		g := gender(v.SSMLGender)
		if persona {
			id := famPart + "-" + who
			if seen[id] {
				continue
			}
			seen[id] = true
			voices = append(voices, Voice{ID: id, Name: who, Category: "premade", Gender: g, Usable: true})
			continue
		}
		voices = append(voices, Voice{ID: v.Name, Name: rest, Category: "premade", Gender: g, Accent: accentOf(loc), Language: loc, Usable: true})
	}
	sortVoices(voices, "uk")
	return voices
}

// googleProbe: GET /v1/voices?languageCode=uk-UA (authenticated, free).
func (c *Client) googleProbe(ctx context.Context, s settings, key string) probeResult {
	out := probeResult{used: -1}
	res, err := c.do(ctx, http.MethodGet, googleBase+"/v1/voices?"+url.Values{"languageCode": {"uk-UA"}}.Encode(), googleHeaders(key), nil, listTimeout, 4<<20)
	if err != nil {
		out.err = kindOf(netErr(ctx, "Google TTS", err))
		return out
	}
	if !res.ok() {
		out.err = googleErr(res)
		return out
	}
	var j struct {
		Voices []googleVoiceJSON `json:"voices"`
	}
	_ = json.Unmarshal(res.body, &j)
	n := 0
	fam := strings.ToLower(firstNonEmpty(s.model, s.info.DefaultModel))
	for _, v := range j.Voices {
		if strings.Contains(strings.ToLower(v.Name), "-"+fam+"-") {
			n++
		}
	}
	out.info = fmt.Sprintf("%s: %d %s для української", firstNonEmpty(s.model, s.info.DefaultModel), n, plural(n, "голос", "голоси", "голосів"))
	return out
}
