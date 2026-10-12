package config

// Settings profiles: named presets of the creative step-1 settings and the
// montage defaults ("YouTube історія 16:9", "Shorts / Reels 9:16", …),
// persisted as profiles.json next to config.json.

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Profile is one settings preset.
type Profile struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	TargetSeconds   int    `json:"targetSeconds"`
	Aspect          string `json:"aspect"`
	Style           string `json:"style"`
	CustomStyle     string `json:"customStyle"`
	Genre           string `json:"genre"`
	Tempo           string `json:"tempo"`
	Camera          string `json:"camera"`
	ImageModel      string `json:"imageModel"`
	Voiceover       bool   `json:"voiceover"`
	NarratorVoiceID string `json:"narratorVoiceId"`
	// NarratorStyle is the kind of narrator voice picked automatically while
	// NarratorVoiceID is "" — a studio.NarratorStyles id ("" = any voice).
	NarratorStyle  string `json:"narratorStyle,omitempty"`
	Language       string `json:"language"`
	Tone           string `json:"tone"`
	Notes          string `json:"notes"`
	StoryboardMode string `json:"storyboardMode"`
	ChainFrames    bool   `json:"chainFrames"`
	// AudioMode: "" (Voiceover decides) | "voiceover" | "invideo" (the
	// characters speak in the clips) | "silent" — studio.Input.AudioMode.
	AudioMode string `json:"audioMode,omitempty"`
	// Content: "" (characters) | "characters" | "nopeople" | "motion" |
	// "mixed" — studio.Input.Content.
	Content string `json:"content,omitempty"`
	// Overlays: animated on-screen text — "" (auto) | "on" | "off".
	Overlays string `json:"overlays,omitempty"`
	// Pipeline («Режим виробництва»): "" (full) | "norefs" | "stills" |
	// "norefs+stills" | "code" | "norefs+code" — studio.Input.Pipeline.
	Pipeline string `json:"pipeline,omitempty"`
	// CodeGrammar: the animation grammar of a «Анімація кодом» film ("" =
	// auto) — studio.Input.CodeGrammar.
	CodeGrammar string `json:"codeGrammar,omitempty"`
	Transition  string `json:"transition"` // media transition catalog id (validated when applied)
	// Montage look (catalog ids; validated when applied to a project).
	TransitionDuration float64 `json:"transitionDuration,omitempty"` // seconds, 0 = default
	Filter             string  `json:"filter,omitempty"`             // colour look, "" = none
	Effect             string  `json:"effect,omitempty"`             // motion of stills, "" = gentle zoom-in
	SubtitlePreset     string  `json:"subtitlePreset,omitempty"`     // "" = classic white
	Subtitles          string  `json:"subtitles"`
	MusicVolume        float64 `json:"musicVolume"`
	AmbientVolume      float64 `json:"ambientVolume"`
	Ducking            bool    `json:"ducking"`
	CreatedAt          int64   `json:"createdAt"`
	UpdatedAt          int64   `json:"updatedAt"`
}

// ErrProfileNotFound: there is no profile with this id.
var ErrProfileNotFound = errors.New("Профіль не знайдено")

const profilesFile = "profiles.json"

// ProfilesPath is where the profiles of the user's settings folder live.
func ProfilesPath() string { return filepath.Join(Dir(), profilesFile) }

// StarterProfiles are the presets a fresh install starts with (stable ids,
// so Autopilot.DefaultProfileID can point at them).
func StarterProfiles() []Profile {
	return []Profile{
		{
			ID: "starter-youtube", Name: "YouTube історія 16:9",
			TargetSeconds: 300, Aspect: "16:9", Style: "cinematic", Tempo: "balanced", Camera: "cinematic",
			ImageModel: "NARWHAL", Voiceover: true, Tone: "storyteller",
			Transition: "fade", Subtitles: "srt", MusicVolume: 0.18, AmbientVolume: 0.25, Ducking: true,
		},
		{
			ID: "starter-shorts", Name: "Shorts / Reels 9:16",
			TargetSeconds: 48, Aspect: "9:16", Style: "cinematic", Tempo: "fast", Camera: "dynamic",
			ImageModel: "NARWHAL", Voiceover: true, Tone: "youtuber",
			Notes:      "Сильний гачок у першій сцені, коротко й динамічно.",
			Transition: "none", Subtitles: "burn", MusicVolume: 0.22, AmbientVolume: 0.2, Ducking: true,
		},
		{
			ID: "starter-kids", Name: "Дитяча казка",
			TargetSeconds: 180, Aspect: "16:9", Style: "family-3d", Genre: "fairy tale", Tempo: "balanced", Camera: "medium",
			ImageModel: "NARWHAL", Voiceover: true, Tone: "kids",
			Notes:      "Добра історія для дітей 4–8 років: без страшних сцен і насильства, прості слова, щасливий кінець.",
			Transition: "fade", Subtitles: "srt", MusicVolume: 0.2, AmbientVolume: 0.2, Ducking: true,
		},
		{
			ID: "starter-explainer", Name: "Документальне пояснення",
			TargetSeconds: 300, Aspect: "16:9", Style: "infographic", Genre: "documentary", Tempo: "balanced", Camera: "tripod",
			ImageModel: "NARWHAL", Voiceover: true, Tone: "documentary",
			Transition: "fade", Subtitles: "srt", MusicVolume: 0.12, AmbientVolume: 0.15, Ducking: true,
		},
	}
}

// builtinByID is every shipped starter profile by id.
func builtinByID() map[string]Profile {
	m := map[string]Profile{}
	for _, p := range StarterProfiles() {
		m[p.ID] = p
	}
	return m
}

// laterStarters are starter profiles shipped after profiles.json existed on
// users' machines: an existing store gets each of them ONCE (see
// offerStarters) — a user who deletes one never sees it again. None now.
var laterStarters []string

// startersFile remembers which laterStarters this profiles.json was offered.
const startersFile = "profiles-starters.json"

type startersMark struct {
	Offered []string `json:"offered"`
}

func readStartersMark(dir string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(dir, startersFile))
	if err != nil {
		return out
	}
	var m startersMark
	if json.Unmarshal(b, &m) == nil {
		for _, id := range m.Offered {
			out[id] = true
		}
	}
	return out
}

func writeStartersMark(dir string, offered map[string]bool) error {
	var m startersMark
	for _, id := range laterStarters {
		if offered[id] {
			m.Offered = append(m.Offered, id)
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, startersFile), b, 0o600)
}

// offerStarters is the one-time migration of an existing store: every
// laterStarters profile not offered yet is appended unless the list already
// has its id or a profile of the same name (a user's own profile is never
// replaced). Idempotent; the list is written before the mark, so a failed
// write offers it again next time.
func offerStarters(dir string, list []Profile) (out []Profile, changed bool, offered map[string]bool, marked bool) {
	offered = readStartersMark(dir)
	out = list
	byID := builtinByID()
	now := time.Now().UnixMilli()
	for _, id := range laterStarters {
		if offered[id] {
			continue
		}
		offered[id] = true
		marked = true
		st := byID[id]
		taken := indexOf(out, id) >= 0
		for _, p := range out {
			if strings.EqualFold(strings.TrimSpace(p.Name), strings.TrimSpace(st.Name)) {
				taken = true
			}
		}
		if taken {
			continue
		}
		st.CreatedAt, st.UpdatedAt = now, now
		st.Normalize()
		out = append(out, st)
		changed = true
	}
	return out, changed, offered, marked
}

func allOffered() map[string]bool {
	m := map[string]bool{}
	for _, id := range laterStarters {
		m[id] = true
	}
	return m
}

// ProfileStore holds the profiles; safe for concurrent use. A store with a
// path writes every change atomically; a memory store never touches disk.
type ProfileStore struct {
	mu   sync.RWMutex
	wmu  sync.Mutex // serialises read-modify-write (Save, Delete)
	path string     // "" = memory only
	list []Profile
}

// NewProfileStore opens the profiles of the user's settings folder.
func NewProfileStore() (*ProfileStore, error) { return NewProfileStoreAt(Dir()) }

// NewProfileStoreAt opens <dir>/profiles.json. The first run (no file) seeds
// the starter profiles. A damaged file is kept aside as
// profiles.broken-<time>.json and the starters are used. The store is usable
// even when err != nil (then changes may fail to persist).
func NewProfileStoreAt(dir string) (*ProfileStore, error) {
	s := &ProfileStore{path: filepath.Join(dir, profilesFile)}
	b, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.list = seed()
		if err := s.write(s.list); err != nil {
			return s, err
		}
		_ = writeStartersMark(dir, allOffered()) // the seed holds every starter
		return s, nil
	case err != nil:
		s.list = seed()
		return s, fmt.Errorf("не вдалося прочитати профілі: %w", err)
	}
	var list []Profile
	if jerr := json.Unmarshal(b, &list); jerr != nil {
		bad := filepath.Join(dir, fmt.Sprintf("profiles.broken-%d.json", time.Now().Unix()))
		_ = os.Rename(s.path, bad)
		s.list = seed()
		if werr := s.write(s.list); werr != nil {
			return s, werr
		}
		_ = writeStartersMark(dir, allOffered())
		return s, fmt.Errorf("файл профілів пошкоджений — його збережено як %s, створено стартові профілі", filepath.Base(bad))
	}
	s.list = clean(list)
	// Starters shipped later reach existing users once.
	next, changed, offered, marked := offerStarters(dir, s.list)
	if changed {
		if err := s.write(next); err != nil {
			return s, nil // offered again next time; the loaded list stays usable
		}
		s.list = next
	}
	if marked {
		_ = writeStartersMark(dir, offered)
	}
	return s, nil
}

// NewMemoryProfileStore is a store that never writes to disk (tests, headless
// screenshots), seeded with the starter profiles.
func NewMemoryProfileStore() *ProfileStore { return &ProfileStore{list: seed()} }

// ProfileStoreFor is the profile store matching a settings store: the user's
// profiles.json for a store that persists (NewStore), a memory store for an
// in-memory one (NewStoreWith) — so tests and screenshots never touch the
// user's files.
func ProfileStoreFor(cfg *Store) (*ProfileStore, error) {
	if cfg == nil || !cfg.persist {
		return NewMemoryProfileStore(), nil
	}
	return NewProfileStore()
}

// Path of the backing file ("" for a memory store).
func (s *ProfileStore) Path() string { return s.path }

// List returns every profile (a copy, in creation order).
func (s *ProfileStore) List() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Profile{}, s.list...)
}

// Get finds a profile.
func (s *ProfileStore) Get(id string) (Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.list {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// Save creates a profile (ID == "") or replaces the one with p.ID
// (ErrProfileNotFound when there is none). Values are normalized; the saved
// profile is returned. Nothing changes when the file cannot be written.
func (s *ProfileStore) Save(p Profile) (Profile, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	next := s.List()
	now := time.Now().UnixMilli()
	p.ID = strings.TrimSpace(p.ID)
	i := -1
	if p.ID == "" {
		p.ID = newProfileID(next)
		p.CreatedAt = now
	} else if i = indexOf(next, p.ID); i < 0 {
		return Profile{}, ErrProfileNotFound
	} else {
		p.CreatedAt = next[i].CreatedAt
	}
	p.UpdatedAt = now
	p.Normalize()
	if i < 0 {
		next = append(next, p)
	} else {
		next[i] = p
	}
	if err := s.write(next); err != nil {
		return Profile{}, err
	}
	s.mu.Lock()
	s.list = next
	s.mu.Unlock()
	return p, nil
}

// Delete removes a profile (ErrProfileNotFound when there is none).
func (s *ProfileStore) Delete(id string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	next := s.List()
	i := indexOf(next, id)
	if i < 0 {
		return ErrProfileNotFound
	}
	next = append(next[:i], next[i+1:]...)
	if err := s.write(next); err != nil {
		return err
	}
	s.mu.Lock()
	s.list = next
	s.mu.Unlock()
	return nil
}

// write stores list atomically (temp file + rename); a memory store keeps it.
func (s *ProfileStore) write(list []Profile) error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("не вдалося зберегти профілі: %w", err)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("не вдалося зберегти профілі: %w", err)
	}
	for i := 0; ; i++ { // Windows: an indexer or antivirus may hold the target for a moment
		if err = os.Rename(tmp, s.path); err == nil || i == 5 {
			break
		}
		time.Sleep(time.Duration(i+1) * 40 * time.Millisecond)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("не вдалося зберегти профілі: %w", err)
	}
	return nil
}

// Normalize trims and clamps the values so a profile can always be applied.
// Unknown catalogue ids (style, genre, camera, language, tone) are kept: the
// catalogue lives in the studio package, which validates them.
func (p *Profile) Normalize() {
	p.Name = clipText(strings.TrimSpace(p.Name), 80)
	if p.Name == "" {
		p.Name = "Профіль"
	}
	if p.TargetSeconds != 0 {
		p.TargetSeconds = max(8, min(3600, p.TargetSeconds))
	}
	if p.Aspect != "9:16" {
		p.Aspect = "16:9"
	}
	switch p.Tempo {
	case "slow", "balanced", "fast", "rapid":
	default:
		p.Tempo = "balanced"
	}
	switch p.ImageModel {
	case "NARWHAL", "GEM_PIX_2", "GEM_PIX":
	default:
		p.ImageModel = "NARWHAL"
	}
	if p.Transition = clipText(strings.TrimSpace(p.Transition), 40); p.Transition == "" {
		p.Transition = "fade"
	}
	if d := p.TransitionDuration; math.IsNaN(d) || math.IsInf(d, 0) || d < 0 {
		p.TransitionDuration = 0
	} else if d > 5 {
		p.TransitionDuration = 5
	}
	p.Filter = clipText(strings.TrimSpace(p.Filter), 40)
	p.Effect = clipText(strings.TrimSpace(p.Effect), 40)
	p.SubtitlePreset = clipText(strings.TrimSpace(p.SubtitlePreset), 40)
	switch p.Subtitles {
	case "none", "srt", "burn":
	default:
		p.Subtitles = "srt"
	}
	p.Style = clipText(strings.TrimSpace(p.Style), 60)
	p.CustomStyle = clipText(p.CustomStyle, 1000)
	p.Genre = clipText(strings.TrimSpace(p.Genre), 40)
	p.Camera = clipText(strings.TrimSpace(p.Camera), 40)
	p.NarratorVoiceID = clipText(strings.TrimSpace(p.NarratorVoiceID), 80)
	p.NarratorStyle = clipText(strings.TrimSpace(p.NarratorStyle), 40)
	p.Language = clipText(strings.ToLower(strings.TrimSpace(p.Language)), 16)
	p.Tone = clipText(strings.TrimSpace(p.Tone), 40)
	p.Notes = clipText(p.Notes, 2000)
	p.StoryboardMode = clipText(strings.TrimSpace(p.StoryboardMode), 40)
	switch p.AudioMode {
	case "", "voiceover", "invideo", "silent":
	default:
		p.AudioMode = ""
	}
	if p.AudioMode != "" {
		p.Voiceover = p.AudioMode == "voiceover"
	}
	switch p.Content {
	case "", "characters", "nopeople", "motion", "mixed":
	default:
		p.Content = ""
	}
	switch p.Overlays {
	case "", "on", "off":
	default:
		p.Overlays = ""
	}
	switch p.Pipeline {
	case "", "norefs", "stills", "norefs+stills", "code", "norefs+code":
	default:
		p.Pipeline = ""
	}
	p.CodeGrammar = clipText(strings.ToLower(strings.TrimSpace(p.CodeGrammar)), 48)
	p.MusicVolume = clampUnit(p.MusicVolume)
	p.AmbientVolume = clampUnit(p.AmbientVolume)
}

func seed() []Profile {
	now := time.Now().UnixMilli()
	list := StarterProfiles()
	for i := range list {
		list[i].CreatedAt, list[i].UpdatedAt = now, now
		list[i].Normalize()
	}
	return list
}

// clean normalizes a loaded list: every profile gets a unique id.
func clean(list []Profile) []Profile {
	out := make([]Profile, 0, len(list))
	seen := map[string]bool{}
	for _, p := range list {
		p.ID = strings.TrimSpace(p.ID)
		if p.ID == "" || seen[p.ID] {
			p.ID = newProfileID(out)
		}
		seen[p.ID] = true
		p.Normalize()
		out = append(out, p)
	}
	return out
}

func indexOf(list []Profile, id string) int {
	for i, p := range list {
		if p.ID == id {
			return i
		}
	}
	return -1
}

const idChars = "abcdefghijkmnpqrstuvwxyz23456789"

func newProfileID(taken []Profile) string {
	for {
		b := make([]byte, 10)
		_, _ = rand.Read(b)
		for i := range b {
			b[i] = idChars[int(b[i])%len(idChars)]
		}
		id := "p-" + string(b)
		if indexOf(taken, id) < 0 {
			return id
		}
	}
}

func clipText(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func clampUnit(x float64) float64 {
	if x != x { // NaN
		return 0
	}
	return max(0, min(1, x))
}
