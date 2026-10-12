package media

// Pieces of the film for editor exports (studio/export.go): the voice lines
// and the ducked music as whole-film stems (sample-exact, placed like the
// montage's mix), scenes cut to exactly the frames they cover on the film
// (for editors that cannot read in/out points from a file — CapCut) and
// single stills (a conformed frame, the frame a clip holds at its end).

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// plan resolves o for the stems: missing files are blanked like Montage
// does, the timeline is laid out.
func stemPlan(o MontageOptions) (MontageOptions, []Slot, float64) {
	segs := make([]Segment, len(o.Segments))
	copy(segs, o.Segments)
	for i := range segs {
		s := &segs[i]
		if !isFile(s.Video) {
			s.Video = ""
		}
		if !isFile(s.Frame) {
			s.Frame = ""
		}
		if !isFile(s.Voice) {
			s.Voice = ""
		}
	}
	o.Segments = segs
	slots, total := Layout(o)
	return o, slots, total
}

// tempNear makes a scratch folder next to out (same volume, removed by the caller).
func tempNear(out string) (string, error) {
	dir := filepath.Dir(out)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(dir, ".stem-*")
}

// voiceParts renders every voice line of the plan as raw PCM into tmp and
// returns the parts placed on the film.
func (f FF) voiceParts(ctx context.Context, o *MontageOptions, slots []Slot, tmp string) ([]pcmPart, error) {
	j := &montageJob{f: f, o: o, segs: o.Segments, slots: slots, dir: tmp}
	var parts []pcmPart
	for k, sl := range slots {
		if o.Segments[sl.Segment].Voice == "" || sl.voiceLen <= 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := j.renderVoice(ctx, k); err != nil {
			return nil, err
		}
		parts = append(parts, pcmPart{j.voice(k), sl.voiceAt0, sl.voiceLen})
	}
	return parts, nil
}

// speechParts renders the sound of the clips that carry speech (films whose
// characters talk on camera) as PCM at their scenes — the other half of the
// montage's ducking key.
func (f FF) speechParts(ctx context.Context, o *MontageOptions, slots []Slot, tmp string) ([]pcmPart, error) {
	var parts []pcmPart
	for k, sl := range slots {
		s := o.Segments[sl.Segment]
		if !sl.HasClip || !s.ClipSpeech || s.Video == "" {
			continue
		}
		if info, err := f.Probe(ctx, s.Video); err != nil || !info.HasAudio {
			continue
		}
		n := sl.frames * samplesPerFrame
		vol := clamp01(o.AmbientVolume)
		if s.ClipVolume > 0 {
			vol = clamp01(s.ClipVolume)
		}
		var args []string
		if sl.TrimIn > 0 {
			args = append(args, "-ss", f3(sl.TrimIn))
		}
		if sl.TrimIn > 0 || sl.TrimOut > 0 {
			args = append(args, "-t", f3(frameSec(sl.clipFrames)))
		}
		out := filepath.Join(tmp, fmt.Sprintf("speech_%04d.pcm", k))
		args = append(args, "-i", s.Video, "-filter_complex",
			"[0:a]aresample=48000:async=1:first_pts=0,aformat=sample_fmts=fltp:channel_layouts=stereo,volume="+num(vol)+
				",apad,"+fmt.Sprintf("atrim=end_sample=%d", n)+",asetpts=PTS-STARTPTS[a]",
			"-map", "[a]", "-c:a", "pcm_s16le", "-ar", strconv.Itoa(sampleRate), "-ac", "2", "-f", "s16le", out)
		if err := f.Run(ctx, "", args...); err != nil {
			return nil, fmt.Errorf("звук сцени %d: %w", k+1, err)
		}
		parts = append(parts, pcmPart{out, sl.start * samplesPerFrame, n})
	}
	return parts, nil
}

// VoiceStem writes the film's voice lines — trimmed, faded at the cuts and
// placed sample-exactly like the montage mixes them — as one 48 kHz stereo
// 16-bit WAV exactly as long as the film (silence between the lines). ok is
// false (and nothing is written) when the film has no voice line. Pass
// probed durations in o.
func (f FF) VoiceStem(ctx context.Context, o MontageOptions, out string) (bool, error) {
	if f.FFmpeg == "" {
		return false, ErrNotFound
	}
	o, slots, total := stemPlan(o)
	if len(slots) == 0 {
		return false, nil
	}
	tmp, err := tempNear(out)
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)
	parts, err := f.voiceParts(ctx, &o, slots, tmp)
	if err != nil || len(parts) == 0 {
		return false, err
	}
	films := int(math.Round(total*fps)) * samplesPerFrame
	if err := mixParts(out, films, parts, true); err != nil {
		return false, err
	}
	return true, nil
}

// MusicStem writes the music bed exactly like the montage's final pass:
// looped over the film, at MusicVolume, faded out over the last 2.5 s and —
// with Ducking — compressed under the voice lines and the speech in the
// clips (the same sidechain settings). A 48 kHz stereo 16-bit WAV as long as
// the film; ok false when there is no music file.
func (f FF) MusicStem(ctx context.Context, o MontageOptions, out string) (bool, error) {
	if f.FFmpeg == "" {
		return false, ErrNotFound
	}
	if !isFile(o.MusicPath) {
		return false, nil
	}
	o, slots, total := stemPlan(o)
	if len(slots) == 0 || total <= 0 {
		return false, nil
	}
	tmp, err := tempNear(out)
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)
	films := int(math.Round(total*fps)) * samplesPerFrame
	key := ""
	if o.Ducking {
		voices, err := f.voiceParts(ctx, &o, slots, tmp)
		if err != nil {
			return false, err
		}
		speech, err := f.speechParts(ctx, &o, slots, tmp)
		if err != nil {
			return false, err
		}
		if parts := append(voices, speech...); len(parts) > 0 {
			key = filepath.Join(tmp, "key.wav")
			if err := mixParts(key, films, parts, false); err != nil {
				return false, err
			}
		}
	}
	args := []string{"-stream_loop", "-1", "-i", o.MusicPath}
	chain := "[0:a]aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo,volume=" + num(clamp01(o.MusicVolume)) +
		",afade=t=out:st=" + f3(math.Max(0, total-2.5)) + ":d=2.5"
	if key != "" {
		args = append(args, "-i", key)
		// sidechaincompress stops a little before its key ends: pad the
		// rest (the music has faded out there).
		chain += "[m];[m][1:a]sidechaincompress=threshold=0.02:ratio=12:attack=15:release=450,apad[md]"
	} else {
		chain += "[md]"
	}
	args = append(args, "-filter_complex", chain, "-map", "[md]", "-t", f3(total),
		"-c:a", "pcm_s16le", "-ar", strconv.Itoa(sampleRate), "-ac", "2", out)
	if err := f.Run(ctx, "", args...); err != nil {
		_ = os.Remove(out)
		return false, fmt.Errorf("музика: %w", err)
	}
	return true, nil
}

// CutClip renders scene k of the plan (slots = Layout(o)) between its local
// frames from…to (0 … Frames()) as an H.264 MP4 at W×H and FPS: the trimmed
// clip holding its last frame, or the still with its motion effect — no
// colour look, dips, text or voice. A clip that has sound (hasAudio) keeps
// it at the film's level (ClipVolume, else AmbientVolume), cut to the same
// frames.
func (f FF) CutClip(ctx context.Context, o MontageOptions, slots []Slot, k, from, to, W, H int, hasAudio bool, out string) error {
	if f.FFmpeg == "" {
		return ErrNotFound
	}
	if k < 0 || k >= len(slots) {
		return errors.New("немає такої сцени")
	}
	sl := slots[k]
	s := o.Segments[sl.Segment]
	F := sl.frames
	from, to = max(0, min(from, F)), max(0, min(to, F))
	if to <= from {
		return errors.New("порожній відрізок сцени")
	}
	var args []string
	clip := sl.HasClip && s.Video != ""
	if clip {
		if sl.TrimIn > 0 {
			args = append(args, "-ss", f3(sl.TrimIn))
		}
		if sl.TrimIn > 0 || sl.TrimOut > 0 {
			args = append(args, "-t", f3(frameSec(sl.clipFrames)))
		}
		args = append(args, "-i", s.Video)
	} else if s.Frame != "" {
		args = append(args, "-framerate", strconv.Itoa(fps), "-i", s.Frame)
	} else {
		return errors.New("у сцени немає ні кліпу, ні кадру")
	}
	var v []string
	if clip {
		v = append(v,
			fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,setsar=1", W, H, W, H),
			fmt.Sprintf("fps=%d:start_time=0", fps), "format=yuv420p",
			fmt.Sprintf("tpad=stop_mode=clone:stop=%d", max(0, F-sl.clipFrames)+fps))
	} else {
		v = append(v,
			fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase:flags=lanczos,crop=%d:%d,setsar=1", W, H, W, H),
			"format=yuv420p", fmt.Sprintf("tpad=stop_mode=clone:stop=%d", F-1))
	}
	v = append(v, fmt.Sprintf("trim=end_frame=%d", F))
	if e := effectFilter(sl.Effect, W, H, F, sl.HasClip); e != "" {
		v = append(v, e)
	}
	v = append(v, fmt.Sprintf("trim=start_frame=%d:end_frame=%d", from, to), "setpts=PTS-STARTPTS", "format=yuv420p")
	fc := []string{"[0:v]" + strings.Join(v, ",") + "[v]"}
	sound := clip && hasAudio
	if sound {
		vol := clamp01(o.AmbientVolume)
		if s.ClipVolume > 0 {
			vol = clamp01(s.ClipVolume)
		}
		fc = append(fc, "[0:a]aresample=48000:async=1:first_pts=0,aformat=sample_fmts=fltp:channel_layouts=stereo,volume="+num(vol)+
			",apad,"+fmt.Sprintf("atrim=start_sample=%d:end_sample=%d", from*samplesPerFrame, to*samplesPerFrame)+",asetpts=PTS-STARTPTS[a]")
	}
	args = append(args, "-filter_complex", strings.Join(fc, ";"), "-map", "[v]")
	args = append(args, encFinal...)
	if sound {
		args = append(args, "-map", "[a]", "-c:a", "aac", "-b:a", "192k")
	}
	args = append(args, "-movflags", "+faststart", out)
	if err := f.Run(ctx, "", args...); err != nil {
		_ = os.Remove(out)
		return fmt.Errorf("сцена %d: %w", k+1, err)
	}
	return nil
}

// StillFrame writes one picture of src (a video at `at` seconds, or an
// image) as a JPEG: W×H > 0 scales it to cover W×H and crops the overflow
// (like the montage frames a still), else the original size is kept.
func (f FF) StillFrame(ctx context.Context, src string, at float64, W, H int, out string) error {
	if f.FFmpeg == "" {
		return ErrNotFound
	}
	var args []string
	if at > 0 {
		args = append(args, "-ss", strconv.FormatFloat(at, 'f', 4, 64))
	}
	args = append(args, "-i", src, "-an", "-sn", "-dn", "-frames:v", "1")
	vf := "format=yuvj444p"
	if W > 0 && H > 0 {
		vf = fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase:flags=lanczos,crop=%d:%d,setsar=1,", W, H, W, H) + vf
	}
	args = append(args, "-vf", vf, "-q:v", "2", "-update", "1", out)
	if err := f.Run(ctx, "", args...); err != nil {
		_ = os.Remove(out)
		return err
	}
	if !isFile(out) {
		return errors.New("не вдалося отримати кадр")
	}
	return nil
}
