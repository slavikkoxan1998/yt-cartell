// Package player plays media inside the app window, without a browser: sound
// through one shared oto context (48 kHz, stereo, s16le) and video as RGBA
// frames — both decoded by ffmpeg into pipes. Only one thing plays at a time:
// starting a sound pauses the playing video and vice versa.
package player

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"

	"ytcartell/internal/media"
	"ytcartell/internal/sysx"
)

const (
	sampleRate  = 48000
	channels    = 2
	frameBytes  = channels * 2 // one s16le stereo sample frame
	bytesPerSec = sampleRate * frameBytes
)

// sink is the audio output fed by a PCM reader (*oto.Player in the app).
type sink interface {
	Play()
	Pause()
	IsPlaying() bool
}

// newSink opens the output for r. Tests replace it so no audio device is touched.
var newSink = otoSink

var (
	otoOnce sync.Once
	otoCtx  *oto.Context
	otoErr  error
)

// otoSink creates the process-wide oto context on first use (oto allows only one).
func otoSink(r io.Reader) (sink, error) {
	otoOnce.Do(func() {
		c, ready, err := oto.NewContext(&oto.NewContextOptions{
			SampleRate:   sampleRate,
			ChannelCount: channels,
			Format:       oto.FormatSignedInt16LE,
		})
		if err != nil {
			otoErr = err
			return
		}
		select {
		case <-ready:
		case <-time.After(10 * time.Second):
			otoErr = errors.New("аудіопристрій не відповідає")
			return
		}
		if err := c.Err(); err != nil {
			otoErr = err
			return
		}
		otoCtx = c
	})
	if otoErr != nil {
		return nil, fmt.Errorf("Не вдалося увімкнути звук: %v", otoErr)
	}
	return otoCtx.NewPlayer(r), nil
}

// AudioPlayer plays one sound at a time (voice lines, voice previews, the
// soundtrack of a Video). Safe for concurrent use.
type AudioPlayer struct {
	mu  sync.Mutex
	cur *playback
	err error // why the last playback failed (nil when it ended fine)
}

type playback struct {
	src    string
	owner  *Video // the Video this is the soundtrack of; nil for plain sounds
	out    sink
	cancel context.CancelFunc
	stop   chan struct{}
	once   sync.Once
	onDone func()
}

// halt silences the output and kills the decoder (idempotent).
func (pb *playback) halt() {
	pb.once.Do(func() {
		close(pb.stop)
		pb.out.Pause()
		pb.cancel()
	})
}

func (pb *playback) halted() bool {
	select {
	case <-pb.stop:
		return true
	default:
		return false
	}
}

var theAudio = &AudioPlayer{}

// Audio returns the process-wide player.
func Audio() *AudioPlayer { return theAudio }

// Play starts src (a file path or an http(s) URL) from the beginning, stopping
// whatever plays now (a playing Video is paused). onDone (may be nil) runs once,
// on a background goroutine, when this playback ends for any reason — finished,
// stopped or replaced.
func (a *AudioPlayer) Play(ff media.FF, src string, onDone func()) error {
	claim(nil)
	return a.start(ff, src, 0, 0, nil, onDone)
}

// PlayRange is Play limited to the part of src from at (seconds) lasting dur
// seconds (dur ≤ 0 = to the end) — e.g. a trimmed voice line.
func (a *AudioPlayer) PlayRange(ff media.FF, src string, at, dur float64, onDone func()) error {
	claim(nil)
	return a.start(ff, src, max(0, at), dur, nil, onDone)
}

// Stop silences the current sound; if it is a Video's soundtrack, the video pauses too.
func (a *AudioPlayer) Stop() {
	a.mu.Lock()
	pb := a.cur
	a.cur = nil
	a.mu.Unlock()
	if pb == nil {
		return
	}
	pb.halt()
	if pb.owner != nil {
		pb.owner.Pause()
	}
}

// Playing reports whether src is the sound playing right now.
func (a *AudioPlayer) Playing(src string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cur != nil && a.cur.src == src && !a.cur.halted()
}

// Err explains why the last playback failed (bad file, unreachable URL…); nil otherwise.
func (a *AudioPlayer) Err() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

func isURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// start decodes src from `at` seconds through ffmpeg into a new output.
func (a *AudioPlayer) start(ff media.FF, src string, at, dur float64, owner *Video, onDone func()) error {
	a.mu.Lock()
	prev := a.cur
	a.cur = nil
	a.mu.Unlock()
	if prev != nil {
		prev.halt()
	}
	if ff.FFmpeg == "" {
		return media.ErrNotFound
	}
	if strings.TrimSpace(src) == "" {
		return errors.New("Немає що відтворювати")
	}
	if !isURL(src) {
		if st, err := os.Stat(src); err != nil || st.IsDir() {
			return fmt.Errorf("Файл не знайдено: %s", filepath.Base(src))
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if at > 0.005 {
		args = append(args, "-ss", strconv.FormatFloat(at, 'f', 3, 64))
	}
	if dur > 0.005 {
		args = append(args, "-t", strconv.FormatFloat(dur, 'f', 3, 64))
	}
	args = append(args, "-i", src, "-vn", "-sn", "-dn",
		"-f", "s16le", "-acodec", "pcm_s16le", "-ar", strconv.Itoa(sampleRate), "-ac", strconv.Itoa(channels), "pipe:1")
	cmd := exec.CommandContext(ctx, ff.FFmpeg, args...)
	hideConsole(cmd)
	cmd.WaitDelay = 2 * time.Second
	stderr := &tailBuffer{max: 2 << 10}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = sysx.Start(cmd)
	}
	if err != nil {
		cancel()
		return fmt.Errorf("FFmpeg не запустився: %w", err)
	}
	pr := newPCMReader(stdout)
	out, err := newSink(pr)
	if err != nil {
		cancel()
		_ = cmd.Wait()
		return err
	}
	pb := &playback{src: src, owner: owner, out: out, cancel: cancel, stop: make(chan struct{}), onDone: onDone}

	a.mu.Lock()
	prev = a.cur // a concurrent start may have slipped in
	a.cur = pb
	a.err = nil
	a.mu.Unlock()
	if prev != nil {
		prev.halt()
	}
	out.Play()
	go a.watch(pb, cmd, pr, stderr)
	return nil
}

// watch waits for the end of a playback (source drained or halted), reaps
// ffmpeg and fires onDone.
func (a *AudioPlayer) watch(pb *playback, cmd *exec.Cmd, pr *pcmReader, stderr *tailBuffer) {
	select {
	case <-pr.eof:
		// Let the buffered tail play out.
		t := time.NewTicker(20 * time.Millisecond)
	drain:
		for pb.out.IsPlaying() {
			select {
			case <-pb.stop:
				break drain
			case <-t.C:
			}
		}
		t.Stop()
	case <-pb.stop:
	}
	halted := pb.halted()
	if halted {
		pb.cancel()
	}
	werr := cmd.Wait() // after a natural end ffmpeg is already exiting
	pb.cancel()
	halted = halted || pb.halted()

	a.mu.Lock()
	if a.cur == pb {
		a.cur = nil
	}
	if !halted && werr != nil {
		msg := stderr.tail(300)
		if msg == "" {
			msg = werr.Error()
		}
		a.err = fmt.Errorf("Не вдалося відтворити: %s", msg)
	}
	a.mu.Unlock()
	if pb.onDone != nil {
		pb.onDone()
	}
}

// stopOwner silences v's soundtrack (if it is what plays).
func (a *AudioPlayer) stopOwner(v *Video) {
	a.mu.Lock()
	pb := a.cur
	if pb == nil || pb.owner != v {
		pb = nil
	} else {
		a.cur = nil
	}
	a.mu.Unlock()
	if pb != nil {
		pb.halt()
	}
}

// stopOthers silences anything that is not v's soundtrack.
func (a *AudioPlayer) stopOthers(v *Video) {
	a.mu.Lock()
	pb := a.cur
	if pb == nil || pb.owner == v {
		pb = nil
	} else {
		a.cur = nil
	}
	a.mu.Unlock()
	if pb != nil {
		pb.halt()
	}
}

// ---- exclusivity ----------------------------------------------------------------------

var excl struct {
	sync.Mutex
	v *Video
}

// claim makes v (nil = a plain sound) the only thing playing: the previously
// playing video is paused, and for a video any other sound is stopped.
func claim(v *Video) {
	excl.Lock()
	prev := excl.v
	excl.v = v
	excl.Unlock()
	if prev != nil && prev != v {
		prev.Pause()
	}
	if v != nil {
		theAudio.stopOthers(v)
	}
}

func release(v *Video) {
	excl.Lock()
	if excl.v == v {
		excl.v = nil
	}
	excl.Unlock()
}

// ---- pipes ------------------------------------------------------------------------------

// pcmReader hands oto whole sample frames only (oto mixes partial reads without
// re-aligning channels) and turns any read error into io.EOF — a killed decoder
// just ends the sound.
type pcmReader struct {
	r     io.Reader
	carry [frameBytes]byte
	nc    int
	eof   chan struct{}
	once  sync.Once
}

func newPCMReader(r io.Reader) *pcmReader { return &pcmReader{r: r, eof: make(chan struct{})} }

func (p *pcmReader) Read(b []byte) (int, error) {
	if len(b) < frameBytes {
		return 0, nil
	}
	n := copy(b, p.carry[:p.nc])
	p.nc = 0
	m, err := p.r.Read(b[n:])
	n += m
	if err != nil {
		n -= n % frameBytes
		p.once.Do(func() { close(p.eof) })
		return n, io.EOF
	}
	if rem := n % frameBytes; rem != 0 {
		p.nc = copy(p.carry[:], b[n-rem:n])
		n -= rem
	}
	return n, nil
}

// tailBuffer keeps the last max bytes written (ffmpeg stderr).
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append(t.b[:0], t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) tail(n int) string {
	t.mu.Lock()
	s := strings.TrimSpace(strings.ToValidUTF8(string(t.b), ""))
	t.mu.Unlock()
	if r := []rune(s); len(r) > n {
		s = string(r[len(r)-n:])
	}
	return s
}
