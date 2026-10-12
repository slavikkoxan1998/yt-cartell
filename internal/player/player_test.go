package player

import (
	"bytes"
	"context"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
)

// ---- device-free output -------------------------------------------------------------------

// fakeSink consumes PCM at real-time pace, like a sound card, without opening one.
type fakeSink struct {
	mu            sync.Mutex
	playing, done bool
	n             int64
}

var sinks atomic.Int64

func newFakeSink(r io.Reader) (sink, error) {
	sinks.Add(1)
	f := &fakeSink{}
	go func() {
		buf := make([]byte, bytesPerSec/50)
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			n, err := io.ReadFull(r, buf)
			f.mu.Lock()
			f.n += int64(n)
			if err != nil {
				f.done = true
			}
			f.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return f, nil
}

func (f *fakeSink) Play()  { f.mu.Lock(); f.playing = true; f.mu.Unlock() }
func (f *fakeSink) Pause() { f.mu.Lock(); f.playing = false; f.mu.Unlock() }
func (f *fakeSink) IsPlaying() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.playing && !f.done
}
func (f *fakeSink) bytes() int64 { f.mu.Lock(); defer f.mu.Unlock(); return f.n }

// ---- fixtures -----------------------------------------------------------------------------

var (
	ff     media.FF
	ffOK   bool
	fixDir string
	clip   string // 2 s, 320×240, 24 fps, sine audio
	silent string // 2 s, 320×240, no audio
	tone1  string // 1 s sine mp3
	tone20 string // 20 s sine mp3
)

func TestMain(m *testing.M) {
	newSink = newFakeSink // never open an audio device in tests
	var err error
	ff, err = media.Locate(config.Config{})
	if err == nil && ff.OK() {
		fixDir, err = os.MkdirTemp("", "player-fixtures-*")
		if err == nil {
			clip = filepath.Join(fixDir, "кліп.mp4")
			silent = filepath.Join(fixDir, "silent.mp4")
			tone1 = filepath.Join(fixDir, "tone1.mp3")
			tone20 = filepath.Join(fixDir, "tone20.mp3")
			ctx := context.Background()
			steps := [][]string{
				{"-f", "lavfi", "-i", "testsrc=size=320x240:rate=24:duration=2", "-f", "lavfi", "-i", "sine=frequency=440:duration=2",
					"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", clip},
				{"-f", "lavfi", "-i", "testsrc=size=320x240:rate=24:duration=2", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", silent},
				{"-f", "lavfi", "-i", "sine=frequency=880:duration=1", "-c:a", "libmp3lame", tone1},
				{"-f", "lavfi", "-i", "sine=frequency=660:duration=20", "-c:a", "libmp3lame", tone20},
			}
			ffOK = true
			for _, a := range steps {
				if err := ff.Run(ctx, "", a...); err != nil {
					ffOK = false
					break
				}
			}
		}
	}
	code := m.Run()
	if fixDir != "" {
		_ = os.RemoveAll(fixDir)
	}
	os.Exit(code)
}

func need(t *testing.T) {
	t.Helper()
	if !ffOK {
		t.Skip("ffmpeg not available")
	}
}

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func done(ch chan struct{}) func() {
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

func closedWithin(ch chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

// ---- pure parts ---------------------------------------------------------------------------

type chunky struct {
	data  []byte
	sizes []int
}

func (c *chunky) Read(b []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := len(c.data)
	if len(c.sizes) > 0 {
		n, c.sizes = min(n, c.sizes[0]), c.sizes[1:]
	}
	n = copy(b, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

func TestPCMReaderAligns(t *testing.T) {
	src := make([]byte, 4*100+3) // trailing partial frame is dropped
	for i := range src {
		src[i] = byte(i)
	}
	p := newPCMReader(&chunky{data: src, sizes: []int{1, 5, 7, 2, 2, 99, 3, 1000}})
	var got []byte
	buf := make([]byte, 64)
	for {
		n, err := p.Read(buf)
		if n%frameBytes != 0 {
			t.Fatalf("unaligned read %d", n)
		}
		got = append(got, buf[:n]...)
		if err == io.EOF {
			break
		}
	}
	if !bytes.Equal(got, src[:400]) {
		t.Fatalf("data mismatch: %d bytes", len(got))
	}
	select {
	case <-p.eof:
	default:
		t.Fatal("eof not signalled")
	}
}

func TestFitSize(t *testing.T) {
	for _, c := range []struct{ w, h, max, ew, eh int }{
		{1920, 1080, 960, 960, 540},
		{320, 240, 960, 320, 240}, // never upscale
		{1080, 1920, 480, 480, 852},
		{321, 241, 0, 320, 240},
		{0, 0, 960, 0, 0},
	} {
		if w, h := fitSize(c.w, c.h, c.max); w != c.ew || h != c.eh {
			t.Errorf("fitSize(%d,%d,%d) = %d×%d, want %d×%d", c.w, c.h, c.max, w, h, c.ew, c.eh)
		}
	}
}

// ---- audio ----------------------------------------------------------------------------------

func TestAudioPlaysToEnd(t *testing.T) {
	need(t)
	a := Audio()
	ch := make(chan struct{})
	if err := a.Play(ff, tone1, done(ch)); err != nil {
		t.Fatal(err)
	}
	if !a.Playing(tone1) || a.Playing(tone20) {
		t.Fatal("Playing(src) wrong")
	}
	a.mu.Lock()
	out := a.cur.out.(*fakeSink)
	a.mu.Unlock()
	if !closedWithin(ch, 6*time.Second) {
		t.Fatal("onDone not called")
	}
	if a.Playing(tone1) || a.Err() != nil {
		t.Fatalf("still playing / err %v", a.Err())
	}
	// 1 s of 48 kHz stereo s16le ≈ 192000 bytes (mp3 padding adds a little).
	if n := out.bytes(); n < 180000 || n > 215000 {
		t.Fatalf("decoded %d PCM bytes", n)
	}
}

func TestAudioStopAndReplace(t *testing.T) {
	need(t)
	a := Audio()
	ch1, ch2 := make(chan struct{}), make(chan struct{})
	if err := a.Play(ff, tone20, done(ch1)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if err := a.Play(ff, tone1, done(ch2)); err != nil {
		t.Fatal(err)
	}
	if !closedWithin(ch1, 3*time.Second) {
		t.Fatal("replaced playback did not end")
	}
	if a.Playing(tone20) || !a.Playing(tone1) {
		t.Fatal("wrong source playing")
	}
	a.Stop()
	if a.Playing(tone1) {
		t.Fatal("still playing after Stop")
	}
	if !closedWithin(ch2, 3*time.Second) {
		t.Fatal("stopped playback: onDone not called")
	}
	if a.Err() != nil {
		t.Fatalf("stop is not an error: %v", a.Err())
	}
}

func TestAudioErrors(t *testing.T) {
	need(t)
	a := Audio()
	if err := a.Play(ff, filepath.Join(fixDir, "missing.mp3"), nil); err == nil {
		t.Fatal("missing file must fail")
	}
	if err := a.Play(media.FF{}, tone1, nil); err == nil {
		t.Fatal("no ffmpeg must fail")
	}
	// A file ffmpeg cannot decode ends quickly and reports why.
	bad := filepath.Join(fixDir, "bad.mp3")
	_ = os.WriteFile(bad, []byte("not audio at all"), 0o644)
	ch := make(chan struct{})
	if err := a.Play(ff, bad, done(ch)); err != nil {
		t.Fatal(err)
	}
	if !closedWithin(ch, 5*time.Second) {
		t.Fatal("bad file did not end")
	}
	if a.Err() == nil {
		t.Fatal("want an error for an undecodable file")
	}
}

// ---- video ------------------------------------------------------------------------------------

func newCountingVideo(path string, maxW int) (*Video, *atomic.Int64) {
	var n atomic.Int64
	return NewVideo(ff, path, maxW, func() { n.Add(1) }), &n
}

func TestVideoFirstFrameSeekPlay(t *testing.T) {
	need(t)
	v, inv := newCountingVideo(clip, 160)
	defer v.Close()
	waitFor(t, "first frame", 10*time.Second, func() bool { return v.Frame() != nil })
	f0 := v.Frame()
	if b := f0.Bounds(); b != image.Rect(0, 0, 160, 120) {
		t.Fatalf("frame bounds %v", b)
	}
	if d := v.Duration(); math.Abs(d-2) > 0.15 {
		t.Fatalf("duration %v", d)
	}
	if v.Playing() || v.Position() != 0 || v.Err() != nil {
		t.Fatalf("fresh video: playing=%v pos=%v err=%v", v.Playing(), v.Position(), v.Err())
	}

	// Paused seek shows the frame at that time.
	v.Seek(1.0)
	waitFor(t, "seek frame", 5*time.Second, func() bool { return v.Frame() != f0 })
	if v.Position() != 1.0 || v.Playing() {
		t.Fatalf("after seek: pos %v playing %v", v.Position(), v.Playing())
	}
	if pixelsEqual(v.Frame(), f0) {
		t.Fatal("seek frame identical to t=0")
	}

	// Play from 1.0 to the end, with the soundtrack.
	before := inv.Load()
	start := time.Now()
	v.Play()
	if !v.Playing() {
		t.Fatal("not playing")
	}
	waitFor(t, "soundtrack", 3*time.Second, func() bool { return Audio().Playing(clip) })
	waitFor(t, "end", 8*time.Second, func() bool { return !v.Playing() })
	took := time.Since(start)
	if took < 700*time.Millisecond || took > 4*time.Second {
		t.Fatalf("1 s of video played in %v", took)
	}
	if frames := inv.Load() - before; frames < 15 {
		t.Fatalf("only %d frames published", frames)
	}
	if p := v.Position(); math.Abs(p-v.Duration()) > 0.01 || v.Err() != nil {
		t.Fatalf("end position %v / %v, err %v", p, v.Duration(), v.Err())
	}

	// Play at the end starts over.
	v.Play()
	time.Sleep(300 * time.Millisecond)
	if p := v.Position(); p > 1.0 {
		t.Fatalf("did not restart: pos %v", p)
	}
	v.Close()
	if v.Playing() {
		t.Fatal("playing after Close")
	}
	waitFor(t, "soundtrack stop", 2*time.Second, func() bool { return !Audio().Playing(clip) })
}

func pixelsEqual(a, b image.Image) bool {
	ra, ok1 := a.(*image.RGBA)
	rb, ok2 := b.(*image.RGBA)
	return ok1 && ok2 && bytes.Equal(ra.Pix, rb.Pix)
}

func TestVideoPauseResumeSeekWhilePlaying(t *testing.T) {
	need(t)
	v, _ := newCountingVideo(clip, 0)
	defer v.Close()
	v.Play() // before the probe finished: starts once ready
	waitFor(t, "playing frame", 10*time.Second, func() bool { return v.Frame() != nil })
	if b := v.Frame().Bounds(); b.Dx() != 320 {
		t.Fatalf("maxW 0 keeps the source size, got %v", b)
	}
	time.Sleep(400 * time.Millisecond)
	v.Pause()
	p := v.Position()
	if v.Playing() || p < 0.2 || p > 1.5 {
		t.Fatalf("pause: playing %v pos %v", v.Playing(), p)
	}
	waitFor(t, "soundtrack stop", 2*time.Second, func() bool { return !Audio().Playing(clip) })
	time.Sleep(200 * time.Millisecond)
	if v.Position() != p {
		t.Fatal("position moved while paused")
	}

	v.Seek(1.5)
	v.Play()
	time.Sleep(100 * time.Millisecond)
	v.Seek(0.2) // seek while playing restarts both
	if !v.Playing() {
		t.Fatal("seek stopped playback")
	}
	if p := v.Position(); p < 0.2 || p > 0.6 {
		t.Fatalf("position after seek %v", p)
	}
	waitFor(t, "soundtrack", 3*time.Second, func() bool { return Audio().Playing(clip) })
	v.Toggle()
	if v.Playing() {
		t.Fatal("toggle did not pause")
	}
	v.Seek(99) // clamped to the end; shows the last frame
	if math.Abs(v.Position()-v.Duration()) > 0.001 {
		t.Fatalf("seek clamp: %v", v.Position())
	}
	time.Sleep(600 * time.Millisecond)
	if v.Err() != nil {
		t.Fatalf("seek to end: %v", v.Err())
	}
}

func TestOnlyOneThingPlays(t *testing.T) {
	need(t)
	v1, _ := newCountingVideo(clip, 160)
	v2, _ := newCountingVideo(silent, 160)
	defer v1.Close()
	defer v2.Close()
	waitFor(t, "frames", 10*time.Second, func() bool { return v1.Frame() != nil && v2.Frame() != nil })

	v1.Play()
	waitFor(t, "v1 sound", 3*time.Second, func() bool { return Audio().Playing(clip) })
	v2.Play() // silent video still stops v1 and its sound
	if v1.Playing() || !v2.Playing() {
		t.Fatalf("v1 %v v2 %v", v1.Playing(), v2.Playing())
	}
	waitFor(t, "v1 sound stop", 2*time.Second, func() bool { return !Audio().Playing(clip) })

	ch := make(chan struct{})
	if err := Audio().Play(ff, tone20, done(ch)); err != nil {
		t.Fatal(err)
	}
	if v2.Playing() {
		t.Fatal("a sound must pause the video")
	}
	v1.Play() // a video stops the sound
	if !closedWithin(ch, 3*time.Second) || Audio().Playing(tone20) {
		t.Fatal("sound not stopped by video")
	}
	Audio().Stop() // stopping the soundtrack pauses its video
	if v1.Playing() {
		t.Fatal("Stop did not pause the video")
	}
}

func TestVideoBrokenFile(t *testing.T) {
	need(t)
	bad := filepath.Join(fixDir, "bad.mp4")
	_ = os.WriteFile(bad, []byte("nope"), 0o644)
	v, _ := newCountingVideo(bad, 160)
	defer v.Close()
	waitFor(t, "error", 5*time.Second, func() bool { return v.Err() != nil })
	v.Play()
	if v.Playing() || v.Frame() != nil {
		t.Fatal("broken file must not play")
	}
	v.Close()
	v.Close() // idempotent
}

func TestNoAudioDeviceTouched(t *testing.T) {
	if otoCtx != nil {
		t.Fatal("tests opened a real audio device")
	}
	if ffOK && sinks.Load() == 0 {
		t.Fatal("fake sink unused")
	}
}
