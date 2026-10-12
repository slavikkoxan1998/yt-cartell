package catalog

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
)

// The catalogue files (one per service or group; see the package doc).
//
//go:embed video image text
var files embed.FS

// Entry is what every model entry has (video and image).
type Entry struct {
	ID        string          `json:"id"`
	Label     string          `json:"label"`
	Vendor    string          `json:"vendor"`
	Family    string          `json:"family,omitempty"`
	Host      string          `json:"host"`
	HostLabel string          `json:"hostLabel"`
	Preset    string          `json:"preset"`
	BaseURL   string          `json:"baseUrl,omitempty"`
	Model     string          `json:"model"`
	Spec      json.RawMessage `json:"spec,omitempty"` // preset "universal": the adapter's spec
	Modes     []string        `json:"modes"`
	Price     string          `json:"price,omitempty"`
	Notes     string          `json:"notes,omitempty"`
	Source    string          `json:"source"`
	Checked   string          `json:"checked,omitempty"` // "" = the file's date
	Popular   int             `json:"popular"`           // 0 niche … 3 flagship
}

// VideoEntry is one video model of one service.
type VideoEntry struct {
	Entry
	Lengths     []int    `json:"lengths"`
	Aspects     []string `json:"aspects"`
	Resolutions []string `json:"resolutions"`
	MaxRefs     int      `json:"maxRefs"`
	Audio       string   `json:"audio"` // AudioSpeech | AudioSound | AudioNone
	PromptMax   int      `json:"promptMax"`
}

// ImageEntry is one image model of one service.
type ImageEntry struct {
	Entry
	MaxRefs   int      `json:"maxRefs"`
	Aspects   []string `json:"aspects"`
	Sizes     []string `json:"sizes"`
	PromptMax int      `json:"promptMax"`
	Text      bool     `json:"text"` // renders legible text well
}

// TextEntry is one text (LLM) provider.
type TextEntry struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Vendor       string   `json:"vendor"`
	Protocol     string   `json:"protocol"`
	BaseURL      string   `json:"baseUrl"`
	Models       []string `json:"models"`
	DefaultModel string   `json:"defaultModel"`
	KeyHint      string   `json:"keyHint"`
	KeyOptional  bool     `json:"keyOptional"`
	Local        bool     `json:"local"`
	Vision       bool     `json:"vision"`
	JSON         bool     `json:"json"`
	Docs         string   `json:"docs"`
	Source       string   `json:"source"`
	Notes        string   `json:"notes,omitempty"`
	Checked      string   `json:"checked,omitempty"`
	Popular      int      `json:"popular"`
}

// Video modes.
const (
	ModeT2V       = "t2v"        // text → video
	ModeI2V       = "i2v"        // start frame → video
	ModeRef       = "ref"        // reference images → video (the studio's components mode)
	ModeFirstLast = "first-last" // first + last frame
	ModeV2V       = "v2v"        // video edit / extend
)

// Image modes.
const (
	ModeT2I  = "t2i"  // text → image
	ModeEdit = "edit" // reference / input images → image
)

// Audio of a video model.
const (
	AudioSpeech = "speech" // sound with lip-synced speech («Голос у відео»)
	AudioSound  = "sound"  // sound / music, no reliable speech
	AudioNone   = "none"
)

// Video presets the entries may use (providers.Preset* ids).
var (
	VideoPresets = []string{"fal", "replicate", "runway", "luma", "minimax", "royaltechno", "universal"}
	ImagePresets = []string{"openai", "openai-compat", "fal", "replicate", "royaltechno", "universal"}
)

// file is one catalogue file.
type file[T any] struct {
	List    string `json:"list"`
	Checked string `json:"checked"`
	Entries []T    `json:"entries"`
}

// Data is a loaded catalogue set.
type Data struct {
	Video []VideoEntry
	Image []ImageEntry
	Text  []TextEntry
	// Problems are the files that could not be read (the tests fail on them;
	// the app shows what it could read).
	Problems []string
}

var loaded = sync.OnceValue(func() *Data { return Load(files) })

// Video lists the video entries: flagship first, then in file order. The
// entries are shared — read them, never change them.
func Video() []VideoEntry { return slices.Clone(loaded().Video) }

// Image lists the image entries (see Video).
func Image() []ImageEntry { return slices.Clone(loaded().Image) }

// Text lists the text providers (see Video).
func Text() []TextEntry { return slices.Clone(loaded().Text) }

// Embedded is the catalogue set built into the app (with its Problems).
func Embedded() *Data { return loaded() }

// FindVideo is the entry of model on preset (ok false when the catalogue
// does not know it). Base URLs are not compared: a gateway of the same
// protocol runs the same model.
func FindVideo(preset, model string) (VideoEntry, bool) {
	preset, model = strings.TrimSpace(preset), strings.TrimSpace(model)
	for _, e := range loaded().Video {
		if e.Preset == preset && strings.EqualFold(e.Model, model) {
			return e, true
		}
	}
	return VideoEntry{}, false
}

// FindImage is the entry of model on preset (see FindVideo).
func FindImage(preset, model string) (ImageEntry, bool) {
	preset, model = strings.TrimSpace(preset), strings.TrimSpace(model)
	for _, e := range loaded().Image {
		if e.Preset == preset && strings.EqualFold(e.Model, model) {
			return e, true
		}
	}
	return ImageEntry{}, false
}

// Load reads every catalogue file of fsys (video/, image/, text/ *.json, in
// name order). A broken file is skipped and named in Problems.
func Load(fsys fs.FS) *Data {
	d := &Data{}
	d.Video = loadList[VideoEntry](fsys, "video", d, func(e *VideoEntry, checked string) {
		e.Checked = firstNonEmpty(e.Checked, checked)
	})
	d.Image = loadList[ImageEntry](fsys, "image", d, func(e *ImageEntry, checked string) {
		e.Checked = firstNonEmpty(e.Checked, checked)
	})
	d.Text = loadList[TextEntry](fsys, "text", d, func(e *TextEntry, checked string) {
		e.Checked = firstNonEmpty(e.Checked, checked)
	})
	sortPopular(d.Video, func(e VideoEntry) int { return e.Popular })
	sortPopular(d.Image, func(e ImageEntry) int { return e.Popular })
	sortPopular(d.Text, func(e TextEntry) int { return e.Popular })
	return d
}

func loadList[T any](fsys fs.FS, dir string, d *Data, fix func(e *T, checked string)) []T {
	names, err := fs.Glob(fsys, dir+"/*.json")
	if err != nil {
		d.Problems = append(d.Problems, dir+": "+err.Error())
		return nil
	}
	sort.Strings(names)
	var out []T
	for _, name := range names {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			d.Problems = append(d.Problems, name+": "+err.Error())
			continue
		}
		var f file[T]
		dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&f); err != nil {
			d.Problems = append(d.Problems, fmt.Sprintf("%s: %v", path.Base(name), err))
			continue
		}
		if f.List != "" && f.List != dir {
			d.Problems = append(d.Problems, fmt.Sprintf("%s: list %q in the %s folder", path.Base(name), f.List, dir))
			continue
		}
		for i := range f.Entries {
			fix(&f.Entries[i], f.Checked)
		}
		out = append(out, f.Entries...)
	}
	return out
}

// sortPopular puts the popular entries first, keeping the file order within a tier.
func sortPopular[T any](list []T, pop func(T) int) {
	sort.SliceStable(list, func(i, j int) bool { return pop(list[i]) > pop(list[j]) })
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// HasMode reports whether the entry lists mode.
func (e Entry) HasMode(mode string) bool { return slices.Contains(e.Modes, mode) }

// Haystack is the lower-case text a search looks in.
func (e Entry) Haystack() string {
	return strings.ToLower(strings.Join([]string{e.Label, e.Vendor, e.Family, e.HostLabel, e.Host, e.Model, e.Notes}, " "))
}

// Haystack is the lower-case text a search looks in.
func (e TextEntry) Haystack() string {
	return strings.ToLower(strings.Join(append([]string{e.Label, e.Vendor, e.ID, e.Protocol, e.BaseURL, e.Notes}, e.Models...), " "))
}

// SpecText is the entry's universal spec as indented JSON ("" = none).
func (e Entry) SpecText() string {
	if len(bytes.TrimSpace(e.Spec)) == 0 || string(bytes.TrimSpace(e.Spec)) == "null" {
		return ""
	}
	var out bytes.Buffer
	if err := json.Indent(&out, e.Spec, "", "  "); err != nil {
		return string(e.Spec)
	}
	return out.String()
}
