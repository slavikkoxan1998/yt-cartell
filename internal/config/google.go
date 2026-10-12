package config

// Video models of the «Google» video provider (Google.VeoModel).
//
// Gemini Omni Flash (gemini-omni-1.1-flash, Interactions API) is the default.
// The Veo 3.1 previews (predictLongRunning) are shut down on 2026-10-22
// (https://ai.google.dev/gemini-api/docs/deprecations, replacement
// gemini-omni-1.1-flash); until then they stay selectable, afterwards they
// are hidden and migrated to Omni. Veo 3.0 / 2.0 are already shut down.

import (
	"strings"
	"time"
)

// GoogleOmniModel is the default Google video model (Gemini Omni Flash).
const GoogleOmniModel = "gemini-omni-1.1-flash"

// VeoShutdown is when the Veo 3.1 preview models stop working (midnight
// Pacific time on the documented date).
var VeoShutdown = time.Date(2026, 10, 22, 0, 0, 0, 0, time.FixedZone("PDT", -7*3600))

// now is the clock of the time-based migration (tests pin it).
var now = time.Now

// GoogleVideoModel is one choice of the Google video model.
type GoogleVideoModel struct {
	ID    string
	Label string
	// Retires is when the model is shut down (zero = no date announced).
	Retires time.Time
}

// googleVideoModels in display order.
var googleVideoModels = []GoogleVideoModel{
	{ID: GoogleOmniModel, Label: "Gemini Omni Flash"},
	{ID: "veo-3.1-generate-preview", Label: "Veo 3.1", Retires: VeoShutdown},
	{ID: "veo-3.1-fast-generate-preview", Label: "Veo 3.1 Fast", Retires: VeoShutdown},
	{ID: "veo-3.1-lite-generate-preview", Label: "Veo 3.1 Lite", Retires: VeoShutdown},
}

// GoogleVideoModels are the Google video models still served at t (the Veo
// previews disappear on VeoShutdown).
func GoogleVideoModels(t time.Time) []GoogleVideoModel {
	var out []GoogleVideoModel
	for _, m := range googleVideoModels {
		if m.Retires.IsZero() || t.Before(m.Retires) {
			out = append(out, m)
		}
	}
	return out
}

// GoogleOmni reports whether a Google video model id is Gemini Omni (the
// Interactions API) rather than Veo (predictLongRunning).
func GoogleOmni(model string) bool {
	m := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
	return strings.HasPrefix(m, "gemini-omni")
}

// GoogleRendersOmni reports whether the Google video provider renders with
// Gemini Omni for this Google.VeoModel: the Omni models, an empty id and every
// shut-down Veo model (the gemini client sends those to Omni too).
func GoogleRendersOmni(model string) bool {
	return strings.TrimSpace(model) == "" || GoogleOmni(model) || VeoRetired(model, now())
}

// VeoRetired reports whether a Google video model id is a Veo model that is
// shut down at t: Veo 2 / 3.0 (June 2026) and, from VeoShutdown on, the
// Veo 3.1 previews.
func VeoRetired(model string, t time.Time) bool {
	m := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
	switch {
	case !strings.HasPrefix(m, "veo-"):
		return false
	case strings.HasPrefix(m, "veo-2"), strings.HasPrefix(m, "veo-3.0"):
		return true
	case strings.HasPrefix(m, "veo-3.1") && strings.HasSuffix(m, "-preview"):
		return !t.Before(VeoShutdown)
	}
	return false
}

// normalizeVideo fills and migrates Google.VeoModel. Once per config
// (OmniMigrated) a Veo 3.1 preview — the old default, written into every
// saved config — moves to Gemini Omni; the user may pick Veo again until the
// shutdown. Shut-down models always move to Omni.
func (g *Google) normalizeVideo(t time.Time) {
	g.VeoModel = strings.TrimSpace(g.VeoModel)
	switch {
	case g.VeoModel == "", VeoRetired(g.VeoModel, t):
		g.VeoModel = GoogleOmniModel
	case !g.OmniMigrated && strings.HasPrefix(strings.ToLower(g.VeoModel), "veo-3.1-") && strings.HasSuffix(g.VeoModel, "-preview"):
		g.VeoModel = GoogleOmniModel
	}
	g.OmniMigrated = true
}

// videoLengthModel is the model whose clip lengths Video.Length snaps to:
// Google Omni renders 4/6/8/10 s like G-Labs Omni Flash, Google Veo 4/6/8 s.
func (c *Config) videoLengthModel() string {
	if c.Video.Provider == "google" {
		if GoogleRendersOmni(c.Google.VeoModel) {
			return "omni_flash"
		}
		return ""
	}
	return c.Video.GlabsModel
}
