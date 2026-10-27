package tts

import "strings"

// ProviderInfo describes one TTS provider preset for the Settings screen.
type ProviderInfo struct {
	ID           string `json:"id"`           // value of config.TTS.Provider
	Label        string `json:"label"`        // shown in the provider picker
	NeedsBaseURL bool   `json:"needsBaseUrl"` // the URL/region field is required
	// BaseURLHint is the placeholder of the URL field; non-empty also when the
	// field is optional (OpenAI-compatible gateway, MiniMax China, Azure region).
	BaseURLHint string `json:"baseUrlHint,omitempty"`
	// DefaultModel is used when config.TTS.Model is "" ("" for ElevenLabs:
	// the studio picks the model per film language).
	DefaultModel string   `json:"defaultModel"`
	Models       []string `json:"models"` // model ids for the model picker (first = recommended)
	// ModelNotes are short notes on some models ("вимкнуть 6 січня 2027").
	ModelNotes map[string]string `json:"modelNotes,omitempty"`
	// ModelHint explains what the model field means for this provider.
	ModelHint   string `json:"modelHint,omitempty"`
	KeyHint     string `json:"keyHint"`               // where to get a key / what it looks like
	KeyOptional bool   `json:"keyOptional,omitempty"` // works without a key (own server)
	DocsURL     string `json:"docsUrl"`               // API documentation
	Languages   string `json:"languages"`             // language support note (Ukrainian first)
	// ListsVoices: Voices returns the account's voices (picker); false = the
	// voice is typed as an id (VoiceHint is an example).
	ListsVoices bool   `json:"listsVoices"`
	VoiceHint   string `json:"voiceHint,omitempty"`
	// DefaultVoice is used when no voice is chosen ("" = one must be chosen).
	DefaultVoice string `json:"defaultVoice,omitempty"`
	// Concurrency is how many requests may run at once on one key.
	Concurrency int `json:"concurrency"`
	// Plans says in one line what the free plan allows and which voices need
	// a paid one (from the provider's docs; "" = nothing to say).
	Plans string `json:"plans,omitempty"`
}

// providers in picker order. Docs (checked 2026-10-02):
//
//	ElevenLabs  https://elevenlabs.io/docs/api-reference/text-to-speech/convert
//	Cartesia    https://docs.cartesia.ai/api-reference/tts/bytes (Cartesia-Version 2026-08-14)
//	OpenAI      https://developers.openai.com/api/docs/guides/text-to-speech
//	Google      https://docs.cloud.google.com/text-to-speech/docs/chirp3-hd
//	Azure       https://learn.microsoft.com/azure/ai-services/speech-service/rest-text-to-speech
//	MiniMax     https://platform.minimax.io/docs/api-reference/speech-t2a-http
//	Fish Audio  https://docs.fish.audio/api-reference/endpoint/openapi-v1/text-to-speech
var providers = []ProviderInfo{
	{
		ID: "piper", Label: "Piper TTS (Локально / Офлайн)",
		DefaultModel: "uk_UA-ukrainian_tts-medium",
		Models:       []string{"uk_UA-ukrainian_tts-medium", "uk_UA-lada-x_low", "en_US-lessac-medium", "en_US-amy-medium"},
		ModelHint:    "Локальна ONNX-модель. Завантажується автоматично при першому використанні.",
		KeyHint:      "Ключ не потрібен (працює повністю автономно й безкоштовно на CPU)",
		KeyOptional:  true,
		DocsURL:      "https://github.com/rhasspy/piper",
		Languages:    "Українська (Тетяна, Микита, Лада) та англійська без інтернету",
		ListsVoices:  true, DefaultVoice: "tetiana", Concurrency: 2,
		Plans:        "Повністю безкоштовно: локальний нейромережевий синтез мови на вашому комп'ютері.",
	},
	{
		ID: "elevenlabs", Label: "ElevenLabs",
		Models:      []string{"eleven_multilingual_v2", "eleven_v3", "eleven_v4", "eleven_flash_v2_5", "eleven_v4_turbo"},
		ModelHint:   "Порожньо — студія сама вибере модель під мову фільму.",
		KeyHint:     "elevenlabs.io → Developers → API Keys (ключ sk_…)",
		DocsURL:     "https://elevenlabs.io/docs/api-reference/text-to-speech/convert",
		Languages:   "Українська та ще 70+ мов (Eleven v3), 90+ у Eleven v4; модель під мову фільму студія вибирає сама",
		ListsVoices: true, Concurrency: 2,
		Plans: "Безкоштовний тариф: через API — лише стандартні й власні голоси; голоси з бібліотеки (Voice Library) потребують платного тарифу. " +
			"Стандартні голоси ElevenLabs прибирає 31 грудня 2026.",
	},
	{
		ID: "cartesia", Label: "Cartesia",
		DefaultModel: "sonic-3.6", Models: []string{"sonic-3.6", "sonic-3.5", "sonic-3"},
		KeyHint:     "play.cartesia.ai → API Keys (ключ sk_car_…)",
		DocsURL:     "https://docs.cartesia.ai/api-reference/tts/bytes",
		Languages:   "44 мови, зокрема українська (Sonic 3.6); мову фільму студія передає сама",
		ListsVoices: true, Concurrency: 2,
		Plans: "Безкоштовний тариф: 20 000 кредитів на місяць, лише некомерційно й без клонування голосу; платні — від 100 000 кредитів.",
	},
	{
		ID: "openai", Label: "OpenAI (або сумісний)", BaseURLHint: "https://api.openai.com/v1",
		DefaultModel: "gpt-4o-mini-tts", Models: []string{"gpt-4o-mini-tts", "tts-1-hd", "tts-1"},
		// OpenAI shuts tts-1, tts-1-hd and the dated gpt-4o-mini-tts snapshots down on 2027-01-06.
		ModelNotes:  map[string]string{"tts-1-hd": "OpenAI вимкне 6 січня 2027", "tts-1": "OpenAI вимкне 6 січня 2027"},
		KeyHint:     "platform.openai.com → API keys (ключ sk-…)",
		DocsURL:     "https://developers.openai.com/api/docs/guides/text-to-speech",
		Languages:   "Мову визначає з тексту; найприродніше звучить англійська",
		ListsVoices: true, VoiceHint: "marin", DefaultVoice: "alloy", Concurrency: 4,
		Plans: "Без безкоштовного тарифу: оплата за символи з балансу. 13 вбудованих голосів; ballad, verse, marin і cedar — лише з gpt-4o-mini-tts, а tts-1 і tts-1-hd OpenAI вимкне 6 січня 2027.",
	},
	{
		ID: "google", Label: "Google Cloud (Chirp 3 HD)",
		DefaultModel: "Chirp3-HD", Models: []string{"Chirp3-HD", "Chirp-HD", "Neural2", "Wavenet", "Studio", "Standard"},
		ModelHint:   "Сімейство голосів; Chirp 3 HD — найкраща якість.",
		KeyHint:     "console.cloud.google.com → APIs & Services → Credentials: API key з увімкненим Cloud Text-to-Speech API (потрібен білінг); без ключа спробуємо ключі Google",
		DocsURL:     "https://docs.cloud.google.com/text-to-speech/docs/chirp3-hd",
		Languages:   "Chirp 3 HD: 50+ мов, зокрема українська (uk-UA); голос сам перемикається на мову фільму",
		ListsVoices: true, DefaultVoice: "Chirp3-HD-Charon", Concurrency: 4,
		Plans: "Щомісяця безкоштовно 1 млн символів Chirp 3 HD (4 млн WaveNet і Standard), далі — оплата за символи; потрібен білінг. До 5000 байтів за запит.",
	},
	{
		ID: "azure", Label: "Azure AI Speech", NeedsBaseURL: true, BaseURLHint: "westeurope",
		KeyHint:     "portal.azure.com → ресурс Speech → Keys and Endpoint: ключ, а в полі адреси — регіон (напр. westeurope)",
		DocsURL:     "https://learn.microsoft.com/azure/ai-services/speech-service/rest-text-to-speech",
		Languages:   "140+ мов; українські голоси — Поліна й Остап, Multilingual-голоси говорять багатьма мовами",
		ListsVoices: true, DefaultVoice: "uk-UA-OstapNeural", Concurrency: 1,
		Plans: "Тариф F0 — безкоштовно 0,5 млн символів на місяць і до 20 запитів за хвилину; S0 — оплата за символи.",
	},
	{
		ID: "minimax", Label: "MiniMax Speech", BaseURLHint: "https://api.minimax.io",
		DefaultModel: "speech-2.8-hd", Models: []string{"speech-2.8-hd", "speech-2.8-turbo", "speech-2.6-hd", "speech-2.6-turbo", "speech-02-hd", "speech-02-turbo"},
		KeyHint:     "platform.minimax.io → Account → API Keys (для китайського акаунта адреса https://api.minimax.cn)",
		DocsURL:     "https://platform.minimax.io/docs/api-reference/speech-t2a-http",
		Languages:   "40 мов, зокрема українська; мову фільму студія підказує сервісу",
		ListsVoices: true, Concurrency: 2,
		Plans: "Оплата за символи або підписка; системні голоси — без доплат, свій клонований голос — 1,5 $, створений — 3 $ (при першій озвучці). До 60 запитів за хвилину.",
	},
	{
		ID: "fish", Label: "Fish Audio",
		DefaultModel: "s2.1-pro", Models: []string{"s2.1-pro", "s2.1-pro-free", "s2-pro", "s1"},
		KeyHint:     "fish.audio → API Keys",
		DocsURL:     "https://docs.fish.audio/api-reference/endpoint/openapi-v1/text-to-speech",
		Languages:   "80+ мов, зокрема українська (S2.1 Pro; S1 української не знає); мову визначає з тексту",
		ListsVoices: true, Concurrency: 2,
		Plans: "Модель s2.1-pro-free — безкоштовно в межах чесного використання, інші — 15 $ за 1 млн байтів тексту. Публічні голоси бібліотеки доступні будь-якому ключу.",
	},
	{
		ID: "lumean", Label: "Lumean", BaseURLHint: "https://api.lumean.app/api/public",
		DefaultModel: "eleven_multilingual_v2", Models: append([]string(nil), lumeanModels...),
		ModelHint:   "Моделі ElevenLabs через Lumean; мову поза 29 базовими студія віддає eleven_v3.",
		KeyHint:     "lumean.app → Розробникам → API-ключі (пресет full: потрібні orders.write, orders.download, templates.write, voices.read; billing.read — для балансу)",
		DocsURL:     "https://lumean.app/developers/docs",
		Languages:   "Голоси бібліотеки ElevenLabs: 29 мов з українською (eleven_multilingual_v2), 73 — з eleven_v3",
		ListsVoices: true, Concurrency: 2,
		Plans: "Підписка з токенами або оплата з балансу (LMC). Озвучка асинхронна: замовлення → готовий файл; голос — з бібліотеки ElevenLabs.",
	},
	{
		ID: "custom", Label: "Власний сервер", NeedsBaseURL: true, BaseURLHint: "https://мій-сервер/tts",
		ModelHint:   "Передається сервісу як є.",
		KeyHint:     "Bearer-токен твого сервера (необов'язково)",
		KeyOptional: true,
		Languages:   "Залежить від сервера; мову фільму передаємо полем language",
		VoiceHint:   "voice-id", Concurrency: 4,
	},
}

// Providers lists the TTS presets in picker order (a copy).
func Providers() []ProviderInfo {
	out := make([]ProviderInfo, len(providers))
	for i, p := range providers {
		out[i] = p.clone()
	}
	return out
}

// ModelNote is the note on a model id ("" = none): a retired or soon retired
// model (a dated gpt-4o-mini-tts snapshot is shut down like tts-1).
func (p ProviderInfo) ModelNote(model string) string {
	if n := p.ModelNotes[model]; n != "" {
		return n
	}
	if p.ID == "openai" && strings.HasPrefix(model, "gpt-4o-mini-tts-20") {
		return "OpenAI вимкне 6 січня 2027"
	}
	if p.ID == "elevenlabs" && model == "eleven_turbo_v2_5" {
		return "застаріла — студія бере eleven_flash_v2_5"
	}
	return ""
}

func (p ProviderInfo) clone() ProviderInfo {
	p.Models = append([]string(nil), p.Models...)
	if p.ModelNotes != nil {
		m := make(map[string]string, len(p.ModelNotes))
		for k, v := range p.ModelNotes {
			m[k] = v
		}
		p.ModelNotes = m
	}
	return p
}

// ProviderByID is the preset of id ("openai-compatible" = "openai").
func ProviderByID(id string) (ProviderInfo, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "openai-compatible" {
		id = "openai"
	}
	for _, p := range providers {
		if p.ID == id {
			return p.clone(), true
		}
	}
	return ProviderInfo{}, false
}

// normProvider is the provider id the client works with: unknown or empty
// ids fall back to ElevenLabs (like config.Normalize).
func normProvider(id string) string {
	if p, ok := ProviderByID(id); ok {
		return p.ID
	}
	return "elevenlabs"
}
