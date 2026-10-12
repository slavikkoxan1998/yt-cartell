package media

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestVisualSheets renders contact sheets for looking at the montage by eye
// (colour looks, camera moves, transitions, subtitle presets) through the real
// pipeline. Skipped unless MEDIA_VISUAL_SRC (a folder with at least two
// 16:9 pictures: .jpg/.png) and MEDIA_VISUAL_OUT (where the PNGs go) are set:
//
//	MEDIA_VISUAL_SRC=… MEDIA_VISUAL_OUT=… go test ./internal/media -run TestVisualSheets -v
func TestVisualSheets(t *testing.T) {
	src, dst := os.Getenv("MEDIA_VISUAL_SRC"), os.Getenv("MEDIA_VISUAL_OUT")
	if src == "" || dst == "" {
		t.Skip("set MEDIA_VISUAL_SRC and MEDIA_VISUAL_OUT")
	}
	f := locate(t)
	var pics []string
	for _, pat := range []string{"*.jpg", "*.png"} {
		m, _ := filepath.Glob(filepath.Join(src, pat))
		pics = append(pics, m...)
	}
	if len(pics) < 2 {
		t.Fatalf("need two pictures in %s", src)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(f.FFmpeg, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	// tile stacks the given frames (files) cols per row into one PNG.
	tile := func(name string, cols int, frames []string) {
		t.Helper()
		var args []string
		var in strings.Builder
		for i, fr := range frames {
			args = append(args, "-i", fr)
			fmt.Fprintf(&in, "[%d:v]scale=480:270:force_original_aspect_ratio=decrease,pad=480:270:(ow-iw)/2:(oh-ih)/2[t%d];", i, i)
		}
		layout := make([]string, len(frames))
		for i := range frames {
			layout[i] = fmt.Sprintf("%d_%d", (i%cols)*484, (i/cols)*274)
			in.WriteString(fmt.Sprintf("[t%d]", i))
		}
		fmt.Fprintf(&in, "xstack=inputs=%d:layout=%s:fill=white", len(frames), strings.Join(layout, "|"))
		run(append(args, "-filter_complex", in.String(), "-frames:v", "1", filepath.Join(dst, name))...)
	}
	grab := func(video string, at float64, name string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), name)
		run("-ss", f3(at), "-i", video, "-frames:v", "1", p)
		return p
	}

	// 1. Colour looks through the real scene renderer (still, no motion).
	for pi, pic := range pics[:2] {
		var frames []string
		for _, flt := range Filters {
			res, err := f.Montage(ctx, MontageOptions{Segments: []Segment{{Frame: pic, Effect: "none", TrimOut: 7}}, Aspect: "16:9", Transition: "none", Filter: flt.ID,
				Subtitles: "none", OutDir: filepath.Join(t.TempDir(), flt.ID)})
			if err != nil {
				t.Fatalf("%s: %v", flt.ID, err)
			}
			frames = append(frames, grab(res.Output, 0.5, flt.ID+".png"))
		}
		tile(fmt.Sprintf("looks_%d.png", pi+1), 4, frames)
	}

	// 2. Camera moves: first / middle / last frame of every effect.
	var segs []Segment
	for _, e := range Effects {
		segs = append(segs, Segment{Frame: pics[0], Effect: e.ID, TrimOut: 4})
	}
	res, err := f.Montage(ctx, MontageOptions{Segments: segs, Aspect: "16:9", Transition: "none", Subtitles: "none", OutDir: filepath.Join(t.TempDir(), "fx")})
	if err != nil {
		t.Fatal(err)
	}
	var frames []string
	for i, s := range res.Timeline {
		for j, at := range []float64{s.Start + 0.02, s.Start + s.Duration/2, s.Start + s.Duration - 0.06} {
			frames = append(frames, grab(res.Output, at, fmt.Sprintf("fx%02d_%d.png", i, j)))
		}
	}
	tile("effects_1.png", 3, frames[:18])
	tile("effects_2.png", 3, frames[18:])

	// 3. Transitions: the middle of every overlapping transition.
	segs = nil
	for i, tr := range Transitions {
		segs = append(segs, Segment{Frame: pics[i%2], Effect: "none", TrimOut: 6.5, Transition: tr.ID})
	}
	segs = append(segs, Segment{Frame: pics[len(Transitions)%2], Effect: "none", TrimOut: 6.5})
	res, err = f.Montage(ctx, MontageOptions{Segments: segs, Aspect: "16:9", Transition: "none", TransitionDuration: 1, Subtitles: "none", OutDir: filepath.Join(t.TempDir(), "tr")})
	if err != nil {
		t.Fatal(err)
	}
	frames = nil
	for i, s := range res.Timeline {
		if s.Overlap > 0 {
			frames = append(frames, grab(res.Output, s.Start+s.Duration-s.Overlap*0.45, fmt.Sprintf("tr%02d.png", i)))
		}
	}
	tile("transitions.png", 5, frames)

	// 4. Subtitle presets burnt in (16:9) and two on vertical video.
	frames = nil
	for _, p := range SubtitlePresets {
		res, err := f.Montage(ctx, MontageOptions{
			Segments: []Segment{{Frame: pics[1], Effect: "none", TrimOut: 6, Narration: "Лисиця тихо вийшла з туману і подивилася на хлопця."}},
			Aspect:   "16:9", Transition: "none", Subtitles: "burn", SubtitleStyle: SubtitleStyle{Preset: p.ID},
			OutDir: filepath.Join(t.TempDir(), "sub-"+p.ID),
		})
		if err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
		frames = append(frames, grab(res.Output, 1.0, "sub-"+p.ID+".png"))
	}
	tile("subtitles.png", 4, frames)
	frames = nil
	for _, p := range []string{"classic", "shorts", "youtube"} {
		res, err := f.Montage(ctx, MontageOptions{
			Segments: []Segment{{Frame: pics[0], Effect: "none", TrimOut: 6, Narration: "Лисиця тихо вийшла з туману і подивилася на хлопця."}},
			Aspect:   "9:16", Transition: "none", Subtitles: "burn", SubtitleStyle: SubtitleStyle{Preset: p},
			OutDir: filepath.Join(t.TempDir(), "vsub-"+p),
		})
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		frames = append(frames, grab(res.Output, 1.0, "vsub-"+p+".png"))
	}
	tile("subtitles_vertical.png", 3, frames)
	t.Logf("sheets written to %s", dst)
}
