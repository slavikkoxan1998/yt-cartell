package tgbot

// FFmpeg for deliveries over the upload limit: a two-pass re-encode to a
// target size and a stream-copy split into parts.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/media"
	"ytcartell/internal/sysx"
)

// Bitrate plan of a re-encode (kbit/s).
type encodePlan struct {
	video, audio int
	scale        string // the -vf scale filter ("" = keep the size)
}

// planEncode spreads target bytes over dur seconds: the audio first (128,
// 96 or 64 kbit/s as the budget allows), the rest for the picture with 4 %
// for the container; the picture goes down to 720p (480p on a tiny budget)
// when there are not enough bits for its size.
func planEncode(target int64, dur float64, w, h int, audio bool) (encodePlan, error) {
	if dur <= 0 {
		return encodePlan{}, errors.New("невідома тривалість відео")
	}
	total := float64(target) * 8 / dur / 1000 * 0.96
	var p encodePlan
	if audio {
		switch {
		case total > 1500:
			p.audio = 128
		case total > 600:
			p.audio = 96
		default:
			p.audio = 64
		}
	}
	p.video = int(total) - p.audio
	if p.video < 150 {
		return encodePlan{}, fmt.Errorf("відео задовге (%s), щоб вмістити його в такий розмір без каші — надішли частинами", clock(dur))
	}
	short := min(w, h)
	to := 0
	switch {
	case short > 480 && p.video < 900:
		to = 480
	case short > 720 && p.video < 3000:
		to = 720
	}
	if to > 0 {
		if w >= h {
			p.scale = "scale=-2:" + strconv.Itoa(to)
		} else {
			p.scale = "scale=" + strconv.Itoa(to) + ":-2"
		}
	}
	return p, nil
}

// compress re-encodes src into out (two-pass x264 + AAC) to fit target
// bytes; dir is a scratch folder (the pass log is written there under a
// plain ASCII name — x264 cannot open non-ASCII paths on Windows).
// progress gets 0…1.
func compress(ctx context.Context, ff media.FF, src, dir, out string, info media.ProbeInfo, target int64, progress func(float64)) error {
	plan, err := planEncode(target, info.Duration, info.Width, info.Height, info.HasAudio || info.Width == 0)
	if err != nil {
		return err
	}
	for try := 0; try < 3; try++ {
		vf := []string{}
		if plan.scale != "" {
			vf = []string{"-vf", plan.scale}
		}
		common := append([]string{"-i", src}, vf...)
		common = append(common, "-c:v", "libx264", "-preset", "fast", "-pix_fmt", "yuv420p",
			"-b:v", strconv.Itoa(plan.video)+"k", "-passlogfile", "ytcpass")
		if try == 0 {
			pass1 := append(append([]string{}, common...), "-pass", "1", "-an", "-f", "mp4", os.DevNull)
			if err := runFF(ctx, ff, dir, info.Duration, func(f float64) { progress(f * 0.45) }, pass1...); err != nil {
				return err
			}
		}
		pass2 := append(append([]string{}, common...), "-pass", "2")
		if plan.audio > 0 {
			pass2 = append(pass2, "-c:a", "aac", "-b:a", strconv.Itoa(plan.audio)+"k", "-ac", "2")
		} else {
			pass2 = append(pass2, "-an")
		}
		pass2 = append(pass2, "-movflags", "+faststart", out)
		if err := runFF(ctx, ff, dir, info.Duration, func(f float64) { progress(0.45 + f*0.55) }, pass2...); err != nil {
			return err
		}
		st, err := os.Stat(out)
		if err != nil {
			return err
		}
		if st.Size() <= target {
			progress(1)
			return nil
		}
		// Over the target (x264 missed it): 10 % less for the picture.
		plan.video = int(float64(plan.video) * 0.9 * float64(target) / float64(st.Size()))
		if plan.video < 150 {
			break
		}
	}
	return errors.New("не вдалося вмістити відео в потрібний розмір — надішли частинами")
}

// split stream-copies src into dir/partNN.mp4 of at most limit bytes each
// (cut at keyframes; when a part comes out too big the cut is redone shorter).
func split(ctx context.Context, ff media.FF, src, dir string, size int64, dur float64, limit int64) ([]string, error) {
	if dur <= 0 {
		return nil, errors.New("невідома тривалість відео")
	}
	n := math.Ceil(float64(size) / (float64(limit) * 0.85))
	seg := dur / n
	for try := 0; try < 4; try++ {
		for _, p := range partsIn(dir) {
			_ = os.Remove(p)
		}
		args := []string{"-i", src, "-map", "0:v:0", "-map", "0:a?", "-c", "copy", "-f", "segment",
			"-segment_time", strconv.FormatFloat(seg, 'f', 2, 64), "-reset_timestamps", "1",
			"-segment_format_options", "movflags=+faststart", "part%02d.mp4"}
		if err := runFF(ctx, ff, dir, 0, nil, args...); err != nil {
			return nil, err
		}
		parts := partsIn(dir)
		if len(parts) == 0 {
			return nil, errors.New("FFmpeg не створив жодної частини")
		}
		var biggest int64
		for _, p := range parts {
			st, err := os.Stat(p)
			if err != nil {
				return nil, err
			}
			biggest = max(biggest, st.Size())
		}
		if biggest <= limit {
			return parts, nil
		}
		// The bitrate is uneven (the start of a film takes more): shorter
		// parts in proportion to the overshoot.
		seg *= min(0.8, 0.9*float64(limit)/float64(biggest))
		if seg < 0.4 {
			break
		}
	}
	return nil, errors.New("не вдалося розрізати відео на достатньо малі частини")
}

// runFF runs ffmpeg in dir; with total > 0 and progress, -progress pipe:1
// reports the done share. Errors carry the tail of stderr.
func runFF(ctx context.Context, ff media.FF, dir string, total float64, progress func(float64), args ...string) error {
	if ff.FFmpeg == "" {
		return media.ErrNotFound
	}
	full := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	if progress != nil && total > 0 {
		full = append(full, "-progress", "pipe:1", "-nostats")
	}
	cmd := sysx.Command(ctx, ff.FFmpeg, append(full, args...)...)
	cmd.Dir = dir
	var stderr tail
	cmd.Stderr = &stderr
	if progress != nil && total > 0 {
		cmd.Stdout = &progressLines{total: total, report: progress}
	}
	err := sysx.Run(cmd)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Errorf("ffmpeg %d: %s", ee.ExitCode(), stderr.String())
	}
	return err
}

// progressLines reads ffmpeg's "out_time_us=…" lines.
type progressLines struct {
	total  float64
	report func(float64)
	buf    []byte
	last   time.Time
}

func (p *progressLines) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(p.buf[:i])
		p.buf = p.buf[i+1:]
		v, ok := bytes.CutPrefix(line, []byte("out_time_us="))
		if !ok {
			continue
		}
		us, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil || us < 0 {
			continue
		}
		if now := time.Now(); now.Sub(p.last) >= 250*time.Millisecond {
			p.last = now
			p.report(min(1, float64(us)/1e6/p.total))
		}
	}
	if len(p.buf) > 4096 {
		p.buf = p.buf[:0]
	}
	return len(b), nil
}

// tail keeps the last 2 KB of stderr.
type tail struct {
	mu sync.Mutex
	b  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 2048 {
		t.b = t.b[len(t.b)-2048:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := strings.TrimSpace(strings.ToValidUTF8(string(t.b), ""))
	if r := []rune(s); len(r) > 300 {
		s = string(r[len(r)-300:])
	}
	return s
}
