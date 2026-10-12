package studio

// Scenes the montage cannot show as planned ("gaps"): no clip (it failed,
// was never shot or its file is gone), no frame either, or — in a voice-over
// film — no voice line. The montage never fills them in silently: Run
// refuses (MissingScenesError) until the user chooses what to do —
//
//	MissingSkip   «Змонтувати без них»     every scene with a gap is left out
//	MissingStill  «З кадром замість відео» a missing clip → its frame with a
//	              motion effect; a scene without a frame is left out; a missing
//	              voice line → the scene plays without it
//	MissingStop   «Зупинитись»             nothing is rendered
//
// — in the montage dialog, or once for all autopilot runs in Settings
// (config.Autopilot.OnFailed; unset = ask). What a render left out or
// replaced is listed in Project.Montage.Gaps, in the job message and in the
// Telegram notification, each scene with the exact error.

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
)

// What the montage does with scenes that are not ready (RunOptions.Missing,
// config.Autopilot.OnFailed).
const (
	MissingAsk   = config.FailedAsk   // refuse; the UI asks
	MissingSkip  = config.FailedSkip  // leave them out
	MissingStill = config.FailedStill // the frame instead of the clip
	MissingStop  = config.FailedStop  // refuse (an explicit choice)
)

// Gap actions (MontageGap.Action) of a finished render.
const (
	GapSkipped = "skipped" // the scene is not in the film
	GapStill   = "still"   // its frame instead of the clip
	GapSilent  = "silent"  // in the film without its voice line
)

// BlockedGaps is Job.Blocked of a job that stopped before the montage
// because scenes are not ready: the UI offers the montage choices.
const BlockedGaps = "gaps"

// MontageGap is one scene that is not ready for the montage.
type MontageGap struct {
	SceneID string `json:"sceneId"`
	Number  int    `json:"number"` // 1-based position in the film
	// What is missing: the clip, the frame too (only leaving the scene out
	// is possible), the voice line (voice-over films).
	NoClip  bool `json:"noClip,omitempty"`
	NoFrame bool `json:"noFrame,omitempty"`
	NoVoice bool `json:"noVoice,omitempty"`
	// Reason is why, with the services' exact errors ("кліп: G-Labs: …").
	Reason string `json:"reason,omitempty"`
	// Action is what the render did (GapSkipped | GapStill | GapSilent);
	// set only in Montage.Gaps.
	Action string `json:"action,omitempty"`
}

// Line is the gap as one line for the user: "Сцена 3 — кліп: G-Labs: …".
func (g MontageGap) Line() string {
	s := "Сцена " + strconv.Itoa(g.Number)
	switch g.Action {
	case GapSkipped:
		s += " (пропущена)"
	case GapStill:
		s += " (кадр замість відео)"
	case GapSilent:
		s += " (без голосу)"
	}
	if g.Reason != "" {
		s += " — " + g.Reason
	}
	return s
}

// MontageGaps lists the scenes of p that the montage cannot show as planned.
// dir is the project folder: files that are gone count as missing; "" checks
// the statuses only (cheap enough for the UI's frame loop).
func MontageGaps(p *Project, dir string) []MontageGap {
	has := func(a Asset) bool {
		if a.Path == "" {
			return false
		}
		return dir == "" || isFile(filepath.Join(dir, filepath.FromSlash(a.Path)))
	}
	var out []MontageGap
	for i := range p.Scenes {
		s := &p.Scenes[i]
		// «Тільки картинки» (and a code scene whose animation failed): the
		// frame IS the scene.
		stills := p.Input.StillsOnly() || (p.Input.CodeAnim() && s.Code.Fallback)
		g := MontageGap{SceneID: s.ID, Number: i + 1}
		var why []string
		if !has(s.Video) && !(stills && has(s.Frame)) {
			g.NoClip = true
			g.NoFrame = !has(s.Frame)
			if p.Input.CodeAnim() && !s.Code.Fallback {
				why = append(why, codeWhy(s))
			} else {
				why = append(why, clipWhy(s, g.NoFrame))
			}
		}
		if needsVoice(p, s) && !has(s.Audio) {
			g.NoVoice = true
			why = append(why, assetWhy("озвучка", s.Audio, "озвучку ще не зроблено"))
		}
		if g.NoClip || g.NoVoice {
			g.Reason = strings.Join(why, "; ")
			out = append(out, g)
		}
	}
	return out
}

// needsVoice: a voice-over film needs a line for s (its narration, or its
// slice of the user's recording).
func needsVoice(p *Project, s *Scene) bool {
	if !p.Input.Voiceover || p.Input.SoundMode() != AudioVoiceover {
		return false
	}
	return strings.TrimSpace(s.Narration) != "" || s.AudioSource == AudioUpload
}

// clipWhy says why a scene has no clip, with the exact error when it failed.
func clipWhy(s *Scene, noFrame bool) string {
	switch {
	case s.Video.Status == StatusError && strings.TrimSpace(s.Video.Error) != "":
		return "кліп: " + strings.TrimSpace(s.Video.Error)
	case noFrame && s.Frame.Status == StatusError:
		e := firstNonBlank(strings.TrimSpace(s.Frame.Error), strings.TrimSpace(s.Board.Error))
		if e == "" {
			return "кадр не вдався"
		}
		return "кадр: " + e
	case s.Video.Path != "":
		return "файл кліпу зник з папки проєкту (" + s.Video.Path + ")"
	case noFrame && s.Frame.Path != "":
		return "файл кадру зник з папки проєкту (" + s.Frame.Path + ")"
	case noFrame:
		return "кадр ще не намальовано"
	case s.Video.Status == StatusRunning || s.Video.Status == StatusQueued:
		return "кліп ще знімається"
	}
	return "кліп ще не знято"
}

// codeWhy says why a scene of a code film has no animation clip.
func codeWhy(s *Scene) string {
	switch {
	case s.Video.Status == StatusError && strings.TrimSpace(s.Video.Error) != "":
		return "анімація: " + strings.TrimSpace(s.Video.Error)
	case s.Video.Path != "":
		return "файл анімації зник з папки проєкту (" + s.Video.Path + ")"
	case s.Video.Status == StatusRunning || s.Video.Status == StatusQueued:
		return "анімація ще рендериться"
	case len(s.Code.Spec) > 0:
		return "анімацію написано, але ще не відрендерено"
	}
	return "анімацію ще не зроблено"
}

// assetWhy is "<what>: <exact error>", or none when it was never made.
func assetWhy(what string, a Asset, none string) string {
	switch {
	case a.Status == StatusError && strings.TrimSpace(a.Error) != "":
		return what + ": " + strings.TrimSpace(a.Error)
	case a.Path != "":
		return "файл зник з папки проєкту (" + a.Path + ")"
	case a.Status == StatusRunning || a.Status == StatusQueued:
		return what + " ще триває"
	}
	return none
}

// MissingScenesError: the montage was not started because scenes are not
// ready and nobody chose what to do with them (or the choice was to stop).
type MissingScenesError struct {
	Gaps []MontageGap
	// Stop: the user chose «Зупинитись» (Settings) — no question pending.
	Stop bool
}

func (e *MissingScenesError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Не всі сцени готові до монтажу (%d з них): ", len(e.Gaps))
	for i, g := range e.Gaps {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(g.Line())
	}
	b.WriteString(". ")
	if e.Stop {
		b.WriteString("У Налаштуваннях → Автопілот обрано «Зупинитись»: перезніми ці сцени або змонтуй вручну на кроці «Монтаж».")
	} else {
		b.WriteString("Обери на кроці «Монтаж», як бути: змонтувати без них, з кадром замість відео або спершу перезняти.")
	}
	return b.String()
}

// errMissing is a MissingScenesError for gaps under policy, nil when the
// montage may go on (no gaps, or a policy that handles them).
func errMissing(gaps []MontageGap, policy string) error {
	if len(gaps) == 0 {
		return nil
	}
	switch policy {
	case MissingSkip, MissingStill:
		return nil
	}
	return &MissingScenesError{Gaps: gaps, Stop: policy == MissingStop}
}

// IsMissingScenes reports whether err is a MissingScenesError (and returns it).
func IsMissingScenes(err error) (*MissingScenesError, bool) {
	var m *MissingScenesError
	ok := errors.As(err, &m)
	return m, ok
}

// applyGaps makes o follow policy for gaps (the segments are in scene
// order, one per scene) and returns the gaps with what was done to each.
func applyGaps(o *media.MontageOptions, gaps []MontageGap, policy string) []MontageGap {
	out := make([]MontageGap, 0, len(gaps))
	for _, g := range gaps {
		k := g.Number - 1
		if k < 0 || k >= len(o.Segments) {
			continue
		}
		seg := &o.Segments[k]
		switch {
		case policy == MissingSkip || g.NoFrame:
			*seg = media.Segment{} // neither clip nor frame: Layout leaves it out
			g.Action = GapSkipped
		case g.NoClip:
			seg.Video, seg.VideoDur = "", 0 // the frame with a motion effect
			g.Action = GapStill
		default: // only the voice is missing
			g.Action = GapSilent
		}
		if g.NoVoice || g.Action == GapSkipped {
			seg.Voice, seg.VoiceDur = "", 0
		}
		out = append(out, g)
	}
	return out
}

// gapsSummary lists what a render left out or replaced, scene by scene with
// the reason (each clipped to keep the message readable): "" for none.
func gapsSummary(gaps []MontageGap) string {
	if len(gaps) == 0 {
		return ""
	}
	groups := []struct{ action, label string }{
		{GapStill, "кадр замість відео"}, {GapSkipped, "пропущено"}, {GapSilent, "без голосу"},
	}
	var parts []string
	for _, gr := range groups {
		var items []string
		for _, g := range gaps {
			if g.Action != gr.action {
				continue
			}
			item := "сцена " + strconv.Itoa(g.Number)
			if g.Reason != "" {
				item += " (" + clipRunes(g.Reason, 160) + ")"
			}
			items = append(items, item)
		}
		if len(items) > 0 {
			parts = append(parts, gr.label+": "+strings.Join(items, ", "))
		}
	}
	return "⚠ " + strings.Join(parts, " · ")
}
