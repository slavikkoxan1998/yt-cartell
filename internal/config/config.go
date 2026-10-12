// Package config holds the application settings (providers, keys, paths),
// persisted as JSON in the user's config directory, and a thread-safe Store
// that the UI and the engine share.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Text generation (story → plan, prompts).
type Text struct {
	// Provider is the protocol: "openai" (OpenAI-compatible /chat/completions,
	// e.g. CarteLink, OpenRouter) | "anthropic" (Messages API) | "ollama"
	// (native /api/chat) | "azure" (Azure OpenAI: v1 API or a deployment URL)
	// | "google" (Gemini through the Google AI Studio keys).
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"` // e.g. http://127.0.0.1:8317/v1
	APIKey   string `json:"apiKey"`
	Model    string `json:"model"`
	// Auth is how the key travels: "" = the protocol's own (Bearer;
	// Anthropic x-api-key; Azure api-key) | "bearer" | "x-api-key" |
	// "api-key" | "none".
	Auth string `json:"auth,omitempty"`
	// APIVersion is the api-version of an Azure deployment URL ("" = the
	// default GA version; the v1 API needs none).
	APIVersion string `json:"apiVersion,omitempty"`
}

// TextProviders are the stored Text.Provider ids (never rename them).
var TextProviders = []string{"openai", "anthropic", "ollama", "azure", "google"}

// OpenAICompatible: the text endpoint speaks the OpenAI wire format, so
// other services (Whisper STT, dictation, the remake's vision) may reuse its
// address and key.
func (t Text) OpenAICompatible() bool { return t.Provider == "" || t.Provider == "openai" }

// TextDefaultBaseURL is the address a protocol starts with ("" = none: the
// user's own resource, e.g. Azure).
func TextDefaultBaseURL(provider string) string {
	switch provider {
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "ollama":
		return "http://127.0.0.1:11434"
	case "azure":
		return ""
	}
	return Default().Text.BaseURL // openai, and google's stored OpenAI-compatible side
}

// Image generation provider.
type Image struct {
	Provider string `json:"provider"` // "glabs" | "google"
}

// Video generation provider and parameters.
type Video struct {
	Provider   string `json:"provider"`   // "glabs" | "google"
	GlabsModel string `json:"glabsModel"` // veo_31_fast | veo_31_lite | veo_31_quality | veo_31_lite_relaxed | omni_flash
	Resolution string `json:"resolution"` // 720p | 1080p | 4K
	Length     int    `json:"length"`     // 4 | 6 | 8 seconds
	// Dynamic: each scene's clip is the shortest allowed length that covers
	// its voice line (5.7 s of speech → a 6 s clip instead of 10 s).
	Dynamic bool `json:"dynamic"`
	// Ultra: the Google account has AI Ultra — G-Labs Veo then renders 4 and
	// 6 s clips, 4K and veo_31_lite_relaxed (Omni Flash does 4/6 s without it).
	Ultra bool `json:"ultra"`
}

// G-Labs Studio local Webhook API.
type Glabs struct {
	BaseURL string `json:"baseUrl"` // http://127.0.0.1:8765
	APIKey  string `json:"apiKey"`
}

// Google AI Studio (Gemini / Veo) — only used when a provider above is "google".
type Google struct {
	Keys       []string `json:"keys"`
	StoryModel string   `json:"storyModel"` // default gemini-3.8-flash
	ImageModel string   `json:"imageModel"` // default gemini-3.1-flash-image
	// VeoModel is the video model (historic name): default gemini-omni-1.1-flash
	// (Gemini Omni Flash); the Veo 3.1 previews until their shutdown (google.go).
	VeoModel string `json:"veoModel"`
	// OmniMigrated: the one-time move from the old Veo default to Omni is done.
	OmniMigrated bool `json:"omniMigrated,omitempty"`
}

// TTS (voiceover): see tts.go — settings per provider, the flat fields are
// the active provider's view.

// Autopilot controls unattended runs (story text → finished mp4).
type Autopilot struct {
	DefaultProfileID string `json:"defaultProfileId"` // profile applied to autopilot projects ("" = app defaults)
	MakeCover        bool   `json:"makeCover"`        // also draw the YouTube cover
	Retries          int    `json:"retries"`          // extra attempts for failed frames/clips/voices (0–3)
	ContinueOnError  bool   `json:"continueOnError"`  // legacy (default on, never asked): the studio no longer reads it — see OnFailed
	// OnFailed is what the autopilot's montage does with scenes that are not
	// ready, as the user chose it in Settings: FailedAsk ("" = stop and ask
	// in the montage step), FailedSkip, FailedStill, FailedStop.
	OnFailed string `json:"onFailed,omitempty"`
}

// Autopilot.OnFailed values (stored on disk — never rename).
const (
	FailedAsk   = ""      // not chosen: stop before the montage and ask
	FailedSkip  = "skip"  // montage without the failed scenes
	FailedStill = "still" // the frame instead of a failed clip
	FailedStop  = "stop"  // stop before the montage
)

// STT (speech-to-text) transcribes an uploaded voice-over ("Своя озвучка").
type STT struct {
	// Provider: "elevenlabs" (Scribe; APIKey "" = the ElevenLabs key stored for
	// the voice-over) | "openai" (Whisper-compatible POST {BaseURL}/audio/transcriptions;
	// BaseURL "" = the Text endpoint, APIKey "" = the Text key) | "assemblyai"
	// (AssemblyKey; AssemblyEU keeps the data in the EU) | "custom" (any API
	// described by Custom.STTSpec at BaseURL with APIKey).
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`
	Model    string `json:"model"` // elevenlabs: scribe_v2 · openai: whisper-1 ("" = provider default)
	// Provider "local": whisper.cpp on this computer (no internet, no key).
	LocalBin   string `json:"localBin,omitempty"`   // whisper.cpp CLI path ("" = auto: next to the exe, app data, PATH)
	LocalModel string `json:"localModel,omitempty"` // ggml model file path ("" = the downloaded default)
	// Provider "assemblyai": its own key (never mixed with APIKey, which the
	// other services share) and the EU endpoint (api.eu.assemblyai.com).
	AssemblyKey string `json:"assemblyKey,omitempty"`
	AssemblyEU  bool   `json:"assemblyEu,omitempty"`
}

// Telegram sends job notifications through the user's own bot, and lets the
// owner control the studio through the same bot (package tgbot; telegram.go).
type Telegram struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"botToken"`
	ChatID   string `json:"chatId"`
	OnDone   bool   `json:"onDone"`  // a stage or an autopilot run finished
	OnError  bool   `json:"onError"` // a stage failed
	OnFinal  bool   `json:"onFinal"` // the final video is ready (message + cover image)

	// Remote control («Керування через бота», telegram.go).
	Remote    bool           `json:"remote,omitempty"`    // the bot takes commands (needs a pairing: OwnerID)
	OwnerID   int64          `json:"ownerId,omitempty"`   // who paired with /start <code>
	OwnerName string         `json:"ownerName,omitempty"` // shown in Settings
	Allowed   []TelegramUser `json:"allowed,omitempty"`   // more people who may control it in the owner's group
	Live      bool           `json:"live"`                // one status message per running job, edited in place
	APIServer string         `json:"apiServer,omitempty"` // own Bot API server ("" = api.telegram.org)
}

// UI holds desktop-app preferences.
type UI struct {
	Theme         string `json:"theme"`         // "light" | "dark" | "system"
	ReducedMotion bool   `json:"reducedMotion"` // "Менше анімацій": transitions become instant
	TourDone      bool   `json:"tourDone"`      // the first-run tour was finished or skipped
	TipsHidden    bool   `json:"tipsHidden"`    // hide the smart tip cards
	// AutoUpdate: look for a new version on GitHub a few seconds after the
	// start, at most every 6 h (default on). Installing always waits for a click.
	AutoUpdate bool `json:"autoUpdate"`
	// UpdateChecked is when the last check succeeded (Unix seconds).
	UpdateChecked int64 `json:"updateChecked,omitempty"`
	// UpdateSeen is the newest version that check found ("2.1.0"; "" = none).
	UpdateSeen string `json:"updateSeen,omitempty"`
}

// Config is the whole persisted settings document.
type Config struct {
	// DataDir is where projects and every generated file are stored.
	// The user can pick any folder; default: <Documents>/YT Cartell Studio.
	DataDir     string    `json:"dataDir"`
	FFmpegPath  string    `json:"ffmpegPath"`  // "" = auto (next to exe, then PATH)
	FFprobePath string    `json:"ffprobePath"` // "" = auto
	Text        Text      `json:"text"`
	Image       Image     `json:"image"`
	Video       Video     `json:"video"`
	Glabs       Glabs     `json:"glabs"`
	Google      Google    `json:"google"`
	TTS         TTS       `json:"tts"`
	Concurrency int       `json:"concurrency"` // scenes rendered at once (1–MaxConcurrency; the engine clamps per service)
	Autopilot   Autopilot `json:"autopilot"`
	STT         STT       `json:"stt"`
	Dictation   Dictation `json:"dictation"` // voice typing in the Home story box (dictation.go)
	Remake      Remake    `json:"remake"`    // «Перезняти за прикладом»: who watches the example (remake.go)
	Telegram    Telegram  `json:"telegram"`
	UI          UI        `json:"ui"`
	Custom      CustomAPI `json:"custom"` // own image/video API (Image/Video.Provider "custom")
	// CodeWriters is how many scene specs of a code animation the text
	// model writes at once (0 = auto: 3, a local Ollama 1; at most
	// MaxCodeWriters) — studio.SpecConcurrency.
	CodeWriters int `json:"codeWriters,omitempty"`
	// OnboardingDone hides the first-run welcome once the user saw it.
	OnboardingDone bool `json:"onboardingDone"`
}

// Default returns sensible defaults for a fresh install.
func Default() Config {
	return Config{
		DataDir: defaultDataDir(),
		Text:    Text{Provider: "openai", BaseURL: "http://127.0.0.1:8317/v1", Model: "gemini-3.8-flash"},
		Image:   Image{Provider: "glabs"},
		Video:   Video{Provider: "glabs", GlabsModel: "veo_31_fast", Resolution: "720p", Length: 8},
		Glabs:   Glabs{BaseURL: "http://127.0.0.1:8765"},
		Google: Google{
			StoryModel: "gemini-3.8-flash",
			ImageModel: "gemini-3.1-flash-image",
			VeoModel:   GoogleOmniModel,
		},
		TTS:         TTS{Provider: "elevenlabs"},
		Concurrency: 2,
		Autopilot:   Autopilot{MakeCover: true, Retries: 1, ContinueOnError: true},
		STT:         STT{Provider: "elevenlabs"},
		Telegram:    Telegram{OnDone: true, OnError: true, OnFinal: true, Live: true},
		UI:          UI{Theme: "light", AutoUpdate: true},
	}
}

// Normalize clamps and fills fields so the rest of the app can trust them.
func (c *Config) Normalize() {
	d := Default()
	if strings.TrimSpace(c.DataDir) == "" {
		c.DataDir = d.DataDir
	}
	c.Text.Provider = strings.ToLower(strings.TrimSpace(c.Text.Provider))
	switch c.Text.Provider {
	case "anthropic", "ollama", "azure", "google":
	default:
		c.Text.Provider = "openai"
	}
	c.Text.BaseURL = strings.TrimRight(strings.TrimSpace(c.Text.BaseURL), "/")
	if c.Text.BaseURL == "" {
		c.Text.BaseURL = TextDefaultBaseURL(c.Text.Provider)
	}
	c.Text.APIKey = strings.TrimSpace(c.Text.APIKey)
	c.Text.Auth = strings.ToLower(strings.TrimSpace(c.Text.Auth))
	switch c.Text.Auth {
	case "", "bearer", "x-api-key", "api-key", "none":
	default:
		c.Text.Auth = ""
	}
	c.Text.APIVersion = strings.TrimSpace(c.Text.APIVersion)
	if strings.TrimSpace(c.Text.Model) == "" {
		c.Text.Model = d.Text.Model
	}
	switch c.Image.Provider {
	case "google", "custom":
	default:
		c.Image.Provider = "glabs"
	}
	switch c.Video.Provider {
	case "google", "custom":
	default:
		c.Video.Provider = "glabs"
	}
	if c.Video.GlabsModel == "" {
		c.Video.GlabsModel = d.Video.GlabsModel
	}
	if c.Video.Resolution == "" {
		c.Video.Resolution = d.Video.Resolution
	}
	// Default Omni, migrates the shut-down Veo models (google.go) — before the
	// lengths: Google Omni renders 10 s clips, Veo does not.
	c.Google.normalizeVideo(now())
	if c.Video.Provider == "custom" {
		// The own API's preset/model decides (e.g. Kling 5/10 s); the engine
		// snaps to it per request.
		c.Video.Length = CustomVideoLength(c.Video.Length)
	} else {
		c.Video.Length = SnapVideoLength(float64(c.Video.Length), c.videoLengthModel(), 8)
	}
	c.Custom.Normalize()
	if c.Autopilot.Retries < 0 {
		c.Autopilot.Retries = 0
	}
	if c.Autopilot.Retries > 3 {
		c.Autopilot.Retries = 3
	}
	switch c.Autopilot.OnFailed {
	case FailedAsk, FailedSkip, FailedStill, FailedStop:
	default:
		c.Autopilot.OnFailed = FailedAsk
	}
	c.Glabs.BaseURL = strings.TrimRight(strings.TrimSpace(c.Glabs.BaseURL), "/")
	if c.Glabs.BaseURL == "" {
		c.Glabs.BaseURL = d.Glabs.BaseURL
	}
	if c.Google.StoryModel == "" {
		c.Google.StoryModel = d.Google.StoryModel
	}
	if c.Google.ImageModel == "" {
		c.Google.ImageModel = d.Google.ImageModel
	}
	keys := c.Google.Keys[:0]
	seen := map[string]bool{}
	for _, k := range c.Google.Keys {
		k = strings.TrimSpace(k)
		if k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	c.Google.Keys = keys
	c.TTS.normalize() // ids of tts.Providers(); per-provider settings (tts.go)
	switch c.STT.Provider {
	case "openai", "local", "assemblyai", "custom": // local = whisper.cpp on this computer; custom = Custom.STTSpec
	default:
		c.STT.Provider = "elevenlabs"
	}
	c.STT.BaseURL = strings.TrimRight(strings.TrimSpace(c.STT.BaseURL), "/")
	c.STT.APIKey = strings.TrimSpace(c.STT.APIKey)
	c.STT.Model = strings.TrimSpace(c.STT.Model)
	c.STT.AssemblyKey = strings.TrimSpace(c.STT.AssemblyKey)
	c.Dictation.normalize()
	c.Remake.normalize()
	c.Telegram.normalize() // token, chat, owner, allowed users, own server (telegram.go)
	c.UI.UpdateSeen = strings.TrimSpace(c.UI.UpdateSeen)
	switch c.UI.Theme {
	case "dark", "system":
	default:
		c.UI.Theme = "light"
	}
	if c.Concurrency < 1 {
		c.Concurrency = 1
	}
	if c.Concurrency > MaxConcurrency {
		c.Concurrency = MaxConcurrency
	}
	c.CodeWriters = max(0, min(MaxCodeWriters, c.CodeWriters))
}

// MaxCodeWriters is the most code-animation specs written at once.
const MaxCodeWriters = 4

// MaxConcurrency is the hard end of «Сцен одночасно». The usable maximum
// depends on the video service (studio.MaxConcurrency: G-Labs 10,
// RoyalTechno 20 …); the stored value is kept when the service changes and
// the engine clamps it per job.
const MaxConcurrency = 20

// SnapVeoLength snaps any length to the nearest value Veo accepts (4, 6, 8).
func SnapVeoLength(sec float64, fallback int) int {
	if sec <= 0 {
		return fallback
	}
	best := fallback
	for _, v := range []int{4, 6, 8} {
		if abs(float64(v)-sec) < abs(float64(best)-sec) {
			best = v
		}
	}
	return best
}

// SnapVideoLength is SnapVeoLength that also allows 10 s for Omni Flash
// (glabsModel "omni_flash": G-Labs Omni Flash, also used for Google Gemini
// Omni — the models that render 4/6/8/10 s clips).
func SnapVideoLength(sec float64, glabsModel string, fallback int) int {
	if glabsModel == "omni_flash" && sec >= 9 {
		return 10
	}
	return SnapVeoLength(sec, fallback)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// Dir is the per-user settings directory (created on demand).
func Dir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		// Never "." — that is "/" for an app started from Finder.
		if home, herr := os.UserHomeDir(); herr == nil && home != "" {
			base = home
		} else {
			base = os.TempDir()
		}
	}
	return filepath.Join(base, "YT Cartell Studio")
}

// Path of the settings file.
func Path() string { return filepath.Join(Dir(), "config.json") }

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(Dir(), "data")
	}
	// macOS: ~/Movies — Documents is privacy-protected (a permission prompt)
	// and often synced to iCloud, which would upload every render.
	if runtime.GOOS == "darwin" {
		if movies := filepath.Join(home, "Movies"); isDirPath(movies) {
			return filepath.Join(movies, "YT Cartell Studio")
		}
	}
	docs := filepath.Join(home, "Documents")
	if st, err := os.Stat(docs); err == nil && st.IsDir() {
		return filepath.Join(docs, "YT Cartell Studio")
	}
	return filepath.Join(home, "YT Cartell Studio")
}

// Load reads the settings file (defaults when missing).
func Load() (Config, error) {
	c := Default()
	b, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		c.Normalize()
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		d := Default()
		d.Normalize()
		return d, err
	}
	c.Normalize()
	return c, nil
}

// Save writes the settings atomically.
func Save(c Config) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	// Windows refuses to replace a file another process holds open (an
	// antivirus scan, a sync client, an editor): retry briefly, then write
	// in place so a change is never lost.
	var rerr error
	for i := 0; i < 5; i++ {
		if rerr = os.Rename(tmp, Path()); rerr == nil {
			return nil
		}
		time.Sleep(time.Duration(40*(i+1)) * time.Millisecond)
	}
	if err := os.WriteFile(Path(), b, 0o600); err != nil {
		return fmt.Errorf("%v; %w", rerr, err)
	}
	_ = os.Remove(tmp)
	return nil
}

// Store is the shared, thread-safe settings holder.
type Store struct {
	mu      sync.RWMutex
	saveMu  sync.Mutex // serializes writes of config.json
	cfg     Config
	subs    []func(Config)
	persist bool
	disk    [32]byte // SHA-256 of config.json as last read or written (diskwatch.go)
}

// NewStore loads settings from disk; Update persists them back.
func NewStore() (*Store, error) {
	c, err := Load()
	s := &Store{cfg: c, persist: true}
	s.disk = diskSum()
	return s, err
}

// NewStoreWith wraps an in-memory config (tests, headless screenshots).
// Such a store NEVER writes to disk, so it can't clobber the user's real settings.
func NewStoreWith(c Config) *Store {
	c.Normalize()
	return &Store{cfg: c}
}

// Get returns a copy of the current settings.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.Google.Keys = append([]string(nil), s.cfg.Google.Keys...)
	c.TTS = s.cfg.TTS.clone()
	c.Telegram = s.cfg.Telegram.clone()
	return c
}

// Update mutates, normalizes, persists and notifies subscribers. Writes are
// serialised in order (a save never overwrites a newer one), and a file that
// another process changed meanwhile is read first, so fn applies on top of it
// (diskwatch.go) instead of an old snapshot overwriting it.
func (s *Store) Update(fn func(c *Config)) error {
	c, subs, err := s.apply(fn)
	for _, f := range subs {
		f(c)
	}
	return err
}

// apply is Update without the subscribers; a panic in fn leaves no lock held.
func (s *Store) apply(fn func(c *Config)) (Config, []func(Config), error) {
	if s.persist {
		s.saveMu.Lock()
		defer s.saveMu.Unlock()
		s.adoptDisk()
	}
	c, subs := s.change(fn)
	if !s.persist {
		return c, subs, nil
	}
	err := Save(c)
	s.disk = diskSum()
	if err != nil {
		log.Printf("config: saving %s failed: %v", Path(), err)
	}
	return c, subs, err
}

// change applies fn to a copy, normalises it and makes it current.
func (s *Store) change(fn func(c *Config)) (Config, []func(Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	c.Google.Keys = append([]string(nil), s.cfg.Google.Keys...)
	c.TTS = s.cfg.TTS.clone()
	c.Telegram = s.cfg.Telegram.clone()
	fn(&c)
	c.Normalize()
	s.cfg = c
	return c, append([]func(Config){}, s.subs...)
}

// Subscribe registers a callback run after every Update.
func (s *Store) Subscribe(fn func(Config)) {
	s.mu.Lock()
	s.subs = append(s.subs, fn)
	s.mu.Unlock()
}

// Mask returns a non-secret hint for a key, e.g. "cg-c2…69b8".
func Mask(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return ""
	}
	r := []rune(k)
	if len(r) <= 10 {
		return "•••"
	}
	return string(r[:5]) + "…" + string(r[len(r)-4:])
}

func isDirPath(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
