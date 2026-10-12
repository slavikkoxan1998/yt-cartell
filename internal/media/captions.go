package media

// Subtitles of speech inside the clips (films whose characters speak on
// camera): Segment.Captions are timed in the clip; on the film they move with
// the scene and lose what the trims cut away.

import (
	"math"
	"strings"
)

// captionCues places a segment's captions on the final timeline (nothing for
// a scene shown as a still: its clip — and the speech — is missing).
func captionCues(s Segment, sl Slot) []srtSeg {
	if !sl.HasClip || len(s.Captions) == 0 {
		return nil
	}
	from, to := sl.TrimIn, sl.TrimIn+sl.ClipDuration
	var out []srtSeg
	for _, c := range s.Captions {
		text := strings.TrimSpace(c.Text)
		a, b := math.Max(finite(c.Start), from), math.Min(finite(c.End), to)
		if text == "" || b-a < 0.2 {
			continue
		}
		out = append(out, srtSeg{narration: text, start: sl.Start + a - from, span: b - a})
	}
	return out
}
