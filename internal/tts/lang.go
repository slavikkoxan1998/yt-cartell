package tts

// Language handling of the providers whose voices or requests need an
// explicit language: SynthOptions.LanguageCode (catalogue code: ISO 639-1,
// else 639-3) → a BCP-47 locale (Google, Azure), Cartesia's language codes,
// MiniMax's language_boost names. With the film language on "auto" the
// writing system of the text decides where it is unambiguous (guessLanguage).

import (
	"strings"
	"unicode"
)

// locales: language → the locale a locale-bound provider speaks it in.
var locales = map[string]string{
	"af": "af-ZA", "am": "am-ET", "ar": "ar-SA", "az": "az-AZ", "bg": "bg-BG", "bn": "bn-IN", "bs": "bs-BA",
	"ca": "ca-ES", "cs": "cs-CZ", "cy": "cy-GB", "da": "da-DK", "de": "de-DE", "el": "el-GR", "en": "en-US",
	"es": "es-ES", "et": "et-EE", "eu": "eu-ES", "fa": "fa-IR", "fi": "fi-FI", "fil": "fil-PH", "fr": "fr-FR",
	"ga": "ga-IE", "gl": "gl-ES", "gu": "gu-IN", "he": "he-IL", "hi": "hi-IN", "hr": "hr-HR", "hu": "hu-HU",
	"hy": "hy-AM", "id": "id-ID", "is": "is-IS", "it": "it-IT", "ja": "ja-JP", "jv": "jv-ID", "ka": "ka-GE",
	"kk": "kk-KZ", "km": "km-KH", "kn": "kn-IN", "ko": "ko-KR", "lo": "lo-LA", "lt": "lt-LT", "lv": "lv-LV",
	"mk": "mk-MK", "ml": "ml-IN", "mn": "mn-MN", "mr": "mr-IN", "ms": "ms-MY", "mt": "mt-MT", "my": "my-MM",
	"nb": "nb-NO", "ne": "ne-NP", "nl": "nl-NL", "no": "nb-NO", "or": "or-IN", "pa": "pa-IN", "pl": "pl-PL",
	"ps": "ps-AF", "pt": "pt-BR", "ro": "ro-RO", "ru": "ru-RU", "si": "si-LK", "sk": "sk-SK", "sl": "sl-SI",
	"so": "so-SO", "sq": "sq-AL", "sr": "sr-RS", "su": "su-ID", "sv": "sv-SE", "sw": "sw-KE", "ta": "ta-IN",
	"te": "te-IN", "th": "th-TH", "tr": "tr-TR", "uk": "uk-UA", "ur": "ur-IN", "uz": "uz-UZ", "vi": "vi-VN",
	"yue": "yue-HK", "zh": "zh-CN", "zu": "zu-ZA",
}

// googleLocales are Google Cloud TTS's own codes where they differ.
var googleLocales = map[string]string{"ar": "ar-XA", "zh": "cmn-CN", "yue": "yue-HK", "sr": "sr-RS"}

// localeFor is the BCP-47 locale for a language code ("" = unknown). A code
// that already names a region ("pt-PT") is kept.
func localeFor(provider, code string) string {
	raw := strings.TrimSpace(strings.ReplaceAll(code, "_", "-"))
	if raw == "" {
		return ""
	}
	if base, region, ok := strings.Cut(raw, "-"); ok && len(region) >= 2 {
		lang := NormalizeLanguage(base)
		if lang == "" {
			lang = strings.ToLower(base)
		}
		if provider == "google" && lang == "zh" && strings.EqualFold(region, "CN") {
			return "cmn-CN"
		}
		return lang + "-" + strings.ToUpper(region)
	}
	lang := langCode(code)
	if provider == "google" {
		if l, ok := googleLocales[lang]; ok {
			return l
		}
	}
	return locales[lang]
}

// langOfLocale is the catalogue language of a locale ("uk-UA" → "uk", "cmn-CN" → "zh").
func langOfLocale(locale string) string {
	base, _, _ := strings.Cut(strings.ReplaceAll(locale, "_", "-"), "-")
	return langCode(base)
}

// cartesiaLangs: the languages of Sonic 3.6 (docs: build-with-cartesia/tts-models/latest).
var cartesiaLangs = map[string]string{
	"en": "en", "fr": "fr", "de": "de", "es": "es", "pt": "pt", "zh": "zh", "ja": "ja", "hi": "hi", "it": "it",
	"ko": "ko", "nl": "nl", "pl": "pl", "ru": "ru", "sv": "sv", "tr": "tr", "fil": "tl", "bg": "bg", "ro": "ro",
	"ar": "ar", "cs": "cs", "el": "el", "fi": "fi", "hr": "hr", "ms": "ms", "sk": "sk", "da": "da", "ta": "ta",
	"uk": "uk", "hu": "hu", "no": "no", "vi": "vi", "bn": "bn", "th": "th", "he": "he", "ka": "ka", "id": "id",
	"te": "te", "gu": "gu", "kn": "kn", "ml": "ml", "mr": "mr", "pa": "pa", "or": "or", "ur": "ur",
}

// minimaxLangs: language_boost values of the MiniMax T2A API.
var minimaxLangs = map[string]string{
	"zh": "Chinese", "yue": "Chinese,Yue", "en": "English", "ar": "Arabic", "ru": "Russian", "es": "Spanish",
	"fr": "French", "pt": "Portuguese", "de": "German", "tr": "Turkish", "nl": "Dutch", "uk": "Ukrainian",
	"vi": "Vietnamese", "id": "Indonesian", "ja": "Japanese", "it": "Italian", "ko": "Korean", "th": "Thai",
	"pl": "Polish", "ro": "Romanian", "el": "Greek", "cs": "Czech", "fi": "Finnish", "hi": "Hindi",
	"bg": "Bulgarian", "da": "Danish", "he": "Hebrew", "ms": "Malay", "fa": "Persian", "sk": "Slovak",
	"sv": "Swedish", "hr": "Croatian", "fil": "Filipino", "hu": "Hungarian", "no": "Norwegian",
	"sl": "Slovenian", "ca": "Catalan", "ta": "Tamil", "af": "Afrikaans",
}

// filmLanguage is the language of a synthesis: the film's (opt) or, on
// "auto", the one the text's writing system shows.
func filmLanguage(code, text string) string {
	if c := langCode(code); c != "" {
		return c
	}
	return guessLanguage(text)
}

// guessLanguage names the language of text from its writing system when that
// is unambiguous enough for a voice choice; "" for Latin and mixed text.
// Cyrillic: і ї є ґ → Ukrainian, ђ ј љ њ ћ џ → Serbian, ы э ё → Russian,
// ъ without those → Bulgarian; plain Cyrillic → Ukrainian (the app's audience).
func guessLanguage(text string) string {
	var cyr, latin, letters int
	scripts := map[string]int{}
	var uk, sr, ru, bg bool
	for _, r := range text {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			cyr++
			switch unicode.ToLower(r) {
			case 'і', 'ї', 'є', 'ґ':
				uk = true
			case 'ђ', 'ј', 'љ', 'њ', 'ћ', 'џ':
				sr = true
			case 'ы', 'э', 'ё':
				ru = true
			case 'ъ':
				bg = true
			}
		case unicode.Is(unicode.Latin, r):
			latin++
		case unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
			scripts["ja"] += 2 // kana beats the kanji around it
		case unicode.Is(unicode.Han, r):
			scripts["zh"]++
		default:
			for _, s := range otherScripts {
				if unicode.Is(s.table, r) {
					scripts[s.code]++
					break
				}
			}
		}
	}
	if letters == 0 {
		return ""
	}
	if cyr*2 > letters {
		switch {
		case uk:
			return "uk"
		case sr:
			return "sr"
		case ru:
			return "ru"
		case bg:
			return "bg"
		}
		return "uk"
	}
	if ja := scripts["ja"]; ja > 0 && ja+scripts["zh"] > letters/2 {
		return "ja"
	}
	best, n := "", 0
	for code, c := range scripts {
		if c > n || (c == n && code < best) {
			best, n = code, c
		}
	}
	if n*2 > letters {
		return best
	}
	return ""
}

var otherScripts = []struct {
	table *unicode.RangeTable
	code  string
}{
	{unicode.Greek, "el"}, {unicode.Arabic, "ar"}, {unicode.Hebrew, "he"}, {unicode.Hangul, "ko"},
	{unicode.Devanagari, "hi"}, {unicode.Bengali, "bn"}, {unicode.Thai, "th"}, {unicode.Georgian, "ka"},
	{unicode.Armenian, "hy"}, {unicode.Tamil, "ta"}, {unicode.Telugu, "te"}, {unicode.Gujarati, "gu"},
	{unicode.Gurmukhi, "pa"}, {unicode.Kannada, "kn"}, {unicode.Malayalam, "ml"}, {unicode.Oriya, "or"},
	{unicode.Ethiopic, "am"}, {unicode.Lao, "lo"}, {unicode.Myanmar, "my"}, {unicode.Khmer, "km"},
	{unicode.Sinhala, "si"},
}

// accentOf is how a voice's locale reads in the voice picker (English words
// the picker translates: "american", "british", "ukrainian"…).
func accentOf(locale string) string {
	switch strings.ToLower(strings.ReplaceAll(locale, "_", "-")) {
	case "en-us":
		return "american"
	case "en-gb":
		return "british"
	case "en-au":
		return "australian"
	case "en-in":
		return "indian"
	case "en-ie":
		return "irish"
	case "en-ca":
		return "canadian"
	}
	if name := languageName(langOfLocale(locale)); name != "" {
		return name
	}
	return locale
}

// languageName is the English adjective of a language code, lower case.
func languageName(code string) string {
	names := map[string]string{
		"uk": "ukrainian", "en": "english", "pl": "polish", "de": "german", "fr": "french", "es": "spanish",
		"it": "italian", "pt": "portuguese", "ru": "russian", "sv": "swedish", "nl": "dutch", "cs": "czech",
		"ja": "japanese", "zh": "chinese", "ko": "korean", "tr": "turkish", "ar": "arabic", "hi": "hindi",
		"el": "greek", "ro": "romanian", "hu": "hungarian", "bg": "bulgarian", "he": "hebrew", "da": "danish",
		"fi": "finnish", "no": "norwegian", "sk": "slovak", "hr": "croatian", "sr": "serbian", "vi": "vietnamese",
		"th": "thai", "id": "indonesian", "ka": "georgian", "kk": "kazakh", "lt": "lithuanian", "lv": "latvian",
		"et": "estonian", "sl": "slovenian", "be": "belarusian",
	}
	return names[code]
}
