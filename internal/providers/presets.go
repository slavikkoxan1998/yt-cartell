package providers

import "strings"

// Preset ids (config.CustomAPI.ImagePreset / VideoPreset). Stored on disk —
// never rename them.
const (
	PresetOpenAI       = "openai"        // OpenAI Images API (images)
	PresetOpenAICompat = "openai-compat" // any OpenAI-compatible images endpoint (images)
	PresetFal          = "fal"           // fal.ai queue API (images, video)
	PresetReplicate    = "replicate"     // Replicate predictions API (images, video)
	PresetRunway       = "runway"        // Runway API (video)
	PresetLuma         = "luma"          // Luma Agents API, Ray (video)
	PresetMiniMax      = "minimax"       // MiniMax API, Hailuo / H3 (video)
	PresetRoyal        = "royaltechno"   // RoyalTechno AI jobs API (images, video)
	PresetUniversal    = "universal"     // any API described by the user's JSON spec (images, video)
)

// universalPreset is the last entry of both lists: any API, described by the
// user's spec (config.CustomAPI.ImageSpec / VideoSpec).
var universalPreset = Preset{
	ID: PresetUniversal, Label: "Універсальний (будь-яке API)",
	KeyHint:     `Ключ із кабінету твого сервісу. Як його передати, задає поле "auth" в описі API`,
	KeyOptional: true, // "auth": "none"
}

// Preset describes one supported API for the Settings screen.
type Preset struct {
	ID             string
	Label          string
	DefaultBaseURL string   // "" = the user must enter one (OpenAI-compatible)
	Models         []string // suggestions; any model id of the API works
	DefaultModel   string
	KeyHint        string // where to get the key (Ukrainian)
	KeyOptional    bool   // a local OpenAI-compatible server may need no key
	DocsURL        string
	Caps           Caps // capabilities of DefaultModel (Image or Video)
}

// ImagePresets lists the image APIs, in display order, with the
// capabilities of their default models.
func ImagePresets() []Preset {
	list := imagePresetList()
	for i := range list {
		list[i].Caps = Caps{Image: ImageCapsFor(list[i].ID, list[i].DefaultModel)}
	}
	return list
}

// VideoPresets lists the video APIs, in display order, with the
// capabilities of their default models.
func VideoPresets() []Preset {
	list := videoPresetList()
	for i := range list {
		list[i].Caps = Caps{Video: VideoCapsFor(list[i].ID, list[i].DefaultModel)}
	}
	return list
}

// imagePresetList / videoPresetList are the presets without Caps (the caps
// come from the presets' default models, see ImagePresets / VideoPresets).
func imagePresetList() []Preset {
	return []Preset{
		{
			ID: PresetOpenAI, Label: "OpenAI (GPT Image)",
			DefaultBaseURL: "https://api.openai.com/v1",
			// gpt-image-1 / 1.5 / -mini and chatgpt-image-latest shut down by
			// 2026-12-01: not suggested any more (a typed id still works).
			Models:       []string{"gpt-image-2.5-flare", "gpt-image-2.5-sunburst", "gpt-image-2"},
			DefaultModel: "gpt-image-2.5-flare",
			KeyHint:      "Ключ sk-… з platform.openai.com → API keys. Для GPT Image організацію треба верифікувати.",
			DocsURL:      "https://developers.openai.com/api/docs/guides/image-generation",
		},
		{
			ID: PresetFal, Label: "fal.ai",
			DefaultBaseURL: "https://queue.fal.run",
			Models: []string{
				"fal-ai/nano-banana-2/edit", "fal-ai/nano-banana-pro/edit", "fal-ai/flux-2-pro/edit",
				"fal-ai/bytedance/seedream/v4.5/edit", "bytedance/seedream/v5/pro/edit",
			},
			DefaultModel: "fal-ai/nano-banana-2/edit",
			KeyHint:      "Ключ з fal.ai → Dashboard → API Keys. Моделі «…/edit» тримають схожість персонажів за референсами.",
			DocsURL:      "https://fal.ai/docs/documentation/model-apis/inference/queue",
		},
		{
			ID: PresetReplicate, Label: "Replicate",
			DefaultBaseURL: "https://api.replicate.com/v1",
			Models: []string{
				"google/nano-banana-2", "google/nano-banana-pro", "black-forest-labs/flux-2-pro",
				"bytedance/seedream-4.5", "black-forest-labs/flux-kontext-pro",
			},
			DefaultModel: "google/nano-banana-2",
			KeyHint:      "Токен r8_… з replicate.com → Account settings → API tokens",
			DocsURL:      "https://replicate.com/docs/reference/http",
		},
		{
			ID: PresetRoyal, Label: "RoyalTechno",
			DefaultBaseURL: royalBase,
			Models:         []string{"nano-banana-pro", "nano-banana-2", "seedream-4.6", "gpt-image-2"},
			DefaultModel:   "nano-banana-pro",
			KeyHint:        royalKeyHint,
			DocsURL:        royalDocs,
		},
		{
			ID: PresetOpenAICompat, Label: "OpenAI-сумісний сервер",
			Models:       []string{"gpt-image-2.5-flare", "gpt-image-2.5-sunburst", "gpt-image-2"},
			DefaultModel: "gpt-image-2.5-flare",
			KeyHint:      "Адреса з /v1 наприкінці (напр. http://127.0.0.1:4000/v1) і ключ сервера, якщо він потрібен",
			KeyOptional:  true,
			DocsURL:      "https://developers.openai.com/api/reference/resources/images",
		},
		universalPreset,
	}
}

func videoPresetList() []Preset {
	return []Preset{
		{
			ID: PresetFal, Label: "fal.ai",
			DefaultBaseURL: "https://queue.fal.run",
			Models: []string{
				"fal-ai/veo3.1/fast/image-to-video", "fal-ai/kling-video/v3/pro/image-to-video",
				"bytedance/seedance-2.0/image-to-video", "fal-ai/minimax/hailuo-2.3/standard/image-to-video",
				"wan/v2.6/image-to-video", "google/gemini-omni-flash/v1.1/image-to-video",
				"fal-ai/veo3.1/reference-to-video", "fal-ai/kling-video/o1/reference-to-video",
				"google/gemini-omni-flash/v1.1/reference-to-video",
			},
			DefaultModel: "fal-ai/veo3.1/fast/image-to-video",
			KeyHint:      "Ключ з fal.ai → Dashboard → API Keys. «…/image-to-video» оживляє кадр, «…/reference-to-video» знімає за референсами.",
			DocsURL:      "https://fal.ai/docs/documentation/model-apis/inference/queue",
		},
		{
			ID: PresetReplicate, Label: "Replicate",
			DefaultBaseURL: "https://api.replicate.com/v1",
			Models: []string{
				"google/veo-3.1-fast", "google/veo-3.1", "kwaivgi/kling-v2.6", "kwaivgi/kling-v3-video",
				"minimax/hailuo-2.3", "bytedance/seedance-1-pro", "wan-video/wan-2.6-i2v",
			},
			DefaultModel: "google/veo-3.1-fast",
			KeyHint:      "Токен r8_… з replicate.com → Account settings → API tokens",
			DocsURL:      "https://replicate.com/docs/reference/http",
		},
		{
			ID: PresetRunway, Label: "Runway",
			DefaultBaseURL: "https://api.dev.runwayml.com/v1",
			Models:         []string{"gen4_turbo", "gen4.5", "veo3.1_fast", "veo3.1"},
			DefaultModel:   "gen4_turbo",
			KeyHint:        "Ключ key_… з dev.runwayml.com → API Keys (кредити купуються там само)",
			DocsURL:        "https://docs.dev.runwayml.com/",
		},
		{
			ID: PresetLuma, Label: "Luma (Ray)",
			DefaultBaseURL: "https://agents.lumalabs.ai/v1",
			Models:         []string{"ray-3.2"},
			DefaultModel:   "ray-3.2",
			KeyHint:        "Ключ luma-api-… з lumalabs.ai → API",
			DocsURL:        "https://docs.agents.lumalabs.ai/guides/videos/generation/",
		},
		{
			ID: PresetMiniMax, Label: "MiniMax (Hailuo)",
			DefaultBaseURL: "https://api.minimax.io",
			Models:         []string{"MiniMax-H3", "MiniMax-H3-Max", "MiniMax-Hailuo-2.3", "MiniMax-Hailuo-2.3-Fast", "MiniMax-Hailuo-02"},
			DefaultModel:   "MiniMax-H3",
			KeyHint:        "Ключ з platform.minimax.io → API Keys. Для Китаю адреса https://api.minimax.cn",
			DocsURL:        "https://platform.minimax.io/docs/api-reference/video-generation-v2-create",
		},
		{
			ID: PresetRoyal, Label: "RoyalTechno",
			DefaultBaseURL: royalBase,
			Models: []string{
				"omni-flash", "veo-3.1", "seedance-2.0", "seedance-2.0-fast", "seedance-2.0-mini",
				"grok-imagine-1.5", "grok-imagine-1.5-720p",
			},
			DefaultModel: "omni-flash",
			KeyHint:      royalKeyHint,
			DocsURL:      royalDocs,
		},
		universalPreset,
	}
}

const (
	royalBase    = "https://api.royaltechno.cc/v1"
	royalDocs    = "https://api.royaltechno.cc/docs"
	royalKeyHint = "Ключ rt_live_… з Telegram-бота @royaltechno_ai_bot (команда /api_key). Обирай моделі зі свого тарифу — «Перевірити» покаже, що в тебе без ліміту"
)

// FindImagePreset returns the image preset with id (ok false when unknown).
func FindImagePreset(id string) (Preset, bool) { return findPreset(imagePresetList(), id) }

// FindVideoPreset returns the video preset with id (ok false when unknown).
func FindVideoPreset(id string) (Preset, bool) { return findPreset(videoPresetList(), id) }

func findPreset(list []Preset, id string) (Preset, bool) {
	id = strings.TrimSpace(id)
	for _, p := range list {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
