# YT Cartell Studio — Go desktop edition (architecture contract)

A native desktop app (ONE executable, own window, **no browser, no WebView**): story → plan →
storyboard → video + voice → montage → mp4, step by step or unattended («Автопілот»). UI language:
**Ukrainian**. Visual world: the "warm app" UI — design contract [`GUI.md`](GUI.md), kit guide at the
top of `internal/gui/theme.go`. Open source (MIT, `LICENSE`; third-party notices in
`THIRD_PARTY_NOTICES.md`): no sign-in, no accounts — the window opens straight on Home, and every
external service is the user's own (keys in their settings).

The Node/web studio in `../apps` is the behavioural reference for the ported core (planner,
production, montage timing, web import): when in doubt there, read the TS source and port it
faithfully. Everything added since (profiles, autopilot, catalogues, storyboards, timeline, covers,
translation, own voice-over + transcripts, local STT, start materials, own image/video APIs, dynamic
clip lengths, TTS key rotation, AI profiles, voice-line editing, queue, Telegram) exists
only here: this file and the package doc comments are its contract. The code is the truth — when
this file disagrees, fix this file.

## Hard rules

- **Never draw a fully opaque, full-window rectangle after other content.** Gio v0.10.3
  treats such a fill as "clear the screen": it drops every op recorded before it but keeps
  the opacity layers already collected, which then point at the wrong ops and crash the
  Direct3D renderer (`driver.Texture is nil`). Only `gui.Root` may paint the opaque ground
  (it is the first op of a frame); overlays that cover the window use alpha ≤ 254.
  Headless snapshots don't reproduce it — smoke-test
  the real exe after adding a full-window overlay.
- Go 1.27, module `ytcartell`, root = this `GO/` folder. Build with **`CGO_ENABLED=0`**
  (there is no C compiler on the dev machine; Gio + oto build without cgo on Windows).
- Deps: **Gio** `gioui.org v0.10.3` (core packages only — the kit is custom, no `widget/material`),
  `github.com/ebitengine/oto/v3` (audio), `golang.org/x/image` (webp, scaling, sfnt + vector for
  covers), `golang.org/x/text/unicode/norm`, `golang.org/x/sys/windows/registry` (system theme).
  `internal/deps` (build tag `tools`) pins modules so parallel work never edits `go.mod`.
  No other third-party deps unless unavoidable (ask in your report).
- Assets are embedded, never fetched at build/run time: Fira Sans (UI), 8 OFL faces (covers), 72 style
  previews (`cmd/stylegen`), Lucide icons in `internal/gui/icons_data.go` (ISC; regenerate:
  `go run ./icons/fetch_icons.go` in `internal/gui`). Optional parts download only on the user's button (whisper.cpp, FFmpeg — `toolsinst`).
- FFmpeg/ffprobe are external executables: config path → next to the exe (also `ffmpeg/`,
  `ffmpeg/bin/`, `bin/`) → the studio's own copy (`<config.ToolsDir()>/ffmpeg/bin`, installed by
  `toolsinst`) → PATH (scoop shims resolved) → common install folders (`media.Locate`).
- Never shell out through a shell string: `sysx.Command(ctx, bin, args...)` (CREATE_NO_WINDOW).
  Heavy or long-lived children (ffmpeg, ffprobe, player decoders, whisper-cli) start via
  `sysx.Start/Run/Output`: on Windows they join a kill-on-close Job Object and die with the app.
- All network calls take a `context.Context`; long jobs are cancellable. Errors are friendly
  Ukrainian; secrets (API keys, the bot token) never reach logs or messages (`config.Mask`,
  `notify.Redact`, the providers' redaction; `tts-keys.json` keeps key hashes and masks only).
- **No secrets in the tree.** The repository is public: no real keys, tokens, personal paths or ids in
  code, tests or docs; test fixtures use obviously fake values (`sk-test-…`, `demo-…`).
- The UI goroutine never blocks: network, ffmpeg, OS dialogs, file copies and settings writes go
  through `Ctx.Background`/`Ctx.Do`; results come back through `Host.Post`.
- Catalogue ids (styles, genres, cameras, tempos, tones, languages, transitions, effects, filters,
  subtitle presets, cover templates/fonts, starter profiles, material kinds, TTS providers, custom-API
  presets) are stored on disk — never rename or reuse them.
- UI copy is Ukrainian, plain words, «ти» (`../PRODUCT.md` principles). Code/comments English;
  prompts sent to the models English.
- Never touch the user's data (`Documents/YT Cartell Studio`, `%AppData%/YT Cartell Studio`,
  `tts-keys.json`, `whisper/`) or `GO/YT-Cartell-Studio.exe` (the user's build): tests,
  screenshots and tools use temp folders and in-memory settings; check compilation with
  `go build ./...` / `go vet ./...` (no output file).
- Keep every package compiling on its own: `CGO_ENABLED=0 go build ./...` and `go vet ./...` must
  pass; `go test ./...` must pass offline. Do NOT touch anything outside `GO/`.

## Layout

```
GO/
  main.go            log → config.NewStore → studio.NewEngine → appui.Run (appui.Fatal on start-up errors)
  ARCHITECTURE.md    this file · GUI.md = UI design contract · README.md = user guide (Ukrainian)
  LICENSE (MIT) · THIRD_PARTY_NOTICES.md · CONTRIBUTING.md
  build.bat/build.sh build YT-Cartell-Studio(.exe); rsrc_windows_amd64.syso = exe icon + version info
  release.ps1        Windows release → GitHub (-Repo, default gitkalenyuk/yt-cartell-studio) · build-mac.sh = the macOS .app zips (docs/BUILD-MAC.md)
  cmd/guishots/ cmd/guikit/   headless PNGs: every screen (whole window) · the UI kit gallery
  cmd/stylegen/      dev tool (not shipped): renders the 72 style previews through G-Labs (ledger, hard cap)
  internal/
    gen/       provider-neutral image/video request types (no deps)
    config/    settings (config.json, incl. own APIs), thread-safe Store, profiles store (profiles.json)
    sysx/      hidden-console processes, kill-on-close job, detached Launch, open/reveal, native file dialogs
    version/   Version (2.1.0) / Commit / BuildDate — set by the build scripts via -ldflags -X
    update/    GitHub releases: latest (ETag cache), download + SHA-256 check, in-place install (Windows exe, macOS .app)
    gemini/    Google AI Studio: key pool, text, images, Veo
    llm/       text generation (+ vision): OpenAI-compatible, Anthropic, Ollama, Azure OpenAI or Gemini; address forms, checks; JSON extraction
    httpdiag/  the exact failed request for «Перевірити» (method + URL without the key, status, reply, hint; a Trace in the context)
    glabs/     G-Labs Studio Webhook API (Nano Banana, Veo 3.1, Omni Flash)
    providers/ own image/video APIs: OpenAI Images, OpenAI-compatible, fal.ai, Replicate, Runway, Luma, MiniMax
      catalog/  preset catalogues (embedded JSON): video, image and text (LLM) models across services, with caps + sources
    tts/       TTS presets (ElevenLabs, Cartesia, OpenAI, Google, Azure, MiniMax, Fish, custom), key rotation, voices
      catalog/  voice-over catalogue (embedded JSON): providers × models × voices, own servers, services not spoken yet
    stt/       speech-to-text: ElevenLabs Scribe v2, Whisper-compatible, AssemblyAI, local whisper.cpp (+ installer); transcript files
    dictate/   voice typing: FFmpeg microphone capture + streaming/phrase recognisers, text placement at the caret
    reference/ «Перезняти за прикладом»: a YouTube example watched by Gemini → a structured breakdown (no download)
    toolsinst/ helper programs installed on the user's button (FFmpeg; generic pinned + SHA-256 downloader, unzip, swap)
    notify/    Telegram Bot API client: messages, keyboards, edits, updates (long poll), streamed uploads, albums, downloads
    tgbot/     the Telegram remote control: pairing, menus, new projects / remakes, live progress, video delivery
    cover/     pure-Go cover text renderer (12 templates, 8 fonts)
    media/     ffmpeg/ffprobe, montage renderer + timeline (voice moves/trims, limiter), catalogues, 3×3 grids, last frame
    nle/       editor timelines: FCPXML 1.10, FCP 7 XML (xmeml), OpenTimelineIO, MLT (Shotcut), CMX 3600 EDL — bytes only
    player/    in-window audio (oto; a whole file or a range) + video (ffmpeg rawvideo) playback
    assets/    embedded logo, app icon, 72 style previews
    studio/    the engine: projects, jobs, planner, production, materials, montage, autopilot, queue …
    gui/       "warm app" UI kit: theme, widgets, motion, overlays, image cache, headless snapshots
    appui/     shell: window, rail, routes, overlays, tour, headless Shell
      core/    shell ↔ screens contract (Ctx, Host, Route, Sheet, StepView …)
      home/ queue/ project/ pickers/ produce/ montage/ settings/ dialogs/    the screens
  screenshots/new/   current PNGs: shell/ (guishots), <screen package>/ (TestShots renders); only README's are tracked
```

```
# import graph — keep it acyclic and one-directional
gen, config, sysx, notify, cover, gui, assets, version, nle, httpdiag    no internal imports (gui: Gio only)
providers/catalog, tts/catalog    no internal imports (data + loaders; their tests import providers / tts)
tgbot → config, notify, studio, media, sysx, reference, tts, version    (appui and settings → tgbot)
update → version, sysx
gemini, glabs, providers → config, gen, httpdiag · llm → config, gemini, httpdiag · tts → config, providers (the universal adapter), httpdiag
stt → config, tts, providers, media, sysx, httpdiag · dictate → config, stt, sysx · media → config, sysx · player → media, sysx · toolsinst → config, sysx
reference → config, gemini
studio → config, gen, cover, media, nle, toolsinst, reference, codeanim + the services (gemini, glabs, providers, llm, tts, stt, notify)   (never UI, player)
appui/core → studio, gui, player, sysx, tts, config
screens → core (+ pickers), studio, gui + types/catalogues/presets of config, media, cover, tts, stt, providers, glabs, assets, sysx, providers/catalog, tts/catalog · home, settings → dictate
project → produce, montage (step views) · appui → core, update, version, tgbot, dictate (Shutdown) + the other screens · cmd/stylegen → studio, glabs, gen, config
settings → version (+ the shell's OpenUpdates/UpdateAvailable through an optional interface on core.Host)
```

## Package contracts (exported API)

### internal/gen
```go
type RefImage struct{ Data []byte; MIME, Name string }  // Name binds "@name" in the prompt (G-Labs)
type ImageRequest struct{ Prompt, UIModel, Aspect string; Refs []RefImage } // UIModel NARWHAL | GEM_PIX_2 | GEM_PIX
type VideoRequest struct{ Prompt string; Start *RefImage; Aspect string; Seconds int; Model, Resolution, Mode string; Refs []RefImage }
const ModeAuto, ModeComponents = "", "components"        // components: named Refs in priority order
type Progress func(pct int, phase string); func NoProgress(int, string)
```

### internal/config
```go
type Config struct{ DataDir, FFmpegPath, FFprobePath string; Text; Image; Video; Glabs; Google; TTS; Concurrency int; Autopilot; STT; Telegram; UI; Custom CustomAPI; CodeWriters int /* 0 auto, ≤ MaxCodeWriters 4 */; OnboardingDone bool }
func Default() Config; func Load() (Config, error); func Save(c Config) error; func (c *Config) Normalize()
func Dir() string; func Path() string; func ProfilesPath() string  // <UserConfigDir>/YT Cartell Studio/{config,profiles}.json
func ToolsDir() string  // programs the studio installs: %LOCALAPPDATA%\YT Cartell Studio on Windows, else Dir(); const MaxConcurrency = 20
func NewStore() (*Store, error); func NewStoreWith(c Config) *Store  // NewStoreWith: in memory, NEVER writes
func (s *Store) Get() Config; Update(fn func(c *Config)) error; Subscribe(fn func(Config)); Refresh() bool
// Update: saves serialised in order; a config.json another process changed is read first and fn applies on top
// of it (diskwatch.go — two studio windows no longer undo each other); Refresh adopts such a change (Settings polls it)
type Profile struct{...}; func StarterProfiles() []Profile; var ErrProfileNotFound error
func NewProfileStore() / NewProfileStoreAt(dir string) (*ProfileStore, error); func NewMemoryProfileStore() *ProfileStore
func ProfileStoreFor(cfg *Store) (*ProfileStore, error)            // memory store for an in-memory Store
func (s *ProfileStore) List() []Profile; Get(id) (Profile, bool); Save(p) (Profile, error); Delete(id) error; Path() string
func Mask(k string) string; func SnapVeoLength(sec float64, fallback int) int; func SnapVideoLength(sec float64, glabsModel string, fallback int) int
func CustomVideoLength(sec int) int                                // Video.Provider "custom": 0 → 8, else 1–60
type CustomAPI struct{ ImagePreset, ImageBaseURL, ImageKey, ImageModel, ImageSpec, VideoPreset, VideoBaseURL, VideoKey, VideoModel, VideoSpec, TTSSpec, STTSpec string } // *Spec: the universal adapter's JSON
// legacy keys of older config.json files ("auth" — a removed sign-in session, "unlocks") are ignored on load and dropped on the next save
// settings transfer (export.go, export_crypto.go): the .ytcs file of Settings → Дані
func Export(c Config, profiles []Profile, keyStates []byte, opt ExportOptions) ([]byte, error); func NewBundle(…) *Bundle; func WriteExportFile(path string, data []byte) error
func ReadExport(b []byte) / ReadExportFile(path) (*ExportFile, error); (f) Open(password) (*Bundle, error) // ErrNotExport ErrNeedPassword ErrWrongPassword ErrDamaged *NewerFormatError
type Bundle struct{ Header ExportHeader; Connections *Connections; Profiles []Profile; HasProfiles bool; Defaults *Defaults; KeyStates json.RawMessage; Hints Hints; Skipped, Warnings []string }
func ApplyImport(c *Config, b *Bundle, plan ImportPlan, profileID func(string) string) // inside Store.Update; ImportPlan{Connections, Profiles, Defaults, KeyStates SectionPlan{On, Mode replace|merge}; Hints HintPlan}
func MergeProfiles(cur, in []Profile) (out, ids, added, same); func ReplaceProfiles(in) (out, ids); func (s *ProfileStore) ReplaceAll(list []Profile) error
func (s *Store) SaveBackup(profiles []Profile, now time.Time) (Backup, error); (s) Backups() []Backup; func ApplyBackup(c *Config, b Backup) // config.json.bak-<date> + profiles.json.bak-<date>, newest 5; memory stores keep them in memory
```
Settings file: JSON `{"ytcs":1, "note", "header":{app, appVersion, schema, createdAt, os, arch, encrypted, kdf{pbkdf2-sha256,
iterations 600 000, salt 32 B}, cipher{aes-256-gcm, nonce 12 B}, check 16 B | sha256}, "data": base64 | "payload": {…}}`;
master = PBKDF2-HMAC-SHA256(NFC password), key / check = HKDF-SHA256 expansions, GCM with the canonical header JSON as
additional data (a wrong password and a damaged file are told apart; 100 k–10 M iterations accepted). Payload sections:
connections (text, image, video, glabs, google, tts incl. every provider and key, stt, dictation, remake, telegram, custom incl.
the universal specs), profiles, defaults (concurrency, autopilot, ui), keyStates (tts-keys.json), hints (data folder, FFmpeg /
ffprobe, whisper.cpp program + model, microphone — taken only when asked and present here). Never exported: update
bookkeeping. A top-level "unlocks" section of 2.0.6 files is known and ignored. Merge = what is set up here stays, key lists joined without repeats, services not set up here taken whole,
profiles added by name («… (імпорт)» on a conflict, identical ones skipped, Autopilot.DefaultProfileID follows its id). A newer
payload schema imports what is known (`Skipped` lists unknown sections/fields, a field of a changed shape is skipped alone); a
newer container (crypto) is refused.
Sections: Text {openai|anthropic|ollama|azure|google; Auth, APIVersion}; Image/Video {glabs|google|custom; GlabsModel veo_31_fast|veo_31_lite|
veo_31_quality|veo_31_lite_relaxed|omni_flash; Length 4/6/8, 10 on Omni Flash (custom 1–60, snapped to the
model by the engine); Dynamic = per-scene clip lengths; Ultra = G-Labs may render 4/6 s}; Custom {preset id,
key; base URL / model "" = the preset's}; TTS {elevenlabs|cartesia|openai|google|azure|minimax|fish|custom|lumean;
per provider (config/tts.go): `Providers map[id]TTSProvider{APIKey, Keys, BaseURL, Model, Voice}`, the flat
APIKey/Keys/BaseURL/Model/Voice = the ACTIVE provider's view (Normalize stores it back); `Switch(id)` (or assigning
Provider: fields edited with it go to the new one) shows id's own settings, empty if never set; `SetKeys`, `Of(id)`;
a legacy flat section is migrated losslessly — keys/voice/model whose shape surely belongs to another preset
(`TTSKeyOwner`, `TTSVoiceOwner`: ElevenLabs sk_+hex / 20-char ids, Cartesia sk_car_ / UUIDs …) move there;
`ForeignTTSVoice`; APIKey + Keys rotate; `ByID` = «Мої голоси за ID» per provider (`VoiceRef{ID, Name, Language, Gender, Library}`,
`VoicesByID`, `SaveVoiceByID`, `ForgetVoiceByID`, ≤ 50 each)}; STT {elevenlabs|openai|assemblyai|custom|local; empty key/URL reuse the stored
ElevenLabs voice-over key / Text; AssemblyKey + AssemblyEU are AssemblyAI's own; LocalBin/LocalModel "" = auto};
Dictation (dictation.go) {Provider ""=auto|elevenlabs|openai|deepgram|assemblyai|openai-chunks|gemini|local, Language
""=detect, Device ""=default, SilenceSec 0=8 s|-1=never|3–60, per-service own keys/URL/models — "" reuses the app's}; Telegram {Enabled, BotToken, ChatID, OnDone, OnError, OnFinal; the remote control (telegram.go): Remote, OwnerID, OwnerName, Allowed []TelegramUser{ID, Name}, Live (default on), APIServer ("" = api.telegram.org); Paired(), RemoteOn(), Controls(id)}; UI {Theme light|dark|system,
ReducedMotion, TourDone, TipsHidden, AutoUpdate (default on), UpdateChecked (unix s of the last successful
check), UpdateSeen (the newer version it found)}; Autopilot {DefaultProfileID, MakeCover, Retries 0–3, OnFailed ""=ask|skip|still|stop — what the autopilot's montage does
with scenes that failed, only as the user chose it (`ContinueOnError` is legacy, on by default, never read)};
Remake {Analysis ""=auto|google|text — who watches a YouTube example; Model "" = Google.StoryModel};
Concurrency 1–MaxConcurrency (20; the engine clamps it per video service, a stored value is kept); `OnboardingDone` is
legacy. Profile = step-1 creative settings + montage look (+ `NarratorStyle`, a studio.NarratorStyles id); 4 starters with stable ids
(`starter-youtube|shorts|kids|explainer`); a damaged profiles.json is kept as
`profiles.broken-<unix>.json`. Starters shipped later (`laterStarters`, none now) reach an existing profiles.json ONCE (`offerStarters`:
appended unless its id or a profile of the same name exists; `profiles-starters.json` remembers what was offered, so a deleted one
never returns). Profiles in a user's profiles.json are never rewritten by start-up code: whatever ids they have, they are the user's.

### internal/sysx
```go
func Command(ctx context.Context, name string, args ...string) *exec.Cmd // hidden console on Windows
func Start(c *exec.Cmd) error; func Run(c *exec.Cmd) error; func Output(c *exec.Cmd) ([]byte, error) // + kill-on-close job
func OpenPath(path string) error    // file, folder or http(s) URL with the default app
func RevealPath(path string) error  // select in Explorer/Finder/file manager
func PickFolder(title, start string) (string, error); func PickFile(title string, exts []string) (string, error)
func PickSaveFile(title, defaultName string) (string, error)        // all: "" + nil when cancelled
func ExeDir() string
func Launch(name string, args ...string) error  // outlives the app: no job (breakaway on Windows), own session on Unix
```
Dialogs: PowerShell + WinForms (`-NoProfile -STA`, UTF-8) on Windows, `osascript` on macOS, `zenity`
then `kdialog` on Linux. The quoting builders (script.go) are unit-tested on every OS.

### internal/version, internal/update  (auto-update from GitHub releases)
```go
var version.Version = "2.0.1"; Commit, BuildDate string  // -ldflags -X ytcartell/internal/version.Version=…
func version.String() string; func version.Built() time.Time
var update.Owner, update.Repo = "gitkalenyuk", "yt-cartell-studio"  // -ldflags -X only
type Updater interface{ Latest(ctx) (Release, error); Download(ctx, Release, progress func(done, total int64)) (string, error); Apply(staged string) (Restart, error) }
type Restart func(from string) error                  // starts the installed build with --updated-from=<from>
func New(cacheDir string) *Client                     // *Client fields: HTTP, API, Owner, Repo, UserAgent, CacheFile, Dir (tests: httptest)
type Release struct{ Tag, Version, Title, Notes, URL string; Published time.Time; Draft, Prerelease bool; Assets []Asset } // Newer, AssetFor, Checksums, PageURL
func Apply(staged string) (Restart, error); func Installable() error; func Cleanup(dir, current string); func UpdatedFrom(args []string) (string, bool)
func ParseVersion(s string) (Version, bool); func Newer(latest, current string) bool; func ParseNotes(md string) []Block; func ParseSums([]byte) map[string]string
type Error struct{ Kind Kind; Msg string; Reset time.Time; Err error }   // KindOffline, KindRateLimited, KindNoRelease, KindChecksum, KindManual, KindPermission …
type Fake struct{…}                                    // Updater for headless renders and UI tests (never the network)
```
Latest: `GET api.github.com/repos/{Owner}/{Repo}/releases/latest` (User-Agent, 15 s, `If-None-Match` from
`<settings dir>/update-cache.json` — a 304 does not count against the 60/h anonymous limit; 404 = nothing
published; 403/429 = rate limited, with the reset time). Assets `YT-Cartell-Studio-<ver>-windows-x64.exe`,
`-macos-arm64.zip` / `-macos-x64.zip` (`-macos-universal.zip` as fallback) and `SHA256SUMS.txt`; a build without
its checksum line is refused. Download: `<temp>/yt-cartell-studio-update/<asset>.<sha12>.part`, resumed with
`Range`, 60 s stall timeout, verified, then renamed. Apply — Windows: running exe → `<exe>.old`, the new one
moved in (copy fallback across volumes, rollback on failure); macOS: zip unpacked next to the running `.app`
(modes kept; `..`, absolute paths and escaping links refused), bundles swapped (`<name>.old.app`), quarantine
cleared, relaunch with `open -n … --args --updated-from=…`; elsewhere or outside a bundle → `KindManual` (the
release page). Cleanup (start-up, own goroutine) removes `*.old`, `.old.app` and stale downloads, retrying while
the old process exits.

### internal/gemini  (port: api_balancer.ts, gemini.ts, imagegen.ts viaGoogle, veo.ts)
```go
func NewPool(keys []string) *Pool; (p) SetKeys, Size, Healthy; (p) Do(ctx, fn func(key string) error) error // quota → park for its window, next key
func (p *Pool) Status() []KeyStatus; (c *Client) KeyStatus() []KeyStatus; (c) CheckKeys(ctx) (bool, string)  // keystatus.go
type KeyStatus struct{ Index int; Mask, State string; Until time.Time; LastError string; Requests int; Checked time.Time } // KeyOK|KeyUnknown|KeyResting|KeyError
func New(get func() config.Config) *Client; (c) Pool() *Pool; (c) Test(ctx) error
func (c *Client) GenerateText(ctx, model, system, prompt string, temperature float64, jsonMode bool, schema any) (string, error)
func (c *Client) Image(ctx, r gen.ImageRequest) ([]byte, string, error)
func (c *Client) Video(ctx, r gen.VideoRequest, progress gen.Progress) ([]byte, error)
func ImageModelFor(uiModel, fallback string) string; func AcceptsTemperature(model string) bool
const MaxOmniRefs = 10; var OmniLengths = []int{4, 6, 8, 10}
```
Key states (keystatus.go): CheckKeys = GET models?pageSize=1 per key (3 at a time, 120 ms apart); a key Google
rejects (API_KEY_INVALID/EXPIRED, 401/403) is skipped for 1 h while others work (tried last when none does).
Pool: limits are per PROJECT; a quota error parks the key only for its window — `RetryInfo.retryDelay`,
a per-minute `QuotaFailure` 1 min, a per-day one until midnight Pacific, 402 (prepay) 1 h, no hint 1 min
doubling per hit (≤ the daily reset). Gemini 3+ gets no temperature (`AcceptsTemperature`). Images: thought
parts skipped, ≤ 14 refs; named refs → legend "Reference images, in order: image 1 = @tag, …".
Video (`Google.VeoModel`): **Gemini Omni Flash** (default `gemini-omni-1.1-flash`, omni.go) — `POST
/interactions` {model, input (prompt | image blocks + text), response_format {type video, aspect_ratio,
resolution, delivery "uri", duration "Ns"}}, synchronous; the clip is the `model_output` step's video block
(base64 `data` or a Files API `uri` polled until ACTIVE, then downloaded); start frame = `<FIRST_FRAME>`,
ModeComponents = ≤ 10 refs bound as `<IMAGE_REF_N>` (@tags rewritten); 4/6/8/10 s; still-running
interactions are polled. **Veo 3.1 previews** until their 2026-10-22 shutdown (then routed to Omni):
predictLongRunning, 16:9/9:16, 4/6/8 s, the whole operation pinned to one key, budget 10 min; operation
errors are `google.rpc.Status` (code 8 = quota); ModeComponents → `referenceImages` (asset, ≤ 3, 8 s).

### internal/llm  (port: services/llm.ts)
```go
type Request struct{ System, Prompt string; Temperature float64; Schema any; Timeout time.Duration; Images []Image }
type Image struct{ MIME string; Data []byte }                       // vision: png | jpeg | webp
func New(get func() config.Config, g *gemini.Client) *Client
func (c *Client) Text(ctx, r Request) (string, error); JSON(ctx, r Request, out any) error; Models(ctx) ([]string, error)
func ExtractJSON(raw string) ([]byte, error) // fences / prose tolerated
// remote.go / resolve.go: the address-and-key endpoints
type Conn struct{ Protocol, BaseURL, APIKey, Model, Auth, APIVersion string }; func ConnOf(t config.Text) Conn
const ProtoOpenAI, ProtoAnthropic, ProtoOllama, ProtoAzure; AnthropicVersion = "2023-06-01"; AzureAPIVersion = "2024-10-21"
func NormalizeBase(protocol, raw string) (base, apiVersion string); func Candidates(protocol, base string) []string
func (c *Client) Check(ctx, conn Conn, chat bool) (Probe, error) // *CheckError{Err, Tried}; Probe{Conn, Models, Reply, Latency, Fixed, AuthSet, Tried}
func (c *Client) ModelsOf(ctx, conn Conn) ([]string, error); (c) OnResolved(fn func(from, to string))
type HTTPError struct{ Msg, Method, URL string; Status int; Body []byte; Hint, Err string } // Error(): Msg + httpdiag.Detail
```
Config.Text.Provider: openai | anthropic | ollama | azure | google; Text.Auth "" (the protocol's own: Bearer, Anthropic
x-api-key, Azure api-key) | bearer | x-api-key | api-key | none; Text.APIVersion for Azure deployment URLs.
Wire formats (checked 2026-10-05): OpenAI-compatible `POST {base}/chat/completions`; Anthropic `POST {base}/messages`
{model, max_tokens 16000 (4096 after a 400 naming it), system, messages, temperature ≤ 1}, `content[]` text blocks,
stop_reason refusal / max_tokens → errors, `GET /models?limit=1000` paged by has_more/after_id; Ollama `POST /api/chat`
{stream:false, format:"json", options.temperature, images}, `GET /api/tags`; Azure v1 `…/openai/v1/chat/completions`
(model = deployment) or `…/openai/deployments/{dep}/chat/completions?api-version=…`. Model lists: {data:[{id}]},
{models:[{name}]}, a bare array (Together). Addresses: a pasted request URL loses its endpoint (/chat/completions,
/models, /messages, /api/chat …), a scheme is added; on 404/405 that is not about the model the client tries the
other forms (as is, +/v1, /v1, /api/v1, /openai/v1) by their model list, remembers the working one for the session
and reports it (OnResolved → the engine stores it in Text.BaseURL). Check (Settings «Перевірити», the assistant):
the model list on each form (no tokens), 401/403 on auto auth → the other headers, then a one-word chat; the error
lists every request tried. Every error names the request (method + URL without the key), status, reply, hint.
OpenAI path: ONE user message "### INSTRUCTIONS … ### INPUT …" (gateways like CarteLink may replace
system prompts), retry 3× on rate-limit 429/5xx/timeouts honouring `Retry-After` (billing 429s —
insufficient_quota, credit_balance_exhausted, *_spend/usage_limit_exceeded — never retried); `temperature`
only for models that take it (not o-series / GPT-5+ / Gemini 3+; a 400 naming it → resent without);
`message.refusal`, finish_reason `content_filter` / `length` become plain errors; Images ride in it as `image_url` data-URL parts.
Google path: `cfg.Google.StoryModel`, Images as inlineData. A model without vision fails the request.

### internal/httpdiag  (no internal imports)
```go
func Detail(method, url string, status int, body []byte, hint string, secrets ...string) string // "Запит: POST … → 404 Not Found\nВідповідь: …\nПідказка: …"
func Line(method, url string, status int, secrets ...string) string; URL(raw, secrets...) string; Body(b, n, secrets...) string; Redact; Mask; Hint(status, body) string
func WithTrace(ctx) (context.Context, *Trace); func Record(ctx, method, url string, status int, body []byte, err error) // no-op without a Trace
func (t *Trace) Detail(secrets ...string) string; (t) Append(msg string, secrets ...string) string; Calls(); Last()
```
llm, tts, stt, providers, glabs and gemini record their calls; the «Перевірити» of every card (TestText, TestTTS,
TestSTT, TestCustomImage/Video, TestGlabs, TestGoogle) appends the last failed request to its message. Keys never
appear: credential query parameters and known key shapes are masked.

### internal/glabs  (port: services/glabs.ts; https://duckmartians.info/g-labs/guide/en/webhook-api/)
```go
func New(get func() config.Config) *Client
func (c *Client) Image(ctx, r gen.ImageRequest) ([]byte, string, error)
func (c *Client) Video(ctx, r gen.VideoRequest, progress gen.Progress) ([]byte, error)
func (c *Client) Health(ctx) (ok bool, msg string)       // GET /api/health (no key), then /api/tasks (key)
func ImageModel(uiModel string) string; func MaxVideoRefs(model string) int; const OmniFlash = "omni_flash"
type Error struct{ Msg string; Code int; Status, Reject string; Retry bool }
func Rejected(msg string) bool; func Rejection(err error) string  // RejectInvalid | RejectSafety | ""
```
POST /api/image|video/generate → task_id, poll /api/status/{id}, X-API-Key. Video modes: components
(named `{data,name}` refs; Omni Flash ≤ 7, Veo ≤ 3) → start_image → text_to_video; length snapped
(Veo 4/6/8, Omni Flash 4/6/8/10). Failed tasks are classified by error_code + how `error` starts:
400 / INVALID_ARGUMENT (`flag=[3]` = gRPC code 3) → `RejectInvalid`, a Google safety category (REPUTATIONAL,
PROMINENT_PERSON, DANGEROUS …) → `RejectSafety` — both final, message «G-Labs відхилив запит: змінити промпт
або референси» (+ «фільтр безпеки Google»); 429, 5xx, UPLOAD_ERROR, timeouts → `Retry`. A task given up on
(cancel, timeout) gets POST /api/stop/{id} (never without the id). /api/files/ result URLs are fetched from the
configured address (G-Labs builds them from its bind address). 403 → «Webhook потребує тарифу MAX у G-Labs»,
413 → body over 50 MB.

### internal/providers  (own image/video APIs: config.CustomAPI, Image/Video.Provider "custom")
```go
func New(get func() config.Config) *Client; (c) TestImage(ctx), TestVideo(ctx) (bool, string) // stateless; tests bill nothing
func (c *Client) Image(ctx, r gen.ImageRequest) ([]byte, string, error)
func (c *Client) Video(ctx, r gen.VideoRequest, progress gen.Progress) ([]byte, error) // Seconds/Aspect snapped to the model
const PresetOpenAI, PresetOpenAICompat, PresetFal, PresetReplicate, PresetRunway, PresetLuma, PresetMiniMax, PresetRoyal, PresetUniversal // stored ids
func ImagePresets() []Preset; VideoPresets() []Preset; FindImagePreset(id) / FindVideoPreset(id) (Preset, bool)
type Preset struct{ ID, Label, DefaultBaseURL string; Models []string; DefaultModel, KeyHint string; KeyOptional bool; DocsURL string; Caps Caps }
func VideoCapsFor(preset, model string) VideoCaps; func ImageCapsFor(preset, model string) ImageCaps // built in, no network
func SpecCaps(spec string) Caps                                   // the universal preset: caps from the user's spec
type VideoCaps struct{ Lengths []int; MaxRefs int; StartFrame, Components bool; Aspects []string } // (c) Snap, Cover, Aspect
type ImageCaps struct{ MaxRefs int; Aspects []string }; type Error struct{ Msg string; Status int; Retry bool }
// universal (universal*.go): any API described in JSON
type Spec struct{…}; func ParseSpec(text string) (*Spec, error) /* *SpecError{Problems} */; func CheckSpec(text string, video bool) (summary string, warnings []string, err error)
func SpecTemplates(video bool) []SpecTemplate; FindSpecTemplate(video, id); func SpecPlaceholders() []string; func FormatSpec(raw []byte) string
func FetchDocs(ctx, url string, progress func(string)) (DocsText, error); func PrepareDocs(text string) DocsText; func SpecPrompt(video bool) string
// universal_audio*.go: the same spec for audio APIs
type SpecKind string; const KindImage, KindVideo, KindTTS, KindSTT
func ParseSpecFor(text string, kind SpecKind) (*Spec, error); CheckSpecFor(text, kind); SpecTemplatesFor(kind); FindSpecTemplateFor(kind, id); SpecPlaceholdersFor(kind); SpecPromptFor(kind)
type AudioAPI struct{ Kind SpecKind; BaseURL, Key, Model, Spec string; HTTP *http.Client }
func (a AudioAPI) Speech(ctx, SpeechRequest{Text, Voice, Language string; Speed float64; Format string}) ([]byte, string, error)
func (a AudioAPI) Transcribe(ctx, TranscribeRequest{Audio []byte; Name, MIME, Language string}) ([]byte, error) // the reply for stt.ParseTranscript
func (a AudioAPI) Test(ctx) (bool, string)
```
Audio placeholders: TTS `{{text}} {{voice}} {{model}} {{language}} {{language_name}} {{speed}} {{format}}` (result = the
reply audio, a URL, a data: URI or base64; sync or async); STT `{{audio}}` (a data: URI → a file part named
audio-N.<ext> with "encoding": "multipart") `{{audio_b64}} {{audio_name}} {{audio_mime}} {{language}} {{model}}` (result =
the reply itself — JSON, SRT, VTT, text — or the value at "result": objects as JSON, URLs downloaded). Templates: TTS
OpenAI-compatible /audio/speech, ElevenLabs-compatible, Lumean (orders), sync JSON, async jobs; STT Whisper
/audio/transcriptions (verbose_json + word timings), ElevenLabs Scribe, sync, async.
Images: openai (GPT Image) · openai-compat (any `/v1` images server, key optional) · fal · replicate · royaltechno ·
universal; video: fal · replicate · runway · luma (Ray) · minimax (Hailuo/H3) · royaltechno · universal. fal.ai/Replicate
map the request onto the model's live OpenAPI input schema (cached; built-in per-family schemas in models.go are the
fallback, the caps and the reference limit when a live schema has none). Polls every 5 s (image ≤ 8 min, clip ≤ 25 min;
RoyalTechno 10 s for video, ≤ 30 / 60 min), 429/5xx retried 3× with backoff, cancellable; Ukrainian errors, key
redacted. `Snap` = the nearest length (ties → the longer), `Cover` = the shortest one ≥ a need.
**universal** (the `universal.go` header is the spec reference): `config.CustomAPI.ImageSpec/VideoSpec` describe the API —
`submit` (path, headers, JSON/multipart/form body template with `{{prompt}} {{model}} {{aspect}} {{orientation}}
{{seconds}} {{width}} {{height}} {{size}} {{start_image}} {{start_image_b64}} {{refs}} {{refs_b64}} {{resolution}}`,
`|text`), `auth` (bearer · basic · header:Name[:Prefix] · query:name · none), synchronous or async (`id`, `poll` with
`{id}` / `{{submit.…}}` / `{{status.…}}`, `state` + done/failed/queued, `progress`, optional `fetch`), `result` JSON
paths (URL · data: URI · base64 · array; none = the reply bytes), `error`, `cancel`, `test`, `caps`. The key goes only
to the base URL's origin; one Idempotency-Key per job; submits repeat only after 429/503 or a connection lost before
an answer (never after a timeout); a job the client gives up on is cancelled. Templates (RoyalTechno, Replicate,
fal.ai queue, OpenAI Images, sync URL, async jobs) round-trip in universal_test.go. The base URL may come from an
absolute submit path; a spec without `test` is checked offline («опис коректний»).
**catalog** (`providers/catalog`, «Вибрати з каталогу» in Settings): `video/*.json`, `image/*.json`, `text/*.json` — one file per
service/group, `{"list","checked","entries":[…]}`, every entry with its `source` URL (format in catalog.go).
```go
func Video() []VideoEntry; Image() []ImageEntry; Text() []TextEntry   // popular first (0–3), then file order; read-only
func FindVideo(preset, model string) (VideoEntry, bool); FindImage(…); Embedded() *Data; Load(fs.FS) *Data; Check(*Data) []string
type Entry struct{ ID, Label, Vendor, Family, Host, HostLabel, Preset, BaseURL, Model string; Spec json.RawMessage; Modes []string; Price, Notes, Source, Checked string; Popular int }
type VideoEntry struct{ Entry; Lengths []int; Aspects, Resolutions []string; MaxRefs int; Audio string /* speech|sound|none */; PromptMax int }
type ImageEntry struct{ Entry; MaxRefs int; Aspects, Sizes []string; PromptMax int; Text bool }
type TextEntry struct{ ID, Label, Vendor, Protocol, BaseURL string; Models []string; DefaultModel, KeyHint string; KeyOptional, Local, Vision, JSON bool; Docs, Source, Notes string; Popular int }
```
Modes t2v i2v ref first-last v2v / t2i edit; preset = the studio preset (fal, replicate, runway, luma, minimax, royaltechno,
openai, openai-compat) or `universal` with a `spec` (APIs without a preset: Gemini API, Kling, BytePlus, Alibaba, Vidu, LTX,
xAI, BFL, Ideogram, Recraft, Stability …). Picking an entry sets preset, base URL ("" when it is the preset's own), model and
the spec (asks before replacing the user's own description), never the key; the caps line adds what the catalogue knows
(`catalogCapsLine`). Tests: every entry checked, every spec parsed and its caps equal to the entry's.

### internal/tts
```go
func New(get func() config.Config) *Client
func (c *Client) Synthesize(ctx, text, voiceID string) ([]byte, error)              // mp3
func (c *Client) SynthesizeWith(ctx, text, voiceID string, opt SynthOptions) ([]byte, error)
type SynthOptions struct{ Model, LanguageCode string; Stability, Similarity, Speed float64 }
func (c *Client) ModelFor(ctx, language string) (modelID, languageCode string)
func (c *Client) Voices(ctx, force bool) (VoiceList, error)                           // cached 10 min per provider+key
func (c *Client) Test(ctx) (bool, string)       // every key (3 at a time, 120 ms apart): a cheap authenticated read, never a synthesis
func (c *Client) KeyStatus() []KeyStatus; ResetKeyErrors(); SetStateFile(path string) error; FlushKeyState() error
func (c *Client) KeyStateJSON() ([]byte, error); ImportKeyState(data []byte, replace bool) (int, error) // settings transfer (keyio.go)
type KeyStatus struct{ Mask, State string; Until time.Time; LastError string; LastUsed time.Time; UsedChars, Limit, Index int; Shared bool; Checked time.Time; Plan, Info string }
type Voice struct{ ID, Name, Category, Gender, Age, Accent, PreviewURL, Language string; Usable bool; Locked, Note string } // Category "library" = Voice Library
func (c *Client) ForeignVoice(id string) string; func ForeignVoiceOf(t config.TTS, id string) string // a voice id of another provider
func ParseKeys(text, provider string, gateway bool, existing []string) KeyPaste // bulk paste: labels/junk skipped, other providers' keys refused; (KeyPaste) Summary / SummaryFor(label)
func ParseGoogleKeys(text string, existing []string) KeyPaste; ParseKeyList(text string, r PasteRule, existing []string) KeyPaste // Google AI Studio: AIza + 35
func (c *Client) LookupVoice(ctx, id string) (Voice, error) // voicebyid.go: Cartesia GET /voices/{id}; ElevenLabs GET /v1/voices/{id} → /v1/shared-voices; ErrVoiceNotFound
func CanLookupVoice(provider string) bool; VoiceIDProblem(provider, id string) string; CleanVoiceID(id string) string
const KeyOK, KeyExhausted, KeyError, KeyResting; const KeyStateFile = "tts-keys.json"; func DefaultStateFile() string
func Providers() []ProviderInfo; func ProviderByID(id string) (ProviderInfo, bool)  // Settings presets (+ Plans line, ModelNote)
func NormalizeLanguage(code string) string
```
Providers (one file each): elevenlabs, cartesia (Sonic 3.6, Cartesia-Version 2026-08-14), openai
(-compatible), google (Cloud TTS, Chirp 3 HD personas follow the film locale; no TTS key → the Google
keys), azure (SSML; URL field = region), minimax (hex audio, language_boost), fish (S2.1 Pro), lumean, custom.
**lumean** (lumean.go; https://api.lumean.app/docs/llm.md, read 2026-10-05): `X-API-KEY`, base
`https://api.lumean.app/api/public`; asynchronous only — a template per key and model (`POST /templates`
{service_key elevenlabs, tts_settings{mode_v1, model_id, voice_id, voice_settings}, output_settings mp3}, an existing
«YT Cartell Studio · <model>» one reused), `POST /orders` {template_id, input_text, config_override.tts_settings{voice_id,
language_code, voice_settings}}, `GET /orders/{id}` until completed/result_delivered, `POST /storage/url` {path:
result.files[0]} → a signed link fetched without the key; a language outside the 29 of multilingual v2 → eleven_v3
(v3 stability 0/0.5/1; speed 0.7–1.2); voices = ElevenLabs Voice Library through `GET /voices/elevenlabs/library`
(Ukrainian first, page from 0); check = `GET /subscriptions` (plan, tokens left) or `GET /user`; errors by
reason/error_code (402 balance, 403 permissions/no_subscription, 422 content_blocked, 429 rate/quota/ip_banned). Its
ElevenLabs-shaped voice ids are its own (config.ambiguousVoice). Lumean's API makes no images or video (the docs: those
order types cannot be created with an API key) — so no «Своє API» preset.
**custom** with `config.CustomAPI.TTSSpec` (universal.go) = the universal adapter (providers.AudioAPI, keys rotate
through the pool); without a spec the plain own-server protocol stays.
All return MP3; "auto" language → the writing system decides where a provider needs one.
Key pool (keys.go, the user's Cartelsia rotation): APIKey + Keys round-robin per request, ≤
ProviderInfo.Concurrency requests per key; quota/credits spent → "exhausted" until 00:00 on the 1st
of next month; rejected key → "error" for 1 h (or until the key list changes / ResetKeyErrors);
429 → "resting" a few s (Retry-After); 5xx/network → next key; a voice of another account → next key.
States: in memory unless SetStateFile (tts-keys.json next to config.json; key id = sha-256, never the key).
ElevenLabs model: Settings wins, else per language from GET /v1/models (1 h cache, documented tables as
fallback): multilingual_v2 → v3 → v4 → flash v2.5 (a pinned turbo v2.5, deprecated, is sent as flash v2.5).
Plans: the ElevenLabs plan (subscription tier / status "free") is remembered per key; on a free plan with no
paid key, Voice Library voices (not premade, `is_owner` false) are `Usable: false, Locked: "потрібен платний
тариф ElevenLabs"`; 402 paid_plan_required → a friendly message, next key; premade voices carry Note "доступний
до 31 грудня 2026" (ElevenLabs retires them). A voice id of another provider is refused before any request.
A rejected key whose shape is another provider's says so ("схоже, це ключ ElevenLabs …"). Provider docs were
checked 2026-10-02 (see each file's header).
**catalog** (`tts/catalog`, «Вибрати з каталогу» in the Озвучка card): `data/*.json` — `Entry{ID, Kind model|voice|server, Label,
Vendor, Provider (a studio provider id, "" = not spoken yet), Protocol, BaseURL, Model, Voice, VoiceName, Gender, Ukrainian,
Languages, LanguagesNote, MaxChars, Concurrency, SelfHosted, KeyOptional, Spec, Price, Notes, Source, Checked, Popular}`;
`All()`, `Embedded()`, `Load(fs.FS)`, `Check(d, knownProvider)`, `(Entry) Supported(providerOK)`. A pick switches to the
provider (its own keys come back) and sets model / voice / server address; a service with a `spec` (provider custom,
protocol rest) becomes «Власний сервер» through the universal adapter (`Custom.TTSSpec`, asks before replacing the user's);
services without a provider are listed with their docs only.

### internal/stt
```go
func New(get func() config.Config) *Client
func (c *Client) Transcribe(ctx, path, lang string) (Transcript, error) // word timings; lang "" = detect
func (c *Client) TranscribeProgress(ctx, path, lang string, progress func(pct int, msg string)) (Transcript, error) // local reports
func (c *Client) Test(ctx) (bool, string); (c) Model() (provider, model string); func DefaultModel(provider string) string
type Transcript struct{ Text, Language string; Duration float64; Words []Word; Segments []Segment }
func ParseTranscript(data []byte, name string) (Transcript, error); func TranscriptExts() []string; var ErrNoTimecodes error; func Unspaced(r rune) bool
const ProviderLocal = "local"; const DefaultLocalModel = "small"; func LocalModels() []ModelInfo // tiny … large-v3-turbo
func LocalDir() string /* <settings dir>/whisper */; func LocalStatus(cfg config.Config) (bin, model string, ok bool, hint string)
func InstallLocal(ctx, dir, model string, progress func(int, string)) (bin, modelPath string, err error); InstallLocalModel(…) (string, error)
```
Scribe `scribe_v2` (a configured v1 is upgraded) or Whisper-compatible `/audio/transcriptions` (≤ 25 MB:
the studio uploads a 16 kHz mono MP3; models without verbose_json word timings — gpt-4o(-mini)-transcribe,
gpt-transcribe — are refused with a hint; whisper-1 shuts down 2027-02-26); empty key/URL reuse the stored
ElevenLabs voice-over key or the Text credentials. **custom** (`custom.go`): `config.CustomAPI.STTSpec` through providers.AudioAPI (KindSTT), STT.BaseURL/APIKey/Model;
the reply goes through ParseTranscript (any known JSON, SRT, VTT). The "openai" provider reuses the Text address/key
only when Text is OpenAI-compatible (`config.Text.OpenAICompatible`).
**assemblyai** (`assemblyai.go`): `POST /v2/upload` (raw
bytes) → `POST /v2/transcript` (`language_code`, Ukrainian → `speech_models:["universal-2"]`, else
`language_detection`) → poll `GET /v2/transcript/{id}` every 3 s; words in ms; `authorization: <key>`;
`AssemblyEU` → api.eu.assemblyai.com.
**local** (`local*.go`): whisper.cpp `whisper-cli` ≥ 1.7.4 from STT.LocalBin → next to the exe →
`LocalDir()/bin` → PATH → Homebrew/MacPorts; model STT.LocalModel or the best `LocalDir()/models/ggml-*.bin`;
a run = FFmpeg 16 kHz mono WAV → `whisper-cli -l <lang|auto> -oj -ml 1 -sow -pp [-sns]` in a temp folder
(ASCII-safe paths for Cyrillic user folders), one run at a time, success = the JSON file. Installer
(user-started, official sources only): Windows — the CPU zip of the newest whisper.cpp `b<build>` GitHub
release (a pinned build + SHA-256 as fallback); macOS/Linux have no official CLI build — Homebrew's
`whisper-cpp`, else an error saying `brew install whisper-cpp`, and only the model; models from
huggingface.co/ggerganov/whisper.cpp (size + SHA-256 checked, `.part` files resume with HTTP Range).
`ParseTranscript` (transcript.go) sniffs SRT, WebVTT, the JSON of ElevenLabs, OpenAI/Whisper (+ faster-whisper,
WhisperX), Deepgram, AssemblyAI, whisper.cpp and a dozen more, and timed TXT ([00:12], SBV, LRC, Audacity
labels …); UTF-8/16 or Windows-1251; phrase timings spread over the words; "name.uk.srt" names the language.

### internal/dictate  (voice typing in the Home story box)
```go
func Resolve(cfg config.Config) Plan; func For(cfg config.Config, id string) Plan   // settings → service, key reuse; files only
type Plan struct{ Provider, Label string; Auto bool; Key, KeyFrom, BaseURL, Model, Bin, ModelPath, Language, Device string; Keys []string; Silence int; Ready bool; Problem string }
func Providers() []ProviderInfo; ProviderByID(id) (ProviderInfo, bool)               // ids: elevenlabs openai deepgram assemblyai openai-chunks gemini local
func Start(opt Options) *Session      // Options{Plan, FFmpeg, Notify}; never blocks, errors arrive in State
func (s *Session) Stop(); StopFor(StopReason); Cancel(); Heartbeat(); Done() <-chan struct{}
func (s *Session) State() State /* Phase, Level, Speaking, Heard, Elapsed, Mute, Device, Label, Reason, Err */; Take() (finals []string, interim string, changed bool)
type Splice struct{…}; (s) Begin(text, selStart, selEnd) []Edit; Update(text, caret, finals, interim) []Edit; End(text, keep) []Edit; Interim() (a, b int)
func ListDevices(ctx, ffmpeg string) ([]Device, error); func Check(ctx, Plan) (bool, string)
type Error struct{ Kind Kind; Msg string; Err error }; KindOf(err); NeedsSettings(err)  // NoProvider NoKey Auth Quota Network NoMic Permission NoFFmpeg NotInstalled Service
```
Capture (capture.go): FFmpeg writes 16 kHz mono s16le to stdout (`-flush_packets 1`): Windows `-f dshow
-audio_buffer_size 50 -i audio=<alternative name>` (list: `-list_devices true -f dshow -i dummy`), macOS `-f avfoundation
-i :<index>|:default` (Info.plist NSMicrophoneUsageDescription in build-mac.sh), Linux `-f pulse`. A session: devices →
capture → 20 ms frames → level meter, energy voice detector (floor = quietest frame of 3 s), silence stop, MaxLength 15 min,
heartbeat (4 s), digital-zero "Mute" warning — timing counted in audio. Streaming (own RFC 6455 client on net/http, ws.go):
ElevenLabs Scribe v2 Realtime (partial/committed, VAD commits, a final commit at the end), OpenAI Realtime transcription
(`?intent=transcription`, 24 kHz upsampled, gpt-live-transcribe with our commits or server VAD for the other models; items
released in commit order), Deepgram Nova-3 (interim/is_final; Finalize + CloseStream), AssemblyAI Universal-Streaming v3
(Turn: end_of_turn + turn_is_formatted = final; Terminate; no Ukrainian). Phrase services (chunker.go): pauses cut phrases,
interim re-reads every ~2.5 s, forced cuts with 0.4 s overlap de-duplicated, Whisper hallucinations dropped; OpenAI-compatible
`/audio/transcriptions` (json), Gemini `generateContent` with inline WAV (Google keys rotate), whisper.cpp — `whisper-server`
next to `whisper-cli` kept warm 2 min (else `whisper-cli` per phrase, no interim). Splice: finals in for good, interim
replaced each update (gui.FieldStyle.Pending fades it), spaces/capitals at the joins, the owner's caret moves respected.
UI: home/mic.go (mic in the card head, live strip, notice, «not set up» dialog; stops on Esc, route change, window blur via
the shell's optional `WindowFocused`), settings «Диктування» card (stt.go, `Route.Section` "dictate").

### internal/toolsinst  (helper programs, only on the user's button)
```go
type Asset struct{ URL string; Mirrors []string; Size int64; SHA256, Name string }  // SHA256 required
func Download(ctx, a Asset, dst string, onBytes func(done, total int64)) error       // .part + Range resume, 60 s stall, retries, mirrors
func FetchChecksums(ctx, url) (map[string]string, error); ParseChecksums(r) map[string]string; FileSHA256(path) (string, error)
func ExtractZip(archive, dst string, pick func(name string) string) error           // no escapes/links, size limits
func ReplaceDir(src, dst string) error /* ErrInUse */; Lock(tool) (unlock func(), error) /* ErrBusy */; RunVersion(ctx, bin, args...) (string, error); Unquarantine(path)
func Root() string /* config.ToolsDir() */; ToolDir(name) string; type Progress func(pct int, msg string); type UserError; func FormatSize(int64) string
func FFmpegDir() string; EnsureFFmpeg(ctx, Progress) (FFmpeg, error); InstallFFmpeg(ctx, Options{Dir; Force; NoHomebrew}, Progress) (FFmpeg, error)
func InstalledFFmpeg() (FFmpeg, bool); FFmpegOffer() Offer; type FFmpeg struct{ FFmpeg, FFprobe, Version, Source string }
```
FFmpeg 9.0.2, pinned (size + SHA-256 as published): Windows x64/ARM64 — Gyan's essentials build
(`github.com/GyanD/codexffmpeg/releases/download/9.0.2/ffmpeg-9.0.2-essentials_build.zip`, mirror
`gyan.dev/ffmpeg/builds/packages/…`, sha256 `60f46726…2047ba`) → `ffmpeg.exe`, `ffprobe.exe`, LICENSE, README;
macOS — `brew install ffmpeg` when Homebrew exists, else Martin Riedl's signed + notarized arm64/x86_64 builds
(`ffmpeg.martin-riedl.de/download/macos/<arch>/<build>_9.0.2/{ffmpeg,ffprobe}.zip`), quarantine cleared; Linux —
an error naming apt/dnf/pacman. Layout `<ToolsDir>/ffmpeg/{bin/, install.json}` (size + SHA-256 of every file);
downloads in `<ToolsDir>/.download/ffmpeg-<ver>/`, staging `.ffmpeg-unpack-*` swapped in whole after
`-version` runs. Idempotent: a copy matching install.json that starts is kept, anything else is installed
again (repair). One installation per tool; cancel keeps the part.

### internal/reference  («Перезняти за прикладом»: a model watches a YouTube video)
```go
func NormalizeURL(raw string) (string, error); func YouTubeID(raw string) string; func WatchURL(id string) string
func Probe(ctx, hc *http.Client, id string) (Meta, error)   // oEmbed + watch page, no key; *Error only for sure blockers
func FetchThumb(ctx, hc, id, dst string) error              // i.ytimg.com maxres → hq
type Watcher interface{ Name() string; Label() string; Watch(ctx, Request) (string, error) }
type Google struct{ Pool *gemini.Pool; Model string; HTTP *http.Client }               // Gemini API, the owner's keys
type Text struct{ BaseURL, Key, Model string; HTTP *http.Client; Truth Meta }          // OpenAI-compatible gateway
func TextCan(baseURL, model string) (can, known bool)       // what a gateway proved this session
func Analyze(ctx, w Watcher, o Options) (Breakdown, error); func ParseBreakdown(raw string) (Breakdown, error)
func (b Breakdown) Text(lang func(code string) string) string   // the Ukrainian text the user edits in step 1
type Error struct{ Kind Kind; Msg, Detail string }; func KindOf(err error) Kind; const MaxDuration = 90 min
```
Nothing is downloaded: Gemini reads the video from YouTube. **Google**: `POST …/v1beta/models/{model}:generateContent`,
the video part `{"fileData":{"fileUri":<watch URL>,"mimeType":"video/*"},"videoMetadata":{"startOffset":"600s",
"endOffset":"1200s","fps":0.2}}` before the text, JSON output, `mediaResolution LOW` for long videos; keys rotate through
`gemini.Pool` (quota → next key, then `KindQuota`); 5xx retried twice; clip/fps options the API refuses → once more without
them (the prompt still names the part). **Text**: one user message
("### INSTRUCTIONS … ### INPUT …") + the URL as `image_url` → `file{file_id,format}` → `video_url`; an HTTP 4xx, a refusal
or an answer whose `durationSeconds` misses the watch page's length (±max(6 s, 12 %); else the oEmbed title) moves to
the next shape; the working shape — or "cannot" for 6 h — is remembered per endpoint+model. **Analyze**: ≤ 12 min (or
unknown) = one look (overview + every scene with timestamps); longer = an overview at 0.2 fps + 10-minute parts merged
(clip-relative timestamps shifted); a reply cut off at its token limit is closed/trimmed and continued with parts;
`seen:false`/refusals → `KindNotSeen`. Errors: private, unlisted, age-restricted, removed, live, too long, no key,
quota → friendly Ukrainian (`Kind` lets the studio try the next watcher only for service problems).

### internal/notify
```go
func New(token, baseURL string) *Client                 // baseURL "" = https://api.telegram.org (else an own Bot API server)
func (c *Client) GetMe(ctx) (string, error); SendMessage(ctx, chatID, html string) error
func (c *Client) SendPhoto(ctx, chatID, path, caption string) error; FindChat(ctx) (Chat, error)
func Escape(s string) string; func ClipHTML(s string, max int) string; func Redact(s, token string) string
var ErrNoChat, ErrPhoto error; type Error struct{ Code int; Description string; RetryAfter int }
// api.go — the remote control's surface
func (c *Client) Me(ctx) (User, error); Send(ctx, chat, html string, *SendOptions) (Message, error)
func (c *Client) Edit(ctx, chat string, id int, html string, InlineKeyboard) error /* "not modified" = ok */; EditMarkup; Delete; Answer(ctx, queryID, text string, alert bool) error
func (c *Client) GetUpdates(ctx, offset int64, timeout int, allowed []string) ([]Update, error); SetCommands(ctx, []Command, chat string) error // scope: that chat
func (c *Client) Action(ctx, chat, action string) error; GetFile(ctx, fileID) (File, error); Download(ctx, File, dst string, max int64) error
type Update struct{ UpdateID int64; Message, EditedMessage *Message; CallbackQuery *CallbackQuery }   // + Message, User, MsgChat, File, CallbackQuery
type InlineKeyboard [][]Button /* Button{Text, Data ≤ 64 B, URL, Style} */; ReplyKeyboard{Rows, Persistent, Resize, Placeholder}; RemoveKeyboard; SendOptions{Markup, Silent, ReplyTo, Preview}
func RetryAfter(err) time.Duration; IsNotModified, IsGone, Unauthorized, Temporary(err) bool; Conflict(err) (conflict, webhook bool); FormatSize(int64) string
// upload.go
func (c *Client) Upload(ctx, Upload{Method, Field, ChatID, Path, Name, MIME, Caption, Params, Thumb, Cover, Opt, Progress}) (Message, error)
func (c *Client) SendPhotoWith(ctx, chat, path, caption, *SendOptions) (Message, error); SendAlbum(ctx, chat, paths []string, caption string) error
func (c *Client) UploadLimit() int64 /* 50 MB; 2000 MB on an own server */; Local() bool; func ThumbJPEG(path) ([]byte, error) /* ≤ 320 px, < 200 kB */; PhotoJPEG(path)
const MaxMessage 4096, MaxCaption 1024, MaxCallbackData 64, MaxAnswer 200, MaxUpload 50 MB, MaxUploadLocal 2000 MB, MaxDownload 20 MB, MaxAlbum 10
```
15 s per request (getUpdates: + its long-poll timeout), HTML parse mode, Telegram's length limits; errors never contain
the token. Uploads stream from disk with an exact Content-Length (prefix + file + closing boundary), report progress and
stop after 60 s without a moved byte or 3 min without an answer; a file over UploadLimit is refused before any byte goes
out. Checked against core.telegram.org/bots/api (Bot API 10.3, 2026-08-24) and the Bot FAQ's rate limits.

### internal/tgbot  (the Telegram remote control)
```go
func New(e *studio.Engine, opt Options) *Bot   // Options{StateDir, BaseURL, UploadLimit, PollTimeout, LiveAfter, Retry}
func (b *Bot) Start(); Stop(timeout); Closing(); State() State; Subscribe(fn func()) (cancel func())
func (b *Bot) Pair() State; CancelPairing(); Reconnect(); ForgetAttempt(id int64)
func Active() *Bot /* the started bot; nil headless */; func Preview(st State) *Bot /* pinned, never polls */
type State struct{ Status; Note, Username, Code string; CodeUntil time.Time; PairNote string; Attempts []Attempt }
// Status: Off | Starting | On | Offline | Conflict | Webhook | BadToken | BusyHere
```
- **Polling** (poll.go): a supervisor long-polls getUpdates (50 s, `["message","callback_query"]`) only while
  `Telegram.RemoteOn()` (switch + token + pairing) or a pairing code waits; a token/server change restarts it. The next
  update id goes to `<settings dir>/telegram-bot.json` BEFORE a batch is handled (a restart never repeats a command);
  `telegram-bot.lock` (pid + a beat every 15 s) keeps a second copy of the studio on this computer off the bot. 409
  "terminated by other getUpdates" → pauses 30 s, 60 s; the third in a row stops it (StatusConflict — two pollers would
  split the updates) until «Спробувати знову» (Reconnect); a webhook 409 or a refused token stop at once; 429 waits
  retry_after; network / 5xx back off 1 … 60 s.
- **Security** (auth.go): only the chat `Telegram.ChatID`, and there only `OwnerID` and `Allowed`; bots never. Pairing:
  Settings shows a crypto/rand 6-digit code (10 min, one use, 5 wrong tries end it); `/start <code>` (or
  t.me/<bot>?start=<code>) saves the chat, the owner and their name and turns the switch on. Everyone else gets «Цей
  бот приватний» only (private chats or commands for this bot; ≤ 1/min per chat, ≤ 20/min overall; presses answered
  with it) — no titles, no hint of a pairing; refused group members are listed in Settings («Дозволити»). Delete and «Зупинити все» need a
  second press within 2 min (a random confirm token).
- **Routing** (router.go): authorisation on the poller's goroutine; the owner's updates go to one worker per chat
  (session: the flow, drafts, the confirmation); every press is answered. Callback data ≤ 64 bytes: verbs + an
  8-character project key (base64 of the id's SHA-256 — stable across restarts, old buttons keep working).
- **Menus**: reply keyboard «🎬 Новий проєкт · 🔁 За прикладом · 📁 Проєкти · ⏳ Черга · ⚙️ Профілі · 📊 Статус»;
  /start /menu /help /new /remake /projects /queue /status /cancel (setMyCommands scoped to the chat). New project
  (newproject.go): text, .txt (UTF-8/16, Windows-1251) or a voice message (`Engine.TranscribeFile`, shown for
  confirmation) → one options message edited in place (profile, Автопілот/Покроково, 30 с/1/3/5 хв/своя, мова авто +
  8 + «інша», sound, content, aspect) → `CreateProject`. Remake (remake.go): link → wish → fidelity, length, profile,
  mode → `StartRemake`. Projects (menus.go): pages of 6 → card (stage, bar, «Далі: …», Автопілот, Зупинити, Повторити
  невдалі, Кадри, Озвучка, Монтаж, Відео, .srt, Обкладинка, Видалити). Queue: running jobs with bars and stop buttons,
  recent errors with retry, «Зупинити все». Profiles: the autopilot's default. Status (status.go): the Settings checks
  in parallel, the TTS keys' balance info, free disk space, the version.
- **Scenes not ready for the montage** (gaps.go): a montage refused with `MissingScenesError` (Монтаж / Далі /
  Повторити) or an autopilot stopped before it (`Job.Blocked = BlockedGaps`) → a message (never the 200-char popup)
  with every scene, the exact error and `FailureHint`, ≤ ~2 900 visible chars of list («і ще N»), and the step-5
  choices through the same API: «Змонтувати без них» / «З кадром замість відео» = `RunWith(StageMontage,
  {Missing: MissingSkip|MissingStill})`, «Перезняти невдалі» = `Retry` of the stopped autopilot (else the autopilot
  without re-planning), «Зупинитись» renders nothing. A stopped autopilot: its live message becomes this question,
  the studio's error notification carries the buttons (`TelegramHooks.ErrorMarkup`), without either the bot sends
  it; the card says «чекає на твоє рішення перед монтажем» with «Обрати, як змонтувати» / «Перезняти невдалі».
- **Live progress** (live.go): one silent message per running job (bot launches at once — the launch message turns into
  it —, computer jobs after 20 s while «Живий прогрес» is on), edited ≤ every 3 s and only on a free pacing slot; the
  end comes from `TelegramHooks.JobEnded`: deleted when a notification went out for a finished job, else (and for
  errors, with «Повторити») edited into the result; stopped by a closing app → says so.
- **Delivery** (delivery.go, encode.go): sendVideo with supports_streaming, duration, width, height, a ≤ 320 px thumbnail
  and the cover; «Надсилаю… 42 %» edited every 2 s; over the limit (50 MB, 2000 MB with an own server): «Стиснути»
  (two-pass x264 + AAC to the limit − 2 MB, 720p/480p when the bitrate is low; the pass log under an ASCII name in a
  temp dir) or «Частинами» (stream-copy segments ≤ the limit − 1 MB, re-cut shorter when a part comes out big).
  Subtitles and the full cover as documents, albums of ≤ 10 frames, a voice line as audio. One delivery at a time;
  temp files go after sending.
- **Pacing** (send.go): ≤ 1 message/s per chat (burst 3), groups ≤ 19/min and 3 s apart; a 429 blocks the chat and the
  call is repeated (≤ 2×).
- Tests: a fake Bot API (fake_test.go) enforcing Telegram's limits + a demo engine on temp folders; the transport guard
  admits only the test's own mock servers (never the owner's local services).

### internal/cover
```go
var Templates []TemplateDef   // 12 (incl. "shorts-top"); Fonts []FontDef — 8 OFL faces with full Ukrainian; Positions — 5
func ResolveSpec(t Text) Spec; func Render(base image.Image, spec Spec, size image.Point) (image.Image, error)
func Draw(canvas *image.RGBA, spec Spec) error; func Measure(spec Spec, size image.Point) (Metrics, error)
func SizeFor(aspect string) image.Point; func DefaultTemplate(vertical bool) string; func CoverFit(src image.Image, size image.Point) *image.RGBA
func SafeArea(size image.Point) (safe image.Rectangle, avoid []image.Rectangle)
func EncodeJPEG(w io.Writer, img image.Image) error  // stays under YouTube's 2 MB (MaxBytes)
func Missing(text, fontID string) []rune             // runes no face can draw (emoji, non-Latin/Cyrillic)
type Overlay struct{ Title, Value, Caption, Anim, Position, Accent, Font string } // a scene's on-screen text (overlay.go)
func NewOverlayClip(o Overlay, size image.Point, show, hide float64) (*OverlayClip, error)
func (c *OverlayClip) Bounds() image.Rectangle; Frame(t float64) *image.RGBA; Still(t float64) (still bool, id int)
```
Pure Go: balanced 1–3 lines (1–4 on Shorts) at the largest size that fits the safe area, outlines,
shadows/glows, `*word*` = highlight colour. Canvases 1280×720 and 1080×1920. Overlays (montage text):
`center` (the key number big, the headline as an accent label above it, an accent bar, the caption) |
`top` | `lower` (a lower third with a vertical accent bar); a soft dark glow behind; animations fade,
slide, pop (overshoot), count (the number counts up, grouping/decimals kept) and type (word by word);
entrances staggered, squeezed into short scenes, one fade-out; `Still` marks the frames that repeat.

### internal/media  (port + extension of studio/montage.ts)
```go
type FF struct{ FFmpeg, FFprobe string }; func Locate(cfg config.Config) (FF, error)
var InstalledDir func() string /* <config.ToolsDir()>/ffmpeg/bin */; func IsInstalled(path string) bool  // the studio's own FFmpeg
func (f FF) OK() bool; Version(ctx) (string, error); Run(ctx, dir string, args ...string) error // stderr tail in error
func (f FF) Probe(ctx, path string) (ProbeInfo, error); Thumbnail(ctx, video string, at float64, maxW int) (image.Image, error)
func (f FF) LastFrame(ctx, video, outJPG string) error          // the true final frame (chaining)
func (f FF) Montage(ctx, o MontageOptions) (MontageResult, error)
func Layout(o MontageOptions) (slots []Slot, total float64)       // the timeline; touches no file
var Transitions (23, Kind cut|dip|xfade), Effects (12), Filters (15), SubtitlePresets (9; `MaxWords` > 0 = phrase cues of 1…N words — "phrase")
func TransitionByID / EffectByID / FilterByID / SubtitlePresetByID(id string) (T, bool)
func ClampTransitionDuration(d float64) float64; func (s SubtitleStyle) Resolve() SubtitlePreset
func GridRects(img, rows, cols) ([]image.Rectangle, error); GridPanel; CropGridPanel(src, rows, cols, index, outJPG) error; SplitGrid
func DetectGrid(img) Grid // {Rows, Cols, Rects, Even}: the layout the sheet REALLY has (gutter projections + whiteness cores); g.Clean(3, 3)
func FitAspect(r, aw, ah) image.Rectangle; AspectOf("9:16") (9, 16); SubImage(img, r) // panel crops keep the frame format, never stretched
func DecodeImage(data []byte) (image.Image, string, error); func EncodeJPEG(img image.Image) ([]byte, error)
```
Segment {Video, Frame, Voice, VideoDur, VoiceDur, StillDur (a still lasts this before its voice lengthens it; 0 = 8 s), Narration, TrimIn, TrimOut, Transition, Effect,
OwnVoice, VoiceLead, VoiceTail, VoiceShift, VoiceTrimIn, VoiceTrimOut, ClipVolume (0 = AmbientVolume),
ClipSpeech (the clip's own speech keys the music ducking), Captions (subtitles timed inside the clip,
trimmed parts dropped)}; MontageOptions {Segments,
Aspect, Transition, TransitionDuration, Filter, DefaultEffect, Subtitles none|srt|burn, SubtitleStyle,
Music*, AmbientVolume, Ducking, TightLines (0.04 s breath + 0.08 s tail per line instead of 0.25 + 0.35 — the rapid tempo), OutDir, Overlay (OverlayFunc: per scene a concat list of transparent PNG
frames + X/Y, composited after the colour look, before the dips), Workers, Progress, Cancelled}; Slot adds the effective voice
line (`Voice*`). Timing (timeline.go — the single source of truth for the renderer AND the editor):
24 fps, 48 kHz (2000 samples/frame); a line starts 0.25 s into its scene with a 0.35 s tail and never
plays inside a transition overlap (the scene holds its last frame instead); unknown clip = 8 s; trims
leave ≥ 0.5 s; dips last 0.6 × the duration per side; own-voice slices keep their recorded lead/tail.
The editor's VoiceTrimIn/Out cut the voice audio (≥ 0.1 s stays) and VoiceShift moves the line
sample-exactly, clamped into the film — never the picture; a moved line may run under neighbouring
scenes and over other lines, its subtitles follow. Pipeline: scenes render in parallel (default ≤ 4
by CPU, ≤ 2 above 1080p) — trimmed clip or still with a perspective-based motion effect, colour look,
exact frames + exact PCM; xfades from the tail/head overlaps; stream-copy join; sound mixed in Go into
one sample-exact WAV (ambient parts at their scenes, voice lines where the editor put them, a
look-ahead limiter at −1 dBFS; the voices alone key the ducking); final pass: music looped + faded +
sidechain ducking, burnt subtitles from a generated `.ass` (the `.srt` stays plain). Old outputs are
cleared first (files a player locks stay); temp files removed.

Editor exports (exportplan.go, probestreams.go, stems.go — read-only views of the same plan):
```go
const FPS = 24; const SampleRate = 48000
func (s Slot) StartFrame(), Frames(), InFrames(), OutFrames(), ClipFrames() int; Fades() (in, out float64, inColor, outColor string); TransitionKind() TransitionKind
func SubtitleCues(o MontageOptions) []Cue; func SRT(cues []Cue) string      // exactly the montage's .srt
func EffectMove(id string, clip bool, W, H int) (Move, bool)                 // a motion effect as scale/offset/rotation keys; (m Move) At(p)
func (f FF) ProbeStreams(ctx, path) (StreamInfo, error)                      // + fps, frames, audio rate/channels, codecs, Image
func (f FF) VoiceStem / MusicStem(ctx, o MontageOptions, out string) (bool, error) // whole-film WAVs: lines in place; music looped, faded, ducked
func (f FF) CutClip(ctx, o, slots, k, from, to, W, H int, hasAudio bool, out string) error // a scene cut to its frames (CapCut)
func (f FF) StillFrame(ctx, src string, at float64, W, H int, out string) error            // a frame (conformed to W×H when > 0) as JPEG
```

### internal/nle  (editor timelines; no internal imports)
```go
type Rate struct{ Num, Den int64 }; func RateOf(fps float64) Rate /* snaps to 23.976 … 60 within 0.01 % */; (r) Frames, Seconds, Timebase, NTSC
type Timeline struct{ Name; Rate; Width, Height, SampleRate int; Length int64; Video, Audio []Track; Markers []Marker }; (t) Validate() error; Media()
type Track struct{ Name; Kind; Clips []Clip; Transitions []Transition; SceneSound bool }  // SceneSound: A1 mirrors V1's movie clips
type Clip struct{ Media *Media; Name; Start, Len, In int64; Gain float64; Mute bool; Levels []Level; FadeIn, FadeOut int64; Motion []Key; Scene int; Role string }
type Transition struct{ At, Len int64; Kind Dissolve|DipBlack|DipWhite; Source string }    // centred on the cut At
type Media struct{ ID, Name, Rel, Abs string; Kind Movie|Sound|Picture; Duration float64; Rate; Width, Height; HasAudio; SampleRate, Channels; Alpha }
func FCPXML(t, FCPXMLOptions{Event, Project, StaticStills}) ([]byte, error)   // 1.10; Resolve 18+, FCP 10.5+
func XMEML(t, XMEMLOptions{}) ([]byte, error)   // FCP 7 XML v4: Premiere, Resolve
func OTIO(t, OTIOOptions{Relative, Clip1}) ([]byte, error); func EDL(t) ([]byte, error); func MLT(t, MLTOptions{Absolute}) ([]byte, error)
```
Frames of the timeline rate everywhere (FCPXML: exact rationals, a clip's start on its media's own grid).
V1 = scenes (a scene longer than its clip holds the clip's last frame as a still after it), V2 = the
on-screen texts, A1 = the clips' own sound, A2… = voice lanes, last = music (looped; level, ducking and
fade-out as volume keyframes). FCPXML: scenes on the primary storyline, everything else connected (lanes
+k / −k), media by absolute file:// URL; `StaticStills` (Resolve: it reads no keyframes on pictures — a
fading text would stay invisible) writes the move's middle framing instead. xmeml: a clip next to a
transition has start/end −1 and in/out reaching under it (FCP 7's rule), stills sit 10 s into their
"file" so transitions have handles, keyframe `when` = media frames. Checked in DaVinci Resolve 21.1
through its scripting API (headless, a throwaway project, deleted): xmeml and OTIO come in exact (every
track, in/out, dissolves, dips, xmeml keyframes incl. stills and levels); FCPXML exact for pictures and
timing, its connected sound Resolve packs onto whatever track is free; a transition without media handles
makes Resolve leave the clip offline (the studio turns such dips into fades). Tests: golden files
(`go test ./internal/nle -run TestGolden -update`), timing per format, `FCPXML_DTD=<FCPXMLv1_10.dtd>`
checks the FCPXML against Apple's DTD (not shipped).

### internal/player
```go
func Audio() *AudioPlayer                    // process-wide, one oto context, 48 kHz s16le stereo
func (a *AudioPlayer) Play(ff media.FF, src string, onDone func()) error; Stop(); Playing(src string) bool; Err() error
func (a *AudioPlayer) PlayRange(ff media.FF, src string, at, dur float64, onDone func()) error // a part (a trimmed voice line)
func NewVideo(ff media.FF, path string, maxW int, invalidate func()) *Video
func (v *Video) Play(); Pause(); Toggle(); Seek(sec float64); Frame() image.Image; Position(), Duration() float64; Playing() bool; Err() error; Close()
```
Only one thing plays at a time; A/V sync = restart both at the seek position; frames ≤ maxW, 24 fps.

### internal/assets
`FS embed.FS; Logo(), Icon() []byte; StylePreview(id string) []byte` — a 640×480 JPEG for each of the 72
styles (nil → a palette tile, `StyleDef.Palette`). `cmd/stylegen` (dev tool) renders them through the user's
G-Labs with ONE fixed scene + "Art style:" and the style prompt the frames send: `check`, `prompt`, `gen`,
`reroll`, `process`, `sheet`; settings read only, key never printed; every submission → `<work>/ledger.jsonl`,
hard cap `-cap` 90.

### internal/studio  (port: studio/*.ts, routes/studio.ts, services/styles.ts)
One file per feature: types, engine (store, notifications, CRUD, service tests), edit, jobs, planner,
production, storyboard (+ clip lengths), montage, autopilot, projectnew, materials, profiles, profileai,
catalog + styles, cover + coverimage, translate, duplicate, ownvoice (+ transcripts), sttlocal, providers,
queue, notify (Telegram), importweb, util.
```go
func NewEngine(cfg *config.Store) (*Engine, error)  // usable even when err != nil (unreadable data folder)
// projects
List() []Summary; Create(title string) (Project, error); CreateWithProfile(title, profileID string) (Project, error)
CreateProject(opt NewProjectOptions) (Project, error)       // the new-project flows in one call (Behaviour)
Get(id) (Project, bool) /* deep copy */; Edit(id, fn func(p *Project)) error; Delete(id) error; Duplicate(id) (Project, error)
Dir(id) string; Abs(id, rel string) string; Subscribe(fn func(projectID string)) (cancel func()) // "" = list, profiles, FFmpeg
// jobs and single items
Run(id string, stage Stage, force bool, prompt string) error; RunWith(id, stage, RunOptions{Force, Prompt, Missing}) error
Cancel(id); CancelAll() int; Queue() QueueSnapshot
func MontageGaps(p *Project, dir string) []MontageGap; IsMissingScenes(err) (*MissingScenesError, bool); FailureHint(msg string) string // gaps.go, failure.go
Retry(projectID string) error; RetryItem(it QueueItem) error; Regen(id string, kind RegenKind, itemID, prompt string) error
Rewrite(ctx, id, sceneID, wish string) error /* sync */; StartAutopilot(title, text, profileID string) (projectID string, err error)
Translate(id string, opt TranslateOptions) (newID string, err error)             // {Language, Voice, Montage}
// user files: start materials, own recording + transcript, music, pictures
AddMaterial(id string, m MaterialInput) (Material, error); UpdateMaterial(id, materialID string, fn func(m *Material)) error
RemoveMaterial(id, materialID string) error; MaterialDescribing(id, materialID string) bool  // its look is being written
ImportImage(id string, kind RegenKind, itemID, srcPath string) error; SetMusic(id, srcPath string) error
SetVoiceFile(id, srcPath string) error; SetTranscriptFile(id, srcPath string) error; CreateFromVoice(title, srcPath, profileID string) (Project, error)
StartVoiceAutopilot(title, srcPath, profileID string) (string, error); StartVoiceAutopilotWith(title, srcPath, transcriptPath, profileID string) (string, error)
Transcribe(id) error; VoiceTranscript(id) (stt.Transcript, bool)
// remake after a YouTube example (remake.go)
StartRemake(opt RemakeOptions) (Project, error); RewriteRemake(id) error; ReanalyzeRemake(id) error; RemakeService() RemakeService
// montage and cover
Timeline(p Project) []TimelineItem; ApplyCoverText(id) error /* sync */; PreviewCover(id string, ct CoverText, maxW int) (image.Image, error)
SuggestCoverTitle(ctx, id string) (string, error); SetCoverImage(id, srcPath string) error /* sync */
// profiles
Profiles() []config.Profile; Profile(id) (config.Profile, bool); SaveProfile(p config.Profile) (config.Profile, error)
DeleteProfile(id) error; ApplyProfile(projectID, profileID string) error; ProfileFromProject(projectID, name string) (config.Profile, error)
SuggestProfile(ctx, description string) (config.Profile, error); SuggestProfileWithReasons(ctx, description string) (config.Profile, []string, error)
SuggestCustomSpec(ctx, video bool, docs string, progress func(string)) (CustomSpecSuggestion, error)   // the universal own API, from its docs (not saved)
SuggestSpec(ctx, kind providers.SpecKind, docs string, progress func(string)) (CustomSpecSuggestion, error) // any kind: image, video, TTS, STT
// «Помічник підключення» (assistant.go) and the text checks (textcheck.go)
AnalyzeConnections(ctx, text string, progress func(string)) (*AssistPlan, error); ApplyAssist(plan *AssistPlan) (AssistResult, error)
func ParseAssist(text string) []AssistItem; type AssistPlan struct{ Items []AssistItem; Steps []*AssistStep; Notes []string; AI string }
type AssistStep struct{ Role, Target string; Lines []string; Status, Msg string; On bool } // Role text|image|video|tts|stt; Status ok|fail|skip
CheckText(ctx, t config.Text, chat bool) (llm.Probe, error); TextModelsOf(ctx, t config.Text) ([]string, error)
// settings and services
Config() *config.Store; FF() media.FF /* never blocks */; Budget(p) (scenes, clipSeconds, wordsPerScene int); ClipSeconds() int; ClipLengths() []int
TestText, TestGlabs, TestGoogle, TestFFmpeg, TestSTT, TestTTS, TestTelegram, TestCustomImage, TestCustomVideo(ctx) (bool, string)
TTSKeyStatus() []tts.KeyStatus; ResetTTSKeyErrors()                                 // Settings → Озвучка key list
GoogleKeyStatus() []gemini.KeyStatus; CheckGoogleKeys(ctx) (bool, string); LookupVoice(ctx, id) (tts.Voice, error) // settingskeys.go
InstallLocalSTT(ctx, model string, progress func(int, string)) error; LocalSTTStatus() (bin, model string, ok bool, hint string); LocalSTTModels() []stt.ModelInfo
InstallFFmpeg() bool /* background; false while one runs */; CancelFFmpegInstall(); FFmpegInstall() ToolInstall; FFmpegOffer() ToolOffer; FFmpegManaged() bool
func MaxConcurrency(c config.Config) ConcurrencyLimit /* {Max, Recommended, Service, Why} */; ClipConcurrency, ImageConcurrency, VoiceConcurrency, SpecConcurrency(c) int /* specs written at once: CodeWriters, else 3 (Ollama 1) */
TextModels(ctx) ([]string, error); Voices(ctx, force bool) (tts.VoiceList, error); FindTelegramChat(ctx) (chatID, title string, err error)
SetTelegramHooks(TelegramHooks{FinalMarkup, ErrorMarkup, JobEnded, FindChat}); TranscribeFile(ctx, path, lang string) (stt.Transcript, error); func JobLabel(k JobKind) string   // remote.go, for tgbot
ImportFromWeb(root string) (projects int, err error); Shutdown(timeout time.Duration) error; Flush() error
// editor export (export.go): «Експортувати в…»
ExportTimeline(projectID string, opt ExportOptions) (dir string, err error); ExportTimelineContext(ctx, id, opt) (ExportResult, error); ExportCheck(id) (ExportReadiness, error)
func ExportTargets() []ExportTarget; ExportTargetByID(id); DefaultExportOptions(target) ExportOptions   // resolve premiere finalcut capcut kdenlive otio files
// settings transfer (settingsio.go): backup → profiles (ReplaceAll) → one Store.Update → key states
ExportSettings(opt config.ExportOptions) ([]byte, error); SettingsBundle(opt) *config.Bundle; ImportSettings(b *config.Bundle, plan config.ImportPlan) (config.ImportResult, error)
SettingsBackups() []config.Backup; RestoreSettings(b config.Backup) error
```
Errors: `ErrBusy` (match with `errors.Is`), `ErrNotFound`, `ErrClosing`, `ErrNoCover`,
`config.ErrProfileNotFound`. Helpers: `SceneState`, `AssetState`, `SpeechSeconds` (words / 2.3),
`TimelineDuration`, `LastFrameRel`, `VoiceExts`, `TranscriptExts`, `MaterialExts`, `VideoCapsFor`,
`OmniFlash`, `VideoSpeech` (Speech No/Maybe/Yes), `DialogueSeconds`, `DialogueWords`, `DialogueClipSeconds`, `VoiceOutgrowsClip`, `LinesText`, `SpeakerName`,
`Input.SoundMode/ContentMode/NoPeople/TextOverlays`, `StylePrompt`, `StyleByID`, `ChoiceByID`, `LanguageByCode`, `CoverTemplates/Fonts/Positions`,
`CoverSpec`, `CoverFinalOutdated`, `Default*`.
Catalogues (Ukrainian labels, English hints — they go into prompts): `Styles` 72 (the web's 23 first,
same ids/prompts; Category, Suits, Palette, optional Direction (planner rules of the style) + TextPolicy (replaces "no text" in
frames and shot prompts — the map style's labels)), `StyleCategories` 6, `Genres` 37 (the web's 12 first),
`Cameras` 10, `Tempos` 4 (`rapid` = Shorts pacing: with stills the plan uses 3 s scenes at 2.9 words/s and the montage's `TightLines`),
`Tones` 10, `NarratorStyles` 4 (narrator kind → voice when none is chosen: Settings' voice of that gender, else the kind's
fixed voices of OpenAI / Google / Azure, else by gender and age), `Languages` 93 (uk, en first; ISO 639-1, else 639-3).
Stages: plan, refs, frames, render (= videos + voices), videos, voices, montage, cover, autopilot.
`JobTranslate` and `JobTranscribe` are started by Translate/Transcribe; Retry restarts every kind —
failed, stopped, or "done" with `Failures` (then only the failed items are redone).

Behaviour:
- **Plan** (planner.ts): "bible" pass + batched shot pass (8/batch), script splitting, unique
  transliterated @tags, voices auto-assigned; ≤ 150 scenes, words fitted to the clip (2.3 w/s);
  Language and Tone steer the writing. ModeVoice: transcribe if needed, cut the words into scenes at
  sentence ends/pauses (4 s … the model's longest clip); the narration is fixed text.
- **Production** (production.ts): refs 3:4; frames with the refs named `<tag>.<ext>` + a no-text line;
  concurrency (concurrency.go, the documented limits with sources): clips = `cfg.Concurrency` clamped to the video
  service's maximum (G-Labs 10, Google Veo 4 + 2 per extra key, RoyalTechno/fal/Replicate/Runway 20, Luma 10,
  MiniMax 10/20; 1 while chaining), images = the same setting, at least 4, up to the image service's cap (G-Labs 8,
  OpenAI Images 4 …), voices = the TTS key pool's capacity (per-key concurrency × keys, ≤ 8). Storyboard mode draws a
  3×3 sheet per scene (panel 1 → Frame); on Omni Flash a clip renders in components mode (board,
  previous shot's last frame `images/last-<scene>.jpg` — only while the location stays the same, cast/location
  refs); a components clip G-Labs refuses for its content (INVALID_ARGUMENT or a safety filter) gets ONE safer try
  (`renderClip`): the frame alone with the plain motion prompt — on the owner's G-Labs every refused clip carried the
  3×3 board, frame-only clips went through; both exact errors stay when it fails too (`saferError`). A board refused
  for its content gets one single-frame try (`boardFallback`: the frame is done, `Scene.Board` keeps the refusal).
  Failed assets store the service's exact error, keys masked (`failureText`, ≤ 1500 runes); screens and Telegram add
  `FailureHint` (PROMINENT_PERSON, REPUTATIONAL, DANGEROUS, INVALID_ARGUMENT, quota, 5xx …). Voices:
  `tts.SynthesizeWith` in the audience language, or a slice of the own recording. Cover: GEM_PIX_2.
- **Board layout check** (storyboard.go): the sheet is requested in the project aspect with the grid first ("ONE image … exactly 3
  columns and 3 rows of nine equal-sized 9:16 panels", thin white gutters, no text); `media.DetectGrid` must find a clean 3×3,
  else ONE redraw with the strict prompt (`boardPromptFor(…, true)`), else `boardFallback` (single frame; the board says why) —
  never a crop through a wrong grid. Panel 1 → `FitAspect` to the project format. The lightbox numbers the detected panels.
- **Prompt rewrites** (rewrite.go): a CONTENT refusal of any provider (`contentRefusal`: glabs.Rejection, safety / moderation /
  content_policy / PROMINENT_PERSON …; never 429 / 5xx / timeouts) → the text model rewrites the prompt (`rewriteWithLLM`:
  fictional people, softened violence / drugs / weapons, no brands, @tags and layout kept, lost tags re-appended) and it is
  retried — at most 2 rewrites (a board: 1, then the single frame), then the exact error + a note (`rewriteError`). Refs, frames,
  boards and clips (the plain frame-only prompt after `renderClip`). The rewrite that went through is `Asset.Rewrite`
  (`PromptRewrite {Original, Prompt, Reason, Count, At}`), reused while the composed prompt is unchanged; `RevertRewrite(id,
  kind, itemID)` drops it (asset stale). The autopilot never resubmits a refused item (`rejected` → `refusedText`).
  `Engine.rewriter` is the test hook.
- **Production modes** (modes.go, `Input.Pipeline` «Режим виробництва»): "" full | "norefs" (no reference generation;
  `castLinesFor` and the frame / board prompts carry the looks in words; material pictures still bound) | "stills" (no clips:
  `runRender` voices only, Regen video refused, `MontageGaps` takes the frame as the scene, `montageOptions` sets `StillDur =
  stillLength` and `AutoStillEffect` from the motion prompt unless an effect was chosen) | "norefs+stills".
  `PipelineCostOf(p, mode)` (refs / frames / clips / code / minutes) feeds `pickers.Pipeline`; profiles, `NewProjectOptions.Pipeline /
  PipelineSet`, the Telegram draft («🏭 Виробництво») and the step tabs (`core.StepLabel` badges) carry it.
- **«Анімація кодом»** (codeanim.go, codeanim_engine.go; Pipeline "code" | "norefs+code", `Input.CodeAnim()`): no video model —
  every scene is a codeanim clip (internal/codeanim behind `CodeRenderer`; `SetCodeRenderer` for tests/headless). Grammar:
  `Input.CodeGrammar` ("" = auto) / `Scene.Code.Grammar`; the shot writer's "motionPrompt" is the animation brief (+ "grammar" →
  `Code.Suggested`). StageFrames = voices first, then per scene the text model writes the spec (`codeSystem`: the grammars' schema +
  example; duration = `codeTiming` — the voice + the montage's breath/overlaps, exact to the frame; the project's 1920×1080 / 1080×1920,
  30 fps; cues on the words of `codeWords`: the recording's transcript, multi-voice part places, else syllable-weighted spread;
  `SpecConcurrency` specs at once — a map film writes its first scene alone, then each prompt continues the nearest earlier scene
  already written; map specs get `fixMapSprites` (scales under 0.3/0.26 dropped → the grammar's big default) and `ensureMapMotion`
  (codeanim_map.go: no action cue besides the camera → a sprite bounce / focus-region glow; no camera cue → a `zoomBy` 1.1 push-in)), checked
  (`checkSpec`), ONE repair round, else `Code.Fallback` + the exact error (the scene = its frame, drawn then; `Montage.Gaps` lists it
  as GapStill). Pictures the spec declares (`images`: sprite | picture) come from the image service (sprites on chroma green, keyed —
  `keyGreen`; the cast's reference bound; one file per identical request, `images/code/`). StageRender = `Available` check (else every
  clip carries the exact reason), then `renderCode` (1–2 at a time, `CodeConcurrency`; ctx = the job's, cancellable) →
  `videos/code-<scene>-<ms>.mp4` = `Scene.Video` (Duration = spec, `Code.Rendered` = its signature; unchanged specs are not rendered
  again); the montage plays it with no motion effect. Edits: the spec is the user's (`Code.Edited`; re-timed, never rewritten, when the
  voice changes), a new grammar / text rewrites it; `RewriteCode`, `CodePreview` (still → `Code.Preview`), `GrammarPreview` (the
  examples' stills, cached in `<data>/.cache/codeanim/`), `ValidateCodeSpec`, `CodeCues`/`SetCodeCue` for the step-3 editor
  (produce/codeanim.go: fields + raw JSON, live check, «Попередній перегляд кадру»); pickers.CodeGrammar shows the previews.
- **Providers, clip lengths** (providers.go, storyboard.go): `Image/Video.Provider` glabs (default) | google |
  custom (`providers.Client`); storyboards and chaining only on Omni Flash (`OmniFlash`: G-Labs, Google Gemini Omni, RoyalTechno), the others
  animate the frame. `videoCaps` is the single source of lengths — G-Labs Veo 8 s (+ 4/6 s with
  `Video.Ultra`), Omni Flash 4/6/8/10 s; Google Omni 4/6/8/10 (≤ 10 refs), Google Veo 4/6/8; custom per preset/model. With `Video.Dynamic` (always for own-voice
  slices) a clip is the shortest allowed length ≥ its line + 0.6 s + what the project's montage adds around it
  (`montagePad`: crossfade overlaps, long dips — else the clip froze for them); lines said on camera allow slow
  speech (syllables ÷ 4.4/s, `dialogueNeed`; `DialogueClipSeconds` = the same for the screens) (`Scene.ClipLen`,
  sent as `Seconds`); plans use the average length (`Budget`); a clip waits for its voice made in the same run (voices
  go in scene order). A new voice line (TTS, multi-voice too) that outgrows the scene's finished clip —
  voice + 0.6 s + `montagePad` > the clip minus its trims (`VoiceOutgrowsClip`) — makes the clip stale; step 4's card
  shows «Озвучка довша за кліп — перезняти?» and it joins «Перезняти змінені».
- **New project** (projectnew.go): `CreateProject` = the profile (none with NoProfile; Autopilot without
  ProfileID = the default autopilot profile) → the set options → materials → recording → transcript →
  autopilot; inputs checked first, a failed step deletes the project (a failed autopilot START returns it).
- **Start materials** (materials.go): ≤ 24 pictures (PNG/JPG/WEBP ≤ 40 MB; character, location, object,
  style) copied into `materials/`; the text model describes each in the background (vision → `Material.Look`;
  failure → the user's note). The plan waits ≤ 90 s for pending looks; material characters/locations are in
  every plan under their names (`MaterialID`, kept across re-plans) with the picture as their reference
  (hard link, no generation); objects become props in the shot prompts, style samples join the style guide.
- **Transcript files** (ownvoice.go): `SetTranscriptFile` copies SRT/VTT/JSON/TXT into `audio/` (refused when
  phrases run > 2 s past the recording), stores the sidecar (provider "file") and fills Input.Text — no
  speech-to-text then; "" removes it (and its text, unless edited); `SetVoiceFile` keeps it while it fits the
  new recording (±2 s). **Local STT** (sttlocal.go): `InstallLocalSTT` installs into `stt.LocalDir()` and
  stores STT.LocalBin/LocalModel (the provider is not switched).
- **Remake** (remake.go, `JobRemake` "remake" «Перезйомка»): `StartRemake` = `CreateProject` (profile or «як у прикладі»,
  language) + `Project.Reference` (URL, wish, fidelity exact|close|structure, keep length/style, autopilot) → the job:
  probe (refuses private/unlisted/removed/live/too long at once; thumbnail `reference/thumb.jpg`) → the watchers of
  `config.Remake.Analysis` (auto = Google keys, then the text gateway; the next one only on service errors, and when all
  fail the first one's error is shown) →
  `reference/breakdown.json` + `Reference.Breakdown` (editable Ukrainian text) → the text model writes EXACTLY N scenes
  (N = length ÷ the plan's clip; scene k covers its share of the original) → story-mode `Input.Text` («Сцена n · …»,
  «Кадр:», «Текст на екрані:», the spoken line), `TargetSeconds = N × clip` (so the plan has N scenes), and — only without a
  profile — the reference's aspect, style, genre, tempo, camera, tone, AudioMode/Voiceover, Content, Overlays and English notes. With a
  profile (`Reference.KeepStyle`) the profile's look, pacing, voice and modes win: the screenwriter gets `profileLook` (the style prompt
  + Direction, genre/tempo/camera/tone hints, the profile's notes, the format) and only content, beats and timing follow the
  reference; the notes become the remake lead + the profile's notes (`stripRemakeLead` keeps rewrites from doubling it) →
  with Autopilot the same job turns into `JobAutopilot` and runs it. Stage "" → analysed → scripted: «Повторити»
  continues; `RewriteRemake` writes the screenplay again from the edited breakdown, `ReanalyzeRemake` watches again.
- **AI profile** (profileai.go): `SuggestProfileWithReasons` fills a whole profile from a free
  description (the reply repaired locally or asked once more; values checked against the catalogues,
  unknown → defaults; ≤ 8 Ukrainian reasons); nothing is saved until the UI calls SaveProfile.
- **AI own-API setup** (customspec.go): `SuggestCustomSpec(ctx, video, docs, progress)` — docs = a URL
  (`providers.FetchDocs`: a linked/well-known OpenAPI or llms.txt preferred, ≤ 120k chars, big OpenAPI summarised)
  or pasted text → the text model with `providers.SpecPrompt` → `{spec, baseUrl, model, notes}`, validated
  (`providers.ParseSpec`; one repair round with the problems) → `CustomSpecSuggestion` for review. Every key of the
  settings is masked out of what the model gets; nothing is saved (Settings shows a draft). `SuggestSpec` does the
  same for any `providers.SpecKind` (TTS / STT with `SpecPromptFor`).
- **Connection assistant** (assistant.go): pasted text → `ParseAssist` (URLs, bare hosts, keys by known prefixes
  sk-ant-, sk-or-v1-, sk_car_, sk_+hex, AIza/AQ., gsk_, xai-, r8_, key_, luma-, rt_live_, fw_, fal id:secret, cg- and
  long tokens after «ключ/key/token»; models after «модель» or by family; voices; docs links; the line's words hint
  the role) → groups (items on one line, a key/model under an address) → known hosts/keys decide without requests
  (OpenAI, Anthropic, OpenRouter, Groq, xAI, DeepSeek, Mistral, Together, Fireworks, Google AI Studio, Ollama, Azure,
  CarteLink, G-Labs, ElevenLabs, Cartesia, Lumean, Fish, MiniMax, AssemblyAI, fal, Replicate, Runway, Luma,
  RoyalTechno; never path-scanned) → an unknown address gets only cheap GETs (llm Check without chat: model lists on
  every form, then Anthropic's on a refusal; Ollama /api/tags; ElevenLabs-style /v1/voices; G-Labs /api/health) → the
  text model (when unclear) sees the text with keys as [KEYn], URLs as [URLn: …] and every stored secret masked →
  a docs link with a role becomes a universal spec (SuggestSpec) → one candidate per role (a Google key alone is a
  fallback; it does not switch a working frames/video service on by itself) → each checked with its service's own
  check on the candidate settings (nothing generated; text: one word) → `ApplyAssist`: `Store.SaveBackup`, then one
  `Store.Update` with the steps left on. Works without any text model.
- **Text checks** (textcheck.go): `TestText` = llm Check with chat; the address form, auth header and Azure
  api-version that worked are stored («Адресу виправлено: … → …»); a generation that finds another form (OnResolved)
  stores it too.
- **Autopilot**: plan (kept when present unless forced) → refs → frames → render → retries of failed
  items (`Autopilot.Retries`; never what G-Labs refused for its content — `glabs.Rejected`) → montage → cover (MakeCover). Scenes the montage cannot show as planned (`MontageGaps`: no clip, no
  frame either, no voice line in a voice-over film) are NEVER filled in or dropped silently: `Run(StageMontage)` refuses
  with `MissingScenesError` (every scene + its exact error) until the user picks `RunOptions.Missing` in the montage
  dialog — MissingSkip «Змонтувати без них» | MissingStill «З кадром замість відео» (no frame → left out, no voice →
  silent) | «Зупинитись»; the autopilot follows `Autopilot.OnFailed` only when set, else stops before the montage
  (`Job.Blocked = BlockedGaps`; the banner's «Обрати, як змонтувати» opens the dialog). What a render left out or
  replaced is `Montage.Gaps` (+ the job message, the final card, the Telegram caption). One job with progress slices ("Автопілот · …");
  Retry continues where it stopped.
- **Sound and content modes** (modes.go, overlay.go): `Input.AudioMode` voiceover | invideo | silent
  ("" = Voiceover decides; Edit keeps Voiceover = (mode == voiceover); the own recording is always the
  voice-over) and `Input.Content` characters | nopeople | motion | mixed, `Input.Overlays` "" | on | off
  (auto = on for motion). **invideo**: the bible writes per-scene `Lines` (speaker = a visible character or
  "narrator", words ≤ `DialogueWords(clip)` at 2.0 w/s (`talkWPS`: the owner's Ukrainian lines ran ~2.0, the slowest tenth ~1.7), an English delivery) instead of narration, every
  character's `Voice` and the off-screen `NarratorVoice`; no TTS voices; the clip prompt starts with the
  lines in Veo's form — `Name (look cue; voice: …) says (delivery): "…"` + "(Spoken aloud in <lang>,
  lip-synced; no subtitles…)" — then the motion, "only the lines above are spoken… no background music";
  dynamic clip lengths follow `DialogueClipSeconds` (slow speech + 1 s); the voice step is skipped; the montage plays the
  clips at full level (`ClipVolume` 1), their speech ducks the music, the subtitles are the lines
  (timed by estimate, `lineTiming`). `VideoSpeech` warns when the video model has no sound (G-Labs,
  Google Veo 3.x, RoyalTechno veo-3.1/omni yes; seedance/grok/kling/wan maybe; Gen-4, Ray, Hailuo no).
  **nopeople / motion**: the cast holds objects/motifs (`Character.Kind` "object": object-shot
  references; object and — without people — character materials bind to them), locations are places /
  designed backdrops, every prompt says "no people"; motion frames are design compositions that leave
  room for text. **Overlays**: the bible writes `Scene.OnScreen` {title, value, caption, anim} (every
  scene in motion graphics, key facts elsewhere); the montage renders it with `cover.OverlayClip`
  (accent = a bright colour of the style palette) as PNG frames (`writeOverlay`: each distinct picture
  once) composited over the scene; edits never make a clip stale. **Changed lines** make the clip stale.
- **Multi-voice** (multivoice.go, «Мультиголос» = `Input.MultiVoice`, voice-over only): the bible writes per-scene
  `Parts` (speaker "narrator" | "narrator2" | "narrator3" (`Input.ExtraNarrators`, ≤ 2) | a character id, text, delivery;
  script parts kept verbatim, else one narrator part) and character `Age`; `Narration` = the parts joined (old readers
  unchanged). `assignSpeakerVoices` (plan, `AssignVoices`) picks the current provider's usable voices by gender, age and
  the film's language, each once while enough (cast first, «Диктор 2» a contrasting gender). The voice step: one voice →
  one request; ElevenLabs + Eleven v3 → one `tts.SynthesizeDialogue` (text-to-dialogue/with-timestamps, ≤ 10 voices,
  ≤ 2 000 chars; delivery as v3 audio tags); else every part on its own, joined by `media.JoinVoices` (0.18 s pause,
  0.38 s when the speaker changes); `Line.Start/End` place the parts in the line → `Segment.VoiceParts` → subtitles per
  part (`SubtitleStyle.SpeakerColors`: ASS `c` in `SpeakerColor`). Edits: a removed speaker → narrator, the whole
  narration edited → one part, changed parts / a speaker's new voice make the line stale. Translate keeps the speakers.
- **Translate**: a copy (SourceID) sharing every picture/clip by hard link; its job translates the
  texts in batches of 8 fitted to the source's speaking time (±10 %), voices every scene, optionally
  renders the film; @tags, ids and image/motion prompts are never touched. Spoken lines (invideo) and
  on-screen text are translated too; clips with lines go stale (they speak the source language) and
  the job says so. **Duplicate**: full copy
  "(копія)", media hard-linked (copied across drives). Both carry the materials and the transcript.
- **Editor export** (export.go, export_readme.go; no job — the caller's goroutine, ctx cancels and removes
  the half-made folder): `<project>/export/<target>-<date>[-n]/` (+ `<folder>.zip`) = `media/` (`001_scene.mp4` as
  generated — the timeline holds in/out —, `001_frame.jpg` conformed to the film, `001_hold.jpg` a clip's held last
  frame, `001_voice.*`, `001_text.png` full-frame transparent text, `music.*`; hard links on the same volume, else
  copies), the target's timeline (Resolve: `timeline.xml` + `timeline.fcpxml` + `timeline.edl`; Premiere `timeline.xml`;
  Final Cut `timeline.fcpxmld/Info.fcpxml` + `.fcpxml`; Kdenlive/Shotcut `timeline.otio` + `timeline.mlt`; OTIO), `subtitles.srt`
  (media.SubtitleCues), `cover.jpg`, `edit-list.csv` (the cut list), `README.txt` (UTF-8 BOM, CRLF: contents, steps,
  rate/size, approximations), `stems/voice.wav` + `stems/music.wav` (always for CapCut and «Лише файли», else
  `Stems`). The plan is `media.Layout` with probed durations; the timeline runs at the clips' most common rate
  (`nle.RateOf`), size = the first clip's (like the montage). Cuts between overlapping scenes sit in the middle of the
  overlap (a dissolve of the overlap's length centred there, shortened to the media handles); xfades other than
  «Розчинення» become dissolves (note); dips become dip-to-colour transitions when both clips have media past the
  cut, else fades on the two clips; the film's opening/closing fades are clip fades; motion effects → transform keys
  (eased: 9). Music ducking: −12 dB under voice lines and talking clips (spans merged within 0.6 s, 50 ms down,
  450 ms back). **CapCut**: it imports no timeline files and its drafts are encrypted (JianYing 6+) or change shape
  with every version (CapCut 7.7 keeps the timeline under `Timelines/<id>/`, unregistered folders are invisible) — so
  no draft is written: every scene is cut to its exact frames (`media.CutClip`, hard cuts at the overlap middles),
  numbered, and the stems line up from 0:00. `ExportCheck`: clips, stills, skipped scenes, voices, what the project
  has, `Blocker` (no frame/clip, no FFmpeg); an incomplete project needs `Partial` («Експортувати те, що є»), else
  `ErrExportIncomplete`.
- **Queue**: running jobs + single regenerations (tracked by a subscriber); recent = each project's last
  job and ≤ 30 regeneration results; `PartlyFailed()` = a "done" job with `Failures`; `Counts` feed the rail
  badge. **Telegram**: after a job ends (not when cancelled), on its own goroutine — OnError / OnDone /
  OnFinal (cover + "🎬 Відео готове"). With the remote control on, the final (and a final «done») message carries the
  bot's buttons (`TelegramHooks.FinalMarkup`), every job end — stopped ones too — reaches `JobEnded` (live messages),
  «Знайти мій чат» asks the polling bot instead of calling getUpdates, and `Telegram.APIServer` serves the
  notifications as well.
- **Edit** (PATCH /projects/:id): engine-owned fields (ids, assets, job, outputs, the recording + transcript,
  materials, MaterialID) are restored; edited text/prompts flag the dependent voice/frame/clip stale; deleted
  cast leaves every scene; the step never goes back; VoiceShift is clamped to ±60 s. Re-voicing a line is
  `Regen(RegenVoice)` (an own-voice slice is cut again; a TTS line that outgrows its clip makes the clip stale). Unreferenced files move to `<project>/trash/`.

Storage: `<DataDir>/projects/<id>/project.json` (atomic, debounced 400 ms; immediate at job end,
Flush and Shutdown) + `images/ videos/ audio/ montage/ user/ materials/ trash/` (an attached
transcript: `audio/transcript-<name>-<id>[.<lang>].<ext>`, parsed into the sidecar
`audio/transcript.json`). On load (store.ts normalize) a job left running becomes an error, running
assets become done (they keep an older file) or error; fields missing in older files take their defaults.

## Data model (project.json)

`Project {ID, Title, Logline, StyleGuide, CreatedAt, UpdatedAt, Step 1–5, Input, Characters, Locations,
Scenes, Montage, Job *Job, ProfileID, SourceID}`; `Asset {Path (relative, forward slashes), Status
empty|queued|running|done|error, Error, Progress, Duration, UpdatedAt (cache version), Stale}`. New fields:

| Where | Field | Meaning |
|---|---|---|
| Project | `ProfileID`, `SourceID` | profile applied last; the project this translation came from |
| Project | `Reference` | the YouTube example of a remake: URL, wish, fidelity, facts, `Breakdown` (editable), `Stage` (omitzero) |
| Input | `Mode` `"voice"` | ModeVoice: the user's recording drives the film (besides story/script) |
| Input | `Language`, `Tone` | audience language (catalogue code, "" = like the text); narration tone |
| Input | `StoryboardMode` `"grid3x3"`, `ChainFrames` | 3×3 sheet per scene; last-frame chaining (Omni Flash) |
| Input | `Pipeline` | «Режим виробництва»: "" full, "norefs", "stills", "norefs+stills", "code", "norefs+code" (modes.go) |
| Input, Scene | `CodeGrammar`; `Code` | «Анімація кодом»: the grammar ("" auto); the scene's spec, pictures, preview, fallback (`CodeClip`, codeanim.go) |
| Asset | `Rewrite` | the automatic prompt rewrite that made this file (`PromptRewrite`, rewrite.go) |
| Input | `VoiceFile`, `VoiceFileName` | the recording (`audio/voiceover-…`); transcript sidecar `audio/transcript.json` |
| Input | `TranscriptFile` | a transcript with timecodes attached to the recording (`audio/transcript-…`): no speech-to-text |
| Input | `Materials` | start materials `Material {ID, Kind, Name, Note, Path (materials/…), Look}`; Kind = character, location, object or style |
| Character, Location | `MaterialID` | bound to a material: its picture is the reference, its look the description; kept across re-plans |
| Scene | `Board` | the 3×3 sheet; panel 1 is cropped into `Frame` |
| Scene | `TrimIn`, `TrimOut`, `Transition`, `Effect` | timeline controls ("" = the project's default) |
| Scene | `AudioSource` `"upload"`, `VoiceStart`, `VoiceEnd` | this scene's slice of the own recording |
| Scene | `VoiceShift`, `VoiceTrimIn`, `VoiceTrimOut` | the voice line moved (+ later, − earlier) and trimmed in the montage editor |
| Scene | `ClipLen` | the clip length chosen for this scene (dynamic lengths, own-voice slices; 0 = Settings) |
| Input | `AudioMode`, `Content`, `Overlays` | sound mode (voiceover / invideo / silent), what the frames show (characters / nopeople / motion / mixed), on-screen text switch |
| Scene | `Lines` | invideo: `Line {Speaker (character id or "narrator"), Text, Delivery}` said on camera |
| Scene | `OnScreen` | `{Title, Value, Caption, Anim fade/slide/pop/count/type, Position center/lower/top}` animated by the montage |
| Character | `Kind`, `Voice` | `"object"` = an object / motif (object reference); how the video model voices the character |
| Project | `NarratorVoice` | invideo: the off-screen voice for "narrator" lines |
| Input, Scene, Character | `MultiVoice`, `ExtraNarrators`; `Parts` (+ `Line.Start/End`); `Age` | «Мультиголос»: the voice-over split between speakers, each with a TTS voice (multivoice.go) |
| Profile | `AudioMode`, `Content`, `Overlays` | the modes of a profile (config.Profile) |
| Montage | `Transition` (catalogue id), `TransitionDuration`, `Filter`, `Effect`, `SubtitleStyle` | montage look |
| Montage | `CoverText`, `CoverFinal` | cover design; the rendered JPEG `images/cover-final-<n>.jpg` |
| Job | `Failures` | items a "done" job left failed («Повторити» redoes only those) |
| Job | `Blocked` | `"gaps"`: stopped before the montage — scenes failed, the user chooses (gaps.go) |
| Montage | `Gaps` | scenes the last render left out (`skipped`), showed as a still (`still`) or without its line (`silent`), with the exact reason |
| JobKind | `autopilot`, `translate`, `transcribe` | new job kinds |

## Threading model

- **Engine**: every method is safe for concurrent use (e.mu maps · per project st.mu model, st.wmu
  writes, st.fmu file writes vs Delete · nmu subscribers). `Subscribe` callbacks come from any
  goroutine, never under an engine lock, coalesced to ≤ ~8/s per project (125 ms): keep them cheap —
  mark dirty + request a frame, read the engine on the next frame.
- **Jobs**: one goroutine per job, one job per project (`ErrBusy`), projects run in parallel.
  `jobCtx.ctx` is cancelled by Cancel (text calls, downloads, ffmpeg stop at once); `jobCtx.work` is
  not — paid generations already submitted finish and are kept; it ends with Delete or Shutdown.
  Regen runs one item on its own goroutine outside the job (it cannot be cancelled). Material looks: a
  goroutine each; whisper.cpp: one run at a time; TTS keys: per-key slots, state file ≤ 1 write/s.
- **Telegram bot** (tgbot): a supervisor goroutine (config changes kick it) runs at most one poller; the poller
  authorises and hands updates to one worker goroutine per allowed chat (that chat's session is the worker's); a
  refusals goroutine; the live loop (engine `Subscribe` marks it dirty, a 1 s tick); deliveries, status checks and
  albums on their own goroutines (one video delivery at a time). Engine hooks never block (JobEnded queues).
- **Saves and settings**: debounced 400 ms per project, retried after 2 s on failure.
  `config.Store.Update` persists and calls subscribers synchronously: the engine re-locates FFmpeg and
  switches data folders there (serialised; still-running projects of the old folder stay "detached"
  until they end). `Shutdown(timeout)`: nothing new starts, everything is cancelled, waits, Flush.
- **UI**: Gio's `app.Main` owns the main goroutine; one UI goroutine runs the window loop. Every frame
  `Shell.Layout`: posted funcs → `Ctx.Refresh` → rail + page inside `th.Root` → shortcuts →
  `EndFrame`. `Host.Post` (any goroutine → next frame),
  `Host.Background` (new goroutine, panics logged, headless renders wait for it), `Invalidate` (any
  goroutine). `ProjectView` is the frame's snapshot (a deep copy re-read only after a notification);
  `queue.Feed` caches `Engine.Queue()` alike.
- **Images**: `gui.ImageCache` decodes off the UI goroutine (3 workers, > 16 MP one at a time, > 50 MP
  refused) into 240/480/720/960 px buckets keyed on path + `Asset.UpdatedAt`, LRU within 160 MB; the
  old version shows while the new one decodes (headless: synchronous). **Media**: player control
  (spawning ffmpeg, opening the sound device, Close) runs in order on one serial background goroutine
  (`core.runMedia`), never on the UI goroutine; one picture moves at a time; a player not laid out in
  a frame closes at `EndFrame`; route changes call `StopMedia`.
- **UI edits**: the settings page writes through one background goroutine in order (settings/store.go,
  optimistic mirror); project text fields commit after a 600 ms pause or on blur (project/bound.go);
  `Workspace.FlushEdits` runs before the window closes.
- **Close/crash**: closing while jobs run asks first; then
  `Engine.Shutdown(5 s)` (jobs record "stopped") and exit. A UI panic flushes the saves and shows a
  message; `debug.SetCrashOutput` sends any goroutine's crash to studio.log (`<settings dir>/studio.log`,
  truncated at start).

## UI architecture

```
internal/gui (kit)  →  appui/core (contract)  →  screen packages  →  appui (shell, main.go)
```

- **gui** — the kit, Gio only; every widget and token is listed in the KIT GUIDE (theme.go).
  `LightPalette` / `DarkPalette` ("Вечір") crossfade in 250 ms, `ModeSystem` follows the Windows app
  theme; Fira Sans type scale; widgets are value styles + state structs you keep; `th.Root` draws the
  overlay layers (dialogs, drawers, popovers, toasts, tooltip, tour — `th.Layer`, `gui.Anchor`);
  motion runs on `gtx.Now` (`SetReducedMotion` = instant); `Snapshot` renders on a fixed clock.
- **core** — `Ctx {Host; E; Th; Img; Audio; Offline; LogPath}`: Refresh/EndFrame, List, Project, UI,
  Run, Regen, Edit, Do, PickFile/PickFolder/PickSave, Reveal, OpenPath, Voices, Toast/Error,
  PlayRange, StopMedia. `Host` {Navigate, Current, Back, Post, Background, Invalidate, OpenSheet,
  Confirm, OpenQueue, StartTour}. `Route {Page, ProjectID, Step, Section, Scene}`; `Sheet {Kind,
  ProjectID, ProfileID, FromProject, Text, Done}` (new project | translate | own voice | profile |
  profiles); `Confirm`. `ProjectView` (frame snapshot, `Ver`), `ProjectUI`. `StepView {Layout, Side,
  Next}`, optional `Bleeder {Bleed()}`, `Next` (next-step card). `Video`/`VideoStyle` (inline player
  card: opens on first Play, closes when not laid out). `AssetState`/`Pill`, `ErrorText`, formatters
  (`Clock`, `Duration`, `Count`, `Updated`, `JobLabel` …). Headless: `HeadlessHost`, `NewHeadless`,
  `Frame`, `Shot`, `DemoEngine`.
- **Screens** (each receives `*core.Ctx`):

| Package | Owns |
|---|---|
| home | Головна (story box with voice typing — mic, live strip, interim text (mic.go), «Матеріали:» pictures for the new project, profile/language/duration chips, Автопілот / Покроково via `CreateProject`, quick actions, continue card, live queue, tip, projects grid + ⋯ menu; FFmpeg missing → a call-out with «Встановити автоматично», progress and «Скасувати»); Проєкти (search, filters, sort, service chips) |
| queue | Черга: `Feed`, `View` — drawer body/footer and the full page: a card per running job with its scene tiles (≤ 24), single regenerations, «Що можна запустити» (projects waiting for a step) when idle, «Нещодавно» (7 days by day, «Показати ще»); «Повторити» (failed, stopped, partly failed), «Скасувати все» |
| project | `Workspace`: header card, job banner, step tabs, context column; steps 1 Сценарій (with «Розбір прикладу» of a remake — stages/progress/stop, the error + retry, or the thumbnail, facts and the editable breakdown with «Переписати сценарій» / «Подивитися ще раз»; the «Матеріали» card and the own-voice panel: recording, «Текст із таймкодами») and 2 План |
| produce | steps 3 Розкадровка and 4 Відео й озвучка (virtualised grids), `Lightbox` |
| montage | step 5: timeline editor (clips + voice lane: drag a line to move it, its edges to trim; inspector: the line's text, «Переозвучити» / «Нарізати знову», «Скинути»), sound, transition/effect/filter galleries, subtitles, cover studio, final video; «Експортувати в…» next to «Змонтувати відео» (menu of editors → drawer: editor, readiness, what to include, folder/ZIP, progress + «Зупинити», «Відкрити папку» + import steps + approximations; the project ⋯ menu opens it through `ProjectUI.Export`) |
| pickers | shared choosers: Style (real previews), Language, Voice (Cartesia / ElevenLabs: «Вставити ID голосу» → a dialog that looks the id up (`Engine.LookupVoice`), shows name · language · gender and keeps it in «Мої голоси за ID», listed with the account's voices — voicebyid.go), Choice, Duration, Aspect, ImageModel, VideoMode, Menu, Catalog (catalog.go: a drawer of presets — search, single-choice filter groups with live counts, on/off filters, popular first, 60 per page, source links; filters fold behind «Фільтри» under 520 dp) |
| settings | Налаштування: Підключення («Помічник підключення» first (assistant.go: paste anything → «Розібрати й перевірити» → found items with masked keys, a plan of checked steps with switches, notes → «Застосувати» after a backup, «Повернути як було»; Home's set-up call-out opens it), cards «Текст» (OpenAI-сумісний / Anthropic / Ollama / Azure / Google Gemini, address normalised and fixed by «Перевірити», «Авторизація» Авто/Bearer/x-api-key/api-key/Без ключа, Azure api-version, model list with search + free text; a warning + «Повернути OpenAI-сумісний» when Google has no key), «Кадри» and «Відео» — each its own provider selector (G-Labs / Google / «Своє API»), that provider's settings and «Перевірити» —, «G-Labs Studio» (the shared address + key, shown while either uses it), «Google AI Studio» (keys one by one or «Додати кілька», then every key checked; working / resting until … / rejected from the rotation); key lists of more than 3 keys fold into one summary with «Показати всі» → search, filter chips, a scrolling list of fixed height (keylist.go); a dropdown that closes swallows presses for 450 ms (guard.go: the second click of a double click no longer switches a selector under the list); the page polls config.Store.Refresh; G-Labs / Google / «Своє API» for frames and video — the universal preset adds the JSON description editor with live validation, «Шаблон…» and «Налаштувати з документації (ШІ)» (a draft until «Зберегти опис»), «Динамічна довжина сцен», «Google AI Ultra», TTS service with per-provider keys/voice + rotating key list (Lumean; «Власний сервер» with the JSON description panel of audiospec.go) («Додати кілька» bulk paste → check, «Видалити всі з помилкою»), STT incl. AssemblyAI, «Своє API» (a JSON description, audiospec.go) and «Локально» install, «Диктування» (service, own keys, language, microphone, silence stop, check), FFmpeg «Встановити автоматично» (engine state: progress, «Скасувати», «Відновити») / «Встановити вручну» (steps, official pages, commands to copy), «Сцен одночасно» up to the video service's limit with a warning above the recommended value), Профілі, Автопілот, Сповіщення («Telegram-бот», telegram.go: token, chat, test, events; «Керування через бота» — pairing code with «Відкрити @bot» / «Скопіювати команду», status, «Відключити», «Живий прогрес», allowed users + refused group members; «Свій Bot API сервер»; how to connect and what the bot can do), Вигляд, Дані (folder; «Перенести налаштування» — transfer.go: export sheet with section switches, password twice + strength or «Без пароля (небезпечно)», native save dialog; import sheet: native open dialog → file facts → password → sections with «Замінити» / «Об'єднати», opt-in path hints → result + «Відновити попередні»; web import) (`Route.Section` targets) |
| dialogs | global sheets: new project (+ materials, «Перезняти за прикладом»), translate, own voice-over (+ transcript, materials), remake after a YouTube example (link, fidelity, wish, length, language, profile, who watches), profile editor («Налаштувати з опису»), profiles manager |

- **appui** — `Run(e, notices...)` (1440×900 dp, min 480×560, maximised on small screens), `Fatal`,
  `LogPath`; `Shell` implements `core.Host`: rail (bottom bar under 900 dp; community link, theme
  toggle, big «+»), route transitions (`gui.Route`), queue drawer, confirm dialog, sheets, first-run
  tour (5 steps, `UI.TourDone`), shortcuts (Esc/Back, Ctrl+N), close protection. The window opens
  straight on Home. Run starts the Telegram remote control (`tgbot.New` + `Start`;
  `Closing` before `Engine.Shutdown`, `Stop(3 s)` after it). `Headless(e)` is the same window offline, without
  a bot (`Snapshot`, `SetDark`, `ShowTour` …).
- **Updates** (appui/update.go): Run wires `update.New(config.Dir())`, runs `update.Cleanup`, toasts
  «Готово: студію оновлено до версії …» after `--updated-from`, and checks 5 s after the start when
  `UI.AutoUpdate` is on and the last check is ≥ 6 h old (silent on errors; a newer version → the rail dot +
  one toast). Rail item «Оновлення» (from 680 dp of height; 580–680 dp only while an update waits); phones and
  short windows put the dot on «Налаштування», where Settings › Про студію has «Перевірити оновлення» and
  the «Перевіряти автоматично» switch. The sheet (`OpenUpdates`, a drawer): this vs the new version, the
  notes (`update.ParseNotes`), size, «Завантажити й встановити» → progress + «Скасувати» → «Перезапустити
  зараз» (asks while jobs run) → `Apply`, then the window closes the normal way (engine Shutdown + flush)
  and Run calls the `Restart`; «Відкрити сторінку релізу», «Перевірити ще раз». Headless shells have no
  updater (never GitHub): `SetUpdater(update.Fake)` in tests, `ShowUpdate` for guishots.
- Screen rules: change the model only through the engine (`c.Edit`, `c.Run`, `c.Regen`, `c.Do`); an
  Offline ctx never calls services, OS dialogs or players (`Run`/`Regen` refuse: "Демо-режим");
  every interactive element has hover/press/focus/disabled states; redoing paid work asks first.

Headless screenshots (no window, no network):
- `go run ./cmd/guishots [-out dir] [-only names] [-sizes 1920x1080,1440x900,390x844] [-themes
  light,dark] [-jobs=false] [-web root]` — the whole window, every screen and state (default out
  `screenshots/new`); local mock servers produce the running/error states.
- `go run ./cmd/guikit -out guikit-out [-pages …]` — the kit gallery; `GUISHOTS=<dir>
  [GUISHOTS_ONLY=…] go test ./internal/appui/<pkg> -run TestShots` — per-screen states.
- `core.DemoEngine(tempDir, webRoot)`: temp data folder, in-memory settings (hence in-memory
  profiles), fake keys, the web demo projects imported read-only — never the user's data or settings.
- Agents MUST look at the PNGs of the screens they changed before finishing.

## Testing rules

- **Mocks only, never real services.** Service tests use `httptest` servers; the studio harness
  replaces `http.DefaultTransport` with a guard that blocks every host except this binary's mocks
  (api.elevenlabs.io is routed to the ElevenLabs mock). No real keys in tests. stt and tts guard
  too (local STT: the test binary as a fake `whisper-cli`).
- **Temp dirs.** Data folders come from `t.TempDir()`/`os.MkdirTemp`; `TestMain` in config and studio
  redirects `AppData`/`XDG_CONFIG_HOME`/`HOME` and refuses to run when the settings path is not redirected.
- **In-memory config.** `config.NewStoreWith` (never writes), `NewMemoryProfileStore` /
  `ProfileStoreFor`; UI tests use `core.NewHeadless`, `appui.Headless`, `core.DemoEngine`.
- FFmpeg-dependent tests skip when ffmpeg is missing; heavy or visual renders are opt-in via env:
  `GUISHOTS`, `GUISHOTS_ONLY`, `COVER_SAMPLES`, `MEDIA_VISUAL_SRC` + `MEDIA_VISUAL_OUT`.
- Before finishing: `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`.

## Build

- `build.bat` (Windows): `CGO_ENABLED=0`; a missing `rsrc_windows_<arch>.syso` is regenerated with
  `go run github.com/tc-hib/go-winres@v0.3.3 simply --icon internal\assets\icon256.png …` (version
  1.0.0; skipped without internet); then `go build -trimpath -ldflags "-s -w -H windowsgui" -o YT-Cartell-Studio.exe .`.
- `build.sh`: the same under Git Bash/MSYS; on macOS/Linux natively `CGO_ENABLED=1 go build -trimpath
  -ldflags "-s -w" -o YT-Cartell-Studio .` (C compiler + Gio's dev packages — see the script header;
  file dialogs need zenity or kdialog).
- Version: both scripts pass `-X ytcartell/internal/version.Version=<argument, else version.go>` plus
  `BuildDate` and `Commit` (and `UPDATE_OWNER` / `UPDATE_REPO` → `update.Owner` / `update.Repo`); go-winres gets
  the same version when it regenerates the `.syso`. `release.ps1 -Repo` and `build-mac.sh --repo` also set
  `update.Owner` / `update.Repo` from the repository they publish to (forks update from their own releases).
- Release: `release.ps1 -Notes notes.md [-Version X.Y.Z] [-Title …] [-Draft] [-DryRun]` (Windows, in GO/)
  builds `YT-Cartell-Studio-<ver>-windows-x64.exe` into `%TEMP%\yt-cartell-release-<ver>`, writes / merges
  `SHA256SUMS.txt` and creates or updates the GitHub release `v<ver>` with `gh`; `bash build-mac.sh [--upload]`
  (on a Mac) builds arm64 + x64 (+ universal) `.app` bundles (Info.plist, .icns, ad-hoc signature), zips them
  with `ditto` and merges their lines into the release's `SHA256SUMS.txt` — guide: `docs/BUILD-MAC.md`.
- The `.syso` gives the exe its icon and version info; the window icon comes from the embedded PNG at
  run time (run_windows.go). README explains the requirements and first-run setup.
