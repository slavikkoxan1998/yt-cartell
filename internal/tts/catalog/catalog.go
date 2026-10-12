// Package catalog is the voice-over preset catalogue of Settings → Озвучка:
// TTS providers × models × notable voices, self-hosted OpenAI-compatible
// servers and services the studio does not speak yet. The data is embedded
// JSON (data/*.json, one file per group) — update a file, run `go test
// ./internal/tts/catalog`, done. No internal imports.
//
// File shape: {"list": "tts", "checked": "2026-10-05", "entries": [ {…} ]}
//
//	id            unique "<provider-or-vendor>:<model>[:<voice>]"
//	kind          model | voice | server
//	label, vendor display name, maker
//	provider      the studio TTS provider that runs it today (tts.Providers ids:
//	              elevenlabs cartesia openai google azure minimax fish custom), "" = not yet
//	protocol      elevenlabs | cartesia | openai | google | azure | minimax | fish | rest
//	baseUrl       the API base when it is not the provider's default ("" = default)
//	model, voice  ids exactly as the API takes them; voiceName, gender
//	ukrainian     documented to speak Ukrainian
//	languages     how many languages (0 = unknown), languagesNote (Ukrainian)
//	maxChars      characters per request (0 = unknown); concurrency per key (0 = unknown)
//	selfHosted, keyOptional
//	spec          provider "custom" + protocol "rest": the universal adapter's spec (KindTTS)
//	price         only when quoted from the provider's docs
//	notes         one short Ukrainian sentence; source = the page it was checked against
//	popular       0 niche · 1 notable · 2 popular · 3 flagship
package catalog

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
)

//go:embed data
var files embed.FS

// Entry kinds.
const (
	KindModel  = "model"
	KindVoice  = "voice"
	KindServer = "server"
)

// Protocols a TTS entry may name ("rest" = an own REST API the studio does
// not speak yet).
var Protocols = []string{"elevenlabs", "cartesia", "openai", "google", "azure", "minimax", "fish", "custom", "rest"}

// Entry is one TTS preset.
type Entry struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Label         string `json:"label"`
	Vendor        string `json:"vendor"`
	Provider      string `json:"provider"`
	Protocol      string `json:"protocol"`
	BaseURL       string `json:"baseUrl"`
	Model         string `json:"model"`
	Voice         string `json:"voice"`
	VoiceName     string `json:"voiceName"`
	Gender        string `json:"gender"`
	Ukrainian     bool   `json:"ukrainian"`
	Languages     int    `json:"languages"`
	LanguagesNote string `json:"languagesNote"`
	MaxChars      int    `json:"maxChars"`
	Concurrency   int    `json:"concurrency"`
	SelfHosted    bool   `json:"selfHosted"`
	KeyOptional   bool   `json:"keyOptional"`
	// Spec (provider "custom", protocol "rest") describes the service for the
	// universal adapter (config.CustomAPI.TTSSpec, providers.KindTTS).
	Spec    json.RawMessage `json:"spec,omitempty"`
	Price   string          `json:"price"`
	Notes   string          `json:"notes"`
	Source  string          `json:"source"`
	Checked string          `json:"checked,omitempty"`
	Popular int             `json:"popular"`
}

// Data is a loaded catalogue.
type Data struct {
	Entries  []Entry
	Problems []string // files that could not be read
}

var loaded = sync.OnceValue(func() *Data { return Load(files) })

// All lists the entries: flagship first, then in file order (shared — read only).
func All() []Entry { return slices.Clone(loaded().Entries) }

// Embedded is the catalogue built into the app (with its Problems).
func Embedded() *Data { return loaded() }

// Supported reports whether the studio can voice with the entry today:
// providerOK says whether a provider id exists (tts.ProviderByID).
func (e Entry) Supported(providerOK func(id string) bool) bool {
	return e.Provider != "" && providerOK(e.Provider)
}

// Haystack is the lower-case text a search looks in.
func (e Entry) Haystack() string {
	return strings.ToLower(strings.Join([]string{e.Label, e.Vendor, e.Provider, e.Model, e.Voice, e.VoiceName, e.LanguagesNote, e.Notes}, " "))
}

// Load reads data/*.json of fsys in name order; a broken file is skipped and
// named in Problems.
func Load(fsys fs.FS) *Data {
	d := &Data{}
	names, err := fs.Glob(fsys, "data/*.json")
	if err != nil {
		d.Problems = append(d.Problems, err.Error())
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			d.Problems = append(d.Problems, name+": "+err.Error())
			continue
		}
		var f struct {
			List    string  `json:"list"`
			Checked string  `json:"checked"`
			Entries []Entry `json:"entries"`
		}
		dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&f); err != nil {
			d.Problems = append(d.Problems, fmt.Sprintf("%s: %v", path.Base(name), err))
			continue
		}
		if f.List != "" && f.List != "tts" {
			d.Problems = append(d.Problems, fmt.Sprintf("%s: list %q (want tts)", path.Base(name), f.List))
			continue
		}
		for i := range f.Entries {
			if strings.TrimSpace(f.Entries[i].Checked) == "" {
				f.Entries[i].Checked = f.Checked
			}
		}
		d.Entries = append(d.Entries, f.Entries...)
	}
	sort.SliceStable(d.Entries, func(i, j int) bool { return d.Entries[i].Popular > d.Entries[j].Popular })
	return d
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Check lists what is wrong with the catalogue (empty = every entry is usable).
// knownProvider says whether a studio provider id exists (tts.ProviderByID).
func Check(d *Data, knownProvider func(id string) bool) []string {
	out := slices.Clone(d.Problems)
	add := func(id, f string, a ...any) { out = append(out, id+": "+fmt.Sprintf(f, a...)) }
	seen := map[string]bool{}
	for _, e := range d.Entries {
		id := e.ID
		if id == "" {
			add(e.Label, "no id")
		}
		if seen[id] {
			add(id, "duplicate id")
		}
		seen[id] = true
		if e.Label == "" || e.Vendor == "" {
			add(id, "no label or vendor")
		}
		if !slices.Contains([]string{KindModel, KindVoice, KindServer}, e.Kind) {
			add(id, "kind %q (model | voice | server)", e.Kind)
		}
		if !slices.Contains(Protocols, e.Protocol) {
			add(id, "protocol %q", e.Protocol)
		}
		if e.Provider != "" && !knownProvider(e.Provider) {
			add(id, "provider %q is not a studio TTS provider", e.Provider)
		}
		hasSpec := e.SpecText() != ""
		switch {
		case e.Provider == "" && e.Protocol != "rest":
			add(id, "protocol %q is spoken by the studio: name its provider", e.Protocol)
		case e.Protocol == "rest" && e.Provider != "" && !(e.Provider == "custom" && hasSpec):
			add(id, "protocol rest with provider %q (only custom with a spec)", e.Provider)
		case hasSpec && (e.Provider != "custom" || e.Protocol != "rest"):
			add(id, "a spec belongs to provider custom + protocol rest")
		case hasSpec && e.BaseURL == "":
			add(id, "a spec without a baseUrl")
		}
		if e.Kind == KindVoice && e.Voice == "" {
			add(id, "a voice entry without a voice id")
		}
		if e.Kind == KindServer && e.BaseURL == "" {
			add(id, "a server entry without a baseUrl")
		}
		for name, u := range map[string]string{"baseUrl": e.BaseURL, "source": e.Source} {
			switch {
			case u == "" && name == "source":
				add(id, "no source")
			case u == "":
			case strings.ContainsAny(u, " \t\n"):
				add(id, "%s has spaces", name)
			case name == "source" && !strings.HasPrefix(u, "https://"):
				add(id, "source must be https: %q", u)
			case !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "wss://"):
				add(id, "%s must be a URL: %q", name, u)
			}
		}
		if e.Languages < 0 || e.MaxChars < 0 || e.Concurrency < 0 {
			add(id, "negative languages / maxChars / concurrency")
		}
		if e.Popular < 0 || e.Popular > 3 {
			add(id, "popular %d (0–3)", e.Popular)
		}
		if !dateRe.MatchString(e.Checked) {
			add(id, "checked %q (YYYY-MM-DD)", e.Checked)
		}
	}
	return out
}

// SpecText is the entry's universal spec as indented JSON ("" = none).
func (e Entry) SpecText() string {
	b := bytes.TrimSpace(e.Spec)
	if len(b) == 0 || string(b) == "null" {
		return ""
	}
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		return string(b)
	}
	return out.String()
}
