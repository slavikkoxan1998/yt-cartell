package config

import "strings"

// CustomAPI is the user's own image / video generation API, used when
// Image.Provider or Video.Provider is "custom". Preset names a known
// protocol (package providers: "openai" OpenAI Images, "fal" fal.ai queue,
// "replicate" Replicate predictions, "openai-compat" any OpenAI-compatible
// images endpoint; video also "runway", "luma", "minimax"; both
// "royaltechno" and "universal"); BaseURL/Model override the preset's
// defaults ("" = preset default). ImageSpec / VideoSpec are the JSON
// descriptions of any API for the "universal" preset (providers.ParseSpec);
// they never hold the key. Keys are never logged.
type CustomAPI struct {
	ImagePreset  string `json:"imagePreset,omitempty"`
	ImageBaseURL string `json:"imageBaseUrl,omitempty"`
	ImageKey     string `json:"imageKey,omitempty"`
	ImageModel   string `json:"imageModel,omitempty"`
	ImageSpec    string `json:"imageSpec,omitempty"`

	VideoPreset  string `json:"videoPreset,omitempty"`
	VideoBaseURL string `json:"videoBaseUrl,omitempty"`
	VideoKey     string `json:"videoKey,omitempty"`
	VideoModel   string `json:"videoModel,omitempty"`
	VideoSpec    string `json:"videoSpec,omitempty"`

	// TTSSpec / STTSpec describe an own voice-over (TTS provider "custom") or
	// speech-recognition (STT provider "custom") API for the universal
	// adapter (providers.KindTTS / KindSTT); "" = the plain own-server
	// protocol. The address and key are the TTS / STT section's own.
	TTSSpec string `json:"ttsSpec,omitempty"`
	STTSpec string `json:"sttSpec,omitempty"`
}

// Normalize trims the fields (base URLs also lose trailing slashes).
func (a *CustomAPI) Normalize() {
	for _, s := range []*string{&a.ImagePreset, &a.ImageKey, &a.ImageModel, &a.ImageSpec, &a.VideoPreset, &a.VideoKey, &a.VideoModel, &a.VideoSpec, &a.TTSSpec, &a.STTSpec} {
		*s = strings.TrimSpace(*s)
	}
	a.ImageBaseURL = strings.TrimRight(strings.TrimSpace(a.ImageBaseURL), "/")
	a.VideoBaseURL = strings.TrimRight(strings.TrimSpace(a.VideoBaseURL), "/")
}

// Clip lengths the own video API may be asked for (seconds).
const (
	minCustomLength = 1
	maxCustomLength = 60
)

// CustomVideoLength clamps Video.Length for Video.Provider "custom": 0 → 8,
// else 1–60 s. The exact allowed lengths depend on the preset and model, so
// the engine snaps to them per request.
func CustomVideoLength(sec int) int {
	switch {
	case sec <= 0:
		return 8
	case sec < minCustomLength:
		return minCustomLength
	case sec > maxCustomLength:
		return maxCustomLength
	}
	return sec
}
