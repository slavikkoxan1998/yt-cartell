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
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
)

// ---- fixtures -----------------------------------------------------------------------------

func locate(t *testing.T) FF {
	t.Helper()
	f, err := Locate(config.Config{})
	if err != nil || !f.OK() {
		t.Skipf("ffmpeg/ffprobe not available: %v", err)
	}
	return f
}

type fixtures struct {
	dir                       string
	clip, frame, voice, music string
}

var (
	fixOnce sync.Once
	fix     fixtures
	fixErr  error
)

// synth creates a 2 s testsrc clip with a sine track, a PNG frame, a 3 s sine
// mp3 "voice" and a 1.5 s music loop — shared by all tests of the package.
func synth(t *testing.T, f FF) fixtures {
	t.Helper()
	fixOnce.Do(func() {
		dir, err := os.MkdirTemp("", "media-fixtures-*")
		if err != nil {
			fixErr = err
			return
		}
		fix = fixtures{
			dir:   dir,
			clip:  filepath.Join(dir, "кліп 1.mp4"), // non-ASCII + space on purpose
			frame: filepath.Join(dir, "frame.png"),
			voice: filepath.Join(dir, "voice.mp3"),
			music: filepath.Join(dir, "music.mp3"),
		}
		ctx := context.Background()
		steps := [][]string{
			{"-f", "lavfi", "-i", "testsrc=size=320x240:rate=24:duration=2", "-f", "lavfi", "-i", "sine=frequency=440:duration=2",
				"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", fix.clip},
			{"-f", "lavfi", "-i", "testsrc=size=320x240", "-frames:v", "1", fix.frame},
			{"-f", "lavfi", "-i", "sine=frequency=880:duration=3", "-c:a", "libmp3lame", "-b:a", "96k", fix.voice},
			{"-f", "lavfi", "-i", "sine=frequency=220:duration=1.5", "-c:a", "libmp3lame", "-b:a", "96k", fix.music},
		}
		for _, a := range steps {
			if err := f.Run(ctx, "", a...); err != nil {
				fixErr = err
				return
			}
		}
	})
	if fixErr != nil {
		t.Fatalf("fixtures: %v", fixErr)
	}
	return fix
}

func TestMain(m *testing.M) {
	code := m.Run()
	if fix.dir != "" {
		_ = os.RemoveAll(fix.dir)
	}
	os.Exit(code)
}

func hasFilter(f FF, name string) bool {
	out, err := exec.Command(f.FFmpeg, "-hide_banner", "-filters").Output()
	return err == nil && regexp.MustCompile(`\s`+name+`\s`).Match(out)
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// ---- locate / version / run ----------------------------------------------------------------

func TestLocateAndVersion(t *testing.T) {
	f := locate(t)
	if !filepath.IsAbs(f.FFmpeg) || !filepath.IsAbs(f.FFprobe) {
		t.Fatalf("paths must be absolute: %+v", f)
	}
	if strings.EqualFold(filepath.Base(filepath.Dir(f.FFmpeg)), "shims") {
		t.Fatalf("scoop shim not resolved: %s", f.FFmpeg)
	}
	v, err := f.Version(context.Background())
	if err != nil || !strings.HasPrefix(v, "ffmpeg version") {
		t.Fatalf("version %q, %v", v, err)
	}

	// Configured folder → both binaries from that folder.
	g, err := Locate(config.Config{FFmpegPath: filepath.Dir(f.FFmpeg)})
	if err != nil || !g.OK() {
		t.Fatalf("locate by folder: %+v %v", g, err)
	}
	// Configured file that no longer exists (moved by an upgrade) → the
	// auto-detected ffmpeg is used instead.
	if h, err := Locate(config.Config{FFmpegPath: filepath.Join(t.TempDir(), "nope", "ffmpeg.exe")}); err != nil || !h.OK() {
		t.Fatalf("a missing configured ffmpeg must fall back to auto-detection: %+v %v", h, err)
	}
}

func TestResolveShim(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real", "tool.exe")
	_ = os.MkdirAll(filepath.Dir(real), 0o755)
	_ = os.WriteFile(real, []byte("x"), 0o644)
	shim := filepath.Join(dir, "tool.exe")
	_ = os.WriteFile(shim, []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "tool.shim"), []byte("path = \""+real+"\"\nargs = \n"), 0o644)
	if got := resolveShim(shim); got != real {
		t.Fatalf("resolveShim = %q, want %q", got, real)
	}
	if got := resolveShim(real); got != real {
		t.Fatalf("non-shim changed: %q", got)
	}
}

func TestRunErrorTail(t *testing.T) {
	f := locate(t)
	err := f.Run(context.Background(), "", "-i", filepath.Join(t.TempDir(), "missing.mp4"), "-f", "null", "-")
	if err == nil || !strings.HasPrefix(err.Error(), "ffmpeg ") || !strings.Contains(err.Error(), "missing.mp4") {
		t.Fatalf("want stderr tail in error, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.Run(ctx, "", "-f", "lavfi", "-i", "testsrc", "-f", "null", "-"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run: %v", err)
	}
}

// ---- probe / thumbnail ------------------------------------------------------------------------

func TestProbe(t *testing.T) {
	f := locate(t)
	fx := synth(t, f)
	ctx := context.Background()

	info, err := f.Probe(ctx, fx.clip)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 320 || info.Height != 240 || !near(info.Duration, 2, 0.15) || !info.HasAudio {
		t.Fatalf("clip: %+v", info)
	}
	info, err = f.Probe(ctx, fx.frame)
	if err != nil || info.Width != 320 || info.Height != 240 || info.HasAudio {
		t.Fatalf("frame: %+v %v", info, err)
	}
	info, err = f.Probe(ctx, fx.voice)
	if err != nil || info.Width != 0 || !info.HasAudio || !near(info.Duration, 3, 0.15) {
		t.Fatalf("voice: %+v %v", info, err)
	}
	if _, err := f.Probe(ctx, filepath.Join(t.TempDir(), "missing.mp4")); err == nil {
		t.Fatal("expected error for a missing file")
	}
}

func TestThumbnail(t *testing.T) {
	f := locate(t)
	fx := synth(t, f)
	ctx := context.Background()

	img, err := f.Thumbnail(ctx, fx.clip, 1.0, 160)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 160 || b.Dy() != 120 {
		t.Fatalf("thumb size %v", b)
	}
	// Past the end → falls back to the first frame.
	img, err = f.Thumbnail(ctx, fx.clip, 30, 160)
	if err != nil || img.Bounds().Dx() != 160 {
		t.Fatalf("past-end thumb: %v %v", img, err)
	}
	// Never upscales; stills work too.
	img, err = f.Thumbnail(ctx, fx.frame, 0, 2000)
	if err != nil || img.Bounds().Dx() != 320 {
		t.Fatalf("frame thumb: %v %v", img, err)
	}
}

// ---- montage ----------------------------------------------------------------------------------

const firstLine = "Привіт, світе. Це перша сцена, і тут звучить голос оповідача."

func montageSegments(fx fixtures) []Segment {
	return []Segment{
		// Clip (2 s) + voice (3 s) → held last frame, L = 3 + 0.6.
		{Video: fx.clip, Voice: fx.voice, Narration: "  " + firstLine + " "},
		// Clip without voice → L = clip length; narration timed over the clip.
		{Video: fx.clip, Narration: "Друга сцена без голосу."},
		// Frame only (missing clip file) + voice → Ken Burns over 8 s.
		{Video: filepath.Join(fx.dir, "missing.mp4"), Frame: fx.frame, Voice: fx.voice, Narration: ""},
	}
}

func checkOutput(t *testing.T, f FF, res MontageResult, want float64) ProbeInfo {
	t.Helper()
	if !filepath.IsAbs(res.Output) {
		t.Fatalf("output not absolute: %q", res.Output)
	}
	info, err := f.Probe(context.Background(), res.Output)
	if err != nil {
		t.Fatalf("probe output: %v", err)
	}
	if !near(res.Duration, want, 0.2) || !near(info.Duration, res.Duration, 0.35) {
		t.Fatalf("duration: result %.3f, file %.3f, want ≈%.2f", res.Duration, info.Duration, want)
	}
	if !info.HasAudio || info.Width != 320 || info.Height != 240 {
		t.Fatalf("output: %+v", info)
	}
	if res.MissingClips != 1 {
		t.Fatalf("missing clips = %d", res.MissingClips)
	}
	// Intermediates are gone: only the film and its subtitles remain.
	entries, _ := os.ReadDir(filepath.Dir(res.Output))
	var left []string
	for _, e := range entries {
		if n := e.Name(); !(strings.HasPrefix(n, "final-") && strings.HasSuffix(n, ".mp4")) && !(strings.HasPrefix(n, "subtitles-") && strings.HasSuffix(n, ".srt")) {
			left = append(left, n)
		}
	}
	if len(left) > 0 {
		t.Fatalf("intermediates left: %v", left)
	}
	return info
}

var srtRe = regexp.MustCompile(`(?m)^(\d+)\n(\d\d:\d\d:\d\d,\d\d\d) --> (\d\d:\d\d:\d\d,\d\d\d)\n`)

func parseSRTTime(s string) float64 {
	var h, m, sec, ms int
	_, _ = fmt.Sscanf(s, "%d:%d:%d,%d", &h, &m, &sec, &ms)
	return float64(h*3600+m*60+sec) + float64(ms)/1000
}

func checkSRT(t *testing.T, res MontageResult, voiceLen float64) {
	t.Helper()
	b, err := os.ReadFile(res.SRT)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	// The long first cue is wrapped on two lines; the short one is not.
	if !strings.Contains(s, "\n"+wrap2(firstLine, 42)+"\n") || !strings.Contains(s, "\nДруга сцена без голосу.\n") ||
		!strings.Contains(wrap2(firstLine, 42), "\n") {
		t.Fatalf("srt text:\n%s", s)
	}
	m := srtRe.FindAllStringSubmatch(s, -1)
	if len(m) != 2 {
		t.Fatalf("want 2 cues, got %d:\n%s", len(m), s)
	}
	// Cue 1 spans the voice from 0.25 s; cue 2 starts 0.25 s into segment 2 and lasts 1.5 s.
	a0, a1 := parseSRTTime(m[0][2]), parseSRTTime(m[0][3])
	b0, b1 := parseSRTTime(m[1][2]), parseSRTTime(m[1][3])
	// Scene 1 lasts whole frames: 0.25 s breath + the line + 0.35 s tail (±1 frame).
	if len(res.Timeline) != 3 {
		t.Fatalf("timeline: %+v", res.Timeline)
	}
	seg1 := res.Timeline[1].Start
	if !near(seg1, voiceLen+voiceDelay+voiceTail, 1.0/fps) || !near(res.Timeline[1].VoiceStart, seg1+0.25, 1e-9) {
		t.Fatalf("scene 2 at %.3f (voice %.3f)", seg1, voiceLen)
	}
	if !near(a0, 0.25, 0.001) || !near(a1-a0, voiceLen, 0.01) {
		t.Fatalf("cue 1 timing %v → %v (voice %.3f)", a0, a1, voiceLen)
	}
	if !near(b0, seg1+0.25, 0.01) || !near(b1-b0, 1.5, 0.01) {
		t.Fatalf("cue 2 timing %v → %v (seg1 %.3f)", b0, b1, seg1)
	}
}

func TestMontageFull(t *testing.T) {
	f := locate(t)
	fx := synth(t, f)
	burn := hasFilter(f, "subtitles")
	subs := "burn"
	if !burn {
		t.Log("ffmpeg built without libass: testing srt instead of burn")
		subs = "srt"
	}
	out := filepath.Join(t.TempDir(), "montage")
	_ = os.MkdirAll(out, 0o755)
	stale := filepath.Join(out, "final-1.mp4")
	_ = os.WriteFile(stale, []byte("old"), 0o644)

	var mu sync.Mutex
	var pcts []int
	var msgs []string
	res, err := f.Montage(context.Background(), MontageOptions{
		Segments:      montageSegments(fx),
		Aspect:        "16:9",
		Transition:    "fade",
		Subtitles:     subs,
		MusicPath:     fx.music,
		MusicVolume:   0.3,
		AmbientVolume: 0.35,
		Ducking:       true,
		OutDir:        out,
		Progress: func(p int, m string) {
			mu.Lock()
			pcts, msgs = append(pcts, p), append(msgs, m)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	voiceLen := f.probeDuration(context.Background(), fx.voice)
	want := (voiceLen + 0.6) + 2 + 8
	checkOutput(t, f, res, want)
	if isFile(stale) {
		t.Fatal("old output not cleared")
	}
	if res.SRT == "" {
		t.Fatal("no srt")
	}
	checkSRT(t, res, voiceLen)

	wantPct := []int{0, 25, 50, 78, 85}
	// The final pass repeats its message with a growing percentage.
	var distinct []string
	for i, m := range msgs {
		if i == 0 || m != msgs[i-1] {
			distinct = append(distinct, m)
		}
		if i > 0 && pcts[i] < pcts[i-1] {
			t.Fatalf("progress went back: %v", pcts)
		}
	}
	if strings.Join(distinct, "|") != "Монтаж сцен · 1/3|Монтаж сцен · 2/3|Монтаж сцен · 3/3|Склеюємо сцени|Музика, субтитри, фінальний рендер" {
		t.Fatalf("progress messages: %q", msgs)
	}
	for i, p := range wantPct {
		if pcts[i] != p {
			t.Fatalf("progress %v, want %v", pcts, wantPct)
		}
	}
	if m := res.Message(); !strings.HasPrefix(m, "Фінальне відео готове · 0:1") || !strings.Contains(m, "(1 сцен без кліпу — використано кадр)") {
		t.Fatalf("message %q", m)
	}
}

func TestMontagePlain(t *testing.T) {
	f := locate(t)
	fx := synth(t, f)
	out := t.TempDir()
	res, err := f.Montage(context.Background(), MontageOptions{
		Segments:      montageSegments(fx),
		Aspect:        "9:16",
		Transition:    "none",
		Subtitles:     "srt",
		AmbientVolume: 0,
		OutDir:        out,
	})
	if err != nil {
		t.Fatal(err)
	}
	voiceLen := f.probeDuration(context.Background(), fx.voice)
	checkOutput(t, f, res, (voiceLen+0.6)+2+8)
	checkSRT(t, res, voiceLen)
}

func TestMontageNoSubtitlesFramesOnly(t *testing.T) {
	f := locate(t)
	fx := synth(t, f)
	res, err := f.Montage(context.Background(), MontageOptions{
		Segments:   []Segment{{Frame: fx.frame, Narration: "Текст"}},
		Aspect:     "9:16",
		Transition: "fade",
		Subtitles:  "none",
		OutDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.Probe(context.Background(), res.Output)
	if err != nil {
		t.Fatal(err)
	}
	// No clip → size from the format; frame-only scene lasts the default clip length.
	if info.Width != 720 || info.Height != 1280 || !near(info.Duration, 8, 0.3) || !info.HasAudio || res.SRT != "" {
		t.Fatalf("%+v %+v", info, res)
	}
}

func TestMontageErrorsAndCancel(t *testing.T) {
	f := locate(t)
	fx := synth(t, f)
	_, err := f.Montage(context.Background(), MontageOptions{
		Segments: []Segment{{Video: filepath.Join(fx.dir, "nope.mp4")}},
		OutDir:   t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "Немає жодного кліпу") {
		t.Fatalf("want empty error, got %v", err)
	}
	start := time.Now()
	_, err = f.Montage(context.Background(), MontageOptions{
		Segments:  montageSegments(fx),
		OutDir:    t.TempDir(),
		Cancelled: func() bool { return true },
	})
	if !errors.Is(err, ErrCancelled) || time.Since(start) > 5*time.Second {
		t.Fatalf("cancel: %v", err)
	}
}
