// Package studio is the production engine: projects, the five-step pipeline
// (script → plan → storyboard → render → montage), jobs and persistence.
//
// A project moves through five steps:
//
//	1 script     — user text + settings (target duration, format, style, voice)
//	2 plan       — LLM writes characters, locations and per-scene prompts (editable)
//	3 storyboard — reference images for characters/locations, then scene frames
//	4 render     — Veo clip + voiceover per scene
//	5 montage    — final assembly (voice sync, music, subtitles, cover) → mp4
//
// Every file lives inside the project folder <DataDir>/projects/<id>/.
// Asset.Path is RELATIVE to that folder (portable if the data folder moves).
package studio

import "strings"

// AssetStatus of a generated file.
type AssetStatus string

const (
	StatusEmpty   AssetStatus = "empty"
	StatusQueued  AssetStatus = "queued"
	StatusRunning AssetStatus = "running"
	StatusDone    AssetStatus = "done"
	StatusError   AssetStatus = "error"
)

// Asset is one generated (or user-supplied) file plus its generation state.
type Asset struct {
	Path     string      `json:"path,omitempty"` // relative to the project folder, forward slashes
	Status   AssetStatus `json:"status"`
	Error    string      `json:"error,omitempty"`
	Progress int         `json:"progress,omitempty"`
	Duration float64     `json:"duration,omitempty"` // seconds (audio/video)
	// UpdatedAt (unix ms) changes whenever the file is replaced — use it to bust image caches.
	UpdatedAt int64 `json:"updatedAt,omitempty"`
	// Stale: the source prompt/text was edited after this asset was made.
	Stale bool `json:"stale,omitempty"`
	// Rewrite: the prompt was refused for its content and rewritten
	// automatically before this file was made (rewrite.go); nil = the
	// studio's own prompt.
	Rewrite *PromptRewrite `json:"rewrite,omitempty"`
}

// Character is a recurring cast member. Description is the ONLY source of looks.
type Character struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Tag         string `json:"tag"` // latin handle, used as @tag in prompts and as the reference file name
	Role        string `json:"role"`
	Description string `json:"description"` // English visual description
	Gender      string `json:"gender,omitempty"`
	VoiceID     string `json:"voiceId,omitempty"`
	Image       Asset  `json:"image"`
	// MaterialID binds the character to a start material (Input.Materials):
	// its picture is the reference image, its look the description. Kept
	// across re-plans; "" = planned by the model alone.
	MaterialID string `json:"materialId,omitempty"`
	// Kind: "" = a person or creature of the cast; KindObject = a recurring
	// object, product or visual motif (films without people, motion
	// graphics) — its reference is an object shot, never a person.
	Kind string `json:"kind,omitempty"`
	// Voice is how the character sounds when the video model voices them
	// (AudioInVideo): English, age, timbre, pitch, accent, pace. It is
	// repeated in every clip prompt so the voice stays the same.
	Voice string `json:"voice,omitempty"`
	// Age is the character's age group for picking a TTS voice in a
	// multi-voice voice-over: AgeChild | AgeYoung | AgeAdult | AgeOld ("" =
	// unknown).
	Age string `json:"age,omitempty"`
}

// Age groups of a character (Character.Age).
const (
	AgeChild = "child"
	AgeYoung = "young"
	AgeAdult = "adult"
	AgeOld   = "old"
)

// KindObject is the Character.Kind of a recurring object or motif.
const KindObject = "object"

// IsObject reports a cast entry that is an object, not a person.
func (c Character) IsObject() bool { return c.Kind == KindObject }

// Line is one spoken line of a scene in AudioInVideo films: the video model
// speaks it on camera with the speaker's voice (Character.Voice).
type Line struct {
	Speaker  string `json:"speaker"`            // a character id, or "narrator" = an off-screen voice
	Text     string `json:"text"`               // the words, in the audience language
	Delivery string `json:"delivery,omitempty"` // how it is said, English ("whispers, scared")
	// Start/End (s) place a multi-voice part (Scene.Parts) inside the scene's
	// voice audio; set by the voice step (0, 0 = not voiced yet).
	Start float64 `json:"start,omitempty"`
	End   float64 `json:"end,omitempty"`
}

// OnScreen is the animated text the montage draws over a scene (motion
// graphics, explainers, key facts). The studio renders it — never the image
// or video model.
type OnScreen struct {
	Title   string `json:"title,omitempty"`   // a short headline
	Value   string `json:"value,omitempty"`   // a key number or fact: "73%", "1 200 км", "1914"
	Caption string `json:"caption,omitempty"` // a short supporting line
	// Anim: OverlayFade | OverlaySlide | OverlayPop | OverlayCount |
	// OverlayType ("" = a counter for a number, else a fade).
	Anim string `json:"anim,omitempty"`
	// Position: "center" | "lower" | "top" ("" = centre in motion graphics,
	// a lower third elsewhere).
	Position string `json:"position,omitempty"`
}

// Empty reports an OnScreen without any text.
func (o OnScreen) Empty() bool {
	return strings.TrimSpace(o.Title) == "" && strings.TrimSpace(o.Value) == "" && strings.TrimSpace(o.Caption) == ""
}

// OnScreen animations.
const (
	OverlayFade  = "fade"
	OverlaySlide = "slide"
	OverlayPop   = "pop"
	OverlayCount = "count" // the number counts up
	OverlayType  = "type"  // kinetic typography: the words appear one by one
)

// Location is a distinct place, drawn once as a reference.
type Location struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Tag         string `json:"tag"`
	Description string `json:"description"`
	Image       Asset  `json:"image"`
	// MaterialID binds the location to a start material (like Character.MaterialID).
	MaterialID string `json:"materialId,omitempty"`
}

// Scene is one clip of the film.
type Scene struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Speaker      string   `json:"speaker"`   // "narrator" or a character id
	Narration    string   `json:"narration"` // voiceover / dialogue line (story language)
	CharacterIDs []string `json:"characterIds"`
	LocationID   string   `json:"locationId,omitempty"`
	Shot         string   `json:"shot"`
	ImagePrompt  string   `json:"imagePrompt"`  // still first frame (English), cast referenced as @tag
	MotionPrompt string   `json:"motionPrompt"` // camera + action for the video model (English)
	Frame        Asset    `json:"frame"`
	// Board is the 3×3 storyboard sheet (only when Input.StoryboardMode == "grid3x3").
	Board Asset `json:"board"`
	Video Asset `json:"video"`
	Audio Asset `json:"audio"`
	// Timeline controls (montage editor). TrimIn/TrimOut cut seconds from the
	// start/end of the clip; Transition is the transition INTO the next scene
	// ("" = Montage.Transition); Effect is the motion effect used when the scene
	// is shown as a still ("" = Montage.Effect).
	TrimIn     float64 `json:"trimIn,omitempty"`
	TrimOut    float64 `json:"trimOut,omitempty"`
	Transition string  `json:"transition,omitempty"`
	Effect     string  `json:"effect,omitempty"`
	// AudioSource: "" = generated TTS; "upload" = a slice of the user's own
	// voiceover (Input.VoiceFile), cut to this scene's timing.
	AudioSource string `json:"audioSource,omitempty"`
	// VoiceStart/VoiceEnd: this scene's time range inside Input.VoiceFile (seconds).
	VoiceStart float64 `json:"voiceStart,omitempty"`
	VoiceEnd   float64 `json:"voiceEnd,omitempty"`
	// Voice line placement in the montage editor: VoiceShift moves the line
	// later (+) or earlier (−) than its default start (seconds); VoiceTrimIn /
	// VoiceTrimOut cut seconds from the start / end of the voice audio.
	VoiceShift   float64 `json:"voiceShift,omitempty"`
	VoiceTrimIn  float64 `json:"voiceTrimIn,omitempty"`
	VoiceTrimOut float64 `json:"voiceTrimOut,omitempty"`
	// ClipLen is the clip length (s) chosen for this scene when dynamic clip
	// lengths are on (0 = Settings → video length).
	ClipLen int `json:"clipLen,omitempty"`
	// Lines are what is said on camera in an AudioInVideo film (Narration
	// stays empty there): the clip prompt carries them, the video model
	// speaks them, the subtitles show them.
	Lines []Line `json:"lines,omitempty"`
	// OnScreen is the animated text over this scene (Input.TextOverlays).
	OnScreen OnScreen `json:"onScreen,omitzero"`
	// Parts split the voice-over of a multi-voice film (Input.MultiVoice)
	// between its speakers: Speaker = "narrator", "narrator2", "narrator3"
	// or a character id. Narration always holds their words joined (what
	// older code, subtitles and exports read); the voice step speaks each
	// part with its speaker's voice and stores where it sits (Line.Start/End).
	Parts []Line `json:"parts,omitempty"`
	// Code is the scene's code-drawn animation in a «Анімація кодом» film
	// (Input.Pipeline PipelineCode): the spec, its pictures and preview
	// (codeanim.go). The rendered clip itself is Video.
	Code CodeClip `json:"code,omitzero"`
}

// StoryMode: "story" (AI writes the voiceover) or "script" (voice verbatim).
type StoryMode string

const (
	ModeStory  StoryMode = "story"
	ModeScript StoryMode = "script"
	// ModeVoice: the user's own voiceover drives the film — the audio is
	// transcribed and split into scenes timed to the speech (Input.VoiceFile).
	ModeVoice StoryMode = "voice"
)

// Input is everything the user sets on step 1.
type Input struct {
	Text            string    `json:"text"`
	Mode            StoryMode `json:"mode"`
	TargetSeconds   int       `json:"targetSeconds"`
	Aspect          string    `json:"aspect"` // "16:9" | "9:16"
	Style           string    `json:"style"`  // built-in style id
	CustomStyle     string    `json:"customStyle"`
	Genre           string    `json:"genre"`
	Tempo           string    `json:"tempo"`      // slow | balanced | fast | rapid (stills: 3 s scenes, tight lines)
	Camera          string    `json:"camera"`     // cinematic | medium | wide | dynamic
	ImageModel      string    `json:"imageModel"` // NARWHAL | GEM_PIX_2 | GEM_PIX
	Voiceover       bool      `json:"voiceover"`
	NarratorVoiceID string    `json:"narratorVoiceId"`
	// NarratorStyle: the kind of narrator voice the plan picks while
	// NarratorVoiceID is "" (NarratorStyles id, "" = any).
	NarratorStyle string `json:"narratorStyle,omitempty"`
	Notes         string `json:"notes"`
	// Language of everything the AI writes for the audience (narration, title,
	// subtitles, voice): "" = auto (same as the user's text), else an ISO 639-1
	// code from the Languages catalog, e.g. "en". The user may write in Ukrainian
	// and still get an English film.
	Language string `json:"language,omitempty"`
	// Tone of the narration (catalog id, "" = neutral storyteller).
	Tone string `json:"tone,omitempty"`
	// StoryboardMode: "" = one first frame per scene (default);
	// "grid3x3" = a 3×3 storyboard sheet per scene used as the reference for
	// the video model (Omni Flash components mode). Frame = panel 1 of the grid.
	StoryboardMode string `json:"storyboardMode,omitempty"`
	// ChainFrames: pass the last frame of the previous scene's clip as an extra
	// reference to the video model (continuity). Forces sequential rendering.
	ChainFrames bool `json:"chainFrames,omitempty"`
	// VoiceFile (relative to the project folder) is the user's own narration for
	// ModeVoice; VoiceFileName is its original name for display.
	VoiceFile     string `json:"voiceFile,omitempty"`
	VoiceFileName string `json:"voiceFileName,omitempty"`
	// TranscriptFile (relative) is a transcript with timecodes the user
	// attached to VoiceFile (SRT/VTT/JSON…): no speech recognition needed.
	TranscriptFile string `json:"transcriptFile,omitempty"`
	// Materials are the user's own references given up front (character
	// photos, locations, objects): the plan uses them and their images become
	// the reference pictures (no generation for those).
	Materials []Material `json:"materials,omitempty"`
	// AudioMode is how the film sounds: AudioVoiceover (a narrator read by
	// TTS — or the user's recording), AudioInVideo (the characters speak on
	// camera with the video model's own voices, no voice-over), AudioSilent
	// (music and ambience only). "" = older projects: Voiceover decides.
	// Voiceover always follows it (SoundMode is the effective mode).
	AudioMode string `json:"audioMode,omitempty"`
	// Content is what the frames show: ContentCharacters ("" = it),
	// ContentNoPeople (places, objects, nature — nobody in frame),
	// ContentMotion (motion graphics, infographics, kinetic type) or
	// ContentMixed.
	Content string `json:"content,omitempty"`
	// Overlays switches the animated on-screen text (Scene.OnScreen): "" =
	// auto (on for motion graphics), "on", "off".
	Overlays string `json:"overlays,omitempty"`
	// MultiVoice («Мультиголос», voice-over only): the plan splits every
	// scene's voice-over into parts (Scene.Parts) read by the narrator, extra
	// narrators and the characters who speak, each with their own TTS voice.
	MultiVoice bool `json:"multiVoice,omitempty"`
	// ExtraNarrators are the voice ids of «Диктор 2» and «Диктор 3»
	// (speakers "narrator2", "narrator3"); "" = picked automatically. Their
	// number is how many extra narrators the plan may use (at most 2).
	ExtraNarrators []string `json:"extraNarrators,omitempty"`
	// Pipeline («Режим виробництва») is which stages the film skips:
	// PipelineFull ("" — everything), PipelineNoRefs (no character/location
	// reference images: the prompts carry the looks in words),
	// PipelineStills (no video: every scene is its frame animated by the
	// montage) or PipelineNoRefsStills (both), PipelineCode («Анімація
	// кодом»: every scene is drawn by code — codeanim.go) or
	// PipelineNoRefsCode. See modes.go.
	Pipeline string `json:"pipeline,omitempty"`
	// CodeGrammar is the animation grammar of a «Анімація кодом» film: "" =
	// auto (the text model picks one per scene), else a codeanim grammar id
	// ("t3_finance_chart", "map" …). Scene.Code.Grammar overrides it.
	CodeGrammar string `json:"codeGrammar,omitempty"`
}

// Sound modes (Input.AudioMode).
const (
	AudioVoiceover = "voiceover"
	AudioInVideo   = "invideo"
	AudioSilent    = "silent"
)

// What the frames show (Input.Content).
const (
	ContentCharacters = "characters"
	ContentNoPeople   = "nopeople"
	ContentMotion     = "motion"
	ContentMixed      = "mixed"
)

// SoundMode is the effective sound mode: AudioMode, else (older projects and
// the user's own recording) the Voiceover switch.
func (in Input) SoundMode() string {
	if in.Mode != ModeVoice {
		switch in.AudioMode {
		case AudioVoiceover, AudioInVideo, AudioSilent:
			return in.AudioMode
		}
	}
	if in.Voiceover {
		return AudioVoiceover
	}
	return AudioSilent
}

// ContentMode is the effective content of the frames ("" = characters).
func (in Input) ContentMode() string {
	switch in.Content {
	case ContentNoPeople, ContentMotion, ContentMixed:
		return in.Content
	}
	return ContentCharacters
}

// NoPeople reports a film without people in frame (no people, or motion graphics).
func (in Input) NoPeople() bool {
	c := in.ContentMode()
	return c == ContentNoPeople || c == ContentMotion
}

// TextOverlays reports whether the montage draws the scenes' on-screen text
// (and the plan writes it): Overlays, else on for motion graphics.
func (in Input) TextOverlays() bool {
	switch in.Overlays {
	case "on":
		return true
	case "off":
		return false
	}
	return in.ContentMode() == ContentMotion
}

// Montage holds step-5 settings and outputs.
type Montage struct {
	Transition    string  `json:"transition"`          // media transition catalog id ("fade" = dip to black, "none" = hard cut)
	Subtitles     string  `json:"subtitles"`           // "none" | "srt" | "burn"
	MusicPath     string  `json:"musicPath,omitempty"` // relative to project folder
	MusicName     string  `json:"musicName,omitempty"`
	MusicVolume   float64 `json:"musicVolume"`
	Ducking       bool    `json:"ducking"`
	AmbientVolume float64 `json:"ambientVolume"`
	Output        Asset   `json:"output"`
	SRTPath       string  `json:"srtPath,omitempty"`
	Cover         Asset   `json:"cover"`
	CoverPrompt   string  `json:"coverPrompt,omitempty"`
	// Filter is a colour look id from the media filter catalog ("" = none).
	Filter string `json:"filter,omitempty"`
	// Effect is the default motion effect for stills ("" = gentle zoom-in).
	Effect string `json:"effect,omitempty"`
	// TransitionDuration in seconds for crossfade-type transitions (default 0.5).
	TransitionDuration float64       `json:"transitionDuration,omitempty"`
	SubtitleStyle      SubtitleStyle `json:"subtitleStyle"`
	// CoverText is the text overlay design; CoverFinal is Cover with the text rendered on it.
	CoverText  CoverText `json:"coverText"`
	CoverFinal Asset     `json:"coverFinal"`
	// Gaps are the scenes the last render left out or showed differently
	// (a frame instead of the clip, no voice line), each with the exact
	// reason (gaps.go); none = every scene as planned.
	Gaps []MontageGap `json:"gaps,omitempty"`
}

// SubtitleStyle for burnt-in subtitles.
type SubtitleStyle struct {
	Preset    string `json:"preset,omitempty"`   // catalog id ("" = classic white with outline)
	Size      int    `json:"size,omitempty"`     // font size at 1080p height (0 = auto)
	Position  string `json:"position,omitempty"` // "bottom" | "middle" | "top"
	Color     string `json:"color,omitempty"`    // #RRGGBB text colour
	Outline   bool   `json:"outline,omitempty"`
	Box       bool   `json:"box,omitempty"` // semi-transparent box behind the text
	Uppercase bool   `json:"uppercase,omitempty"`
	// SpeakerColors: multi-voice films colour each speaker's subtitles (the
	// narrator keeps Color).
	SpeakerColors bool `json:"speakerColors,omitempty"`
}

// CoverText is the YouTube thumbnail text overlay.
type CoverText struct {
	Template string `json:"template,omitempty"` // catalog id
	Title    string `json:"title,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
	Badge    string `json:"badge,omitempty"`    // small highlighted label, e.g. "НОВЕ"
	Font     string `json:"font,omitempty"`     // catalog font id
	Color    string `json:"color,omitempty"`    // #RRGGBB
	Stroke   string `json:"stroke,omitempty"`   // #RRGGBB outline colour ("" = none)
	Accent   string `json:"accent,omitempty"`   // #RRGGBB badge/band colour
	Position string `json:"position,omitempty"` // "top" | "center" | "bottom" | "left" | "right"
	Size     int    `json:"size,omitempty"`     // 0 = auto
}

// Material is a reference the user supplies before planning.
type Material struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`           // "character" | "location" | "object" | "style"
	Name string `json:"name"`           // e.g. "Тарас" (characters/locations keep it)
	Note string `json:"note,omitempty"` // the user's description (look, role, outfit…)
	Path string `json:"path"`           // image relative to the project folder
	// Look is the text model's description of the picture (filled by the engine).
	Look string `json:"look,omitempty"`
}

// JobKind names a background stage.
type JobKind string

const (
	JobPlan    JobKind = "plan"
	JobRefs    JobKind = "refs"
	JobFrames  JobKind = "frames"
	JobVideos  JobKind = "videos"
	JobVoices  JobKind = "voices"
	JobRender  JobKind = "render"
	JobMontage JobKind = "montage"
	JobCover   JobKind = "cover"
	// JobAutopilot runs plan → refs → frames → render → montage (→ cover) unattended.
	JobAutopilot JobKind = "autopilot"
)

// JobStatus of a background stage.
type JobStatus string

const (
	JobRunning   JobStatus = "running"
	JobDone      JobStatus = "done"
	JobError     JobStatus = "error"
	JobCancelled JobStatus = "cancelled"
)

// Job is the (single) running or last-finished background stage of a project.
type Job struct {
	Kind       JobKind   `json:"kind"`
	Status     JobStatus `json:"status"`
	Progress   int       `json:"progress"`
	Message    string    `json:"message"`
	StartedAt  int64     `json:"startedAt"`
	FinishedAt int64     `json:"finishedAt,omitempty"`
	Error      string    `json:"error,omitempty"`
	// Failures: a job that finished "done" but left this many of its items
	// failed (frames, clips, voices…); «Повторити» redoes only those.
	Failures int `json:"failures,omitempty"`
	// Blocked: the job stopped before the montage because scenes are not
	// ready (BlockedGaps, gaps.go) — the UI offers the montage choices.
	Blocked string `json:"blocked,omitempty"`
}

// Project is one video production.
type Project struct {
	ID         string      `json:"id"`
	Title      string      `json:"title"`
	Logline    string      `json:"logline"`
	StyleGuide string      `json:"styleGuide"`
	CreatedAt  int64       `json:"createdAt"`
	UpdatedAt  int64       `json:"updatedAt"`
	Step       int         `json:"step"` // furthest step reached (1–5)
	Input      Input       `json:"input"`
	Characters []Character `json:"characters"`
	Locations  []Location  `json:"locations"`
	Scenes     []Scene     `json:"scenes"`
	Montage    Montage     `json:"montage"`
	Job        *Job        `json:"job,omitempty"`
	// ProfileID of the settings profile the project was created from ("" = none).
	ProfileID string `json:"profileId,omitempty"`
	// SourceID: the project this one was translated from ("" = original).
	SourceID string `json:"sourceId,omitempty"`
	// NarratorVoice is the off-screen voice of an AudioInVideo film (lines
	// spoken by "narrator"), English — repeated in every clip prompt.
	NarratorVoice string `json:"narratorVoice,omitempty"`
	// Reference is the YouTube video a remake («Перезняти за прикладом»)
	// follows, with its editable breakdown (remake.go); zero = none.
	Reference Reference `json:"reference,omitzero"`
}

// Summary is the lightweight view used by the projects list.
type Summary struct {
	ID        string
	Title     string
	Step      int
	UpdatedAt int64
	Scenes    int
	Aspect    string
	ThumbPath string  // absolute path of a representative image, "" if none
	Running   JobKind // "" when idle
}

// DefaultInput for a new project.
func DefaultInput() Input {
	return Input{
		Mode:          ModeStory,
		TargetSeconds: 60,
		Aspect:        "16:9",
		Style:         "cinematic",
		Tempo:         "balanced",
		Camera:        "cinematic",
		ImageModel:    "NARWHAL",
		Voiceover:     true,
	}
}

// DefaultMontage for a new project.
func DefaultMontage() Montage {
	return Montage{
		Transition:    "fade",
		Subtitles:     "srt",
		MusicVolume:   0.18,
		Ducking:       true,
		AmbientVolume: 0.25,
		Output:        Asset{Status: StatusEmpty},
		Cover:         Asset{Status: StatusEmpty},
	}
}

// EmptyAsset is a fresh, never-generated asset.
func EmptyAsset() Asset { return Asset{Status: StatusEmpty} }
