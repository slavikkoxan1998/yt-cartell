package studio

// Step 5: montage orchestration (runMontage of montage.ts). The rendering
// itself lives in media.Montage; this file maps the project onto segments
// (with the per-scene timeline controls), exposes the exact timeline for the
// editor and stores the outputs under <project>/montage/.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"ytcartell/internal/media"
)

// TimelineItem is one scene on the final film's timeline, timed exactly as
// the montage renders it (seconds; picture times are multiples of one frame
// = 1/24 s, voice times of one audio sample).
type TimelineItem struct {
	SceneID string
	// Start and Duration on the final timeline. Duration includes the
	// overlaps: with an overlapping transition the next item starts
	// Overlap seconds before this one ends.
	Start, Duration float64
	HasClip         bool    // false: the frame is shown with a motion effect
	Transition      string  // effective transition id into the next scene ("" for the last)
	Effect          string  // effective motion effect id ("none" = static)
	Overlap         float64 // seconds shared with the next scene (overlapping transitions)
	// ClipDuration is the clip (or still) time used after trimming; the rest
	// of Duration holds the last frame while the voice finishes.
	ClipDuration float64
	// TrimIn/TrimOut actually applied (Scene.TrimIn/TrimOut clamped to the clip).
	TrimIn, TrimOut float64

	// The voice line as it plays (all 0 durations = the scene has no voice):
	// VoiceStart…VoiceEnd (VoiceDuration long) is the trimmed, moved line on
	// the film timeline — also where its subtitles run. VoiceDefaultStart is
	// where that trimmed line would start without a move, so a drag to x sets
	// Scene.VoiceShift = x − VoiceDefaultStart; VoiceShift is the move really
	// applied (clamped so the line stays inside the film). VoiceRawDuration is
	// the whole voice file and VoiceTrimIn/VoiceTrimOut the trims really
	// applied: the untrimmed audio would span [VoiceStart−VoiceTrimIn,
	// VoiceStart−VoiceTrimIn+VoiceRawDuration] (draw trim handles there).
	// Trimming the head moves the rest of the line to VoiceDefaultStart; to
	// keep the words in place add the same amount to VoiceShift.
	VoiceStart, VoiceEnd, VoiceDuration float64
	VoiceDefaultStart, VoiceShift       float64
	VoiceRawDuration                    float64
	VoiceTrimIn, VoiceTrimOut           float64
}

// Timeline lays the project out exactly like the montage will: scenes with
// neither a clip nor a frame file are skipped, lengths come from the assets'
// durations (a clip of unknown length counts as 8 s, a voice of unknown
// length does not lengthen its scene — the montage probes such files and
// stores their durations, so the timeline is exact afterwards). No ffmpeg is
// run, only file-existence checks: cheap, but cache it per project revision
// rather than calling it every frame.
func (e *Engine) Timeline(p Project) []TimelineItem { return timelineOf(p, e.Dir(p.ID)) }

// timelineOf is Timeline for a project stored in dir.
func timelineOf(p Project, dir string) []TimelineItem {
	slots, _ := media.Layout(montageOptions(&p, dir))
	out := make([]TimelineItem, 0, len(slots))
	for _, s := range slots {
		out = append(out, TimelineItem{
			SceneID:           p.Scenes[s.Segment].ID,
			Start:             s.Start,
			Duration:          s.Duration,
			HasClip:           s.HasClip,
			Transition:        s.Transition,
			Effect:            s.Effect,
			Overlap:           s.Overlap,
			ClipDuration:      s.ClipDuration,
			TrimIn:            s.TrimIn,
			TrimOut:           s.TrimOut,
			VoiceStart:        s.VoiceStart,
			VoiceEnd:          s.VoiceEnd,
			VoiceDuration:     s.VoiceDuration,
			VoiceDefaultStart: s.VoiceDefaultStart,
			VoiceShift:        s.VoiceShift,
			VoiceRawDuration:  s.VoiceRawDuration,
			VoiceTrimIn:       s.VoiceTrimIn,
			VoiceTrimOut:      s.VoiceTrimOut,
		})
	}
	return out
}

// TimelineDuration is the length of the final film Timeline describes —
// exactly the montage's Duration (a whole number of 1/24 s frames).
func TimelineDuration(items []TimelineItem) float64 {
	if len(items) == 0 {
		return 0
	}
	last := items[len(items)-1]
	return math.Round((last.Start+last.Duration)*24) / 24
}

// montageOptions maps a project onto the montage settings (dir = the
// project folder). Missing files are left out exactly like media.Montage
// does, so Layout gives the same plan as the render.
func montageOptions(p *Project, dir string) media.MontageOptions {
	abs := func(rel string) string {
		if rel == "" {
			return ""
		}
		return filepath.Join(dir, filepath.FromSlash(rel))
	}
	file := func(rel string) string {
		if a := abs(rel); isFile(a) {
			return a
		}
		return ""
	}
	segs := make([]media.Segment, 0, len(p.Scenes))
	inVideo := p.Input.SoundMode() == AudioInVideo
	stills := p.Input.StillsOnly()
	for i, s := range p.Scenes {
		seg := media.Segment{
			Narration:  s.Narration,
			TrimIn:     s.TrimIn,
			TrimOut:    s.TrimOut,
			Transition: s.Transition,
			Effect:     s.Effect,
			Frame:      file(s.Frame.Path),
		}
		if seg.Video = file(s.Video.Path); seg.Video != "" {
			seg.VideoDur = s.Video.Duration
		}
		if p.Input.CodeAnim() && !s.Code.Fallback {
			// «Анімація кодом»: the clip is the scene, drawn to its exact
			// length — no camera move on it.
			seg.Effect = ""
		}
		if stills || (p.Input.CodeAnim() && s.Code.Fallback) {
			// «Тільки картинки» (or a code scene whose animation failed):
			// the frame with a motion effect (chosen per scene from its
			// camera move unless the user picked one), as long as its line.
			seg.Video, seg.VideoDur = "", 0
			seg.StillDur = stillLength(&s)
			if s.Effect == "" && p.Montage.Effect == "" {
				seg.Effect = AutoStillEffect(&s, i)
			}
		}
		if p.Input.Voiceover {
			if seg.Voice = file(s.Audio.Path); seg.Voice != "" {
				seg.VoiceDur = s.Audio.Duration
				seg.VoiceShift, seg.VoiceTrimIn, seg.VoiceTrimOut = s.VoiceShift, s.VoiceTrimIn, s.VoiceTrimOut
				seg.VoiceParts = voiceParts(p, &s) // multi-voice: each speaker's subtitles at their time (multivoice.go)
			}
			if s.AudioSource == AudioUpload {
				// A slice of the user's own recording already holds its lead-in
				// and tail (ownvoice.go): keep the lines back to back.
				seg.OwnVoice, seg.VoiceLead, seg.VoiceTail = true, speechLead, speechTail
			}
		}
		if inVideo && seg.Video != "" {
			// The characters speak in the clip: its sound is the film's
			// voice — full level, it ducks the music, its lines are the
			// subtitles.
			seg.ClipVolume = 1
			seg.Captions = lineCaptions(s.Lines, s.Video.Duration)
			seg.ClipSpeech = len(seg.Captions) > 0
		}
		segs = append(segs, seg)
	}
	m := p.Montage
	transition := m.Transition
	if transition == "" {
		transition = media.DefaultTransition
	}
	st := m.SubtitleStyle
	return media.MontageOptions{
		Segments:           segs,
		Aspect:             p.Input.Aspect,
		Transition:         transition,
		TransitionDuration: m.TransitionDuration, // 0 → 0.5 s
		Filter:             m.Filter,
		DefaultEffect:      m.Effect, // "" → gentle zoom-in
		Subtitles:          m.Subtitles,
		SubtitleStyle: media.SubtitleStyle{
			Preset: st.Preset, Size: st.Size, Position: st.Position, Color: st.Color,
			Outline: st.Outline, Box: st.Box, Uppercase: st.Uppercase,
		},
		MusicPath:     abs(m.MusicPath),
		MusicVolume:   m.MusicVolume,
		AmbientVolume: m.AmbientVolume,
		Ducking:       m.Ducking,
		TightLines:    p.Input.TightLines(),
		OutDir:        filepath.Join(dir, "montage"),
		Overlay:       overlayFunc(p),
	}
}

// lineCaptions are a scene's spoken lines as subtitles inside its clip
// (clip = its length, 0 = unknown: the usual 8 s), timed by lineTiming.
func lineCaptions(lines []Line, clip float64) []media.Caption {
	lines = cleanLines(lines)
	if len(lines) == 0 {
		return nil
	}
	if clip <= 0 {
		clip = 8
	}
	start, end := lineTiming(lines, clip)
	out := make([]media.Caption, 0, len(lines))
	for i, l := range lines {
		out = append(out, media.Caption{Text: strings.TrimSpace(l.Text), Start: start[i], End: end[i]})
	}
	return out
}

// fillDurations probes clips and voices whose duration is unknown (older or
// imported projects) and stores it, so the render and Timeline agree.
func (e *Engine) fillDurations(ctx context.Context, st *proj, snap *Project) {
	type probe struct {
		scene string
		video bool
		path  string
		dur   float64
	}
	var todo []*probe
	for _, s := range snap.Scenes {
		if s.Video.Path != "" && s.Video.Duration <= 0 {
			todo = append(todo, &probe{scene: s.ID, video: true, path: filepath.Join(st.dir, filepath.FromSlash(s.Video.Path))})
		}
		if snap.Input.Voiceover && s.Audio.Path != "" && s.Audio.Duration <= 0 {
			todo = append(todo, &probe{scene: s.ID, path: filepath.Join(st.dir, filepath.FromSlash(s.Audio.Path))})
		}
	}
	if len(todo) == 0 {
		return
	}
	ff := e.ffNow()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, pr := range todo {
		if !isFile(pr.path) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			if info, err := ff.Probe(ctx, pr.path); err == nil {
				pr.dur = info.Duration
			}
		}()
	}
	wg.Wait()
	set := func(p *Project) {
		for _, pr := range todo {
			if pr.dur <= 0 {
				continue
			}
			for i := range p.Scenes {
				s := &p.Scenes[i]
				if s.ID != pr.scene {
					continue
				}
				if a := &s.Audio; !pr.video && a.Duration <= 0 {
					a.Duration = pr.dur
				}
				if a := &s.Video; pr.video && a.Duration <= 0 {
					a.Duration = pr.dur
				}
			}
		}
	}
	set(snap)
	e.update(st, set)
}

// runMontage renders the film. Scenes that are not ready (gaps.go) follow
// j.missing: without a choice the montage is refused (MissingScenesError)
// — nothing is ever replaced or dropped silently; what was is stored in
// Montage.Gaps and listed in the returned message.
func (e *Engine) runMontage(j *jobCtx) (string, error) {
	snap := j.snapshot()
	dir := j.st.dir
	gaps := MontageGaps(&snap, dir)
	if err := errMissing(gaps, j.missing); err != nil {
		return "", err
	}
	e.fillDurations(j.ctx, j.st, &snap)
	opts := montageOptions(&snap, dir)
	gaps = applyGaps(&opts, gaps, j.missing)
	gaps = append(gaps, codeGaps(&snap)...) // code scenes shown as their frame (codeanim.go)
	sort.SliceStable(gaps, func(a, b int) bool { return gaps[a].Number < gaps[b].Number })
	usable := false
	for _, s := range opts.Segments {
		usable = usable || s.Video != "" || s.Frame != ""
	}
	if !usable {
		if len(gaps) > 0 {
			return "", fmt.Errorf("Немає жодної готової сцени для монтажу: %s", gapsSummary(gaps))
		}
		return "", errors.New("Немає жодного кліпу чи кадру — спершу згенеруй відео")
	}
	abs := func(rel string) string {
		if rel == "" {
			return ""
		}
		return filepath.Join(dir, filepath.FromSlash(rel))
	}

	e.update(j.st, func(p *Project) {
		p.Montage.Output.Status = StatusRunning
		p.Montage.Output.Error = ""
	})

	opts.Progress = func(pct int, msg string) { j.progress(float64(pct), msg) }
	opts.Cancelled = j.cancelled
	res, err := e.ffNow().Montage(j.ctx, opts)
	if err != nil {
		stopped := j.cancelled() && isCancel(err)
		e.update(j.st, func(p *Project) {
			out := &p.Montage.Output
			// The montage folder is cleared first: forget outputs that are gone.
			if out.Path != "" && !isFile(abs(out.Path)) {
				out.Path, out.Duration = "", 0
			}
			if p.Montage.SRTPath != "" && !isFile(abs(p.Montage.SRTPath)) {
				p.Montage.SRTPath = ""
			}
			switch {
			case stopped && out.Path != "":
				out.Status = StatusDone
			case stopped:
				out.Status = StatusEmpty
			default:
				out.Status = StatusError
				out.Error = e.failureText(err)
			}
		})
		return "", err
	}

	rel := func(path string) string {
		if path == "" {
			return ""
		}
		r, err := filepath.Rel(dir, path)
		if err != nil {
			return filepath.ToSlash(path)
		}
		return filepath.ToSlash(r)
	}
	e.update(j.st, func(p *Project) {
		out := &p.Montage.Output
		out.Path, out.Duration = rel(res.Output), res.Duration
		out.Status, out.Error, out.Stale, out.Progress = StatusDone, "", false, 100
		out.UpdatedAt = nowMs()
		p.Montage.SRTPath = rel(res.SRT)
		p.Montage.Gaps = gaps
		p.Step = max(p.Step, 5)
	})
	sec := int(math.Round(res.Duration))
	msg := fmt.Sprintf("Фінальне відео готове · %d:%02d", sec/60, sec%60)
	if sum := gapsSummary(gaps); sum != "" {
		msg += " · " + sum
	}
	return msg, nil
}
