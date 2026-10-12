package providers

import (
	"slices"
	"strings"
	"testing"
)

func TestVideoCapsSnapCoverAspect(t *testing.T) {
	c := VideoCaps{Lengths: []int{5, 10}, Aspects: []string{"16:9", "9:16", "1:1"}}
	for in, want := range map[float64]int{0: 7, 3: 5, 7.4: 5, 7.5: 10, 8: 10, 30: 10} {
		if got := c.Snap(in, 7); got != want {
			t.Errorf("Snap(%v) = %d, want %d", in, got, want)
		}
	}
	for in, want := range map[float64]int{2: 5, 5: 5, 5.1: 10, 12: 10} {
		if got := c.Cover(in, 8); got != want {
			t.Errorf("Cover(%v) = %d, want %d", in, got, want)
		}
	}
	if got := (VideoCaps{}).Snap(6.6, 8); got != 7 {
		t.Errorf("unknown lengths pass through: %d", got)
	}
	for in, want := range map[string]string{"16:9": "16:9", "3:4": "9:16", "4:3": "16:9", "": "16:9", "21:9": "16:9"} {
		if got := c.Aspect(in); got != want {
			t.Errorf("Aspect(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVideoCapsFor(t *testing.T) {
	cases := []struct {
		preset, model string
		lengths       []int
		start, comp   bool
		refs          int
	}{
		{PresetFal, "", []int{4, 6, 8}, true, false, 0}, // default: Veo 3.1 Fast
		{PresetFal, "fal-ai/kling-video/v2.5-turbo/pro/image-to-video", []int{5, 10}, true, false, 0},
		{PresetFal, "fal-ai/kling-video/v3/pro/image-to-video", []int{3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}, true, false, 0},
		{PresetFal, "fal-ai/veo3.1/reference-to-video", []int{8}, false, true, 3},
		{PresetFal, "fal-ai/kling-video/o1/reference-to-video", []int{3, 4, 5, 6, 7, 8, 9, 10}, false, true, 7},
		{PresetFal, "google/gemini-omni-flash/v1.1/reference-to-video", []int{3, 4, 5, 6, 7, 8, 9, 10}, false, true, 10},
		{PresetFal, "fal-ai/minimax/hailuo-2.3/standard/image-to-video", []int{6, 10}, true, false, 0},
		{PresetFal, "wan/v2.6/image-to-video", []int{5, 10, 15}, true, false, 0},
		{PresetReplicate, "", []int{4, 6, 8}, true, false, 0},
		{PresetReplicate, "google/veo-3.1", []int{4, 6, 8}, true, true, 3},
		{PresetReplicate, "kwaivgi/kling-v2.1", []int{5, 10}, true, false, 0},
		{PresetReplicate, "minimax/hailuo-02", []int{6, 10}, true, false, 0},
		{PresetRunway, "gen4_turbo", []int{2, 3, 4, 5, 6, 7, 8, 9, 10}, true, false, 0},
		{PresetRunway, "veo3.1_fast", []int{4, 6, 8}, true, false, 0},
		{PresetLuma, "", []int{5, 10}, true, false, 0},
		{PresetMiniMax, "MiniMax-Hailuo-2.3", []int{6, 10}, true, false, 0},
		{PresetMiniMax, "MiniMax-H3-Max", []int{5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}, true, true, 9},
	}
	for _, c := range cases {
		got := VideoCapsFor(c.preset, c.model)
		if !slices.Equal(got.Lengths, c.lengths) || got.StartFrame != c.start || got.Components != c.comp || got.MaxRefs != c.refs {
			t.Errorf("%s %q: %+v, want lengths %v start %v components %v refs %d", c.preset, c.model, got, c.lengths, c.start, c.comp, c.refs)
		}
		if len(got.Aspects) == 0 {
			t.Errorf("%s %q: no aspects", c.preset, c.model)
		}
	}
}

func TestImageCapsFor(t *testing.T) {
	if c := ImageCapsFor(PresetFal, ""); c.MaxRefs != 10 || !slices.Contains(c.Aspects, "16:9") {
		t.Errorf("fal default: %+v", c)
	}
	if c := ImageCapsFor(PresetFal, "fal-ai/flux-pro/kontext"); c.MaxRefs != 1 {
		t.Errorf("kontext: %+v", c)
	}
	if c := ImageCapsFor(PresetReplicate, "black-forest-labs/flux-2-pro"); c.MaxRefs != 8 {
		t.Errorf("flux-2-pro: %+v", c)
	}
	if c := ImageCapsFor(PresetOpenAI, ""); c.MaxRefs != 16 {
		t.Errorf("openai: %+v", c)
	}
}

func TestPresetsComplete(t *testing.T) {
	check := func(kind string, list []Preset, video bool) {
		seen := map[string]bool{}
		for i, p := range list {
			if p.ID == "" || seen[p.ID] {
				t.Errorf("%s: bad/duplicate id %q", kind, p.ID)
			}
			seen[p.ID] = true
			if p.ID == PresetUniversal { // described by the user's spec (universal_test.go)
				if i != len(list)-1 || p.Label == "" || p.KeyHint == "" || !p.KeyOptional {
					t.Errorf("%s universal preset: %+v", kind, p)
				}
				continue
			}
			if p.Label == "" || p.KeyHint == "" || !strings.HasPrefix(p.DocsURL, "https://") {
				t.Errorf("%s %s: label/key hint/docs missing", kind, p.ID)
			}
			if p.DefaultBaseURL == "" && p.ID != PresetOpenAICompat || p.DefaultBaseURL != "" && !strings.HasPrefix(p.DefaultBaseURL, "https://") {
				t.Errorf("%s %s: base URL %q", kind, p.ID, p.DefaultBaseURL)
			}
			if p.DefaultModel == "" || !slices.Contains(p.Models, p.DefaultModel) {
				t.Errorf("%s %s: default model %q not among the suggestions", kind, p.ID, p.DefaultModel)
			}
			if _, ok := protocolFor(p.ID); !ok {
				t.Errorf("%s %s: no protocol", kind, p.ID)
			}
			if video && (len(p.Caps.Video.Lengths) == 0 || len(p.Caps.Video.Aspects) == 0) {
				t.Errorf("%s %s: video caps %+v", kind, p.ID, p.Caps.Video)
			}
			if !video && (p.Caps.Image.MaxRefs == 0 || len(p.Caps.Image.Aspects) == 0) {
				t.Errorf("%s %s: image caps %+v", kind, p.ID, p.Caps.Image)
			}
		}
	}
	check("image", ImagePresets(), false)
	check("video", VideoPresets(), true)
	for _, p := range ImagePresets() {
		if pr, _ := protocolFor(p.ID); p.ID != PresetUniversal && pr.image == nil {
			t.Errorf("image preset %s cannot draw", p.ID)
		}
	}
	for _, p := range VideoPresets() {
		if pr, _ := protocolFor(p.ID); p.ID != PresetUniversal && pr.video == nil {
			t.Errorf("video preset %s cannot shoot", p.ID)
		}
	}
	if p, ok := FindVideoPreset(" runway "); !ok || p.DefaultModel != "gen4_turbo" {
		t.Errorf("FindVideoPreset: %+v %v", p, ok)
	}
	if _, ok := FindImagePreset("runway"); ok {
		t.Error("runway is not an image preset")
	}
}

func TestFalImageModelSiblings(t *testing.T) {
	cases := []struct {
		model string
		refs  bool
		want  string
	}{
		{"fal-ai/nano-banana-2/edit", false, "fal-ai/nano-banana-2"},
		{"fal-ai/nano-banana-2/edit", true, "fal-ai/nano-banana-2/edit"},
		{"fal-ai/nano-banana-2", true, "fal-ai/nano-banana-2/edit"},
		{"fal-ai/bytedance/seedream/v4.5/edit", false, "fal-ai/bytedance/seedream/v4.5/text-to-image"},
		{"fal-ai/bytedance/seedream/v4.5/text-to-image", true, "fal-ai/bytedance/seedream/v4.5/edit"},
		{"fal-ai/flux-pro/kontext", false, "fal-ai/flux-pro/kontext/text-to-image"},
		{"fal-ai/flux-pro/kontext/text-to-image", true, "fal-ai/flux-pro/kontext"},
		{"fal-ai/flux-2-pro/edit", false, "fal-ai/flux-2-pro"},
		{"fal-ai/recraft/v3/text-to-image", true, "fal-ai/recraft/v3/text-to-image"},
	}
	for _, c := range cases {
		if got := falImageModel(c.model, c.refs); got != c.want {
			t.Errorf("falImageModel(%q, %v) = %q, want %q", c.model, c.refs, got, c.want)
		}
	}
	for in, want := range map[string]string{
		"fal-ai/kling-video/v3/pro/image-to-video": "fal-ai/kling-video",
		"bytedance/seedance-2.0/image-to-video":    "bytedance/seedance-2.0",
		"workflows/me/flow/run":                    "workflows/me/flow",
		"fal-ai/flux":                              "fal-ai/flux",
	} {
		if got := falApp(in); got != want {
			t.Errorf("falApp(%q) = %q, want %q", in, got, want)
		}
	}
}
