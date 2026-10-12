package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeCustomProviders(t *testing.T) {
	c := Default()
	c.Image.Provider, c.Video.Provider = "custom", "custom"
	c.Video.Length = 5 // Kling 5 s: not snapped to Veo's 4/6/8
	c.Custom = CustomAPI{
		ImagePreset: " fal ", ImageBaseURL: " https://queue.fal.run/ ", ImageKey: " k1 ", ImageModel: " fal-ai/nano-banana-2/edit ",
		VideoPreset: "runway", VideoBaseURL: "https://api.dev.runwayml.com/v1//", VideoKey: "key_x\n",
		VideoSpec: "\n  {\"submit\":{\"path\":\"/x\"}}\n\n",
	}
	c.Normalize()
	if c.Image.Provider != "custom" || c.Video.Provider != "custom" || c.Video.Length != 5 {
		t.Errorf("providers %q/%q length %d", c.Image.Provider, c.Video.Provider, c.Video.Length)
	}
	want := CustomAPI{
		ImagePreset: "fal", ImageBaseURL: "https://queue.fal.run", ImageKey: "k1", ImageModel: "fal-ai/nano-banana-2/edit",
		VideoPreset: "runway", VideoBaseURL: "https://api.dev.runwayml.com/v1", VideoKey: "key_x",
		VideoSpec: `{"submit":{"path":"/x"}}`,
	}
	if c.Custom != want {
		t.Errorf("custom = %+v", c.Custom)
	}
	for in, out := range map[int]int{0: 8, -3: 8, 1: 1, 12: 12, 90: 60} {
		c.Video.Length = in
		c.Normalize()
		if c.Video.Length != out {
			t.Errorf("custom length %d → %d, want %d", in, c.Video.Length, out)
		}
	}
	// G-Labs keeps snapping; unknown providers fall back to G-Labs
	c.Video.Provider, c.Image.Provider, c.Video.Length = "midjourney", "x", 5
	c.Normalize()
	if c.Video.Provider != "glabs" || c.Image.Provider != "glabs" || c.Video.Length == 5 {
		t.Errorf("fallback: %q %q %d", c.Video.Provider, c.Image.Provider, c.Video.Length)
	}
	// the JSON field names are stable
	b, _ := json.Marshal(CustomAPI{ImagePreset: "openai", VideoKey: "v", ImageSpec: "{}"})
	if s := string(b); !strings.Contains(s, `"imagePreset":"openai"`) || !strings.Contains(s, `"videoKey":"v"`) || strings.Contains(s, "imageKey") ||
		!strings.Contains(s, `"imageSpec":"{}"`) || strings.Contains(s, "videoSpec") {
		t.Errorf("json = %s", s)
	}
}
