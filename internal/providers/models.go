package providers

// Built-in knowledge of the model families (late 2026): the input schemas
// used when a provider's live schema cannot be read, and the capabilities
// the engine plans with (VideoCapsFor / ImageCapsFor — no network).
// Sources: fal.ai model pages (https://fal.ai/models/<id>/api), Replicate
// model schemas (https://replicate.com/<owner>/<name>/api/schema), Runway
// (https://docs.dev.runwayml.com/), Luma (https://docs.agents.lumalabs.ai/),
// MiniMax (https://platform.minimax.io/docs/api-reference/).

import (
	"slices"
	"strings"
)

var (
	nanoAspects   = []string{"auto", "21:9", "16:9", "3:2", "4:3", "5:4", "1:1", "4:5", "3:4", "2:3", "9:16"}
	falImageSizes = []string{"auto", "square_hd", "square", "portrait_4_3", "portrait_16_9", "landscape_4_3", "landscape_16_9"}
	veoAspects    = []string{"16:9", "9:16"}
)

func has(m string, subs ...string) bool {
	for _, s := range subs {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// ---- fal.ai ---------------------------------------------------------------------------

// falVideoSchema is the built-in input schema of a fal.ai video endpoint.
func falVideoSchema(model string) *inputSchema {
	m := strings.ToLower(model)
	prompt := fReq(fStr("prompt"))
	ref := has(m, "reference-to-video", "/elements")
	switch {
	case has(m, "veo3", "veo-3") && ref:
		return schemaOf(prompt, fReq(fArr("image_urls", 3)), fDef(fStr("duration"), "8s"),
			fEnumS("aspect_ratio", veoAspects...), fEnumS("resolution", "720p", "1080p", "4k"))
	case has(m, "veo3", "veo-3"):
		res := fEnumS("resolution", "720p", "1080p", "4k")
		if has(m, "lite") {
			res = fEnumS("resolution", "720p", "1080p")
		}
		return schemaOf(prompt, fReq(fStr("image_url")), fEnumS("duration", "4s", "6s", "8s"),
			fEnumS("aspect_ratio", "auto", "16:9", "9:16"), res)
	case has(m, "gemini-omni-flash") && ref:
		return schemaOf(prompt, fArr("image_urls", 10), fInt("duration", 3, 10),
			fEnumS("aspect_ratio", veoAspects...), fEnumS("resolution", "360p", "720p", "1080p", "4k"))
	case has(m, "gemini-omni-flash"):
		return schemaOf(prompt, fReq(fStr("image_url")), fInt("duration", 3, 10),
			fEnumS("aspect_ratio", veoAspects...), fEnumS("resolution", "360p", "720p", "1080p", "4k"))
	case has(m, "kling") && has(m, "/o1/") && ref:
		return schemaOf(prompt, fArr("image_urls", 7), fEnumS("duration", secs(3, 10)...), fEnumS("aspect_ratio", "16:9", "9:16", "1:1"))
	case has(m, "kling") && ref:
		return schemaOf(prompt, fArr("image_urls", 4), fEnumS("duration", secs(3, 15)...), fEnumS("aspect_ratio", "16:9", "9:16", "1:1"))
	case has(m, "kling") && has(m, "/o1/"):
		return schemaOf(prompt, fReq(fStr("start_image_url")), fEnumS("duration", secs(3, 10)...))
	case has(m, "kling") && has(m, "/v3/", "/o3/") && has(m, "turbo"):
		return schemaOf(prompt, fReq(fStr("image_url")), fEnumS("duration", secs(3, 15)...))
	case has(m, "kling") && has(m, "/o3/"):
		return schemaOf(prompt, fReq(fStr("image_url")), fEnumS("duration", secs(3, 15)...))
	case has(m, "kling") && has(m, "/v3/"):
		return schemaOf(prompt, fReq(fStr("start_image_url")), fEnumS("duration", secs(3, 15)...))
	case has(m, "kling") && has(m, "v2.6"):
		return schemaOf(prompt, fReq(fStr("start_image_url")), fEnumS("duration", "5", "10"))
	case has(m, "kling"):
		return schemaOf(prompt, fReq(fStr("image_url")), fEnumS("duration", "5", "10"))
	case has(m, "seedance") && ref:
		max, top := 9, 15
		if has(m, "2.5") {
			max, top = 30, 30
		} else if has(m, "/v1/") {
			max, top = 4, 12
		}
		field := "image_urls"
		if has(m, "/v1/") {
			field = "reference_image_urls"
		}
		return schemaOf(prompt, fArr(field, max), fEnumS("duration", secs(4, top, "auto")...),
			fEnumS("aspect_ratio", "auto", "21:9", "16:9", "4:3", "1:1", "3:4", "9:16"), fEnumS("resolution", "480p", "720p", "1080p"))
	case has(m, "seedance"):
		lo, top := 4, 15
		switch {
		case has(m, "2.5"):
			top = 30
		case has(m, "v1.5"):
			top = 12
		case has(m, "/v1/"):
			lo, top = 2, 12
		}
		return schemaOf(prompt, fReq(fStr("image_url")), fEnumS("duration", secs(lo, top)...),
			fEnumS("aspect_ratio", "auto", "21:9", "16:9", "4:3", "1:1", "3:4", "9:16"), fEnumS("resolution", "480p", "720p", "1080p"))
	case has(m, "minimax/h3", "minimax-h3") && ref:
		return schemaOf(prompt, fArr("image_urls", 9), fInt("duration", 5, 15), fEnumS("resolution", "480P", "768P", "2K", "4K"))
	case has(m, "minimax/h3", "minimax-h3"):
		return schemaOf(prompt, fStr("image_url"), fInt("duration", 5, 15), fEnumS("resolution", "480P", "768P", "2K", "4K"))
	case has(m, "hailuo") && has(m, "/pro/"):
		return schemaOf(prompt, fReq(fStr("image_url")))
	case has(m, "hailuo-02"):
		return schemaOf(prompt, fReq(fStr("image_url")), fEnumS("duration", "6", "10"), fEnumS("resolution", "512P", "768P"))
	case has(m, "hailuo", "minimax"):
		return schemaOf(prompt, fReq(fStr("image_url")), fEnumS("duration", "6", "10"))
	case has(m, "wan-3", "wan/v3"):
		return schemaOf(fStr("prompt"), fReq(fStr("start_image_url")), fInt("duration", 2, 30),
			fEnumS("aspect_ratio", "adaptive", "16:9", "4:3", "1:1", "3:4", "9:16"), fEnumS("resolution", "480p", "720p", "1080p"))
	case has(m, "wan/v2.7", "wan-2.7"):
		return schemaOf(fStr("prompt"), fStr("image_url"), fEnumI("duration", 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15), fEnumS("resolution", "720p", "1080p"))
	case has(m, "wan/v2.6", "wan-2.6"):
		return schemaOf(prompt, fReq(fStr("image_url")), fEnumS("duration", "5", "10", "15"), fEnumS("resolution", "720p", "1080p"))
	case has(m, "wan/v2.2", "wan-2.2"):
		return schemaOf(prompt, fReq(fStr("image_url")), fEnumS("resolution", "480p", "580p", "720p"))
	case has(m, "wan"):
		return schemaOf(prompt, fReq(fStr("image_url")), fEnumS("duration", "5", "10"), fEnumS("resolution", "480p", "720p", "1080p"))
	case has(m, "vidu") && ref:
		return schemaOf(prompt, fArr("reference_image_urls", 7), fEnumS("aspect_ratio", "16:9", "9:16", "1:1"))
	case has(m, "pixverse"):
		return schemaOf(prompt, fReq(fStr("image_url")), fEnumS("duration", "5", "8"), fEnumS("resolution", "360p", "540p", "720p", "1080p"))
	}
	// unknown model: the most common shape
	return schemaOf(prompt, fStr("image_url"), fStr("duration"), fStr("aspect_ratio"))
}

// falImageSchema is the built-in input schema of a fal.ai image endpoint.
func falImageSchema(model string) *inputSchema {
	m := strings.ToLower(model)
	prompt := fReq(fStr("prompt"))
	edit := has(m, "/edit", "/multi")
	switch {
	case has(m, "nano-banana", "gemini-") && edit:
		return schemaOf(prompt, fReq(fArr("image_urls", 10)), fEnumS("aspect_ratio", nanoAspects...), fInt("num_images", 1, 4))
	case has(m, "nano-banana", "gemini-"):
		return schemaOf(prompt, fEnumS("aspect_ratio", nanoAspects...), fInt("num_images", 1, 4))
	case has(m, "kontext") && edit:
		return schemaOf(prompt, fReq(fArr("image_urls", 4)), fEnumS("aspect_ratio", "21:9", "16:9", "4:3", "3:2", "1:1", "2:3", "3:4", "9:16", "9:21"))
	case has(m, "kontext") && has(m, "text-to-image"):
		return schemaOf(prompt, fEnumS("aspect_ratio", "21:9", "16:9", "4:3", "3:2", "1:1", "2:3", "3:4", "9:16", "9:21"), fInt("num_images", 1, 4))
	case has(m, "kontext"):
		return schemaOf(prompt, fReq(fStr("image_url")), fEnumS("aspect_ratio", "21:9", "16:9", "4:3", "3:2", "1:1", "2:3", "3:4", "9:16", "9:21"), fInt("num_images", 1, 4))
	case has(m, "seedream") && edit:
		return schemaOf(prompt, fReq(fArr("image_urls", 10)), fEnumS("image_size", append(falImageSizes[1:], "auto_2K", "auto_4K")...), fInt("num_images", 1, 6))
	case has(m, "seedream"):
		return schemaOf(prompt, fEnumS("image_size", append(falImageSizes[1:], "auto_2K", "auto_4K")...), fInt("num_images", 1, 6))
	case has(m, "flux-2") && edit:
		return schemaOf(prompt, fReq(fArr("image_urls", 4)), fEnumS("image_size", falImageSizes...))
	case has(m, "flux"):
		return schemaOf(prompt, fEnumS("image_size", falImageSizes...), fInt("num_images", 1, 4))
	case has(m, "qwen-image-edit"):
		return schemaOf(prompt, fReq(fArr("image_urls", 3)), fEnumS("image_size", falImageSizes...), fInt("num_images", 1, 4))
	case edit:
		return schemaOf(prompt, fReq(fArr("image_urls", 10)), fStr("aspect_ratio"), fInt("num_images", 1, 4))
	}
	return schemaOf(prompt, fArr("image_urls", 10), fStr("aspect_ratio"), fInt("num_images", 1, 4))
}

// falImageModel picks the endpoint for a request: edit endpoints need
// reference images, so a request without refs goes to the text-to-image
// sibling and one with refs to the edit sibling (when the family has one).
func falImageModel(model string, refs bool) string {
	m := strings.TrimRight(strings.TrimSpace(model), "/")
	l := strings.ToLower(m)
	switch {
	case !refs && strings.HasSuffix(l, "/edit") && has(l, "seedream"):
		return strings.TrimSuffix(m, "/edit") + "/text-to-image"
	case !refs && strings.HasSuffix(l, "/edit") && has(l, "nano-banana", "gemini-", "flux-2"):
		return m[:len(m)-len("/edit")]
	case !refs && has(l, "flux-pro/kontext") && !has(l, "text-to-image", "/multi"):
		return m + "/text-to-image"
	case refs && strings.HasSuffix(l, "/text-to-image") && has(l, "seedream"):
		return strings.TrimSuffix(m, "/text-to-image") + "/edit"
	case refs && strings.HasSuffix(l, "/text-to-image") && has(l, "kontext"):
		return strings.TrimSuffix(m, "/text-to-image")
	case refs && !strings.HasSuffix(l, "/edit") && has(l, "nano-banana", "flux-2", "gemini-3") && !has(l, "/text-to-image"):
		return m + "/edit"
	}
	return m
}

// ---- Replicate ------------------------------------------------------------------------

// replicateVideoSchema is the built-in input schema of a Replicate video model.
func replicateVideoSchema(model string) *inputSchema {
	m := strings.ToLower(model)
	prompt := fReq(fStr("prompt"))
	switch {
	case has(m, "veo-3.1") && !has(m, "fast", "lite"):
		return schemaOf(prompt, fStr("image"), fArr("reference_images", 3), fEnumI("duration", 4, 6, 8),
			fEnumS("aspect_ratio", veoAspects...), fEnumS("resolution", "720p", "1080p"))
	case has(m, "veo-3"):
		return schemaOf(prompt, fStr("image"), fEnumI("duration", 4, 6, 8), fEnumS("aspect_ratio", veoAspects...), fEnumS("resolution", "720p", "1080p"))
	case has(m, "kling-v3-omni", "kling-o1", "kling-o3"):
		return schemaOf(prompt, fStr("start_image"), fArr("reference_images", 7), fInt("duration", 3, 15), fEnumS("aspect_ratio", "16:9", "9:16", "1:1"))
	case has(m, "kling-v3"):
		return schemaOf(prompt, fReq(fStr("start_image")), fInt("duration", 3, 15))
	case has(m, "kling-v2.1"):
		return schemaOf(prompt, fReq(fStr("start_image")), fEnumI("duration", 5, 10))
	case has(m, "kling"):
		return schemaOf(prompt, fStr("start_image"), fEnumI("duration", 5, 10), fEnumS("aspect_ratio", "16:9", "9:16", "1:1"))
	case has(m, "hailuo-2.3"):
		return schemaOf(prompt, fStr("first_frame_image"), fEnumI("duration", 6, 10), fEnumS("resolution", "768p", "1080p"))
	case has(m, "hailuo", "minimax/video"):
		return schemaOf(prompt, fStr("first_frame_image"), fEnumI("duration", 6, 10), fEnumS("resolution", "512p", "768p", "1080p"))
	case has(m, "seedance-2.5"):
		return schemaOf(prompt, fStr("image"), fArr("reference_images", 30), fInt("duration", 4, 30),
			fEnumS("aspect_ratio", "adaptive", "16:9", "4:3", "1:1", "3:4", "9:16", "21:9"), fEnumS("resolution", "480p", "720p"))
	case has(m, "seedance-2"):
		return schemaOf(prompt, fStr("image"), fArr("reference_images", 9), fInt("duration", 4, 15),
			fEnumS("aspect_ratio", "adaptive", "16:9", "4:3", "1:1", "3:4", "9:16", "21:9"), fEnumS("resolution", "480p", "720p", "1080p"))
	case has(m, "seedance-1-lite"):
		return schemaOf(prompt, fStr("image"), fArr("reference_images", 4), fInt("duration", 2, 12),
			fEnumS("aspect_ratio", "16:9", "4:3", "1:1", "3:4", "9:16", "21:9", "9:21"), fEnumS("resolution", "480p", "720p", "1080p"))
	case has(m, "seedance"):
		return schemaOf(prompt, fStr("image"), fInt("duration", 2, 12),
			fEnumS("aspect_ratio", "16:9", "4:3", "1:1", "3:4", "9:16", "21:9", "9:21"), fEnumS("resolution", "480p", "720p", "1080p"))
	case has(m, "wan-2.7"):
		return schemaOf(prompt, fStr("first_frame"), fInt("duration", 2, 15))
	case has(m, "wan-3"):
		return schemaOf(prompt, fStr("image"), fInt("duration", 2, 30))
	case has(m, "wan-2.6"):
		return schemaOf(prompt, fReq(fStr("image")), fEnumI("duration", 5, 10, 15), fEnumS("resolution", "720p", "1080p"))
	case has(m, "wan-2.2"):
		return schemaOf(prompt, fReq(fStr("image")), fEnumS("resolution", "480p", "720p"))
	case has(m, "wan"):
		return schemaOf(prompt, fReq(fStr("image")), fEnumI("duration", 5, 10), fEnumS("resolution", "480p", "720p", "1080p"))
	case has(m, "r2v", "reference"):
		return schemaOf(prompt, fReq(fArr("reference_images", 7)))
	}
	return schemaOf(prompt, fStr("image"), field{name: "duration", p: prop{kind: "integer"}}, fStr("aspect_ratio"))
}

// replicateImageSchema is the built-in input schema of a Replicate image model.
func replicateImageSchema(model string) *inputSchema {
	m := strings.ToLower(model)
	prompt := fReq(fStr("prompt"))
	switch {
	case has(m, "nano-banana"):
		return schemaOf(prompt, fArr("image_input", 14), fEnumS("aspect_ratio", "match_input_image", "1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9"))
	case has(m, "kontext"):
		return schemaOf(prompt, fStr("input_image"), fEnumS("aspect_ratio", "match_input_image", "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "4:5", "5:4", "21:9", "9:21", "2:1", "1:2"))
	case has(m, "flux-2"):
		return schemaOf(prompt, fArr("input_images", 8), fEnumS("aspect_ratio", "match_input_image", "1:1", "16:9", "3:2", "2:3", "4:5", "5:4", "9:16", "3:4", "4:3"))
	case has(m, "seedream"):
		return schemaOf(prompt, fArr("image_input", 10), fEnumS("aspect_ratio", "match_input_image", "1:1", "4:3", "3:4", "16:9", "9:16", "3:2", "2:3", "21:9"))
	case has(m, "gpt-image-1") && !has(m, "1.5"):
		return schemaOf(prompt, fReq(fStr("openai_api_key")), fArr("input_images", 10), fEnumS("aspect_ratio", "1:1", "3:2", "2:3"), fInt("number_of_images", 1, 10))
	case has(m, "gpt-image"):
		return schemaOf(prompt, fArr("input_images", 10), fEnumS("aspect_ratio", "1:1", "3:2", "2:3", "4:3", "3:4", "16:9", "9:16"), fInt("number_of_images", 1, 10))
	}
	return schemaOf(prompt, fArr("image_input", 10), fStr("aspect_ratio"))
}

// ---- capabilities ---------------------------------------------------------------------

// VideoCapsFor returns the capabilities of model on the video preset (model
// "" = the preset's default model). Built-in knowledge, no network: the
// client re-checks against the provider's live schema when it renders.
func VideoCapsFor(preset, model string) VideoCaps {
	if strings.TrimSpace(model) == "" {
		if p, ok := FindVideoPreset(preset); ok {
			model = p.DefaultModel
		}
	}
	m := strings.ToLower(strings.TrimSpace(model))
	switch strings.TrimSpace(preset) {
	case PresetFal:
		return falVideoSchema(model).videoCaps()
	case PresetReplicate:
		return replicateVideoSchema(model).videoCaps()
	case PresetRunway:
		return runwayCaps(m)
	case PresetLuma:
		return VideoCaps{Lengths: []int{5, 10}, StartFrame: true, Aspects: []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"}}
	case PresetMiniMax:
		return minimaxCaps(m)
	case PresetRoyal:
		return royalVideoCaps(m)
	case PresetUniversal: // the spec knows more: SpecCaps
		return universalDefaults().Video
	}
	return VideoCaps{Lengths: []int{5, 10}, StartFrame: true, Aspects: slices.Clone(commonAspects)}
}

// ImageCapsFor returns the capabilities of model on the image preset (model
// "" = the preset's default model).
func ImageCapsFor(preset, model string) ImageCaps {
	if strings.TrimSpace(model) == "" {
		if p, ok := FindImagePreset(preset); ok {
			model = p.DefaultModel
		}
	}
	switch strings.TrimSpace(preset) {
	case PresetFal:
		return falImageSchema(falImageModel(model, true)).imageCaps()
	case PresetReplicate:
		return replicateImageSchema(model).imageCaps()
	case PresetOpenAI, PresetOpenAICompat:
		return ImageCaps{MaxRefs: 16, Aspects: []string{"16:9", "9:16", "1:1", "4:3", "3:4", "3:2", "2:3"}}
	case PresetRoyal:
		return royalImageCaps(model)
	case PresetUniversal: // the spec knows more: SpecCaps
		return universalDefaults().Image
	}
	return ImageCaps{MaxRefs: 10, Aspects: slices.Clone(commonAspects)}
}

// runwayCaps: Gen-4 Turbo / Gen-4.5 2–10 s, Veo 3.1 4/6/8 s.
func runwayCaps(m string) VideoCaps {
	if strings.HasPrefix(m, "veo") {
		return VideoCaps{Lengths: []int{4, 6, 8}, StartFrame: true, Aspects: slices.Clone(veoAspects)}
	}
	return VideoCaps{Lengths: []int{2, 3, 4, 5, 6, 7, 8, 9, 10}, StartFrame: true, Aspects: []string{"16:9", "9:16", "4:3", "3:4", "1:1", "21:9"}}
}

// minimaxCaps: Hailuo 2.3 / 02 6 or 10 s, I2V-01 6 s; H3 4–15 s and
// H3-Max 5–15 s with up to 9 reference images.
func minimaxCaps(m string) VideoCaps {
	aspects := []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"}
	switch {
	case strings.HasPrefix(m, "i2v-") || strings.HasPrefix(m, "s2v-") || strings.HasPrefix(m, "t2v-"):
		return VideoCaps{Lengths: []int{6}, StartFrame: true, Aspects: aspects}
	case strings.HasPrefix(m, "minimax-hailuo"):
		return VideoCaps{Lengths: []int{6, 10}, StartFrame: true, Aspects: aspects}
	}
	lo := 4
	if strings.Contains(m, "max") {
		lo = 5
	}
	var lengths []int
	for i := lo; i <= 15; i++ {
		lengths = append(lengths, i)
	}
	return VideoCaps{Lengths: lengths, StartFrame: true, Components: true, MaxRefs: 9, Aspects: aspects}
}
