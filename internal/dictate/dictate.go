// Package dictate is voice typing for the Home story box: the microphone is
// captured through FFmpeg (no cgo — capture.go) as 16 kHz mono 16-bit PCM and
// a speech-to-text service turns it into text while the owner speaks
// (session.go). Settings: config.Dictation (Settings → «Диктування»).
//
// Services (stored ids — never rename):
//
//	elevenlabs     ElevenLabs Scribe v2 Realtime, WebSocket: partial and
//	               committed transcripts (eleven.go)
//	openai         OpenAI Realtime API, transcription session over WebSocket:
//	               gpt-live-transcribe deltas (or gpt-4o-transcribe …) (openai.go)
//	deepgram       Deepgram Nova-3 live streaming, WebSocket (deepgram.go)
//	assemblyai     AssemblyAI Universal-Streaming v3, WebSocket: turns with
//	               partial and formatted final text (assembly.go; no Ukrainian)
//	openai-chunks  any OpenAI-compatible POST /audio/transcriptions, one
//	               phrase at a time (chunked.go)
//	gemini         Google AI Studio (Gemini) generateContent with the phrase
//	               as inline audio, one phrase at a time (chunked.go)
//	local          whisper.cpp on this computer: whisper-server keeps the model
//	               loaded (whisper-cli per phrase when there is no server) —
//	               offline and free (local.go)
//
// The phrase-at-a-time services share one chunker (chunker.go): an energy
// voice detector cuts the stream at pauses, re-reads the phrase in progress
// every ~2.5 s for interim text and keeps a short overlap when a long phrase
// has to be cut without a pause (the repeated words are dropped).
//
// Keys: each service has its own override in config.Dictation; empty ones
// reuse the app's other settings (Resolve): the ElevenLabs key of STT or of
// the ElevenLabs voice-over, the OpenAI key/endpoint of STT or the Text card,
// the Google AI Studio keys, the AssemblyAI key of STT. "" (auto) picks the
// first service that is ready,
// starting with the one chosen for uploaded recordings (cfg.STT).
//
// Nothing here touches the UI: the session reports through State and Take
// (any goroutine), and the text goes in at the caret through Splice.
package dictate

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"ytcartell/internal/config"
	"ytcartell/internal/stt"
)

// Provider ids (config.Dictation.Provider).
const (
	Auto         = ""
	ElevenLabs   = "elevenlabs"
	OpenAI       = "openai"
	OpenAIChunks = "openai-chunks"
	Deepgram     = "deepgram"
	Gemini       = "gemini"
	Local        = "local"
)

// ProviderInfo describes a service for Settings.
type ProviderInfo struct {
	ID    string
	Label string // "ElevenLabs Scribe v2 Realtime"
	Short string // a compact label for narrow pickers
	Hint  string // one Ukrainian line: what it is like
	// Streaming services return text while the owner speaks; the others
	// recognise phrase by phrase.
	Streaming bool
	Key       bool   // needs a key
	DocsURL   string // the API page the client follows
}

var providers = []ProviderInfo{
	{ID: ElevenLabs, Label: "ElevenLabs Scribe v2 Realtime", Short: "ElevenLabs", Streaming: true, Key: true,
		Hint:    "Найшвидше й точно, з розділовими знаками. Ключ — той самий, що для озвучки ElevenLabs.",
		DocsURL: "https://elevenlabs.io/docs/api-reference/speech-to-text/v-1-speech-to-text-realtime"},
	{ID: OpenAI, Label: "OpenAI Realtime (GPT Live Transcribe)", Short: "OpenAI Realtime", Streaming: true, Key: true,
		Hint:    "Текст з'являється під час мовлення. Потрібен ключ OpenAI (platform.openai.com).",
		DocsURL: "https://platform.openai.com/docs/guides/realtime-transcription"},
	{ID: Deepgram, Label: "Deepgram Nova-3", Short: "Deepgram", Streaming: true, Key: true,
		Hint:    "Дуже швидко. Потрібен ключ Deepgram (console.deepgram.com).",
		DocsURL: "https://developers.deepgram.com/reference/speech-to-text/listen-streaming"},
	{ID: AssemblyAI, Label: "AssemblyAI Universal-Streaming", Short: "AssemblyAI", Streaming: true, Key: true,
		Hint:    "У реальному часі, понад 30 мов — української поки немає. Нові акаунти мають $50 безкоштовних кредитів.",
		DocsURL: "https://www.assemblyai.com/docs/streaming/api-spec/streaming-websocket"},
	{ID: OpenAIChunks, Label: "OpenAI-сумісний сервіс (фразами)", Short: "OpenAI-сумісний", Key: true,
		Hint:    "Будь-який сервер з /audio/transcriptions (OpenAI, Groq…): текст приходить після кожної фрази.",
		DocsURL: "https://platform.openai.com/docs/api-reference/audio/createTranscription"},
	{ID: Gemini, Label: "Google AI Studio (Gemini, фразами)", Short: "Gemini", Key: true,
		Hint:    "Ключі Google AI Studio з налаштувань: текст приходить після кожної фрази.",
		DocsURL: "https://ai.google.dev/gemini-api/docs/audio"},
	{ID: Local, Label: "whisper.cpp на цьому комп'ютері", Short: "Локально",
		Hint:    "Без інтернету й без ключа, безкоштовно. Повільніше за хмару; потрібна встановлена модель.",
		DocsURL: "https://github.com/ggml-org/whisper.cpp/tree/master/examples/server"},
}

// Providers lists the services in the order Settings shows them.
func Providers() []ProviderInfo { return append([]ProviderInfo(nil), providers...) }

// ProviderByID finds a service.
func ProviderByID(id string) (ProviderInfo, bool) {
	for _, p := range providers {
		if p.ID == id {
			return p, true
		}
	}
	return ProviderInfo{}, false
}

// Plan is a resolved dictation setup: the service, its key and endpoint.
type Plan struct {
	Provider string // concrete id ("" = nothing is ready)
	Label    string
	Auto     bool // picked automatically

	Key     string   // never shown or logged
	Keys    []string // Gemini: every Google key, rotated on quota errors
	KeyFrom string   // where the key comes from, for Settings ("з «Озвучки»"); "" = own
	BaseURL string
	Model   string

	Bin, ModelPath string // local: whisper.cpp program and model

	Language string // catalogue code, "" = detect
	Device   string // microphone name, "" = default
	Silence  int    // seconds of silence that end it (0 = never)

	Ready   bool
	Problem string // why it is not ready (Ukrainian), "" when ready
}

// Default endpoints and models (package vars: tests point them at mocks).
var (
	elevenWS    = "wss://api.elevenlabs.io/v1/speech-to-text/realtime"
	openaiBase  = "https://api.openai.com/v1"
	deepgramWS  = "wss://api.deepgram.com/v1/listen"
	geminiBase  = "https://generativelanguage.googleapis.com/v1beta"
	localStatus = stt.LocalStatus // finds whisper.cpp (files only)
)

// Models.
const (
	ElevenModel        = "scribe_v2_realtime"
	OpenAIRealtimeDef  = "gpt-live-transcribe"    // deltas while speaking; commits are ours
	OpenAIChunksDef    = "gpt-4o-mini-transcribe" // api.openai.com
	WhisperCompatDef   = "whisper-1"              // any other OpenAI-compatible server
	GroqDef            = "whisper-large-v3-turbo" // api.groq.com
	DeepgramModel      = "nova-3"
	GeminiDef          = "gemini-3.5-flash-lite" // 2.5 models are limited-access since 2026-09-18
	openaiOfficialHost = "api.openai.com"
)

// Resolve works out the service for cfg.Dictation: the chosen one, or with
// auto the first that is ready (local only counts when whisper.cpp and a
// model are installed). It only reads settings and looks for files.
func Resolve(cfg config.Config) Plan {
	d := cfg.Dictation
	if d.Provider != Auto {
		return For(cfg, d.Provider)
	}
	for _, id := range autoOrder(cfg) {
		if p := For(cfg, id); p.Ready {
			p.Auto = true
			return p
		}
	}
	p := base(cfg)
	p.Auto = true
	p.Problem = "Диктування ще не налаштоване: вибери сервіс у Налаштуваннях → Диктування (whisper.cpp працює без ключа, якщо встановлений)."
	return p
}

// autoOrder is the order auto tries the services in: the one used for
// uploaded recordings first, then the realtime ones, then this computer.
func autoOrder(cfg config.Config) []string {
	var first string
	switch strings.ToLower(cfg.STT.Provider) {
	case "openai":
		if isOfficialOpenAI(sttOpenAIBase(cfg)) {
			first = OpenAI
		} else {
			first = OpenAIChunks
		}
	case stt.ProviderLocal:
		first = Local
	default:
		first = ElevenLabs
	}
	order := []string{first}
	// AssemblyAI streams no Ukrainian: auto takes it last (For refuses it
	// for "uk").
	for _, id := range []string{ElevenLabs, Deepgram, OpenAI, Local, Gemini, AssemblyAI} {
		if id != first {
			order = append(order, id)
		}
	}
	return order
}

func base(cfg config.Config) Plan {
	d := cfg.Dictation
	return Plan{Language: d.Language, Device: d.Device, Silence: d.Silence()}
}

// For resolves one service (Settings shows each one's state with it).
func For(cfg config.Config, id string) Plan {
	p := base(cfg)
	info, ok := ProviderByID(id)
	if !ok {
		p.Problem = "Невідомий сервіс диктування — вибери інший у Налаштуваннях"
		return p
	}
	p.Provider, p.Label = id, info.Label
	d := cfg.Dictation
	switch id {
	case ElevenLabs:
		p.Key, p.KeyFrom = elevenKey(cfg)
		p.BaseURL, p.Model = elevenWS, ElevenModel
		if p.Key == "" {
			p.Problem = "Немає ключа ElevenLabs: встав його в Налаштуваннях → Диктування або налаштуй озвучку ElevenLabs."
		}
	case OpenAI:
		p.BaseURL = openaiBase
		p.Key, p.KeyFrom = openaiKey(cfg, true)
		p.Model = firstNonEmpty(d.OpenAIModel, OpenAIRealtimeDef)
		if p.Key == "" {
			p.Problem = "Немає ключа OpenAI: встав його в Налаштуваннях → Диктування."
		}
	case OpenAIChunks:
		p.BaseURL, p.Key, p.KeyFrom = chunksEndpoint(cfg)
		p.Model = firstNonEmpty(d.ChunksModel, chunksModel(cfg, p.BaseURL))
		switch {
		case p.BaseURL == "":
			p.Problem = "Не задано адресу сервісу розпізнавання (…/v1) — вкажи її в Налаштуваннях → Диктування."
		case p.Key == "" && !isLoopback(p.BaseURL):
			p.Problem = "Немає ключа для " + hostOf(p.BaseURL) + ": встав його в Налаштуваннях → Диктування."
		}
	case Deepgram:
		p.Key, p.BaseURL, p.Model = d.DeepgramKey, deepgramWS, DeepgramModel
		if p.Key == "" {
			p.Problem = "Немає ключа Deepgram: встав його в Налаштуваннях → Диктування (console.deepgram.com)."
		}
	case AssemblyAI:
		p.Key = d.AssemblyKey
		if p.Key == "" && cfg.STT.AssemblyKey != "" {
			p.Key, p.KeyFrom = cfg.STT.AssemblyKey, "з «Розпізнавання мови»"
		}
		p.BaseURL, p.Model = assemblyWS, AssemblyModel
		if cfg.STT.AssemblyEU {
			p.BaseURL = assemblyWSEU // the owner keeps AssemblyAI data in the EU
		}
		switch {
		case p.Key == "":
			p.Problem = "Немає ключа AssemblyAI: встав його в Налаштуваннях → Диктування (" + assemblyKeyURL + ")."
		case p.Language == "uk":
			p.Problem = "AssemblyAI у реальному часі поки не розпізнає українську — вибери іншу мову диктування або інший сервіс."
		}
	case Gemini:
		p.BaseURL, p.Model = geminiBase, firstNonEmpty(d.GeminiModel, GeminiDef)
		if k := d.GoogleKey; k != "" {
			p.Key, p.Keys = k, []string{k}
		} else if len(cfg.Google.Keys) > 0 {
			p.Key, p.KeyFrom = cfg.Google.Keys[0], "з «Google AI Studio»"
			p.Keys = append([]string(nil), cfg.Google.Keys...)
		}
		if p.Key == "" {
			p.Problem = "Немає ключа Google AI Studio: додай його в Налаштуваннях (Google AI Studio або Диктування)."
		}
	case Local:
		bin, model, ok, hint := localStatus(cfg)
		p.Bin, p.ModelPath = bin, model
		if !ok {
			p.Problem = hint
		}
	}
	p.Ready = p.Problem == ""
	return p
}

// elevenKey: the own key, the STT key (when STT is ElevenLabs), then the
// ElevenLabs voice-over keys (kept per provider in TTS).
func elevenKey(cfg config.Config) (string, string) {
	if k := cfg.Dictation.ElevenKey; k != "" {
		return k, ""
	}
	if strings.EqualFold(cfg.STT.Provider, "elevenlabs") && strings.TrimSpace(cfg.STT.APIKey) != "" {
		return strings.TrimSpace(cfg.STT.APIKey), "з «Розпізнавання мови»"
	}
	if keys := cfg.TTS.Of("elevenlabs").AllKeys(); len(keys) > 0 {
		return keys[0], "з «Озвучки» (ElevenLabs)"
	}
	return "", ""
}

// openaiKey finds an OpenAI key: the own one, else one the app already uses
// with the official API (STT, Text, the OpenAI voice-over). official limits it
// to keys whose endpoint is api.openai.com (a gateway's key would not work).
func openaiKey(cfg config.Config, official bool) (string, string) {
	if k := cfg.Dictation.OpenAIKey; k != "" {
		return k, ""
	}
	if strings.EqualFold(cfg.STT.Provider, "openai") {
		k := strings.TrimSpace(cfg.STT.APIKey)
		if k == "" {
			k = strings.TrimSpace(cfg.Text.APIKey)
		}
		if k != "" && (!official || isOfficialOpenAI(sttOpenAIBase(cfg))) {
			return k, "з «Розпізнавання мови»"
		}
	}
	if k := strings.TrimSpace(cfg.Text.APIKey); k != "" && cfg.Text.OpenAICompatible() && (!official || isOfficialOpenAI(cfg.Text.BaseURL)) {
		return k, "з картки «Текст»"
	}
	if o := cfg.TTS.Of("openai"); official && (o.BaseURL == "" || isOfficialOpenAI(o.BaseURL)) {
		if keys := o.AllKeys(); len(keys) > 0 {
			return keys[0], "з «Озвучки» (OpenAI)"
		}
	}
	return "", ""
}

// sttOpenAIBase is the endpoint STT uses for "openai" (its own, else Text's).
func sttOpenAIBase(cfg config.Config) string {
	if b := strings.TrimSpace(cfg.STT.BaseURL); b != "" {
		return b
	}
	return cfg.Text.BaseURL
}

// chunksEndpoint: the own base URL + key, else STT's OpenAI endpoint, else
// the Text card's.
func chunksEndpoint(cfg config.Config) (baseURL, key, from string) {
	d := cfg.Dictation
	if d.ChunksURL != "" {
		key = d.ChunksKey
		if key == "" && sameHost(d.ChunksURL, sttOpenAIBase(cfg)) && strings.EqualFold(cfg.STT.Provider, "openai") {
			key, from = firstNonEmpty(strings.TrimSpace(cfg.STT.APIKey), strings.TrimSpace(cfg.Text.APIKey)), "з «Розпізнавання мови»"
		}
		if key == "" && sameHost(d.ChunksURL, cfg.Text.BaseURL) {
			key, from = strings.TrimSpace(cfg.Text.APIKey), "з картки «Текст»"
		}
		return strings.TrimRight(d.ChunksURL, "/"), key, from
	}
	if strings.EqualFold(cfg.STT.Provider, "openai") {
		k := firstNonEmpty(d.ChunksKey, strings.TrimSpace(cfg.STT.APIKey), strings.TrimSpace(cfg.Text.APIKey))
		if d.ChunksKey == "" && k != "" {
			from = "з «Розпізнавання мови»"
		}
		return strings.TrimRight(sttOpenAIBase(cfg), "/"), k, from
	}
	k := firstNonEmpty(d.ChunksKey, strings.TrimSpace(cfg.Text.APIKey))
	if d.ChunksKey == "" && k != "" {
		from = "з картки «Текст»"
	}
	return strings.TrimRight(cfg.Text.BaseURL, "/"), k, from
}

// chunksModel is the usual model of an OpenAI-compatible server: the STT
// model when STT uses it, the fast transcribe model on OpenAI, Whisper Large
// v3 Turbo on Groq, whisper-1 elsewhere.
func chunksModel(cfg config.Config, baseURL string) string {
	if strings.EqualFold(cfg.STT.Provider, "openai") && sameHost(baseURL, sttOpenAIBase(cfg)) && strings.TrimSpace(cfg.STT.Model) != "" {
		return strings.TrimSpace(cfg.STT.Model)
	}
	switch hostOf(baseURL) {
	case openaiOfficialHost:
		return OpenAIChunksDef
	case "api.groq.com":
		return GroqDef
	}
	return WhisperCompatDef
}

func isOfficialOpenAI(base string) bool { return hostOf(base) == openaiOfficialHost }

func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.TrimSpace(raw)
	}
	return strings.ToLower(u.Hostname())
}

func sameHost(a, b string) bool {
	ha, hb := hostOf(a), hostOf(b)
	return ha != "" && ha == hb
}

func isLoopback(raw string) bool {
	h := hostOf(raw)
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost")
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// ---- errors ---------------------------------------------------------------------------------

// Kind classifies what went wrong (the UI picks the help it offers).
type Kind int

const (
	KindOther        Kind = iota
	KindNoProvider        // nothing configured
	KindNoKey             // the service needs a key
	KindAuth              // the key was refused
	KindQuota             // credits or rate limit
	KindNetwork           // no connection
	KindNoMic             // no microphone, or it cannot be opened
	KindPermission        // the OS refused the microphone
	KindNoFFmpeg          // FFmpeg missing
	KindNotInstalled      // local whisper.cpp missing
	KindService           // the service failed
)

// Error is a friendly Ukrainian error with its kind.
type Error struct {
	Kind Kind
	Msg  string
	Err  error // the cause (never contains a key)
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Err }

// KindOf returns the kind of err (KindOther for foreign errors).
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindOther
}

// NeedsSettings reports errors fixed in Settings → Диктування.
func NeedsSettings(err error) bool {
	switch KindOf(err) {
	case KindNoProvider, KindNoKey, KindAuth, KindNoMic, KindNoFFmpeg, KindNotInstalled:
		return true
	}
	return false
}

func newErr(k Kind, cause error, format string, args ...any) *Error {
	return &Error{Kind: k, Msg: fmt.Sprintf(format, args...), Err: cause}
}

// planErr turns an unready plan into its error.
func planErr(p Plan) *Error {
	switch {
	case p.Provider == "":
		return newErr(KindNoProvider, nil, "%s", p.Problem)
	case p.Provider == Local:
		return newErr(KindNotInstalled, nil, "%s", p.Problem)
	}
	return newErr(KindNoKey, nil, "%s", p.Problem)
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
