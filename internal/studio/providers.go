package studio

// Generation providers chosen in Settings: G-Labs Studio ("glabs", default),
// Google Gemini / Veo ("google") or the user's own API ("custom",
// config.CustomAPI via package providers).

import (
	"context"
	"strings"

	"ytcartell/internal/config"
	"ytcartell/internal/gemini"
	"ytcartell/internal/gen"
	"ytcartell/internal/glabs"
	"ytcartell/internal/providers"
)

// Provider ids of config.Image.Provider / config.Video.Provider.
const (
	providerGlabs  = "glabs"
	providerGoogle = "google"
	providerCustom = "custom"
)

// ---- providers (chosen in Settings) --------------------------------------------------

func (e *Engine) image(ctx context.Context, r gen.ImageRequest) ([]byte, string, error) {
	switch e.cfg.Get().Image.Provider {
	case providerGoogle:
		return e.gem.Image(ctx, r)
	case providerCustom:
		return e.customAPI().Image(ctx, r)
	}
	return e.gl.Image(ctx, r)
}

func (e *Engine) video(ctx context.Context, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	switch e.cfg.Get().Video.Provider {
	case providerGoogle:
		return e.gem.Video(ctx, r, progress)
	case providerCustom:
		return e.customAPI().Video(ctx, r, progress)
	}
	return e.gl.Video(ctx, r, progress)
}

// customAPI is the client of the user's own API. It keeps no state (settings
// are read on every call; connections are pooled by the shared transport).
func (e *Engine) customAPI() *providers.Client { return providers.New(e.cfg.Get) }

// ---- service checks (Settings → Власний API) --------------------------------------------

// TestCustomImage checks the own image API (Settings → custom image preset,
// key, base URL, model) with a cheap authenticated call — nothing is
// generated or billed.
func (e *Engine) TestCustomImage(ctx context.Context) (bool, string) {
	return e.customAPI().TestImage(ctx)
}

// TestCustomVideo checks the own video API the same way (nothing is
// generated or billed).
func (e *Engine) TestCustomVideo(ctx context.Context) (bool, string) {
	return e.customAPI().TestVideo(ctx)
}

// ---- capabilities ----------------------------------------------------------------------

// VideoCapsFor is what the video provider/model configured in c renders —
// the allowed clip lengths (ascending), aspect ratios and references — so
// the Settings and step screens offer exactly what the engine uses (the
// engine's own helper is videoCaps).
func VideoCapsFor(c config.Config) providers.VideoCaps { return videoCaps(c) }

// OmniFlash reports whether c renders with Gemini Omni Flash — G-Labs Omni
// Flash, Google Gemini Omni or Omni Flash through the user's own API
// (storyboards, chained shots).
func OmniFlash(c config.Config) bool { return omniFlash(c) }

// videoCaps is what the configured video provider/model renders:
//
//	G-Labs  Veo 3.1 models 8 s, + 4/6 s only with Google AI Ultra
//	        (c.Video.Ultra); Omni Flash 4/6/8/10 s always (no Ultra needed);
//	        components refs: Veo 3, Omni Flash 7
//	Google  Gemini Omni Flash (default): 4/6/8/10 s, 16:9 | 9:16, up to 10
//	        reference images (<IMAGE_REF_N>); Veo until its 2026-10-22
//	        shutdown: 4/6/8 s, up to 3 "asset" reference images (Veo 3.1 /
//	        3.1 Fast; not Lite)
//	custom  per preset and model (providers.VideoCapsFor); the universal
//	        preset's from the user's spec (providers.SpecCaps)
func videoCaps(c config.Config) providers.VideoCaps {
	switch c.Video.Provider {
	case providerGoogle:
		if config.GoogleRendersOmni(c.Google.VeoModel) {
			// Gemini Omni Flash: 3–10 s clips (requested as 4/6/8/10), up to
			// 10 images per prompt, start frame or references
			return providers.VideoCaps{Lengths: append([]int(nil), gemini.OmniLengths...), MaxRefs: gemini.MaxOmniRefs,
				StartFrame: true, Components: true, Aspects: []string{"16:9", "9:16"}}
		}
		model := strings.ToLower(c.Google.VeoModel)
		refs := !strings.Contains(model, "lite") && !strings.Contains(model, "veo-2") && !strings.Contains(model, "veo-3.0")
		// Gemini API durationSeconds 4/6/8 (reference images need 8)
		caps := providers.VideoCaps{Lengths: []int{4, 6, 8}, StartFrame: true, Components: refs, Aspects: []string{"16:9", "9:16"}}
		if refs {
			caps.MaxRefs = 3
		}
		return caps
	case providerCustom:
		if strings.TrimSpace(c.Custom.VideoPreset) == providers.PresetUniversal {
			return providers.SpecCaps(c.Custom.VideoSpec).Video
		}
		return providers.VideoCapsFor(c.Custom.VideoPreset, c.Custom.VideoModel)
	}
	model := strings.TrimSpace(c.Video.GlabsModel)
	lengths := []int{8}
	switch {
	case model == glabs.OmniFlash: // 4/6 s need no Ultra on Omni Flash (G-Labs Webhook guide)
		lengths = []int{4, 6, 8, 10}
	case c.Video.Ultra: // Veo 4/6 s only on Google AI Ultra accounts
		lengths = []int{4, 6, 8}
	}
	return providers.VideoCaps{
		Lengths:    lengths,
		MaxRefs:    glabs.MaxVideoRefs(model),
		StartFrame: true,
		Components: true,
		Aspects:    []string{"16:9", "9:16"},
	}
}
