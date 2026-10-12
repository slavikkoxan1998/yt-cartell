package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// lastFrameTail is how much of the end of a clip LastFrame decodes first.
const lastFrameTail = "-2"

// LastFrame writes the final decodable picture of video to outJPG (JPEG).
//
// Only the tail of the file is decoded (-sseof, accurate seek from the
// previous keyframe) and every decoded frame overwrites the same image
// (-update 1), so the file ends up holding the frame that decoded LAST in
// presentation order — the true final frame, not the one nearest to some
// timestamp (B-frame reordering and variable frame rates are handled by the
// decoder). When the tail holds no picture (an audio track that outlasts the
// video) the whole file is decoded instead. outJPG is replaced atomically.
func (f FF) LastFrame(ctx context.Context, video, outJPG string) error {
	if f.FFmpeg == "" {
		return ErrNotFound
	}
	if !isFile(video) {
		return fmt.Errorf("відео не знайдено: %s", video)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
	}
	if err := os.MkdirAll(filepath.Dir(outJPG), 0o755); err != nil {
		return err
	}
	tmp := outJPG + ".part"
	defer os.Remove(tmp)
	grab := func(tail bool) error {
		_ = os.Remove(tmp)
		var args []string
		if tail {
			args = append(args, "-sseof", lastFrameTail)
		}
		args = append(args, "-i", video, "-map", "0:v:0", "-an", "-sn", "-dn",
			"-c:v", "mjpeg", "-q:v", "2", "-update", "1", "-f", "image2", tmp)
		err := f.Run(ctx, "", args...)
		if err == nil && !nonEmpty(tmp) {
			err = errors.New("у відео немає кадрів")
		}
		return err
	}
	err := grab(true)
	if err != nil && ctx.Err() == nil {
		err = grab(false)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("не вдалося взяти останній кадр відео: %w", err)
	}
	return replaceFile(tmp, outJPG)
}

func nonEmpty(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Size() > 0
}

// replaceFile renames src over dst, retrying briefly: on Windows a reader
// (player, antivirus, indexer) may hold dst for a moment.
func replaceFile(src, dst string) error {
	var err error
	for i := 0; i < 6; i++ {
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 40 * time.Millisecond)
	}
	return err
}
