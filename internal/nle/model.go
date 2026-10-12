// Package nle writes the studio's film as a timeline other editors open:
// DaVinci Resolve and Final Cut Pro (FCPXML 1.10), Premiere Pro (Final Cut
// Pro 7 XML, xmeml v4), any OpenTimelineIO reader (.otio — Kdenlive 25.04+,
// Resolve 18.5+ …), Shotcut (MLT XML) and the CMX3600 EDL fallback.
//
// A Timeline is editor-neutral and counted in whole frames of its Rate:
//
//	V1  the scenes, cut where the montage cuts (an overlapping transition is
//	    a transition centred on the cut, a dip is a dip-to-colour across it;
//	    a scene longer than its clip holds the clip's last frame as a still
//	    right after it; a scene without a clip is its frame with the motion
//	    effect as transform keyframes)
//	V2  the on-screen texts (transparent PNGs of the whole frame)
//	A1  the clips' own sound, at the film's level
//	A2… the voice lines where the montage puts them (more lanes when moved
//	    lines overlap)
//	last the music, looped over the film, its level and ducking as volume
//	    keyframes
//
// Writers never touch files: package studio collects the media into the
// export folder, probes them and builds the Timeline; the writers turn it
// into bytes. Times inside the files are exact rationals (FCPXML) or frames
// at the timeline rate (xmeml, OTIO, MLT, EDL).
package nle

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Rate is a frame rate as the fraction Num/Den frames per second
// (24/1, 30000/1001 …).
type Rate struct{ Num, Den int64 }

// Common rates.
var (
	Rate24 = Rate{24, 1}
	Rate25 = Rate{25, 1}
	Rate30 = Rate{30, 1}
)

// standard rates a probed frame rate snaps to.
var standard = []Rate{{24000, 1001}, {24, 1}, {25, 1}, {30000, 1001}, {30, 1}, {48, 1}, {50, 1}, {60000, 1001}, {60, 1}}

// RateOf snaps a measured frame rate to a standard editing rate (within
// 0.01 % — 23.976 and 24 are 0.1 % apart); anything else rounds to whole
// frames per second, and an unusable value gives 24.
func RateOf(fps float64) Rate {
	if math.IsNaN(fps) || fps < 1 || fps > 240 {
		return Rate24
	}
	for _, r := range standard {
		if math.Abs(r.FPS()-fps) <= r.FPS()*0.0001 {
			return r
		}
	}
	return Rate{int64(math.Round(fps)), 1}
}

// OK reports a usable rate.
func (r Rate) OK() bool { return r.Num > 0 && r.Den > 0 }

// FPS is the rate as a number.
func (r Rate) FPS() float64 {
	if !r.OK() {
		return 0
	}
	return float64(r.Num) / float64(r.Den)
}

// Frames is the nearest whole frame to sec seconds.
func (r Rate) Frames(sec float64) int64 {
	if !r.OK() || math.IsNaN(sec) {
		return 0
	}
	return int64(math.Round(sec * float64(r.Num) / float64(r.Den)))
}

// Seconds is the time of frame n.
func (r Rate) Seconds(n int64) float64 {
	if !r.OK() {
		return 0
	}
	return float64(n) * float64(r.Den) / float64(r.Num)
}

// Timebase is the whole-number frame rate a timecode counts (24 for 23.976).
func (r Rate) Timebase() int64 {
	if !r.OK() {
		return 24
	}
	return int64(math.Round(r.FPS()))
}

// NTSC reports a 1000/1001 rate (23.976, 29.97, 59.94).
func (r Rate) NTSC() bool { return r.Den == 1001 }

// String is "24", "23.976", "29.97".
func (r Rate) String() string {
	if r.Den == 1 {
		return fmt.Sprint(r.Num)
	}
	s := fmt.Sprintf("%.3f", r.FPS())
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s
}

// Kind of a media file.
type Kind int

const (
	// Movie is a video file (it may carry sound).
	Movie Kind = iota
	// Sound is an audio file.
	Sound
	// Picture is a still image (a frame, a hold, a transparent text).
	Picture
)

// Media is one file of the export.
type Media struct {
	ID   string // unique in the timeline ("m3"), stable for a file
	Name string // file name ("001_scene.mp4")
	Rel  string // path relative to the export folder, forward slashes ("media/001_scene.mp4")
	Abs  string // absolute path on this computer
	Kind Kind
	// Duration in seconds (0 for pictures).
	Duration float64
	// Rate of a movie ({0,0} = unknown: the timeline's).
	Rate          Rate
	Width, Height int
	HasAudio      bool
	// SampleRate and Channels of the sound (0 = unknown: 48 kHz stereo).
	SampleRate, Channels int
	// Alpha: a picture with transparency (the on-screen texts).
	Alpha bool
}

// HasVideo reports a picture track (movies and stills).
func (m *Media) HasVideo() bool { return m.Kind != Sound }

// rate is the media's frame rate, else fallback.
func (m *Media) rate(fallback Rate) Rate {
	if m.Rate.OK() {
		return m.Rate
	}
	return fallback
}

// sampleRate is the media's audio rate, else 48 kHz.
func (m *Media) sampleRate() int {
	if m.SampleRate > 0 {
		return m.SampleRate
	}
	return 48000
}

// channels is the media's channel count, else 2.
func (m *Media) channels() int {
	if m.Channels > 0 {
		return m.Channels
	}
	return 2
}

// FileURL is the absolute file:// URL of the media (percent-encoded; on
// Windows file:///C:/…).
func (m *Media) FileURL() string { return FileURL(m.Abs) }

// FileURL turns an absolute path into a file:// URL.
func FileURL(abs string) string {
	p := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" || (len(p) > 1 && p[1] == ':') {
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// Level is one volume keyframe: At frames from the clip's start, DB the gain.
type Level struct {
	At int64
	DB float64
}

// Key is one transform keyframe of a picture: At frames from the clip's
// start; Scale (1 = fills the frame), the offset of the picture's centre
// from the frame centre in pixels of the timeline frame (+X right, +Y
// down), Rotation in degrees (clockwise).
type Key struct {
	At                    int64
	Scale, X, Y, Rotation float64
}

// Clip is one piece of media on a track.
type Clip struct {
	Media *Media
	Name  string
	Start int64 // first frame on the timeline
	Len   int64 // frames
	// In is the first frame of the media used (timeline rate); 0 for pictures.
	In int64
	// Gain is the level of the clip's sound in dB (0 = as recorded); Mute
	// drops its sound (a clip whose ambience the film does not use).
	Gain float64
	Mute bool
	// Levels is volume automation (dB, frames from the clip's start); when
	// set it replaces Gain.
	Levels []Level
	// FadeIn / FadeOut ramp the picture's opacity (video) or the sound's
	// level (audio) at the clip's edges, in frames.
	FadeIn, FadeOut int64
	// Motion animates the picture's transform (nil = none).
	Motion []Key
	// Scene is the 1-based scene number (markers, names), 0 = none.
	Scene int
	// Role says what the clip is: RoleScene, RoleHold, RoleStill, RoleText, RoleSound, RoleVoice, RoleMusic.
	Role string
}

// End is the first frame after the clip.
func (c Clip) End() int64 { return c.Start + c.Len }

// Clip roles.
const (
	RoleScene = "scene" // a clip of a scene
	RoleHold  = "hold"  // a scene's last frame held after its clip
	RoleStill = "still" // a scene's frame (no clip) with its motion
	RoleText  = "text"  // on-screen text
	RoleSound = "sound" // a clip's own sound
	RoleVoice = "voice" // a voice line
	RoleMusic = "music" // the music
)

// TransKind is how a transition looks.
type TransKind int

const (
	// Dissolve: a cross dissolve over the overlap.
	Dissolve TransKind = iota
	// DipBlack / DipWhite: the outgoing clip fades to the colour, the
	// incoming one fades in from it (no overlap of pictures).
	DipBlack
	DipWhite
)

// Transition sits on the cut between two adjacent clips of a track.
type Transition struct {
	At   int64 // the cut: the incoming clip's first frame
	Len  int64 // frames, centred on the cut
	Kind TransKind
	// Source is the studio's transition id ("wipeleft" …) — what the film
	// really uses when Kind only approximates it.
	Source string
}

// From is the transition's first frame (it starts Len/2 before the cut).
func (t Transition) From() int64 { return t.At - t.Len/2 }

// To is the first frame after the transition.
func (t Transition) To() int64 { return t.From() + t.Len }

// TrackKind tells a picture track from a sound track.
type TrackKind int

const (
	VideoTrack TrackKind = iota
	AudioTrack
)

// Track is one lane of clips (sorted, never overlapping).
type Track struct {
	Name        string
	Kind        TrackKind
	Clips       []Clip
	Transitions []Transition
	// SceneSound marks the scenes' own sound (A1): each clip mirrors a V1
	// movie clip. FCPXML keeps that sound inside the scene clips (their Gain
	// and Mute) and skips this track; the other formats lay it out here.
	SceneSound bool
}

// Marker is a named point on the timeline (one per scene).
type Marker struct {
	At   int64
	Name string
}

// Timeline is the whole edit.
type Timeline struct {
	Name          string
	Rate          Rate
	Width, Height int
	SampleRate    int
	Length        int64   // frames
	Video         []Track // bottom first: V1 the scenes, V2 the texts
	Audio         []Track // A1 the clips' sound, then voice lanes, then music
	Markers       []Marker
}

// Media lists every media file used, in the order of first use (V1 → V2 → A…).
func (t *Timeline) Media() []*Media {
	seen := map[*Media]bool{}
	var out []*Media
	for _, tr := range t.Tracks() {
		for _, c := range tr.Clips {
			if c.Media != nil && !seen[c.Media] {
				seen[c.Media] = true
				out = append(out, c.Media)
			}
		}
	}
	return out
}

// Tracks is Video then Audio.
func (t *Timeline) Tracks() []Track {
	return append(append([]Track(nil), t.Video...), t.Audio...)
}

// sampleRate is the timeline's audio rate (48 kHz by default).
func (t *Timeline) sampleRate() int {
	if t.SampleRate > 0 {
		return t.SampleRate
	}
	return 48000
}

// Validate checks the invariants every writer relies on: a usable rate and
// size, clips with media, positive lengths, sorted non-overlapping clips
// inside the timeline, sources inside the media, transitions on cuts
// between two adjacent clips with the frames they need on both sides.
func (t *Timeline) Validate() error {
	if !t.Rate.OK() {
		return errors.New("timeline: no frame rate")
	}
	if t.Width <= 0 || t.Height <= 0 {
		return errors.New("timeline: no frame size")
	}
	if t.Length <= 0 {
		return errors.New("timeline: empty")
	}
	ids := map[string]*Media{}
	for ti, tr := range t.Tracks() {
		for i, c := range tr.Clips {
			where := fmt.Sprintf("track %d (%s) clip %d", ti+1, tr.Name, i+1)
			switch {
			case c.Media == nil:
				return fmt.Errorf("%s: no media", where)
			case c.Len <= 0:
				return fmt.Errorf("%s: length %d", where, c.Len)
			case c.Start < 0 || c.End() > t.Length:
				return fmt.Errorf("%s: %d…%d outside the timeline (%d)", where, c.Start, c.End(), t.Length)
			case c.In < 0:
				return fmt.Errorf("%s: source in %d", where, c.In)
			case i > 0 && c.Start < tr.Clips[i-1].End():
				return fmt.Errorf("%s overlaps the previous clip", where)
			case tr.Kind == AudioTrack && c.Media.Kind == Picture:
				return fmt.Errorf("%s: a picture on a sound track", where)
			case tr.Kind == VideoTrack && c.Media.Kind == Sound:
				return fmt.Errorf("%s: sound on a picture track", where)
			}
			if m := ids[c.Media.ID]; m != nil && m != c.Media {
				return fmt.Errorf("media id %q used twice", c.Media.ID)
			}
			ids[c.Media.ID] = c.Media
			if c.Media.Kind != Picture && c.Media.Duration > 0 {
				// The source must exist (a frame of slack for rounding).
				if have := t.Rate.Frames(c.Media.Duration); c.In+c.Len > have+1 {
					return fmt.Errorf("%s: uses %d frames of %s, it has %d", where, c.In+c.Len, c.Media.Name, have)
				}
			}
		}
		for _, x := range tr.Transitions {
			if x.Len <= 0 {
				return fmt.Errorf("track %s: transition at %d without length", tr.Name, x.At)
			}
			k := sort.Search(len(tr.Clips), func(i int) bool { return tr.Clips[i].Start >= x.At })
			if k == 0 || k >= len(tr.Clips) || tr.Clips[k].Start != x.At || tr.Clips[k-1].End() != x.At {
				return fmt.Errorf("track %s: transition at %d is not on a cut between two clips", tr.Name, x.At)
			}
			a, b := tr.Clips[k-1], tr.Clips[k]
			if x.From() < a.Start || x.To() > b.End() {
				return fmt.Errorf("track %s: transition at %d is longer than its clips", tr.Name, x.At)
			}
			if x.Kind != Dissolve {
				continue // a dip shows each side only on its own half: no handles needed
			}
			if a.Media.Kind != Picture && a.Media.Duration > 0 && a.In+a.Len+(x.To()-x.At) > t.Rate.Frames(a.Media.Duration)+1 {
				return fmt.Errorf("track %s: transition at %d: %s has no frames after the cut", tr.Name, x.At, a.Media.Name)
			}
			if b.Media.Kind != Picture && b.In < x.At-x.From() {
				return fmt.Errorf("track %s: transition at %d: %s has no frames before the cut", tr.Name, x.At, b.Media.Name)
			}
		}
	}
	return nil
}

// clipAt finds the clip of tr that covers frame f (-1 = none).
func clipAt(tr Track, f int64) int {
	for i, c := range tr.Clips {
		if c.Start <= f && f < c.End() {
			return i
		}
	}
	return -1
}

// transitionAt finds the transition of tr on the cut at frame f.
func transitionAt(tr Track, f int64) (Transition, bool) {
	for _, x := range tr.Transitions {
		if x.At == f {
			return x, true
		}
	}
	return Transition{}, false
}

// dB → linear gain (−∞ below −96 dB).
func gainOf(db float64) float64 {
	if db <= -96 {
		return 0
	}
	return math.Pow(10, db/20)
}

// levelAt is the clip's volume (dB) at a clip-local frame: its Levels
// interpolated linearly in dB, else Gain.
func (c Clip) levelAt(f int64) float64 {
	if len(c.Levels) == 0 {
		return c.Gain
	}
	if f <= c.Levels[0].At {
		return c.Levels[0].DB
	}
	for i := 1; i < len(c.Levels); i++ {
		a, b := c.Levels[i-1], c.Levels[i]
		if f <= b.At {
			if b.At == a.At {
				return b.DB
			}
			return a.DB + (b.DB-a.DB)*float64(f-a.At)/float64(b.At-a.At)
		}
	}
	return c.Levels[len(c.Levels)-1].DB
}
