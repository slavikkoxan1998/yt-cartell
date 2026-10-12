package player

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"ytcartell/internal/media"
	"ytcartell/internal/sysx"
)

const fps = 24

// Video plays a clip inside the UI: ffmpeg decodes RGBA frames (scaled to at
// most maxW wide, 24 fps) into a pipe, paced by the wall clock; the soundtrack
// goes through the shared AudioPlayer from the same position. A/V sync is kept
// simple: Play and Seek restart both from the same point.
//
// Frame returns a new image each time the picture changes (images are never
// mutated after they are published), so callers can compare pointers.
type Video struct {
	ff         media.FF
	path       string
	maxW       int
	invalidate func()

	ctx     context.Context // cancelled by Close
	closeFn context.CancelFunc

	ctl sync.Mutex // serialises Play/Pause/Seek/Close and the post-probe start

	mu      sync.Mutex
	probed  bool
	broken  bool // probe failed: nothing can be played
	info    media.ProbeInfo
	w, h    int // decoded frame size (0 = no picture)
	frame   *image.RGBA
	pos     float64   // paused position, or where the current run started
	clock   time.Time // wall time at which pos was current (while playing)
	playing bool
	closed  bool
	gen     uint64             // bumps on every Play/Pause/Seek/end; stale decoders drop out
	stopDec context.CancelFunc // cancels the current decoder
	err     error
}

// NewVideo returns immediately; probing and the first frame (t=0) are decoded in
// the background, invalidate (may be nil) is called whenever something changes.
func NewVideo(ff media.FF, path string, maxW int, invalidate func()) *Video {
	if invalidate == nil {
		invalidate = func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	v := &Video{ff: ff, path: path, maxW: maxW, invalidate: invalidate, ctx: ctx, closeFn: cancel}
	go v.open()
	return v
}

// fitSize scales w×h down to maxW wide (never up), even dimensions for the scaler.
func fitSize(w, h, maxW int) (int, int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	tw := w
	if maxW > 0 && tw > maxW {
		tw = maxW
	}
	th := int(math.Round(float64(h) * float64(tw) / float64(w)))
	tw, th = max(2, tw&^1), max(2, th&^1)
	return tw, th
}

func (v *Video) open() {
	info, err := v.ff.Probe(v.ctx, v.path)
	if err == nil && info.Width == 0 && !info.HasAudio {
		err = errors.New("файл не містить ні відео, ні звуку")
	}
	v.ctl.Lock()
	defer v.ctl.Unlock()
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	v.probed = true
	v.info = info
	v.w, v.h = fitSize(info.Width, info.Height, v.maxW)
	if err != nil {
		v.broken = true
		v.err = err
		v.playing = false
		v.mu.Unlock()
		release(v)
		v.invalidate()
		return
	}
	gen, ctx := v.restartLocked()
	playing, pos, hasPicture := v.playing, v.pos, v.w > 0
	if playing {
		v.clock = time.Now()
	}
	v.mu.Unlock()
	if playing {
		v.launch(ctx, gen, pos)
	} else if hasPicture {
		go v.still(ctx, gen, pos, false)
	}
	v.invalidate()
}

func (v *Video) haltLocked() {
	v.gen++
	if v.stopDec != nil {
		v.stopDec()
		v.stopDec = nil
	}
}

func (v *Video) restartLocked() (uint64, context.Context) {
	v.haltLocked()
	ctx, cancel := context.WithCancel(v.ctx)
	v.stopDec = cancel
	return v.gen, ctx
}

func (v *Video) positionLocked() float64 {
	p := v.pos
	if v.playing {
		p += time.Since(v.clock).Seconds()
	}
	if d := v.info.Duration; d > 0 && p > d {
		p = d
	}
	return max(p, 0)
}

// Play starts (or resumes) playback; from the end it starts over.
func (v *Video) Play() {
	claim(v)
	v.ctl.Lock()
	defer v.ctl.Unlock()
	v.mu.Lock()
	if v.closed || v.playing || v.broken {
		v.mu.Unlock()
		if !v.Playing() {
			release(v)
		}
		return
	}
	if d := v.info.Duration; d > 0 && v.pos >= d-0.05 {
		v.pos = 0
	}
	v.playing = true
	v.err = nil
	v.clock = time.Now()
	gen, ctx := v.restartLocked()
	probed, pos := v.probed, v.pos
	v.mu.Unlock()
	if probed { // otherwise open() starts it
		v.launch(ctx, gen, pos)
	}
	v.invalidate()
}

// Pause stops picture and sound, keeping the position and the last frame.
func (v *Video) Pause() {
	v.ctl.Lock()
	defer v.ctl.Unlock()
	v.mu.Lock()
	if !v.playing {
		v.mu.Unlock()
		return
	}
	v.pos = v.positionLocked()
	v.playing = false
	v.haltLocked()
	v.mu.Unlock()
	theAudio.stopOwner(v)
	release(v)
	v.invalidate()
}

// Toggle plays when paused and pauses when playing.
func (v *Video) Toggle() {
	if v.Playing() {
		v.Pause()
	} else {
		v.Play()
	}
}

// Seek jumps to sec: while playing both picture and sound restart there,
// while paused the frame at sec is shown.
func (v *Video) Seek(sec float64) {
	v.ctl.Lock()
	defer v.ctl.Unlock()
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	if math.IsNaN(sec) || sec < 0 {
		sec = 0
	}
	if d := v.info.Duration; d > 0 && sec > d {
		sec = d
	}
	v.pos = sec
	v.clock = time.Now()
	gen, ctx := v.restartLocked()
	probed, playing, broken, hasPicture := v.probed, v.playing, v.broken, v.w > 0
	v.mu.Unlock()
	if probed && !broken {
		if playing {
			v.launch(ctx, gen, sec)
		} else if hasPicture {
			go v.still(ctx, gen, sec, true)
		}
	}
	v.invalidate()
}

// Frame is the latest decoded picture (nil until the first one is ready).
func (v *Video) Frame() image.Image {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.frame == nil {
		return nil
	}
	return v.frame
}

// Position in seconds (runs with the wall clock while playing).
func (v *Video) Position() float64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.positionLocked()
}

// Duration in seconds (0 until probed).
func (v *Video) Duration() float64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.info.Duration
}

// Playing reports whether playback runs.
func (v *Video) Playing() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.playing
}

// Err is the last problem (unreadable file, decoder failure, no sound device).
func (v *Video) Err() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.err
}

// Close stops everything and kills the decoders; the Video is unusable afterwards.
func (v *Video) Close() {
	v.ctl.Lock()
	defer v.ctl.Unlock()
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	v.closed = true
	v.playing = false
	v.haltLocked()
	v.mu.Unlock()
	v.closeFn()
	theAudio.stopOwner(v)
	release(v)
}

// launch starts sound and picture for run gen from pos (ctl held).
func (v *Video) launch(ctx context.Context, gen uint64, pos float64) {
	v.mu.Lock()
	hasAudio, hasPicture := v.info.HasAudio, v.w > 0
	v.mu.Unlock()
	var onDone func()
	if !hasPicture { // sound only: the sound decides when it ends
		onDone = func() { v.finish(gen, nil) }
	}
	if hasAudio {
		if err := theAudio.start(v.ff, v.path, pos, 0, v, onDone); err != nil {
			if !hasPicture {
				v.finish(gen, err)
				return
			}
			v.mu.Lock()
			if v.gen == gen {
				v.err = err // keep the picture going, silently
			}
			v.mu.Unlock()
		}
	}
	if hasPicture {
		go v.decode(ctx, gen, pos)
	}
}

// finish ends run gen (natural end or failure).
func (v *Video) finish(gen uint64, err error) {
	v.mu.Lock()
	if v.gen != gen || !v.playing {
		v.mu.Unlock()
		return
	}
	if d := v.info.Duration; d > 0 && err == nil {
		v.pos = d
	} else {
		v.pos = v.positionLocked()
	}
	v.playing = false
	if err != nil {
		v.err = err
	}
	v.haltLocked()
	v.mu.Unlock()
	release(v)
	v.invalidate()
}

func (v *Video) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, v.ff.FFmpeg, args...)
	hideConsole(cmd)
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

func ss(sec float64) []string {
	if sec <= 0.005 {
		return nil
	}
	return []string{"-ss", strconv.FormatFloat(sec, 'f', 3, 64)}
}

// decode streams frames from `from` and publishes each one when it is due.
func (v *Video) decode(parent context.Context, gen uint64, from float64) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	v.mu.Lock()
	w, h, clock := v.w, v.h, v.clock
	v.mu.Unlock()

	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, ss(from)...)
	args = append(args, "-i", v.path, "-an", "-sn", "-dn",
		"-vf", fmt.Sprintf("scale=%d:%d,fps=%d", w, h, fps),
		"-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1")
	cmd := v.command(ctx, args...)
	stderr := &tailBuffer{max: 2 << 10}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = sysx.Start(cmd)
	}
	if err != nil {
		if ctx.Err() == nil {
			v.finish(gen, fmt.Errorf("FFmpeg не запустився: %w", err))
		}
		return
	}

	stopped := false
	for n := 0; ; n++ {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		if _, err := io.ReadFull(stdout, img.Pix); err != nil {
			break // end of stream (or the decoder died — Wait tells)
		}
		if d := time.Until(clock.Add(time.Duration(float64(n) * float64(time.Second) / fps))); d > 0 {
			t := time.NewTimer(d)
			select {
			case <-ctx.Done():
			case <-t.C:
			}
			t.Stop()
		}
		v.mu.Lock()
		current := v.gen == gen && ctx.Err() == nil
		if current {
			v.frame = img
		}
		v.mu.Unlock()
		if !current {
			stopped = true
			break
		}
		v.invalidate()
	}
	if stopped || ctx.Err() != nil {
		cancel() // an abandoned decoder would block on a full pipe
		_ = cmd.Wait()
		return // superseded or closed
	}
	werr := cmd.Wait() // stdout ended: ffmpeg is exiting on its own
	if ctx.Err() != nil {
		return
	}
	if werr != nil {
		msg := stderr.tail(300)
		if msg == "" {
			msg = werr.Error()
		}
		v.finish(gen, fmt.Errorf("Не вдалося відтворити відео: %s", msg))
		return
	}
	v.finish(gen, nil)
}

// still shows the frame at `at` while paused (debounced for scrubbing).
func (v *Video) still(ctx context.Context, gen uint64, at float64, debounce bool) {
	if debounce {
		t := time.NewTimer(40 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
	v.mu.Lock()
	w, h, dur := v.w, v.h, v.info.Duration
	v.mu.Unlock()
	img, err := v.grab(ctx, at, w, h)
	if err != nil && ctx.Err() == nil && at > 0 {
		// Past the last frame: show the end of the clip instead.
		back := at - 0.5
		if dur > 0 {
			back = min(back, dur-0.5)
		}
		img, err = v.grab(ctx, max(0, back), w, h)
	}
	if ctx.Err() != nil {
		return
	}
	v.mu.Lock()
	if v.gen == gen {
		if img != nil {
			v.frame = img
		}
		if err != nil {
			v.err = err
		}
	}
	v.mu.Unlock()
	v.invalidate()
}

// grab decodes the single frame at `at`.
func (v *Video) grab(ctx context.Context, at float64, w, h int) (*image.RGBA, error) {
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, ss(at)...)
	args = append(args, "-i", v.path, "-an", "-sn", "-dn", "-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d", w, h),
		"-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1")
	cmd := v.command(ctx, args...)
	stderr := &tailBuffer{max: 2 << 10}
	cmd.Stderr = stderr
	out, err := sysx.Output(cmd)
	size := w * h * 4
	if len(out) >= size {
		return &image.RGBA{Pix: out[:size:size], Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}, nil
	}
	if err != nil && ctx.Err() == nil {
		if msg := stderr.tail(300); msg != "" {
			return nil, fmt.Errorf("Не вдалося прочитати кадр: %s", msg)
		}
		return nil, fmt.Errorf("Не вдалося прочитати кадр: %w", err)
	}
	return nil, errors.New("Не вдалося прочитати кадр")
}
