package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/providers"
	"ytcartell/internal/providers/catalog"
	ttscat "ytcartell/internal/tts/catalog"
)

// TestCatalogStyles: every catalogue entry becomes one item with a unique id,
// a service filter tag and a source link; the entry in use is marked.
func TestCatalogStyles(t *testing.T) {
	check := func(name string, st pickers.CatalogStyle, want int) {
		t.Helper()
		if len(st.Items) != want {
			t.Errorf("%s: %d items, catalogue has %d", name, len(st.Items), want)
		}
		seen := map[string]bool{}
		for _, it := range st.Items {
			if seen[it.ID] {
				t.Errorf("%s: duplicate item %s", name, it.ID)
			}
			seen[it.ID] = true
			if it.Title == "" || it.Link == "" || len(it.Tags) == 0 {
				t.Errorf("%s: item %s has no title, link or tags", name, it.ID)
			}
		}
		for _, g := range st.Groups {
			for _, o := range g.Options {
				if o.Label == "" {
					t.Errorf("%s: group %s option %q has no label", name, g.Key, o.Value)
				}
			}
		}
	}
	check("video", modelCatalogStyle(true, customVals{}), len(catalog.Video()))
	check("image", modelCatalogStyle(false, customVals{}), len(catalog.Image()))
	check("tts", ttsCatalogStyle(config.TTS{}), len(ttscat.All()))
	check("text", textCatalogStyle(config.Text{}), len(catalog.Text()))

	for _, video := range []bool{true, false} {
		var e catalog.Entry
		if video && len(catalog.Video()) > 0 {
			e = catalog.Video()[0].Entry
		} else if !video && len(catalog.Image()) > 0 {
			e = catalog.Image()[0].Entry
		} else {
			continue
		}
		st := modelCatalogStyle(video, customVals{preset: e.Preset, model: e.Model})
		n := 0
		for _, it := range st.Items {
			if it.Current {
				n++
				if it.ID != e.ID && !strings.EqualFold(strings.SplitN(it.ID, ":", 2)[1], e.Model) {
					t.Errorf("video=%v: %s marked current for %s", video, it.ID, e.ID)
				}
			}
		}
		if n == 0 {
			t.Errorf("video=%v: the entry in use (%s) is not marked", video, e.ID)
		}
	}
}

// TestApplyModelEntry: a dedicated preset gets its model and no base URL when
// the catalogue's is the preset's own; the universal one its spec; the key stays.
func TestApplyModelEntry(t *testing.T) {
	cu := config.CustomAPI{VideoKey: "k-1", VideoSpec: "{}", ImageKey: "k-2"}
	applyModelEntry(&cu, true, catalog.Entry{Preset: providers.PresetFal, BaseURL: "https://queue.fal.run/", Model: "fal-ai/x/image-to-video"})
	if cu.VideoPreset != "fal" || cu.VideoBaseURL != "" || cu.VideoModel != "fal-ai/x/image-to-video" || cu.VideoKey != "k-1" || cu.VideoSpec != "{}" {
		t.Errorf("fal: %+v", cu)
	}
	spec := `{"submit":{"path":"/v1/videos","body":{"model":"{{model}}","prompt":"{{prompt}}"}},"id":"id","poll":{"path":"/v1/videos/{id}"},"state":"status","done":["completed"],"result":"url"}`
	applyModelEntry(&cu, true, catalog.Entry{Preset: providers.PresetUniversal, BaseURL: "https://api.example.com/v1", Model: "m-2", Spec: []byte(spec)})
	if cu.VideoPreset != "universal" || cu.VideoBaseURL != "https://api.example.com/v1" || cu.VideoModel != "m-2" || cu.VideoKey != "k-1" {
		t.Errorf("universal: %+v", cu)
	}
	if _, err := providers.ParseSpec(cu.VideoSpec); err != nil || !strings.Contains(cu.VideoSpec, "\n") {
		t.Errorf("universal spec %q: %v", cu.VideoSpec, err)
	}
	applyModelEntry(&cu, false, catalog.Entry{Preset: providers.PresetOpenAICompat, BaseURL: "https://api.together.xyz/v1", Model: "flux"})
	if cu.ImagePreset != "openai-compat" || cu.ImageBaseURL != "https://api.together.xyz/v1" || cu.ImageKey != "k-2" || cu.VideoPreset != "universal" {
		t.Errorf("image: %+v", cu)
	}
	// Every shipped entry applies to a preset the studio has, and a universal
	// one leaves a description the adapter accepts.
	for _, e := range catalog.Video() {
		var c config.CustomAPI
		applyModelEntry(&c, true, e.Entry)
		if _, ok := providers.FindVideoPreset(c.VideoPreset); !ok {
			t.Errorf("%s: preset %q", e.ID, c.VideoPreset)
		}
		if c.VideoPreset == providers.PresetUniversal {
			if _, err := providers.ParseSpec(c.VideoSpec); err != nil {
				t.Errorf("%s: %v", e.ID, err)
			}
		}
	}
	for _, e := range catalog.Image() {
		var c config.CustomAPI
		applyModelEntry(&c, false, e.Entry)
		if _, ok := providers.FindImagePreset(c.ImagePreset); !ok {
			t.Errorf("%s: preset %q", e.ID, c.ImagePreset)
		}
		if c.ImagePreset == providers.PresetUniversal {
			if _, err := providers.ParseSpec(c.ImageSpec); err != nil {
				t.Errorf("%s: %v", e.ID, err)
			}
		}
	}
}

// TestApplyTTSEntry: a pick switches the provider (its own keys come back),
// sets model, voice and server address, and leaves the other providers alone.
func TestApplyTTSEntry(t *testing.T) {
	cf := config.Default()
	cf.TTS.Switch("elevenlabs")
	cf.TTS.APIKey, cf.TTS.Voice = "sk_eleven", "v-eleven"
	cf.Normalize()
	applyTTSEntry(&cf, ttscat.Entry{Provider: "openai", Model: "kokoro", Voice: "af_heart", BaseURL: "http://localhost:8880/v1"})
	cf.Normalize()
	if cf.TTS.Provider != "openai" || cf.TTS.Model != "kokoro" || cf.TTS.Voice != "af_heart" || cf.TTS.BaseURL != "http://localhost:8880/v1" || cf.TTS.APIKey != "" {
		t.Errorf("openai view: %+v", cf.TTS)
	}
	if el := cf.TTS.Of("elevenlabs"); el.APIKey != "sk_eleven" || el.Voice != "v-eleven" {
		t.Errorf("elevenlabs lost its settings: %+v", el)
	}
	// A model entry keeps the voice already chosen.
	applyTTSEntry(&cf, ttscat.Entry{Provider: "openai", Model: "gpt-4o-mini-tts"})
	if cf.TTS.Voice != "af_heart" || cf.TTS.Model != "gpt-4o-mini-tts" {
		t.Errorf("model entry: %+v", cf.TTS)
	}
	for _, e := range ttscat.All() {
		if !e.Supported(ttsProviderOK) {
			continue
		}
		c := config.Default()
		applyTTSEntry(&c, e)
		c.Normalize()
		if c.TTS.Provider != e.Provider {
			t.Errorf("%s: provider %q after apply", e.ID, c.TTS.Provider)
		}
		if e.SpecText() != "" {
			if _, err := providers.ParseSpecFor(c.Custom.TTSSpec, providers.KindTTS); err != nil || c.TTS.BaseURL == "" {
				t.Errorf("%s: spec %v, base %q", e.ID, err, c.TTS.BaseURL)
			}
		}
	}
}

// TestApplyTextEntry: protocols the studio speaks are applied, the others refused.
func TestApplyTextEntry(t *testing.T) {
	cf := config.Default()
	cf.Text.APIKey = "sk-keep"
	if !applyTextEntry(&cf, catalog.TextEntry{Protocol: "openai", BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "x/y"}) ||
		cf.Text.Provider != "openai" || cf.Text.BaseURL != "https://openrouter.ai/api/v1" || cf.Text.Model != "x/y" || cf.Text.APIKey != "sk-keep" {
		t.Errorf("openai: %+v", cf.Text)
	}
	if !applyTextEntry(&cf, catalog.TextEntry{Protocol: "gemini", DefaultModel: "gemini-x"}) || cf.Text.Provider != "google" || cf.Google.StoryModel != "gemini-x" {
		t.Errorf("gemini: %+v / %q", cf.Text, cf.Google.StoryModel)
	}
	if applyTextEntry(&cf, catalog.TextEntry{Protocol: "vertex"}) || cf.Text.Provider != "google" {
		t.Errorf("vertex was applied: %+v", cf.Text)
	}
}

// TestCatalogShots renders the four choosers over the settings page
// (GUISHOTS=<dir>; GUISHOTS_ONLY=catalog-… narrows).
func TestCatalogShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("GUISHOTS=<dir> renders the catalogue screenshots")
	}
	only := os.Getenv("GUISHOTS_ONLY")
	custom := func(cf *config.Config) {
		cf.Image.Provider, cf.Video.Provider = "custom", "custom"
		cf.Custom.VideoPreset, cf.Custom.ImagePreset = "fal", "replicate"
	}
	var textUI catalogUI
	states := []struct {
		name, section string
		config        func(cf *config.Config)
		setup         func(p *Page)
		text          bool
	}{
		{name: "catalog-video", section: "video", config: custom, setup: func(p *Page) { p.video.custom.catalog.pick.Open() }},
		{name: "catalog-video-speech", section: "video", config: custom, setup: func(p *Page) {
			p.video.custom.catalog.pick.Open()
			p.video.custom.catalog.pick.SetToggle("speech", true)
			p.video.custom.catalog.pick.SetFilter("mode", catalog.ModeI2V)
		}},
		{name: "catalog-image", section: "frames", config: custom, setup: func(p *Page) {
			p.frames.custom.catalog.pick.Open()
			p.frames.custom.catalog.pick.SetToggle("refs", true)
		}},
		{name: "catalog-tts", section: "tts", setup: func(p *Page) {
			p.tts.catalog.pick.Open()
			p.tts.catalog.pick.SetToggle("uk", true)
		}},
		{name: "catalog-text", section: "text", text: true, setup: func(p *Page) { textUI.pick.Open() }},
		{name: "catalog-card", section: "video", config: custom},
		{name: "catalog-ttscard", section: "tts"},
	}
	sizes := []shotSize{{1440, 900}, {390, 844}}
	for _, st := range states {
		if only != "" && !strings.Contains(st.name, only) {
			continue
		}
		for _, dark := range []bool{false, true} {
			for _, sz := range sizes {
				if dark && sz.w < 900 {
					continue
				}
				theme := map[bool]string{false: "light", true: "dark"}[dark]
				name := fmt.Sprintf("settings-%s-%s-%dx%d.png", st.name, theme, sz.w, sz.h)
				t.Run(name, func(t *testing.T) {
					c, h := demo(t)
					if st.config != nil {
						if err := c.E.Config().Update(st.config); err != nil {
							t.Fatal(err)
						}
					}
					c.Th.SetPaletteNow(dark)
					p := New()
					textUI = catalogUI{}
					frame := 0
					draw := shell(c, func(gtx C) D {
						frame++
						if frame == 2 && st.setup != nil {
							st.setup(p)
						}
						d := p.Layout(gtx, c, st.section)
						if st.text {
							p.textCatalog(gtx, c, &textUI, p.now.Text, func() {}, nil)
						}
						return d
					})
					if err := core.Shot(c, h, sz.w, sz.h, 1, filepath.Join(out, name), draw, gui.Frames(8), gui.Settle(400)); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

// TestApplyTTSSpecEntry: a service through the universal adapter becomes the
// «Власний сервер» with the catalogue's spec, address, model and voice.
func TestApplyTTSSpecEntry(t *testing.T) {
	cf := config.Default()
	spec := `{"auth":"header:Authorization:Token","submit":{"path":"/v1/speak?model={{model}}","body":{"text":"{{text}}"}},"error":"err_msg"}`
	applyTTSEntry(&cf, ttscat.Entry{Provider: "custom", Protocol: "rest", BaseURL: "https://api.deepgram.com", Model: "aura-2-thalia-en", Spec: []byte(spec)})
	cf.Normalize()
	if cf.TTS.Provider != "custom" || cf.TTS.BaseURL != "https://api.deepgram.com" || cf.TTS.Model != "aura-2-thalia-en" {
		t.Errorf("tts: %+v", cf.TTS)
	}
	if _, err := providers.ParseSpecFor(cf.Custom.TTSSpec, providers.KindTTS); err != nil || !strings.Contains(cf.Custom.TTSSpec, "\n") {
		t.Errorf("spec %q: %v", cf.Custom.TTSSpec, err)
	}
}
