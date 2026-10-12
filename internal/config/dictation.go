package config

import "strings"

// Dictation is voice typing in the Home story box (package dictate): the
// microphone is captured through FFmpeg and a speech-to-text service turns it
// into text while the owner speaks. It is chosen apart from STT (which
// transcribes uploaded recordings).
type Dictation struct {
	// Provider (stored ids, never renamed): "" = auto (the first service
	// that is ready, starting with the STT choice) | "elevenlabs" (Scribe v2
	// Realtime) | "openai" (OpenAI Realtime transcription) | "deepgram"
	// (Nova-3 streaming) | "assemblyai" (Universal-Streaming) |
	// "openai-chunks" (any OpenAI-compatible /audio/transcriptions, phrase by
	// phrase) | "gemini" (Google AI Studio, phrase by phrase) | "local"
	// (whisper.cpp on this computer).
	Provider string `json:"provider"`
	// Language is a catalogue code ("uk", "en", …); "" = detect.
	Language string `json:"language,omitempty"`
	// Device is the microphone's name as FFmpeg lists it ("" = the system
	// default, else the first one).
	Device string `json:"device,omitempty"`
	// SilenceSec: seconds of silence that end the dictation (0 = the
	// default 8 s, -1 = never; 3–60).
	SilenceSec int `json:"silenceSec,omitempty"`

	// Own keys, endpoints and models per service; "" reuses the app's other
	// settings (the ElevenLabs key of STT or the voice-over, the OpenAI key
	// of STT/Text/voice-over when they use api.openai.com, the
	// OpenAI-compatible endpoint of STT or Text, the Google AI Studio keys,
	// the AssemblyAI key of STT) or the service's usual model.
	ElevenKey   string `json:"elevenKey,omitempty"`
	OpenAIKey   string `json:"openaiKey,omitempty"`   // OpenAI Realtime (api.openai.com)
	OpenAIModel string `json:"openaiModel,omitempty"` // "" = gpt-live-transcribe
	ChunksURL   string `json:"chunksUrl,omitempty"`   // OpenAI-compatible base URL ending in /v1
	ChunksKey   string `json:"chunksKey,omitempty"`
	ChunksModel string `json:"chunksModel,omitempty"`
	DeepgramKey string `json:"deepgramKey,omitempty"`
	AssemblyKey string `json:"assemblyKey,omitempty"`
	GoogleKey   string `json:"googleKey,omitempty"`
	GeminiModel string `json:"geminiModel,omitempty"`
}

// DefaultSilenceSec is the pause that ends a dictation by default.
const DefaultSilenceSec = 8

// Silence is the pause in seconds that ends a dictation (0 = never).
func (d Dictation) Silence() int {
	switch {
	case d.SilenceSec < 0:
		return 0
	case d.SilenceSec == 0:
		return DefaultSilenceSec
	}
	return d.SilenceSec
}

// DictationProviders are the stored provider ids ("" = auto).
var DictationProviders = []string{"elevenlabs", "openai", "deepgram", "assemblyai", "openai-chunks", "gemini", "local"}

// normalize trims and clamps the dictation settings.
func (d *Dictation) normalize() {
	d.Provider = strings.ToLower(strings.TrimSpace(d.Provider))
	known := d.Provider == ""
	for _, p := range DictationProviders {
		known = known || p == d.Provider
	}
	if !known {
		d.Provider = ""
	}
	d.Language = strings.ToLower(strings.TrimSpace(d.Language))
	if d.Language == "auto" {
		d.Language = ""
	}
	d.Device = strings.TrimSpace(d.Device)
	switch {
	case d.SilenceSec < 0:
		d.SilenceSec = -1
	case d.SilenceSec > 0 && d.SilenceSec < 3:
		d.SilenceSec = 3
	case d.SilenceSec > 60:
		d.SilenceSec = 60
	}
	for _, s := range []*string{&d.ElevenKey, &d.OpenAIKey, &d.OpenAIModel, &d.ChunksKey, &d.ChunksModel,
		&d.DeepgramKey, &d.AssemblyKey, &d.GoogleKey, &d.GeminiModel} {
		*s = strings.TrimSpace(*s)
	}
	d.ChunksURL = strings.TrimRight(strings.TrimSpace(d.ChunksURL), "/")
}
