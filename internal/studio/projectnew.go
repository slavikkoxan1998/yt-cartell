package studio

// CreateProject: one call for the "new project" flows of Home and step 1
// (idea / script / own voice, start materials, optional autopilot). It
// replaces the UI's multi-call sequences; StartAutopilot, CreateFromVoice and
// StartVoiceAutopilot stay for compatibility.

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// NewProjectOptions describe a new project. Set fields override the
// profile's settings; zero values keep them.
type NewProjectOptions struct {
	Title     string    // "" lets the plan name the film
	Text      string    // story or script (ignored in ModeVoice: the recording is transcribed)
	ProfileID string    // "" = app defaults (with Autopilot: the default autopilot profile, see NoProfile)
	Language  string    // ISO 639-1 audience language ("" = keep the profile's, "auto" = auto; see LanguageSet)
	Aspect    string    // "16:9" | "9:16" ("" = keep)
	Mode      StoryMode // "" = story, or voice when VoiceFile is set

	// NoProfile: the app defaults, no profile at all — not even the default
	// autopilot profile (the user picked «Типові налаштування»). ProfileID
	// is ignored then.
	NoProfile bool
	// LanguageSet applies Language even when it is "" = auto (the user picked
	// «Мова: авто» over a profile's language). Language "auto" does the same.
	LanguageSet bool

	TargetSeconds int // 0 = keep

	// AudioMode / Content: the sound and content modes (Input.AudioMode,
	// Input.Content; "" = keep the profile's). A recording (ModeVoice) is
	// always the voice-over.
	AudioMode, Content string
	// Overlays: animated on-screen text "" (keep / auto) | "on" | "off".
	Overlays string
	// Pipeline («Режим виробництва», Input.Pipeline) applies when PipelineSet
	// (PipelineFull is "", so "" alone keeps the profile's).
	Pipeline    string
	PipelineSet bool
	// CodeGrammar: the animation grammar of a «Анімація кодом» film
	// (Input.CodeGrammar; "" = keep the profile's / auto).
	CodeGrammar string

	VoiceFile      string // absolute path of the user's own voice-over (ModeVoice)
	TranscriptFile string // absolute path of its transcript with timecodes (optional)

	Materials []MaterialInput
	Autopilot bool // start the autopilot right away (ModeVoice needs no text)
}

// CreateProject creates a project from opt: the profile applied, the options
// on top, the materials copied (described in the background), the recording
// and its transcript attached, and the autopilot started when asked. Inputs
// are checked first; when a step fails the new project is removed again.
// Only a failure to START the autopilot returns the created project with the
// error.
func (e *Engine) CreateProject(opt NewProjectOptions) (Project, error) {
	if e.closing.Load() {
		return Project{}, ErrClosing
	}
	voice := strings.TrimSpace(opt.VoiceFile)
	mode := opt.Mode
	if mode == "" {
		mode = ModeStory
		if voice != "" {
			mode = ModeVoice
		}
	}
	text := strings.TrimSpace(opt.Text)
	switch {
	case mode != ModeStory && mode != ModeScript && mode != ModeVoice:
		return Project{}, fmt.Errorf("Невідомий режим «%s»", mode)
	case mode == ModeVoice && voice == "":
		return Project{}, errNoVoice
	case strings.TrimSpace(opt.TranscriptFile) != "" && voice == "":
		return Project{}, errors.New("Транскрипт додається разом із файлом озвучки")
	case opt.Autopilot && mode != ModeVoice && text == "":
		return Project{}, errors.New("Спершу напиши історію або сценарій")
	case opt.Aspect != "" && !oneOf(opt.Aspect, "16:9", "9:16"):
		return Project{}, fmt.Errorf("Невідомий формат кадру «%s»", opt.Aspect)
	case len(opt.Materials) > maxMaterials:
		return Project{}, fmt.Errorf("Забагато матеріалів (максимум %d)", maxMaterials)
	case !oneOf(opt.AudioMode, "", AudioVoiceover, AudioInVideo, AudioSilent):
		return Project{}, fmt.Errorf("Невідомий режим звуку «%s»", opt.AudioMode)
	case !oneOf(opt.Content, "", ContentCharacters, ContentNoPeople, ContentMotion, ContentMixed):
		return Project{}, fmt.Errorf("Невідомий вміст кадру «%s»", opt.Content)
	case !oneOf(opt.Overlays, "", "on", "off"):
		return Project{}, fmt.Errorf("Невідоме значення тексту в кадрі «%s»", opt.Overlays)
	case !ValidPipeline(opt.Pipeline):
		return Project{}, fmt.Errorf("Невідомий режим виробництва «%s»", opt.Pipeline)
	}
	lang, setLang := strings.TrimSpace(opt.Language), opt.LanguageSet
	if strings.EqualFold(lang, "auto") {
		lang, setLang = "", true
	}
	if lang != "" {
		setLang = true
		l, ok := LanguageByCode(lang)
		if !ok {
			return Project{}, fmt.Errorf("Невідома мова «%s»", strings.TrimSpace(opt.Language))
		}
		lang = l.Code
	}
	for _, m := range opt.Materials {
		if _, err := materialKind(m.Kind); err != nil {
			return Project{}, err
		}
		if _, _, err := readPicture(m.Path); err != nil {
			return Project{}, fmt.Errorf("%s: %w", filepath.Base(m.Path), err)
		}
	}
	pid := strings.TrimSpace(opt.ProfileID)
	if opt.NoProfile {
		pid = ""
	} else if pid == "" && opt.Autopilot {
		pid = strings.TrimSpace(e.cfg.Get().Autopilot.DefaultProfileID)
		if _, ok := e.profiles.Get(pid); !ok {
			pid = ""
		}
	}

	p, err := e.CreateWithProfile(opt.Title, pid)
	if err != nil {
		if p.ID != "" {
			_ = e.Delete(p.ID)
		}
		return Project{}, err
	}
	fail := func(err error) (Project, error) {
		_ = e.Delete(p.ID)
		return Project{}, err
	}
	if err := e.Edit(p.ID, func(q *Project) {
		in := &q.Input
		in.Mode = mode
		if mode != ModeVoice {
			in.Text = text
		}
		if setLang {
			in.Language = lang
		}
		if opt.Aspect != "" {
			in.Aspect = opt.Aspect
		}
		if opt.TargetSeconds > 0 {
			in.TargetSeconds = opt.TargetSeconds
		}
		if opt.AudioMode != "" && mode != ModeVoice {
			in.AudioMode = opt.AudioMode
		}
		if opt.PipelineSet || opt.Pipeline != "" {
			in.Pipeline = opt.Pipeline
		}
		if g := cleanGrammarID(opt.CodeGrammar); g != "" {
			in.CodeGrammar = g
		}
		if opt.Content != "" {
			in.Content, in.Overlays = opt.Content, opt.Overlays
		} else if opt.Overlays != "" {
			in.Overlays = opt.Overlays
		}
	}); err != nil {
		return fail(err)
	}
	for _, m := range opt.Materials {
		if _, err := e.AddMaterial(p.ID, m); err != nil {
			return fail(fmt.Errorf("%s: %w", filepath.Base(m.Path), err))
		}
	}
	if voice != "" {
		if err := e.SetVoiceFile(p.ID, voice); err != nil {
			return fail(err)
		}
	}
	if tf := strings.TrimSpace(opt.TranscriptFile); tf != "" {
		if err := e.SetTranscriptFile(p.ID, tf); err != nil {
			return fail(err)
		}
	}
	if opt.Autopilot {
		if err := e.startAutopilotJob(p.ID); err != nil {
			q, _ := e.Get(p.ID)
			return q, err
		}
	}
	q, ok := e.Get(p.ID)
	if !ok {
		return Project{}, ErrNotFound
	}
	return q, nil
}

// startAutopilotJob runs the autopilot on a new project: a story/script
// needs its text, a voice-over its recording (the plan transcribes it).
func (e *Engine) startAutopilotJob(id string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	return e.startJobKind(st, JobAutopilot, func(p *Project) error {
		if p.Input.Mode == ModeVoice {
			if p.Input.VoiceFile == "" {
				return errNoVoice
			}
			return nil
		}
		if strings.TrimSpace(p.Input.Text) == "" {
			return errors.New("Спершу напиши історію або сценарій")
		}
		return nil
	}, func(j *jobCtx) (string, error) { return e.runAutopilot(j, false, "") })
}
