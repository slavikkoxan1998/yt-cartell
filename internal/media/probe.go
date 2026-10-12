package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"os/exec"
	"strconv"
	"time"

	"ytcartell/internal/sysx"
)

// ProbeInfo is what the app needs to know about a media file. Width/Height are
// display dimensions of the first real video stream (rotation applied, cover
// art ignored); 0 when there is no picture.
type ProbeInfo struct {
	Width, Height int
	Duration      float64
	HasAudio      bool
}

type probeJSON struct {
	Streams []struct {
		CodecType   string            `json:"codec_type"`
		Width       int               `json:"width"`
		Height      int               `json:"height"`
		Duration    string            `json:"duration"`
		Tags        map[string]string `json:"tags"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
		SideData []struct {
			Rotation float64 `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Probe reads dimensions, duration and audio presence via ffprobe JSON.
func (f FF) Probe(ctx context.Context, path string) (ProbeInfo, error) {
	var info ProbeInfo
	if f.FFprobe == "" {
		return info, ErrNoProbe
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}
	cmd := command(ctx, f.FFprobe, "-v", "error", "-print_format", "json", "-show_streams", "-show_format", path)
	stderr := &tailBuffer{max: 4 << 10}
	cmd.Stderr = stderr
	out, err := sysx.Output(cmd)
	if err != nil {
		if ctx.Err() != nil {
			return info, ctx.Err()
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return info, fmt.Errorf("ffprobe %d: %s", ee.ExitCode(), stderr.Tail(300))
		}
		return info, fmt.Errorf("ffprobe не запускається: %w", err)
	}
	var pj probeJSON
	if err := json.Unmarshal(out, &pj); err != nil {
		return info, fmt.Errorf("ffprobe: незрозуміла відповідь: %w", err)
	}
	if len(pj.Streams) == 0 {
		return info, errors.New("файл не містить ні відео, ні звуку")
	}
	info.Duration = parseSec(pj.Format.Duration)
	picked := false
	for _, st := range pj.Streams {
		switch st.CodecType {
		case "audio":
			info.HasAudio = true
		case "video":
			if picked || st.Disposition.AttachedPic == 1 || st.Width <= 0 || st.Height <= 0 {
				continue
			}
			picked = true
			info.Width, info.Height = st.Width, st.Height
			rot := 0.0
			if r, err := strconv.ParseFloat(st.Tags["rotate"], 64); err == nil {
				rot = r
			}
			for _, sd := range st.SideData {
				if sd.Rotation != 0 {
					rot = sd.Rotation
				}
			}
			if int(math.Abs(math.Round(rot)))%180 == 90 {
				info.Width, info.Height = info.Height, info.Width
			}
		}
		if info.Duration <= 0 {
			if d := parseSec(st.Duration); d > info.Duration {
				info.Duration = d
			}
		}
	}
	return info, nil
}

func parseSec(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	return v
}

// probeDuration is the TS probeDuration(): seconds, 0 when unknown.
func (f FF) probeDuration(ctx context.Context, path string) float64 {
	info, err := f.Probe(ctx, path)
	if err != nil {
		return 0
	}
	return info.Duration
}

// Thumbnail grabs one frame at `at` seconds (falls back to the start when `at`
// is past the end), scaled down to at most maxW pixels wide (0 = original).
// Works for videos and still images.
func (f FF) Thumbnail(ctx context.Context, video string, at float64, maxW int) (image.Image, error) {
	if f.FFmpeg == "" {
		return nil, ErrNotFound
	}
	img, err := f.grabPNG(ctx, video, at, maxW)
	if err == nil || ctx.Err() != nil || at <= 0 {
		return img, err
	}
	return f.grabPNG(ctx, video, 0, maxW)
}

func (f FF) grabPNG(ctx context.Context, src string, at float64, maxW int) (image.Image, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if at > 0 {
		args = append(args, "-ss", strconv.FormatFloat(at, 'f', 3, 64))
	}
	args = append(args, "-i", src, "-an", "-sn", "-dn", "-frames:v", "1")
	if maxW > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale=w='min(%d,iw)':h=-2", maxW))
	}
	args = append(args, "-f", "image2pipe", "-c:v", "png", "pipe:1")
	cmd := command(ctx, f.FFmpeg, args...)
	stderr := &tailBuffer{max: 4 << 10}
	cmd.Stderr = stderr
	out, err := sysx.Output(cmd)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("ffmpeg %d: %s", ee.ExitCode(), stderr.Tail(300))
		}
		return nil, fmt.Errorf("FFmpeg не знайдено: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("не вдалося отримати кадр з файлу")
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("кадр пошкоджено: %w", err)
	}
	return img, nil
}
