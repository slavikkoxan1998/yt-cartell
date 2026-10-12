package studio

// Settings profiles: named presets of the step-1 creative settings and the
// montage defaults (stored by config.ProfileStore in profiles.json next to
// config.json). Subscribers are notified with projectID "" when they change.

import (
	"strings"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
)

// Profiles lists every settings profile the user can pick (creation order).
func (e *Engine) Profiles() []config.Profile { return e.profiles.List() }

// Profile finds one settings profile.
func (e *Engine) Profile(id string) (config.Profile, bool) { return e.profiles.Get(id) }

// SaveProfile creates a profile (p.ID == "") or updates the one with p.ID and
// returns it as stored (normalized, with id and timestamps).
func (e *Engine) SaveProfile(p config.Profile) (config.Profile, error) {
	if p.Camera != "" && !knownCamera(p.Camera) {
		p.Camera = "" // keeps the project's camera when applied
	}
	// Montage look: unknown catalog ids fall back to the defaults.
	if _, ok := media.TransitionByID(p.Transition); !ok {
		p.Transition = media.DefaultTransition
	}
	if _, ok := media.FilterByID(p.Filter); !ok {
		p.Filter = ""
	}
	if _, ok := media.EffectByID(p.Effect); !ok {
		p.Effect = ""
	}
	if _, ok := media.SubtitlePresetByID(p.SubtitlePreset); !ok {
		p.SubtitlePreset = ""
	}
	if _, ok := NarratorStyleByID(p.NarratorStyle); !ok {
		p.NarratorStyle = ""
	}
	out, err := e.profiles.Save(p)
	if err != nil {
		return config.Profile{}, err
	}
	e.notify("")
	return out, nil
}

// DeleteProfile removes a profile. Projects made from it keep their settings.
func (e *Engine) DeleteProfile(id string) error {
	if err := e.profiles.Delete(id); err != nil {
		return err
	}
	e.notify("")
	return nil
}

// ApplyProfile copies a profile's creative settings (runtime, format, style,
// genre, tempo, camera, image model, voice, language, tone, notes, storyboard
// mode, frame chaining, sound and content modes, on-screen text) and montage
// defaults onto a project and records
// Project.ProfileID. The story text, mode, plan and generated files are never
// touched.
func (e *Engine) ApplyProfile(projectID, profileID string) error {
	pr, ok := e.profiles.Get(profileID)
	if !ok {
		return config.ErrProfileNotFound
	}
	return e.applyProfile(projectID, pr)
}

func (e *Engine) applyProfile(projectID string, pr config.Profile) error {
	return e.Edit(projectID, func(p *Project) {
		in, m := &p.Input, &p.Montage
		if pr.TargetSeconds > 0 {
			in.TargetSeconds = pr.TargetSeconds
		}
		if pr.Aspect != "" {
			in.Aspect = pr.Aspect
		}
		if pr.Style != "" {
			in.Style = pr.Style
		}
		in.CustomStyle = pr.CustomStyle // "" lets the chosen style show
		in.Genre = pr.Genre
		if pr.Tempo != "" {
			in.Tempo = pr.Tempo
		}
		if knownCamera(pr.Camera) {
			in.Camera = pr.Camera
		}
		if pr.ImageModel != "" {
			in.ImageModel = pr.ImageModel
		}
		in.Voiceover = pr.Voiceover
		if pr.NarratorVoiceID != "" { // "" = keep the project's (or automatic) voice
			in.NarratorVoiceID = pr.NarratorVoiceID
		}
		in.NarratorStyle = pr.NarratorStyle
		in.Language = pr.Language
		in.Tone = pr.Tone
		if strings.TrimSpace(pr.Notes) != "" { // "" = keep the notes written for this story
			in.Notes = pr.Notes
		}
		in.StoryboardMode = pr.StoryboardMode
		in.ChainFrames = pr.ChainFrames
		in.AudioMode, in.Content, in.Overlays = pr.AudioMode, pr.Content, pr.Overlays
		in.Pipeline = pr.Pipeline
		in.CodeGrammar = cleanGrammarID(pr.CodeGrammar)
		if pr.Transition != "" {
			m.Transition = pr.Transition
		}
		if pr.TransitionDuration > 0 {
			m.TransitionDuration = pr.TransitionDuration
		}
		m.Filter = pr.Filter
		m.Effect = pr.Effect
		m.SubtitleStyle.Preset = pr.SubtitlePreset
		if pr.Subtitles != "" {
			m.Subtitles = pr.Subtitles
		}
		m.MusicVolume = pr.MusicVolume
		m.AmbientVolume = pr.AmbientVolume
		m.Ducking = pr.Ducking
		p.ProfileID = pr.ID
	})
}

// ProfileFromProject saves the creative settings and montage defaults of a
// project as a new profile (name "" = the project's title).
func (e *Engine) ProfileFromProject(projectID, name string) (config.Profile, error) {
	p, ok := e.Get(projectID)
	if !ok {
		return config.Profile{}, ErrNotFound
	}
	if strings.TrimSpace(name) == "" {
		name = p.Title
	}
	in, m := p.Input, p.Montage
	return e.SaveProfile(config.Profile{
		Name:               name,
		TargetSeconds:      in.TargetSeconds,
		Aspect:             in.Aspect,
		Style:              in.Style,
		CustomStyle:        in.CustomStyle,
		Genre:              in.Genre,
		Tempo:              in.Tempo,
		Camera:             in.Camera,
		ImageModel:         in.ImageModel,
		Voiceover:          in.Voiceover,
		NarratorVoiceID:    in.NarratorVoiceID,
		NarratorStyle:      in.NarratorStyle,
		Language:           in.Language,
		Tone:               in.Tone,
		Notes:              in.Notes,
		StoryboardMode:     in.StoryboardMode,
		ChainFrames:        in.ChainFrames,
		AudioMode:          in.AudioMode,
		Content:            in.Content,
		Overlays:           in.Overlays,
		Pipeline:           in.Pipeline,
		CodeGrammar:        in.CodeGrammar,
		Transition:         m.Transition,
		TransitionDuration: m.TransitionDuration,
		Filter:             m.Filter,
		Effect:             m.Effect,
		SubtitlePreset:     m.SubtitleStyle.Preset,
		Subtitles:          m.Subtitles,
		MusicVolume:        m.MusicVolume,
		AmbientVolume:      m.AmbientVolume,
		Ducking:            m.Ducking,
	})
}

// CreateWithProfile makes a new project with a profile applied (profileID ""
// = the app defaults, like Create).
func (e *Engine) CreateWithProfile(title, profileID string) (Project, error) {
	var pr config.Profile
	if profileID != "" {
		var ok bool
		if pr, ok = e.profiles.Get(profileID); !ok {
			return Project{}, config.ErrProfileNotFound
		}
	}
	p, err := e.Create(title)
	if err != nil || profileID == "" {
		return p, err
	}
	if err := e.applyProfile(p.ID, pr); err != nil {
		return p, err
	}
	q, ok := e.Get(p.ID)
	if !ok {
		return p, ErrNotFound
	}
	return q, nil
}
