package media

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// numberedClip renders a clip whose frame N is the solid colour
// (N*10 mod 250, 255 - that, 128), encoded with B-frames and a short GOP. audio is the
// length of its sine track (0 = none).
func numberedClip(t *testing.T, f FF, path string, frames int, audio float64) {
	t.Helper()
	args := []string{"-f", "lavfi", "-i", fmt.Sprintf("nullsrc=s=64x36:r=24:d=%.4f,geq=r='mod(N*10,250)':g='255-mod(N*10,250)':b=128", float64(frames)/24)}
	if audio > 0 {
		args = append(args, "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:sample_rate=48000:d=%g", audio), "-c:a", "aac")
	}
	args = append(args, "-c:v", "libx264", "-bf", "3", "-g", "12", "-pix_fmt", "yuv420p", path)
	if err := f.Run(context.Background(), "", args...); err != nil {
		t.Fatalf("make clip: %v", err)
	}
}

// decodedFrames returns the first pixel (r,g,b) of every frame as the decoder
// outputs them, in presentation order.
func decodedFrames(t *testing.T, f FF, video string) [][3]int {
	t.Helper()
	out, err := exec.Command(f.FFmpeg, "-hide_banner", "-loglevel", "error", "-i", video, "-map", "0:v:0",
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	const frame = 64 * 36 * 3
	var px [][3]int
	for i := 0; i+frame <= len(out); i += frame {
		px = append(px, [3]int{int(out[i]), int(out[i+1]), int(out[i+2])})
	}
	return px
}

func jpegColor(t *testing.T, path string) [3]int {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	img, err := jpeg.Decode(fh)
	if err != nil {
		t.Fatalf("%s is not a JPEG: %v", path, err)
	}
	return avgColor(img, img.Bounds())
}

func avgColor(img image.Image, r image.Rectangle) [3]int {
	var s [3]int64
	n := int64(0)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			s[0] += int64(cr >> 8)
			s[1] += int64(cg >> 8)
			s[2] += int64(cb >> 8)
			n++
		}
	}
	if n == 0 {
		return [3]int{}
	}
	return [3]int{int(s[0] / n), int(s[1] / n), int(s[2] / n)}
}

func closeRGB(a, b [3]int, tol int) bool {
	for i := range a {
		if d := a[i] - b[i]; d > tol || d < -tol {
			return false
		}
	}
	return true
}

func TestLastFrameIsTheFinalDecodedFrame(t *testing.T) {
	f := locate(t)
	dir := t.TempDir()
	cases := []struct {
		name   string
		frames int
		audio  float64
	}{
		{"b-frames, tail seek", 72, 3},
		{"audio outlasts the picture (whole-file fallback)", 24, 5},
		{"shorter than the tail window", 12, 0},
	}
	for i, tc := range cases {
		clip := filepath.Join(dir, fmt.Sprintf("clip %d.mp4", i))
		numberedClip(t, f, clip, tc.frames, tc.audio)
		frames := decodedFrames(t, f, clip)
		if len(frames) != tc.frames {
			t.Fatalf("%s: decoded %d frames, want %d", tc.name, len(frames), tc.frames)
		}
		out := filepath.Join(dir, "out", fmt.Sprintf("last-%d.jpg", i))
		if err := f.LastFrame(context.Background(), clip, out); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := jpegColor(t, out)
		last, before := frames[len(frames)-1], frames[len(frames)-2]
		if !closeRGB(got, last, 4) || closeRGB(got, before, 4) {
			t.Fatalf("%s: got %v, last frame %v, the one before %v", tc.name, got, last, before)
		}
		r := (tc.frames - 1) * 10 % 250
		if want := [3]int{r, 255 - r, 128}; !closeRGB(got, want, 6) {
			t.Fatalf("%s: got %v, want frame %d ≈ %v", tc.name, got, tc.frames-1, want)
		}
		if _, err := os.Stat(out + ".part"); !os.IsNotExist(err) {
			t.Fatalf("%s: temp file left behind", tc.name)
		}
	}

	// An existing file is replaced.
	out := filepath.Join(dir, "out", "last-0.jpg")
	short := filepath.Join(dir, "clip 2.mp4")
	if err := f.LastFrame(context.Background(), short, out); err != nil {
		t.Fatal(err)
	}
	if got := jpegColor(t, out); !closeRGB(got, [3]int{110, 145, 128}, 6) {
		t.Fatalf("overwrite: %v", got)
	}
}

func TestLastFrameErrors(t *testing.T) {
	f := locate(t)
	dir := t.TempDir()
	if err := (FF{}).LastFrame(context.Background(), "x.mp4", filepath.Join(dir, "a.jpg")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no ffmpeg: %v", err)
	}
	if err := f.LastFrame(context.Background(), filepath.Join(dir, "missing.mp4"), filepath.Join(dir, "a.jpg")); err == nil {
		t.Fatal("missing clip must fail")
	}
	audioOnly := filepath.Join(dir, "voice.mp3")
	if err := f.Run(context.Background(), "", "-f", "lavfi", "-i", "sine=d=1", "-c:a", "libmp3lame", audioOnly); err != nil {
		t.Fatal(err)
	}
	if err := f.LastFrame(context.Background(), audioOnly, filepath.Join(dir, "b.jpg")); err == nil {
		t.Fatal("a file without video must fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "b.jpg")); !os.IsNotExist(err) {
		t.Fatal("nothing may be written on failure")
	}
	clip := filepath.Join(dir, "c.mp4")
	numberedClip(t, f, clip, 24, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.LastFrame(ctx, clip, filepath.Join(dir, "c.jpg")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}
