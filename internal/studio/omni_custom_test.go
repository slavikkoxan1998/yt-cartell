package studio

import (
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/providers"
)

// Gemini Omni Flash through the user's own API (RoyalTechno) gets the
// storyboard and previous-shot features, with Omni Flash's 7 references.
func TestOmniFlashOnRoyalTechno(t *testing.T) {
	c := config.Default()
	c.Video.Provider = providerCustom
	c.Custom.VideoPreset = providers.PresetRoyal
	for model, want := range map[string]bool{"": true, "omni-flash": true, "veo-3.1": false} {
		c.Custom.VideoModel = model
		if got := omniFlash(c); got != want {
			t.Errorf("model %q: omniFlash = %v", model, got)
		}
	}
	c.Custom.VideoModel = "omni-flash"
	if caps := videoCaps(c); caps.MaxRefs != 7 || videoSeconds(c) == 0 {
		t.Fatalf("caps %+v", caps)
	}
	c.Custom.VideoPreset = providers.PresetFal
	if omniFlash(c) {
		t.Fatal("other presets keep rendering in parallel")
	}
}
