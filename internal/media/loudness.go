package media

// Loudness levelling of the final film: YouTube, TikTok, Instagram and most
// players normalise to about −14 LUFS, so a quieter film plays quiet next to
// everything else. FFmpeg's loudnorm (EBU R128) measures the mix, then one
// linear gain (capped by the true peak) levels it, so the mix (voice over
// ducked music) keeps its dynamics.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"ytcartell/internal/sysx"
)

const (
	loudTarget = -14.0 // integrated loudness, LUFS
	loudPeak   = -1.5  // true peak, dBTP
	loudRange  = 11.0  // loudness range, LU
)

// loudMeasure is loudnorm's first-pass report.
type loudMeasure struct {
	I, TP, LRA, Thresh, Offset float64
}

// gain is the one linear gain (dB) that brings the film to the target
// without pushing its true peak over loudPeak — so nothing is compressed
// (loudnorm's own second pass turns dynamic when the peak limit binds,
// which pumps quiet parts up). A peaky mix stays a little under the target.
func (m loudMeasure) gain() float64 {
	g := math.Min(loudTarget-m.I, loudPeak-m.TP)
	return math.Max(-20, math.Min(20, g))
}

// filter is the levelling filter: a plain volume change.
func (m loudMeasure) filter() string {
	return "volume=" + f2(m.gain()) + "dB"
}

func f2(x float64) string { return strconv.FormatFloat(x, 'f', 2, 64) }

// measureLoudness runs the first pass over the film's audio graph: inputs
// are the ffmpeg input arguments, graph the filter chains so far and src the
// label of the finished mix ("[1:a]", "[mix]"). ok is false when the film is
// silent or FFmpeg could not tell — the caller then leaves the level alone.
func (f FF) measureLoudness(ctx context.Context, dir string, inputs, graph []string, src string, total float64) (loudMeasure, bool) {
	if f.FFmpeg == "" {
		return loudMeasure{}, false
	}
	fc := append(append([]string(nil), graph...),
		src+fmt.Sprintf("loudnorm=I=%s:TP=%s:LRA=%s:print_format=json[ln]", num(loudTarget), num(loudPeak), num(loudRange)))
	args := append([]string{"-hide_banner", "-nostdin", "-y"}, inputs...)
	args = append(args, "-filter_complex", strings.Join(fc, ";"), "-map", "[ln]", "-t", f3(total), "-f", "null", "-")
	cmd := command(ctx, f.FFmpeg, args...)
	cmd.Dir = dir
	out := &tailBuffer{max: 64 << 10}
	cmd.Stderr = out
	if err := sysx.Run(cmd); err != nil {
		return loudMeasure{}, false
	}
	return parseLoudnorm(out.Tail(64 << 10))
}

// parseLoudnorm reads the JSON block loudnorm prints at the end of a run.
func parseLoudnorm(s string) (loudMeasure, bool) {
	i := strings.LastIndex(s, `"input_i"`)
	if i < 0 {
		return loudMeasure{}, false
	}
	start := strings.LastIndex(s[:i], "{")
	end := strings.Index(s[i:], "}")
	if start < 0 || end < 0 {
		return loudMeasure{}, false
	}
	var raw map[string]string
	if json.Unmarshal([]byte(s[start:i+end+1]), &raw) != nil {
		return loudMeasure{}, false
	}
	get := func(k string) (float64, bool) {
		v, err := strconv.ParseFloat(strings.TrimSpace(raw[k]), 64)
		return v, err == nil && !math.IsInf(v, 0) && !math.IsNaN(v)
	}
	var m loudMeasure
	var ok [5]bool
	m.I, ok[0] = get("input_i")
	m.TP, ok[1] = get("input_tp")
	m.LRA, ok[2] = get("input_lra")
	m.Thresh, ok[3] = get("input_thresh")
	m.Offset, ok[4] = get("target_offset")
	for _, b := range ok {
		if !b {
			return loudMeasure{}, false
		}
	}
	if m.I < -70 { // silence: nothing to level
		return loudMeasure{}, false
	}
	return m, true
}
