package media

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---- fixtures ---------------------------------------------------------------------------------

// kit makes ffmpeg-generated media in a per-test temp folder.
type kit struct {
	t   *testing.T
	f   FF
	dir string
}

func newKit(t *testing.T) *kit {
	t.Helper()
	return &kit{t: t, f: locate(t), dir: t.TempDir()}
}

func (k *kit) make(name string, args ...string) string {
	k.t.Helper()
	p := filepath.Join(k.dir, name)
	if err := k.f.Run(context.Background(), "", append(args, p)...); err != nil {
		k.t.Fatalf("fixture %s: %v", name, err)
	}
	return p
}

// lavfi adds size (and rate) to the first filter of a lavfi source graph,
// e.g. "testsrc2" or "color=c=black,drawbox=…".
func lavfi(src string, w, h int, rate bool) string {
	first, rest, chained := strings.Cut(src, ",")
	sep := "="
	if strings.Contains(first, "=") {
		sep = ":"
	}
	first += fmt.Sprintf("%ssize=%dx%d", sep, w, h)
	if rate {
		first += ":rate=24"
	}
	if chained {
		return first + "," + rest
	}
	return first
}

// clip: a lavfi picture source with a sine sound track.
func (k *kit) clip(name, src string, w, h int, dur, tone float64) string {
	return k.make(name, "-f", "lavfi", "-i", lavfi(src, w, h, true),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=%g:sample_rate=48000", tone),
		"-t", f3(dur), "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
}

func (k *kit) still(name, src string, w, h int) string {
	return k.make(name, "-f", "lavfi", "-i", lavfi(src, w, h, false), "-frames:v", "1")
}

// voice: an 880 Hz "line" (easy to find again in the final mix).
func (k *kit) voice(name string, dur float64) string {
	return k.make(name, "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=880:sample_rate=44100:duration=%s", f3(dur)), "-c:a", "libmp3lame", "-b:a", "96k")
}

func (k *kit) music() string {
	return k.make("music.mp3", "-f", "lavfi", "-i", "sine=frequency=220:sample_rate=44100:duration=1.5", "-c:a", "libmp3lame", "-b:a", "96k")
}

// ---- probes -------------------------------------------------------------------------------------

func countFrames(t *testing.T, f FF, path string) int {
	t.Helper()
	out, err := exec.Command(f.FFprobe, "-v", "error", "-count_frames", "-select_streams", "v:0",
		"-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("count frames: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.Trim(strings.TrimSpace(string(out)), ",")))
	if err != nil {
		t.Fatalf("count frames %q: %v", out, err)
	}
	return n
}

var reSilenceEnd = regexp.MustCompile(`silence_end: (-?[0-9.]+)`)

// onsets returns the times where a tone around freq Hz starts in the file.
func onsets(t *testing.T, f FF, path string, freq int) []float64 {
	t.Helper()
	// The film is levelled to −14 LUFS, so the threshold follows the band's
	// own peak (the tones peak about 17 dB above the old fixed −35 dB).
	band := fmt.Sprintf("bandpass=f=%d:width_type=h:width=150", freq)
	vd, err := exec.Command(f.FFmpeg, "-hide_banner", "-nostats", "-i", path, "-vn", "-af", band+",volumedetect", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("volumedetect: %v: %s", err, vd)
	}
	thr := -35.0
	if m := regexp.MustCompile(`max_volume: (-?[0-9.]+) dB`).FindStringSubmatch(string(vd)); m != nil {
		if mx, err := strconv.ParseFloat(m[1], 64); err == nil {
			thr = mx - 17
		}
	}
	out, err := exec.Command(f.FFmpeg, "-hide_banner", "-nostats", "-i", path, "-vn",
		"-af", fmt.Sprintf("%s,silencedetect=n=%.1fdB:d=0.12", band, thr), "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("silencedetect: %v\n%s", err, out)
	}
	info, err := f.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	var ts []float64
	for _, m := range reSilenceEnd.FindAllStringSubmatch(string(out), -1) {
		// silencedetect also "ends" the silence at the end of the stream: not an onset.
		if v, _ := strconv.ParseFloat(m[1], 64); v < info.Duration-0.05 {
			ts = append(ts, v)
		}
	}
	return ts
}

// grayFrames decodes every frame of a video as w×h 8-bit gray.
func grayFrames(t *testing.T, f FF, path string, w, h int) [][]byte {
	t.Helper()
	out, err := exec.Command(f.FFmpeg, "-hide_banner", "-loglevel", "error", "-i", path, "-an",
		"-vf", fmt.Sprintf("scale=%d:%d:flags=area,format=gray", w, h), "-f", "rawvideo", "-").Output()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var fr [][]byte
	for len(out) >= w*h {
		fr, out = append(fr, out[:w*h]), out[w*h:]
	}
	return fr
}

// frameAt decodes one frame at sec as w×h pixels of pixFmt (gray | rgb24).
func frameAt(t *testing.T, f FF, path string, sec float64, w, h int, pixFmt string) []byte {
	t.Helper()
	out, err := exec.Command(f.FFmpeg, "-hide_banner", "-loglevel", "error", "-ss", f3(sec), "-i", path, "-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d:flags=area,format=%s", w, h, pixFmt), "-f", "rawvideo", "-").Output()
	if err != nil {
		t.Fatalf("frame at %.2f: %v", sec, err)
	}
	return out
}

func meanAbsDiff(a, b []byte) float64 {
	n := min(len(a), len(b))
	s := 0.0
	for i := 0; i < n; i++ {
		s += math.Abs(float64(a[i]) - float64(b[i]))
	}
	return s / float64(max(n, 1))
}

func mean(b []byte) float64 {
	s := 0.0
	for _, v := range b {
		s += float64(v)
	}
	return s / float64(max(len(b), 1))
}

// checkFilm verifies the file against the rendered plan: exact frame count,
// duration, and the plan equals Layout of the resolved segments.
func checkFilm(t *testing.T, f FF, res MontageResult) ProbeInfo {
	t.Helper()
	if len(res.Timeline) == 0 {
		t.Fatal("no timeline")
	}
	last := res.Timeline[len(res.Timeline)-1]
	if !near(res.Duration, last.Start+last.Duration, 1e-9) {
		t.Fatalf("duration %v, timeline ends at %v", res.Duration, last.Start+last.Duration)
	}
	want := int(math.Round(res.Duration * fps))
	if got := countFrames(t, f, res.Output); got != want {
		t.Fatalf("frames %d, want %d", got, want)
	}
	info, err := f.Probe(context.Background(), res.Output)
	if err != nil {
		t.Fatal(err)
	}
	if !near(info.Duration, res.Duration, 0.05) || !info.HasAudio {
		t.Fatalf("file %+v, want %.3f s", info, res.Duration)
	}
	return info
}

// ---- tests ----------------------------------------------------------------------------------------

// A small project with every feature at once: an overlapping transition, a
// dip, trims, a still with a camera move, a clip with an explicit effect, a
// colour look, burnt styled subtitles, music with ducking.
func TestMontageMixedFeatures(t *testing.T) {
	k := newKit(t)
	f := k.f
	a := k.clip("a.mp4", "testsrc2", 320, 240, 2, 440)
	b := k.clip("b.mp4", "smptehdbars", 320, 240, 3, 330)
	still := k.still("still.png", "testsrc2", 480, 360)
	v1, v2, v3 := k.voice("v1.mp3", 1.2), k.voice("v2.mp3", 1.5), k.voice("v3.mp3", 2.0)
	subs := "burn"
	if !hasFilter(f, "subtitles") {
		subs = "srt"
	}
	opts := MontageOptions{
		Segments: []Segment{
			{Video: a, Voice: v1, Narration: "Перша сцена. Лисиця виходить з туману.", TrimIn: 0.25, Transition: "dissolve"},
			{Frame: still, Voice: v2, Narration: "Друга сцена — кадр із рухом камери.", Effect: "panright", Transition: "wipeleft"},
			{Video: b, Voice: v3, Narration: "Третя сцена завершує історію.", TrimOut: 0.5, Effect: "zoomin"},
		},
		Aspect: "16:9", Transition: "fade", TransitionDuration: 0.5,
		Filter: "cinematic", DefaultEffect: "zoomin",
		Subtitles: subs, SubtitleStyle: SubtitleStyle{Preset: "youtube", Uppercase: true},
		MusicPath: k.music(), MusicVolume: 0.3, Ducking: true, AmbientVolume: 0.3,
		OutDir: filepath.Join(k.dir, "out"),
	}
	res, err := f.Montage(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	info := checkFilm(t, f, res)
	if info.Width != 320 || info.Height != 240 || res.MissingClips != 1 {
		t.Fatalf("%+v missing %d", info, res.MissingClips)
	}
	tl := res.Timeline
	if len(tl) != 3 || tl[0].Transition != "dissolve" || tl[1].Transition != "wipeleft" || tl[2].Transition != "" {
		t.Fatalf("timeline %+v", tl)
	}
	if tl[0].Effect != "none" || tl[1].Effect != "panright" || tl[2].Effect != "zoomin" || tl[0].TrimIn != 0.25 || tl[2].TrimOut != 0.5 {
		t.Fatalf("effects/trims %+v", tl)
	}
	// The same plan as Layout with the probed durations (what the UI draws).
	resolved := opts
	resolved.Segments = append([]Segment(nil), opts.Segments...)
	for i := range resolved.Segments {
		s := &resolved.Segments[i]
		if s.Video != "" {
			s.VideoDur = f.probeDuration(context.Background(), s.Video)
		}
		s.VoiceDur = f.probeDuration(context.Background(), s.Voice)
	}
	plan, total := Layout(resolved)
	if total != res.Duration {
		t.Fatalf("Layout total %v, rendered %v", total, res.Duration)
	}
	for i := range plan {
		p, r := plan[i], tl[i]
		if p.Start != r.Start || p.Duration != r.Duration || p.VoiceStart != r.VoiceStart || p.Overlap != r.Overlap {
			t.Fatalf("slot %d: layout %+v, rendered %+v", i, p, r)
		}
	}
	// Overlaps never hold a voice.
	for i, s := range tl {
		in := 0.0
		if i > 0 {
			in = tl[i-1].Overlap
		}
		if s.VoiceStart < s.Start+in+voiceDelay-1e-9 || s.VoiceStart+s.VoiceDuration > s.Start+s.Duration-s.Overlap-0.3 {
			t.Fatalf("voice %d inside a transition: %+v", i, s)
		}
	}
	// Subtitles: uppercase, first cue of every scene = its voice start.
	b2, err := os.ReadFile(res.SRT)
	if err != nil {
		t.Fatal(err)
	}
	srt := string(b2)
	if !strings.Contains(srt, "ПЕРША СЦЕНА.") || strings.Contains(srt, "Перша") {
		t.Fatalf("srt not uppercase:\n%s", srt)
	}
	cues := srtRe.FindAllStringSubmatch(srt, -1)
	if len(cues) != 3 {
		t.Fatalf("cues:\n%s", srt)
	}
	for i, c := range cues {
		if at := parseSRTTime(c[2]); !near(at, tl[i].VoiceStart, 0.001) {
			t.Fatalf("cue %d at %.3f, voice at %.3f", i+1, at, tl[i].VoiceStart)
		}
	}
	// The voices really are where the timeline says (880 Hz onsets in the final mix).
	on := onsets(t, f, res.Output, 880)
	if len(on) != 3 {
		t.Fatalf("voice onsets %v, want 3 (timeline %+v)", on, tl)
	}
	for i, at := range on {
		if !near(at, tl[i].VoiceStart, 0.03) {
			t.Fatalf("voice %d starts at %.3f, timeline says %.3f", i+1, at, tl[i].VoiceStart)
		}
	}
}

// Every transition of the catalog renders, frame-exact.
func TestMontageAllTransitions(t *testing.T) {
	k := newKit(t)
	f := k.f
	a := k.clip("a.mp4", "testsrc2", 160, 120, 2, 440)
	b := k.clip("b.mp4", "smptehdbars", 160, 120, 2, 330)
	var segs []Segment
	for i, tr := range Transitions {
		clip := a
		if i%2 == 1 {
			clip = b
		}
		segs = append(segs, Segment{Video: clip, TrimOut: 1, Transition: tr.ID})
	}
	segs = append(segs, Segment{Video: a, TrimOut: 1})
	start := time.Now()
	res, err := f.Montage(context.Background(), MontageOptions{
		Segments: segs, Transition: "none", TransitionDuration: 0.25, Subtitles: "none", AmbientVolume: 0.5,
		OutDir: filepath.Join(k.dir, "out"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d scenes, %d transitions: %v", len(segs), len(Transitions), time.Since(start).Round(time.Millisecond))
	checkFilm(t, f, res)
	tl := res.Timeline
	for i, tr := range Transitions {
		if tl[i].Transition != tr.ID || (tl[i].Overlap > 0) != tr.Overlaps() {
			t.Fatalf("scene %d: %+v for %q", i, tl[i], tr.ID)
		}
	}
	// Mid-transition frames are really blends: unlike the frame just before
	// the overlap and the one just after it.
	for i, tr := range Transitions {
		if !tr.Overlaps() {
			continue
		}
		s := tl[i]
		end := s.Start + s.Duration
		before := frameAt(t, f, res.Output, end-s.Overlap-1.5/fps, 80, 60, "gray")
		mid := frameAt(t, f, res.Output, end-s.Overlap/2, 80, 60, "gray")
		after := frameAt(t, f, res.Output, end+0.5/fps, 80, 60, "gray")
		if d1, d2 := meanAbsDiff(mid, before), meanAbsDiff(mid, after); d1 < 2 || d2 < 2 {
			t.Errorf("%s: mid-transition frame too close to a neighbour (%.2f, %.2f)", tr.ID, d1, d2)
		}
	}
}

// Trimming a clip keeps picture and sound together: a flash and a beep that
// coincide in the source coincide in the film.
func TestTrimKeepsSync(t *testing.T) {
	k := newKit(t)
	f := k.f
	a := k.clip("a.mp4", "testsrc2", 320, 240, 2, 440)
	sync := k.make("sync.mp4",
		"-f", "lavfi", "-i", "color=black:size=320x240:rate=24:duration=3,drawbox=enable='eq(n,24)':color=white:t=fill",
		"-f", "lavfi", "-i", "aevalsrc=exprs='if(between(t,1,1.1),0.5*sin(2*PI*1500*t),0)':s=48000:d=3",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
	res, err := f.Montage(context.Background(), MontageOptions{
		Segments: []Segment{
			{Video: a, Transition: "dissolve"},
			{Video: sync, TrimIn: 0.25, TrimOut: 0.5},
		},
		Transition: "none", TransitionDuration: 0.5, Subtitles: "none", AmbientVolume: 1,
		OutDir: filepath.Join(k.dir, "out"),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkFilm(t, f, res)
	s := res.Timeline[1]
	if s.TrimIn != 0.25 || s.ClipDuration != 2.25 {
		t.Fatalf("slot %+v", s)
	}
	// Source flash at 1.0 s, trimmed by 0.25 s → 0.75 s into the scene.
	want := s.Start + 0.75
	flash := -1
	for i, fr := range grayFrames(t, f, res.Output, 32, 24) {
		if mean(fr) > 150 {
			if flash >= 0 {
				t.Fatalf("second flash at frame %d", i)
			}
			flash = i
		}
	}
	if flash < 0 || !near(frameSec(flash), want, 1e-9) {
		t.Fatalf("flash at frame %d (%.3f s), want %.3f s", flash, frameSec(flash), want)
	}
	on := onsets(t, f, res.Output, 1500)
	if len(on) != 1 || !near(on[0], want, 0.03) {
		t.Fatalf("beep at %v, want %.3f", on, want)
	}
}

// bandRMS is the RMS (16-bit units) of the freq band of path between from and to.
func bandRMS(t *testing.T, f FF, path string, freq, width int, from, to float64) float64 {
	t.Helper()
	raw, err := exec.Command(f.FFmpeg, "-hide_banner", "-loglevel", "error", "-i", path, "-vn",
		"-af", fmt.Sprintf("bandpass=f=%d:width_type=h:width=%d,aformat=sample_fmts=s16:channel_layouts=mono", freq, width),
		"-ar", "48000", "-f", "s16le", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	s, n := 0.0, 0.0
	for i := int(from * 48000); i < int(to*48000) && 2*i+1 < len(raw); i++ {
		x := float64(int16(uint16(raw[2*i]) | uint16(raw[2*i+1])<<8))
		s, n = s+x*x, n+1
	}
	return math.Sqrt(s / math.Max(n, 1))
}

// The editor's voice moves and trims: the lines play where the timeline says
// (clamped into the film), the picture does not move, subtitles and the
// music ducking follow the lines.
func TestVoiceMoveAndTrim(t *testing.T) {
	k := newKit(t)
	f := k.f
	a := k.clip("a.mp4", "testsrc2", 320, 240, 2, 440)
	// Two "words": tone 0–0.5 s and 1–1.5 s.
	words := k.make("words.mp3", "-f", "lavfi", "-i", "aevalsrc=exprs='if(lt(t,0.5)+between(t,1,1.5),0.125*sin(2*PI*880*t),0)':s=44100:d=1.5",
		"-c:a", "libmp3lame", "-b:a", "96k")
	plain := k.voice("plain.mp3", 1.5)
	segs := []Segment{
		// Head trimmed into the pause (0.25 s of it left), moved before the film start → clamped to 0.
		{Video: a, Voice: words, Narration: "Перший.", VoiceTrimIn: 0.75, VoiceShift: -1},
		{Video: a, Voice: plain, Narration: "Другий.", VoiceShift: 0.5},
		{Video: a, Voice: plain, Narration: "Третій.", VoiceShift: 3}, // past the end → clamped
	}
	opts := MontageOptions{Segments: segs, Transition: "none", Subtitles: "srt", AmbientVolume: 0.3,
		MusicPath: k.music(), MusicVolume: 0.5, Ducking: true, OutDir: filepath.Join(k.dir, "out")}
	res, err := f.Montage(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	checkFilm(t, f, res)
	tl := res.Timeline
	total := res.Duration
	// Placement.
	if tl[0].VoiceStart != 0 || !near(tl[0].VoiceShift, -tl[0].VoiceDefaultStart, 1e-9) || !near(tl[0].VoiceDuration, 0.75, 0.01) || tl[0].VoiceTrimIn != 0.75 {
		t.Fatalf("line 1 %+v", tl[0])
	}
	if !near(tl[1].VoiceStart, tl[1].VoiceDefaultStart+0.5, 1.0/sampleRate) || tl[1].VoiceStart < tl[1].Start+tl[1].Duration-1.5 {
		t.Fatalf("line 2 %+v", tl[1])
	}
	if !near(tl[2].VoiceEnd, total, 1.0/sampleRate) || tl[2].VoiceShift >= 3 {
		t.Fatalf("line 3 %+v (film %.3f)", tl[2], total)
	}
	// The picture is where it would be without the moves.
	resolved := opts
	resolved.Segments = append([]Segment(nil), segs...)
	for i := range resolved.Segments {
		s := &resolved.Segments[i]
		s.VideoDur, s.VoiceDur, s.VoiceShift = f.probeDuration(context.Background(), s.Video), f.probeDuration(context.Background(), s.Voice), 0
	}
	still, stillTotal := Layout(resolved)
	if stillTotal != total {
		t.Fatalf("moves changed the film: %.4f vs %.4f", total, stillTotal)
	}
	for i := range still {
		if still[i].Start != tl[i].Start || still[i].Duration != tl[i].Duration {
			t.Fatalf("scene %d moved: %+v vs %+v", i+1, still[i], tl[i])
		}
	}
	// The voices sound where the timeline says (line 1: its word 0.25 s in).
	on := onsets(t, f, res.Output, 880)
	want := []float64{tl[0].VoiceStart + 0.25, tl[1].VoiceStart, tl[2].VoiceStart}
	if len(on) != 3 {
		t.Fatalf("onsets %v, want %v", on, want)
	}
	for i := range want {
		if !near(on[i], want[i], 0.03) {
			t.Fatalf("line %d at %.3f, want %.3f", i+1, on[i], want[i])
		}
	}
	// Subtitles follow the lines.
	b, err := os.ReadFile(res.SRT)
	if err != nil {
		t.Fatal(err)
	}
	cues := srtRe.FindAllStringSubmatch(string(b), -1)
	for i, c := range cues {
		if !near(parseSRTTime(c[2]), tl[i].VoiceStart, 0.001) {
			t.Fatalf("cue %d at %s, line at %.3f", i+1, c[2], tl[i].VoiceStart)
		}
	}
	// Ducking follows the moved line: the music is down under line 2, not
	// where line 2 used to start.
	free := bandRMS(t, f, res.Output, 220, 60, tl[1].VoiceDefaultStart+0.05, tl[1].VoiceStart-0.05)
	under := bandRMS(t, f, res.Output, 220, 60, tl[1].VoiceStart+0.3, tl[1].VoiceEnd-0.1)
	t.Logf("music (220 Hz band RMS): %.0f where line 2 used to start, %.0f under the moved line", free, under)
	if free < 2*under {
		t.Fatalf("music not ducked under the moved line: %.0f free vs %.0f under the voice", free, under)
	}
}

// Lines moved over each other are summed, but the mix never exceeds −1 dBFS.
func TestVoiceOverlapLimited(t *testing.T) {
	k := newKit(t)
	f := k.f
	a := k.clip("a.mp4", "testsrc2", 160, 120, 3, 440)
	loud := k.make("loud.wav", "-f", "lavfi", "-i", "aevalsrc=exprs='0.85*sin(2*PI*660*t)':s=48000:d=2")
	res, err := f.Montage(context.Background(), MontageOptions{
		Segments:   []Segment{{Video: a, Voice: loud}, {Video: a, Voice: loud, VoiceShift: -3}},
		Transition: "none", Subtitles: "none", AmbientVolume: 0, OutDir: filepath.Join(k.dir, "out"),
	})
	if err != nil {
		t.Fatal(err)
	}
	tl := res.Timeline
	if tl[1].VoiceStart >= tl[0].VoiceEnd {
		t.Fatalf("lines do not overlap: %+v", tl)
	}
	raw, err := exec.Command(f.FFmpeg, "-hide_banner", "-loglevel", "error", "-i", res.Output, "-vn", "-f", "s16le", "-ac", "1", "-ar", "48000", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	peak := 0.0
	for i := 0; i+1 < len(raw); i += 2 {
		peak = math.Max(peak, math.Abs(float64(int16(uint16(raw[i])|uint16(raw[i+1])<<8))))
	}
	// −1 dBFS plus a little room for the AAC encode.
	db := 20 * math.Log10(peak/32768)
	t.Logf("two overlapping −1.4 dBFS lines peak at %.2f dBFS", db)
	if db > -0.6 {
		t.Fatalf("overlapping lines peak at %.2f dBFS", db)
	}
}

// Long dips fade the picture and the ambient sound, never the voice.
func TestLongDipKeepsVoice(t *testing.T) {
	k := newKit(t)
	f := k.f
	a := k.clip("a.mp4", "testsrc2", 160, 120, 2, 440)
	v := k.voice("v.mp3", 1.5)
	res, err := f.Montage(context.Background(), MontageOptions{
		Segments:   []Segment{{Video: a, Voice: v}, {Video: a, Voice: v}},
		Transition: "fade", TransitionDuration: 2, Subtitles: "none", AmbientVolume: 0.5,
		OutDir: filepath.Join(k.dir, "out"),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkFilm(t, f, res)
	on := onsets(t, f, res.Output, 880)
	if len(on) != 2 {
		t.Fatalf("onsets %v", on)
	}
	for i, s := range res.Timeline {
		if !near(on[i], s.VoiceStart, 0.03) || !near(s.VoiceStart-s.Start, 14.0/fps, 1e-9) {
			t.Fatalf("voice %d at %.3f (timeline %.3f, scene %.3f)", i+1, on[i], s.VoiceStart, s.Start)
		}
	}
	// Full level right from the start of each line: the first 100 ms of the
	// 880 Hz band are as loud as the middle of the line.
	raw, err := exec.Command(f.FFmpeg, "-hide_banner", "-loglevel", "error", "-i", res.Output, "-vn",
		"-af", "bandpass=f=880:width_type=h:width=150,aformat=sample_fmts=s16:channel_layouts=mono", "-ar", "48000", "-f", "s16le", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	rms := func(from, to float64) float64 {
		s, n := 0.0, 0.0
		for i := int(from * 48000); i < int(to*48000) && 2*i+1 < len(raw); i++ {
			x := float64(int16(uint16(raw[2*i]) | uint16(raw[2*i+1])<<8))
			s, n = s+x*x, n+1
		}
		return math.Sqrt(s / math.Max(n, 1))
	}
	for i, s := range res.Timeline {
		head, mid := rms(s.VoiceStart+0.01, s.VoiceStart+0.1), rms(s.VoiceStart+0.6, s.VoiceStart+0.9)
		if head < 0.8*mid {
			t.Fatalf("voice %d faded in: %.0f vs %.0f", i+1, head, mid)
		}
	}
}

// Own-voice slices (0.15 s lead, 0.25 s tail built in) keep the recording's
// rhythm: the pause between two lines stays 0.4 s, subtitles start on the word.
func TestOwnVoiceContinuous(t *testing.T) {
	k := newKit(t)
	f := k.f
	a := k.clip("a.mp4", "testsrc2", 160, 120, 2, 440)
	slice := func(name string) string {
		return k.make(name, "-f", "lavfi", "-i", "aevalsrc=exprs='if(between(t,0.15,2.15),0.4*sin(2*PI*880*t),0)':s=44100:d=2.4",
			"-c:a", "libmp3lame", "-b:a", "96k")
	}
	v1, v2 := slice("v1.mp3"), slice("v2.mp3")
	res, err := f.Montage(context.Background(), MontageOptions{
		Segments: []Segment{
			{Video: a, Voice: v1, OwnVoice: true, VoiceLead: 0.15, VoiceTail: 0.25, Narration: "Перший рядок."},
			{Video: a, Voice: v2, OwnVoice: true, VoiceLead: 0.15, VoiceTail: 0.25, Narration: "Другий рядок."},
		},
		Transition: "fade", Subtitles: "srt", AmbientVolume: 0.3, OutDir: filepath.Join(k.dir, "out"),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkFilm(t, f, res)
	on := onsets(t, f, res.Output, 880)
	if len(on) != 2 {
		t.Fatalf("onsets %v", on)
	}
	if gap := on[1] - (on[0] + 2.0); !near(gap, 0.4, 0.05) {
		t.Fatalf("pause between the lines %.3f s, want 0.4 (onsets %v)", gap, on)
	}
	b, err := os.ReadFile(res.SRT)
	if err != nil {
		t.Fatal(err)
	}
	cues := srtRe.FindAllStringSubmatch(string(b), -1)
	if len(cues) != 2 || !near(parseSRTTime(cues[0][2]), on[0], 0.03) || !near(parseSRTTime(cues[1][2]), on[1], 0.03) {
		t.Fatalf("cues %v, onsets %v", cues, on)
	}
}

// Frame-only scenes move with their effect (or the project default); "none" stays still.
func TestStillEffectsMove(t *testing.T) {
	k := newKit(t)
	f := k.f
	still := k.still("still.png", "testsrc2", 640, 360)
	res, err := f.Montage(context.Background(), MontageOptions{
		Segments: []Segment{
			{Frame: still, TrimOut: 5},                 // project default
			{Frame: still, TrimOut: 5, Effect: "none"}, // static
			{Frame: still, TrimOut: 5, Effect: "shake"},
		},
		Aspect: "16:9", Transition: "none", DefaultEffect: "zoomout", Subtitles: "none",
		OutDir: filepath.Join(k.dir, "out"),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkFilm(t, f, res)
	tl := res.Timeline
	if tl[0].Effect != "zoomout" || tl[1].Effect != "none" || tl[2].Effect != "shake" || tl[0].Duration != 3 {
		t.Fatalf("%+v", tl)
	}
	moved := func(s Slot) float64 {
		a := frameAt(t, f, res.Output, s.Start+0.1, 640, 360, "gray")
		b := frameAt(t, f, res.Output, s.Start+s.Duration-0.2, 640, 360, "gray")
		return meanAbsDiff(a, b)
	}
	if d := moved(tl[0]); d < 3 {
		t.Fatalf("zoom-out still did not move (%.2f)", d)
	}
	if d := moved(tl[1]); d > 1 {
		t.Fatalf("static still moved (%.2f)", d)
	}
	if d := moved(tl[2]); d < 1 {
		t.Fatalf("handheld still did not move (%.2f)", d)
	}
}

// The camera moves are smooth: a tracked dot moves without stair-stepping
// (second difference of its position at the 8-bit measurement floor ≈ 0.12 px;
// zoompan on a 4× upscaled frame measures ≈ 0.4 px).
func TestEffectSmooth(t *testing.T) {
	k := newKit(t)
	f := k.f
	dot := k.make("dot.png", "-f", "lavfi", "-i", "color=black:size=1376x768,format=gray",
		"-vf", "geq=lum='255*exp(-((X-400)*(X-400)+(Y-250)*(Y-250))/72)'", "-frames:v", "1")
	const W, H, N = 1280, 720, 96
	for _, id := range []string{"zoomin", "panright", "diagonal", "pushin", "rotate", "drift"} {
		chain := fmt.Sprintf("[0:v]scale=%d:%d:force_original_aspect_ratio=increase:flags=lanczos,crop=%d:%d,format=yuv420p,tpad=stop_mode=clone:stop=%d,trim=end_frame=%d,%s,format=gray[v]",
			W, H, W, H, N-1, N, effectFilter(id, W, H, N, false))
		out, err := exec.Command(f.FFmpeg, "-hide_banner", "-loglevel", "error", "-framerate", "24", "-i", dot,
			"-filter_complex", chain, "-map", "[v]", "-f", "rawvideo", "-").Output()
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		var xs, ys []float64
		for len(out) >= W*H {
			fr := out[:W*H]
			out = out[W*H:]
			var sx, sy, sw float64
			for y := 0; y < H; y++ {
				for x := 0; x < W; x++ {
					if v := float64(fr[y*W+x]); v >= 40 {
						sx, sy, sw = sx+v*float64(x), sy+v*float64(y), sw+v
					}
				}
			}
			xs, ys = append(xs, sx/sw), append(ys, sy/sw)
		}
		if len(xs) != N {
			t.Fatalf("%s: %d frames", id, len(xs))
		}
		jerk := 0.0
		for i := 1; i < N-1; i++ {
			jerk = math.Max(jerk, math.Hypot(xs[i+1]-2*xs[i]+xs[i-1], ys[i+1]-2*ys[i]+ys[i-1]))
		}
		travel := math.Hypot(xs[N-1]-xs[0], ys[N-1]-ys[0])
		t.Logf("%-9s travel %6.1f px, max jerk %.3f px", id, travel, jerk)
		if jerk > 0.2 {
			t.Errorf("%s: jerky motion (%.3f px)", id, jerk)
		}
		if travel < 3 {
			t.Errorf("%s: the dot did not move (%.2f px)", id, travel)
		}
	}
}

// lookRGB runs a colour look over a lavfi source and returns the frames as rgb24.
func lookRGB(t *testing.T, f FF, src, id string, w, h, n int) [][]byte {
	t.Helper()
	flt, ok := FilterByID(id)
	if !ok {
		t.Fatalf("filter %q", id)
	}
	g := "[0:v]format=yuv420p[vm]"
	cur := "[vm]"
	if l := flt.look(cur, "[vl]", "lk"); l != "" {
		g += ";" + l
		cur = "[vl]"
	}
	g += ";" + cur + "format=rgb24[v]"
	out, err := exec.Command(f.FFmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", lavfi(src, w, h, true),
		"-filter_complex", g, "-map", "[v]", "-frames:v", strconv.Itoa(n), "-f", "rawvideo", "-").Output()
	if err != nil {
		t.Fatalf("look %s: %v", id, err)
	}
	var fr [][]byte
	for len(out) >= w*h*3 {
		fr, out = append(fr, out[:w*h*3]), out[w*h*3:]
	}
	if len(fr) != n {
		t.Fatalf("look %s: %d frames", id, len(fr))
	}
	return fr
}

// rgbMean averages the channels over a rectangle (fractions of the frame).
func rgbMean(fr []byte, w, h int, x0, y0, x1, y1 float64) (r, g, b float64) {
	n := 0.0
	for y := int(y0 * float64(h)); y < int(y1*float64(h)); y++ {
		for x := int(x0 * float64(w)); x < int(x1*float64(w)); x++ {
			i := (y*w + x) * 3
			r, g, b, n = r+float64(fr[i]), g+float64(fr[i+1]), b+float64(fr[i+2]), n+1
		}
	}
	return r / n, g / n, b / n
}

// Each colour look does what its name promises (checked on neutral and
// colourful test pictures; the looks were tuned by eye on real frames).
func TestFiltersLook(t *testing.T) {
	f := locate(t)
	const w, h = 96, 54
	gray := "color=c=0x808080"
	whole := func(fr []byte) (float64, float64, float64) { return rgbMean(fr, w, h, 0, 0, 1, 1) }
	for _, flt := range Filters { // every chain runs on a colourful picture
		lookRGB(t, f, "testsrc2", flt.ID, w, h, 2)
	}
	r0, g0, b0 := whole(lookRGB(t, f, gray, "none", w, h, 1)[0])
	if math.Abs(r0-b0) > 2 || math.Abs(r0-g0) > 2 {
		t.Fatalf("gray is not neutral: %.1f %.1f %.1f", r0, g0, b0)
	}
	type rule struct {
		id, src string
		ok      func(r, g, b float64) bool
	}
	for _, c := range []rule{
		{"warm", gray, func(r, g, b float64) bool { return r-b > 8 }},
		{"cool", gray, func(r, g, b float64) bool { return b-r > 8 }},
		{"golden", gray, func(r, g, b float64) bool { return r-b > 20 }},
		{"sepia", gray, func(r, g, b float64) bool { return r > g+3 && g > b+3 }},
		{"cinematic", gray, func(r, g, b float64) bool { return b > r+3 }}, // neutrals lean teal
		{"moonlight", gray, func(r, g, b float64) bool { return b > r+5 && (r+g+b)/3 < (r0+g0+b0)/3-5 }},
		{"faded", "color=c=black", func(r, g, b float64) bool { return (r+g+b)/3 > 15 }}, // lifted blacks
		{"vintage", "color=c=black", func(r, g, b float64) bool { return (r+g+b)/3 > 12 }},
		{"contrast", "color=c=0x404040", func(r, g, b float64) bool { return (r+g+b)/3 < 0x40-3 }},
		{"contrast", "color=c=0xC0C0C0", func(r, g, b float64) bool { return (r+g+b)/3 > 0xC0+3 }},
	} {
		if r, g, b := whole(lookRGB(t, f, c.src, c.id, w, h, 1)[0]); !c.ok(r, g, b) {
			t.Errorf("%s on %s: rgb %.1f %.1f %.1f", c.id, c.src, r, g, b)
		}
	}
	// Noir is black and white.
	nr := lookRGB(t, f, "testsrc2", "noir", w, h, 1)[0]
	for j := 0; j < len(nr); j += 3 {
		if math.Abs(float64(nr[j])-float64(nr[j+1])) > 6 || math.Abs(float64(nr[j+1])-float64(nr[j+2])) > 6 {
			t.Fatalf("noir pixel %d not gray: %v", j/3, nr[j:j+3])
		}
	}
	// Vivid saturates.
	chroma := func(fr []byte) float64 {
		s := 0.0
		for j := 0; j < len(fr); j += 3 {
			r, g, b := float64(fr[j]), float64(fr[j+1]), float64(fr[j+2])
			s += math.Max(r, math.Max(g, b)) - math.Min(r, math.Min(g, b))
		}
		return s
	}
	if chroma(lookRGB(t, f, "testsrc2", "vivid", w, h, 1)[0]) < 1.05*chroma(lookRGB(t, f, "testsrc2", "none", w, h, 1)[0]) {
		t.Error("vivid does not saturate")
	}
	// Vignette darkens the corners, not the centre.
	vg := lookRGB(t, f, "color=c=0xC0C0C0", "vignette", w, h, 1)[0]
	cr, _, _ := rgbMean(vg, w, h, 0, 0, 0.1, 0.1)
	mr, _, _ := rgbMean(vg, w, h, 0.45, 0.45, 0.55, 0.55)
	if cr > 0.8*mr || mr < 0xC0-8 {
		t.Errorf("vignette corner %.1f centre %.1f", cr, mr)
	}
	// Grain changes from frame to frame.
	gr := lookRGB(t, f, gray, "grain", w, h, 2)
	if meanAbsDiff(gr[0], gr[1]) < 1 {
		t.Error("grain is not animated")
	}
	// Dreamy glows: light bleeds around a white square on black.
	sq := "color=c=black,drawbox=x=40:y=20:w=16:h=14:color=white:t=fill"
	plain := lookRGB(t, f, sq, "none", w, h, 1)[0]
	glow := lookRGB(t, f, sq, "dreamy", w, h, 1)[0]
	p, _, _ := rgbMean(plain, w, h, 0.3, 0.3, 0.38, 0.6)
	q, _, _ := rgbMean(glow, w, h, 0.3, 0.3, 0.38, 0.6)
	if q < p+4 {
		t.Errorf("dreamy: no glow next to the light (%.1f vs %.1f)", q, p)
	}
}

// Burnt subtitles land where the style puts them.
func TestBurnPresets(t *testing.T) {
	k := newKit(t)
	f := k.f
	if !hasFilter(f, "subtitles") {
		t.Skip("ffmpeg without libass")
	}
	black := k.still("black.png", "color=c=black", 640, 360)
	for _, c := range []struct {
		name   string
		style  SubtitleStyle
		y0, y1 float64 // where the text must be (share of the height)
		yellow bool
	}{
		{"classic", SubtitleStyle{}, 0.6, 1, false},
		{"shorts", SubtitleStyle{Preset: "shorts"}, 0.3, 0.7, false},
		{"top", SubtitleStyle{Position: "top", Size: 80}, 0, 0.4, false},
		{"youtube", SubtitleStyle{Preset: "youtube"}, 0.6, 1, true},
		{"plate", SubtitleStyle{Preset: "plate"}, 0.6, 1, false},
	} {
		res, err := f.Montage(context.Background(), MontageOptions{
			Segments: []Segment{{Frame: black, TrimOut: 6, Effect: "none", Narration: "Текст субтитрів"}},
			Aspect:   "16:9", Transition: "none", Subtitles: "burn", SubtitleStyle: c.style,
			OutDir: filepath.Join(k.dir, c.name),
		})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		const fw, fh = 320, 180
		fr := frameAt(t, f, res.Output, 1.0, fw, fh, "rgb24")
		var ys, n, yellow float64
		for i := 0; i < len(fr); i += 3 {
			r, g, b := float64(fr[i]), float64(fr[i+1]), float64(fr[i+2])
			if max(r, g, b) > 110 { // glyphs (or the plate) on the black frame
				ys += float64(i / 3 / fw)
				n++
				if r > 1.5*b+40 && g > 1.5*b+30 {
					yellow++
				}
			}
		}
		if n < 20 {
			t.Fatalf("%s: no subtitle pixels", c.name)
		}
		if y := ys / n / fh; y < c.y0 || y > c.y1 {
			t.Errorf("%s: text at %.2f of the height, want %.2f…%.2f", c.name, y, c.y0, c.y1)
		}
		if c.yellow != (yellow > n/3) {
			t.Errorf("%s: yellow share %.2f", c.name, yellow/n)
		}
	}
}

// Cancelling mid-render stops every ffmpeg quickly and leaves no intermediates.
func TestMontageCancelMidway(t *testing.T) {
	k := newKit(t)
	f := k.f
	still := k.still("still.png", "testsrc2", 1280, 720)
	var segs []Segment
	for i := 0; i < 12; i++ {
		segs = append(segs, Segment{Frame: still, Effect: "diagonal"})
	}
	var stop atomic.Bool
	out := filepath.Join(k.dir, "out")
	var at time.Time
	_, err := f.Montage(context.Background(), MontageOptions{
		Segments: segs, Transition: "dissolve", Subtitles: "none", OutDir: out, Workers: 2,
		Progress: func(pct int, msg string) {
			if pct > 0 && !stop.Load() {
				at = time.Now()
				stop.Store(true)
			}
		},
		Cancelled: stop.Load,
	})
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("want ErrCancelled, got %v", err)
	}
	if d := time.Since(at); d > 5*time.Second {
		t.Fatalf("cancel took %v", d)
	}
	if left, _ := os.ReadDir(out); len(left) != 0 {
		var names []string
		for _, e := range left {
			names = append(names, e.Name())
		}
		t.Fatalf("left behind: %v", names)
	}
}

// A broken clip names the scene in the error.
func TestMontageErrorNamesScene(t *testing.T) {
	k := newKit(t)
	f := k.f
	a := k.clip("a.mp4", "testsrc2", 160, 120, 1, 440)
	bad := filepath.Join(k.dir, "bad.mp4")
	if err := os.WriteFile(bad, []byte("not a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := f.Montage(context.Background(), MontageOptions{
		Segments: []Segment{{Video: a}, {Video: bad}}, Subtitles: "none", OutDir: filepath.Join(k.dir, "out"), Workers: 1,
	})
	if err == nil || !strings.HasPrefix(err.Error(), "сцена 2: ") {
		t.Fatalf("error %v", err)
	}
}

// A 40-scene film (720p clips and stills, voices, mixed transitions, a look,
// burnt subtitles, music) renders in reasonable time and stays in sync.
func TestMontage40Scenes(t *testing.T) {
	if testing.Short() {
		t.Skip("long")
	}
	k := newKit(t)
	f := k.f
	clip := k.clip("clip.mp4", "testsrc2", 1280, 720, 3, 440)
	still := k.still("still.png", "testsrc2", 1280, 720)
	voices := []string{k.voice("v1.mp3", 1.0), k.voice("v2.mp3", 1.6), k.voice("v3.mp3", 2.4)}
	trs := []string{"dissolve", "fade", "none", "wipeleft", "slideup", "circleopen", "pixelize", "fadewhite"}
	var segs []Segment
	for i := 0; i < 40; i++ {
		s := Segment{Voice: voices[i%3], Narration: fmt.Sprintf("Сцена номер %d. Голос звучить рівно.", i+1), Transition: trs[i%len(trs)]}
		if i%2 == 0 {
			s.Video = clip
		} else {
			s.Frame, s.TrimOut = still, 4 // 4 s stills
		}
		segs = append(segs, s)
	}
	subs := "burn"
	if !hasFilter(f, "subtitles") {
		subs = "srt"
	}
	var phases []string
	var last string
	marks := map[string]time.Time{}
	start := time.Now()
	res, err := f.Montage(context.Background(), MontageOptions{
		Segments: segs, Aspect: "16:9", Transition: "dissolve", Filter: "vivid",
		Subtitles: subs, SubtitleStyle: SubtitleStyle{Preset: "classic"},
		MusicPath: k.music(), MusicVolume: 0.25, Ducking: true, AmbientVolume: 0.25,
		OutDir: filepath.Join(k.dir, "out"),
		Progress: func(pct int, msg string) {
			phase, _, _ := strings.Cut(msg, " · ")
			if phase != last {
				last = phase
				phases = append(phases, phase)
				marks[phase] = time.Now()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	checkFilm(t, f, res)
	t.Logf("40 scenes → %.1f s of 1280×720 film in %v (%.1f× real time, %d workers)",
		res.Duration, elapsed.Round(100*time.Millisecond), res.Duration/elapsed.Seconds(), autoWorkers(1280, 720))
	for _, p := range phases {
		t.Logf("  from %6.1f s: %s", marks[p].Sub(start).Seconds(), p)
	}
	if elapsed > 4*time.Minute {
		t.Errorf("too slow: %v", elapsed)
	}
	on := onsets(t, f, res.Output, 880)
	if len(on) != 40 {
		t.Fatalf("%d voice onsets", len(on))
	}
	for i, at := range on {
		if !near(at, res.Timeline[i].VoiceStart, 0.03) {
			t.Fatalf("voice %d at %.3f, timeline %.3f", i+1, at, res.Timeline[i].VoiceStart)
		}
	}
}
