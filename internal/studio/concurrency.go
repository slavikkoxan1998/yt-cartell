package studio

// How much of one job runs at once: clips («Сцен одночасно» in Settings,
// config.Concurrency, clamped per video service), frames and voices. One
// place for the numbers; each comes from what the service documents
// (checked 2026-10-02). Projects run their jobs in parallel, each with these
// limits.
//
// Clips — Max (the slider's end) / Recommended (above it Settings warns):
//
//	G-Labs Studio   10 / 6   the webhook server "processes up to 10 tasks concurrently; extras wait
//	                         their turn" — https://duckmartians.info/g-labs/guide/en/webhook-api/
//	Google           4 / 2   Gemini API limits (Omni, Veo) apply per project (not per key), shown only in
//	                         AI Studio; Veo's Tier 1 quota is a few requests a day —
//	                         https://ai.google.dev/gemini-api/docs/rate-limits ; +2 per extra key, ≤ 10
//	RoyalTechno     20 / 10  "Keep at most 20 jobs in flight per API key"; generation runs at the
//	                         plan's concurrency (2–10) — https://api.royaltechno.cc/openapi.json
//	fal.ai          20 / 4   2 concurrent for new accounts, up to 40 as credits grow; queued requests
//	                         are not rejected — https://fal.ai/docs/documentation/model-apis/concurrency-limits
//	Replicate       20 / 6   600 predictions a minute, no concurrency cap —
//	                         https://replicate.com/docs/topics/predictions/rate-limits
//	Runway          20 / 2   1/3/5/10/20 tasks at once by tier, extras wait as THROTTLED —
//	                         https://docs.dev.runwayml.com/usage/tiers/
//	Luma (Ray)      10 / 3   Ray: 10 concurrent generations, extras get 429 —
//	                         https://docs.lumalabs.ai/docs/rate-limits
//	MiniMax         10 / 4   Hailuo: 20 requests a minute; H3: 30 in flight (20 / 6) —
//	                         https://platform.minimax.io/docs/guides/rate-limits
//	other own APIs  20 / 4   unknown — the user's service decides
//
// Frames follow the same setting, never below the old fixed 4, up to the
// image service's cap: G-Labs 8 (it shares the 10 tasks with clips), Google 4
// (+2 per extra key, ≤ 8), OpenAI Images 4 (Tier 1: 5 images a minute —
// https://developers.openai.com/api/docs/models/gpt-image-1.5), fal.ai /
// Replicate / RoyalTechno 8, others 4.
//
// Voices: what the TTS key pool can serve at once — its per-key concurrency
// (tts.ProviderInfo.Concurrency: ElevenLabs 2 = the Free plan's limit, OpenAI
// 4, Azure 1 …) × the keys — at most 8. More would only wait for a key.

import (
	"strings"

	"ytcartell/internal/config"
	"ytcartell/internal/providers"
	"ytcartell/internal/tts"
)

// ConcurrencyLimit is the «Сцен одночасно» range for the configured video service.
type ConcurrencyLimit struct {
	Max         int    // the slider's end (≤ config.MaxConcurrency)
	Recommended int    // above it: faster, but the service's limits bite more often
	Service     string // who renders the clips: "G-Labs", "Google Veo", "RoyalTechno" …
	Why         string // the documented limit in one Ukrainian line
}

// MaxConcurrency is how many clips one job may render at once with the
// configured video service (see the table above).
func MaxConcurrency(c config.Config) ConcurrencyLimit {
	l := concurrencyFor(c)
	l.Max = max(1, min(config.MaxConcurrency, l.Max))
	l.Recommended = max(1, min(l.Max, l.Recommended))
	return l
}

func concurrencyFor(c config.Config) ConcurrencyLimit {
	switch c.Video.Provider {
	case providerGoogle:
		return ConcurrencyLimit{Max: min(10, 4+2*max(0, len(c.Google.Keys)-1)), Recommended: 2, Service: "Google",
			Why: "Ліміти відео (Gemini Omni, Veo) в Google AI Studio діють на весь проєкт, а не на ключ, і на першому рівні дуже малі. Кожен ключ іншого проєкту додає ще 2."}
	case providerCustom:
		return customConcurrency(c)
	}
	return ConcurrencyLimit{Max: 10, Recommended: 6, Service: "G-Labs",
		Why: "Сервер вебхука G-Labs знімає до 10 завдань одночасно, решта чекає в черзі."}
}

func customConcurrency(c config.Config) ConcurrencyLimit {
	preset := strings.TrimSpace(c.Custom.VideoPreset)
	label := "твого API"
	if p, ok := providers.FindVideoPreset(preset); ok && p.Label != "" {
		label = p.Label
	}
	switch preset {
	case providers.PresetRoyal:
		return ConcurrencyLimit{Max: 20, Recommended: 10, Service: "RoyalTechno",
			Why: "RoyalTechno радить тримати до 20 завдань на ключ; одночасно генерує 2–10 — залежно від тарифу, решта чекає."}
	case providers.PresetFal:
		return ConcurrencyLimit{Max: 20, Recommended: 4, Service: "fal.ai",
			Why: "fal.ai дає новому акаунту 2 одночасні запити, з поповненням балансу — до 40; зайві чекають у черзі."}
	case providers.PresetReplicate:
		return ConcurrencyLimit{Max: 20, Recommended: 6, Service: "Replicate",
			Why: "Replicate приймає до 600 запусків на хвилину й не обмежує, скільки йде одночасно."}
	case providers.PresetRunway:
		return ConcurrencyLimit{Max: 20, Recommended: 2, Service: "Runway",
			Why: "Runway знімає від 1 до 20 відео одночасно — залежно від рівня акаунта; зайві чекають у черзі."}
	case providers.PresetLuma:
		return ConcurrencyLimit{Max: 10, Recommended: 3, Service: "Luma",
			Why: "Luma знімає до 10 відео Ray одночасно, а зайві запити відхиляє."}
	case providers.PresetMiniMax:
		model := strings.ToLower(c.Custom.VideoModel)
		if model == "" {
			if p, ok := providers.FindVideoPreset(preset); ok {
				model = strings.ToLower(p.DefaultModel)
			}
		}
		if strings.Contains(model, "h3") {
			return ConcurrencyLimit{Max: 20, Recommended: 6, Service: "MiniMax H3",
				Why: "MiniMax H3 тримає до 30 завдань одночасно."}
		}
		return ConcurrencyLimit{Max: 10, Recommended: 4, Service: "MiniMax Hailuo",
			Why: "MiniMax Hailuo приймає до 20 запитів на хвилину."}
	}
	return ConcurrencyLimit{Max: config.MaxConcurrency, Recommended: 4, Service: label,
		Why: "Ліміт задає твій сервіс — глянь у його документації, скільки завдань він тримає одночасно."}
}

// ClipConcurrency is how many clips one job renders at once: the setting,
// clamped to the video service's maximum (chaining renders one at a time —
// the caller's rule).
func ClipConcurrency(c config.Config) int {
	return max(1, min(c.Concurrency, MaxConcurrency(c).Max))
}

// ImageConcurrency is how many frames / references one job draws at once.
func ImageConcurrency(c config.Config) int {
	limit := imageCap(c)
	return max(min(4, limit), min(c.Concurrency, limit))
}

func imageCap(c config.Config) int {
	switch c.Image.Provider {
	case providerGoogle:
		return min(8, 4+2*max(0, len(c.Google.Keys)-1))
	case providerCustom:
		switch strings.TrimSpace(c.Custom.ImagePreset) {
		case providers.PresetFal, providers.PresetReplicate, providers.PresetRoyal:
			return 8
		}
		return 4 // OpenAI Images (Tier 1: 5 a minute), OpenAI-compatible, universal
	}
	return 8 // G-Labs: ≤ 10 tasks on the server, shared with clips
}

// VoiceConcurrency is how many voice lines one job synthesizes at once: the
// TTS key pool's capacity (per-key concurrency × keys), 1–8.
func VoiceConcurrency(c config.Config) int {
	per := 2
	if p, ok := tts.ProviderByID(c.TTS.Provider); ok && p.Concurrency > 0 {
		per = p.Concurrency
	}
	keys := distinctKeys(append([]string{c.TTS.APIKey}, c.TTS.Keys...))
	if keys == 0 && c.TTS.Provider == "google" {
		keys = distinctKeys(c.Google.Keys) // Google Cloud TTS borrows the AI Studio keys
	}
	return max(1, min(8, per*max(1, keys)))
}

func distinctKeys(keys []string) int {
	seen := map[string]bool{}
	for _, k := range keys {
		if k = strings.TrimSpace(k); k != "" {
			seen[k] = true
		}
	}
	return len(seen)
}

// SpecConcurrency is how many scene specs of a code animation one job asks
// the text model for at once (each is one long request, ~1 min): the
// setting (config.CodeWriters, ≤ config.MaxCodeWriters), else 3 — 1 for a
// local Ollama, which answers one request at a time and makes the others
// wait (or time out).
func SpecConcurrency(c config.Config) int {
	if c.CodeWriters > 0 {
		return min(c.CodeWriters, config.MaxCodeWriters)
	}
	if strings.EqualFold(strings.TrimSpace(c.Text.Provider), "ollama") {
		return 1
	}
	return 3
}
