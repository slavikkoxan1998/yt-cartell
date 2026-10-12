package media

// The montage timeline: where every scene lands in the final film. Layout is
// the single source of truth — Montage renders exactly this plan and the UI
// draws it (studio.Engine.Timeline) — so everything is counted in whole
// frames (1/24 s) and whole audio samples (2000 per frame at 48 kHz).
//
// Per scene (a "slot"):
//
//	|<in>|<-------------------- body -------------------->|<out>|
//	 ^ overlap with the previous scene     overlap with the next ^
//	      ^ voice starts here: in + 0.25 s
//	                             voice ends + 0.35 s tail ^ (before <out>)
//
// in/out exist only for overlapping (xfade) transitions. A voice line never
// plays inside an overlap, so it is never cut, faded or doubled: the scene is
// lengthened (holding the last clip frame) until the whole line fits between
// the two transitions. Dip transitions (to black/white) and hard cuts do not
// overlap: the scenes are simply concatenated. A slice of the user's own
// recording (Segment.OwnVoice) gets no extra breath/tail — its built-in lead
// and tail may lie under the transitions, so the speech stays continuous.

import "math"

const (
	fps             = 24
	sampleRate      = 48000
	samplesPerFrame = sampleRate / fps // 2000

	voiceDelay       = 0.25 // seconds of breath before each line
	voiceDelayFrames = 6    // voiceDelay in frames
	voiceTail        = 0.35 // seconds after the line ends
	// MontageOptions.TightLines (a breathless Shorts narrator): one frame of
	// breath and a short tail, so consecutive lines nearly touch.
	tightDelayFrames = 1
	tightTail        = 0.08
	clipLen          = 8.0 // assumed clip length when unknown; the length of a still
	minClip          = 0.5 // trimming never leaves less than this (unless the clip is shorter)
	minBodyFrames    = 2   // frames of a scene outside its overlaps, at least
	minVoiceLeft     = 0.1 // voice trims never leave less than this (unless the line is shorter)
	dipShare         = 0.6 // each side of a dip lasts 0.6 × the transition duration (0.3 s by default)
)

// Slot is one scene's place on the final timeline. All times are in seconds,
// exact multiples of one frame.
type Slot struct {
	Segment int  // index into MontageOptions.Segments
	HasClip bool // false: the still frame is used (with a motion effect)
	// Start and Duration on the final timeline. Duration includes both
	// overlaps, so an overlapping transition makes neighbours intersect.
	Start, Duration float64
	// Overlap is the time shared with the next scene (overlapping transition).
	Overlap float64
	// The scene's voice line as it plays (0 durations = no voice), after the
	// editor's trims and move:
	//   - VoiceStart/VoiceEnd/VoiceDuration: the effective line on the final
	//     timeline (the trimmed audio, moved, clamped into the film); also
	//     where its subtitles live;
	//   - VoiceDefaultStart: where the trimmed line starts without a move
	//     (VoiceShift = VoiceStart − VoiceDefaultStart);
	//   - VoiceShift: the move actually applied (the requested one clamped so
	//     the line stays inside the film);
	//   - VoiceRawDuration: the whole voice file; VoiceTrimIn/VoiceTrimOut the
	//     trims actually applied (the full audio would span
	//     [VoiceStart−VoiceTrimIn, VoiceStart−VoiceTrimIn+VoiceRawDuration]).
	VoiceStart, VoiceEnd, VoiceDuration float64
	VoiceDefaultStart, VoiceShift       float64
	VoiceRawDuration                    float64
	VoiceTrimIn, VoiceTrimOut           float64
	// ClipDuration is the clip (or still) material used after trimming; the
	// rest of Duration holds the last frame.
	ClipDuration float64
	// TrimIn/TrimOut actually applied (clamped to the clip).
	TrimIn, TrimOut float64
	// Transition is the effective transition id into the next scene ("" for
	// the last scene); Effect the effective motion effect id ("none" = none).
	Transition string
	Effect     string

	// frame-exact plan
	frames     int     // length in frames (incl. overlaps)
	start      int     // first frame on the final timeline
	in, out    int     // overlap frames with the previous / next scene
	voiceAt    int     // frame offset of the voice inside the scene
	clipFrames int     // frames taken from the clip/still before holding
	fadeIn     float64 // dip fade-in seconds at the start (0 = none)
	fadeOut    float64 // dip fade-out seconds at the end (0 = none)
	fadeInCol  string  // "black" | "white"
	fadeOutCol string
	tr         Transition
	cueStart   float64 // when the subtitles of this scene appear (absolute)
	cueSpan    float64 // how long they stay on screen
	cueLead    float64 // own voice: seconds from the line start to its first word
	voiceAt0   int     // sample (48 kHz) where the effective line starts on the film
	voiceSkip  int     // samples cut from the start of the voice file
	voiceLen   int     // samples of the effective line
}

func frameSec(n int) float64 { return float64(n) / fps }

// transitionFrames is the overlap of an xfade transition in frames.
func transitionFrames(d float64) int {
	return int(math.Round(ClampTransitionDuration(d) * fps))
}

// ClampTransitionDuration is the transition duration the montage really uses
// for a setting: 0 (or NaN) = DefaultTransitionDuration, else clamped to
// MinTransitionDuration…MaxTransitionDuration seconds.
func ClampTransitionDuration(d float64) float64 {
	if math.IsNaN(d) || d <= 0 {
		d = DefaultTransitionDuration
	}
	return math.Max(MinTransitionDuration, math.Min(MaxTransitionDuration, d))
}

func finite(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	return x
}

// clampTrims keeps at least minClip seconds (or the whole clip when it is shorter).
func clampTrims(vDur, tin, tout float64) (float64, float64) {
	keep := math.Min(minClip, vDur)
	tin = math.Max(0, math.Min(finite(tin), vDur-keep))
	tout = math.Max(0, math.Min(finite(tout), vDur-keep-tin))
	return tin, tout
}

// clampVoiceTrims keeps at least minVoiceLeft seconds of a line (all of a
// shorter one).
func clampVoiceTrims(raw, tin, tout float64) (float64, float64) {
	keep := math.Min(minVoiceLeft, raw)
	tin = math.Max(0, math.Min(finite(tin), raw-keep))
	tout = math.Max(0, math.Min(finite(tout), raw-keep-tin))
	return tin, tout
}

// projectTransition is the default transition: "" or an unknown id = hard cut.
func projectTransition(id string) Transition {
	if t, ok := TransitionByID(id); ok {
		return t
	}
	t, _ := TransitionByID("none")
	return t
}

// stillEffect is the effective motion of a still: the scene's choice, else
// the project default, else DefaultEffect.
func stillEffect(scene, project string) string {
	for _, id := range []string{scene, project} {
		if _, ok := EffectByID(id); ok {
			return id
		}
	}
	return DefaultEffect
}

// clipEffect: clips move only when the scene explicitly asks for it.
func clipEffect(scene string) string {
	if _, ok := EffectByID(scene); ok {
		return scene
	}
	return "none"
}

// Layout computes the final timeline exactly as Montage renders it, without
// touching any file: a segment is used when it has a Video or a Frame path; a
// clip of unknown length (VideoDur ≤ 0) counts as 8 s; a voice of unknown
// length (VoiceDur ≤ 0) does not lengthen the scene (Montage probes such
// files first, so pass real durations to get the same plan). total is the
// film length in seconds.
func Layout(o MontageOptions) (slots []Slot, total float64) {
	def := projectTransition(o.Transition)
	xf := transitionFrames(o.TransitionDuration)
	dip := dipShare * ClampTransitionDuration(o.TransitionDuration)
	delayFrames, lineTail := voiceDelayFrames, voiceTail
	if o.TightLines {
		delayFrames, lineTail = tightDelayFrames, tightTail
	}

	for i, s := range o.Segments {
		if s.Video != "" || s.Frame != "" {
			slots = append(slots, Slot{Segment: i, HasClip: s.Video != ""})
		}
	}
	n := len(slots)
	// Transitions: the cut after slot k belongs to slot k (its Transition, else the default).
	for k := range slots {
		sl := &slots[k]
		if k < n-1 {
			tr := def
			if t, ok := TransitionByID(o.Segments[sl.Segment].Transition); ok {
				tr = t
			}
			sl.tr, sl.Transition = tr, tr.ID
			switch tr.Kind {
			case KindXFade:
				sl.out = xf
			case KindDip:
				sl.fadeOut, sl.fadeOutCol = dip, tr.Color
			}
		}
		if k > 0 {
			prev := &slots[k-1]
			sl.in = prev.out
			if prev.tr.Kind == KindDip {
				sl.fadeIn, sl.fadeInCol = dip, prev.tr.Color
			}
		}
	}
	// The film opens from black and ends in black unless the project uses hard cuts.
	if n > 0 && def.Kind != KindCut {
		slots[0].fadeIn, slots[0].fadeInCol = dip, "black"
		slots[n-1].fadeOut, slots[n-1].fadeOutCol = dip, "black"
	}

	// Lengths: every scene has a nominal length in seconds — its clip frames,
	// or its line (breath + voice + tail) between the overlaps. The scene
	// boundaries are the nominal running times rounded to whole frames (error
	// diffusion), so the film stays within half a frame of its nominal length
	// however many scenes it has; a clip-driven scene keeps exactly its frames
	// and a voice-driven one gives up at most one frame of its 0.35 s tail.
	nominal := 0.0 // nominal start of the current scene
	at := 0        // its first frame (= round(nominal × fps))
	for k := range slots {
		sl := &slots[k]
		s := o.Segments[sl.Segment]
		vDur := clipLen
		if sl.HasClip && finite(s.VideoDur) > 0 {
			vDur = s.VideoDur
		}
		if !sl.HasClip && finite(s.StillDur) > 0 {
			vDur = math.Max(minClip, s.StillDur)
		}
		sl.TrimIn, sl.TrimOut = clampTrims(vDur, s.TrimIn, s.TrimOut)
		sl.clipFrames = max(1, int(math.Round((vDur-sl.TrimIn-sl.TrimOut)*fps)))

		raw := 0.0
		if s.Voice != "" && finite(s.VoiceDur) > 0 {
			raw = s.VoiceDur
		}
		// The editor's trims cut the audio; the scene then needs (and holds)
		// only the trimmed line. Its move is applied after the whole film is
		// laid out (it never changes the picture).
		tin, tout := clampVoiceTrims(raw, s.VoiceTrimIn, s.VoiceTrimOut)
		aDur := raw - tin - tout
		sl.VoiceRawDuration, sl.VoiceTrimIn, sl.VoiceTrimOut = raw, tin, tout
		// Where the line sits. A generated line starts after the incoming
		// overlap plus the breath — after a long dip not before the picture is
		// half faded in — and ends with its tail (at least half a long
		// fade-out) before the outgoing overlap. An own-voice slice carries its
		// lead/tail already: it starts as early as its lead can hide under the
		// incoming overlap and may leave its tail under the outgoing one, so
		// the recording's rhythm is kept and speech never meets a transition.
		tail := math.Max(lineTail, sl.fadeOut/2)
		own := s.OwnVoice && aDur > 0
		need, under, minVoice := 0.0, 0.0, 0
		sl.cueLead, sl.cueSpan = 0, aDur
		if own {
			lead0 := math.Max(0, math.Min(finite(s.VoiceLead), raw))
			pad0 := math.Max(0, math.Min(finite(s.VoiceTail), raw-lead0))
			lead, pad := math.Max(0, lead0-tin), math.Max(0, pad0-tout) // what the trims left of it
			sl.voiceAt = max(0, sl.in-int(math.Floor(lead*fps+1e-9)))
			under = math.Min(frameSec(sl.out), pad)
			need = frameSec(sl.voiceAt+sl.out) + aDur - under
			minVoice = sl.voiceAt + int(math.Ceil((aDur-under)*fps-1e-9)) + sl.out
			sl.cueLead, sl.cueSpan = lead, math.Max(0.5, aDur-lead-pad)
		} else {
			sl.voiceAt = sl.in + max(delayFrames, int(math.Round(sl.fadeIn*fps/2)))
			need = frameSec(sl.voiceAt+sl.out) + aDur + tail
			minVoice = sl.voiceAt + int(math.Ceil(aDur*fps)) + sl.out + 1
		}
		length := frameSec(max(sl.clipFrames, sl.in+sl.out+minBodyFrames))
		if aDur > 0 {
			length = math.Max(length, need)
		}
		end := int(math.Round((nominal + length) * fps))
		sl.start = at
		sl.frames = max(end-at, sl.in+sl.out+minBodyFrames)
		if aDur > 0 {
			// Never cut the line itself, whatever the rounding did.
			sl.frames = max(sl.frames, minVoice)
		}
		if sl.start+sl.frames == end {
			nominal += length - frameSec(sl.out)
		} else { // lengthened beyond the nominal: continue from the real boundary
			nominal = frameSec(sl.start + sl.frames - sl.out)
		}
		at = sl.start + sl.frames - sl.out

		if sl.HasClip {
			sl.Effect = clipEffect(s.Effect)
		} else {
			sl.Effect = stillEffect(s.Effect, o.DefaultEffect)
		}
		if aDur <= 0 {
			sl.cueSpan = math.Max(1.5, frameSec(sl.frames-sl.voiceAt-sl.out)-tail)
		}

		sl.Start, sl.Duration = frameSec(sl.start), frameSec(sl.frames)
		sl.Overlap = frameSec(sl.out)
		sl.VoiceDefaultStart, sl.VoiceDuration = frameSec(sl.start+sl.voiceAt), aDur
		sl.ClipDuration = frameSec(min(sl.clipFrames, sl.frames))
	}
	if n == 0 {
		return slots, 0
	}
	last := slots[n-1]
	total = frameSec(last.start + last.frames)
	place(slots, o.Segments, (last.start+last.frames)*samplesPerFrame)
	return slots, total
}

// place puts every voice line where the editor moved it: default start +
// VoiceShift, sample-exact, clamped so the line stays inside the film (films
// samples long). Moves never touch the picture; a moved line may run under
// neighbouring scenes and over other lines (the mix is limited to −1 dBFS).
// Subtitles follow the line.
func place(slots []Slot, segs []Segment, films int) {
	for k := range slots {
		sl := &slots[k]
		sl.VoiceStart, sl.VoiceEnd, sl.VoiceShift = sl.VoiceDefaultStart, sl.VoiceDefaultStart, 0
		sl.cueStart = sl.VoiceDefaultStart + sl.cueLead
		if sl.VoiceDuration <= 0 {
			continue
		}
		sl.voiceLen = max(1, min(films, int(math.Round(sl.VoiceDuration*sampleRate))))
		sl.voiceSkip = int(math.Round(sl.VoiceTrimIn * sampleRate))
		at := int(math.Round((sl.VoiceDefaultStart + finite(segs[sl.Segment].VoiceShift)) * sampleRate))
		sl.voiceAt0 = max(0, min(at, films-sl.voiceLen))
		sl.VoiceStart = float64(sl.voiceAt0) / sampleRate
		sl.VoiceEnd = sl.VoiceStart + sl.VoiceDuration
		sl.VoiceShift = sl.VoiceStart - sl.VoiceDefaultStart
		sl.cueStart = sl.VoiceStart + sl.cueLead
	}
}
