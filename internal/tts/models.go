package tts

// ElevenLabs model choice by language.
//
// Settings may pin a model (cfg.TTS.Model). Otherwise the model is chosen per
// language from the account's GET /v1/models (cached 1 h; each model lists
// languages[{language_id, name}] and can_do_text_to_speech):
//
//	""        → eleven_multilingual_v2 (it detects the language itself)
//	language  → eleven_multilingual_v2 when it speaks it, else eleven_v3, else
//	            eleven_v4 (only when the account lists it), else Flash/Turbo v2.5
//
// When /v1/models is unavailable the documented language tables below stand in.
// language_code (ISO 639-1) is sent only to models that enforce a language —
// the API docs: "This parameter is not supported for multilingual_v2 models."

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const defaultElevenModel = "eleven_multilingual_v2"

var (
	modelsTTL      = time.Hour
	modelsRetryTTL = 2 * time.Minute // after a failed /v1/models (network, 401…)
)

// autoModels is the preference order of the automatic choice
// (eleven_turbo_v2_5 is deprecated: Flash v2.5 is the same model).
var autoModels = []string{defaultElevenModel, "eleven_v3", "eleven_v4", "eleven_flash_v2_5"}

type modelCache struct {
	at   time.Time
	key  string
	ttl  time.Duration
	list []elModel // nil: /v1/models unavailable → documented tables
}

type elModel struct {
	ID        string `json:"model_id"`
	Name      string `json:"name"`
	CanTTS    *bool  `json:"can_do_text_to_speech"`
	Languages []struct {
		ID   string `json:"language_id"`
		Name string `json:"name"`
	} `json:"languages"`
}

// ModelFor picks the ElevenLabs model for text in language (ISO 639-1, "" =
// unknown) and the language_code to send with it ("" = do not send). A model
// set in Settings always wins. Other providers get their model (Settings, else
// the preset's default) and the language as their API receives it ("" = none).
func (c *Client) ModelFor(ctx context.Context, language string) (modelID, languageCode string) {
	s := c.settings()
	code := langCode(language)
	switch s.provider {
	case "elevenlabs":
	case "openai":
		return firstNonEmpty(s.model, openAIModel(s)), ""
	case "cartesia":
		return firstNonEmpty(s.model, s.info.DefaultModel), cartesiaLangs[code]
	case "google", "azure":
		return firstNonEmpty(s.model, s.info.DefaultModel), localeFor(s.provider, code)
	case "minimax":
		lang := minimaxLangs[code]
		if lang == "" {
			lang = "auto"
		}
		return firstNonEmpty(s.model, s.info.DefaultModel), lang
	default: // fish (detects the language itself), custom
		return firstNonEmpty(s.model, s.info.DefaultModel), ""
	}
	if m := currentElevenModel(s.model); m != "" {
		return m, c.enforcedLanguage(ctx, s, m, code)
	}
	if code == "" {
		return defaultElevenModel, ""
	}
	list := c.elevenModels(ctx, s)
	for _, id := range autoModels {
		if speaks(list, id, code) {
			return id, enforcedCode(id, code)
		}
	}
	// Nobody lists the language: the most multilingual model, detecting it itself.
	if list == nil || listed(list, "eleven_v3") {
		return "eleven_v3", ""
	}
	return defaultElevenModel, ""
}

// enforcedLanguage is the language_code for a chosen model: only when the
// model enforces languages and speaks this one.
func (c *Client) enforcedLanguage(ctx context.Context, s settings, model, code string) string {
	if code == "" || enforcedCode(model, code) == "" {
		return ""
	}
	if !speaks(c.elevenModels(ctx, s), model, code) {
		return ""
	}
	return code
}

// enforcedCode is code when model accepts language_code (Eleven v3/v4,
// Flash/Turbo v2.5), else "".
func enforcedCode(model, code string) string {
	switch {
	case code == "":
		return ""
	case isV3(model), strings.HasPrefix(model, "eleven_v4"),
		model == "eleven_flash_v2_5", model == "eleven_turbo_v2_5":
		return code
	}
	return ""
}

func isV3(model string) bool { return strings.HasPrefix(model, "eleven_v3") }

// snapV3Stability: Eleven v3 accepts stability 0 (creative), 0.5 (natural) or 1 (robust).
func snapV3Stability(v float64) float64 {
	switch {
	case v < 0.25:
		return 0
	case v < 0.75:
		return 0.5
	}
	return 1
}

// speaks reports whether model id can voice language code: from the account
// list when there is one (a listed model with no languages falls back to the
// documented table), else from the documented table.
func speaks(list []elModel, id, code string) bool {
	if list != nil {
		m := find(list, id)
		if m == nil || (m.CanTTS != nil && !*m.CanTTS) {
			return false
		}
		if len(m.Languages) > 0 {
			for _, l := range m.Languages {
				if langCode(l.ID) == code || nameCode(l.Name) == code {
					return true
				}
			}
			return false
		}
	}
	for _, c := range documented[id] {
		if c == code {
			return true
		}
	}
	return false
}

func find(list []elModel, id string) *elModel {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func listed(list []elModel, id string) bool { return find(list, id) != nil }

// elevenModels returns the account's models (cached per API key — the first
// usable key of the pool), nil when they cannot be listed.
func (c *Client) elevenModels(ctx context.Context, s settings) []elModel {
	if len(s.keys) == 0 {
		return nil
	}
	key := c.pool.usable(s).key
	cached := func() ([]elModel, bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if m := c.models; m != nil && m.key == key && time.Since(m.at) < m.ttl {
			return m.list, true
		}
		return nil, false
	}
	if list, ok := cached(); ok {
		return list
	}
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()
	if list, ok := cached(); ok { // filled while we waited
		return list
	}
	res, err := c.do(ctx, http.MethodGet, elevenBase+"/v1/models", map[string]string{"xi-api-key": key}, nil, listTimeout, 8<<20)
	if err != nil && ctx.Err() != nil {
		return nil // cancelled: do not remember a failure that was ours
	}
	var list []elModel
	ttl := modelsTTL
	if err != nil || !res.ok() || !decodeModels(res.body, &list) {
		list, ttl = nil, modelsRetryTTL
	}
	c.mu.Lock()
	c.models = &modelCache{at: time.Now(), key: key, ttl: ttl, list: list}
	c.mu.Unlock()
	return list
}

// decodeModels accepts the documented array and a {"models":[…]} wrapper.
func decodeModels(b []byte, out *[]elModel) bool {
	if json.Unmarshal(b, out) == nil && len(*out) > 0 {
		return true
	}
	var wrapped struct {
		Models []elModel `json:"models"`
	}
	if json.Unmarshal(b, &wrapped) == nil && len(wrapped.Models) > 0 {
		*out = wrapped.Models
		return true
	}
	*out = nil
	return false
}

// ---- languages -------------------------------------------------------------------------

// NormalizeLanguage maps a language id as ElevenLabs or a user may write it
// (ISO 639-1 "uk", ISO 639-3 "ukr", a region form "pt-BR", a legacy code
// "iw", an English name "Ukrainian") to the catalogue code: ISO 639-1, or
// ISO 639-3 for languages without one ("fil", "yue", "ceb"). "" = unknown.
func NormalizeLanguage(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	if c == "" {
		return ""
	}
	if v, ok := langCanon[c]; ok {
		return v
	}
	c = strings.ReplaceAll(c, "_", "-")
	if base, _, ok := strings.Cut(c, "-"); ok {
		if v, ok := langCanon[base]; ok {
			return v
		}
	}
	return nameCode(code)
}

// langCode is NormalizeLanguage that keeps an unknown non-empty code as
// written (lower case): a new language still reaches the API.
func langCode(code string) string {
	if v := NormalizeLanguage(code); v != "" {
		return v
	}
	return strings.ToLower(strings.TrimSpace(code))
}

// nameCode maps a language name ("Portuguese, Brazil", "Fula/Pulaar",
// "Kurdish (Sorani)") to its code, "" when unknown.
func nameCode(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return ""
	}
	if v, ok := langCanon[n]; ok {
		return v
	}
	if i := strings.IndexAny(n, "(,"); i > 0 {
		if v, ok := langCanon[strings.TrimSpace(n[:i])]; ok {
			return v
		}
	}
	for _, part := range strings.Split(n, "/") {
		if v, ok := langCanon[strings.TrimSpace(part)]; ok {
			return v
		}
	}
	return ""
}

// languageAliases: catalogue code → other ids of the same language
// (ISO 639-3/639-2 codes, legacy codes, English names), "|"-separated.
var languageAliases = map[string]string{
	"af": "afr|afrikaans", "am": "amh|amharic", "ar": "ara|arabic", "hy": "hye|arm|armenian",
	"as": "asm|assamese", "ast": "asturian", "az": "aze|azerbaijani", "be": "bel|belarusian",
	"bn": "ben|bengali|bangla", "bs": "bos|bosnian", "bg": "bul|bulgarian", "my": "mya|bur|burmese|myanmar",
	"yue": "cantonese|chinese (cantonese)", "ca": "cat|catalan", "ceb": "cebuano", "ny": "nya|chichewa|nyanja",
	"zh": "zho|chi|cmn|chinese|mandarin|mandarin chinese|chinese (mandarin)|zh-cn|zh-hans",
	"hr": "hrv|croatian", "cs": "ces|cze|czech", "da": "dan|danish", "nl": "nld|dut|dutch",
	"en": "eng|english", "et": "est|ekk|estonian", "fil": "tl|tgl|filipino|tagalog", "fi": "fin|finnish",
	"fr": "fra|fre|french", "ff": "ful|fula|fulah|pulaar|fulfulde", "gl": "glg|galician", "ka": "kat|geo|georgian",
	"de": "deu|ger|german", "el": "ell|gre|greek", "gu": "guj|gujarati", "ha": "hau|hausa",
	"he": "heb|iw|hebrew", "hi": "hin|hindi", "hu": "hun|hungarian", "is": "isl|ice|icelandic",
	"id": "ind|in|indonesian", "ga": "gle|irish", "it": "ita|italian", "ja": "jpn|japanese",
	"jv": "jav|javanese", "kam": "kamba", "kn": "kan|kannada", "kk": "kaz|kazakh", "ko": "kor|korean",
	"ckb": "sorani|sorani kurdish|kurdish (sorani)|central kurdish", "ky": "kir|kirghiz|kyrgyz", "lo": "lao",
	"lv": "lav|lvs|latvian", "ln": "lin|lingala", "lt": "lit|lithuanian", "lg": "lug|luganda|ganda",
	"lb": "ltz|luxembourgish", "mk": "mkd|mac|macedonian", "ms": "msa|may|zsm|malay", "ml": "mal|malayalam",
	"mt": "mlt|maltese", "mi": "mri|mao|maori|māori", "mr": "mar|marathi", "mn": "mon|mongolian",
	"ne": "nep|npi|nepali", "no": "nor|nob|nno|nb|nn|norwegian|norwegian bokmål|norwegian bokmal",
	"oc": "oci|occitan", "or": "ori|ory|odia|oriya", "ps": "pus|pbt|pashto", "fa": "fas|per|pes|persian|farsi",
	"pl": "pol|polish", "pt": "por|portuguese", "pa": "pan|punjabi|panjabi", "ro": "ron|rum|romanian",
	"ru": "rus|russian", "sr": "srp|serbian", "sn": "sna|shona", "sd": "snd|sindhi", "sk": "slk|slo|slovak",
	"sl": "slv|slovenian|slovene", "so": "som|somali", "es": "spa|spanish", "sw": "swa|swh|swahili",
	"sv": "swe|swedish", "tg": "tgk|tajik", "ta": "tam|tamil", "te": "tel|telugu", "th": "tha|thai",
	"tr": "tur|turkish", "uk": "ukr|ukrainian", "ur": "urd|urdu", "uz": "uzb|uzbek", "vi": "vie|vietnamese",
	"cy": "cym|wel|welsh", "wo": "wol|wolof", "zu": "zul|zulu",
}

var langCanon = func() map[string]string {
	m := make(map[string]string, len(languageAliases)*4)
	for code, aliases := range languageAliases {
		m[code] = code
		for _, a := range strings.Split(aliases, "|") {
			m[a] = code
		}
	}
	return m
}()

// documented are the language tables of the ElevenLabs model docs, used
// when the account's /v1/models cannot be read.
var documented = func() map[string][]string {
	v2 := strings.Fields("en ja zh de hi fr ko pt it es id nl tr fil pl sv bg ro ar cs el fi hr ms sk da ta uk ru")
	v25 := append(append([]string{}, v2...), "hu", "no", "vi")
	v3 := strings.Fields("af ar hy as az be bn bs bg ca ceb ny hr cs da nl en et fil fi fr gl ka de el gu ha he hi hu is id ga it ja jv " +
		"kn kk ky ko lv ln lt lb mk ms ml zh mr ne no ps fa pl pt pa ro ru sr sd sk sl so es sw sv ta te th tr uk ur vi cy")
	return map[string][]string{
		defaultElevenModel:  v2,
		"eleven_flash_v2_5": v25,
		"eleven_turbo_v2_5": v25,
		"eleven_v3":         v3,
	}
}()
