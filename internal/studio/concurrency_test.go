package studio

import (
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/providers"
)

// TestConcurrencyLimits: the clip limit follows the video service, a stored
// value above it is kept but clamped per job (the owner's 6 on G-Labs stays
// 6), frames follow the setting from 4 up to the image service's cap and
// voices the TTS key pool.
func TestConcurrencyLimits(t *testing.T) {
	base := config.Default()
	base.Normalize()
	cfg := func(mut func(c *config.Config)) config.Config {
		c := base
		mut(&c)
		c.Normalize()
		return c
	}

	owner := cfg(func(c *config.Config) { c.Concurrency = 6 })
	if l := MaxConcurrency(owner); l.Max != 10 || l.Recommended != 6 || l.Service != "G-Labs" || l.Why == "" {
		t.Fatalf("G-Labs %+v", l)
	}
	if ClipConcurrency(owner) != 6 || ImageConcurrency(owner) != 6 || VoiceConcurrency(owner) != 2 {
		t.Fatalf("owner: clips %d images %d voices %d", ClipConcurrency(owner), ImageConcurrency(owner), VoiceConcurrency(owner))
	}

	cases := []struct {
		name      string
		mut       func(c *config.Config)
		max, rec  int
		clips     int
		service   string
		images    int
		maxVoices int
	}{
		{"default", func(c *config.Config) {}, 10, 6, 2, "G-Labs", 4, 2},
		{"glabs-high", func(c *config.Config) { c.Concurrency = 20 }, 10, 6, 10, "G-Labs", 8, 2},
		{"google", func(c *config.Config) { c.Video.Provider, c.Concurrency = "google", 6 }, 4, 2, 4, "Google", 6, 2},
		{"google-3-keys", func(c *config.Config) {
			c.Video.Provider, c.Image.Provider, c.Concurrency = "google", "google", 9
			c.Google.Keys = []string{"k1", "k2", "k3"}
		}, 8, 2, 8, "Google", 8, 2},
		{"royal", func(c *config.Config) {
			c.Video.Provider, c.Image.Provider, c.Concurrency = "custom", "custom", 20
			c.Custom.VideoPreset, c.Custom.ImagePreset = providers.PresetRoyal, providers.PresetRoyal
		}, 20, 10, 20, "RoyalTechno", 8, 2},
		{"luma", func(c *config.Config) {
			c.Video.Provider, c.Concurrency = "custom", 15
			c.Custom.VideoPreset = providers.PresetLuma
		}, 10, 3, 10, "Luma", 8, 2},
		{"minimax-h3", func(c *config.Config) {
			c.Video.Provider, c.Concurrency = "custom", 15
			c.Custom.VideoPreset, c.Custom.VideoModel = providers.PresetMiniMax, "MiniMax-H3"
		}, 20, 6, 15, "MiniMax H3", 8, 2},
		{"openai-images", func(c *config.Config) {
			c.Image.Provider, c.Concurrency = "custom", 12
			c.Custom.ImagePreset = providers.PresetOpenAI
		}, 10, 6, 10, "G-Labs", 4, 2},
		{"one", func(c *config.Config) { c.Concurrency = 1 }, 10, 6, 1, "G-Labs", 4, 2},
		{"tts-keys", func(c *config.Config) {
			c.TTS.Provider, c.TTS.APIKey, c.TTS.Keys = "elevenlabs", "a", []string{"b", "c", "c", " "}
		}, 10, 6, 2, "G-Labs", 4, 6},
		{"tts-openai-many", func(c *config.Config) {
			c.TTS.Provider, c.TTS.APIKey, c.TTS.Keys = "openai", "a", []string{"b", "c"}
		}, 10, 6, 2, "G-Labs", 4, 8},
		{"tts-azure", func(c *config.Config) { c.TTS.Provider, c.TTS.APIKey = "azure", "a" }, 10, 6, 2, "G-Labs", 4, 1},
		{"tts-google-borrowed", func(c *config.Config) {
			c.TTS.Provider, c.TTS.APIKey = "google", ""
			c.Google.Keys = []string{"g1", "g2"}
		}, 10, 6, 2, "G-Labs", 4, 8},
	}
	for _, tc := range cases {
		c := cfg(tc.mut)
		l := MaxConcurrency(c)
		if l.Max != tc.max || l.Recommended != tc.rec || l.Service != tc.service {
			t.Errorf("%s: limit %+v, want max %d rec %d %s", tc.name, l, tc.max, tc.rec, tc.service)
		}
		if got := ClipConcurrency(c); got != tc.clips {
			t.Errorf("%s: clips %d, want %d", tc.name, got, tc.clips)
		}
		if got := ImageConcurrency(c); got != tc.images {
			t.Errorf("%s: images %d, want %d", tc.name, got, tc.images)
		}
		if got := VoiceConcurrency(c); got != tc.maxVoices {
			t.Errorf("%s: voices %d, want %d", tc.name, got, tc.maxVoices)
		}
		if l.Max > config.MaxConcurrency || l.Recommended > l.Max {
			t.Errorf("%s: out of range %+v", tc.name, l)
		}
	}
	// Every preset has a limit within the hard end.
	for _, p := range providers.VideoPresets() {
		c := cfg(func(c *config.Config) { c.Video.Provider, c.Custom.VideoPreset = "custom", p.ID })
		if l := MaxConcurrency(c); l.Max < 1 || l.Max > config.MaxConcurrency || l.Service == "" || l.Why == "" {
			t.Errorf("preset %s: %+v", p.ID, l)
		}
	}
}
