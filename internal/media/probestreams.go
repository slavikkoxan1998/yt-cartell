package media

// A detailed probe for editor exports: frame rate, frame count, audio rate
// and channels, codecs — what an editor timeline declares about its media.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"ytcartell/internal/sysx"
)

// StreamInfo is ProbeInfo plus the details an editor timeline declares.
type StreamInfo struct {
	ProbeInfo
	// FPSNum/FPSDen is the video's frame rate (r_frame_rate, else
	// avg_frame_rate); 0/0 for audio and still pictures.
	FPSNum, FPSDen int
	// Frames is the video's frame count when the container states it (0 = unknown).
	Frames int
	// SampleRate and Channels of the first audio stream (0 = no sound).
	SampleRate, Channels   int
	VideoCodec, AudioCodec string
	// Image: a single still picture (PNG, JPEG, WebP …).
	Image bool
}

// FPS is the frame rate as a number (0 when unknown).
func (s StreamInfo) FPS() float64 {
	if s.FPSNum <= 0 || s.FPSDen <= 0 {
		return 0
	}
	return float64(s.FPSNum) / float64(s.FPSDen)
}

type streamsJSON struct {
	Streams []struct {
		CodecType    string            `json:"codec_type"`
		CodecName    string            `json:"codec_name"`
		Width        int               `json:"width"`
		Height       int               `json:"height"`
		Duration     string            `json:"duration"`
		RFrameRate   string            `json:"r_frame_rate"`
		AvgFrameRate string            `json:"avg_frame_rate"`
		NbFrames     string            `json:"nb_frames"`
		SampleRate   string            `json:"sample_rate"`
		Channels     int               `json:"channels"`
		Tags         map[string]string `json:"tags"`
		Disposition  struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
		SideData []struct {
			Rotation float64 `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
	Format struct {
		Duration   string `json:"duration"`
		FormatName string `json:"format_name"`
	} `json:"format"`
}

// ProbeStreams reads the details of a media file via ffprobe JSON.
func (f FF) ProbeStreams(ctx context.Context, path string) (StreamInfo, error) {
	var info StreamInfo
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
	return parseStreams(out)
}

// parseStreams decodes ffprobe's JSON (split out for tests).
func parseStreams(out []byte) (StreamInfo, error) {
	var info StreamInfo
	var pj streamsJSON
	if err := json.Unmarshal(out, &pj); err != nil {
		return info, fmt.Errorf("ffprobe: незрозуміла відповідь: %w", err)
	}
	if len(pj.Streams) == 0 {
		return info, errors.New("файл не містить ні відео, ні звуку")
	}
	info.Duration = parseSec(pj.Format.Duration)
	format := pj.Format.FormatName
	info.Image = strings.Contains(format, "image2") || strings.HasSuffix(format, "_pipe")
	picked, sound := false, false
	for _, st := range pj.Streams {
		switch st.CodecType {
		case "audio":
			info.HasAudio = true
			if !sound {
				sound = true
				info.AudioCodec = st.CodecName
				info.SampleRate, _ = strconv.Atoi(st.SampleRate)
				info.Channels = st.Channels
			}
		case "video":
			if picked || st.Disposition.AttachedPic == 1 || st.Width <= 0 || st.Height <= 0 {
				continue
			}
			picked = true
			info.VideoCodec = st.CodecName
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
			if !info.Image {
				n, d := parseRate(st.RFrameRate)
				if a, b := parseRate(st.AvgFrameRate); a > 0 && (n <= 0 || float64(n)/float64(d) > 120) {
					n, d = a, b // r_frame_rate of some files is a timebase (90000/1)
				}
				if n > 0 && float64(n)/float64(d) <= 240 {
					info.FPSNum, info.FPSDen = n, d
				}
				info.Frames, _ = strconv.Atoi(st.NbFrames)
			}
		}
		if info.Duration <= 0 {
			if d := parseSec(st.Duration); d > info.Duration {
				info.Duration = d
			}
		}
	}
	if info.Image {
		info.Duration = 0
	}
	return info, nil
}

// parseRate reads "30000/1001" (or "24") as a positive fraction; 0, 0 when invalid.
func parseRate(s string) (int, int) {
	a, b, ok := strings.Cut(strings.TrimSpace(s), "/")
	n, err := strconv.Atoi(a)
	if err != nil || n <= 0 {
		return 0, 0
	}
	d := 1
	if ok {
		if d, err = strconv.Atoi(b); err != nil || d <= 0 {
			return 0, 0
		}
	}
	return n, d
}
