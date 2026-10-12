package media

// What an editor export (studio/export.go → package nle) needs from the
// montage plan beyond Slot's exported fields: the frame-exact geometry of a
// slot, its dip fades, the subtitles exactly as the montage writes them and
// the camera move of a motion effect in editor terms. Nothing here touches a
// file; Layout stays the single source of truth.

import "math"

// FPS is the montage's frame rate: every Slot time is a multiple of 1/FPS.
const FPS = fps

// SampleRate is the montage's audio rate (Hz).
const SampleRate = sampleRate

// StartFrame is the slot's first frame on the film (1/FPS s).
func (s Slot) StartFrame() int { return s.start }

// Frames is the slot's length in frames, both overlaps included.
func (s Slot) Frames() int { return s.frames }

// InFrames / OutFrames are the frames shared with the previous / next scene
// (overlapping transitions).
func (s Slot) InFrames() int  { return s.in }
func (s Slot) OutFrames() int { return s.out }

// ClipFrames is how many frames come from the clip (or the still); the rest
// of the slot holds the clip's last frame.
func (s Slot) ClipFrames() int { return min(s.clipFrames, s.frames) }

// Fades reports the slot's dip fades: seconds of fade-in at its start and
// fade-out at its end (0 = none) and their colours ("black" | "white"). The
// film's opening and closing fades from/to black are included.
func (s Slot) Fades() (in, out float64, inColor, outColor string) {
	return s.fadeIn, s.fadeOut, s.fadeInCol, s.fadeOutCol
}

// TransitionKind is the kind of the slot's transition into the next scene
// (KindCut for the last one).
func (s Slot) TransitionKind() TransitionKind {
	if s.tr.Kind == "" {
		return KindCut
	}
	return s.tr.Kind
}

// Cue is one subtitle on the final timeline (seconds); lines of a two-line
// cue are separated by "\n".
type Cue struct {
	Start, End float64
	Text       string
}

// SubtitleCues are the film's subtitles for o — exactly the cues of the .srt
// Montage writes (Uppercase from the subtitle style). Pass probed durations
// in o (like Layout).
func SubtitleCues(o MontageOptions) []Cue {
	slots, _ := Layout(o)
	segs := make([]srtSeg, 0, len(slots))
	for _, sl := range slots {
		segs = append(segs, voiceCues(o.Segments[sl.Segment], sl)...)
		segs = append(segs, captionCues(o.Segments[sl.Segment], sl)...)
	}
	var out []Cue
	st := o.SubtitleStyle.Resolve()
	for _, c := range timedCuesWith(segs, st.Uppercase, st.MaxWords) {
		out = append(out, Cue{Start: c.start, End: c.end, Text: c.text})
	}
	return out
}

// SRT is the .srt text of cues ("" when there are none), formatted like the
// montage's own file.
func SRT(cues []Cue) string {
	var b []byte
	for i, c := range cues {
		if i > 0 {
			b = append(b, '\n')
		}
		b = append(b, []byte(itoa(i+1)+"\n"+srtTime(c.Start)+" --> "+srtTime(c.End)+"\n"+c.Text+"\n")...)
	}
	return string(b)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d [20]byte
	i := len(d)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		d[i] = '-'
	}
	return string(d[i:])
}

// Move is a motion effect as an editor's transform animation of a W×H
// picture filling a W×H frame: at the scene's first frame (…0) and its last
// (…1), the picture's scale (1 = fills the frame exactly), the offset of
// its centre from the frame centre in pixels (+X right, +Y down) and its
// rotation in degrees (clockwise). Between them the montage moves linearly
// (Ease: smoothstep). A handheld shake (Shake) cannot be keyframed simply:
// only its steady zoom is kept.
type Move struct {
	Scale0, Scale1 float64
	X0, Y0, X1, Y1 float64
	Rot0, Rot1     float64
	Ease, Shake    bool
}

// At is the move at progress p (0…1 over the scene), easing applied.
func (m Move) At(p float64) (scale, x, y, rot float64) {
	p = math.Max(0, math.Min(1, p))
	if m.Ease {
		p = p * p * (3 - 2*p)
	}
	lerp := func(a, b float64) float64 { return a + (b-a)*p }
	return lerp(m.Scale0, m.Scale1), lerp(m.X0, m.X1), lerp(m.Y0, m.Y1), lerp(m.Rot0, m.Rot1)
}

// EffectMove is the editor form of motion effect id over a W×H picture
// (clip: the half-strength move a clip gets); false when it does not move
// the picture ("none", unknown ids).
//
// The montage shows a window of 1/z of the frame centred at
// c = centre + x·(W/2)(1−1/z) (the same for y), rotated by θ. Seen from the
// frame, the picture is scaled by z, turned by −θ and shifted so that c
// lands in the centre: offset = −R(−θ)·(x·(W/2)(z−1), y·(H/2)(z−1)).
func EffectMove(id string, clip bool, W, H int) (Move, bool) {
	m, ok := motions[id]
	if !ok || W <= 0 || H <= 0 {
		return Move{}, false
	}
	if clip {
		m = m.scaled(clipStrength)
	}
	m = m.safe(W, H)
	at := func(z, x, y, r float64) (float64, float64, float64) {
		dx, dy := x*float64(W)/2*(z-1), y*float64(H)/2*(z-1)
		th := -r * math.Pi / 180
		ox := -(dx*math.Cos(th) - dy*math.Sin(th))
		oy := -(dx*math.Sin(th) + dy*math.Cos(th))
		return ox, oy, -r
	}
	mv := Move{Scale0: m.z0, Scale1: m.z1, Ease: m.ease, Shake: m.shake > 0}
	mv.X0, mv.Y0, mv.Rot0 = at(m.z0, m.x0, m.y0, m.r0)
	mv.X1, mv.Y1, mv.Rot1 = at(m.z1, m.x1, m.y1, m.r1)
	return mv, true
}
