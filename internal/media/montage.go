package media

// Step 5: assemble the final video (port of apps/api/src/studio/montage.ts,
// extended with transitions, motion effects, colour looks, trims and
// subtitle styles).
//
// Pipeline (timing model: timeline.go):
//
//  1. Every scene is rendered on its own, several in parallel: the picture —
//     the clip (trimmed), or the still with a motion effect — scaled, graded
//     with the colour look, dip-faded, held on its last frame while the voice
//     is longer, exactly N frames long; plus its sound (ambient + voice) as
//     exact-length PCM. The picture is split into the body and, for
//     overlapping transitions, the head/tail overlaps (lossless).
//  2. Every overlapping transition is rendered from the tail of one scene and
//     the head of the next (xfade) as soon as both exist; the overlaps are
//     then deleted, so temporary disk use stays small for any film length.
//  3. Bodies and transitions share one encoding and are joined by stream copy;
//     the sound parts are mixed in Go into one sample-exact WAV.
//  4. Final pass: music (looped, faded out, ducked under the voice) and
//     burnt-in subtitles.
//
// Hard cuts and dips need no step 2 — they stay "render segments + concat".

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Segment is one scene's material (absolute paths; "" or a missing file = none).
// VideoDur/VoiceDur of 0 are probed. Voice should be "" when voiceover is off.
type Segment struct {
	Video, Frame, Voice string
	VideoDur, VoiceDur  float64
	Narration           string
	// TrimIn/TrimOut cut seconds from the start/end of the clip (of the 8 s
	// still for frame-only scenes); at least 0.5 s always remains.
	TrimIn, TrimOut float64
	// Transition into the NEXT scene (Transitions id; "" = MontageOptions.Transition).
	Transition string
	// StillDur (s) is how long a still scene's picture lasts before its
	// voice line lengthens it (0 = 8 s). «Тільки картинки» films set it so
	// every scene is as long as its line; such stills are no missing clips.
	StillDur float64
	// Effect is the motion effect (Effects id). Stills: "" = the project's
	// DefaultEffect; clips: "" = none (a chosen effect runs at half strength).
	Effect string
	// OwnVoice marks a slice of the user's own recording that already holds
	// VoiceLead seconds before its first word and VoiceTail after its last:
	// no breath/tail is added, so consecutive lines keep the recording's
	// rhythm, and only that padding (never speech) may lie under a transition.
	OwnVoice             bool
	VoiceLead, VoiceTail float64
	// The montage editor's controls of the voice line: VoiceTrimIn/Out cut
	// seconds from the start/end of the voice audio (the scene then needs —
	// and holds — only the trimmed line); VoiceShift moves the trimmed line
	// later (+) / earlier (−) than its default start. A move never changes
	// the picture: the line may run under neighbouring scenes and is clamped
	// into the film.
	VoiceShift, VoiceTrimIn, VoiceTrimOut float64
	// ClipVolume is the level of the clip's own sound (0 = AmbientVolume).
	// Films whose characters speak on camera play their clips at full level.
	ClipVolume float64
	// ClipSpeech: the clip's own sound carries speech — it keys the music
	// ducking like a voice line does.
	ClipSpeech bool
	// Captions are subtitles spoken inside the clip (the clip's own speech),
	// timed in seconds from the clip's start (before TrimIn); the parts cut
	// away by the trims are dropped.
	Captions []Caption
	// VoiceParts: a multi-voice line's parts timed in the voice file — the
	// subtitles show each part at its time, in its speaker's colour
	// (join.go); none = the narration spread over the line.
	VoiceParts []VoicePart
}

// Caption is one subtitle inside a clip (seconds from the clip's start).
type Caption struct {
	Text       string
	Start, End float64
}

// Overlay is animated on-screen text drawn over one scene: transparent PNG
// frames listed in an FFmpeg concat file (List) that covers the scene from
// its first frame, composited at X, Y of the picture.
type Overlay struct {
	List string
	X, Y int
}

// OverlaySpan is the time a scene is on screen (s): its whole length and the
// overlaps it shares with the previous (In) and the next (Out) scene.
type OverlaySpan struct {
	Duration, In, Out float64
}

// OverlayFunc renders the on-screen text of segment seg (an index into
// MontageOptions.Segments) for a size film into dir (a temporary folder the
// montage removes afterwards); ok = false when the scene has none.
type OverlayFunc func(ctx context.Context, seg int, span OverlaySpan, size image.Point, dir string) (o Overlay, ok bool, err error)

// MontageOptions are the step-5 settings. Progress and Cancelled may be nil.
type MontageOptions struct {
	Segments []Segment
	Aspect   string // "16:9" | "9:16" — output size when there is no clip
	// Transition is the default transition between scenes (Transitions id;
	// "" or unknown = hard cut). Anything but a hard cut also fades the film
	// in from black and out to black.
	Transition string
	// TransitionDuration in seconds for overlapping transitions (0 = 0.5,
	// clamped to 0.2…2); dips last 0.6 × it on each side (0.3 s by default).
	TransitionDuration float64
	Filter             string // colour look for every scene (Filters id; "" = none)
	DefaultEffect      string // motion of stills (Effects id; "" = "zoomin")
	Subtitles          string // none|srt|burn ("" behaves like srt)
	SubtitleStyle      SubtitleStyle
	MusicPath          string
	MusicVolume        float64
	AmbientVolume      float64
	Ducking            bool
	OutDir             string
	// TightLines: voice lines get almost no breath and tail (0.04 s + 0.08 s
	// instead of 0.25 + 0.35) — the rapid Shorts tempo, nearly no pauses.
	TightLines bool
	// Overlay draws animated on-screen text over the scenes (nil = none).
	Overlay OverlayFunc
	// Workers is how many scenes render at once (0 = by CPU count).
	Workers   int
	Progress  func(pct int, msg string)
	Cancelled func() bool
}

// MontageResult: absolute paths of the final mp4 and the .srt ("" when none).
type MontageResult struct {
	Output, SRT  string
	Duration     float64
	MissingClips int
	// Timeline is the plan that was rendered (probed durations filled in).
	Timeline []Slot
}

// ErrCancelled is returned when Cancelled() reports true (or ctx is done).
var ErrCancelled = errors.New("Зупинено")

// Message is the job summary shown to the user (same text as the TS job).
func (r MontageResult) Message() string {
	sec := int(math.Round(r.Duration))
	msg := fmt.Sprintf("Фінальне відео готове · %d:%02d", sec/60, sec%60)
	if r.MissingClips > 0 {
		msg += fmt.Sprintf(" (%d сцен без кліпу — використано кадр)", r.MissingClips)
	}
	return msg
}

func f3(x float64) string  { return strconv.FormatFloat(x, 'f', 3, 64) }
func num(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }

func clamp01(x float64) float64 {
	if math.IsNaN(x) {
		return 0
	}
	return math.Max(0, math.Min(1, x))
}

// Encoding shared by every body and transition part: identical parameters
// are what makes the stream-copy join valid.
var (
	encFinal    = []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "19", "-pix_fmt", "yuv420p", "-r", strconv.Itoa(fps)}
	encLossless = []string{"-c:v", "libx264", "-preset", "ultrafast", "-qp", "0", "-pix_fmt", "yuv420p", "-r", strconv.Itoa(fps)}
)

// autoWorkers: scenes rendered at once. Each ffmpeg already runs x264 on
// many threads, but decoding, scaling and the effect filter are mostly
// single-threaded, so a few processes in parallel keep every core busy.
func autoWorkers(W, H int) int {
	w := max(1, min(4, runtime.NumCPU()/4))
	if W*H > 1920*1088 {
		w = min(w, 2) // 4K frames: keep memory in check
	}
	return w
}

// ---- job ------------------------------------------------------------------------------------

type montageJob struct {
	f     FF
	o     *MontageOptions
	segs  []Segment // resolved: missing files blanked, durations probed
	amb   []bool    // the clip has a sound track
	slots []Slot
	dir   string
	W, H  int
	look  Filter
}

func (j *montageJob) file(format string, k int) string {
	return filepath.Join(j.dir, fmt.Sprintf(format, k))
}
func (j *montageJob) body(k int) string  { return j.file("seg_%04d.mp4", k) }
func (j *montageJob) head(k int) string  { return j.file("seg_%04d_in.mp4", k) }
func (j *montageJob) tail(k int) string  { return j.file("seg_%04d_out.mp4", k) }
func (j *montageJob) pcm(k int) string   { return j.file("seg_%04d.pcm", k) }
func (j *montageJob) voice(k int) string { return j.file("seg_%04d_voice.pcm", k) }
func (j *montageJob) trans(k int) string {
	return j.file("tr_%04d.mp4", k)
}

// ambient reports whether scene k's own clip sound goes into the mix.
func (j *montageJob) ambient(k int) bool {
	sl := j.slots[k]
	return sl.HasClip && j.amb[sl.Segment] && j.clipVolume(sl.Segment) > 0
}

// clipVolume is the level of segment i's own clip sound.
func (j *montageJob) clipVolume(i int) float64 {
	if v := j.segs[i].ClipVolume; v > 0 {
		return clamp01(v)
	}
	return clamp01(j.o.AmbientVolume)
}

// overlayInput renders scene k's on-screen text (nil options or no text:
// ok false). The frames live in their own folder, removed by the caller.
func (j *montageJob) overlayInput(ctx context.Context, k int) (Overlay, string, bool, error) {
	if j.o.Overlay == nil {
		return Overlay{}, "", false, nil
	}
	sl := j.slots[k]
	dir := j.file("ov_%04d", k)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Overlay{}, "", false, err
	}
	span := OverlaySpan{Duration: frameSec(sl.frames), In: frameSec(sl.in), Out: frameSec(sl.out)}
	o, ok, err := j.o.Overlay(ctx, sl.Segment, span, image.Pt(j.W, j.H), dir)
	if err != nil || !ok || o.List == "" {
		_ = os.RemoveAll(dir)
		if err != nil {
			return Overlay{}, "", false, fmt.Errorf("текст у кадрі сцени %d: %w", k+1, err)
		}
		return Overlay{}, "", false, nil
	}
	return o, dir, true, nil
}

// cutFade (samples, 8 ms) fades a voice line where the editor cut into it,
// so a trim never clicks.
const cutFade = 384

// renderSlot renders scene k: body (+ head/tail overlaps), its ambient sound
// as PCM (when the clip has any) and its voice line.
func (j *montageJob) renderSlot(ctx context.Context, k int) error {
	sl := &j.slots[k]
	s := j.segs[sl.Segment]
	W, H, F := j.W, j.H, sl.frames

	var args []string
	if sl.HasClip {
		if sl.TrimIn > 0 {
			args = append(args, "-ss", f3(sl.TrimIn))
		}
		if sl.TrimIn > 0 || sl.TrimOut > 0 {
			args = append(args, "-t", f3(frameSec(sl.clipFrames)))
		}
		args = append(args, "-i", s.Video)
	} else {
		args = append(args, "-framerate", strconv.Itoa(fps), "-i", s.Frame)
	}
	// On-screen text: a second input of transparent frames covering the scene.
	ov, ovDir, hasOv, err := j.overlayInput(ctx, k)
	if err != nil {
		return err
	}
	if hasOv {
		defer os.RemoveAll(ovDir)
		args = append(args, "-f", "concat", "-safe", "0", "-i", ov.List)
	}

	// Picture: exactly F frames of W×H.
	var v []string
	if sl.HasClip {
		// start_time=0 / first_pts=0 (sound below) anchor picture and sound to
		// the same zero, so a trimmed clip keeps its lip-sync.
		v = append(v,
			fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,setsar=1", W, H, W, H),
			fmt.Sprintf("fps=%d:start_time=0", fps), "format=yuv420p",
			// Hold the last frame (voice longer than the clip, or a short clip), then cut to F.
			fmt.Sprintf("tpad=stop_mode=clone:stop=%d", max(0, F-sl.clipFrames)+fps))
	} else {
		v = append(v,
			fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase:flags=lanczos,crop=%d:%d,setsar=1", W, H, W, H),
			"format=yuv420p",
			fmt.Sprintf("tpad=stop_mode=clone:stop=%d", F-1))
	}
	v = append(v, fmt.Sprintf("trim=end_frame=%d", F))
	if e := effectFilter(sl.Effect, W, H, F, sl.HasClip); e != "" {
		v = append(v, e)
	}
	fc := []string{"[0:v]" + strings.Join(v, ",") + "[vm]"}
	cur := "[vm]"
	if g := j.look.look(cur, "[vl]", "lk"); g != "" {
		fc = append(fc, g)
		cur = "[vl]"
	}
	if hasOv {
		// The text is not graded with the look; it fades with the picture.
		// fps drops a frame the overlay may repeat when its second input
		// starts (concat of PNGs), trim keeps exactly F frames.
		fc = append(fc, "[1:v]format=rgba,setpts=PTS-STARTPTS[ovf]",
			fmt.Sprintf("%s[ovf]overlay=x=%d:y=%d:eof_action=pass:format=auto,fps=%d,trim=end_frame=%d[vo]", cur, ov.X, ov.Y, fps, F))
		cur = "[vo]"
	}
	var post []string
	if sl.fadeIn > 0 {
		post = append(post, fmt.Sprintf("fade=t=in:st=0:d=%s:color=%s", f3(sl.fadeIn), sl.fadeInCol))
	}
	if sl.fadeOut > 0 {
		post = append(post, fmt.Sprintf("fade=t=out:st=%s:d=%s:color=%s", f3(math.Max(0, frameSec(F)-sl.fadeOut)), f3(sl.fadeOut), sl.fadeOutCol))
	}
	post = append(post, "format=yuv420p")

	type output struct {
		label, file string
		enc         []string
	}
	outs := []output{{"[vb]", j.body(k), encFinal}}
	if sl.in == 0 && sl.out == 0 {
		fc = append(fc, cur+strings.Join(post, ",")+"[vb]")
	} else {
		parts := []string{fmt.Sprintf("[sb]trim=start_frame=%d:end_frame=%d,setpts=PTS-STARTPTS[vb]", sl.in, F-sl.out)}
		labels := "[sb]"
		if sl.in > 0 {
			labels += "[sh]"
			parts = append(parts, fmt.Sprintf("[sh]trim=end_frame=%d,setpts=PTS-STARTPTS[vh]", sl.in))
			outs = append(outs, output{"[vh]", j.head(k), encLossless})
		}
		if sl.out > 0 {
			labels += "[st]"
			parts = append(parts, fmt.Sprintf("[st]trim=start_frame=%d,setpts=PTS-STARTPTS[vt]", F-sl.out))
			outs = append(outs, output{"[vt]", j.tail(k), encLossless})
		}
		fc = append(fc, cur+strings.Join(post, ",")+fmt.Sprintf(",split=%d", len(outs))+labels)
		fc = append(fc, parts...)
	}

	// Ambient sound, exactly F × 2000 samples, following the picture's fades
	// (an equal-power crossfade over an overlap, the dip in/out). It stays in
	// the same graph as the picture: with separate graphs FFmpeg 8 deadlocks
	// once the sound is complete (input 0's audio is no longer drained).
	// The voice line is rendered on its own (renderVoice) and placed by the mix.
	ambient := j.ambient(k)
	if ambient {
		n := F * samplesPerFrame
		a := []string{"aresample=48000:async=1:first_pts=0", "aformat=sample_fmts=fltp:channel_layouts=stereo",
			"volume=" + num(j.clipVolume(sl.Segment)), "apad", fmt.Sprintf("atrim=end_sample=%d", n), "asetpts=PTS-STARTPTS"}
		switch {
		case sl.in > 0:
			a = append(a, fmt.Sprintf("afade=t=in:ss=0:ns=%d:curve=qsin", sl.in*samplesPerFrame))
		case sl.fadeIn > 0:
			a = append(a, "afade=t=in:st=0:d="+f3(sl.fadeIn))
		}
		switch {
		case sl.out > 0:
			a = append(a, fmt.Sprintf("afade=t=out:ss=%d:ns=%d:curve=qsin", n-sl.out*samplesPerFrame, sl.out*samplesPerFrame))
		case sl.fadeOut > 0:
			a = append(a, fmt.Sprintf("afade=t=out:st=%s:d=%s", f3(math.Max(0, frameSec(F)-sl.fadeOut)), f3(sl.fadeOut)))
		}
		fc = append(fc, "[0:a]"+strings.Join(a, ",")+"[a]")
	}

	args = append(args, "-filter_complex", strings.Join(fc, ";"))
	for _, o := range outs {
		args = append(args, "-map", o.label)
		args = append(args, o.enc...)
		args = append(args, o.file)
	}
	if ambient {
		args = append(args, "-map", "[a]", "-c:a", "pcm_s16le", "-ar", strconv.Itoa(sampleRate), "-ac", "2", "-f", "s16le", j.pcm(k))
	}
	if err := j.f.Run(ctx, "", args...); err != nil {
		return fmt.Errorf("сцена %d: %w", k+1, err)
	}
	return j.renderVoice(ctx, k)
}

// renderVoice cuts scene k's voice line to its trims — sample-exact, with
// cutFade only where the editor cut into the audio — as raw PCM of exactly
// the line's length (the mix places it on the film).
func (j *montageJob) renderVoice(ctx context.Context, k int) error {
	sl := j.slots[k]
	s := j.segs[sl.Segment]
	if s.Voice == "" || sl.voiceLen <= 0 {
		return nil
	}
	n := sl.voiceLen
	a := []string{"aresample=48000:async=1:first_pts=0", "aformat=sample_fmts=fltp:channel_layouts=stereo"}
	if sl.voiceSkip > 0 {
		a = append(a, fmt.Sprintf("atrim=start_sample=%d", sl.voiceSkip), "asetpts=PTS-STARTPTS")
	}
	fade := min(cutFade, n/2)
	if sl.VoiceTrimIn > 0 && fade > 0 {
		a = append(a, fmt.Sprintf("afade=t=in:ss=0:ns=%d", fade))
	}
	if sl.VoiceTrimOut > 0 && fade > 0 {
		a = append(a, fmt.Sprintf("afade=t=out:ss=%d:ns=%d", n-fade, fade))
	}
	a = append(a, "apad", fmt.Sprintf("atrim=end_sample=%d", n))
	err := j.f.Run(ctx, "", "-i", s.Voice, "-filter_complex", "[0:a]"+strings.Join(a, ",")+"[a]",
		"-map", "[a]", "-c:a", "pcm_s16le", "-ar", strconv.Itoa(sampleRate), "-ac", "2", "-f", "s16le", j.voice(k))
	if err != nil {
		return fmt.Errorf("голос сцени %d: %w", k+1, err)
	}
	return nil
}

// renderTransition blends the tail of scene k into the head of scene k+1.
func (j *montageJob) renderTransition(ctx context.Context, k int) error {
	sl := j.slots[k]
	args := []string{"-i", j.tail(k), "-i", j.head(k + 1),
		"-filter_complex", fmt.Sprintf("[0:v][1:v]xfade=transition=%s:duration=%s:offset=0,format=yuv420p[v]", sl.tr.XFade, num(frameSec(sl.out))),
		"-map", "[v]", "-frames:v", strconv.Itoa(sl.out)}
	args = append(args, encFinal...)
	if err := j.f.Run(ctx, "", append(args, j.trans(k))...); err != nil {
		return fmt.Errorf("перехід після сцени %d: %w", k+1, err)
	}
	// The overlaps are no longer needed (they can be large).
	_ = os.Remove(j.tail(k))
	_ = os.Remove(j.head(k + 1))
	return nil
}

// renderAll renders every scene and transition on up to workers goroutines.
// A transition runs as soon as both of its scenes are done (and before new
// scenes), which keeps the lossless overlaps on disk only briefly. done is
// called (serialised) after every finished task.
func (j *montageJob) renderAll(ctx context.Context, workers int, done func(scenes, transitions int)) error {
	n := len(j.slots)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu        sync.Mutex
		cond      = sync.NewCond(&mu)
		next      int   // next scene to start
		running   int   // tasks in flight
		ready     []int // transitions whose two scenes are rendered
		finished  = make([]bool, n)
		nScenes   int
		nTrans    int
		firstErr  error
		wg        sync.WaitGroup
		trPending = func(k int) bool { return k >= 0 && k < n-1 && j.slots[k].out > 0 && finished[k] && finished[k+1] }
	)
	worker := func() {
		defer wg.Done()
		for {
			mu.Lock()
			for firstErr == nil && ctx.Err() == nil && len(ready) == 0 && next >= n && running > 0 {
				cond.Wait() // a running scene may still unlock a transition
			}
			if firstErr != nil || ctx.Err() != nil || (len(ready) == 0 && next >= n) {
				mu.Unlock()
				return
			}
			isTr, k := false, 0
			if len(ready) > 0 {
				isTr, k, ready = true, ready[0], ready[1:]
			} else {
				k = next
				next++
			}
			running++
			mu.Unlock()

			var err error
			if isTr {
				err = j.renderTransition(ctx, k)
			} else {
				err = j.renderSlot(ctx, k)
			}

			mu.Lock()
			running--
			switch {
			case err != nil:
				if firstErr == nil {
					firstErr = err
					cancel()
				}
			case isTr:
				nTrans++
			default:
				finished[k] = true
				nScenes++
				for _, t := range []int{k - 1, k} {
					if trPending(t) {
						ready = append(ready, t)
					}
				}
			}
			if err == nil && done != nil {
				done(nScenes, nTrans)
			}
			cond.Broadcast()
			mu.Unlock()
		}
	}
	// Wake the waiters when ctx is cancelled from outside.
	stop := context.AfterFunc(ctx, func() {
		mu.Lock()
		cond.Broadcast()
		mu.Unlock()
	})
	defer stop()
	for w := 0; w < max(1, min(workers, n)); w++ {
		wg.Add(1)
		go worker()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// ---- Montage --------------------------------------------------------------------------------

// Montage renders every usable segment, joins them with the transitions,
// writes subtitles and runs the final pass (music + ducking, burnt-in
// subtitles). Progress: 0–75 scenes and transitions, 78 join, 85–99 final
// pass. Old files in OutDir are cleared first (files locked by a player are
// left behind); intermediates are always removed.
func (f FF) Montage(ctx context.Context, o MontageOptions) (MontageResult, error) {
	var res MontageResult
	var pmu sync.Mutex
	progress := func(pct int, msg string) {
		if o.Progress != nil {
			pmu.Lock()
			o.Progress(pct, msg)
			pmu.Unlock()
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	cancelled := func() bool { return ctx.Err() != nil || (o.Cancelled != nil && o.Cancelled()) }

	// Resolve the material: missing files are treated as absent.
	segs := make([]Segment, len(o.Segments))
	copy(segs, o.Segments)
	usable := 0
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
		if s.Video != "" || s.Frame != "" {
			usable++
			if s.Video == "" && s.StillDur <= 0 {
				res.MissingClips++
			}
		}
	}
	if usable == 0 {
		return res, errors.New("Немає жодного кліпу чи кадру — спершу згенеруй відео")
	}
	if f.FFmpeg == "" {
		return res, ErrNotFound
	}
	if strings.TrimSpace(o.OutDir) == "" {
		return res, errors.New("не вказано папку для монтажу")
	}
	dir, err := filepath.Abs(o.OutDir)
	if err != nil {
		return res, err
	}
	if cancelled() {
		return res, ErrCancelled
	}
	// Stop every ffmpeg as soon as the job is cancelled.
	if o.Cancelled != nil {
		go func() {
			t := time.NewTicker(150 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if o.Cancelled() {
						stop()
						return
					}
				}
			}
		}()
	}
	fail := func(err error) (MontageResult, error) {
		if cancelled() {
			return MontageResult{}, ErrCancelled
		}
		return MontageResult{}, err
	}

	burn := o.Subtitles == "burn"
	if burn && !f.hasFilter(ctx, "subtitles") {
		return res, errors.New("Ця збірка FFmpeg не вміє вшивати субтитри (немає libass) — обери субтитри файлом .srt або встанови повну збірку FFmpeg")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, fmt.Errorf("не вдалося створити папку монтажу: %w", err)
	}
	// Clear old intermediates/outputs; a file still open in a player (Windows lock) is simply left behind.
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}

	// Probe what the plan needs: clip lengths and sound tracks, voice lengths.
	amb := clamp01(o.AmbientVolume) > 0
	infos := make([]ProbeInfo, len(segs))
	hasAmb := make([]bool, len(segs))
	firstClip := -1
	for i, s := range segs {
		if s.Video != "" {
			firstClip = i
			break
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range segs {
		s := &segs[i]
		needVideo := s.Video != "" && (s.VideoDur <= 0 || amb || s.ClipVolume > 0 || i == firstClip)
		needVoice := s.Voice != "" && s.VoiceDur <= 0
		if !needVideo && !needVoice {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			if needVideo {
				if info, err := f.Probe(ctx, s.Video); err == nil {
					infos[i], hasAmb[i] = info, info.HasAudio
					if s.VideoDur <= 0 {
						s.VideoDur = info.Duration
					}
				}
			}
			if needVoice {
				s.VoiceDur = f.probeDuration(ctx, s.Voice)
			}
		}()
	}
	wg.Wait()
	if cancelled() {
		return res, ErrCancelled
	}

	// Output size from the first real clip (keeps 1080p/4K), else from the format.
	W, H := 1280, 720
	if o.Aspect == "9:16" {
		W, H = 720, 1280
	}
	if firstClip >= 0 && infos[firstClip].Width > 1 && infos[firstClip].Height > 1 {
		W = infos[firstClip].Width - infos[firstClip].Width%2
		H = infos[firstClip].Height - infos[firstClip].Height%2
	}

	plan := o
	plan.Segments = segs
	slots, total := Layout(plan)
	look, _ := FilterByID(o.Filter)
	j := &montageJob{f: f, o: &plan, segs: segs, amb: hasAmb, slots: slots, dir: dir, W: W, H: H, look: look}

	nTr := 0
	var temps []string
	for k, sl := range slots {
		temps = append(temps, j.body(k), j.head(k), j.tail(k), j.pcm(k), j.voice(k))
		if sl.out > 0 {
			nTr++
			temps = append(temps, j.trans(k))
		}
	}
	list := filepath.Join(dir, "list.txt")
	body := filepath.Join(dir, "body.mp4")
	sound := filepath.Join(dir, "sound.wav")
	key := filepath.Join(dir, "voices.wav")
	temps = append(temps, list, body, sound, key)
	defer func() {
		for _, t := range temps {
			_ = os.Remove(t) // in use — ignore
		}
	}()
	if nTr > 0 && !f.hasFilter(ctx, "xfade") {
		return res, errors.New("Для цих переходів потрібен FFmpeg 4.3 або новіший — онови FFmpeg або обери «Затемнення»")
	}

	// 1–2. Scenes and transitions.
	n := len(slots)
	workers := o.Workers
	if workers <= 0 {
		workers = autoWorkers(W, H)
	}
	progress(0, fmt.Sprintf("Монтаж сцен · %d/%d", 1, n))
	err = j.renderAll(ctx, workers, func(scenes, transitions int) {
		pct := 75 * (scenes + transitions) / (n + nTr)
		switch {
		case scenes < n:
			progress(pct, fmt.Sprintf("Монтаж сцен · %d/%d", scenes+1, n))
		case transitions < nTr:
			progress(pct, fmt.Sprintf("Переходи між сценами · %d/%d", transitions+1, nTr))
		}
	})
	if err != nil {
		return fail(err)
	}

	// 3. Join the pictures (identical encoding → stream copy) and mix the sound.
	if cancelled() {
		return res, ErrCancelled
	}
	progress(78, "Склеюємо сцени")
	var lb strings.Builder
	for k, sl := range slots {
		for _, p := range []string{j.body(k), j.trans(k)} {
			if p == j.trans(k) && sl.out == 0 {
				continue
			}
			if lb.Len() > 0 {
				lb.WriteByte('\n')
			}
			lb.WriteString("file '" + strings.ReplaceAll(filepath.ToSlash(p), "'", `'\''`) + "'")
		}
	}
	if err := os.WriteFile(list, []byte(lb.String()), 0o644); err != nil {
		return res, err
	}
	if err := f.Run(ctx, "", "-f", "concat", "-safe", "0", "-i", list, "-c", "copy", "-an", body); err != nil {
		return fail(err)
	}
	// The sound: ambient parts at their scenes, voice lines where the editor
	// put them, limited to −1 dBFS; the voices alone key the music ducking.
	filmSamples := int(math.Round(total*fps)) * samplesPerFrame
	var bed, voices, speech []pcmPart
	for k, sl := range slots {
		if j.ambient(k) {
			part := pcmPart{j.pcm(k), sl.start * samplesPerFrame, sl.frames * samplesPerFrame}
			bed = append(bed, part)
			if segs[sl.Segment].ClipSpeech {
				speech = append(speech, part) // speech in the clip keys the ducking too
			}
		}
		if segs[sl.Segment].Voice != "" && sl.voiceLen > 0 {
			voices = append(voices, pcmPart{j.voice(k), sl.voiceAt0, sl.voiceLen})
		}
	}
	if err := mixParts(sound, filmSamples, append(bed, voices...), true); err != nil {
		return fail(err)
	}
	music := ""
	if isFile(o.MusicPath) {
		music = o.MusicPath
	}
	duck := music != "" && o.Ducking && len(voices)+len(speech) > 0
	if duck {
		if err := mixParts(key, filmSamples, append(append([]pcmPart(nil), voices...), speech...), false); err != nil {
			return fail(err)
		}
	}

	// Subtitles from the voice timings: the .srt for the user and, to burn
	// them in, the same cues as a styled ASS script (a temporary file).
	style := o.SubtitleStyle.Resolve()
	srtName, assName := "", ""
	if o.Subtitles != "none" { // like TS: anything but "none" (default "srt") writes the file
		cues := make([]srtSeg, 0, n)
		for _, sl := range slots {
			cues = append(cues, voiceCues(segs[sl.Segment], sl)...)
			cues = append(cues, captionCues(segs[sl.Segment], sl)...)
		}
		stamp := time.Now().UnixMilli()
		if text := buildSRTFor(cues, style); text != "" {
			srtName = fmt.Sprintf("subtitles-%d.srt", stamp)
			if err := os.WriteFile(filepath.Join(dir, srtName), []byte(text), 0o644); err != nil {
				return res, err
			}
			if burn {
				assName = fmt.Sprintf("subtitles-%d.ass", stamp)
				temps = append(temps, filepath.Join(dir, assName))
				if err := os.WriteFile(filepath.Join(dir, assName), []byte(buildASS(cues, style, W, H)), 0o644); err != nil {
					return res, err
				}
			}
		}
	}

	// 4. Final pass: music (+ducking) and burnt-in subtitles.
	if cancelled() {
		return res, ErrCancelled
	}
	const finalMsg = "Музика, субтитри, фінальний рендер"
	progress(85, finalMsg)
	burn = burn && assName != ""
	finalName := fmt.Sprintf("final-%d.mp4", time.Now().UnixMilli())
	args := []string{"-i", body, "-i", sound}
	// Picture and sound go through separate filter graphs: in one graph a
	// slow subtitle/encode chain next to the music mix makes FFmpeg 8 drop
	// whole packets of the voice track (non-deterministically).
	var fc []string
	amap := "1:a"
	if music != "" {
		args = append(args, "-stream_loop", "-1", "-i", music)
		fadeAt := f3(math.Max(0, total-2.5))
		fc = append(fc, "[2:a]aresample=48000,volume="+num(clamp01(o.MusicVolume))+",afade=t=out:st="+fadeAt+":d=2.5[m]")
		if duck { // the voice lines alone (wherever they were moved) duck the music
			args = append(args, "-i", key)
			fc = append(fc,
				"[m][3:a]sidechaincompress=threshold=0.02:ratio=12:attack=15:release=450[md]",
				"[1:a][md]amix=inputs=2:duration=first:normalize=0[mix]",
			)
		} else {
			fc = append(fc, "[1:a][m]amix=inputs=2:duration=first:normalize=0[mix]")
		}
		amap = "[mix]"
	}
	// Level the film to the platforms' loudness (−14 LUFS, two-pass).
	src := amap
	if !strings.HasPrefix(src, "[") {
		src = "[" + src + "]"
	}
	if m, ok := f.measureLoudness(ctx, dir, args, fc, src, total); ok {
		fc = append(fc, src+m.filter()+"[aout]")
		amap = "[aout]"
	}
	if len(fc) > 0 {
		args = append(args, "-filter_complex", strings.Join(fc, ";"))
	}
	args = append(args, "-map", "0:v", "-map", amap, "-t", f3(total))
	if burn {
		args = append(args, "-filter:v", "subtitles="+assName)
		args = append(args, encFinal...)
	} else {
		args = append(args, "-c:v", "copy")
	}
	args = append(args, "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart", finalName)
	final := filepath.Join(dir, finalName)
	last := 85
	err = f.runTimed(ctx, dir, total, func(done float64) {
		if pct := 85 + int(14*done); pct > last {
			last = pct
			progress(pct, finalMsg)
		}
	}, args...)
	if err != nil {
		_ = os.Remove(final)
		return fail(err)
	}

	res.Output = final
	if srtName != "" {
		res.SRT = filepath.Join(dir, srtName)
	}
	res.Duration = total
	res.Timeline = slots
	return res, nil
}
