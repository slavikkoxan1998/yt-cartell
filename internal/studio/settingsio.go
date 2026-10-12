package studio

// Settings transfer (Settings → Дані): export the settings into a .ytcs file
// and import one (config/export.go has the format, the crypto and the merge
// rules). An import saves a backup first, then changes the profiles and the
// settings — the settings in one config.Store.Update, so the engine and the
// shell react exactly as to a change made in Settings (FFmpeg re-located,
// data folder switched, key pools rebuilt from the new lists, theme
// applied). RestoreSettings puts a backup back.

import (
	"errors"
	"fmt"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
)

// ExportSettings builds the settings file opt describes (the key states come
// from the voice-over key pool). It never touches the disk; the caller writes
// the bytes (config.WriteExportFile).
func (e *Engine) ExportSettings(opt config.ExportOptions) ([]byte, error) {
	var states []byte
	if opt.KeyStates {
		var err error
		if states, err = e.speech.KeyStateJSON(); err != nil {
			return nil, err
		}
	}
	return config.Export(e.cfg.Get(), e.profiles.List(), states, opt)
}

// SettingsBundle is what an export with opt would carry (the export dialog
// shows its counts). Password is ignored.
func (e *Engine) SettingsBundle(opt config.ExportOptions) *config.Bundle {
	var states []byte
	if opt.KeyStates {
		states, _ = e.speech.KeyStateJSON()
	}
	return config.NewBundle(e.cfg.Get(), e.profiles.List(), states, opt)
}

// ImportSettings applies an opened settings file the way plan says. A backup
// of the current settings and profiles is saved first (nothing changes when
// it cannot be); then the profiles, then the settings in one Store.Update,
// then the key states.
func (e *Engine) ImportSettings(b *config.Bundle, plan config.ImportPlan) (config.ImportResult, error) {
	var res config.ImportResult
	if b == nil {
		return res, errors.New("Файл налаштувань не відкрито.")
	}
	if !plan.Any() {
		return res, errors.New("Вибери, що імпортувати.")
	}
	if e.closing.Load() {
		return res, ErrClosing
	}
	bk, err := e.cfg.SaveBackup(e.profiles.List(), time.Now())
	if err != nil {
		return res, fmt.Errorf("імпорт скасовано — не вдалося зберегти копію поточних налаштувань: %w", err)
	}
	res.Backup = bk

	var ids map[string]string
	if plan.Profiles.On && b.HasProfiles {
		in := make([]config.Profile, 0, len(b.Profiles))
		for _, p := range b.Profiles {
			in = append(in, checkImportedProfile(p))
		}
		var out []config.Profile
		if plan.Profiles.Merge() {
			out, ids, res.ProfilesAdded, res.ProfilesSame = config.MergeProfiles(e.profiles.List(), in)
		} else {
			out, ids = config.ReplaceProfiles(in)
		}
		if err := e.profiles.ReplaceAll(out); err != nil {
			return res, fmt.Errorf("імпорт скасовано — %w", err)
		}
		e.notify("")
	}
	final := e.profiles.List()
	res.ProfilesTotal = len(final)
	profileID := func(id string) string {
		if m, ok := ids[id]; ok {
			id = m
		}
		for _, p := range final {
			if p.ID == id {
				return id
			}
		}
		return ""
	}
	if err := e.cfg.Update(func(c *config.Config) {
		config.ApplyImport(c, b, plan, profileID)
	}); err != nil {
		res.Warnings = append(res.Warnings, "Налаштування застосовано, але не вдалося записати їх на диск: "+err.Error())
	}
	if plan.KeyStates.On && len(b.KeyStates) > 0 {
		n, err := e.speech.ImportKeyState(b.KeyStates, !plan.KeyStates.Merge())
		if err != nil {
			res.Warnings = append(res.Warnings, err.Error())
		}
		res.KeyStates = n
	}
	res.Warnings = append(res.Warnings, b.Warnings...)
	return res, nil
}

// SettingsBackups are the copies saved before imports, newest first.
func (e *Engine) SettingsBackups() []config.Backup { return e.cfg.Backups() }

// RestoreSettings puts a backup back: its profiles (when it kept them) and
// its settings (the update bookkeeping stays as it is now).
func (e *Engine) RestoreSettings(b config.Backup) error {
	if e.closing.Load() {
		return ErrClosing
	}
	if b.Profiles != nil {
		if err := e.profiles.ReplaceAll(b.Profiles); err != nil {
			return err
		}
		e.notify("")
	}
	return e.cfg.Update(func(c *config.Config) { config.ApplyBackup(c, b) })
}

// checkImportedProfile drops catalogue ids this version does not know, like
// SaveProfile does (a profile from a newer studio may use new ones).
func checkImportedProfile(p config.Profile) config.Profile {
	if p.Camera != "" && !knownCamera(p.Camera) {
		p.Camera = ""
	}
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
	return p
}
