package catalog

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	ratioRe = regexp.MustCompile(`^\d{1,2}(\.\d+)?:\d{1,2}(\.\d+)?$`)
	dateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// TextProtocols are the protocols a text entry may name.
var TextProtocols = []string{"openai", "anthropic", "gemini", "azure-openai", "bedrock", "vertex", "cohere", "ollama"}

// Check lists what is wrong with the catalogue set: missing fields, unknown
// presets or modes, duplicates, specs where none belong. Specs themselves are
// parsed by the tests (providers.ParseSpec — this package imports nothing of
// the app). Empty = every entry is usable.
func Check(d *Data) []string {
	out := slices.Clone(d.Problems)
	add := func(id, f string, a ...any) { out = append(out, id+": "+fmt.Sprintf(f, a...)) }
	seen := map[string]bool{}
	common := func(kind string, e Entry, presets, modes []string) {
		id := kind + " " + e.ID
		if e.ID == "" {
			add(kind+" "+e.Model, "no id")
		}
		if seen[kind+e.ID] {
			add(id, "duplicate id")
		}
		seen[kind+e.ID] = true
		for name, v := range map[string]string{"label": e.Label, "vendor": e.Vendor, "host": e.Host, "hostLabel": e.HostLabel, "model": e.Model} {
			if strings.TrimSpace(v) == "" {
				add(id, "no %s", name)
			}
		}
		if !slices.Contains(presets, e.Preset) {
			add(id, "unknown preset %q", e.Preset)
		}
		hasSpec := e.SpecText() != ""
		if e.Preset == "universal" && !hasSpec {
			add(id, "the universal preset needs a spec")
		}
		if e.Preset != "universal" && hasSpec {
			add(id, "a spec on preset %q (only universal takes one)", e.Preset)
		}
		if e.Preset == "universal" && e.BaseURL == "" {
			add(id, "the universal preset needs a baseUrl")
		}
		checkURL(add, id, "baseUrl", e.BaseURL, false)
		checkURL(add, id, "source", e.Source, true)
		if len(e.Modes) == 0 {
			add(id, "no modes")
		}
		for _, m := range e.Modes {
			if !slices.Contains(modes, m) {
				add(id, "unknown mode %q", m)
			}
		}
		if e.Popular < 0 || e.Popular > 3 {
			add(id, "popular %d (0–3)", e.Popular)
		}
		checkDate(add, id, e.Checked)
	}
	for _, e := range d.Video {
		common("video", e.Entry, VideoPresets, []string{ModeT2V, ModeI2V, ModeRef, ModeFirstLast, ModeV2V})
		id := "video " + e.ID
		if !slices.Contains([]string{AudioSpeech, AudioSound, AudioNone}, e.Audio) {
			add(id, "audio %q (speech | sound | none)", e.Audio)
		}
		for i, l := range e.Lengths {
			if l < 1 || l > 120 || i > 0 && l <= e.Lengths[i-1] {
				add(id, "lengths must be ascending seconds 1–120: %v", e.Lengths)
				break
			}
		}
		checkAspects(add, id, e.Aspects)
		if e.MaxRefs < 0 || e.MaxRefs > 50 {
			add(id, "maxRefs %d", e.MaxRefs)
		}
		if e.HasMode(ModeRef) && e.MaxRefs == 0 {
			add(id, "mode ref needs maxRefs")
		}
		if e.PromptMax < 0 {
			add(id, "promptMax %d", e.PromptMax)
		}
	}
	for _, e := range d.Image {
		common("image", e.Entry, ImagePresets, []string{ModeT2I, ModeEdit})
		id := "image " + e.ID
		checkAspects(add, id, e.Aspects)
		if e.HasMode(ModeEdit) && e.MaxRefs == 0 {
			add(id, "mode edit needs maxRefs")
		}
		if e.MaxRefs > 0 && !e.HasMode(ModeEdit) {
			add(id, "maxRefs without mode edit")
		}
		if e.MaxRefs < 0 || e.MaxRefs > 50 || e.PromptMax < 0 {
			add(id, "maxRefs %d / promptMax %d", e.MaxRefs, e.PromptMax)
		}
	}
	for _, e := range d.Text {
		id := "text " + e.ID
		if e.ID == "" || e.Label == "" || e.Vendor == "" {
			add(id, "no id, label or vendor")
		}
		if seen["text"+e.ID] {
			add(id, "duplicate id")
		}
		seen["text"+e.ID] = true
		if !slices.Contains(TextProtocols, e.Protocol) {
			add(id, "unknown protocol %q", e.Protocol)
		}
		if e.BaseURL == "" {
			add(id, "no baseUrl")
		}
		checkURL(add, id, "baseUrl", e.BaseURL, false)
		checkURL(add, id, "docs", e.Docs, true)
		checkURL(add, id, "source", e.Source, true)
		if e.DefaultModel != "" && !slices.Contains(e.Models, e.DefaultModel) {
			add(id, "defaultModel %q is not in models", e.DefaultModel)
		}
		if e.Popular < 0 || e.Popular > 3 {
			add(id, "popular %d (0–3)", e.Popular)
		}
		checkDate(add, id, e.Checked)
	}
	return out
}

func checkURL(add func(id, f string, a ...any), id, name, u string, required bool) {
	switch {
	case u == "" && required:
		add(id, "no %s", name)
	case u == "":
	case strings.ContainsAny(u, " \t\n"):
		add(id, "%s has spaces: %q", name, u)
	case required && !strings.HasPrefix(u, "https://"):
		add(id, "%s must be an https URL: %q", name, u)
	case !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://"):
		add(id, "%s must be an http(s) URL: %q", name, u)
	}
}

func checkAspects(add func(id, f string, a ...any), id string, aspects []string) {
	for _, a := range aspects {
		if !ratioRe.MatchString(a) {
			add(id, "aspect %q is not a ratio", a)
		}
	}
}

func checkDate(add func(id, f string, a ...any), id, d string) {
	if !dateRe.MatchString(d) {
		add(id, "checked %q (YYYY-MM-DD)", d)
		return
	}
	if _, err := time.Parse("2006-01-02", d); err != nil {
		add(id, "checked %q: %v", d, err)
	}
}
