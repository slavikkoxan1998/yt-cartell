package media

// Multi-voice voice-overs: the parts of a scene, each voiced with its
// speaker's voice, joined into one line with short natural pauses (the
// montage then places the line like any other voice), and their subtitles
// placed at the parts' own times (voicePartCues).

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// JoinVoices joins the voice files parts, in order, into one MP3 at out
// (44.1 kHz mono): gaps[i] seconds of silence go before part i (gaps[0] and
// missing entries = none). start/end are where each part sits in the result
// (seconds) — each part keeps its probed length exactly.
func (f FF) JoinVoices(ctx context.Context, parts []string, gaps []float64, out string) (start, end []float64, err error) {
	if len(parts) == 0 {
		return nil, nil, errors.New("немає частин озвучки")
	}
	if f.FFmpeg == "" {
		return nil, nil, ErrNotFound
	}
	durs := make([]float64, len(parts))
	for i, p := range parts {
		info, err := f.Probe(ctx, p)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			return nil, nil, fmt.Errorf("частина озвучки %d: %w", i+1, err)
		}
		if !(info.Duration > 0) {
			return nil, nil, fmt.Errorf("частина озвучки %d порожня", i+1)
		}
		durs[i] = info.Duration
	}
	sec := func(x float64) string { return strconv.FormatFloat(x, 'f', 3, 64) }
	args := make([]string, 0, 2*len(parts)+12)
	var graph, chain []string
	start, end = make([]float64, len(parts)), make([]float64, len(parts))
	at := 0.0
	for i, p := range parts {
		args = append(args, "-i", p)
		if g := gapAt(gaps, i); i > 0 && g > 0 {
			label := fmt.Sprintf("[g%d]", i)
			graph = append(graph, fmt.Sprintf("aevalsrc=0:c=mono:s=44100:d=%s%s", sec(g), label))
			chain = append(chain, label)
			at += g
		}
		label := fmt.Sprintf("[a%d]", i)
		graph = append(graph, fmt.Sprintf("[%d:a]aresample=44100,aformat=sample_fmts=fltp:channel_layouts=mono,atrim=end=%s,asetpts=N/SR/TB%s", i, sec(durs[i]), label))
		chain = append(chain, label)
		start[i], end[i] = at, at+durs[i]
		at += durs[i]
	}
	graph = append(graph, fmt.Sprintf("%sconcat=n=%d:v=0:a=1[out]", strings.Join(chain, ""), len(chain)))
	args = append(args, "-filter_complex", strings.Join(graph, ";"), "-map", "[out]",
		"-ar", "44100", "-ac", "1", "-c:a", "libmp3lame", "-b:a", "192k", out)
	if err := f.Run(ctx, "", args...); err != nil {
		return nil, nil, err
	}
	return start, end, nil
}

// gapAt is the pause before part i (0 when unset or not a sane number).
func gapAt(gaps []float64, i int) float64 {
	if i >= len(gaps) {
		return 0
	}
	g := gaps[i]
	if math.IsNaN(g) || math.IsInf(g, 0) || g < 0 {
		return 0
	}
	return math.Min(g, 5)
}

// VoicePart is one speaker's part of a multi-voice line, timed in the voice
// file (seconds from its start, before the editor's trims). Color is the
// burnt-in subtitle colour of its speaker (#RRGGBB, "" = the style's).
type VoicePart struct {
	Text       string
	Start, End float64
	Color      string
}

// voiceCues are a scene's voice-over subtitles: its narration spread over
// the line, or — for a multi-voice line with timed parts — each part at its
// own time (moved and trimmed with the line).
func voiceCues(s Segment, sl Slot) []srtSeg {
	if parts := voicePartCues(s, sl); len(parts) > 0 {
		return parts
	}
	return []srtSeg{{narration: s.Narration, start: sl.cueStart, span: sl.cueSpan}}
}

// voicePartCues places the timed parts of a voiced line on the final timeline
// (nil when the line has no voice or its parts are not timed).
func voicePartCues(s Segment, sl Slot) []srtSeg {
	if len(s.VoiceParts) == 0 || sl.VoiceDuration <= 0 {
		return nil
	}
	origin := sl.VoiceStart - sl.VoiceTrimIn // where the voice file's 0 lies on the film
	var out []srtSeg
	for _, p := range s.VoiceParts {
		text := strings.TrimSpace(p.Text)
		a, b := finite(p.Start), finite(p.End)
		if text == "" || !(b > a) {
			return nil // an untimed part: the whole narration instead
		}
		a, b = math.Max(origin+a, sl.VoiceStart), math.Min(origin+b, sl.VoiceEnd)
		if b-a < 0.2 {
			continue // trimmed away
		}
		out = append(out, srtSeg{narration: text, start: a, span: b - a, color: p.Color})
	}
	return out
}
